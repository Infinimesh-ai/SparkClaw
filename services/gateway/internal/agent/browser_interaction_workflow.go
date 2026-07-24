package agent

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/app"

const browserInteractionMaxClicks = 3

type browserInteractionProfile struct{}

func (browserInteractionProfile) ID() app.WorkflowID { return app.WorkflowBrowserInteraction }
func (browserInteractionProfile) Revision() int      { return 1 }
func (browserInteractionProfile) Capability() app.CapabilityID {
	return app.CapabilityBrowserInteraction
}
func (browserInteractionProfile) RoutingSemantics() workflowRoutingSemantics {
	return workflowRoutingSemantics{Variants: []workflowRoutingVariant{{
		Key: "interact", Route: workflowRouteTemplate{Operation: app.RouteOperationInteract},
		EmbedTexts: []string{
			"点击当前页面的下一步", "打开苹果官网并点击 Mac", "打开QQ邮箱的草稿箱", "Click the Learn more button on https://example.com", "在这个标签页点开详情",
		},
		TreeDescription: "Inspect one managed browser page and perform a bounded verified click goal. Opening a nested in-site section such as drafts, inbox, or details requires a page click and is interaction, not merely opening a registered destination. Typing, form submission, login, payment, upload, deletion, or other consequential interaction is unsupported.",
		HardNegatives:   []string{"打开这个网址", "输入用户名并登录", "查询网站上的最新价格", "点击文档里的链接", "购买这个商品"},
	}}}
}
func (browserInteractionProfile) Finalization() workflowFinalizationMode {
	return workflowFinalizationGrounded
}

func unsupportedBrowserInteractionIntent(lower string) bool {
	return containsEnglishSemanticTerm(lower,
		"type", "fill", "select", "check", "upload", "download", "login", "sign in", "authenticate", "submit",
		"delete", "remove", "publish", "send", "buy", "purchase", "pay", "payment", "checkout", "place order", "confirm order",
		"log out", "logout", "sign out", "authorize", "grant access",
	) || containsAny(lower,
		"输入", "填写", "选择", "勾选", "上传", "下载", "登录", "认证", "验证码", "提交表单",
		"删除", "移除", "发布", "发送", "购买", "付款", "支付", "下单", "确认订单", "退出登录", "注销", "授权",
	)
}

func explicitCurrentBrowserTab(lower string) bool {
	return containsAny(lower, "current page", "current tab", "selected tab", "当前页面", "当前网页", "当前标签页", "当前tab", "这个页面")
}

