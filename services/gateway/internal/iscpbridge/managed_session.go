// Managed session layer (ISCP v0.2 spec/session.md + the JingSi/happy fusion
// contract): the Bridge holds the Trust Grant and therefore INITIATES the
// session toward the responder-only phone. Handshake objects travel in
// envelope-shaped messages (payload_type = handshake object type, ciphertext
// = base64url(JSON), sequence 0, random nonce — NOT AEAD; authenticity is the
// object's own signature). The first business payload after ready is the
// agent.capability.v1 manifest, and business payloads are refused until the
// peer's manifest arrives. Inbound iscp.session.reopen.v1 control frames
// (verified through the v0.2 SDK ladder) trigger a fresh handshake.
package iscpbridge

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/Infinimesh-ai/ISCP/pkg/iscp/envelope"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/session"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/trust"
)

const (
	manifestPayloadType     = "agent.capability.v1"
	sparkclawBridgeProtocol = "sparkclaw.bridge.v1"
	handshakeTTLSeconds     = 75
	reopenReplayWindow      = 64
	initiateMinBackoff      = 5 * time.Second
	initiateMaxBackoff      = 60 * time.Second
)

// bridgeManifest is the capability manifest announced to the phone,
// mirroring the wire shape of happy's agent.capability.v1
// (JingSi docs/sparkclaw-iscp-bridge-requirements.md registration metadata).
func bridgeManifest() map[string]any {
	capabilityIDs := []string{
		"agent.sessions", "agent.conversation", "agent.streaming",
		"agent.activities", "agent.snapshot", "agent.approvals",
		"agent.notifications", "agent.operations",
	}
	capabilities := make([]map[string]any, 0, len(capabilityIDs))
	for _, id := range capabilityIDs {
		capabilities = append(capabilities, map[string]any{"id": id, "version": 1})
	}
	return map[string]any{
		"product_kind":      "sparkclaw",
		"device_type":       "agent_runtime",
		"device_role":       "owner_runtime",
		"runtime_kind":      "sparkclaw",
		"protocol_versions": []string{manifestPayloadType, sparkclawBridgeProtocol},
		"capabilities":      capabilities,
	}
}

type managedPeer struct {
	mu           sync.Mutex
	identity     identity.DeviceIdentity
	grant        trust.Grant
	sessionID    string
	local        session.LocalHello
	state        *session.State
	manifestSent bool
	peerManifest json.RawMessage
	// stale session ids tombstoned by replace/reopen so late relay replies
	// cannot resurrect them (bounded).
	stale []string
	// seen reopen request ids (bounded LRU): repeats coalesce silently.
	reopenSeen []string
	reinit     chan struct{}
}

func (p *managedPeer) markStale(sessionID string) {
	if sessionID == "" {
		return
	}
	p.stale = append(p.stale, sessionID)
	if len(p.stale) > 32 {
		p.stale = p.stale[len(p.stale)-32:]
	}
}

func (p *managedPeer) isStale(sessionID string) bool {
	for _, id := range p.stale {
		if id == sessionID {
			return true
		}
	}
	return false
}

func (p *managedPeer) reopenCoalesced(requestID string) bool {
	for _, id := range p.reopenSeen {
		if id == requestID {
			return true
		}
	}
	p.reopenSeen = append(p.reopenSeen, requestID)
	if len(p.reopenSeen) > reopenReplayWindow {
		p.reopenSeen = p.reopenSeen[len(p.reopenSeen)-reopenReplayWindow:]
	}
	return false
}

func (p *managedPeer) signalReinitiate() {
	select {
	case p.reinit <- struct{}{}:
	default:
	}
}

func (s *Service) managedMode() bool {
	return s.relay.Enrollment().Mode == BundleModeManaged
}

func (s *Service) managedPeerFor(deviceID string) *managedPeer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.managedPeers[deviceID]
}

