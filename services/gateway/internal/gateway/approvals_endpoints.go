package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func (s *Server) listApprovals(w http.ResponseWriter, r *http.Request) {
	approvals, err := s.store.ListApprovals(r.Context(), app.ApprovalStatus(r.URL.Query().Get("status")))
	if err != nil {
		writeApprovalStoreError(w, err)
		return
	}
	for index := range approvals {
		presentation, err := s.approvalPresentation(r.Context(), approvals[index])
		if err != nil {
			writeSessionStoreError(w, err)
			return
		}
		approvals[index].Presentation = presentation
	}
	writeJSON(w, http.StatusOK, map[string]any{"approvals": approvals})
}

func (s *Server) approvalPresentation(ctx context.Context, approval app.Approval) (*app.ApprovalPresentation, error) {
	if approval.Tool != app.ToolWorkspaceDataAccess || approval.PolicyContext == nil ||
		approval.PolicyContext.PrincipalClass != app.PolicyPrincipalExternalMCPAI ||
		approval.PolicyContext.ResourceClass != app.PolicyResourceSparkClawWorkspaceData {
		return nil, nil
	}
	locatorsRaw, ok := approval.Arguments["locators"]
	if !ok {
		return nil, nil
	}
	raw, err := json.Marshal(locatorsRaw)
	if err != nil {
		return nil, nil
	}
	var locators []app.MessageMediaLocator
	if err := json.Unmarshal(raw, &locators); err != nil || len(locators) == 0 {
		return nil, nil
	}
	requester := "AI"
	session, ok, err := s.store.GetSession(ctx, approval.SessionID)
	if err != nil {
		return nil, err
	}
	if ok && strings.TrimSpace(session.Title) != "" {
		requester = session.Title
	}
	return &app.ApprovalPresentation{
		Kind:          "external_mcp_workspace_data_access",
		SessionID:     approval.SessionID,
		Requester:     requester,
		Locators:      locators,
		LocatorStatus: "unverified",
		AccessClass:   approval.PolicyContext.AccessClass,
		OutputClass:   approval.PolicyContext.OutputClass,
		ReturnRoute:   approval.PolicyContext.ReturnRoute,
		Scope:         "single_operation",
	}, nil
}

func (s *Server) approveApproval(w http.ResponseWriter, r *http.Request) {
	s.resolveApproval(w, r, app.ApprovalStatusApproved)
}

func (s *Server) rejectApproval(w http.ResponseWriter, r *http.Request) {
	s.resolveApproval(w, r, app.ApprovalStatusRejected)
}

