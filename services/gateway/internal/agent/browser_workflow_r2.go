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

// Browser revision-2 stage names. The frozen plan table, the direct-stage
// dispatch, the direct-argument and assessment switches, the collaborative
// stage-context list, and the login-handoff reset all reference these
// constants; never restate a stage as a string literal.
const (
	browserStageHealthCheck           = "health_check"
	browserStageDiscoverTarget        = "discover_target"
	browserStageIdentifyTarget        = "identify_target"
	browserStageScanTabs              = "scan_tabs"
	browserStageFocusExisting         = "focus_existing"
	browserStageNavigateBlank         = "navigate_blank"
	browserStageOpenNew               = "open_new"
	browserStageSettleHidden          = "settle_hidden"
	browserStageSnapshotHidden        = "snapshot_hidden"
	browserStagePresentVisible        = "present_visible"
	browserStageSettleVisible         = "settle_visible"
	browserStageSnapshotVisible       = "snapshot_visible"
	browserStageAssessGoalInitial     = "assess_goal_initial"
	browserStageChooseAndClick        = "choose_and_click"
	browserStageSettleAfterAction     = "settle_after_action"
	browserStageSnapshotAfterAction   = "snapshot_after_action"
	browserStageValidateTransition    = "validate_transition"
	browserStageAssessGoalAfterAction = "assess_goal_after_action"
	browserStageVisualInspect         = "visual_inspect_hidden"
	browserStageAssessGoalVisible     = "assess_goal_visible"
	// browserStageAssessGoalPrefix matches every assess_goal_* stage.
	browserStageAssessGoalPrefix = "assess_goal"
)

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
		browserRouteIntentTarget(route), app.DataScopePublic)
	plan := browserRevision2Plan(p.ID(), p.Revision(), false)
	if browserVisualReason(route) != "" {
		browserRevision2EnableVisualInspection(&plan)
	}
	if route.Slots.TargetKind == string(app.TargetKindPublicNamedTarget) {
		browserRevision2EnableTargetDiscovery(&plan)
	}
	return intent, plan, nil
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
	} else if route.Slots.TargetKind == string(app.TargetKindPublicNamedTarget) {
		target = app.TargetRef{Kind: app.TargetKindPublicNamedTarget, Ref: route.Slots.TargetRef}
	}
	intent := singleObjectiveIntent(sourceTurnID, app.IntentDomainWeb, app.IntentOperationAutomate, target, app.DataScopePublic)
	plan := browserRevision2Plan(p.ID(), p.Revision(), true)
	if browserVisualReason(route) != "" {
		browserRevision2EnableVisualInspection(&plan)
	}
	if route.Slots.TargetKind == string(app.TargetKindPublicNamedTarget) {
		browserRevision2EnableTargetDiscovery(&plan)
	}
	return intent, plan, nil
}

