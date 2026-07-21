package agent

import (
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestBrowserInteractionPlanKeepsBoundedFullToolScope(t *testing.T) {
	profile := browserInteractionProfile{}
	intent, plan, err := profile.Resolve(app.RouteDecision{
		Slots: app.RouteSlots{
			Operation: app.RouteOperationInteract, Query: "点击当前页面的下一步按钮",
			TargetKind: string(app.TargetKindBrowserCurrentTab), TargetRef: "selected",
		},
	}, "turn")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateWorkflowPlan(intent, profile, plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Nodes) != 1 {
		t.Fatalf("unexpected browser.interaction plan: %#v", plan)
	}
	node := plan.Nodes[0]
	if !node.InitialScope.MaterializeAll || len(node.InitialScope.Requirements) != 9 || node.MaxAttempts != 24 {
		t.Fatalf("browser.interaction lost its bounded full-lifecycle scope: %#v", node)
	}
	stages := map[string][]string{}
	for _, rule := range node.StageCapabilities {
		stages[rule.Stage] = rule.Capabilities
	}
	for stage, capability := range map[string]string{
		"health_check":           app.ToolCapabilityBrowserHealth,
		"scan_tabs":              app.ToolCapabilityBrowserListTabs,
		"focus_existing":         app.ToolCapabilityBrowserFocus,
		"navigate_blank":         app.ToolCapabilityBrowserNavigate,
		"open_new":               app.ToolCapabilityBrowserOpen,
		"snapshot_before_action": app.ToolCapabilityBrowserSnapshot,
		"choose_and_click":       app.ToolCapabilityBrowserClick,
		"verify_action":          app.ToolCapabilityBrowserVerify,
	} {
		if len(stages[stage]) != 1 || stages[stage][0] != capability {
			t.Fatalf("stage %q escaped its capability boundary: %#v", stage, stages[stage])
		}
	}
	if got := stages["snapshot_after_action"]; len(got) != 2 || got[0] != app.ToolCapabilityBrowserWait || got[1] != app.ToolCapabilityBrowserSnapshot {
		t.Fatalf("post-click stage lost wait/snapshot ordering: %#v", got)
	}
}

func TestBrowserInteractionRepeatedPostSnapshotFailsClosed(t *testing.T) {
	state := &app.WorkflowState{
		ActiveNodeIDs: []app.WorkflowNodeID{"browser_interaction"},
		Nodes: map[app.WorkflowNodeID]app.WorkflowNodeState{
			"browser_interaction": {Stage: "snapshot_after_action"},
		},
	}
	outcome := app.ToolOutcome{
		NodeID:  "browser_interaction",
		Signals: []app.OutcomeSignal{app.OutcomeSignalSnapshotAvailable},
		Refs: []app.ResourceRef{{
			Kind: "browser_snapshot", Ref: "snapshot_2",
			Attributes: map[string]string{"previous_snapshot_id": "snapshot_1", "repeated": "true"},
		}},
	}
	assessment := (browserInteractionProfile{}).Assess(state, outcome)
	if assessment.Status != app.AssessmentBlocked || assessment.ReasonCode != "interaction_loop_detected" {
		t.Fatalf("repeated post-click state did not fail closed: %#v", assessment)
	}
}

func TestBrowserInteractionConsequentialClicksFailRoutingClosed(t *testing.T) {
	profile := browserInteractionProfile{}
	for _, goal := range []string{
		"点击当前页面的删除账户按钮",
		"click Send on the current page",
		"点击当前页面的确认订单按钮",
	} {
		recognition, matched := profile.Recognize(workflowRecognitionContext{Content: goal})
		if !matched || recognition.Status != app.RouteBlocked {
			t.Fatalf("consequential click did not fail routing closed: goal=%q recognition=%#v matched=%v", goal, recognition, matched)
		}
	}
}

func TestWorkflowPromptContextKeepsStageAndFullToolList(t *testing.T) {
	hint := TaskHint{WorkflowID: app.WorkflowBrowserInteraction, Reason: "workflow_stage: verify_action. Verify before another click."}
	tools := []app.ToolDefinition{{Name: "browser.snapshot"}, {Name: "browser.click"}, {Name: "browser.verify"}}
	prompt := appendWorkflowReActContext("REACT_OUTPUT_REQUEST", hint, tools)
	for _, expected := range []string{"workflow_stage: verify_action", "browser.snapshot", "browser.click", "browser.verify"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("workflow prompt context lost %q: %s", expected, prompt)
		}
	}
}

func TestUnsafeBrowserClickGroundingOverridesEarlierTabEvidence(t *testing.T) {
	calls := []app.ToolCall{
		{Tool: "browser.list_tabs", Status: "completed", Result: map[string]any{"pages": []any{map[string]any{"page_id": "page_1", "selected": true}}}},
		{Tool: "browser.click", Status: "failed", Error: `unsafe click target "Delete account" is outside browser.interaction revision 1`},
	}
	answer, ok := groundedBrowserAutomationSummary("点击当前页面的下一步", "fallback", calls)
	if !ok || answer != "页面交互已阻止：目标点击可能产生不允许的后果。" {
		t.Fatalf("unsafe click failure was hidden by earlier browser evidence: %q ok=%v", answer, ok)
	}
}
