package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/workspacefiles"
)

const (
	responseMediaMaxFiles       = 8
	responseMediaMaxTotalBytes  = 4 << 20
	responseMediaMaxWalkEntries = 20000
	responseMediaMaxWalkDepth   = 32
	responseMediaWalkTimeout    = 3 * time.Second
)

type responseMediaCandidate struct {
	relPath string
	name    string
}

// detectResponseMediaNodeID identifies the conversation-answer stage that
// gates external MCP access to response media.
const detectResponseMediaNodeID app.WorkflowNodeID = "detect_response_media"

// responseMediaDetectionStage reports whether the run is parked exactly on
// the conversation-answer response-media detection node. The check is keyed
// on the plan's node content rather than a profile-revision literal so a
// revision bump cannot silently disable the workspace approval gate.
func responseMediaDetectionStage(run *app.AgentRun) bool {
	if run == nil || run.Workflow == nil || run.Workflow.Plan.ProfileID != app.WorkflowConversationAnswer {
		return false
	}
	if len(run.Workflow.ActiveNodeIDs) != 1 || run.Workflow.ActiveNodeIDs[0] != detectResponseMediaNodeID {
		return false
	}
	for _, node := range run.Workflow.Plan.Nodes {
		if node.ID == detectResponseMediaNodeID {
			return true
		}
	}
	return false
}

// requireApprovedWorkspaceAccessCall verifies that the workspace data access
// call completed through an approved, still-valid owner approval.
func (r Runtime) requireApprovedWorkspaceAccessCall(ctx context.Context, call *app.ToolCall) error {
	if call == nil || call.Status != app.ToolCallStatusCompletedAfterApproval {
		return errors.New("external MCP workspace data access requires owner approval")
	}
	approval, ok, err := r.store.GetApproval(ctx, call.ApprovalID)
	if err != nil {
		return err
	}
	if !ok || approval.Status != app.ApprovalStatusApproved {
		return errors.New("external MCP workspace data access approval is unavailable")
	}
	return r.validateWorkspaceDataAccessApproval(ctx, *call, approval)
}

func (r Runtime) completeConversationMediaDetection(ctx context.Context, run *app.AgentRun) error {
	if run == nil || run.MessageContext == nil || !responseMediaDetectionStage(run) {
		return nil
	}
	if _, required, err := mcpResponseMediaAccessRequest(run); err != nil {
		return err
	} else if required {
		call, err := r.workspaceDataAccessCallForRun(ctx, run.ID)
		if err != nil {
			return err
		}
		if err := r.requireApprovedWorkspaceAccessCall(ctx, call); err != nil {
			return err
		}
	}
	nodeID := run.Workflow.ActiveNodeIDs[0]
	node, ok := workflowPlanNode(run.Workflow.Plan, nodeID)
	if !ok || node.Goal.Completion != app.CompletionDeterministic {
		return errors.New("conversation response-media detector has an invalid workflow contract")
	}
	decision, content := r.detectConversationResponseMedia(ctx, *run)
	run.MessageContext.ResponseMedia = &decision
	run.MessageContext.ResponseContent = cloneMessageContent(content)
	nodeState := run.Workflow.Nodes[nodeID]
	nodeState.Status = app.WorkflowNodeSucceeded
	nodeState.Attempts = 1
	nodeState.OutcomeRefs = []app.ResourceRef{{Kind: "response_media_decision", Ref: string(decision.Status), Provenance: "workflow_runtime"}}
	run.Workflow.Nodes[nodeID] = nodeState
	run.Workflow.ActiveNodeIDs = removeWorkflowNodeID(run.Workflow.ActiveNodeIDs, nodeID)
	activateReadyWorkflowNodes(run.Workflow)
	if len(run.Workflow.ActiveNodeIDs) != 1 || run.Workflow.ActiveNodeIDs[0] != "answer" {
		return errors.New("conversation response-media detection did not activate the answer node")
	}
	saved, err := r.saveRun(ctx, *run)
	if err != nil {
		return err
	}
	*run = saved
	r.addAudit(ctx, app.AuditEvent{
		SessionID: run.SessionID, RunID: run.ID, Actor: "workflow_dispatcher", Type: "workflow.response_media_detected",
		Summary: string(decision.Status), Fields: map[string]any{
			"reason_code": decision.ReasonCode, "resource_count": len(decision.Resources), "locator_count": len(run.MessageContext.MediaLocators),
		},
	})

	return nil
}