func browserRouteIntentTarget(route app.RouteDecision) app.TargetRef {
	switch route.Slots.TargetKind {
	case string(app.TargetKindPublicNamedTarget):
		return app.TargetRef{Kind: app.TargetKindPublicNamedTarget, Ref: route.Slots.TargetRef}
	case string(app.TargetKindBrowserCurrentTab):
		return app.TargetRef{Kind: app.TargetKindBrowserCurrentTab, Ref: route.Slots.TargetRef}
	default:
		return app.TargetRef{Kind: app.TargetKindExplicitURL, Ref: route.Slots.TargetRef}
	}
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
		browserRevision2Transition("health_ready", browserStageScanTabs, app.OutcomeSignalBrowserHealthy, 1, scope),
		browserRevision2Transition("reuse_existing", browserStageFocusExisting, app.OutcomeSignalTargetTabExists, 1, scope),
		browserRevision2Transition("reuse_blank", browserStageNavigateBlank, app.OutcomeSignalTargetTabBlank, 1, scope),
		browserRevision2Transition("open_missing", browserStageOpenNew, app.OutcomeSignalTargetTabMissing, 1, scope),
		// Presentation also uses browser.open, so its assessment-specific signal
		// must be considered before the generic open outcome.
		browserRevision2Transition("visible_opened", browserStageSettleVisible, app.OutcomeSignalPresentationOpened, 2, scope),
		browserRevision2Transition("focus_acquired", browserStageSettleHidden, app.OutcomeSignalFocusCompleted, 1, scope),
		browserRevision2Transition("open_acquired", browserStageSettleHidden, app.OutcomeSignalOpenCompleted, 2, scope),
		browserRevision2Transition("navigate_acquired", browserStageSettleHidden, app.OutcomeSignalNavigateCompleted, 1, scope),
		browserRevision2Transition("hidden_settled", browserStageSnapshotHidden, app.OutcomeSignalHiddenTargetSettled, 4, scope),
		browserRevision2Transition("visible_settled", browserStageSnapshotVisible, app.OutcomeSignalVisibleTargetSettled, 2, scope),
		browserRevision2Transition("hidden_snapshot_drifted", browserStageSettleHidden, app.OutcomeSignalHiddenSnapshotDrifted, browserSnapshotSettleRetryLimit, scope),
		browserRevision2Transition("visible_snapshot_drifted", browserStageSettleVisible, app.OutcomeSignalVisibleSnapshotDrifted, browserSnapshotSettleRetryLimit, scope),
	}
	if !interaction {
		transitions = append(transitions,
			browserRevision2Transition("hidden_validated", browserStagePresentVisible, app.OutcomeSignalTargetValidated, 1, scope),
		)
	}
	stageCapabilities := []app.StageCapabilityRule{
		{Stage: browserStageHealthCheck, Capabilities: []string{app.ToolCapabilityBrowserHealth}},
		{Stage: browserStageScanTabs, Capabilities: []string{app.ToolCapabilityBrowserListTabs}},
		{Stage: browserStageFocusExisting, Capabilities: []string{app.ToolCapabilityBrowserFocus}},
		{Stage: browserStageNavigateBlank, Capabilities: []string{app.ToolCapabilityBrowserNavigate}},
		{Stage: browserStageOpenNew, Capabilities: []string{app.ToolCapabilityBrowserOpen}},
		{Stage: browserStageSettleHidden, Capabilities: []string{app.ToolCapabilityBrowserWait}},
		{Stage: browserStageSnapshotHidden, Capabilities: []string{app.ToolCapabilityBrowserSnapshot}},
		{Stage: browserStagePresentVisible, Capabilities: []string{app.ToolCapabilityBrowserOpen}},
		{Stage: browserStageSettleVisible, Capabilities: []string{app.ToolCapabilityBrowserWait}},
		{Stage: browserStageSnapshotVisible, Capabilities: []string{app.ToolCapabilityBrowserSnapshot}},
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
			browserRevision2Transition("assess_initial", browserStageAssessGoalInitial, app.OutcomeSignalTargetValidated, 1, scope),
			browserRevision2Transition("initial_needs_action", browserStageChooseAndClick, app.OutcomeSignalInteractionProgress, 1, scope),
			browserRevision2Transition("click_recorded", browserStageSettleAfterAction, app.OutcomeSignalClickCompleted, browserInteractionMaxClicks, scope),
			browserRevision2Transition("action_settled", browserStageSnapshotAfterAction, app.OutcomeSignalActionTargetSettled, browserInteractionMaxClicks, scope),
			browserRevision2Transition("action_snapshot_drifted", browserStageSettleAfterAction, app.OutcomeSignalActionSnapshotDrifted, browserSnapshotSettleRetryLimit, scope),
			browserRevision2Transition("validate_transition", browserStageValidateTransition, app.OutcomeSignalInteractionVerificationRequired, browserInteractionMaxClicks, scope),
			browserRevision2Transition("assess_after_action", browserStageAssessGoalAfterAction, app.OutcomeSignalTargetValidated, browserInteractionMaxClicks, scope),
			browserRevision2Transition("continue_interaction", browserStageChooseAndClick, app.OutcomeSignalInteractionProgress, browserInteractionMaxClicks-1, scope),
			browserRevision2Transition("goal_satisfied", browserStagePresentVisible, app.OutcomeSignalInteractionGoalSatisfied, 2, scope),
			browserRevision2Transition("assess_visible", browserStageAssessGoalVisible, app.OutcomeSignalPresentationValidated, 1, scope),
		)
		stageCapabilities = append(stageCapabilities,
			app.StageCapabilityRule{Stage: browserStageAssessGoalInitial, Capabilities: []string{app.ToolCapabilityBrowserGoalAssess}},
			app.StageCapabilityRule{Stage: browserStageChooseAndClick, Capabilities: []string{app.ToolCapabilityBrowserClick}},
			app.StageCapabilityRule{Stage: browserStageSettleAfterAction, Capabilities: []string{app.ToolCapabilityBrowserWait}},
			app.StageCapabilityRule{Stage: browserStageSnapshotAfterAction, Capabilities: []string{app.ToolCapabilityBrowserSnapshot}},
			app.StageCapabilityRule{Stage: browserStageValidateTransition, Capabilities: []string{app.ToolCapabilityBrowserTransitionValidate}},
			app.StageCapabilityRule{Stage: browserStageAssessGoalAfterAction, Capabilities: []string{app.ToolCapabilityBrowserGoalAssess}},
			app.StageCapabilityRule{Stage: browserStageAssessGoalVisible, Capabilities: []string{app.ToolCapabilityBrowserGoalAssess}},
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
			ID: nodeID, InitialStage: browserStageHealthCheck,
			Goal:         app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "Produce one settled and verified visible browser result", Completion: app.CompletionEvidence},
			InitialScope: scope, Transitions: transitions, ArgumentBindings: bindings,
			StageCapabilities: stageCapabilities,
			AllowedRisks:      []app.RiskLevel{app.RiskRead, app.RiskDraft}, MaxAttempts: 32,
		}},
	}
}