func (s *Service) initManagedPeers() {
	bundle := s.relay.Enrollment()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.managedPeers == nil {
		s.managedPeers = map[string]*managedPeer{}
	}
	for _, peer := range bundle.Peers {
		if _, ok := s.managedPeers[peer.Identity.DeviceID]; !ok {
			s.managedPeers[peer.Identity.DeviceID] = &managedPeer{
				identity: peer.Identity,
				grant:    peer.OutboundGrant,
				reinit:   make(chan struct{}, 1),
			}
		}
	}
}

// runManagedInitiators keeps one session initiator loop per authorized peer:
// initiate when there is no ready session, back off on failure
// (5s → 60s, matching the happy daemon's initiator ladder), and re-initiate
// immediately on a verified reopen request.
func (s *Service) runManagedInitiators(ctx context.Context) {
	s.initManagedPeers()
	s.mu.RLock()
	peers := make([]*managedPeer, 0, len(s.managedPeers))
	for _, peer := range s.managedPeers {
		peers = append(peers, peer)
	}
	s.mu.RUnlock()
	for _, peer := range peers {
		go s.runManagedInitiator(ctx, peer)
	}
}

func (s *Service) runManagedInitiator(ctx context.Context, peer *managedPeer) {
	backoff := initiateMinBackoff
	for {
		peer.mu.Lock()
		ready := peer.state != nil && peer.state.Ready()
		grantExpired := !time.Now().UTC().Before(peer.grant.ExpiresAt)
		peer.mu.Unlock()
		if grantExpired {
			// authorization_expired is terminal until renewal lands; keep the
			// loop alive so a rotated bundle can resume without a restart.
			select {
			case <-ctx.Done():
				return
			case <-time.After(initiateMaxBackoff):
			}
			continue
		}
		if !ready {
			if err := s.initiateManagedSession(ctx, peer); err != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(backoff):
				case <-peer.reinit:
				}
				backoff = min(backoff*2, initiateMaxBackoff)
				continue
			}
			backoff = initiateMinBackoff
		}
		// Session pending or ready: wait for a reopen signal or re-check
		// periodically (covers a hello that never got answered).
		select {
		case <-ctx.Done():
			return
		case <-peer.reinit:
		case <-time.After(handshakeTTLSeconds * time.Second):
		}
	}
}

func (s *Service) initiateManagedSession(ctx context.Context, peer *managedPeer) error {
	bundle := s.relay.Enrollment()
	now := time.Now().UTC()
	localThumbprint, err := identity.Thumbprint(s.device.Identity)
	if err != nil {
		return errors.New("local identity thumbprint is invalid")
	}
	permission := ""
	if len(peer.grant.Permissions) > 0 {
		permission = peer.grant.Permissions[0]
	}
	// Our grant authorizes this session; verify before every initiation so a
	// revoked or expired grant fails closed here, not mid-handshake.
	if err := trust.VerifyGrant(s.provider, peer.grant, bundle.TrustRootIdentity, trust.VerifyOptions{
		Audience:               peer.identity.DeviceID,
		SubjectDeviceID:        bundle.DeviceID,
		ConfirmationThumbprint: localThumbprint,
		Permission:             permission,
		RelayID:                bundle.RelayID,
		Now:                    now,
	}); err != nil {
		return errors.New("outbound Trust Grant verification failed")
	}
	sessionID := newWireID("s")
	local, err := session.CreateHello(s.provider, s.device, sessionID, peer.identity.DeviceID, peer.grant.GrantID, now)
	if err != nil {
		return errors.New("create Session Hello")
	}
	peer.mu.Lock()
	if peer.sessionID != "" {
		peer.markStale(peer.sessionID)
	}
	peer.sessionID = sessionID
	peer.local = local
	peer.state = nil
	peer.manifestSent = false
	peer.peerManifest = nil
	peer.mu.Unlock()
	return s.submitHandshake(ctx, peer.identity.DeviceID, sessionID, session.TypeHello, local.Hello)
}

