package agent

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// Fallback strategy identifiers recorded in fallback.policy_applied audit events.
// Each grounded summary builder is bound to its strategy in groundedSummaryWithStrategy,
// so the audit no longer sniffs user-facing display strings.
const (
	strategyGroundedResult     = "grounded_result"
	strategyDocumentMutation   = "document_mutation_result"
	strategyWeatherFailure     = "weather_failure"
	strategyWeatherCard        = "weather_card_result"
	strategyExplicitFailure    = "explicit_failure"
	strategyCodeDiagnostics    = "code_diagnostics_result"
	strategyFileSearch         = "file_search_result"
	strategyFileReadNoFinal    = "files.read_no_final"
	strategyBrowserReadNoFinal = "browser.read_no_final"
	strategySandboxShell       = "sandbox_shell_result"
	strategyWorkspacePatch     = "workspace_patch_result"
)

func (r Runtime) applyGroundedSummary(sessionID, runID, goal, fallback string, calls []app.ToolCall) string {
	summary, strategy := groundedSummaryWithStrategy(goal, fallback, calls)
	if summary != fallback && fallbackPolicyEligible(fallback) {
		r.store.AddAudit(app.AuditEvent{
			SessionID: sessionID,
			RunID:     runID,
			Actor:     "runtime",
			Type:      "fallback.policy_applied",
			Summary:   "Applied grounded fallback policy after missing or unusable final answer",
			Fields: map[string]any{
				"strategy":         strategy,
				"had_final":        strings.TrimSpace(fallback) != "",
				"fallback_blocked": isBlockedFinalAnswer(fallback),
				"tools":            toolNamesForAudit(calls),
			},
		})
	}
	return summary
}

func fallbackPolicyEligible(fallback string) bool {
	cleaned := cleanUserFinalAnswer(fallback)
	return cleaned == "" || isBlockedFinalAnswer(cleaned)
}

func toolNamesForAudit(calls []app.ToolCall) []string {
	names := []string{}
	for _, call := range calls {
		if strings.TrimSpace(call.Tool) != "" {
			names = append(names, call.Tool)
		}
	}
	return uniqueNonEmpty(names)
}

func fallbackToolCandidatesForAudit(hint TaskHint) []string {
	if strictCandidateToolsForHint(hint) {
		return []string{}
	}
	return fallbackToolsForHint(hint)
}

func groundedSummary(goal, fallback string, calls []app.ToolCall) string {
	summary, _ := groundedSummaryWithStrategy(goal, fallback, calls)
	return summary
}

func groundedSummaryWithStrategy(goal, fallback string, calls []app.ToolCall) (string, string) {
	if cleaned := cleanUserFinalAnswer(fallback); cleaned != "" && !isBlockedFinalAnswer(cleaned) {
		return cleaned, strategyGroundedResult
	}
	if grounded, ok := groundedDocumentMutationSummary(calls); ok {
		return grounded, strategyDocumentMutation
	}
	if grounded, ok := groundedWeatherFailureSummary(calls); ok {
		return grounded, strategyWeatherFailure
	}
	if grounded, ok := groundedFailureSummary(goal, calls); ok {
		return grounded, strategyExplicitFailure
	}
	if grounded, ok := groundedDocumentPendingApprovalSummary(calls); ok {
		return grounded, strategyGroundedResult
	}
	if grounded, ok := groundedWeatherCardSummary(calls); ok {
		return grounded, strategyWeatherCard
	}
	if grounded, ok := groundedBrowserAutomationSummary(goal, fallback, calls); ok {
		return grounded, strategyGroundedResult
	}
	if grounded, ok := groundedCodeDiagnosticsSummary(goal, fallback, calls); ok {
		return grounded, strategyCodeDiagnostics
	}
	if grounded, ok := groundedKnowledgeSummary(goal, fallback, calls); ok {
		return grounded, strategyGroundedResult
	}
	if grounded, strategy, ok := groundedFileReadSummary(goal, fallback, calls); ok {
		return grounded, strategy
	}
	if grounded, ok := groundedFileSearchSummary(goal, fallback, calls); ok {
		return grounded, strategyFileSearch
	}
	if grounded, strategy, ok := groundedBrowserReadSummary(goal, fallback, calls); ok {
		return grounded, strategy
	}
	if grounded, ok := groundedImageInspectSummary(goal, fallback, calls); ok {
		return grounded, strategyGroundedResult
	}
	if grounded, ok := groundedWebSearchSummary(goal, fallback, calls); ok {
		return grounded, strategyGroundedResult
	}
	if grounded, ok := groundedEmailSummary(goal, fallback, calls); ok {
		return grounded, strategyGroundedResult
	}
	if grounded, ok := groundedCalendarReadSummary(goal, fallback, calls); ok {
		return grounded, strategyGroundedResult
	}
	if grounded, ok := groundedShellSummary(goal, fallback, calls); ok {
		return grounded, strategySandboxShell
	}
	if grounded, ok := groundedPatchSummary(goal, fallback, calls); ok {
		return grounded, strategyWorkspacePatch
	}
	return fallback, strategyGroundedResult
}

func groundedWeatherCardSummary(calls []app.ToolCall) (string, bool) {
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		if call.Tool != "media.render_weather_card" || !toolCallCompleted(call) {
			continue
		}
		result, ok := anyMap(call.Result)
		if !ok {
			continue
		}
		mediaPath := strings.TrimSpace(stringValue(result["media_path"]))
		if mediaPath == "" {
			mediaPath = strings.TrimSpace(stringValue(result["uri"]))
			mediaPath = strings.TrimPrefix(mediaPath, "workspace://")
		}
		if mediaPath == "" || !strings.HasPrefix(filepath.ToSlash(mediaPath), "media/") {
			continue
		}
		return "![天气卡片](" + filepath.ToSlash(mediaPath) + ")", true
	}
	return "", false
}

func groundedWeatherFailureSummary(calls []app.ToolCall) (string, bool) {
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		if call.Tool != "media.render_weather_card" {
			continue
		}
		if toolCallCompleted(call) {
			return "", false
		}
		if !strings.Contains(call.Status, "failed") && call.Error == "" {
			return "", false
		}
		reason := strings.TrimSpace(call.Error)
		if reason == "" {
			reason = "Open-Meteo weather lookup failed"
		}
		return "天气查询失败：" + reason, true
	}
	return "", false
}

func groundedImageInspectSummary(goal, fallback string, calls []app.ToolCall) (string, bool) {
	if !imageInspectCanFinalize(goal) {
		return "", false
	}
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		if call.Tool != "images.inspect" || !toolCallCompleted(call) {
			continue
		}
		result, ok := anyMap(call.Result)
		if !ok {
			continue
		}
		summary := strings.TrimSpace(stringValue(result["summary"]))
		if summary == "" || summary == "<nil>" {
			continue
		}
		return strings.TrimSpace(summary), true
	}
	return "", false
}

