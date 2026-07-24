package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestCanonicalBrowserVerificationVerdictAcceptsBoundedProgressAliases(t *testing.T) {
	for input, expected := range map[string]string{
		"success": "success", "progress": "progress", "failure": "failure",
		"partial_progress": "progress", "in_progress": "progress",
	} {
		actual, ok := canonicalBrowserVerificationVerdict(input)
		if !ok || actual != expected {
			t.Fatalf("verdict %q normalized to %q ok=%v, want %q", input, actual, ok, expected)
		}
	}
	if _, ok := canonicalBrowserVerificationVerdict("maybe"); ok {
		t.Fatal("unsupported verification verdict must remain invalid")
	}
}

func TestAdaptBrowserHealthOutcomeAcceptsStableAndAgentBrowserResults(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload map[string]any
		want    app.OutcomeSignal
	}{
		{name: "stable boolean", payload: map[string]any{"ok": true}, want: app.OutcomeSignalBrowserHealthy},
		{name: "agent-browser status", payload: map[string]any{"status": "ok", "provider": "agent-browser"}, want: app.OutcomeSignalBrowserHealthy},
		{name: "explicit failure wins", payload: map[string]any{"ok": false, "status": "ok"}, want: app.OutcomeSignalBrowserUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			outcome := adaptBrowserHealthOutcome(app.ToolCall{
				ID: "tc_health", Tool: "browser.status", Status: "completed",
				Result: map[string]any{"output": test.payload},
			}, "browser_interaction")
			if len(outcome.Signals) != 1 || outcome.Signals[0] != test.want {
				t.Fatalf("unexpected browser health outcome: %#v", outcome)
			}
		})
	}
}

func TestAdaptBrowserFocusOutcomeCarriesFocusedPageWithoutPagesArray(t *testing.T) {
	outcome := adaptBrowserFocusOutcome(app.ToolCall{
		ID: "tc_focus", Tool: "browser.focus", Status: "completed",
		Arguments: map[string]any{"page_id": "page_7"},
		Result: map[string]any{"output": map[string]any{
			"tabId": "t7", "url": "https://example.test/workspace", "title": "Workspace",
		}},
	}, "browser_interaction")
	if len(outcome.Signals) != 1 || outcome.Signals[0] != app.OutcomeSignalFocusCompleted || len(outcome.Refs) != 1 {
		t.Fatalf("focused page outcome is incomplete: %#v", outcome)
	}
	ref := outcome.Refs[0]
	if ref.Kind != "browser_page" || ref.Ref != "page_7" || ref.Attributes["url"] != "https://example.test/workspace" {
		t.Fatalf("focused page was not normalized for the snapshot stage: %#v", ref)
	}
}

func TestBrowserInteractionExposesOnlyActiveStageWhilePersistingFullBoundary(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		cfg.config.Tools.BrowserAutomation.Enabled = true
		cfg.browserAdapter = &fakeInteractionBrowserAdapter{}
	})
	defer closeRuntime()

	route, err := runtime.routeIntentForTest(session.ID, "turn", "打开QQ邮箱的草稿箱", agentContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if route.Status != app.RouteMatched || route.CapabilityPath[1] != app.CapabilityBrowserInteraction {
		t.Fatalf("QQ Mail interaction did not select browser.interaction: %#v", route)
	}
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC()}
	dispatch, err := runtime.dispatchMatchedWorkflow(context.Background(), run, route, app.ReturnRoute{Mode: app.ReturnToSource}, "turn")
	if err != nil {
		t.Fatal(err)
	}
	if got := visibleToolNames(dispatch.Tools); len(got) != 1 || got[0] != "browser.status" {
		t.Fatalf("health_check exposed an out-of-stage browser tool: %#v", got)
	}
	if dispatch.Hint.Capability != app.ToolCapabilityBrowserHealth {
		t.Fatalf("health_check hint selected the wrong capability: %#v", dispatch.Hint)
	}
	stored, ok := st.GetRun(dispatch.Run.ID)
	if !ok || stored.Workflow == nil {
		t.Fatal("browser interaction workflow was not persisted")
	}
	node := stored.Workflow.Nodes["browser_interaction"]
	if len(node.SelectedEntries) != 9 {
		t.Fatalf("stage projection changed the fixed nine-tool boundary: %#v", node.SelectedEntries)
	}
}

