// Grounded summary builders for the browser-automation, browser-tabs,
// browser-screenshot, and browser-read strategies.
package agent

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func groundedBrowserAutomationSummary(goal, fallback string, calls []app.ToolCall) (string, bool) {
	if failure, ok := browserInteractionFailureAnswerFromCalls(calls); ok {
		return failure, true
	}
	if result, ok := browserVerifiedResultAnswerFromCalls(goal, calls); ok {
		return result, true
	}
	if tabs, ok := browserTabsAnswerFromCalls(calls); ok {
		return tabs, true
	}
	if screenshot, ok := browserScreenshotAnswerFromCalls(goal, fallback, calls); ok {
		return screenshot, true
	}
	return "", false
}

func browserVerifiedResultAnswerFromCalls(goal string, calls []app.ToolCall) (string, bool) {
	snapshotIndex := latestBrowserResultSnapshot(calls)
	if snapshotIndex < 0 {
		return "", false
	}
	snapshot, ok := browserSnapshotPayload(calls[snapshotIndex].Result)
	if !ok || cleanOptionalString(snapshot["presentation"]) != string(app.BrowserPresentationVisible) {
		return "", false
	}
	for index := len(calls) - 1; index > snapshotIndex; index-- {
		call := calls[index]
		if call.Tool != "browser.assess_goal" || !toolCallCompleted(call) {
			continue
		}
		assessment := browserOutcomePayload(call.Result)
		if strings.TrimSpace(stringValue(assessment["status"])) != "succeeded" ||
			!boolLikeValue(assessment["goal_satisfied"]) {
			continue
		}
		if snapshotID := cleanOptionalString(assessment["snapshot_id"]); snapshotID != "" && cleanOptionalString(snapshot["snapshot_id"]) == snapshotID {
			if resultURL := cleanOptionalString(snapshot["url"]); resultURL != "" {
				return browserResultAnswer(goal, resultURL, snapshot, calls[snapshotIndex].Result), true
			}
		}
	}
	return "", false
}

// Opening a destination finishes at the validated result snapshot; unlike an
// interaction, it does not call assess_goal. Use the workflow's accepted result,
// not an arbitrary successful snapshot or its requested presentation lane.
func browserWorkflowResultAnswer(goal string, run app.AgentRun, calls []app.ToolCall) (string, bool) {
	state := run.Workflow
	if state == nil || state.Status != app.WorkflowStatusSucceeded || state.Browser == nil || state.Browser.Result == nil ||
		(state.Plan.ProfileID != app.WorkflowBrowserAutomation && state.Plan.ProfileID != app.WorkflowBrowserInteraction) {
		return "", false
	}
	evidence := state.Browser.Result
	index := latestBrowserResultSnapshot(calls)
	if index < 0 || evidence.VerifiedAt.IsZero() || evidence.VisibleSnapshotID == "" || evidence.VisibleSnapshotDigest == "" ||
		evidence.VisiblePageID == "" || evidence.VisibleSession.Generation == 0 || evidence.Target.CanonicalURL == "" {
		return "", false
	}
	call := calls[index]
	if call.RunID != run.ID || call.WorkflowID != state.Plan.ProfileID || !slices.Contains(evidence.SourceToolCallIDs, call.ID) {
		return "", false
	}
	snapshot, ok := browserSnapshotPayload(call.Result)
	if !ok || cleanOptionalString(snapshot["snapshot_id"]) != evidence.VisibleSnapshotID ||
		cleanOptionalString(snapshot["digest"]) != evidence.VisibleSnapshotDigest ||
		cleanOptionalString(snapshot["page_id"]) != evidence.VisiblePageID ||
		browserSessionGenerationString(snapshot["session_generation"]) != fmt.Sprint(evidence.VisibleSession.Generation) ||
		cleanOptionalString(snapshot["presentation"]) != string(app.BrowserPresentationVisible) ||
		cleanOptionalString(snapshot["owner_id"]) != evidence.VisibleSession.OwnerID ||
		cleanOptionalString(snapshot["profile_id"]) != evidence.VisibleSession.ProfileID ||
		cleanOptionalString(snapshot["provider_session_ref"]) != evidence.VisibleSession.ProviderSessionRef ||
		!browserLoginResumeURLUsable(cleanOptionalString(snapshot["url"])) ||
		browserSafeResultURL(evidence.Target, cleanOptionalString(snapshot["url"])) != evidence.Target.CanonicalURL {
		return "", false
	}
	return browserResultAnswer(goal, evidence.Target.CanonicalURL, snapshot, call.Result), true
}

// Later actions or failures invalidate an earlier result. The final goal
// assessment and passive health observations can follow its snapshot.
func latestBrowserResultSnapshot(calls []app.ToolCall) int {
	for index := len(calls) - 1; index >= 0; index-- {
		call := calls[index]
		if !strings.HasPrefix(call.Tool, "browser.") {
			continue
		}
		if !toolCallCompleted(call) {
			return -1
		}
		switch call.Tool {
		case "browser.snapshot":
			return index
		case "browser.status":
			continue
		case "browser.assess_goal":
			assessment := browserOutcomePayload(call.Result)
			if assessment["status"] == "succeeded" && boolLikeValue(assessment["goal_satisfied"]) {
				continue
			}
		}
		return -1
	}
	return -1
}

