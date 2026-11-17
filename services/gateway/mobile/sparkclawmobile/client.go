package sparkclawmobile

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpbridge"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpnodeenroll"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

// Listener receives connection lifecycle callbacks on Go-owned threads.
// States include connecting, relay_ready, disconnected, verifying_relay,
// discovery_failed, stopped and authorization_revoked. Capabilities JSON is
// the negotiated transport admission (profile, limits, expiry).
type Listener interface {
	OnState(state string)
	OnCapabilities(capabilitiesJSON string)
}

// StreamListener receives one JSON event per call.
type StreamListener interface {
	OnEvent(eventJSON string, authorityValidForMillis int64)
}

// Client is one running workbench endpoint. Its Grant renews automatically
// while the endpoint runs; after the app was suspended past expiry, the next
// Open (or the running lifecycle) recovers with the enrolled device key.
type Client struct {
	endpoint     *iscpworkbench.Endpoint
	identity     string
	installation string
	binding      string
	bindMu       sync.Mutex
	bound        bool
	cancel       context.CancelFunc
	done         chan struct{}
	mu           sync.Mutex
	streams      map[string]context.CancelFunc
	closed       bool
}

// Open loads helperConfig (from Enroll) and starts the endpoint in the
// background. It returns an error when the device was revoked; the listener
// then also receives "authorization_revoked".
func Open(helperConfig string, listener Listener) (*Client, error) {
	if listener == nil {
		return nil, errors.New("listener is required")
	}
	if !iscpbridge.ValidIdentityKeyBackend(iscpbridge.IdentityKeyBackendHost) {
		return nil, errors.New("secret store is not registered")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	cfg, err := iscpworkbench.LoadRuntimeConfig(ctx, helperConfig)
	cancel()
	if err != nil {
		if errors.Is(err, iscpbridge.ErrAuthorizationRevoked) {
			listener.OnState("authorization_revoked")
			return nil, errors.New("authorization revoked")
		}
		return nil, errors.New("SparkClaw connection profile unavailable: " + err.Error())
	}
	if cfg.Role != iscpworkbench.RoleInitiator {
		return nil, errors.New("profile is not a client profile")
	}
	public, err := cfg.PublicIdentity()
	if err != nil {
		return nil, errors.New("device identity unavailable")
	}
	identity, _ := json.Marshal(public)
	endpoint, err := iscpworkbench.NewEndpoint(cfg, denyReverse, listener.OnState)
	if err != nil {
		return nil, errors.New("SparkClaw endpoint initialization failed")
	}
	runCtx, runCancel := context.WithCancel(context.Background())
	var binding []byte
	if cfg.Binding != nil {
		binding, _ = json.Marshal(map[string]string{"deployment_id": cfg.Binding.DeploymentID, "owner_id": cfg.Binding.OwnerID, "client_id": cfg.Binding.ClientID, "installation_id": cfg.ExpectedInstallationID})
	}
	c := &Client{endpoint: endpoint, identity: string(identity), installation: cfg.ExpectedInstallationID, binding: string(binding), cancel: runCancel, done: make(chan struct{}), streams: map[string]context.CancelFunc{}}
	endpoint.SetCapabilitiesHandler(func(caps iscpworkbench.TransportCapabilities) {
		raw, _ := json.Marshal(caps)
		// Like the desktop, bind this installation before any business call:
		// v2 operations are accepted only for the bound installation.
		go func() {
			if err := c.bindInstallation(runCtx); err != nil {
				if runCtx.Err() == nil {
					listener.OnState("installation_bind_failed")
				}
				return
			}
			if runCtx.Err() == nil {
				listener.OnCapabilities(string(raw))
			}
		}()
	})
	go func() {
		defer close(c.done)
		err := endpoint.Run(runCtx)
		if errors.Is(err, iscpbridge.ErrAuthorizationRevoked) {
			listener.OnState("authorization_revoked")
		} else if runCtx.Err() == nil {
			listener.OnState("stopped")
		}
	}()
	return c, nil
}

func (c *Client) bindInstallation(ctx context.Context) error {
	c.bindMu.Lock()
	defer c.bindMu.Unlock()
	if c.bound {
		return nil
	}
	if !iscpnodeenroll.ValidUUID(c.installation) {
		return errors.New("profile has no installation")
	}
	body, _ := json.Marshal(map[string]any{"schema_version": 1, "installation_id": c.installation})
	request := iscpworkbench.Request{Type: iscpworkbench.RequestType, Profile: iscpworkbench.ProfileV2, ID: iscpnodeenroll.NewUUID(), Operation: iscpworkbench.OperationBind, InstallationID: c.installation, Body: body}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	response, err := c.endpoint.Call(ctx, request)
	if err != nil {
		return err
	}
	if response.Status != 200 {
		return errors.New("installation bind rejected: " + response.Error)
	}
	c.bound = true
	return nil
}

// Phone clients serve no reverse (host-command) operations.
func denyReverse(_ context.Context, r iscpworkbench.Request) iscpworkbench.Response {
	return iscpworkbench.Response{Type: iscpworkbench.ResponseType, Profile: r.Profile, ID: r.ID, Status: 403, Code: iscpworkbench.ErrorPermissionDenied, Error: "reverse operation denied"}
}

// IdentityJSON returns this device's public identity.
func (c *Client) IdentityJSON() string { return c.identity }

// Call sends one v2 workbench operation and blocks until its response. params
// is a JSON object of string values (may be empty); body is a JSON value or
// empty; operationID is a UUID for idempotent mutations or empty. The returned
// JSON is the workbench response (status, error_code, retryable, body). Only
// invalid input returns an error; transport failures are typed responses.
func (c *Client) Call(operation, paramsJSON, bodyJSON, operationID string, timeoutMillis int64) (string, error) {
	request, err := buildRequest(operation, paramsJSON, bodyJSON, operationID, c.installation)
	if err != nil {
		return "", err
	}
	if request.Operation == iscpworkbench.OperationExecutionProgressStream {
		return "", errors.New("stream operation requires StreamCall")
	}
	ctx, cancel := callContext(timeoutMillis)
	defer cancel()
	response, err := c.endpoint.Call(ctx, request)
	if err != nil {
		response = failedResponse(request, err, "ISCP workbench call failed")
	}
	return encodeResponse(response)
}

// StreamCall runs execution.progress.stream. streamID identifies the stream
// for CancelStream and is also the request ID (UUID). It blocks until the
// stream ends and returns the final response JSON.
func (c *Client) StreamCall(streamID, paramsJSON, bodyJSON string, timeoutMillis int64, listener StreamListener) (string, error) {
	if listener == nil {
		return "", errors.New("stream listener is required")
	}
	request, err := buildRequest(iscpworkbench.OperationExecutionProgressStream, paramsJSON, bodyJSON, "", c.installation)
	if err != nil {
		return "", err
	}
	request.ID = streamID
	if err := request.Validate(); err != nil {
		return "", errors.New("invalid stream request")
	}
	return c.runStream(streamID, request, timeoutMillis, listener)
}

func (c *Client) runStream(streamID string, request iscpworkbench.Request, timeoutMillis int64, listener StreamListener) (string, error) {
	ctx, cancel := callContext(timeoutMillis)
	defer cancel()
	c.mu.Lock()
	if c.closed || c.streams[streamID] != nil {
		c.mu.Unlock()
		return "", errors.New("client closed or stream identifier in use")
	}
	c.streams[streamID] = cancel
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.streams, streamID); c.mu.Unlock() }()
	response, err := c.endpoint.StreamCall(ctx, request, func(event json.RawMessage) error {
		remaining, err := c.endpoint.StreamAuthorityRemaining(request.ID)
		if err != nil {
			return err
		}
		listener.OnEvent(string(event), remaining.Milliseconds())
		return nil
	})
	if err != nil {
		response = failedResponse(request, err, "ISCP stream unavailable")
		if errors.Is(err, iscpworkbench.ErrStreamAuthorityExpired) {
			response.Status, response.Code, response.Retryable = 403, iscpworkbench.ErrorGrantExpired, false
		}
	}
	return encodeResponse(response)
}