// submitHandshake sends a handshake object in envelope shape: payload_type is
// the object type, ciphertext is base64url of its JSON — no AEAD.
func (s *Service) submitHandshake(ctx context.Context, peerDeviceID, sessionID, payloadType string, object any) error {
	raw, err := json.Marshal(object)
	if err != nil {
		return errors.New("encode handshake object")
	}
	bundle := s.relay.Enrollment()
	ttl := min(s.config.Relay.EnvelopeTTLSeconds, handshakeTTLSeconds)
	if ttl <= 0 {
		ttl = handshakeTTLSeconds
	}
	return s.relay.Submit(ctx, envelope.SecureEnvelope{
		Type:              envelope.TypeSecureEnvelope,
		DomainID:          bundle.DomainID,
		MessageID:         newWireID("hs"),
		SessionID:         sessionID,
		SenderDeviceID:    bundle.DeviceID,
		RecipientDeviceID: peerDeviceID,
		Sequence:          0,
		Nonce:             base64.RawURLEncoding.EncodeToString(randomBytesN(12)),
		PayloadType:       payloadType,
		Route:             envelope.Route{RelayID: bundle.RelayID, TTLSeconds: ttl, Priority: defaultBridgeRoutePriority},
		Ciphertext:        base64.RawURLEncoding.EncodeToString(raw),
	})
}

// managedHandleRelayMessage dispatches every inbound managed envelope: the
// envelope-shaped handshake and control payload types are intercepted;
// AEAD business envelopes flow to the shared dispatch (manifest-gated).
func (s *Service) managedHandleRelayMessage(ctx context.Context, raw json.RawMessage) error {
	var secureEnvelope envelope.SecureEnvelope
	if err := strictUnmarshal(raw, &secureEnvelope); err != nil {
		return errors.New("Relay delivered an invalid SecureEnvelope")
	}
	bundle := s.relay.Enrollment()
	if secureEnvelope.Type != envelope.TypeSecureEnvelope ||
		secureEnvelope.DomainID != bundle.DomainID ||
		secureEnvelope.RecipientDeviceID != bundle.DeviceID ||
		secureEnvelope.Route.RelayID != bundle.RelayID {
		return errors.New("envelope binding is invalid")
	}
	peer := s.managedPeerFor(secureEnvelope.SenderDeviceID)
	if peer == nil {
		return errors.New("envelope sender is not an authorized peer")
	}
	switch secureEnvelope.PayloadType {
	case session.TypeHello:
		return s.managedHandleHello(ctx, peer, secureEnvelope)
	case session.TypeReady:
		return s.managedHandleReady(ctx, peer, secureEnvelope)
	case session.TypeReopen:
		return s.managedHandleReopen(ctx, peer, secureEnvelope)
	case session.TypeClose:
		return s.managedHandleClose(peer, secureEnvelope)
	default:
		return s.managedHandleBusiness(ctx, peer, secureEnvelope)
	}
}

func decodeHandshakePayload(ciphertext string, dst any) error {
	raw, err := base64.RawURLEncoding.DecodeString(ciphertext)
	if err != nil {
		return errors.New("handshake payload is not base64url")
	}
	return strictUnmarshal(raw, dst)
}

