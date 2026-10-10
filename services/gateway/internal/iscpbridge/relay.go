package iscpbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	iscpcrypto "github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"
	"github.com/gorilla/websocket"
)

const (
	relayEnvelopePath = "/v2/relay/envelopes"
	relayRefreshPath  = "/v2/relay/devices/refresh-access"
	relayMaxBody      = 4 << 20
	accessProofHeader = "X-ISCP-Access-Proof"
)

type RelayClient struct {
	provider       iscpcrypto.Provider
	device         identity.Device
	enrollmentPath string
	profile        string
	timeout        time.Duration
	client         *http.Client
	credentialOnly bool

	mu         sync.RWMutex
	enrollment EnrollmentBundle
}

type relayMessage struct {
	State     string          `json:"state"`
	Challenge string          `json:"challenge,omitempty"`
	MessageID string          `json:"message_id,omitempty"`
	Envelope  json.RawMessage `json:"envelope,omitempty"`
	Error     string          `json:"error,omitempty"`
}

func NewRelayClient(profile, enrollmentPath string, enrollment EnrollmentBundle, device identity.Device, timeout time.Duration) (*RelayClient, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if err := validateRelayURLs(profile, enrollment.RelayBaseURL, enrollment.RelayWebSocketURL); err != nil {
		return nil, err
	}
	if device.Identity.DomainID != enrollment.DomainID || device.Identity.DeviceID != enrollment.DeviceID {
		return nil, errors.New("device identity does not match enrollment")
	}
	return &RelayClient{
		provider:       iscpcrypto.NewProvider(),
		device:         device,
		enrollmentPath: enrollmentPath,
		profile:        profile,
		timeout:        timeout,
		client:         &http.Client{Timeout: timeout},
		enrollment:     enrollment,
	}, nil
}

// NewRelayCredentialClient uses enrolled cloud access credentials without
// importing Bridge session-grant semantics. End-to-end workbench grants and
// issuer pins remain entirely separate from this cloud credential bundle.
func NewRelayCredentialClient(profile, enrollmentPath string, enrollment EnrollmentBundle, device identity.Device, timeout time.Duration) (*RelayClient, error) {
	if err := ValidateWorkbenchRelayURLs(profile, enrollment.RelayBaseURL, enrollment.RelayWebSocketURL); err != nil {
		return nil, err
	}
	if profile == ProfileLocalLab && enrollment.Mode != BundleModeWorkbenchLocalLab {
		return nil, errors.New("local-lab Relay requires reference enrollment")
	}
	if profile == ProfileProduction && enrollment.Mode == BundleModeWorkbenchLocalLab {
		return nil, errors.New("production Relay rejects local-lab enrollment")
	}
	if err := enrollment.ValidateCredentials(time.Now().UTC()); err != nil {
		return nil, err
	}
	client, err := NewRelayClient(profile, enrollmentPath, enrollment, device, timeout)
	if err != nil {
		return nil, err
	}
	client.credentialOnly = true
	if profile == ProfileLocalLab {
		client.client.Transport = &http.Transport{Proxy: nil}
	}
	return client, nil
}

func (c *RelayClient) validateEnrollment(bundle EnrollmentBundle, now time.Time) error {
	if c.credentialOnly {
		return bundle.ValidateCredentials(now)
	}
	return bundle.Validate(now)
}

func (c *RelayClient) Enrollment() EnrollmentBundle {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.enrollment
}

func (c *RelayClient) Submit(ctx context.Context, value any) error {
	if err := c.ensureAccess(ctx); err != nil {
		return err
	}
	if err := c.submit(ctx, value); err != nil {
		var relayErr *relayHTTPError
		if errors.As(err, &relayErr) && relayErr.status == http.StatusUnauthorized {
			if refreshErr := c.refresh(ctx); refreshErr == nil {
				return c.submit(ctx, value)
			}
		}
		return err
	}
	return nil
}