func browserRevision2EnableTargetDiscovery(plan *app.WorkflowPlan) {
	if plan == nil || len(plan.Nodes) != 1 {
		return
	}
	node := &plan.Nodes[0]
	baseScope := node.InitialScope
	discoveryScope := baseScope
	discoveryScope.Requirements = append(append([]app.CapabilityRequirement(nil), baseScope.Requirements...),
		app.CapabilityRequirement{Name: app.ToolCapabilityWebDiscovery},
		app.CapabilityRequirement{Name: app.ToolCapabilityBrowserPublicTarget},
	)
	node.InitialStage = browserStageDiscoverTarget
	node.InitialScope = discoveryScope
	node.Transitions = append([]app.ScopeTransition{
		browserRevision2Transition("target_search_ready", browserStageIdentifyTarget, app.OutcomeSignalResultsAvailable, 1, discoveryScope),
		browserRevision2Transition("target_identified", browserStageHealthCheck, app.OutcomeSignalPublicTargetResolved, 1, baseScope),
	}, node.Transitions...)
	node.StageCapabilities = append([]app.StageCapabilityRule{
		{Stage: browserStageDiscoverTarget, Capabilities: []string{app.ToolCapabilityWebDiscovery}},
		{Stage: browserStageIdentifyTarget, Capabilities: []string{app.ToolCapabilityBrowserPublicTarget}},
	}, node.StageCapabilities...)
	node.ArgumentBindings = append(node.ArgumentBindings,
		app.ArgumentBinding{Capability: app.ToolCapabilityWebDiscovery, Argument: "query", ResourceKind: "query", Source: app.ArgumentBindingRouteSlot, SourceKey: "target_ref"},
		app.ArgumentBinding{Capability: app.ToolCapabilityBrowserOpen, Argument: "url", ResourceKind: "public_target_url", Source: app.ArgumentBindingOutcomeRef},
		app.ArgumentBinding{Capability: app.ToolCapabilityBrowserNavigate, Argument: "url", ResourceKind: "public_target_url", Source: app.ArgumentBindingOutcomeRef},
	)
}

func browserRevision2EnableVisualInspection(plan *app.WorkflowPlan) {
	if plan == nil || len(plan.Nodes) != 1 {
		return
	}
	node := &plan.Nodes[0]
	addRequirement := func(scope *app.CapabilityScope) {
		if scope == nil {
			return
		}
		for _, requirement := range scope.Requirements {
			if requirement.Name == app.ToolCapabilityBrowserVisualInspect {
				return
			}
		}
		scope.Requirements = append(scope.Requirements, app.CapabilityRequirement{Name: app.ToolCapabilityBrowserVisualInspect})
	}
	addRequirement(&node.InitialScope)
	for index := range node.Transitions {
		transition := &node.Transitions[index]
		if transition.Replace != nil {
			addRequirement(transition.Replace)
		}
		if transition.NextStage == browserStagePresentVisible {
			transition.NextStage = browserStageVisualInspect
		}
	}
	visualScope := node.InitialScope
	node.Transitions = append(node.Transitions,
		browserRevision2Transition("visual_inspected", browserStagePresentVisible, app.OutcomeSignalVisualEvidenceAvailable, 1, visualScope),
	)
	node.StageCapabilities = append(node.StageCapabilities,
		app.StageCapabilityRule{Stage: browserStageVisualInspect, Capabilities: []string{app.ToolCapabilityBrowserVisualInspect}},
	)
	node.ArgumentBindings = append(node.ArgumentBindings,
		app.ArgumentBinding{Capability: app.ToolCapabilityBrowserVisualInspect, Argument: "page_id", ResourceKind: "browser_page", Source: app.ArgumentBindingOutcomeRef},
		app.ArgumentBinding{Capability: app.ToolCapabilityBrowserVisualInspect, Argument: "snapshot_id", ResourceKind: "browser_snapshot", Source: app.ArgumentBindingOutcomeRef},
		app.ArgumentBinding{Capability: app.ToolCapabilityBrowserVisualInspect, Argument: "session_generation", ResourceKind: "browser_snapshot", Source: app.ArgumentBindingOutcomeRef, SourceKey: "session_generation"},
		app.ArgumentBinding{Capability: app.ToolCapabilityBrowserVisualInspect, Argument: "page_generation", ResourceKind: "browser_snapshot", Source: app.ArgumentBindingOutcomeRef, SourceKey: "page_generation"},
		app.ArgumentBinding{Capability: app.ToolCapabilityBrowserVisualInspect, Argument: "snapshot_digest", ResourceKind: "browser_snapshot", Source: app.ArgumentBindingOutcomeRef, SourceKey: "digest"},
	)
}

