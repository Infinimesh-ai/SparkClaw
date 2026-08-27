package iscpbridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpaccess"
	iscpcrypto "github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/envelope"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/payload"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/session"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/trust"
)

const (
	wireFrameType              = "sparkclaw.iscp.relay_frame.v1"
	defaultBridgeRoutePriority = 5
	maxSecureSessions          = 64
)

type relayFrame struct {
	Type              string          `json:"type"`
	DomainID          string          `json:"domain_id"`
	MessageID         string          `json:"message_id"`
	SenderDeviceID    string          `json:"sender_device_id"`
	RecipientDeviceID string          `json:"recipient_device_id"`
	PayloadType       string          `json:"payload_type"`
	Route             envelope.Route  `json:"route"`
	Payload           json.RawMessage `json:"payload"`
}

type secureSession struct {
	mu        sync.Mutex
	peer      PeerAuthorization
	state     *session.State
	createdAt time.Time
	expiresAt time.Time
}

type Service struct {
	config   Config
	provider iscpcrypto.Provider
	device   identity.Device
	gateway  *GatewayClient
	relay    *RelayClient

	mu           sync.RWMutex
	sessions     map[string]*secureSession
	pumps        map[string]context.CancelFunc
	managedPeers map[string]*managedPeer
}

func NewService(config Config, gateway *GatewayClient, relay *RelayClient, device identity.Device) (*Service, error) {
	if gateway == nil || relay == nil {
		return nil, errors.New("Gateway and Relay clients are required")
	}
	if device.Identity.DeviceID == "" {
		return nil, errors.New("device identity is required")
	}
	return &Service{
		config: config, provider: iscpcrypto.NewProvider(), device: device, gateway: gateway, relay: relay,
		sessions: map[string]*secureSession{}, pumps: map[string]context.CancelFunc{},
	}, nil
}

func LoadService(config Config) (*Service, error) {
	now := time.Now().UTC()
	bundle, err := LoadEnrollment(config.EnrollmentFile, now)
	if err != nil {
		return nil, err
	}
	files := config.DeviceFiles()
	device, err := LoadDeviceWithKeyBackend(files.IdentityFile, files.KeyFile, config.IdentityKeyBackend, config.IdentityKeyringService)
	if err != nil {
		return nil, err
	}
	if device.Identity.DomainID != bundle.DomainID || device.Identity.DeviceID != bundle.DeviceID {
		return nil, errors.New("local device identity does not match enrollment bundle")
	}
	token, err := config.LoadGatewayToken()
	if err != nil {
		return nil, err
	}
	gateway, err := NewGatewayClient(GatewayClientOptions{
		BaseURL: config.Gateway.BaseURL, UnixSocket: config.Gateway.UnixSocket,
		Token: token, Timeout: config.GatewayTimeout(),
	})
	if err != nil {
		return nil, err
	}
	relay, err := NewRelayClient(config.Profile, config.EnrollmentFile, bundle, device, config.RelayRequestTimeout())
	if err != nil {
		return nil, err
	}
	return NewService(config, gateway, relay, device)
}

func (s *Service) Run(ctx context.Context) error {
	// Managed bundles flip the session direction: the Bridge holds the grant
	// and initiates toward the responder-only phone (ISCP v0.2).
	if s.managedMode() {
		s.runManagedInitiators(ctx)
		go s.runManagedLifecycle(ctx)
	}
	backoff := time.Duration(s.config.Relay.ReconnectMinSeconds) * time.Second
	maxBackoff := time.Duration(s.config.Relay.ReconnectMaxSeconds) * time.Second
	for {
		err := s.relay.RunOnce(ctx, s.handleRelayMessage)
		if ctx.Err() != nil {
			s.closePumps()
			return ctx.Err()
		}
		if err != nil && strings.Contains(err.Error(), "revoked") {
			s.closePumps()
			return err
		}
		wait := backoff
		if err == nil {
			wait = time.Duration(s.config.Relay.ReconnectMinSeconds) * time.Second
			backoff = wait
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			s.closePumps()
			return ctx.Err()
		case <-timer.C:
		}
		if err != nil {
			backoff = min(backoff*2, maxBackoff)
		}
	}
}

