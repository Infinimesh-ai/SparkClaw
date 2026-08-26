package agent

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestJingSiRuntimeToolExposureRequiresExactToolAndApprovalScope(t *testing.T) {
	run := app.AgentRun{MessageContext: &app.MessageRunContext{
		Source: app.MessageSourceContext{Adapter: "jingsi-runtime-v1"},
		Authorization: app.MessageAuthorization{Scope: []string{
			"sparkclaw.tool:files.read", "sparkclaw.approval:deny",
		}},
	}}
	if !jingsiRuntimeToolAuthorized(run, app.ToolDefinition{Name: "files.read", RequiresApproval: false}) {
		t.Fatal("exact allowed read tool was rejected")
	}
	if jingsiRuntimeToolAuthorized(run, app.ToolDefinition{Name: "files.write", RequiresApproval: false}) {
		t.Fatal("unscoped tool was exposed")
	}
	if jingsiRuntimeToolAuthorized(run, app.ToolDefinition{Name: "files.read", RequiresApproval: true}) {
		t.Fatal("deny approval policy exposed an approval-requiring tool")
	}
	run.MessageContext.Authorization.Scope = append(run.MessageContext.Authorization.Scope, "sparkclaw.budget.max_tool_calls:0")
	if jingsiRuntimeToolAuthorized(run, app.ToolDefinition{Name: "files.read", RequiresApproval: false}) {
		t.Fatal("zero tool-call budget exposed a tool")
	}
}
