package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestDocumentEditCancellationStopsBeforeRepeatingActiveStage(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		writeTestOfficePackage(t, filepath.Join(cfg.root, "note.docx"), "word/document.xml")
	})
	defer closeRuntime()

	route, err := runtime.routeIntentForTest(session.ID, "turn_cancelled_document_edit", "Replace a paragraph in note.docx", agentContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := runtime.dispatchMatchedWorkflow(context.Background(), app.AgentRun{
		ID: app.NewID("run"), SessionID: session.ID, State: "received", Risk: app.RiskReversible, StartedAt: time.Now().UTC(),
	}, route, app.ReturnRoute{Mode: app.ReturnToSource}, "turn_cancelled_document_edit")
	if err != nil {
		t.Fatal(err)
	}
	dispatch.Run, dispatch.Tools = advanceDocumentEditToEditor(t, runtime, st, dispatch, route.Slots.TargetRef, "docx.replace_paragraph", "replace_paragraph")
	stageContext := dispatch.Profile.StageContext(dispatch.Run.Workflow)
	budgetStopsBefore := countAuditEvents(st.ListAudit(session.ID), "workflow_step.budget_stopped")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := runtime.runWorkflowWithSeed(ctx, session.ID, dispatch.Run, "Replace a paragraph in note.docx", dispatch.Profile, stageContext, dispatch.Tools, nil, nil)

	if !result.Halted || !result.Cancelled || result.Completed || len(result.ToolCalls) != 0 {
		t.Fatalf("cancelled editor stage did not stop cleanly: %#v", result)
	}
	if strings.Contains(result.FinalAnswer, "stopped before its completion rule") {
		t.Fatalf("cancellation was overwritten by the generic incomplete-workflow message: %q", result.FinalAnswer)
	}
	storedRun, ok := testGetRun(st, dispatch.Run.ID)
	if !ok || storedRun.Workflow == nil {
		t.Fatal("cancelled workflow state was not persisted")
	}
	editor := storedRun.Workflow.Nodes["document_edit"]
	if storedRun.Workflow.Status != app.WorkflowStatusRunning || editor.Status != app.WorkflowNodeActive || editor.Attempts != 0 {
		t.Fatalf("cancelled workflow advanced or blocked the editor: %#v", storedRun.Workflow)
	}
	if got := countAuditEvents(st.ListAudit(session.ID), "workflow_step.budget_stopped") - budgetStopsBefore; got != 1 {
		t.Fatalf("cancelled workflow recorded %d budget stops; want exactly one", got)
	}
}

func TestFinalizeWorkflowRunStateRequiresSucceededWorkflow(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name      string
		status    app.WorkflowStatus
		execution workflowExecutionResult
		wantState string
	}{
		{name: "running workflow", status: app.WorkflowStatusRunning, execution: workflowExecutionResult{}, wantState: "failed"},
		{name: "model timeout", status: app.WorkflowStatusRunning, execution: workflowExecutionResult{Halted: true}, wantState: "failed"},
		{name: "gateway cancellation", status: app.WorkflowStatusRunning, execution: workflowExecutionResult{Halted: true, Cancelled: true}, wantState: "cancelled"},
		{name: "succeeded workflow", status: app.WorkflowStatusSucceeded, execution: workflowExecutionResult{}, wantState: "completed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			run := app.AgentRun{Workflow: &app.WorkflowState{Status: test.status}}
			finalizeWorkflowRunState(&run, test.execution, now)
			if run.State != test.wantState || run.CompletedAt == nil {
				t.Fatalf("finalized run = %#v, want state %q with completion time", run, test.wantState)
			}
		})
	}
}

func countAuditEvents(events []app.AuditEvent, eventType string) int {
	count := 0
	for _, event := range events {
		if event.Type == eventType {
			count++
		}
	}
	return count
}
