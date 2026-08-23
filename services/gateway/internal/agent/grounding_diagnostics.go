// Grounded summary builders for the code-diagnostics strategy, which
// combines file-search evidence with sandboxed test execution.
package agent

import (
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

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
