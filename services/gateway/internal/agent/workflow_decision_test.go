package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestDocumentEditDecisionInvalidOutputRetriesThenBlocks(t *testing.T) {
	runtime, st, _, dispatch := newDocumentDecisionFixture(t, "Improve the existing paragraph in report.docx")
	dispatch.Run.Workflow.Route.Slots.Query += "\nMOCK_OPERATION_SELECTION_RESPONSE:not-json"
	testSaveRun(st, dispatch.Run)

	_, changed, err := runtime.resolveActiveWorkflowDecisions(context.Background(), &dispatch.Run, dispatch.Profile)
	if err != nil || !changed {
		t.Fatalf("invalid decision output did not reach a terminal state: changed=%t err=%v", changed, err)
	}
	decision := dispatch.Run.Workflow.Nodes["select_edit_operation"]
	if dispatch.Run.Workflow.Status != app.WorkflowStatusBlocked || decision.Status != app.WorkflowNodeBlocked ||
		decision.Attempts != 2 || decision.LastAssessment == nil ||
		decision.LastAssessment.ReasonCode != "edit_operation_selection_invalid" {
		t.Fatalf("invalid decision output did not exhaust its own attempt bound: %#v", decision)
	}
	if countModelCalls(testListModelCalls(st, dispatch.Run.SessionID, dispatch.Run.ID), "workflow_operation_selection", documentWorkflowModelLane) != 2 {
		t.Fatalf("invalid decision output did not retry on the document workflow lane: %#v", testListModelCalls(st, dispatch.Run.SessionID, dispatch.Run.ID))
	}
	projections := []app.AuditEvent{}
	for _, event := range st.ListAudit(dispatch.Run.SessionID) {
		if event.RunID == dispatch.Run.ID && event.Type == "workflow.evidence_projection.created" &&
			event.Fields["semantic_variable"] == "eligible_document_operation" {
			projections = append(projections, event)
		}
	}
	var initial, repair *app.AuditEvent
	for index := range projections {
		switch intLikeValue(projections[index].Fields["repair_attempt"]) {
		case 0:
			initial = &projections[index]
		case 1:
			repair = &projections[index]
		}
	}
	if len(projections) != 2 || initial == nil || repair == nil ||
		initial.Fields["model_payload_digest"] != repair.Fields["model_payload_digest"] ||
		initial.Fields["reused"] != false || repair.Fields["reused"] != true ||
		!hasAgentAuditStringSliceField([]app.AuditEvent{*repair}, repair.Type, "validation_error_codes", "selection_empty_or_invalid") ||
		initial.Fields["complete_for_consumer"] != true || intLikeValue(initial.Fields["archived_bytes"]) <= 0 {
		t.Fatalf("decision repair did not reuse one complete evidence projection: %#v", projections)
	}
}

func TestWorkflowDecisionPromptDeclaresOnlyEligibleEntrySemanticVariable(t *testing.T) {
	run := app.AgentRun{Workflow: &app.WorkflowState{Route: app.RouteDecision{Slots: app.RouteSlots{Query: "Update the requested target"}}}}
	system, _ := workflowDecisionSelectionPromptWithLimit(run, documentEditProfile{}, app.WorkflowNode{}, `{"schema_version":"workflow_operation_candidates_v1","candidates":[{"candidate_id":"candidate_1"}]}`, "candidate-local content", 8000)
	if !strings.Contains(system, "Semantic variable: eligible_document_operation.") {
		t.Fatalf("workflow decision prompt does not declare its semantic variable: %s", system)
	}
}

