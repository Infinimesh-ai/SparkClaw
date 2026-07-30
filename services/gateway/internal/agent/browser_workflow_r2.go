package agent

import (
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const browserWorkflowRevision2 = 2
const browserResultPresentationReason = "workflow_result_presentation"
const browserSnapshotSettleRetryLimit = 2

type browserAutomationProfile struct{}

func (browserAutomationProfile) ID() app.WorkflowID           { return app.WorkflowBrowserAutomation }
func (browserAutomationProfile) Revision() int                { return browserWorkflowRevision2 }
func (browserAutomationProfile) Capability() app.CapabilityID { return app.CapabilityBrowserAutomation }
func (browserAutomationProfile) RoutingSemantics() workflowRoutingSemantics {
	return workflowRoutingSemantics{Variants: []workflowRoutingVariant{{
		Key: "open", Route: workflowRouteTemplate{Operation: app.RouteOperationOpen},
		EmbedTexts: []string{
			"打开 https://example.com", "访问苹果官网", "Open this URL in the browser", "切换到已打开的项目页面",
		},
		TreeDescription: "Open or focus exactly one explicit URL or registered browser destination. A request to enter a nested in-site section such as drafts, inbox, or details is browser interaction, not this candidate. Do not use for page clicks, typing, login, research, or extracting current facts.",
		HardNegatives:   []string{"点击网页按钮", "打开QQ邮箱的草稿箱", "打开邮箱收件箱", "在网站里搜索商品", "查询官网价格", "登录这个网站", "打开本地文档"},
	}}}
}
func (browserAutomationProfile) Finalization() workflowFinalizationMode {
	return workflowFinalizationGrounded
}
func (p browserAutomationProfile) Resolve(route app.RouteDecision, sourceTurnID string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	intent := singleObjectiveIntent(sourceTurnID, app.IntentDomainWeb, app.IntentOperationAutomate,
		app.TargetRef{Kind: app.TargetKindExplicitURL, Ref: route.Slots.TargetRef}, app.DataScopePublic)
	return intent, browserRevision2Plan(p.ID(), p.Revision(), false), nil
}
func (browserAutomationProfile) Prepare(state *app.WorkflowState) (workflowPreparation, error) {
	ensureBrowserWorkflowState(state)
	return workflowPreparation{}, nil
}
func (browserAutomationProfile) Assess(state *app.WorkflowState, outcome app.ToolOutcome) app.NodeAssessment {
	return assessBrowserRevision2(state, outcome, false)
}
func (browserAutomationProfile) StageContext(state *app.WorkflowState) workflowStageContext {
	return browserRevision2StageContext(state, false)
}
func (browserAutomationProfile) TransitionInstruction(_ app.ToolOutcome, assessment app.NodeAssessment) string {
	return browserRevision2TransitionInstruction(assessment)
}
func (browserAutomationProfile) DirectStage(state *app.WorkflowState) bool {
	return browserRevision2DirectStage(state, false)
}
func (browserAutomationProfile) DirectStageArguments(state *app.WorkflowState) map[string]any {
	return browserRevision2DirectArguments(state)
}

type browserInteractionProfile struct{}

func (browserInteractionProfile) ID() app.WorkflowID { return app.WorkflowBrowserInteraction }
func (browserInteractionProfile) Revision() int      { return browserWorkflowRevision2 }
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
func (p browserInteractionProfile) Resolve(route app.RouteDecision, sourceTurnID string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	target := app.TargetRef{Kind: app.TargetKindBrowserCurrentTab, Ref: route.Slots.TargetRef}
	if route.Slots.TargetKind == "url" {
		target = app.TargetRef{Kind: app.TargetKindExplicitURL, Ref: route.Slots.TargetRef}
	}
	intent := singleObjectiveIntent(sourceTurnID, app.IntentDomainWeb, app.IntentOperationAutomate, target, app.DataScopePublic)
	return intent, browserRevision2Plan(p.ID(), p.Revision(), true), nil
}
func (browserInteractionProfile) Prepare(state *app.WorkflowState) (workflowPreparation, error) {
	ensureBrowserWorkflowState(state)
	return workflowPreparation{}, nil
}
func (browserInteractionProfile) Assess(state *app.WorkflowState, outcome app.ToolOutcome) app.NodeAssessment {
	return assessBrowserRevision2(state, outcome, true)
}
func (browserInteractionProfile) StageContext(state *app.WorkflowState) workflowStageContext {
	return browserRevision2StageContext(state, true)
}
func (browserInteractionProfile) TransitionInstruction(_ app.ToolOutcome, assessment app.NodeAssessment) string {
	return browserRevision2TransitionInstruction(assessment)
}
func (browserInteractionProfile) DirectStage(state *app.WorkflowState) bool {
	return browserRevision2DirectStage(state, true)
}
func (browserInteractionProfile) DirectStageArguments(state *app.WorkflowState) map[string]any {
	return browserRevision2DirectArguments(state)
}

func browserRevision2Plan(id app.WorkflowID, revision int, interaction bool) app.WorkflowPlan {
	nodeID := app.WorkflowNodeID("browser_result")
	scope := browserRevision2Scope(interaction)
	transitions := []app.ScopeTransition{
		browserRevision2Transition("health_ready", "scan_tabs", app.OutcomeSignalBrowserHealthy, 1, scope),
		browserRevision2Transition("reuse_existing", "focus_existing", app.OutcomeSignalTargetTabExists, 1, scope),
		browserRevision2Transition("reuse_blank", "navigate_blank", app.OutcomeSignalTargetTabBlank, 1, scope),
		browserRevision2Transition("open_missing", "open_new", app.OutcomeSignalTargetTabMissing, 1, scope),
		// Presentation also uses browser.open, so its assessment-specific signal
		// must be considered before the generic open outcome.
		browserRevision2Transition("visible_opened", "settle_visible", app.OutcomeSignalPresentationOpened, 2, scope),
		browserRevision2Transition("focus_acquired", "settle_hidden", app.OutcomeSignalFocusCompleted, 1, scope),
		browserRevision2Transition("open_acquired", "settle_hidden", app.OutcomeSignalOpenCompleted, 2, scope),
		browserRevision2Transition("navigate_acquired", "settle_hidden", app.OutcomeSignalNavigateCompleted, 1, scope),
		browserRevision2Transition("hidden_settled", "snapshot_hidden", app.OutcomeSignalHiddenTargetSettled, 4, scope),
		browserRevision2Transition("visible_settled", "snapshot_visible", app.OutcomeSignalVisibleTargetSettled, 2, scope),
		browserRevision2Transition("hidden_snapshot_drifted", "settle_hidden", app.OutcomeSignalHiddenSnapshotDrifted, browserSnapshotSettleRetryLimit, scope),
		browserRevision2Transition("visible_snapshot_drifted", "settle_visible", app.OutcomeSignalVisibleSnapshotDrifted, browserSnapshotSettleRetryLimit, scope),
	}
	if !interaction {
		transitions = append(transitions,
			browserRevision2Transition("hidden_validated", "present_visible", app.OutcomeSignalTargetValidated, 1, scope),
		)
	}
	stageCapabilities := []app.StageCapabilityRule{
		{Stage: "health_check", Capabilities: []string{app.ToolCapabilityBrowserHealth}},
		{Stage: "scan_tabs", Capabilities: []string{app.ToolCapabilityBrowserListTabs}},
		{Stage: "focus_existing", Capabilities: []string{app.ToolCapabilityBrowserFocus}},
		{Stage: "navigate_blank", Capabilities: []string{app.ToolCapabilityBrowserNavigate}},
		{Stage: "open_new", Capabilities: []string{app.ToolCapabilityBrowserOpen}},
		{Stage: "settle_hidden", Capabilities: []string{app.ToolCapabilityBrowserWait}},
		{Stage: "snapshot_hidden", Capabilities: []string{app.ToolCapabilityBrowserSnapshot}},
		{Stage: "present_visible", Capabilities: []string{app.ToolCapabilityBrowserOpen}},
		{Stage: "settle_visible", Capabilities: []string{app.ToolCapabilityBrowserWait}},
		{Stage: "snapshot_visible", Capabilities: []string{app.ToolCapabilityBrowserSnapshot}},
	}
	bindings := []app.ArgumentBinding{
		{Capability: app.ToolCapabilityBrowserFocus, Argument: "page_id", ResourceKind: "browser_tab", Source: app.ArgumentBindingOutcomeRef},
		{Capability: app.ToolCapabilityBrowserOpen, Argument: "url", ResourceKind: "url", Source: app.ArgumentBindingIntentTarget, TargetKinds: []app.TargetKind{app.TargetKindExplicitURL}},
		{Capability: app.ToolCapabilityBrowserOpen, Argument: "url", ResourceKind: "browser_result_url", Source: app.ArgumentBindingOutcomeRef},
		{Capability: app.ToolCapabilityBrowserNavigate, Argument: "url", ResourceKind: "url", Source: app.ArgumentBindingIntentTarget, TargetKinds: []app.TargetKind{app.TargetKindExplicitURL}},
		{Capability: app.ToolCapabilityBrowserNavigate, Argument: "page_id", ResourceKind: "browser_tab", Source: app.ArgumentBindingOutcomeRef},
		{Capability: app.ToolCapabilityBrowserWait, Argument: "page_id", ResourceKind: "browser_page", Source: app.ArgumentBindingOutcomeRef},
		{Capability: app.ToolCapabilityBrowserSnapshot, Argument: "page_id", ResourceKind: "browser_page", Source: app.ArgumentBindingOutcomeRef},
	}
	if interaction {
		transitions = append(transitions,
			browserRevision2Transition("assess_initial", "assess_goal_initial", app.OutcomeSignalTargetValidated, 1, scope),
			browserRevision2Transition("initial_needs_action", "choose_and_click", app.OutcomeSignalInteractionProgress, 1, scope),
			browserRevision2Transition("click_recorded", "settle_after_action", app.OutcomeSignalClickCompleted, browserInteractionMaxClicks, scope),
			browserRevision2Transition("action_settled", "snapshot_after_action", app.OutcomeSignalActionTargetSettled, browserInteractionMaxClicks, scope),
			browserRevision2Transition("action_snapshot_drifted", "settle_after_action", app.OutcomeSignalActionSnapshotDrifted, browserSnapshotSettleRetryLimit, scope),
			browserRevision2Transition("validate_transition", "validate_transition", app.OutcomeSignalInteractionVerificationRequired, browserInteractionMaxClicks, scope),
			browserRevision2Transition("assess_after_action", "assess_goal_after_action", app.OutcomeSignalTargetValidated, browserInteractionMaxClicks, scope),
			browserRevision2Transition("continue_interaction", "choose_and_click", app.OutcomeSignalInteractionProgress, browserInteractionMaxClicks-1, scope),
			browserRevision2Transition("goal_satisfied", "present_visible", app.OutcomeSignalInteractionGoalSatisfied, 2, scope),
			browserRevision2Transition("assess_visible", "assess_goal_visible", app.OutcomeSignalPresentationValidated, 1, scope),
		)
		stageCapabilities = append(stageCapabilities,
			app.StageCapabilityRule{Stage: "assess_goal_initial", Capabilities: []string{app.ToolCapabilityBrowserGoalAssess}},
			app.StageCapabilityRule{Stage: "choose_and_click", Capabilities: []string{app.ToolCapabilityBrowserClick}},
			app.StageCapabilityRule{Stage: "settle_after_action", Capabilities: []string{app.ToolCapabilityBrowserWait}},
			app.StageCapabilityRule{Stage: "snapshot_after_action", Capabilities: []string{app.ToolCapabilityBrowserSnapshot}},
			app.StageCapabilityRule{Stage: "validate_transition", Capabilities: []string{app.ToolCapabilityBrowserTransitionValidate}},
			app.StageCapabilityRule{Stage: "assess_goal_after_action", Capabilities: []string{app.ToolCapabilityBrowserGoalAssess}},
			app.StageCapabilityRule{Stage: "assess_goal_visible", Capabilities: []string{app.ToolCapabilityBrowserGoalAssess}},
		)
		bindings = append(bindings,
			app.ArgumentBinding{Capability: app.ToolCapabilityBrowserClick, Argument: "page_id", ResourceKind: "browser_page", Source: app.ArgumentBindingOutcomeRef},
			app.ArgumentBinding{Capability: app.ToolCapabilityBrowserClick, Argument: "snapshot_id", ResourceKind: "browser_snapshot", Source: app.ArgumentBindingOutcomeRef},
			app.ArgumentBinding{Capability: app.ToolCapabilityBrowserClick, Argument: "uid", ResourceKind: "browser_element", Source: app.ArgumentBindingOutcomeRef},
			app.ArgumentBinding{Capability: app.ToolCapabilityBrowserTransitionValidate, Argument: "before_snapshot_id", ResourceKind: "browser_before_snapshot", Source: app.ArgumentBindingOutcomeRef},
			app.ArgumentBinding{Capability: app.ToolCapabilityBrowserTransitionValidate, Argument: "after_snapshot_id", ResourceKind: "browser_after_snapshot", Source: app.ArgumentBindingOutcomeRef},
			app.ArgumentBinding{Capability: app.ToolCapabilityBrowserTransitionValidate, Argument: "element_ref", ResourceKind: "browser_click", Source: app.ArgumentBindingOutcomeRef},
			app.ArgumentBinding{Capability: app.ToolCapabilityBrowserGoalAssess, Argument: "snapshot_id", ResourceKind: "browser_snapshot", Source: app.ArgumentBindingOutcomeRef},
		)
	}
	return app.WorkflowPlan{
		SchemaVersion: 1, ProfileID: id, ProfileRevision: revision,
		InitialNodeIDs: []app.WorkflowNodeID{nodeID}, Completion: app.CompletionEvidence,
		Nodes: []app.WorkflowNode{{
			ID: nodeID, InitialStage: "health_check",
			Goal:         app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "Produce one settled and verified visible browser result", Completion: app.CompletionEvidence},
			InitialScope: scope, Transitions: transitions, ArgumentBindings: bindings,
			StageCapabilities: stageCapabilities,
			AllowedRisks:      []app.RiskLevel{app.RiskRead, app.RiskDraft}, MaxAttempts: 32,
		}},
	}
}

