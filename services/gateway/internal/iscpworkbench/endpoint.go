package iscpworkbench

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpbridge"
	iscpcrypto "github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/envelope"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/session"
)

const (
	manifestType       = "sparkclaw.workbench.capability.v1"
	pingType           = "sparkclaw.workbench.ping.v1"
	pongType           = "sparkclaw.workbench.pong.v1"
	requestTimeout     = 30 * time.Second
	handshakeTimeout   = 10 * time.Second
	heartbeatInterval  = 10 * time.Second
	heartbeatTimeout   = 35 * time.Second
	maxSessionMessages = 4096
)

// RelayTransport is injectable for encrypted integration tests. Runtime
// construction still requires pinned identities, private files and HTTPS/WSS;
// no configuration flag permits an insecure Relay transport.
type RelayTransport interface {
	Submit(context.Context, any) error
	RunOnceReady(context.Context, func(context.Context, json.RawMessage) error, func()) error
}

type capabilityManifest struct {
	Profile          string   `json:"profile"`
	Role             string   `json:"role"`
	Operations       []string `json:"operations"`
	MaxRequestBytes  int      `json:"max_request_bytes"`
	MaxResponseBytes int      `json:"max_response_bytes"`
	MaxConcurrent    int      `json:"max_concurrent"`
}

type activeSession struct {
	id                              string
	local                           session.LocalHello
	state                           *session.State
	manifest                        bool
	started, timeReceived, timePing time.Time
	messageCount                    int
	cancel                          context.CancelFunc
	ctx                             context.Context
	seenRequests                    map[string]struct{}
}

type callResult struct {
	response Response
	err      error
}

type Endpoint struct {
	config          Config
	material        material
	provider        iscpcrypto.Provider
	relay           RelayTransport
	handler         Handler
	onState         func(string)
	mu              sync.Mutex
	session         *activeSession
	stale           []string
	pending         map[string]chan callResult
	stateName       string
	lastHelloAt     time.Time
	cancel          context.CancelFunc
	running, closed bool
	slots           chan struct{}
	workers         sync.WaitGroup
	verifyDiscovery func(context.Context) error
}

func NewEndpoint(cfg Config, handler Handler, onState func(string)) (*Endpoint, error) {
	m, err := loadMaterial(cfg)
	if err != nil {
		return nil, err
	}
	relay, err := iscpbridge.NewRelayCredentialClient(cfg.EffectiveRelayProfile(), cfg.EnrollmentFile, m.enrollment, m.device, requestTimeout)
	if err != nil {
		return nil, errors.New("create workbench Relay transport")
	}
	e, err := newEndpoint(cfg, m, relay, handler, onState)
	if err == nil {
		e.verifyDiscovery = func(ctx context.Context) error {
			return iscpbridge.VerifyWorkbenchRelayDiscovery(ctx, cfg.EffectiveRelayProfile(), m.enrollment)
		}
	}
	return e, err
}

func NewEndpointWithRelay(cfg Config, relay RelayTransport, handler Handler, onState func(string)) (*Endpoint, error) {
	m, err := loadMaterial(cfg)
	if err != nil {
		return nil, err
	}
	return newEndpoint(cfg, m, relay, handler, onState)
}

func newEndpoint(cfg Config, m material, relay RelayTransport, handler Handler, onState func(string)) (*Endpoint, error) {
	if relay == nil {
		return nil, errors.New("workbench Relay transport is required")
	}
	if cfg.Role == RoleResponder && handler == nil {
		return nil, errors.New("workbench responder requires a handler")
	}
	e := &Endpoint{config: cfg, material: m, provider: iscpcrypto.NewProvider(), relay: relay, handler: handler, onState: onState, pending: map[string]chan callResult{}, slots: make(chan struct{}, MaxConcurrent)}
	if cfg.Role == RoleResponder && cfg.EffectiveRelayProfile() == iscpbridge.ProfileLocalLab {
		// The isolated reference queue outlives a Gateway process. A Hello
		// predating this responder cannot recover keys from its old process;
		// let the desktop's bounded retry initiate a fresh session instead.
		e.lastHelloAt = time.Now().UTC().Add(-time.Second)
	}
	return e, nil
}