func (r Runtime) detectConversationResponseMedia(ctx context.Context, run app.AgentRun) (app.ResponseMediaDecision, app.MessageContent) {
	request := run.MessageContext.RequestContent
	locators := append([]app.MessageMediaLocator(nil), run.MessageContext.MediaLocators...)
	if run.Workflow.Route.Slots.Operation == app.RouteOperationPublish {
		media := make([]app.MessagePart, 0, len(request.Parts))
		for _, part := range request.Parts {
			if isMediaMessagePart(part.Kind) {
				media = append(media, part)
			}
		}
		if len(media) > 0 {
			return r.governResponseMediaParts(ctx, run, media, "current_turn_attachment")
		}
		if len(locators) == 0 {
			if locator, ok := implicitResponseMediaLocator(run.Workflow.Route.Slots.Query); ok {
				locators = []app.MessageMediaLocator{locator}
			} else {
				return app.ResponseMediaDecision{Status: app.ResponseMediaNone}, cloneMessageContent(request)
			}
		}
	}
	if len(locators) == 0 {
		return app.ResponseMediaDecision{Status: app.ResponseMediaNone}, app.MessageContent{}
	}
	parts := make([]app.MessagePart, 0, len(locators))
	seen := map[string]bool{}
	for index, locator := range locators {
		candidate, reason, err := r.resolveResponseMediaLocator(ctx, run, locator)
		if err != nil {
			status := app.ResponseMediaBlocked
			if reason == "file_not_found" {
				status = app.ResponseMediaClarify
			}
			return app.ResponseMediaDecision{Status: status, ReasonCode: reason}, app.MessageContent{}
		}
		if seen[candidate.relPath] {
			return app.ResponseMediaDecision{Status: app.ResponseMediaBlocked, ReasonCode: "duplicate_response_media"}, app.MessageContent{}
		}
		seen[candidate.relPath] = true
		part := responseMediaPart(index, candidate.relPath, locator.Caption)
		parts = append(parts, part)
	}
	return r.governResponseMediaParts(ctx, run, parts, "media_locator")
}

func (r Runtime) governResponseMediaParts(ctx context.Context, run app.AgentRun, parts []app.MessagePart, provenance string) (app.ResponseMediaDecision, app.MessageContent) {
	if len(parts) == 0 {
		return app.ResponseMediaDecision{Status: app.ResponseMediaNone}, app.MessageContent{}
	}
	if len(parts) > responseMediaMaxFiles {
		return app.ResponseMediaDecision{Status: app.ResponseMediaBlocked, ReasonCode: "response_media_limit_exceeded"}, app.MessageContent{}
	}
	governed := make([]app.MessagePart, 0, len(parts))
	refs := make([]app.ResourceRef, 0, len(parts))
	seen := map[string]bool{}
	maxBytes := responseMediaMaxTotalBytes
	if run.MessageContext != nil && run.MessageContext.MCP != nil {
		maxBytes = app.MCPMaxResultRawBinaryBytes
	}
	totalBytes := 0
	for _, source := range parts {
		part, err := r.governWorkflowRequestPart(ctx, run, source)
		if err != nil {
			return app.ResponseMediaDecision{Status: app.ResponseMediaBlocked, ReasonCode: "response_media_invalid"}, app.MessageContent{}
		}
		if part.Resource == nil || seen[part.Resource.Ref] {
			return app.ResponseMediaDecision{Status: app.ResponseMediaBlocked, ReasonCode: "duplicate_response_media"}, app.MessageContent{}
		}
		seen[part.Resource.Ref] = true
		totalBytes += part.Bytes
		if part.Bytes < 0 || part.Bytes > maxBytes || totalBytes > maxBytes {
			return app.ResponseMediaDecision{Status: app.ResponseMediaBlocked, ReasonCode: "response_media_too_large"}, app.MessageContent{}
		}
		part.Resource.Provenance = provenance
		governed = append(governed, part)
		refs = append(refs, app.ResourceRef{Kind: "workspace_file", Ref: part.Resource.Ref, Provenance: provenance, Attributes: map[string]string{
			"artifact_id": part.ArtifactID, "name": part.Name, "content_type": part.ContentType,
			"bytes": fmt.Sprint(part.Bytes), "sha256": part.SHA256,
		}})
	}
	return app.ResponseMediaDecision{Status: app.ResponseMediaSelected, Resources: refs}, app.MessageContent{Parts: governed}
}

