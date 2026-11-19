package iscplink

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/envelope"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/session"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/trust"

	"github.com/Chiiz0/SparkClaw/services/link/relay"
)

// Initiator states reported through OnState.
const (
	StateConnecting   = "connecting"
	StateHandshaking  = "handshaking"
	StateReady        = "ready"
	StateDisconnected = "disconnected"
)

// InitiatorConfig configures a device's session with its backend.
type InitiatorConfig struct {
	Relay  *relay.Client
	Device identity.Device
	Host   identity.DeviceIdentity
	// Grant returns the device's current Grant (renewing it when needed).
	Grant func(ctx context.Context) (trust.Grant, error)
	// OnFrame receives every decrypted link frame, in order.
	OnFrame func([]byte)
	// OnState reports connection state changes.
	OnState func(string)
}

// Initiator keeps one end-to-end session from a device to the backend.
type Initiator struct {
	cfg      InitiatorConfig
	provider crypto.Provider

	mu      sync.Mutex
	current *secureState
	sessID  string
	pending *pendingHandshake
	readyCh chan struct{}
	kick    chan struct{}
	state   string
}

type pendingHandshake struct {
	sessionID string
	local     session.LocalHello
	state     *session.State
}

// NewInitiator builds an initiator.
func NewInitiator(cfg InitiatorConfig) (*Initiator, error) {
	if cfg.Relay == nil || cfg.Grant == nil || cfg.Host.DeviceID == "" {
		return nil, errors.New("initiator configuration incomplete")
	}
	return &Initiator{cfg: cfg, provider: crypto.NewProvider(), readyCh: make(chan struct{}), kick: make(chan struct{}, 1)}, nil
}

func (i *Initiator) setState(s string) {
	i.mu.Lock()
	changed := i.state != s
	i.state = s
	i.mu.Unlock()
	if changed && i.cfg.OnState != nil {
		i.cfg.OnState(s)
	}
}

// State returns the current state.
func (i *Initiator) State() string {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.state == "" {
		return StateDisconnected
	}
	return i.state
}

