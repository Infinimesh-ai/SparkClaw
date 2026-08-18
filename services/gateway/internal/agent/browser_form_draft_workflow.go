package agent

import (
	"strconv"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

const browserFormDraftRevision2 = 2
const browserStageChooseAndDraft = "choose_and_draft"

type browserFormDraftProfile struct{}

func (browserFormDraftProfile) ID() app.WorkflowID           { return app.WorkflowBrowserFormDraft }
func (browserFormDraftProfile) Revision() int                { return browserFormDraftRevision2 }
func (browserFormDraftProfile) Capability() app.CapabilityID { return app.CapabilityBrowserFormDraft }
func (browserFormDraftProfile) Finalization() workflowFinalizationMode {
	return workflowFinalizationGrounded
}
func (browserFormDraftProfile) RoutingSemantics() workflowRoutingSemantics {
	return workflowRoutingSemantics{Variants: []workflowRoutingVariant{{
		Key: "draft", Route: workflowRouteTemplate{Operation: app.RouteOperationDraft},
		EmbedTexts: []string{
			"在当前网页的姓名栏填写张三但不要提交", "把官网联系表单的主题选成技术支持，不要发送", "Fill the visible message field with this text without submitting", "Select the requested option in the web form but leave it as a draft",
		},
		TreeDescription: "Fill or select up to five ordinary reversible values in one managed browser form without clicking, submitting, sending, publishing, uploading, entering credentials, or making a payment. Every field action requires separate owner approval.",
		HardNegatives:   []string{"点击下一步", "提交这个表单", "发送这封邮件", "输入密码登录", "购买商品", "编辑本地文档"},
	}}}
}
func (p browserFormDraftProfile) Resolve(route app.RouteDecision, sourceTurnID string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	intent := singleObjectiveIntent(sourceTurnID, app.IntentDomainWeb, app.IntentOperationDraft, browserRouteIntentTarget(route), app.DataScopePublic)
	plan := browserFormDraftPlan()
	if browserVisualReason(route) != "" {
		browserRevision2EnableVisualInspection(&plan)
	}
	if route.Slots.TargetKind == string(app.TargetKindPublicNamedTarget) {
		browserRevision2EnableTargetDiscovery(&plan)
	}
	return intent, plan, nil
}
func (browserFormDraftProfile) Prepare(state *app.WorkflowState) (workflowPreparation, error) {
	ensureBrowserWorkflowState(state)
	return workflowPreparation{}, nil
}
func (browserFormDraftProfile) DirectStage(state *app.WorkflowState) bool {
	return browserRevision2DirectStage(state, true)
}
func (browserFormDraftProfile) DirectStageArguments(state *app.WorkflowState) map[string]any {
	args := browserRevision2DirectArguments(state)
	if browserActiveStage(state) == browserStageSettleAfterAction {
		// Draft values are intentionally omitted from the rendered-content
		// digest. Fresh snapshot generation and lineage verify the mutation.
		args["allow_no_change"] = true
	}
	return args
}
func (browserFormDraftProfile) TransitionInstruction(_ app.ToolOutcome, assessment app.NodeAssessment) string {
	return browserRevision2TransitionInstruction(assessment)
}
func (browserFormDraftProfile) StageContext(state *app.WorkflowState) workflowStageContext {
	stage := browserActiveStage(state)
	mode := "autonomous"
	if stage == browserStagePresentVisible || stage == browserStageSettleVisible || stage == browserStageSnapshotVisible || stage == browserStageAssessGoalVisible ||
		browserFormDraftCurrentPresentation(state) == app.BrowserPresentationVisible {
		mode = "collaborative"
	}
	reason := "workflow_stage: " + stage + ". Bind only fresh structured browser refs and exact values from the frozen owner request. Draft actions never click, submit, send, publish, upload, enter credentials, or handle payment data."
	if stage == browserStageChooseAndDraft {
		reason += " For browser.type or browser.select, copy uid exactly from the current tool schema enum; never invent an alias or reuse an older snapshot ref."
		if completed := completedBrowserDraftControlNames(state); len(completed) > 0 {
			reason += " Completed draft controls: " + strings.Join(completed, ", ") + ". Choose a remaining requested control and never repeat a completed control."
		}
	}
	context := workflowStageContextForState(state, "browse", "reversible_form_draft", "public", mode, reason)
	context.EstimatedRisk = app.RiskDraft
	switch stage {
	case browserStageAssessGoalInitial, browserStageChooseAndDraft, browserStageAssessGoalAfterAction, browserStageAssessGoalVisible:
		context.EvidenceRequirements = []workflowEvidenceRequirement{{ResourceKind: "browser_snapshot", Mode: workflowEvidenceStructured, MaxBytes: 8000}}
	}
	return context
}

func completedBrowserDraftControlNames(state *app.WorkflowState) []string {
	if state == nil || state.Browser == nil {
		return nil
	}
	names := []string{}
	seen := map[string]bool{}
	for _, action := range state.Browser.DraftActions {
		name := strings.TrimSpace(action.AccessibleName)
		key := strings.ToLower(name)
		if !action.Completed || name == "" || seen[key] {
			continue
		}
		seen[key] = true
		names = append(names, name)
	}
	return names
}
func (browserFormDraftProfile) Assess(state *app.WorkflowState, outcome app.ToolOutcome) app.NodeAssessment {
	assessment := baseNodeAssessment(outcome)
	if state == nil || state.Browser == nil {
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "browser_state_missing"
		return assessment
	}
	node := state.Nodes[outcome.NodeID]
	switch node.Stage {
	case browserStageSnapshotHidden:
		refs, reason := browserRevision2ValidatedSnapshot(state, outcome, app.BrowserPresentationHidden)
		if reason == "" && !browserSnapshotHasInteractionEvidence(refs) {
			reason = "browser_snapshot_not_ready"
		}
		if reason == "browser_snapshot_route_changed" || reason == "browser_snapshot_not_ready" {
			return browserRevision2SnapshotRetry(state, assessment, "hidden_snapshot_drifted", app.OutcomeSignalHiddenSnapshotDrifted, browserStageSettleHidden)
		}
		if reason != "" {
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, reason
			return assessment
		}
		freezeBrowserDraftGoalContract(state, refs)
		return browserRevision2NeedsMore(assessment, app.OutcomeSignalTargetValidated, browserStageAssessGoalInitial, refs)
	case browserStageChooseAndDraft:
		switch {
		case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalDraftActionForbidden):
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "draft_action_forbidden"
		case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalSnapshotStale):
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "draft_action_stale"
		case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalDraftActionCompleted):
			if len(state.Browser.DraftActions) >= app.BrowserFormDraftMaxActions {
				assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "draft_action_limit"
				return assessment
			}
			recordBrowserDraftAction(state, outcome.Refs)
			refs := append(append([]app.ResourceRef(nil), node.OutcomeRefs...), outcome.Refs...)
			return browserRevision2NeedsMore(assessment, app.OutcomeSignalDraftActionCompleted, browserStageSettleAfterAction, refs)
		default:
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "browser_draft_action_failed"
		}
	case browserStageSnapshotAfterAction:
		expectedPresentation := app.BrowserPresentationHidden
		if browserFormDraftCurrentPresentation(state) == app.BrowserPresentationVisible {
			expectedPresentation = app.BrowserPresentationVisible
		}
		refs, reason := browserRevision2ValidatedSnapshot(state, outcome, expectedPresentation)
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
		if !browserDraftTransitionValid(refs) {
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "draft_transition_invalid"
			return assessment
		}
		return browserRevision2NeedsMore(assessment, app.OutcomeSignalTargetValidated, browserStageAssessGoalAfterAction, refs)
	case browserStageAssessGoalInitial:
		switch {
		case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalInteractionGoalSatisfied):
			refs := currentBrowserSnapshotRefs(node.OutcomeRefs)
			recordBrowserHiddenResult(state, refs, outcome.ToolCallID, browserGoalEvidenceRefs(outcome.Refs))
			return browserRevision2NeedsMore(assessment, app.OutcomeSignalInteractionGoalSatisfied, browserStagePresentVisible, browserPresentationRefs(state, refs))
		case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalInteractionProgress):
			refs := currentBrowserSnapshotRefs(node.OutcomeRefs)
			recordBrowserHiddenResult(state, refs, outcome.ToolCallID, browserGoalEvidenceRefs(outcome.Refs))
			return browserRevision2NeedsMore(assessment, app.OutcomeSignalInteractionProgress, browserStagePresentVisible, browserPresentationRefs(state, refs))
		default:
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "draft_goal_not_satisfied"
		}
	case browserStageAssessGoalAfterAction:
		refs := currentBrowserSnapshotRefs(node.OutcomeRefs)
		switch {
		case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalInteractionGoalSatisfied):
			if browserFormDraftHasRequestedIncompleteControl(state, refs) {
				return browserRevision2NeedsMore(assessment, app.OutcomeSignalInteractionProgress, browserStageChooseAndDraft, refs)
			}
			recordBrowserVisibleResult(state, refs, outcome.ToolCallID)
			assessment.Status, assessment.ReasonCode = app.AssessmentComplete, "browser_visible_draft_verified"
			assessment.SelectedRefs = append(refs, outcome.Refs...)
			updateBrowserDraftGoalEvidence(state, outcome)
		case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalInteractionProgress):
			if len(state.Browser.DraftActions) >= app.BrowserFormDraftMaxActions {
				assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "draft_action_limit"
				return assessment
			}
			return browserRevision2NeedsMore(assessment, app.OutcomeSignalInteractionProgress, browserStageChooseAndDraft, refs)
		default:
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "draft_goal_not_satisfied"
		}
	case browserStageAssessGoalVisible:
		refs := currentBrowserSnapshotRefs(node.OutcomeRefs)
		switch {
		case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalInteractionGoalSatisfied) && !browserFormDraftHasRequestedIncompleteControl(state, refs):
			assessment.Status, assessment.ReasonCode = app.AssessmentComplete, "browser_visible_draft_verified"
			assessment.SelectedRefs = append(refs, outcome.Refs...)
			updateBrowserDraftGoalEvidence(state, outcome)
		case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalInteractionGoalSatisfied), containsOutcomeSignal(outcome.Signals, app.OutcomeSignalInteractionProgress):
			if len(state.Browser.DraftActions) >= app.BrowserFormDraftMaxActions {
				assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "draft_action_limit"
				return assessment
			}
			return browserRevision2NeedsMore(assessment, app.OutcomeSignalInteractionProgress, browserStageChooseAndDraft, refs)
		default:
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "browser_visible_draft_not_verified"
		}
	default:
		return assessBrowserRevision2(state, outcome, true)
	}
	return assessment
}

