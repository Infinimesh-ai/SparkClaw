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
	st.SaveRun(dispatch.Run)

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
	if countModelCalls(st.ListModelCalls(dispatch.Run.SessionID, dispatch.Run.ID), "workflow_operation_selection", documentWorkflowModelLane) != 2 {
		t.Fatalf("invalid decision output did not retry on the document workflow lane: %#v", st.ListModelCalls(dispatch.Run.SessionID, dispatch.Run.ID))
	}
}

func TestDocumentEditDecisionEmptySelectionRetriesThenBlocksWithoutFastFallback(t *testing.T) {
	runtime, st, _, dispatch := newDocumentDecisionFixture(t, "Replace a paragraph in report.docx")
	dispatch.Run.Workflow.Route.Slots.Query += "\nMOCK_OPERATION_SELECTION_RESPONSE:{\"entry_id\":\"\"}"
	st.SaveRun(dispatch.Run)

	_, changed, err := runtime.resolveActiveWorkflowDecisions(context.Background(), &dispatch.Run, dispatch.Profile)
	if err != nil || !changed {
		t.Fatalf("empty decision output did not block: changed=%t err=%v", changed, err)
	}
	decision := dispatch.Run.Workflow.Nodes["select_edit_operation"]
	if dispatch.Run.Workflow.Status != app.WorkflowStatusBlocked || decision.Attempts != 2 ||
		decision.LastAssessment == nil || decision.LastAssessment.ReasonCode != "no_registered_editor_matches" {
		t.Fatalf("empty decision output did not preserve its terminal reason: %#v", decision)
	}
	if countModelCalls(st.ListModelCalls(dispatch.Run.SessionID, dispatch.Run.ID), "workflow_operation_selection", documentWorkflowModelLane) != 2 {
		t.Fatalf("empty decision output did not use the decision node attempt bound: %#v", st.ListModelCalls(dispatch.Run.SessionID, dispatch.Run.ID))
	}
	if hasModelCallOperation(st.ListModelCalls(dispatch.Run.SessionID, dispatch.Run.ID), "workflow_directory_selection", "fast") {
		t.Fatal("document edit decision fell back to the retired fast directory selector")
	}
}

func TestDocumentEditDecisionAndConsumerFailClosed(t *testing.T) {
	t.Run("decision node is never materialized", func(t *testing.T) {
		runtime, _, session, dispatch := newDocumentDecisionFixture(t, "Improve report.docx")
		stageContext := dispatch.Profile.StageContext(dispatch.Run.Workflow)
		if _, err := runtime.materializeActiveWorkflowTools(context.Background(), dispatch.Run, runtime.workflowActorRef(session.ID), &stageContext); err == nil ||
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
		st.SaveRun(dispatch.Run)

		stageContext := dispatch.Profile.StageContext(dispatch.Run.Workflow)
		if _, err := runtime.materializeActiveWorkflowTools(context.Background(), dispatch.Run, runtime.workflowActorRef(session.ID), &stageContext); err == nil ||
			!strings.Contains(err.Error(), "decision reference") {
			t.Fatalf("editor materialized without a persisted decision reference: %v", err)
		}
		if hasModelCallOperation(st.ListModelCalls(session.ID, dispatch.Run.ID), "workflow_directory_selection", "fast") {
			t.Fatal("missing document decision invoked the generic fast fallback")
		}
	})

	t.Run("unexpected multi-candidate scope requires a decision node", func(t *testing.T) {
		_, st, _, dispatch := newDocumentDecisionFixture(t, "Improve report.docx")
		before := len(st.ListModelCalls(dispatch.Run.SessionID, dispatch.Run.ID))
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
		if after := len(st.ListModelCalls(dispatch.Run.SessionID, dispatch.Run.ID)); after != before {
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