// BindingJSON returns {"deployment_id","owner_id","client_id","installation_id"}
// for request bodies that must name the bound client (execution.submit).
func (c *Client) BindingJSON() string { return c.binding }

// envelope carries the request fields that live outside params and body.
type envelope struct {
	Params           map[string]string `json:"params,omitempty"`
	OperationID      string            `json:"operation_id,omitempty"`
	RequestID        string            `json:"request_id,omitempty"`
	InputDigest      string            `json:"input_digest,omitempty"`
	ExpectedRevision string            `json:"expected_revision,omitempty"`
}

func requestWithEnvelope(operation, envelopeJSON, bodyJSON, installation string) (iscpworkbench.Request, error) {
	var e envelope
	if envelopeJSON != "" {
		decoder := json.NewDecoder(strings.NewReader(envelopeJSON))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&e); err != nil {
			return iscpworkbench.Request{}, errors.New("envelope must be a JSON object with params, operation_id, request_id, input_digest or expected_revision")
		}
	}
	request, err := buildRequest(operation, "", bodyJSON, e.OperationID, installation)
	if err != nil && !strings.Contains(err.Error(), "invalid workbench request") {
		return request, err
	}
	request.Params, request.RequestID, request.InputDigest, request.ExpectedRevision = e.Params, e.RequestID, e.InputDigest, e.ExpectedRevision
	if err := request.Validate(); err != nil {
		return request, errors.New("invalid workbench request: " + err.Error())
	}
	return request, nil
}