func browserRevision2Scope(interaction bool) app.CapabilityScope {
	requirements := []app.CapabilityRequirement{
		{Name: app.ToolCapabilityBrowserHealth},
		{Name: app.ToolCapabilityBrowserListTabs},
		{Name: app.ToolCapabilityBrowserFocus},
		{Name: app.ToolCapabilityBrowserOpen},
		{Name: app.ToolCapabilityBrowserNavigate},
		{Name: app.ToolCapabilityBrowserWait},
		{Name: app.ToolCapabilityBrowserSnapshot},
	}
	if interaction {
		requirements = append(requirements,
			app.CapabilityRequirement{Name: app.ToolCapabilityBrowserClick},
			app.CapabilityRequirement{Name: app.ToolCapabilityBrowserTransitionValidate},
			app.CapabilityRequirement{Name: app.ToolCapabilityBrowserGoalAssess},
		)
	}
	return app.CapabilityScope{MaterializeAll: true, Requirements: requirements}
}

func browserRevision2Transition(id, stage string, signal app.OutcomeSignal, max int, scope app.CapabilityScope) app.ScopeTransition {
	return app.ScopeTransition{
		ID: app.TransitionID(id), NextStage: stage,
		On: app.TransitionPredicate{
			OutcomeSignals: []app.OutcomeSignal{signal},
			Assessments:    []app.AssessmentStatus{app.AssessmentNeedsMoreEvidence},
		},
		Replace: &scope, MaxActivations: max,
	}
}

