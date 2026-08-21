package iscpbridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

const defaultOperationTimeout = 15 * time.Minute

const (
	maxOperationRecords = 1000
	maxMutationRecords  = 2000
)

type Principal struct {
	OwnerID string
	ActorID string
}

type AgentRuntime interface {
	HandleMessageWithAttachmentsIdempotent(context.Context, string, string, string, string, []agent.MessageAttachment) (agent.Result, error)
	HandleMessageWithIngress(context.Context, string, string, string, string, []agent.MessageAttachment, app.MessageIngressContext) (agent.Result, error)
	ExecuteApprovedToolCall(context.Context, app.Approval) (app.ToolCall, error)
	ResumeRunAfterApproval(context.Context, string, string) (agent.Result, bool, error)
	CompleteRunIfApprovalsResolved(context.Context, string) error
}

type RuntimeProvider func() AgentRuntime

type GatewayAdapter struct {
	store          store.Store
	runtime        RuntimeProvider
	manifest       Manifest
	operationLimit time.Duration

	// notificationMaxPerOwner / notificationRetentionDays bound the durable
	// passive-notification inbox on ingestion. Zero disables the respective
	// bound. Set once before serving via ConfigureNotificationRetention.
	notificationMaxPerOwner   int
	notificationRetentionDays int

	mu         sync.Mutex
	mutationMu sync.Mutex
	operations map[string]*operationRecord
	mutations  map[string]mutationRecord
}

type operationRecord struct {
	operation   Operation
	endpointID  string
	principal   Principal
	fingerprint string
	cancel      context.CancelFunc
}

type mutationRecord struct {
	fingerprint string
	response    Response
}

type SessionCreatePayload struct {
	Title string `json:"title"`
}

type MessageSendPayload struct {
	Content string `json:"content"`
}

type MessageCancelPayload struct {
	OperationID string `json:"operation_id"`
}

type NotificationDeliverPayload struct {
	NotificationID string    `json:"notification_id"`
	Source         string    `json:"source"`
	Kind           string    `json:"kind"`
	DeepLink       string    `json:"deep_link"`
	OccurredAt     time.Time `json:"occurred_at"`
}

type NotificationDeliveryResult struct {
	ID             string `json:"id"`
	NotificationID string `json:"notification_id"`
	Created        bool   `json:"created"`
}

type EventResumePayload struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

type ApprovalListPayload struct {
	Status string `json:"status,omitempty"`
}

type ApprovalResolvePayload struct {
	ApprovalID    string `json:"approval_id"`
	Decision      string `json:"decision"`
	PreviewHash   string `json:"preview_hash"`
	ExpectedState string `json:"expected_state"`
	Note          string `json:"note,omitempty"`
}

type OperationStatusPayload struct {
	OperationID string `json:"operation_id"`
}

type ApprovalView struct {
	app.Approval
	PreviewHash string `json:"preview_hash"`
}

func NewGatewayAdapter(st store.Store, runtime RuntimeProvider) *GatewayAdapter {
	return &GatewayAdapter{
		store:          st,
		runtime:        runtime,
		manifest:       DefaultManifest(),
		operationLimit: defaultOperationTimeout,
		operations:     map[string]*operationRecord{},
		mutations:      map[string]mutationRecord{},
	}
}

// ConfigureNotificationRetention bounds the durable passive-notification
// inbox: maxPerOwner caps stored records per owner and retentionDays expires
// old ones. Zero disables the respective bound. Call before serving requests.
func (a *GatewayAdapter) ConfigureNotificationRetention(maxPerOwner, retentionDays int) {
	a.notificationMaxPerOwner = maxPerOwner
	a.notificationRetentionDays = retentionDays
}

