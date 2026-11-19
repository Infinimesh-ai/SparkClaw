package relay

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/envelope"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"
	"github.com/gorilla/websocket"

	"github.com/Chiiz0/SparkClaw/services/link/relay/duplex"
)

// Endpoint describes one Relay.
type Endpoint struct {
	DomainID          string                   `json:"domain_id"`
	RelayID           string                   `json:"relay_id"`
	BaseURL           string                   `json:"base_url"`
	WebSocketURL      string                   `json:"ws_url"`
	TLSCertificatePEM string                   `json:"tls_certificate_pem,omitempty"`
	Signer            *identity.DeviceIdentity `json:"signer,omitempty"`
}

// Token is one Relay credential.
type Token struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Credentials is the access/refresh pair the Relay issues.
type Credentials struct {
	Access  Token `json:"access"`
	Refresh Token `json:"refresh"`
}

// CredentialStore persists Relay credentials across restarts.
type CredentialStore interface {
	LoadRelayCredentials() (Credentials, error)
	SaveRelayCredentials(Credentials) error
}

// Rebinder obtains fresh credentials when the refresh credential is no
// longer valid (for example after a long time offline). The console
// adapter implements it.
type Rebinder func(ctx context.Context) (Credentials, error)

// ErrRejected is returned by Submit when the Relay rejects an envelope.
type ErrRejected struct{ Code string }

func (e *ErrRejected) Error() string { return "relay rejected envelope: " + e.Code }

// ErrNotConnected is returned by Submit when no connection is ready.
var ErrNotConnected = errors.New("relay is not connected")

// Client is one device's connection to one Relay.
type Client struct {
	ep       Endpoint
	device   identity.Device
	store    CredentialStore
	rebind   Rebinder
	provider crypto.Provider
	tls      *tls.Config
	http     *http.Client

	credMu sync.Mutex
	creds  Credentials

	mu   sync.Mutex
	conn *connection
}

// NewClient builds a client. store and rebind may be nil.
func NewClient(ep Endpoint, device identity.Device, store CredentialStore, initial Credentials, rebind Rebinder) (*Client, error) {
	if ep.RelayID == "" || ep.DomainID == "" || ep.WebSocketURL == "" || ep.BaseURL == "" {
		return nil, errors.New("relay endpoint incomplete")
	}
	if device.Identity.DomainID != ep.DomainID {
		return nil, errors.New("device identity is not in the relay domain")
	}
	cfg, err := PinnedTLSConfig(ep.TLSCertificatePEM)
	if err != nil {
		return nil, err
	}
	c := &Client{ep: ep, device: device, store: store, rebind: rebind, provider: crypto.NewProvider(), tls: cfg, http: HTTPClient(cfg, 20*time.Second), creds: initial}
	if store != nil && initial.Refresh.Token == "" {
		if loaded, err := store.LoadRelayCredentials(); err == nil {
			c.creds = loaded
		}
	}
	return c, nil
}

// Endpoint returns the Relay endpoint.
func (c *Client) Endpoint() Endpoint { return c.ep }

// DeviceID returns this client's device id.
func (c *Client) DeviceID() string { return c.device.Identity.DeviceID }

func (c *Client) setCredentials(next Credentials) {
	c.credMu.Lock()
	c.creds = next
	c.credMu.Unlock()
	if c.store != nil {
		_ = c.store.SaveRelayCredentials(next)
	}
}

func (c *Client) credentials() Credentials {
	c.credMu.Lock()
	defer c.credMu.Unlock()
	return c.creds
}

// ensureAccess returns an access token valid for at least margin, refreshing
// or rebinding as needed.
func (c *Client) ensureAccess(ctx context.Context, margin time.Duration, force bool) (Credentials, error) {
	cur := c.credentials()
	if !force && cur.Access.Token != "" && time.Until(cur.Access.ExpiresAt) > margin {
		return cur, nil
	}
	if cur.Refresh.Token != "" && time.Now().Before(cur.Refresh.ExpiresAt) {
		next, status, err := c.refresh(ctx, cur.Refresh.Token)
		if err == nil {
			c.setCredentials(next)
			return next, nil
		}
		if status != http.StatusUnauthorized && status != http.StatusForbidden && status != http.StatusConflict {
			return cur, err
		}
	}
	if c.rebind == nil {
		return cur, errors.New("relay credentials expired and no rebinder is configured")
	}
	next, err := c.rebind(ctx)
	if err != nil {
		return cur, fmt.Errorf("rebind relay credentials: %w", err)
	}
	c.setCredentials(next)
	return next, nil
}