func ensureBrowserWorkflowState(state *app.WorkflowState) {
	if state == nil || state.Browser != nil {
		return
	}
	state.Browser = &app.BrowserWorkflowState{
		SchemaVersion: app.BrowserWorkflowStateSchemaVersion,
		Target:        browserTargetDescriptor(state.Route),
	}
}

func browserTargetDescriptor(route app.RouteDecision) app.BrowserTargetDescriptor {
	if route.Slots.TargetKind == string(app.TargetKindBrowserCurrentTab) {
		return app.BrowserTargetDescriptor{TargetKind: app.BrowserTargetCurrentTab}
	}
	target := normalizeBrowserURL(route.Slots.TargetRef)
	parsed, _ := url.Parse(target)
	descriptor := app.BrowserTargetDescriptor{
		TargetKind:      app.BrowserTargetExplicitURL,
		CanonicalURL:    target,
		RoutePath:       parsed.EscapedPath(),
		RouteFragment:   parsed.Fragment,
		QueryProvenance: app.BrowserQueryOwnerSupplied,
		RedactedURL:     target,
	}
	if destination := strings.TrimSpace(route.Facts["browser_destination"]); destination != "" {
		descriptor.TargetKind = app.BrowserTargetRegisteredDestination
		descriptor.DestinationID = destination
		descriptor.QueryProvenance = app.BrowserQueryDestinationStatic
	}
	return descriptor
}

