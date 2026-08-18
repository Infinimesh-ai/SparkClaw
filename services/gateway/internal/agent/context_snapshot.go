package agent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const (
	defaultContextMessageLimit     = 8
	defaultContextEpisodeLimit     = 4
	defaultContextMemoryLimit      = 4
	defaultContextToolLimit        = 6
	contextToolSummaryLimit        = 4000
	compactContextToolLimit        = 3
	compactContextToolSummaryLimit = 1200
	compactContextMemoryLimit      = 2
	compactContextMemoryBytes      = 120
)

type agentContextSnapshot struct {
	Messages     []app.Message
	Episodes     []app.EpisodeSummary
	Memories     []app.Memory
	ToolResults  []app.ToolCall
	RecentImages []app.MessageAttachment
}

func (r Runtime) buildAgentContextSnapshot(sessionID, currentRunID, currentContent string) agentContextSnapshot {
	if run, ok := r.store.GetRun(currentRunID); ok && run.MessageContext != nil && isExternalMCPInvocation(run.MessageContext.MCP) {
		// An inbound MCP request must not inherit cached workspace derivatives
		// through conversation, tool, memory, image, or episode context. Exact
		// workspace access is admitted through the current run's bound approval
		// and its current-run observations instead.
		return agentContextSnapshot{}
	}
	return agentContextSnapshot{
		Messages:     recentContextMessages(r.store.ListMessages(sessionID), currentRunID, defaultContextMessageLimit),
		Episodes:     recentContextEpisodes(r.store.ListEpisodeSummaries(sessionID), defaultContextEpisodeLimit),
		Memories:     relevantContextMemories(r.store.SearchMemories(currentContent), defaultContextMemoryLimit),
		ToolResults:  recentContextToolResults(r.store.ListToolCalls(sessionID), currentRunID, defaultContextToolLimit),
		RecentImages: recentContextImages(r.store.ListMessages(sessionID), currentRunID, 3),
	}
}

func (snapshot agentContextSnapshot) ForIntentRouting() string {
	return snapshot.contextBuilder(contextRenderIntent).Render(0)
}

func (snapshot agentContextSnapshot) ForWorkflowStep() string {
	return snapshot.contextBuilder(contextRenderWorkflow).Render(0)
}

type contextRenderMode string

const (
	contextRenderIntent   contextRenderMode = "intent"
	contextRenderWorkflow contextRenderMode = "workflow"
)

func (snapshot agentContextSnapshot) contextBuilder(mode contextRenderMode) contextBuilder {
	messageFull := titledContextSection("Recent conversation:", formatContextMessages(snapshot.Messages))
	messageCompact := titledContextSection("Recent conversation (older context compacted):", formatContextMessages(tailMessages(snapshot.Messages, 4)))
	toolFull := titledContextSection("Recent tool results / current working context:", formatContextToolResults(snapshot.ToolResults))
	toolCompact := titledContextSection("Recent tool results / prior working context (old session context compacted; current workflow step observations are preserved separately):", formatContextToolResultsWithLimit(tailToolCalls(snapshot.ToolResults, compactContextToolLimit), compactContextToolSummaryLimit))
	imageFull := titledContextSection("Recent session images available for image understanding or final Markdown media replies:", formatContextImages(snapshot.RecentImages))
	imageCompact := titledContextSection("Latest session image available for image understanding or final Markdown media replies:", formatCompactContextImages(snapshot.RecentImages))
	memoryFull := titledContextSection("Relevant accepted memories:", formatContextMemories(snapshot.Memories))
	memoryCompact := titledContextSection("Most relevant accepted memories (compact):", formatContextMemoriesWithLimit(snapshot.Memories, compactContextMemoryLimit, compactContextMemoryBytes))
	sections := []contextSection{
		degradingContextSection("recent_conversation", 40, messageFull, messageCompact, true),
	}
	if mode == contextRenderIntent {
		episodes := titledContextSection("Recent episode summaries:", formatContextEpisodes(snapshot.Episodes))
		compactEpisodes := titledContextSection("Recent episode summaries (compact):", formatCompactContextEpisodes(snapshot.Episodes))
		sections = append(sections, degradingContextSection("episodes", 10, episodes, compactEpisodes, true))
	}
	sections = append(sections,
		degradingContextSection("session_tool_results", 50, toolFull, toolCompact, true),
		degradingContextSection("recent_images", 20, imageFull, imageCompact, true),
		degradingContextSection("memories", 20, memoryFull, memoryCompact, true),
	)
	return contextBuilder{Sections: sections, SystemJoiner: "\n\n", UserJoiner: "\n\n"}
}