func (a *GatewayAdapter) Dispatch(ctx context.Context, principal Principal, req Request) Response {
	now := time.Now().UTC()
	if err := req.Validate(now); err != nil {
		return newResponse(req, "error", nil, nil, err, now)
	}
	if strings.TrimSpace(principal.OwnerID) == "" || strings.TrimSpace(principal.ActorID) == "" {
		return newResponse(req, "error", nil, nil, bridgeError(CodeUnauthenticated, "authenticated owner and actor are required", false), now)
	}
	if req.Type == TypeCapabilitiesDescribe || req.Type == TypeSessionList {
		if err := DecodePayload(req.Payload, &struct{}{}); err != nil {
			return newResponse(req, "error", nil, nil, bridgeError(CodeInvalidRequest, err.Error(), false), now)
		}
	}

	switch req.Type {
	case TypeCapabilitiesDescribe:
		return newResponse(req, "ok", a.manifest, nil, nil, now)
	case TypeSessionList:
		return a.listSessions(ctx, req, principal, now)
	case TypeSessionCreate:
		return a.createSession(ctx, req, principal, now)
	case TypeMessageSend:
		return a.sendMessage(ctx, req, principal, now)
	case TypeMessageCancel:
		return a.cancelMessage(ctx, req, principal, now)
	case TypeNotificationDeliver:
		return a.deliverNotification(ctx, req, principal, now)
	case TypeEventResume:
		return a.resumeEvents(ctx, req, principal, now)
	case TypeApprovalList:
		return a.listApprovals(ctx, req, principal, now)
	case TypeApprovalResolve:
		return a.resolveApproval(ctx, req, principal, now)
	case TypeOperationStatus:
		return a.operationStatus(ctx, req, principal, now)
	default:
		return newResponse(req, "error", nil, nil, bridgeError(CodeUnsupportedCapability, "unsupported request type", false), now)
	}
}

func (a *GatewayAdapter) deliverNotification(ctx context.Context, req Request, principal Principal, now time.Time) Response {
	if strings.TrimSpace(req.SessionID) != "" {
		return newResponse(req, "error", nil, nil, bridgeError(CodeInvalidRequest, "session_id is not allowed for passive notifications", false), now)
	}
	var payload NotificationDeliverPayload
	if err := DecodePayload(req.Payload, &payload); err != nil {
		return newResponse(req, "error", nil, nil, bridgeError(CodeInvalidRequest, err.Error(), false), now)
	}
	payload.NotificationID = strings.TrimSpace(payload.NotificationID)
	payload.Source = strings.TrimSpace(payload.Source)
	payload.Kind = strings.TrimSpace(payload.Kind)
	if payload.NotificationID == "" || len(payload.NotificationID) > 200 {
		return newResponse(req, "error", nil, nil, bridgeError(CodeInvalidRequest, "notification_id is required and must not exceed 200 bytes", false), now)
	}
	if payload.Source != "localmind" {
		return newResponse(req, "error", nil, nil, bridgeError(CodeInvalidRequest, "source must be localmind", false), now)
	}
	if payload.Kind != app.PassiveNotificationKindDocumentMention && payload.Kind != app.PassiveNotificationKindCommentMention {
		return newResponse(req, "error", nil, nil, bridgeError(CodeInvalidRequest, "unsupported notification kind", false), now)
	}
	if err := validateNotificationDeepLink(payload.DeepLink); err != nil {
		return newResponse(req, "error", nil, nil, bridgeError(CodeInvalidRequest, err.Error(), false), now)
	}
	if payload.OccurredAt.IsZero() || payload.OccurredAt.After(now.Add(2*time.Minute)) {
		return newResponse(req, "error", nil, nil, bridgeError(CodeInvalidRequest, "occurred_at is invalid", false), now)
	}
	notification, created, err := a.store.CreatePassiveNotification(ctx, app.PassiveNotification{
		ID:             stableID("notification", req.EndpointID, req.IdempotencyKey),
		OwnerID:        principal.OwnerID,
		EndpointID:     req.EndpointID,
		IdempotencyKey: req.IdempotencyKey,
		Fingerprint:    requestFingerprint(req),
		NotificationID: payload.NotificationID,
		Source:         payload.Source,
		Kind:           payload.Kind,
		DeepLink:       payload.DeepLink,
		OccurredAt:     payload.OccurredAt.UTC(),
		CreatedAt:      now,
	})
	if errors.Is(err, store.ErrPassiveNotificationConflict) {
		return newResponse(req, "error", nil, nil, bridgeError(CodeConflict, err.Error(), false), now)
	}
	if err != nil {
		return newResponse(req, "error", nil, nil, bridgeError(CodeTemporarilyUnavailable, "notification could not be persisted", true), now)
	}
	if created {
		if err := a.prunePassiveNotifications(ctx, now); err != nil {
			return newResponse(req, "error", nil, nil, bridgeError(CodeTemporarilyUnavailable, "notification retention could not be persisted", true), now)
		}
	}
	return newResponse(req, "ok", NotificationDeliveryResult{
		ID: notification.ID, NotificationID: notification.NotificationID, Created: created,
	}, nil, nil, now)
}

// prunePassiveNotifications applies the configured retention window and
// per-owner cap after each accepted delivery, so the inbox stays bounded at
// its only ingestion point.
func (a *GatewayAdapter) prunePassiveNotifications(ctx context.Context, now time.Time) error {
	cutoff := time.Time{}
	if a.notificationRetentionDays > 0 {
		cutoff = now.AddDate(0, 0, -a.notificationRetentionDays)
	}
	if cutoff.IsZero() && a.notificationMaxPerOwner <= 0 {
		return nil
	}
	_, err := a.store.PrunePassiveNotifications(ctx, cutoff, a.notificationMaxPerOwner)
	return err
}

