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

func (r Runtime) validateWorkflowToolPlan(runID string, plan toolPlan, definition app.ToolDefinition) error {
	if plan.WorkflowID == "" {
		return nil
	}
	run, ok := r.store.GetRun(runID)
	if !ok || run.Workflow == nil || run.Workflow.PlanDigest == "" ||
		workflowPlanDigest(run.Workflow.Plan) != run.Workflow.PlanDigest || run.Workflow.Plan.ProfileID != plan.WorkflowID {
		return errors.New("tool call does not belong to the persisted workflow plan")
	}
	state, ok := run.Workflow.Nodes[plan.WorkflowNodeID]
	if !ok || state.Status != app.WorkflowNodeActive || state.ScopeRevision != plan.ScopeRevision ||
		!containsWorkflowNodeID(run.Workflow.ActiveNodeIDs, plan.WorkflowNodeID) {
		return errors.New("tool call does not belong to the active workflow scope")
	}

	matchedEntry := app.ToolDirectoryEntryID("")
	for _, capability := range definition.Capabilities {
		if capability.Name == plan.Capability && matchesAnyRequirement(capability, state.CurrentScope.Requirements) {
			matchedEntry = directoryEntryID(definition, capability)
			break
		}
	}
	if matchedEntry == "" || !containsDirectoryEntryID(state.SelectedEntries, matchedEntry) {
		return errors.New("tool call was not materialized for the active workflow scope")
	}

	node, ok := workflowPlanNode(run.Workflow.Plan, plan.WorkflowNodeID)
	if !ok {
		return errors.New("active workflow node is missing from the frozen plan")
	}
	for _, binding := range node.ArgumentBindings {
		if binding.Capability != plan.Capability {
			continue
		}
		if !workflowArgumentAllowed(binding, node, run.Workflow.Intent, state, plan.Args) {
			return errors.New("tool arguments are outside the frozen workflow resource boundary")
		}
	}
	return nil
}

func containsDirectoryEntryID(values []app.ToolDirectoryEntryID, expected app.ToolDirectoryEntryID) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func workflowArgumentAllowed(binding app.ArgumentBinding, node app.WorkflowNode, intent app.IntentEnvelope, state app.WorkflowNodeState, args map[string]any) bool {
	requested := strings.TrimSpace(stringValue(args[binding.Argument]))
	if requested == "" || requested == "<nil>" {
		return false
	}
	allowed := []string{}
	switch binding.Source {
	case app.ArgumentBindingIntentTarget:
		for _, objective := range intent.Objectives {
			if !containsString(node.Goal.ObjectiveIDs, objective.ID) || !containsTargetKind(binding.TargetKinds, objective.Target.Kind) {
				continue
			}
			if strings.TrimSpace(objective.Target.Ref) != "" {
				allowed = append(allowed, objective.Target.Ref)
			}
		}
	case app.ArgumentBindingOutcomeRef:
		for _, ref := range state.OutcomeRefs {
			if ref.Kind == binding.ResourceKind && strings.TrimSpace(ref.Ref) != "" {
				allowed = append(allowed, ref.Ref)
			}
		}
	default:
		return false
	}
	for _, candidate := range allowed {
		if requested == strings.TrimSpace(candidate) {
			return true
		}
	}
	return false
}

