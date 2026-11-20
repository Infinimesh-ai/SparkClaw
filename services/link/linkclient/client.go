// Package linkclient is the device side of the link layer (design 3.6):
// one interface — connect, request, subscribe, fetch large objects — with
// path selection inside. LAN and ISCP are probed together, LAN is
// preferred, ISCP is the fallback, and after a switch callers resume from
// their cursor. Nobody chooses a path: when the device's network changes
// the client checks the LAN first and uses the ISCP Relay otherwise
// (NetworkChanged). Both paths carry the same operations with the same
// rights. It depends only on the ISCP packages and the link contract.
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
	"github.com/Chiiz0/SparkClaw/services/link/iscplink"
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
	// OnHostLAN receives the backend's LAN connector when a hello reply
	// carries one that differs from the known one, so the caller can keep
	// it for the next start.
	OnHostLAN func(consoleapi.LAN)
	// DisableNetworkWatch turns off the built-in watch of the device's
	// addresses and of suspend/resume; the caller then reports every
	// change through NetworkChanged.
	DisableNetworkWatch bool
}

// Timing of path selection after a network change.
const (
	// settleWindow is how long requests wait for a verified path after
	// the network changed instead of failing at once.
	settleWindow = 8 * time.Second
	// The LAN answers within milliseconds when it is there, so it is
	// checked first and briefly; the Relay gets longer.
	lanVerifyTimeout  = 2 * time.Second
	iscpVerifyTimeout = 6 * time.Second
	networkDebounce   = 250 * time.Millisecond
)

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

	// lan holds the LAN candidates: the membership's at first, then
	// whatever the backend's hello last named.
	lan consoleapi.LAN
	// After a network change every path is suspect until a ping proves
	// it; netGen tells verifications of an older change apart.
	settleUntil time.Time
	netGen      uint64
	netSignal   chan struct{}
	lanKick     chan struct{}
	lanAttempt  context.CancelFunc
	reconnect   func() // drops and redials the Relay connection
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
	// suspect (under Client.mu): the network changed since this path last
	// answered; it carries no request until a ping proves it.
	suspect bool
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
	return &Client{opts: opts, paths: map[string]*pathConn{}, changed: make(chan struct{}), lan: opts.Host.LAN,
		netSignal: make(chan struct{}, 1), lanKick: make(chan struct{}, 1)}, nil
}

// Start brings the paths up in the background until Close.
func (c *Client) Start(parent context.Context) {
	c.mu.Lock()
	c.ctx, c.cancel = context.WithCancel(parent)
	c.mu.Unlock()
	// The LAN loop runs even without a known endpoint: the backend's hello
	// over the Relay can name one later.
	if !c.opts.DisableLAN {
		c.wg.Add(1)
		go func() { defer c.wg.Done(); c.runLAN(c.ctx) }()
	}
	if c.opts.Relay != nil && c.opts.Grant != nil {
		c.wg.Add(1)
		go func() { defer c.wg.Done(); c.runISCP(c.ctx) }()
	}
	c.wg.Add(1)
	go func() { defer c.wg.Done(); c.runReselect(c.ctx) }()
	if !c.opts.DisableNetworkWatch {
		c.wg.Add(1)
		go func() { defer c.wg.Done(); c.watchNetwork(c.ctx) }()
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

// usableLocked is the path requests go on: the LAN when it is up and
// proven, the Relay otherwise.
func (c *Client) usableLocked() *pathConn {
	for _, name := range [...]string{PathLAN, PathISCP} {
		if p := c.paths[name]; p != nil && !p.suspect {
			return p
		}
	}
	return nil
}

// refreshLocked recomputes the reported path and wakes waiters. While
// only suspect paths exist the reported path stays as it was: the outcome
// of their verification decides, and callers see one change at most.
func (c *Client) refreshLocked() (active string, hello *contract.Hello, changed bool) {
	prev := c.active
	if p := c.usableLocked(); p != nil {
		c.active, c.hello = p.name, p.hello
	} else if len(c.paths) == 0 {
		c.active, c.hello = PathNone, nil
	}
	close(c.changed)
	c.changed = make(chan struct{})
	return c.active, c.hello, prev != c.active
}

func (c *Client) setPath(name string, p *pathConn) {
	c.mu.Lock()
	if p == nil {
		delete(c.paths, name)
	} else {
		c.paths[name] = p
	}
	active, hello, changed := c.refreshLocked()
	c.mu.Unlock()
	if changed && c.opts.OnState != nil {
		c.opts.OnState(active, hello)
	}
}

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// NetworkChanged tells the client that the device's network changed: it
// joined or left a Wi-Fi, moved to cellular, or woke from sleep. Every
// path is checked again, the LAN first; requests made meanwhile wait for
// the outcome for a short time instead of going to a connection that may
// be dead. The LAN is dialed again at once and the Relay connection is
// renewed when it no longer answers. Safe to call often and from any
// thread.
func (c *Client) NetworkChanged() {
	c.mu.Lock()
	if c.ctx == nil || c.ctx.Err() != nil {
		c.mu.Unlock()
		return
	}
	c.netGen++
	c.settleUntil = time.Now().Add(settleWindow)
	for _, p := range c.paths {
		p.suspect = true
	}
	c.refreshLocked()
	c.mu.Unlock()
	signal(c.netSignal)
}

// runReselect serves NetworkChanged; a burst of signals is one check.
func (c *Client) runReselect(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.netSignal:
		}
		quiet := time.NewTimer(networkDebounce)
		for settled := false; !settled; {
			select {
			case <-ctx.Done():
				quiet.Stop()
				return
			case <-c.netSignal:
			case <-quiet.C:
				settled = true
			}
		}
		c.reselect(ctx)
	}
}