func (p browserInteractionProfile) Resolve(route app.RouteDecision, sourceTurnID string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	target := app.TargetRef{Kind: app.TargetKindBrowserCurrentTab, Ref: route.Slots.TargetRef}
	if route.Slots.TargetKind == "url" {
		target = app.TargetRef{Kind: app.TargetKindExplicitURL, Ref: route.Slots.TargetRef}
	}
	intent := singleObjectiveIntent(sourceTurnID, app.IntentDomainWeb, app.IntentOperationAutomate, target, app.DataScopePublic)
	nodeID := app.WorkflowNodeID("browser_interaction")
	scope := browserInteractionScope()
	return intent, app.WorkflowPlan{
		SchemaVersion: 1, ProfileID: p.ID(), ProfileRevision: p.Revision(),
		InitialNodeIDs: []app.WorkflowNodeID{nodeID}, Completion: app.CompletionEvidence,
		Nodes: []app.WorkflowNode{{
			ID: nodeID, InitialStage: "health_check",
			Goal:         app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "Complete the frozen managed-browser click goal with mandatory snapshot verification", Completion: app.CompletionEvidence},
			InitialScope: scope,
			Transitions: []app.ScopeTransition{
				browserInteractionTransition("health_ready", "scan_tabs", app.OutcomeSignalBrowserHealthy, 1, scope),
				browserInteractionTransition("reuse_existing", "focus_existing", app.OutcomeSignalTargetTabExists, 1, scope),
				browserInteractionTransition("reuse_blank", "navigate_blank", app.OutcomeSignalTargetTabBlank, 1, scope),
				browserInteractionTransition("open_missing", "open_new", app.OutcomeSignalTargetTabMissing, 1, scope),
				browserInteractionTransition("focused", "snapshot_before_action", app.OutcomeSignalFocusCompleted, 1, scope),
				browserInteractionTransition("opened", "snapshot_before_action", app.OutcomeSignalOpenCompleted, 1, scope),
				browserInteractionTransition("navigated", "snapshot_before_action", app.OutcomeSignalNavigateCompleted, 1, scope),
				browserInteractionTransition("post_snapshot_ready", "verify_action", app.OutcomeSignalInteractionVerificationRequired, browserInteractionMaxClicks, scope),
				browserInteractionTransition("snapshot_ready", "choose_and_click", app.OutcomeSignalSnapshotAvailable, browserInteractionMaxClicks+1, scope),
				browserInteractionTransition("click_recorded", "snapshot_after_action", app.OutcomeSignalClickCompleted, browserInteractionMaxClicks, scope),
				browserInteractionTransition("wait_settled", "snapshot_after_action", app.OutcomeSignalWaitCompleted, browserInteractionMaxClicks, scope),
				browserInteractionTransition("snapshot_stale", "snapshot_before_action", app.OutcomeSignalSnapshotStale, browserInteractionMaxClicks, scope),
				browserInteractionTransition("continue_interaction", "choose_and_click", app.OutcomeSignalInteractionProgress, browserInteractionMaxClicks-1, scope),
			},
			ArgumentBindings: []app.ArgumentBinding{
				{Capability: app.ToolCapabilityBrowserFocus, Argument: "page_id", ResourceKind: "browser_tab", Source: app.ArgumentBindingOutcomeRef},
				{Capability: app.ToolCapabilityBrowserOpen, Argument: "url", ResourceKind: "url", Source: app.ArgumentBindingIntentTarget, TargetKinds: []app.TargetKind{app.TargetKindExplicitURL}},
				{Capability: app.ToolCapabilityBrowserNavigate, Argument: "url", ResourceKind: "url", Source: app.ArgumentBindingIntentTarget, TargetKinds: []app.TargetKind{app.TargetKindExplicitURL}},
				{Capability: app.ToolCapabilityBrowserNavigate, Argument: "page_id", ResourceKind: "browser_tab", Source: app.ArgumentBindingOutcomeRef},
				{Capability: app.ToolCapabilityBrowserSnapshot, Argument: "page_id", ResourceKind: "browser_page", Source: app.ArgumentBindingOutcomeRef},
				{Capability: app.ToolCapabilityBrowserSnapshot, Argument: "interaction_goal", ResourceKind: "query", Source: app.ArgumentBindingRouteSlot, SourceKey: "query"},
				{Capability: app.ToolCapabilityBrowserWait, Argument: "page_id", ResourceKind: "browser_page", Source: app.ArgumentBindingOutcomeRef},
				{Capability: app.ToolCapabilityBrowserClick, Argument: "page_id", ResourceKind: "browser_page", Source: app.ArgumentBindingOutcomeRef},
				{Capability: app.ToolCapabilityBrowserClick, Argument: "snapshot_id", ResourceKind: "browser_snapshot", Source: app.ArgumentBindingOutcomeRef},
				{Capability: app.ToolCapabilityBrowserClick, Argument: "uid", ResourceKind: "browser_element", Source: app.ArgumentBindingOutcomeRef},
				{Capability: app.ToolCapabilityBrowserVerify, Argument: "before_snapshot_id", ResourceKind: "browser_snapshot", Source: app.ArgumentBindingOutcomeRef},
				{Capability: app.ToolCapabilityBrowserVerify, Argument: "after_snapshot_id", ResourceKind: "browser_snapshot", Source: app.ArgumentBindingOutcomeRef},
				{Capability: app.ToolCapabilityBrowserVerify, Argument: "element_ref", ResourceKind: "browser_click", Source: app.ArgumentBindingOutcomeRef},
			},
			StageCapabilities: []app.StageCapabilityRule{
				{Stage: "health_check", Capabilities: []string{app.ToolCapabilityBrowserHealth}},
				{Stage: "scan_tabs", Capabilities: []string{app.ToolCapabilityBrowserListTabs}},
				{Stage: "focus_existing", Capabilities: []string{app.ToolCapabilityBrowserFocus}},
				{Stage: "navigate_blank", Capabilities: []string{app.ToolCapabilityBrowserNavigate}},
				{Stage: "open_new", Capabilities: []string{app.ToolCapabilityBrowserOpen}},
				{Stage: "snapshot_before_action", Capabilities: []string{app.ToolCapabilityBrowserSnapshot}},
				{Stage: "choose_and_click", Capabilities: []string{app.ToolCapabilityBrowserClick}},
				{Stage: "snapshot_after_action", Capabilities: []string{app.ToolCapabilityBrowserWait, app.ToolCapabilityBrowserSnapshot}},
				{Stage: "verify_action", Capabilities: []string{app.ToolCapabilityBrowserVerify}},
			},
			AllowedRisks: []app.RiskLevel{app.RiskRead, app.RiskDraft}, MaxAttempts: 24,
		}},
	}, nil
}