func browserRevision2DirectStage(state *app.WorkflowState, interaction bool) bool {
	stage := browserActiveStage(state)
	switch stage {
	case "health_check", "scan_tabs", "focus_existing", "navigate_blank", "open_new",
		"settle_hidden", "snapshot_hidden", "present_visible", "settle_visible", "snapshot_visible":
		return true
	case "settle_after_action", "snapshot_after_action", "validate_transition":
		return interaction
	default:
		return false
	}
}

func browserRevision2DirectArguments(state *app.WorkflowState) map[string]any {
	stage := browserActiveStage(state)
	args := map[string]any{}
	switch stage {
	case "health_check":
		args["require_visible_environment"] = true
	case "settle_hidden", "settle_visible", "settle_after_action":
		args["mode"] = "stable_state"
		args["require_url_stable"] = true
		args["require_ready_state"] = true
		args["allow_no_change"] = stage != "settle_after_action"
		if state != nil && state.Browser != nil {
			expectedURL := state.Browser.Target.CanonicalURL
			if stage == "settle_visible" && state.Browser.Result != nil &&
				state.Browser.Result.Target.CanonicalURL != "" {
				expectedURL = state.Browser.Result.Target.CanonicalURL
			}
			if expectedURL != "" {
				args["expected_url"] = expectedURL
			}
			if state.Browser.Target.TargetKind != "" {
				args["target_kind"] = string(state.Browser.Target.TargetKind)
			}
		}
		if stage == "settle_after_action" && state != nil && len(state.ActiveNodeIDs) == 1 {
			node := state.Nodes[state.ActiveNodeIDs[0]]
			for _, ref := range currentBrowserSnapshotRefs(node.OutcomeRefs) {
				if ref.Kind == "browser_snapshot" {
					args["before_digest"] = ref.Attributes["digest"]
					args["session_generation"] = browserRefGeneration(ref)
				}
			}
		}
	case "snapshot_hidden", "snapshot_after_action", "snapshot_visible":
		if state != nil && state.Route.Slots.Query != "" {
			args["interaction_goal"] = state.Route.Slots.Query
		}
	case "present_visible":
		args["reason"] = browserResultPresentationReason
	case "validate_transition":
		args["schema_version"] = 2
	}
	return args
}

func browserRevision2StageContext(state *app.WorkflowState, interaction bool) workflowStageContext {
	stage := browserActiveStage(state)
	mode := "autonomous"
	if stage == "present_visible" || stage == "settle_visible" || stage == "snapshot_visible" || stage == "assess_goal_visible" {
		mode = "collaborative"
	}
	reason := "workflow_stage: " + stage + ". Structural browser stages are Runtime-owned and every page action is followed by stable settle and a fresh snapshot."
	if interaction && strings.HasPrefix(stage, "assess_goal") {
		reason += " Assess the frozen owner goal independently and cite only refs from the bound current snapshot."
	}
	stageContext := workflowStageContextForState(state, "browse", "web", "public", mode, reason)
	if interaction {
		stageContext.EstimatedRisk = app.RiskDraft
	}
	return stageContext
}

func browserActiveStage(state *app.WorkflowState) string {
	if state == nil || len(state.ActiveNodeIDs) != 1 {
		return ""
	}
	return state.Nodes[state.ActiveNodeIDs[0]].Stage
}

