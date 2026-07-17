package agent

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
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
	if call, approval, queued := r.queueExternalSendApproval(&run); queued {
		workflowExecution.ToolCalls = append(workflowExecution.ToolCalls, call)
		workflowExecution.Approvals = append(workflowExecution.Approvals, approval)
		currentToolCalls = append(currentToolCalls, call)
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
		WorkflowResult: r.workflowResultForRun(run, route, run.Workflow.ReturnRoute, run.Summary),
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
	if err := prepareWorkflowState(resolved.Profile, run.Workflow); err != nil {
		return matchedWorkflowDispatch{}, err
	}
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
	result := r.workflowResultForTerminalRoute(run, route, returnRoute, run.Summary)
	return Result{Run: run, Message: assistant, RouteDecision: &route, WorkflowResult: result, ToolCalls: []app.ToolCall{}, Approvals: []app.Approval{}}
}

func (r Runtime) workflowResultForRun(run app.AgentRun, route app.RouteDecision, returnRoute app.ReturnRoute, summary string) *app.WorkflowResult {
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
	ownerID, authorization := r.workflowResultIdentity(run)
	result := &app.WorkflowResult{
		SchemaVersion: app.WorkflowResultSchemaVersion, ID: "workflow_result_" + run.ID, RunID: run.ID,
		OwnerID: ownerID, Authorization: authorization,
		Status: status, CapabilityPath: append([]app.CapabilityID(nil), route.CapabilityPath...),
		Workflow:   app.WorkflowContractRef{ID: run.Workflow.Plan.ProfileID, Revision: run.Workflow.Plan.ProfileRevision},
		Content:    r.workflowResultContent(run, summary),
		References: workflowResourceRefs(run.Workflow), ReturnRoute: returnRoute,
	}
	if status == app.WorkflowResultFailed || status == app.WorkflowResultBlocked {
		result.Error = &app.WorkflowResultError{Code: "workflow_" + string(status), Message: summary}
	}
	return r.protectExternalSendResult(run, result)
}

