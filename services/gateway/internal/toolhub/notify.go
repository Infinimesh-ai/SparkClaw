package toolhub

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func (h *ToolHub) notifyAskApproval(ctx context.Context, args map[string]any, sessionID, runID string) (Result, error) {
	summary := stringArg(args, "summary", "")
	if summary == "" {
		return Result{}, errors.New("summary cannot be empty")
	}
	reason := stringArg(args, "reason", "Manual confirmation requested.")
	now := time.Now().UTC()
	call := app.ToolCall{
		ID:        app.NewID("tc"),
		SessionID: sessionID,
		RunID:     runID,
		Tool:      "notify.ask_approval",
		Risk:      app.RiskDraft,
		Status:    app.ToolCallStatusApprovalPending,
		Arguments: args,
		StartedAt: now,
	}
	approval := app.Approval{
		ID:         app.NewID("ap"),
		Source:     app.ApprovalSourceTool,
		SessionID:  sessionID,
		RunID:      runID,
		ToolCallID: call.ID,
		Tool:       "notify.ask_approval",
		Risk:       app.RiskDraft,
		Status:     app.ApprovalStatusPending,
		Summary:    summary,
		Reason:     reason,
		Resources:  []string{},
		Arguments:  args,
		CreatedAt:  now,
	}
	call.ApprovalID = approval.ID
	candidate, err := h.store.SaveToolCall(ctx, call)
	if _, err = store.ReconcileToolCallWrite(ctx, h.store, candidate, err); err != nil {
		return Result{}, fmt.Errorf("persist approval tool call: %w", err)
	}
	approvalCandidate, err := h.store.SaveApproval(ctx, approval)
	approval, err = store.ReconcileApprovalWrite(ctx, h.store, approvalCandidate, err)
	if err != nil {
		return Result{}, fmt.Errorf("persist approval request: %w", err)
	}
	return Result{Output: map[string]any{
		"status":      "approval_requested",
		"approval_id": approval.ID,
		"tool_call":   call.ID,
	}}, nil
}