// Run keeps the Relay connection and the session up until ctx ends.
func (i *Initiator) Run(ctx context.Context) error {
	backoff := time.Second
	for ctx.Err() == nil {
		i.setState(StateConnecting)
		started := time.Now()
		connCtx, cancel := context.WithCancel(ctx)
		err := i.cfg.Relay.Run(connCtx, i.receive, func() { go i.maintain(connCtx) })
		cancel()
		i.dropSession()
		i.setState(StateDisconnected)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_ = err
		if time.Since(started) > time.Minute {
			backoff = time.Second
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
	return ctx.Err()
}

// maintain performs the handshake and repeats it when the session is reset
// or grows old.
func (i *Initiator) maintain(ctx context.Context) {
	for ctx.Err() == nil {
		// A reset that arrived for an earlier session must not end the
		// one about to be established.
		select {
		case <-i.kick:
		default:
		}
		if err := i.handshake(ctx); err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
			continue
		}
		i.mu.Lock()
		cur := i.current
		i.mu.Unlock()
		age := time.NewTimer(MaxSessionAge)
		select {
		case <-ctx.Done():
			age.Stop()
			return
		case <-i.kick:
			age.Stop()
		case <-age.C:
		}
		i.mu.Lock()
		if i.current == cur {
			i.current = nil
			i.readyCh = make(chan struct{})
		}
		i.mu.Unlock()
	}
}

func (i *Initiator) handshake(ctx context.Context) error {
	i.setState(StateHandshaking)
	grant, err := i.cfg.Grant(ctx)
	if err != nil {
		return err
	}
	sid := newID()
	local, err := session.CreateHello(i.provider, i.cfg.Device, sid, i.cfg.Host.DeviceID, grant.GrantID, time.Now().UTC())
	if err != nil {
		return err
	}
	i.mu.Lock()
	i.pending = &pendingHandshake{sessionID: sid, local: local}
	ready := i.readyCh
	i.mu.Unlock()
	relayID := i.cfg.Relay.Endpoint().RelayID
	domain := i.cfg.Device.Identity.DomainID
	me := i.cfg.Device.Identity.DeviceID
	g, err := plaintext(domain, sid, me, i.cfg.Host.DeviceID, relayID, PayloadGrant, HandshakeTTL, grant)
	if err != nil {
		return err
	}
	h, err := plaintext(domain, sid, me, i.cfg.Host.DeviceID, relayID, PayloadHello, HandshakeTTL, local.Hello)
	if err != nil {
		return err
	}
	sctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := i.cfg.Relay.Submit(sctx, g); err != nil {
		return err
	}
	if err := i.cfg.Relay.Submit(sctx, h); err != nil {
		return err
	}
	wait := time.NewTimer(20 * time.Second)
	defer wait.Stop()
	select {
	case <-ready:
		i.setState(StateReady)
		return nil
	case <-wait.C:
		return errors.New("backend did not answer the handshake")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (i *Initiator) dropSession() {
	i.mu.Lock()
	i.current = nil
	i.pending = nil
	i.sessID = ""
	select {
	case <-i.readyCh:
		i.readyCh = make(chan struct{})
	default:
	}
	i.mu.Unlock()
}

func (i *Initiator) reset() {
	i.dropSession()
	select {
	case i.kick <- struct{}{}:
	default:
	}
}

func (i *Initiator) receive(raw json.RawMessage) {
	var env envelope.SecureEnvelope
	if json.Unmarshal(raw, &env) != nil {
		return
	}
	if env.RecipientDeviceID != i.cfg.Device.Identity.DeviceID || env.SenderDeviceID != i.cfg.Host.DeviceID {
		return
	}
	switch env.PayloadType {
	case PayloadHello:
		var remote session.Hello
		if decodePlain(env, &remote) != nil {
			return
		}
		i.mu.Lock()
		p := i.pending
		i.mu.Unlock()
		if p == nil || p.sessionID != env.SessionID || remote.SessionID != env.SessionID {
			return
		}
		st, err := session.Establish(i.provider, p.local, remote, i.cfg.Device.Identity, i.cfg.Host)
		if err != nil {
			return
		}
		i.mu.Lock()
		if i.pending == p {
			p.state = st
		}
		i.mu.Unlock()
	case PayloadReady:
		var ready session.Ready
		if decodePlain(env, &ready) != nil {
			return
		}
		i.mu.Lock()
		p := i.pending
		i.mu.Unlock()
		if p == nil || p.state == nil || p.sessionID != env.SessionID {
			return
		}
		if p.state.VerifyReady(i.provider, ready, i.cfg.Host) != nil {
			return
		}
		mine, err := p.state.CreateReady(i.provider, i.cfg.Device)
		if err != nil {
			return
		}
		out, err := plaintext(i.cfg.Device.Identity.DomainID, p.sessionID, i.cfg.Device.Identity.DeviceID, i.cfg.Host.DeviceID, i.cfg.Relay.Endpoint().RelayID, PayloadReady, HandshakeTTL, mine)
		if err != nil {
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if i.cfg.Relay.Submit(ctx, out) != nil {
				i.reset()
				return
			}
			i.mu.Lock()
			if i.pending != p {
				i.mu.Unlock()
				return
			}
			i.current = &secureState{state: p.state, relayID: i.cfg.Relay.Endpoint().RelayID, created: time.Now()}
			i.sessID = p.sessionID
			i.pending = nil
			ch := i.readyCh
			i.mu.Unlock()
			close(ch)
		}()
	case PayloadReset:
		i.mu.Lock()
		match := env.SessionID == i.sessID || (i.pending != nil && env.SessionID == i.pending.sessionID)
		i.mu.Unlock()
		if match {
			i.reset()
		}
	case PayloadFrame:
		i.mu.Lock()
		cur, sid := i.current, i.sessID
		i.mu.Unlock()
		if cur == nil || env.SessionID != sid {
			return
		}
		payload, err := cur.open(i.provider, env)
		if err != nil {
			return
		}
		if i.cfg.OnFrame != nil {
			i.cfg.OnFrame(payload)
		}
	}
}

// ErrNotReady is returned when no session is established in time.
var ErrNotReady = errors.New("backend link is not ready")

// WaitReady blocks until a session is ready or ctx ends.
func (i *Initiator) WaitReady(ctx context.Context) error {
	i.mu.Lock()
	ch, cur := i.readyCh, i.current
	i.mu.Unlock()
	if cur != nil {
		return nil
	}
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ErrNotReady
	}
}

// Ready reports whether a session is established.
func (i *Initiator) Ready() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.current != nil
}

// Send encrypts one frame and hands it to the Relay, waiting for the
// session while ctx allows.
func (i *Initiator) Send(ctx context.Context, frame []byte) error {
	if err := i.WaitReady(ctx); err != nil {
		return err
	}
	i.mu.Lock()
	cur := i.current
	i.mu.Unlock()
	if cur == nil {
		return ErrNotReady
	}
	env, err := cur.seal(i.provider, frame)
	if err != nil {
		return ErrNotReady
	}
	if err := i.cfg.Relay.Submit(ctx, env); err != nil {
		var rej *relay.ErrRejected
		if errors.As(err, &rej) || errors.Is(err, relay.ErrNotConnected) {
			return ErrNotReady
		}
		return err
	}
	return nil
}

// Reset forces a new handshake (for example after a renewed Grant).
func (i *Initiator) Reset() { i.reset() }
