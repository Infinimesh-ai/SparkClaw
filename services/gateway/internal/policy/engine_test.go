package policy

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

func TestPolicyApprovalRequiredTools(t *testing.T) {
	cfg := config.Default()
	cfg.Security.ApprovalRequiredTools = []string{"files.write_draft"}
	def := app.ToolDefinition{Name: "files.write_draft", Risk: app.RiskDraft}

	decision := New(cfg).Decide(def, map[string]any{"path": "draft.md"})
	if !decision.Allowed || !decision.RequiresApproval {
		t.Fatalf("expected approval-required draft tool, got %#v", decision)
	}
}

func TestMayExposeIsStaticAndExecutionRemainsArgumentAware(t *testing.T) {
	cfg := config.Default()
	cfg.Security.ApprovalRequiredTools = []string{"file.delete"}
	engine := New(cfg)
	def := app.ToolDefinition{Name: "file.delete", Risk: app.RiskReversible}

	exposure := engine.MayExpose(def)
	if !exposure.Allowed || exposure.RequiresApproval || len(exposure.Resources) != 0 {
		t.Fatalf("static exposure must not make an execution decision: %#v", exposure)
	}

	execution := engine.Decide(def, map[string]any{"path": "notes.txt"})
	if !execution.Allowed || !execution.RequiresApproval || len(execution.Resources) != 1 {
		t.Fatalf("execution decision did not use exact arguments: %#v", execution)
	}
}

func TestRemoteMutationKeepsApprovalWithoutClaimingLocalSandbox(t *testing.T) {
	cfg := config.Default()
	def := app.ToolDefinition{
		Name: "localmind.create_document", Risk: app.RiskReversible,
		RequiresApproval: true, Sandbox: "remote",
	}

	decision := New(cfg).Decide(def, map[string]any{"title": "Draft"})
	if !decision.Allowed || !decision.RequiresApproval {
		t.Fatalf("remote mutation lost approval: %#v", decision)
	}
	if decision.RequiresSandbox {
		t.Fatalf("remote mutation was mislabeled as local sandbox execution: %#v", decision)
	}
}