func TestBrowserInteractionLeavesExplicitlyOpenedTabAvailable(t *testing.T) {
	profile := browserInteractionProfile{}
	goalSatisfied := app.ToolOutcome{
		NodeID: "browser_interaction", Signals: []app.OutcomeSignal{app.OutcomeSignalInteractionGoalSatisfied},
	}
	for _, tc := range []struct {
		name        string
		activations map[app.TransitionID]int
	}{
		{name: "workflow opened tab", activations: map[app.TransitionID]int{"open_missing": 1}},
		{name: "workflow reused tab", activations: map[app.TransitionID]int{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := &app.WorkflowState{Nodes: map[app.WorkflowNodeID]app.WorkflowNodeState{
				"browser_interaction": {Stage: "verify_action", TransitionActivations: tc.activations},
			}}
			assessment := profile.Assess(state, goalSatisfied)
			if assessment.Status != app.AssessmentComplete || assessment.ReasonCode != "interaction_goal_satisfied" {
				t.Fatalf("successful explicit interaction did not leave its page available: %#v", assessment)
			}
		})
	}
}

func TestBrowserInteractionReusesOnlyMatchingTargetTabs(t *testing.T) {
	registeredRoute := app.RouteDecision{
		Slots: app.RouteSlots{TargetKind: "url", TargetRef: "https://mail.qq.com/"},
		Facts: map[string]string{"browser_destination": "qq_mail"},
	}
	for _, test := range []struct {
		name       string
		route      app.RouteDecision
		refs       []app.ResourceRef
		wantRef    string
		wantSignal app.OutcomeSignal
	}{
		{
			name:  "selected registered subdomain page",
			route: registeredRoute,
			refs: []app.ResourceRef{{Kind: "browser_tab", Ref: "page_1", Attributes: map[string]string{
				"url": "https://wx.mail.qq.com/home/index?sid=redacted#/list/4", "selected": "true",
			}}},
			wantRef: "page_1", wantSignal: app.OutcomeSignalTargetTabExists,
		},
		{
			name:  "matching page instead of selected unrelated page",
			route: registeredRoute,
			refs: []app.ResourceRef{
				{Kind: "browser_tab", Ref: "page_1", Attributes: map[string]string{"url": "https://example.com/", "selected": "true"}},
				{Kind: "browser_tab", Ref: "page_2", Attributes: map[string]string{"url": "https://wx.mail.qq.com/home/index#/list/4"}},
			},
			wantRef: "page_2", wantSignal: app.OutcomeSignalTargetTabExists,
		},
		{
			name:  "lookalike host is rejected",
			route: registeredRoute,
			refs: []app.ResourceRef{{Kind: "browser_tab", Ref: "page_1", Attributes: map[string]string{
				"url": "https://mail.qq.com.example.org/home", "selected": "true",
			}}},
			wantSignal: app.OutcomeSignalTargetTabMissing,
		},
		{
			name:  "explicit URL still requires exact match",
			route: app.RouteDecision{Slots: app.RouteSlots{TargetKind: "url", TargetRef: "https://example.com/a"}},
			refs: []app.ResourceRef{{Kind: "browser_tab", Ref: "page_1", Attributes: map[string]string{
				"url": "https://example.com/b", "selected": "true",
			}}},
			wantSignal: app.OutcomeSignalTargetTabMissing,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			selected, signal, reason := selectBrowserInteractionTab(test.route, test.refs)
			if reason != "" || signal != test.wantSignal {
				t.Fatalf("unexpected target-tab decision: selected=%#v signal=%q reason=%q", selected, signal, reason)
			}
			if test.wantRef == "" {
				if selected != nil {
					t.Fatalf("non-matching tab was reused: %#v", selected)
				}
				return
			}
			if selected == nil || selected.Ref != test.wantRef {
				t.Fatalf("matching target tab was not selected: %#v", selected)
			}
		})
	}
}

