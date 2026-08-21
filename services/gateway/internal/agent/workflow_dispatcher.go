package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
)

type matchedWorkflowDispatch struct {
	Run     app.AgentRun
	Profile workflowProfile
	Context workflowStageContext
	Tools   []app.ToolDefinition
}

func (r Runtime) resumeMatchedWorkflowAfterApproval(ctx context.Context, run app.AgentRun, content string, seedCalls []app.ToolCall) (Result, bool, error) {
	return r.resumeMatchedWorkflow(ctx, run, content, seedCalls, "workflow.resumed_after_approval")
}

func (r Runtime) resumeMatchedWorkflow(ctx context.Context, run app.AgentRun, content string, seedCalls []app.ToolCall, auditType string) (Result, bool, error) {
	if err := r.capabilities.ValidateDecision(run.Workflow.Route); err != nil {
		result, resultErr := r.blockPersistedWorkflowResume(ctx, run, content, err)
		return result, true, resultErr
	}
	profile, err := r.profiles.Get(run.Workflow.Plan.ProfileID, run.Workflow.Plan.ProfileRevision)
	if err != nil {
		result, resultErr := r.blockPersistedWorkflowResume(ctx, run, content, err)
		return result, true, resultErr
	}
	for _, call := range seedCalls {
		if call.WorkflowID == "" || workflowAppliedToolCall(run.Workflow, call.ID) {
			continue
		}
		definition, ok := r.tools.Definition(call.Tool)
		if !ok {
			result, resultErr := r.blockPersistedWorkflowResume(ctx, run, content, errors.New("approved workflow tool is no longer registered"))
			return result, true, resultErr
		}
		outcome, adaptErr := adaptWorkflowOutcome(definition, call)
		if adaptErr != nil {
			result, resultErr := r.blockPersistedWorkflowResume(ctx, run, content, adaptErr)
			return result, true, resultErr
		}
		assessment := profile.Assess(run.Workflow, outcome)
		changed, applyErr := applyWorkflowOutcome(&run, outcome, assessment)
		if run, err = r.saveRun(ctx, run); err != nil {
			return Result{}, true, err
		}
		r.auditWorkflowOutcome(run, outcome, assessment, changed, applyErr)
		if applyErr != nil && assessment.Status != app.AssessmentBlocked {
			result, resultErr := r.blockPersistedWorkflowResume(ctx, run, content, applyErr)
			return result, true, resultErr
		}
	}
	decisionObservations := []string{}
	if run.Workflow.Status == app.WorkflowStatusRunning {
		observation, _, decisionErr := r.resolveActiveWorkflowDecisions(ctx, &run, profile)
		if decisionErr != nil {
			result, resultErr := r.blockPersistedWorkflowResume(ctx, run, content, decisionErr)
			return result, true, resultErr
		}
		if strings.TrimSpace(observation) != "" {
			decisionObservations = append(decisionObservations, observation)
		}
		if refreshed, ok, err := r.store.GetRun(ctx, run.ID); err != nil {
			return Result{}, true, err
		} else if ok {
			run = refreshed
		}
	}
	workflowExecution := workflowExecutionResult{}
	if run.Workflow.Status == app.WorkflowStatusRunning {
		stageContext := profile.StageContext(run.Workflow)
		visibleTools, exposeErr := r.materializeActiveWorkflowTools(ctx, run, r.workflowActorRef(run), &stageContext)
		if exposeErr != nil {
			result, resultErr := r.blockPersistedWorkflowResume(ctx, run, content, exposeErr)
			return result, true, resultErr
		}
		if refreshed, ok, err := r.store.GetRun(ctx, run.ID); err != nil {
			return Result{}, true, err
		} else if ok {
			run = refreshed
		}
		run.State = "executing"
		run.CompletedAt = nil
		if run, err = r.saveRun(ctx, run); err != nil {
			return Result{}, true, err
		}
		workflowExecution = r.runWorkflowWithSeed(
			ctx, run.SessionID, run, content, profile, stageContext, visibleTools,
			seedCalls, append(observationsForResume(seedCalls), decisionObservations...),
		)
		if refreshed, ok, err := r.store.GetRun(ctx, run.ID); err != nil {
			return Result{}, true, err
		} else if ok {
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
	storedToolCalls, err := r.store.ListToolCalls(ctx, run.SessionID)
	if err != nil {
		return Result{}, true, err
	}
	currentToolCalls := toolCallsForRun(storedToolCalls, run.ID)
	if workflowExecution.FailureCode != "" {
		run.Summary = publicWorkflowFailureMessage(workflowExecution.FailureCode)
	} else {
		run.Summary = summarizeRun(workflowExecution.Chat, workflowObservationTexts(workflowExecution.Observations), workflowExecution.Approvals)
		if strings.TrimSpace(workflowExecution.FinalAnswer) != "" {
			run.Summary = workflowExecution.FinalAnswer
		}
		run.Summary = r.applyGroundedSummary(run.SessionID, run.ID, content, run.Summary, currentToolCalls)
	}
	if strings.TrimSpace(run.Summary) == "" {
		run.Summary = "The matched workflow completed after its approved action."
	}
	if run, err = r.saveRun(ctx, run); err != nil {
		return Result{}, true, err
	}
	allApprovals := approvalsForRun(r.store.ListApprovals(""), run.ID)
	feedback, err := r.store.ListRunFeedback(ctx, run.ID)
	if err != nil {
		return Result{}, true, err
	}
	episode := summarizeEpisode(content, run, currentToolCalls, allApprovals, run.Summary, now)
	if _, err := r.store.SaveEpisodeSummary(ctx, episode); err != nil {
		return Result{}, true, err
	}
	route := run.Workflow.Route
	workflowResult, err := r.workflowResultForRun(ctx, run, route, run.Workflow.ReturnRoute, run.Summary, workflowExecution.FailureCode)
	if err != nil {
		return Result{}, true, err
	}
	assistant, err := r.persistWorkflowAssistantMessage(ctx, run, workflowResult, now)
	if err != nil {
		return Result{}, true, fmt.Errorf("persist resumed workflow response: %w", err)
	}
	r.store.AddAudit(app.AuditEvent{SessionID: run.SessionID, RunID: run.ID, Actor: "workflow_dispatcher", Type: auditType, Summary: string(run.Workflow.Status)})
	r.writeTrace(ctx, run, modelrouter.ChatResult{}, currentToolCalls, allApprovals, feedback, &episode)
	return Result{
		Run: run, Message: assistant, ToolCalls: workflowExecution.ToolCalls, Approvals: workflowExecution.Approvals, RouteDecision: &route,
		WorkflowResult: workflowResult,
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
	// Persist the frozen plan before Policy binds it into an approval contract.
	// This write contains no workspace discovery or file metadata.
	if run, err = r.saveRun(ctx, run); err != nil {
		return matchedWorkflowDispatch{}, err
	}
	if _, _, queued, err := r.queueMCPWorkspaceDataApproval(ctx, &run); err != nil {
		return matchedWorkflowDispatch{}, err
	} else if queued {
		return matchedWorkflowDispatch{Run: run, Profile: resolved.Profile}, nil
	}
	if err := prepareWorkflowState(resolved.Profile, run.Workflow); err != nil {
		return matchedWorkflowDispatch{}, err
	}
	if run, err = r.saveRun(ctx, run); err != nil {
		return matchedWorkflowDispatch{}, err
	}
	if err := r.completeConversationMediaDetection(ctx, &run); err != nil {
		return matchedWorkflowDispatch{}, err
	}
	run.State = "routing"
	if run, err = r.saveRun(ctx, run); err != nil {
		return matchedWorkflowDispatch{}, err
	}
	r.store.AddAudit(app.AuditEvent{
		SessionID: run.SessionID, RunID: run.ID, Actor: "workflow_dispatcher", Type: "workflow.dispatched",
		Summary: "Dispatched a validated capability leaf to its exact workflow contract",
		Fields: map[string]any{
			"catalog_revision": route.CatalogRevision, "capability_path": route.CapabilityPath,
			"workflow_id": resolved.Plan.ProfileID, "workflow_revision": resolved.Plan.ProfileRevision,
			"plan_digest": run.Workflow.PlanDigest, "active_node_ids": run.Workflow.ActiveNodeIDs,
		},
	})
	stageContext := resolved.Profile.StageContext(run.Workflow)
	visibleTools, err := r.materializeActiveWorkflowTools(ctx, run, r.workflowActorRef(run), &stageContext)
	if err != nil {
		return matchedWorkflowDispatch{}, err
	}
	if refreshed, ok, err := r.store.GetRun(ctx, run.ID); err != nil {
		return matchedWorkflowDispatch{}, err
	} else if ok {
		run = refreshed
	}
	r.store.AddAudit(app.AuditEvent{
		SessionID: run.SessionID, RunID: run.ID, Actor: "gateway", Type: "gateway.dispatch",
		Summary: "Dispatched a matched capability through its fixed workflow boundary",
		Fields: map[string]any{
			"workflow_id": resolved.Plan.ProfileID, "node_id": stageContext.WorkflowNodeID,
			"scope_revision": stageContext.ScopeRevision, "tools": visibleToolNames(visibleTools),
		},
	})
	return matchedWorkflowDispatch{Run: run, Profile: resolved.Profile, Context: stageContext, Tools: visibleTools}, nil
}

func (r Runtime) completeTerminalRoute(ctx context.Context, run app.AgentRun, goal string, returnRoute app.ReturnRoute, route app.RouteDecision) (Result, error) {
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
	var err error
	if run, err = r.saveRun(ctx, run); err != nil {
		return Result{}, err
	}
	assistant, err := r.store.AddMessage(ctx, app.Message{SessionID: run.SessionID, RunID: run.ID, Role: "assistant", Content: run.Summary, CreatedAt: now})
	if err != nil {
		return Result{}, fmt.Errorf("persist terminal route response: %w", err)
	}
	episode := summarizeEpisode(goal, run, nil, nil, run.Summary, now)
	if _, err := r.store.SaveEpisodeSummary(ctx, episode); err != nil {
		return Result{}, err
	}
	r.writeTrace(ctx, run, modelrouter.ChatResult{}, nil, nil, nil, &episode)
	result, err := r.workflowResultForTerminalRoute(ctx, run, route, returnRoute, run.Summary)
	if err != nil {
		return Result{}, err
	}
	return Result{Run: run, Message: assistant, RouteDecision: &route, WorkflowResult: result, ToolCalls: []app.ToolCall{}, Approvals: []app.Approval{}}, nil
}

func (r Runtime) workflowResultForRun(ctx context.Context, run app.AgentRun, route app.RouteDecision, returnRoute app.ReturnRoute, summary string, failureCodes ...workflowFailureCode) (*app.WorkflowResult, error) {
	if run.Workflow == nil {
		return nil, nil
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
	ownerID, authorization, err := r.workflowResultIdentity(ctx, run)
	if err != nil {
		return nil, err
	}
	content, err := r.workflowResultContent(ctx, run, summary)
	if err != nil {
		return nil, err
	}
	data, err := r.workflowResultData(ctx, run)
	if err != nil {
		return nil, err
	}
	result := &app.WorkflowResult{
		SchemaVersion: app.WorkflowResultSchemaVersion, ID: "workflow_result_" + run.ID, RunID: run.ID,
		OwnerID: ownerID, Authorization: authorization,
		Status: status, CapabilityPath: append([]app.CapabilityID(nil), route.CapabilityPath...),
		Workflow:   app.WorkflowContractRef{ID: run.Workflow.Plan.ProfileID, Revision: run.Workflow.Plan.ProfileRevision},
		Data:       data,
		Content:    content,
		References: workflowResourceRefs(run.Workflow), ReturnRoute: workflowResultReturnRoute(status, returnRoute),
	}
	if run.MessageContext != nil {
		result.MCP = run.MessageContext.MCP
	}
	if status == app.WorkflowResultFailed || status == app.WorkflowResultBlocked {
		code := "workflow_" + string(status)
		if len(failureCodes) > 0 && failureCodes[0] != "" {
			code = string(failureCodes[0])
		}
		result.Error = &app.WorkflowResultError{Code: code, Message: summary}
	}
	return result, nil
}

func (r Runtime) workflowResultData(ctx context.Context, run app.AgentRun) (map[string]any, error) {
	if run.Workflow == nil || r.tools == nil || r.store == nil {
		return nil, nil
	}
	for _, ref := range workflowResourceRefs(run.Workflow) {
		if strings.TrimSpace(ref.Provenance) == "" {
			continue
		}
		call, ok, err := r.store.GetToolCall(ctx, ref.Provenance)
		if err != nil {
			return nil, err
		}
		if !ok || !toolCallCompleted(call) {
			continue
		}
		definition, ok := r.tools.Definition(call.Tool)
		if !ok || definition.OutcomeAdapter != app.OutcomeAdapterDocumentEdit {
			continue
		}
		output, ok := anyMap(call.Result)
		if !ok {
			continue
		}
		changeSummary, ok := anyMap(output["change_summary"])
		if !ok || len(changeSummary) == 0 {
			continue
		}
		data := map[string]any{"change_summary": changeSummary}
		for _, key := range []string{"status", "operation", "output_path", "outputs"} {
			if value, exists := output[key]; exists {
				data[key] = value
			}
		}
		return data, nil
	}
	return nil, nil
}

func (r Runtime) workflowResultContent(ctx context.Context, run app.AgentRun, summary string) (app.MessageContent, error) {
	outputParts := []app.MessagePart{}
	if run.Workflow == nil {
		return workflowResultTextContent(summary), nil
	}
	if run.Workflow.Plan.ProfileID == app.WorkflowConversationAnswer && run.Workflow.Route.Slots.Operation == app.RouteOperationPublish &&
		run.MessageContext != nil {
		if run.Workflow.Plan.ProfileRevision == 3 && len(run.MessageContext.ResponseContent.Parts) > 0 {
			return cloneMessageContent(run.MessageContext.ResponseContent), nil
		}
		if len(run.MessageContext.RequestContent.Parts) > 0 {
			return cloneMessageContent(run.MessageContext.RequestContent), nil
		}
	}
	if run.Workflow.Plan.ProfileID == app.WorkflowConversationAnswer && run.Workflow.Plan.ProfileRevision == 3 &&
		run.MessageContext != nil && run.MessageContext.ResponseMedia != nil && run.MessageContext.ResponseMedia.Status == app.ResponseMediaSelected {
		content := app.MessageContent{Parts: []app.MessagePart{{ID: "result_text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: summary}}}
		content.Parts = append(content.Parts, cloneMessageContent(run.MessageContext.ResponseContent).Parts...)
		return content, nil
	}
	for refIndex, ref := range workflowResourceRefs(run.Workflow) {
		if ref.Kind != "path" || strings.TrimSpace(ref.Ref) == "" || strings.TrimSpace(ref.Provenance) == "" {
			continue
		}
		call, ok, err := r.store.GetToolCall(ctx, ref.Provenance)
		if err != nil {
			return app.MessageContent{}, err
		}
		if !ok || !toolCallCompleted(call) {
			continue
		}
		part, ok, err := r.workflowOutputPart(ctx, run.SessionID, call, ref, refIndex)
		if err != nil {
			return app.MessageContent{}, err
		}
		if ok {
			outputParts = append(outputParts, part)
		}
	}
	if len(outputParts) > 0 {
		return app.MessageContent{Parts: outputParts}, nil
	}
	return workflowResultTextContent(summary), nil
}

func (r Runtime) governWorkflowRequestContent(ctx context.Context, run app.AgentRun) (app.MessageContent, error) {
	if run.MessageContext == nil || len(run.MessageContext.RequestContent.Parts) == 0 {
		return app.MessageContent{}, errors.New("normalized request content is empty")
	}
	content := cloneMessageContent(run.MessageContext.RequestContent)
	hasMedia := false
	for _, part := range content.Parts {
		if isMediaMessagePart(part.Kind) {
			hasMedia = true
			break
		}
	}
	governed := make([]app.MessagePart, 0, len(content.Parts))
	for index := range content.Parts {
		if content.Parts[index].Kind == app.MessagePartText {
			if !hasMedia {
				governed = append(governed, content.Parts[index])
			}
			continue
		}
		part, err := r.governWorkflowRequestPart(ctx, run, content.Parts[index])
		if err != nil {
			return app.MessageContent{}, fmt.Errorf("message part %q: %w", content.Parts[index].ID, err)
		}
		governed = append(governed, part)
	}
	content.Parts = governed
	return content, nil
}

func (r Runtime) governWorkflowRequestPart(ctx context.Context, run app.AgentRun, part app.MessagePart) (app.MessagePart, error) {
	if r.store == nil || part.Resource == nil || part.Resource.Kind != "workspace_file" {
		return app.MessagePart{}, errors.New("binary message part is not a governed workspace file")
	}
	ref := filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.TrimSpace(part.Resource.Ref))))
	if ref == "." || filepath.IsAbs(filepath.FromSlash(ref)) || ref == ".." || strings.HasPrefix(ref, "../") {
		return app.MessagePart{}, errors.New("workspace file reference is invalid")
	}
	workspaceRoot, err := r.workspaceRootForSession(ctx, run.SessionID)
	if err != nil {
		return app.MessagePart{}, err
	}
	root := strings.TrimSpace(workspaceRoot)
	if root == "" {
		return app.MessagePart{}, errors.New("source workspace is unavailable")
	}
	root, err = filepath.Abs(root)
	if err == nil {
		root, err = filepath.EvalSymlinks(root)
	}
	if err != nil {
		return app.MessagePart{}, errors.New("source workspace is unavailable")
	}
	candidate, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(ref)))
	if err != nil || candidate == root || !strings.HasPrefix(candidate, root+string(os.PathSeparator)) {
		return app.MessagePart{}, errors.New("workspace file escapes the source workspace")
	}
	lstat, err := os.Lstat(candidate)
	if err != nil || lstat.Mode()&os.ModeSymlink != 0 || !lstat.Mode().IsRegular() {
		return app.MessagePart{}, errors.New("workspace file is unavailable or not regular")
	}
	candidate, err = filepath.EvalSymlinks(candidate)
	if err != nil || !strings.HasPrefix(candidate, root+string(os.PathSeparator)) {
		return app.MessagePart{}, errors.New("workspace file resolves outside the source workspace")
	}
	info, err := os.Stat(candidate)
	if err != nil || !info.Mode().IsRegular() {
		return app.MessagePart{}, errors.New("workspace file is unavailable")
	}

	object := app.ArtifactObject{}
	for _, existing := range r.store.ListArtifactObjects(0) {
		if existing.SessionID != run.SessionID {
			continue
		}
		matchesID := strings.TrimSpace(part.ArtifactID) != "" && existing.ID == strings.TrimSpace(part.ArtifactID)
		matchesFile := filepath.ToSlash(filepath.Clean(existing.Key)) == ref || filepath.Clean(existing.Path) == filepath.Clean(candidate)
		if matchesID && !matchesFile {
			return app.MessagePart{}, errors.New("artifact identity does not match the source workspace file")
		}
		if matchesID || matchesFile {
			object = existing
			break
		}
	}
	if object.ID != "" && object.Bytes > 0 && int64(object.Bytes) != info.Size() {
		return app.MessagePart{}, errors.New("workspace artifact changed after it was registered")
	}
	contentType := strings.TrimSpace(part.ContentType)
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = mime.TypeByExtension(strings.ToLower(filepath.Ext(ref)))
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if object.ID == "" {
		object = app.ArtifactObject{
			ID: app.NewID("obj"), Kind: "message_attachment", RunID: run.ID, SessionID: run.SessionID,
			Backend: "workspace", Key: ref, URI: "workspace://" + ref, Path: candidate,
			ContentType: contentType, Bytes: int(info.Size()), CreatedAt: time.Now().UTC(),
		}
		r.store.SaveArtifactObject(object)
	}
	digest, err := workflowFileSHA256(candidate)
	if err != nil {
		return app.MessagePart{}, errors.New("workspace file could not be verified")
	}
	part.ArtifactID = object.ID
	part.Resource = &app.ResourceRef{Kind: "workspace_file", Ref: ref, Provenance: "message_publish"}
	if strings.TrimSpace(part.Name) == "" {
		part.Name = filepath.Base(ref)
	}
	part.ContentType = contentType
	part.Bytes = int(info.Size())
	part.SHA256 = digest
	return part, nil
}

func cloneMessageContent(content app.MessageContent) app.MessageContent {
	clone := app.MessageContent{Parts: append([]app.MessagePart(nil), content.Parts...)}
	for index := range clone.Parts {
		if clone.Parts[index].Resource != nil {
			resource := *clone.Parts[index].Resource
			clone.Parts[index].Resource = &resource
		}
	}
	return clone
}

func workflowFileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (r Runtime) workflowResultContentFromToolCalls(ctx context.Context, run app.AgentRun, summary string) (app.MessageContent, error) {
	parts := []app.MessagePart{}
	storedCalls, err := r.store.ListToolCalls(ctx, run.SessionID)
	if err != nil {
		return app.MessageContent{}, err
	}
	for _, call := range toolCallsForRun(storedCalls, run.ID) {
		if !toolCallCompleted(call) {
			continue
		}
		for index, ref := range toolCallOutputRefs(call, r.tools) {
			part, ok, err := r.workflowOutputPart(ctx, run.SessionID, call, ref, index)
			if err != nil {
				return app.MessageContent{}, err
			}
			if ok {
				parts = append(parts, part)
			}
		}
	}
	if len(parts) > 0 {
		return app.MessageContent{Parts: parts}, nil
	}
	return workflowResultTextContent(summary), nil
}

func (r Runtime) workflowOutputPart(ctx context.Context, sessionID string, call app.ToolCall, ref app.ResourceRef, index int) (app.MessagePart, bool, error) {
	definition, ok := r.tools.Definition(call.Tool)
	if !ok {
		return app.MessagePart{}, false, nil
	}
	kind := app.MessagePartKind("")
	switch {
	case containsToolEffect(definition.Directory.Effects, app.ToolEffectWorkspaceWrite) && containsOutputKind(definition.Directory.OutputKinds, app.OutputKindFile):
		kind = app.MessagePartFile
	case containsOutputKind(definition.Directory.OutputKinds, app.OutputKindImage):
		kind = app.MessagePartImage
	default:
		return app.MessagePart{}, false, nil
	}
	resourceRef, ok, err := r.workflowOutputResourceRef(ctx, sessionID, ref)
	if err != nil {
		return app.MessagePart{}, false, err
	}
	if !ok {
		return app.MessagePart{}, false, nil
	}
	name := filepath.Base(filepath.Clean(resourceRef.Ref))
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	output, _ := anyMap(call.Result)
	if declared := cleanOptionalString(output["content_type"]); declared != "" {
		contentType = declared
	}
	disposition := app.MessageDispositionAttachment
	if kind == app.MessagePartImage {
		disposition = app.MessageDispositionInline
	}
	caption := cleanOptionalString(output["summary"])
	if definition.OutcomeAdapter == app.OutcomeAdapterWeatherCard {
		caption = ""
	}
	artifactID, err := r.persistWorkflowOutputArtifact(ctx, sessionID, call, resourceRef, contentType)
	if err != nil {
		return app.MessagePart{}, false, err
	}
	return app.MessagePart{
		ID: fmt.Sprintf("%s:output:%d", call.ID, index), Kind: kind, Disposition: disposition,
		ArtifactID: artifactID, Resource: &resourceRef,
		Name: name, ContentType: contentType, Bytes: intLikeValue(output["bytes"]),
		Width: intLikeValue(output["width"]), Height: intLikeValue(output["height"]),
		SHA256: cleanOptionalString(output["sha256"]), Caption: caption,
	}, true, nil
}

func (r Runtime) persistWorkflowOutputArtifact(ctx context.Context, sessionID string, call app.ToolCall, resource app.ResourceRef, contentType string) (string, error) {
	if r.store == nil || resource.Kind != "workspace_file" || strings.TrimSpace(resource.Ref) == "" {
		return "", nil
	}
	workspaceRoot, err := r.workspaceRootForSession(ctx, sessionID)
	if err != nil {
		return "", err
	}
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", nil
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", nil
	}
	candidate, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(resource.Ref)))
	if err != nil || candidate == root || !strings.HasPrefix(candidate, root+string(os.PathSeparator)) {
		return "", nil
	}
	lstat, err := os.Lstat(candidate)
	if err != nil || lstat.Mode()&os.ModeSymlink != 0 || !lstat.Mode().IsRegular() {
		return "", nil
	}
	candidate, err = filepath.EvalSymlinks(candidate)
	if err != nil || !strings.HasPrefix(candidate, root+string(os.PathSeparator)) {
		return "", nil
	}
	info, err := os.Stat(candidate)
	if err != nil || !info.Mode().IsRegular() {
		return "", nil
	}
	for _, object := range r.store.ListArtifactObjects(0) {
		if object.RunID == call.RunID && object.SessionID == sessionID && filepath.Clean(object.Path) == candidate && object.Bytes == int(info.Size()) {
			return object.ID, nil
		}
	}
	object := app.ArtifactObject{
		ID: app.NewID("obj"), Kind: "workflow_output", RunID: call.RunID, SessionID: sessionID,
		Backend: "workspace", Key: resource.Ref, URI: "workspace://" + filepath.ToSlash(resource.Ref),
		Path: candidate, ContentType: contentType, Bytes: int(info.Size()), CreatedAt: completedToolCallTime(call),
	}
	r.store.SaveArtifactObject(object)
	return object.ID, nil
}