func browserVisualReason(route app.RouteDecision) string {
	if route.Facts == nil {
		return ""
	}
	switch reason := strings.TrimSpace(route.Facts["browser_visual_reason"]); reason {
	case "owner_requested":
		return reason
	default:
		return ""
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

// browserHandoffPreservedTransitions names the plan-table transitions that
// meter the bounded click budget. A login handoff resets the node for a fresh
// hidden acquisition and re-arms every other transition budget so the replay
// can repeat settling, snapshots, and presentation — but consumed click
// accounting must survive the reset or a handoff would widen the interaction
// budget.
var browserHandoffPreservedTransitions = map[app.TransitionID]bool{
	"click_recorded":            true,
	"action_settled":            true,
	"validate_transition":       true,
	"assess_after_action":       true,
	"continue_interaction":      true,
	"draft_action_recorded":     true,
	"draft_action_settled":      true,
	"draft_assess_after_action": true,
	"draft_continue":            true,
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
	if route.Slots.TargetKind == string(app.TargetKindPublicNamedTarget) {
		return app.BrowserTargetDescriptor{
			TargetKind: app.BrowserTargetInfoSearch, TargetPhrase: strings.TrimSpace(route.Slots.TargetRef),
			QueryProvenance: app.BrowserQueryOwnerSupplied,
		}
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
	case browserStageDiscoverTarget, browserStageIdentifyTarget, browserStageHealthCheck, browserStageScanTabs, browserStageFocusExisting, browserStageNavigateBlank, browserStageOpenNew,
		browserStageSettleHidden, browserStageSnapshotHidden, browserStageVisualInspect, browserStagePresentVisible, browserStageSettleVisible, browserStageSnapshotVisible:
		return true
	case browserStageSettleAfterAction, browserStageSnapshotAfterAction, browserStageValidateTransition:
		return interaction
	default:
		return false
	}
}

func browserRevision2DirectArguments(state *app.WorkflowState) map[string]any {
	stage := browserActiveStage(state)
	args := map[string]any{}
	switch stage {
	case browserStageDiscoverTarget:
		if state != nil {
			args["query"] = state.Route.Slots.TargetRef
			args["max_results"] = 5
		}
	case browserStageHealthCheck:
		args["require_visible_environment"] = true
	case browserStageSettleHidden, browserStageSettleVisible, browserStageSettleAfterAction:
		args["mode"] = "stable_state"
		args["require_url_stable"] = true
		args["require_ready_state"] = true
		args["allow_no_change"] = stage != browserStageSettleAfterAction
		if state != nil && state.Browser != nil {
			expectedURL := state.Browser.Target.CanonicalURL
			if stage == browserStageSettleVisible && state.Browser.Result != nil &&
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
		if stage == browserStageSettleAfterAction && state != nil && len(state.ActiveNodeIDs) == 1 {
			node := state.Nodes[state.ActiveNodeIDs[0]]
			for _, ref := range currentBrowserSnapshotRefs(node.OutcomeRefs) {
				if ref.Kind == "browser_snapshot" {
					args["before_digest"] = ref.Attributes["content_digest"]
					args["session_generation"] = browserRefGeneration(ref)
				}
			}
		}
	case browserStageSnapshotHidden, browserStageSnapshotAfterAction, browserStageSnapshotVisible:
		if state != nil && state.Route.Slots.Query != "" {
			args["interaction_goal"] = state.Route.Slots.Query
		}
	case browserStagePresentVisible:
		args["reason"] = browserResultPresentationReason
	case browserStageVisualInspect:
		args["reason"] = browserVisualReason(state.Route)
		args["question"] = state.Route.Slots.Query
	case browserStageValidateTransition:
		args["schema_version"] = 2
	}
	return args
}

func browserRevision2StageContext(state *app.WorkflowState, interaction bool) workflowStageContext {
	stage := browserActiveStage(state)
	mode := "autonomous"
	if stage == browserStagePresentVisible || stage == browserStageSettleVisible || stage == browserStageSnapshotVisible || stage == browserStageAssessGoalVisible {
		mode = "collaborative"
	}
	reason := "workflow_stage: " + stage + ". Structural browser stages are Runtime-owned and every page action is followed by stable settle and a fresh snapshot."
	if interaction && strings.HasPrefix(stage, browserStageAssessGoalPrefix) {
		reason += " Assess the frozen owner goal independently and cite only refs from the bound current snapshot."
		switch stage {
		case browserStageAssessGoalInitial:
			reason += " A matching clickable control proves only that a next action is available, not that its destination or effect is already active. Return progress when the evidence only offers that control. Return success only when explicit current or selected state or destination-specific rendered content proves the requested effect already holds, with any current route consistent with that evidence; a route alone is insufficient."
		case browserStageAssessGoalAfterAction:
			reason += " Runtime has already validated that the cited click produced a new rendered-content digest. Decide whether that verified transition and the current route/snapshot evidence semantically satisfy the goal; return progress only when another distinct action is still required, and never retry the same semantic control merely because it remains in navigation. Do not require raw body text or an explicit selected marker when the bounded projection and verified transition otherwise prove the requested destination."
		case browserStageAssessGoalVisible:
			reason += " Runtime has already verified the hidden result, transferred its profile, settled the visible rendered content, and revalidated the result route. Confirm that the current visible snapshot remains semantically consistent with that verified result and preserve the verified result unless the visible snapshot contradicts it; do not demote it merely because the bounded projection exposes the matching destination as a control."
		}
	}
	stageContext := workflowStageContextForState(state, "browse", "web", "public", mode, reason)
	switch stage {
	case browserStageAssessGoalInitial, browserStageChooseAndClick, browserStageAssessGoalAfterAction, browserStageAssessGoalVisible:
		stageContext.EvidenceRequirements = []workflowEvidenceRequirement{{
			ResourceKind: "browser_snapshot", Mode: workflowEvidenceStructured, MaxBytes: 8000,
		}}
	}
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
	case browserStageDiscoverTarget:
		if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalResultsAvailable) {
			return browserRevision2NeedsMore(assessment, app.OutcomeSignalResultsAvailable, browserStageIdentifyTarget, outcome.Refs)
		}
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "public_target_not_found"
	case browserStageIdentifyTarget:
		if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalPublicTargetResolved) {
			if !recordBrowserPublicTarget(state, outcome.Refs) {
				assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "public_target_evidence_invalid"
				return assessment
			}
			return browserRevision2NeedsMore(assessment, app.OutcomeSignalPublicTargetResolved, browserStageHealthCheck, outcome.Refs)
		}
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "public_target_unavailable"
	case browserStageHealthCheck:
		if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalBrowserHealthy) {
			return browserRevision2NeedsMore(assessment, app.OutcomeSignalBrowserHealthy, browserStageScanTabs, nil)
		}
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "browser_environment_unavailable"
	case browserStageScanTabs:
		route := state.Route
		if route.Slots.TargetKind == string(app.TargetKindPublicNamedTarget) && state.Browser.Target.CanonicalURL != "" {
			route.Slots.TargetKind = "url"
			route.Slots.TargetRef = state.Browser.Target.CanonicalURL
		}
		selected, signal, reason := selectBrowserInteractionTab(route, outcome.Refs)
		if reason != "" {
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, reason
			return assessment
		}
		var refs []app.ResourceRef
		if selected != nil {
			refs = []app.ResourceRef{*selected}
		}
		return browserRevision2NeedsMore(assessment, signal, browserInteractionStageForTabSignal(signal), refs)
	case browserStageFocusExisting:
		return browserRevision2Acquired(assessment, outcome, app.OutcomeSignalFocusCompleted, "browser_focus_failed")
	case browserStageOpenNew:
		return browserRevision2Acquired(assessment, outcome, app.OutcomeSignalOpenCompleted, "browser_open_failed")
	case browserStageNavigateBlank:
		return browserRevision2Acquired(assessment, outcome, app.OutcomeSignalNavigateCompleted, "browser_navigate_failed")
	case browserStageSettleHidden, browserStageSettleAfterAction, browserStageSettleVisible:
		if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalTargetSettled) {
			next := browserStageSnapshotHidden
			signal := app.OutcomeSignalHiddenTargetSettled
			if node.Stage == browserStageSettleAfterAction {
				next = browserStageSnapshotAfterAction
				signal = app.OutcomeSignalActionTargetSettled
			} else if node.Stage == browserStageSettleVisible {
				next = browserStageSnapshotVisible
				signal = app.OutcomeSignalVisibleTargetSettled
			}
			return browserRevision2NeedsMore(assessment, signal, next, outcome.Refs)
		}
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "browser_settle_timeout"
	case browserStageSnapshotHidden:
		refs, reason := browserRevision2ValidatedSnapshot(state, outcome, app.BrowserPresentationHidden)
		if reason == "" && interaction && !browserSnapshotHasInteractionEvidence(refs) {
			reason = "browser_snapshot_not_ready"
		}
		if reason == "browser_snapshot_route_changed" || reason == "browser_snapshot_not_ready" {
			return browserRevision2SnapshotRetry(state, assessment, "hidden_snapshot_drifted", app.OutcomeSignalHiddenSnapshotDrifted, browserStageSettleHidden)
		}
		if reason != "" {
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, reason
			return assessment
		}
		if interaction {
			freezeBrowserGoalContract(state, refs)
			return browserRevision2NeedsMore(assessment, app.OutcomeSignalTargetValidated, browserStageAssessGoalInitial, refs)
		}
		recordBrowserHiddenResult(state, refs, outcome.ToolCallID, nil)
		return browserRevision2NeedsMore(assessment, app.OutcomeSignalTargetValidated, browserStagePresentVisible, browserPresentationRefs(state, refs))
	case browserStageChooseAndClick:
		switch {
		case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalInteractionLoopDetected):
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "interaction_loop_detected"
		case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalUnsafeClickTarget):
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "unsafe_click_target"
		case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalSnapshotStale):
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "snapshot_stale"
		case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalClickCompleted):
			state.Browser.CompletedClicks++
			return browserRevision2NeedsMore(assessment, app.OutcomeSignalClickCompleted, browserStageSettleAfterAction, outcome.Refs)
		default:
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "browser_click_failed"
		}
	case browserStageSnapshotAfterAction:
		refs, reason := browserRevision2ValidatedSnapshot(state, outcome, app.BrowserPresentationHidden)
		if reason == "" && browserSnapshotOutcomeRepeated(refs) {
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "interaction_loop_detected"
			return assessment
		}
		if reason == "" && !browserSnapshotHasInteractionEvidence(refs) {
			reason = "browser_snapshot_not_ready"
		}
		if reason == "browser_snapshot_route_changed" || reason == "browser_snapshot_not_ready" {
			return browserRevision2SnapshotRetry(state, assessment, "action_snapshot_drifted", app.OutcomeSignalActionSnapshotDrifted, browserStageSettleAfterAction)
		}
		if reason != "" {
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, reason
			return assessment
		}
		refs = browserTransitionRefs(node.OutcomeRefs, refs)
		return browserRevision2NeedsMore(assessment, app.OutcomeSignalInteractionVerificationRequired, browserStageValidateTransition, refs)
	case browserStageValidateTransition:
		if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalTargetValidated) {
			return browserRevision2NeedsMore(assessment, app.OutcomeSignalTargetValidated, browserStageAssessGoalAfterAction, append(node.OutcomeRefs, outcome.Refs...))
		}
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "interaction_transition_invalid"
	case browserStageAssessGoalInitial, browserStageAssessGoalAfterAction:
		switch {
		case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalInteractionGoalSatisfied):
			refs := currentBrowserSnapshotRefs(node.OutcomeRefs)
			recordBrowserHiddenResult(state, refs, outcome.ToolCallID, browserGoalEvidenceRefs(outcome.Refs))
			return browserRevision2NeedsMore(assessment, app.OutcomeSignalInteractionGoalSatisfied, browserStagePresentVisible, browserPresentationRefs(state, refs))
		case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalInteractionProgress):
			if state.Browser.CompletedClicks >= browserInteractionMaxClicks {
				assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "interaction_attempt_limit"
				return assessment
			}
			refs := currentBrowserSnapshotRefs(node.OutcomeRefs)
			return browserRevision2NeedsMore(assessment, app.OutcomeSignalInteractionProgress, browserStageChooseAndClick, refs)
		default:
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "interaction_goal_not_satisfied"
		}
	case browserStageVisualInspect:
		if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalVisualEvidenceAvailable) && recordBrowserVisualEvidence(state, outcome.Refs) {
			return browserRevision2NeedsMore(assessment, app.OutcomeSignalVisualEvidenceAvailable, browserStagePresentVisible, browserPresentationRefs(state, nil))
		}
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "browser_visual_inspection_failed"
	case browserStagePresentVisible:
		if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalOpenCompleted) {
			return browserRevision2NeedsMore(assessment, app.OutcomeSignalPresentationOpened, browserStageSettleVisible, outcome.Refs)
		}
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "browser_presentation_failed"
	case browserStageSnapshotVisible:
		refs, reason := browserRevision2ValidatedSnapshot(state, outcome, app.BrowserPresentationVisible)
		if reason == "" && interaction && !browserSnapshotHasInteractionEvidence(refs) {
			reason = "browser_snapshot_not_ready"
		}
		if reason == "browser_snapshot_route_changed" || reason == "browser_snapshot_not_ready" {
			return browserRevision2SnapshotRetry(state, assessment, "visible_snapshot_drifted", app.OutcomeSignalVisibleSnapshotDrifted, browserStageSettleVisible)
		}
		if reason != "" {
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, reason
			return assessment
		}
		recordBrowserVisibleResult(state, refs, outcome.ToolCallID)
		if interaction {
			if assertion, equivalent := browserPresentationEquivalent(state, refs); equivalent {
				assessment.Status, assessment.ReasonCode = app.AssessmentComplete, "browser_visible_presentation_equivalent"
				assessment.SelectedRefs = append(refs, assertion)
				return assessment
			}
			return browserRevision2NeedsMore(assessment, app.OutcomeSignalPresentationValidated, browserStageAssessGoalVisible, refs)
		}
		assessment.Status, assessment.ReasonCode = app.AssessmentComplete, "browser_visible_result_verified"
		assessment.SelectedRefs = refs
	case browserStageAssessGoalVisible:
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