// managedHandleHello processes the responder phone's answering hello (and
// applies the normative tie-break should a competing session ever appear).
func (s *Service) managedHandleHello(ctx context.Context, peer *managedPeer, env envelope.SecureEnvelope) error {
	var hello session.Hello
	if err := decodeHandshakePayload(env.Ciphertext, &hello); err != nil {
		return errors.New("invalid Session Hello")
	}
	bundle := s.relay.Enrollment()
	if hello.DeviceID != env.SenderDeviceID || hello.PeerDeviceID != bundle.DeviceID || hello.DomainID != bundle.DomainID {
		return errors.New("Session Hello binding is invalid")
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	if peer.isStale(hello.SessionID) {
		return nil // late reply to an abandoned session
	}
	if peer.sessionID != hello.SessionID {
		// Competing session (normative tie-break, spec/session.md): an
		// established session wins; between in-progress sessions the
		// initiator with the lower device id wins. The phone is
		// responder-only, so a differing id here means it answered an older
		// hello of ours — treat it as stale.
		if peer.state != nil && peer.state.Ready() {
			return nil
		}
		peer.markStale(hello.SessionID)
		return nil
	}
	if peer.state != nil {
		return nil // duplicate hello
	}
	// The responder-only phone carries no grant of its own (grant_id "");
	// authorization is OUR grant, verified at initiation. The hello's
	// signature is verified against the enrolled phone identity inside
	// Establish.
	state, err := session.Establish(s.provider, peer.local, hello, s.device.Identity, peer.identity)
	if err != nil {
		return errors.New("establish ISCP session")
	}
	ready, err := state.CreateReady(s.provider, s.device)
	if err != nil {
		return errors.New("create Session Ready")
	}
	peer.state = state
	return s.submitHandshake(ctx, peer.identity.DeviceID, peer.sessionID, session.TypeReady, ready)
}

func (s *Service) managedHandleReady(ctx context.Context, peer *managedPeer, env envelope.SecureEnvelope) error {
	var ready session.Ready
	if err := decodeHandshakePayload(env.Ciphertext, &ready); err != nil {
		return errors.New("invalid Session Ready")
	}
	peer.mu.Lock()
	if peer.isStale(ready.SessionID) || ready.SessionID != peer.sessionID {
		peer.mu.Unlock()
		return nil
	}
	if peer.state == nil {
		peer.mu.Unlock()
		return errors.New("Session Ready received before hello exchange")
	}
	if peer.state.Ready() {
		peer.mu.Unlock()
		return nil // duplicate ready
	}
	if err := peer.state.VerifyReady(s.provider, ready, peer.identity); err != nil {
		peer.mu.Unlock()
		return errors.New("Session Ready verification failed")
	}
	state := peer.state
	sendManifest := !peer.manifestSent
	peer.manifestSent = true
	peer.mu.Unlock()

	// Register the ready session with the shared business machinery
	// (dispatch, operation pumps) under the session id.
	s.storeSession(peer.sessionID, &secureSession{
		peer: PeerAuthorization{
			Identity:      peer.identity,
			OutboundGrant: peer.grant,
		},
		state:     state,
		createdAt: time.Now().UTC(),
		expiresAt: peer.grant.ExpiresAt,
	})
	if sendManifest {
		return s.sendManagedPayload(ctx, peer, manifestPayloadType, bridgeManifest())
	}
	return nil
}

// managedHandleReopen validates the phone's authenticated reopen request
// through the v0.2 SDK ladder plus the envelope bindings, then tombstones
// the current session and re-initiates immediately.
func (s *Service) managedHandleReopen(_ context.Context, peer *managedPeer, env envelope.SecureEnvelope) error {
	raw, err := base64.RawURLEncoding.DecodeString(env.Ciphertext)
	if err != nil {
		return errors.New("reopen payload is not base64url")
	}
	var request session.Reopen
	if err := strictUnmarshal(raw, &request); err != nil {
		return errors.New("invalid session reopen request")
	}
	bundle := s.relay.Enrollment()
	// Envelope binding: sender/recipient/session_id/route must match the
	// signed request (spec/session.md receiver ladder).
	if env.SenderDeviceID != request.DeviceID || env.RecipientDeviceID != request.PeerDeviceID ||
		env.SessionID != request.RequestID || env.Route.RelayID != request.RelayID ||
		env.Route.TTLSeconds <= 0 || env.Route.TTLSeconds > 30 {
		return errors.New("session reopen envelope binding mismatch")
	}
	// Grant authority: our grant must authorize sessions with the requester.
	now := time.Now().UTC()
	localThumbprint, err := identity.Thumbprint(s.device.Identity)
	if err != nil {
		return errors.New("local identity thumbprint is invalid")
	}
	permission := ""
	if len(peer.grant.Permissions) > 0 {
		permission = peer.grant.Permissions[0]
	}
	if err := trust.VerifyGrant(s.provider, peer.grant, bundle.TrustRootIdentity, trust.VerifyOptions{
		Audience:               request.DeviceID,
		SubjectDeviceID:        bundle.DeviceID,
		ConfirmationThumbprint: localThumbprint,
		Permission:             permission,
		RelayID:                request.RelayID,
		Now:                    now,
	}); err != nil {
		return errors.New("session reopen is not authorized by the current grant")
	}
	if err := session.VerifyReopen(s.provider, request, peer.identity, session.ReopenVerifyOptions{
		LocalDeviceID: bundle.DeviceID,
		DomainID:      bundle.DomainID,
		RelayID:       bundle.RelayID,
		Now:           now,
	}); err != nil {
		return errors.New("session reopen verification failed")
	}
	peer.mu.Lock()
	if peer.reopenCoalesced(request.RequestID) {
		peer.mu.Unlock()
		return nil // duplicate request: coalesce silently
	}
	stale := peer.sessionID
	peer.markStale(stale)
	peer.sessionID = ""
	peer.state = nil
	peer.peerManifest = nil
	peer.mu.Unlock()
	if stale != "" {
		s.mu.Lock()
		if secured, ok := s.sessions[stale]; ok {
			delete(s.sessions, stale)
			_ = secured
			s.cancelSessionPumpsLocked(stale)
		}
		s.mu.Unlock()
	}
	peer.signalReinitiate()
	return nil
}

// managedHandleClose tears down the named session on a verified deliberate
// close (iscp.session.close.v1, optional in v0.2).
func (s *Service) managedHandleClose(peer *managedPeer, env envelope.SecureEnvelope) error {
	raw, err := base64.RawURLEncoding.DecodeString(env.Ciphertext)
	if err != nil {
		return errors.New("close payload is not base64url")
	}
	var frame session.Close
	if err := strictUnmarshal(raw, &frame); err != nil {
		return errors.New("invalid session close frame")
	}
	bundle := s.relay.Enrollment()
	if err := session.VerifyClose(s.provider, frame, peer.identity, session.ReopenVerifyOptions{
		LocalDeviceID: bundle.DeviceID,
		DomainID:      bundle.DomainID,
		RelayID:       bundle.RelayID,
		Now:           time.Now().UTC(),
	}); err != nil {
		return errors.New("session close verification failed")
	}
	peer.mu.Lock()
	if frame.SessionID != peer.sessionID {
		peer.mu.Unlock()
		return nil
	}
	peer.markStale(peer.sessionID)
	peer.sessionID = ""
	peer.state = nil
	peer.peerManifest = nil
	peer.mu.Unlock()
	s.mu.Lock()
	delete(s.sessions, frame.SessionID)
	s.cancelSessionPumpsLocked(frame.SessionID)
	s.mu.Unlock()
	return nil
}

// managedHandleBusiness gates AEAD envelopes on the exchanged manifest, then
// hands them to the shared agent dispatch.
func (s *Service) managedHandleBusiness(ctx context.Context, peer *managedPeer, env envelope.SecureEnvelope) error {
	if strings.HasPrefix(env.PayloadType, "iscp.session.") {
		return errors.New("handshake payload types are reserved")
	}
	if env.PayloadType == manifestPayloadType {
		secured, ok := s.session(env.SessionID)
		if !ok {
			return errors.New("manifest session is unknown")
		}
		secured.mu.Lock()
		plaintext, err := envelope.Decrypt(s.provider, secured.state, env)
		secured.mu.Unlock()
		if err != nil {
			return errors.New("manifest envelope verification failed")
		}
		peer.mu.Lock()
		peer.peerManifest = plaintext
		peer.mu.Unlock()
		return nil
	}
	peer.mu.Lock()
	gated := peer.peerManifest == nil
	peer.mu.Unlock()
	if gated {
		return errors.New("business payload received before capability manifest")
	}
	return s.handleSecureEnvelope(ctx, env)
}

// sendManagedPayload encrypts and submits one business payload on the
// peer's current ready session.
func (s *Service) sendManagedPayload(ctx context.Context, peer *managedPeer, payloadType string, value any) error {
	peer.mu.Lock()
	sessionID := peer.sessionID
	peer.mu.Unlock()
	secured, ok := s.session(sessionID)
	if !ok {
		return errors.New("no ready session for peer")
	}
	return s.sendSecure(ctx, secured, payloadType, value)
}

func randomBytesN(n int) []byte {
	raw := make([]byte, n)
	if _, err := cryptorand.Read(raw); err != nil {
		panic(err)
	}
	return raw
}
