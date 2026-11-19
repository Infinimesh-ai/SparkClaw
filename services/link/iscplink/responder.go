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

// DeviceEntry is what the responder needs to know about a peer device.
type DeviceEntry struct {
	Identity        identity.DeviceIdentity
	Trusted         bool
	RevocationEpoch uint64
	Class           string
	Name            string
}

// Directory resolves peer devices. The backend keeps it synced from the
// console and checks it on every message.
type Directory interface {
	Lookup(deviceID string) (DeviceEntry, bool)
}

// GrantPermission is the permission every link Grant carries.
const GrantPermission = "sparkclaw.link"

// ResponderConfig configures the backend side.
type ResponderConfig struct {
	Relay     *relay.Client
	Device    identity.Device
	Signer    func() identity.DeviceIdentity
	Directory Directory
	// OnSession is called (on its own goroutine) for every established
	// session. A newer session from the same device supersedes the older.
	OnSession func(*PeerSession)
	Now       func() time.Time
}

// Responder terminates sessions from many devices on one Relay connection.
type Responder struct {
	cfg      ResponderConfig
	provider crypto.Provider

	mu       sync.Mutex
	grants   map[string]trust.Grant  // sender\x00session -> grant presented
	halves   map[string]*halfSession // sender -> handshake awaiting Ready
	sessions map[string]*PeerSession // sender -> established session
	resets   map[string]time.Time    // sender+session -> last reset sent
}

type halfSession struct {
	sessionID string
	state     *session.State
	grant     trust.Grant
	entry     DeviceEntry
	created   time.Time
}

// PeerSession is one established session with a device.
type PeerSession struct {
	DeviceID  string
	Entry     DeviceEntry
	Grant     trust.Grant
	SessionID string

	r      *Responder
	state  *secureState
	frames chan []byte
	done   chan struct{}
	once   sync.Once
	reason string
}