func (a *GatewayAdapter) listSessions(ctx context.Context, req Request, principal Principal, now time.Time) Response {
	sessions := make([]app.Session, 0)
	listed, err := a.store.ListSessions(ctx)
	if err != nil {
		return newResponse(req, "error", nil, nil, bridgeError(CodeTemporarilyUnavailable, "sessions are temporarily unavailable", true), now)
	}
	for _, session := range listed {
		if ownerIDForSession(session) == principal.OwnerID && !session.Hidden {
			sessions = append(sessions, session)
		}
	}
	return newResponse(req, "ok", map[string]any{"sessions": sessions}, nil, nil, now)
}

func (a *GatewayAdapter) createSession(ctx context.Context, req Request, principal Principal, now time.Time) Response {
	a.mutationMu.Lock()
	defer a.mutationMu.Unlock()
	if cached, ok := a.cachedMutation(req); ok {
		return cached
	}
	var payload SessionCreatePayload
	if err := DecodePayload(req.Payload, &payload); err != nil {
		return newResponse(req, "error", nil, nil, bridgeError(CodeInvalidRequest, err.Error(), false), now)
	}
	title := strings.TrimSpace(payload.Title)
	if title == "" {
		title = "JingSi conversation"
	}
	if len(title) > 200 {
		return newResponse(req, "error", nil, nil, bridgeError(CodeInvalidRequest, "session title is too long", false), now)
	}
	profile, ok, err := a.store.GetOwnerProfileByID(ctx, principal.OwnerID)
	if err != nil {
		return newResponse(req, "error", nil, nil, bridgeError(CodeTemporarilyUnavailable, "owner profile is temporarily unavailable", true), now)
	}
	if !ok {
		return newResponse(req, "error", nil, nil, bridgeError(CodeNotFound, "owner profile not found", false), now)
	}
	session, err := a.store.CreateSessionWithScope(ctx, title, profile.ID, profile.WorkspaceRoot, "iscp", false)
	if err != nil {
		return newResponse(req, "error", nil, nil, bridgeError(CodeTemporarilyUnavailable, "session creation is temporarily unavailable", true), now)
	}
	response := newResponse(req, "ok", map[string]any{"session": session}, nil, nil, now)
	a.rememberMutation(req, response)
	return response
}

func (a *GatewayAdapter) sendMessage(ctx context.Context, req Request, principal Principal, now time.Time) Response {
	if err := a.requireSession(ctx, req.SessionID, principal); err != nil {
		return newResponse(req, "error", nil, nil, err, now)
	}
	var payload MessageSendPayload
	if err := DecodePayload(req.Payload, &payload); err != nil {
		return newResponse(req, "error", nil, nil, bridgeError(CodeInvalidRequest, err.Error(), false), now)
	}
	content := strings.TrimSpace(payload.Content)
	if content == "" {
		return newResponse(req, "error", nil, nil, bridgeError(CodeInvalidRequest, "message content is required", false), now)
	}
	if len(content) > 256*1024 {
		return newResponse(req, "error", nil, nil, bridgeError(CodeInvalidRequest, "message content is too large", false), now)
	}

	operationID := stableID("run_iscp", req.EndpointID, req.IdempotencyKey)
	messageID := stableID("m_iscp", req.EndpointID, req.IdempotencyKey)
	fingerprint := requestFingerprint(req)
	a.mu.Lock()
	if existing, ok := a.operations[operationID]; ok {
		if existing.fingerprint != fingerprint {
			a.mu.Unlock()
			return newResponse(req, "error", nil, nil, bridgeError(CodeConflict, "idempotency key was reused for a different message", false), now)
		}
		operation := existing.operation
		a.mu.Unlock()
		return newResponse(req, "accepted", nil, &operation, nil, now)
	}
	if run, ok, err := a.store.GetRun(ctx, operationID); err != nil {
		a.mu.Unlock()
		return newResponse(req, "error", nil, nil, bridgeError(CodeTemporarilyUnavailable, "operation state is temporarily unavailable", true), now)
	} else if ok {
		if runEndpointID(run) != req.EndpointID {
			a.mu.Unlock()
			return newResponse(req, "error", nil, nil, bridgeError(CodePermissionDenied, "operation is not accessible", false), now)
		}
		matches, messageErr := a.storedMessageMatches(ctx, run.SessionID, messageID, content)
		if messageErr != nil {
			a.mu.Unlock()
			return newResponse(req, "error", nil, nil, bridgeError(CodeTemporarilyUnavailable, "conversation state is temporarily unavailable", true), now)
		}
		if run.SessionID != req.SessionID || !matches {
			a.mu.Unlock()
			return newResponse(req, "error", nil, nil, bridgeError(CodeConflict, "idempotency key was reused for a different message", false), now)
		}
		operation, err := a.operationFromRun(ctx, run, req.RequestID)
		if err != nil {
			a.mu.Unlock()
			return newResponse(req, "error", nil, nil, bridgeError(CodeTemporarilyUnavailable, "conversation state is temporarily unavailable", true), now)
		}
		a.mu.Unlock()
		return newResponse(req, "accepted", nil, &operation, nil, now)
	}
	opCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), a.operationLimit)
	operation := Operation{
		ID: operationID, RequestID: req.RequestID, SessionID: req.SessionID, RunID: operationID,
		State: "accepted", CreatedAt: now, UpdatedAt: now,
	}
	record := &operationRecord{operation: operation, endpointID: req.EndpointID, principal: principal, fingerprint: fingerprint, cancel: cancel}
	a.pruneOperationsLocked()
	if len(a.operations) >= maxOperationRecords {
		a.mu.Unlock()
		cancel()
		return newResponse(req, "error", nil, nil, bridgeError(CodeRateLimited, "too many Bridge operations are retained", true), now)
	}
	a.operations[operationID] = record
	a.mu.Unlock()

	go a.executeMessage(opCtx, record, messageID, content)
	return newResponse(req, "accepted", nil, &operation, nil, now)
}

