package agent

import (
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// Exercise the real assessment-to-binding boundary: presentation normally drops
// hidden snapshot refs, but an explicitly requested visual stage still needs
// that exact page, generation and digest before it can take a screenshot.
func TestBrowserVisualStageRetainsFreshSnapshotThroughPresentationTransition(t *testing.T) {
	for _, profile := range []workflowProfile{browserAutomationProfile{}, browserInteractionProfile{}, browserFormDraftProfile{}} {
		t.Run(string(profile.ID()), func(t *testing.T) {
			runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
				cfg.config.Tools.BrowserAutomation.Enabled = true
				cfg.browserAdapter = &fakeInteractionBrowserAdapter{}
			})
			defer closeRuntime()
			route := app.RouteDecision{Slots: app.RouteSlots{TargetKind: "url", TargetRef: "https://example.com/", Query: "Show the page and take a screenshot"},
				Facts: map[string]string{"browser_visual_reason": "owner_requested"}}
			intent, plan, err := profile.Resolve(route, "turn")
			if err != nil {
				t.Fatal(err)
			}
			state := newWorkflowState(route, app.ReturnRoute{Mode: app.ReturnToSource}, intent, plan)
			if _, err := profile.Prepare(state); err != nil {
				t.Fatal(err)
			}
			nodeID := plan.Nodes[0].ID
			refs := []app.ResourceRef{
				{Kind: "browser_page", Ref: "page_1", Attributes: map[string]string{"url": "https://example.com/", "owner_id": "owner", "profile_id": "client_embedded"}},
				{Kind: "browser_snapshot", Ref: "snapshot_fresh", Attributes: map[string]string{"page_id": "page_1", "session_generation": "7", "page_generation": "9",
					"digest": "fresh_digest", "content_digest": "fresh_content", "presentation": "hidden"}},
				{Kind: "browser_element", Ref: "fresh_element", Attributes: map[string]string{"snapshot_id": "snapshot_fresh"}},
			}
			node := state.Nodes[nodeID]
			node.Stage, node.OutcomeRefs = browserStageAssessGoalInitial, refs
			outcome := app.ToolOutcome{NodeID: nodeID, ToolCallID: "hidden_observation", Signals: []app.OutcomeSignal{app.OutcomeSignalInteractionGoalSatisfied}, Refs: refs}
			if profile.ID() == app.WorkflowBrowserAutomation {
				node.Stage = browserStageSnapshotHidden
				outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalSnapshotAvailable}
			}
			state.Nodes[nodeID] = node
			assessment := profile.Assess(state, outcome)
			if assessment.Status != app.AssessmentNeedsMoreEvidence || state.Browser.Result == nil {
				t.Fatalf("hidden result did not reach presentation: %#v", assessment)
			}
			// The frozen plan redirects this presentation transition through visual
			// inspection. Use the assessment's selected refs, just as runtime does.
			redirected := false
			for _, transition := range plan.Nodes[0].Transitions {
				if transition.NextStage == browserStageVisualInspect && len(transition.On.OutcomeSignals) == 1 &&
					containsOutcomeSignal(assessment.Signals, transition.On.OutcomeSignals[0]) {
					redirected = true
				}
			}
			if !redirected {
				t.Fatal("test did not exercise the actual visual transition")
			}
			node.Stage, node.ScopeRevision, node.OutcomeRefs = browserStageVisualInspect, 5, assessment.SelectedRefs
			state.Nodes[nodeID] = node
			run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, Workflow: state, StartedAt: time.Now().UTC()}
			testSaveRun(st, run)
			materialized, err := runtime.materializeWorkflowBoundArguments(t.Context(), run.ID, toolPlan{
				Name: "browser.visual_inspect", WorkflowID: profile.ID(), WorkflowNodeID: nodeID, ScopeRevision: 5, Capability: app.ToolCapabilityBrowserVisualInspect,
				Args: map[string]any{"reason": "owner_requested", "page_id": "forged_page", "snapshot_id": "forged_snapshot", "snapshot_digest": "forged_digest", "session_generation": "100", "page_generation": "100"},
			})
			if err != nil {
				t.Fatal(err)
			}
			for key, want := range map[string]string{"page_id": "page_1", "snapshot_id": "snapshot_fresh", "snapshot_digest": "fresh_digest", "session_generation": "7", "page_generation": "9"} {
				if materialized.Args[key] != want {
					t.Errorf("visual stage lost trusted %s: got %v, want %s", key, materialized.Args[key], want)
				}
			}
			if len(assessment.SelectedRefs) != 3 {
				t.Errorf("visual handoff should carry only page, snapshot and result URL: %#v", assessment.SelectedRefs)
			}
			if refs := browserPresentationRefs(state, nil); len(refs) != 1 || refs[0].Kind != "browser_result_url" {
				t.Fatalf("post-inspection presentation retained execution refs: %#v", refs)
			}
			delete(state.Route.Facts, "browser_visual_reason")
			if refs := browserPresentationRefs(state, outcome.Refs); len(refs) != 1 || refs[0].Kind != "browser_result_url" {
				t.Fatalf("ordinary presentation retained hidden execution refs: %#v", refs)
			}
		})
	}
}
