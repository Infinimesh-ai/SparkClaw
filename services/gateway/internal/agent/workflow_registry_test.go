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
		decision app.RouteDecision
		want     app.WorkflowID
	}{
		{app.RouteDecision{CapabilityPath: []app.CapabilityID{"browser", app.CapabilityBrowserInternetSearch}, Slots: app.RouteSlots{Operation: app.RouteOperationSearch, Query: "test"}}, app.WorkflowBrowserInternetSearch},
		{app.RouteDecision{CapabilityPath: []app.CapabilityID{"browser", app.CapabilityBrowserAutomation}, Slots: app.RouteSlots{Operation: app.RouteOperationOpen, TargetKind: "url", TargetRef: "https://example.com/"}, Facts: map[string]string{"url": "https://example.com/"}}, app.WorkflowBrowserAutomation},
		{app.RouteDecision{CapabilityPath: []app.CapabilityID{"document", app.CapabilityDocumentRead}, Slots: app.RouteSlots{Operation: app.RouteOperationRead, TargetKind: "workspace_path", TargetRef: "test.txt"}, Facts: map[string]string{"path": "test.txt", "document_format": app.DocumentFormatText}}, app.WorkflowDocumentRead},
		{app.RouteDecision{CapabilityPath: []app.CapabilityID{"document", app.CapabilityDocumentEdit}, Slots: app.RouteSlots{Operation: app.RouteOperationEdit, TargetKind: "workspace_path", TargetRef: "test.docx"}, Facts: map[string]string{"path": "test.docx", "output_path": "test-sparkclaw-edit.docx", "document_format": app.DocumentFormatDOCX, "document_operation": "replace_paragraph"}}, app.WorkflowDocumentEdit},
	}
	for _, test := range tests {
		test.decision.SchemaVersion = app.RouteDecisionSchemaVersion
		test.decision.Status = app.RouteMatched
		test.decision.CatalogRevision = catalog.Revision()
		resolved, err := registry.Resolve(catalog, test.decision, "turn")
		if err != nil {
			t.Fatalf("resolve %v: %v", test.decision.CapabilityPath, err)
		}
		if resolved.Profile.ID() != test.want || resolved.Plan.ProfileID != test.want || resolved.Plan.ProfileRevision != 1 {
			t.Fatalf("leaf %v resolved wrong contract: %#v", test.decision.CapabilityPath, resolved)
		}
	}
}

func TestWorkflowRegistryRejectsStaleInventedAndUnmatchedRoutes(t *testing.T) {
	registry := defaultWorkflowProfileRegistry()
	catalog := capability.MustDefaultCatalog()
	tests := []app.RouteDecision{
		{SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched, CatalogRevision: "stale", CapabilityPath: []app.CapabilityID{"browser", app.CapabilityBrowserInternetSearch}},
		{SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched, CatalogRevision: catalog.Revision(), CapabilityPath: []app.CapabilityID{"browser", app.CapabilityDocumentRead}},
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
		`{"schema_version":1,"status":"matched","catalog_revision":"revision","capability_path":["browser","browser.internet_search"],"tool":"web.search"}`,
		`{"schema_version":1,"status":"matched","catalog_revision":"revision","capability_path":["browser","browser.internet_search"],"workflow_id":"browser.internet_search"}`,
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
		CapabilityPath: []app.CapabilityID{"browser", app.CapabilityBrowserAutomation},
		Slots:          app.RouteSlots{Operation: app.RouteOperationOpen, TargetKind: "url", TargetRef: "https://example.com/allowed"},
		Facts:          map[string]string{"url": "https://example.com/allowed"}, Confidence: 0.8,
	}
	candidate := fallback
	candidate.Facts = map[string]string{"url": "https://attacker.example/rewritten"}
	candidate.Slots.TargetRef = "https://attacker.example/rewritten"
	normalized := runtime.normalizeFastRoute(candidate, fallback)
	if normalized.Facts["url"] != fallback.Facts["url"] || normalized.Slots.TargetRef != fallback.Facts["url"] {
		t.Fatalf("Fast route rewrote deterministic URL: %#v", normalized)
	}
}

func TestLegacyWorkflowIdentityFailsClosedInsteadOfBeingReinterpreted(t *testing.T) {
	registry := defaultWorkflowProfileRegistry()
	for _, id := range []app.WorkflowID{
		app.WorkflowLegacyBrowserSearch, app.WorkflowLegacyDocumentInformation, app.WorkflowLegacyDocumentProcessing,
		app.WorkflowWebPublicResearch, app.WorkflowWebExplicitURL, app.WorkflowWorkspaceSearch, app.WorkflowWorkspaceRead,
	} {
		if _, err := registry.Get(id); err == nil || !strings.Contains(err.Error(), "not registered") {
			t.Fatalf("legacy workflow %q did not fail closed: %v", id, err)
		}
	}
}

