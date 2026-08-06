package agent

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const (
	externalSendApprovalAction = "message_control.external_send"
	externalSendApprovalTool   = "notify.ask_approval"
)

type externalSendMetadata struct {
	EndpointID     app.EndpointID
	OwnerID        string
	ActorID        string
	EnvelopeID     string
	IdempotencyKey string
	CorrelationID  string
	CausationID    string
}

// queueExternalSendApproval creates a confirmation-only tool call after the
// business result is ready. The call is never exposed to the model and does
// not perform delivery; it only integrates Message Control with the existing
// approval/resume transport.
func (r Runtime) queueExternalSendApproval(run *app.AgentRun) (app.ToolCall, app.Approval, bool) {
	if run == nil || run.State != "completed" || approvalsStillPending(r.store.ListApprovals("pending"), run.ID) {
		return app.ToolCall{}, app.Approval{}, false
	}
	if r.isExternalMediaPublication(*run) {
		return app.ToolCall{}, app.Approval{}, false
	}
	metadata, required := r.externalSendMetadata(*run)
	if !required || r.externalSendApprovalForRun(run.ID) != nil {
		return app.ToolCall{}, app.Approval{}, false
	}
	now := time.Now().UTC()
	arguments := map[string]any{
		"summary":                "Approve sending this result to the resolved external recipient.",
		"reason":                 "External delivery requires a separate owner approval after exact recipient resolution.",
		"message_control_action": externalSendApprovalAction,
		"endpoint_id":            string(metadata.EndpointID),
		"owner_id":               metadata.OwnerID,
		"actor_id":               metadata.ActorID,
		"envelope_id":            metadata.EnvelopeID,
		"idempotency_key":        metadata.IdempotencyKey,
		"correlation_id":         metadata.CorrelationID,
		"causation_id":           metadata.CausationID,
	}
	call := app.ToolCall{
		ID: app.NewID("tc"), SessionID: run.SessionID, RunID: run.ID,
		Tool: externalSendApprovalTool, Risk: app.RiskDangerous, Status: "approval_pending",
		Arguments: arguments, StartedAt: now,
	}
	approval := app.Approval{
		ID: app.NewID("ap"), Source: app.ApprovalSourceTool, SessionID: run.SessionID, RunID: run.ID, ToolCallID: call.ID,
		Tool: externalSendApprovalTool, Risk: app.RiskDangerous, Status: "pending",
		Summary: stringValue(arguments["summary"]), Reason: stringValue(arguments["reason"]),
		Resources: []string{string(metadata.EndpointID)}, Arguments: arguments, CreatedAt: now,
	}
	call.ApprovalID = approval.ID
	r.store.SaveToolCall(call)
	r.store.SaveApproval(approval)
	run.State = "approval_pending"
	run.CompletedAt = nil
	r.store.SaveRun(*run)
	r.store.AddAudit(app.AuditEvent{
		SessionID: run.SessionID, RunID: run.ID, Actor: "message_control",
		Type: "message.control.send_approval_requested", Summary: "External send is waiting for owner approval",
		Fields: map[string]any{"approval_id": approval.ID, "endpoint_id": metadata.EndpointID},
	})
	return call, approval, true
}

func (r Runtime) isExternalMediaPublication(run app.AgentRun) bool {
	if !isOrdinaryMediaPublication(run) {
		return false
	}
	_, external := r.externalSendMetadata(run)
	return external
}

func (r Runtime) externalSendMetadata(run app.AgentRun) (externalSendMetadata, bool) {
	if run.MessageContext == nil || run.MessageContext.ReturnRoute.Mode != app.ReturnToEndpoint || run.MessageContext.ReturnRoute.EndpointID == "" {
		return externalSendMetadata{}, false
	}
	// Timer routes were authorized and frozen when the schedule was created;
	// due-time execution must not stop for a second interactive send approval.
	if run.MessageContext.Source.Kind == app.MessageSourceTimer {
		return externalSendMetadata{}, false
	}
	for _, event := range r.store.ListAudit(run.SessionID) {
		if event.RunID != run.ID || event.Type != "message.control.routed" || event.Fields == nil ||
			cleanOptionalString(event.Fields["status"]) != string(TargetResolved) {
			continue
		}
		endpointID := app.EndpointID(cleanOptionalString(event.Fields["resolved_endpoint_id"]))
		if endpointID == "" || endpointID != run.MessageContext.ReturnRoute.EndpointID {
			return externalSendMetadata{}, false
		}
		return externalSendMetadata{
			EndpointID: endpointID, OwnerID: cleanOptionalString(event.Fields["owner_id"]),
			ActorID: cleanOptionalString(event.Fields["actor_id"]), EnvelopeID: cleanOptionalString(event.Fields["envelope_id"]),
			IdempotencyKey: cleanOptionalString(event.Fields["idempotency_key"]), CorrelationID: cleanOptionalString(event.Fields["correlation_id"]),
			CausationID: cleanOptionalString(event.Fields["causation_id"]),
		}, true
	}
	return externalSendMetadata{}, false
}

