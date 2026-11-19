// Package linkclient is the device side of the link layer (design 3.6):
// one interface — connect, request, subscribe, fetch large objects — with
// path selection inside. LAN and ISCP are probed together, LAN is
// preferred, ISCP is the fallback, and after a switch callers resume from
// their cursor. It depends only on the ISCP packages and the link contract.
package linkclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/trust"

	"github.com/Chiiz0/SparkClaw/services/link/consoleapi"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
	"github.com/Chiiz0/SparkClaw/services/link/relay"
)

// Path names.
const (
	PathNone = ""
	PathLAN  = "lan"
	PathISCP = "iscp"
)

// ReverseHandler serves a reverse operation the device declared.
type ReverseHandler func(ctx context.Context, op string, params json.RawMessage) (any, error)

// Options configures a client.
type Options struct {
	Device identity.Device
	Host   consoleapi.Host
	// Relay is the ISCP fallback; nil disables it.
	Relay       *relay.Endpoint
	RelayStore  relay.CredentialStore
	RelayCreds  relay.Credentials
	RelayRebind relay.Rebinder
	// Grant returns the current Grant (renewing through the console
	// adapter when needed). Required for the ISCP path.
	Grant func(ctx context.Context) (trust.Grant, error)
	// DisableLAN skips the LAN path (for example when the host publishes
	// no LAN endpoint the device can reach).
	DisableLAN bool

	Describe  contract.Device // conditions declared at hello
	Profile   string          // contract.ProfileDuplex or contract.ProfilePull
	Reverse   []string
	OnReverse ReverseHandler
	// OnEvent receives pushed events (run output, sync, approvals).
	OnEvent func(contract.Frame)
	// OnState reports path changes ("", "lan", "iscp") and the hello reply.
	OnState func(path string, hello *contract.Hello)
}

// Client is a LinkClient.
type Client struct {
	opts Options

	mu      sync.Mutex
	paths   map[string]*pathConn
	active  string
	hello   *contract.Hello
	changed chan struct{}
	nextID  atomic.Uint64
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// pathConn is one established path with a completed hello.
type pathConn struct {
	name    string
	send    func(ctx context.Context, frame []byte) error
	close   func()
	hello   *contract.Hello
	mu      sync.Mutex
	pending map[string]chan contract.Frame
	done    chan struct{}
	once    sync.Once
}

func (p *pathConn) finish() {
	p.once.Do(func() {
		close(p.done)
		p.mu.Lock()
		for id, ch := range p.pending {
			close(ch)
			delete(p.pending, id)
		}
		p.mu.Unlock()
		if p.close != nil {
			p.close()
		}
	})
}

// New builds a client; call Start.
func New(opts Options) (*Client, error) {
	if opts.Host.DeviceID == "" {
		return nil, errors.New("linkclient: the backend host is unknown (join a domain first)")
	}
	if opts.Describe.Class == "" {
		return nil, errors.New("linkclient: device class is required")
	}
	if opts.Profile == "" {
		opts.Profile = contract.ProfileDuplex
	}
	return &Client{opts: opts, paths: map[string]*pathConn{}, changed: make(chan struct{})}, nil
}

// Start brings the paths up in the background until Close.
func (c *Client) Start(parent context.Context) {
	c.ctx, c.cancel = context.WithCancel(parent)
	if !c.opts.DisableLAN && len(c.opts.Host.LAN.Endpoints) > 0 && c.opts.Host.LAN.TLSPin != "" {
		c.wg.Add(1)
		go func() { defer c.wg.Done(); c.runLAN(c.ctx) }()
	}
	if c.opts.Relay != nil && c.opts.Grant != nil {
		c.wg.Add(1)
		go func() { defer c.wg.Done(); c.runISCP(c.ctx) }()
	}
}

// Close stops every path.
func (c *Client) Close() {
	if c.cancel != nil {
		c.cancel()
	}
	c.wg.Wait()
}

// Path returns the active path.
func (c *Client) Path() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active
}

// Hello returns the backend's hello reply on the active path.
func (c *Client) Hello() *contract.Hello {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hello
}

// Available reports whether the backend offers op to this device.
func (c *Client) Available(op string) bool {
	h := c.Hello()
	if h == nil {
		return false
	}
	for _, f := range h.Features {
		if f == op {
			return true
		}
	}
	return false
}

func (c *Client) setPath(name string, p *pathConn) {
	c.mu.Lock()
	if p == nil {
		delete(c.paths, name)
	} else {
		c.paths[name] = p
	}
	prev := c.active
	switch {
	case c.paths[PathLAN] != nil:
		c.active = PathLAN
	case c.paths[PathISCP] != nil:
		c.active = PathISCP
	default:
		c.active = PathNone
	}
	var hello *contract.Hello
	if ap := c.paths[c.active]; ap != nil {
		hello = ap.hello
	}
	c.hello = hello
	active := c.active
	if prev != active {
		close(c.changed)
		c.changed = make(chan struct{})
	}
	c.mu.Unlock()
	if prev != active && c.opts.OnState != nil {
		c.opts.OnState(active, hello)
	}
}