func (a *GatewayAdapter) executeMessage(ctx context.Context, record *operationRecord, messageID, content string) {
	a.updateOperation(record.operation.ID, func(operation *Operation) {
		operation.State = "running"
	})
	result, err := a.runtime().HandleMessageWithIngress(ctx, record.operation.SessionID, messageID, record.operation.RunID, content, nil, app.MessageIngressContext{
		Source: app.MessageSourceContext{
			Kind: app.MessageSourceThirdPartyDevice, Adapter: "iscp-bridge",
			EndpointID: app.EndpointID(record.endpointID), NativeMessageID: messageID,
		},
		OwnerID: record.principal.OwnerID,
		Authorization: app.MessageAuthorization{
			PrincipalID: record.principal.ActorID,
		},
		ReturnRoute: app.ReturnRoute{Mode: app.ReturnNowhere},
	})
	now := time.Now().UTC()
	a.mu.Lock()
	defer a.mu.Unlock()
	current, ok := a.operations[record.operation.ID]
	if !ok {
		return
	}
	current.cancel = nil
	current.operation.UpdatedAt = now
	if err != nil {
		if errors.Is(err, context.Canceled) {
			current.operation.State = "cancelled"
			current.operation.Error = bridgeError(CodeConflict, "operation was cancelled", false)
			return
		}
		if errors.Is(err, context.DeadlineExceeded) {
			current.operation.State = "unknown"
			current.operation.Error = bridgeError(CodeTemporarilyUnavailable, "operation timed out; query status before retrying", true)
			return
		}
		current.operation.State = "failed"
		current.operation.Error = bridgeError(CodeInternal, "operation failed", false)
		return
	}
	current.operation.State = operationStateForRun(result.Run)
	current.operation.Result = result
}

func (a *GatewayAdapter) cancelMessage(ctx context.Context, req Request, principal Principal, now time.Time) Response {
	a.mutationMu.Lock()
	defer a.mutationMu.Unlock()
	if cached, ok := a.cachedMutation(req); ok {
		return cached
	}
	var payload MessageCancelPayload
	if err := DecodePayload(req.Payload, &payload); err != nil {
		return newResponse(req, "error", nil, nil, bridgeError(CodeInvalidRequest, err.Error(), false), now)
	}
	operationID := strings.TrimSpace(payload.OperationID)
	a.mu.Lock()
	record, ok := a.operations[operationID]
	if !ok {
		a.mu.Unlock()
		return newResponse(req, "error", nil, nil, bridgeError(CodeNotFound, "operation not found", false), now)
	}
	if record.endpointID != req.EndpointID || record.operation.SessionID != req.SessionID {
		a.mu.Unlock()
		return newResponse(req, "error", nil, nil, bridgeError(CodePermissionDenied, "operation is not accessible", false), now)
	}
	if err := a.requireSession(ctx, record.operation.SessionID, principal); err != nil {
		a.mu.Unlock()
		return newResponse(req, "error", nil, nil, err, now)
	}
	if record.cancel != nil {
		record.cancel()
		record.cancel = nil
		record.operation.State = "cancelled"
		record.operation.UpdatedAt = now
	}
	operation := record.operation
	a.mu.Unlock()
	response := newResponse(req, "ok", nil, &operation, nil, now)
	a.rememberMutation(req, response)
	return response
}

