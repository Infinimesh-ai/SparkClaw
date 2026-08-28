package iscpbridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	iscpcrypto "github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/envelope"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/session"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/trust"
)

// managedHarness simulates the managed deployment: a relay endpoint that
// records submitted envelopes and a loopback gateway, with the phone peer
// driven directly through the ISCP SDK exactly as the JingSi responder
// behaves (no grant of its own; it answers a grant-authorized hello).
type managedHarness struct {
	t           *testing.T
	provider    iscpcrypto.Provider
	service     *Service
	bridge      identity.Device
	phone       identity.Device
	trustSigner identity.Device
	grant       trust.Grant
	// lifecycleHandler lets lifecycle tests plug the optional device
	// lifecycle endpoints into the relay stub; return true when handled.
	lifecycleHandler func(http.ResponseWriter, *http.Request) bool

	mu        sync.Mutex
	submitted []envelope.SecureEnvelope
}

func newManagedHarness(t *testing.T) *managedHarness {
	t.Helper()
	provider := iscpcrypto.NewProvider()
	now := time.Now().UTC()
	trustSigner, err := identity.NewDevice(provider, "infinimesh-cloud", "trust-root-test", now)
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := identity.NewDevice(provider, "dom_test", "dev_bridge", now)
	if err != nil {
		t.Fatal(err)
	}
	phone, err := identity.NewDevice(provider, "dom_test", "dev_phone", now)
	if err != nil {
		t.Fatal(err)
	}
	bridgeThumb, err := identity.Thumbprint(bridge.Identity)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := trust.SignGrant(provider, trustSigner, trust.Grant{
		GrantID:                "grant_test_1",
		SubjectDeviceID:        bridge.Identity.DeviceID,
		Audience:               phone.Identity.DeviceID,
		ConfirmationThumbprint: bridgeThumb,
		Permissions:            []string{"conversation"},
		RelayConstraints:       []string{"relay-test"},
		NotBefore:              now.Add(-time.Minute),
		ExpiresAt:              now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}

	h := &managedHarness{t: t, provider: provider, bridge: bridge, phone: phone, trustSigner: trustSigner, grant: grant}

	relayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.lifecycleHandler != nil && h.lifecycleHandler(w, r) {
			return
		}
		if r.URL.Path == relayEnvelopePath {
			var env envelope.SecureEnvelope
			if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			h.mu.Lock()
			h.submitted = append(h.submitted, env)
			h.mu.Unlock()
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(relayServer.Close)

	gatewayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request Request
		_ = json.NewDecoder(r.Body).Decode(&request)
		response := newResponse(request, "ok", map[string]any{"echo": string(request.Type)}, nil, nil, time.Now().UTC())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(gatewayServer.Close)

	bundle := EnrollmentBundle{
		Type:              EnrollmentBundleType,
		Mode:              BundleModeManaged,
		DomainID:          "dom_test",
		DeviceID:          bridge.Identity.DeviceID,
		RelayID:           "relay-test",
		RelayBaseURL:      relayServer.URL,
		RelayWebSocketURL: "ws" + strings.TrimPrefix(relayServer.URL, "http") + "/v2/relay/connect",
		TrustRootIdentity: trustSigner.Identity,
		Access:            RelayCredential{DomainID: "dom_test", DeviceID: bridge.Identity.DeviceID, Token: "tok_a", ExpiresAt: now.Add(time.Hour)},
		Refresh:           RelayCredential{DomainID: "dom_test", DeviceID: bridge.Identity.DeviceID, Token: "tok_r", ExpiresAt: now.Add(24 * time.Hour)},
		Peers: []PeerAuthorization{{
			Identity:      phone.Identity,
			OutboundGrant: grant,
		}},
		IssuedAt:  now,
		ExpiresAt: grant.ExpiresAt,
	}
	if err := bundle.Validate(now); err != nil {
		t.Fatalf("managed bundle validation: %v", err)
	}
	relay, err := NewRelayClient(ProfileLocalLab, t.TempDir()+"/enrollment.json", bundle, bridge, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := NewGatewayClient(GatewayClientOptions{BaseURL: gatewayServer.URL, Token: "test-token", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	config := Config{Profile: ProfileLocalLab, Permission: "conversation", Relay: RelaySettings{EnvelopeTTLSeconds: 300, EventPollMilliseconds: 500}}
	service, err := NewService(config, gateway, relay, bridge)
	if err != nil {
		t.Fatal(err)
	}
	h.service = service
	service.initManagedPeers()
	return h
}

func (h *managedHarness) takeSubmitted() []envelope.SecureEnvelope {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := h.submitted
	h.submitted = nil
	return out
}

func (h *managedHarness) handshakeOf(env envelope.SecureEnvelope, dst any) {
	h.t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(env.Ciphertext)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		h.t.Fatal(err)
	}
}

func (h *managedHarness) phoneEnvelope(sessionID, payloadType string, object any) json.RawMessage {
	h.t.Helper()
	raw, err := json.Marshal(object)
	if err != nil {
		h.t.Fatal(err)
	}
	env := envelope.SecureEnvelope{
		Type:              envelope.TypeSecureEnvelope,
		DomainID:          "dom_test",
		MessageID:         newWireID("phone"),
		SessionID:         sessionID,
		SenderDeviceID:    h.phone.Identity.DeviceID,
		RecipientDeviceID: h.bridge.Identity.DeviceID,
		Sequence:          0,
		Nonce:             base64.RawURLEncoding.EncodeToString(randomBytesN(12)),
		PayloadType:       payloadType,
		Route:             envelope.Route{RelayID: "relay-test", TTLSeconds: 60, Priority: 5},
		Ciphertext:        base64.RawURLEncoding.EncodeToString(raw),
	}
	wire, err := json.Marshal(env)
	if err != nil {
		h.t.Fatal(err)
	}
	return wire
}

// runHandshake drives the full initiator handshake and returns the phone's
// established session state plus the session id.
func (h *managedHarness) runHandshake(ctx context.Context) (*session.State, string) {
	h.t.Helper()
	peer := h.service.managedPeerFor(h.phone.Identity.DeviceID)
	if peer == nil {
		h.t.Fatal("managed peer missing")
	}
	if err := h.service.initiateManagedSession(ctx, peer); err != nil {
		h.t.Fatalf("initiate: %v", err)
	}
	sent := h.takeSubmitted()
	if len(sent) != 1 || sent[0].PayloadType != session.TypeHello || sent[0].Sequence != 0 {
		h.t.Fatalf("expected one hello handshake envelope, got %#v", sent)
	}
	var bridgeHello session.Hello
	h.handshakeOf(sent[0], &bridgeHello)
	if bridgeHello.GrantID != h.grant.GrantID {
		h.t.Fatalf("initiator hello must carry the bridge grant, got %q", bridgeHello.GrantID)
	}
	// Phone responder: adopts the session id and answers with hello (its
	// grant_id is empty — the phone holds no grant) then ready.
	phoneLocal, err := session.CreateHello(h.provider, h.phone, bridgeHello.SessionID, h.bridge.Identity.DeviceID, "", time.Now().UTC())
	if err != nil {
		h.t.Fatal(err)
	}
	phoneState, err := session.Establish(h.provider, phoneLocal, bridgeHello, h.phone.Identity, h.bridge.Identity)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := h.service.managedHandleRelayMessage(ctx, h.phoneEnvelope(bridgeHello.SessionID, session.TypeHello, phoneLocal.Hello)); err != nil {
		h.t.Fatalf("phone hello: %v", err)
	}
	sent = h.takeSubmitted()
	if len(sent) != 1 || sent[0].PayloadType != session.TypeReady {
		h.t.Fatalf("expected bridge ready, got %#v", sent)
	}
	var bridgeReady session.Ready
	h.handshakeOf(sent[0], &bridgeReady)
	if err := phoneState.VerifyReady(h.provider, bridgeReady, h.bridge.Identity); err != nil {
		h.t.Fatalf("phone could not verify bridge ready: %v", err)
	}
	phoneReady, err := phoneState.CreateReady(h.provider, h.phone)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := h.service.managedHandleRelayMessage(ctx, h.phoneEnvelope(bridgeHello.SessionID, session.TypeReady, phoneReady)); err != nil {
		h.t.Fatalf("phone ready: %v", err)
	}
	return phoneState, bridgeHello.SessionID
}

func TestManagedHandshakeManifestAndGating(t *testing.T) {
	h := newManagedHarness(t)
	ctx := context.Background()
	phoneState, sessionID := h.runHandshake(ctx)

	// After ready the bridge's first business payload is its manifest.
	sent := h.takeSubmitted()
	if len(sent) != 1 || sent[0].PayloadType != manifestPayloadType {
		t.Fatalf("expected bridge manifest, got %#v", sent)
	}
	manifestPlain, err := envelope.Decrypt(h.provider, phoneState, sent[0])
	if err != nil {
		t.Fatalf("phone could not decrypt manifest: %v", err)
	}
	var manifest struct {
		ProductKind      string   `json:"product_kind"`
		RuntimeKind      string   `json:"runtime_kind"`
		ProtocolVersions []string `json:"protocol_versions"`
	}
	if err := json.Unmarshal(manifestPlain, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.ProductKind != "sparkclaw" || manifest.RuntimeKind != "sparkclaw" {
		t.Fatalf("manifest identity = %#v", manifest)
	}

	// Business payloads are refused before the phone's own manifest arrives.
	request := Request{
		ProtocolVersion: ProtocolVersion, Type: TypeCapabilitiesDescribe,
		RequestID: "req-1", EndpointID: h.phone.Identity.DeviceID,
		IssuedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Minute),
	}
	plaintext, _ := json.Marshal(request)
	makeBusiness := func() json.RawMessage {
		env, err := envelope.Encrypt(h.provider, phoneState, newWireID("m"), "task.invoke",
			envelope.Route{RelayID: "relay-test", TTLSeconds: 60, Priority: 5}, plaintext)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(env)
		return raw
	}
	if err := h.service.managedHandleRelayMessage(ctx, makeBusiness()); err == nil ||
		!strings.Contains(err.Error(), "capability manifest") {
		t.Fatalf("expected manifest gating, got %v", err)
	}

	// Phone manifest unlocks business dispatch through the shared gateway path.
	phoneManifest, _ := json.Marshal(map[string]any{
		"product_kind": "jingsi", "runtime_kind": "jingsi-app",
		"protocol_versions": []string{manifestPayloadType, sparkclawBridgeProtocol},
		"capabilities":      []any{},
	})
	manifestEnv, err := envelope.Encrypt(h.provider, phoneState, newWireID("m"), manifestPayloadType,
		envelope.Route{RelayID: "relay-test", TTLSeconds: 60, Priority: 5}, phoneManifest)
	if err != nil {
		t.Fatal(err)
	}
	rawManifest, _ := json.Marshal(manifestEnv)
	if err := h.service.managedHandleRelayMessage(ctx, rawManifest); err != nil {
		t.Fatalf("phone manifest: %v", err)
	}
	if err := h.service.managedHandleRelayMessage(ctx, makeBusiness()); err != nil {
		t.Fatalf("business dispatch after manifest: %v", err)
	}
	sent = h.takeSubmitted()
	if len(sent) != 1 || sent[0].PayloadType != "task.result" {
		t.Fatalf("expected one task.result, got %#v", sent)
	}
	if _, err := envelope.Decrypt(h.provider, phoneState, sent[0]); err != nil {
		t.Fatalf("phone could not decrypt task result: %v", err)
	}
	_ = sessionID
}

func TestManagedReopenTriggersFreshHandshake(t *testing.T) {
	h := newManagedHarness(t)
	ctx := context.Background()
	_, firstSession := h.runHandshake(ctx)
	h.takeSubmitted() // drop the manifest

	reopen, err := session.CreateReopen(h.provider, h.phone, "reopen-req-1", h.bridge.Identity.DeviceID, "relay-test", session.CauseRuntimeStarted, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(reopen)
	env := envelope.SecureEnvelope{
		Type:              envelope.TypeSecureEnvelope,
		DomainID:          "dom_test",
		MessageID:         newWireID("ctl"),
		SessionID:         reopen.RequestID, // binding: session_id == request_id
		SenderDeviceID:    h.phone.Identity.DeviceID,
		RecipientDeviceID: h.bridge.Identity.DeviceID,
		Sequence:          0,
		Nonce:             base64.RawURLEncoding.EncodeToString(randomBytesN(12)),
		PayloadType:       session.TypeReopen,
		Route:             envelope.Route{RelayID: "relay-test", TTLSeconds: 30, Priority: 5},
		Ciphertext:        base64.RawURLEncoding.EncodeToString(raw),
	}
	wire, _ := json.Marshal(env)
	if err := h.service.managedHandleRelayMessage(ctx, wire); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	peer := h.service.managedPeerFor(h.phone.Identity.DeviceID)
	peer.mu.Lock()
	tombstoned := peer.sessionID == "" && peer.isStale(firstSession)
	peer.mu.Unlock()
	if !tombstoned {
		t.Fatal("reopen must tombstone the current session")
	}
	select {
	case <-peer.reinit:
	default:
		t.Fatal("reopen must signal re-initiation")
	}
	// A duplicate request id coalesces silently.
	if err := h.service.managedHandleRelayMessage(ctx, wire); err != nil {
		t.Fatalf("duplicate reopen must coalesce, got %v", err)
	}
	// Re-initiation produces a fresh session id.
	if err := h.service.initiateManagedSession(ctx, peer); err != nil {
		t.Fatalf("re-initiate: %v", err)
	}
	sent := h.takeSubmitted()
	if len(sent) != 1 || sent[0].PayloadType != session.TypeHello {
		t.Fatalf("expected fresh hello, got %#v", sent)
	}
	var hello session.Hello
	h.handshakeOf(sent[0], &hello)
	if hello.SessionID == firstSession {
		t.Fatal("fresh handshake must use a new session id")
	}
}

func TestManagedReopenRejectsTamperAndWindow(t *testing.T) {
	h := newManagedHarness(t)
	ctx := context.Background()
	h.runHandshake(ctx)
	h.takeSubmitted()

	reopen, err := session.CreateReopen(h.provider, h.phone, "reopen-bad-1", h.bridge.Identity.DeviceID, "relay-test", session.CauseRuntimeStarted, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	send := func(mutate func(*envelope.SecureEnvelope)) error {
		raw, _ := json.Marshal(reopen)
		env := envelope.SecureEnvelope{
			Type: envelope.TypeSecureEnvelope, DomainID: "dom_test",
			MessageID: newWireID("ctl"), SessionID: reopen.RequestID,
			SenderDeviceID: h.phone.Identity.DeviceID, RecipientDeviceID: h.bridge.Identity.DeviceID,
			Sequence: 0, Nonce: base64.RawURLEncoding.EncodeToString(randomBytesN(12)),
			PayloadType: session.TypeReopen,
			Route:       envelope.Route{RelayID: "relay-test", TTLSeconds: 30, Priority: 5},
			Ciphertext:  base64.RawURLEncoding.EncodeToString(raw),
		}
		if mutate != nil {
			mutate(&env)
		}
		wire, _ := json.Marshal(env)
		return h.service.managedHandleRelayMessage(ctx, wire)
	}
	if err := send(func(e *envelope.SecureEnvelope) { e.SessionID = "not-the-request-id" }); err == nil {
		t.Fatal("expected envelope binding rejection")
	}
	if err := send(func(e *envelope.SecureEnvelope) { e.Route.TTLSeconds = 300 }); err == nil {
		t.Fatal("expected route TTL rejection")
	}
}
