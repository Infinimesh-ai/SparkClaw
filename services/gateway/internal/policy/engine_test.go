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