func TestBrowserAutomationReusesRegisteredDestinationPage(t *testing.T) {
	state := &app.WorkflowState{Route: app.RouteDecision{
		Slots: app.RouteSlots{TargetKind: "url", TargetRef: "https://mail.qq.com/"},
		Facts: map[string]string{"browser_destination": "qq_mail"},
	}}
	outcome := app.ToolOutcome{
		Signals: []app.OutcomeSignal{app.OutcomeSignalTabsScanned},
		Refs: []app.ResourceRef{{Kind: "browser_tab", Ref: "page_7", Attributes: map[string]string{
			"url": "https://wx.mail.qq.com/home/index#/list/4", "selected": "true",
		}}},
	}
	assessment := (browserAutomationProfile{}).Assess(state, outcome)
	if assessment.Status != app.AssessmentNeedsMoreEvidence ||
		!containsOutcomeSignal(assessment.Signals, app.OutcomeSignalTargetTabExists) ||
		len(assessment.SelectedRefs) != 1 || assessment.SelectedRefs[0].Ref != "page_7" {
		t.Fatalf("registered target page was not reused by browser.automation: %#v", assessment)
	}
}

func TestBrowserInteractionMaterializesFrozenBlankTabNavigationArguments(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		cfg.config.Tools.BrowserAutomation.Enabled = true
		cfg.browserAdapter = &fakeInteractionBrowserAdapter{}
	})
	defer closeRuntime()

	route, err := runtime.routeIntentForTest(session.ID, "turn", "打开QQ邮箱的草稿箱", agentContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC()}
	dispatch, err := runtime.dispatchMatchedWorkflow(context.Background(), run, route, app.ReturnRoute{Mode: app.ReturnToSource}, "turn")
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := st.GetRun(dispatch.Run.ID)
	if !ok || stored.Workflow == nil {
		t.Fatal("browser interaction workflow was not persisted")
	}
	node := stored.Workflow.Nodes["browser_interaction"]
	node.Stage = "navigate_blank"
	node.ScopeRevision = 3
	node.OutcomeRefs = []app.ResourceRef{{Kind: "browser_tab", Ref: "page_1", Attributes: map[string]string{"url": "about:blank"}}}
	stored.Workflow.Nodes["browser_interaction"] = node
	st.SaveRun(stored)

	plan := runtime.materializeWorkflowBoundArguments(stored.ID, toolPlan{
		Name: "browser.navigate", Args: map[string]any{"url": "https://example.invalid/"},
		WorkflowID: app.WorkflowBrowserInteraction, WorkflowNodeID: "browser_interaction", ScopeRevision: 3,
		Capability: app.ToolCapabilityBrowserNavigate,
	})
	if plan.Args["url"] != "https://mail.qq.com/" || plan.Args["page_id"] != "page_1" {
		t.Fatalf("blank-tab navigation did not use the frozen URL and tab ref: %#v", plan.Args)
	}
}

func TestBrowserInteractionMaterializesSnapshotAndExpandsFrozenElementShortRef(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		cfg.config.Tools.BrowserAutomation.Enabled = true
		cfg.browserAdapter = &fakeInteractionBrowserAdapter{}
	})
	defer closeRuntime()

	route, err := runtime.routeIntentForTest(session.ID, "turn", "打开QQ邮箱的草稿箱", agentContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC()}
	dispatch, err := runtime.dispatchMatchedWorkflow(context.Background(), run, route, app.ReturnRoute{Mode: app.ReturnToSource}, "turn")
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := st.GetRun(dispatch.Run.ID)
	if !ok || stored.Workflow == nil {
		t.Fatal("browser interaction workflow was not persisted")
	}
	snapshotCall := app.ToolCall{ID: "tc_snapshot", Tool: "browser.snapshot", Status: "completed", Result: map[string]any{"output": map[string]any{
		"snapshot": map[string]any{
			"snapshot_id": "snapshot_1", "page_id": "page_1", "url": "https://mail.qq.com/",
			"controls": []any{map[string]any{"ref": "snapshot_1:e7:fingerprint", "short_ref": "e7", "role": "link", "accessible_name": "草稿箱"}},
		},
	}}}
	outcome := adaptBrowserSnapshotOutcome(snapshotCall, "browser_interaction")
	node := stored.Workflow.Nodes["browser_interaction"]
	node.Stage = "choose_and_click"
	node.ScopeRevision = 5
	node.OutcomeRefs = outcome.Refs
	stored.Workflow.Nodes["browser_interaction"] = node
	st.SaveRun(stored)

	plan := runtime.materializeWorkflowBoundArguments(stored.ID, toolPlan{
		Name: "browser.click", Args: map[string]any{"uid": "e7"},
		WorkflowID: app.WorkflowBrowserInteraction, WorkflowNodeID: "browser_interaction", ScopeRevision: 5,
		Capability: app.ToolCapabilityBrowserClick,
	})
	if plan.Args["page_id"] != "page_1" || plan.Args["snapshot_id"] != "snapshot_1" || plan.Args["uid"] != "snapshot_1:e7:fingerprint" {
		t.Fatalf("click arguments did not bind to the current snapshot ref: %#v", plan.Args)
	}
	definition, ok := runtime.tools.Definition("browser.click")
	if !ok {
		t.Fatal("browser.click is unavailable")
	}
	if err := runtime.validateWorkflowToolPlan(stored.ID, plan, definition); err != nil {
		t.Fatalf("materialized short ref did not pass the frozen workflow boundary: %v", err)
	}
}