func browserFormDraftCurrentPresentation(state *app.WorkflowState) app.BrowserPresentation {
	if state == nil || len(state.ActiveNodeIDs) != 1 {
		return ""
	}
	refs := currentBrowserSnapshotRefs(state.Nodes[state.ActiveNodeIDs[0]].OutcomeRefs)
	for _, ref := range refs {
		if ref.Kind == "browser_snapshot" {
			return app.BrowserPresentation(ref.Attributes["presentation"])
		}
	}
	return ""
}

func browserFormDraftHasRequestedIncompleteControl(state *app.WorkflowState, refs []app.ResourceRef) bool {
	if state == nil || state.Browser == nil {
		return true
	}
	request := strings.ToLower(strings.TrimSpace(state.Route.Slots.Query))
	if request == "" {
		return true
	}
	for _, ref := range currentBrowserSnapshotRefs(refs) {
		name := strings.TrimSpace(ref.Attributes["name"])
		if ref.Kind != "browser_element" || name == "" || !strings.Contains(request, strings.ToLower(name)) {
			continue
		}
		role, container := ref.Attributes["role"], ref.Attributes["container"]
		if !toolhub.BrowserDraftControlAllowed("browser.type", role, name, container) &&
			!toolhub.BrowserDraftControlAllowed("browser.select", role, name, container) {
			continue
		}
		if !browserDraftElementAlreadyCompleted(state.Browser.DraftActions, ref) {
			return true
		}
	}
	return false
}