func (s *Service) handleRelayMessage(ctx context.Context, raw json.RawMessage) error {
	if s.managedMode() {
		return s.managedHandleRelayMessage(ctx, raw)
	}
	var metadata struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return errors.New("Relay delivered malformed JSON")
	}
	switch metadata.Type {
	case wireFrameType:
		var frame relayFrame
		if err := strictUnmarshal(raw, &frame); err != nil {
			return errors.New("Relay delivered an invalid session frame")
		}
		return s.handleFrame(ctx, frame)
	case envelope.TypeSecureEnvelope:
		var secureEnvelope envelope.SecureEnvelope
		if err := strictUnmarshal(raw, &secureEnvelope); err != nil {
			return errors.New("Relay delivered an invalid SecureEnvelope")
		}
		return s.handleSecureEnvelope(ctx, secureEnvelope)
	default:
		return errors.New("Relay delivered an unsupported envelope type")
	}
}

func (s *Service) handleFrame(ctx context.Context, frame relayFrame) error {
	bundle := s.relay.Enrollment()
	if frame.Type != wireFrameType || frame.DomainID != bundle.DomainID || frame.RecipientDeviceID != bundle.DeviceID ||
		frame.Route.RelayID != bundle.RelayID || frame.Route.TTLSeconds <= 0 || frame.Route.TTLSeconds > 86400 {
		return errors.New("session frame binding is invalid")
	}
	peer, ok := bundle.Peer(frame.SenderDeviceID)
	if !ok {
		return errors.New("session frame sender is not an authorized peer")
	}
	switch frame.PayloadType {
	case session.TypeHello:
		var hello session.Hello
		if err := strictUnmarshal(frame.Payload, &hello); err != nil {
			return errors.New("invalid Session Hello")
		}
		return s.acceptHello(ctx, frame, peer, hello)
	case session.TypeReady:
		var ready session.Ready
		if err := strictUnmarshal(frame.Payload, &ready); err != nil {
			return errors.New("invalid Session Ready")
		}
		return s.acceptReady(ctx, frame, peer, ready)
	default:
		return errors.New("unsupported session frame payload")
	}
}