func (c *RelayClient) submit(ctx context.Context, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode Relay envelope: %w", err)
	}
	if len(raw) > relayMaxBody {
		return errors.New("Relay envelope is too large")
	}
	bundle := c.Enrollment()
	endpoint := strings.TrimRight(bundle.RelayBaseURL, "/") + relayEnvelopePath
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return errors.New("create Relay envelope request")
	}
	request.Header.Set("Authorization", "Bearer "+bundle.Access.Token)
	request.Header.Set("Content-Type", "application/json")
	proof, err := c.accessProof(bundle.Access.Token, http.MethodPost, relayEnvelopePath, bundle.RelayID)
	if err != nil {
		return err
	}
	request.Header.Set(accessProofHeader, proof)
	response, err := c.client.Do(request)
	if err != nil {
		return fmt.Errorf("submit Relay envelope: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, relayMaxBody))
	if response.StatusCode != http.StatusAccepted {
		return &relayHTTPError{status: response.StatusCode}
	}
	return nil
}

func (c *RelayClient) RunOnce(ctx context.Context, handle func(context.Context, json.RawMessage) error) error {
	return c.RunOnceReady(ctx, handle, nil)
}

// RunOnceReady reports the authenticated receive connection before reading
// messages. Session initiators must wait for this point before sending Hello.
func (c *RelayClient) RunOnceReady(ctx context.Context, handle func(context.Context, json.RawMessage) error, onReady func()) error {
	if c.credentialOnly && c.profile == ProfileLocalLab {
		return c.runLocalDrainPolling(ctx, handle, onReady)
	}
	return c.runSocketOnce(ctx, handle, onReady)
}

// The locked reference Relay intentionally authenticates, drains its current
// queue and closes each WebSocket. A normal drained frame completes one poll,
// not the peer's encrypted session. Expose one logical receive lifetime and
// one readiness callback while repeating authenticated bounded drain polls.
func (c *RelayClient) runLocalDrainPolling(ctx context.Context, handle func(context.Context, json.RawMessage) error, onReady func()) error {
	ready := false
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := c.runSocketOnce(ctx, handle, func() {
			if !ready {
				ready = true
				if onReady != nil {
					onReady()
				}
			}
		}); err != nil {
			return err
		}
		// 1s plus processing leaves room below reference's shared 120/IP/min
		// allowance for access-PoP POSTs, initial discovery and enrollment.
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *RelayClient) runSocketOnce(ctx context.Context, handle func(context.Context, json.RawMessage) error, onReady func()) error {
	bundle := c.Enrollment()
	dialer := websocket.Dialer{HandshakeTimeout: c.timeout, Proxy: http.ProxyFromEnvironment}
	if c.credentialOnly && c.profile == ProfileLocalLab {
		dialer.Proxy = nil
	}
	connection, response, err := dialer.DialContext(ctx, bundle.RelayWebSocketURL, nil)
	if err != nil {
		if response != nil {
			return &relayHTTPError{status: response.StatusCode}
		}
		return fmt.Errorf("connect Relay: %w", err)
	}
	defer connection.Close()
	connection.SetReadLimit(relayMaxBody)
	stopClose := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stopClose()
	// Authentication reads and writes must not outlive the dial timeout when
	// a reachable Relay stalls before sending challenge or authenticated ready.
	if err := connection.SetReadDeadline(time.Now().Add(c.timeout)); err != nil {
		return errors.New("set Relay authentication read deadline")
	}
	if err := connection.SetWriteDeadline(time.Now().Add(c.timeout)); err != nil {
		return errors.New("set Relay authentication write deadline")
	}

	var challenge relayMessage
	if err := connection.ReadJSON(&challenge); err != nil {
		return fmt.Errorf("read Relay challenge: %w", err)
	}
	if challenge.State != "challenge" || strings.TrimSpace(challenge.Challenge) == "" {
		return errors.New("Relay returned an invalid connection challenge")
	}
	proof, err := c.device.CreateProof(c.provider, bundle.RelayID, challenge.Challenge, randomNonce(), time.Now().UTC())
	if err != nil {
		return errors.New("create Relay connection proof")
	}
	if err := connection.WriteJSON(proof); err != nil {
		return fmt.Errorf("send Relay connection proof: %w", err)
	}

	var ready relayMessage
	if err := connection.ReadJSON(&ready); err != nil {
		return fmt.Errorf("read Relay connection state: %w", err)
	}
	if ready.State == "closed" {
		return errors.New("Relay rejected or revoked the enrolled device")
	}
	if ready.State != "ready" {
		return errors.New("Relay did not enter ready state")
	}
	if err := connection.SetReadDeadline(time.Time{}); err != nil {
		return errors.New("clear Relay authentication read deadline")
	}
	if onReady != nil {
		onReady()
	}
	for {
		if c.credentialOnly {
			// A responder can wait without an active session. Bound this idle
			// read too so a silently broken receive socket eventually reconnects.
			if err := connection.SetReadDeadline(time.Now().Add(2 * time.Minute)); err != nil {
				return errors.New("set Relay receive liveness deadline")
			}
		}
		var message relayMessage
		if err := connection.ReadJSON(&message); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("read Relay message: %w", err)
		}
		switch message.State {
		case "message":
			if len(message.Envelope) == 0 {
				return errors.New("Relay delivered an empty envelope")
			}
			if err := handle(ctx, message.Envelope); err != nil {
				return err
			}
		case "drained":
			return nil
		case "closed":
			return errors.New("Relay closed the connection")
		default:
			return errors.New("Relay returned an unknown connection state")
		}
	}
}

func (c *RelayClient) ensureAccess(ctx context.Context) error {
	bundle := c.Enrollment()
	if time.Until(bundle.Access.ExpiresAt) > time.Minute && !c.localEnrollmentRefreshDue(bundle) {
		return nil
	}
	return c.refresh(ctx)
}

