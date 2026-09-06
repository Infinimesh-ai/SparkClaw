package jingsiscope

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func sampleGrant() Grant {
	return Grant{
		Tools: []string{"browser.read", "files.read"}, ApprovalPolicy: ApprovalAsk,
		MaxToolCalls: 8, MaxOutputBytes: 4096,
		DataScope: []string{"memory.context", "workspace.read"}, NetworkScope: []string{"external.read"},
		Purpose: "task.execute", GrantID: "grant:space/demo", GrantVersion: "v1",
	}
}

func TestScopesRoundTripThroughParse(t *testing.T) {
	want := sampleGrant()
	scopes := want.Scopes()
	if !isSorted(scopes) {
		t.Fatalf("projection is not sorted: %v", scopes)
	}
	got, err := Parse(scopes)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse(Scopes()) = %#v, want %#v", got, want)
	}
	// The parser accepts any order and normalizes list fields.
	reversed := make([]string, 0, len(scopes))
	for index := len(scopes) - 1; index >= 0; index-- {
		reversed = append(reversed, scopes[index])
	}
	if again, err := Parse(reversed); err != nil || !reflect.DeepEqual(again, want) {
		t.Fatalf("Parse(reversed) = %#v, %v", again, err)
	}
}

func TestParseFailsClosedOnMalformedProjection(t *testing.T) {
	base := sampleGrant().Scopes()
	replace := func(prefix, value string) []string {
		out := make([]string, 0, len(base))
		for _, scope := range base {
			if strings.HasPrefix(scope, prefix) {
				continue
			}
			out = append(out, scope)
		}
		if value != "" {
			out = append(out, prefix+value)
		}
		return out
	}
	cases := map[string][]string{
		"unknown prefix":         append(append([]string{}, base...), "sparkclaw.unknown:value"),
		"foreign scope":          append(append([]string{}, base...), "read:files"),
		"empty tool":             append(append([]string{}, base...), PrefixTool),
		"non-integer budget":     replace(PrefixMaxToolCalls, "many"),
		"negative budget":        replace(PrefixMaxToolCalls, "-1"),
		"non-integer output":     replace(PrefixMaxOutputBytes, "1k"),
		"unknown approval":       replace(PrefixApproval, "allow"),
		"missing approval":       replace(PrefixApproval, ""),
		"missing budget":         replace(PrefixMaxToolCalls, ""),
		"missing grant":          replace(PrefixGrant, ""),
		"grant without version":  replace(PrefixGrant, "grant_demo"),
		"grant with empty id":    replace(PrefixGrant, "@v1"),
		"grant with two markers": replace(PrefixGrant, "grant@v1@v2"),
		"repeated approval":      append(append([]string{}, base...), PrefixApproval+ApprovalDeny),
		"repeated purpose":       append(append([]string{}, base...), PrefixPurpose+"other"),
	}
	for name, scopes := range cases {
		if grant, err := Parse(scopes); err == nil {
			t.Errorf("%s: Parse() accepted %v as %#v", name, scopes, grant)
		}
	}
}

func TestForRunClosesMalformedJingSiRunsAndIgnoresOthers(t *testing.T) {
	if _, scoped := ForRun(app.AgentRun{}); scoped {
		t.Fatal("run without message context was treated as JingSi ingress")
	}
	other := app.AgentRun{MessageContext: &app.MessageRunContext{
		Source:        app.MessageSourceContext{Adapter: "weixin"},
		Authorization: app.MessageAuthorization{Scope: []string{"anything"}},
	}}
	if _, scoped := ForRun(other); scoped {
		t.Fatal("another adapter was treated as JingSi ingress")
	}
	run := app.AgentRun{MessageContext: &app.MessageRunContext{
		Source:        app.MessageSourceContext{Adapter: AdapterID},
		Authorization: app.MessageAuthorization{Scope: sampleGrant().Scopes()},
	}}
	grant, scoped := ForRun(run)
	if !scoped || !reflect.DeepEqual(grant, sampleGrant()) {
		t.Fatalf("ForRun() = %#v, %v", grant, scoped)
	}
	run.MessageContext.Authorization.Scope = append(run.MessageContext.Authorization.Scope, PrefixMaxToolCalls+"oops")
	grant, scoped = ForRun(run)
	if !scoped || !reflect.DeepEqual(grant, Closed()) {
		t.Fatalf("malformed JingSi run was not closed: %#v", grant)
	}
	if grant.AllowsTool(app.ToolDefinition{Name: "files.read"}) {
		t.Fatal("closed grant exposed a tool")
	}
}

