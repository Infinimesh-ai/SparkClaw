package agent

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/skills"
)

type matchedWorkflowDispatch struct {
	Run     app.AgentRun
	Profile workflowProfile
	Hint    TaskHint
	Skills  []skills.Skill
	Tools   []app.ToolDefinition
}

func (r Runtime) resumeMatchedWorkflowAfterApproval(ctx context.Context, run app.AgentRun, content string, seedCalls []app.ToolCall) (Result, bool, error) {
	if err := r.capabilities.ValidateDecision(run.Workflow.Route); err != nil {
		result := r.blockPersistedWorkflowResume(ctx, run, content, err)
		return result, true, nil
	}
	profile, err := r.profiles.Get(run.Workflow.Plan.ProfileID)
	if err != nil {
		result := r.blockPersistedWorkflowResume(ctx, run, content, err)
		return result, true, nil
	}
	for _, call := range seedCalls {
		if call.WorkflowID == "" || workflowAppliedToolCall(run.Workflow, call.ID) {
			continue
		}
		definition, ok := r.tools.Definition(call.Tool)
		if !ok {
			result := r.blockPersistedWorkflowResume(ctx, run, content, errors.New("approved workflow tool is no longer registered"))
			return result, true, nil
		}
		outcome, adaptErr := adaptWorkflowOutcome(definition, call)
		if adaptErr != nil {
			result := r.blockPersistedWorkflowResume(ctx, run, content, adaptErr)
			return result, true, nil
		}
		assessment := profile.Assess(run.Workflow, outcome)
		changed, applyErr := applyWorkflowOutcome(&run, outcome, assessment)
		r.store.SaveRun(run)
		r.auditWorkflowOutcome(run, outcome, assessment, changed, applyErr)
		if applyErr != nil && assessment.Status != app.AssessmentBlocked {
			result := r.blockPersistedWorkflowResume(ctx, run, content, applyErr)
			return result, true, nil
		}
	}
	workflowExecution := reactRunResult{}
	if run.Workflow.Status == app.WorkflowStatusRunning {
		hint := profile.Hint(run.Workflow)
		visibleTools, exposeErr := r.materializeActiveWorkflowTools(ctx, run, r.workflowActorRef(run.SessionID), &hint)
		if exposeErr != nil {
			result := r.blockPersistedWorkflowResume(ctx, run, content, exposeErr)
			return result, true, nil
		}
		if refreshed, ok := r.store.GetRun(run.ID); ok {
			run = refreshed
		}
		run.State = "reacting"
		run.CompletedAt = nil
		r.store.SaveRun(run)
		workflowExecution = r.runWorkflowWithSeed(
			ctx, run.SessionID, run, content, profile, hint.taskHint(), r.exactWorkflowSkills(run.Workflow.Plan.SkillIDs), visibleTools,
			seedCalls, observationsForResume(seedCalls),
		)
		if refreshed, ok := r.store.GetRun(run.ID); ok {
			run = refreshed
		}
	}

	now := time.Now().UTC()
	if len(workflowExecution.Approvals) > 0 {
		run.State = "approval_pending"
		run.CompletedAt = nil
	} else if run.Workflow.Status == app.WorkflowStatusSucceeded {
		run.State = "completed"
		run.CompletedAt = &now
	} else {
		run.State = "blocked"
		run.CompletedAt = &now
	}
	currentToolCalls := toolCallsForRun(r.store.ListToolCalls(run.SessionID), run.ID)
	run.Summary = summarizeRun(workflowExecution.Chat, workflowExecution.Observations, workflowExecution.Approvals)
	if strings.TrimSpace(workflowExecution.FinalAnswer) != "" {
		run.Summary = workflowExecution.FinalAnswer
	}
	run.Summary = r.applyGroundedSummary(run.SessionID, run.ID, content, run.Summary, currentToolCalls)
	if strings.TrimSpace(run.Summary) == "" {
		run.Summary = "The matched workflow completed after its approved action."
	}
	r.store.SaveRun(run)
	allApprovals := approvalsForRun(r.store.ListApprovals(""), run.ID)
	feedback := r.store.ListRunFeedback(run.ID)
	episode := summarizeEpisode(content, run, currentToolCalls, allApprovals, run.Summary, now)
	r.store.SaveEpisodeSummary(episode)
	assistant := r.store.AddMessage(app.Message{SessionID: run.SessionID, RunID: run.ID, Role: "assistant", Content: run.Summary, CreatedAt: now})
	r.store.AddAudit(app.AuditEvent{SessionID: run.SessionID, RunID: run.ID, Actor: "workflow_dispatcher", Type: "workflow.resumed_after_approval", Summary: string(run.Workflow.Status)})
	r.writeTrace(ctx, run, modelrouter.ChatResult{}, currentToolCalls, allApprovals, feedback, &episode)
	route := run.Workflow.Route
	return Result{
		Run: run, Message: assistant, ToolCalls: workflowExecution.ToolCalls, Approvals: workflowExecution.Approvals, RouteDecision: &route,
		WorkflowResult: workflowResultForRun(run, route, run.Workflow.ReturnRoute, run.Summary),
	}, true, nil
}

