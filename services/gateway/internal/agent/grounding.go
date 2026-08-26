// Grounded summary entry point and strategy dispatch. Per-strategy
// builders live in the grounding_*.go files alongside this one.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
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
	strategyLocalMindTask      = "localmind_task_result"
)

func (r Runtime) applyGroundedSummary(ctx context.Context, sessionID, runID, goal, fallback string, calls []app.ToolCall) string {
	summary, strategy := groundedSummaryWithStrategy(goal, fallback, calls)
	if summary != fallback && fallbackPolicyEligible(fallback) {
		r.addAudit(ctx, app.AuditEvent{
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

func groundedSummary(goal, fallback string, calls []app.ToolCall) string {
	summary, _ := groundedSummaryWithStrategy(goal, fallback, calls)
	return summary
}

func groundedSummaryWithStrategy(goal, fallback string, calls []app.ToolCall) (string, string) {
	if grounded, ok := groundedLocalMindTaskSummary(calls); ok {
		return grounded, strategyLocalMindTask
	}
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
	return fallback, strategyGroundedResult
}

func groundedLocalMindTaskSummary(calls []app.ToolCall) (string, bool) {
	for index := len(calls) - 1; index >= 0; index-- {
		call := calls[index]
		if !toolCallCompleted(call) || !isLocalMindTaskToolCall(call) {
			continue
		}
		output, ok := anyMap(call.Result)
		if !ok {
			continue
		}
		taskID := strings.TrimSpace(stringValue(output["taskId"]))
		status := strings.ToLower(strings.TrimSpace(stringValue(output["status"])))
		terminal, terminalOK := output["terminal"].(bool)
		if taskID == "" || taskID == "<nil>" || status == "" || status == "<nil>" || !terminalOK {
			continue
		}
		if !terminal && (call.WorkflowID == app.WorkflowLocalMindRead || call.WorkflowID == app.WorkflowLocalMindWrite) && strings.TrimSpace(stringValue(call.Arguments["wait_ms"])) == "0" {
			return fmt.Sprintf("LocalMind 任务 `%s` 等待已达到 10 分钟上限，最新状态：%s。", taskID, status), true
		}
		if status == "completed" {
			if result := localMindResultText(output["result"]); result != "" {
				return fmt.Sprintf("%s\n\nLocalMind 任务 `%s`：completed", result, taskID), true
			}
		}
		if status == "failed" {
			if detail := localMindResultText(output["error"]); detail != "" {
				return fmt.Sprintf("LocalMind 任务 `%s` 失败：%s", taskID, detail), true
			}
		}
		if status == "cancelled" || status == "canceled" {
			return fmt.Sprintf("LocalMind 任务 `%s` 已取消。", taskID), true
		}
		return fmt.Sprintf("LocalMind 任务 `%s` 当前状态：%s。", taskID, status), true
	}
	return "", false
}

func localMindResultText(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	if object, ok := anyMap(value); ok {
		for _, key := range []string{"answer", "message", "summary"} {
			if text := strings.TrimSpace(stringValue(object[key])); text != "" && text != "<nil>" {
				return text
			}
		}
	}
	raw, err := json.Marshal(value)
	if err != nil || string(raw) == "null" || string(raw) == "{}" {
		return ""
	}
	return trimForEpisode(string(raw), 4000)
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
	lines := []string{blockedAnswerTaskIncomplete + "。"}
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

func failedToolCalls(calls []app.ToolCall) []app.ToolCall {
	out := []app.ToolCall{}
	laterCompleted := map[string]bool{}
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		if toolCallCompleted(call) {
			laterCompleted[call.Tool] = true
			continue
		}
		if call.Status.Failed() || call.Status == app.ToolCallStatusBlocked || call.Status == app.ToolCallStatusRejected {
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

func isActionOrModificationGoal(goal string) bool {
	lower := strings.ToLower(goal)
	return containsAny(lower,
		"modify", "edit", "replace", "delete", "remove", "write", "create", "update", "change", "send", "forward", "deliver",
		"修改", "编辑", "替换", "删除", "删掉", "移除", "写入", "生成", "创建", "改成", "换成", "发送", "发给", "发到", "转发", "投递", "传给",
	)
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

func shouldPreferModelFinal(goal, answer string) bool {
	return len([]rune(answer)) >= 12 && !isBlockedFinalAnswer(answer)
}

func pendingApprovalAnswer(call app.ToolCall) string {
	return "等待审批中。"
}
