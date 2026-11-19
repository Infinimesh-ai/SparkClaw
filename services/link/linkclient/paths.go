package linkclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/gorilla/websocket"

	"github.com/Chiiz0/SparkClaw/services/link/contract"
	"github.com/Chiiz0/SparkClaw/services/link/iscplink"
	"github.com/Chiiz0/SparkClaw/services/link/relay"
)

// --- LAN ---

func (c *Client) runLAN(ctx context.Context) {
	attempt := 0
	for ctx.Err() == nil {
		started := time.Now()
		err := c.lanOnce(ctx)
		c.setPath(PathLAN, nil)
		if ctx.Err() != nil {
			return
		}
		_ = err
		if time.Since(started) > time.Minute {
			attempt = 0
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff(attempt)):
		}
		attempt++
	}
}

func (c *Client) lanOnce(ctx context.Context) error {
	var lastErr error
	for _, endpoint := range c.opts.Host.LAN.Endpoints {
		err := c.lanEndpoint(ctx, endpoint)
		if err == nil || ctx.Err() != nil {
			return err
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("no LAN endpoint")
	}
	return lastErr
}

func (c *Client) lanEndpoint(ctx context.Context, endpoint string) error {
	d := websocket.Dialer{HandshakeTimeout: 5 * time.Second, Subprotocols: []string{contract.LANProtocol}, TLSClientConfig: relay.PinnedSHA256Config(c.opts.Host.LAN.TLSPin)}
	dctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	ws, _, err := d.DialContext(dctx, endpoint, http.Header{})
	cancel()
	if err != nil {
		return err
	}
	defer ws.Close()
	ws.SetReadLimit(contract.MaxFrameBytes + 4096)
	_ = ws.SetReadDeadline(time.Now().Add(15 * time.Second))
	var challenge contract.LANControl
	if err := ws.ReadJSON(&challenge); err != nil {
		return err
	}
	if challenge.Type != "challenge" || challenge.Host != c.opts.Host.DeviceID || challenge.Nonce == "" {
		return errors.New("LAN peer is not the bound backend")
	}
	proof, err := c.opts.Device.CreateProof(crypto.NewProvider(), c.opts.Host.DeviceID, contract.LANChallengePrefix+challenge.Nonce, crypto.Base64URL(crypto.RandomBytes(16)), time.Now().UTC())
	if err != nil {
		return err
	}
	rawProof, _ := json.Marshal(proof)
	if err := ws.WriteJSON(contract.LANControl{Type: "auth", DeviceID: c.opts.Device.Identity.DeviceID, Proof: rawProof}); err != nil {
		return err
	}
	var reply contract.LANControl
	if err := ws.ReadJSON(&reply); err != nil {
		return err
	}
	if reply.Type != "authenticated" {
		return &contract.Error{Code: reply.Code, Message: reply.Message}
	}
	var wmu sync.Mutex
	write := func(wctx context.Context, b []byte) error {
		wmu.Lock()
		defer wmu.Unlock()
		dl := time.Now().Add(20 * time.Second)
		if d, ok := wctx.Deadline(); ok && d.Before(dl) {
			dl = d
		}
		_ = ws.SetWriteDeadline(dl)
		return ws.WriteMessage(websocket.TextMessage, b)
	}
	hello, err := c.helloFrame()
	if err != nil {
		return err
	}
	if err := write(ctx, hello); err != nil {
		return err
	}
	_, raw, err := ws.ReadMessage()
	if err != nil {
		return err
	}
	hf, err := contract.Decode(raw)
	if err != nil {
		return err
	}
	ch := make(chan contract.Frame, 1)
	ch <- hf
	h, err := awaitHello(ctx, ch)
	if err != nil {
		return err
	}
	life, stop := context.WithCancel(ctx)
	defer stop()
	go func() { <-life.Done(); _ = ws.Close() }()
	p := &pathConn{name: PathLAN, send: write, close: func() { _ = ws.Close() }, hello: h, pending: map[string]chan contract.Frame{}, done: make(chan struct{})}
	defer p.finish()
	ws.SetPingHandler(func(data string) error {
		_ = ws.SetReadDeadline(time.Now().Add(90 * time.Second))
		wmu.Lock()
		defer wmu.Unlock()
		return ws.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(5*time.Second))
	})
	_ = ws.SetReadDeadline(time.Now().Add(90 * time.Second))
	c.setPath(PathLAN, p)
	for {
		kind, b, err := ws.ReadMessage()
		if err != nil {
			return err
		}
		_ = ws.SetReadDeadline(time.Now().Add(90 * time.Second))
		if kind != websocket.TextMessage {
			continue
		}
		c.receive(p, b)
	}
}