func assessBrowserRevision2(state *app.WorkflowState, outcome app.ToolOutcome, interaction bool) app.NodeAssessment {
	assessment := baseNodeAssessment(outcome)
	if state == nil || state.Browser == nil {
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "browser_state_missing"
		return assessment
	}
	node := state.Nodes[outcome.NodeID]
	switch node.Stage {
	case "health_check":
		if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalBrowserHealthy) {
			return browserRevision2NeedsMore(assessment, app.OutcomeSignalBrowserHealthy, "scan_tabs", nil)
		}
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "browser_environment_unavailable"
	case "scan_tabs":
		selected, signal, reason := selectBrowserInteractionTab(state.Route, outcome.Refs)
		if reason != "" {
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, reason
			return assessment
		}
		var refs []app.ResourceRef
		if selected != nil {
			refs = []app.ResourceRef{*selected}
		}
		return browserRevision2NeedsMore(assessment, signal, browserInteractionStageForTabSignal(signal), refs)
	case "focus_existing":
		return browserRevision2Acquired(assessment, outcome, app.OutcomeSignalFocusCompleted, "browser_focus_failed")
	case "open_new":
		return browserRevision2Acquired(assessment, outcome, app.OutcomeSignalOpenCompleted, "browser_open_failed")
	case "navigate_blank":
		return browserRevision2Acquired(assessment, outcome, app.OutcomeSignalNavigateCompleted, "browser_navigate_failed")
	case "settle_hidden", "settle_after_action", "settle_visible":
		if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalTargetSettled) {
			next := "snapshot_hidden"
			signal := app.OutcomeSignalHiddenTargetSettled
			if node.Stage == "settle_after_action" {
				next = "snapshot_after_action"
				signal = app.OutcomeSignalActionTargetSettled
			} else if node.Stage == "settle_visible" {
				next = "snapshot_visible"
				signal = app.OutcomeSignalVisibleTargetSettled
			}
			return browserRevision2NeedsMore(assessment, signal, next, outcome.Refs)
		}
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "browser_settle_timeout"
	case "snapshot_hidden":
		refs, reason := browserRevision2ValidatedSnapshot(state, outcome, app.BrowserPresentationHidden)
		if reason == "" && interaction && !browserSnapshotHasInteractionEvidence(refs) {
			reason = "browser_snapshot_not_ready"
		}
		if reason == "browser_snapshot_route_changed" || reason == "browser_snapshot_not_ready" {
			return browserRevision2SnapshotRetry(state, assessment, "hidden_snapshot_drifted", app.OutcomeSignalHiddenSnapshotDrifted, "settle_hidden")
		}
		if reason != "" {
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, reason
			return assessment
		}
		if interaction {
			freezeBrowserGoalContract(state, refs)
			return browserRevision2NeedsMore(assessment, app.OutcomeSignalTargetValidated, "assess_goal_initial", refs)
		}
		recordBrowserHiddenResult(state, refs, outcome.ToolCallID, nil)
		return browserRevision2NeedsMore(assessment, app.OutcomeSignalTargetValidated, "present_visible", browserPresentationRefs(state, refs))
	case "choose_and_click":
		switch {
		case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalUnsafeClickTarget):
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "unsafe_click_target"
		case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalSnapshotStale):
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "snapshot_stale"
		case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalClickCompleted):
			state.Browser.CompletedClicks++
			return browserRevision2NeedsMore(assessment, app.OutcomeSignalClickCompleted, "settle_after_action", outcome.Refs)
		default:
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "browser_click_failed"
		}
	case "snapshot_after_action":
		refs, reason := browserRevision2ValidatedSnapshot(state, outcome, app.BrowserPresentationHidden)
		if reason == "" && browserSnapshotOutcomeRepeated(refs) {
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "interaction_loop_detected"
			return assessment
		}
		if reason == "" && !browserSnapshotHasInteractionEvidence(refs) {
			reason = "browser_snapshot_not_ready"
		}
		if reason == "browser_snapshot_route_changed" || reason == "browser_snapshot_not_ready" {
			return browserRevision2SnapshotRetry(state, assessment, "action_snapshot_drifted", app.OutcomeSignalActionSnapshotDrifted, "settle_after_action")
		}
		if reason != "" {
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, reason
			return assessment
		}
		refs = browserTransitionRefs(node.OutcomeRefs, refs)
		return browserRevision2NeedsMore(assessment, app.OutcomeSignalInteractionVerificationRequired, "validate_transition", refs)
	case "validate_transition":
		if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalTargetValidated) {
			return browserRevision2NeedsMore(assessment, app.OutcomeSignalTargetValidated, "assess_goal_after_action", append(node.OutcomeRefs, outcome.Refs...))
		}
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "interaction_transition_invalid"
	case "assess_goal_initial", "assess_goal_after_action":
		switch {
		case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalInteractionGoalSatisfied):
			refs := currentBrowserSnapshotRefs(node.OutcomeRefs)
			recordBrowserHiddenResult(state, refs, outcome.ToolCallID, browserGoalEvidenceRefs(outcome.Refs))
			return browserRevision2NeedsMore(assessment, app.OutcomeSignalInteractionGoalSatisfied, "present_visible", browserPresentationRefs(state, refs))
		case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalInteractionProgress):
			if state.Browser.CompletedClicks >= browserInteractionMaxClicks {
				assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "interaction_attempt_limit"
				return assessment
			}
			refs := currentBrowserSnapshotRefs(node.OutcomeRefs)
			return browserRevision2NeedsMore(assessment, app.OutcomeSignalInteractionProgress, "choose_and_click", refs)
		default:
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "interaction_goal_not_satisfied"
		}
	case "present_visible":
		if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalOpenCompleted) {
			return browserRevision2NeedsMore(assessment, app.OutcomeSignalPresentationOpened, "settle_visible", outcome.Refs)
		}
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "browser_presentation_failed"
	case "snapshot_visible":
		refs, reason := browserRevision2ValidatedSnapshot(state, outcome, app.BrowserPresentationVisible)
		if reason == "" && interaction && !browserSnapshotHasInteractionEvidence(refs) {
			reason = "browser_snapshot_not_ready"
		}
		if reason == "browser_snapshot_route_changed" || reason == "browser_snapshot_not_ready" {
			return browserRevision2SnapshotRetry(state, assessment, "visible_snapshot_drifted", app.OutcomeSignalVisibleSnapshotDrifted, "settle_visible")
		}
		if reason != "" {
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, reason
			return assessment
		}
		recordBrowserVisibleResult(state, refs, outcome.ToolCallID)
		if interaction {
			return browserRevision2NeedsMore(assessment, app.OutcomeSignalPresentationValidated, "assess_goal_visible", refs)
		}
		assessment.Status, assessment.ReasonCode = app.AssessmentComplete, "browser_visible_result_verified"
		assessment.SelectedRefs = refs
	case "assess_goal_visible":
		if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalInteractionGoalSatisfied) {
			assessment.Status, assessment.ReasonCode = app.AssessmentComplete, "browser_visible_goal_verified"
			assessment.SelectedRefs = append(currentBrowserSnapshotRefs(node.OutcomeRefs), outcome.Refs...)
			if state.Browser.Result != nil {
				state.Browser.Result.GoalEvidenceRefs = browserGoalEvidenceRefs(outcome.Refs)
				state.Browser.Result.GoalAssessmentCallID = outcome.ToolCallID
				state.Browser.Result.VerifiedAt = time.Now().UTC()
			}
		} else {
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "browser_visible_goal_not_satisfied"
		}
	default:
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "browser_revision2_stage_invalid"
	}
	return assessment
}

