package toolhub

import (
	"errors"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (h *ToolHub) notifyAskApproval(args map[string]any, sessionID, runID string) (Result, error) {
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
		Status:    "approval_pending",
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
		Status:     "pending",
		Summary:    summary,
		Reason:     reason,
		Resources:  []string{},
		Arguments:  args,
		CreatedAt:  now,
	}
	call.ApprovalID = approval.ID
	h.store.SaveToolCall(call)
	h.store.SaveApproval(approval)
	return Result{Output: map[string]any{
		"status":      "approval_requested",
		"approval_id": approval.ID,
		"tool_call":   call.ID,
	}}, nil
}
