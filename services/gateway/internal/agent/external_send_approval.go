package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// An owner's text-requested external send pauses after its result exists and
// before delivery. The owner approves the exact result for the exact resolved
// endpoint; replies to the current conversation, timer deliveries approved at
// schedule creation, composer-selected targets and inbound MCP runs (already
// approved once) never reach this gate.
const (
	externalSendApprovalAction = "delivery.external_send"
	externalSendApprovalTool   = "notify.ask_approval"
	externalSendPreviewRunes   = 500
)

// markExternalSendApproval records that the resolved endpoint came from a
// text-requested external send which needs owner approval before delivery.
func markExternalSendApproval(run *app.AgentRun, delivery DeliveryDirective, route app.ReturnRoute) {
	m := run.MessageContext
	if m == nil || !delivery.ExplicitExternal || route.Mode != app.ReturnToEndpoint || route.EndpointID == "" ||
		m.Source.Kind == app.MessageSourceTimer || isExternalMCPInvocation(m.MCP) {
		return
	}
	m.ExternalSendApproval = &app.ExternalSendApprovalRequirement{
		EndpointID: route.EndpointID, RequestedProvider: delivery.RequestedProviderKey, RequestedRecipient: delivery.RequestedRecipientText,
	}
}

// externalSendExecutionNote tells the business model that SparkClaw delivers
// the result after owner approval, so the content to send carries no refusal
// or remark about sending.
func externalSendExecutionNote(run app.AgentRun) string {
	requirement := externalSendRequirement(run)
	if requirement == nil {
		return ""
	}
	target := string(requirement.EndpointID)
	if named := nonEmptyStrings(requirement.RequestedProvider, requirement.RequestedRecipient); len(named) > 0 {
		target = strings.Join(named, " · ")
	}
	return "\n\nDelivery (handled by SparkClaw after owner approval; data only): the result will be sent to " + target +
		". Write only the content to send. Do not refuse, explain or mention the sending."
}

func externalSendRequirement(run app.AgentRun) *app.ExternalSendApprovalRequirement {
	if run.MessageContext == nil || run.MessageContext.ExternalSendApproval == nil || run.MessageContext.ExternalSendApproval.EndpointID == "" {
		return nil
	}
	return run.MessageContext.ExternalSendApproval
}