func workflowAppliedToolCall(state *app.WorkflowState, toolCallID string) bool {
	for _, node := range state.Nodes {
		if containsString(node.ToolCallIDs, toolCallID) {
			return true
		}
	}
	return false
}

func (r Runtime) dispatchMatchedWorkflow(ctx context.Context, run app.AgentRun, route app.RouteDecision, returnRoute app.ReturnRoute, sourceTurnID string) (matchedWorkflowDispatch, error) {
	resolved, err := r.profiles.Resolve(r.capabilities, route, sourceTurnID)
	if err != nil {
		return matchedWorkflowDispatch{}, err
	}
	run.Workflow = newWorkflowState(route, returnRoute, resolved.Intent, resolved.Plan)
	run.State = "routing"
	r.store.SaveRun(run)
	r.store.AddAudit(app.AuditEvent{
		SessionID: run.SessionID, RunID: run.ID, Actor: "workflow_dispatcher", Type: "workflow.dispatched",
		Summary: "Dispatched a validated capability leaf to its exact workflow contract",
		Fields: map[string]any{
			"catalog_revision": route.CatalogRevision, "capability_path": route.CapabilityPath,
			"workflow_id": resolved.Plan.ProfileID, "workflow_revision": resolved.Plan.ProfileRevision,
			"plan_digest": run.Workflow.PlanDigest, "active_node_ids": run.Workflow.ActiveNodeIDs,
		},
	})
	workflowHint := resolved.Profile.Hint(run.Workflow)
	relevantSkills := r.exactWorkflowSkills(resolved.Plan.SkillIDs)
	visibleTools, err := r.materializeActiveWorkflowTools(ctx, run, r.workflowActorRef(run.SessionID), &workflowHint)
	if err != nil {
		return matchedWorkflowDispatch{}, err
	}
	if refreshed, ok := r.store.GetRun(run.ID); ok {
		run = refreshed
	}
	r.store.AddAudit(app.AuditEvent{
		SessionID: run.SessionID, RunID: run.ID, Actor: "gateway", Type: "gateway.dispatch",
		Summary: "Dispatched a matched capability through its fixed workflow boundary",
		Fields: map[string]any{
			"workflow_id": resolved.Plan.ProfileID, "node_id": workflowHint.WorkflowNodeID,
			"scope_revision": workflowHint.ScopeRevision, "tools": visibleToolNames(visibleTools),
		},
	})
	return matchedWorkflowDispatch{Run: run, Profile: resolved.Profile, Hint: workflowHint.taskHint(), Skills: relevantSkills, Tools: visibleTools}, nil
}

func (r Runtime) completeTerminalRoute(ctx context.Context, run app.AgentRun, goal string, returnRoute app.ReturnRoute, route app.RouteDecision) Result {
	now := time.Now().UTC()
	run.CompletedAt = &now
	run.State = "blocked"
	if route.Status == app.RouteClarify {
		run.State = "clarification_required"
		run.Summary = "I need more information before I can select a registered capability."
	} else {
		run.Summary = "Blocked: the request cannot be routed under the current capability boundary."
	}
	if strings.TrimSpace(route.Reason) != "" {
		run.Summary += " " + strings.TrimSpace(route.Reason)
	}
	r.store.SaveRun(run)
	assistant := r.store.AddMessage(app.Message{SessionID: run.SessionID, RunID: run.ID, Role: "assistant", Content: run.Summary, CreatedAt: now})
	episode := summarizeEpisode(goal, run, nil, nil, run.Summary, now)
	r.store.SaveEpisodeSummary(episode)
	r.writeTrace(ctx, run, modelrouter.ChatResult{}, nil, nil, nil, &episode)
	result := workflowResultForTerminalRoute(run, route, returnRoute, run.Summary)
	return Result{Run: run, Message: assistant, RouteDecision: &route, WorkflowResult: result, ToolCalls: []app.ToolCall{}, Approvals: []app.Approval{}}
}

