package iscpworkbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

func (e *Endpoint) acceptResponse(raw []byte) error {

	var response Response
	if err := strictDecode(raw, &response); err != nil {
		return errors.New("invalid workbench response")
	}
	if err := response.Validate(); err != nil {
		return err
	}
	e.mu.Lock()
	ch := e.pending[response.ID]
	if ch != nil {
		delete(e.pending, response.ID)
		ch <- callResult{response: response}
	}
	e.mu.Unlock()
	// A deadline may expire while a valid response is already in flight.
	return nil
}

func (e *Endpoint) acceptRequest(ctx context.Context, raw []byte, id string) error {

	var request Request
	if err := strictDecode(raw, &request); err != nil {
		return errors.New("invalid workbench request")
	}
	if err := request.Validate(); err != nil {
		return err
	}
	e.mu.Lock()
	if e.session == nil || e.session.id != id {
		e.mu.Unlock()
		return errors.New("request session was replaced")
	}
	profile := Profile
	if e.session.capabilities != nil {
		profile = e.session.capabilities.Profile
	}
	spec, _ := LookupOperation(request.Operation)
	if (request.Profile == ProfileV2 && (profile != ProfileV2 || e.session.capabilities == nil || !time.Now().Before(e.session.capabilities.ExpiresAt))) || (e.config.Role == RoleInitiator && spec.Direction != "reverse") || (e.config.Role == RoleResponder && spec.Direction != "forward") {
		e.mu.Unlock()
		return errors.New("operation not admitted by negotiated direction/profile")
	}
	info := SessionInfo{SessionID: id, Profile: profile, GrantRevision: e.grantMaterial().grant.RevocationEpoch, Binding: e.config.Binding, QualifiedOperations: slices.Clone(e.config.QualifiedCapabilities)}
	if _, ok := e.session.seenRequests[request.ID]; ok {
		e.mu.Unlock()
		return errors.New("replayed workbench request ID")
	}
	e.session.seenRequests[request.ID] = struct{}{}
	e.mu.Unlock()
	slots := e.operationSlots(request)
	select {
	case slots <- struct{}{}:
	default:
		return e.sendResponse(ctx, id, Response{Profile: request.Profile, ID: request.ID, Status: 429, Code: ErrorThrottled, Retryable: true, RetryAfterMS: 250, Error: "workbench concurrency limit exceeded"})
	}
	e.workers.Add(1)
	go func() {
		defer e.workers.Done()
		defer func() { <-slots }()
		callCtx, cancel := context.WithTimeout(ctx, operationTimeout(request.Operation))
		defer cancel()
		policy, policyErr := e.AuthorizationPolicy(callCtx)
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
			response = e.handler(callCtx, request)
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

func (e *Endpoint) sendResponse(ctx context.Context, id string, response Response) error {
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

func (e *Endpoint) Call(ctx context.Context, request Request) (Response, error) {
	spec, ok := LookupOperation(request.Operation)
	if !ok || (e.config.Role == RoleResponder && spec.Direction != "reverse") || (e.config.Role == RoleInitiator && spec.Direction != "forward") {
		return Response{}, errors.New("operation direction denied")
	}
	if err := request.Validate(); err != nil {
		return Response{}, err
	}
	raw, err := encodeRequest(request)
	if err != nil || len(raw) > MaxRequestBytes {
		return Response{}, errors.New("workbench request exceeds transport limit")
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout(request.Operation))
	defer cancel()
	if request.Profile == ProfileV2 {
		ctx = context.WithValue(ctx, capacityContextKey{}, spec.CapacityClass)
	}
	slots := e.operationSlots(request)
	select {
	case slots <- struct{}{}:
	default:
		return Response{}, &TransportError{Code: ErrorThrottled, Status: 429, Retryable: true, NotSent: true, Message: "workbench concurrency limit exceeded"}
	}
	defer func() { <-slots }()
	e.mu.Lock()
	if e.closed || e.session == nil || !e.session.manifest {
		e.mu.Unlock()
		return Response{}, errors.New("workbench transport is not ready")
	}
	if request.Profile == ProfileV2 && (e.session.capabilities == nil || e.session.capabilities.Profile != ProfileV2 || !time.Now().Before(e.session.capabilities.ExpiresAt)) {
		e.mu.Unlock()
		return Response{}, errors.New("v2 profile is not negotiated")
	}
	if _, exists := e.pending[request.ID]; exists {
		e.mu.Unlock()
		return Response{}, errors.New("duplicate pending workbench call ID")
	}
	id := e.session.id
	ch := make(chan callResult, 1)
	e.pending[request.ID] = ch
	e.mu.Unlock()
	defer func() { e.mu.Lock(); delete(e.pending, request.ID); e.mu.Unlock() }()
	if err := e.sendPayload(ctx, RequestType, raw, id); err != nil {
		e.mu.Lock()
		if e.session != nil && e.session.id == id {
			e.resetLocked()
		}
		e.mu.Unlock()
		e.setState("disconnected")
		return Response{}, err
	}
	select {
	case result := <-ch:
		return result.response, result.err
	case <-ctx.Done():
		// A live Relay cannot establish peer liveness. A lost response requires
		// fresh keys and identity/installation verification before more calls.
		e.mu.Lock()
		if e.session != nil && e.session.id == id {
			e.resetLocked()
		}
		e.mu.Unlock()
		e.setState("disconnected")
		return Response{}, fmt.Errorf("workbench call deadline or cancellation: %w", ctx.Err())
	}
}

func (e *Endpoint) operationSlots(r Request) chan struct{} {
	if r.Profile == ProfileV2 {
		class, _ := OperationCapacity(r.Operation)
		if slots := e.classSlots[class]; slots != nil {
			return slots
		}
	}
	return e.slots
}

// Mail sends can upload the bounded 10 MiB attachment manifest through the
// browser runtime. Keep transport and business budgets aligned while preserving
// the ordinary 30-second deadline and earlier caller/session cancellation.
func operationTimeout(operation string) time.Duration {
	switch operation {
	case OperationMailDraftsSave, OperationMailDraftsSend, OperationMailSend:
		return 180 * time.Second
	default:
		return requestTimeout
	}
}