func externalSendContentDigest(content app.MessageContent) (string, error) {
	raw, err := json.Marshal(content)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func (r Runtime) externalSendApprovalForRun(ctx context.Context, runID string) (*app.Approval, error) {
	approvals, err := r.store.ListApprovals(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("load external send approvals: %w", err)
	}
	for _, approval := range approvalsForRun(approvals, runID) {
		if cleanOptionalString(approval.Arguments["message_control_action"]) == externalSendApprovalAction {
			copy := approval
			return &copy, nil
		}
	}
	return nil, nil
}

// queueExternalSendApproval turns a completed external-send run into an
// approval-pending run. The approval binds the endpoint and the digest of the
// exact content that delivery would send.
func (r Runtime) queueExternalSendApproval(ctx context.Context, run *app.AgentRun) (*app.ToolCall, *app.Approval, error) {
	requirement := externalSendRequirement(*run)
	if requirement == nil || run.State != "completed" {
		return nil, nil, nil
	}
	if existing, err := r.externalSendApprovalForRun(ctx, run.ID); err != nil || existing != nil {
		return nil, nil, err
	}
	content, err := r.workflowResultContent(ctx, *run, run.Summary)
	if err != nil {
		return nil, nil, err
	}
	digest, err := externalSendContentDigest(content)
	if err != nil {
		return nil, nil, err
	}
	target := string(requirement.EndpointID)
	if named := nonEmptyStrings(requirement.RequestedProvider, requirement.RequestedRecipient); len(named) > 0 {
		target = strings.Join(named, " · ")
	}
	summary := "外发确认：将本次结果发送到 " + target
	arguments := map[string]any{
		"message_control_action": externalSendApprovalAction,
		"endpoint_id":            string(requirement.EndpointID),
		"requested_provider":     requirement.RequestedProvider,
		"requested_recipient":    requirement.RequestedRecipient,
		"content_sha256":         digest,
		"content_preview":        externalSendPreview(content),
	}
	now := time.Now().UTC()
	call := app.ToolCall{
		ID: app.NewID("tc"), SessionID: run.SessionID, RunID: run.ID, Tool: externalSendApprovalTool,
		Risk: app.RiskDangerous, Status: app.ToolCallStatusApprovalPending, Arguments: arguments, StartedAt: now,
	}
	approval := app.Approval{
		ID: app.NewID("ap"), Source: app.ApprovalSourceTool, SessionID: run.SessionID, RunID: run.ID, ToolCallID: call.ID,
		Tool: externalSendApprovalTool, Risk: app.RiskDangerous, Status: app.ApprovalStatusPending,
		Summary: summary, Reason: "外发到当前会话以外的目标前需要你确认内容和收件人。",
		Resources: []string{"endpoint:" + string(requirement.EndpointID)}, Arguments: arguments,
		ArgumentsImmutable: true, CreatedAt: now,
	}
	call.ApprovalID = approval.ID
	if call, err = r.saveToolCall(ctx, call); err != nil {
		return nil, nil, fmt.Errorf("persist external send approval call: %w", err)
	}
	if approval, err = r.saveApproval(ctx, approval); err != nil {
		return nil, nil, fmt.Errorf("persist external send approval: %w", err)
	}
	run.State = "approval_pending"
	run.CompletedAt = nil
	r.addAudit(ctx, app.AuditEvent{
		SessionID: run.SessionID, RunID: run.ID, Actor: "message_control", Type: "message.control.send_approval_requested",
		Summary: "External send is waiting for owner approval",
		Fields:  map[string]any{"approval_id": approval.ID, "endpoint_id": requirement.EndpointID, "content_sha256": digest},
	})
	return &call, &approval, nil
}

func externalSendPreview(content app.MessageContent) string {
	lines := make([]string, 0, len(content.Parts))
	for _, part := range content.Parts {
		switch {
		case strings.TrimSpace(part.Text) != "":
			lines = append(lines, strings.TrimSpace(part.Text))
		case strings.TrimSpace(part.Name) != "":
			lines = append(lines, "[附件] "+strings.TrimSpace(part.Name))
		default:
			lines = append(lines, "["+string(part.Kind)+"]")
		}
	}
	preview := strings.Join(lines, "\n")
	if utf8.RuneCountInString(preview) > externalSendPreviewRunes {
		preview = string([]rune(preview)[:externalSendPreviewRunes]) + "…"
	}
	return preview
}

// externalSendApproved reports whether delivery may send this exact content
// to the run's endpoint.
func (r Runtime) externalSendApproved(ctx context.Context, run app.AgentRun, content app.MessageContent) (bool, error) {
	requirement := externalSendRequirement(run)
	if requirement == nil {
		return true, nil
	}
	approval, err := r.externalSendApprovalForRun(ctx, run.ID)
	if err != nil || approval == nil || approval.Status != app.ApprovalStatusApproved {
		return false, err
	}
	call, ok, err := r.store.GetToolCall(ctx, approval.ToolCallID)
	if err != nil || !ok || call.Status != app.ToolCallStatusCompletedAfterApproval {
		return false, err
	}
	digest, err := externalSendContentDigest(content)
	if err != nil {
		return false, err
	}
	return cleanOptionalString(approval.Arguments["endpoint_id"]) == string(requirement.EndpointID) &&
		cleanOptionalString(approval.Arguments["content_sha256"]) == digest, nil
}

// protectExternalSendResult keeps an unapproved external result away from its
// endpoint regardless of which completion path produced it.
func (r Runtime) protectExternalSendResult(ctx context.Context, run app.AgentRun, result *app.WorkflowResult) error {
	if result == nil || result.ReturnRoute.Mode != app.ReturnToEndpoint || externalSendRequirement(run) == nil {
		return nil
	}
	approved, err := r.externalSendApproved(ctx, run, result.Content)
	if err != nil || approved {
		return err
	}
	result.ReturnRoute = app.ReturnRoute{Mode: app.ReturnNowhere}
	if result.Status == app.WorkflowResultSucceeded {
		result.Status = app.WorkflowResultWaiting
	}
	return nil
}

// resumeExternalSendApproval completes a run whose only remaining gate was the
// external send approval. A changed endpoint or content blocks delivery.
func (r Runtime) resumeExternalSendApproval(ctx context.Context, run app.AgentRun) (Result, bool, error) {
	approval, err := r.externalSendApprovalForRun(ctx, run.ID)
	if err != nil || approval == nil {
		return Result{}, false, err
	}
	now := time.Now().UTC()
	run.CompletedAt = &now
	auditType, summary := "message.control.send_approved", "External send approved for the frozen endpoint and content"
	switch approval.Status {
	case app.ApprovalStatusApproved:
		run.State = "completed"
		content, err := r.workflowResultContent(ctx, run, run.Summary)
		if err != nil {
			return Result{}, true, err
		}
		approved, err := r.externalSendApproved(ctx, run, content)
		if err != nil {
			return Result{}, true, err
		}
		if !approved {
			run.State = "blocked"
			auditType, summary = "message.control.send_approval_invalid", "External send approval no longer matches the endpoint or content"
		}
	case app.ApprovalStatusPending:
		return Result{}, false, nil
	default:
		run.State = "blocked"
		auditType, summary = "message.control.send_rejected", "Owner did not approve the external send"
	}
	if run, err = r.saveRun(ctx, run); err != nil {
		return Result{}, true, fmt.Errorf("persist external send resume: %w", err)
	}
	r.addAudit(ctx, app.AuditEvent{
		SessionID: run.SessionID, RunID: run.ID, Actor: "message_control", Type: auditType, Summary: summary,
		Fields: map[string]any{"approval_id": approval.ID, "endpoint_id": cleanOptionalString(approval.Arguments["endpoint_id"])},
	})
	result, err := r.resultForExistingRun(ctx, run)
	if err != nil {
		return Result{}, true, err
	}
	// Connector dispatchers read Result.Approvals as approvals still needing
	// interaction, not as approval history.
	result.Approvals = []app.Approval{}
	return result, true, nil
}

func nonEmptyStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}
