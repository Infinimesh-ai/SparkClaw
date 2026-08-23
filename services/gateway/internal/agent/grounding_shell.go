// Grounded summary builders for the sandboxed-shell strategy.
package agent

import (
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

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

func shellAnswerFromCalls(goal string, calls []app.ToolCall) (string, bool) {
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		if call.Tool != "shell.exec_sandboxed" {
			continue
		}
		if call.Status == app.ToolCallStatusApprovalPending {
			return pendingApprovalAnswer(call), true
		}
		if !strings.Contains(string(call.Status), "completed") && !strings.Contains(string(call.Status), "failed") {
			continue
		}
		result, ok := anyMap(call.Result)
		if !ok {
			if call.Error != "" {
				return "Command: " + quoteInline(stringValue(call.Arguments["command"])) + "\nStatus: " + string(call.Status) + "\nError: " + call.Error, true
			}
			continue
		}
		lines := []string{
			"Command: " + quoteInline(stringValue(call.Arguments["command"])),
			"Tool status: " + string(call.Status),
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