func browserInteractionScope() app.CapabilityScope {
	return app.CapabilityScope{MaterializeAll: true, Requirements: []app.CapabilityRequirement{
		{Name: app.ToolCapabilityBrowserHealth},
		{Name: app.ToolCapabilityBrowserListTabs},
		{Name: app.ToolCapabilityBrowserFocus},
		{Name: app.ToolCapabilityBrowserOpen},
		{Name: app.ToolCapabilityBrowserNavigate},
		{Name: app.ToolCapabilityBrowserSnapshot},
		{Name: app.ToolCapabilityBrowserWait},
		{Name: app.ToolCapabilityBrowserClick},
		{Name: app.ToolCapabilityBrowserVerify},
	}}
}

func browserInteractionTransition(id, stage string, signal app.OutcomeSignal, max int, scope app.CapabilityScope) app.ScopeTransition {
	return app.ScopeTransition{
		ID: app.TransitionID(id), NextStage: stage,
		On:      app.TransitionPredicate{OutcomeSignals: []app.OutcomeSignal{signal}, Assessments: []app.AssessmentStatus{app.AssessmentNeedsMoreEvidence}},
		Replace: &scope, MaxActivations: max,
	}
}

func (browserInteractionProfile) Prepare(*app.WorkflowState) (app.TransitionID, bool, error) {
	return "", false, nil
}

func (browserInteractionProfile) Assess(state *app.WorkflowState, outcome app.ToolOutcome) app.NodeAssessment {
	assessment := baseNodeAssessment(outcome)
	node := state.Nodes[outcome.NodeID]
	switch node.Stage {
	case "health_check":
		if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalBrowserHealthy) {
			return browserInteractionNeedsMore(assessment, app.OutcomeSignalBrowserHealthy, "scan_tabs")
		}
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "browser_provider_unavailable"
	case "scan_tabs":
		selected, signal, reason := selectBrowserInteractionTab(state.Route, outcome.Refs)
		if reason != "" {
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, reason
			return assessment
		}
		assessment = browserInteractionNeedsMore(assessment, signal, browserInteractionStageForTabSignal(signal))
		if selected != nil {
			assessment.SelectedRefs = []app.ResourceRef{*selected}
		}
	case "focus_existing":
		if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalFocusCompleted) {
			assessment = browserInteractionNeedsMore(assessment, app.OutcomeSignalFocusCompleted, "snapshot_before_action")
		} else {
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "browser_focus_failed"
		}
	case "open_new":
		if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalOpenCompleted) {
			assessment = browserInteractionNeedsMore(assessment, app.OutcomeSignalOpenCompleted, "snapshot_before_action")
		} else {
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "browser_open_failed"
		}
	case "navigate_blank":
		if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalNavigateCompleted) {
			assessment = browserInteractionNeedsMore(assessment, app.OutcomeSignalNavigateCompleted, "snapshot_before_action")
		} else {
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "browser_navigate_failed"
		}
	case "snapshot_before_action":
		if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalSnapshotAvailable) && outcomeRefCount(outcome.Refs, "browser_element") > 0 {
			assessment = browserInteractionNeedsMore(assessment, app.OutcomeSignalSnapshotAvailable, "choose_and_click")
		} else {
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "interaction_target_unavailable"
		}
	case "choose_and_click":
		switch {
		case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalUnsafeClickTarget):
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "unsafe_click_target"
		case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalSnapshotStale):
			assessment = browserInteractionNeedsMore(assessment, app.OutcomeSignalSnapshotStale, "snapshot_before_action")
		case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalClickCompleted):
			assessment = browserInteractionNeedsMore(assessment, app.OutcomeSignalClickCompleted, "snapshot_after_action")
		default:
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "browser_click_failed"
		}
	case "snapshot_after_action":
		if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalWaitCompleted) {
			assessment = browserInteractionNeedsMore(assessment, app.OutcomeSignalWaitCompleted, "snapshot_after_action")
			return assessment
		}
		if !containsOutcomeSignal(outcome.Signals, app.OutcomeSignalSnapshotAvailable) {
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "interaction_verification_snapshot_unavailable"
			return assessment
		}
		if browserSnapshotOutcomeRepeated(outcome.Refs) {
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "interaction_loop_detected"
			return assessment
		}
		assessment = browserInteractionNeedsMore(assessment, app.OutcomeSignalInteractionVerificationRequired, "verify_action")
	case "verify_action":
		switch {
		case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalInteractionGoalSatisfied):
			assessment.Status, assessment.ReasonCode = app.AssessmentComplete, "interaction_goal_satisfied"
		case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalInteractionProgress):
			assessment = browserInteractionNeedsMore(assessment, app.OutcomeSignalInteractionProgress, "choose_and_click")
		case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalInteractionLoopDetected):
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "interaction_loop_detected"
		case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalInteractionAttemptLimit):
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "interaction_attempt_limit"
		case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalUnsafeClickTarget):
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "unsafe_click_target"
		default:
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "interaction_verification_failed"
		}
	default:
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "browser_interaction_stage_invalid"
	}
	return assessment
}

