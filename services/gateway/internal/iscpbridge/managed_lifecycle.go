// Managed device lifecycle clients (ISCP v0.2 spec/device-lifecycle.md):
// bounded silent grant auto-renewal and existing-device credential recovery.
// Both are OPTIONAL relay surface, feature-detected through the relay
// descriptor's metadata capability keys; both require a fresh possession
// proof with the enrolled key on every attempt (no long-lived bearer ever
// reaches the Bridge), and both are single-shot per idempotency key — a
// retry after an unknown outcome replays the byte-identical original request.
package iscpbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	iscpcrypto "github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/recovery"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/trust"
)

const (
	capabilityGrantRenewal       = "grant_renewal"
	capabilityCredentialRecovery = "credential_recovery"
	lifecycleCheckInterval       = 5 * time.Minute
	// refreshRecoveryLead recovers relay credentials before the refresh
	// credential's 24h TTL runs out, not after.
	refreshRecoveryLead = 30 * time.Minute
)

// grantRenewalWindow mirrors the platform rule: renewal becomes eligible
// min(24h, grant_ttl/5) before expiry (spec/device-lifecycle.md).
func grantRenewalWindow(grant trust.Grant) time.Duration {
	ttl := grant.ExpiresAt.Sub(grant.NotBefore)
	window := ttl / 5
	if window > 24*time.Hour {
		window = 24 * time.Hour
	}
	if window <= 0 {
		window = time.Hour
	}
	return window
}

// fetchRelayCapabilities re-reads the relay descriptor's metadata capability
// keys. Descriptors are short-lived (24h), so feature detection is never
// cached beyond one lifecycle pass. The descriptor is signature- and
// expiry-verified before its metadata steers anything: a forged capability
// only costs a probe (both endpoints re-prove possession and verify their
// results against pinned identities), but reading an unverified document is
// not a habit worth keeping in the lifecycle loop.
func (s *Service) fetchRelayCapabilities(ctx context.Context, client *http.Client, baseURL string) (map[string]string, error) {
	relayDesc, _, err := fetchVerifiedRelayDescriptor(ctx, client, s.provider, baseURL, s.config.Profile, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if relayDesc.Metadata == nil {
		return map[string]string{}, nil
	}
	return relayDesc.Metadata, nil
}

type lifecycleHTTPError struct {
	status     int
	reason     string
	retryAfter time.Duration
}

func (e *lifecycleHTTPError) Error() string {
	if e.reason != "" {
		return fmt.Sprintf("HTTP %d (%s)", e.status, e.reason)
	}
	return fmt.Sprintf("HTTP %d", e.status)
}

func (s *Service) lifecyclePost(ctx context.Context, client *http.Client, path, idempotencyKey string, body []byte) ([]byte, error) {
	bundle := s.relay.Enrollment()
	endpoint := strings.TrimRight(bundle.RelayBaseURL, "/") + path
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("create lifecycle request")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", idempotencyKey)
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("lifecycle request: %w", err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, relayMaxBody))
	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusOK {
		httpErr := &lifecycleHTTPError{status: response.StatusCode, reason: summarizeWireError(raw)}
		if retryAfter := strings.TrimSpace(response.Header.Get("Retry-After")); retryAfter != "" {
			if seconds, parseErr := strconv.Atoi(retryAfter); parseErr == nil && seconds > 0 {
				httpErr.retryAfter = time.Duration(seconds) * time.Second
			}
		}
		return nil, httpErr
	}
	return raw, nil
}

