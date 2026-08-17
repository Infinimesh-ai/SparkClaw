package mcptools

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpclient"
)

func TestEvaluateUsesAnnotationsInsteadOfToolName(t *testing.T) {
	for _, test := range []struct {
		name      string
		tool      mcpclient.Tool
		mutations bool
		visible   bool
		risk      app.RiskLevel
		approval  bool
	}{
		{name: "spoofed get prefix", tool: mcpclient.Tool{Name: "get_wipe_workspace"}, mutations: false, visible: false, risk: app.RiskReversible, approval: true},
		{name: "unannotated mutation enabled", tool: mcpclient.Tool{Name: "list_tasks"}, mutations: true, visible: true, risk: app.RiskReversible, approval: true},
		{name: "explicit read", tool: mcpclient.Tool{Name: "arbitrary", Annotations: map[string]any{"readOnlyHint": true}}, visible: true, risk: app.RiskRead, approval: false},
		{name: "dangerous overrides read", tool: mcpclient.Tool{Name: "conflict", Annotations: map[string]any{"readOnlyHint": true, "destructiveHint": true}}, mutations: true, visible: true, risk: app.RiskDangerous, approval: true},
		{name: "open world overrides read", tool: mcpclient.Tool{Name: "conflict", Annotations: map[string]any{"readOnlyHint": true, "openWorldHint": true}}, mutations: false, visible: false, risk: app.RiskDangerous, approval: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			decision := Evaluate(test.tool, Policy{AllowMutations: test.mutations})
			if decision.Visible != test.visible || decision.Risk != test.risk || decision.RequiresApproval != test.approval {
				t.Fatalf("decision = %#v", decision)
			}
		})
	}
}

func TestEvaluateAppliesExactAllowAndDenyFilters(t *testing.T) {
	read := mcpclient.Tool{Name: "get_task", Annotations: map[string]any{"readOnlyHint": true}}
	for _, test := range []struct {
		name    string
		policy  Policy
		visible bool
	}{
		{name: "empty filters", policy: Policy{}, visible: true},
		{name: "allowed", policy: Policy{ToolAllow: []string{"get_task"}}, visible: true},
		{name: "allow is exact", policy: Policy{ToolAllow: []string{"get_*"}}, visible: false},
		{name: "not allowed", policy: Policy{ToolAllow: []string{"list_tasks"}}, visible: false},
		{name: "deny wins", policy: Policy{ToolAllow: []string{"get_task"}, ToolDeny: []string{"get_task"}}, visible: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := Evaluate(read, test.policy).Visible; got != test.visible {
				t.Fatalf("visible = %t, want %t", got, test.visible)
			}
		})
	}
}

func TestTranslateBuildsOneConsistentDefinition(t *testing.T) {
	discovered := mcpclient.DiscoveredTool{
		LocalName: "mcp.fixture.read", RemoteName: "read",
		Tool: mcpclient.Tool{Name: "read", Title: " Remote read ", Description: "Read remote data",
			Annotations: map[string]any{"readOnlyHint": true, "idempotentHint": true}},
	}
	classification := Classify(discovered.Tool)
	definition := Translate(discovered, classification, DefinitionOptions{
		TimeoutMS: 2000, Sandbox: "remote", IncludeWorkspaceEffects: true,
		Capabilities: []app.CapabilityDescriptor{{Name: app.ToolCapabilityExternalMCPWorkspace}},
		Directory:    app.ToolDirectoryMetadata{Summary: "Read data"}, OutcomeAdapter: app.OutcomeAdapterGeneric,
	})
	if definition.InputSchema == nil || definition.Risk != app.RiskRead || definition.RequiresApproval || !definition.Idempotent || definition.Title != "Remote read" {
		t.Fatalf("definition = %#v", definition)
	}
	if len(definition.Directory.Effects) != 2 || definition.Directory.Effects[0] != app.ToolEffectExternalRead || definition.Directory.Effects[1] != app.ToolEffectWorkspaceRead {
		t.Fatalf("effects = %#v", definition.Directory.Effects)
	}
}

func TestReadOnlyDoesNotImplyIdempotent(t *testing.T) {
	classification := Classify(mcpclient.Tool{Name: "read", Annotations: map[string]any{"readOnlyHint": true}})
	if classification.Idempotent {
		t.Fatal("readOnlyHint incorrectly implied idempotentHint")
	}
}