func (c *Client) refresh(ctx context.Context, refresh string) (Credentials, int, error) {
	body, _ := json.Marshal(map[string]string{"refresh": refresh})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.ep.BaseURL, "/")+"/v2/relay/devices/refresh-access", bytes.NewReader(body))
	if err != nil {
		return Credentials{}, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return Credentials{}, 0, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 64*1024))
	if res.StatusCode != http.StatusOK {
		return Credentials{}, res.StatusCode, fmt.Errorf("relay refresh: HTTP %d", res.StatusCode)
	}
	var out Credentials
	if err := json.Unmarshal(raw, &out); err != nil || out.Access.Token == "" || out.Refresh.Token == "" {
		return Credentials{}, res.StatusCode, errors.New("relay refresh: malformed response")
	}
	return out, res.StatusCode, nil
}

type result struct {
	frame duplex.Frame
	err   error
}

type connection struct {
	c       *Client
	ws      *websocket.Conn
	ready   duplex.Frame
	writeMu sync.Mutex
	request uint64

	mu        sync.Mutex
	pending   map[string]chan result
	reauthID  string
	reauthTok string
	reauthCh  chan error
	expires   time.Time
	cancel    context.CancelFunc
}

// Run opens one connection and serves it until it ends. onEnvelope is
// called for every delivered envelope (raw JSON) on the reading goroutine;
// it must not block for long. onReady runs once the connection is ready.
func (c *Client) Run(ctx context.Context, onEnvelope func(json.RawMessage), onReady func()) error {
	creds, err := c.ensureAccess(ctx, time.Minute, false)
	if err != nil {
		return err
	}
	d := websocket.Dialer{HandshakeTimeout: 15 * time.Second, Subprotocols: []string{duplex.Protocol}, TLSClientConfig: c.tls}
	if c.tls == nil {
		d.Proxy = http.ProxyFromEnvironment
	}
	ws, res, err := d.DialContext(ctx, c.ep.WebSocketURL, nil)
	if err != nil {
		if res != nil {
			return fmt.Errorf("connect relay: HTTP %d", res.StatusCode)
		}
		return fmt.Errorf("connect relay: %w", err)
	}
	defer ws.Close()
	if ws.Subprotocol() != duplex.Protocol {
		return errors.New("relay selected wrong subprotocol")
	}
	life, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(life, func() { ws.Close() })
	defer stop()
	ws.SetReadLimit(duplex.MaxFrameBytes)
	_ = ws.SetReadDeadline(time.Now().Add(15 * time.Second))
	challenge, err := readFrame(ws)
	if err != nil {
		return err
	}
	limits := duplex.DefaultLimits()
	if challenge.Kind != "challenge" || challenge.RelayID != c.ep.RelayID || challenge.DomainID != c.ep.DomainID || challenge.Limits == nil || *challenge.Limits != limits {
		return errors.New("relay challenge binding mismatch")
	}
	h, err := duplex.AuthChallenge(challenge, "authenticate", "1", "0", creds.Access.Token, c.device.Identity)
	if err != nil {
		return err
	}
	proof, err := c.device.CreateProof(c.provider, c.ep.RelayID, h, duplex.RandomID(), time.Now().UTC().Truncate(time.Second))
	if err != nil {
		return err
	}
	if err = writeFrame(ws, duplex.Frame{V: 1, Kind: "authenticate", ConnectionID: challenge.ConnectionID, RequestID: "1", ChallengeID: challenge.ChallengeID, AccessToken: creds.Access.Token, Proof: &proof}); err != nil {
		return err
	}
	ready, err := readFrame(ws)
	if err != nil {
		return err
	}
	if ready.Kind == "closing" {
		if ready.Code == "authentication_failed" {
			// Force a refresh next time.
			c.credMu.Lock()
			c.creds.Access.ExpiresAt = time.Time{}
			c.credMu.Unlock()
		}
		return fmt.Errorf("relay closed during authentication: %s", ready.Code)
	}
	if ready.Kind != "ready" || ready.ConnectionID != challenge.ConnectionID || ready.DeviceID != c.device.Identity.DeviceID || ready.HistoryID != challenge.HistoryID {
		return errors.New("relay ready binding mismatch")
	}
	conn := &connection{c: c, ws: ws, ready: ready, request: 1, pending: map[string]chan result{}, expires: duplex.Time(ready.AuthorityExpiresAt), cancel: cancel}
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		if c.conn == conn {
			c.conn = nil
		}
		c.mu.Unlock()
		conn.failPending(errors.New("relay connection closed"))
	}()
	ws.SetPingHandler(func(data string) error {
		_ = ws.SetReadDeadline(time.Now().Add(75 * time.Second))
		conn.writeMu.Lock()
		defer conn.writeMu.Unlock()
		return ws.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(5*time.Second))
	})
	ws.SetPongHandler(func(string) error {
		_ = ws.SetReadDeadline(time.Now().Add(75 * time.Second))
		return nil
	})
	_ = ws.SetReadDeadline(time.Now().Add(75 * time.Second))
	go conn.maintain(life)
	if onReady != nil {
		go onReady()
	}
	for life.Err() == nil {
		f, err := readFrame(ws)
		if err != nil {
			if life.Err() != nil {
				return life.Err()
			}
			return err
		}
		if err := conn.receive(f, onEnvelope); err != nil {
			return err
		}
	}
	return life.Err()
}