func (e *Endpoint) setState(state string) {
	e.mu.Lock()
	if e.closed && state != "closed" {
		e.mu.Unlock()
		return
	}
	changed := e.stateName != state
	e.stateName = state
	e.mu.Unlock()
	if changed && e.onState != nil {
		e.onState(state)
	}
}

// Run owns the authenticated receive connection and retries with bounded
// backoff. Every reconnect discards old keys and causes a fresh desktop Hello.
func (e *Endpoint) Run(ctx context.Context) error {
	e.mu.Lock()
	if e.running || e.closed {
		e.mu.Unlock()
		return errors.New("workbench endpoint is already running or closed")
	}
	ctx, cancel := context.WithCancel(ctx)
	e.cancel, e.running = cancel, true
	e.mu.Unlock()
	defer func() {
		cancel()
		e.mu.Lock()
		e.resetLocked()
		e.mu.Unlock()
		e.workers.Wait()
		e.setState("disconnected")
	}()
	backoff := time.Second
	for ctx.Err() == nil {
		if e.verifyDiscovery != nil {
			e.setState("verifying_relay")
			if err := e.verifyDiscovery(ctx); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				e.setState("discovery_failed")
				timer := time.NewTimer(backoff)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-timer.C:
				}
				backoff = min(backoff*2, 15*time.Second)
				continue
			}
		}
		e.setState("connecting")
		connCtx, stop := context.WithCancel(ctx)
		var monitor sync.WaitGroup
		err := e.relay.RunOnceReady(connCtx, func(callCtx context.Context, raw json.RawMessage) error { return e.receive(callCtx, raw) }, func() {
			e.setState("relay_ready")
			monitor.Add(1)
			go func() { defer monitor.Done(); e.sessionLoop(connCtx, stop) }()
		})
		stop()
		monitor.Wait()
		e.mu.Lock()
		e.resetLocked()
		e.mu.Unlock()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		e.setState("disconnected")
		if err == nil {
			backoff = time.Second
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		backoff = min(backoff*2, 15*time.Second)
	}
	return ctx.Err()
}

func (e *Endpoint) sessionLoop(ctx context.Context, stopConnection context.CancelFunc) {
	if e.config.Role == RoleInitiator {
		_ = e.initiate(ctx)
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if err := verifyGrant(e.config, e.material, now.UTC()); err != nil {
				e.mu.Lock()
				e.resetLocked()
				e.mu.Unlock()
				e.setState("authorization_expired")
				continue
			}
			e.mu.Lock()
			s := e.session
			missing := s == nil
			timedOut := s != nil && ((!s.manifest && now.Sub(s.started) > handshakeTimeout) || (s.manifest && now.Sub(s.timeReceived) > heartbeatTimeout))
			if timedOut {
				e.resetLocked()
				s = nil
				missing = true
			}
			ping := s != nil && s.manifest && now.Sub(s.timePing) >= heartbeatInterval
			if ping {
				s.timePing = now
			}
			e.mu.Unlock()
			if timedOut {
				e.setState("disconnected")
				// An authenticated but stalled receive socket must be replaced;
				// repeatedly posting Hello cannot repair that WebSocket stream.
				stopConnection()
				return
			}
			if missing && e.config.Role == RoleInitiator {
				_ = e.initiate(ctx)
			}
			if ping {
				raw, _ := json.Marshal(map[string]string{"nonce": newUUID()})
				_ = e.sendPayload(ctx, pingType, raw, s.id)
			}
		}
	}
}

func (e *Endpoint) initiate(ctx context.Context) error {
	if err := verifyGrant(e.config, e.material, time.Now().UTC()); err != nil {
		e.setState("authorization_expired")
		return err
	}
	id := newUUID()
	local, err := session.CreateHello(e.provider, e.material.device, id, e.material.peer.DeviceID, e.material.grant.GrantID, time.Now().UTC())
	if err != nil {
		return errors.New("create workbench Hello")
	}
	e.mu.Lock()
	e.resetLocked()
	e.session = newSession(ctx, id, local)
	e.mu.Unlock()
	e.setState("handshaking")
	return e.sendHandshake(ctx, id, session.TypeHello, local.Hello)
}

func newSession(ctx context.Context, id string, local session.LocalHello) *activeSession {
	ctx, cancel := context.WithCancel(ctx)
	now := time.Now().UTC()
	return &activeSession{id: id, local: local, ctx: ctx, cancel: cancel, started: now, timeReceived: now, timePing: now, seenRequests: map[string]struct{}{}}
}