func browserRevision2Acquired(assessment app.NodeAssessment, outcome app.ToolOutcome, signal app.OutcomeSignal, failure string) app.NodeAssessment {
	if containsOutcomeSignal(outcome.Signals, signal) {
		return browserRevision2NeedsMore(assessment, signal, "settle_hidden", outcome.Refs)
	}
	assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, failure
	return assessment
}

func browserRevision2NeedsMore(assessment app.NodeAssessment, signal app.OutcomeSignal, reason string, refs []app.ResourceRef) app.NodeAssessment {
	assessment.Status = app.AssessmentNeedsMoreEvidence
	assessment.Signals = []app.OutcomeSignal{signal}
	assessment.ReasonCode = reason
	if refs != nil {
		assessment.SelectedRefs = refs
	}
	return assessment
}

func browserRevision2SnapshotRetry(state *app.WorkflowState, assessment app.NodeAssessment, transitionID string, signal app.OutcomeSignal, stage string) app.NodeAssessment {
	node := state.Nodes[assessment.NodeID]
	if node.TransitionActivations[app.TransitionID(transitionID)] >= browserSnapshotSettleRetryLimit {
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "browser_snapshot_not_ready"
		return assessment
	}
	return browserRevision2NeedsMore(assessment, signal, stage, []app.ResourceRef{})
}

func browserRevision2TransitionInstruction(assessment app.NodeAssessment) string {
	if assessment.ReasonCode == "" {
		return ""
	}
	return "workflow_stage: " + assessment.ReasonCode
}

func browserRevision2ValidatedSnapshot(state *app.WorkflowState, outcome app.ToolOutcome, presentation app.BrowserPresentation) ([]app.ResourceRef, string) {
	if !containsOutcomeSignal(outcome.Signals, app.OutcomeSignalSnapshotAvailable) {
		return nil, "browser_snapshot_unavailable"
	}
	page, snapshot, ok := browserSnapshotRefs(outcome.Refs)
	if !ok {
		return nil, "browser_snapshot_identity_missing"
	}
	liveURL := page.Attributes["url"]
	if !browserPresentationURLMatchesRoute(state, liveURL) || !browserLoginResumeURLUsable(liveURL) {
		return nil, "browser_route_diverged"
	}
	if browserSnapshotRouteChangedAfterSettle(state, outcome.NodeID, liveURL) {
		return nil, "browser_snapshot_route_changed"
	}
	if strings.TrimSpace(snapshot.Attributes["digest"]) == "" {
		return nil, "browser_snapshot_empty"
	}
	generation := browserRefGeneration(snapshot)
	if generation == 0 {
		return nil, "browser_session_generation_missing"
	}
	expectedPresentation := string(presentation)
	if got := snapshot.Attributes["presentation"]; got != "" && got != expectedPresentation {
		return nil, "browser_snapshot_presentation_mismatch"
	}
	validated := append([]app.ResourceRef(nil), outcome.Refs...)
	for index := range validated {
		validated[index].Attributes = cloneStringMap(validated[index].Attributes)
		if validated[index].Kind == "browser_page" {
			validated[index].Attributes["url"] = browserSafeResultURL(state.Browser.Target, liveURL)
		}
	}
	return validated, ""
}

