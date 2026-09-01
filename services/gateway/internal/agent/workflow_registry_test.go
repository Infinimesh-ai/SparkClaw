package agent

import (
	"slices"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/capability"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/semanticrouting"
)

func assertNoLegacyRoutingAudit(t *testing.T, events []app.AuditEvent) {
	t.Helper()
	for _, event := range events {
		if event.Type == "task_hint.generated" || event.Type == "task_hint.fallback" {
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
		{app.RouteDecision{CapabilityPath: []app.CapabilityID{"conversation", app.CapabilityConversationAnswer}, Slots: app.RouteSlots{Operation: app.RouteOperationAnswer, Query: "hello"}}, app.WorkflowConversationAnswer},
		{app.RouteDecision{CapabilityPath: []app.CapabilityID{"conversation", app.CapabilityConversationAnswer}, Slots: app.RouteSlots{Operation: app.RouteOperationPublish, Query: "send this file"}}, app.WorkflowConversationAnswer},
		{app.RouteDecision{CapabilityPath: []app.CapabilityID{"browser", app.CapabilityBrowserInternetSearch}, Slots: app.RouteSlots{Operation: app.RouteOperationSearch, FactScope: app.RouteFactScopeCurrentInternet, Query: "test"}}, app.WorkflowBrowserInternetSearch},
		{app.RouteDecision{CapabilityPath: []app.CapabilityID{"browser", app.CapabilityBrowserWeather}, Slots: app.RouteSlots{Operation: app.RouteOperationRead, FactScope: app.RouteFactScopeWeatherSnapshot, Query: "今天杭州天气", TargetKind: string(app.TargetKindLocation), TargetRef: "杭州", Location: "杭州"}, Facts: map[string]string{"location_source": "current_turn"}}, app.WorkflowBrowserWeather},
		{app.RouteDecision{CapabilityPath: []app.CapabilityID{"browser", app.CapabilityBrowserAutomation}, Slots: app.RouteSlots{Operation: app.RouteOperationOpen, TargetKind: "url", TargetRef: "https://example.com/"}, Facts: map[string]string{"url": "https://example.com/"}}, app.WorkflowBrowserAutomation},
		{app.RouteDecision{CapabilityPath: []app.CapabilityID{"browser", app.CapabilityBrowserPageRead}, Slots: app.RouteSlots{Operation: app.RouteOperationRead, Query: "read this page", TargetKind: "url", TargetRef: "https://example.com/"}, Facts: map[string]string{"url": "https://example.com/"}}, app.WorkflowBrowserPageRead},
		{app.RouteDecision{CapabilityPath: []app.CapabilityID{"browser", app.CapabilityBrowserInteraction}, Slots: app.RouteSlots{Operation: app.RouteOperationInteract, Query: "click Next", TargetKind: string(app.TargetKindBrowserCurrentTab), TargetRef: "selected"}}, app.WorkflowBrowserInteraction},
		{app.RouteDecision{CapabilityPath: []app.CapabilityID{"browser", app.CapabilityBrowserFormDraft}, Slots: app.RouteSlots{Operation: app.RouteOperationDraft, Query: "fill the name field with Alice", TargetKind: string(app.TargetKindBrowserCurrentTab), TargetRef: "selected"}}, app.WorkflowBrowserFormDraft},
		{app.RouteDecision{CapabilityPath: []app.CapabilityID{"document", app.CapabilityDocumentRead}, Slots: app.RouteSlots{Operation: app.RouteOperationRead, Query: "read test.txt", TargetKind: "workspace_path", TargetRef: "test.txt"}, Facts: map[string]string{"path": "test.txt", "document_format": app.DocumentFormatText}}, app.WorkflowDocumentRead},
		{app.RouteDecision{CapabilityPath: []app.CapabilityID{"document", app.CapabilityDocumentEdit}, Slots: app.RouteSlots{Operation: app.RouteOperationEdit, Query: "edit test.docx", TargetKind: "workspace_path", TargetRef: "test.docx"}, Facts: map[string]string{"path": "test.docx", "output_path": "test-2.docx", "document_format": app.DocumentFormatDOCX}}, app.WorkflowDocumentEdit},
	}
	for _, test := range tests {
		test.decision.SchemaVersion = app.RouteDecisionSchemaVersion
		test.decision.Status = app.RouteMatched
		test.decision.CatalogRevision = catalog.Revision()
		resolved, err := registry.Resolve(catalog, test.decision, "turn")
		if err != nil {
			t.Fatalf("resolve %v: %v", test.decision.CapabilityPath, err)
		}
		wantRevision := 1
		if test.want == app.WorkflowConversationAnswer {
			wantRevision = 3
		}
		if test.want == app.WorkflowBrowserAutomation || test.want == app.WorkflowBrowserInteraction {
			wantRevision = app.BrowserWorkflowRevision3
		}
		if test.want == app.WorkflowBrowserInternetSearch || test.want == app.WorkflowBrowserFormDraft {
			wantRevision = 2
		}
		if test.want == app.WorkflowBrowserWeather {
			wantRevision = 3
		}
		if test.want == app.WorkflowDocumentRead {
			wantRevision = 4
		}
		if test.want == app.WorkflowDocumentEdit {
			wantRevision = 7
		}
		if resolved.Profile.ID() != test.want || resolved.Plan.ProfileID != test.want || resolved.Plan.ProfileRevision != wantRevision {
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

func TestWorkflowRegistryDoesNotResumeRetiredDocumentEditR5(t *testing.T) {
	if _, err := defaultWorkflowProfileRegistry().Get(app.WorkflowDocumentEdit, 5); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("retired document.edit r5 remained resumable: %v", err)
	}
}

func TestWorkflowSemanticGraphIsCatalogValidatedAndCoversScheduleVariants(t *testing.T) {
	catalog := capability.MustDefaultCatalog()
	graph, err := defaultWorkflowProfileRegistry().SemanticGraph(catalog)
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[string]app.RouteOperation{
		"schedule.manage#create": app.RouteOperationCreate,
		"schedule.manage#read":   app.RouteOperationRead,
		"schedule.manage#edit":   app.RouteOperationEdit,
		"schedule.manage#delete": app.RouteOperationDelete,
	}
	for _, candidate := range graph.Candidates() {
		want, ok := wanted[candidate.ID]
		if !ok {
			continue
		}
		if candidate.Route.Operation != want || len(candidate.CapabilityPath) != 2 || candidate.CapabilityPath[1] != app.CapabilityScheduleManage {
			t.Fatalf("schedule candidate %q is not Catalog-grounded: %#v", candidate.ID, candidate)
		}
		delete(wanted, candidate.ID)
	}
	if len(wanted) != 0 {
		t.Fatalf("semantic graph is missing schedule variants: %#v", wanted)
	}
	if candidate, ok := graph.Candidate("conversation.answer#answer"); !ok || candidate.Route.Operation != app.RouteOperationAnswer {
		t.Fatal("semantic graph is missing the timer-eligible conversation candidate")
	}
	if candidate, ok := graph.Candidate("conversation.answer#publish"); !ok || candidate.Route.Operation != app.RouteOperationPublish {
		t.Fatal("semantic graph is missing the ordinary multipart message candidate")
	}
}

func TestFastGraphProjectionCarriesProfileEmbeddingAndTreeSemantics(t *testing.T) {
	graph, err := defaultWorkflowProfileRegistry().SemanticGraph(capability.MustDefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	candidate, ok := graph.Candidate("schedule.manage#edit")
	if !ok {
		t.Fatal("schedule edit semantic candidate is missing")
	}
	projected := treeGraphProjection([]semanticrouting.Candidate{candidate})
	if len(projected) != 1 {
		t.Fatalf("projection count=%d want 1", len(projected))
	}
	path, pathOK := projected[0]["capability_path"].([]app.CapabilityID)
	embedTexts, embedOK := projected[0]["positive_semantics"].([]string)
	hardNegatives, negativeOK := projected[0]["hard_negatives"].([]string)
	if !pathOK || !slices.Equal(path, candidate.CapabilityPath) {
		t.Fatalf("Tree projection lost the Catalog-owned domain path: %#v", projected[0])
	}
	if !embedOK || !slices.Equal(embedTexts, candidate.EmbedTexts) {
		t.Fatalf("Tree projection lost embedding examples: %#v", projected[0])
	}
	if projected[0]["semantic_boundary"] != candidate.TreeDescription {
		t.Fatalf("Tree projection lost tree reasoning text: %#v", projected[0])
	}
	if !negativeOK || !slices.Equal(hardNegatives, candidate.HardNegatives) {
		t.Fatalf("Tree projection lost sibling hard negatives: %#v", projected[0])
	}
}

func TestTreeCandidateParserRejectsFieldsOutsideScoreContract(t *testing.T) {
	for _, raw := range []string{
		`{"graph_revision":"revision","candidates":[],"route":{}}`,
		`{"graph_revision":"revision","candidates":[{"candidate_id":"x#read","tree_score":0.9,"reason_code":"x"}]}`,
		`{"graph_revision":"revision","candidates":[{"candidate_id":"x#read","tree_score":0.9,"workflow_id":"x"}]}`,
		`{"graph_revision":"revision","candidates":[{"candidate_id":"x#read","tree_score":0.9,"tool":"web.search"}]}`,
	} {
		if _, err := parseTreeRoutingOutput(raw); err == nil {
			t.Fatalf("forbidden routing field was accepted: %s", raw)
		}
	}
}

func TestTreeCandidateParserFindsStrictObjectAfterMalformedProse(t *testing.T) {
	raw := "I first considered {not-json}, then returned:\n```json\n" +
		`{"graph_revision":"revision","candidates":[{"candidate_id":"conversation.answer#answer","tree_score":0.9}]}` +
		"\n```"
	decision, err := parseTreeRoutingOutput(raw)
	if err != nil {
		t.Fatal(err)
	}
	if decision.GraphRevision != "revision" || len(decision.Candidates) != 1 || decision.Candidates[0].CandidateID != "conversation.answer#answer" {
		t.Fatalf("strict routing object was not extracted intact: %#v", decision)
	}
}

func TestTreeCandidateValidationRejectsInvalidOutput(t *testing.T) {
	catalog := capability.MustDefaultCatalog()
	graph, err := defaultWorkflowProfileRegistry().SemanticGraph(catalog)
	if err != nil {
		t.Fatal(err)
	}
	eligible := graph.EligibleCandidates(app.MessageSourceWeb)
	valid := make([]treeRoutingCandidate, 0, len(eligible))
	for _, candidate := range eligible {
		valid = append(valid, treeRoutingCandidate{CandidateID: candidate.ID, TreeScore: testTreeScore(0.9)})
	}
	if err := validateTreeRoutingOutput(treeRoutingOutput{GraphRevision: graph.Revision(), Candidates: valid}, graph.Revision(), eligible); err != nil {
		t.Fatalf("complete Tree output was rejected: %v", err)
	}
	unknown := append([]treeRoutingCandidate(nil), valid...)
	unknown[0].CandidateID = "unknown#candidate"
	duplicate := append([]treeRoutingCandidate(nil), valid...)
	duplicate[1] = duplicate[0]
	missingScore := append([]treeRoutingCandidate(nil), valid...)
	missingScore[0].TreeScore = nil
	invalidScore := append([]treeRoutingCandidate(nil), valid...)
	invalidScore[0].TreeScore = testTreeScore(1.1)
	tests := []treeRoutingOutput{
		{GraphRevision: "stale", Candidates: valid},
		{GraphRevision: graph.Revision(), Candidates: unknown},
		{GraphRevision: graph.Revision(), Candidates: duplicate},
		{GraphRevision: graph.Revision(), Candidates: valid[:len(valid)-1]},
		{GraphRevision: graph.Revision(), Candidates: missingScore},
		{GraphRevision: graph.Revision(), Candidates: invalidScore},
	}
	for index, output := range tests {
		if err := validateTreeRoutingOutput(output, graph.Revision(), eligible); err == nil {
			t.Fatalf("invalid Tree output %d was accepted: %#v", index, output)
		}
	}
}

func testTreeScore(score float64) *float64 {
	return &score
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

func TestBrowserRevision1ContractsAreRetiredAndRevision2RemainsCompatible(t *testing.T) {
	registry := defaultWorkflowProfileRegistry()
	for _, id := range []app.WorkflowID{app.WorkflowBrowserAutomation, app.WorkflowBrowserInteraction} {
		if _, err := registry.Get(id, 1); err == nil || !strings.Contains(err.Error(), "not registered") {
			t.Fatalf("retired browser contract %s r1 remained resumable: %v", id, err)
		}
		if _, err := registry.Get(id, app.BrowserWorkflowRevision2); err != nil {
			t.Fatalf("compatible browser contract %s r2 is not registered: %v", id, err)
		}
		profile, err := registry.Get(id, app.BrowserWorkflowRevision3)
		if err != nil {
			t.Fatalf("current browser contract %s r3 is not registered: %v", id, err)
		}
		if profile.Revision() != app.BrowserWorkflowRevision3 {
			t.Fatalf("browser contract %s resolved revision %d", id, profile.Revision())
		}
	}
}

func TestLegacyProfilePlansDoNotGainObservationReadOnResume(t *testing.T) {
	registry := defaultWorkflowProfileRegistry()
	legacy, err := registry.Get(app.WorkflowBrowserInternetSearch, 1)
	if err != nil {
		t.Fatal(err)
	}
	route := app.RouteDecision{Slots: app.RouteSlots{Operation: app.RouteOperationSearch, Query: "current facts"}}
	_, legacyPlan, err := legacy.Resolve(route, "turn")
	if err != nil {
		t.Fatal(err)
	}
	if len(legacyPlan.Nodes) != 1 || len(legacyPlan.Nodes[0].InitialScope.SupportRequirements) != 0 {
		t.Fatalf("legacy plan was widened during resolution: %#v", legacyPlan.Nodes)
	}
	state := newWorkflowState(route, app.ReturnRoute{}, app.IntentEnvelope{}, legacyPlan)
	if len(state.Nodes[legacyPlan.InitialNodeIDs[0]].CurrentScope.SupportRequirements) != 0 {
		t.Fatalf("legacy persisted state gained a support capability: %#v", state.Nodes)
	}

	currentRoute := route
	currentRoute.SchemaVersion = app.RouteDecisionSchemaVersion
	currentRoute.Status = app.RouteMatched
	currentRoute.CatalogRevision = capability.MustDefaultCatalog().Revision()
	currentRoute.CapabilityPath = []app.CapabilityID{"browser", app.CapabilityBrowserInternetSearch}
	currentRoute.Slots.FactScope = app.RouteFactScopeCurrentInternet
	resolved, err := registry.Resolve(capability.MustDefaultCatalog(), currentRoute, "turn")
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Plan.Nodes[0].InitialScope.SupportRequirements) != 1 || resolved.Plan.Nodes[0].InitialScope.SupportRequirements[0].Name != app.ToolCapabilityObservationRead {
		t.Fatalf("current plan omitted its frozen support requirement: %#v", resolved.Plan.Nodes[0].InitialScope)
	}
}

func TestWorkflowPlanRejectsScopedCapabilityWithoutStageExposure(t *testing.T) {
	profile := browserInteractionProfile{}
	resolve := func() (app.IntentEnvelope, app.WorkflowPlan) {
		intent, plan, err := profile.Resolve(app.RouteDecision{Slots: app.RouteSlots{
			Operation: app.RouteOperationInteract, Query: "点击当前页面的下一步按钮",
			TargetKind: string(app.TargetKindBrowserCurrentTab), TargetRef: "selected",
		}}, "turn")
		if err != nil {
			t.Fatal(err)
		}
		return intent, plan
	}

	intent, plan := resolve()
	plan.Nodes[0].StageCapabilities = plan.Nodes[0].StageCapabilities[:len(plan.Nodes[0].StageCapabilities)-1]
	if err := validateWorkflowPlan(intent, profile, plan); err == nil || !strings.Contains(err.Error(), "no capability rule") {
		t.Fatalf("missing stage exposure did not invalidate the workflow plan: %v", err)
	}

	intent, plan = resolve()
	for index := range plan.Nodes[0].StageCapabilities {
		if plan.Nodes[0].StageCapabilities[index].Stage == "open_new" ||
			plan.Nodes[0].StageCapabilities[index].Stage == "present_visible" {
			plan.Nodes[0].StageCapabilities[index].Capabilities = []string{app.ToolCapabilityBrowserFocus}
		}
	}
	if err := validateWorkflowPlan(intent, profile, plan); err == nil || !strings.Contains(err.Error(), "no stage exposes") {
		t.Fatalf("scoped capability without a matching stage did not invalidate the workflow plan: %v", err)
	}
}

func TestWorkflowStatePersistsRouteIdentity(t *testing.T) {
	catalog := capability.MustDefaultCatalog()
	route := app.RouteDecision{
		SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched, CatalogRevision: catalog.Revision(),
		CapabilityPath: []app.CapabilityID{"document", app.CapabilityDocumentRead},
		Slots:          app.RouteSlots{Operation: app.RouteOperationRead, Query: "read note.txt", TargetKind: "workspace_path", TargetRef: "note.txt"},
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
func (futureWorkflowProfile) RoutingSemantics() workflowRoutingSemantics {
	return workflowRoutingSemantics{Variants: []workflowRoutingVariant{{
		Key: "translate", Route: workflowRouteTemplate{Operation: "translate"},
		EmbedTexts: []string{"translate this"}, TreeDescription: "Translate one supplied passage.",
	}}}
}
func (futureWorkflowProfile) Finalization() workflowFinalizationMode {
	return workflowFinalizationGrounded
}
func (p futureWorkflowProfile) Resolve(route app.RouteDecision, sourceTurnID string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	intent := singleObjectiveIntent(sourceTurnID, app.IntentDomainWeb, app.IntentOperation("translate"), app.TargetRef{Kind: app.TargetKindNone}, app.DataScopePublic)
	nodeID := app.WorkflowNodeID("translate")
	return intent, app.WorkflowPlan{SchemaVersion: 1, ProfileID: p.ID(), ProfileRevision: 1, InitialNodeIDs: []app.WorkflowNodeID{nodeID}, Completion: app.CompletionEvidence,
		Nodes: []app.WorkflowNode{{ID: nodeID, InitialStage: "translate", Goal: app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "translate", Completion: app.CompletionEvidence},
			InitialScope: app.CapabilityScope{Requirements: []app.CapabilityRequirement{{Name: "future.translate"}}}, AllowedRisks: []app.RiskLevel{app.RiskRead}, MaxAttempts: 1}}}, nil
}
func (futureWorkflowProfile) Prepare(*app.WorkflowState) (workflowPreparation, error) {
	return workflowPreparation{}, nil
}
func (futureWorkflowProfile) Assess(*app.WorkflowState, app.ToolOutcome) app.NodeAssessment {
	return app.NodeAssessment{Status: app.AssessmentComplete}
}
func (futureWorkflowProfile) StageContext(*app.WorkflowState) workflowStageContext {
	return workflowStageContext{}
}
func (futureWorkflowProfile) TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string {
	return ""
}

func TestWorkflowSemanticGraphRoutesFutureRegistrationWithoutCoreSwitch(t *testing.T) {
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
	graph, err := registry.SemanticGraph(catalog)
	if err != nil {
		t.Fatal(err)
	}
	candidate, ok := graph.Candidate("future.translate#translate")
	if !ok || len(candidate.CapabilityPath) != 2 || candidate.CapabilityPath[1] != "future.translate" {
		t.Fatalf("future workflow was not compiled through registrations: %#v", candidate)
	}
	decision := app.RouteDecision{SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched, CatalogRevision: catalog.Revision(),
		CapabilityPath: candidate.CapabilityPath, Slots: app.RouteSlots{Operation: candidate.Route.Operation, Query: "translate this"}}
	if _, err := registry.Resolve(catalog, decision, "turn"); err != nil {
		t.Fatalf("future workflow was not resolved generically: %v", err)
	}
}