func (a *GatewayAdapter) resumeEvents(ctx context.Context, req Request, principal Principal, now time.Time) Response {
	if err := a.requireSession(ctx, req.SessionID, principal); err != nil {
		return newResponse(req, "error", nil, nil, err, now)
	}
	var payload EventResumePayload
	if err := DecodePayload(req.Payload, &payload); err != nil {
		return newResponse(req, "error", nil, nil, bridgeError(CodeInvalidRequest, err.Error(), false), now)
	}
	limit := payload.Limit
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	stored, err := a.store.EventsAfter(ctx, req.SessionID, strings.TrimSpace(payload.Cursor))
	if err != nil {
		return newResponse(req, "error", nil, nil, bridgeError(CodeTemporarilyUnavailable, "events are temporarily unavailable", true), now)
	}
	if payload.Cursor != "" {
		allEvents, err := a.store.EventsAfter(ctx, req.SessionID, "")
		if err != nil {
			return newResponse(req, "error", nil, nil, bridgeError(CodeTemporarilyUnavailable, "events are temporarily unavailable", true), now)
		}
		found := false
		for _, event := range allEvents {
			if event.ID == payload.Cursor {
				found = true
				break
			}
		}
		if !found {
			return newResponse(req, "error", nil, nil, bridgeError(CodeStaleState, "event cursor is not valid for this session", false), now)
		}
	}
	if len(stored) > limit {
		stored = stored[:limit]
	}
	events := make([]Event, 0, len(stored))
	for _, storedEvent := range stored {
		if storedEvent.ID == payload.Cursor {
			continue
		}
		events = append(events, Event{
			ProtocolVersion: ProtocolVersion,
			Type:            TypeEvent,
			EndpointID:      req.EndpointID,
			SessionID:       req.SessionID,
			OperationID:     storedEvent.RunID,
			Cursor:          storedEvent.ID,
			EventType:       storedEvent.Type,
			Payload:         storedEvent.Payload,
			IssuedAt:        storedEvent.Time,
		})
	}
	return newResponse(req, "ok", map[string]any{"events": events}, nil, nil, now)
}

func (a *GatewayAdapter) listApprovals(ctx context.Context, req Request, principal Principal, now time.Time) Response {
	if req.SessionID != "" {
		if err := a.requireSession(ctx, req.SessionID, principal); err != nil {
			return newResponse(req, "error", nil, nil, err, now)
		}
	}
	var payload ApprovalListPayload
	if err := DecodePayload(req.Payload, &payload); err != nil {
		return newResponse(req, "error", nil, nil, bridgeError(CodeInvalidRequest, err.Error(), false), now)
	}
	status := strings.TrimSpace(payload.Status)
	if status == "" {
		status = "pending"
	}
	approvals, err := a.store.ListApprovals(ctx, status)
	if err != nil {
		return newResponse(req, "error", nil, nil, bridgeError(CodeTemporarilyUnavailable, "approval state is temporarily unavailable", true), now)
	}
	views := make([]ApprovalView, 0)
	for _, approval := range approvals {
		if req.SessionID != "" && approval.SessionID != req.SessionID {
			continue
		}
		if a.requireSession(ctx, approval.SessionID, principal) == nil {
			views = append(views, ApprovalView{Approval: approval, PreviewHash: ApprovalPreviewHash(approval)})
		}
	}
	return newResponse(req, "ok", map[string]any{"approvals": views}, nil, nil, now)
}