func updateBrowserDraftGoalEvidence(state *app.WorkflowState, outcome app.ToolOutcome) {
	if state == nil || state.Browser == nil || state.Browser.Result == nil {
		return
	}
	state.Browser.Result.GoalEvidenceRefs = browserGoalEvidenceRefs(outcome.Refs)
	state.Browser.Result.GoalAssessmentCallID = outcome.ToolCallID
	state.Browser.Result.VerifiedAt = time.Now().UTC()
}

func browserFormDraftPlan() app.WorkflowPlan {
	nodeID := app.WorkflowNodeID("browser_form_draft")
	scope := browserFormDraftScope()
	transitions := []app.ScopeTransition{
		browserRevision2Transition("health_ready", browserStageScanTabs, app.OutcomeSignalBrowserHealthy, 1, scope),
		browserRevision2Transition("reuse_existing", browserStageFocusExisting, app.OutcomeSignalTargetTabExists, 1, scope),
		browserRevision2Transition("reuse_blank", browserStageNavigateBlank, app.OutcomeSignalTargetTabBlank, 1, scope),
		browserRevision2Transition("open_missing", browserStageOpenNew, app.OutcomeSignalTargetTabMissing, 1, scope),
		browserRevision2Transition("visible_opened", browserStageSettleVisible, app.OutcomeSignalPresentationOpened, 2, scope),
		browserRevision2Transition("focus_acquired", browserStageSettleHidden, app.OutcomeSignalFocusCompleted, 1, scope),
		browserRevision2Transition("open_acquired", browserStageSettleHidden, app.OutcomeSignalOpenCompleted, 2, scope),
		browserRevision2Transition("navigate_acquired", browserStageSettleHidden, app.OutcomeSignalNavigateCompleted, 1, scope),
		browserRevision2Transition("hidden_settled", browserStageSnapshotHidden, app.OutcomeSignalHiddenTargetSettled, app.BrowserFormDraftMaxActions+2, scope),
		browserRevision2Transition("hidden_snapshot_drifted", browserStageSettleHidden, app.OutcomeSignalHiddenSnapshotDrifted, browserSnapshotSettleRetryLimit, scope),
		browserRevision2Transition("draft_assess_initial", browserStageAssessGoalInitial, app.OutcomeSignalTargetValidated, 1, scope),
		browserRevision2Transition("draft_initial_action", browserStagePresentVisible, app.OutcomeSignalInteractionProgress, 1, scope),
		browserRevision2Transition("draft_action_recorded", browserStageSettleAfterAction, app.OutcomeSignalDraftActionCompleted, app.BrowserFormDraftMaxActions, scope),
		browserRevision2Transition("draft_action_settled", browserStageSnapshotAfterAction, app.OutcomeSignalActionTargetSettled, app.BrowserFormDraftMaxActions, scope),
		browserRevision2Transition("draft_action_snapshot_drifted", browserStageSettleAfterAction, app.OutcomeSignalActionSnapshotDrifted, browserSnapshotSettleRetryLimit, scope),
		browserRevision2Transition("draft_assess_after_action", browserStageAssessGoalAfterAction, app.OutcomeSignalTargetValidated, app.BrowserFormDraftMaxActions, scope),
		browserRevision2Transition("draft_continue", browserStageChooseAndDraft, app.OutcomeSignalInteractionProgress, app.BrowserFormDraftMaxActions-1, scope),
		browserRevision2Transition("draft_goal_satisfied", browserStagePresentVisible, app.OutcomeSignalInteractionGoalSatisfied, 2, scope),
		browserRevision2Transition("visible_settled", browserStageSnapshotVisible, app.OutcomeSignalVisibleTargetSettled, 2, scope),
		browserRevision2Transition("visible_snapshot_drifted", browserStageSettleVisible, app.OutcomeSignalVisibleSnapshotDrifted, browserSnapshotSettleRetryLimit, scope),
		browserRevision2Transition("draft_assess_visible", browserStageAssessGoalVisible, app.OutcomeSignalPresentationValidated, 1, scope),
	}
	bindings := []app.ArgumentBinding{
		{Capability: app.ToolCapabilityBrowserFocus, Argument: "page_id", ResourceKind: "browser_tab", Source: app.ArgumentBindingOutcomeRef},
		{Capability: app.ToolCapabilityBrowserOpen, Argument: "url", ResourceKind: "url", Source: app.ArgumentBindingIntentTarget, TargetKinds: []app.TargetKind{app.TargetKindExplicitURL}},
		{Capability: app.ToolCapabilityBrowserOpen, Argument: "url", ResourceKind: "browser_result_url", Source: app.ArgumentBindingOutcomeRef},
		{Capability: app.ToolCapabilityBrowserNavigate, Argument: "url", ResourceKind: "url", Source: app.ArgumentBindingIntentTarget, TargetKinds: []app.TargetKind{app.TargetKindExplicitURL}},
		{Capability: app.ToolCapabilityBrowserNavigate, Argument: "page_id", ResourceKind: "browser_tab", Source: app.ArgumentBindingOutcomeRef},
		{Capability: app.ToolCapabilityBrowserWait, Argument: "page_id", ResourceKind: "browser_page", Source: app.ArgumentBindingOutcomeRef},
		{Capability: app.ToolCapabilityBrowserSnapshot, Argument: "page_id", ResourceKind: "browser_page", Source: app.ArgumentBindingOutcomeRef},
		{Capability: app.ToolCapabilityBrowserGoalAssess, Argument: "snapshot_id", ResourceKind: "browser_snapshot", Source: app.ArgumentBindingOutcomeRef},
	}
	for _, capability := range []string{app.ToolCapabilityBrowserFormType, app.ToolCapabilityBrowserFormSelect} {
		bindings = append(bindings,
			app.ArgumentBinding{Capability: capability, Argument: "page_id", ResourceKind: "browser_page", Source: app.ArgumentBindingOutcomeRef},
			app.ArgumentBinding{Capability: capability, Argument: "snapshot_id", ResourceKind: "browser_snapshot", Source: app.ArgumentBindingOutcomeRef},
			app.ArgumentBinding{Capability: capability, Argument: "session_generation", ResourceKind: "browser_snapshot", Source: app.ArgumentBindingOutcomeRef, SourceKey: "session_generation"},
			app.ArgumentBinding{Capability: capability, Argument: "page_generation", ResourceKind: "browser_snapshot", Source: app.ArgumentBindingOutcomeRef, SourceKey: "page_generation"},
			app.ArgumentBinding{Capability: capability, Argument: "uid", ResourceKind: "browser_element", Source: app.ArgumentBindingOutcomeRef},
		)
	}
	return app.WorkflowPlan{
		SchemaVersion: 1, ProfileID: app.WorkflowBrowserFormDraft, ProfileRevision: browserFormDraftRevision2,
		InitialNodeIDs: []app.WorkflowNodeID{nodeID}, Completion: app.CompletionEvidence,
		Nodes: []app.WorkflowNode{{
			ID: nodeID, InitialStage: browserStageHealthCheck,
			Goal:         app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "Produce one verified reversible browser form draft without committing it", Completion: app.CompletionEvidence},
			InitialScope: scope, Transitions: transitions, ArgumentBindings: bindings,
			StageCapabilities: []app.StageCapabilityRule{
				{Stage: browserStageHealthCheck, Capabilities: []string{app.ToolCapabilityBrowserHealth}},
				{Stage: browserStageScanTabs, Capabilities: []string{app.ToolCapabilityBrowserListTabs}},
				{Stage: browserStageFocusExisting, Capabilities: []string{app.ToolCapabilityBrowserFocus}},
				{Stage: browserStageNavigateBlank, Capabilities: []string{app.ToolCapabilityBrowserNavigate}},
				{Stage: browserStageOpenNew, Capabilities: []string{app.ToolCapabilityBrowserOpen}},
				{Stage: browserStageSettleHidden, Capabilities: []string{app.ToolCapabilityBrowserWait}},
				{Stage: browserStageSnapshotHidden, Capabilities: []string{app.ToolCapabilityBrowserSnapshot}},
				{Stage: browserStageAssessGoalInitial, Capabilities: []string{app.ToolCapabilityBrowserGoalAssess}},
				{Stage: browserStageChooseAndDraft, Capabilities: []string{app.ToolCapabilityBrowserFormType, app.ToolCapabilityBrowserFormSelect}},
				{Stage: browserStageSettleAfterAction, Capabilities: []string{app.ToolCapabilityBrowserWait}},
				{Stage: browserStageSnapshotAfterAction, Capabilities: []string{app.ToolCapabilityBrowserSnapshot}},
				{Stage: browserStageAssessGoalAfterAction, Capabilities: []string{app.ToolCapabilityBrowserGoalAssess}},
				{Stage: browserStagePresentVisible, Capabilities: []string{app.ToolCapabilityBrowserOpen}},
				{Stage: browserStageSettleVisible, Capabilities: []string{app.ToolCapabilityBrowserWait}},
				{Stage: browserStageSnapshotVisible, Capabilities: []string{app.ToolCapabilityBrowserSnapshot}},
				{Stage: browserStageAssessGoalVisible, Capabilities: []string{app.ToolCapabilityBrowserGoalAssess}},
			},
			AllowedRisks: []app.RiskLevel{app.RiskRead, app.RiskDraft}, MaxAttempts: 48,
		}},
	}
}