func imageInspectCanFinalize(goal string) bool {
	lower := strings.ToLower(goal)
	if isActionOrModificationGoal(goal) {
		return false
	}
	if containsAny(lower,
		"查证", "验证", "核实", "真假", "是否真实", "是真的吗", "真的假的", "可靠", "来源", "出处", "官方",
		"最新", "今天", "昨天", "昨日", "联网", "上网", "搜索", "查一下", "网页", "新闻",
		"compare", "comparison", "versus", " vs ", "比较", "对比",
	) {
		return false
	}
	return true
}

func groundedFailureSummary(goal string, calls []app.ToolCall) (string, bool) {
	failed := failedToolCalls(calls)
	if len(failed) == 0 {
		return "", false
	}
	if !isActionOrModificationGoal(goal) && !hasNonReadFailedTool(failed) {
		return "", false
	}
	last := failed[len(failed)-1]
	lines := []string{"任务没有完成。"}
	if last.Tool != "" {
		lines = append(lines, "失败工具："+last.Tool)
	}
	if last.Error != "" {
		lines = append(lines, "原因："+last.Error)
	}
	if hint := failureNextStepHint(goal, last); hint != "" {
		lines = append(lines, "建议："+hint)
	}
	return strings.Join(lines, "\n"), true
}

func groundedDocumentPendingApprovalSummary(calls []app.ToolCall) (string, bool) {
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		if call.Status == "approval_pending" && isDocumentMutationTool(call.Tool) {
			return pendingApprovalAnswer(call), true
		}
	}
	return "", false
}

func groundedDocumentMutationSummary(calls []app.ToolCall) (string, bool) {
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		if !toolCallCompleted(call) || !isDocumentMutationTool(call.Tool) {
			continue
		}
		result, ok := anyMap(call.Result)
		if !ok {
			continue
		}
		if outputPath := cleanOptionalString(result["output_path"]); outputPath != "" {
			return "修改好的文件：" + outputPath, true
		}
	}
	return "", false
}

func failedToolCalls(calls []app.ToolCall) []app.ToolCall {
	out := []app.ToolCall{}
	laterCompleted := map[string]bool{}
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		if toolCallCompleted(call) {
			laterCompleted[call.Tool] = true
			continue
		}
		if strings.Contains(call.Status, "failed") || call.Status == "blocked" || call.Status == "rejected" {
			if laterCompleted[call.Tool] {
				continue
			}
			out = append(out, call)
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func hasNonReadFailedTool(calls []app.ToolCall) bool {
	for _, call := range calls {
		if !isReadOnlyEvidenceTool(call.Tool) {
			return true
		}
	}
	return false
}

func isReadOnlyEvidenceTool(tool string) bool {
	switch tool {
	case "files.read", "files.search", "browser.read", "web.search", "knowledge.search", "memory.search", "pdf.extract_text":
		return true
	default:
		return false
	}
}

func isDocumentMutationTool(tool string) bool {
	if strings.HasPrefix(tool, "docx.") || strings.HasPrefix(tool, "pptx.") || strings.HasPrefix(tool, "xlsx.") {
		return true
	}
	switch tool {
	case "office.replace_text", "pdf.transform":
		return true
	default:
		return false
	}
}

func isTerminalApprovedActionTool(tool string) bool {
	return isDocumentMutationTool(tool)
}

func hasMutatingOrPendingNonReadTool(calls []app.ToolCall) bool {
	for _, call := range calls {
		if call.Status == "approval_pending" || isDocumentMutationTool(call.Tool) {
			return true
		}
		if !isReadOnlyEvidenceTool(call.Tool) && call.Tool != "" {
			return true
		}
	}
	return false
}

func failureNextStepHint(goal string, call app.ToolCall) string {
	lower := strings.ToLower(goal)
	if call.Tool == "office.replace_text" && strings.Contains(strings.ToLower(call.Error), "file not found") {
		return "请使用上传后显示的完整 workspace 路径，或先让 SparkClaw 搜索文件并确认目标路径后再修改。"
	}
	if call.Tool == "office.replace_text" && strings.Contains(strings.ToLower(call.Error), "not matched") {
		return "请先读取文档确认原文内容，再给出明确的 find -> replace 文本。"
	}
	if containsAny(lower, "行", "row", "删除", "delete") && strings.HasPrefix(call.Tool, "office.") {
		return "当前 Office 修改工具主要支持明确文本替换，表格整行删除需要后续补充结构化 xlsx 行操作工具。"
	}
	return ""
}

func groundedBrowserAutomationSummary(goal, fallback string, calls []app.ToolCall) (string, bool) {
	if tabs, ok := browserTabsAnswerFromCalls(calls); ok {
		return tabs, true
	}
	if screenshot, ok := browserScreenshotAnswerFromCalls(goal, fallback, calls); ok {
		return screenshot, true
	}
	return "", false
}

func browserTabsAnswerFromCalls(calls []app.ToolCall) (string, bool) {
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		if call.Tool != "browser.list_tabs" || !toolCallCompleted(call) {
			continue
		}
		result, ok := anyMap(call.Result)
		if !ok {
			continue
		}
		pages := anySlice(result["pages"])
		if pages == nil {
			output, ok := anyMap(result["output"])
			if ok {
				pages = anySlice(output["pages"])
			}
		}
		if len(pages) == 0 {
			return "当前没有打开任何浏览器界面。", true
		}
		lines := []string{"当前打开的浏览器界面："}
		for index, page := range pages {
			item, ok := anyMap(page)
			if !ok {
				lines = append(lines, fmt.Sprintf("%d. %s", index+1, stringValue(page)))
				continue
			}
			title := strings.TrimSpace(stringValue(firstPresent(item, "title", "name")))
			url := strings.TrimSpace(stringValue(firstPresent(item, "url")))
			id := strings.TrimSpace(stringValue(firstPresent(item, "page_id", "targetId", "target_id", "id")))
			line := fmt.Sprintf("%d.", index+1)
			if title != "" && title != "<nil>" {
				line += " " + title
			}
			if url != "" && url != "<nil>" {
				line += " " + url
			}
			if id != "" && id != "<nil>" {
				line += " (" + id + ")"
			}
			lines = append(lines, strings.TrimSpace(line))
		}
		return strings.Join(lines, "\n"), true
	}
	return "", false
}

func browserScreenshotAnswerFromCalls(goal, fallback string, calls []app.ToolCall) (string, bool) {
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		if call.Tool != "browser.screenshot" || !toolCallCompleted(call) {
			continue
		}
		result, ok := anyMap(call.Result)
		if !ok {
			continue
		}
		path := strings.TrimSpace(stringValue(result["screenshot_path"]))
		markdown := strings.TrimSpace(stringValue(result["screenshot_markdown"]))
		output, ok := anyMap(result["output"])
		if !ok {
			output = result
		}
		if path == "" {
			path = strings.TrimSpace(stringValue(output["screenshot_path"]))
		}
		if markdown == "" {
			markdown = strings.TrimSpace(stringValue(output["screenshot_markdown"]))
		}
		if path == "<nil>" {
			path = ""
		}
		if markdown == "<nil>" {
			markdown = ""
		}
		if markdown == "" && path != "" {
			markdown = "![browser screenshot](" + path + ")"
		}
		if markdown == "" && path == "" {
			if errText := strings.TrimSpace(stringValue(output["text"])); strings.Contains(strings.ToLower(errText), "error") || strings.Contains(strings.ToLower(errText), "failed") || strings.Contains(strings.ToLower(errText), "unknown argument") {
				return "截图未完成：" + errText, true
			}
			if errText := strings.TrimSpace(stringValue(output["screenshot_save_error"])); errText != "" && errText != "<nil>" {
				return "截图已调用，但保存失败：" + errText, true
			}
			continue
		}
		lines := []string{"已完成截图。"}
		if markdown != "" {
			lines = append(lines, "", markdown)
		}
		if path != "" {
			lines = append(lines, "", "截图已保存到："+path)
		}
		return strings.Join(lines, "\n"), true
	}
	if asksForBrowserScreenshot(goal) {
		for i := len(calls) - 1; i >= 0; i-- {
			call := calls[i]
			if strings.HasPrefix(call.Tool, "browser.") && call.Status == "failed" && call.Error != "" {
				return "未能完成截图。浏览器自动化在 `" + call.Tool + "` 失败：" + call.Error, true
			}
		}
	}
	return "", false
}

