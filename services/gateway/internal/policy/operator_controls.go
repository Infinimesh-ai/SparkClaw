package policy

import (
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

func operatorAllows(def app.ToolDefinition, controls config.OperatorToolControls) bool {
	name := def.Name
	if controls.WebAccess != nil && !*controls.WebAccess && hasToolPrefix(name, "web.", "browser.", "weather.") {
		return false
	}
	if controls.WorkspaceFiles != nil && !*controls.WorkspaceFiles && isWorkspaceFileTool(name) {
		return false
	}
	if controls.ShellCommands != nil && !*controls.ShellCommands && strings.HasPrefix(name, "shell.") {
		return false
	}
	return controls.ExternalActions != "block" || !isExternalActionTool(name)
}

func operatorNeedsApproval(def app.ToolDefinition, controls config.OperatorToolControls) bool {
	return controls.FileChanges == "ask" && isWorkspaceFileTool(def.Name) && def.Risk != app.RiskRead ||
		controls.ExternalActions == "ask" && isExternalActionTool(def.Name)
}

func isWorkspaceFileTool(name string) bool {
	return hasToolPrefix(name, "file.", "files.", "docx.", "xlsx.", "pptx.", "office.")
}

func isExternalActionTool(name string) bool {
	return name == "browser.click" || name == "browser.type" || name == "browser.select" ||
		hasToolPrefix(name, "email.send", "message.send", "publish.")
}

func hasToolPrefix(name string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