func (a *GatewayAdapter) resolveApproval(ctx context.Context, req Request, principal Principal, now time.Time) Response {
	a.mutationMu.Lock()
	defer a.mutationMu.Unlock()
	if cached, ok := a.cachedMutation(req); ok {
		return cached
	}
	var payload ApprovalResolvePayload
	if err := DecodePayload(req.Payload, &payload); err != nil {
		return newResponse(req, "error", nil, nil, bridgeError(CodeInvalidRequest, err.Error(), false), now)
	}
	decision := strings.ToLower(strings.TrimSpace(payload.Decision))
	if decision != "approved" && decision != "rejected" {
		return newResponse(req, "error", nil, nil, bridgeError(CodeInvalidRequest, "decision must be approved or rejected", false), now)
	}
	if strings.TrimSpace(payload.ExpectedState) != "pending" {
		return newResponse(req, "error", nil, nil, bridgeError(CodeInvalidRequest, "expected_state must be pending", false), now)
	}
	if strings.TrimSpace(payload.PreviewHash) == "" {
		return newResponse(req, "error", nil, nil, bridgeError(CodeInvalidRequest, "preview_hash is required", false), now)
	}
	approval, ok, err := a.findApproval(ctx, strings.TrimSpace(payload.ApprovalID))
	if err != nil {
		return newResponse(req, "error", nil, nil, bridgeError(CodeTemporarilyUnavailable, "approval state is temporarily unavailable", true), now)
	}
	if !ok {
		return newResponse(req, "error", nil, nil, bridgeError(CodeNotFound, "approval not found", false), now)
	}
	if req.SessionID != "" && approval.SessionID != req.SessionID {
		return newResponse(req, "error", nil, nil, bridgeError(CodePermissionDenied, "approval session mismatch", false), now)
	}
	if err := a.requireSession(ctx, approval.SessionID, principal); err != nil {
		return newResponse(req, "error", nil, nil, err, now)
	}
	if payload.PreviewHash != ApprovalPreviewHash(approval) {
		return newResponse(req, "error", nil, nil, bridgeError(CodeStaleState, "approval preview has changed", false), now)
	}
	if approval.Status != "pending" {
		if approval.Status == decision {
			response := newResponse(req, "ok", map[string]any{"approval": ApprovalView{Approval: approval, PreviewHash: ApprovalPreviewHash(approval)}}, nil, nil, now)
			a.rememberMutation(req, response)
			return response
		}
		return newResponse(req, "error", nil, nil, bridgeError(CodeStaleState, "approval is no longer pending", false), now)
	}

	candidate, err := a.store.ResolveApproval(ctx, approval.ID, decision, payload.Note)
	resolved, err := store.ReconcileApprovalWrite(ctx, a.store, candidate, err)
	if err != nil {
		return newResponse(req, "error", nil, nil, bridgeError(CodeStaleState, "approval could not be resolved", false), now)
	}
	var call *app.ToolCall
	var result *agent.Result
	if decision == "approved" {
		executed, execErr := a.runtime().ExecuteApprovedToolCall(ctx, resolved)
		if execErr != nil {
			return newResponse(req, "error", nil, nil, bridgeError(CodeInternal, "approved operation failed", false), now)
		}
		call = &executed
		if resumed, resumedOK, resumeErr := a.runtime().ResumeRunAfterApproval(ctx, resolved.SessionID, resolved.RunID); resumeErr != nil {
			return newResponse(req, "error", nil, nil, bridgeError(CodeInternal, "approved run could not resume", false), now)
		} else if resumedOK {
			result = &resumed
		}
	} else if rejected, found, readErr := a.store.GetToolCall(ctx, resolved.ToolCallID); readErr != nil {
		return newResponse(req, "error", nil, nil, bridgeError(CodeTemporarilyUnavailable, "tool call state is temporarily unavailable", true), now)
	} else if found {
		completedAt := time.Now().UTC()
		rejected.Status = "rejected"
		rejected.Error = "owner rejected approval"
		rejected.CompletedAt = &completedAt
		persisted, saveErr := a.store.SaveToolCall(ctx, rejected)
		persisted, saveErr = store.ReconcileToolCallWrite(ctx, a.store, persisted, saveErr)
		if saveErr != nil {
			return newResponse(req, "error", nil, nil, bridgeError(CodeTemporarilyUnavailable, "tool call decision is temporarily unavailable", true), now)
		}
		call = &persisted
	}
	if err := a.runtime().CompleteRunIfApprovalsResolved(ctx, resolved.RunID); err != nil {
		return newResponse(req, "error", nil, nil, bridgeError(CodeTemporarilyUnavailable, "run completion is temporarily unavailable", true), now)
	}
	response := newResponse(req, "ok", map[string]any{
		"approval":  ApprovalView{Approval: resolved, PreviewHash: ApprovalPreviewHash(resolved)},
		"tool_call": call,
		"result":    result,
	}, nil, nil, now)
	a.rememberMutation(req, response)
	return response
}