func browserSnapshotRouteChangedAfterSettle(state *app.WorkflowState, nodeID app.WorkflowNodeID, liveURL string) bool {
	if state == nil {
		return false
	}
	node, ok := state.Nodes[nodeID]
	if !ok {
		return false
	}
	for index := len(node.OutcomeRefs) - 1; index >= 0; index-- {
		ref := node.OutcomeRefs[index]
		if ref.Kind != "browser_page" ||
			strings.TrimSpace(ref.Attributes["state_digest"]) == "" ||
			strings.TrimSpace(ref.Attributes["url"]) == "" {
			continue
		}
		return !sameBrowserDocumentRoute(ref.Attributes["url"], liveURL)
	}
	return false
}

func sameBrowserDocumentRoute(leftRaw, rightRaw string) bool {
	left, leftErr := url.Parse(strings.TrimSpace(leftRaw))
	right, rightErr := url.Parse(strings.TrimSpace(rightRaw))
	if leftErr != nil || rightErr != nil || !sameBrowserOrigin(leftRaw, rightRaw) {
		return false
	}
	leftPath := left.EscapedPath()
	if leftPath == "" {
		leftPath = "/"
	}
	rightPath := right.EscapedPath()
	if rightPath == "" {
		rightPath = "/"
	}
	return leftPath == rightPath && left.EscapedFragment() == right.EscapedFragment()
}