func (r Runtime) resolveResponseMediaLocator(ctx context.Context, run app.AgentRun, locator app.MessageMediaLocator) (responseMediaCandidate, string, error) {
	workspaceRoot, err := r.workspaceRootForSession(ctx, run.SessionID)
	if err != nil {
		return responseMediaCandidate{}, "file_lookup_failed", err
	}
	root, err := governedResponseMediaRoot(workspaceRoot)
	if err != nil {
		return responseMediaCandidate{}, "file_lookup_failed", err
	}
	if locator.Path != "" {
		rel := filepath.ToSlash(filepath.Clean(filepath.FromSlash(locator.Path)))
		if _, err := governedResponseMediaFile(root, rel); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return responseMediaCandidate{}, "file_not_found", err
			}
			return responseMediaCandidate{}, "response_media_invalid", err
		}
		return responseMediaCandidate{relPath: rel, name: filepath.Base(rel)}, "", nil
	}
	query := locator.Query
	exactName := locator.Name
	mode := workspacefiles.MatchFuzzy
	term := query
	if exactName != "" {
		mode = workspacefiles.MatchExact
		term = exactName
	}
	result, err := workspacefiles.Search(ctx, root, workspacefiles.SearchRequest{
		Mode: mode, Term: term, MaxResults: 2,
		MaxEntries: responseMediaMaxWalkEntries, MaxDepth: responseMediaMaxWalkDepth, Timeout: responseMediaWalkTimeout,
		Validate: func(relPath string) error {
			_, err := governedResponseMediaFile(root, relPath)
			return err
		},
	})
	if err != nil {
		r.auditResponseMediaLookup(ctx, run, mode, term, workspacefiles.SearchResult{}, err)
		return responseMediaCandidate{}, "file_lookup_failed", err
	}
	r.auditResponseMediaLookup(ctx, run, mode, term, result, nil)
	if !result.Complete {
		return responseMediaCandidate{}, "file_lookup_incomplete", errors.New("workspace traversal did not complete")
	}
	if exactName != "" && len(result.Matches) == 0 {
		result, err = workspacefiles.Search(ctx, root, workspacefiles.SearchRequest{
			Mode: workspacefiles.MatchFuzzy, Term: exactName, MaxResults: 2,
			MaxEntries: responseMediaMaxWalkEntries, MaxDepth: responseMediaMaxWalkDepth, Timeout: responseMediaWalkTimeout,
			Validate: func(relPath string) error {
				_, err := governedResponseMediaFile(root, relPath)
				return err
			},
		})
		if err != nil {
			r.auditResponseMediaLookup(ctx, run, workspacefiles.MatchFuzzy, exactName, workspacefiles.SearchResult{}, err)
			return responseMediaCandidate{}, "file_lookup_failed", err
		}
		r.auditResponseMediaLookup(ctx, run, workspacefiles.MatchFuzzy, exactName, result, nil)
		if !result.Complete {
			return responseMediaCandidate{}, "file_lookup_incomplete", errors.New("workspace traversal did not complete")
		}
	}
	if len(result.Matches) == 0 {
		return responseMediaCandidate{}, "file_not_found", fs.ErrNotExist
	}
	return responseMediaCandidate{relPath: result.Matches[0].RelPath, name: result.Matches[0].Name}, "", nil
}