func TestWorkflowDecisionCandidateProjectionUsesOnlyProjectionLocalIDs(t *testing.T) {
	entries := []app.ToolDirectoryEntry{
		{
			ID: "directory_entry_internal_append_row", Name: "xlsx.append_row", Summary: "Append one row",
			WhenToUse: "Use for a new row at the current end boundary",
			Capability: app.CapabilityDescriptor{
				Name: app.ToolCapabilityDocumentEdit,
				Qualifiers: map[string]string{
					app.CapabilityQualifierFormat:    app.DocumentFormatXLSX,
					app.CapabilityQualifierOperation: "append_row",
				},
			},
		},
		{
			ID: "directory_entry_internal_update_cell", Name: "xlsx.update_cells", Summary: "Update existing cells",
			WhenToUse: "Use for bounded changes to existing cells",
			Capability: app.CapabilityDescriptor{
				Name: app.ToolCapabilityDocumentEdit,
				Qualifiers: map[string]string{
					app.CapabilityQualifierFormat:    app.DocumentFormatXLSX,
					app.CapabilityQualifierOperation: "update_cells",
				},
			},
		},
	}
	projection, bindings, err := buildWorkflowDecisionCandidateProjection(entries)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(projection, string(entry.ID)) || strings.Contains(projection, entry.Name) {
			t.Fatalf("candidate projection leaked Runtime directory representation: %s", projection)
		}
		candidateID := workflowDecisionCandidateID(entry.ID)
		if bindings[candidateID] != entry.ID || !strings.Contains(projection, candidateID) {
			t.Fatalf("candidate %q was not locally bound to %q: projection=%s bindings=%#v", candidateID, entry.ID, projection, bindings)
		}
	}
	if _, exists := bindings["candidate_foreign"]; exists {
		t.Fatalf("foreign candidate unexpectedly entered the frozen binding manifest: %#v", bindings)
	}
}

func TestDocumentEditDecisionForeignCandidateRetriesThenBlocks(t *testing.T) {
	runtime, st, _, dispatch := newDocumentDecisionFixture(t, "Improve the existing paragraph in report.docx")
	dispatch.Run.Workflow.Route.Slots.Query += `
MOCK_OPERATION_SELECTION_RESPONSE:{"status":"selected","candidate_id":"candidate_foreign"}`
	testSaveRun(st, dispatch.Run)

	_, changed, err := runtime.resolveActiveWorkflowDecisions(context.Background(), &dispatch.Run, dispatch.Profile)
	if err != nil || !changed {
		t.Fatalf("foreign candidate did not reach a terminal decision: changed=%t err=%v", changed, err)
	}
	decision := dispatch.Run.Workflow.Nodes["select_edit_operation"]
	if dispatch.Run.Workflow.Status != app.WorkflowStatusBlocked || decision.Attempts != 2 ||
		decision.LastAssessment == nil || decision.LastAssessment.ReasonCode != "edit_operation_selection_invalid" {
		t.Fatalf("foreign candidate escaped the projection binding boundary: %#v", decision)
	}
}

func TestDocumentEditDecisionEmptySelectionRetriesThenBlocksWithoutFastFallback(t *testing.T) {
	runtime, st, _, dispatch := newDocumentDecisionFixture(t, "Replace a paragraph in report.docx")
	dispatch.Run.Workflow.Route.Slots.Query += mockWorkflowDecisionNoMatchResponse()
	testSaveRun(st, dispatch.Run)

	_, changed, err := runtime.resolveActiveWorkflowDecisions(context.Background(), &dispatch.Run, dispatch.Profile)
	if err != nil || !changed {
		t.Fatalf("empty decision output did not block: changed=%t err=%v", changed, err)
	}
	decision := dispatch.Run.Workflow.Nodes["select_edit_operation"]
	if dispatch.Run.Workflow.Status != app.WorkflowStatusBlocked || decision.Attempts != 2 ||
		decision.LastAssessment == nil || decision.LastAssessment.ReasonCode != "no_registered_editor_matches" {
		t.Fatalf("empty decision output did not preserve its terminal reason: %#v", decision)
	}
	if countModelCalls(testListModelCalls(st, dispatch.Run.SessionID, dispatch.Run.ID), "workflow_operation_selection", documentWorkflowModelLane) != 2 {
		t.Fatalf("empty decision output did not use the decision node attempt bound: %#v", testListModelCalls(st, dispatch.Run.SessionID, dispatch.Run.ID))
	}
	if hasModelCallOperation(testListModelCalls(st, dispatch.Run.SessionID, dispatch.Run.ID), "workflow_directory_selection", "fast") {
		t.Fatal("document edit decision fell back to the retired fast directory selector")
	}
}

