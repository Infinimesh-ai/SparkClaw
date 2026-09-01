package agent

import (
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestWorkflowModelStepHonorsExplicitLaneAndDefaultsDeep(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()

	for _, test := range []struct {
		name     string
		laneHint string
		wantLane string
	}{
		{name: "explicit fast", laneHint: documentWorkflowModelLane, wantLane: documentWorkflowModelLane},
		{name: "empty defaults deep", wantLane: workflowExecutionModelLane},
	} {
		t.Run(test.name, func(t *testing.T) {
			workflowRun := app.AgentRun{ID: app.NewID("run_workflow_lane"), SessionID: session.ID, Risk: app.RiskRead, StartedAt: time.Now().UTC()}
			testSaveRun(st, workflowRun)
			workflowResult := runtime.runWorkflowModelStep(
				t.Context(), session.ID, workflowRun,
				`workflow goal
MOCK_STEP_RESPONSE:{"type":"final","answer":"done"}`,
				workflowStageContext{WorkflowID: app.WorkflowBrowserInternetSearch, ModelLaneHint: test.laneHint}, nil, nil, nil, nil, agentContextSnapshot{},
			)
			if workflowResult.Chat.Lane != test.wantLane ||
				!hasModelCallOperation(testListModelCalls(st, session.ID, workflowRun.ID), "workflow_step_1", test.wantLane) {
				t.Fatalf("workflow execution lane = %q, want %q: calls=%#v", workflowResult.Chat.Lane, test.wantLane, testListModelCalls(st, session.ID, workflowRun.ID))
			}
		})
	}
}

func TestWorkflowProfileModelLanePolicy(t *testing.T) {
	for _, test := range []struct {
		profileID app.WorkflowID
		wantLane  string
	}{
		{profileID: app.WorkflowDocumentRead, wantLane: documentWorkflowModelLane},
		{profileID: app.WorkflowDocumentEdit, wantLane: documentWorkflowModelLane},
		{profileID: app.WorkflowBrowserInternetSearch, wantLane: workflowExecutionModelLane},
		{profileID: app.WorkflowConversationAnswer, wantLane: workflowExecutionModelLane},
	} {
		if got := workflowModelLaneForProfile(test.profileID); got != test.wantLane {
			t.Errorf("workflowModelLaneForProfile(%q) = %q, want %q", test.profileID, got, test.wantLane)
		}
	}
}