// Connected reports whether a connection is ready.
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}

// Submit sends an envelope and waits for the Relay's acceptance.
func (c *Client) Submit(ctx context.Context, env envelope.SecureEnvelope) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return ErrNotConnected
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	if len(raw) > duplex.MaxEnvelopeBytes {
		return errors.New("envelope too large for relay")
	}
	digest, err := duplex.Digest(json.RawMessage(raw))
	if err != nil {
		return err
	}
	ttl := env.Route.TTLSeconds
	if ttl > 1 {
		ttl--
	}
	f := duplex.Frame{V: 1, Kind: "submit", ConnectionID: conn.ready.ConnectionID, OwnerGeneration: conn.ready.OwnerGeneration, HistoryID: conn.ready.HistoryID, MessageID: env.MessageID, EnvelopeDigest: digest, NotAfter: duplex.Timestamp(time.Now().Add(time.Duration(ttl) * time.Second)), Envelope: raw}
	ch := make(chan result, 1)
	id, err := conn.send(f, ch)
	if err != nil {
		return err
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return r.err
		}
		switch r.frame.Kind {
		case "accepted":
			if c.ep.Signer != nil && r.frame.Receipt != nil {
				sub := duplex.Submission{MessageID: f.MessageID, EnvelopeDigest: f.EnvelopeDigest, HistoryID: f.HistoryID, NotAfter: f.NotAfter, Envelope: raw}
				if err := duplex.VerifyReceipt(*r.frame.Receipt, *c.ep.Signer, sub, c.ep.RelayID, env.DomainID, env.SenderDeviceID, env.RecipientDeviceID); err != nil {
					return err
				}
			}
			return nil
		case "rejected":
			if r.frame.Code == "auth_required" {
				conn.cancel()
			}
			return &ErrRejected{Code: r.frame.Code}
		default:
			return fmt.Errorf("relay submission state %s", r.frame.State)
		}
	case <-ctx.Done():
		conn.forget(id)
		return ctx.Err()
	}
}

func (k *connection) send(f duplex.Frame, ch chan result) (string, error) {
	k.writeMu.Lock()
	defer k.writeMu.Unlock()
	k.request++
	id := strconv.FormatUint(k.request, 10)
	f.RequestID = id
	if ch != nil {
		k.mu.Lock()
		k.pending[id] = ch
		k.mu.Unlock()
	}
	if err := writeFrame(k.ws, f); err != nil {
		k.forget(id)
		k.cancel()
		return id, err
	}
	return id, nil
}

func (k *connection) forget(id string) {
	k.mu.Lock()
	delete(k.pending, id)
	k.mu.Unlock()
}

func (k *connection) failPending(err error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	for id, ch := range k.pending {
		ch <- result{err: err}
		delete(k.pending, id)
	}
	if k.reauthCh != nil {
		k.reauthCh <- err
		k.reauthCh = nil
	}
}

func (k *connection) receive(f duplex.Frame, onEnvelope func(json.RawMessage)) error {
	if f.ConnectionID != k.ready.ConnectionID || f.OwnerGeneration != k.ready.OwnerGeneration {
		return errors.New("relay frame owner mismatch")
	}
	switch f.Kind {
	case "closing":
		return fmt.Errorf("relay closed: %s", f.Code)
	case "delivery":
		if onEnvelope != nil {
			onEnvelope(append(json.RawMessage(nil), f.Envelope...))
		}
		return nil
	case "accepted", "rejected", "submit.status":
		k.mu.Lock()
		ch := k.pending[f.RequestID]
		delete(k.pending, f.RequestID)
		k.mu.Unlock()
		if ch != nil {
			ch <- result{frame: f}
		}
		return nil
	case "reauth.challenge":
		return k.answerReauth(f)
	case "reauthenticated":
		k.mu.Lock()
		if f.RequestID != k.reauthID || k.reauthCh == nil {
			k.mu.Unlock()
			return errors.New("unexpected reauthentication")
		}
		k.expires = duplex.Time(f.AuthorityExpiresAt)
		ch := k.reauthCh
		k.reauthCh = nil
		k.reauthID = ""
		k.mu.Unlock()
		ch <- nil
		return nil
	default:
		return fmt.Errorf("unexpected relay frame %s", f.Kind)
	}
}