func workflowResultForRun(run app.AgentRun, route app.RouteDecision, returnRoute app.ReturnRoute, summary string) *app.WorkflowResult {
	if run.Workflow == nil {
		return nil
	}
	status := app.WorkflowResultFailed
	switch {
	case run.State == "approval_pending" || run.State == "browser_login_blocked":
		status = app.WorkflowResultWaiting
	case run.Workflow.Status == app.WorkflowStatusSucceeded && run.State == "completed":
		status = app.WorkflowResultSucceeded
	case run.Workflow.Status == app.WorkflowStatusBlocked || run.State == "blocked":
		status = app.WorkflowResultBlocked
	}
	result := &app.WorkflowResult{
		SchemaVersion: app.WorkflowResultSchemaVersion, ID: "workflow_result_" + run.ID, RunID: run.ID,
		Status: status, CapabilityPath: append([]app.CapabilityID(nil), route.CapabilityPath...),
		Workflow:   app.WorkflowContractRef{ID: run.Workflow.Plan.ProfileID, Revision: run.Workflow.Plan.ProfileRevision},
		Content:    app.MessageContent{Parts: []app.MessagePart{{ID: "result_text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: summary}}},
		References: workflowResourceRefs(run.Workflow), ReturnRoute: returnRoute,
	}
	if status == app.WorkflowResultFailed || status == app.WorkflowResultBlocked {
		result.Error = &app.WorkflowResultError{Code: "workflow_" + string(status), Message: summary}
	}
	return result
}

func (r Runtime) workflowResultForDispatchFailure(run app.AgentRun, route app.RouteDecision, returnRoute app.ReturnRoute, summary string) *app.WorkflowResult {
	workflow := app.WorkflowContractRef{}
	if leaf, err := r.capabilities.ResolveLeaf(route.CapabilityPath); err == nil && leaf.Workflow != nil {
		workflow = *leaf.Workflow
	}
	return &app.WorkflowResult{
		SchemaVersion: app.WorkflowResultSchemaVersion, ID: "workflow_result_" + run.ID, RunID: run.ID,
		Status: app.WorkflowResultFailed, CapabilityPath: append([]app.CapabilityID(nil), route.CapabilityPath...), Workflow: workflow,
		Content:     app.MessageContent{Parts: []app.MessagePart{{ID: "result_text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: summary}}},
		ReturnRoute: returnRoute, Error: &app.WorkflowResultError{Code: "workflow_dispatch_failed", Message: summary},
	}
}

func workflowResultForTerminalRoute(run app.AgentRun, route app.RouteDecision, returnRoute app.ReturnRoute, summary string) *app.WorkflowResult {
	status := app.WorkflowResultBlocked
	workflowID := app.WorkflowID("router.blocked")
	if route.Status == app.RouteClarify {
		status = app.WorkflowResultWaiting
		workflowID = "router.clarify"
	}
	return &app.WorkflowResult{
		SchemaVersion: app.WorkflowResultSchemaVersion, ID: "workflow_result_" + run.ID, RunID: run.ID,
		Status: status, CapabilityPath: append([]app.CapabilityID(nil), route.CapabilityPath...),
		Workflow:    app.WorkflowContractRef{ID: workflowID, Revision: 1},
		Content:     app.MessageContent{Parts: []app.MessagePart{{ID: "result_text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: summary}}},
		ReturnRoute: returnRoute,
	}
}

func workflowResultForUnmatched(run app.AgentRun, route app.RouteDecision, returnRoute app.ReturnRoute, summary string) *app.WorkflowResult {
	status := app.WorkflowResultSucceeded
	if run.State == "approval_pending" || run.State == "browser_login_blocked" {
		status = app.WorkflowResultWaiting
	} else if run.State == "blocked" {
		status = app.WorkflowResultBlocked
	}
	return &app.WorkflowResult{
		SchemaVersion: app.WorkflowResultSchemaVersion, ID: "workflow_result_" + run.ID, RunID: run.ID,
		Status: status, CapabilityPath: nil, Workflow: app.WorkflowContractRef{ID: "react.unmatched", Revision: 1},
		Content:     app.MessageContent{Parts: []app.MessagePart{{ID: "result_text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: summary}}},
		ReturnRoute: returnRoute,
	}
}

func workflowResourceRefs(state *app.WorkflowState) []app.ResourceRef {
	refs := []app.ResourceRef{}
	for _, node := range state.Nodes {
		refs = appendUniqueResourceRefs(refs, node.OutcomeRefs...)
	}
	return refs
}
