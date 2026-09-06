package agent

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/jingsiscope"
)

func jingsiScopedRun(grant jingsiscope.Grant) app.AgentRun {
	return app.AgentRun{MessageContext: &app.MessageRunContext{
		Source:        app.MessageSourceContext{Adapter: jingsiscope.AdapterID},
		Authorization: app.MessageAuthorization{Scope: grant.Scopes()},
	}}
}

func jingsiTestGrant() jingsiscope.Grant {
	return jingsiscope.Grant{
		Tools: []string{"files.read"}, ApprovalPolicy: jingsiscope.ApprovalDeny, MaxToolCalls: 4, EffectScopesEnforced: true,
		DataScope: []string{string(app.ToolEffectWorkspaceRead)},
		Purpose:   "task.execute", GrantID: "grant_demo", GrantVersion: "v1",
	}
}

func workspaceReadTool(name string, requiresApproval bool) app.ToolDefinition {
	return app.ToolDefinition{
		Name: name, RequiresApproval: requiresApproval,
		Directory: app.ToolDirectoryMetadata{Effects: []app.ToolEffect{app.ToolEffectWorkspaceRead}},
	}
}

func TestJingSiRuntimeToolExposureRequiresExactToolAndApprovalScope(t *testing.T) {
	grant := jingsiTestGrant()
	run := jingsiScopedRun(grant)
	if !jingsiRuntimeToolAuthorized(run, workspaceReadTool("files.read", false)) {
		t.Fatal("exact allowed read tool was rejected")
	}
	if jingsiRuntimeToolAuthorized(run, workspaceReadTool("files.write", false)) {
		t.Fatal("unscoped tool was exposed")
	}
	if jingsiRuntimeToolAuthorized(run, workspaceReadTool("files.read", true)) {
		t.Fatal("deny approval policy exposed an approval-requiring tool")
	}
	grant.MaxToolCalls = 0
	if jingsiRuntimeToolAuthorized(jingsiScopedRun(grant), workspaceReadTool("files.read", false)) {
		t.Fatal("zero tool-call budget exposed a tool")
	}
	if !jingsiRuntimeToolAuthorized(app.AgentRun{}, app.ToolDefinition{Name: "files.write"}) {
		t.Fatal("non-JingSi run was scoped")
	}
}

func TestJingSiRuntimeToolExposureNarrowsToDataAndNetworkScope(t *testing.T) {
	grant := jingsiTestGrant()
	grant.Tools = []string{"files.read", "web.search"}
	webSearch := app.ToolDefinition{Name: "web.search", Directory: app.ToolDirectoryMetadata{Effects: []app.ToolEffect{app.ToolEffectExternalRead}}}
	if jingsiRuntimeToolAuthorized(jingsiScopedRun(grant), webSearch) {
		t.Fatal("network tool was exposed without network_scope")
	}
	grant.NetworkScope = []string{string(app.ToolEffectExternalRead)}
	if !jingsiRuntimeToolAuthorized(jingsiScopedRun(grant), webSearch) {
		t.Fatal("network tool stayed hidden after external.read was granted")
	}
	grant.DataScope = nil
	if jingsiRuntimeToolAuthorized(jingsiScopedRun(grant), workspaceReadTool("files.read", false)) {
		t.Fatal("workspace tool was exposed without data_scope")
	}
}

// The exposure gate and the run budget must read one grant: a projection the
// budget parser rejects must also hide every tool, and vice versa.
func TestJingSiRuntimeExposureAndBudgetShareOneGrant(t *testing.T) {
	grant := jingsiTestGrant()
	run := jingsiScopedRun(grant)
	if calls, ok := jingsiRuntimeMaxToolCalls(run); !ok || calls != grant.MaxToolCalls {
		t.Fatalf("budget = %d, %v; want %d", calls, ok, grant.MaxToolCalls)
	}
	if _, ok := jingsiRuntimeMaxToolCalls(app.AgentRun{}); ok {
		t.Fatal("non-JingSi run received a JingSi budget")
	}
	run.MessageContext.Authorization.Scope = append(run.MessageContext.Authorization.Scope, jingsiscope.PrefixTool+"files.write")
	run.MessageContext.Authorization.Scope[0] = jingsiscope.PrefixMaxToolCalls + "unbounded"
	calls, ok := jingsiRuntimeMaxToolCalls(run)
	if !ok || calls != 0 {
		t.Fatalf("malformed budget granted %d calls", calls)
	}
	if jingsiRuntimeToolAuthorized(run, workspaceReadTool("files.write", false)) {
		t.Fatal("malformed projection exposed a tool the budget parser rejected")
	}
}

func TestJingSiDocumentPreflightRequiresAuthorizedReader(t *testing.T) {
	runtime, _, _, closeRuntime := newDocumentDispatchRuntime(t, t.TempDir())
	defer closeRuntime()
	for _, tc := range []struct {
		name   string
		change func(*jingsiscope.Grant)
		allow  bool
	}{
		{"read", func(g *jingsiscope.Grant) {}, true},
		{"missing_effect", func(g *jingsiscope.Grant) { g.DataScope = nil }, false},
		{"wrong_family", func(g *jingsiscope.Grant) { g.DataScope = nil; g.NetworkScope = []string{"workspace.read"} }, false},
		{"write_only", func(g *jingsiscope.Grant) { g.DataScope = []string{"workspace.write"} }, false},
		{"missing_tool", func(g *jingsiscope.Grant) { g.Tools = nil }, false},
		{"zero_budget", func(g *jingsiscope.Grant) { g.MaxToolCalls = 0 }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			grant := jingsiTestGrant()
			tc.change(&grant)
			if got := runtime.jingSiDocumentPreflightAuthorized(jingsiScopedRun(grant), "private.txt"); got != tc.allow {
				t.Fatalf("preflight allowed=%v want=%v", got, tc.allow)
			}
		})
	}
}