func titledContextSection(title, body string) string {
	if strings.TrimSpace(body) == "" {
		return ""
	}
	return title + "\n" + body
}

func tailMessages(messages []app.Message, limit int) []app.Message {
	if limit <= 0 || len(messages) == 0 {
		return nil
	}
	start := len(messages) - limit
	if start < 0 {
		start = 0
	}
	return append([]app.Message(nil), messages[start:]...)
}

func tailToolCalls(calls []app.ToolCall, limit int) []app.ToolCall {
	if limit <= 0 || len(calls) == 0 {
		return nil
	}
	start := len(calls) - limit
	if start < 0 {
		start = 0
	}
	return append([]app.ToolCall(nil), calls[start:]...)
}

func recentContextImages(messages []app.Message, currentRunID string, limit int) []app.MessageAttachment {
	if limit <= 0 || len(messages) == 0 {
		return nil
	}
	images := []app.MessageAttachment{}
	for _, message := range messages {
		if message.RunID == currentRunID {
			continue
		}
		for _, attachment := range message.Attachments {
			if isContextImageAttachment(attachment) {
				images = append(images, attachment)
			}
		}
	}
	if len(images) == 0 {
		return nil
	}
	start := len(images) - limit
	if start < 0 {
		start = 0
	}
	return append([]app.MessageAttachment(nil), images[start:]...)
}

func isContextImageAttachment(attachment app.MessageAttachment) bool {
	contentType := strings.ToLower(strings.TrimSpace(attachment.ContentType))
	if strings.HasPrefix(contentType, "image/") {
		return true
	}
	relPath := strings.ToLower(filepath.ToSlash(strings.TrimSpace(attachment.RelPath)))
	return strings.HasPrefix(relPath, "media/") && (strings.HasSuffix(relPath, ".png") ||
		strings.HasSuffix(relPath, ".jpg") ||
		strings.HasSuffix(relPath, ".jpeg") ||
		strings.HasSuffix(relPath, ".gif") ||
		strings.HasSuffix(relPath, ".webp"))
}

func recentContextMessages(messages []app.Message, currentRunID string, limit int) []app.Message {
	if limit <= 0 || len(messages) == 0 {
		return nil
	}
	filtered := make([]app.Message, 0, len(messages))
	for _, message := range messages {
		if message.RunID == currentRunID {
			continue
		}
		role := strings.TrimSpace(message.Role)
		if role != "user" && role != "assistant" {
			continue
		}
		if strings.TrimSpace(message.Content) == "" && len(message.Attachments) == 0 {
			continue
		}
		filtered = append(filtered, message)
	}
	if len(filtered) == 0 {
		return nil
	}
	start := len(filtered) - limit
	if start < 0 {
		start = 0
	}
	return append([]app.Message(nil), filtered[start:]...)
}

func recentContextEpisodes(episodes []app.EpisodeSummary, limit int) []app.EpisodeSummary {
	if limit <= 0 || len(episodes) == 0 {
		return nil
	}
	if len(episodes) > limit {
		episodes = episodes[:limit]
	}
	return append([]app.EpisodeSummary(nil), episodes...)
}