func (s *Service) acceptHello(ctx context.Context, frame relayFrame, peer PeerAuthorization, remote session.Hello) error {
	bundle := s.relay.Enrollment()
	if remote.DeviceID != frame.SenderDeviceID || remote.PeerDeviceID != bundle.DeviceID || remote.DomainID != bundle.DomainID {
		return errors.New("Session Hello route binding is invalid")
	}
	now := time.Now().UTC()
	if remote.IssuedAt.Before(now.Add(-2*time.Minute)) || remote.IssuedAt.After(now.Add(2*time.Minute)) {
		return errors.New("Session Hello is outside the allowed time window")
	}
	if _, exists := s.session(remote.SessionID); exists {
		return errors.New("Session Hello reuses an existing session ID")
	}
	if remote.GrantID != peer.InboundGrant.GrantID || peer.InboundGrant.Issuer != bundle.TrustRootIdentity.DeviceID {
		return errors.New("Session Hello grant binding is invalid")
	}
	thumbprint, err := identity.Thumbprint(peer.Identity)
	if err != nil {
		return errors.New("peer identity thumbprint is invalid")
	}
	if err := trust.VerifyGrant(s.provider, peer.InboundGrant, bundle.TrustRootIdentity, trust.VerifyOptions{
		Audience: bundle.DeviceID, SubjectDeviceID: peer.Identity.DeviceID,
		ConfirmationThumbprint: thumbprint, Permission: s.config.Permission,
		RelayID: bundle.RelayID, CurrentRevocationEpoch: peer.InboundRevocationEpoch, Now: now,
	}); err != nil {
		return errors.New("peer Trust Grant verification failed")
	}
	localThumbprint, err := identity.Thumbprint(s.device.Identity)
	if err != nil {
		return errors.New("local identity thumbprint is invalid")
	}
	if peer.OutboundGrant.Issuer != bundle.TrustRootIdentity.DeviceID {
		return errors.New("local Trust Grant issuer is invalid")
	}
	if err := trust.VerifyGrant(s.provider, peer.OutboundGrant, bundle.TrustRootIdentity, trust.VerifyOptions{
		Audience: peer.Identity.DeviceID, SubjectDeviceID: bundle.DeviceID,
		ConfirmationThumbprint: localThumbprint, Permission: s.config.Permission,
		RelayID: bundle.RelayID, CurrentRevocationEpoch: peer.OutboundRevocationEpoch, Now: now,
	}); err != nil {
		return errors.New("local Trust Grant verification failed")
	}
	if err := session.VerifyHello(s.provider, remote, peer.Identity); err != nil {
		return errors.New("peer Session Hello verification failed")
	}
	localHello, err := session.CreateHello(s.provider, s.device, remote.SessionID, peer.Identity.DeviceID, peer.OutboundGrant.GrantID, time.Now().UTC())
	if err != nil {
		return errors.New("create local Session Hello")
	}
	state, err := session.Establish(s.provider, localHello, remote, s.device.Identity, peer.Identity)
	if err != nil {
		return errors.New("establish ISCP session")
	}
	ready, err := state.CreateReady(s.provider, s.device)
	if err != nil {
		return errors.New("create local Session Ready")
	}
	secured := &secureSession{
		peer: peer, state: state, createdAt: now,
		expiresAt: earlierTime(peer.InboundGrant.ExpiresAt, peer.OutboundGrant.ExpiresAt),
	}
	s.storeSession(remote.SessionID, secured)
	if err := s.sendFrame(ctx, peer.Identity.DeviceID, session.TypeHello, localHello.Hello); err != nil {
		s.removeSession(remote.SessionID, secured)
		return err
	}
	if err := s.sendFrame(ctx, peer.Identity.DeviceID, session.TypeReady, ready); err != nil {
		s.removeSession(remote.SessionID, secured)
		return err
	}
	return nil
}

func (s *Service) acceptReady(ctx context.Context, frame relayFrame, peer PeerAuthorization, ready session.Ready) error {
	secured, ok := s.session(ready.SessionID)
	if !ok || secured.peer.Identity.DeviceID != peer.Identity.DeviceID || ready.DeviceID != frame.SenderDeviceID {
		return errors.New("Session Ready has no matching pending session")
	}
	secured.mu.Lock()
	if secured.state.Ready() {
		secured.mu.Unlock()
		return errors.New("Session Ready was already accepted")
	}
	err := secured.state.VerifyReady(s.provider, ready, peer.Identity)
	secured.mu.Unlock()
	if err != nil {
		return errors.New("peer Session Ready verification failed")
	}
	request := Request{
		ProtocolVersion: ProtocolVersion, Type: TypeCapabilitiesDescribe,
		RequestID: "session-ready-" + ready.SessionID, EndpointID: peer.Identity.DeviceID,
		IssuedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(5 * time.Minute),
	}
	response, gatewayErr := s.gateway.Dispatch(ctx, request)
	if gatewayErr != nil && response.Type == "" {
		response = newResponse(request, "error", nil, nil, bridgeError(CodeTemporarilyUnavailable, "Gateway is unavailable", true), time.Now().UTC())
	}
	return s.sendSecure(ctx, secured, payload.TypeTaskResult, response)
}