// NewResponder builds a responder.
func NewResponder(cfg ResponderConfig) (*Responder, error) {
	if cfg.Relay == nil || cfg.Signer == nil || cfg.Directory == nil || cfg.OnSession == nil {
		return nil, errors.New("responder configuration incomplete")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Responder{cfg: cfg, provider: crypto.NewProvider(), grants: map[string]trust.Grant{}, halves: map[string]*halfSession{}, sessions: map[string]*PeerSession{}, resets: map[string]time.Time{}}, nil
}

// Run keeps the Relay connection up until ctx ends.
func (r *Responder) Run(ctx context.Context, onState func(connected bool)) error {
	backoff := time.Second
	for ctx.Err() == nil {
		started := time.Now()
		err := r.cfg.Relay.Run(ctx, r.receive, func() {
			if onState != nil {
				onState(true)
			}
		})
		if onState != nil {
			onState(false)
		}
		r.closeAll("relay_disconnected")
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

func (r *Responder) closeAll(reason string) {
	r.mu.Lock()
	all := make([]*PeerSession, 0, len(r.sessions))
	for _, s := range r.sessions {
		all = append(all, s)
	}
	r.sessions = map[string]*PeerSession{}
	r.halves = map[string]*halfSession{}
	r.grants = map[string]trust.Grant{}
	r.mu.Unlock()
	for _, s := range all {
		s.finish(reason)
	}
}

// Sessions returns the established sessions.
func (r *Responder) Sessions() []*PeerSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*PeerSession, 0, len(r.sessions))
	for _, s := range r.sessions {
		out = append(out, s)
	}
	return out
}

// Revoke ends a device's session and tells it so.
func (r *Responder) Revoke(deviceID string) {
	r.mu.Lock()
	s := r.sessions[deviceID]
	delete(r.sessions, deviceID)
	delete(r.halves, deviceID)
	r.mu.Unlock()
	if s != nil {
		r.sendReset(deviceID, s.SessionID, ResetRevoked, true)
		s.finish(ResetRevoked)
	}
}

func (r *Responder) verifyGrant(g trust.Grant, entry DeviceEntry, sender string) error {
	return trust.VerifyGrant(r.provider, g, r.cfg.Signer(), trust.VerifyOptions{
		Audience:               r.cfg.Device.Identity.DeviceID,
		SubjectDeviceID:        sender,
		ConfirmationThumbprint: entry.Identity.PublicKey.KID,
		Permission:             GrantPermission,
		RelayID:                r.cfg.Relay.Endpoint().RelayID,
		CurrentRevocationEpoch: entry.RevocationEpoch,
		Now:                    r.cfg.Now(),
	})
}

func (r *Responder) entry(sender string) (DeviceEntry, bool) {
	e, ok := r.cfg.Directory.Lookup(sender)
	if !ok || !e.Trusted || e.Identity.DeviceID != sender {
		return DeviceEntry{}, false
	}
	return e, true
}

func (r *Responder) receive(raw json.RawMessage) {
	var env envelope.SecureEnvelope
	if json.Unmarshal(raw, &env) != nil {
		return
	}
	me := r.cfg.Device.Identity.DeviceID
	sender := env.SenderDeviceID
	if env.RecipientDeviceID != me || sender == "" || sender == me || env.DomainID != r.cfg.Device.Identity.DomainID {
		return
	}
	switch env.PayloadType {
	case PayloadGrant:
		var g trust.Grant
		if decodePlain(env, &g) != nil {
			return
		}
		entry, ok := r.entry(sender)
		if !ok {
			r.sendReset(sender, env.SessionID, ResetRevoked, true)
			return
		}
		if r.verifyGrant(g, entry, sender) != nil {
			r.sendReset(sender, env.SessionID, ResetGrantInvalid, true)
			return
		}
		r.mu.Lock()
		if len(r.grants) > 4096 {
			r.grants = map[string]trust.Grant{}
		}
		r.grants[sender+"\x00"+env.SessionID] = g
		r.mu.Unlock()
	case PayloadHello:
		r.acceptHello(env)
	case PayloadReady:
		r.acceptReady(env)
	case PayloadReset:
		r.mu.Lock()
		s := r.sessions[sender]
		if s != nil && s.SessionID == env.SessionID {
			delete(r.sessions, sender)
		} else {
			s = nil
		}
		r.mu.Unlock()
		if s != nil {
			s.finish("peer_reset")
		}
	case PayloadFrame:
		r.mu.Lock()
		s := r.sessions[sender]
		r.mu.Unlock()
		if s == nil || s.SessionID != env.SessionID {
			r.sendReset(sender, env.SessionID, ResetUnknownSession, false)
			return
		}
		entry, ok := r.entry(sender)
		if !ok {
			r.Revoke(sender)
			return
		}
		if r.verifyGrant(s.Grant, entry, sender) != nil {
			r.mu.Lock()
			if r.sessions[sender] == s {
				delete(r.sessions, sender)
			}
			r.mu.Unlock()
			r.sendReset(sender, s.SessionID, ResetGrantInvalid, true)
			s.finish(ResetGrantInvalid)
			return
		}
		payload, err := s.state.open(r.provider, env)
		if err != nil {
			return
		}
		select {
		case s.frames <- payload:
		case <-s.done:
		default:
			// The handler stopped reading; drop the session rather than
			// block every other device.
			r.mu.Lock()
			if r.sessions[sender] == s {
				delete(r.sessions, sender)
			}
			r.mu.Unlock()
			r.sendReset(sender, s.SessionID, ResetUnknownSession, true)
			s.finish("backpressure")
		}
	}
}

func (r *Responder) acceptHello(env envelope.SecureEnvelope) {
	sender := env.SenderDeviceID
	var remote session.Hello
	if decodePlain(env, &remote) != nil || remote.SessionID != env.SessionID || remote.DeviceID != sender || remote.PeerDeviceID != r.cfg.Device.Identity.DeviceID {
		return
	}
	now := r.cfg.Now()
	if remote.IssuedAt.Before(now.Add(-90*time.Second)) || remote.IssuedAt.After(now.Add(60*time.Second)) {
		r.sendReset(sender, env.SessionID, ResetUnknownSession, true)
		return
	}
	entry, ok := r.entry(sender)
	if !ok {
		r.sendReset(sender, env.SessionID, ResetRevoked, true)
		return
	}
	r.mu.Lock()
	g, ok := r.grants[sender+"\x00"+env.SessionID]
	delete(r.grants, sender+"\x00"+env.SessionID)
	r.mu.Unlock()
	if !ok || g.GrantID != remote.GrantID {
		r.sendReset(sender, env.SessionID, ResetGrantInvalid, true)
		return
	}
	local, err := session.CreateHello(r.provider, r.cfg.Device, env.SessionID, sender, "", now.UTC())
	if err != nil {
		return
	}
	st, err := session.Establish(r.provider, local, remote, r.cfg.Device.Identity, entry.Identity)
	if err != nil {
		r.sendReset(sender, env.SessionID, ResetUnknownSession, true)
		return
	}
	ready, err := st.CreateReady(r.provider, r.cfg.Device)
	if err != nil {
		return
	}
	r.mu.Lock()
	r.halves[sender] = &halfSession{sessionID: env.SessionID, state: st, grant: g, entry: entry, created: now}
	r.mu.Unlock()
	relayID := r.cfg.Relay.Endpoint().RelayID
	domain := r.cfg.Device.Identity.DomainID
	me := r.cfg.Device.Identity.DeviceID
	h, err := plaintext(domain, env.SessionID, me, sender, relayID, PayloadHello, HandshakeTTL, local.Hello)
	if err != nil {
		return
	}
	rd, err := plaintext(domain, env.SessionID, me, sender, relayID, PayloadReady, HandshakeTTL, ready)
	if err != nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if r.cfg.Relay.Submit(ctx, h) != nil {
			return
		}
		_ = r.cfg.Relay.Submit(ctx, rd)
	}()
}