func recordBrowserVisualEvidence(state *app.WorkflowState, refs []app.ResourceRef) bool {
	if state == nil || state.Browser == nil {
		return false
	}
	for _, ref := range refs {
		if ref.Kind != "browser_visual_evidence" || strings.TrimSpace(ref.Ref) == "" {
			continue
		}
		sessionGeneration, sessionErr := strconv.ParseUint(ref.Attributes["session_generation"], 10, 64)
		pageGeneration, pageErr := strconv.ParseUint(ref.Attributes["page_generation"], 10, 64)
		createdAt, timeErr := time.Parse(time.RFC3339Nano, ref.Attributes["created_at"])
		if sessionErr != nil || pageErr != nil || timeErr != nil || sessionGeneration == 0 || pageGeneration == 0 ||
			ref.Attributes["page_id"] == "" || ref.Attributes["snapshot_id"] == "" || ref.Attributes["snapshot_digest"] == "" ||
			ref.Attributes["screenshot_ref"] == "" || ref.Attributes["screenshot_digest"] == "" {
			return false
		}
		state.Browser.VisualEvidence = append(state.Browser.VisualEvidence, app.BrowserVisualEvidence{
			EvidenceID: ref.Ref, Reason: ref.Attributes["reason"], SessionGeneration: sessionGeneration,
			PageGeneration: pageGeneration, PageID: ref.Attributes["page_id"], SnapshotID: ref.Attributes["snapshot_id"],
			SnapshotDigest: ref.Attributes["snapshot_digest"], ScreenshotRef: ref.Attributes["screenshot_ref"],
			ScreenshotDigest: ref.Attributes["screenshot_digest"], NormalizedURL: ref.Attributes["normalized_url"],
			Summary: ref.Attributes["summary"], Model: ref.Attributes["model"], Stale: false, CreatedAt: createdAt.UTC(),
		})
		return true
	}
	return false
}

