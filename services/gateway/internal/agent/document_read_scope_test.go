package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func TestScopedDocumentReadStartsWithStructureForShortSource(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	if err := os.WriteFile(filepath.Join(session.WorkspaceRoot, "short.md"), []byte("# Notice\n\nThe exception applies on Friday.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := runtime.HandleMessage(t.Context(), session.ID, "Read short.md")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, call := range testListToolCalls(st, session.ID) {
		if call.RunID == result.Run.ID {
			names = append(names, call.Tool)
		}
	}
	if strings.Join(names, ",") != "document.outline,document.read_range" {
		t.Fatalf("short document bypassed structure: %v", names)
	}
	facts := result.Run.Workflow.Route.Facts
	if result.Run.Workflow.Plan.ProfileRevision != 10 || result.Run.Workflow.Status != app.WorkflowStatusSucceeded || facts[documentReadFactPrefix+"scope_status"] != "complete" || facts[documentReadFactPrefix+"source_sha256"] == "" || facts[documentReadFactPrefix+"scope_selector"] != `{"all":true}` {
		t.Fatalf("declared source scope missing: %#v", result.Run.Workflow)
	}
}

func TestScopedDocumentReadContinuesOneFrozenRange(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) { cfg.config.Runtime.StageEvidenceMaxBytes = 16000 })
	defer closeRuntime()
	text := "# Long node\n\n" + strings.Repeat("bounded source content ", 6000) + "LATE_EXCEPTION\n"
	if err := os.WriteFile(filepath.Join(session.WorkspaceRoot, "range.md"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := runtime.HandleMessage(t.Context(), session.ID, "Read range.md")
	if err != nil {
		t.Fatal(err)
	}
	var ranges []app.ToolCall
	for _, call := range testListToolCalls(st, session.ID) {
		if call.RunID == result.Run.ID && call.Tool == "document.read_range" {
			ranges = append(ranges, call)
		}
	}
	if len(ranges) < 2 || result.Run.Workflow.Status != app.WorkflowStatusSucceeded {
		for _, call := range ranges {
			t.Logf("range status=%s error=%s", call.Status, call.Error)
		}
		t.Fatalf("range did not continue: calls=%d status=%s", len(ranges), result.Run.Workflow.Status)
	}
	for i, call := range ranges {
		if call.Arguments["source_sha256"] != result.Run.Workflow.Route.Facts[documentReadFactPrefix+"source_sha256"] {
			t.Fatal("continuation changed source pin")
		}
		if i > 0 && (call.Arguments["cursor"] == nil || len(documentReadSelector(call.Arguments)) != 0) {
			t.Fatalf("continuation did not reuse its frozen selector: %#v", call.Arguments)
		}
	}
	last, _ := anyMap(ranges[len(ranges)-1].Result)
	if !strings.Contains(stringValue(last["content"]), "LATE_EXCEPTION") || last["read_complete"] != true {
		t.Fatalf("late range missing: %#v", last)
	}
}

func TestScopedDocumentReadTaskFactsSurviveFileStoreReopen(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "state.json")
	st, err := store.NewFileStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := agentTestConfig()
	cfg.Workspaces.DefaultRoot, cfg.Workspaces.Allowlist = root, []string{root}
	cfg.Storage.TraceDir, cfg.Storage.ArtifactDir = filepath.Join(root, "traces"), filepath.Join(root, "artifacts")
	hub := toolhub.New(cfg, st)
	defer hub.Close()
	runtime := NewRuntime(st, hub, policy.New(cfg), modelrouter.New(cfg), nil)
	session := storetest.MustCreateSessionWithScope(t, st, "scope reopen", app.DefaultOwnerID, root, "web", false)
	if err := os.WriteFile(filepath.Join(root, "values.md"), []byte("# Values\n\nTwo boxes contain 3 and 4 items.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := runtime.HandleMessage(t.Context(), session.ID, "Read values.md\nMOCK_STEP_RESPONSE:{\"type\":\"action\",\"tool\":\"document.read_range\",\"arguments\":{\"all\":true,\"task_mode\":\"calculation\",\"scope_extent\":\"whole_document\",\"scope_reason\":\"Use the declared dataset.\"}}")
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := store.NewFileStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	run, found, err := reopened.GetRun(t.Context(), result.Run.ID)
	if err != nil || !found || run.Workflow == nil {
		t.Fatalf("run lost after reopen: %v", err)
	}
	facts := run.Workflow.Route.Facts
	for _, key := range []string{"task_mode", "scope_selector", "coverage", "source_sha256", "structure_generation", "scope_selection_call_id"} {
		if facts[documentReadFactPrefix+key] == "" {
			t.Errorf("scope fact %s was not persisted", key)
		}
	}
	if facts[documentReadFactPrefix+"task_mode"] != "calculation" {
		t.Fatalf("program replaced the structured model mode: %v", facts)
	}
}

func TestLegacyDocumentReadProfilesRemainDirectAndKeepAssessment(t *testing.T) {
	registry := defaultWorkflowProfileRegistry()
	route := app.RouteDecision{Facts: map[string]string{"document_format": app.DocumentFormatText}}
	for _, revision := range []int{4, 5} {
		profile, err := registry.Get(app.WorkflowDocumentRead, revision)
		if err != nil {
			t.Fatal(err)
		}
		_, plan, err := profile.Resolve(route, "turn")
		if err != nil || plan.ProfileRevision != revision || plan.Nodes[1].InitialStage != documentReadStage || plan.Nodes[1].InitialScope.Requirements[0].Name != app.ToolCapabilityDocumentRead {
			t.Fatalf("legacy plan reinterpreted: r%d %#v %v", revision, plan, err)
		}
		assessment := profile.Assess(nil, app.ToolOutcome{Signals: []app.OutcomeSignal{app.OutcomeSignalContentAvailable}})
		if assessment.Status != app.AssessmentComplete {
			t.Fatalf("legacy assessment changed: r%d %#v", revision, assessment)
		}
	}
}

func TestScopedDocumentReadDoesNotCompleteFromContentSignal(t *testing.T) {
	for _, next := range []string{"", "cursor-2"} {
		state := scopedReadState()
		outcome := scopedReadOutcome(false, next)
		assessment := (documentReadProfile{}).Assess(state, outcome)
		if assessment.Status == app.AssessmentComplete || state.Route.Facts[documentReadFactPrefix+"scope_status"] != "partial" {
			t.Fatalf("partial content became complete: %#v", assessment)
		}
		if next != "" && assessment.Status != app.AssessmentNeedsMoreEvidence {
			t.Fatal("continuation was not scheduled")
		}
	}
}

func TestScopedDocumentReadRejectsUnannouncedGenerationChange(t *testing.T) {
	for _, rebase := range []bool{false, true} {
		state := scopedReadState()
		state.Route.Facts[documentReadFactPrefix+"source_sha256"] = "source"
		state.Route.Facts[documentReadFactPrefix+"structure_generation"] = "generation-1"
		outcome := scopedReadOutcome(true, "")
		attrs := outcome.Refs[0].Attributes
		attrs["structure_generation"] = "generation-2"
		if rebase {
			attrs["generation_rebased"], attrs["previous_structure_generation"] = "true", "generation-1"
		}
		assessment := (documentReadProfile{}).Assess(state, outcome)
		if (assessment.Status == app.AssessmentComplete) != rebase {
			t.Fatalf("incorrect generation boundary: rebase=%v assessment=%#v", rebase, assessment)
		}
	}
}

func TestScopedDocumentReadBindsContinuationFromStoredState(t *testing.T) {
	state := scopedReadState()
	for key, value := range map[string]string{"source_sha256": "source", "structure_generation": "generation", "next_cursor": "trusted-cursor", "task_mode": "exhaustive"} {
		state.Route.Facts[documentReadFactPrefix+key] = value
	}
	args, err := bindScopedDocumentReadArguments(state, toolPlan{Capability: app.ToolCapabilityDocumentReadRange}, map[string]any{"source_sha256": "other", "structure_generation": "other", "cursor": "forged", "node_ids": []string{"different"}})
	if err != nil || args["cursor"] != "trusted-cursor" || args["source_sha256"] != "source" || args["structure_generation"] != "generation" || len(documentReadSelector(args)) != 0 {
		t.Fatalf("model replaced frozen continuation: %#v %v", args, err)
	}
}

func TestScopedDocumentHeadingExpansionIsRuntimeOwnedAndRevisionBound(t *testing.T) {
	for _, revision := range []int{6, 7} {
		state := scopedReadState()
		state.Plan.ProfileRevision = revision
		state.ActiveNodeIDs = []app.WorkflowNodeID{documentReadNodeID}
		args, err := bindScopedDocumentReadArguments(state, toolPlan{Capability: app.ToolCapabilityDocumentReadRange}, map[string]any{"task_mode": "targeted", "scope_extent": "selected_range", "node_ids": []string{"heading"}, "include_descendants": false})
		if err != nil {
			t.Fatal(err)
		}
		if (args["include_descendants"] == true) != (revision == 7) {
			t.Fatalf("revision %d expansion binding: %+v", revision, args)
		}
		profile, err := defaultWorkflowProfileRegistry().Get(app.WorkflowDocumentRead, revision)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(profile.StageContext(state).Reason, "Revision 7") != (revision == 7) {
			t.Fatal("persisted revision instruction drift")
		}
	}
}

func TestWholeDocumentExtentCannotShrinkToOutlinePage(t *testing.T) {
	state := scopedReadState()
	state.Plan.ProfileRevision = 7
	args, err := bindScopedDocumentReadArguments(state, toolPlan{Capability: app.ToolCapabilityDocumentReadRange}, map[string]any{"task_mode": "exhaustive", "scope_extent": "whole_document", "node_ids": []string{"first_outline_page_only"}, "section": "invented"})
	if err != nil || args["all"] != true || args["node_ids"] != nil || args["section"] != nil {
		t.Fatalf("whole-source intent narrowed: %+v %v", args, err)
	}
	state.Route.Facts[documentReadFactPrefix+"scope_extent"] = "whole_document"
	if _, err := bindScopedDocumentReadArguments(state, toolPlan{Capability: app.ToolCapabilityDocumentReadRange}, map[string]any{"task_mode": "exhaustive", "scope_extent": "selected_range", "node_ids": []string{"one"}}); err == nil {
		t.Fatal("frozen scope extent narrowed")
	}
}

func TestScopedFinalProjectionPreservesLateRangeAndExposureLimitation(t *testing.T) {
	state := scopedReadState()
	state.Route.Facts[documentReadFactPrefix+"task_mode"] = "whole_summary"
	state.Route.Facts[documentReadFactPrefix+"scope_selector"] = `{"all":true}`
	state.Route.Facts[documentReadFactPrefix+"scope_status"] = "complete"
	calls := []app.ToolCall{
		{ID: "first", Tool: "document.read_range", Capability: app.ToolCapabilityDocumentReadRange, Status: app.ToolCallStatusCompleted, Result: map[string]any{"content": strings.Repeat("early detail ", 1000), "read_complete": false}},
		{ID: "last", Tool: "document.read_range", Capability: app.ToolCapabilityDocumentReadRange, Status: app.ToolCallStatusCompleted, Result: map[string]any{"content": "LATE_EXCEPTION_ONLY", "read_complete": true}},
	}
	projection := buildWorkflowFinalEvidenceProjection(app.AgentRun{ID: "run", Workflow: state}, calls, nil, nil, 1000)
	payload := projection.modelPayload()
	if !strings.Contains(payload, "LATE_EXCEPTION_ONLY") || !strings.Contains(payload, `"model_exposure_complete":false`) || projection.Coverage.Claim != workflowCoveragePartial {
		t.Fatalf("finalizer lost the late range or claimed complete exposure: %s", payload)
	}
}

func TestScopedDocumentReadRejectsConflictingCoverageCounts(t *testing.T) {
	outcome := scopedReadOutcome(true, "")
	outcome.Refs[0].Attributes["coverage_missing"] = "1"
	assessment := (documentReadProfile{}).Assess(scopedReadState(), outcome)
	if assessment.Status == app.AssessmentComplete {
		t.Fatal("claimed complete coverage despite a missing required unit")
	}
}

func TestScopedDocumentReadKeepsImageFormatOnVisualAdapter(t *testing.T) {
	profile := documentReadProfile{}
	route := app.RouteDecision{Facts: map[string]string{"document_format": app.DocumentFormatImage}}
	_, plan, err := profile.Resolve(route, "turn")
	if err != nil || plan.ProfileRevision != 10 || plan.Nodes[1].InitialStage != documentReadStage || plan.Nodes[1].InitialScope.Requirements[0].Qualifiers[app.CapabilityQualifierFormat] != app.DocumentFormatImage {
		t.Fatalf("image format lost its visual capability: %#v %v", plan, err)
	}
}

func TestScopedDocumentReadBudgetExhaustionPersistsRemainingWork(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		cfg.config.Runtime.StageEvidenceMaxBytes = 6000
		cfg.config.Runtime.RunMaxToolCalls = 3
	})
	defer closeRuntime()
	if err := os.WriteFile(filepath.Join(session.WorkspaceRoot, "bounded.md"), []byte("# Required scope\n\n"+strings.Repeat("long source data ", 2000)), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := runtime.HandleMessage(t.Context(), session.ID, "Read bounded.md")
	if err != nil {
		t.Fatal(err)
	}
	stored, found, err := st.GetRun(t.Context(), result.Run.ID)
	if err != nil || !found || stored.Workflow == nil {
		t.Fatal("budget outcome was not persisted")
	}
	facts := stored.Workflow.Route.Facts
	if stored.Workflow.Status == app.WorkflowStatusSucceeded || facts[documentReadFactPrefix+"scope_status"] != "partial" || facts[documentReadFactPrefix+"next_cursor"] == "" || facts[documentReadFactPrefix+"coverage"] == "" {
		t.Fatalf("budget exhaustion lost remaining work: status=%s facts=%v", stored.Workflow.Status, facts)
	}
	if !strings.Contains(result.Message.Content, "incomplete") || !strings.Contains(result.Message.Content, "Remaining scope units") {
		t.Fatalf("partial result was not disclosed: %q", result.Message.Content)
	}
}

func TestScopedDocumentReadDoesNotTreatSearchTopKAsExhaustive(t *testing.T) {
	state := scopedReadState()
	_, err := bindScopedDocumentReadArguments(state, toolPlan{Capability: app.ToolCapabilityDocumentReadRange}, map[string]any{"task_mode": "exhaustive", "chunk_ids": []string{"c00001"}})
	if err == nil {
		t.Fatal("top-k search chunks were accepted as an exhaustive task scope")
	}
}

func scopedReadState() *app.WorkflowState {
	return &app.WorkflowState{Plan: app.WorkflowPlan{ProfileID: app.WorkflowDocumentRead, ProfileRevision: 6}, Route: app.RouteDecision{Facts: map[string]string{"document_format": app.DocumentFormatText}}, Nodes: map[app.WorkflowNodeID]app.WorkflowNodeState{documentReadNodeID: {Stage: documentScopeStage}}}
}

func scopedReadOutcome(complete bool, next string) app.ToolOutcome {
	status := "partial"
	if complete {
		status = "complete"
	}
	coverage, _ := json.Marshal(map[string]any{"scope": "selected_nodes", "status": status, "required": 2, "covered": 1, "missing": 1, "denominator_known": true})
	attrs := map[string]string{"source_sha256": "source", "structure_generation": "generation-1", "task_mode": "exhaustive", "scope_selector": `{"all":true}`, "coverage_status": status, "coverage_denominator_known": "true", "coverage": string(coverage), "read_complete": "false", "next_cursor": next}
	attrs["coverage_required"], attrs["coverage_covered"], attrs["coverage_missing"] = "2", "1", "1"
	if complete {
		attrs["read_complete"], attrs["coverage_covered"], attrs["coverage_missing"] = "true", "2", "0"
	}
	return app.ToolOutcome{ID: "outcome", ToolCallID: "call", Tool: "document.read_range", NodeID: documentReadNodeID, Status: app.ToolCallStatusCompleted, Signals: []app.OutcomeSignal{app.OutcomeSignalContentAvailable}, Refs: []app.ResourceRef{{Kind: documentRangeResourceKind, Attributes: attrs}}}
}

func TestScopedFinalProjectionKeepsFormulaAndCachedStateAtomic(t *testing.T) {
	output := map[string]any{"source_sha256": "source", "structure_generation": "gen", "nodes": []any{map[string]any{"id": "cell", "text": "42", "values": []any{map[string]any{"formula": "SUM(A1:A3)", "cached_result": 42, "formula_result_recalculated": false}}}}, "dependencies": []any{map[string]any{"id": "header", "text": "Amount in EUR"}}}
	full, truncated := scopedDocumentFinalEvidence(output, "42", 4096)
	for _, marker := range []string{"SUM(A1:A3)", `"formula_result_recalculated":false`, "Amount in EUR", "source"} {
		if truncated || !strings.Contains(full, marker) {
			t.Fatalf("typed context lost %q: %s", marker, full)
		}
	}
	bounded, truncated := scopedDocumentFinalEvidence(output, "42", 120)
	if !truncated || strings.Contains(bounded, "42") || len(bounded) > 120 {
		t.Fatalf("partial typed value escaped: %s", bounded)
	}
}

func TestScopedFinalProjectionRetainsPlainTextRequiredUnitContext(t *testing.T) {
	state := scopedReadState()
	state.Route.Facts[documentReadFactPrefix+"scope_status"] = "complete"
	state.Route.Facts[documentReadFactPrefix+"scope_selector"] = `{"node_ids":["amount"]}`
	output := map[string]any{"content": "12", "source_sha256": "source", "structure_generation": "gen", "read_complete": true,
		"nodes":        []any{map[string]any{"id": "amount", "text": "12", "dependencies": []string{"heading"}}},
		"dependencies": []any{map[string]any{"id": "heading", "text": "Fee amount (EUR)"}}}
	calls := []app.ToolCall{{ID: "read", Tool: "document.read_range", Capability: app.ToolCapabilityDocumentReadRange, Status: app.ToolCallStatusCompleted, Result: output}}
	full := buildWorkflowFinalEvidenceProjection(app.AgentRun{ID: "run", Workflow: state}, calls, nil, nil, 4096)
	if !strings.Contains(full.modelPayload(), "Fee amount (EUR)") || full.Coverage.Claim != workflowCoverageComplete || !full.Coverage.CompleteForConsumer {
		t.Fatalf("plain-text value lost its required unit context: %s", full.modelPayload())
	}
	partial := buildWorkflowFinalEvidenceProjection(app.AgentRun{ID: "run", Workflow: state}, calls, nil, nil, 100)
	payload := partial.modelPayload()
	if partial.Coverage.Claim != workflowCoveragePartial || partial.Coverage.CompleteForConsumer || !strings.Contains(payload, `"model_exposure_complete":false`) || strings.Contains(payload, "evenly_spaced_windows") || strings.Contains(payload, `"text":"12"`) {
		t.Fatalf("omitted context advertised complete or sampled exposure: coverage=%#v payload=%s", partial.Coverage, payload)
	}
}

func TestInitialDocumentSelectionRepairIsBoundedAndCannotChangeFrozenScope(t *testing.T) {
	state := scopedReadState()
	state.Plan.ProfileRevision = 7
	call := app.ToolCall{ID: "bad-selection", Capability: app.ToolCapabilityDocumentReadRange, Status: app.ToolCallStatusFailed, Error: "node is not in this document"}
	outcome := adaptDocumentRetrievalOutcome(call, documentReadNodeID)
	for attempt := 0; attempt < 3; attempt++ {
		assessment := (documentReadProfile{}).Assess(state, outcome)
		want := app.AssessmentNeedsMoreEvidence
		if attempt == 2 {
			want = app.AssessmentBlocked
		}
		if assessment.Status != want {
			t.Fatalf("attempt %d: %+v", attempt, assessment)
		}
	}
	for _, key := range []string{"scope_selector", "next_cursor"} {
		frozen := scopedReadState()
		frozen.Plan.ProfileRevision = 7
		frozen.Route.Facts[documentReadFactPrefix+key] = "frozen"
		if assessment := (documentReadProfile{}).Assess(frozen, outcome); assessment.Status != app.AssessmentBlocked {
			t.Fatalf("retried frozen %s: %+v", key, assessment)
		}
	}
	legacy := scopedReadState()
	if assessment := (documentReadProfile{}).Assess(legacy, outcome); assessment.Status != app.AssessmentBlocked {
		t.Fatalf("changed revision 6 failure behavior: %+v", assessment)
	}
}

func TestNewDocumentPlansUseCanonicalInsteadOfLegacySectionSelectors(t *testing.T) {
	state := scopedReadState()
	state.Plan.ProfileRevision = 7
	args, err := bindScopedDocumentReadArguments(state, toolPlan{Capability: app.ToolCapabilityDocumentReadRange}, map[string]any{"task_mode": "targeted", "scope_extent": "selected_range", "node_ids": []string{"canonical-node"}, "section": "a title is not a legacy section ID"})
	if err != nil || len(documentReadSelector(args)) != 1 || args["section"] != nil || args["include_descendants"] != true {
		t.Fatalf("legacy selector conflicts with canonical selection: %#v %v", args, err)
	}
	definition := app.ToolDefinition{Capabilities: []app.CapabilityDescriptor{{Name: app.ToolCapabilityDocumentReadRange}}}
	bound, _ := scopedDocumentReadModelArguments(app.AgentRun{Workflow: state}, definition)
	if !bound["section"] {
		t.Fatal("new model schema exposed the legacy section selector")
	}
	state.Plan.ProfileRevision = 6
	bound, _ = scopedDocumentReadModelArguments(app.AgentRun{Workflow: state}, definition)
	if bound["section"] {
		t.Fatal("legacy schema changed")
	}
}

func TestTaskSummaryBindsPersistedUserGoalAndKeepsLegacyContract(t *testing.T) {
	run := app.AgentRun{MessageContext: &app.MessageRunContext{RequestContent: app.MessageContent{Parts: []app.MessagePart{{Kind: app.MessagePartText, Text: "Preserve the last checklist"}}}}}
	if documentReadSummaryGoal(run) != "Preserve the last checklist" || documentReadSummaryGoal(app.AgentRun{}) != "" {
		t.Fatal("task summary did not use persisted user content")
	}
	for _, revision := range []int{6, 7} {
		state := scopedReadState()
		state.Plan.ProfileRevision = revision
		args, err := bindScopedDocumentReadArguments(state, toolPlan{Capability: app.ToolCapabilityDocumentSummarize}, map[string]any{})
		if err != nil || args["contract_version"] != revision-4 {
			t.Fatalf("revision %d summary contract: %+v %v", revision, args, err)
		}
	}
}
