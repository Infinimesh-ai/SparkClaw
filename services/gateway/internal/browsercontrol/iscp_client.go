package browsercontrol

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// Only a transport failure proved to precede submission may use this sentinel.
// HTTP/ISCP status alone, after a send, is never evidence that a page was untouched.
var ErrControllerNotSent = errors.New("client Controller request was not sent")

type ISCPControllerTransport interface {
	ControllerCall(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

type ControllerIntent struct {
	CommandID string
	Digest    string
	Binding   json.RawMessage
	Operation string
	Mutation  bool
}
type ControllerFenceHooks struct {
	Before func(context.Context, ControllerIntent) error
	After  func(context.Context, ControllerIntent, string) error
}
type ControllerWireLimits struct {
	ResponseBytes    int `json:"response_bytes"`
	TimeoutMS        int `json:"timeout_ms"`
	WireBytes        int `json:"wire_bytes"`
	CacheBytes       int `json:"cache_bytes"`
	ChunkBytes       int `json:"chunk_bytes"`
	ChunkConcurrency int `json:"chunk_concurrency"`
}
type ClientControllerStatus struct {
	SchemaVersion        int                  `json:"schema_version"`
	Protocol             string               `json:"protocol"`
	Available            bool                 `json:"available"`
	ProcessState         string               `json:"process_state"`
	Incarnation          string               `json:"supervisor_incarnation"`
	ControllerGeneration int64                `json:"controller_generation"`
	Versions             map[string]string    `json:"versions"`
	Limits               ControllerWireLimits `json:"limits"`
}
type controllerResultDescriptor struct {
	ResultID   string    `json:"result_id"`
	TotalBytes int       `json:"total_bytes"`
	SHA256     string    `json:"sha256"`
	ExpiresAt  time.Time `json:"expires_at"`
}
type controllerReply struct {
	SchemaVersion int                         `json:"schema_version"`
	CommandID     string                      `json:"command_id"`
	Digest        string                      `json:"digest"`
	Binding       json.RawMessage             `json:"binding"`
	Incarnation   string                      `json:"supervisor_incarnation"`
	Outcome       string                      `json:"outcome"`
	ErrorCode     string                      `json:"error_code,omitempty"`
	Payload       json.RawMessage             `json:"payload,omitempty"`
	Result        *controllerResultDescriptor `json:"result,omitempty"`
}

// One client is bound to one trusted outer host binding and one ISCP session.
// It never reads the backend extension vault, changes routes, or retries writes.
type ISCPControllerClient struct {
	transport   ISCPControllerTransport
	binding     json.RawMessage
	revision    int64
	hooks       ControllerFenceHooks
	mu          sync.Mutex
	lease       SessionLease
	incarnation string
	limits      ControllerWireLimits
}

func NewISCPControllerClient(transport ISCPControllerTransport, binding json.RawMessage, revision uint64, hooks ControllerFenceHooks) (*ISCPControllerClient, error) {
	if transport == nil || !json.Valid(binding) || len(binding) > 8192 || revision == 0 || revision > maxJSSafeInteger || hooks.Before == nil || hooks.After == nil {
		return nil, newError(CodeInvalidRequest, false, nil)
	}
	return &ISCPControllerClient{transport: transport, binding: append(json.RawMessage(nil), binding...), revision: int64(revision), hooks: hooks}, nil
}

func (c *ISCPControllerClient) ControllerStatus(ctx context.Context) (ClientControllerStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, err := c.transport.ControllerCall(ctx, "browser.controller.status", json.RawMessage(`{"schema_version":1}`))
	if err != nil {
		return ClientControllerStatus{}, newError(CodeControllerUnavailable, true, err)
	}
	defer zero(raw)
	var status ClientControllerStatus
	if len(raw) > controllerWireLimit || strictControllerJSON(raw, &status) != nil || status.SchemaVersion != 1 || status.Protocol != ClientControllerProtocol ||
		status.Limits.ResponseBytes != maxControllerExecutionResponseBytes || status.Limits.TimeoutMS < 1 || status.Limits.TimeoutMS > 25000 ||
		status.Limits.CacheBytes != 20<<20 || status.Limits.WireBytes < 4096 || status.Limits.WireBytes > controllerWireLimit ||
		status.Limits.ChunkBytes < 1 || status.Limits.ChunkBytes > controllerChunkLimit || status.Limits.ChunkConcurrency < 1 || status.Limits.ChunkConcurrency > 4 ||
		status.Versions["@playwright/mcp"] != "0.0.80" || status.Versions["playwright"] != "1.63.0-alpha-2026-08-31" || status.Versions["playwright-core"] != "1.63.0-alpha-2026-08-31" {
		return ClientControllerStatus{}, invalidControllerResponse()
	}
	switch status.ProcessState {
	case "stopped", "starting", "ready", "busy", "stopping", "unavailable":
	default:
		return ClientControllerStatus{}, invalidControllerResponse()
	}
	if status.ControllerGeneration < 0 || status.ControllerGeneration > maxJSSafeInteger || status.Incarnation != "" && !controllerIncarnation.MatchString(status.Incarnation) {
		return ClientControllerStatus{}, invalidControllerResponse()
	}
	c.mu.Lock()
	c.limits = status.Limits
	c.mu.Unlock()
	return status, nil
}

func (c *ISCPControllerClient) Acquire(ctx context.Context, input AcquireRequest, token []byte) (SessionLease, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if input.ProfileID != ClientControllerProfile || input.CredentialGeneration != c.revision || len(token) != 0 || input.WaitTimeoutMS < 0 || input.WaitTimeoutMS > 30000 || input.SessionTTLMS < 1 || input.SessionTTLMS > 600000 {
		return SessionLease{}, newError(CodeInvalidRequest, false, nil)
	}
	status, err := c.ControllerStatus(ctx)
	if err != nil {
		return SessionLease{}, err
	}
	if !status.Available {
		return SessionLease{}, newError(CodeControllerUnavailable, true, nil)
	}
	raw, incarnation, err := c.call(ctx, "acquire", map[string]any{"wait_timeout_ms": input.WaitTimeoutMS, "session_ttl_ms": input.SessionTTLMS}, true)
	if err != nil {
		return SessionLease{}, err
	}
	defer zero(raw)
	var lease SessionLease
	if strictControllerJSON(raw, &lease) != nil || lease.SchemaVersion != 1 || lease.State != "acquired" || lease.ProfileID != ClientControllerProfile || lease.Lane != "mcp" ||
		!controllerID.MatchString(lease.SessionID) || lease.CredentialGeneration != c.revision || !safeGeneration(lease.ControllerGeneration) || !safeGeneration(lease.SessionGeneration) ||
		!safeGeneration(lease.PageGeneration) || !time.Now().Before(lease.ExpiresAt) || lease.ExpiresAt.After(time.Now().Add(time.Duration(input.SessionTTLMS)*time.Millisecond+time.Second)) {
		return SessionLease{}, newError(CodeResultUnavailable, false, nil)
	}
	c.mu.Lock()
	c.lease, c.incarnation = lease, incarnation
	c.mu.Unlock()
	return lease, nil
}

func (c *ISCPControllerClient) Execute(ctx context.Context, input ExecuteRequest) (ExecutionResult, error) {
	mutation, err := ClientControllerOperation(input.Operation, input.Arguments)
	if err != nil {
		return ExecutionResult{}, err
	}
	c.mu.Lock()
	lease := c.lease
	incarnation := c.incarnation
	c.mu.Unlock()
	if input.Lease != lease || lease.SessionID == "" {
		return ExecutionResult{}, newError(CodeSessionStale, false, nil)
	}
	raw, replyIncarnation, err := c.call(ctx, "execute", map[string]any{"lease": lease, "operation": input.Operation, "arguments": input.Arguments}, mutation)
	if err != nil {
		return ExecutionResult{}, err
	}
	defer zero(raw)
	var result ExecutionResult
	wantPageGeneration := lease.PageGeneration
	if mutation {
		wantPageGeneration++
	}
	if strictControllerJSON(raw, &result) != nil || replyIncarnation != incarnation || result.SchemaVersion != 1 || result.State != "completed" || result.ProfileID != lease.ProfileID ||
		result.Lane != lease.Lane || result.SessionID != lease.SessionID || result.CredentialGeneration != lease.CredentialGeneration || result.ControllerGeneration != lease.ControllerGeneration ||
		result.SessionGeneration != lease.SessionGeneration || result.PageGeneration != wantPageGeneration || result.Operation != input.Operation || len(result.Result) == 0 || !json.Valid(result.Result) {
		return ExecutionResult{}, newError(CodeResultUnavailable, false, nil)
	}
	c.mu.Lock()
	c.lease.PageGeneration = result.PageGeneration
	c.mu.Unlock()
	return result, nil
}

func (c *ISCPControllerClient) Release(ctx context.Context, input ReleaseRequest) (ReleaseResult, error) {
	c.mu.Lock()
	lease := c.lease
	c.mu.Unlock()
	if input.ProfileID != ClientControllerProfile || input.SessionID != lease.SessionID || input.ControllerGeneration != lease.ControllerGeneration || input.SessionGeneration != lease.SessionGeneration {
		return ReleaseResult{}, newError(CodeSessionStale, false, nil)
	}
	raw, _, err := c.call(ctx, "release", map[string]any{"lease": lease}, true)
	if err != nil {
		return ReleaseResult{}, err
	}
	defer zero(raw)
	var result ReleaseResult
	if strictControllerJSON(raw, &result) != nil || result.SchemaVersion != 1 || result.State != "released" || result.ProfileID != lease.ProfileID || result.ControllerGeneration != lease.ControllerGeneration || result.SessionGeneration != lease.SessionGeneration {
		return ReleaseResult{}, newError(CodeResultUnavailable, false, nil)
	}
	c.mu.Lock()
	c.lease = SessionLease{}
	c.mu.Unlock()
	return result, nil
}

func (c *ISCPControllerClient) call(ctx context.Context, action string, fields map[string]any, mutation bool) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	timeoutMS := min(int64(25000), time.Until(deadline).Milliseconds())
	if timeoutMS < 1 || ctx.Err() != nil {
		return nil, "", newError(CodeControllerUnavailable, true, ErrControllerNotSent)
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, "", err
	}
	operation := "browser.controller." + action
	body := map[string]any{"schema_version": 1, "command_id": "cmd_" + hex.EncodeToString(random[:]), "digest": "", "binding": c.binding, "timeout_ms": timeoutMS}
	for key, value := range fields {
		body[key] = value
	}
	digest, err := ControllerRequestDigest(operation, body)
	if err != nil {
		return nil, "", newError(CodeInvalidRequest, false, err)
	}
	body["digest"] = digest
	encoded, err := json.Marshal(body)
	c.mu.Lock()
	limits := c.limits
	expectedIncarnation := c.incarnation
	c.mu.Unlock()
	if err != nil || limits.WireBytes < 4096 || len(encoded)+1024 > limits.WireBytes {
		return nil, "", newError(CodeInvalidRequest, false, err)
	}
	defer zero(encoded)
	intent := ControllerIntent{CommandID: body["command_id"].(string), Digest: digest, Binding: c.binding, Operation: operation, Mutation: mutation}
	if mutation {
		if err := c.hooks.Before(ctx, intent); err != nil {
			return nil, "", err
		}
	}
	finish := func(outcome string) error {
		if !mutation {
			return nil
		}
		persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return c.hooks.After(persistCtx, intent, outcome)
	}
	raw, err := c.transport.ControllerCall(ctx, operation, encoded)
	if err != nil {
		outcome := "unknown"
		if errors.Is(err, ErrControllerNotSent) {
			outcome = "not_dispatched"
		}
		_ = finish(outcome)
		if mutation && outcome == "unknown" {
			return nil, "", newError(CodeWriteUnknown, false, err)
		}
		return nil, "", newError(CodeControllerUnavailable, !mutation, err)
	}
	defer zero(raw)
	var reply controllerReply
	if len(raw) > limits.WireBytes || strictControllerJSON(raw, &reply) != nil || reply.SchemaVersion != 1 || reply.CommandID != intent.CommandID || reply.Digest != digest || !sameControllerJSON(reply.Binding, c.binding) ||
		(reply.Outcome != "not_dispatched" && reply.Outcome != "failed" && reply.Outcome != "unknown" && reply.Outcome != "completed") ||
		(action != "acquire" && reply.Outcome == "completed" && reply.ErrorCode == "" && reply.Incarnation != expectedIncarnation) ||
		(reply.Incarnation != "" && !controllerIncarnation.MatchString(reply.Incarnation)) || (reply.Outcome == "completed" && reply.ErrorCode == "" && !controllerIncarnation.MatchString(reply.Incarnation)) {
		_ = finish("unknown")
		if mutation {
			return nil, "", newError(CodeWriteUnknown, false, nil)
		}
		return nil, "", invalidControllerResponse()
	}
	// Persist the effect before any payload download. A missing chunk must never
	// convert a verified action into an automatic retry or a new command ID.
	if err := finish(reply.Outcome); err != nil {
		return nil, "", newError(CodeWriteUnknown, false, err)
	}
	if reply.Outcome != "completed" {
		if reply.Outcome == "unknown" {
			return nil, "", newError(CodeWriteUnknown, false, nil)
		}
		code := CodeControllerUnavailable
		switch reply.ErrorCode {
		case "browser_busy":
			code = CodeBusy
		case "browser_session_stale":
			code = CodeSessionStale
		case "browser_page_stale":
			code = CodePageStale
		case "browser_write_outcome_unknown":
			code = CodeWriteUnknown
		case "browser_result_capacity":
			code = CodeCleanupPending
		}
		return nil, "", newError(code, false, nil)
	}
	if reply.ErrorCode != "" || (len(reply.Payload) == 0) == (reply.Result == nil) {
		return nil, reply.Incarnation, newError(CodeResultUnavailable, false, nil)
	}
	if len(reply.Payload) > 0 {
		return append([]byte(nil), reply.Payload...), reply.Incarnation, nil
	}
	payload, err := c.fetchResult(ctx, intent, reply.Result, limits)
	return payload, reply.Incarnation, err
}

func safeGeneration(value int64) bool { return value > 0 && value <= maxJSSafeInteger }
func (*ISCPControllerClient) ValidateToken(context.Context, string, []byte) (ValidationResult, error) {
	return ValidationResult{}, newError(CodeOperationUnavailable, false, nil)
}
func (*ISCPControllerClient) RunScript(context.Context, RunScriptRequest, []byte) (ScriptExecutionResult, error) {
	return ScriptExecutionResult{}, newError(CodeOperationUnavailable, false, nil)
}
func (*ISCPControllerClient) OpenProviderLogin(context.Context, OpenProviderLoginRequest) (OpenProviderLoginResult, error) {
	return OpenProviderLoginResult{}, newError(CodeOperationUnavailable, false, nil)
}

var _ ControllerClient = (*ISCPControllerClient)(nil)