func TestAllowsToolRequiresExactNameBudgetAndApproval(t *testing.T) {
	grant := sampleGrant()
	read := app.ToolDefinition{Name: "files.read", Directory: app.ToolDirectoryMetadata{Effects: []app.ToolEffect{app.ToolEffectWorkspaceRead}}}
	if !grant.AllowsTool(read) {
		t.Fatal("scoped read tool was hidden")
	}
	approval := read
	approval.RequiresApproval = true
	if !grant.AllowsTool(approval) {
		t.Fatal("ask policy hid an approval-requiring tool in scope")
	}
	other := read
	other.Name = "files.write"
	if grant.AllowsTool(other) {
		t.Fatal("tool outside tool_scope was exposed")
	}
	grant.ApprovalPolicy = ApprovalDeny
	if grant.AllowsTool(approval) {
		t.Fatal("deny policy exposed an approval-requiring tool")
	}
	if !grant.AllowsTool(read) {
		t.Fatal("deny policy hid a tool that needs no approval")
	}
	grant.MaxToolCalls = 0
	if grant.AllowsTool(read) {
		t.Fatal("zero tool-call budget exposed a tool")
	}
}

func TestAllowsToolNarrowsToGrantedDataAndNetworkScope(t *testing.T) {
	grant := sampleGrant() // data: memory.context, workspace.read; network: external.read
	grant.Tools = []string{"browser.read", "email.send", "files.write", "files.read", "reminders.list", "browser.assess_goal", "memory.search", "odd.tool"}
	definition := func(name string, effects ...app.ToolEffect) app.ToolDefinition {
		return app.ToolDefinition{Name: name, Directory: app.ToolDirectoryMetadata{Effects: effects}}
	}
	cases := []struct {
		name string
		def  app.ToolDefinition
		want bool
	}{
		{"external read within network scope", definition("browser.read", app.ToolEffectExternalRead), true},
		{"external interact outside network scope", definition("email.send", app.ToolEffectExternalInteract), false},
		{"workspace write outside data scope", definition("files.write", app.ToolEffectWorkspaceWrite), false},
		{"workspace read within data scope", definition("files.read", app.ToolEffectWorkspaceRead), true},
		{"local read outside data scope", definition("reminders.list", app.ToolEffectLocalRead), false},
		{"pure compute needs no scope", definition("browser.assess_goal", app.ToolEffectLocalCompute), true},
		{"no declared effect fails closed", definition("memory.search"), false},
		{"unknown effect fails closed", definition("odd.tool", app.ToolEffect("cloud.sync")), false},
		{"every effect must be covered", definition("files.read", app.ToolEffectWorkspaceRead, app.ToolEffectExternalInteract), false},
	}
	for _, tc := range cases {
		if got := grant.AllowsTool(tc.def); got != tc.want {
			t.Errorf("%s: AllowsTool = %v, want %v", tc.name, got, tc.want)
		}
	}
	// An unknown token in the granted lists widens nothing.
	grant.NetworkScope = []string{"anywhere"}
	if grant.AllowsTool(definition("email.send", app.ToolEffectExternalInteract)) {
		t.Fatal("unrecognized network token exposed an interacting tool")
	}
	if grant.AllowsTool(definition("browser.read", app.ToolEffectExternalRead)) {
		t.Fatal("unrecognized network token stood in for external.read")
	}
}

func TestEffectRequirementCoversEveryDeclaredEffect(t *testing.T) {
	want := map[app.ToolEffect]struct {
		family ScopeFamily
		token  string
	}{
		app.ToolEffectExternalRead:     {ScopeFamilyNetwork, "external.read"},
		app.ToolEffectExternalInteract: {ScopeFamilyNetwork, "external.interact"},
		app.ToolEffectWorkspaceRead:    {ScopeFamilyData, "workspace.read"},
		app.ToolEffectWorkspaceWrite:   {ScopeFamilyData, "workspace.write"},
		app.ToolEffectLocalRead:        {ScopeFamilyData, "local.read"},
		app.ToolEffectLocalWrite:       {ScopeFamilyData, "local.write"},
		app.ToolEffectLocalCompute:     {ScopeFamilyNone, ""},
	}
	for effect, expected := range want {
		family, token, known := EffectRequirement(effect)
		if !known || family != expected.family || token != expected.token {
			t.Errorf("EffectRequirement(%q) = %q, %q, %v; want %q, %q", effect, family, token, known, expected.family, expected.token)
		}
	}
	if _, _, known := EffectRequirement(app.ToolEffect("remote.exec")); known {
		t.Fatal("unknown effect was mapped")
	}
}

func isSorted(values []string) bool {
	for index := 1; index < len(values); index++ {
		if values[index-1] > values[index] {
			return false
		}
	}
	return true
}
