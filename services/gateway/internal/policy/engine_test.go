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

	decision := New(cfg).Decide(def, map[string]any{"path": "draft.md"}, app.PolicyExecutionContext{})
	if !decision.Allowed || !decision.RequiresApproval {
		t.Fatalf("expected approval-required draft tool, got %#v", decision)
	}
}

func TestOperatorControlsEnforceIndependentToolAndApprovalChoices(t *testing.T) {
	cfg := config.Default()
	falseValue := false
	cfg.Security.OperatorControls = config.OperatorToolControls{
		WebAccess: &falseValue, FileChanges: "ask", ExternalActions: "block",
	}
	engine := New(cfg)
	for _, name := range []string{"web.search", "browser.read", "browser.click"} {
		def := app.ToolDefinition{Name: name, Risk: app.RiskRead}
		if engine.MayExpose(def).Allowed || engine.Decide(def, nil, app.PolicyExecutionContext{}).Allowed {
			t.Fatalf("%s bypassed an operator block", name)
		}
	}
	fileWrite := app.ToolDefinition{Name: "files.write_draft", Risk: app.RiskDraft}
	if decision := engine.Decide(fileWrite, nil, app.PolicyExecutionContext{}); !decision.Allowed || !decision.RequiresApproval {
		t.Fatalf("file change did not require approval: %#v", decision)
	}
	if decision := engine.Decide(app.ToolDefinition{Name: "files.read", Risk: app.RiskRead}, nil, app.PolicyExecutionContext{}); !decision.Allowed || decision.RequiresApproval {
		t.Fatalf("read-only file access changed: %#v", decision)
	}

	trueValue := true
	cfg.Security.OperatorControls.WebAccess = &trueValue
	engine = New(cfg)
	if !engine.Decide(app.ToolDefinition{Name: "browser.read", Risk: app.RiskRead}, nil, app.PolicyExecutionContext{}).Allowed ||
		engine.Decide(app.ToolDefinition{Name: "browser.click", Risk: app.RiskDraft}, nil, app.PolicyExecutionContext{}).Allowed {
		t.Fatal("enabling web access removed the independent external action block")
	}

	cfg.Security.OperatorControls.ExternalActions = "ask"
	decision := New(cfg).Decide(app.ToolDefinition{Name: "browser.click", Risk: app.RiskDraft}, nil, app.PolicyExecutionContext{})
	if !decision.Allowed || !decision.RequiresApproval {
		t.Fatalf("external action did not require approval: %#v", decision)
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

	execution := engine.Decide(def, map[string]any{"path": "notes.txt"}, app.PolicyExecutionContext{})
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

	decision := New(cfg).Decide(def, map[string]any{"title": "Draft"}, app.PolicyExecutionContext{})
	if !decision.Allowed || !decision.RequiresApproval {
		t.Fatalf("remote mutation lost approval: %#v", decision)
	}
	if decision.RequiresSandbox {
		t.Fatalf("remote mutation was mislabeled as local sandbox execution: %#v", decision)
	}
}

func TestExternalMCPWorkspaceDataContextEscalatesReadOnlyTool(t *testing.T) {
	cfg := config.Default()
	def := app.ToolDefinition{Name: "files.read", Risk: app.RiskRead}
	engine := New(cfg)

	human := engine.Decide(def, map[string]any{"path": "notes.txt"}, app.PolicyExecutionContext{})
	if !human.Allowed || human.RequiresApproval {
		t.Fatalf("human workspace read changed its registered baseline: %#v", human)
	}
	external := engine.Decide(def, map[string]any{"path": "notes.txt"}, app.PolicyExecutionContext{
		PrincipalClass: app.PolicyPrincipalExternalMCPAI,
		ResourceClass:  app.PolicyResourceSparkClawWorkspaceData,
		AccessClass:    app.PolicyAccessWorkspaceSourceRead,
	})
	if !external.Allowed || !external.RequiresApproval || external.Reason != "external MCP AI workspace data access requires owner approval" {
		t.Fatalf("external MCP workspace read was not escalated: %#v", external)
	}
}

func TestExternalMCPContextDoesNotEscalateSafeNonWorkspaceTool(t *testing.T) {
	cfg := config.Default()
	def := app.ToolDefinition{Name: "weather.lookup", Risk: app.RiskRead}
	decision := New(cfg).Decide(def, map[string]any{"location": "Shanghai"}, app.PolicyExecutionContext{
		PrincipalClass: app.PolicyPrincipalExternalMCPAI,
	})
	if !decision.Allowed || decision.RequiresApproval {
		t.Fatalf("external MCP weather lookup was escalated: %#v", decision)
	}
}

func TestExecutionContextCannotDowngradeRegisteredApproval(t *testing.T) {
	cfg := config.Default()
	def := app.ToolDefinition{Name: "file.delete", Risk: app.RiskDangerous, RequiresApproval: true}
	decision := New(cfg).Decide(def, map[string]any{"path": "notes.txt"}, app.PolicyExecutionContext{})
	if !decision.Allowed || !decision.RequiresApproval {
		t.Fatalf("empty execution context downgraded registered approval: %#v", decision)
	}
}

func TestPolicyUpdatesReachExistingExecutionScopes(t *testing.T) {
	cfg := config.Default()
	engine := New(cfg)
	scope := engine
	tool := app.ToolDefinition{Name: "files.write_draft", Risk: app.RiskDraft}
	if !scope.Decide(tool, nil, app.PolicyExecutionContext{}).Allowed {
		t.Fatal("initial policy denied tool")
	}
	cfg.Security.DeniedTools = []string{tool.Name}
	engine.Update(cfg)
	// Caller slice reuse must not mutate an already-published policy.
	cfg.Security.DeniedTools[0] = "unrelated"
	if scope.MayExpose(tool).Allowed || scope.Decide(tool, nil, app.PolicyExecutionContext{}).Allowed {
		t.Fatal("existing scope ignored policy revocation")
	}
	cfg.Security.DeniedTools = nil
	cfg.Security.ApprovalRequiredTools = []string{tool.Name}
	engine.Update(cfg)
	if decision := scope.Decide(tool, nil, app.PolicyExecutionContext{}); !decision.Allowed || !decision.RequiresApproval {
		t.Fatalf("scope missed current approval policy: %+v", decision)
	}
}