func relevantContextMemories(memories []app.Memory, limit int) []app.Memory {
	if limit <= 0 || len(memories) == 0 {
		return nil
	}
	if len(memories) > limit {
		memories = memories[:limit]
	}
	return append([]app.Memory(nil), memories...)
}

func recentContextToolResults(calls []app.ToolCall, currentRunID string, limit int) []app.ToolCall {
	if limit <= 0 || len(calls) == 0 {
		return nil
	}
	filtered := make([]app.ToolCall, 0, len(calls))
	for _, call := range calls {
		if call.RunID == currentRunID {
			continue
		}
		if strings.TrimSpace(call.ObservationSummary) == "" {
			continue
		}
		if !contextToolResultUseful(call) {
			continue
		}
		filtered = append(filtered, call)
	}
	if len(filtered) == 0 {
		return nil
	}
	start := len(filtered) - limit
	if start < 0 {
		start = 0
	}
	return append([]app.ToolCall(nil), filtered[start:]...)
}

func contextToolResultUseful(call app.ToolCall) bool {
	if strings.HasPrefix(call.Tool, "files.") ||
		strings.HasPrefix(call.Tool, "docx.") ||
		strings.HasPrefix(call.Tool, "pptx.") ||
		strings.HasPrefix(call.Tool, "xlsx.") ||
		strings.HasPrefix(call.Tool, "pdf.") ||
		strings.HasPrefix(call.Tool, "office.") {
		return true
	}
	if strings.HasPrefix(call.Tool, "browser.") || strings.HasPrefix(call.Tool, "web.") {
		return true
	}
	return false
}

func formatContextMessages(messages []app.Message) string {
	lines := make([]string, 0, len(messages))
	for _, message := range messages {
		content := strings.Join(strings.Fields(message.Content), " ")
		if content == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", message.Role, trimForEpisode(content, 360)))
	}
	return strings.Join(lines, "\n")
}

func formatContextEpisodes(episodes []app.EpisodeSummary) string {
	lines := make([]string, 0, len(episodes))
	for _, episode := range episodes {
		fields := []string{
			"goal=" + quoteEpisodeField(episode.Goal, 160),
			"outcome=" + quoteEpisodeField(episode.Outcome, 80),
			"risk=" + quoteEpisodeField(string(episode.Risk), 40),
		}
		if episode.ModelLane != "" {
			fields = append(fields, "lane="+quoteEpisodeField(episode.ModelLane, 40))
		}
		if len(episode.Tools) > 0 {
			fields = append(fields, "tools="+quoteEpisodeField(strings.Join(episode.Tools, ","), 240))
		}
		if len(episode.Approvals) > 0 {
			fields = append(fields, "approvals="+quoteEpisodeField(strings.Join(episode.Approvals, ","), 200))
		}
		if len(episode.Failures) > 0 {
			fields = append(fields, "failures="+quoteEpisodeField(strings.Join(episode.Failures, ","), 200))
		}
		if episode.RepairPerformed {
			fields = append(fields, "repair=true")
		}
		if episode.Summary != "" {
			fields = append(fields, "summary="+quoteEpisodeField(episode.Summary, 360))
		}
		lines = append(lines, "- "+strings.Join(fields, " "))
	}
	return strings.Join(lines, "\n")
}

func formatCompactContextEpisodes(episodes []app.EpisodeSummary) string {
	if len(episodes) > 2 {
		episodes = episodes[:2]
	}
	lines := make([]string, 0, len(episodes))
	for _, episode := range episodes {
		fields := []string{
			"goal=" + quoteEpisodeField(episode.Goal, 120),
			"outcome=" + quoteEpisodeField(episode.Outcome, 60),
		}
		if len(episode.Failures) > 0 {
			fields = append(fields, "failures="+quoteEpisodeField(strings.Join(episode.Failures, ","), 120))
		}
		if episode.Summary != "" {
			fields = append(fields, "summary="+quoteEpisodeField(episode.Summary, 160))
		}
		lines = append(lines, "- "+strings.Join(fields, " "))
	}
	return strings.Join(lines, "\n")
}

