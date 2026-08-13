package mcpaccess

import (
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
		if operation.Invocation.AllowApproval {
			operation.State = app.MCPOperationApprovalRequired
			operation.CompletedAt = nil
			return
		}
		operation.State = app.MCPOperationFailed
		operation.ErrorCode = "approval_not_granted"
		operation.ErrorMessage = "This MCP binding did not grant approval-backed execution for the invocation"
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

func updateOperationRecord(st store.Store, id string, mutate func(*app.MCPOperation) bool) (app.MCPOperation, bool, error) {
	for range maxOperationUpdateAttempts {
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
	operation, ok := st.GetMCPOperation(id)
	if !ok {
		return app.MCPOperation{}, false, errors.New("MCP operation not found after version conflict")
	}
	return operation, false, store.ErrMCPOperationVersionConflict
}

func rejectPendingApprovals(st store.Store, operation app.MCPOperation) {
	for _, approval := range st.ListApprovals("pending") {
		if approval.RunID == operation.Invocation.RunID {
			if _, err := st.ResolveApproval(approval.ID, "rejected", "MCP operation cannot continue approval-backed execution"); err != nil {
				continue
			}
			if call, ok := st.GetToolCall(approval.ToolCallID); ok && call.Status == "approval_pending" {
				now := time.Now().UTC()
				call.Status = "rejected"
				call.Error = "MCP operation cannot continue approval-backed execution"
				call.CompletedAt = &now
				st.SaveToolCall(call)
			}
		}
	}
	if run, ok := st.GetRun(operation.Invocation.RunID); ok && run.State == "approval_pending" {
		now := time.Now().UTC()
		run.State = "blocked"
		run.CompletedAt = &now
		st.SaveRun(run)
	}
}