func browserSnapshotHasInteractionEvidence(refs []app.ResourceRef) bool {
	for _, ref := range refs {
		if ref.Kind == "browser_element" && strings.TrimSpace(ref.Ref) != "" {
			return true
		}
	}
	return false
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func browserSnapshotRefs(refs []app.ResourceRef) (app.ResourceRef, app.ResourceRef, bool) {
	var page, snapshot app.ResourceRef
	for _, ref := range refs {
		switch ref.Kind {
		case "browser_page":
			page = ref
		case "browser_snapshot":
			snapshot = ref
		}
	}
	return page, snapshot, page.Ref != "" && snapshot.Ref != ""
}

func browserRefGeneration(ref app.ResourceRef) uint64 {
	value, _ := strconv.ParseUint(strings.TrimSpace(ref.Attributes["session_generation"]), 10, 64)
	return value
}

func freezeBrowserGoalContract(state *app.WorkflowState, refs []app.ResourceRef) {
	if state.Browser.Goal != nil {
		return
	}
	_, snapshot, _ := browserSnapshotRefs(refs)
	state.Browser.Goal = &app.BrowserGoalContract{
		GoalID: "browser_goal_" + state.Intent.SourceTurnID, SchemaVersion: app.BrowserGoalContractSchemaVersion,
		OwnerGoal:        state.Route.Slots.Query,
		RequiredCriteria: []string{strings.TrimSpace(state.Route.Slots.Query)},
		ForbiddenEffects: []string{"typing", "form_submission", "credential_entry", "payment", "upload", "deletion", "external_send"},
		Target:           state.Browser.Target, MaxClicks: browserInteractionMaxClicks,
		CreatedFromSnapshotID: snapshot.Ref, CreatedAt: time.Now().UTC(),
	}
}

func recordBrowserHiddenResult(state *app.WorkflowState, refs []app.ResourceRef, callID string, citations []string) {
	page, snapshot, ok := browserSnapshotRefs(refs)
	if !ok {
		return
	}
	safeURL := browserSafeResultURL(state.Browser.Target, page.Attributes["url"])
	state.Browser.Result = &app.BrowserResultEvidence{
		ID: "browser_result_" + callID, SchemaVersion: 2, Target: state.Browser.Target,
		HiddenSession: app.BrowserSessionRef{
			OwnerID: page.Attributes["owner_id"], ProfileID: page.Attributes["profile_id"],
			Presentation: app.BrowserPresentationHidden, Generation: browserRefGeneration(snapshot),
			ProviderSessionRef: snapshot.Attributes["provider_session_ref"],
		},
		HiddenPageID: page.Ref, HiddenSnapshotID: snapshot.Ref, HiddenSnapshotDigest: snapshot.Attributes["digest"],
		GoalAssessmentCallID: callID, GoalEvidenceRefs: citations,
		SourceToolCallIDs: []string{snapshot.Provenance, callID},
	}
	state.Browser.Result.Target.CanonicalURL = safeURL
	state.Browser.Result.Target.RedactedURL = safeURL
}

func recordBrowserVisibleResult(state *app.WorkflowState, refs []app.ResourceRef, callID string) {
	if state.Browser.Result == nil {
		return
	}
	page, snapshot, ok := browserSnapshotRefs(refs)
	if !ok {
		return
	}
	state.Browser.Result.VisibleSession = app.BrowserSessionRef{
		OwnerID: page.Attributes["owner_id"], ProfileID: page.Attributes["profile_id"],
		Presentation: app.BrowserPresentationVisible, Generation: browserRefGeneration(snapshot),
		ProviderSessionRef: snapshot.Attributes["provider_session_ref"],
	}
	state.Browser.Result.VisiblePageID = page.Ref
	state.Browser.Result.VisibleSnapshotID = snapshot.Ref
	state.Browser.Result.VisibleSnapshotDigest = snapshot.Attributes["digest"]
	state.Browser.Result.SourceToolCallIDs = appendUniqueString(state.Browser.Result.SourceToolCallIDs, callID)
	state.Browser.Result.VerifiedAt = time.Now().UTC()
}

func browserPresentationRefs(state *app.WorkflowState, refs []app.ResourceRef) []app.ResourceRef {
	if state == nil || state.Browser == nil || state.Browser.Result == nil {
		return refs
	}
	targetURL := state.Browser.Result.Target.CanonicalURL
	return []app.ResourceRef{{
		Kind: "browser_result_url", Ref: targetURL,
		Provenance: state.Browser.Result.HiddenSnapshotID,
		Attributes: map[string]string{"redacted_url": state.Browser.Result.Target.RedactedURL},
	}}
}

func browserSafeResultURL(target app.BrowserTargetDescriptor, liveRaw string) string {
	live, err := url.Parse(strings.TrimSpace(liveRaw))
	if err != nil || live.Scheme == "" || live.Host == "" {
		return target.CanonicalURL
	}
	live.Scheme = strings.ToLower(live.Scheme)
	live.Host = strings.ToLower(live.Host)
	if live.Path == "" {
		live.Path = "/"
	}
	live.RawQuery = ""
	live.ForceQuery = false
	if target.QueryProvenance == app.BrowserQueryOwnerSupplied {
		if frozen, frozenErr := url.Parse(strings.TrimSpace(target.CanonicalURL)); frozenErr == nil &&
			frozen.Scheme != "" && frozen.Host != "" {
			live.RawQuery = frozen.RawQuery
			live.ForceQuery = frozen.ForceQuery
		}
	}
	return live.String()
}

func browserPresentationURLMatchesRoute(state *app.WorkflowState, candidateURL string) bool {
	if state == nil || !browserLoginResumeURLUsable(candidateURL) {
		return false
	}
	switch state.Route.Slots.TargetKind {
	case "url", string(app.TargetKindExplicitURL):
		targetURL := state.Route.Slots.TargetRef
		if browserTargetMatchesURL(targetURL, state.Route.Facts["browser_destination"], candidateURL) {
			return true
		}
		return state.Plan.ProfileID == app.WorkflowBrowserInteraction && sameBrowserOrigin(targetURL, candidateURL)
	case string(app.TargetKindBrowserCurrentTab):
		return state.Plan.ProfileID == app.WorkflowBrowserInteraction
	default:
		return false
	}
}

func sameBrowserOrigin(left, right string) bool {
	leftURL, leftErr := url.Parse(strings.TrimSpace(left))
	rightURL, rightErr := url.Parse(strings.TrimSpace(right))
	if leftErr != nil || rightErr != nil || leftURL.Hostname() == "" || rightURL.Hostname() == "" {
		return false
	}
	return strings.EqualFold(leftURL.Scheme, rightURL.Scheme) &&
		strings.EqualFold(leftURL.Hostname(), rightURL.Hostname()) &&
		leftURL.Port() == rightURL.Port()
}

func browserTransitionRefs(existing, after []app.ResourceRef) []app.ResourceRef {
	refs := make([]app.ResourceRef, 0, len(existing)+len(after))
	for _, ref := range existing {
		switch ref.Kind {
		case "browser_snapshot":
			ref.Kind = "browser_before_snapshot"
			refs = append(refs, ref)
		case "browser_click":
			refs = append(refs, ref)
		}
	}
	for _, ref := range after {
		switch ref.Kind {
		case "browser_snapshot":
			beforeCopy := ref
			beforeCopy.Kind = "browser_after_snapshot"
			refs = append(refs, beforeCopy, ref)
		default:
			refs = append(refs, ref)
		}
	}
	return refs
}

func currentBrowserSnapshotRefs(refs []app.ResourceRef) []app.ResourceRef {
	currentSnapshot := ""
	for index := len(refs) - 1; index >= 0; index-- {
		if refs[index].Kind == "browser_snapshot" {
			currentSnapshot = refs[index].Ref
			break
		}
	}
	out := []app.ResourceRef{}
	for _, ref := range refs {
		switch ref.Kind {
		case "browser_page", "browser_snapshot":
			if ref.Kind == "browser_snapshot" && ref.Ref != currentSnapshot {
				continue
			}
			out = append(out, ref)
		case "browser_element":
			if ref.Attributes["snapshot_id"] == currentSnapshot {
				out = append(out, ref)
			}
		}
	}
	return out
}

func browserGoalEvidenceRefs(refs []app.ResourceRef) []string {
	out := []string{}
	for _, ref := range refs {
		if ref.Kind == "browser_goal_assessment" {
			for _, value := range strings.Split(ref.Attributes["evidence_refs"], ",") {
				if value = strings.TrimSpace(value); value != "" {
					out = appendUniqueString(out, value)
				}
			}
		}
	}
	return out
}