func toolCallOutputRefs(call app.ToolCall, tools interface {
	Definition(string) (app.ToolDefinition, bool)
}) []app.ResourceRef {
	definition, ok := tools.Definition(call.Tool)
	if !ok || (!containsOutputKind(definition.Directory.OutputKinds, app.OutputKindFile) && !containsOutputKind(definition.Directory.OutputKinds, app.OutputKindImage)) {
		return nil
	}
	output, ok := anyMap(call.Result)
	if !ok {
		return nil
	}
	paths := []string{}
	for _, value := range anySlice(output["output_paths"]) {
		paths = append(paths, cleanOptionalString(value))
	}
	for _, value := range anySlice(output["outputs"]) {
		if item, ok := anyMap(value); ok {
			paths = append(paths, firstNonEmptyString(item["output_path"], item["path"]))
		}
	}
	paths = append(paths, firstNonEmptyString(output["output_path"], output["media_path"], output["screenshot_path"]))
	if len(paths) == 0 || strings.TrimSpace(paths[len(paths)-1]) == "" {
		if containsOutputKind(definition.Directory.OutputKinds, app.OutputKindImage) {
			paths = append(paths, cleanOptionalString(output["path"]))
		}
	}
	refs := []app.ResourceRef{}
	for _, path := range uniqueNonEmpty(paths) {
		refs = append(refs, app.ResourceRef{Kind: "path", Ref: path, Provenance: call.ID})
	}
	return refs
}

