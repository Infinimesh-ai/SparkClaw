package iscpworkbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpbridge"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpnodeclient"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpnodeissuer"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/latencytrace"
)

func (e *Endpoint) acceptResponse(ctx context.Context, raw []byte, id string, arrived time.Time) error {
	// Decryption, sequence advancement and structural validation remain on the
	// receive owner. Only a verified response for an original pending call may
	// start a fresh authorization worker; duplicates cannot create more work.
	if handled, err := e.acceptStreamCompletion(raw, id); handled {
		return err
	}
	var response Response
	if err := strictDecode(raw, &response); err != nil {
		latencytrace.RecordDetail(ctx, "rpc.response_invalid", arrived, err, latencytrace.Detail{Reason: "decode"})
		return errors.New("invalid workbench response")
	}
	if err := response.Validate(); err != nil {
		latencytrace.RecordDetail(ctx, "rpc.response_invalid", arrived, err, latencytrace.Detail{Reason: "validation"})
		return err
	}
	ctx = latencytrace.WithFields(ctx, latencytrace.Fields{Role: e.config.Role, RPCID: response.ID})
	e.mu.Lock()
	s, call := e.session, e.pending[response.ID]
	if call != nil {
		ctx = latencytrace.WithFields(ctx, call.trace)
	}
	latencytrace.RecordDetail(ctx, "rpc.response_received", time.Time{}, nil, responseTraceDetail(response))
	if e.closed || e.authorizationRevoked || s == nil || s.id != id || ctx.Err() != nil || call == nil || call.ctx.Err() != nil || call.responseStarted {
		reason := "session_ended"
		switch {
		case call == nil:
			reason = "no_pending_call"
		case call.ctx.Err() != nil:
			reason = "call_ended"
		case call.responseStarted:
			reason = "duplicate"
		}
		e.mu.Unlock()
		latencytrace.RecordDetail(ctx, "rpc.response_dropped", time.Time{}, nil, latencytrace.Detail{Reason: reason})
		return nil
	}
	call.responseStarted = true
	latencytrace.At(ctx, "rpc.encrypted_received", arrived)
	e.workers.Add(1)
	e.mu.Unlock()
	go e.authorizeResponse(ctx, s, call, response)
	return nil
}

func (e *Endpoint) authorizeResponse(ctx context.Context, s *activeSession, call *pendingCall, response Response) {
	defer e.workers.Done()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(call.ctx, cancel)
	defer func() { stop(); cancel() }()
	started := latencytrace.Clock()
	err := e.checkAuthorization(ctx)
	traceAuthorization(ctx, "rpc.response_authorization", started, err)
	if err != nil && !started.IsZero() {
		latencytrace.Record(ctx, "rpc.response_authorization."+responseAuthorizationErrorClass(err), time.Time{}, err, 0)
	}
	e.mu.Lock()
	// The pending object owns its original channel and deadline. Both it and
	// the original session must still match: a stale worker cannot complete a
	// reused RPC ID or disconnect a freshly authenticated replacement session.
	if e.closed || e.authorizationRevoked || ctx.Err() != nil || call.ctx.Err() != nil || e.session != s || e.pending[response.ID] != call {
		e.mu.Unlock()
		latencytrace.RecordDetail(ctx, "rpc.response_dropped", time.Time{}, nil, latencytrace.Detail{Reason: "call_or_session_ended_during_authorization"})
		return
	}
	if err == nil {
		err = verifyGrant(e.config, e.grantMaterial(), time.Now().UTC())
		if err != nil {
			latencytrace.RecordDetail(ctx, "rpc.response_dropped", time.Time{}, err, latencytrace.Detail{Reason: "grant_validation"})
		}
	}
	if err != nil {
		if cause := callContextError(err); cause != nil {
			// A fresh authorization check that times out still denies this
			// result. Other calls must perform their own checks; signed
			// revocation remains terminal in observeAuthorizationError.
			delete(e.pending, response.ID)
			call.result <- callResult{err: fmt.Errorf("workbench response authorization: %w", cause)}
			e.mu.Unlock()
			return
		}
		// Keep transient authorization failures fail-closed without turning them
		// into revocation. The existing receive owner establishes fresh keys.
		e.resetLocked()
		e.mu.Unlock()
		e.setStateForSession("disconnected", nil, true)
		return
	}
	delete(e.pending, response.ID)
	call.result <- callResult{response: response}
	latencytrace.RecordDetail(ctx, "rpc.delivered", time.Time{}, nil, responseTraceDetail(response))
	e.mu.Unlock()
}