func (a *GatewayAdapter) operationStatus(ctx context.Context, req Request, principal Principal, now time.Time) Response {
	var payload OperationStatusPayload
	if err := DecodePayload(req.Payload, &payload); err != nil {
		return newResponse(req, "error", nil, nil, bridgeError(CodeInvalidRequest, err.Error(), false), now)
	}
	operationID := strings.TrimSpace(payload.OperationID)
	a.mu.Lock()
	if record, ok := a.operations[operationID]; ok {
		if record.endpointID != req.EndpointID {
			a.mu.Unlock()
			return newResponse(req, "error", nil, nil, bridgeError(CodePermissionDenied, "operation is not accessible", false), now)
		}
		operation := record.operation
		a.mu.Unlock()
		if req.SessionID != "" && req.SessionID != operation.SessionID {
			return newResponse(req, "error", nil, nil, bridgeError(CodePermissionDenied, "operation session mismatch", false), now)
		}
		if err := a.requireSession(ctx, operation.SessionID, principal); err != nil {
			return newResponse(req, "error", nil, nil, err, now)
		}
		return newResponse(req, "ok", nil, &operation, nil, now)
	}
	a.mu.Unlock()
	if run, ok, err := a.store.GetRun(ctx, operationID); err != nil {
		return newResponse(req, "error", nil, nil, bridgeError(CodeTemporarilyUnavailable, "operation state is temporarily unavailable", true), now)
	} else if ok {
		if runEndpointID(run) != req.EndpointID {
			return newResponse(req, "error", nil, nil, bridgeError(CodePermissionDenied, "operation is not accessible", false), now)
		}
		if req.SessionID != "" && req.SessionID != run.SessionID {
			return newResponse(req, "error", nil, nil, bridgeError(CodePermissionDenied, "operation session mismatch", false), now)
		}
		if err := a.requireSession(ctx, run.SessionID, principal); err != nil {
			return newResponse(req, "error", nil, nil, err, now)
		}
		operation, err := a.operationFromRun(ctx, run, req.RequestID)
		if err != nil {
			return newResponse(req, "error", nil, nil, bridgeError(CodeTemporarilyUnavailable, "conversation state is temporarily unavailable", true), now)
		}
		return newResponse(req, "ok", nil, &operation, nil, now)
	}
	return newResponse(req, "error", nil, nil, bridgeError(CodeNotFound, "operation not found", false), now)
}

func (a *GatewayAdapter) operationFromRun(ctx context.Context, run app.AgentRun, requestID string) (Operation, error) {
	toolCalls, err := a.store.ListToolCalls(ctx, run.SessionID)
	if err != nil {
		return Operation{}, err
	}
	approvals, err := a.store.ListApprovals(ctx, "")
	if err != nil {
		return Operation{}, err
	}
	result := map[string]any{
		"run":        run,
		"tool_calls": toolCallsForRun(toolCalls, run.ID),
		"approvals":  approvalsForRun(approvals, run.ID),
	}
	messages, err := a.store.ListMessages(ctx, run.SessionID)
	if err != nil {
		return Operation{}, err
	}
	for _, message := range messages {
		if message.RunID == run.ID && message.Role == "assistant" {
			result["message"] = message
		}
	}
	createdAt := run.StartedAt
	updatedAt := run.StartedAt
	if run.CompletedAt != nil {
		updatedAt = *run.CompletedAt
	}
	return Operation{
		ID: run.ID, RequestID: requestID, SessionID: run.SessionID, RunID: run.ID,
		State: operationStateForRun(run), Result: result, CreatedAt: createdAt, UpdatedAt: updatedAt,
	}, nil
}

func (a *GatewayAdapter) updateOperation(id string, update func(*Operation)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if record, ok := a.operations[id]; ok {
		update(&record.operation)
		record.operation.UpdatedAt = time.Now().UTC()
	}
}

func (a *GatewayAdapter) requireSession(ctx context.Context, sessionID string, principal Principal) *BridgeError {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return bridgeError(CodeInvalidRequest, "session_id is required", false)
	}
	session, ok, err := a.store.GetSession(ctx, sessionID)
	if err != nil {
		return bridgeError(CodeTemporarilyUnavailable, "session is temporarily unavailable", true)
	}
	if !ok {
		return bridgeError(CodeNotFound, "session not found", false)
	}
	if ownerIDForSession(session) != principal.OwnerID {
		return bridgeError(CodePermissionDenied, "session is not accessible", false)
	}
	return nil
}

func (a *GatewayAdapter) cachedMutation(req Request) (Response, bool) {
	key := req.EndpointID + "\x00" + req.IdempotencyKey
	a.mu.Lock()
	defer a.mu.Unlock()
	record, ok := a.mutations[key]
	if !ok {
		return Response{}, false
	}
	if record.fingerprint != requestFingerprint(req) {
		return newResponse(req, "error", nil, nil, bridgeError(CodeConflict, "idempotency key was reused for a different mutation", false), time.Now().UTC()), true
	}
	response := record.response
	response.RequestID = req.RequestID
	response.IssuedAt = time.Now().UTC()
	return response, true
}