func formatContextMemories(memories []app.Memory) string {
	return formatContextMemoriesWithLimit(memories, len(memories), 260)
}

func formatContextMemoriesWithLimit(memories []app.Memory, limit, contentBytes int) string {
	if limit > 0 && len(memories) > limit {
		memories = memories[:limit]
	}
	lines := make([]string, 0, len(memories))
	for _, memory := range memories {
		content := strings.Join(strings.Fields(memory.Content), " ")
		if content == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("- kind=%s content=%s", quoteEpisodeField(memory.Kind, 60), quoteEpisodeField(content, contentBytes)))
	}
	return strings.Join(lines, "\n")
}

func formatCompactContextImages(images []app.MessageAttachment) string {
	if len(images) == 0 {
		return ""
	}
	image := images[len(images)-1]
	relPath := strings.TrimSpace(filepath.ToSlash(image.RelPath))
	if relPath == "" {
		return ""
	}
	fields := []string{
		"path=" + quoteEpisodeField(relPath, 180),
		"name=" + quoteEpisodeField(image.Name, 120),
	}
	if image.ContentType != "" {
		fields = append(fields, "content_type="+quoteEpisodeField(image.ContentType, 80))
	}
	if image.Summary != "" {
		fields = append(fields, "summary="+quoteEpisodeField(image.Summary, 140))
	}
	return "- " + strings.Join(fields, " ")
}

func formatContextImages(images []app.MessageAttachment) string {
	lines := make([]string, 0, len(images))
	for _, image := range images {
		relPath := strings.TrimSpace(filepath.ToSlash(image.RelPath))
		if relPath == "" {
			continue
		}
		fields := []string{
			"path=" + quoteEpisodeField(relPath, 180),
			"name=" + quoteEpisodeField(image.Name, 120),
		}
		if image.ContentType != "" {
			fields = append(fields, "content_type="+quoteEpisodeField(image.ContentType, 80))
		}
		if image.Bytes > 0 {
			fields = append(fields, fmt.Sprintf("bytes=%d", image.Bytes))
		}
		if image.Width > 0 && image.Height > 0 {
			fields = append(fields, fmt.Sprintf("size=%dx%d", image.Width, image.Height))
		}
		if image.Caption != "" {
			fields = append(fields, "caption="+quoteEpisodeField(image.Caption, 180))
		}
		if image.Summary != "" {
			fields = append(fields, "summary="+quoteEpisodeField(image.Summary, 260))
		}
		lines = append(lines, "- "+strings.Join(fields, " "))
	}
	return strings.Join(lines, "\n")
}

func formatContextToolResults(calls []app.ToolCall) string {
	return formatContextToolResultsWithLimit(calls, contextToolSummaryLimit)
}

func formatContextToolResultsWithLimit(calls []app.ToolCall, summaryLimit int) string {
	if summaryLimit <= 0 {
		summaryLimit = contextToolSummaryLimit
	}
	lines := make([]string, 0, len(calls))
	for _, call := range calls {
		summary := compactToolCallObservationForContext(call)
		if summary == "" {
			continue
		}
		fields := []string{
			"tool_call_id=" + quoteEpisodeField(call.ID, 80),
			"tool=" + quoteEpisodeField(call.Tool, 80),
			"status=" + quoteEpisodeField(call.Status, 60),
			"summary=" + quoteEpisodeField(summary, summaryLimit),
		}
		lines = append(lines, "- "+strings.Join(fields, " "))
	}
	return strings.Join(lines, "\n")
}

func compactObservationSummaryForContext(summary string) string {
	return compactToolCallObservationForContext(app.ToolCall{ObservationSummary: summary})
}