func asksForBrowserScreenshot(goal string) bool {
	return containsAny(strings.ToLower(goal), "截图", "截屏", "screenshot", "screen shot", "capture screen")
}

func isBrowserAutomationPlan(name string) bool {
	switch name {
	case "browser.status", "browser.list_tabs", "browser.open", "browser.focus", "browser.close", "browser.navigate", "browser.snapshot", "browser.screenshot", "browser.wait", "browser.click", "browser.type", "browser.select":
		return true
	default:
		return false
	}
}

func groundedKnowledgeSummary(goal, fallback string, calls []app.ToolCall) (string, bool) {
	answer, ok := knowledgeAnswerFromCalls(goal, calls)
	if !ok {
		return "", false
	}
	lines := []string{
		"Answer from local knowledge:",
		answer,
	}
	return strings.Join(lines, "\n"), true
}

func groundedFileReadSummary(goal, fallback string, calls []app.ToolCall) (string, string, bool) {
	if !hasCompletedFileRead(calls) {
		return "", "", false
	}
	if cleaned := cleanUserFinalAnswer(fallback); cleaned != "" && shouldPreferModelFinal(goal, cleaned) {
		return cleaned, strategyGroundedResult, true
	}
	return fileReadFallbackFailure(calls), strategyFileReadNoFinal, true
}

func hasCompletedFileRead(calls []app.ToolCall) bool {
	for _, call := range calls {
		if call.Tool == "files.read" && toolCallCompleted(call) {
			return true
		}
	}
	return false
}

func fileReadFallbackFailure(calls []app.ToolCall) string {
	paths := []string{}
	truncated := false
	for _, call := range calls {
		if call.Tool != "files.read" || !toolCallCompleted(call) {
			continue
		}
		result, _ := anyMap(call.Result)
		path := cleanOptionalString(firstPresent(result, "rel_path", "path"))
		if path == "" {
			path = cleanOptionalString(call.Arguments["path"])
		}
		if path != "" {
			paths = append(paths, filepath.ToSlash(path))
		}
		if boolLikeValue(result["truncated"]) {
			truncated = true
		}
	}
	lines := []string{
		"任务没有完成。",
		"兜底策略：files.read_no_final",
		"原因：文件读取已完成，但系统没有生成用户请求的最终回答；不会用原文片段伪装成摘要或答案。",
	}
	if len(paths) > 0 {
		lines = append(lines, "已读取文件："+strings.Join(uniqueNonEmpty(paths), ", "))
	}
	if truncated {
		lines = append(lines, "读取状态：内容被 max_bytes 截断，需要提高 max_bytes 或使用更精确的读取工具后再回答。")
	}
	lines = append(lines, "建议：请重试；如果持续出现，请检查模型 final 生成链路或文档解析链路。")
	return strings.Join(lines, "\n")
}

func hasCompletedBrowserRead(calls []app.ToolCall) bool {
	for _, call := range calls {
		if call.Tool == "browser.read" && toolCallCompleted(call) {
			return true
		}
	}
	return false
}

func browserReadFallbackFailure(calls []app.ToolCall) string {
	sources := []string{}
	truncated := false
	for _, call := range calls {
		if call.Tool != "browser.read" || !toolCallCompleted(call) {
			continue
		}
		result, _ := anyMap(call.Result)
		if url := cleanOptionalString(result["url"]); url != "" {
			sources = append(sources, url)
		}
		if boolLikeValue(result["truncated"]) {
			truncated = true
		}
	}
	lines := []string{
		"任务没有完成。",
		"兜底策略：browser.read_no_final",
		"原因：网页读取已完成，但系统没有生成用户请求的最终回答；不会用页面片段伪装成摘要、查证或结论。",
	}
	if len(sources) > 0 {
		lines = append(lines, "已读取来源："+strings.Join(uniqueNonEmpty(sources), ", "))
	}
	if truncated {
		lines = append(lines, "读取状态：页面内容被截断，需要缩小范围或继续读取。")
	}
	lines = append(lines, "建议：请重试；如果持续出现，请检查模型 final 生成链路或浏览器读取链路。")
	return strings.Join(lines, "\n")
}

func isActionOrModificationGoal(goal string) bool {
	lower := strings.ToLower(goal)
	return containsAny(lower,
		"modify", "edit", "replace", "delete", "remove", "write", "create", "update", "change",
		"修改", "编辑", "替换", "删除", "删掉", "移除", "写入", "生成", "创建", "改成", "换成",
	)
}

func groundedFileSearchSummary(goal, fallback string, calls []app.ToolCall) (string, bool) {
	answer, ok := fileSearchAnswerFromCalls(goal, calls)
	if !ok {
		return "", false
	}
	lines := []string{
		"File search results:",
		answer,
	}
	return strings.Join(lines, "\n"), true
}

