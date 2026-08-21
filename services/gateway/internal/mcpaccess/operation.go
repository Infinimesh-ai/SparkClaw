package mcpaccess

import (
	"context"
	"errors"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func applyWorkflowResultToOperation(operation *app.MCPOperation, status app.WorkflowResultStatus, resultErr *app.WorkflowResultError) {
	operation.ErrorCode = ""
	operation.ErrorMessage = ""
	switch status {
	case app.WorkflowResultSucceeded:
		operation.State = app.MCPOperationSucceeded
	case app.WorkflowResultWaiting:
		operation.State = app.MCPOperationApprovalRequired
		operation.CompletedAt = nil
		return
	case app.WorkflowResultBlocked, app.WorkflowResultFailed:
		operation.State = app.MCPOperationFailed
		operation.ErrorCode = "workflow_" + string(status)
		operation.ErrorMessage = "SparkClaw workflow execution did not succeed"
		if resultErr != nil {
			if resultErr.Code != "" {
				operation.ErrorCode = resultErr.Code
			}
			if resultErr.Message != "" {
				operation.ErrorMessage = resultErr.Message
			}
		}
	default:
		operation.State = app.MCPOperationFailed
		operation.ErrorCode = "workflow_result_invalid"
		operation.ErrorMessage = "SparkClaw produced an unsupported workflow result state"
	}
	now := time.Now().UTC()
	operation.CompletedAt = &now
}

func updateOperationRecord(ctx context.Context, st store.Store, id string, mutate func(*app.MCPOperation) bool) (app.MCPOperation, bool, error) {
	for range maxOperationUpdateAttempts {
		if err := ctx.Err(); err != nil {
			return app.MCPOperation{}, false, err
		}
		operation, ok := st.GetMCPOperation(id)
		if !ok {
			return app.MCPOperation{}, false, errors.New("MCP operation not found")
		}
		if !mutate(&operation) {
			return operation, false, nil
		}
		updated, err := st.UpdateMCPOperation(operation, operation.Version)
		if err == nil {
			return updated, true, nil
		}
		if !errors.Is(err, store.ErrMCPOperationVersionConflict) {
			return operation, false, err
		}
	}
	if err := ctx.Err(); err != nil {
		return app.MCPOperation{}, false, err
	}
	operation, ok := st.GetMCPOperation(id)
	if !ok {
		return app.MCPOperation{}, false, errors.New("MCP operation not found after version conflict")
	}
	return operation, false, store.ErrMCPOperationVersionConflict
}

func rejectPendingApprovals(ctx context.Context, st store.Store, operation app.MCPOperation) error {
	for _, approval := range st.ListApprovals("pending") {
		if approval.RunID == operation.Invocation.RunID {
			if _, err := st.ResolveApproval(approval.ID, "rejected", "MCP operation cannot continue approval-backed execution"); err != nil {
				return err
			}
			if call, ok, err := st.GetToolCall(ctx, approval.ToolCallID); err != nil {
				return err
			} else if ok && call.Status == "approval_pending" {
				now := time.Now().UTC()
				call.Status = "rejected"
				call.Error = "MCP operation cannot continue approval-backed execution"
				call.CompletedAt = &now
				candidate, saveErr := st.SaveToolCall(ctx, call)
				if _, saveErr = store.ReconcileToolCallWrite(ctx, st, candidate, saveErr); saveErr != nil {
					return saveErr
				}
			}
		}
	}
	if run, ok, err := st.GetRun(ctx, operation.Invocation.RunID); err != nil {
		return err
	} else if ok && run.State == "approval_pending" {
		now := time.Now().UTC()
		run.State = "blocked"
		run.CompletedAt = &now
		candidate, saveErr := st.SaveRun(ctx, run)
		if _, saveErr = store.ReconcileRunWrite(ctx, st, candidate, saveErr); saveErr != nil {
			return saveErr
		}
	}
	return nil
}