func TestQQMailLoginSnapshotCreatesVisibleLoginHandoff(t *testing.T) {
	st := store.NewMemoryStore()
	session := st.CreateSession("QQ Mail login handoff")
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC()}
	st.SaveRun(run)
	runtime := Runtime{store: st}
	call := app.ToolCall{
		ID: "tc_snapshot", SessionID: session.ID, RunID: run.ID, Tool: "browser.snapshot", Status: "completed",
		Arguments: map[string]any{"browser_mode": "collaborative", "presentation": "visible", "surface_visible": true},
		Result: map[string]any{"output": map[string]any{"snapshot": map[string]any{
			"title": "登录QQ邮箱", "url": "https://wx.mail.qq.com/?cancel_login=true", "snapshot_id": "snapshot_1", "page_id": "page_1",
		}}},
	}
	block, ok := runtime.recordBrowserLoginBlockFromToolCall(session.ID, run.ID, "打开QQ邮箱的草稿箱", toolPlan{Name: "browser.snapshot", Args: call.Arguments}, call)
	if !ok || block.Status != app.BrowserLoginBlockStatusWaiting || block.BrowserAuthStatus != "handoff_waiting" {
		t.Fatalf("QQ Mail login page did not create a visible login handoff: %#v ok=%v", block, ok)
	}
}

func TestQQMailAuthenticatedSnapshotUsesStructuredPageEvidence(t *testing.T) {
	call := app.ToolCall{
		Tool: "browser.snapshot", Status: "completed",
		Result: map[string]any{"output": map[string]any{
			"snapshot": map[string]any{
				"browser_page_auth_state":      "authenticated",
				"browser_page_auth_confidence": "application_continuity",
				"browser_page_auth_signals":    []string{"visible_identity_control", "usable_application_shell"},
			},
			"text": "QQ邮箱 收件箱 草稿箱 邮件正文中的登录与退出说明",
		}},
	}
	assessment := assessBrowserAuthentication(call, browserLoginToolFields(call))
	if assessment.State != browserAuthAuthenticated || assessment.Confidence != "application_continuity" {
		t.Fatalf("authenticated QQ Mail shell was not recognized from structured snapshot evidence: %#v", assessment)
	}
}

func TestBrowserInteractionLoginResumeDiscardsPreLoginSnapshotAndContinues(t *testing.T) {
	adapter := &fakeInteractionBrowserAdapter{}
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		cfg.config.Tools.BrowserAutomation.Enabled = true
		cfg.browserAdapter = adapter
	})
	defer closeRuntime()

	route, err := runtime.routeIntentForTest(session.ID, "turn", "点击当前页面的下一步按钮", agentContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC()}
	dispatch, err := runtime.dispatchMatchedWorkflow(context.Background(), run, route, app.ReturnRoute{Mode: app.ReturnToSource}, "turn")
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := st.GetRun(dispatch.Run.ID)
	if !ok || stored.Workflow == nil {
		t.Fatal("browser interaction workflow was not persisted")
	}
	node := stored.Workflow.Nodes["browser_interaction"]
	node.Stage = "snapshot_before_action"
	node.ScopeRevision = 4
	node.Attempts = 3
	node.OutcomeRefs = []app.ResourceRef{{Kind: "browser_page", Ref: "page_1", Attributes: map[string]string{"url": "https://example.com/checkout"}}}
	stored.Workflow.Nodes["browser_interaction"] = node
	stored.State = "browser_login_blocked"
	st.SaveRun(stored)
	done := time.Now().UTC()
	interrupted := app.ToolCall{
		ID: "tc_pre_login_snapshot", SessionID: session.ID, RunID: stored.ID, Tool: "browser.snapshot", Status: "completed",
		WorkflowID: app.WorkflowBrowserInteraction, WorkflowNodeID: "browser_interaction", ScopeRevision: 4, Capability: app.ToolCapabilityBrowserSnapshot,
		Arguments: map[string]any{"page_id": "page_1"}, Result: map[string]any{"output": map[string]any{"snapshot": map[string]any{
			"snapshot_id": "snapshot_login", "page_id": "page_1", "title": "登录QQ邮箱", "url": "https://wx.mail.qq.com/",
		}}}, StartedAt: done, CompletedAt: &done,
	}
	st.SaveToolCall(interrupted)

	result := runtime.finishMatchedBrowserLoginResume(context.Background(), stored, "点击当前页面的下一步按钮", interrupted.ID, nil)
	if result.Run.State != "completed" || result.Run.Workflow == nil || result.Run.Workflow.Status != app.WorkflowStatusSucceeded {
		t.Fatalf("browser interaction did not continue after login: %#v", result.Run)
	}
	finalNode := result.Run.Workflow.Nodes["browser_interaction"]
	if !containsString(finalNode.ToolCallIDs, interrupted.ID) || adapter.clicks != 1 || adapter.snapshots != 2 {
		t.Fatalf("login resume reused the stale snapshot or skipped the verified click: node=%#v adapter=%#v", finalNode, adapter)
	}
}

