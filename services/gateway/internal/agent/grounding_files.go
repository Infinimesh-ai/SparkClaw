// Grounded summary builders for the file-read and file-search strategies.
package agent

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

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
		blockedAnswerTaskIncomplete + "。",
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
		count := intLikeValue(result["count"])
		lines := []string{
			"Query: " + quoteInline(query),
			fmt.Sprintf("Matches: %d", count),
		}
		lines = append(lines, fmt.Sprintf("Complete: %t", result["complete"] == true))
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
			if path := cleanOptionalString(entry["rel_path"]); path != "" {
				parts = append(parts, path)
			}
			if score := intLikeValue(entry["score"]); score > 0 {
				parts = append(parts, fmt.Sprintf("score=%d", score))
			}
			if reason := cleanOptionalString(entry["reason"]); reason != "" {
				parts = append(parts, "reason="+reason)
			}
			if len(parts) > 0 {
				lines = append(lines, "- "+strings.Join(parts, " "))
			}
		}
		return strings.Join(lines, "\n"), true
	}
	return "", false
}