func (r Runtime) externalSendApprovalForRun(runID string) *app.Approval {
	for _, approval := range approvalsForRun(r.store.ListApprovals(""), runID) {
		if cleanOptionalString(approval.Arguments["message_control_action"]) == externalSendApprovalAction {
			copy := approval
			return &copy
		}
	}
	return nil
}

func (r Runtime) externalSendApproved(run app.AgentRun) bool {
	metadata, required := r.externalSendMetadata(run)
	if !required {
		return true
	}
	approval := r.externalSendApprovalForRun(run.ID)
	if approval == nil || approval.Status != "approved" || approval.ToolCallID == "" {
		return false
	}
	call, ok := r.store.GetToolCall(approval.ToolCallID)
	if !ok || call.Status != "completed_after_approval" {
		return false
	}
	return externalSendApprovalMatches(*approval, metadata, run)
}

func externalSendApprovalMatches(approval app.Approval, metadata externalSendMetadata, run app.AgentRun) bool {
	if run.MessageContext == nil || app.EndpointID(cleanOptionalString(approval.Arguments["endpoint_id"])) != metadata.EndpointID {
		return false
	}
	checks := [][2]string{
		{cleanOptionalString(approval.Arguments["owner_id"]), metadata.OwnerID},
		{cleanOptionalString(approval.Arguments["actor_id"]), metadata.ActorID},
		{cleanOptionalString(approval.Arguments["envelope_id"]), metadata.EnvelopeID},
		{cleanOptionalString(approval.Arguments["idempotency_key"]), metadata.IdempotencyKey},
		{cleanOptionalString(approval.Arguments["correlation_id"]), metadata.CorrelationID},
		{cleanOptionalString(approval.Arguments["causation_id"]), metadata.CausationID},
	}
	for _, check := range checks {
		if check[0] != check[1] {
			return false
		}
	}
	return strings.TrimSpace(run.MessageContext.OwnerID) == metadata.OwnerID &&
		run.MessageContext.Authorization.PrincipalID == metadata.ActorID
}

func (r Runtime) resumeExternalSendApproval(ctx context.Context, run app.AgentRun) (Result, bool, error) {
	_ = ctx
	approval := r.externalSendApprovalForRun(run.ID)
	if approval == nil {
		return Result{}, false, nil
	}
	if approval.Status != "approved" {
		return Result{}, false, nil
	}
	metadata, required := r.externalSendMetadata(run)
	if !required || !externalSendApprovalMatches(*approval, metadata, run) {
		return r.blockExternalSendResume(run, errors.New("external send approval no longer matches the frozen message control state")), true, nil
	}
	call, ok := r.store.GetToolCall(approval.ToolCallID)
	if !ok || call.Status != "completed_after_approval" {
		return Result{}, false, nil
	}
	now := time.Now().UTC()
	run.State = "completed"
	run.CompletedAt = &now
	r.store.SaveRun(run)
	r.store.AddAudit(app.AuditEvent{
		SessionID: run.SessionID, RunID: run.ID, Actor: "message_control",
		Type: "message.control.send_approved", Summary: "External send approval resolved for the frozen endpoint",
		Fields: map[string]any{"approval_id": approval.ID, "endpoint_id": metadata.EndpointID},
	})
	result := r.resultForExistingRun(run)
	// Connector dispatchers interpret Result.Approvals as the approvals still
	// requiring interaction, not as approval history.
	result.Approvals = approvalsForRun(r.store.ListApprovals("pending"), run.ID)
	return result, true, nil
}

func (r Runtime) blockExternalSendResume(run app.AgentRun, err error) Result {
	now := time.Now().UTC()
	run.State = "blocked"
	run.CompletedAt = &now
	r.store.SaveRun(run)
	r.store.AddAudit(app.AuditEvent{
		SessionID: run.SessionID, RunID: run.ID, Actor: "message_control",
		Type: "message.control.send_approval_invalid", Summary: err.Error(),
	})
	return r.resultForExistingRun(run)
}

func (r Runtime) protectExternalSendResult(run app.AgentRun, result *app.WorkflowResult) *app.WorkflowResult {
	if result == nil {
		return nil
	}
	if r.isExternalMediaPublication(run) {
		return result
	}
	_, required := r.externalSendMetadata(run)
	if !required || r.externalSendApproved(run) {
		return result
	}
	result.ReturnRoute = app.ReturnRoute{Mode: app.ReturnNowhere}
	if approval := r.externalSendApprovalForRun(run.ID); approval != nil {
		switch approval.Status {
		case "pending", "approved":
			result.Status = app.WorkflowResultWaiting
			result.Resume = &app.WorkflowResumeState{
				Kind: externalSendApprovalAction, Token: approval.ID,
				Data: map[string]any{"endpoint_id": cleanOptionalString(approval.Arguments["endpoint_id"])},
			}
		case "rejected":
			result.Status = app.WorkflowResultBlocked
			result.Resume = nil
			result.Error = &app.WorkflowResultError{Code: "external_send_rejected", Message: "External send approval was rejected."}
		}
	}
	return result
}