func mockWorkflowDecisionSelectedResponse(entryID app.ToolDirectoryEntryID) string {
	return "\nMOCK_OPERATION_SELECTION_RESPONSE:{\"status\":\"selected\",\"candidate_id\":\"" + workflowDecisionCandidateID(entryID) + "\"}"
}

func mockWorkflowDecisionNoMatchResponse() string {
	return "\nMOCK_OPERATION_SELECTION_RESPONSE:{\"status\":\"no_match\",\"reason_code\":\"unsupported_operation\"}"
}

func TestDocumentEditDecisionAndConsumerFailClosed(t *testing.T) {
	t.Run("decision node is never materialized", func(t *testing.T) {
		runtime, _, _, dispatch := newDocumentDecisionFixture(t, "Improve report.docx")
		stageContext := dispatch.Profile.StageContext(dispatch.Run.Workflow)
		if _, err := runtime.materializeActiveWorkflowTools(context.Background(), dispatch.Run, runtime.workflowActorRef(dispatch.Run), &stageContext); err == nil ||
			!strings.Contains(err.Error(), "decision node must be resolved") {
			t.Fatalf("active decision node did not fail materialization closed: %v", err)
		}
	})

	t.Run("editor requires persisted decision reference", func(t *testing.T) {
		runtime, st, session, dispatch := newDocumentDecisionFixture(t, "Improve report.docx")
		decision := dispatch.Run.Workflow.Nodes["select_edit_operation"]
		decision.Status = app.WorkflowNodeSucceeded
		dispatch.Run.Workflow.Nodes["select_edit_operation"] = decision
		dispatch.Run.Workflow.ActiveNodeIDs = removeWorkflowNodeID(dispatch.Run.Workflow.ActiveNodeIDs, "select_edit_operation")
		if !activateReadyWorkflowNodes(dispatch.Run.Workflow) {
			t.Fatal("test setup did not activate the editor node")
		}
		testSaveRun(st, dispatch.Run)

		stageContext := dispatch.Profile.StageContext(dispatch.Run.Workflow)
		if _, err := runtime.materializeActiveWorkflowTools(context.Background(), dispatch.Run, runtime.workflowActorRef(dispatch.Run), &stageContext); err == nil ||
			!strings.Contains(err.Error(), "decision reference") {
			t.Fatalf("editor materialized without a persisted decision reference: %v", err)
		}
		if hasModelCallOperation(testListModelCalls(st, session.ID, dispatch.Run.ID), "workflow_directory_selection", "fast") {
			t.Fatal("missing document decision invoked the generic fast fallback")
		}
	})

	t.Run("unexpected multi-candidate scope requires a decision node", func(t *testing.T) {
		_, st, _, dispatch := newDocumentDecisionFixture(t, "Improve report.docx")
		before := len(testListModelCalls(st, dispatch.Run.SessionID, dispatch.Run.ID))
		state := dispatch.Run.Workflow.Nodes["document_locate_evidence"]
		state.SelectedEntries = nil
		view := app.DirectoryView{
			NodeID: "document_locate_evidence",
			Entries: []app.ToolDirectoryEntry{
				{ID: "entry_one"},
				{ID: "entry_two"},
			},
		}
		if _, err := workflowDirectorySelection(dispatch.Run, state, view); err == nil ||
			!strings.Contains(err.Error(), "explicit decision node is required") {
			t.Fatalf("multi-candidate scope without a decision node did not fail closed: %v", err)
		}
		if after := len(testListModelCalls(st, dispatch.Run.SessionID, dispatch.Run.ID)); after != before {
			t.Fatalf("deterministic directory selection unexpectedly called a model: before=%d after=%d", before, after)
		}
	})
}

