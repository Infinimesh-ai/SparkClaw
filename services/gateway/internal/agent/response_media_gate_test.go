package agent

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// TestResponseMediaDetectionStageTracksCurrentProfilePlan guards the external
// MCP workspace approval gate against silent decoupling: whatever plan the
// current conversation-answer profile resolves must satisfy the gate
// predicate while the run is parked on the detection node. A revision bump
// that keeps the node keeps the gate; renaming or dropping the node fails
// here instead of silently disabling the approval requirement.
func TestResponseMediaDetectionStageTracksCurrentProfilePlan(t *testing.T) {
	profile := conversationAnswerProfile{}
	route := app.RouteDecision{Slots: app.RouteSlots{Operation: app.RouteOperationAnswer}}
	_, plan, err := profile.Resolve(route, "turn_1")
	if err != nil {
		t.Fatal(err)
	}
	hasDetectNode := false
	for _, node := range plan.Nodes {
		if node.ID == detectResponseMediaNodeID {
			hasDetectNode = true
		}
	}
	if !hasDetectNode {
		t.Fatalf("conversation answer plan no longer contains %q; if response-media detection moved, update the workspace approval gate with it", detectResponseMediaNodeID)
	}
	run := &app.AgentRun{Workflow: &app.WorkflowState{
		Plan:          plan,
		ActiveNodeIDs: []app.WorkflowNodeID{detectResponseMediaNodeID},
	}}
	if !responseMediaDetectionStage(run) {
		t.Fatal("responseMediaDetectionStage rejects the plan the current profile resolves; the MCP workspace approval gate is disabled")
	}
	run.Workflow.ActiveNodeIDs = []app.WorkflowNodeID{"answer"}
	if responseMediaDetectionStage(run) {
		t.Fatal("responseMediaDetectionStage must only match while the detection node is active")
	}
}