func browserFormDraftScope() app.CapabilityScope {
	return app.CapabilityScope{
		MaterializeAll: true,
		Requirements: []app.CapabilityRequirement{
			{Name: app.ToolCapabilityBrowserHealth}, {Name: app.ToolCapabilityBrowserListTabs},
			{Name: app.ToolCapabilityBrowserFocus}, {Name: app.ToolCapabilityBrowserOpen},
			{Name: app.ToolCapabilityBrowserNavigate}, {Name: app.ToolCapabilityBrowserWait},
			{Name: app.ToolCapabilityBrowserSnapshot}, {Name: app.ToolCapabilityBrowserFormType},
			{Name: app.ToolCapabilityBrowserFormSelect}, {Name: app.ToolCapabilityBrowserGoalAssess},
		},
		DeniedEffects: []app.ToolEffect{app.ToolEffectWorkspaceWrite, app.ToolEffectLocalWrite},
	}
}

func freezeBrowserDraftGoalContract(state *app.WorkflowState, refs []app.ResourceRef) {
	if state.Browser.Goal != nil {
		return
	}
	_, snapshot, _ := browserSnapshotRefs(refs)
	state.Browser.Goal = &app.BrowserGoalContract{
		GoalID: "browser_draft_goal_" + state.Intent.SourceTurnID, SchemaVersion: app.BrowserGoalContractSchemaVersion,
		OwnerGoal: state.Route.Slots.Query, RequiredCriteria: []string{strings.TrimSpace(state.Route.Slots.Query)},
		ForbiddenEffects: []string{"click", "form_submission", "credential_entry", "captcha", "payment", "upload", "download", "deletion", "external_send", "publish"},
		Target:           state.Browser.Target, MaxClicks: 0, CreatedFromSnapshotID: snapshot.Ref, CreatedAt: time.Now().UTC(),
	}
}

