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
	bundle := c.Enrollment()
	dialer := websocket.Dialer{HandshakeTimeout: c.timeout, Proxy: http.ProxyFromEnvironment}
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
	for {
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
	if time.Until(bundle.Access.ExpiresAt) > time.Minute {
		return nil
	}
	return c.refresh(ctx)
}

func (c *RelayClient) refresh(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Until(c.enrollment.Access.ExpiresAt) > time.Minute {
		return nil
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
	if err := updated.Validate(now); err != nil {
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
	if err := updated.Validate(time.Now().UTC()); err != nil {
		return fmt.Errorf("validate updated enrollment: %w", err)
	}
	if err := SaveEnrollment(c.enrollmentPath, updated); err != nil {
		return fmt.Errorf("persist updated enrollment: %w", err)
	}
	c.enrollment = updated
	return nil
}