// autoRenewGrant executes one bounded silent renewal attempt for the peer's
// grant: the mandatory Idempotency-Key doubles as the proof challenge, and
// the renewed grant is verified with the same bindings before it replaces
// the stored one (same subject, audience, confirmation, relay — any
// deviation fails closed).
func (s *Service) autoRenewGrant(ctx context.Context, client *http.Client, peer *managedPeer) error {
	bundle := s.relay.Enrollment()
	idempotencyKey := newWireID("renew")
	proof, err := s.device.CreateProof(s.provider, bundle.RelayID, idempotencyKey, randomNonce(), time.Now().UTC())
	if err != nil {
		return errors.New("create renewal proof")
	}
	body, err := json.Marshal(map[string]any{
		"identity":       s.device.Identity,
		"identity_proof": proof,
	})
	if err != nil {
		return errors.New("encode renewal request")
	}
	raw, err := s.lifecyclePost(ctx, client, "/v2/relay/devices/auto-renew-grant", idempotencyKey, body)
	if err != nil {
		return err
	}
	var renewal struct {
		Grant trust.Grant `json:"grant"`
	}
	if err := json.Unmarshal(raw, &renewal); err != nil || renewal.Grant.GrantID == "" {
		return errors.New("renewal response is invalid")
	}
	localThumbprint, err := identity.Thumbprint(s.device.Identity)
	if err != nil {
		return errors.New("local identity thumbprint is invalid")
	}
	peer.mu.Lock()
	previous := peer.grant
	peer.mu.Unlock()
	// Verify against the PREVIOUS grant's permission, not the renewed grant's
	// own: trust.VerifyGrant only checks slices.Contains(grant.Permissions,
	// opts.Permission), so feeding it renewal.Grant.Permissions[0] would be
	// tautological and would wave through any permission the Cloud added.
	permission := ""
	if len(previous.Permissions) > 0 {
		permission = previous.Permissions[0]
	}
	if err := trust.VerifyGrant(s.provider, renewal.Grant, bundle.TrustRootIdentity, trust.VerifyOptions{
		Audience:               previous.Audience,
		SubjectDeviceID:        bundle.DeviceID,
		ConfirmationThumbprint: localThumbprint,
		Permission:             permission,
		RelayID:                bundle.RelayID,
		Now:                    time.Now().UTC(),
	}); err != nil {
		return errors.New("renewed Trust Grant verification failed")
	}
	// Bounds: silent renewal extends the pairing's lifetime and nothing else.
	// A renewal may drop permissions but must never add one — widening is an
	// authorization change and requires a human-approved re-pairing.
	if renewal.Grant.Audience != previous.Audience ||
		renewal.Grant.SubjectDeviceID != previous.SubjectDeviceID ||
		renewal.Grant.ConfirmationThumbprint != previous.ConfirmationThumbprint {
		return errors.New("renewed Trust Grant deviates from the authorized pairing")
	}
	for _, granted := range renewal.Grant.Permissions {
		if !slices.Contains(previous.Permissions, granted) {
			return errors.New("renewed Trust Grant widens the authorized permissions")
		}
	}
	if err := s.relay.UpdateEnrollment(func(b *EnrollmentBundle) {
		for index := range b.Peers {
			if b.Peers[index].Identity.DeviceID == previous.Audience {
				b.Peers[index].OutboundGrant = renewal.Grant
			}
		}
		if renewal.Grant.ExpiresAt.After(b.ExpiresAt) {
			b.ExpiresAt = renewal.Grant.ExpiresAt
		}
	}); err != nil {
		return err
	}
	peer.mu.Lock()
	peer.grant = renewal.Grant
	peer.mu.Unlock()
	// A renewed grant means the next session carries the new grant id.
	peer.signalReinitiate()
	return nil
}