func groundedBrowserReadSummary(goal, fallback string, calls []app.ToolCall) (string, string, bool) {
	if !hasCompletedBrowserRead(calls) {
		return "", "", false
	}
	if cleaned := cleanUserFinalAnswer(fallback); cleaned != "" && shouldPreferModelFinal(goal, cleaned) {
		return cleaned, strategyGroundedResult, true
	}
	return browserReadFallbackFailure(calls), strategyBrowserReadNoFinal, true
}

func groundedWebSearchSummary(goal, fallback string, calls []app.ToolCall) (string, bool) {
	answer, ok := webSearchAnswerFromCalls(goal, calls)
	if !ok {
		return "", false
	}
	if cleaned := cleanUserFinalAnswer(fallback); cleaned != "" && shouldPreferModelFinal(goal, cleaned) {
		return cleaned, true
	}
	return answer, true
}

func groundedCodeDiagnosticsSummary(goal, fallback string, calls []app.ToolCall) (string, bool) {
	answer, ok := codeDiagnosticsAnswerFromCalls(goal, calls)
	if !ok {
		return "", false
	}
	lines := []string{
		"Code diagnostics:",
		answer,
	}
	return strings.Join(lines, "\n"), true
}

func groundedEmailSummary(goal, fallback string, calls []app.ToolCall) (string, bool) {
	answer, ok := emailAnswerFromCalls(goal, calls)
	if !ok {
		return "", false
	}
	heading := "Answer from email data:"
	if containsAny(goal, "search", "find", "查", "找") {
		heading = "Email search results:"
	}
	lines := []string{
		heading,
		answer,
	}
	return strings.Join(lines, "\n"), true
}

func groundedCalendarReadSummary(goal, fallback string, calls []app.ToolCall) (string, bool) {
	answer, ok := calendarAnswerFromCalls(goal, calls)
	if !ok {
		return "", false
	}
	lines := []string{
		"Calendar results:",
		answer,
	}
	return strings.Join(lines, "\n"), true
}

func groundedShellSummary(goal, fallback string, calls []app.ToolCall) (string, bool) {
	answer, ok := shellAnswerFromCalls(goal, calls)
	if !ok {
		return "", false
	}
	lines := []string{
		"Sandboxed shell result:",
		answer,
	}
	return strings.Join(lines, "\n"), true
}

func groundedPatchSummary(goal, fallback string, calls []app.ToolCall) (string, bool) {
	answer, ok := patchAnswerFromCalls(goal, calls)
	if !ok {
		return "", false
	}
	lines := []string{
		"Workspace patch result:",
		answer,
	}
	return strings.Join(lines, "\n"), true
}

func knowledgeAnswerFromCalls(goal string, calls []app.ToolCall) (string, bool) {
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		if call.Tool != "knowledge.search" || call.Status != "completed" {
			continue
		}
		result, ok := call.Result.(map[string]any)
		if !ok {
			continue
		}
		evidence := strings.TrimSpace(stringValue(result["evidence_context"]))
		if evidence == "" || evidence == "<nil>" {
			continue
		}
		citations := stringSliceValue(result["citations"])
		answer := "I found local evidence for " + quoteInline(searchQuery(goal)) + "."
		if rewritten := strings.TrimSpace(stringValue(result["rewritten_query"])); rewritten != "" && rewritten != "<nil>" {
			answer = "I searched for " + quoteInline(rewritten) + " and found local evidence."
		}
		if count := intLikeValue(result["count"]); count > 0 {
			answer += fmt.Sprintf(" Top %d cited result(s):", count)
		} else {
			answer += " Cited result(s):"
		}
		answer += "\n" + evidence
		if len(citations) > 0 {
			answer += "\nCitations: " + strings.Join(citations, ", ")
		}
		return answer, true
	}
	return "", false
}

func webSearchAnswerFromCalls(goal string, calls []app.ToolCall) (string, bool) {
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		if call.Tool != "web.search" || call.Status != "completed" {
			continue
		}
		result, ok := anyMap(call.Result)
		if !ok {
			continue
		}
		answer := strings.TrimSpace(stringValue(result["answer"]))
		count := intLikeValue(result["count"])
		if answer != "" && answer != "<nil>" {
			return answer, true
		}
		if count == 0 {
			return "没有找到可靠的联网搜索结果。", true
		}
		if citations := stringSliceValue(result["citations"]); len(citations) > 0 {
			return "找到了相关来源：" + strings.Join(citations, ", "), true
		}
	}
	return "", false
}

func cleanUserFinalAnswer(answer string) string {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return ""
	}
	if strings.HasPrefix(answer, "Answer from ") ||
		strings.Contains(answer, "\nObserved:") ||
		strings.Contains(answer, "\nModel note:") ||
		strings.Contains(answer, "I reviewed the observed evidence") ||
		strings.Contains(answer, "prepared the bounded answer") {
		return ""
	}
	return answer
}

func isBlockedFinalAnswer(answer string) bool {
	answer = strings.TrimSpace(answer)
	lower := strings.ToLower(answer)
	return strings.HasPrefix(answer, "I could not continue") ||
		strings.HasPrefix(answer, "Reached the ReAct step limit") ||
		strings.HasPrefix(answer, "任务没有完成") ||
		strings.HasPrefix(answer, "无法完成") ||
		strings.Contains(lower, "waiting for approval") ||
		strings.Contains(lower, "pending approval")
}

func shouldPreferModelFinal(goal, answer string) bool {
	return len([]rune(answer)) >= 12 && !isBlockedFinalAnswer(answer)
}

func shellAnswerFromCalls(goal string, calls []app.ToolCall) (string, bool) {
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		if call.Tool != "shell.exec_sandboxed" {
			continue
		}
		if call.Status == "approval_pending" {
			return pendingApprovalAnswer(call), true
		}
		if !strings.Contains(call.Status, "completed") && !strings.Contains(call.Status, "failed") {
			continue
		}
		result, ok := anyMap(call.Result)
		if !ok {
			if call.Error != "" {
				return "Command: " + quoteInline(stringValue(call.Arguments["command"])) + "\nStatus: " + call.Status + "\nError: " + call.Error, true
			}
			continue
		}
		lines := []string{
			"Command: " + quoteInline(stringValue(call.Arguments["command"])),
			"Tool status: " + call.Status,
		}
		if status := cleanOptionalString(result["status"]); status != "" {
			lines = append(lines, "Sandbox status: "+status)
		}
		if backend := cleanOptionalString(result["backend"]); backend != "" {
			lines = append(lines, "Backend: "+backend)
		}
		if network := cleanOptionalString(result["network"]); network != "" {
			lines = append(lines, "Network: "+network)
		}
		if call.Error != "" {
			lines = append(lines, "Error: "+call.Error)
		}
		if stdout := cleanOptionalString(result["stdout"]); stdout != "" {
			lines = append(lines, "", "Stdout:", shellOutputLines(stdout, 8, 1200))
		}
		if stderr := cleanOptionalString(result["stderr"]); stderr != "" {
			lines = append(lines, "", "Stderr:", shellOutputLines(stderr, 6, 900))
		}
		if ref := cleanOptionalString(call.ObservationRef); ref != "" {
			lines = append(lines, "Observation: "+ref)
		}
		return strings.Join(lines, "\n"), true
	}
	return "", false
}