func (r Runtime) auditResponseMediaLookup(ctx context.Context, run app.AgentRun, mode workspacefiles.MatchMode, term string, result workspacefiles.SearchResult, searchErr error) {
	digest := sha256.Sum256([]byte(strings.TrimSpace(term)))
	fields := map[string]any{
		"query_digest": hex.EncodeToString(digest[:]), "stage": mode, "match_count": result.Total,
		"complete": result.Complete, "truncated": result.Truncated, "ranker_revision": "filename_v1",
	}
	if len(result.Matches) > 0 {
		fields["selected_score"] = result.Matches[0].Score
		fields["selected_reason"] = result.Matches[0].Reason
		fields["tie_break_used"] = len(result.Matches) > 1 && result.Matches[0].Score == result.Matches[1].Score
	}
	if searchErr != nil {
		fields["reason_code"] = "file_lookup_failed"
	}
	r.addAudit(ctx, app.AuditEvent{
		SessionID: run.SessionID, RunID: run.ID, Actor: "workflow_dispatcher", Type: "workflow.response_media_lookup",
		Summary: string(mode), Fields: fields,
	})

}

func governedResponseMediaRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", errors.New("workspace is unavailable")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return resolved, nil
}

func governedResponseMediaFile(root, rel string) (string, error) {
	rel = filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.TrimSpace(rel))))
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || filepath.IsAbs(filepath.FromSlash(rel)) {
		return "", errors.New("workspace-relative file path is invalid")
	}
	candidate, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil || candidate == root || !strings.HasPrefix(candidate, root+string(os.PathSeparator)) {
		return "", errors.New("workspace file escapes the workspace")
	}
	lstat, err := os.Lstat(candidate)
	if err != nil {
		return "", err
	}
	if lstat.Mode()&os.ModeSymlink != 0 || !lstat.Mode().IsRegular() {
		return "", errors.New("workspace file is not a regular file")
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil || !strings.HasPrefix(resolved, root+string(os.PathSeparator)) {
		return "", errors.New("workspace file resolves outside the workspace")
	}
	return resolved, nil
}

func implicitResponseMediaLocator(text string) (app.MessageMediaLocator, bool) {
	query := strings.TrimSpace(text)
	lower := strings.ToLower(query)
	mediaWords := []string{"file", "document", "report", "image", "photo", "audio", "video", "attachment", "presentation", "spreadsheet", "文件", "文档", "报告", "图片", "照片", "音频", "视频", "附件", "演示", "表格"}
	if query == "" || !slices.ContainsFunc(mediaWords, func(word string) bool { return strings.Contains(lower, word) }) {
		return app.MessageMediaLocator{}, false
	}
	for _, token := range strings.Fields(query) {
		candidate := strings.Trim(token, "\"'`.,;:!?()[]{}<>，。；：！？（）【】《》")
		if filepath.Ext(candidate) != "" && filepath.Base(candidate) == candidate {
			return app.MessageMediaLocator{Name: candidate}, true
		}
	}
	replacer := strings.NewReplacer(
		"please", " ", "send", " ", "publish", " ", "share", " ", "give me", " ", "to me", " ",
		"请", " ", "把", " ", "发送", " ", "发给我", " ", "发我", " ", "给我", " ", "返回", " ", "这个", " ", "这份", " ", "那个", " ",
	)
	query = strings.TrimSpace(replacer.Replace(lower))
	if query == "" {
		return app.MessageMediaLocator{}, false
	}
	return app.MessageMediaLocator{Query: query}, true
}

func responseMediaPart(index int, relPath, caption string) app.MessagePart {
	name := filepath.Base(relPath)
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	kind := app.MessagePartFile
	disposition := app.MessageDispositionAttachment
	if strings.HasPrefix(contentType, "image/") {
		kind, disposition = app.MessagePartImage, app.MessageDispositionInline
	} else if strings.HasPrefix(contentType, "audio/") {
		kind = app.MessagePartAudio
	}
	return app.MessagePart{
		ID: fmt.Sprintf("response_media:%d", index), Kind: kind, Disposition: disposition,
		Resource: &app.ResourceRef{Kind: "workspace_file", Ref: relPath, Provenance: "media_locator"},
		Name:     name, ContentType: contentType, Caption: strings.TrimSpace(caption),
	}
}