// CallEnvelope is Call with request-envelope fields. envelopeJSON may hold
// params (object of strings), operation_id, request_id, input_digest and
// expected_revision. bodyJSON is sent byte-for-byte, so input_digest may be the
// SHA-256 of exactly these bytes.
func (c *Client) CallEnvelope(operation, envelopeJSON, bodyJSON string, timeoutMillis int64) (string, error) {
	request, err := requestWithEnvelope(operation, envelopeJSON, bodyJSON, c.installation)
	if err != nil {
		return "", err
	}
	if request.Operation == iscpworkbench.OperationExecutionProgressStream {
		return "", errors.New("stream operation requires StreamCallEnvelope")
	}
	ctx, cancel := callContext(timeoutMillis)
	defer cancel()
	response, err := c.endpoint.Call(ctx, request)
	if err != nil {
		response = failedResponse(request, err, "ISCP workbench call failed")
	}
	return encodeResponse(response)
}

// StreamCallEnvelope runs execution.progress.stream for envelope.request_id.
// streamID must be a fresh UUID different from the request ID.
func (c *Client) StreamCallEnvelope(streamID, envelopeJSON, bodyJSON string, timeoutMillis int64, listener StreamListener) (string, error) {
	if listener == nil {
		return "", errors.New("stream listener is required")
	}
	request, err := requestWithEnvelope(iscpworkbench.OperationExecutionProgressStream, envelopeJSON, bodyJSON, c.installation)
	if err != nil {
		return "", err
	}
	request.ID = streamID
	if err := request.Validate(); err != nil || request.RequestID == "" || request.RequestID == streamID {
		return "", errors.New("invalid stream request: stream ID must be a UUID different from request_id")
	}
	return c.runStream(streamID, request, timeoutMillis, listener)
}

// CancelStream stops a running StreamCall.
func (c *Client) CancelStream(streamID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cancel := c.streams[streamID]; cancel != nil {
		cancel()
	}
}

// Close stops the endpoint. Call it when the app goes to background for long
// periods; Open again on resume (expired Grants recover automatically).
func (c *Client) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	for _, cancel := range c.streams {
		cancel()
	}
	c.mu.Unlock()
	c.cancel()
	_ = c.endpoint.Close()
	<-c.done
}

func callContext(timeoutMillis int64) (context.Context, context.CancelFunc) {
	if timeoutMillis <= 0 || timeoutMillis > 10*60*1000 {
		timeoutMillis = 30000
	}
	return context.WithTimeout(context.Background(), time.Duration(timeoutMillis)*time.Millisecond)
}

func buildRequest(operation, paramsJSON, bodyJSON, operationID, installation string) (iscpworkbench.Request, error) {
	request := iscpworkbench.Request{Type: iscpworkbench.RequestType, Profile: iscpworkbench.ProfileV2, ID: iscpnodeenroll.NewUUID(), Operation: operation, OperationID: operationID, InstallationID: installation}
	if paramsJSON != "" {
		if err := json.Unmarshal([]byte(paramsJSON), &request.Params); err != nil {
			return request, errors.New("params must be a JSON object of strings")
		}
	}
	if bodyJSON != "" {
		if len(bodyJSON) > iscpworkbench.MaxRequestBytes || !json.Valid([]byte(bodyJSON)) {
			return request, errors.New("body must be valid JSON within the request limit")
		}
		request.Body = json.RawMessage(bodyJSON)
	}
	if err := request.Validate(); err != nil {
		return request, errors.New("invalid workbench request: " + err.Error())
	}
	return request, nil
}

func failedResponse(request iscpworkbench.Request, err error, message string) iscpworkbench.Response {
	response := iscpworkbench.Response{Type: iscpworkbench.ResponseType, Profile: request.Profile, ID: request.ID, Status: 503, Error: message, Code: iscpworkbench.ErrorCapabilityUnavailable, Retryable: true}
	var transportError *iscpworkbench.TransportError
	if errors.As(err, &transportError) {
		response.Status, response.Code, response.Retryable = transportError.Status, transportError.Code, transportError.Retryable
	}
	var unknown *iscpbridge.UnknownAcceptance
	if errors.As(err, &unknown) {
		// The request may have reached SparkClaw. Never retry it as new work;
		// reconcile with the same operation ID instead.
		response.Status, response.Code, response.Retryable = 503, iscpworkbench.ErrorOutcomeUnknown, false
		response.Error = "ISCP transport outcome is unknown"
	}
	return response
}

func encodeResponse(response iscpworkbench.Response) (string, error) {
	raw, err := json.Marshal(response)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