func codeDiagnosticsAnswerFromCalls(goal string, calls []app.ToolCall) (string, bool) {
	if !asksForCodeDiagnostics(goal) {
		return "", false
	}
	fileAnswer, hasFiles := fileSearchAnswerFromCalls(goal, calls)
	shellAnswer, hasShell := shellAnswerFromCalls(goal, calls)
	if !hasFiles || !hasShell {
		return "", false
	}
	lines := []string{
		"Repository evidence:",
		indentBlock(fileAnswer),
		"",
		"Test execution status:",
		indentBlock(shellAnswer),
	}
	if pendingShellCall(calls) != nil {
		lines = append(lines, "", "Next step: approve the sandboxed test run to collect stdout/stderr before diagnosing the failure cause.")
	} else {
		lines = append(lines, "", "Next step: use the sandbox stdout/stderr above as evidence for the failure explanation.")
	}
	return strings.Join(lines, "\n"), true
}

func asksForCodeDiagnostics(goal string) bool {
	return isCodeInspectionTask(strings.ToLower(goal)) && isTerminalTask(strings.ToLower(goal))
}

func pendingShellCall(calls []app.ToolCall) *app.ToolCall {
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].Tool == "shell.exec_sandboxed" && calls[i].Status == "approval_pending" {
			return &calls[i]
		}
	}
	return nil
}

func indentBlock(value string) string {
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines[i] = "  " + line
	}
	return strings.Join(lines, "\n")
}

func patchAnswerFromCalls(goal string, calls []app.ToolCall) (string, bool) {
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		if call.Tool != "code.apply_patch" {
			continue
		}
		if call.Status == "approval_pending" {
			return pendingApprovalAnswer(call), true
		}
		if !strings.Contains(call.Status, "completed") && !strings.Contains(call.Status, "failed") {
			continue
		}
		result, ok := anyMap(call.Result)
		if !ok {
			if call.Error != "" {
				return "Tool status: " + call.Status + "\nError: " + call.Error, true
			}
			continue
		}
		lines := []string{
			"Tool status: " + call.Status,
		}
		if status := cleanOptionalString(result["status"]); status != "" {
			lines = append(lines, "Patch status: "+status)
		}
		if patchID := cleanOptionalString(result["patch_id"]); patchID != "" {
			lines = append(lines, "Patch ID: "+patchID)
		}
		if changed := stringSliceValue(result["changed_files"]); len(changed) > 0 {
			lines = append(lines, "Changed files:")
			for _, path := range changed {
				lines = append(lines, "- "+path)
			}
		}
		if manifest := cleanOptionalString(result["manifest_path"]); manifest != "" {
			lines = append(lines, "Rollback manifest: "+manifest)
		}
		if rollback := cleanOptionalString(result["rollback_patch_path"]); rollback != "" {
			lines = append(lines, "Rollback patch: "+rollback)
		}
		if patchPath := cleanOptionalString(result["patch_path"]); patchPath != "" {
			lines = append(lines, "Stored patch: "+patchPath)
		}
		if call.Error != "" {
			lines = append(lines, "Error: "+call.Error)
		}
		if ref := cleanOptionalString(call.ObservationRef); ref != "" {
			lines = append(lines, "Observation: "+ref)
		}
		return strings.Join(lines, "\n"), true
	}
	return "", false
}

func pendingApprovalAnswer(call app.ToolCall) string {
	return "等待审批中。"
}

func shellOutputLines(output string, maxLines, maxChars int) string {
	lines := boundedContentLines(output, maxLines, maxChars)
	if len(lines) == 0 {
		return "- " + trimForEpisode(strings.Join(strings.Fields(output), " "), 220)
	}
	for i, line := range lines {
		lines[i] = "- " + line
	}
	return strings.Join(lines, "\n")
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func fileSearchAnswerFromCalls(goal string, calls []app.ToolCall) (string, bool) {
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		if call.Tool != "files.search" || call.Status != "completed" {
			continue
		}
		result, ok := anyMap(call.Result)
		if !ok {
			continue
		}
		query := cleanOptionalString(result["query"])
		root := cleanOptionalString(result["root"])
		count := intLikeValue(result["count"])
		lines := []string{
			"Query: " + quoteInline(query),
			fmt.Sprintf("Matches: %d", count),
		}
		if root != "" {
			lines = append(lines, "Root: "+root)
		}
		results := anySlice(result["results"])
		if len(results) == 0 {
			lines = append(lines, "No matching workspace files were found.")
			return strings.Join(lines, "\n"), true
		}
		lines = append(lines, "Files:")
		for _, item := range results {
			entry, ok := anyMap(item)
			if !ok {
				continue
			}
			parts := []string{}
			if path := cleanOptionalString(entry["path"]); path != "" {
				parts = append(parts, path)
			}
			if reason := cleanOptionalString(entry["reason"]); reason != "" {
				parts = append(parts, "reason="+reason)
			}
			if preview := cleanOptionalString(entry["preview"]); preview != "" {
				parts = append(parts, "preview="+quoteInline(trimForEpisode(strings.Join(strings.Fields(preview), " "), 220)))
			}
			if len(parts) > 0 {
				lines = append(lines, "- "+strings.Join(parts, " "))
			}
		}
		return strings.Join(lines, "\n"), true
	}
	return "", false
}

func emailAnswerFromCalls(goal string, calls []app.ToolCall) (string, bool) {
	if answer, ok := emailDraftAnswerFromCalls(goal, calls); ok {
		return answer, true
	}
	if answer, ok := emailTriageAnswerFromCalls(goal, calls); ok {
		return answer, true
	}
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		if call.Status != "completed" {
			continue
		}
		switch call.Tool {
		case "email.read_thread":
			if answer, ok := emailThreadAnswer(call); ok {
				return answer, true
			}
		case "email.search":
			if answer, ok := emailSearchAnswer(call); ok {
				return answer, true
			}
		}
	}
	return "", false
}