func browserRevision2Acquired(assessment app.NodeAssessment, outcome app.ToolOutcome, signal app.OutcomeSignal, failure string) app.NodeAssessment {
	if containsOutcomeSignal(outcome.Signals, signal) {
		return browserRevision2NeedsMore(assessment, signal, browserStageSettleHidden, outcome.Refs)
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
	if strings.TrimSpace(snapshot.Attributes["digest"]) == "" || strings.TrimSpace(snapshot.Attributes["content_digest"]) == "" {
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
		HiddenContentDigest:  snapshot.Attributes["content_digest"],
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
	state.Browser.Result.VisibleContentDigest = snapshot.Attributes["content_digest"]
	state.Browser.Result.SourceToolCallIDs = appendUniqueString(state.Browser.Result.SourceToolCallIDs, callID)
	state.Browser.Result.VerifiedAt = time.Now().UTC()
}

func browserPresentationEquivalent(state *app.WorkflowState, refs []app.ResourceRef) (app.ResourceRef, bool) {
	if state == nil || state.Browser == nil || state.Browser.Result == nil {
		return app.ResourceRef{}, false
	}
	result := state.Browser.Result
	page, snapshot, ok := browserSnapshotRefs(refs)
	if !ok || result.GoalAssessmentCallID == "" || result.HiddenContentDigest == "" ||
		result.HiddenContentDigest != snapshot.Attributes["content_digest"] ||
		result.HiddenSession.OwnerID == "" || result.HiddenSession.OwnerID != page.Attributes["owner_id"] ||
		result.HiddenSession.ProfileID == "" || result.HiddenSession.ProfileID != page.Attributes["profile_id"] {
		return app.ResourceRef{}, false
	}
	assertionID := app.NewID("browser_presentation_equivalence")
	result.PresentationEquivalent = true
	result.PresentationAssertionID = assertionID
	result.VerifiedAt = time.Now().UTC()
	return app.ResourceRef{
		Kind: "browser_presentation_equivalence", Ref: assertionID,
		Provenance: snapshot.Provenance,
		Attributes: map[string]string{
			"hidden_snapshot_id": result.HiddenSnapshotID, "visible_snapshot_id": snapshot.Ref,
			"content_equivalent": "true", "route_consistent": "true", "profile_equivalent": "true",
		},
	}, true
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
		return (state.Plan.ProfileID == app.WorkflowBrowserInteraction || state.Plan.ProfileID == app.WorkflowBrowserFormDraft) && sameBrowserOrigin(targetURL, candidateURL)
	case string(app.TargetKindBrowserCurrentTab):
		return state.Plan.ProfileID == app.WorkflowBrowserInteraction || state.Plan.ProfileID == app.WorkflowBrowserFormDraft
	case string(app.TargetKindPublicNamedTarget):
		if state.Browser == nil || state.Browser.Target.TargetKind != app.BrowserTargetInfoResolved {
			return false
		}
		targetURL := state.Browser.Target.CanonicalURL
		if normalizeBrowserURL(targetURL) == normalizeBrowserURL(candidateURL) {
			return true
		}
		return (state.Plan.ProfileID == app.WorkflowBrowserInteraction || state.Plan.ProfileID == app.WorkflowBrowserFormDraft) &&
			sameBrowserOrigin(targetURL, candidateURL)
	default:
		return false
	}
}

func recordBrowserPublicTarget(state *app.WorkflowState, refs []app.ResourceRef) bool {
	if state == nil || state.Browser == nil {
		return false
	}
	var evidence *app.ResourceRef
	var targetURL string
	for index := range refs {
		switch refs[index].Kind {
		case "public_target_evidence":
			evidence = &refs[index]
		case "public_target_url":
			targetURL = normalizeBrowserURL(refs[index].Ref)
		}
	}
	if evidence == nil || strings.TrimSpace(evidence.Ref) == "" || targetURL == "" ||
		normalizeBrowserURL(evidence.Attributes["normalized_final_url"]) != targetURL ||
		evidence.Attributes["safety_gate_status"] != "passed" {
		return false
	}
	resultIndex, err := strconv.Atoi(evidence.Attributes["info_result_index"])
	if err != nil || resultIndex < 0 {
		return false
	}
	createdAt, err := time.Parse(time.RFC3339Nano, evidence.Attributes["created_at"])
	if err != nil {
		return false
	}
	state.Browser.PublicTarget = &app.BrowserPublicTargetEvidence{
		EvidenceID:         evidence.Ref,
		ResolutionSource:   evidence.Attributes["resolution_source"],
		OwnerTargetPhrase:  evidence.Attributes["owner_target_phrase"],
		RequestedSurface:   evidence.Attributes["requested_surface_kind"],
		InfoRequestID:      evidence.Attributes["info_request_id"],
		InfoResultIndex:    &resultIndex,
		SourceResultRef:    evidence.Attributes["source_result_ref"],
		CanonicalEntryURL:  normalizeBrowserURL(evidence.Attributes["canonical_entry_url"]),
		NormalizedFinalURL: targetURL,
		SafetyGateStatus:   evidence.Attributes["safety_gate_status"],
		CreatedAt:          createdAt.UTC(),
	}
	state.Browser.Target.TargetKind = app.BrowserTargetInfoResolved
	state.Browser.Target.CanonicalURL = targetURL
	state.Browser.Target.RoutePath = ""
	state.Browser.Target.RouteFragment = ""
	if parsed, parseErr := url.Parse(targetURL); parseErr == nil {
		state.Browser.Target.RoutePath = parsed.EscapedPath()
		state.Browser.Target.RouteFragment = parsed.Fragment
	}
	state.Browser.Target.QueryProvenance = app.BrowserQueryProviderVolatile
	state.Browser.Target.RedactedURL = targetURL
	return true
}

func isManagedBrowserWorkflow(id app.WorkflowID) bool {
	switch id {
	case app.WorkflowBrowserAutomation, app.WorkflowBrowserPageRead, app.WorkflowBrowserInteraction, app.WorkflowBrowserFormDraft:
		return true
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
	actionRefs := map[string]bool{}
	for _, ref := range existing {
		if ref.Kind == "browser_click" || ref.Kind == "browser_draft" {
			actionRefs[ref.Ref] = true
		}
	}
	for _, ref := range existing {
		switch ref.Kind {
		case "browser_snapshot":
			ref.Kind = "browser_before_snapshot"
			refs = append(refs, ref)
		case "browser_click", "browser_draft":
			refs = append(refs, ref)
		case "browser_element":
			if actionRefs[ref.Ref] {
				refs = append(refs, ref)
			}
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

func currentBrowserSnapshot(refs []app.ResourceRef) (app.ResourceRef, bool) {
	for _, ref := range currentBrowserSnapshotRefs(refs) {
		if ref.Kind == "browser_snapshot" {
			return ref, true
		}
	}
	return app.ResourceRef{}, false
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