func (s *Server) modifyApproval(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Note      string         `json:"note"`
		Args      map[string]any `json:"args"`
		Arguments map[string]any `json:"arguments"`
		Plan      *string        `json:"plan"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	unlock := s.lockApproval(r.PathValue("id"))
	defer unlock()
	newArgs := input.Arguments
	if newArgs == nil {
		newArgs = input.Args
	}
	approval, ok, err := s.findApproval(r.Context(), r.PathValue("id"))
	if err != nil {
		writeApprovalStoreError(w, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("approval not found"))
		return
	}
	if approval.Status != app.ApprovalStatusPending {
		writeError(w, http.StatusBadRequest, errors.New("approval already resolved"))
		return
	}
	if approval.Source == app.ApprovalSourceHappyTeamPlan {
		s.modifyHappyPlanApproval(w, r, approval, input.Plan, newArgs, input.Note)
		return
	}
	expectedApproval := approval
	if len(newArgs) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("modify requires args or arguments"))
		return
	}
	call, ok, err := s.store.GetToolCall(r.Context(), approval.ToolCallID)
	if err != nil {
		writeSessionStoreError(w, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("tool call not found"))
		return
	}
	if call.Status != app.ToolCallStatusApprovalPending {
		writeError(w, http.StatusBadRequest, fmt.Errorf("tool call cannot be modified from status %q", call.Status))
		return
	}
	if approval.PolicyContext != nil {
		writeError(w, http.StatusConflict, errors.New("context-bound workspace data approvals cannot be modified; reject and submit a new request"))
		return
	}
	if call.Tool == "email.send" {
		writeError(w, http.StatusConflict, errors.New("email send approvals cannot be modified; reject and submit a new request"))
		return
	}
	if s.tools.HasPPTXSealedCandidateArguments(approval.Arguments) {
		writeError(w, http.StatusConflict, errors.New("sealed PPTX candidate approvals cannot be modified; reject and submit a new request"))
		return
	}
	args := mergeApprovalArgs(approval.Arguments, newArgs)
	if def, ok := s.tools.Definition(approval.Tool); ok {
		if err := s.tools.Validate(approval.Tool, args); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		decision := s.policies.Decide(def, args, app.PolicyExecutionContext{})
		if !decision.Allowed {
			writeError(w, http.StatusForbidden, errors.New(decision.Reason))
			return
		}
		approval.Resources = decision.Resources
	}
	approval.Arguments = args
	call.Arguments = args
	persistedCall, saveErr := s.store.SaveToolCall(r.Context(), call)
	persistedCall, saveErr = store.ReconcileToolCallWrite(r.Context(), s.store, persistedCall, saveErr)
	if saveErr != nil {
		writeSessionStoreError(w, saveErr)
		return
	}
	call = persistedCall
	approvalCandidate, approvalErr := s.store.UpdatePendingApproval(r.Context(), store.NewApprovalUpdateWithNote(expectedApproval, approval, input.Note))
	approval, approvalErr = store.ReconcileApprovalWrite(r.Context(), s.store, approvalCandidate, approvalErr)
	if approvalErr != nil {
		writeApprovalStoreError(w, approvalErr)
		return
	}
	s.refreshTrace(r.Context(), approval.RunID)
	writeJSON(w, http.StatusOK, map[string]any{"approval": approval, "tool_call": call})
}

func (s *Server) validateMCPApproval(ctx context.Context, approval app.Approval) error {
	run, ok, err := s.store.GetRun(ctx, approval.RunID)
	if err != nil {
		return err
	}
	if !ok || run.MessageContext == nil || run.MessageContext.MCP == nil {
		return nil
	}
	operation, ok, err := s.store.GetMCPOperation(ctx, run.MessageContext.MCP.OperationID)
	if err != nil {
		return err
	}
	if !ok || operation.Invocation.RunID != run.ID {
		return errors.New("MCP operation is unavailable for this approval")
	}
	switch operation.State {
	case app.MCPOperationApprovalRequired, app.MCPOperationRunning:
		return nil
	default:
		return errors.New("MCP operation is no longer waiting for approval")
	}
}

func (s *Server) modifyHappyPlanApproval(w http.ResponseWriter, r *http.Request, approval app.Approval, plan *string, args map[string]any, note string) {
	if plan == nil || len(args) != 0 {
		writeError(w, http.StatusBadRequest, errors.New("Happy plan modification requires only the plan field"))
		return
	}
	if len(*plan) > app.MaxExternalApprovalPlanBytes {
		writeError(w, http.StatusRequestEntityTooLarge, errors.New("edited Happy plan exceeds the 1 MiB limit"))
		return
	}
	if approval.ExternalContext == nil || approval.ExternalContext.PlanAvailability != app.ExternalPlanAvailable {
		writeError(w, http.StatusConflict, errors.New("Happy task plan is temporarily unavailable; retry after the member machine reconnects"))
		return
	}
	expectedApproval := approval
	contextCopy := *approval.ExternalContext
	contextCopy.Plan = *plan
	contextCopy.PlanEdited = true
	approval.ExternalContext = &contextCopy
	candidate, err := s.store.UpdatePendingApproval(r.Context(), store.NewApprovalUpdateWithNote(expectedApproval, approval, note))
	approval, err = store.ReconcileApprovalWrite(r.Context(), s.store, candidate, err)
	if err != nil {
		writeApprovalStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"approval": approval, "tool_call": nil})
}

func (s *Server) resolveApproval(w http.ResponseWriter, r *http.Request, status app.ApprovalStatus) {
	var input struct {
		Note string `json:"note"`
	}
	_ = readJSON(r, &input)
	unlock := s.lockApproval(r.PathValue("id"))
	defer unlock()
	approval, ok, err := s.findApproval(r.Context(), r.PathValue("id"))
	if err != nil {
		writeApprovalStoreError(w, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("approval not found"))
		return
	}
	if approval.Status != app.ApprovalStatusPending {
		writeError(w, http.StatusBadRequest, errors.New("approval already resolved"))
		return
	}
	mcpRun := false
	if run, ok, err := s.store.GetRun(r.Context(), approval.RunID); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	} else if ok && run.MessageContext != nil && run.MessageContext.MCP != nil {
		mcpRun = true
	}
	if status == app.ApprovalStatusApproved {
		if err := s.validateMCPApproval(r.Context(), approval); err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
	}
	if approval.Source == app.ApprovalSourceHappyTeamPlan {
		s.resolveHappyPlanApproval(w, r, approval, status, input.Note)
		return
	}
	candidate, err := s.store.ResolveApproval(r.Context(), approval.ID, status, input.Note)
	approval, err = store.ReconcileApprovalWrite(r.Context(), s.store, candidate, err)
	if err != nil {
		writeApprovalStoreError(w, err)
		return
	}
	if status == app.ApprovalStatusApproved && mcpRun && s.mcpAccess != nil {
		operation, executionCtx, finishExecution, beginErr := s.mcpAccess.StartApprovalExecution(r.Context(), approval.RunID)
		if beginErr != nil {
			s.refreshTrace(r.Context(), approval.RunID)
			writeJSON(w, http.StatusOK, map[string]any{
				"approval": approval, "approval_status": approval.Status, "execution_status": operation.State,
				"execution_error": beginErr.Error(), "tool_call": nil, "workflow_result": nil, "delivery_receipt": nil,
			})
			return
		}
		s.startApprovedMCPExecution(approval, executionCtx, finishExecution)
		s.refreshTrace(r.Context(), approval.RunID)
		writeJSON(w, http.StatusAccepted, map[string]any{
			"approval": approval, "approval_status": approval.Status, "execution_status": operation.State,
			"operation": operation, "tool_call": nil, "workflow_result": nil, "delivery_receipt": nil,
		})
		return
	}
	var call *app.ToolCall
	var workflowResult *app.WorkflowResult
	var deliveryReceipt *app.DeliveryReceipt
	executionStatus := "not_started"
	resumed := false
	if status == app.ApprovalStatusApproved {
		executed, err := s.runtime.ExecuteApprovedToolCall(r.Context(), approval)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		call = &executed
		executionStatus = "succeeded"
		if executed.Status.Failed() {
			executionStatus = "failed"
		}
		if result, ok, err := s.runtime.ResumeRunAfterApproval(r.Context(), approval.SessionID, approval.RunID); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		} else if ok {
			resumed = true
			workflowResult = result.WorkflowResult
			if workflowResult != nil {
				executionStatus = string(workflowResult.Status)
			}
			if receipt, err := s.deliverAgentResult(r.Context(), result); err != nil {
				writeError(w, http.StatusBadGateway, err)
				return
			} else {
				deliveryReceipt = receipt
			}
		}
	}
	if status == app.ApprovalStatusRejected {
		if rejected, ok, err := s.store.GetToolCall(r.Context(), approval.ToolCallID); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		} else if ok {
			now := time.Now().UTC()
			rejected.Status = app.ToolCallStatusRejected
			rejected.Error = "owner rejected approval"
			rejected.CompletedAt = &now
			persisted, saveErr := s.store.SaveToolCall(r.Context(), rejected)
			persisted, saveErr = store.ReconcileToolCallWrite(r.Context(), s.store, persisted, saveErr)
			if saveErr != nil {
				writeError(w, http.StatusInternalServerError, saveErr)
				return
			}
			rejected = persisted
			call = &rejected
		}
	}
	if !resumed {
		if err := s.runtime.CompleteRunIfApprovalsResolved(r.Context(), approval.RunID); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}
	if status == app.ApprovalStatusRejected && mcpRun && s.mcpAccess != nil {
		if err := s.mcpAccess.FailApprovalExecution(r.Context(), approval.RunID, "approval_rejected", "The local owner rejected the pending action"); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		executionStatus = string(app.MCPOperationFailed)
	}
	s.refreshTrace(r.Context(), approval.RunID)
	writeJSON(w, http.StatusOK, map[string]any{
		"approval": approval, "approval_status": approval.Status, "execution_status": executionStatus,
		"tool_call": call, "workflow_result": workflowResult, "delivery_receipt": deliveryReceipt,
	})
}

func (s *Server) startApprovedMCPExecution(approval app.Approval, executionCtx context.Context, finishExecution func()) {
	s.streamWG.Add(1)
	go func() {
		defer s.streamWG.Done()
		defer finishExecution()
		defer s.refreshTrace(executionCtx, approval.RunID)

		executed, err := s.runtime.ExecuteApprovedToolCall(executionCtx, approval)
		if err != nil {
			s.recordMCPApprovalExecutionFailure(executionCtx, approval.RunID, "approval_execution_failed", "Approved tool execution could not be started")
			return
		}
		if err := executionCtx.Err(); err != nil {
			s.failCancelledMCPApprovalExecution(approval.RunID, err)
			return
		}
		if executed.Status.Failed() {
			s.recordMCPApprovalExecutionFailure(executionCtx, approval.RunID, "approval_tool_failed", "The approved tool execution failed")
			return
		}
		result, resumed, err := s.runtime.ResumeRunAfterApproval(executionCtx, approval.SessionID, approval.RunID)
		if err != nil {
			if executionCtx.Err() != nil {
				s.failCancelledMCPApprovalExecution(approval.RunID, executionCtx.Err())
				return
			}
			s.recordMCPApprovalExecutionFailure(executionCtx, approval.RunID, "workflow_resume_failed", "SparkClaw workflow resume failed after approval")
			return
		}
		if !resumed {
			pending, pendingErr := runHasPendingApproval(executionCtx, s.store, approval.RunID)
			if pendingErr != nil {
				slog.Error("failed to load pending MCP approvals", "run_id", approval.RunID, "code", store.StoreErrorCodeOf(pendingErr))
				if err := s.mcpAccess.RestoreApprovalRequired(executionCtx, approval.RunID); err != nil {
					slog.Error("failed to restore MCP approval-required state", "run_id", approval.RunID, "error", err)
				}
				return
			}
			if pending {
				if err := s.mcpAccess.RestoreApprovalRequired(executionCtx, approval.RunID); err != nil {
					slog.Error("failed to restore MCP approval-required state", "run_id", approval.RunID, "error", err)
				}
				return
			}
			if err := s.runtime.CompleteRunIfApprovalsResolved(executionCtx, approval.RunID); err != nil {
				slog.Error("failed to complete MCP run after approval", "run_id", approval.RunID, "error", err)
			}
			s.recordMCPApprovalExecutionFailure(executionCtx, approval.RunID, "workflow_resume_unavailable", "SparkClaw workflow could not continue after approval")
			return
		}
		if err := executionCtx.Err(); err != nil {
			s.failCancelledMCPApprovalExecution(approval.RunID, err)
			return
		}
		if _, err := s.deliverAgentResult(executionCtx, result); err != nil {
			if executionCtx.Err() != nil {
				s.failCancelledMCPApprovalExecution(approval.RunID, executionCtx.Err())
				return
			}
			s.recordMCPApprovalExecutionFailure(executionCtx, approval.RunID, "delivery_failed", "Approved MCP result delivery failed")
			return
		}
		if err := s.mcpAccess.RecordWorkflowResult(executionCtx, result); err != nil {
			slog.Error("failed to record MCP workflow result", "run_id", approval.RunID, "error", err)
		}
	}()
}

func (s *Server) failCancelledMCPApprovalExecution(runID string, err error) {
	code := "approval_execution_cancelled"
	message := "Approved MCP execution was cancelled"
	if errors.Is(err, context.DeadlineExceeded) {
		code = "approval_execution_expired"
		message = "Approved MCP execution exceeded the operation deadline"
	}
	s.recordMCPApprovalExecutionFailure(context.WithoutCancel(s.lifecycleCtx), runID, code, message)
}

func (s *Server) recordMCPApprovalExecutionFailure(ctx context.Context, runID, code, message string) {
	if err := s.mcpAccess.FailApprovalExecution(context.WithoutCancel(ctx), runID, code, message); err != nil {
		slog.Error("failed to persist MCP approval execution failure", "run_id", runID, "code", code, "error", err)
	}
}

func runHasPendingApproval(ctx context.Context, st store.ApprovalRepository, runID string) (bool, error) {
	approvals, err := st.ListApprovals(ctx, app.ApprovalStatusPending)
	if err != nil {
		return false, err
	}
	for _, approval := range approvals {
		if approval.RunID == runID {
			return true, nil
		}
	}
	return false, nil
}

func (s *Server) resolveHappyPlanApproval(w http.ResponseWriter, r *http.Request, approval app.Approval, status app.ApprovalStatus, note string) {
	if s.externalApprovalResolver == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("Happy approval integration is unavailable"))
		return
	}
	resolvedElsewhere, err := s.externalApprovalResolver.Resolve(r.Context(), approval, status)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	localStatus := status
	if resolvedElsewhere {
		localStatus = app.ApprovalStatusResolvedElsewhere
		if strings.TrimSpace(note) == "" {
			note = "Happy task was already resolved elsewhere"
		}
	}
	candidate, err := s.store.ResolveApproval(r.Context(), approval.ID, localStatus, note)
	resolved, err := store.ReconcileApprovalWrite(r.Context(), s.store, candidate, err)
	if err != nil {
		writeApprovalStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"approval": resolved, "tool_call": nil, "workflow_result": nil, "delivery_receipt": nil,
	})
}

func (s *Server) findApproval(ctx context.Context, id string) (app.Approval, bool, error) {
	return s.store.GetApproval(ctx, id)
}

func (s *Server) lockApproval(id string) func() {
	value, _ := s.approvalLocks.LoadOrStore(id, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func mergeApprovalArgs(current, patch map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range current {
		out[key] = value
	}
	for key, value := range patch {
		if strings.HasPrefix(key, "_") {
			continue
		}
		out[key] = value
	}
	return out
}

func writeApprovalStoreError(w http.ResponseWriter, err error) {
	switch store.StoreErrorCodeOf(err) {
	case store.StoreErrorInvalid:
		writeError(w, http.StatusBadRequest, errors.New("approval request is invalid"))
	case store.StoreErrorNotFound:
		writeError(w, http.StatusNotFound, errors.New("approval not found"))
	case store.StoreErrorConflict:
		writeError(w, http.StatusConflict, errors.New("approval changed or was already resolved"))
	case store.StoreErrorCanceled:
		writeError(w, http.StatusRequestTimeout, errors.New("approval request was canceled"))
	case store.StoreErrorTimeout:
		writeError(w, http.StatusGatewayTimeout, errors.New("approval operation timed out"))
	default:
		writeError(w, http.StatusServiceUnavailable, errors.New("approval service is unavailable"))
	}
}