func containsTargetKind(values []app.TargetKind, expected app.TargetKind) bool {
	if len(values) == 0 {
		return true
	}
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func (r Runtime) blockWorkflowSetup(ctx context.Context, run app.AgentRun, goal string, setupErr error) Result {
	now := time.Now().UTC()
	if run.Workflow != nil {
		run.Workflow.Status = app.WorkflowStatusBlocked
	}
	run.State = "blocked"
	run.CompletedAt = &now
	run.Summary = "Blocked: the resolved workflow could not expose its required capability (" + setupErr.Error() + ")."
	r.store.SaveRun(run)
	r.store.AddAudit(app.AuditEvent{
		SessionID: run.SessionID,
		RunID:     run.ID,
		Actor:     "runtime",
		Type:      "workflow.blocked",
		Summary:   setupErr.Error(),
	})
	assistant := r.store.AddMessage(app.Message{
		SessionID: run.SessionID,
		RunID:     run.ID,
		Role:      "assistant",
		Content:   run.Summary,
		CreatedAt: now,
	})
	episode := summarizeEpisode(goal, run, nil, nil, run.Summary, now)
	r.store.SaveEpisodeSummary(episode)
	r.writeTrace(ctx, run, modelrouter.ChatResult{}, nil, nil, nil, &episode)
	return Result{Run: run, Message: assistant, ToolCalls: []app.ToolCall{}, Approvals: []app.Approval{}}
}

func (r Runtime) runWorkflow(ctx context.Context, sessionID string, run app.AgentRun, content string, profile workflowProfile, hint TaskHint, relevantSkills []skills.Skill, visibleTools []app.ToolDefinition) reactRunResult {
	actorRef := r.workflowActorRef(sessionID)
	allCalls := []app.ToolCall{}
	allApprovals := []app.Approval{}
	allObservations := []string{}
	latest := reactRunResult{}

	for stage, limit := 0, workflowStageLimit(run.Workflow.Plan); stage < limit; stage++ {
		stageResult := r.runReActLoopWithSeed(ctx, sessionID, run, content, hint, relevantSkills, visibleTools, allCalls, allObservations)
		allCalls = append(allCalls, stageResult.ToolCalls...)
		allApprovals = append(allApprovals, stageResult.Approvals...)
		allObservations = stageResult.Observations
		latest = stageResult
		if stageResult.BrowserLoginBlock != nil || len(stageResult.Approvals) > 0 {
			break
		}

		transitioned := false
		for _, call := range stageResult.ToolCalls {
			if call.WorkflowID == "" || call.WorkflowNodeID == "" {
				continue
			}
			definition, ok := r.tools.Definition(call.Tool)
			if !ok {
				continue
			}
			outcome, err := adaptWorkflowOutcome(definition, call)
			if err != nil {
				latest.FinalAnswer = err.Error()
				break
			}
			storedRun, ok := r.store.GetRun(run.ID)
			if !ok || storedRun.Workflow == nil {
				latest.FinalAnswer = "workflow state was not available after tool execution"
				break
			}
			assessment := profile.Assess(storedRun.Workflow, outcome)
			changed, applyErr := applyWorkflowOutcome(&storedRun, outcome, assessment)
			r.store.SaveRun(storedRun)
			r.auditWorkflowOutcome(storedRun, outcome, assessment, changed, applyErr)
			if applyErr != nil && assessment.Status != app.AssessmentBlocked {
				latest.FinalAnswer = applyErr.Error()
				break
			}
			if changed {
				transitioned = true
				if instruction := strings.TrimSpace(profile.TransitionInstruction(outcome, assessment)); instruction != "" {
					allObservations = append(allObservations, instruction)
				}
			}
		}

		storedRun, ok := r.store.GetRun(run.ID)
		if !ok || storedRun.Workflow == nil {
			latest.FinalAnswer = "workflow state could not be reloaded"
			break
		}
		run = storedRun
		if run.Workflow.Status == app.WorkflowStatusSucceeded || run.Workflow.Status == app.WorkflowStatusBlocked {
			break
		}
		if !transitioned {
			allObservations = append(allObservations, "workflow_requirement: The active workflow completion rule is not satisfied. Call the single materialized capability before returning a final answer.")
		}
		workflowHint := profile.Hint(run.Workflow)
		var err error
		visibleTools, err = r.materializeActiveWorkflowTools(ctx, run, actorRef, &workflowHint)
		if err != nil {
			latest.FinalAnswer = err.Error()
			break
		}
		hint = workflowHint.taskHint()
		if refreshed, ok := r.store.GetRun(run.ID); ok {
			run = refreshed
		}
	}

	latest.ToolCalls = allCalls
	latest.Approvals = allApprovals
	latest.Observations = allObservations
	if storedRun, ok := r.store.GetRun(run.ID); ok && storedRun.Workflow != nil {
		switch {
		case storedRun.Workflow.Status == app.WorkflowStatusRunning && latest.BrowserLoginBlock == nil && len(latest.Approvals) == 0:
			latest.Completed = false
			latest.FinalAnswer = "The workflow stopped before its completion rule was satisfied."
		case storedRun.Workflow.Status == app.WorkflowStatusBlocked && strings.TrimSpace(latest.FinalAnswer) == "":
			latest.Completed = false
			latest.FinalAnswer = workflowBlockedMessage(storedRun.Workflow)
		case storedRun.Workflow.Status == app.WorkflowStatusSucceeded && strings.TrimSpace(latest.FinalAnswer) == "":
			// The last model envelope was a tool action, not a user-facing final.
			latest.Chat.Content = ""
		}
	}
	return latest
}

func workflowBlockedMessage(state *app.WorkflowState) string {
	reason := "required evidence is unavailable"
	for _, nodeID := range state.ActiveNodeIDs {
		if assessment := state.Nodes[nodeID].LastAssessment; assessment != nil && assessment.ReasonCode != "" {
			reason = strings.ReplaceAll(assessment.ReasonCode, "_", " ")
			break
		}
	}
	if reason == "required evidence is unavailable" {
		for _, node := range state.Nodes {
			if node.LastAssessment != nil && node.LastAssessment.ReasonCode != "" {
				reason = strings.ReplaceAll(node.LastAssessment.ReasonCode, "_", " ")
				break
			}
		}
	}
	return "The workflow is blocked: " + reason + "."
}

func (r Runtime) auditWorkflowOutcome(run app.AgentRun, outcome app.ToolOutcome, assessment app.NodeAssessment, transitioned bool, transitionErr error) {
	fields := map[string]any{
		"workflow_id":  run.Workflow.Plan.ProfileID,
		"node_id":      outcome.NodeID,
		"outcome_id":   outcome.ID,
		"tool_call_id": outcome.ToolCallID,
		"signals":      outcome.Signals,
		"assessment":   assessment.Status,
		"reason_code":  assessment.ReasonCode,
		"transitioned": transitioned,
	}
	if transitionErr != nil {
		fields["error"] = transitionErr.Error()
	}
	r.store.AddAudit(app.AuditEvent{
		SessionID: run.SessionID,
		RunID:     run.ID,
		Actor:     "runtime",
		Type:      "workflow.transitioned",
		Summary:   assessment.ReasonCode,
		Fields:    fields,
	})
}

func workflowStageLimit(plan app.WorkflowPlan) int {
	limit := 0
	for _, node := range plan.Nodes {
		limit += node.MaxAttempts
		for _, transition := range node.Transitions {
			limit += transition.MaxActivations
		}
	}
	if limit <= 0 {
		return 1
	}
	if limit > 16 {
		return 16
	}
	return limit
}
