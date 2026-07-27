package agent

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestApplyWorkflowOutcomeRestoresEmptyTransitionActivationMap(t *testing.T) {
	const (
		nodeID       app.WorkflowNodeID = "research"
		transitionID app.TransitionID   = "read_source"
	)
	plan := app.WorkflowPlan{
		SchemaVersion:   1,
		ProfileID:       app.WorkflowBrowserInternetSearch,
		ProfileRevision: 1,
		InitialNodeIDs:  []app.WorkflowNodeID{nodeID},
		Nodes: []app.WorkflowNode{{
			ID:           nodeID,
			InitialStage: "search",
			Goal:         app.NodeGoal{Completion: app.CompletionEvidence},
			InitialScope: app.CapabilityScope{Requirements: []app.CapabilityRequirement{{Name: app.ToolCapabilityWebDiscovery}}},
			Transitions: []app.ScopeTransition{{
				ID:             transitionID,
				On:             app.TransitionPredicate{Assessments: []app.AssessmentStatus{app.AssessmentNeedsMoreEvidence}},
				NextStage:      "read",
				MaxActivations: 1,
			}},
			MaxAttempts: 2,
		}},
		Completion: app.CompletionEvidence,
	}
	run := app.AgentRun{Workflow: &app.WorkflowState{
		SchemaVersion: 1,
		Plan:          plan,
		PlanDigest:    workflowPlanDigest(plan),
		Status:        app.WorkflowStatusRunning,
		ActiveNodeIDs: []app.WorkflowNodeID{nodeID},
		Nodes: map[app.WorkflowNodeID]app.WorkflowNodeState{
			nodeID: {
				Status:        app.WorkflowNodeActive,
				Stage:         "search",
				CurrentScope:  plan.Nodes[0].InitialScope,
				ScopeRevision: 1,
			},
		},
	}}
	outcome := app.ToolOutcome{ID: "outcome_1", ToolCallID: "tc_1", NodeID: nodeID, Status: "completed"}
	assessment := app.NodeAssessment{
		OutcomeID: outcome.ID,
		NodeID:    nodeID,
		Status:    app.AssessmentNeedsMoreEvidence,
	}

	transitioned, err := applyWorkflowOutcome(&run, outcome, assessment)
	if err != nil {
		t.Fatal(err)
	}
	if !transitioned {
		t.Fatal("expected workflow transition")
	}
	state := run.Workflow.Nodes[nodeID]
	if state.TransitionActivations[transitionID] != 1 {
		t.Fatalf("transition activation was not restored and recorded: %#v", state.TransitionActivations)
	}
}
