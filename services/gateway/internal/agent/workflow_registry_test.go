package agent

import (
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/capability"
)

func assertNoLegacyRoutingAudit(t *testing.T, events []app.AuditEvent) {
	t.Helper()
	for _, event := range events {
		if event.Type == "task_hint.generated" || event.Type == "task_hint.fallback" || event.Type == "react.visible_tools_expanded" {
			t.Fatalf("migrated workflow used a legacy routing path: %#v", event)
		}
	}
}

func TestWorkflowRegistryResolvesExactlyOneContractPerLeaf(t *testing.T) {
	registry := defaultWorkflowProfileRegistry()
	catalog := capability.MustDefaultCatalog()
	tests := []struct {
		path []app.CapabilityID
		want app.WorkflowID
	}{
		{[]app.CapabilityID{"browser", app.CapabilityBrowserSearch}, app.WorkflowBrowserSearch},
		{[]app.CapabilityID{"browser", app.CapabilityBrowserAutomation}, app.WorkflowBrowserAutomation},
		{[]app.CapabilityID{"document", app.CapabilityDocumentInformation}, app.WorkflowDocumentInformation},
		{[]app.CapabilityID{"document", app.CapabilityDocumentProcessing}, app.WorkflowDocumentProcessing},
	}
	for _, test := range tests {
		decision := app.RouteDecision{SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched, CatalogRevision: catalog.Revision(), CapabilityPath: test.path, Slots: semanticSlotsForRoute(test.path, "test request", nil)}
		resolved, err := registry.Resolve(catalog, decision, "turn")
		if err != nil {
			t.Fatalf("resolve %v: %v", test.path, err)
		}
		if resolved.Profile.ID() != test.want || resolved.Plan.ProfileID != test.want || resolved.Plan.ProfileRevision != 1 {
			t.Fatalf("leaf %v resolved wrong contract: %#v", test.path, resolved)
		}
	}
}

func TestWorkflowRegistryRejectsStaleInventedAndUnmatchedRoutes(t *testing.T) {
	registry := defaultWorkflowProfileRegistry()
	catalog := capability.MustDefaultCatalog()
	tests := []app.RouteDecision{
		{SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched, CatalogRevision: "stale", CapabilityPath: []app.CapabilityID{"browser", app.CapabilityBrowserSearch}},
		{SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched, CatalogRevision: catalog.Revision(), CapabilityPath: []app.CapabilityID{"browser", app.CapabilityDocumentInformation}},
		{SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteUnmatched, CatalogRevision: catalog.Revision()},
	}
	for _, decision := range tests {
		if _, err := registry.Resolve(catalog, decision, "turn"); err == nil {
			t.Fatalf("invalid decision was dispatched: %#v", decision)
		}
	}
}

func TestRouteDecisionParserRejectsToolAndWorkflowFields(t *testing.T) {
	for _, raw := range []string{
		`{"schema_version":1,"status":"matched","catalog_revision":"revision","capability_path":["browser","browser.search"],"tool":"web.search"}`,
		`{"schema_version":1,"status":"matched","catalog_revision":"revision","capability_path":["browser","browser.search"],"workflow_id":"browser.search"}`,
	} {
		if _, err := parseRouteDecision(raw); err == nil {
			t.Fatalf("forbidden routing field was accepted: %s", raw)
		}
	}
}

func TestFastRouteCannotRewriteDeterministicFacts(t *testing.T) {
	catalog := capability.MustDefaultCatalog()
	runtime := Runtime{capabilities: catalog}
	fallback := app.RouteDecision{
		SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched, CatalogRevision: catalog.Revision(),
		CapabilityPath: []app.CapabilityID{"browser", app.CapabilityBrowserSearch},
		Slots:          app.RouteSlots{Operation: app.RouteOperationRead, TargetKind: "url", TargetRef: "https://example.com/allowed"},
		Facts:          map[string]string{"url": "https://example.com/allowed"}, Confidence: 0.8,
	}
	candidate := fallback
	candidate.Facts = map[string]string{"url": "https://attacker.example/rewritten"}
	candidate.Slots.TargetRef = "https://attacker.example/rewritten"
	normalized := runtime.normalizeFastRoute(candidate, fallback, "read the supplied URL")
	if normalized.Facts["url"] != fallback.Facts["url"] || normalized.Slots.TargetRef != fallback.Facts["url"] {
		t.Fatalf("Fast route rewrote deterministic URL: %#v", normalized)
	}
}

func TestLegacyWorkflowIdentityFailsClosedInsteadOfBeingReinterpreted(t *testing.T) {
	registry := defaultWorkflowProfileRegistry()
	for _, id := range []app.WorkflowID{app.WorkflowWebPublicResearch, app.WorkflowWebExplicitURL, app.WorkflowWorkspaceSearch, app.WorkflowWorkspaceRead} {
		if _, err := registry.Get(id); err == nil || !strings.Contains(err.Error(), "not registered") {
			t.Fatalf("legacy workflow %q did not fail closed: %v", id, err)
		}
	}
}

func TestWorkflowStatePersistsRouteIdentity(t *testing.T) {
	catalog := capability.MustDefaultCatalog()
	route := app.RouteDecision{SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched, CatalogRevision: catalog.Revision(), CapabilityPath: []app.CapabilityID{"document", app.CapabilityDocumentInformation}, Slots: app.RouteSlots{Operation: app.RouteOperationSearch}}
	resolved, err := defaultWorkflowProfileRegistry().Resolve(catalog, route, "turn")
	if err != nil {
		t.Fatal(err)
	}
	returnRoute := app.ReturnRoute{Mode: app.ReturnToSource, SourceEndpointID: "web:session"}
	state := newWorkflowState(route, returnRoute, resolved.Intent, resolved.Plan)
	if state.Route.CatalogRevision != catalog.Revision() || state.Route.CapabilityPath[1] != app.CapabilityDocumentInformation || state.ReturnRoute.SourceEndpointID != "web:session" {
		t.Fatalf("workflow state lost route identity: %#v", state)
	}
}