func (s *Service) handleSecureEnvelope(ctx context.Context, secureEnvelope envelope.SecureEnvelope) error {
	secured, ok := s.session(secureEnvelope.SessionID)
	if !ok {
		return errors.New("SecureEnvelope session is unknown")
	}
	secured.mu.Lock()
	if !secured.state.Ready() {
		secured.mu.Unlock()
		return errors.New("SecureEnvelope session is not ready")
	}
	plaintext, err := envelope.Decrypt(s.provider, secured.state, secureEnvelope)
	secured.mu.Unlock()
	if err != nil {
		return errors.New("SecureEnvelope verification failed")
	}
	if secureEnvelope.PayloadType != payload.TypeTaskInvoke {
		return errors.New("SecureEnvelope payload type is unsupported")
	}
	var protocol struct {
		ProtocolVersion string `json:"protocol_version"`
	}
	if err := json.Unmarshal(plaintext, &protocol); err != nil {
		return errors.New("SecureEnvelope contains an invalid request")
	}
	if protocol.ProtocolVersion == mcpaccess.TransportProtocolVersion {
		var request mcpaccess.TransportRequest
		if err := strictUnmarshal(plaintext, &request); err != nil {
			return errors.New("SecureEnvelope contains an invalid MCP request")
		}
		thumbprint, err := identity.Thumbprint(secured.peer.Identity)
		if err != nil {
			return errors.New("authenticated peer thumbprint is invalid")
		}
		response, gatewayErr := s.gateway.DispatchMCP(ctx, mcpaccess.PeerRequest{
			Peer:    app.MCPPeerIdentity{DomainID: secureEnvelope.DomainID, DeviceID: secured.peer.Identity.DeviceID, KeyThumbprint: thumbprint, ISCPSessionID: secureEnvelope.SessionID},
			Request: request,
		})
		if gatewayErr != nil {
			return errors.New("Gateway MCP service is unavailable")
		}
		return s.sendSecure(ctx, secured, payload.TypeTaskResult, response)
	}
	var request Request
	if err := strictUnmarshal(plaintext, &request); err != nil {
		return errors.New("SecureEnvelope contains an invalid agent request")
	}
	if request.EndpointID != secured.peer.Identity.DeviceID {
		return errors.New("agent request endpoint does not match the authenticated peer")
	}
	response, gatewayErr := s.gateway.Dispatch(ctx, request)
	if gatewayErr != nil && response.Type == "" {
		response = newResponse(request, "error", nil, nil, bridgeError(CodeTemporarilyUnavailable, "Gateway is unavailable", true), time.Now().UTC())
	}
	if err := s.sendSecure(ctx, secured, payload.TypeTaskResult, response); err != nil {
		return err
	}
	if request.Type == TypeMessageSend && response.Operation != nil {
		s.startOperationPump(ctx, secured, request, *response.Operation)
	}
	return nil
}

func (s *Service) sendFrame(ctx context.Context, peerDeviceID, payloadType string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return errors.New("encode session frame")
	}
	bundle := s.relay.Enrollment()
	frame := relayFrame{
		Type: wireFrameType, DomainID: bundle.DomainID, MessageID: newWireID("frame"),
		SenderDeviceID: bundle.DeviceID, RecipientDeviceID: peerDeviceID, PayloadType: payloadType,
		Route:   envelope.Route{RelayID: bundle.RelayID, TTLSeconds: s.config.Relay.EnvelopeTTLSeconds, Priority: defaultBridgeRoutePriority},
		Payload: raw,
	}
	return s.relay.Submit(ctx, frame)
}

func (s *Service) sendSecure(ctx context.Context, secured *secureSession, payloadType string, value any) error {
	plaintext, err := json.Marshal(value)
	if err != nil {
		return errors.New("encode secure Bridge payload")
	}
	bundle := s.relay.Enrollment()
	secured.mu.Lock()
	secureEnvelope, err := envelope.Encrypt(s.provider, secured.state, newWireID("message"), payloadType, envelope.Route{
		RelayID: bundle.RelayID, TTLSeconds: s.config.Relay.EnvelopeTTLSeconds, Priority: defaultBridgeRoutePriority,
	}, plaintext)
	secured.mu.Unlock()
	if err != nil {
		return errors.New("encrypt Bridge payload")
	}
	return s.relay.Submit(ctx, secureEnvelope)
}