func emailTriageAnswerFromCalls(goal string, calls []app.ToolCall) (string, bool) {
	if !asksForEmailTriage(goal) {
		return "", false
	}
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		if call.Tool != "email.search" || call.Status != "completed" {
			continue
		}
		result, ok := anyMap(call.Result)
		if !ok {
			continue
		}
		results := anySlice(result["results"])
		unreadCount := 0
		importantCount := 0
		lines := []string{
			"Inbox triage:",
			fmt.Sprintf("Query: %s", quoteInline(cleanOptionalString(result["query"]))),
			fmt.Sprintf("Threads reviewed: %d", intLikeValue(result["count"])),
		}
		threadLines := []string{}
		for _, item := range results {
			thread, ok := anyMap(item)
			if !ok {
				continue
			}
			labels := normalizedLabelSet(stringSliceValue(thread["labels"]))
			if labels["unread"] {
				unreadCount++
			}
			if labels["important"] {
				importantCount++
			}
			classification := classifyEmailThread(labels, cleanOptionalString(thread["subject"]), cleanOptionalString(thread["preview"]))
			parts := []string{}
			if id := cleanOptionalString(thread["id"]); id != "" {
				parts = append(parts, "id="+id)
			}
			if subject := cleanOptionalString(thread["subject"]); subject != "" {
				parts = append(parts, "subject="+quoteInline(subject))
			}
			if from := cleanOptionalString(thread["from"]); from != "" {
				parts = append(parts, "from="+from)
			}
			if len(labels) > 0 {
				parts = append(parts, "labels="+quoteInline(strings.Join(sortedLabelKeys(labels), ",")))
			}
			parts = append(parts, "class="+classification)
			if preview := cleanOptionalString(thread["preview"]); preview != "" {
				parts = append(parts, "preview="+quoteInline(trimForEpisode(strings.Join(strings.Fields(preview), " "), 180)))
			}
			threadLines = append(threadLines, "- "+strings.Join(parts, " "))
		}
		lines = append(lines, fmt.Sprintf("Unread: %d", unreadCount), fmt.Sprintf("Important: %d", importantCount))
		if len(threadLines) == 0 {
			lines = append(lines, "No unread inbox threads were found.")
			return strings.Join(lines, "\n"), true
		}
		lines = append(lines, "Threads:")
		lines = append(lines, threadLines...)
		return strings.Join(lines, "\n"), true
	}
	return "", false
}

func asksForEmailTriage(goal string) bool {
	return containsAny(goal, "triage", "classify", "summarize unread inbox", "unread inbox", "收件箱", "未读", "分类")
}

func normalizedLabelSet(labels []string) map[string]bool {
	out := map[string]bool{}
	for _, label := range labels {
		label = strings.ToLower(strings.TrimSpace(label))
		if label != "" {
			out[label] = true
		}
	}
	return out
}

func sortedLabelKeys(labels map[string]bool) []string {
	out := make([]string, 0, len(labels))
	for label := range labels {
		out = append(out, label)
	}
	sortStrings(out)
	return out
}

func classifyEmailThread(labels map[string]bool, subject, preview string) string {
	if labels["important"] {
		return "important"
	}
	text := strings.ToLower(subject + " " + preview)
	if containsAny(text, "review", "before friday", "deadline", "deploy", "deployment", "checklist") {
		return "needs_reply"
	}
	return "routine"
}

func emailDraftAnswerFromCalls(goal string, calls []app.ToolCall) (string, bool) {
	var draftCall *app.ToolCall
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].Tool == "email.draft_reply" && calls[i].Status == "completed" {
			draftCall = &calls[i]
			break
		}
	}
	if draftCall == nil {
		return "", false
	}
	result, ok := anyMap(draftCall.Result)
	if !ok {
		return "", false
	}
	lines := []string{"Email reply draft:"}
	if threadID := cleanOptionalString(result["thread_id"]); threadID != "" {
		lines = append(lines, "Thread: "+threadID)
	}
	if path := cleanOptionalString(result["path"]); path != "" {
		lines = append(lines, "Draft path: "+path)
	}
	if status := cleanOptionalString(result["status"]); status != "" {
		lines = append(lines, "Status: "+status)
	}
	if summary := emailThreadContextSummary(calls); summary != "" {
		lines = append(lines, "", "Email facts used:", summary)
	}
	if asksForFreeSlots(goal) || containsAny(goal, "calendar", "schedule", "日程", "会议") {
		if slots := calendarAvailabilityLinesForDraft(goal, calls); len(slots) > 0 {
			lines = append(lines, "", "Calendar availability used:")
			lines = append(lines, slots...)
		}
	}
	if body := cleanOptionalString(draftCall.Arguments["body"]); body != "" {
		lines = append(lines, "", "Draft body preview:", "- "+trimForEpisode(strings.Join(strings.Fields(body), " "), 320))
	}
	lines = append(lines, "Safety: Draft only; no email was sent.")
	return strings.Join(lines, "\n"), true
}

func emailSearchAnswer(call app.ToolCall) (string, bool) {
	result, ok := anyMap(call.Result)
	if !ok {
		return "", false
	}
	count := intLikeValue(result["count"])
	lines := []string{
		fmt.Sprintf("Query: %s", quoteInline(stringValue(result["query"]))),
		fmt.Sprintf("Matches: %d", count),
	}
	results := anySlice(result["results"])
	if len(results) == 0 {
		lines = append(lines, "No matching email threads were found.")
		return strings.Join(lines, "\n"), true
	}
	lines = append(lines, "Threads:")
	for _, item := range results {
		thread, ok := anyMap(item)
		if !ok {
			continue
		}
		parts := []string{}
		if id := strings.TrimSpace(stringValue(thread["id"])); id != "" && id != "<nil>" {
			parts = append(parts, "id="+id)
		}
		if subject := strings.TrimSpace(stringValue(thread["subject"])); subject != "" && subject != "<nil>" {
			parts = append(parts, "subject="+quoteInline(subject))
		}
		if from := strings.TrimSpace(stringValue(thread["from"])); from != "" && from != "<nil>" {
			parts = append(parts, "from="+from)
		}
		if date := strings.TrimSpace(stringValue(thread["date"])); date != "" && date != "<nil>" {
			parts = append(parts, "date="+date)
		}
		if preview := strings.TrimSpace(stringValue(thread["preview"])); preview != "" && preview != "<nil>" {
			parts = append(parts, "preview="+quoteInline(preview))
		}
		if len(parts) > 0 {
			lines = append(lines, "- "+strings.Join(parts, " "))
		}
	}
	return strings.Join(lines, "\n"), true
}

func emailThreadContextSummary(calls []app.ToolCall) string {
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		if call.Tool != "email.read_thread" || call.Status != "completed" {
			continue
		}
		result, ok := anyMap(call.Result)
		if !ok {
			continue
		}
		thread, ok := anyMap(result["thread"])
		if !ok {
			continue
		}
		parts := []string{}
		if id := cleanOptionalString(thread["id"]); id != "" {
			parts = append(parts, "id="+id)
		}
		if subject := cleanOptionalString(thread["subject"]); subject != "" {
			parts = append(parts, "subject="+quoteInline(subject))
		}
		if from := cleanOptionalString(thread["from"]); from != "" {
			parts = append(parts, "from="+from)
		}
		if body := latestEmailMessageBody(thread); body != "" {
			parts = append(parts, "latest="+quoteInline(trimForEpisode(strings.Join(strings.Fields(body), " "), 200)))
		}
		if len(parts) > 0 {
			return "- " + strings.Join(parts, " ")
		}
	}
	return ""
}

