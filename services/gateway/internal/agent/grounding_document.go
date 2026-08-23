// Grounded summary builders for the document-mutation strategy.
package agent

import (
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

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

func isDocumentMutationTool(tool string) bool {
	if strings.HasPrefix(tool, "docx.") || strings.HasPrefix(tool, "pptx.") || strings.HasPrefix(tool, "xlsx.") {
		return true
	}
	switch tool {
	case "text.replace_text", "office.replace_text", "pdf.transform":
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