func TestWorkflowStatePersistsRouteIdentity(t *testing.T) {
	catalog := capability.MustDefaultCatalog()
	route := app.RouteDecision{
		SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched, CatalogRevision: catalog.Revision(),
		CapabilityPath: []app.CapabilityID{"document", app.CapabilityDocumentRead},
		Slots:          app.RouteSlots{Operation: app.RouteOperationRead, TargetKind: "workspace_path", TargetRef: "note.txt"},
		Facts:          map[string]string{"path": "note.txt", "document_format": app.DocumentFormatText},
	}
	resolved, err := defaultWorkflowProfileRegistry().Resolve(catalog, route, "turn")
	if err != nil {
		t.Fatal(err)
	}
	returnRoute := app.ReturnRoute{Mode: app.ReturnToSource, SourceEndpointID: "web:session"}
	state := newWorkflowState(route, returnRoute, resolved.Intent, resolved.Plan)
	if state.Route.CatalogRevision != catalog.Revision() || state.Route.CapabilityPath[1] != app.CapabilityDocumentRead || state.ReturnRoute.SourceEndpointID != "web:session" {
		t.Fatalf("workflow state lost route identity: %#v", state)
	}
}

type futureWorkflowProfile struct{}

func (futureWorkflowProfile) ID() app.WorkflowID           { return "future.translate" }
func (futureWorkflowProfile) Revision() int                { return 1 }
func (futureWorkflowProfile) Capability() app.CapabilityID { return "future.translate" }
func (futureWorkflowProfile) Recognize(input workflowRecognitionContext) (workflowRecognition, bool) {
	if input.Content != "translate this" {
		return workflowRecognition{}, false
	}
	return workflowRecognition{Slots: app.RouteSlots{Operation: "translate", Query: input.Content}, Confidence: 1}, true
}
func (p futureWorkflowProfile) Resolve(route app.RouteDecision, sourceTurnID string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	intent := singleObjectiveIntent(sourceTurnID, app.IntentDomainWeb, app.IntentOperation("translate"), app.TargetRef{Kind: app.TargetKindNone}, app.DataScopePublic)
	nodeID := app.WorkflowNodeID("translate")
	return intent, app.WorkflowPlan{SchemaVersion: 1, ProfileID: p.ID(), ProfileRevision: 1, InitialNodeIDs: []app.WorkflowNodeID{nodeID}, Completion: app.CompletionEvidence,
		Nodes: []app.WorkflowNode{{ID: nodeID, InitialStage: "translate", Goal: app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "translate", Completion: app.CompletionEvidence},
			InitialScope: app.CapabilityScope{Requirements: []app.CapabilityRequirement{{Name: "future.translate"}}}, AllowedRisks: []app.RiskLevel{app.RiskRead}, MaxAttempts: 1}}}, nil
}
func (futureWorkflowProfile) Prepare(*app.WorkflowState) (app.TransitionID, bool, error) {
	return "", false, nil
}
func (futureWorkflowProfile) Assess(*app.WorkflowState, app.ToolOutcome) app.NodeAssessment {
	return app.NodeAssessment{Status: app.AssessmentComplete}
}
func (futureWorkflowProfile) Hint(*app.WorkflowState) workflowExecutionHint {
	return workflowExecutionHint{}
}
func (futureWorkflowProfile) TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string {
	return ""
}

func TestWorkflowRecognitionRoutesFutureRegistrationWithoutCoreSwitch(t *testing.T) {
	workflow := app.WorkflowContractRef{ID: "future.translate", Revision: 1}
	catalog, err := capability.NewCatalog("future", []capability.Node{
		{ID: capability.RootID, Kind: capability.NodeBranch, Description: "root"},
		{ID: "future", ParentID: capability.RootID, Kind: capability.NodeBranch, Description: "future"},
		{ID: "future.translate", ParentID: "future", Kind: capability.NodeLeaf, Description: "translate", Workflow: &workflow,
			Route: &capability.RouteContract{Operations: []app.RouteOperation{"translate"}, RequireQuery: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	registry := newWorkflowProfileRegistry(futureWorkflowProfile{})
	decision, err := registry.Recognize(catalog, workflowRecognitionContext{SourceTurnID: "turn", Content: "translate this"})
	if err != nil || decision.Status != app.RouteMatched || len(decision.CapabilityPath) != 2 || decision.CapabilityPath[1] != "future.translate" {
		t.Fatalf("future workflow was not recognized through registrations: %#v %v", decision, err)
	}
	if _, err := registry.Resolve(catalog, decision, "turn"); err != nil {
		t.Fatalf("future workflow was not resolved generically: %v", err)
	}
}