func TestWorkflowPlanRejectsInvalidDecisionNodes(t *testing.T) {
	profile := documentEditProfile{}
	resolve := func() (app.IntentEnvelope, app.WorkflowPlan) {
		intent, plan, err := profile.Resolve(app.RouteDecision{
			Slots: app.RouteSlots{TargetRef: "report.docx"},
			Facts: map[string]string{"document_format": app.DocumentFormatDOCX, "output_path": "report-sparkclaw-edit.docx"},
		}, "turn")
		if err != nil {
			t.Fatal(err)
		}
		return intent, plan
	}

	intent, plan := resolve()
	plan.Nodes[2].InitialScope = app.CapabilityScope{}
	if err := validateWorkflowPlan(intent, profile, plan); err == nil || !strings.Contains(err.Error(), "capability requirement") {
		t.Fatalf("decision node without scope was accepted: %v", err)
	}

	intent, plan = resolve()
	plan.Nodes[2].DependsOn = []app.WorkflowNodeID{"confirm_document_target"}
	if err := validateWorkflowPlan(intent, profile, plan); err == nil || !strings.Contains(err.Error(), "evidence dependency") {
		t.Fatalf("decision node without evidence dependency was accepted: %v", err)
	}

	intent, plan = resolve()
	plan.Nodes[2].InitialScope.MaterializeAll = true
	if err := validateWorkflowPlan(intent, profile, plan); err == nil || !strings.Contains(err.Error(), "materialize all") {
		t.Fatalf("decision node with materialize-all scope was accepted: %v", err)
	}
}

func TestDocumentEditDecisionRulesSeparateXLSXSiblingOperations(t *testing.T) {
	rules := strings.Join((documentEditProfile{}).DecisionRules(app.WorkflowNode{}), "\n")
	for _, boundary := range []string{
		"one explicit cell", "multiple supplied fields", "before or after anchor", "final structured boundary",
		"complete row", "Clearing a cell", "deleting the workbook file", "ambiguous target", "negates an edit",
		"quote edit instructions", "troubleshooting without changing", "never invent a missing target or new value",
		"exact cell address", "uniquely identifying existing record plus field", "otherwise return a typed no_match",
	} {
		if !strings.Contains(rules, boundary) {
			t.Fatalf("document edit decision rules omitted %q: %s", boundary, rules)
		}
	}
}

func TestWorkflowPlanRejectsInvalidDirectOnceNode(t *testing.T) {
	profile := documentEditProfile{}
	resolve := func() (app.IntentEnvelope, app.WorkflowPlan) {
		intent, plan, err := profile.Resolve(app.RouteDecision{
			Slots: app.RouteSlots{TargetRef: "report.docx"},
			Facts: map[string]string{"document_format": app.DocumentFormatDOCX, "output_path": "report-sparkclaw-edit.docx"},
		}, "turn")
		if err != nil {
			t.Fatal(err)
		}
		return intent, plan
	}

	intent, plan := resolve()
	plan.Nodes[1].MaxAttempts = 2
	if err := validateWorkflowPlan(intent, profile, plan); err == nil || !strings.Contains(err.Error(), "direct-once") {
		t.Fatalf("retrying direct-once node was accepted: %v", err)
	}

	intent, plan = resolve()
	plan.Nodes[1].InvocationMode = "unknown"
	if err := validateWorkflowPlan(intent, profile, plan); err == nil || !strings.Contains(err.Error(), "unsupported invocation mode") {
		t.Fatalf("unknown invocation mode was accepted: %v", err)
	}
}

func newDocumentDecisionFixture(t *testing.T, request string) (Runtime, *store.MemoryStore, app.Session, matchedWorkflowDispatch) {
	t.Helper()
	root := t.TempDir()
	writeTestOfficePackage(t, filepath.Join(root, "report.docx"), "word/document.xml")
	runtime, st, session, closeRuntime := newDocumentDispatchRuntime(t, root)
	t.Cleanup(closeRuntime)
	route, err := runtime.routeIntentForTest(session.ID, "turn", request, agentContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC()}
	dispatch, err := runtime.dispatchMatchedWorkflow(context.Background(), run, route, app.ReturnRoute{Mode: app.ReturnToSource}, "turn")
	if err != nil {
		t.Fatal(err)
	}
	dispatch.Run = advanceDocumentEditToDecision(t, runtime, st, dispatch, route.Slots.TargetRef)
	return runtime, st, session, dispatch
}

func countModelCalls(calls []app.ModelCall, operation, lane string) int {
	count := 0
	for _, call := range calls {
		if call.Operation == operation && call.Lane == lane {
			count++
		}
	}
	return count
}