// resetLocked must hold mu; pending calls fail immediately instead of waiting
// for a response that belongs to discarded session keys.
func (e *Endpoint) resetLocked() {
	if e.session != nil {
		e.session.cancel()
		e.stale = append(e.stale, e.session.id)
		if len(e.stale) > 64 {
			e.stale = e.stale[len(e.stale)-64:]
		}
		e.session = nil
	}
	for id, ch := range e.pending {
		ch <- callResult{err: errors.New("workbench session disconnected")}
		delete(e.pending, id)
	}
}

func (e *Endpoint) sendHandshake(ctx context.Context, id, payloadType string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return errors.New("encode workbench handshake")
	}
	b := e.material.enrollment
	env := envelope.SecureEnvelope{Type: envelope.TypeSecureEnvelope, DomainID: b.DomainID, MessageID: newUUID(), SessionID: id, SenderDeviceID: b.DeviceID, RecipientDeviceID: e.material.peer.DeviceID, PayloadType: payloadType, Nonce: iscpcrypto.Base64URL(randomBytes(12)), Route: envelope.Route{RelayID: b.RelayID, TTLSeconds: 30, Priority: 5}, Ciphertext: base64.RawURLEncoding.EncodeToString(raw)}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	if err := e.relay.Submit(ctx, env); err != nil {
		return errors.New("submit workbench handshake")
	}
	return nil
}

func (e *Endpoint) receive(ctx context.Context, raw json.RawMessage) error {
	if len(raw) > 100<<10 {
		return errors.New("workbench envelope exceeds limit")
	}
	var env envelope.SecureEnvelope
	if err := strictDecode(raw, &env); err != nil {
		return errors.New("invalid workbench envelope")
	}
	b := e.material.enrollment
	if env.Type != envelope.TypeSecureEnvelope || env.DomainID != b.DomainID || env.SenderDeviceID != e.material.peer.DeviceID || env.RecipientDeviceID != b.DeviceID || env.Route.RelayID != b.RelayID || env.Route.TTLSeconds <= 0 || env.Route.TTLSeconds > 30 || !uuidPattern.MatchString(env.SessionID) || !uuidPattern.MatchString(env.MessageID) {
		return errors.New("workbench envelope identity or route mismatch")
	}
	if err := verifyGrant(e.config, e.material, time.Now().UTC()); err != nil {
		return err
	}
	switch env.PayloadType {
	case session.TypeHello:
		if e.config.Role == RoleInitiator && !e.matchesSession(env.SessionID) {
			return nil
		}
		return e.acceptHello(ctx, env)
	case session.TypeReady:
		if !e.matchesSession(env.SessionID) {
			return nil
		}
		return e.acceptReady(ctx, env)
	case manifestType, RequestType, ResponseType, pingType, pongType:
		if !e.matchesSession(env.SessionID) {
			return nil
		}
		return e.acceptEncrypted(ctx, env)
	default:
		return errors.New("unsupported workbench payload type")
	}
}

// Queue retention is independent of process/session lifetime. Unknown or
// retired sessions are discarded without decrypting, updating readiness or
// calling business handlers; they must not abort a drain snapshot that also
// contains the fresh peer handshake. Current-session failures still reject.
func (e *Endpoint) matchesSession(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.session != nil && e.session.id == id
}

func handshakeDecode(env envelope.SecureEnvelope, value any) error {
	if env.Sequence != 0 {
		return errors.New("invalid handshake envelope sequence")
	}
	raw, err := base64.RawURLEncoding.DecodeString(env.Ciphertext)
	if err != nil || len(raw) > 8<<10 {
		return errors.New("invalid handshake encoding")
	}
	return strictDecode(raw, value)
}

