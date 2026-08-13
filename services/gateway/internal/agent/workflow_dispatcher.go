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
		result := r.blockPersistedWorkflowResume(ctx, run, content, err)
		return result, true, nil
	}
	profile, err := r.profiles.Get(run.Workflow.Plan.ProfileID, run.Workflow.Plan.ProfileRevision)
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
	decisionObservations := []string{}
	if run.Workflow.Status == app.WorkflowStatusRunning {
		observation, _, decisionErr := r.resolveActiveWorkflowDecisions(ctx, &run, profile)
		if decisionErr != nil {
			result := r.blockPersistedWorkflowResume(ctx, run, content, decisionErr)
			return result, true, nil
		}
		if strings.TrimSpace(observation) != "" {
			decisionObservations = append(decisionObservations, observation)
		}
		if refreshed, ok := r.store.GetRun(run.ID); ok {
			run = refreshed
		}
	}
	workflowExecution := workflowExecutionResult{}
	if run.Workflow.Status == app.WorkflowStatusRunning {
		stageContext := profile.StageContext(run.Workflow)
		visibleTools, exposeErr := r.materializeActiveWorkflowTools(ctx, run, r.workflowActorRef(run.SessionID), &stageContext)
		if exposeErr != nil {
			result := r.blockPersistedWorkflowResume(ctx, run, content, exposeErr)
			return result, true, nil
		}
		if refreshed, ok := r.store.GetRun(run.ID); ok {
			run = refreshed
		}
		run.State = "executing"
		run.CompletedAt = nil
		r.store.SaveRun(run)
		workflowExecution = r.runWorkflowWithSeed(
			ctx, run.SessionID, run, content, profile, stageContext, visibleTools,
			seedCalls, append(observationsForResume(seedCalls), decisionObservations...),
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
	route := run.Workflow.Route
	workflowResult := r.workflowResultForRun(run, route, run.Workflow.ReturnRoute, run.Summary)
	assistant := r.persistWorkflowAssistantMessage(run, workflowResult, now)
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
	stageContext := resolved.Profile.StageContext(run.Workflow)
	visibleTools, err := r.materializeActiveWorkflowTools(ctx, run, r.workflowActorRef(run.SessionID), &stageContext)
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
			"workflow_id": resolved.Plan.ProfileID, "node_id": stageContext.WorkflowNodeID,
			"scope_revision": stageContext.ScopeRevision, "tools": visibleToolNames(visibleTools),
		},
	})
	return matchedWorkflowDispatch{Run: run, Profile: resolved.Profile, Context: stageContext, Tools: visibleTools}, nil
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
		Data:       r.workflowResultData(run),
		Content:    r.workflowResultContent(run, summary),
		References: workflowResourceRefs(run.Workflow), ReturnRoute: returnRoute,
	}
	if run.MessageContext != nil {
		result.MCP = run.MessageContext.MCP
	}
	if status == app.WorkflowResultFailed || status == app.WorkflowResultBlocked {
		result.Error = &app.WorkflowResultError{Code: "workflow_" + string(status), Message: summary}
	}
	return r.protectExternalSendResult(run, result)
}

func (r Runtime) workflowResultData(run app.AgentRun) map[string]any {
	if run.Workflow == nil || r.tools == nil || r.store == nil {
		return nil
	}
	for _, ref := range workflowResourceRefs(run.Workflow) {
		if strings.TrimSpace(ref.Provenance) == "" {
			continue
		}
		call, ok := r.store.GetToolCall(ref.Provenance)
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
		return data
	}
	return nil
}

func (r Runtime) workflowResultContent(run app.AgentRun, summary string) app.MessageContent {
	outputParts := []app.MessagePart{}
	if run.Workflow == nil {
		return workflowResultTextContent(summary)
	}
	if run.Workflow.Plan.ProfileID == app.WorkflowConversationAnswer && run.Workflow.Route.Slots.Operation == app.RouteOperationPublish &&
		run.MessageContext != nil && len(run.MessageContext.RequestContent.Parts) > 0 {
		return cloneMessageContent(run.MessageContext.RequestContent)
	}
	for refIndex, ref := range workflowResourceRefs(run.Workflow) {
		if ref.Kind != "path" || strings.TrimSpace(ref.Ref) == "" || strings.TrimSpace(ref.Provenance) == "" {
			continue
		}
		call, ok := r.store.GetToolCall(ref.Provenance)
		if !ok || !toolCallCompleted(call) {
			continue
		}
		if part, ok := r.workflowOutputPart(run.SessionID, call, ref, refIndex); ok {
			outputParts = append(outputParts, part)
		}
	}
	if len(outputParts) > 0 {
		return app.MessageContent{Parts: outputParts}
	}
	return workflowResultTextContent(summary)
}

func (r Runtime) governWorkflowRequestContent(run app.AgentRun) (app.MessageContent, error) {
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
		part, err := r.governWorkflowRequestPart(run, content.Parts[index])
		if err != nil {
			return app.MessageContent{}, fmt.Errorf("message part %q: %w", content.Parts[index].ID, err)
		}
		governed = append(governed, part)
	}
	content.Parts = governed
	return content, nil
}

func (r Runtime) governWorkflowRequestPart(run app.AgentRun, part app.MessagePart) (app.MessagePart, error) {
	if r.store == nil || part.Resource == nil || part.Resource.Kind != "workspace_file" {
		return app.MessagePart{}, errors.New("binary message part is not a governed workspace file")
	}
	ref := filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.TrimSpace(part.Resource.Ref))))
	if ref == "." || filepath.IsAbs(filepath.FromSlash(ref)) || ref == ".." || strings.HasPrefix(ref, "../") {
		return app.MessagePart{}, errors.New("workspace file reference is invalid")
	}
	root := strings.TrimSpace(r.workspaceRootForSession(run.SessionID))
	if root == "" {
		return app.MessagePart{}, errors.New("source workspace is unavailable")
	}
	root, err := filepath.Abs(root)
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