func browserInteractionNeedsMore(assessment app.NodeAssessment, signal app.OutcomeSignal, nextStage string) app.NodeAssessment {
	assessment.Status = app.AssessmentNeedsMoreEvidence
	assessment.Signals = []app.OutcomeSignal{signal}
	assessment.ReasonCode = nextStage
	return assessment
}

func selectBrowserInteractionTab(route app.RouteDecision, refs []app.ResourceRef) (*app.ResourceRef, app.OutcomeSignal, string) {
	selected := []app.ResourceRef{}
	matches := []app.ResourceRef{}
	blank := []app.ResourceRef{}
	for _, ref := range refs {
		if ref.Kind != "browser_tab" {
			continue
		}
		if ref.Attributes["selected"] == "true" {
			selected = append(selected, ref)
		}
		url := normalizeBrowserURL(ref.Attributes["url"])
		if route.Slots.TargetKind == "url" && browserTargetMatchesURL(route.Slots.TargetRef, route.Facts["browser_destination"], url) {
			matches = append(matches, ref)
		}
		if ref.Attributes["selected"] == "true" && (url == "about:blank" || url == "chrome://newtab/" || url == "") {
			blank = append(blank, ref)
		}
	}
	if route.Slots.TargetKind == string(app.TargetKindBrowserCurrentTab) {
		if len(selected) == 1 {
			return &selected[0], app.OutcomeSignalTargetTabExists, ""
		}
		return nil, "", "browser_current_tab_unavailable"
	}
	for _, ref := range matches {
		if ref.Attributes["selected"] == "true" {
			return &ref, app.OutcomeSignalTargetTabExists, ""
		}
	}
	if len(matches) == 1 {
		return &matches[0], app.OutcomeSignalTargetTabExists, ""
	}
	if len(matches) > 1 {
		return nil, "", "browser_tab_ambiguous"
	}
	if len(blank) == 1 {
		return &blank[0], app.OutcomeSignalTargetTabBlank, ""
	}
	return nil, app.OutcomeSignalTargetTabMissing, ""
}

func browserInteractionStageForTabSignal(signal app.OutcomeSignal) string {
	switch signal {
	case app.OutcomeSignalTargetTabExists:
		return "focus_existing"
	case app.OutcomeSignalTargetTabBlank:
		return "navigate_blank"
	default:
		return "open_new"
	}
}

func outcomeRefCount(refs []app.ResourceRef, kind string) int {
	count := 0
	for _, ref := range refs {
		if ref.Kind == kind {
			count++
		}
	}
	return count
}

func browserSnapshotOutcomeRepeated(refs []app.ResourceRef) bool {
	for _, ref := range refs {
		if ref.Kind == "browser_snapshot" && ref.Attributes["previous_snapshot_id"] != "" && ref.Attributes["repeated"] == "true" {
			return true
		}
	}
	return false
}

func (browserInteractionProfile) Hint(state *app.WorkflowState) workflowExecutionHint {
	nodeID := state.ActiveNodeIDs[0]
	stage := state.Nodes[nodeID].Stage
	hint := workflowHint(state, "browse", "web", "public", "collaborative", "workflow_stage: "+stage+". Use only the capability valid for this stage; every click requires a post-click snapshot and browser.verify before another click. browser.verify verdict must be exactly success, progress, or failure.")
	hint.EstimatedRisk = app.RiskDraft
	return hint
}

func (browserInteractionProfile) TransitionInstruction(_ app.ToolOutcome, assessment app.NodeAssessment) string {
	if assessment.ReasonCode == "" {
		return ""
	}
	instruction := "workflow_stage: " + assessment.ReasonCode
	if len(assessment.SelectedRefs) == 1 && assessment.SelectedRefs[0].Kind == "browser_tab" {
		instruction += " page_id=" + assessment.SelectedRefs[0].Ref
	}
	return instruction
}