// WaitReady blocks until some path is up.
func (c *Client) WaitReady(ctx context.Context) error {
	for {
		c.mu.Lock()
		ok := c.active != PathNone
		ch := c.changed
		c.mu.Unlock()
		if ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return ErrBackendUnreachable
		case <-ch:
		}
	}
}

// ErrBackendUnreachable is returned at once when no path is up (design
// 3.12: nothing is queued; the caller shows the backend as offline).
var ErrBackendUnreachable = &contract.Error{Code: contract.CodeBackendUnavailable, Message: "the backend is unreachable", Retryable: true}

// Request sends one request on the active path and waits for its response.
// out may be nil. A request that was sent but got no reply returns an
// error with Retryable set; the caller retries with the same idempotency
// key to learn its outcome.
func (c *Client) Request(ctx context.Context, op string, params, out any) error {
	c.mu.Lock()
	p := c.paths[c.active]
	c.mu.Unlock()
	if p == nil {
		return ErrBackendUnreachable
	}
	id := "c" + strconv.FormatUint(c.nextID.Add(1), 10)
	f, err := contract.Request(id, op, params)
	if err != nil {
		return err
	}
	raw, err := contract.Encode(f)
	if err != nil {
		return err
	}
	ch := make(chan contract.Frame, 1)
	p.mu.Lock()
	p.pending[id] = ch
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
	}()
	if err := p.send(ctx, raw); err != nil {
		return &contract.Error{Code: contract.CodeBackendUnavailable, Message: "request not sent: " + err.Error(), Retryable: true}
	}
	select {
	case resp, ok := <-ch:
		if !ok {
			return &contract.Error{Code: contract.CodeBackendUnavailable, Message: "connection lost before the reply", Retryable: true}
		}
		if resp.OK == nil || !*resp.OK {
			if resp.Error != nil {
				return resp.Error
			}
			return errors.New("request failed")
		}
		if out != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, out)
		}
		return nil
	case <-ctx.Done():
		cancel, _ := contract.Encode(contract.Frame{Type: contract.FrameCancel, ID: id})
		_ = p.send(context.Background(), cancel)
		return &contract.Error{Code: contract.CodeTimeout, Message: "no reply in time", Retryable: true}
	case <-p.done:
		return &contract.Error{Code: contract.CodeBackendUnavailable, Message: "connection lost before the reply", Retryable: true}
	}
}

// receive handles one inbound frame on path p.
func (c *Client) receive(p *pathConn, raw []byte) {
	f, err := contract.Decode(raw)
	if err != nil {
		return
	}
	switch f.Type {
	case contract.FrameResponse:
		p.mu.Lock()
		ch := p.pending[f.ID]
		p.mu.Unlock()
		if ch != nil {
			select {
			case ch <- f:
			default:
			}
		}
	case contract.FrameEvent:
		if c.opts.OnEvent != nil {
			c.opts.OnEvent(f)
		}
	case contract.FrameRequest:
		go c.serveReverse(p, f)
	}
}

func (c *Client) serveReverse(p *pathConn, f contract.Frame) {
	var resp contract.Frame
	if c.opts.OnReverse == nil {
		resp = contract.Failure(f.ID, contract.Errorf(contract.CodeUnsupportedCapability, "device serves no reverse operations"))
	} else {
		ctx, cancel := context.WithTimeout(c.ctx, 2*time.Minute)
		result, err := c.opts.OnReverse(ctx, f.Op, f.Params)
		cancel()
		if err != nil {
			resp = contract.Failure(f.ID, contract.AsError(err))
		} else if r, e := contract.Success(f.ID, result); e == nil {
			resp = r
		} else {
			resp = contract.Failure(f.ID, contract.AsError(e))
		}
	}
	if raw, err := contract.Encode(resp); err == nil {
		_ = p.send(c.ctx, raw)
	}
}

func (c *Client) helloFrame() ([]byte, error) {
	dev := c.opts.Describe
	f := contract.Frame{Type: contract.FrameHello, ID: "hello", Hello: &contract.Hello{Versions: []string{contract.Version}, Profile: c.opts.Profile, Device: &dev, Reverse: c.opts.Reverse}}
	return contract.Encode(f)
}

// handshake sends hello on a fresh path and waits for the reply, which
// arrives through receiveHello.
func awaitHello(ctx context.Context, ch <-chan contract.Frame) (*contract.Hello, error) {
	select {
	case f := <-ch:
		if f.Type == contract.FrameResponse && f.Error != nil {
			return nil, f.Error
		}
		if f.Type != contract.FrameHello || f.Hello == nil || f.Hello.Version != contract.Version {
			return nil, fmt.Errorf("unexpected hello reply")
		}
		return f.Hello, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Ping checks the active path end to end.
func (c *Client) Ping(ctx context.Context) error {
	return c.Request(ctx, contract.OpPing, nil, nil)
}

func backoff(attempt int) time.Duration {
	d := time.Second << min(attempt, 5)
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}