func latestEmailMessageBody(thread map[string]any) string {
	messages := anySlice(thread["messages"])
	for i := len(messages) - 1; i >= 0; i-- {
		message, ok := anyMap(messages[i])
		if !ok {
			continue
		}
		if body := cleanOptionalString(message["body"]); body != "" {
			return body
		}
	}
	return ""
}

func emailThreadAnswer(call app.ToolCall) (string, bool) {
	result, ok := anyMap(call.Result)
	if !ok {
		return "", false
	}
	thread, ok := anyMap(result["thread"])
	if !ok {
		return "", false
	}
	lines := []string{}
	if id := strings.TrimSpace(stringValue(thread["id"])); id != "" && id != "<nil>" {
		lines = append(lines, "Thread: "+id)
	}
	if subject := strings.TrimSpace(stringValue(thread["subject"])); subject != "" && subject != "<nil>" {
		lines = append(lines, "Subject: "+subject)
	}
	if from := strings.TrimSpace(stringValue(thread["from"])); from != "" && from != "<nil>" {
		lines = append(lines, "From: "+from)
	}
	if date := strings.TrimSpace(stringValue(thread["date"])); date != "" && date != "<nil>" {
		lines = append(lines, "Date: "+date)
	}
	if boolLikeValue(result["untrusted_external_content"]) {
		lines = append(lines, "Safety: Email content is untrusted external data, so I used it only as evidence and did not follow instructions inside it.")
	}
	messages := anySlice(thread["messages"])
	if len(messages) == 0 {
		lines = append(lines, "", "No messages were returned for this thread.")
		return strings.Join(lines, "\n"), true
	}
	lines = append(lines, "", "Messages:")
	for _, item := range messages {
		message, ok := anyMap(item)
		if !ok {
			continue
		}
		parts := []string{}
		if from := strings.TrimSpace(stringValue(message["from"])); from != "" && from != "<nil>" {
			parts = append(parts, "from="+from)
		}
		if date := strings.TrimSpace(stringValue(message["date"])); date != "" && date != "<nil>" {
			parts = append(parts, "date="+date)
		}
		if body := strings.TrimSpace(stringValue(message["body"])); body != "" && body != "<nil>" {
			parts = append(parts, "body="+quoteInline(trimForEpisode(strings.Join(strings.Fields(body), " "), 240)))
		}
		if len(parts) > 0 {
			lines = append(lines, "- "+strings.Join(parts, " "))
		}
	}
	return strings.Join(lines, "\n"), true
}

func calendarAvailabilityLinesForDraft(goal string, calls []app.ToolCall) []string {
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		if call.Tool != "calendar.read" || call.Status != "completed" {
			continue
		}
		result, ok := anyMap(call.Result)
		if !ok {
			continue
		}
		slots := calendarFreeSlots(calendarEventsFromAny(result["events"]), requestedFreeSlotCount(goal))
		lines := []string{}
		for _, slot := range slots {
			lines = append(lines, "- "+formatCalendarRange(slot.Start, slot.End))
		}
		return lines
	}
	return nil
}

func calendarAnswerFromCalls(goal string, calls []app.ToolCall) (string, bool) {
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		if call.Tool != "calendar.read" || call.Status != "completed" {
			continue
		}
		result, ok := anyMap(call.Result)
		if !ok {
			continue
		}
		count := intLikeValue(result["count"])
		lines := []string{fmt.Sprintf("Events: %d", count)}
		events := calendarEventsFromAny(result["events"])
		if len(events) == 0 {
			lines = append(lines, "No calendar events were found for the requested range.")
			return strings.Join(lines, "\n"), true
		}
		for _, event := range events {
			parts := []string{}
			if event.Title != "" {
				parts = append(parts, quoteInline(event.Title))
			}
			if event.StartRaw != "" {
				parts = append(parts, "start="+event.StartRaw)
			}
			if event.EndRaw != "" {
				parts = append(parts, "end="+event.EndRaw)
			}
			if event.Location != "" {
				parts = append(parts, "location="+quoteInline(event.Location))
			}
			if event.Notes != "" {
				parts = append(parts, "notes="+quoteInline(trimForEpisode(strings.Join(strings.Fields(event.Notes), " "), 160)))
			}
			if len(parts) > 0 {
				lines = append(lines, "- "+strings.Join(parts, " "))
			}
		}
		if asksForFreeSlots(goal) {
			lines = append(lines, "")
			lines = append(lines, calendarFreeSlotsSummary(goal, events)...)
		}
		if asksForCalendarConflicts(goal) {
			lines = append(lines, "")
			lines = append(lines, calendarConflictSummary(goal, events)...)
		}
		return strings.Join(lines, "\n"), true
	}
	return "", false
}

type calendarEventView struct {
	ID       string
	Title    string
	StartRaw string
	EndRaw   string
	Start    time.Time
	End      time.Time
	Location string
	Notes    string
}

func calendarEventsFromAny(value any) []calendarEventView {
	items := anySlice(value)
	events := []calendarEventView{}
	for _, item := range items {
		event, ok := anyMap(item)
		if !ok {
			continue
		}
		startRaw := strings.TrimSpace(stringValue(event["start"]))
		endRaw := strings.TrimSpace(stringValue(event["end"]))
		start, startErr := time.Parse(time.RFC3339, startRaw)
		end, endErr := time.Parse(time.RFC3339, endRaw)
		events = append(events, calendarEventView{
			ID:       cleanOptionalString(event["id"]),
			Title:    cleanOptionalString(event["title"]),
			StartRaw: cleanOptionalString(event["start"]),
			EndRaw:   cleanOptionalString(event["end"]),
			Start:    timeOrZero(start, startErr),
			End:      timeOrZero(end, endErr),
			Location: cleanOptionalString(event["location"]),
			Notes:    cleanOptionalString(event["notes"]),
		})
	}
	return events
}

func calendarFreeSlotsSummary(goal string, events []calendarEventView) []string {
	slots := calendarFreeSlots(events, requestedFreeSlotCount(goal))
	lines := []string{"Free slots:"}
	if len(slots) == 0 {
		return append(lines, "No free slots were found in the inferred workday window.")
	}
	for _, slot := range slots {
		lines = append(lines, "- "+formatCalendarRange(slot.Start, slot.End))
	}
	return lines
}