func (r Runtime) workflowResultContent(run app.AgentRun, summary string) app.MessageContent {
	if strings.TrimSpace(summary) == "" {
		summary = "The workflow completed successfully."
	}
	parts := []app.MessagePart{{ID: "result_text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: summary}}
	if run.Workflow == nil {
		return app.MessageContent{Parts: parts}
	}
	for refIndex, ref := range workflowResourceRefs(run.Workflow) {
		if ref.Kind != "path" || strings.TrimSpace(ref.Ref) == "" || strings.TrimSpace(ref.Provenance) == "" {
			continue
		}
		call, ok := r.store.GetToolCall(ref.Provenance)
		if !ok || !toolCallCompleted(call) {
			continue
		}
		definition, ok := r.tools.Definition(call.Tool)
		if !ok {
			continue
		}
		kind := app.MessagePartKind("")
		switch {
		case containsToolEffect(definition.Directory.Effects, app.ToolEffectWorkspaceWrite) && containsOutputKind(definition.Directory.OutputKinds, app.OutputKindFile):
			kind = app.MessagePartFile
		case containsOutputKind(definition.Directory.OutputKinds, app.OutputKindImage):
			kind = app.MessagePartImage
		default:
			continue
		}
		resourceRef, ok := r.workflowOutputResourceRef(run.SessionID, ref)
		if !ok {
			continue
		}
		name := filepath.Base(filepath.Clean(resourceRef.Ref))
		contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		parts = append(parts, app.MessagePart{
			ID: fmt.Sprintf("%s:output:%d", call.ID, refIndex), Kind: kind, Disposition: app.MessageDispositionAttachment,
			Resource: &resourceRef,
			Name:     name, ContentType: contentType,
		})
	}
	return app.MessageContent{Parts: parts}
}

func (r Runtime) workflowOutputResourceRef(sessionID string, ref app.ResourceRef) (app.ResourceRef, bool) {
	session, ok := r.store.GetSession(sessionID)
	if !ok || strings.TrimSpace(session.WorkspaceRoot) == "" {
		return app.ResourceRef{}, false
	}
	root, err := filepath.Abs(session.WorkspaceRoot)
	if err != nil {
		return app.ResourceRef{}, false
	}
	candidate := strings.TrimSpace(ref.Ref)
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, filepath.FromSlash(candidate))
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil || candidate == root || !strings.HasPrefix(candidate, root+string(os.PathSeparator)) {
		return app.ResourceRef{}, false
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return app.ResourceRef{}, false
	}
	return app.ResourceRef{Kind: "workspace_file", Ref: filepath.ToSlash(relative), Provenance: ref.Provenance}, true
}

func containsToolEffect(values []app.ToolEffect, expected app.ToolEffect) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func containsOutputKind(values []app.OutputKind, expected app.OutputKind) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func (r Runtime) workflowResultForDispatchFailure(run app.AgentRun, route app.RouteDecision, returnRoute app.ReturnRoute, summary string) *app.WorkflowResult {
	workflow := app.WorkflowContractRef{}
	if leaf, err := r.capabilities.ResolveLeaf(route.CapabilityPath); err == nil && leaf.Workflow != nil {
		workflow = *leaf.Workflow
	}
	ownerID, authorization := r.workflowResultIdentity(run)
	result := &app.WorkflowResult{
		SchemaVersion: app.WorkflowResultSchemaVersion, ID: "workflow_result_" + run.ID, RunID: run.ID,
		OwnerID: ownerID, Authorization: authorization,
		Status: app.WorkflowResultFailed, CapabilityPath: append([]app.CapabilityID(nil), route.CapabilityPath...), Workflow: workflow,
		Content:     app.MessageContent{Parts: []app.MessagePart{{ID: "result_text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: summary}}},
		ReturnRoute: returnRoute, Error: &app.WorkflowResultError{Code: "workflow_dispatch_failed", Message: summary},
	}
	return r.protectExternalSendResult(run, result)
}

func (r Runtime) workflowResultForTerminalRoute(run app.AgentRun, route app.RouteDecision, returnRoute app.ReturnRoute, summary string) *app.WorkflowResult {
	status := app.WorkflowResultBlocked
	workflowID := app.WorkflowID("router.blocked")
	if route.Status == app.RouteClarify {
		status = app.WorkflowResultWaiting
		workflowID = "router.clarify"
	}
	ownerID, authorization := r.workflowResultIdentity(run)
	result := &app.WorkflowResult{
		SchemaVersion: app.WorkflowResultSchemaVersion, ID: "workflow_result_" + run.ID, RunID: run.ID,
		OwnerID: ownerID, Authorization: authorization,
		Status: status, CapabilityPath: append([]app.CapabilityID(nil), route.CapabilityPath...),
		Workflow:    app.WorkflowContractRef{ID: workflowID, Revision: 1},
		Content:     app.MessageContent{Parts: []app.MessagePart{{ID: "result_text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: summary}}},
		ReturnRoute: returnRoute,
	}
	return r.protectExternalSendResult(run, result)
}

func (r Runtime) workflowResultForUnmatched(run app.AgentRun, route app.RouteDecision, returnRoute app.ReturnRoute, summary string) *app.WorkflowResult {
	status := app.WorkflowResultSucceeded
	if run.State == "approval_pending" || run.State == "browser_login_blocked" {
		status = app.WorkflowResultWaiting
	} else if run.State == "blocked" {
		status = app.WorkflowResultBlocked
	}
	ownerID, authorization := r.workflowResultIdentity(run)
	result := &app.WorkflowResult{
		SchemaVersion: app.WorkflowResultSchemaVersion, ID: "workflow_result_" + run.ID, RunID: run.ID,
		OwnerID: ownerID, Authorization: authorization,
		Status: status, CapabilityPath: nil, Workflow: app.WorkflowContractRef{ID: "react.unmatched", Revision: 1},
		Content:     app.MessageContent{Parts: []app.MessagePart{{ID: "result_text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: summary}}},
		ReturnRoute: returnRoute,
	}
	return r.protectExternalSendResult(run, result)
}

func (r Runtime) workflowResultIdentity(run app.AgentRun) (string, app.MessageAuthorization) {
	if run.MessageContext != nil {
		ownerID := strings.TrimSpace(run.MessageContext.OwnerID)
		authorization := run.MessageContext.Authorization
		if ownerID != "" && strings.TrimSpace(authorization.PrincipalID) == ownerID {
			authorization.Scope = append([]string(nil), authorization.Scope...)
			return ownerID, authorization
		}
	}
	ownerID := app.DefaultOwnerID
	if session, ok := r.store.GetSession(run.SessionID); ok && strings.TrimSpace(session.OwnerID) != "" {
		ownerID = strings.TrimSpace(session.OwnerID)
	}
	return ownerID, app.MessageAuthorization{PrincipalID: ownerID}
}

func workflowResourceRefs(state *app.WorkflowState) []app.ResourceRef {
	refs := []app.ResourceRef{}
	for _, node := range state.Nodes {
		refs = appendUniqueResourceRefs(refs, node.OutcomeRefs...)
	}
	return refs
}