func responseTraceDetail(response Response) latencytrace.Detail {
	detail := latencytrace.Detail{HTTPStatus: response.Status}
	if validErrorCode(response.Code) {
		detail.Reason = string(response.Code)
	}
	return detail
}

func traceAuthorization(ctx context.Context, stage string, start time.Time, err error) {
	detail := latencytrace.Detail{}
	if err != nil && !start.IsZero() {
		detail.Reason = responseAuthorizationErrorClass(err)
	}
	latencytrace.RecordDetail(ctx, stage, start, err, detail)
}

func traceRelayOutcome(ctx context.Context, stage string, start time.Time, err error) {
	detail := latencytrace.Detail{}
	var notSent *iscpbridge.NotSent
	var rejected *iscpbridge.RejectedSubmission
	var unknown *iscpbridge.UnknownAcceptance
	switch {
	case errors.As(err, &notSent):
		detail.Reason = "not_sent"
	case errors.As(err, &rejected):
		detail.Reason = "relay_rejected"
	case errors.As(err, &unknown):
		detail.Reason = "acceptance_unknown"
	case err != nil:
		detail.Reason = "relay_transport"
	}
	latencytrace.RecordDetail(ctx, stage, start, err, detail)
}

// Use only stable classes and numeric HTTP statuses in the private trace.
// Error strings can include request context and must never be recorded.
func responseAuthorizationErrorClass(err error) string {
	var exchange *iscpnodeclient.ExchangeError
	switch {
	case errors.As(err, &exchange):
		if exchange.StatusCode >= 100 && exchange.StatusCode <= 599 {
			return "issuer_http_" + strconv.Itoa(exchange.StatusCode)
		}
		return "issuer_transport"
	case errors.Is(err, iscpbridge.ErrAuthorizationRevoked):
		return "revoked"
	case errors.Is(err, iscpnodeissuer.ErrDenied):
		return "denied"
	case errors.Is(err, iscpnodeissuer.ErrInvalid):
		return "invalid"
	case errors.Is(err, iscpnodeissuer.ErrConflict):
		return "conflict"
	case errors.Is(err, iscpnodeissuer.ErrExpired):
		return "expired"
	case errors.Is(err, iscpnodeissuer.ErrUnavailable):
		return "unavailable"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "unclassified"
	}
}

