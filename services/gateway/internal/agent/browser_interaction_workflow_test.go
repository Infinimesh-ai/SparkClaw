package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

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
	if dispatch.Context.Capability != app.ToolCapabilityBrowserHealth {
		t.Fatalf("health_check stage context selected the wrong capability: %#v", dispatch.Context)
	}
	stored, ok := st.GetRun(dispatch.Run.ID)
	if !ok || stored.Workflow == nil {
		t.Fatal("browser interaction workflow was not persisted")
	}
	node := stored.Workflow.Nodes["browser_result"]
	if len(node.SelectedEntries) != 10 {
		t.Fatalf("stage projection changed the fixed revision-2 ten-tool boundary: %#v", node.SelectedEntries)
	}
}

func TestBrowserInteractionCompletesOnlyAfterVisibleGoalVerification(t *testing.T) {
	profile := browserInteractionProfile{}
	state := &app.WorkflowState{
		Browser:       &app.BrowserWorkflowState{SchemaVersion: app.BrowserWorkflowStateSchemaVersion},
		ActiveNodeIDs: []app.WorkflowNodeID{"browser_result"},
		Nodes: map[app.WorkflowNodeID]app.WorkflowNodeState{
			"browser_result": {Stage: "assess_goal_initial"},
		},
	}
	goalSatisfied := app.ToolOutcome{
		NodeID: "browser_result", Signals: []app.OutcomeSignal{app.OutcomeSignalInteractionGoalSatisfied},
	}
	assessment := profile.Assess(state, goalSatisfied)
	if assessment.Status != app.AssessmentNeedsMoreEvidence || assessment.ReasonCode != "present_visible" {
		t.Fatalf("hidden goal assessment completed before visible presentation: %#v", assessment)
	}
	node := state.Nodes["browser_result"]
	node.Stage = "assess_goal_visible"
	state.Nodes["browser_result"] = node
	assessment = profile.Assess(state, goalSatisfied)
	if assessment.Status != app.AssessmentComplete || assessment.ReasonCode != "browser_visible_goal_verified" {
		t.Fatalf("visible goal assessment did not complete the workflow: %#v", assessment)
	}
}

func TestBrowserWorkflowStageContextsStayHiddenUntilPresentation(t *testing.T) {
	for _, workflowID := range []app.WorkflowID{app.WorkflowBrowserAutomation, app.WorkflowBrowserInteraction} {
		state := &app.WorkflowState{
			Plan:          app.WorkflowPlan{ProfileID: workflowID},
			ActiveNodeIDs: []app.WorkflowNodeID{"browser"},
			Nodes: map[app.WorkflowNodeID]app.WorkflowNodeState{
				"browser": {Stage: "scan_tabs"},
			},
		}
		var stageContext workflowStageContext
		if workflowID == app.WorkflowBrowserAutomation {
			stageContext = (browserAutomationProfile{}).StageContext(state)
		} else {
			stageContext = (browserInteractionProfile{}).StageContext(state)
		}
		plan := enrichPlanWithBrowserMode(stageContext, toolPlan{Name: "browser.list_tabs", Args: map[string]any{}})
		if plan.Args["browser_mode"] != "autonomous" || plan.Args["presentation"] != "hidden" || boolValue(plan.Args["surface_visible"]) {
			t.Fatalf("%s execution stage was not hidden: %#v", workflowID, plan.Args)
		}
	}
}