// --- ISCP ---

func (c *Client) runISCP(ctx context.Context) {
	rc, err := relay.NewClient(*c.opts.Relay, c.opts.Device, c.opts.RelayStore, c.opts.RelayCreds, c.opts.RelayRebind)
	if err != nil {
		return
	}
	var cur struct {
		sync.Mutex
		p     *pathConn
		hello chan contract.Frame
	}
	readyCh := make(chan string, 4)
	ini, err := iscplink.NewInitiator(iscplink.InitiatorConfig{
		Relay:  rc,
		Device: c.opts.Device,
		Host:   c.opts.Host.Identity,
		Grant:  c.opts.Grant,
		OnFrame: func(b []byte) {
			cur.Lock()
			p, hch := cur.p, cur.hello
			cur.Unlock()
			if p != nil {
				c.receive(p, b)
				return
			}
			if hch != nil {
				if f, err := contract.Decode(b); err == nil {
					select {
					case hch <- f:
					default:
					}
				}
			}
		},
		OnState: func(s string) {
			select {
			case readyCh <- s:
			default:
			}
		},
	})
	if err != nil {
		return
	}
	go func() { _ = ini.Run(ctx) }()
	for ctx.Err() == nil {
		wctx, cancel := context.WithTimeout(ctx, time.Minute)
		err := ini.WaitReady(wctx)
		cancel()
		if err != nil {
			continue
		}
		hch := make(chan contract.Frame, 1)
		cur.Lock()
		cur.hello = hch
		cur.Unlock()
		hello, err := c.helloFrame()
		if err != nil {
			return
		}
		sctx, scancel := context.WithTimeout(ctx, 20*time.Second)
		err = ini.Send(sctx, hello)
		var reply *contract.Hello
		if err == nil {
			reply, err = awaitHello(sctx, hch)
		}
		scancel()
		if err != nil {
			ini.Reset()
			continue
		}
		send := func(sctx context.Context, b []byte) error { return ini.Send(sctx, b) }
		p := &pathConn{name: PathISCP, send: send, hello: reply, pending: map[string]chan contract.Frame{}, done: make(chan struct{})}
		cur.Lock()
		cur.p, cur.hello = p, nil
		cur.Unlock()
		c.setPath(PathISCP, p)
		c.watchISCP(ctx, ini, p, readyCh)
		cur.Lock()
		cur.p = nil
		cur.Unlock()
		p.finish()
		c.setPath(PathISCP, nil)
	}
}

// watchISCP returns when the ISCP session ends or stops answering.
func (c *Client) watchISCP(ctx context.Context, ini *iscplink.Initiator, p *pathConn, states <-chan string) {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return
		case s := <-states:
			if s != iscplink.StateReady {
				return
			}
		case <-tick.C:
			if !ini.Ready() {
				return
			}
			pctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			err := c.requestOn(pctx, p, contract.OpPing)
			cancel()
			if err != nil {
				failures++
				if failures >= 2 {
					ini.Reset()
					return
				}
				continue
			}
			failures = 0
		}
	}
}

// requestOn sends a request on a specific path (keepalive probes).
func (c *Client) requestOn(ctx context.Context, p *pathConn, op string) error {
	id := "k" + time.Now().Format("150405.000000000")
	f, err := contract.Request(id, op, nil)
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
	defer func() { p.mu.Lock(); delete(p.pending, id); p.mu.Unlock() }()
	if err := p.send(ctx, raw); err != nil {
		return err
	}
	select {
	case resp, ok := <-ch:
		if !ok || resp.OK == nil || !*resp.OK {
			return errors.New("ping failed")
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return errors.New("path closed")
	}
}