func (s *Service) startOperationPump(parent context.Context, secured *secureSession, original Request, operation Operation) {
	key := secured.state.SessionID + "\x00" + operation.ID
	s.mu.Lock()
	if _, ok := s.pumps[key]; ok {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	s.pumps[key] = cancel
	s.mu.Unlock()
	go s.pumpOperation(ctx, key, secured, original, operation)
}

func (s *Service) pumpOperation(ctx context.Context, key string, secured *secureSession, original Request, operation Operation) {
	defer func() {
		s.mu.Lock()
		delete(s.pumps, key)
		s.mu.Unlock()
	}()
	ticker := time.NewTicker(s.config.EventPollInterval())
	defer ticker.Stop()
	cursor := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		now := time.Now().UTC()
		resumePayload, _ := json.Marshal(EventResumePayload{Cursor: cursor, Limit: 200})
		resumeRequest := Request{
			ProtocolVersion: ProtocolVersion, Type: TypeEventResume,
			RequestID: newWireID("event-resume"), EndpointID: original.EndpointID,
			SessionID: operation.SessionID, IssuedAt: now, ExpiresAt: now.Add(2 * time.Minute), Payload: resumePayload,
		}
		resumeResponse, err := s.gateway.Dispatch(ctx, resumeRequest)
		if err == nil {
			var resumed struct {
				Events []Event `json:"events"`
			}
			if decodeResult(resumeResponse.Result, &resumed) == nil {
				for _, event := range resumed.Events {
					if sendErr := s.sendSecure(ctx, secured, payload.TypeTaskResult, event); sendErr != nil {
						return
					}
					cursor = event.Cursor
				}
			}
		}
		statusPayload, _ := json.Marshal(OperationStatusPayload{OperationID: operation.ID})
		statusRequest := Request{
			ProtocolVersion: ProtocolVersion, Type: TypeOperationStatus,
			RequestID: newWireID("operation-status"), EndpointID: original.EndpointID,
			SessionID: operation.SessionID, IssuedAt: now, ExpiresAt: now.Add(2 * time.Minute), Payload: statusPayload,
		}
		statusResponse, statusErr := s.gateway.Dispatch(ctx, statusRequest)
		if statusErr != nil || statusResponse.Operation == nil {
			continue
		}
		state := statusResponse.Operation.State
		if state == "completed" || state == "failed" || state == "cancelled" || state == "approval_required" || state == "unknown" {
			event := Event{
				ProtocolVersion: ProtocolVersion, Type: TypeEvent, EndpointID: original.EndpointID,
				SessionID: operation.SessionID, OperationID: operation.ID,
				Cursor: operation.ID + ":" + state, EventType: "operation." + state,
				Payload: *statusResponse.Operation, IssuedAt: time.Now().UTC(),
			}
			_ = s.sendSecure(ctx, secured, payload.TypeTaskResult, event)
			return
		}
	}
}

func (s *Service) session(id string) (*secureSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	secured, ok := s.sessions[id]
	if ok && !time.Now().UTC().Before(secured.expiresAt) {
		delete(s.sessions, id)
		s.cancelSessionPumpsLocked(id)
		return nil, false
	}
	return secured, ok
}

func (s *Service) storeSession(id string, secured *secureSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	for sessionID, existing := range s.sessions {
		if !now.Before(existing.expiresAt) {
			delete(s.sessions, sessionID)
			s.cancelSessionPumpsLocked(sessionID)
		}
	}
	if len(s.sessions) >= maxSecureSessions {
		var oldestID string
		var oldest time.Time
		for sessionID, existing := range s.sessions {
			if oldestID == "" || existing.createdAt.Before(oldest) {
				oldestID = sessionID
				oldest = existing.createdAt
			}
		}
		delete(s.sessions, oldestID)
		s.cancelSessionPumpsLocked(oldestID)
	}
	s.sessions[id] = secured
}

func (s *Service) removeSession(id string, expected *secureSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if current, ok := s.sessions[id]; ok && current == expected {
		delete(s.sessions, id)
		s.cancelSessionPumpsLocked(id)
	}
}

func (s *Service) cancelSessionPumpsLocked(sessionID string) {
	prefix := sessionID + "\x00"
	for key, cancel := range s.pumps {
		if strings.HasPrefix(key, prefix) {
			cancel()
			delete(s.pumps, key)
		}
	}
}

func (s *Service) closePumps() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, cancel := range s.pumps {
		cancel()
		delete(s.pumps, key)
	}
}

func strictUnmarshal(raw []byte, dst any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}

func decodeResult(value any, dst any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}

func newWireID(prefix string) string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UTC().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(raw)
}

func earlierTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