func (c *Client) reselect(ctx context.Context) {
	c.mu.Lock()
	gen := c.netGen
	lan, iscp := c.paths[PathLAN], c.paths[PathISCP]
	lanAttempt, reconnect := c.lanAttempt, c.reconnect
	c.mu.Unlock()
	// The LAN is looked at first. A Relay path that answers sooner is
	// only taken once the LAN check is over, so a device that stays on
	// the LAN never reports a switch.
	var lanChecked chan struct{}
	if lan != nil {
		lanChecked = make(chan struct{})
		go func() {
			defer close(lanChecked)
			c.settle(lan, gen, c.probe(ctx, lan, lanVerifyTimeout))
		}()
	} else {
		// A dial started on the previous network is abandoned.
		if lanAttempt != nil {
			lanAttempt()
		}
		signal(c.lanKick)
	}
	if iscp != nil {
		go func() {
			alive := c.probe(ctx, iscp, iscpVerifyTimeout)
			if lanChecked != nil {
				<-lanChecked
			}
			c.settle(iscp, gen, alive)
		}()
	} else if reconnect != nil {
		reconnect()
	}
}

func (c *Client) probe(ctx context.Context, p *pathConn, timeout time.Duration) bool {
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return c.requestOn(pctx, p, contract.OpPing) == nil
}

// settle applies a verification: a path that answered carries requests
// again; one that did not is closed and dialed anew.
func (c *Client) settle(p *pathConn, gen uint64, alive bool) {
	if !alive {
		c.drop(p)
		return
	}
	c.mu.Lock()
	if c.paths[p.name] != p || c.netGen != gen {
		// Gone, or the network changed again: the newer check decides.
		c.mu.Unlock()
		return
	}
	p.suspect = false
	active, hello, changed := c.refreshLocked()
	c.mu.Unlock()
	if changed && c.opts.OnState != nil {
		c.opts.OnState(active, hello)
	}
}

// drop ends a path that stopped answering; its loop dials again at once.
func (c *Client) drop(p *pathConn) {
	if p.name == PathLAN {
		p.finish()
		signal(c.lanKick)
		return
	}
	// The Relay connection goes first, so the session that the path loop
	// waits for next is a new one.
	c.mu.Lock()
	reconnect := c.reconnect
	c.mu.Unlock()
	if reconnect != nil {
		reconnect()
	}
	p.finish()
}

// learnLAN keeps the LAN connector the backend named in its hello.
func (c *Client) learnLAN(l *contract.LAN) {
	if l == nil || c.opts.DisableLAN || l.TLSPin == "" || len(l.Endpoints) == 0 {
		return
	}
	next := consoleapi.LAN{Endpoints: append([]string(nil), l.Endpoints...), TLSPin: l.TLSPin}
	c.mu.Lock()
	same := c.lan.TLSPin == next.TLSPin && len(c.lan.Endpoints) == len(next.Endpoints)
	for i := 0; same && i < len(next.Endpoints); i++ {
		same = c.lan.Endpoints[i] == next.Endpoints[i]
	}
	if !same {
		c.lan = next
	}
	onLAN := c.paths[PathLAN] != nil
	c.mu.Unlock()
	if same {
		return
	}
	if c.opts.OnHostLAN != nil {
		c.opts.OnHostLAN(next)
	}
	if !onLAN {
		signal(c.lanKick)
	}
}

