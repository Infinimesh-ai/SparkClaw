package iscpbridge

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/recovery"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/trust"
)

// lifecycleHarness extends the managed harness's relay stub with the
// optional device lifecycle endpoints, enforcing the v0.2 challenge-binding
// rules exactly as the Cloud does.
func TestAutoRenewGrantChallengeBindingAndBounds(t *testing.T) {
	h := newManagedHarness(t)
	ctx := context.Background()

	trustSigner := h.trustSigner
	var attempts atomic.Int32
	h.lifecycleHandler = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/v2/relay/devices/auto-renew-grant" {
			return false
		}
		key := r.Header.Get("Idempotency-Key")
		var req struct {
			Identity identity.DeviceIdentity `json:"identity"`
			Proof    identity.DeviceProof    `json:"identity_proof"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || key == "" {
			w.WriteHeader(http.StatusBadRequest)
			return true
		}
		// The mandatory Idempotency-Key doubles as the proof challenge.
		if err := identity.VerifyProof(h.provider, req.Identity, req.Proof, "relay-test", key, time.Now().UTC(), 5*time.Minute); err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return true
		}
		if attempts.Add(1) == 1 {
			// First attempt: not yet eligible — 429 with pacing.
			w.Header().Set("Retry-After", "17")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "rate_limited", "reason": "renewal_not_yet_eligible"}})
			return true
		}
		now := time.Now().UTC()
		renewed, _ := trust.SignGrant(h.provider, trustSigner, trust.Grant{
			GrantID:                "grant_renewed_1",
			SubjectDeviceID:        h.grant.SubjectDeviceID,
			Audience:               h.grant.Audience,
			ConfirmationThumbprint: h.grant.ConfirmationThumbprint,
			Permissions:            h.grant.Permissions,
			RelayConstraints:       h.grant.RelayConstraints,
			NotBefore:              now.Add(-time.Second),
			ExpiresAt:              now.Add(2 * time.Hour),
		})
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":  map[string]any{"device_id": h.bridge.Identity.DeviceID, "domain_id": "dom_test"},
			"grant": renewed,
		})
		return true
	}

	peer := h.service.managedPeerFor(h.phone.Identity.DeviceID)
	client := &http.Client{Timeout: 5 * time.Second}

	err := h.service.autoRenewGrant(ctx, client, peer)
	var httpErr *lifecycleHTTPError
	if err == nil || !strings.Contains(err.Error(), "renewal_not_yet_eligible") {
		t.Fatalf("expected 429 pacing error, got %v", err)
	}
	if !asLifecycleError(err, &httpErr) || httpErr.retryAfter != 17*time.Second {
		t.Fatalf("expected Retry-After 17s, got %#v", httpErr)
	}

	if err := h.service.autoRenewGrant(ctx, client, peer); err != nil {
		t.Fatalf("renewal: %v", err)
	}
	peer.mu.Lock()
	renewedID := peer.grant.GrantID
	peer.mu.Unlock()
	if renewedID != "grant_renewed_1" {
		t.Fatalf("peer grant not rotated: %q", renewedID)
	}
	// The renewed grant is persisted in the bundle.
	if h.service.relay.Enrollment().Peers[0].OutboundGrant.GrantID != "grant_renewed_1" {
		t.Fatal("renewed grant not persisted in the enrollment bundle")
	}
	// The renewal signals a re-initiation so the next session carries it.
	select {
	case <-peer.reinit:
	default:
		t.Fatal("renewal must signal re-initiation")
	}
}

func TestRecoverCredentialsSealedDelivery(t *testing.T) {
	h := newManagedHarness(t)
	ctx := context.Background()

	h.lifecycleHandler = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/v2/relay/devices/recover-credentials" {
			return false
		}
		key := r.Header.Get("Idempotency-Key")
		var req struct {
			Identity identity.DeviceIdentity `json:"identity"`
			Proof    identity.DeviceProof    `json:"identity_proof"`
			WrapKey  struct {
				KTY    string `json:"kty"`
				Public string `json:"public"`
			} `json:"recovery_wrap_key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || key == "" || req.WrapKey.KTY != "X25519" {
			w.WriteHeader(http.StatusBadRequest)
			return true
		}
		// challenge = <Idempotency-Key> \0 <wrap public key>
		challenge := recovery.Challenge(key, req.WrapKey.Public)
		if err := identity.VerifyProof(h.provider, req.Identity, req.Proof, "relay-test", challenge, time.Now().UTC(), 5*time.Minute); err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return true
		}
		now := time.Now().UTC()
		pair := map[string]any{
			"access":  RelayCredential{DomainID: "dom_test", DeviceID: h.bridge.Identity.DeviceID, Token: "tok_recovered_a", ExpiresAt: now.Add(15 * time.Minute)},
			"refresh": RelayCredential{DomainID: "dom_test", DeviceID: h.bridge.Identity.DeviceID, Token: "tok_recovered_r", ExpiresAt: now.Add(24 * time.Hour)},
		}
		plaintext, _ := json.Marshal(pair)
		transcript := recovery.Transcript("dom_test", h.bridge.Identity.DeviceID, h.bridge.Identity.PublicKey.KID)
		wrapped, err := recovery.Seal(h.provider, req.WrapKey.Public, transcript, plaintext)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return true
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":                map[string]any{"device_id": h.bridge.Identity.DeviceID, "domain_id": "dom_test"},
			"access":              map[string]any{"credential_id": "cred_a", "domain_id": "dom_test", "device_id": h.bridge.Identity.DeviceID, "issued_at": now, "expires_at": now.Add(15 * time.Minute)},
			"refresh":             map[string]any{"credential_id": "cred_r", "domain_id": "dom_test", "device_id": h.bridge.Identity.DeviceID, "issued_at": now, "expires_at": now.Add(24 * time.Hour), "rotation_counter": 1},
			"credentials_wrapped": wrapped,
		})
		return true
	}

	client := &http.Client{Timeout: 5 * time.Second}
	if err := h.service.recoverCredentials(ctx, client); err != nil {
		t.Fatalf("recovery: %v", err)
	}
	bundle := h.service.relay.Enrollment()
	if bundle.Access.Token != "tok_recovered_a" || bundle.Refresh.Token != "tok_recovered_r" {
		t.Fatalf("recovered credentials not persisted: %#v", bundle.Access)
	}
}