func TestBrowserGoalAssessmentInstructionDistinguishesActionFromCompletion(t *testing.T) {
	for _, test := range []struct {
		stage    string
		expected []string
	}{
		{stage: browserStageAssessGoalInitial, expected: []string{"clickable control", "next action", "Return progress", "a route alone is insufficient"}},
		{stage: browserStageAssessGoalAfterAction, expected: []string{"validated", "new rendered-content digest", "another distinct action"}},
		{stage: browserStageAssessGoalVisible, expected: []string{"verified the hidden result", "settled the visible rendered content", "matching destination as a control"}},
	} {
		t.Run(test.stage, func(t *testing.T) {
			state := &app.WorkflowState{
				Plan:          app.WorkflowPlan{ProfileID: app.WorkflowBrowserInteraction},
				ActiveNodeIDs: []app.WorkflowNodeID{"browser"},
				Nodes: map[app.WorkflowNodeID]app.WorkflowNodeState{
					"browser": {Stage: test.stage},
				},
			}
			context := (browserInteractionProfile{}).StageContext(state)
			for _, expected := range test.expected {
				if !strings.Contains(context.Reason, expected) {
					t.Fatalf("goal assessment instruction is missing %q: %s", expected, context.Reason)
				}
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
	state := &app.WorkflowState{
		Route: app.RouteDecision{
			Slots: app.RouteSlots{TargetKind: "url", TargetRef: "https://mail.qq.com/"},
			Facts: map[string]string{"browser_destination": "qq_mail"},
		},
		Browser: &app.BrowserWorkflowState{SchemaVersion: app.BrowserWorkflowStateSchemaVersion},
		Nodes: map[app.WorkflowNodeID]app.WorkflowNodeState{
			"browser_result": {Stage: "scan_tabs"},
		},
	}
	outcome := app.ToolOutcome{
		NodeID:  "browser_result",
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
	node := stored.Workflow.Nodes["browser_result"]
	node.Stage = "navigate_blank"
	node.ScopeRevision = 3
	node.OutcomeRefs = []app.ResourceRef{{Kind: "browser_tab", Ref: "page_1", Attributes: map[string]string{"url": "about:blank"}}}
	stored.Workflow.Nodes["browser_result"] = node
	st.SaveRun(stored)

	plan := runtime.materializeWorkflowBoundArguments(stored.ID, toolPlan{
		Name: "browser.navigate", Args: map[string]any{"url": "https://example.invalid/"},
		WorkflowID: app.WorkflowBrowserInteraction, WorkflowNodeID: "browser_result", ScopeRevision: 3,
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
	outcome := adaptBrowserSnapshotOutcome(snapshotCall, "browser_result")
	node := stored.Workflow.Nodes["browser_result"]
	node.Stage = "choose_and_click"
	node.ScopeRevision = 5
	node.OutcomeRefs = outcome.Refs
	stored.Workflow.Nodes["browser_result"] = node
	st.SaveRun(stored)

	plan := runtime.materializeWorkflowBoundArguments(stored.ID, toolPlan{
		Name: "browser.click", Args: map[string]any{"uid": "e7"},
		WorkflowID: app.WorkflowBrowserInteraction, WorkflowNodeID: "browser_result", ScopeRevision: 5,
		Capability: app.ToolCapabilityBrowserClick,
	})
	if plan.Args["page_id"] != "page_1" || plan.Args["snapshot_id"] != "snapshot_1" || plan.Args["uid"] != "snapshot_1:e7:fingerprint" {
		t.Fatalf("click arguments did not bind to the current snapshot ref: %#v", plan.Args)
	}
	definition, ok := runtime.tools.Definition("browser.click")
	if !ok {
		t.Fatal("browser.click is unavailable")
	}
	if err := runtime.validateWorkflowToolPlan(context.Background(), stored.ID, plan, definition); err != nil {
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

func TestBrowserInteractionHandoffResetDiscardsPreLoginRefsAndPreservesClickBudget(t *testing.T) {
	state := &app.WorkflowState{
		Plan:          browserRevision2Plan(app.WorkflowBrowserInteraction, app.BrowserWorkflowRevision2, true),
		ActiveNodeIDs: []app.WorkflowNodeID{"browser_result"},
		Status:        app.WorkflowStatusRunning,
		Browser:       &app.BrowserWorkflowState{SchemaVersion: 2, CompletedClicks: 1},
		Nodes: map[app.WorkflowNodeID]app.WorkflowNodeState{
			"browser_result": {
				Status: app.WorkflowNodeActive, Stage: "snapshot_after_action", Attempts: 8,
				CurrentScope: browserRevision2Scope(true), ScopeRevision: 9,
				OutcomeRefs: []app.ResourceRef{
					{Kind: "browser_page", Ref: "page_pre_login"},
					{Kind: "browser_snapshot", Ref: "snapshot_pre_login"},
					{Kind: "browser_click", Ref: "snapshot_pre_login:e1"},
				},
				TransitionActivations: map[app.TransitionID]int{
					"reuse_existing": 1, "focus_acquired": 1, "hidden_settled": 1,
					"hidden_snapshot_drifted":  browserSnapshotSettleRetryLimit,
					"visible_snapshot_drifted": 1, "visible_opened": 1, "visible_settled": 1,
					"click_recorded": 1, "continue_interaction": 1,
				},
			},
		},
	}
	state.PlanDigest = workflowPlanDigest(state.Plan)
	run := app.AgentRun{Workflow: state}

	if err := resetBrowserRevision2AfterHandoff(&run, "browser_result"); err != nil {
		t.Fatal(err)
	}
	node := run.Workflow.Nodes["browser_result"]
	if node.Stage != "scan_tabs" || node.ScopeRevision != 10 || len(node.OutcomeRefs) != 0 {
		t.Fatalf("handoff reset did not require fresh hidden acquisition: %#v", node)
	}
	if run.Workflow.Browser.CompletedClicks != 1 {
		t.Fatalf("handoff reset widened the click budget: %#v", run.Workflow.Browser)
	}
	for _, reset := range []app.TransitionID{
		"reuse_existing", "focus_acquired", "hidden_settled",
		// The pre-fix reset omitted the drift-retry and presentation budgets,
		// so a run that used them before login resumed already exhausted.
		"hidden_snapshot_drifted", "visible_snapshot_drifted", "visible_opened", "visible_settled",
	} {
		if node.TransitionActivations[reset] != 0 {
			t.Fatalf("handoff reset kept transition %s consumed: %#v", reset, node.TransitionActivations)
		}
	}
	if node.TransitionActivations["click_recorded"] != 1 || node.TransitionActivations["continue_interaction"] != 1 {
		t.Fatalf("handoff reset widened the click accounting bounds: %#v", node.TransitionActivations)
	}
}

func TestBrowserInteractionHandoffResetRejectsForeignActiveNode(t *testing.T) {
	plan := browserRevision2Plan(app.WorkflowBrowserInteraction, app.BrowserWorkflowRevision2, true)
	state := &app.WorkflowState{
		Plan: plan, PlanDigest: workflowPlanDigest(plan), Status: app.WorkflowStatusRunning,
		ActiveNodeIDs: []app.WorkflowNodeID{"browser_result"},
		Nodes: map[app.WorkflowNodeID]app.WorkflowNodeState{
			"browser_result": {Status: app.WorkflowNodeActive, Stage: "snapshot_hidden"},
		},
	}
	run := app.AgentRun{Workflow: state}
	if err := resetBrowserRevision2AfterHandoff(&run, "other_node"); err == nil {
		t.Fatal("foreign handoff node unexpectedly reset the persisted workflow")
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
	if !node.InitialScope.MaterializeAll || len(node.InitialScope.Requirements) != 10 || node.MaxAttempts != 32 {
		t.Fatalf("browser.interaction lost its bounded full-lifecycle scope: %#v", node)
	}
	stages := map[string][]string{}
	for _, rule := range node.StageCapabilities {
		stages[rule.Stage] = rule.Capabilities
	}
	for stage, capability := range map[string]string{
		"health_check":             app.ToolCapabilityBrowserHealth,
		"scan_tabs":                app.ToolCapabilityBrowserListTabs,
		"focus_existing":           app.ToolCapabilityBrowserFocus,
		"navigate_blank":           app.ToolCapabilityBrowserNavigate,
		"open_new":                 app.ToolCapabilityBrowserOpen,
		"settle_hidden":            app.ToolCapabilityBrowserWait,
		"snapshot_hidden":          app.ToolCapabilityBrowserSnapshot,
		"assess_goal_initial":      app.ToolCapabilityBrowserGoalAssess,
		"choose_and_click":         app.ToolCapabilityBrowserClick,
		"settle_after_action":      app.ToolCapabilityBrowserWait,
		"snapshot_after_action":    app.ToolCapabilityBrowserSnapshot,
		"validate_transition":      app.ToolCapabilityBrowserTransitionValidate,
		"assess_goal_after_action": app.ToolCapabilityBrowserGoalAssess,
		"present_visible":          app.ToolCapabilityBrowserOpen,
		"settle_visible":           app.ToolCapabilityBrowserWait,
		"snapshot_visible":         app.ToolCapabilityBrowserSnapshot,
		"assess_goal_visible":      app.ToolCapabilityBrowserGoalAssess,
	} {
		if len(stages[stage]) != 1 || stages[stage][0] != capability {
			t.Fatalf("stage %q escaped its capability boundary: %#v", stage, stages[stage])
		}
	}
}

func TestBrowserSettleArgumentsCarryFrozenRegisteredDestinationKind(t *testing.T) {
	state := &app.WorkflowState{
		Browser: &app.BrowserWorkflowState{
			Target: app.BrowserTargetDescriptor{
				TargetKind:    app.BrowserTargetRegisteredDestination,
				CanonicalURL:  "https://mail.qq.com/",
				DestinationID: "qq_mail",
			},
		},
		ActiveNodeIDs: []app.WorkflowNodeID{"browser_result"},
		Nodes: map[app.WorkflowNodeID]app.WorkflowNodeState{
			"browser_result": {Stage: "settle_hidden"},
		},
	}

	args := (browserInteractionProfile{}).DirectStageArguments(state)
	if args["expected_url"] != "https://mail.qq.com/" ||
		args["target_kind"] != string(app.BrowserTargetRegisteredDestination) {
		t.Fatalf("registered destination settle args = %#v", args)
	}
}

func TestBrowserVisibleSettleArgumentsUseVerifiedHiddenResultURL(t *testing.T) {
	state := &app.WorkflowState{
		Browser: &app.BrowserWorkflowState{
			Target: app.BrowserTargetDescriptor{
				TargetKind:    app.BrowserTargetRegisteredDestination,
				CanonicalURL:  "https://mail.qq.com/",
				DestinationID: "qq_mail",
			},
			Result: &app.BrowserResultEvidence{
				Target: app.BrowserTargetDescriptor{
					TargetKind:    app.BrowserTargetRegisteredDestination,
					CanonicalURL:  "https://wx.mail.qq.com/home/index#/list/4",
					DestinationID: "qq_mail",
				},
			},
		},
		ActiveNodeIDs: []app.WorkflowNodeID{"browser_result"},
		Nodes: map[app.WorkflowNodeID]app.WorkflowNodeState{
			"browser_result": {Stage: "settle_visible"},
		},
	}

	args := (browserInteractionProfile{}).DirectStageArguments(state)
	if args["expected_url"] != "https://wx.mail.qq.com/home/index#/list/4" ||
		args["target_kind"] != string(app.BrowserTargetRegisteredDestination) {
		t.Fatalf("visible settle args = %#v", args)
	}
}

func TestBrowserSessionGenerationSurvivesJSONNumberCoercion(t *testing.T) {
	const generation = uint64(1785323495538)
	attributes := browserOutcomeIdentityAttributes(map[string]any{
		"session_generation": float64(generation),
	}, nil)
	if attributes["session_generation"] != "1785323495538" {
		t.Fatalf("session generation attribute = %q", attributes["session_generation"])
	}
	if got := browserRefGeneration(app.ResourceRef{Attributes: attributes}); got != generation {
		t.Fatalf("parsed session generation = %d, want %d", got, generation)
	}

	unsafe := browserOutcomeIdentityAttributes(map[string]any{
		"session_generation": float64(1 << 54),
	}, nil)
	if unsafe["session_generation"] != "" {
		t.Fatalf("unsafe JSON generation was accepted: %#v", unsafe)
	}
}

func TestBrowserInteractionRetriesSnapshotThatChangedAfterSettleOrHasNoEvidence(t *testing.T) {
	for _, test := range []struct {
		name           string
		settledURL     string
		snapshotURL    string
		includeElement bool
	}{
		{
			name:           "route changed after settle",
			settledURL:     "https://wx.mail.qq.com/list/readtemplate",
			snapshotURL:    "https://wx.mail.qq.com/home/index#/list/1/1",
			includeElement: true,
		},
		{
			name:        "interaction evidence not rendered",
			settledURL:  "https://wx.mail.qq.com/home/index#/list/1/1",
			snapshotURL: "https://wx.mail.qq.com/home/index#/list/1/1",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := browserRevision2Plan(app.WorkflowBrowserInteraction, app.BrowserWorkflowRevision2, true)
			initialRefs := []app.ResourceRef{{
				Kind: "browser_page", Ref: "page_1",
				Attributes: map[string]string{"url": test.settledURL, "state_digest": "settled"},
			}}
			state := &app.WorkflowState{
				Plan: plan, PlanDigest: workflowPlanDigest(plan), Status: app.WorkflowStatusRunning,
				Route: app.RouteDecision{
					Slots: app.RouteSlots{TargetKind: "url", TargetRef: "https://mail.qq.com/"},
					Facts: map[string]string{"browser_destination": "qq_mail"},
				},
				Browser: &app.BrowserWorkflowState{Target: app.BrowserTargetDescriptor{
					TargetKind: app.BrowserTargetRegisteredDestination, CanonicalURL: "https://mail.qq.com/",
					DestinationID: "qq_mail",
				}},
				ActiveNodeIDs: []app.WorkflowNodeID{"browser_result"},
				Nodes: map[app.WorkflowNodeID]app.WorkflowNodeState{
					"browser_result": {
						Status: app.WorkflowNodeActive, Stage: "snapshot_hidden",
						CurrentScope: plan.Nodes[0].InitialScope, ScopeRevision: 1,
						TransitionActivations: map[app.TransitionID]int{}, OutcomeRefs: initialRefs,
					},
				},
			}
			refs := []app.ResourceRef{
				{
					Kind: "browser_page", Ref: "page_1",
					Attributes: map[string]string{"url": test.snapshotURL},
				},
				{
					Kind: "browser_snapshot", Ref: "snapshot_1",
					Attributes: map[string]string{
						"digest": "digest_1", "content_digest": "content_1", "session_generation": "7", "presentation": "hidden",
					},
				},
			}
			if test.includeElement {
				refs = append(refs, app.ResourceRef{Kind: "browser_element", Ref: "snapshot_1:e1"})
			}
			outcome := app.ToolOutcome{
				ID: "outcome_snapshot", ToolCallID: "tc_snapshot", Tool: "browser.snapshot",
				NodeID: "browser_result", Status: "completed",
				Signals: []app.OutcomeSignal{app.OutcomeSignalSnapshotAvailable}, Refs: refs,
			}

			assessment := (browserInteractionProfile{}).Assess(state, outcome)
			if assessment.Status != app.AssessmentNeedsMoreEvidence ||
				assessment.ReasonCode != "settle_hidden" ||
				len(assessment.Signals) != 1 ||
				assessment.Signals[0] != app.OutcomeSignalHiddenSnapshotDrifted ||
				assessment.SelectedRefs == nil || len(assessment.SelectedRefs) != 0 {
				t.Fatalf("snapshot retry assessment = %#v", assessment)
			}
			run := app.AgentRun{Workflow: state}
			transitioned, err := applyWorkflowOutcome(&run, outcome, assessment)
			if err != nil {
				t.Fatal(err)
			}
			node := run.Workflow.Nodes["browser_result"]
			if !transitioned || node.Stage != "settle_hidden" || len(node.OutcomeRefs) != len(initialRefs) {
				t.Fatalf("snapshot retry persisted drifting evidence: transitioned=%t node=%#v", transitioned, node)
			}
		})
	}
}

func TestBrowserInteractionRepeatedPostSnapshotFailsClosed(t *testing.T) {
	state := &app.WorkflowState{
		Route: app.RouteDecision{Slots: app.RouteSlots{TargetKind: "url", TargetRef: "https://example.com/"}},
		Browser: &app.BrowserWorkflowState{
			SchemaVersion: app.BrowserWorkflowStateSchemaVersion,
			Target:        app.BrowserTargetDescriptor{TargetKind: app.BrowserTargetExplicitURL, CanonicalURL: "https://example.com/"},
		},
		ActiveNodeIDs: []app.WorkflowNodeID{"browser_result"},
		Nodes: map[app.WorkflowNodeID]app.WorkflowNodeState{
			"browser_result": {Stage: "snapshot_after_action"},
		},
	}
	outcome := app.ToolOutcome{
		NodeID:  "browser_result",
		Signals: []app.OutcomeSignal{app.OutcomeSignalSnapshotAvailable},
		Refs: []app.ResourceRef{
			{Kind: "browser_page", Ref: "page_1", Attributes: map[string]string{"url": "https://example.com/"}},
			{
				Kind: "browser_snapshot", Ref: "snapshot_2",
				Attributes: map[string]string{
					"digest": "digest_2", "content_digest": "content_2", "session_generation": "2",
					"previous_snapshot_id": "snapshot_1", "repeated": "true",
				},
			},
		},
	}
	assessment := (browserInteractionProfile{}).Assess(state, outcome)
	if assessment.Status != app.AssessmentBlocked || assessment.ReasonCode != "interaction_loop_detected" {
		t.Fatalf("repeated post-click state did not fail closed: %#v", assessment)
	}
}

func TestBrowserInteractionConsequentialWordsDoNotOverrideSemanticRouting(t *testing.T) {
	runtime, _, _, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	for _, goal := range []string{
		"点击当前页面的删除账户按钮",
		"click Send on the current page",
		"点击当前页面的确认订单按钮",
	} {
		route := mustRouteIntent(t, runtime, goal)
		if route.Status != app.RouteMatched || len(route.CapabilityPath) != 2 || route.CapabilityPath[1] != app.CapabilityBrowserInteraction {
			t.Fatalf("consequential control words overrode semantic routing: goal=%q route=%#v", goal, route)
		}
	}
}

func TestWorkflowPromptContextKeepsStageAndProvidedToolList(t *testing.T) {
	stageContext := workflowStageContext{WorkflowID: app.WorkflowBrowserInteraction, Reason: "workflow_stage: validate_transition. Validate before goal assessment."}
	tools := []app.ToolDefinition{{Name: "browser.snapshot"}, {Name: "browser.click"}, {Name: "browser.validate_transition"}}
	prompt := appendWorkflowStepContext("WORKFLOW_STEP_REQUEST", stageContext, tools)
	for _, expected := range []string{"workflow_stage: validate_transition", "browser.snapshot", "browser.click", "browser.validate_transition"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("workflow prompt context lost %q: %s", expected, prompt)
		}
	}
}

func TestAdaptBrowserClickOutcomeClassifiesFailuresByTypedCodeWithProseFallback(t *testing.T) {
	for _, test := range []struct {
		name string
		call app.ToolCall
		want app.OutcomeSignal
	}{
		{
			// The typed code decides even when the redacted prose carries none
			// of the legacy marker words.
			name: "typed unsafe click code",
			call: app.ToolCall{Tool: "browser.click", Status: "failed", ErrorCode: string(app.ToolErrorUnsafeClickTarget), Error: "rejected"},
			want: app.OutcomeSignalUnsafeClickTarget,
		},
		{
			name: "typed stale snapshot code",
			call: app.ToolCall{Tool: "browser.click", Status: "failed", ErrorCode: string(app.ToolErrorSnapshotStale), Error: "rejected"},
			want: app.OutcomeSignalSnapshotStale,
		},
		{
			// Records persisted before ErrorCode existed still classify
			// through the documented prose fallback.
			name: "legacy record falls back to prose",
			call: app.ToolCall{Tool: "browser.click", Status: "failed", Error: "stale or unknown snapshot; take a new browser.snapshot"},
			want: app.OutcomeSignalSnapshotStale,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			outcome := adaptBrowserClickOutcome(test.call, "browser_result")
			if len(outcome.Signals) != 1 || outcome.Signals[0] != test.want {
				t.Fatalf("click failure was misclassified: %#v", outcome.Signals)
			}
		})
	}
}

func TestUnsafeBrowserClickGroundingUsesTypedErrorCode(t *testing.T) {
	calls := []app.ToolCall{{
		Tool: "browser.click", Status: "failed",
		ErrorCode: string(app.ToolErrorUnsafeClickTarget), Error: "交互被拒绝",
	}}
	answer, ok := groundedBrowserAutomationSummary("点击当前页面的下一步", "fallback", calls)
	if !ok || answer != "页面交互已阻止：目标点击可能产生不允许的后果。" {
		t.Fatalf("typed unsafe click code was not grounded: %q ok=%v", answer, ok)
	}
}

func TestUnsafeBrowserClickGroundingOverridesEarlierTabEvidence(t *testing.T) {
	calls := []app.ToolCall{
		{Tool: "browser.list_tabs", Status: "completed", Result: map[string]any{"pages": []any{map[string]any{"page_id": "page_1", "selected": true}}}},
		{Tool: "browser.click", Status: "failed", Error: `unsafe click target "Delete account" is outside browser.interaction revision 2`},
	}
	answer, ok := groundedBrowserAutomationSummary("点击当前页面的下一步", "fallback", calls)
	if !ok || answer != "页面交互已阻止：目标点击可能产生不允许的后果。" {
		t.Fatalf("unsafe click failure was hidden by earlier browser evidence: %q ok=%v", answer, ok)
	}
}

func TestVerifiedVisibleBrowserResultGroundingOverridesEarlierBlankTab(t *testing.T) {
	calls := []app.ToolCall{
		{
			Tool: "browser.list_tabs", Status: "completed",
			Result: map[string]any{"pages": []any{map[string]any{
				"page_id": "page_1", "url": "about:blank", "selected": true,
			}}},
		},
		{
			Tool: "browser.snapshot", Status: "completed",
			Result: map[string]any{"output": map[string]any{"snapshot": map[string]any{
				"snapshot_id":  "snapshot_visible",
				"url":          "https://wx.mail.qq.com/home/index#/list/4",
				"presentation": string(app.BrowserPresentationVisible),
			}}},
		},
		{
			Tool: "browser.assess_goal", Status: "completed",
			Result: map[string]any{
				"status": "succeeded", "goal_satisfied": true, "snapshot_id": "snapshot_visible",
			},
		},
	}

	answer, ok := groundedBrowserAutomationSummary("打开qq邮箱的草稿箱", "", calls)
	if !ok || !strings.Contains(answer, "浏览器操作已完成") ||
		!strings.Contains(answer, "https://wx.mail.qq.com/home/index#/list/4") ||
		strings.Contains(answer, "about:blank") {
		t.Fatalf("verified browser result summary = %q, ok=%v", answer, ok)
	}
}
