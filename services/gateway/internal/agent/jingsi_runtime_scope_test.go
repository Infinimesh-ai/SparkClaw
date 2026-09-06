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
		Tools: []string{"files.read"}, ApprovalPolicy: jingsiscope.ApprovalDeny, MaxToolCalls: 4, MaxOutputBytes: 4096,
		Purpose: "task.execute", GrantID: "grant_demo", GrantVersion: "v1",
	}
}

func TestJingSiRuntimeToolExposureRequiresExactToolAndApprovalScope(t *testing.T) {
	grant := jingsiTestGrant()
	run := jingsiScopedRun(grant)
	if !jingsiRuntimeToolAuthorized(run, app.ToolDefinition{Name: "files.read", RequiresApproval: false}) {
		t.Fatal("exact allowed read tool was rejected")
	}
	if jingsiRuntimeToolAuthorized(run, app.ToolDefinition{Name: "files.write", RequiresApproval: false}) {
		t.Fatal("unscoped tool was exposed")
	}
	if jingsiRuntimeToolAuthorized(run, app.ToolDefinition{Name: "files.read", RequiresApproval: true}) {
		t.Fatal("deny approval policy exposed an approval-requiring tool")
	}
	grant.MaxToolCalls = 0
	if jingsiRuntimeToolAuthorized(jingsiScopedRun(grant), app.ToolDefinition{Name: "files.read", RequiresApproval: false}) {
		t.Fatal("zero tool-call budget exposed a tool")
	}
	if !jingsiRuntimeToolAuthorized(app.AgentRun{}, app.ToolDefinition{Name: "files.write"}) {
		t.Fatal("non-JingSi run was scoped")
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
	if jingsiRuntimeToolAuthorized(run, app.ToolDefinition{Name: "files.write"}) {
		t.Fatal("malformed projection exposed a tool the budget parser rejected")
	}
}