func workflowResultTextContent(summary string) app.MessageContent {
	if strings.TrimSpace(summary) == "" {
		summary = "The workflow completed successfully."
	}
	return app.MessageContent{Parts: []app.MessagePart{{ID: "result_text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: summary}}}
}

func (r Runtime) messageWithWorkflowResult(message app.Message, result *app.WorkflowResult) app.Message {
	if result == nil {
		return message
	}
	content, attachments := delivery.ProjectWebMessageContent(result.Content, message.Content)
	message.Content = content
	message.Attachments = attachments
	return message
}

func (r Runtime) persistWorkflowAssistantMessage(ctx context.Context, run app.AgentRun, result *app.WorkflowResult, now time.Time) (app.Message, error) {
	if isEndpointMediaPublication(run) {
		return app.Message{}, nil
	}
	return r.store.AddMessage(ctx, r.messageWithWorkflowResult(app.Message{
		SessionID: run.SessionID,
		RunID:     run.ID,
		Role:      "assistant",
		Content:   run.Summary,
		CreatedAt: now,
	}, result))
}

func (r Runtime) workflowOutputResourceRef(ctx context.Context, sessionID string, ref app.ResourceRef) (app.ResourceRef, bool, error) {
	session, ok, err := r.store.GetSession(ctx, sessionID)
	if err != nil {
		return app.ResourceRef{}, false, err
	}
	if !ok {
		return app.ResourceRef{}, false, nil
	}
	workspaceRoot := strings.TrimSpace(session.WorkspaceRoot)
	if workspaceRoot == "" && r.tools != nil {
		workspaceRoot = strings.TrimSpace(r.tools.Config().Workspaces.DefaultRoot)
	}
	if workspaceRoot == "" {
		return app.ResourceRef{}, false, nil
	}
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return app.ResourceRef{}, false, nil
	}
	candidate := strings.TrimSpace(ref.Ref)
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, filepath.FromSlash(candidate))
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil || candidate == root || !strings.HasPrefix(candidate, root+string(os.PathSeparator)) {
		return app.ResourceRef{}, false, nil
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return app.ResourceRef{}, false, nil
	}
	return app.ResourceRef{Kind: "workspace_file", Ref: filepath.ToSlash(relative), Provenance: ref.Provenance}, true, nil
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

func (r Runtime) workflowResultForDispatchFailure(ctx context.Context, run app.AgentRun, route app.RouteDecision, returnRoute app.ReturnRoute, summary string, failureCodes ...workflowFailureCode) (*app.WorkflowResult, error) {
	workflow := app.WorkflowContractRef{}
	if leaf, err := r.capabilities.ResolveLeaf(route.CapabilityPath); err == nil && leaf.Workflow != nil {
		workflow = *leaf.Workflow
	}
	ownerID, authorization, err := r.workflowResultIdentity(ctx, run)
	if err != nil {
		return nil, err
	}
	code := "workflow_dispatch_failed"
	if len(failureCodes) > 0 && failureCodes[0] != "" {
		code = string(failureCodes[0])
	}
	result := &app.WorkflowResult{
		SchemaVersion: app.WorkflowResultSchemaVersion, ID: "workflow_result_" + run.ID, RunID: run.ID,
		OwnerID: ownerID, Authorization: authorization,
		Status: app.WorkflowResultFailed, CapabilityPath: append([]app.CapabilityID(nil), route.CapabilityPath...), Workflow: workflow,
		Content:     app.MessageContent{Parts: []app.MessagePart{{ID: "result_text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: summary}}},
		ReturnRoute: workflowResultReturnRoute(app.WorkflowResultFailed, returnRoute), Error: &app.WorkflowResultError{Code: code, Message: summary},
	}
	if run.MessageContext != nil {
		result.MCP = run.MessageContext.MCP
	}
	return result, nil
}

func (r Runtime) workflowResultForTerminalRoute(ctx context.Context, run app.AgentRun, route app.RouteDecision, returnRoute app.ReturnRoute, summary string) (*app.WorkflowResult, error) {
	status := app.WorkflowResultBlocked
	workflowID := app.WorkflowID("router.blocked")
	if route.Status == app.RouteClarify {
		status = app.WorkflowResultWaiting
		workflowID = "router.clarify"
	}
	ownerID, authorization, err := r.workflowResultIdentity(ctx, run)
	if err != nil {
		return nil, err
	}
	result := &app.WorkflowResult{
		SchemaVersion: app.WorkflowResultSchemaVersion, ID: "workflow_result_" + run.ID, RunID: run.ID,
		OwnerID: ownerID, Authorization: authorization,
		Status: status, CapabilityPath: append([]app.CapabilityID(nil), route.CapabilityPath...),
		Workflow:    app.WorkflowContractRef{ID: workflowID, Revision: 1},
		Content:     app.MessageContent{Parts: []app.MessagePart{{ID: "result_text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: summary}}},
		ReturnRoute: workflowResultReturnRoute(status, returnRoute),
	}
	if run.MessageContext != nil {
		result.MCP = run.MessageContext.MCP
	}
	return result, nil
}

func (r Runtime) workflowResultForUnmatched(ctx context.Context, run app.AgentRun, route app.RouteDecision, returnRoute app.ReturnRoute, summary string) (*app.WorkflowResult, error) {
	status := app.WorkflowResultSucceeded
	if run.State == "approval_pending" || run.State == "browser_login_blocked" {
		status = app.WorkflowResultWaiting
	} else if run.State == "blocked" {
		status = app.WorkflowResultBlocked
	}
	ownerID, authorization, err := r.workflowResultIdentity(ctx, run)
	if err != nil {
		return nil, err
	}
	content, err := r.workflowResultContentFromToolCalls(ctx, run, summary)
	if err != nil {
		return nil, err
	}
	result := &app.WorkflowResult{
		SchemaVersion: app.WorkflowResultSchemaVersion, ID: "workflow_result_" + run.ID, RunID: run.ID,
		OwnerID: ownerID, Authorization: authorization,
		Status: status, CapabilityPath: nil, Workflow: app.WorkflowContractRef{ID: "legacy.unmatched", Revision: 1},
		Content:     content,
		ReturnRoute: workflowResultReturnRoute(status, returnRoute),
	}
	if run.MessageContext != nil {
		result.MCP = run.MessageContext.MCP
	}
	return result, nil
}

func workflowResultReturnRoute(status app.WorkflowResultStatus, route app.ReturnRoute) app.ReturnRoute {
	if status != app.WorkflowResultSucceeded && route.Mode == app.ReturnToEndpoint {
		return app.ReturnRoute{Mode: app.ReturnNowhere}
	}
	return route
}

func (r Runtime) workflowResultIdentity(ctx context.Context, run app.AgentRun) (string, app.MessageAuthorization, error) {
	if run.MessageContext != nil {
		ownerID := strings.TrimSpace(run.MessageContext.OwnerID)
		authorization := run.MessageContext.Authorization
		if ownerID != "" && strings.TrimSpace(authorization.PrincipalID) == ownerID {
			authorization.Scope = append([]string(nil), authorization.Scope...)
			return ownerID, authorization, nil
		}
	}
	ownerID := app.DefaultOwnerID
	session, ok, err := r.store.GetSession(ctx, run.SessionID)
	if err != nil {
		return "", app.MessageAuthorization{}, err
	}
	if ok && strings.TrimSpace(session.OwnerID) != "" {
		ownerID = strings.TrimSpace(session.OwnerID)
	}
	return ownerID, app.MessageAuthorization{PrincipalID: ownerID}, nil
}

func workflowResourceRefs(state *app.WorkflowState) []app.ResourceRef {
	refs := []app.ResourceRef{}
	if state == nil {
		return refs
	}
	visited := make(map[app.WorkflowNodeID]bool, len(state.Nodes))
	for _, plannedNode := range state.Plan.Nodes {
		node, ok := state.Nodes[plannedNode.ID]
		if !ok {
			continue
		}
		visited[plannedNode.ID] = true
		refs = appendUniqueResourceRefs(refs, node.OutcomeRefs...)
	}
	remaining := make([]app.WorkflowNodeID, 0, len(state.Nodes)-len(visited))
	for nodeID := range state.Nodes {
		if !visited[nodeID] {
			remaining = append(remaining, nodeID)
		}
	}
	slices.Sort(remaining)
	for _, nodeID := range remaining {
		refs = appendUniqueResourceRefs(refs, state.Nodes[nodeID].OutcomeRefs...)
	}
	return refs
}