func (r *Responder) acceptReady(env envelope.SecureEnvelope) {
	sender := env.SenderDeviceID
	var ready session.Ready
	if decodePlain(env, &ready) != nil {
		return
	}
	r.mu.Lock()
	half := r.halves[sender]
	r.mu.Unlock()
	if half == nil || half.sessionID != env.SessionID {
		return
	}
	if half.state.VerifyReady(r.provider, ready, half.entry.Identity) != nil {
		return
	}
	ps := &PeerSession{DeviceID: sender, Entry: half.entry, Grant: half.grant, SessionID: half.sessionID, r: r, state: &secureState{state: half.state, relayID: r.cfg.Relay.Endpoint().RelayID, created: time.Now()}, frames: make(chan []byte, 512), done: make(chan struct{})}
	r.mu.Lock()
	if r.halves[sender] != half {
		r.mu.Unlock()
		return
	}
	delete(r.halves, sender)
	old := r.sessions[sender]
	r.sessions[sender] = ps
	r.mu.Unlock()
	if old != nil {
		old.finish(ResetSuperseded)
	}
	go r.cfg.OnSession(ps)
}

func (r *Responder) sendReset(recipient, sessionID, reason string, force bool) {
	key := recipient + "\x00" + sessionID
	r.mu.Lock()
	last := r.resets[key]
	if !force && time.Since(last) < 2*time.Second {
		r.mu.Unlock()
		return
	}
	r.resets[key] = time.Now()
	if len(r.resets) > 4096 {
		r.resets = map[string]time.Time{}
	}
	r.mu.Unlock()
	env, err := plaintext(r.cfg.Device.Identity.DomainID, sessionID, r.cfg.Device.Identity.DeviceID, recipient, r.cfg.Relay.Endpoint().RelayID, PayloadReset, HandshakeTTL, resetBody{Reason: reason})
	if err != nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = r.cfg.Relay.Submit(ctx, env)
	}()
}

// Frames delivers decrypted frames in order.
func (p *PeerSession) Frames() <-chan []byte { return p.frames }

// Done closes when the session ends.
func (p *PeerSession) Done() <-chan struct{} { return p.done }

// Reason says why the session ended.
func (p *PeerSession) Reason() string { return p.reason }

func (p *PeerSession) finish(reason string) {
	p.once.Do(func() {
		p.reason = reason
		close(p.done)
	})
}

// Send encrypts and submits one frame to the device.
func (p *PeerSession) Send(ctx context.Context, frame []byte) error {
	select {
	case <-p.done:
		return ErrNotReady
	default:
	}
	env, err := p.state.seal(p.r.provider, frame)
	if err != nil {
		return err
	}
	return p.r.cfg.Relay.Submit(ctx, env)
}

// Close ends the session locally and tells the device to handshake again.
func (p *PeerSession) Close(reason string) {
	p.r.mu.Lock()
	if p.r.sessions[p.DeviceID] == p {
		delete(p.r.sessions, p.DeviceID)
	}
	p.r.mu.Unlock()
	p.r.sendReset(p.DeviceID, p.SessionID, reason, true)
	p.finish(reason)
}