func (r Runtime) runConversationResponseContentStep(ctx context.Context, run app.AgentRun) workflowExecutionResult {
	if run.MessageContext == nil || run.MessageContext.ResponseMedia == nil {
		return workflowExecutionResult{Halted: true, FinalAnswer: "Blocked: the response-media decision is unavailable."}
	}
	switch run.MessageContext.ResponseMedia.Status {
	case app.ResponseMediaClarify:
		return workflowExecutionResult{FinalAnswer: responseMediaClarification(), Completed: true}
	case app.ResponseMediaBlocked:
		return workflowExecutionResult{FinalAnswer: responseMediaBlockedMessage(run.MessageContext.ResponseMedia.ReasonCode), Completed: true}
	case app.ResponseMediaNone:
		content := cloneMessageContent(run.MessageContext.ResponseContent)
		if len(content.Parts) == 0 {
			content = cloneMessageContent(run.MessageContext.RequestContent)
		}
		run.MessageContext.ResponseContent = content
		if _, err := r.saveRun(ctx, run); err != nil {
			result := workflowExecutionResult{Halted: true}
			result.fail(workflowFailureStateInvalid, err)
			return result
		}
		return workflowExecutionResult{FinalAnswer: publishedMessageSummary(content), Completed: true}
	case app.ResponseMediaSelected:
		if err := r.revalidateFrozenResponseMedia(ctx, &run); err != nil {
			return workflowExecutionResult{Halted: true, FinalAnswer: "Blocked: response media changed after it was selected."}
		}
		return workflowExecutionResult{FinalAnswer: publishedMessageSummary(run.MessageContext.ResponseContent), Completed: true}
	default:
		return workflowExecutionResult{Halted: true, FinalAnswer: "Blocked: the response-media decision is invalid."}
	}
}

func (r Runtime) revalidateFrozenResponseMedia(ctx context.Context, run *app.AgentRun) error {
	if run == nil || run.MessageContext == nil || run.MessageContext.ResponseMedia == nil ||
		run.MessageContext.ResponseMedia.Status != app.ResponseMediaSelected {
		return errors.New("selected response media is unavailable")
	}
	decision, content := r.governResponseMediaParts(ctx, *run, run.MessageContext.ResponseContent.Parts, "response_media_frozen")
	if decision.Status != app.ResponseMediaSelected || !sameFrozenResponseMedia(*run.MessageContext.ResponseMedia, decision) {
		return errors.New("selected response media changed")
	}
	run.MessageContext.ResponseMedia = &decision
	run.MessageContext.ResponseContent = content
	saved, err := r.saveRun(ctx, *run)
	if err != nil {
		return err
	}
	*run = saved
	return nil
}

func sameFrozenResponseMedia(before, after app.ResponseMediaDecision) bool {
	if len(before.Resources) != len(after.Resources) {
		return false
	}
	for index := range before.Resources {
		if before.Resources[index].Kind != after.Resources[index].Kind || before.Resources[index].Ref != after.Resources[index].Ref {
			return false
		}
		for _, key := range []string{"artifact_id", "name", "content_type", "bytes", "sha256"} {
			if before.Resources[index].Attributes[key] != after.Resources[index].Attributes[key] {
				return false
			}
		}
	}
	return true
}

func responseMediaClarification() string {
	return "I couldn't find a matching workspace file. Please refine the file name or description, or attach the file directly."
}

func responseMediaBlockedMessage(reason string) string {
	if reason == "file_lookup_incomplete" {
		return "Blocked: workspace file lookup did not complete, so no provisional file was sent."
	}
	if reason == "file_lookup_failed" {
		return "Blocked: workspace file lookup failed, so no file was sent."
	}
	return "Blocked: the requested workspace media could not be governed for delivery."
}