func (c *RelayClient) refresh(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Until(c.enrollment.Access.ExpiresAt) > time.Minute && !c.localEnrollmentRefreshDue(c.enrollment) {
		return nil
	}
	// Local enrollment validity comes from signed discovery and rotating Relay
	// credentials, independently of permanent workbench consent. Refresh both
	// together; otherwise a healthy daily credential rotation leaves yesterday's
	// outer bundle expiry behind and prevents the next connection/restart.
	var localExpiry time.Time
	if c.credentialOnly && c.profile == ProfileLocalLab {
		if !time.Now().Before(c.enrollment.Refresh.ExpiresAt) {
			return errors.New("local Relay refresh credential expired")
		}
		discovery, _, err := DiscoverLocalRelay(ctx, c.enrollment.RelayBaseURL, c.enrollment.RelayID, c.enrollment.DomainID, c.enrollment.RelaySignerIdentity)
		if err != nil {
			return err
		}
		localExpiry = discovery.ExpiresAt
	}
	body, _ := json.Marshal(map[string]string{"refresh": c.enrollment.Refresh.Token})
	endpoint := strings.TrimRight(c.enrollment.RelayBaseURL, "/") + relayRefreshPath
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("create Relay credential refresh request")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(request)
	if err != nil {
		return fmt.Errorf("refresh Relay credential: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, relayMaxBody))
		return &relayHTTPError{status: response.StatusCode}
	}
	var credentials struct {
		Access  RelayCredential `json:"access"`
		Refresh RelayCredential `json:"refresh"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, relayMaxBody))
	if err := decoder.Decode(&credentials); err != nil {
		return errors.New("decode Relay credential refresh response")
	}
	if credentials.Access.DomainID != c.enrollment.DomainID || credentials.Access.DeviceID != c.enrollment.DeviceID ||
		credentials.Refresh.DomainID != c.enrollment.DomainID || credentials.Refresh.DeviceID != c.enrollment.DeviceID {
		return errors.New("refreshed Relay credential has an invalid device binding")
	}
	now := time.Now().UTC()
	if !now.Before(credentials.Access.ExpiresAt) || !now.Before(credentials.Refresh.ExpiresAt) {
		return errors.New("refreshed Relay credentials are already expired")
	}
	updated := c.enrollment
	updated.Access = credentials.Access
	updated.Refresh = credentials.Refresh
	if !localExpiry.IsZero() {
		updated.ExpiresAt = localExpiry
		if credentials.Refresh.ExpiresAt.Before(updated.ExpiresAt) {
			updated.ExpiresAt = credentials.Refresh.ExpiresAt
		}
	}
	if err := c.validateEnrollment(updated, now); err != nil {
		return fmt.Errorf("validate refreshed Relay credentials: %w", err)
	}
	if err := SaveEnrollment(c.enrollmentPath, updated); err != nil {
		return fmt.Errorf("persist rotated Relay credentials: %w", err)
	}
	c.enrollment = updated
	return nil
}

func (c *RelayClient) accessProof(token, method, path, audience string) (string, error) {
	challenge := strings.Join([]string{
		"iscp/v2/relay/access-proof",
		strings.ToUpper(method),
		path,
		iscpcrypto.Base64URL(iscpcrypto.SHA256([]byte(token))),
	}, "\x00")
	proof, err := c.device.CreateProof(c.provider, audience, challenge, randomNonce(), time.Now().UTC())
	if err != nil {
		return "", errors.New("create Relay access proof")
	}
	raw, err := json.Marshal(proof)
	if err != nil {
		return "", errors.New("encode Relay access proof")
	}
	return iscpcrypto.Base64URL(raw), nil
}

type relayHTTPError struct {
	status int
}

func (e *relayHTTPError) Error() string {
	return fmt.Sprintf("Relay returned HTTP status %d", e.status)
}

func validateRelayURLs(profile, baseURL, websocketURL string) error {
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || base.Hostname() == "" || (base.Scheme != "https" && base.Scheme != "http") {
		return errors.New("Relay base URL is invalid")
	}
	websocketEndpoint, err := url.Parse(strings.TrimSpace(websocketURL))
	if err != nil || websocketEndpoint.Hostname() == "" || (websocketEndpoint.Scheme != "wss" && websocketEndpoint.Scheme != "ws") {
		return errors.New("Relay WebSocket URL is invalid")
	}
	if profile == ProfileProduction && (base.Scheme != "https" || websocketEndpoint.Scheme != "wss") {
		return errors.New("production Bridge requires HTTPS and WSS Relay URLs")
	}
	if base.User != nil || websocketEndpoint.User != nil {
		return errors.New("Relay URLs must not contain credentials")
	}
	return nil
}

func randomNonce() string {
	return newWireID("nonce")
}

// UpdateEnrollment applies a mutation to the enrollment bundle under the
// client lock and persists it atomically (used by grant renewal and
// credential recovery). The mutation must keep the bundle valid.
func (c *RelayClient) UpdateEnrollment(mutate func(*EnrollmentBundle)) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	updated := c.enrollment
	mutate(&updated)
	if err := c.validateEnrollment(updated, time.Now().UTC()); err != nil {
		return fmt.Errorf("validate updated enrollment: %w", err)
	}
	if err := SaveEnrollment(c.enrollmentPath, updated); err != nil {
		return fmt.Errorf("persist updated enrollment: %w", err)
	}
	c.enrollment = updated
	return nil
}