func calendarFreeSlots(events []calendarEventView, maxSlots int) []calendarSlot {
	if maxSlots <= 0 {
		maxSlots = 3
	}
	valid := validCalendarEvents(events)
	if len(valid) == 0 {
		return nil
	}
	sortCalendarEvents(valid)
	day := time.Date(valid[0].Start.Year(), valid[0].Start.Month(), valid[0].Start.Day(), 0, 0, 0, 0, time.UTC)
	workStart := day.Add(9 * time.Hour)
	workEnd := day.Add(17 * time.Hour)
	busy := []calendarSlot{}
	for _, event := range valid {
		if event.End.Before(workStart) || event.Start.After(workEnd) || !sameUTCDate(event.Start, workStart) {
			continue
		}
		start := maxTime(event.Start, workStart)
		end := minTime(event.End, workEnd)
		if end.After(start) {
			busy = append(busy, calendarSlot{Start: start, End: end})
		}
	}
	busy = mergeCalendarSlots(busy)
	free := []calendarSlot{}
	cursor := workStart
	for _, slot := range busy {
		if slot.Start.After(cursor) {
			free = append(free, calendarSlot{Start: cursor, End: slot.Start})
			if len(free) >= maxSlots {
				return free
			}
		}
		if slot.End.After(cursor) {
			cursor = slot.End
		}
	}
	if workEnd.After(cursor) && len(free) < maxSlots {
		free = append(free, calendarSlot{Start: cursor, End: workEnd})
	}
	return free
}

func calendarConflictSummary(goal string, events []calendarEventView) []string {
	conflicts := calendarConflicts(goal, events)
	lines := []string{"Conflicts:"}
	if len(conflicts) == 0 {
		return append(lines, "No conflicts were found in the observed calendar data.")
	}
	for _, conflict := range conflicts {
		lines = append(lines, "- "+conflict)
	}
	return lines
}

func calendarConflicts(goal string, events []calendarEventView) []string {
	valid := validCalendarEvents(events)
	sortCalendarEvents(valid)
	startRaw := extractDateTimeValue(goal, "start")
	endRaw := extractDateTimeValue(goal, "end")
	if startRaw != "" && endRaw != "" {
		start, startErr := time.Parse(time.RFC3339, startRaw)
		end, endErr := time.Parse(time.RFC3339, endRaw)
		if startErr == nil && endErr == nil && end.After(start) {
			out := []string{}
			for _, event := range valid {
				if rangesOverlap(start, end, event.Start, event.End) {
					out = append(out, fmt.Sprintf("%s overlaps %s", quoteInline(eventTitle(event)), formatCalendarRange(event.Start, event.End)))
				}
			}
			return out
		}
	}
	out := []string{}
	for i := 1; i < len(valid); i++ {
		prev := valid[i-1]
		current := valid[i]
		if rangesOverlap(prev.Start, prev.End, current.Start, current.End) {
			out = append(out, fmt.Sprintf("%s overlaps %s", quoteInline(eventTitle(prev)), quoteInline(eventTitle(current))))
		}
	}
	return out
}

type calendarSlot struct {
	Start time.Time
	End   time.Time
}

func validCalendarEvents(events []calendarEventView) []calendarEventView {
	out := []calendarEventView{}
	for _, event := range events {
		if !event.Start.IsZero() && !event.End.IsZero() && event.End.After(event.Start) {
			out = append(out, event)
		}
	}
	return out
}

func sortCalendarEvents(events []calendarEventView) {
	for i := 1; i < len(events); i++ {
		for j := i; j > 0 && events[j].Start.Before(events[j-1].Start); j-- {
			events[j], events[j-1] = events[j-1], events[j]
		}
	}
}

func mergeCalendarSlots(slots []calendarSlot) []calendarSlot {
	if len(slots) == 0 {
		return nil
	}
	for i := 1; i < len(slots); i++ {
		for j := i; j > 0 && slots[j].Start.Before(slots[j-1].Start); j-- {
			slots[j], slots[j-1] = slots[j-1], slots[j]
		}
	}
	merged := []calendarSlot{slots[0]}
	for _, slot := range slots[1:] {
		last := &merged[len(merged)-1]
		if !slot.Start.After(last.End) {
			if slot.End.After(last.End) {
				last.End = slot.End
			}
			continue
		}
		merged = append(merged, slot)
	}
	return merged
}

func requestedFreeSlotCount(goal string) int {
	lower := strings.ToLower(goal)
	for _, word := range []struct {
		Text  string
		Count int
	}{
		{"one", 1}, {"two", 2}, {"three", 3}, {"four", 4}, {"five", 5}, {"一个", 1}, {"两个", 2}, {"三个", 3},
	} {
		if strings.Contains(lower, word.Text) {
			return word.Count
		}
	}
	if match := regexp.MustCompile(`\b([1-9])\b`).FindStringSubmatch(goal); len(match) > 1 {
		return int(match[1][0] - '0')
	}
	return 3
}

func asksForFreeSlots(goal string) bool {
	return containsAny(goal, "free slot", "free time", "availability", "available", "空档", "空闲", "可用时间")
}

func asksForCalendarConflicts(goal string) bool {
	return containsAny(goal, "conflict", "overlap", "冲突", "重叠")
}

func rangesOverlap(startA, endA, startB, endB time.Time) bool {
	return startA.Before(endB) && endA.After(startB)
}

func formatCalendarRange(start, end time.Time) string {
	return start.UTC().Format("2006-01-02 15:04") + "-" + end.UTC().Format("15:04 UTC")
}

func eventTitle(event calendarEventView) string {
	if event.Title != "" {
		return event.Title
	}
	if event.ID != "" {
		return event.ID
	}
	return "untitled event"
}

func sameUTCDate(a, b time.Time) bool {
	a = a.UTC()
	b = b.UTC()
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func cleanOptionalString(value any) string {
	text := strings.TrimSpace(stringValue(value))
	if text == "<nil>" {
		return ""
	}
	return text
}

func timeOrZero(value time.Time, err error) time.Time {
	if err != nil {
		return time.Time{}
	}
	return value.UTC()
}

func boundedContentLines(content string, maxLines, maxChars int) []string {
	if maxLines <= 0 {
		maxLines = 6
	}
	if maxChars <= 0 {
		maxChars = 900
	}
	out := []string{}
	used := 0
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			continue
		}
		if len([]rune(line)) > 220 {
			line = trimForEpisode(line, 220)
		}
		lineLen := len([]rune(line))
		if used+lineLen > maxChars && len(out) > 0 {
			break
		}
		out = append(out, line)
		used += lineLen
		if len(out) >= maxLines {
			break
		}
	}
	return out
}

func quoteInline(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "\"\""
	}
	value = strings.ReplaceAll(value, "\"", "\\\"")
	return "\"" + value + "\""
}