func TestBrowserInteractionLoginResumeAppliesNavigationAndContinues(t *testing.T) {
	adapter := &fakeInteractionBrowserAdapter{}
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		cfg.config.Tools.BrowserAutomation.Enabled = true
		cfg.browserAdapter = adapter
	})
	defer closeRuntime()

	route, err := runtime.routeIntentForTest(session.ID, "turn", "打开QQ邮箱的草稿箱", agentContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC()}
	dispatch, err := runtime.dispatchMatchedWorkflow(context.Background(), run, route, app.ReturnRoute{Mode: app.ReturnToSource}, "turn")
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := st.GetRun(dispatch.Run.ID)
	if !ok || stored.Workflow == nil {
		t.Fatal("browser interaction workflow was not persisted")
	}
	node := stored.Workflow.Nodes["browser_interaction"]
	node.Stage = "navigate_blank"
	node.ScopeRevision = 3
	node.Attempts = 2
	node.OutcomeRefs = []app.ResourceRef{{Kind: "browser_tab", Ref: "page_1", Attributes: map[string]string{"url": "about:blank"}}}
	stored.Workflow.Nodes["browser_interaction"] = node
	stored.State = "browser_login_blocked"
	st.SaveRun(stored)
	done := time.Now().UTC()
	interrupted := app.ToolCall{
		ID: "tc_login_navigation", SessionID: session.ID, RunID: stored.ID, Tool: "browser.navigate", Status: "completed",
		WorkflowID: app.WorkflowBrowserInteraction, WorkflowNodeID: "browser_interaction", ScopeRevision: 3, Capability: app.ToolCapabilityBrowserNavigate,
		Arguments: map[string]any{"page_id": "page_1", "url": "https://mail.qq.com/"}, Result: map[string]any{"output": map[string]any{
			"page_id": "page_1", "title": "登录QQ邮箱", "url": "https://mail.qq.com/",
		}}, StartedAt: done, CompletedAt: &done,
	}
	st.SaveToolCall(interrupted)

	result := runtime.finishMatchedBrowserLoginResume(context.Background(), stored, "打开QQ邮箱的草稿箱", interrupted.ID, nil)
	if result.Run.State != "completed" || result.Run.Workflow == nil || result.Run.Workflow.Status != app.WorkflowStatusSucceeded {
		t.Fatalf("browser interaction did not continue after login navigation: %#v", result.Run)
	}
	if adapter.clicks != 1 || adapter.snapshots != 2 {
		t.Fatalf("login navigation resume skipped the verified click loop: %#v", adapter)
	}
}

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
	runtime, _, _, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	for _, goal := range []string{
		"点击当前页面的删除账户按钮",
		"click Send on the current page",
		"点击当前页面的确认订单按钮",
	} {
		route := mustRouteIntent(t, runtime, goal)
		if route.Status != app.RouteBlocked {
			t.Fatalf("consequential click did not fail routing closed: goal=%q route=%#v", goal, route)
		}
	}
}

func TestWorkflowPromptContextKeepsStageAndProvidedToolList(t *testing.T) {
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