func (e *Endpoint) acceptHello(ctx context.Context, env envelope.SecureEnvelope) error {
	var hello session.Hello
	if err := handshakeDecode(env, &hello); err != nil {
		return errors.New("invalid workbench Hello")
	}
	now := time.Now().UTC()
	if hello.SessionID != env.SessionID || hello.PeerDeviceID != e.material.device.Identity.DeviceID || hello.Signature.Alg != "Ed25519" || hello.Signature.KID != e.material.peer.PublicKey.KID || hello.IssuedAt.Sub(now) > 30*time.Second {
		return errors.New("workbench Hello binding or freshness mismatch")
	}
	if err := session.VerifyHello(e.provider, hello, e.material.peer); err != nil {
		return errors.New("workbench Hello signature verification failed")
	}
	if now.Sub(hello.IssuedAt) > handshakeTimeout {
		return nil
	}
	e.mu.Lock()
	if slices.Contains(e.stale, env.SessionID) {
		e.mu.Unlock()
		return nil
	}
	var local session.LocalHello
	var err error
	if e.config.Role == RoleResponder {
		if hello.GrantID != e.material.grant.GrantID {
			e.mu.Unlock()
			return errors.New("workbench initiator grant ID mismatch")
		}
		if e.session != nil && e.session.id == env.SessionID {
			e.mu.Unlock()
			return nil
		}
		if !hello.IssuedAt.After(e.lastHelloAt) {
			e.mu.Unlock()
			return nil
		}
		local, err = session.CreateHello(e.provider, e.material.device, env.SessionID, e.material.peer.DeviceID, "", now)
		if err != nil {
			e.mu.Unlock()
			return errors.New("create workbench responder Hello")
		}
		e.resetLocked()
		e.session = newSession(ctx, env.SessionID, local)
		e.lastHelloAt = hello.IssuedAt
	} else {
		if e.session == nil || e.session.id != env.SessionID || e.session.state != nil {
			e.mu.Unlock()
			return nil
		}
		if hello.GrantID != "" {
			e.mu.Unlock()
			return errors.New("unexpected responder workbench Hello")
		}
		local = e.session.local
	}
	state, err := session.Establish(e.provider, local, hello, e.material.device.Identity, e.material.peer)
	if err != nil {
		e.mu.Unlock()
		return errors.New("establish workbench session")
	}
	ready, err := state.CreateReady(e.provider, e.material.device)
	if err != nil {
		e.mu.Unlock()
		return errors.New("create workbench Ready")
	}
	e.session.state = state
	e.mu.Unlock()
	e.setState("handshaking")
	if e.config.Role == RoleResponder {
		if err := e.sendHandshake(ctx, env.SessionID, session.TypeHello, local.Hello); err != nil {
			return err
		}
	}
	return e.sendHandshake(ctx, env.SessionID, session.TypeReady, ready)
}

func (e *Endpoint) acceptReady(ctx context.Context, env envelope.SecureEnvelope) error {
	var ready session.Ready
	if err := handshakeDecode(env, &ready); err != nil {
		return errors.New("invalid workbench Ready")
	}
	if ready.Signature.Alg != "Ed25519" || ready.Signature.KID != e.material.peer.PublicKey.KID {
		return errors.New("workbench Ready signing key mismatch")
	}
	if ready.SessionID != env.SessionID {
		return errors.New("workbench Ready envelope binding mismatch")
	}
	e.mu.Lock()
	s := e.session
	if s == nil || s.id != env.SessionID {
		e.mu.Unlock()
		return nil
	}
	if s.state == nil {
		e.mu.Unlock()
		return errors.New("unexpected or replayed workbench Ready")
	}
	if s.state.Ready() || ready.TranscriptHash != iscpcrypto.Base64URL(s.state.TranscriptHash) {
		e.mu.Unlock()
		return nil
	}
	if err := s.state.VerifyReady(e.provider, ready, e.material.peer); err != nil {
		e.mu.Unlock()
		return errors.New("workbench Ready verification failed")
	}
	e.mu.Unlock()
	manifest := capabilityManifest{Profile: Profile, Role: e.config.Role, Operations: Operations(), MaxRequestBytes: MaxRequestBytes, MaxResponseBytes: MaxResponseBytes, MaxConcurrent: MaxConcurrent}
	raw, _ := json.Marshal(manifest)
	return e.sendPayload(ctx, manifestType, raw, env.SessionID)
}