func compactToolCallObservationForContext(call app.ToolCall) string {
	summary := call.ObservationSummary
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return ""
	}
	var decoded toolResultMessage
	if err := json.Unmarshal([]byte(summary), &decoded); err != nil {
		if modelProjectionRestrictedTool(call) {
			return compactRestrictedLegacyToolSummary(call)
		}
		return strings.Join(strings.Fields(summary), " ")
	}
	if call.Tool == "" {
		call.Tool = decoded.Tool
	}
	if call.Status == "" {
		call.Status = decoded.Status
	}
	legacyProjection := persistedToolMessageHasRuntimeFields(call, decoded.Structured)
	decoded.Summary = persistedModelVisibleToolSummary(call, decoded.Summary)
	decoded.Structured = projectModelVisibleToolStructuredFields(call, decoded.Structured)
	if legacyProjection {
		decoded.Evidence = projectLegacyToolEvidenceForContext(call, decoded.Evidence)
	}
	return compactToolResultMessageForContext(decoded)
}

func persistedModelVisibleToolSummary(call app.ToolCall, fallback string) string {
	if toolCallCompleted(call) {
		return modelVisibleToolSummary(call, nil, fallback)
	}
	if modelProjectionRestrictedTool(call) {
		status := strings.TrimSpace(call.Status)
		if status == "" {
			status = "unavailable"
		}
		return call.Tool + " " + status
	}
	return fallback
}

func compactToolResultMessageForContext(decoded toolResultMessage) string {
	parts := []string{"tool=" + decoded.Tool, "status=" + decoded.Status, "compacted=true"}
	if decoded.Category != "" {
		parts = append(parts, "category="+decoded.Category)
	}
	if decoded.Summary != "" {
		parts = append(parts, "summary="+strings.Join(strings.Fields(decoded.Summary), " "))
	}
	if len(decoded.Evidence) > 0 {
		if evidence := compactPreferredToolEvidence(decoded.Evidence); evidence != "" {
			parts = append(parts, evidence)
		}
	}
	if len(decoded.Structured) > 0 {
		keys := []string{"artifact_uri", "path", "output_path", "url", "final_url", "title", "query", "count", "status_code", "truncated"}
		structured := []string{}
		for _, key := range keys {
			if value, ok := decoded.Structured[key]; ok && usefulStructuredValue(value) {
				structured = append(structured, key+"="+strings.Join(strings.Fields(stringValue(value)), " "))
			}
		}
		if len(structured) > 0 {
			parts = append(parts, "structured={"+strings.Join(structured, "; ")+"}")
		}
		if source := compactDocumentSourceForContext(decoded.Structured["source"]); source != "" {
			parts = append(parts, "source={"+source+"}")
		}
		if message := compactToolMessageForContext(decoded.Structured["message"]); message != "" {
			parts = append(parts, "tool_message={"+message+"}")
		}
		if policy := compactEvidencePolicyForContext(decoded.Structured["evidence_policy"]); policy != "" {
			parts = append(parts, "evidence_policy={"+policy+"}")
		}
		if pipeline := compactDocumentPipelineForContext(decoded.Structured["document_pipeline"]); pipeline != "" {
			parts = append(parts, "document_pipeline={"+pipeline+"}")
		}
	}
	return strings.Join(parts, " ")
}

func modelProjectionRestrictedTool(call app.ToolCall) bool {
	return call.Tool == "files.read" || call.Capability == app.ToolCapabilityDocumentRead ||
		toolResultCategoryForCall(call) == "document_mutation" || strings.HasPrefix(call.Tool, "browser.")
}

func compactRestrictedLegacyToolSummary(call app.ToolCall) string {
	status := strings.TrimSpace(call.Status)
	if status == "" {
		status = "persisted"
	}
	return strings.Join([]string{
		"tool=" + call.Tool,
		"status=" + status,
		"compacted=true",
		"summary=legacy result retained in Runtime; model projection unavailable",
	}, " ")
}