func (k *connection) answerReauth(f duplex.Frame) error {
	k.mu.Lock()
	id, token := k.reauthID, k.reauthTok
	k.mu.Unlock()
	if id == "" || f.RequestID != id {
		return errors.New("reauthentication challenge mismatch")
	}
	f.RelayID = k.c.ep.RelayID
	f.HistoryID = k.ready.HistoryID
	f.Limits = k.ready.Limits
	h, err := duplex.AuthChallenge(f, "reauthenticate", id, k.ready.OwnerGeneration, token, k.c.device.Identity)
	if err != nil {
		return err
	}
	proof, err := k.c.device.CreateProof(k.c.provider, k.c.ep.RelayID, h, duplex.RandomID(), time.Now().UTC().Truncate(time.Second))
	if err != nil {
		return err
	}
	resp := duplex.Frame{V: 1, Kind: "reauthenticate", ConnectionID: k.ready.ConnectionID, OwnerGeneration: k.ready.OwnerGeneration, RequestID: id, ChallengeID: f.ChallengeID, AccessToken: token, Proof: &proof}
	k.writeMu.Lock()
	defer k.writeMu.Unlock()
	return writeFrame(k.ws, resp)
}

// maintain renews access before the connection's authority lapses and keeps
// the socket alive with pings.
func (k *connection) maintain(ctx context.Context) {
	tick := time.NewTicker(20 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		k.writeMu.Lock()
		err := k.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
		k.writeMu.Unlock()
		if err != nil {
			k.cancel()
			return
		}
		k.mu.Lock()
		remaining := time.Until(k.expires)
		k.mu.Unlock()
		if remaining > 2*time.Minute {
			continue
		}
		creds, err := k.c.ensureAccess(ctx, 3*time.Minute, true)
		if err != nil {
			if remaining < 10*time.Second {
				k.cancel()
				return
			}
			continue
		}
		done := make(chan error, 1)
		k.mu.Lock()
		k.reauthTok = creds.Access.Token
		k.reauthCh = done
		k.mu.Unlock()
		k.writeMu.Lock()
		k.request++
		id := strconv.FormatUint(k.request, 10)
		k.mu.Lock()
		k.reauthID = id
		k.mu.Unlock()
		err = writeFrame(k.ws, duplex.Frame{V: 1, Kind: "reauth.request", ConnectionID: k.ready.ConnectionID, OwnerGeneration: k.ready.OwnerGeneration, RequestID: id})
		k.writeMu.Unlock()
		if err != nil {
			k.cancel()
			return
		}
		select {
		case err := <-done:
			if err != nil {
				k.cancel()
				return
			}
		case <-time.After(15 * time.Second):
			k.cancel()
			return
		case <-ctx.Done():
			return
		}
	}
}

func readFrame(ws *websocket.Conn) (duplex.Frame, error) {
	kind, b, err := ws.ReadMessage()
	if err != nil {
		return duplex.Frame{}, err
	}
	if kind != websocket.TextMessage {
		return duplex.Frame{}, errors.New("relay requires text frames")
	}
	return duplex.Decode(b)
}

func writeFrame(ws *websocket.Conn, f duplex.Frame) error {
	b, err := duplex.Encode(f)
	if err != nil {
		return err
	}
	_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return ws.WriteMessage(websocket.TextMessage, b)
}

// Bind registers this device at the Relay through an operator (the console
// holds the Relay admin token). It is the console side of Rebinder.
func Bind(ctx context.Context, client *http.Client, baseURL, adminToken string, id identity.DeviceIdentity, proof identity.DeviceProof) (Credentials, error) {
	body, _ := json.Marshal(map[string]any{"identity": id, "proof": proof})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/v2/relay/devices/bind-self", bytes.NewReader(body))
	if err != nil {
		return Credentials{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)
	res, err := client.Do(req)
	if err != nil {
		return Credentials{}, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 64*1024))
	if res.StatusCode != http.StatusOK {
		return Credentials{}, fmt.Errorf("relay bind: HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out Credentials
	if err := json.Unmarshal(raw, &out); err != nil || out.Access.Token == "" {
		return Credentials{}, errors.New("relay bind: malformed response")
	}
	return out, nil
}

// Revoke revokes a device at the Relay with the operator token.
func Revoke(ctx context.Context, client *http.Client, baseURL, adminToken, deviceID string) error {
	body, _ := json.Marshal(map[string]string{"device_id": deviceID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/v2/relay/devices/revoke-access", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNoContent {
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return fmt.Errorf("relay revoke: HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}