func (r Runtime) workflowResultContentFromToolCalls(run app.AgentRun, summary string) app.MessageContent {
	parts := []app.MessagePart{}
	for _, call := range toolCallsForRun(r.store.ListToolCalls(run.SessionID), run.ID) {
		if !toolCallCompleted(call) {
			continue
		}
		for index, ref := range toolCallOutputRefs(call, r.tools) {
			if part, ok := r.workflowOutputPart(run.SessionID, call, ref, index); ok {
				parts = append(parts, part)
			}
		}
	}
	if len(parts) > 0 {
		return app.MessageContent{Parts: parts}
	}
	return workflowResultTextContent(summary)
}

func (r Runtime) workflowOutputPart(sessionID string, call app.ToolCall, ref app.ResourceRef, index int) (app.MessagePart, bool) {
	definition, ok := r.tools.Definition(call.Tool)
	if !ok {
		return app.MessagePart{}, false
	}
	kind := app.MessagePartKind("")
	switch {
	case containsToolEffect(definition.Directory.Effects, app.ToolEffectWorkspaceWrite) && containsOutputKind(definition.Directory.OutputKinds, app.OutputKindFile):
		kind = app.MessagePartFile
	case containsOutputKind(definition.Directory.OutputKinds, app.OutputKindImage):
		kind = app.MessagePartImage
	default:
		return app.MessagePart{}, false
	}
	resourceRef, ok := r.workflowOutputResourceRef(sessionID, ref)
	if !ok {
		return app.MessagePart{}, false
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
	return app.MessagePart{
		ID: fmt.Sprintf("%s:output:%d", call.ID, index), Kind: kind, Disposition: disposition,
		ArtifactID: r.persistWorkflowOutputArtifact(sessionID, call, resourceRef, contentType), Resource: &resourceRef,
		Name: name, ContentType: contentType, Bytes: intLikeValue(output["bytes"]),
		Width: intLikeValue(output["width"]), Height: intLikeValue(output["height"]),
		SHA256: cleanOptionalString(output["sha256"]), Caption: caption,
	}, true
}

func (r Runtime) persistWorkflowOutputArtifact(sessionID string, call app.ToolCall, resource app.ResourceRef, contentType string) string {
	if r.store == nil || resource.Kind != "workspace_file" || strings.TrimSpace(resource.Ref) == "" {
		return ""
	}
	workspaceRoot := r.workspaceRootForSession(sessionID)
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return ""
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return ""
	}
	candidate, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(resource.Ref)))
	if err != nil || candidate == root || !strings.HasPrefix(candidate, root+string(os.PathSeparator)) {
		return ""
	}
	lstat, err := os.Lstat(candidate)
	if err != nil || lstat.Mode()&os.ModeSymlink != 0 || !lstat.Mode().IsRegular() {
		return ""
	}
	candidate, err = filepath.EvalSymlinks(candidate)
	if err != nil || !strings.HasPrefix(candidate, root+string(os.PathSeparator)) {
		return ""
	}
	info, err := os.Stat(candidate)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	for _, object := range r.store.ListArtifactObjects(0) {
		if object.RunID == call.RunID && object.SessionID == sessionID && filepath.Clean(object.Path) == candidate && object.Bytes == int(info.Size()) {
			return object.ID
		}
	}
	object := app.ArtifactObject{
		ID: app.NewID("obj"), Kind: "workflow_output", RunID: call.RunID, SessionID: sessionID,
		Backend: "workspace", Key: resource.Ref, URI: "workspace://" + filepath.ToSlash(resource.Ref),
		Path: candidate, ContentType: contentType, Bytes: int(info.Size()), CreatedAt: completedToolCallTime(call),
	}
	r.store.SaveArtifactObject(object)
	return object.ID
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

func (r Runtime) persistWorkflowAssistantMessage(run app.AgentRun, result *app.WorkflowResult, now time.Time) app.Message {
	if r.isExternalMediaPublication(run) {
		return app.Message{}
	}
	return r.store.AddMessage(r.messageWithWorkflowResult(app.Message{
		SessionID: run.SessionID,
		RunID:     run.ID,
		Role:      "assistant",
		Content:   run.Summary,
		CreatedAt: now,
	}, result))
}

func (r Runtime) workflowOutputResourceRef(sessionID string, ref app.ResourceRef) (app.ResourceRef, bool) {
	session, ok := r.store.GetSession(sessionID)
	if !ok {
		return app.ResourceRef{}, false
	}
	workspaceRoot := strings.TrimSpace(session.WorkspaceRoot)
	if workspaceRoot == "" && r.tools != nil {
		workspaceRoot = strings.TrimSpace(r.tools.Config().Workspaces.DefaultRoot)
	}
	if workspaceRoot == "" {
		return app.ResourceRef{}, false
	}
	root, err := filepath.Abs(workspaceRoot)
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
	if run.MessageContext != nil {
		result.MCP = run.MessageContext.MCP
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
	if run.MessageContext != nil {
		result.MCP = run.MessageContext.MCP
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
		Status: status, CapabilityPath: nil, Workflow: app.WorkflowContractRef{ID: "legacy.unmatched", Revision: 1},
		Content:     r.workflowResultContentFromToolCalls(run, summary),
		ReturnRoute: returnRoute,
	}
	if run.MessageContext != nil {
		result.MCP = run.MessageContext.MCP
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