func (e *Endpoint) sendPayload(ctx context.Context, payloadType string, raw []byte, id string) error {
	if len(raw) > MaxMessageBytes {
		return errors.New("workbench payload exceeds limit")
	}
	if err := verifyGrant(e.config, e.material, time.Now().UTC()); err != nil {
		return err
	}
	e.mu.Lock()
	s := e.session
	if e.closed || s == nil || s.id != id || s.state == nil || !s.state.Ready() || (payloadType != manifestType && !s.manifest) {
		e.mu.Unlock()
		return errors.New("workbench encrypted session is not ready")
	}
	env, err := envelope.Encrypt(e.provider, s.state, newUUID(), payloadType, envelope.Route{RelayID: e.material.enrollment.RelayID, TTLSeconds: 30, Priority: 5}, raw)
	e.mu.Unlock()
	if err != nil {
		return errors.New("encrypt workbench payload")
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	if err := e.relay.Submit(ctx, env); err != nil {
		return errors.New("submit encrypted workbench payload")
	}
	return nil
}

func (e *Endpoint) acceptEncrypted(ctx context.Context, env envelope.SecureEnvelope) error {
	e.mu.Lock()
	s := e.session
	if s == nil || s.id != env.SessionID {
		e.mu.Unlock()
		return nil
	}
	notReady := s.state == nil || !s.state.Ready()
	if notReady || (env.PayloadType != manifestType && !s.manifest) {
		e.mu.Unlock()
		if e.config.EffectiveRelayProfile() == iscpbridge.ProfileLocalLab && e.config.Role == RoleResponder && notReady {
			return nil
		}
		return errors.New("workbench business payload received before verified manifest")
	}
	s.messageCount++
	if s.messageCount > maxSessionMessages {
		e.mu.Unlock()
		return errors.New("workbench session message limit exceeded")
	}
	raw, err := envelope.Decrypt(e.provider, s.state, env)
	if err != nil {
		e.mu.Unlock()
		return errors.New("workbench payload authentication or replay verification failed")
	}
	if len(raw) > MaxMessageBytes {
		e.mu.Unlock()
		return errors.New("workbench plaintext exceeds limit")
	}
	s.timeReceived = time.Now().UTC()
	sessionCtx := s.ctx
	e.mu.Unlock()
	switch env.PayloadType {
	case manifestType:
		return e.acceptManifest(raw, env.SessionID)
	case pingType, pongType:
		var ping struct {
			Nonce string `json:"nonce"`
		}
		if err := strictDecode(raw, &ping); err != nil || !uuidPattern.MatchString(ping.Nonce) {
			return errors.New("invalid workbench heartbeat")
		}
		if env.PayloadType == pingType {
			return e.sendPayload(ctx, pongType, raw, env.SessionID)
		}
		return nil
	case ResponseType:
		return e.acceptResponse(raw)
	case RequestType:
		return e.acceptRequest(sessionCtx, raw, env.SessionID)
	}
	return errors.New("unsupported workbench business payload")
}

func (e *Endpoint) acceptManifest(raw []byte, id string) error {
	var manifest capabilityManifest
	if err := strictDecode(raw, &manifest); err != nil {
		return errors.New("invalid workbench manifest")
	}
	role := RoleResponder
	if e.config.Role == RoleResponder {
		role = RoleInitiator
	}
	if manifest.Profile != Profile || manifest.Role != role || !slices.Equal(manifest.Operations, Operations()) || manifest.MaxRequestBytes != MaxRequestBytes || manifest.MaxResponseBytes != MaxResponseBytes || manifest.MaxConcurrent != MaxConcurrent {
		return errors.New("incompatible workbench manifest")
	}
	e.mu.Lock()
	if e.session == nil || e.session.id != id || e.session.manifest {
		e.mu.Unlock()
		return errors.New("unexpected or replayed workbench manifest")
	}
	e.session.manifest = true
	e.mu.Unlock()
	e.setState("transport_ready")
	return nil
}

func (e *Endpoint) acceptResponse(raw []byte) error {
	if e.config.Role != RoleInitiator {
		return errors.New("responder cannot receive workbench results")
	}
	var response Response
	if err := strictDecode(raw, &response); err != nil {
		return errors.New("invalid workbench response")
	}
	if err := response.Validate(); err != nil {
		return err
	}
	e.mu.Lock()
	ch := e.pending[response.ID]
	if ch != nil {
		delete(e.pending, response.ID)
		ch <- callResult{response: response}
	}
	e.mu.Unlock()
	// A deadline may expire while a valid response is already in flight.
	return nil
}

func (e *Endpoint) acceptRequest(ctx context.Context, raw []byte, id string) error {
	if e.config.Role != RoleResponder {
		return errors.New("initiator cannot receive workbench invocations")
	}
	var request Request
	if err := strictDecode(raw, &request); err != nil {
		return errors.New("invalid workbench request")
	}
	if err := request.Validate(); err != nil {
		return err
	}
	e.mu.Lock()
	if e.session == nil || e.session.id != id {
		e.mu.Unlock()
		return errors.New("request session was replaced")
	}
	if _, ok := e.session.seenRequests[request.ID]; ok {
		e.mu.Unlock()
		return errors.New("replayed workbench request ID")
	}
	e.session.seenRequests[request.ID] = struct{}{}
	e.mu.Unlock()
	select {
	case e.slots <- struct{}{}:
	default:
		return e.sendResponse(ctx, id, Response{ID: request.ID, Status: 429, Error: "workbench concurrency limit exceeded"})
	}
	e.workers.Add(1)
	go func() {
		defer e.workers.Done()
		defer func() { <-e.slots }()
		callCtx, cancel := context.WithTimeout(ctx, requestTimeout)
		defer cancel()
		if verifyGrant(e.config, e.material, time.Now().UTC()) != nil {
			return
		}
		response := e.handler(callCtx, request)
		response.ID = request.ID
		if callCtx.Err() != nil {
			return
		}
		_ = e.sendResponse(callCtx, id, response)
	}()
	return nil
}

func (e *Endpoint) sendResponse(ctx context.Context, id string, response Response) error {
	response.Type, response.Profile = ResponseType, Profile
	if response.Validate() != nil {
		response.Body = nil
		response.Status = 500
		response.Error = "invalid workbench handler response"
	}
	raw, err := json.Marshal(response)
	if err != nil {
		return errors.New("encode workbench response")
	}
	if len(raw) > MaxResponseBytes {
		response.Body = nil
		response.Status = 413
		response.Error = "workbench result exceeds transport limit"
		raw, _ = json.Marshal(response)
	}
	return e.sendPayload(ctx, ResponseType, raw, id)
}

func (e *Endpoint) Call(ctx context.Context, request Request) (Response, error) {
	if e.config.Role != RoleInitiator {
		return Response{}, errors.New("only the desktop initiator may call workbench operations")
	}
	if err := request.Validate(); err != nil {
		return Response{}, err
	}
	raw, err := encodeRequest(request)
	if err != nil || len(raw) > MaxRequestBytes {
		return Response{}, errors.New("workbench request exceeds transport limit")
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	select {
	case e.slots <- struct{}{}:
	default:
		return Response{}, errors.New("workbench concurrency limit exceeded")
	}
	defer func() { <-e.slots }()
	e.mu.Lock()
	if e.closed || e.session == nil || !e.session.manifest {
		e.mu.Unlock()
		return Response{}, errors.New("workbench transport is not ready")
	}
	if _, exists := e.pending[request.ID]; exists {
		e.mu.Unlock()
		return Response{}, errors.New("duplicate pending workbench call ID")
	}
	id := e.session.id
	ch := make(chan callResult, 1)
	e.pending[request.ID] = ch
	e.mu.Unlock()
	defer func() { e.mu.Lock(); delete(e.pending, request.ID); e.mu.Unlock() }()
	if err := e.sendPayload(ctx, RequestType, raw, id); err != nil {
		e.mu.Lock()
		if e.session != nil && e.session.id == id {
			e.resetLocked()
		}
		e.mu.Unlock()
		e.setState("disconnected")
		return Response{}, err
	}
	select {
	case result := <-ch:
		return result.response, result.err
	case <-ctx.Done():
		// A live Relay cannot establish peer liveness. A lost response requires
		// fresh keys and identity/installation verification before more calls.
		e.mu.Lock()
		if e.session != nil && e.session.id == id {
			e.resetLocked()
		}
		e.mu.Unlock()
		e.setState("disconnected")
		return Response{}, fmt.Errorf("workbench call deadline or cancellation: %w", ctx.Err())
	}
}

func (e *Endpoint) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	if e.cancel != nil {
		e.cancel()
	}
	e.resetLocked()
	e.mu.Unlock()
	e.setState("closed")
	return nil
}

func randomBytes(n int) []byte {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		panic("secure random unavailable")
	}
	return raw
}

func newUUID() string {
	raw := randomBytes(16)
	raw[6] = (raw[6] & 15) | 64
	raw[8] = (raw[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[:4], raw[4:6], raw[6:8], raw[8:10], raw[10:])
}