func (e *Endpoint) acceptRequest(ctx context.Context, raw []byte, id string) error {
	var request Request
	if err := strictDecode(raw, &request); err != nil {
		latencytrace.RecordDetail(ctx, "rpc.request_invalid", time.Time{}, err, latencytrace.Detail{Reason: "decode"})
		return errors.New("invalid workbench request")
	}
	if err := request.Validate(); err != nil {
		latencytrace.RecordDetail(ctx, "rpc.request_invalid", time.Time{}, err, latencytrace.Detail{Reason: "validation"})
		return err
	}
	if request.Operation == OperationExecutionProgressStream {
		return e.acceptStreamRequest(ctx, request, id)
	}
	ctx = latencytrace.WithFields(ctx, requestTraceFields(e.config.Role, request))
	latencytrace.Record(ctx, "rpc.request_received", time.Time{}, nil, 0)
	reject := func(reason string, err error) error {
		latencytrace.RecordDetail(ctx, "rpc.request_rejected", time.Time{}, err, latencytrace.Detail{Reason: reason})
		return err
	}
	if e.config.Mode == "node-managed" && e.config.ExpectedInstallationID != "" {
		if request.InstallationID != "" && request.InstallationID != e.config.ExpectedInstallationID {
			return reject("installation_mismatch", errors.New("request installation differs from enrolled device"))
		}
		if request.Operation == OperationBind {
			var b struct {
				SchemaVersion  int    `json:"schema_version"`
				InstallationID string `json:"installation_id"`
			}
			if strictDecode(request.Body, &b) != nil || b.InstallationID != e.config.ExpectedInstallationID {
				return reject("installation_mismatch", errors.New("installation bind differs from enrolled device"))
			}
		}
	}
	e.mu.Lock()
	if e.session == nil || e.session.id != id {
		e.mu.Unlock()
		return reject("session_replaced", errors.New("request session was replaced"))
	}
	profile := Profile
	if e.session.capabilities != nil {
		profile = e.session.capabilities.Profile
	}
	spec, _ := LookupOperation(request.Operation)
	if (request.Profile == ProfileV2 && (profile != ProfileV2 || e.session.capabilities == nil || !time.Now().Before(e.session.capabilities.ExpiresAt))) || (e.config.Role == RoleInitiator && spec.Direction != "reverse") || (e.config.Role == RoleResponder && spec.Direction != "forward") {
		e.mu.Unlock()
		return reject("direction_or_profile", errors.New("operation not admitted by negotiated direction/profile"))
	}
	if (connectionSettingsOperation(request.Operation) || browserControllerOperation(request.Operation) || MCDSelectionOperation(request.Operation) || MCDCheckoutOperation(request.Operation)) && (e.session.capabilities == nil || !slices.Contains(e.session.capabilities.Operations, request.Operation)) {
		e.mu.Unlock()
		return reject("not_negotiated", errors.New("application extension operation was not negotiated"))
	}
	info := SessionInfo{SessionID: id, Profile: profile, GrantRevision: e.grantMaterial().grant.RevocationEpoch, Binding: e.config.Binding, QualifiedOperations: slices.Clone(e.config.QualifiedCapabilities)}
	if e.session.capabilities != nil {
		info.NegotiatedOperations = slices.Clone(e.session.capabilities.Operations)
	}
	if _, ok := e.session.seenRequests[request.ID]; ok {
		e.mu.Unlock()
		return reject("rpc_replay", errors.New("replayed workbench request ID"))
	}
	e.session.seenRequests[request.ID] = struct{}{}
	e.mu.Unlock()
	slots := e.operationSlots(request)
	if slots != nil {
		select {
		case slots <- struct{}{}:
		default:
			return e.sendResponse(ctx, id, Response{Profile: request.Profile, ID: request.ID, Status: 429, Code: ErrorThrottled, Retryable: true, RetryAfterMS: 250, Error: "workbench concurrency limit exceeded"})
		}
	}
	e.workers.Add(1)
	go func() {
		defer e.workers.Done()
		defer func() {
			if slots != nil {
				<-slots
			}
		}()
		callCtx, cancel := context.WithTimeout(ctx, operationTimeout(request.Operation))
		defer cancel()
		started := latencytrace.Clock()
		policy, policyErr := e.AuthorizationPolicy(callCtx)
		traceAuthorization(callCtx, "rpc.request_authorization", started, policyErr)
		if policyErr != nil {
			return
		}
		if negotiationRequest(request) && e.config.Role == RoleResponder {
			_ = e.answerNegotiation(callCtx, id, request)
			return
		}
		info.Scopes = slices.Clone(policy.Scopes)
		info.CheckAuthorization = e.AuthorizationPolicy
		callCtx = context.WithValue(callCtx, sessionInfoKey{}, info)
		if request.Profile == ProfileV2 {
			callCtx = context.WithValue(callCtx, capacityContextKey{}, spec.CapacityClass)
		}
		response := Response{Status: 501, Code: ErrorCapabilityUnavailable, Error: "operation handler unavailable"}
		if e.handler != nil {
			started := latencytrace.Clock()
			response = e.handler(callCtx, request)
			latencytrace.RecordDetail(callCtx, "rpc.handler", started, callCtx.Err(), responseTraceDetail(response))
		}
		response.Profile = request.Profile
		response.ID = request.ID
		if callCtx.Err() != nil {
			return
		}
		_ = e.sendResponse(callCtx, id, response)
	}()
	return nil
}