func persistedToolMessageHasRuntimeFields(call app.ToolCall, structured map[string]any) bool {
	if len(structured) == 0 || !modelProjectionRestrictedTool(call) {
		return false
	}
	for _, key := range []string{
		"path", "rel_path", "output_path", "url", "final_url", "page_id", "snapshot_id",
		"previous_snapshot_id", "digest", "operation", "source_sha256", "source_document_sha256",
	} {
		if usefulStructuredValue(structured[key]) {
			return true
		}
	}
	if source, ok := anyMap(structured["source"]); ok {
		for _, key := range []string{"path", "rel_path", "kind", "bytes", "max_bytes", "content_type", "source_sha256"} {
			if usefulStructuredValue(source[key]) {
				return true
			}
		}
	}
	if pipeline, ok := anyMap(structured["document_pipeline"]); ok {
		return usefulStructuredValue(pipeline["document_id"]) || usefulStructuredValue(pipeline["strategy"]) || usefulStructuredValue(pipeline["index"])
	}
	return false
}

func projectLegacyToolEvidenceForContext(call app.ToolCall, evidence []toolEvidence) []toolEvidence {
	if call.Tool == "files.read" || call.Capability == app.ToolCapabilityDocumentRead {
		allowed := map[string]bool{
			"content_full": true, "content_excerpt": true, "document.paragraphs": true,
			"document.tables": true, "document.pages": true,
		}
		projected := make([]toolEvidence, 0, len(evidence))
		for _, item := range evidence {
			if allowed[item.Kind] {
				projected = append(projected, item)
			}
		}
		return projected
	}
	if strings.HasPrefix(call.Tool, "browser.") {
		projected := make([]toolEvidence, 0, len(evidence))
		for _, item := range evidence {
			if item.Kind != "browser.interaction_snapshot" {
				continue
			}
			var snapshot map[string]any
			if json.Unmarshal([]byte(item.Text), &snapshot) != nil {
				continue
			}
			raw, err := json.Marshal(browserInteractionSnapshotProjection(snapshot))
			if err == nil {
				item.Text = string(raw)
				projected = append(projected, item)
			}
		}
		return projected
	}
	return nil
}

func compactPreferredToolEvidence(evidence []toolEvidence) string {
	if len(evidence) == 0 {
		return ""
	}
	preferred := []struct {
		kind  string
		limit int
	}{
		{"document.operation_context", 1400},
		{"document.anchors", 520},
		{"document.paragraphs", 520},
		{"content_full", 360},
		{"content_excerpt", 360},
	}
	parts := []string{}
	used := map[int]bool{}
	for _, pref := range preferred {
		for i, item := range evidence {
			if used[i] || item.Kind != pref.kind {
				continue
			}
			text := strings.Join(strings.Fields(item.Text), " ")
			if text != "" {
				if item.Kind == "document.operation_context" {
					text = compactDocumentOperationContextText(text, pref.limit)
				} else {
					text = trimForEpisode(text, pref.limit)
				}
				parts = append(parts, "evidence="+item.Kind+":"+text)
				used[i] = true
			}
			break
		}
	}
	if len(parts) == 0 {
		text := strings.Join(strings.Fields(evidence[0].Text), " ")
		if text != "" {
			parts = append(parts, "evidence="+evidence[0].Kind+":"+trimForEpisode(text, 360))
		}
	}
	return strings.Join(parts, " ")
}

func compactDocumentSourceForContext(value any) string {
	source, ok := anyMap(value)
	if !ok || len(source) == 0 {
		return ""
	}
	fields := []string{}
	for _, key := range []string{"truncated", "read_complete", "coverage_status", "missing_page_indexes", "page_status_counts"} {
		if value, ok := source[key]; ok && usefulStructuredValue(value) {
			fields = append(fields, key+"="+strings.Join(strings.Fields(stringValue(value)), " "))
		}
	}
	return trimForEpisode(strings.Join(fields, "; "), 360)
}

