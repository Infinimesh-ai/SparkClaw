package agent

import (
	"context"
	"errors"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const legacyExternalSendApprovalAction = "message_control.external_send"

func isLegacyExternalSendApproval(approval app.Approval) bool {
	return cleanOptionalString(approval.Arguments["message_control_action"]) == legacyExternalSendApprovalAction
}

func (r Runtime) legacyExternalSendApprovalForRun(runID string) *app.Approval {
	for _, approval := range approvalsForRun(r.store.ListApprovals(""), runID) {
		if isLegacyExternalSendApproval(approval) {
			copy := approval
			return &copy
		}
	}
	return nil
}

func (r Runtime) blockLegacyExternalSendApproval(ctx context.Context, run app.AgentRun, approval app.Approval) Result {
	result := r.blockPersistedWorkflowResume(ctx, run, requestContentForRun(r.store.ListMessages(run.SessionID), run),
		errors.New("legacy external-send approval is retired; submit a fresh instruction"))
	r.store.AddAudit(app.AuditEvent{
		SessionID: run.SessionID, RunID: run.ID, Actor: "policy", Type: "policy.legacy_external_send_blocked",
		Summary: "Blocked a legacy destination-based approval from resuming delivery",
		Fields:  map[string]any{"approval_id": approval.ID},
	})
	return result
}