func browserResultAnswer(goal, resultURL string, snapshot map[string]any, result any) string {
	// The outer ToolHub envelope may OR the requested visibility into metadata.
	// Only the provider's output can establish actual surface visibility.
	visible := boolLikeValue(browserOutcomePayload(result)["surface_visible"])
	if containsCJK(goal) {
		if visible {
			return "浏览器操作已完成，结果已在可见浏览器中打开：\n" + resultURL
		}
		if snapshot["profile_id"] == "client_embedded" {
			return "浏览器操作已完成，结果已加载到当前会话的任务页面，可打开浏览器查看：\n" + resultURL
		}
		return "浏览器操作已完成，结果页面：\n" + resultURL
	}
	if visible {
		return "Browser automation completed. The verified result is open in the visible browser:\n" + resultURL
	}
	if snapshot["profile_id"] == "client_embedded" {
		return "Browser automation completed. Open the browser to view the result in this conversation's task page:\n" + resultURL
	}
	return "Browser automation completed. Verified result page:\n" + resultURL
}

func browserInteractionFailureAnswerFromCalls(calls []app.ToolCall) (string, bool) {
	for index := len(calls) - 1; index >= 0; index-- {
		call := calls[index]
		if call.Tool != "browser.click" || call.Status == app.ToolCallStatusCompleted {
			continue
		}
		// Prose matching is the fallback for records persisted before
		// ErrorCode existed; new failures carry the typed code.
		if call.ErrorCode == string(app.ToolErrorUnsafeClickTarget) ||
			strings.Contains(strings.ToLower(call.Error), "unsafe click target") {
			return "页面交互已阻止：目标点击可能产生不允许的后果。", true
		}
	}
	return "", false
}

func browserTabsAnswerFromCalls(calls []app.ToolCall) (string, bool) {
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		if strings.HasPrefix(call.Tool, "browser.") && call.Tool != "browser.list_tabs" && call.Tool != "browser.status" {
			return "", false
		}
		if strings.HasPrefix(call.Tool, "browser.") && !toolCallCompleted(call) {
			return "", false
		}
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
			id := strings.TrimSpace(stringValue(item["page_id"]))
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
			if strings.HasPrefix(call.Tool, "browser.") && call.Status == app.ToolCallStatusFailed && call.Error != "" {
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
	case "browser.status", "browser.list_tabs", "browser.open", "browser.focus", "browser.close", "browser.navigate", "browser.snapshot", "browser.screenshot", "browser.visual_inspect", "browser.wait", "browser.click", "browser.validate_transition", "browser.assess_goal", "browser.type", "browser.select":
		return true
	default:
		return false
	}
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

func hasCompletedBrowserRead(calls []app.ToolCall) bool {
	for _, call := range calls {
		if call.Tool == "browser.read" && toolCallCompleted(call) {
			return true
		}
	}
	return false
}

func browserReadFallbackFailure(calls []app.ToolCall) string {
	for index := len(calls) - 1; index >= 0; index-- {
		call := calls[index]
		if call.Tool != "browser.read" || !toolCallCompleted(call) {
			continue
		}
		result, _ := anyMap(call.Result)
		content := cleanOptionalString(firstPresent(result, "text", "excerpt"))
		if content == "" {
			continue
		}
		const maxFallbackRunes = 12000
		runes := []rune(content)
		bounded := len(runes) > maxFallbackRunes
		if bounded {
			content = string(runes[:maxFallbackRunes])
		}
		lines := []string{"网页读取内容（外部不可信内容）："}
		if title := cleanOptionalString(result["title"]); title != "" {
			lines = append(lines, "标题："+title)
		}
		if source := cleanOptionalString(firstPresent(result, "final_url", "url")); source != "" {
			lines = append(lines, "来源："+source)
		}
		lines = append(lines, "", content)
		if bounded || boolLikeValue(result["truncated"]) {
			lines = append(lines, "", "[内容已按读取或返回上限截断]")
		}
		return strings.Join(lines, "\n")
	}
	return browserReadUnavailableFallback(calls)
}

func browserReadUnavailableFallback(calls []app.ToolCall) string {
	sources := []string{}
	for _, call := range calls {
		if call.Tool != "browser.read" || !toolCallCompleted(call) {
			continue
		}
		result, _ := anyMap(call.Result)
		if source := cleanOptionalString(firstPresent(result, "final_url", "url")); source != "" {
			sources = append(sources, source)
		}
	}
	lines := []string{blockedAnswerTaskIncomplete + "。", "网页读取完成，但没有返回可用的提取内容。"}
	if len(sources) > 0 {
		lines = append(lines, "已读取来源："+strings.Join(uniqueNonEmpty(sources), ", "))
	}
	return strings.Join(lines, "\n")
}
