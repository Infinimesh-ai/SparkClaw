package agent

import (
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestWorkflowModelStepForcesDeepWithoutChangingFallbackLane(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()

	workflowRun := app.AgentRun{ID: "run_workflow_lane", SessionID: session.ID, Risk: app.RiskRead, StartedAt: time.Now().UTC()}
	st.SaveRun(workflowRun)
	workflowResult := runtime.runWorkflowModelStep(
		t.Context(), session.ID, workflowRun,
		`workflow goal
MOCK_REACT_RESPONSE:{"type":"final","answer":"done"}`,
		TaskHint{WorkflowID: app.WorkflowBrowserInternetSearch, ModelLaneHint: "fast"}, nil, nil, nil,
	)
	if workflowResult.Chat.Lane != workflowExecutionModelLane ||
		!hasModelCallOperation(st.ListModelCalls(session.ID, workflowRun.ID), "react_step_1", workflowExecutionModelLane) {
		t.Fatalf("workflow execution did not force the deep lane: result=%#v calls=%#v", workflowResult.Chat, st.ListModelCalls(session.ID, workflowRun.ID))
	}

	fallbackRun := app.AgentRun{ID: "run_fallback_lane", SessionID: session.ID, Risk: app.RiskRead, StartedAt: time.Now().UTC()}
	st.SaveRun(fallbackRun)
	fallbackResult := runtime.runReActLoop(
		t.Context(), session.ID, fallbackRun,
		`fallback goal
MOCK_REACT_RESPONSE:{"type":"final","answer":"done"}`,
		TaskHint{ModelLaneHint: "fast"}, nil, nil,
	)
	if fallbackResult.Chat.Lane != "fast" ||
		!hasModelCallOperation(st.ListModelCalls(session.ID, fallbackRun.ID), "react_step_1", "fast") {
		t.Fatalf("unmatched fallback lane changed: result=%#v calls=%#v", fallbackResult.Chat, st.ListModelCalls(session.ID, fallbackRun.ID))
	}
}