func compactToolMessageForContext(value any) string {
	message, ok := anyMap(value)
	if !ok || len(message) == 0 {
		return ""
	}
	fields := []string{}
	for _, key := range []string{"truncated", "compacted"} {
		if value, ok := message[key]; ok && usefulStructuredValue(value) {
			fields = append(fields, key+"="+strings.Join(strings.Fields(stringValue(value)), " "))
		}
	}
	if note := strings.TrimSpace(stringValue(message["note"])); note != "" && note != "<nil>" {
		fields = append(fields, "note="+trimForEpisode(strings.Join(strings.Fields(note), " "), 180))
	}
	return trimForEpisode(strings.Join(fields, "; "), 360)
}

func compactEvidencePolicyForContext(value any) string {
	policy, ok := anyMap(value)
	if !ok || len(policy) == 0 {
		return ""
	}
	fields := []string{}
	for _, key := range []string{"content_is_excerpt", "excerpt_does_not_change_source_coverage"} {
		if value, ok := policy[key]; ok && usefulStructuredValue(value) {
			fields = append(fields, key+"="+strings.Join(strings.Fields(stringValue(value)), " "))
		}
	}
	return trimForEpisode(strings.Join(fields, "; "), 240)
}

func compactDocumentPipelineForContext(value any) string {
	pipeline, ok := anyMap(value)
	if !ok || len(pipeline) == 0 {
		return ""
	}
	fields := []string{}
	for _, key := range []string{"status"} {
		if value, ok := pipeline[key]; ok && usefulStructuredValue(value) {
			fields = append(fields, key+"="+strings.Join(strings.Fields(stringValue(value)), " "))
		}
	}
	if profile, ok := anyMap(pipeline["profile"]); ok {
		for _, key := range []string{"token_estimate", "language", "has_tables", "complexity"} {
			if value, ok := profile[key]; ok && usefulStructuredValue(value) {
				fields = append(fields, "profile."+key+"="+strings.Join(strings.Fields(stringValue(value)), " "))
			}
		}
	}
	return trimForEpisode(strings.Join(fields, "; "), 500)
}

func compactDocumentOperationContextText(text string, limit int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if text == "" || limit <= 0 || len([]rune(text)) <= limit {
		return text
	}
	withoutBody := replaceQuotedFieldValue(text, "body_old_text_excerpt=", "[content omitted]")
	withoutBody = replaceQuotedFieldValue(withoutBody, "heading_old_text_excerpt=", "[content omitted]")
	if len([]rune(withoutBody)) <= limit {
		return withoutBody
	}
	withoutHashDupes := strings.ReplaceAll(withoutBody, " body_sourceHash=", " body_sourceHash_legacy=")
	withoutHashDupes = strings.ReplaceAll(withoutHashDupes, " heading_sourceHash=", " heading_sourceHash_legacy=")
	if len([]rune(withoutHashDupes)) <= limit {
		return withoutHashDupes
	}
	return trimForEpisode(withoutHashDupes, limit)
}

func replaceQuotedFieldValue(text, field, replacement string) string {
	if text == "" || field == "" {
		return text
	}
	var out strings.Builder
	start := 0
	for {
		idx := strings.Index(text[start:], field+"\"")
		if idx < 0 {
			out.WriteString(text[start:])
			return out.String()
		}
		idx += start
		out.WriteString(text[start : idx+len(field)])
		out.WriteString(quoteEpisodeField(replacement, 120))
		pos := idx + len(field) + 1
		escaped := false
		for pos < len(text) {
			ch := text[pos]
			if ch == '\\' && !escaped {
				escaped = true
				pos++
				continue
			}
			if ch == '"' && !escaped {
				pos++
				break
			}
			escaped = false
			pos++
		}
		start = pos
	}
}

func isDocumentContextTool(tool string) bool {
	return strings.HasPrefix(tool, "docx.") ||
		strings.HasPrefix(tool, "pptx.") ||
		strings.HasPrefix(tool, "xlsx.") ||
		strings.HasPrefix(tool, "pdf.") ||
		strings.HasPrefix(tool, "office.")
}