func (e *Endpoint) sendResponse(ctx context.Context, id string, response Response) (resultErr error) {
	started := latencytrace.Clock()
	defer func() {
		latencytrace.RecordDetail(ctx, "rpc.response_send", started, resultErr, responseTraceDetail(response))
	}()
	response.Type = ResponseType
	if response.Profile == "" {
		response.Profile = Profile
	}
	if response.Profile == Profile {
		response.Code = ""
		response.Retryable = false
		response.RetryAfterMS = 0
		response.Object = nil
	} else if response.Status >= 400 && response.Code == "" {
		response.Code = CodeForStatus(response.Status)
	}
	if response.Validate() != nil {
		response.Body = nil
		response.Status = 500
		response.Error = "invalid workbench handler response"
	}
	raw, err := json.Marshal(response)
	if err != nil {
		return errors.New("encode workbench response")
	}
	if len(raw) > MaxResponseBytes {
		response.Body = nil
		response.Status = 413
		if response.Profile == ProfileV2 {
			response.Code = ErrorResourceLimit
		}
		response.Error = "workbench result exceeds transport limit"
		raw, _ = json.Marshal(response)
	}
	return e.sendPayload(ctx, ResponseType, raw, id)
}

func (e *Endpoint) Call(ctx context.Context, request Request) (result Response, resultErr error) {
	if request.Operation == OperationExecutionProgressStream {
		return Response{}, errors.New("stream operation requires StreamCall")
	}
	spec, ok := LookupOperation(request.Operation)
	if !ok || (e.config.Role == RoleResponder && spec.Direction != "reverse") || (e.config.Role == RoleInitiator && spec.Direction != "forward") {
		return Response{}, errors.New("operation direction denied")
	}
	if err := request.Validate(); err != nil {
		return Response{}, err
	}
	ctx = latencytrace.WithFields(ctx, requestTraceFields(e.config.Role, request))
	started := latencytrace.Clock()
	latencytrace.At(ctx, "rpc.call.start", started)
	phase := "local_admission"
	defer func() {
		detail := responseTraceDetail(result)
		if resultErr != nil {
			detail.Reason = phase
		}
		latencytrace.RecordDetail(ctx, "rpc.call", started, resultErr, detail)
	}()
	raw, err := encodeRequest(request)
	if err != nil || len(raw) > MaxRequestBytes {
		return Response{}, errors.New("workbench request exceeds transport limit")
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout(request.Operation))
	defer cancel()
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	if request.Profile == ProfileV2 {
		ctx = context.WithValue(ctx, capacityContextKey{}, spec.CapacityClass)
	}
	slots := e.operationSlots(request)
	if slots != nil {
		select {
		case slots <- struct{}{}:
		default:
			return Response{}, &TransportError{Code: ErrorThrottled, Status: 429, Retryable: true, NotSent: true, Message: "workbench concurrency limit exceeded"}
		}
		defer func() { <-slots }()
	}
	e.mu.Lock()
	if want, ok := ctx.Value(expectedSessionKey{}).(string); ok && (e.closed || e.session == nil || e.session.id != want) {
		e.mu.Unlock()
		return Response{}, ErrPeerUnavailable
	}
	if e.closed || e.session == nil || !e.session.manifest {
		e.mu.Unlock()
		return Response{}, errors.New("workbench transport is not ready")
	}
	if request.Profile == ProfileV2 && (e.session.capabilities == nil || e.session.capabilities.Profile != ProfileV2 || !time.Now().Before(e.session.capabilities.ExpiresAt)) {
		e.mu.Unlock()
		return Response{}, errors.New("v2 profile is not negotiated")
	}
	if (connectionSettingsOperation(request.Operation) || browserControllerOperation(request.Operation) || MCDSelectionOperation(request.Operation) || MCDCheckoutOperation(request.Operation)) && (e.session.capabilities == nil || !slices.Contains(e.session.capabilities.Operations, request.Operation)) {
		e.mu.Unlock()
		return Response{}, errors.New("application extension operation was not negotiated")
	}
	if _, exists := e.session.sentRequests[request.ID]; exists {
		e.mu.Unlock()
		return Response{}, errors.New("reused workbench call ID in session")
	}
	// Retain the correlation ID until the session ends, including after a
	// timeout. A late response must never complete a different call with the
	// same ID. Durable operation/request IDs are separate and unchanged.
	e.session.sentRequests[request.ID] = struct{}{}
	id := e.session.id
	ch := make(chan callResult, 1)
	call := &pendingCall{result: ch, ctx: ctx}
	if latencytrace.Enabled() {
		call.trace = requestTraceFields(e.config.Role, request)
	}
	e.pending[request.ID] = call
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		if e.pending[request.ID] == call {
			delete(e.pending, request.ID)
		}
		e.mu.Unlock()
	}()
	phase = "request_send"
	if err := e.sendPayload(ctx, RequestType, raw, id); err != nil {
		if cause := ctx.Err(); cause != nil {
			return Response{}, fmt.Errorf("workbench call deadline or cancellation: %w", cause)
		}
		if callContextError(err) != nil {
			return Response{}, err
		}
		e.mu.Lock()
		reset := false
		if e.session != nil && e.session.id == id {
			e.resetLocked()
			reset = true
		}
		e.mu.Unlock()
		if reset {
			e.setStateForSession("disconnected", nil, true)
		}
		return Response{}, err
	}
	select {
	case result := <-ch:
		phase = "response_processing"
		return result.response, result.err
	case <-ctx.Done():
		phase = "response_wait"
		// End only this call. The authenticated receive owner and heartbeat
		// detect connection loss independently. Do not replay a request whose
		// result is unknown, or cancel unrelated calls sharing its session.
		return Response{}, fmt.Errorf("workbench call deadline or cancellation: %w", ctx.Err())
	}
}

func callContextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	return nil
}

func requestTraceFields(role string, request Request) latencytrace.Fields {
	id := request.RequestID
	if id == "" {
		id = request.Params["request_id"]
	}
	return latencytrace.Fields{Role: role, RPCID: request.ID, RequestID: id, Operation: request.Operation}
}

func (e *Endpoint) operationSlots(r Request) chan struct{} {
	e.mu.Lock()
	unbounded := e.session != nil && e.session.capabilities != nil && e.session.capabilities.Profile == r.Profile && e.session.capabilities.Limits.Concurrent == 0 && time.Now().Before(e.session.capabilities.ExpiresAt)
	e.mu.Unlock()
	if unbounded {
		return nil
	}
	if r.Profile == ProfileV2 {
		class, _ := OperationCapacity(r.Operation)
		if slots := e.classSlots[class]; slots != nil {
			return slots
		}
	}
	return e.slots
}

// Mail sends can upload the bounded 10 MiB attachment manifest through the
// browser runtime. Receipt reconciliation first probes the account and then
// inspects the original task. Keep those bounded workflows aligned while preserving
// the ordinary 30-second deadline and earlier caller/session cancellation.
func operationTimeout(operation string) time.Duration {
	switch operation {
	case OperationSettingsBindingsStart, OperationSettingsBindingsPoll:
		// The Weixin provider's bounded QR request can take 35 seconds. Leave
		// time to commit its result without renewing earlier caller deadlines.
		return 60 * time.Second
	case OperationMailDraftsSave, OperationMailDraftsSend, OperationMailDraftsReconcile, OperationMailSend, OperationMailProvidersCheck, OperationMailProvidersLogin, OperationMailIntakeUpdate, OperationMailRepliesPolish, OperationMailSourceCleanup, OperationMailConversationsDelete:
		return 180 * time.Second
	case OperationMedicineRecognize:
		// One vision model call on a reduced photo; local models can take a minute.
		return 120 * time.Second
	case OperationMedicineParse:
		// One short text model call.
		return 60 * time.Second
	default:
		return requestTimeout
	}
}
