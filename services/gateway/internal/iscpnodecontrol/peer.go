// Package iscpnodecontrol carries a bounded application RPC over ordinary ISCP
// encrypted sessions and Relay HTTPS/WSS. It never uses a reverse TCP tunnel.
package iscpnodecontrol

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpnodeissuer"
	c "github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/envelope"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/session"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/trust"
	"sync"
	"time"
)

const ControlPermission = "sparkclaw.issuer.control.v1"
const payloadType = "sparkclaw.issuer.control.rpc.v1"

type Relay interface {
	Submit(context.Context, any) error
	RunOnceReady(context.Context, func(context.Context, json.RawMessage) error, func()) error
}
type Handler func(context.Context, string, json.RawMessage) (json.RawMessage, error)
type PeerConfig struct {
	Initiator bool
	Device    identity.Device
	Peer      identity.DeviceIdentity
	Issuer    identity.DeviceIdentity
	Grant     trust.Grant
	RelayID   string
}
type rpc struct {
	Version int             `json:"version"`
	ID      string          `json:"id"`
	Kind    string          `json:"kind"`
	Method  string          `json:"method,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   string          `json:"error,omitempty"`
}
type Peer struct {
	mu              sync.Mutex
	cfg             PeerConfig
	relay           Relay
	handler         Handler
	local           session.LocalHello
	state           *session.State
	lastHello       time.Time
	pending         map[string]chan rpc
	workers         chan struct{}
	standing        bool
	active          bool
	grantFile       string
	minimumRevision uint64
	running         bool
}

func NewPeer(cfg PeerConfig, relay Relay, h Handler) (*Peer, error) {
	return newPeer(cfg, relay, h, false, true, "")
}
func newPeer(cfg PeerConfig, relay Relay, h Handler, standing, active bool, grantFile string) (*Peer, error) {
	if relay == nil || iscpnodeissuer.ValidateIdentity(cfg.Device.Identity) != nil || iscpnodeissuer.ValidateIdentity(cfg.Peer) != nil || cfg.Device.Identity.DomainID != cfg.Peer.DomainID || cfg.Device.Identity.DeviceID == cfg.Peer.DeviceID || cfg.RelayID == "" {
		return nil, iscpnodeissuer.ErrInvalid
	}
	p := &Peer{cfg: cfg, relay: relay, handler: h, pending: map[string]chan rpc{}, workers: make(chan struct{}, 16), standing: standing, active: active, grantFile: grantFile, minimumRevision: cfg.Grant.RevocationEpoch}
	at := time.Now().UTC()
	if standing {
		at = cfg.Grant.NotBefore
	}
	if !active || p.minimumRevision == 0 || p.validateGrant(cfg.Grant, at) != nil {
		return nil, iscpnodeissuer.ErrDenied
	}
	return p, nil
}
func (p *Peer) verifyGrant() error {
	if !p.active {
		return iscpnodeissuer.ErrDenied
	}
	return p.validateGrant(p.cfg.Grant, time.Now().UTC())
}
func (p *Peer) Ready() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state != nil && p.state.Ready() && p.verifyGrant() == nil
}
func (p *Peer) begin(ctx context.Context) error {
	p.mu.Lock()
	if !p.cfg.Initiator || (p.state != nil && p.state.Ready()) {
		p.mu.Unlock()
		return nil
	}
	if p.verifyGrant() != nil {
		p.mu.Unlock()
		return iscpnodeissuer.ErrDenied
	}
	if !p.local.Hello.IssuedAt.IsZero() && time.Since(p.local.Hello.IssuedAt) < 5*time.Second {
		p.mu.Unlock()
		return nil
	}
	local, e := session.CreateHello(c.NewProvider(), p.cfg.Device, "ctl-"+c.Base64URL(c.RandomBytes(18)), p.cfg.Peer.DeviceID, p.cfg.Grant.GrantID, time.Now().UTC())
	if e != nil {
		p.mu.Unlock()
		return e
	}
	p.local = local
	p.state = nil
	p.mu.Unlock()
	return p.sendHandshake(ctx, session.TypeHello, local.Hello, local.Hello.SessionID)
}
func (p *Peer) sendHandshake(ctx context.Context, kind string, v any, sid string) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return p.relay.Submit(ctx, envelope.SecureEnvelope{Type: envelope.TypeSecureEnvelope, DomainID: p.cfg.Device.Identity.DomainID, MessageID: "ctl-hs-" + c.Base64URL(c.RandomBytes(18)), SessionID: sid, SenderDeviceID: p.cfg.Device.Identity.DeviceID, RecipientDeviceID: p.cfg.Peer.DeviceID, Sequence: 0, Nonce: c.Base64URL(c.RandomBytes(12)), PayloadType: kind, Route: envelope.Route{RelayID: p.cfg.RelayID, TTLSeconds: 60}, Ciphertext: c.Base64URL(b)})
}
func (p *Peer) receive(ctx context.Context, raw json.RawMessage) error {
	var env envelope.SecureEnvelope
	if iscpnodeissuer.StrictDecode(raw, &env) != nil || env.DomainID != p.cfg.Device.Identity.DomainID || env.SenderDeviceID != p.cfg.Peer.DeviceID || env.RecipientDeviceID != p.cfg.Device.Identity.DeviceID || env.Route.RelayID != p.cfg.RelayID {
		return nil
	}
	if env.PayloadType == session.TypeHello {
		b, e := c.DecodeBase64URL(env.Ciphertext)
		if e != nil {
			return nil
		}
		var hello session.Hello
		if iscpnodeissuer.StrictDecode(b, &hello) != nil || hello.PeerDeviceID != p.cfg.Device.Identity.DeviceID || hello.GrantID != p.cfg.Grant.GrantID || hello.SessionID != env.SessionID || time.Since(hello.IssuedAt) > time.Minute || hello.IssuedAt.After(time.Now().Add(time.Minute)) || session.VerifyHello(c.NewProvider(), hello, p.cfg.Peer) != nil {
			return nil
		}
		p.mu.Lock()
		if p.verifyGrant() != nil {
			p.mu.Unlock()
			return nil
		}
		if !p.cfg.Initiator {
			if !hello.IssuedAt.After(p.lastHello) {
				p.mu.Unlock()
				return nil
			}
			p.lastHello = hello.IssuedAt
			p.local, e = session.CreateHello(c.NewProvider(), p.cfg.Device, hello.SessionID, p.cfg.Peer.DeviceID, p.cfg.Grant.GrantID, time.Now().UTC())
			if e != nil {
				p.mu.Unlock()
				return e
			}
			p.state = nil
		} else if hello.SessionID != p.local.Hello.SessionID {
			p.mu.Unlock()
			return nil
		}
		p.state, e = session.Establish(c.NewProvider(), p.local, hello, p.cfg.Device.Identity, p.cfg.Peer)
		if e != nil {
			p.mu.Unlock()
			return nil
		}
		ready, e := p.state.CreateReady(c.NewProvider(), p.cfg.Device)
		local := p.local
		responder := !p.cfg.Initiator
		p.mu.Unlock()
		if e != nil {
			return e
		}
		if responder {
			if e = p.sendHandshake(ctx, session.TypeHello, local.Hello, hello.SessionID); e != nil {
				return e
			}
		}
		return p.sendHandshake(ctx, session.TypeReady, ready, hello.SessionID)
	}
	if env.PayloadType == session.TypeReady {
		b, e := c.DecodeBase64URL(env.Ciphertext)
		if e != nil {
			return nil
		}
		var ready session.Ready
		if iscpnodeissuer.StrictDecode(b, &ready) != nil {
			return nil
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.state == nil {
			return nil
		}
		_ = p.state.VerifyReady(c.NewProvider(), ready, p.cfg.Peer)
		return nil
	}
	if env.PayloadType != payloadType {
		return nil
	}
	p.mu.Lock()
	if p.state == nil || !p.state.Ready() || p.verifyGrant() != nil {
		p.mu.Unlock()
		return nil
	}
	b, e := envelope.Decrypt(c.NewProvider(), p.state, env)
	p.mu.Unlock()
	if e != nil || len(b) > iscpnodeissuer.MaxFrameBytes {
		return nil
	}
	var msg rpc
	if iscpnodeissuer.StrictDecode(b, &msg) != nil || msg.Version != 1 || len(msg.ID) < 8 || len(msg.ID) > 200 {
		return nil
	}
	if msg.Kind == "response" {
		p.mu.Lock()
		ch := p.pending[msg.ID]
		p.mu.Unlock()
		if ch != nil {
			select {
			case ch <- msg:
			default:
			}
		}
		return nil
	}
	if msg.Kind != "request" || p.handler == nil {
		return nil
	}
	select {
	case p.workers <- struct{}{}:
	default:
		return p.send(ctx, rpc{Version: 1, ID: msg.ID, Kind: "response", Error: "unavailable"})
	}
	go func() {
		defer func() { <-p.workers }()
		callCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		result, e := p.handler(callCtx, msg.Method, msg.Payload)
		reply := rpc{Version: 1, ID: msg.ID, Kind: "response", Payload: result}
		if e != nil {
			reply.Payload = nil
			reply.Error = errorCode(e)
		}
		_ = p.send(callCtx, reply)
	}()
	return nil
}
func errorCode(e error) string {
	switch {
	case errors.Is(e, iscpnodeissuer.ErrInvalid):
		return "invalid"
	case errors.Is(e, iscpnodeissuer.ErrDenied):
		return "denied"
	case errors.Is(e, iscpnodeissuer.ErrConflict):
		return "conflict"
	case errors.Is(e, iscpnodeissuer.ErrExpired):
		return "expired"
	default:
		return "unavailable"
	}
}
func wireError(code string) error {
	switch code {
	case "invalid":
		return iscpnodeissuer.ErrInvalid
	case "denied":
		return iscpnodeissuer.ErrDenied
	case "conflict":
		return iscpnodeissuer.ErrConflict
	case "expired":
		return iscpnodeissuer.ErrExpired
	default:
		return iscpnodeissuer.ErrUnavailable
	}
}
func (p *Peer) send(ctx context.Context, msg rpc) error {
	raw, e := json.Marshal(msg)
	if e != nil || len(raw) > iscpnodeissuer.MaxFrameBytes {
		return iscpnodeissuer.ErrInvalid
	}
	p.mu.Lock()
	if p.state == nil || !p.state.Ready() || p.verifyGrant() != nil {
		p.mu.Unlock()
		return iscpnodeissuer.ErrUnavailable
	}
	env, e := envelope.Encrypt(c.NewProvider(), p.state, "ctl-rpc-"+c.Base64URL(c.RandomBytes(18)), payloadType, envelope.Route{RelayID: p.cfg.RelayID, TTLSeconds: 60}, raw)
	p.mu.Unlock()
	if e != nil {
		return e
	}
	return p.relay.Submit(ctx, env)
}
func (p *Peer) Call(ctx context.Context, method string, input, out any) error {
	if len(method) > 80 {
		return iscpnodeissuer.ErrInvalid
	}
	raw, e := json.Marshal(input)
	if e != nil || len(raw) > iscpnodeissuer.MaxFrameBytes-1024 {
		return iscpnodeissuer.ErrInvalid
	}
	id := "rpc-" + c.Base64URL(c.RandomBytes(18))
	ch := make(chan rpc, 1)
	p.mu.Lock()
	if len(p.pending) >= 64 {
		p.mu.Unlock()
		return iscpnodeissuer.ErrUnavailable
	}
	p.pending[id] = ch
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.pending, id); p.mu.Unlock() }()
	if e = p.send(ctx, rpc{Version: 1, ID: id, Kind: "request", Method: method, Payload: raw}); e != nil {
		return e
	}
	select {
	case <-ctx.Done():
		return iscpnodeissuer.ErrUnavailable
	case r := <-ch:
		if r.Error != "" {
			return wireError(r.Error)
		}
		if out == nil {
			return nil
		}
		return iscpnodeissuer.StrictDecode(r.Payload, out)
	}
}

// Run preserves the encrypted session after standard ready/drained completion;
// fresh process/session handshakes remain explicit and independently proven.
func (p *Peer) Run(ctx context.Context) error {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return iscpnodeissuer.ErrConflict
	}
	p.running = true
	p.mu.Unlock()
	defer func() { p.mu.Lock(); p.running = false; p.mu.Unlock() }()
	for ctx.Err() == nil {
		_ = p.begin(ctx)
		_ = p.relay.RunOnceReady(ctx, p.receive, func() {})
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return ctx.Err()
}