// recoverCredentials executes one existing-device relay credential recovery:
// a fresh X25519 wrap key per attempt, the proof challenge binding both the
// idempotency key and the wrap key, and the bearer plaintext arriving only
// inside the sealed blob (opened through the SDK recovery primitives that
// the three-language v0.2 vectors pin).
func (s *Service) recoverCredentials(ctx context.Context, client *http.Client) error {
	bundle := s.relay.Enrollment()
	wrapPriv, wrapPub, err := s.provider.GenerateSessionKey()
	if err != nil {
		return errors.New("generate recovery wrap key")
	}
	wrapPublic := iscpcrypto.Base64URL(wrapPub.Bytes())
	idempotencyKey := newWireID("recover")
	challenge := recovery.Challenge(idempotencyKey, wrapPublic)
	proof, err := s.device.CreateProof(s.provider, bundle.RelayID, challenge, randomNonce(), time.Now().UTC())
	if err != nil {
		return errors.New("create recovery proof")
	}
	body, err := json.Marshal(map[string]any{
		"identity":       s.device.Identity,
		"identity_proof": proof,
		"recovery_wrap_key": map[string]string{
			"kty":    "X25519",
			"public": wrapPublic,
		},
	})
	if err != nil {
		return errors.New("encode recovery request")
	}
	raw, err := s.lifecyclePost(ctx, client, "/v2/relay/devices/recover-credentials", idempotencyKey, body)
	if err != nil {
		return err
	}
	var wire struct {
		Access  json.RawMessage             `json:"access"`
		Refresh json.RawMessage             `json:"refresh"`
		Wrapped recovery.WrappedCredentials `json:"credentials_wrapped"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil || wire.Wrapped.Ciphertext == "" {
		return errors.New("recovery response is invalid")
	}
	// The cleartext metadata must never carry bearer plaintext.
	for _, cleartext := range []json.RawMessage{wire.Access, wire.Refresh} {
		var meta struct {
			Token string `json:"token"`
		}
		if json.Unmarshal(cleartext, &meta) == nil && meta.Token != "" {
			return errors.New("recovery response leaked token plaintext outside the sealed blob")
		}
	}
	transcript := recovery.Transcript(bundle.DomainID, bundle.DeviceID, s.device.Identity.PublicKey.KID)
	plaintext, err := recovery.Open(s.provider, wire.Wrapped, wrapPriv, wrapPublic, transcript)
	if err != nil {
		return fmt.Errorf("open sealed credentials: %w", err)
	}
	var pair struct {
		Access  RelayCredential `json:"access"`
		Refresh RelayCredential `json:"refresh"`
	}
	if err := json.Unmarshal(plaintext, &pair); err != nil {
		return errors.New("sealed credential pair is invalid")
	}
	now := time.Now().UTC()
	if pair.Access.Token == "" || pair.Refresh.Token == "" ||
		pair.Access.DomainID != bundle.DomainID || pair.Access.DeviceID != bundle.DeviceID ||
		pair.Refresh.DomainID != bundle.DomainID || pair.Refresh.DeviceID != bundle.DeviceID ||
		!now.Before(pair.Access.ExpiresAt) || !now.Before(pair.Refresh.ExpiresAt) {
		return errors.New("sealed credential pair binding is invalid")
	}
	return s.relay.UpdateEnrollment(func(b *EnrollmentBundle) {
		b.Access = pair.Access
		b.Refresh = pair.Refresh
	})
}

// runManagedLifecycle keeps the managed credentials alive proactively:
// grant auto-renewal inside its eligibility window and credential recovery
// before the refresh TTL lapses — both gated on the relay's advertised
// capabilities so an unsupported deployment is never probed.
func (s *Service) runManagedLifecycle(ctx context.Context) {
	client := &http.Client{Timeout: s.config.RelayRequestTimeout()}
	ticker := time.NewTicker(lifecycleCheckInterval)
	defer ticker.Stop()
	var retryNotBefore time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		now := time.Now().UTC()
		if now.Before(retryNotBefore) {
			continue
		}
		bundle := s.relay.Enrollment()
		needsRenewal := false
		s.mu.RLock()
		peers := make([]*managedPeer, 0, len(s.managedPeers))
		for _, peer := range s.managedPeers {
			peers = append(peers, peer)
		}
		s.mu.RUnlock()
		for _, peer := range peers {
			peer.mu.Lock()
			expiresAt := peer.grant.ExpiresAt
			window := grantRenewalWindow(peer.grant)
			peer.mu.Unlock()
			if expiresAt.Sub(now) < window {
				needsRenewal = true
			}
		}
		needsRecovery := bundle.Refresh.ExpiresAt.Sub(now) < refreshRecoveryLead
		if !needsRenewal && !needsRecovery {
			continue
		}
		capabilities, err := s.fetchRelayCapabilities(ctx, client, bundle.RelayBaseURL)
		if err != nil {
			continue
		}
		if needsRenewal && capabilities[capabilityGrantRenewal] == "true" {
			for _, peer := range peers {
				peer.mu.Lock()
				due := peer.grant.ExpiresAt.Sub(now) < grantRenewalWindow(peer.grant)
				peer.mu.Unlock()
				if !due {
					continue
				}
				if err := s.autoRenewGrant(ctx, client, peer); err != nil {
					var httpErr *lifecycleHTTPError
					if errors.As(err, &httpErr) && httpErr.retryAfter > 0 {
						retryNotBefore = now.Add(httpErr.retryAfter)
					}
				}
			}
		}
		if needsRecovery && capabilities[capabilityCredentialRecovery] == "true" {
			_ = s.recoverCredentials(ctx, client)
		}
	}
}
