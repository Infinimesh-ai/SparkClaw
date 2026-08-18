package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// ExecuteApprovedToolCall runs the tool call behind an approval that was just
// resolved as approved and records the outcome on the tool call. Execution
// errors are stored on the call and returned as a nil error so callers can
// surface the failed call; only orchestration problems (missing call, wrong
// state, unknown tool) are returned as errors.
func (r Runtime) ExecuteApprovedToolCall(ctx context.Context, approval app.Approval) (app.ToolCall, error) {
	call, ok := r.store.GetToolCall(approval.ToolCallID)
	if !ok {
		return app.ToolCall{}, fmt.Errorf("approved tool call not found")
	}
	persistedApproval, ok := r.store.GetApproval(call.ApprovalID)
	if !ok || persistedApproval.ID != approval.ID || persistedApproval.ToolCallID != call.ID ||
		persistedApproval.SessionID != call.SessionID || persistedApproval.RunID != call.RunID ||
		persistedApproval.Tool != call.Tool {
		return app.ToolCall{}, fmt.Errorf("approval does not match its persisted tool call")
	}
	if persistedApproval.Status != "approved" {
		return app.ToolCall{}, fmt.Errorf("approval cannot execute from status %q", persistedApproval.Status)
	}
	approval = persistedApproval
	if call.Status != "approval_pending" {
		return app.ToolCall{}, fmt.Errorf("tool call cannot execute from status %q", call.Status)
	}
	if call.Tool == "notify.ask_approval" {
		now := time.Now().UTC()
		if isLegacyExternalSendApproval(approval) {
			call.Status = "failed_after_approval"
			call.CompletedAt = &now
			call.Error = "legacy external-send approval is retired; submit a fresh instruction"
			call.ErrorCode = string(app.ToolErrorPolicyBlocked)
			r.store.SaveToolCall(call)
			return call, nil
		}
		call.Status = "completed_after_approval"
		call.CompletedAt = &now
		call.Result = map[string]any{"status": "approval_confirmed"}
		call.Error = ""
		call.ErrorCode = ""
		call.ObservationSummary = CompressObservation(call.Tool, call.Result, r.observationSummaryLimit())
		call.ObservationRef = store.ArchiveToolObservation(ctx, r.store, r.artifacts, call, call.Result)
		r.store.SaveToolCall(call)
		return call, nil
	}
	workspaceDataApproval := call.Tool == app.ToolWorkspaceDataAccess
	if workspaceDataApproval {
		if err := r.validateWorkspaceDataAccessApproval(call, approval); err != nil {
			now := time.Now().UTC()
			call.Status = "failed_after_approval"
			call.CompletedAt = &now
			call.Error = err.Error()
			call.ErrorCode = string(app.ToolErrorPolicyBlocked)
			r.store.SaveToolCall(call)
			return call, nil
		}
	}
	if r.tools == nil {
		return app.ToolCall{}, fmt.Errorf("approval execution is not configured")
	}
	def, ok := r.tools.Definition(call.Tool)
	if !ok {
		return app.ToolCall{}, fmt.Errorf("tool %q not found", call.Tool)
	}
	if call.PolicyContext != nil && !workspaceDataApproval {
		if err := r.validateContextBoundToolApproval(call, approval, def); err != nil {
			now := time.Now().UTC()
			call.Status = "failed_after_approval"
			call.CompletedAt = &now
			call.Error = err.Error()
			call.ErrorCode = string(app.ToolErrorPolicyBlocked)
			r.store.SaveToolCall(call)
			return call, nil
		}
	}
	timeout := time.Duration(def.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := r.revalidateApprovedDocumentOperation(execCtx, call, def); err != nil {
		now := time.Now().UTC()
		call.Status = "failed_after_approval"
		call.CompletedAt = &now
		call.Error = err.Error()
		call.ErrorCode = string(app.ToolErrorCodeFrom(err))
		r.store.SaveToolCall(call)
		return call, nil
	}
	call.Status = "running_after_approval"
	r.store.SaveToolCall(call)
	result, err := r.tools.Execute(execCtx, call.Tool, call.Arguments, call.SessionID, call.RunID)
	now := time.Now().UTC()
	call.CompletedAt = &now
	if err != nil {
		call.Status = "failed_after_approval"
		call.Error = err.Error()
		call.ErrorCode = string(app.ToolErrorCodeFrom(err))
		if result.Output != nil {
			call.Result = result.Output
			call.ObservationRef = store.ArchiveToolObservation(execCtx, r.store, r.artifacts, call, archiveOutput(result, call.Result))
		}
		call.ObservationSummary = adaptToolResult(toolResultAdapterInput{Call: call, Output: call.Result, Err: err, ObservationRef: call.ObservationRef, MaxBytes: r.observationSummaryLimit()})
		r.store.SaveToolCall(call)
		return call, nil
	}
	call.Status = "completed_after_approval"
	call.Result = result.Output
	call.Error = ""
	call.ErrorCode = ""
	call.ObservationSummary = CompressObservation(call.Tool, result.Output, r.observationSummaryLimit())
	call.ObservationRef = store.ArchiveToolObservation(execCtx, r.store, r.artifacts, call, archiveOutput(result, call.Result))
	r.store.SaveToolCall(call)
	r.recordDocumentToolActivity(call)
	return call, nil
}

// CompleteRunIfApprovalsResolved marks an approval-pending run as completed
// once no pending approvals remain for it.
func (r Runtime) CompleteRunIfApprovalsResolved(runID string) {
	if runID == "" {
		return
	}
	run, ok := r.store.GetRun(runID)
	if !ok || run.State != "approval_pending" {
		return
	}
	for _, approval := range r.store.ListApprovals("pending") {
		if approval.RunID == runID {
			return
		}
	}
	now := time.Now().UTC()
	for _, approval := range approvalsForRun(r.store.ListApprovals(""), runID) {
		if approval.Status != "rejected" && !isLegacyExternalSendApproval(approval) {
			continue
		}
		run.State = "blocked"
		run.CompletedAt = &now
		r.store.SaveRun(run)
		return
	}
	run.State = "completed"
	run.CompletedAt = &now
	r.store.SaveRun(run)
}

func (r Runtime) observationSummaryLimit() int {
	if r.tools == nil {
		return 0
	}
	return r.tools.Config().Runtime.ObservationSummaryMaxBytes
}
