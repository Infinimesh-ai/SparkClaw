package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func (r Runtime) bindIntegrationRun(ctx context.Context, runID string) (context.Context, func()) {
	if r.integrationRuns == nil {
		return ctx, func() {}
	}
	boundCtx, finish := r.integrationRuns.Begin(ctx, runID)
	return boundCtx, func() {
		persisted, ok, err := r.store.GetRun(context.WithoutCancel(boundCtx), runID)
		suspend := err != nil || (ok && integrationRunCanResume(persisted.State))
		finish(suspend)
	}
}

func integrationRunCanResume(state string) bool {
	return state == "approval_pending" || state == "browser_login_blocked"
}

func integrationCredentialChangeCause(ctx context.Context) error {
	cause := context.Cause(ctx)
	switch app.ToolErrorCodeFrom(cause) {
	case app.ToolErrorInfoCredentialsChanged, app.ToolErrorLocalMindCredentialsChanged:
		return cause
	default:
		return nil
	}
}

func integrationCredentialPersistenceContext(ctx context.Context) context.Context {
	if integrationCredentialChangeCause(ctx) != nil {
		return context.WithoutCancel(ctx)
	}
	return ctx
}

// cancelExecutionForCredentialChange stops the run's pending approvals and
// rewrites the finished execution as cancelled when the bound integration
// credentials changed while it ran. The returned context is detached from
// the cancelled parent so the terminal state can still be persisted.
func (r Runtime) cancelExecutionForCredentialChange(ctx context.Context, runID string, execution *workflowExecutionResult, cause error) (context.Context, error) {
	ctx = context.WithoutCancel(ctx)
	if err := r.stopPendingApprovalsForIntegrationChange(ctx, runID, cause); err != nil {
		return ctx, err
	}
	execution.Cancelled = true
	execution.FailureCode = ""
	execution.FinalAnswer = cause.Error()
	execution.FinalAnswerStreamed = false
	execution.Approvals = nil
	return ctx, nil
}

func (r Runtime) completeIntegrationCredentialChangedRun(ctx context.Context, run app.AgentRun, cause error) (Result, error) {
	now := time.Now().UTC()
	if err := r.stopPendingApprovalsForIntegrationChange(ctx, run.ID, cause); err != nil {
		return Result{}, err
	}
	run.State = "cancelled"
	run.CompletedAt = &now
	run.Summary = cause.Error()
	var err error
	if run, err = r.saveRun(ctx, run); err != nil {
		return Result{}, fmt.Errorf("persist credential-changed run: %w", err)
	}
	storedToolCalls, err := r.store.ListToolCalls(ctx, run.SessionID)
	if err != nil {
		return Result{}, fmt.Errorf("load credential-changed tool calls: %w", err)
	}
	toolCalls := toolCallsForRun(storedToolCalls, run.ID)
	storedApprovals, err := r.store.ListApprovals(ctx, "")
	if err != nil {
		return Result{}, fmt.Errorf("load credential-changed approvals: %w", err)
	}
	approvals := approvalsForRun(storedApprovals, run.ID)

	var route *app.RouteDecision
	var workflowResult *app.WorkflowResult
	if run.Workflow != nil {
		value := run.Workflow.Route
		route = &value
		workflowResult, err = r.workflowResultForRun(ctx, run, value, run.Workflow.ReturnRoute, run.Summary)
	} else if run.MessageContext != nil {
		value := run.MessageContext.Route
		route = &value
		workflowResult, err = r.workflowResultForDispatchFailure(ctx, run, value, run.MessageContext.ReturnRoute, run.Summary)
	}
	if err != nil {
		return Result{}, err
	}
	setIntegrationCredentialChangeResultError(workflowResult, cause)
	assistant, err := r.persistWorkflowAssistantMessage(ctx, run, workflowResult, now)
	if err != nil {
		return Result{}, fmt.Errorf("persist credential-changed response: %w", err)
	}
	episode := summarizeEpisode("", run, toolCalls, approvals, run.Summary, now)
	if _, err := r.store.SaveEpisodeSummary(ctx, episode); err != nil {
		return Result{}, fmt.Errorf("persist credential-changed episode: %w", err)
	}
	feedback, err := r.store.ListRunFeedback(ctx, run.ID)
	if err != nil {
		return Result{}, fmt.Errorf("load credential-changed feedback: %w", err)
	}
	r.writeTrace(ctx, run, modelrouter.ChatResult{}, toolCalls, approvals, feedback, &episode)
	return Result{
		Run: run, Message: assistant, ToolCalls: toolCalls, Approvals: approvals,
		RouteDecision: route, WorkflowResult: workflowResult,
	}, nil
}

func (r Runtime) stopPendingApprovalsForIntegrationChange(ctx context.Context, runID string, cause error) error {
	approvals, err := r.store.ListApprovals(ctx, app.ApprovalStatusPending)
	if err != nil {
		return fmt.Errorf("load pending approvals after credential change: %w", err)
	}
	for _, approval := range approvalsForRun(approvals, runID) {
		candidate, resolveErr := r.store.ResolveApproval(ctx, approval.ID, app.ApprovalStatusResolvedElsewhere, cause.Error())
		if _, resolveErr = store.ReconcileApprovalWrite(ctx, r.store, candidate, resolveErr); resolveErr != nil {
			return fmt.Errorf("stop approval after credential change: %w", resolveErr)
		}
		call, ok, loadErr := r.store.GetToolCall(ctx, approval.ToolCallID)
		if loadErr != nil {
			return fmt.Errorf("load approval tool call after credential change: %w", loadErr)
		}
		if !ok || call.Status != app.ToolCallStatusApprovalPending {
			continue
		}
		now := time.Now().UTC()
		call.Status = app.ToolCallStatusFailed
		call.CompletedAt = &now
		call.Error = cause.Error()
		call.ErrorCode = string(app.ToolErrorCodeFrom(cause))
		call.ObservationSummary = adaptToolResult(toolResultAdapterInput{Call: call, Err: cause, MaxBytes: r.observationSummaryLimit()})
		if _, saveErr := r.saveToolCall(ctx, call); saveErr != nil {
			return fmt.Errorf("stop approval tool call after credential change: %w", saveErr)
		}
	}
	return nil
}

func setIntegrationCredentialChangeResultError(result *app.WorkflowResult, cause error) {
	if result == nil || cause == nil {
		return
	}
	result.Error = &app.WorkflowResultError{Code: string(app.ToolErrorCodeFrom(cause)), Message: cause.Error()}
}
