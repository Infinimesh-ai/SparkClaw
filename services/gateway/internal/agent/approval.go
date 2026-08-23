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
	call, ok, err := r.store.GetToolCall(ctx, approval.ToolCallID)
	if err != nil {
		return app.ToolCall{}, fmt.Errorf("load approved tool call: %w", err)
	}
	if !ok {
		return app.ToolCall{}, fmt.Errorf("approved tool call not found")
	}
	persistedApproval, ok, err := r.store.GetApproval(ctx, call.ApprovalID)
	if err != nil {
		return app.ToolCall{}, fmt.Errorf("load persisted approval: %w", err)
	}
	if !ok || persistedApproval.ID != approval.ID || persistedApproval.ToolCallID != call.ID ||
		persistedApproval.SessionID != call.SessionID || persistedApproval.RunID != call.RunID ||
		persistedApproval.Tool != call.Tool {
		return app.ToolCall{}, fmt.Errorf("approval does not match its persisted tool call")
	}
	if persistedApproval.Status != app.ApprovalStatusApproved {
		return app.ToolCall{}, fmt.Errorf("approval cannot execute from status %q", persistedApproval.Status)
	}
	approval = persistedApproval
	if call.Status != app.ToolCallStatusApprovalPending {
		return app.ToolCall{}, fmt.Errorf("tool call cannot execute from status %q", call.Status)
	}
	if call.Tool == "notify.ask_approval" {
		now := time.Now().UTC()
		if isLegacyExternalSendApproval(approval) {
			call.Status = app.ToolCallStatusFailedAfterApproval
			call.CompletedAt = &now
			call.Error = "legacy external-send approval is retired; submit a fresh instruction"
			call.ErrorCode = string(app.ToolErrorPolicyBlocked)
			if _, err := r.saveToolCall(ctx, call); err != nil {
				return app.ToolCall{}, fmt.Errorf("persist retired approved tool call: %w", err)
			}
			return call, nil
		}
		call.Status = app.ToolCallStatusCompletedAfterApproval
		call.CompletedAt = &now
		call.Result = map[string]any{"status": "approval_confirmed"}
		call.Error = ""
		call.ErrorCode = ""
		call.ObservationSummary = CompressObservation(call.Tool, call.Result, r.observationSummaryLimit())
		call.ObservationRef = store.ArchiveToolObservation(ctx, r.store, r.artifacts, call, call.Result)
		if _, err := r.saveToolCall(ctx, call); err != nil {
			return app.ToolCall{}, fmt.Errorf("persist confirmed approval tool call: %w", err)
		}
		return call, nil
	}
	workspaceDataApproval := call.Tool == app.ToolWorkspaceDataAccess
	if workspaceDataApproval {
		if err := r.validateWorkspaceDataAccessApproval(ctx, call, approval); err != nil {
			now := time.Now().UTC()
			call.Status = app.ToolCallStatusFailedAfterApproval
			call.CompletedAt = &now
			call.Error = err.Error()
			call.ErrorCode = string(app.ToolErrorPolicyBlocked)
			if _, saveErr := r.saveToolCall(ctx, call); saveErr != nil {
				return app.ToolCall{}, fmt.Errorf("persist rejected workspace approval: %w", saveErr)
			}
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
		if err := r.validateContextBoundToolApproval(ctx, call, approval, def); err != nil {
			now := time.Now().UTC()
			call.Status = app.ToolCallStatusFailedAfterApproval
			call.CompletedAt = &now
			call.Error = err.Error()
			call.ErrorCode = string(app.ToolErrorPolicyBlocked)
			if _, saveErr := r.saveToolCall(ctx, call); saveErr != nil {
				return app.ToolCall{}, fmt.Errorf("persist invalid context approval: %w", saveErr)
			}
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
		call.Status = app.ToolCallStatusFailedAfterApproval
		call.CompletedAt = &now
		call.Error = err.Error()
		call.ErrorCode = string(app.ToolErrorCodeFrom(err))
		if _, saveErr := r.saveToolCall(ctx, call); saveErr != nil {
			return app.ToolCall{}, fmt.Errorf("persist rejected approved document call: %w", saveErr)
		}
		return call, nil
	}
	call.Status = app.ToolCallStatusRunningAfterApproval
	if _, err := r.saveToolCall(ctx, call); err != nil {
		return app.ToolCall{}, fmt.Errorf("persist running approved tool call: %w", err)
	}
	result, err := r.tools.Execute(execCtx, call.Tool, call.Arguments, call.SessionID, call.RunID)
	now := time.Now().UTC()
	call.CompletedAt = &now
	if err != nil {
		call.Status = app.ToolCallStatusFailedAfterApproval
		call.Error = err.Error()
		call.ErrorCode = string(app.ToolErrorCodeFrom(err))
		if result.Output != nil {
			call.Result = result.Output
			call.ObservationRef = store.ArchiveToolObservation(execCtx, r.store, r.artifacts, call, archiveOutput(result, call.Result))
		}
		call.ObservationSummary = adaptToolResult(toolResultAdapterInput{Call: call, Output: call.Result, Err: err, ObservationRef: call.ObservationRef, MaxBytes: r.observationSummaryLimit()})
		if _, saveErr := r.saveToolCall(ctx, call); saveErr != nil {
			return app.ToolCall{}, fmt.Errorf("persist failed approved tool call: %w", saveErr)
		}
		return call, nil
	}
	call.Status = app.ToolCallStatusCompletedAfterApproval
	call.Result = result.Output
	call.Error = ""
	call.ErrorCode = ""
	call.ObservationSummary = CompressObservation(call.Tool, result.Output, r.observationSummaryLimit())
	call.ObservationRef = store.ArchiveToolObservation(execCtx, r.store, r.artifacts, call, archiveOutput(result, call.Result))
	if _, err := r.saveToolCall(ctx, call); err != nil {
		return app.ToolCall{}, fmt.Errorf("persist completed approved tool call: %w", err)
	}
	if err := r.recordDocumentToolActivity(execCtx, call); err != nil {
		return call, err
	}
	return call, nil
}

// CompleteRunIfApprovalsResolved marks an approval-pending run as completed
// once no pending approvals remain for it.
func (r Runtime) CompleteRunIfApprovalsResolved(ctx context.Context, runID string) error {
	if runID == "" {
		return nil
	}
	run, ok, err := r.store.GetRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("load approval run: %w", err)
	}
	if !ok || run.State != "approval_pending" {
		return nil
	}
	approvals, err := r.store.ListApprovals(ctx, "")
	if err != nil {
		return fmt.Errorf("load run approvals: %w", err)
	}
	for _, approval := range approvals {
		if approval.Status != app.ApprovalStatusPending {
			continue
		}
		if approval.RunID == runID {
			return nil
		}
	}
	now := time.Now().UTC()
	for _, approval := range approvalsForRun(approvals, runID) {
		if approval.Status != app.ApprovalStatusRejected && !isLegacyExternalSendApproval(approval) {
			continue
		}
		run.State = "blocked"
		run.CompletedAt = &now
		_, err := r.saveRun(ctx, run)
		return err
	}
	run.State = "completed"
	run.CompletedAt = &now
	_, err = r.saveRun(ctx, run)
	return err
}

func (r Runtime) saveApproval(ctx context.Context, approval app.Approval) (app.Approval, error) {
	candidate, err := r.store.SaveApproval(ctx, approval)
	return store.ReconcileApprovalWrite(ctx, r.store, candidate, err)
}

func (r Runtime) observationSummaryLimit() int {
	if r.tools == nil {
		return 0
	}
	return r.tools.Config().Runtime.ObservationSummaryMaxBytes
}