func recordBrowserDraftAction(state *app.WorkflowState, refs []app.ResourceRef) {
	for _, ref := range refs {
		if ref.Kind != "browser_draft" {
			continue
		}
		sessionGeneration, _ := strconv.ParseUint(ref.Attributes["session_generation"], 10, 64)
		pageGeneration, _ := strconv.ParseUint(ref.Attributes["page_generation"], 10, 64)
		state.Browser.DraftActions = append(state.Browser.DraftActions, app.BrowserDraftAction{
			ActionID: ref.Attributes["action_id"], SessionGeneration: sessionGeneration, PageGeneration: pageGeneration,
			PageID: ref.Attributes["page_id"], SnapshotID: ref.Attributes["snapshot_id"], SnapshotDigest: ref.Attributes["snapshot_digest"],
			ElementRef: ref.Ref, Role: ref.Attributes["role"], AccessibleName: ref.Attributes["name"], FormContext: ref.Attributes["container"],
			Operation: ref.Attributes["operation"], ValueSource: ref.Attributes["value_source"], ValueDigest: ref.Attributes["value_digest"], Completed: true,
		})
		return
	}
}

func browserDraftTransitionValid(refs []app.ResourceRef) bool {
	var before, after, draft app.ResourceRef
	for _, ref := range refs {
		switch ref.Kind {
		case "browser_before_snapshot":
			before = ref
		case "browser_after_snapshot", "browser_snapshot":
			after = ref
		case "browser_draft":
			draft = ref
		}
	}
	if before.Ref == "" || after.Ref == "" || draft.Ref == "" || before.Ref == after.Ref ||
		draft.Attributes["snapshot_id"] != before.Ref || after.Attributes["previous_snapshot_id"] != before.Ref ||
		before.Attributes["page_id"] != after.Attributes["page_id"] || before.Attributes["page_id"] != draft.Attributes["page_id"] ||
		before.Attributes["session_generation"] == "" || before.Attributes["session_generation"] != after.Attributes["session_generation"] ||
		before.Attributes["digest"] == "" || before.Attributes["digest"] == after.Attributes["digest"] {
		return false
	}
	beforePage, beforeErr := strconv.ParseUint(before.Attributes["page_generation"], 10, 64)
	afterPage, afterErr := strconv.ParseUint(after.Attributes["page_generation"], 10, 64)
	return beforeErr == nil && afterErr == nil && beforePage > 0 && afterPage > beforePage
}