func TestRecoverCredentialsRejectsCleartextTokenLeak(t *testing.T) {
	h := newManagedHarness(t)
	ctx := context.Background()
	h.lifecycleHandler = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/v2/relay/devices/recover-credentials" {
			return false
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":                map[string]any{"device_id": h.bridge.Identity.DeviceID, "domain_id": "dom_test"},
			"access":              map[string]any{"credential_id": "cred_a", "token": "LEAKED"},
			"refresh":             map[string]any{"credential_id": "cred_r"},
			"credentials_wrapped": recovery.WrappedCredentials{Type: recovery.TypeWrappedCredentials, Ciphersuite: "x", Ciphertext: "x", Nonce: "x", RecoveryPublicKey: "x", ServerPublicKey: "x"},
		})
		return true
	}
	err := h.service.recoverCredentials(ctx, &http.Client{Timeout: 5 * time.Second})
	if err == nil || !strings.Contains(err.Error(), "leaked token plaintext") {
		t.Fatalf("expected token-leak rejection, got %v", err)
	}
}

func asLifecycleError(err error, target **lifecycleHTTPError) bool {
	for err != nil {
		if httpErr, ok := err.(*lifecycleHTTPError); ok {
			*target = httpErr
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}

// TestAutoRenewGrantRejectsWidenedPermissions pins the renewal bound that
// trust.VerifyGrant cannot enforce on its own: it only checks that the grant
// CONTAINS the requested permission, so a renewal carrying extra permissions
// verifies fine. Silent renewal extends a pairing's lifetime; widening its
// authority requires a human-approved re-pairing.
func TestAutoRenewGrantRejectsWidenedPermissions(t *testing.T) {
	h := newManagedHarness(t)
	ctx := context.Background()
	trustSigner := h.trustSigner

	h.lifecycleHandler = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/v2/relay/devices/auto-renew-grant" {
			return false
		}
		now := time.Now().UTC()
		widened, _ := trust.SignGrant(h.provider, trustSigner, trust.Grant{
			GrantID:                "grant_widened",
			SubjectDeviceID:        h.grant.SubjectDeviceID,
			Audience:               h.grant.Audience,
			ConfirmationThumbprint: h.grant.ConfirmationThumbprint,
			// Keeps the original permission and quietly adds a new one.
			Permissions:      append(append([]string{}, h.grant.Permissions...), "filesystem"),
			RelayConstraints: h.grant.RelayConstraints,
			NotBefore:        now.Add(-time.Second),
			ExpiresAt:        now.Add(2 * time.Hour),
		})
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":  map[string]any{"device_id": h.bridge.Identity.DeviceID, "domain_id": "dom_test"},
			"grant": widened,
		})
		return true
	}

	peer := h.service.managedPeerFor(h.phone.Identity.DeviceID)
	originalID := h.grant.GrantID
	err := h.service.autoRenewGrant(ctx, &http.Client{Timeout: 5 * time.Second}, peer)
	if err == nil || !strings.Contains(err.Error(), "widens the authorized permissions") {
		t.Fatalf("widened renewal must fail closed, got %v", err)
	}
	// Fails closed: neither the in-memory peer nor the persisted bundle rotate.
	peer.mu.Lock()
	currentID := peer.grant.GrantID
	peer.mu.Unlock()
	if currentID != originalID {
		t.Fatalf("peer grant rotated to a widened grant: %q", currentID)
	}
	if h.service.relay.Enrollment().Peers[0].OutboundGrant.GrantID != originalID {
		t.Fatal("widened grant was persisted into the enrollment bundle")
	}
}
