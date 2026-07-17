package agent

import (
	"fmt"
	"path/filepath"
	"strings"

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
	case "files.read", "files.search", "browser.read", "web.search", "memory.search", "pdf.extract_text":
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

func cleanOptionalString(value any) string {
	text := strings.TrimSpace(stringValue(value))
	if text == "<nil>" {
		return ""
	}
	return text
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