// WaitReady blocks until some path is up.
func (c *Client) WaitReady(ctx context.Context) error {
	for {
		c.mu.Lock()
		ok := c.usableLocked() != nil
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
var ErrBackendUnreachable = &contract.Error{Code: contract.CodeBackendUnavailable, Message: "the backend is unreachable", Retryable: true, NotSent: true}

// errLostReply is returned when a sent request's path ended before its
// reply; the outcome is unknown.
func errLostReply() error {
	return &contract.Error{Code: contract.CodeBackendUnavailable, Message: "connection lost before the reply", Retryable: true}
}

// pick returns the path a request goes on. With no path it fails at once
// (design 3.12), except right after a network change, when it waits for
// the paths being checked or dialed.
func (c *Client) pick(ctx context.Context) (*pathConn, error) {
	for {
		c.mu.Lock()
		p := c.usableLocked()
		wait := time.Until(c.settleUntil)
		ch := c.changed
		c.mu.Unlock()
		if p != nil {
			return p, nil
		}
		if wait <= 0 {
			return nil, ErrBackendUnreachable
		}
		t := time.NewTimer(wait)
		select {
		case <-ch:
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return nil, ErrBackendUnreachable
		}
		t.Stop()
	}
}

// Request sends one request on the active path and waits for its response.
// out may be nil. A request that could not be written to one path is sent
// on the other when that one is up. A request that was sent but got no
// reply returns an error with Retryable set; the caller retries with the
// same idempotency key to learn its outcome.
func (c *Client) Request(ctx context.Context, op string, params, out any) error {
	id := "c" + strconv.FormatUint(c.nextID.Add(1), 10)
	f, err := contract.Request(id, op, params)
	if err != nil {
		return err
	}
	raw, err := contract.Encode(f)
	if err != nil {
		return err
	}
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		p, err := c.pick(ctx)
		if err != nil {
			if last != nil {
				return last
			}
			return err
		}
		sent, err := c.roundTrip(ctx, p, id, raw, out)
		if sent || ctx.Err() != nil {
			return err
		}
		// Nothing left this device: the path is doubted until a ping
		// proves it, and the other path, if any, takes the request.
		last = err
		c.doubt(p)
	}
	return last
}

// doubt takes a path that failed a write out of use until it answers a
// ping, and closes it when it does not.
func (c *Client) doubt(p *pathConn) {
	c.mu.Lock()
	if c.paths[p.name] != p || p.suspect {
		c.mu.Unlock()
		return
	}
	p.suspect = true
	gen := c.netGen
	ctx := c.ctx
	c.refreshLocked()
	c.mu.Unlock()
	timeout := iscpVerifyTimeout
	if p.name == PathLAN {
		timeout = lanVerifyTimeout
	}
	go func() { c.settle(p, gen, c.probe(ctx, p, timeout)) }()
}

// roundTrip sends one encoded request on p. sent is false when the request
// certainly never left this device.
func (c *Client) roundTrip(ctx context.Context, p *pathConn, id string, raw []byte, out any) (sent bool, err error) {
	ch := make(chan contract.Frame, 1)
	p.mu.Lock()
	p.pending[id] = ch
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
	}()
	select {
	case <-p.done:
		return false, &contract.Error{Code: contract.CodeBackendUnavailable, Message: "request not sent: the path closed", Retryable: true, NotSent: true}
	default:
	}
	if err := p.send(ctx, raw); err != nil {
		// A failed LAN write leaves an incomplete frame, and a session
		// that is not ready submits nothing. A Relay submission that ran
		// out of time may still have been accepted, so it is not sent
		// again on the other path.
		certain := p.name == PathLAN || errors.Is(err, iscplink.ErrNotReady)
		return !certain, &contract.Error{Code: contract.CodeBackendUnavailable, Message: "request not sent: " + err.Error(), Retryable: true, NotSent: true}
	}
	select {
	case resp, ok := <-ch:
		if !ok {
			return true, errLostReply()
		}
		if resp.OK == nil || !*resp.OK {
			if resp.Error != nil {
				return true, resp.Error
			}
			return true, errors.New("request failed")
		}
		if out != nil && len(resp.Result) > 0 {
			return true, json.Unmarshal(resp.Result, out)
		}
		return true, nil
	case <-ctx.Done():
		cancel, _ := contract.Encode(contract.Frame{Type: contract.FrameCancel, ID: id})
		_ = p.send(context.Background(), cancel)
		return true, &contract.Error{Code: contract.CodeTimeout, Message: "no reply in time", Retryable: true}
	case <-p.done:
		return true, errLostReply()
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

// lanBackoff spaces LAN dials. A dial on the local network is cheap and
// fails fast, so the cap is low: a device that comes home is back on the
// LAN within seconds even when no network change was reported.
func lanBackoff(attempt int) time.Duration {
	d := time.Second << min(attempt, 4)
	if d > 15*time.Second {
		d = 15 * time.Second
	}
	return d
}