func (a *GatewayAdapter) rememberMutation(req Request, response Response) {
	key := req.EndpointID + "\x00" + req.IdempotencyKey
	a.mu.Lock()
	if len(a.mutations) >= maxMutationRecords {
		for existing := range a.mutations {
			delete(a.mutations, existing)
			break
		}
	}
	a.mutations[key] = mutationRecord{fingerprint: requestFingerprint(req), response: response}
	a.mu.Unlock()
}

func (a *GatewayAdapter) pruneOperationsLocked() {
	if len(a.operations) < maxOperationRecords {
		return
	}
	var oldestID string
	var oldest time.Time
	for id, record := range a.operations {
		if record.cancel != nil {
			continue
		}
		if oldestID == "" || record.operation.UpdatedAt.Before(oldest) {
			oldestID = id
			oldest = record.operation.UpdatedAt
		}
	}
	if oldestID != "" {
		delete(a.operations, oldestID)
	}
}

func (a *GatewayAdapter) findApproval(ctx context.Context, id string) (app.Approval, bool, error) {
	return a.store.GetApproval(ctx, id)
}

func (a *GatewayAdapter) storedMessageMatches(ctx context.Context, sessionID, messageID, content string) (bool, error) {
	messages, err := a.store.ListMessages(ctx, sessionID)
	if err != nil {
		return false, err
	}
	for _, message := range messages {
		if message.ID == messageID {
			return message.Role == "user" && message.Content == content, nil
		}
	}
	return false, nil
}

func ApprovalPreviewHash(approval app.Approval) string {
	resources := append([]string(nil), approval.Resources...)
	sort.Strings(resources)
	payload := struct {
		ID        string         `json:"id"`
		Tool      string         `json:"tool"`
		Risk      app.RiskLevel  `json:"risk"`
		Summary   string         `json:"summary"`
		Reason    string         `json:"reason"`
		Resources []string       `json:"resources"`
		Arguments map[string]any `json:"arguments"`
	}{
		ID: approval.ID, Tool: approval.Tool, Risk: approval.Risk, Summary: approval.Summary,
		Reason: approval.Reason, Resources: resources, Arguments: approval.Arguments,
	}
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func operationStateForRun(run app.AgentRun) string {
	state := strings.ToLower(strings.TrimSpace(run.State))
	switch state {
	case "completed", "succeeded":
		return "completed"
	case "approval_pending", "awaiting_approval":
		return "approval_required"
	case "cancelled", "canceled":
		return "cancelled"
	case "failed", "blocked":
		return "failed"
	case "received", "routing", "running", "executing":
		return "running"
	default:
		if run.CompletedAt != nil {
			return "completed"
		}
		return "running"
	}
}

func ownerIDForSession(session app.Session) string {
	if strings.TrimSpace(session.OwnerID) == "" {
		return app.DefaultOwnerID
	}
	return session.OwnerID
}

func runEndpointID(run app.AgentRun) string {
	if run.MessageContext == nil || run.MessageContext.Source.Adapter != "iscp-bridge" {
		return ""
	}
	return string(run.MessageContext.Source.EndpointID)
}

func toolCallsForRun(calls []app.ToolCall, runID string) []app.ToolCall {
	out := make([]app.ToolCall, 0)
	for _, call := range calls {
		if call.RunID == runID {
			out = append(out, call)
		}
	}
	return out
}

func approvalsForRun(approvals []app.Approval, runID string) []ApprovalView {
	out := make([]ApprovalView, 0)
	for _, approval := range approvals {
		if approval.RunID == runID {
			out = append(out, ApprovalView{Approval: approval, PreviewHash: ApprovalPreviewHash(approval)})
		}
	}
	return out
}

func HTTPStatus(response Response) int {
	if response.Error == nil {
		return 200
	}
	switch response.Error.Code {
	case CodeUnauthenticated:
		return 401
	case CodePermissionDenied:
		return 403
	case CodeNotFound:
		return 404
	case CodeConflict, CodeStaleState:
		return 409
	case CodeRateLimited:
		return 429
	case CodeTemporarilyUnavailable:
		return 503
	case CodeUnsupportedCapability:
		return 501
	case CodeInvalidRequest:
		return 400
	default:
		return 500
	}
}

func SanitizeError(err error) *BridgeError {
	if err == nil {
		return nil
	}
	var bridgeErr *BridgeError
	if errors.As(err, &bridgeErr) {
		return bridgeErr
	}
	return bridgeError(CodeInternal, fmt.Sprintf("bridge operation failed (%T)", err), false)
}
