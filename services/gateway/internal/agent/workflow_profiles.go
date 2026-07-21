package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type browserInternetSearchProfile struct{}

func (browserInternetSearchProfile) ID() app.WorkflowID { return app.WorkflowBrowserInternetSearch }
func (browserInternetSearchProfile) Revision() int      { return 1 }
func (browserInternetSearchProfile) Capability() app.CapabilityID {
	return app.CapabilityBrowserInternetSearch
}
func (browserInternetSearchProfile) Recognize(input workflowRecognitionContext) (workflowRecognition, bool) {
	content, ok := browserInternetSearchQuery(input.Content)
	if !ok {
		return workflowRecognition{}, false
	}
	return workflowRecognition{
		Slots: app.RouteSlots{Operation: app.RouteOperationSearch, FactScope: app.RouteFactScopeCurrentInternet, Query: content}, Confidence: 0.9,
		Reason: "The request asks for an Internet search result.",
	}, true
}

func browserInternetSearchQuery(content string) (string, bool) {
	content = semanticRoutingContent(content)
	lower := strings.ToLower(content)
	if strings.TrimSpace(content) == "" || len(extractURLs(content)) != 0 || shouldUseBrowserAutomation(lower) ||
		containsAny(lower, "current page", "current tab", "当前页面", "当前标签", "chrome") || ordinaryWeatherRequest(content) || (!internetSearchIntent(lower) && !weatherResearchRequest(lower)) {
		return "", false
	}
	return content, true
}

func internetSearchIntent(lower string) bool {
	return containsEnglishSemanticTerm(lower, "web", "internet", "online", "news", "latest", "today", "current") ||
		containsAny(lower, "联网", "网上", "互联网", "新闻", "最新", "今天", "今日", "查一下", "查询一下")
}
func (p browserInternetSearchProfile) Resolve(route app.RouteDecision, sourceTurnID string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	intent := singleObjectiveIntent(sourceTurnID, app.IntentDomainWeb, app.IntentOperationSearch, app.TargetRef{Kind: app.TargetKindNone}, app.DataScopePublic)
	nodeID := app.WorkflowNodeID("search_info")
	return intent, app.WorkflowPlan{
		SchemaVersion: 1, ProfileID: p.ID(), ProfileRevision: p.Revision(),
		InitialNodeIDs: []app.WorkflowNodeID{nodeID}, Completion: app.CompletionEvidence,
		Nodes: []app.WorkflowNode{{
			ID: nodeID, InitialStage: "search_info",
			Goal: app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "Return the bounded Info provider search result", Completion: app.CompletionEvidence},
			InitialScope: app.CapabilityScope{Requirements: []app.CapabilityRequirement{{
				Name: app.ToolCapabilityWebDiscovery, Qualifiers: map[string]string{app.CapabilityQualifierProvider: app.CapabilityProviderInfo},
			}}},
			ArgumentBindings: []app.ArgumentBinding{{
				Capability: app.ToolCapabilityWebDiscovery, Argument: "query", ResourceKind: "query",
				Source: app.ArgumentBindingRouteSlot, SourceKey: "query",
			}},
			AllowedRisks: []app.RiskLevel{app.RiskRead}, MaxAttempts: 1,
		}},
	}, nil
}
func (browserInternetSearchProfile) Prepare(*app.WorkflowState) (app.TransitionID, bool, error) {
	return "", false, nil
}
func (browserInternetSearchProfile) Assess(_ *app.WorkflowState, outcome app.ToolOutcome) app.NodeAssessment {
	assessment := baseNodeAssessment(outcome)
	if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalResultsAvailable) || containsOutcomeSignal(outcome.Signals, app.OutcomeSignalNoResults) {
		assessment.Status, assessment.ReasonCode = app.AssessmentComplete, "info_search_returned"
	} else {
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "info_search_failed"
	}
	return assessment
}
func (browserInternetSearchProfile) Hint(state *app.WorkflowState) workflowExecutionHint {
	return workflowHint(state, "search", "web", "public", "", "Dispatched by the browser.internet_search workflow contract.")
}
func (browserInternetSearchProfile) TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string {
	return ""
}

type browserAutomationProfile struct{}

func (browserAutomationProfile) ID() app.WorkflowID           { return app.WorkflowBrowserAutomation }
func (browserAutomationProfile) Revision() int                { return 1 }
func (browserAutomationProfile) Capability() app.CapabilityID { return app.CapabilityBrowserAutomation }
func (browserAutomationProfile) Recognize(input workflowRecognitionContext) (workflowRecognition, bool) {
	content := semanticRoutingContent(input.Content)
	lower := strings.ToLower(content)
	urls := extractURLs(content)
	wantsOpen := containsEnglishSemanticTerm(lower, "open", "visit", "launch", "focus") || containsAny(lower, "打开", "访问", "聚焦", "切换到")
	unsupportedInteraction := containsEnglishSemanticTerm(lower, "click", "type", "select", "check", "checkbox", "login", "sign in", "authenticate", "authentication", "interact", "screenshot", "inspect") ||
		containsAny(lower, "点击", "输入", "选择", "勾选", "登录", "认证", "交互", "截图", "页面结构")
	if !wantsOpen || unsupportedInteraction {
		return workflowRecognition{}, false
	}
	if len(urls) > 1 {
		return workflowRecognition{Status: app.RouteClarify, Confidence: 0.7, Reason: "Browser automation revision 1 requires one explicit target URL."}, true
	}
	target := ""
	facts := map[string]string{}
	reason := "The request asks to open or focus one explicit browser URL."
	if len(urls) == 1 {
		target = normalizeBrowserURL(urls[0])
	} else if destination, ok := matchRegisteredBrowserDestination(content); ok {
		if registeredBrowserDestinationHasInteractionGoal(content, destination) {
			return workflowRecognition{}, false
		}
		target = normalizeBrowserURL(destination.Destination.URL)
		facts["browser_destination"] = destination.Destination.ID
		reason = "The request names a registered browser destination whose URL is frozen by the runtime."
	} else {
		return workflowRecognition{Status: app.RouteClarify, Confidence: 0.7, Reason: "Browser automation revision 1 requires one explicit or registered target URL."}, true
	}
	facts["url"] = target
	return workflowRecognition{
		Slots: app.RouteSlots{Operation: app.RouteOperationOpen, Query: content, TargetKind: "url", TargetRef: target},
		Facts: facts, Confidence: 0.95,
		Reason: reason,
	}, true
}
func (p browserAutomationProfile) Resolve(route app.RouteDecision, sourceTurnID string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	target := app.TargetRef{Kind: app.TargetKindExplicitURL, Ref: route.Slots.TargetRef}
	intent := singleObjectiveIntent(sourceTurnID, app.IntentDomainWeb, app.IntentOperationAutomate, target, app.DataScopePublic)
	nodeID := app.WorkflowNodeID("browser_automation")
	focusScope := app.CapabilityScope{Requirements: []app.CapabilityRequirement{{Name: app.ToolCapabilityBrowserFocus}}}
	openScope := app.CapabilityScope{Requirements: []app.CapabilityRequirement{{Name: app.ToolCapabilityBrowserOpen}}}
	return intent, app.WorkflowPlan{
		SchemaVersion: 1, ProfileID: p.ID(), ProfileRevision: p.Revision(),
		InitialNodeIDs: []app.WorkflowNodeID{nodeID}, Completion: app.CompletionEvidence,
		Nodes: []app.WorkflowNode{{
			ID: nodeID, InitialStage: "scan_tabs",
			Goal:         app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "Focus an existing exact URL or open the frozen target URL", Completion: app.CompletionEvidence},
			InitialScope: app.CapabilityScope{Requirements: []app.CapabilityRequirement{{Name: app.ToolCapabilityBrowserListTabs}}},
			Transitions: []app.ScopeTransition{
				{ID: "focus_existing", NextStage: "focus_existing", On: app.TransitionPredicate{OutcomeSignals: []app.OutcomeSignal{app.OutcomeSignalTargetTabExists}, Assessments: []app.AssessmentStatus{app.AssessmentNeedsMoreEvidence}}, Replace: &focusScope, MaxActivations: 1},
				{ID: "open_new", NextStage: "open_new", On: app.TransitionPredicate{OutcomeSignals: []app.OutcomeSignal{app.OutcomeSignalTargetTabMissing}, Assessments: []app.AssessmentStatus{app.AssessmentNeedsMoreEvidence}}, Replace: &openScope, MaxActivations: 1},
			},
			ArgumentBindings: []app.ArgumentBinding{
				{Capability: app.ToolCapabilityBrowserFocus, Argument: "page_id", ResourceKind: "browser_tab", Source: app.ArgumentBindingOutcomeRef},
				{Capability: app.ToolCapabilityBrowserOpen, Argument: "url", ResourceKind: "url", Source: app.ArgumentBindingIntentTarget, TargetKinds: []app.TargetKind{app.TargetKindExplicitURL}},
			},
			AllowedRisks: []app.RiskLevel{app.RiskRead}, MaxAttempts: 2,
		}},
	}, nil
}
func (browserAutomationProfile) Prepare(*app.WorkflowState) (app.TransitionID, bool, error) {
	return "", false, nil
}
func (browserAutomationProfile) Assess(state *app.WorkflowState, outcome app.ToolOutcome) app.NodeAssessment {
	assessment := baseNodeAssessment(outcome)
	switch {
	case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalTabsScanned):
		target := normalizeBrowserURL(state.Route.Slots.TargetRef)
		for _, ref := range outcome.Refs {
			if ref.Kind == "browser_tab" && normalizeBrowserURL(ref.Attributes["url"]) == target {
				assessment.Status, assessment.ReasonCode = app.AssessmentNeedsMoreEvidence, "target_tab_exists"
				assessment.Signals = []app.OutcomeSignal{app.OutcomeSignalTargetTabExists}
				assessment.SelectedRefs = []app.ResourceRef{ref}
				return assessment
			}
		}
		assessment.Status, assessment.ReasonCode = app.AssessmentNeedsMoreEvidence, "target_tab_missing"
		assessment.Signals = []app.OutcomeSignal{app.OutcomeSignalTargetTabMissing}
	case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalFocusCompleted):
		assessment.Status, assessment.ReasonCode = app.AssessmentComplete, "browser_focus_completed"
	case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalOpenCompleted):
		assessment.Status, assessment.ReasonCode = app.AssessmentComplete, "browser_open_completed"
	default:
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "browser_automation_failed"
	}
	return assessment
}
func (browserAutomationProfile) Hint(state *app.WorkflowState) workflowExecutionHint {
	return workflowHint(state, "browse", "web", "public", "collaborative", "Dispatched by the browser.automation workflow contract.")
}
func (browserAutomationProfile) TransitionInstruction(_ app.ToolOutcome, assessment app.NodeAssessment) string {
	instruction := "workflow_stage: " + assessment.ReasonCode
	if len(assessment.SelectedRefs) == 1 && assessment.SelectedRefs[0].Kind == "browser_tab" {
		instruction += " page_id=" + assessment.SelectedRefs[0].Ref
	}
	return instruction
}

type documentReadProfile struct{}

func (documentReadProfile) ID() app.WorkflowID           { return app.WorkflowDocumentRead }
func (documentReadProfile) Revision() int                { return 1 }
func (documentReadProfile) Capability() app.CapabilityID { return app.CapabilityDocumentRead }
func (documentReadProfile) Recognize(input workflowRecognitionContext) (workflowRecognition, bool) {
	return recognizeDocumentRoute(input, false)
}
func (p documentReadProfile) Resolve(route app.RouteDecision, sourceTurnID string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	target := app.TargetRef{Kind: app.TargetKindWorkspacePath, Ref: route.Slots.TargetRef}
	intent := singleObjectiveIntent(sourceTurnID, app.IntentDomainWorkspace, app.IntentOperationRead, target, app.DataScopeWorkspace)
	nodeID := app.WorkflowNodeID("document_read")
	readScope := app.CapabilityScope{Requirements: []app.CapabilityRequirement{{
		Name: app.ToolCapabilityDocumentRead, Qualifiers: map[string]string{app.CapabilityQualifierFormat: route.Facts["document_format"]},
	}}}
	return intent, app.WorkflowPlan{
		SchemaVersion: 1, ProfileID: p.ID(), ProfileRevision: p.Revision(),
		InitialNodeIDs: []app.WorkflowNodeID{nodeID}, Completion: app.CompletionEvidence,
		Nodes: []app.WorkflowNode{{
			ID: nodeID, InitialStage: "inspect_type",
			Goal:             app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "Read the exact governed path with its detected format", Completion: app.CompletionEvidence},
			InitialScope:     app.CapabilityScope{},
			Transitions:      []app.ScopeTransition{{ID: "document_type_resolved", Deterministic: true, NextStage: "read_by_type", Replace: &readScope, MaxActivations: 1}},
			ArgumentBindings: []app.ArgumentBinding{{Capability: app.ToolCapabilityDocumentRead, Argument: "path", ResourceKind: "path", Source: app.ArgumentBindingIntentTarget, TargetKinds: []app.TargetKind{app.TargetKindWorkspacePath}}},
			AllowedRisks:     []app.RiskLevel{app.RiskRead}, MaxAttempts: 1,
		}},
	}, nil
}
func (documentReadProfile) Prepare(*app.WorkflowState) (app.TransitionID, bool, error) {
	return "document_type_resolved", true, nil
}
func (documentReadProfile) Assess(_ *app.WorkflowState, outcome app.ToolOutcome) app.NodeAssessment {
	assessment := baseNodeAssessment(outcome)
	if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalContentAvailable) {
		assessment.Status, assessment.ReasonCode = app.AssessmentComplete, "document_content_available"
	} else {
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "document_read_failed"
	}
	return assessment
}
func (documentReadProfile) Hint(state *app.WorkflowState) workflowExecutionHint {
	return workflowHint(state, "inspect", "workspace", "workspace", "", "Dispatched by the document.read workflow contract.")
}
func (documentReadProfile) TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string {
	return ""
}

type documentEditProfile struct{}

func (documentEditProfile) ID() app.WorkflowID           { return app.WorkflowDocumentEdit }
func (documentEditProfile) Revision() int                { return 1 }
func (documentEditProfile) Capability() app.CapabilityID { return app.CapabilityDocumentEdit }
func (documentEditProfile) Recognize(input workflowRecognitionContext) (workflowRecognition, bool) {
	return recognizeDocumentRoute(input, true)
}
func (p documentEditProfile) Resolve(route app.RouteDecision, sourceTurnID string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	target := app.TargetRef{Kind: app.TargetKindWorkspacePath, Ref: route.Slots.TargetRef}
	intent := singleObjectiveIntent(sourceTurnID, app.IntentDomainWorkspace, app.IntentOperationProcess, target, app.DataScopeWorkspace)
	nodeID := app.WorkflowNodeID("document_edit")
	editScope := app.CapabilityScope{Requirements: []app.CapabilityRequirement{{
		Name: app.ToolCapabilityDocumentEdit, Qualifiers: map[string]string{
			app.CapabilityQualifierFormat: route.Facts["document_format"], app.CapabilityQualifierOperation: route.Facts["document_operation"],
		},
	}}}
	return intent, app.WorkflowPlan{
		SchemaVersion: 1, ProfileID: p.ID(), ProfileRevision: p.Revision(),
		InitialNodeIDs: []app.WorkflowNodeID{nodeID}, Completion: app.CompletionEvidence,
		Nodes: []app.WorkflowNode{{
			ID: nodeID, InitialStage: "inspect_type",
			Goal:         app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "Edit a governed output copy with the detected format and requested operation", Completion: app.CompletionEvidence},
			InitialScope: app.CapabilityScope{},
			Transitions:  []app.ScopeTransition{{ID: "document_type_resolved", Deterministic: true, NextStage: "edit_by_type", Replace: &editScope, MaxActivations: 1}},
			ArgumentBindings: []app.ArgumentBinding{
				{Capability: app.ToolCapabilityDocumentEdit, Argument: "path", ResourceKind: "path", Source: app.ArgumentBindingIntentTarget, TargetKinds: []app.TargetKind{app.TargetKindWorkspacePath}},
				{Capability: app.ToolCapabilityDocumentEdit, Argument: "output_path", ResourceKind: "path", Source: app.ArgumentBindingRouteFact, SourceKey: "output_path"},
			},
			AllowedRisks: []app.RiskLevel{app.RiskReversible}, MaxAttempts: 1,
		}},
	}, nil
}
func (documentEditProfile) Prepare(*app.WorkflowState) (app.TransitionID, bool, error) {
	return "document_type_resolved", true, nil
}
func (documentEditProfile) Assess(_ *app.WorkflowState, outcome app.ToolOutcome) app.NodeAssessment {
	assessment := baseNodeAssessment(outcome)
	if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalEditCompleted) {
		assessment.Status, assessment.ReasonCode = app.AssessmentComplete, "document_edit_completed"
	} else {
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "document_edit_failed"
	}
	return assessment
}
func (documentEditProfile) Hint(state *app.WorkflowState) workflowExecutionHint {
	return workflowHint(state, "modify", "workspace", "workspace", "", "Dispatched by the document.edit workflow contract.")
}
func (documentEditProfile) TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string {
	return ""
}

func terminalGenericAssessment(outcome app.ToolOutcome, completedReason, failedReason string) app.NodeAssessment {
	assessment := baseNodeAssessment(outcome)
	if outcome.Status == "completed" || outcome.Status == "completed_after_approval" {
		assessment.Status, assessment.ReasonCode = app.AssessmentComplete, completedReason
	} else {
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, failedReason
	}
	return assessment
}

func singleObjectiveIntent(sourceTurnID string, domain app.IntentDomain, operation app.IntentOperation, target app.TargetRef, scope app.DataScope) app.IntentEnvelope {
	return app.IntentEnvelope{
		Version: 1, SourceTurnID: sourceTurnID,
		Objectives:  []app.Objective{{ID: "objective_1", Domain: domain, Operation: operation, Target: target, Output: app.OutputKindText, Explicit: true}},
		Constraints: app.IntentConstraints{DataScope: scope, EvidenceDepth: app.EvidenceDepthSummary},
		Resolution:  app.IntentResolution{Status: app.IntentResolved},
	}
}

func baseNodeAssessment(outcome app.ToolOutcome) app.NodeAssessment {
	return app.NodeAssessment{OutcomeID: outcome.ID, NodeID: outcome.NodeID}
}

func workspaceMutationRequested(lower string) bool {
	return containsEnglishSemanticTerm(lower, "edit", "modify", "write", "create", "delete", "remove", "patch", "rename", "move") ||
		containsAny(lower, "编辑", "修改", "写入", "创建", "删除", "移除", "补丁", "重命名", "移动", "完善")
}

func newWorkflowState(route app.RouteDecision, returnRoute app.ReturnRoute, intent app.IntentEnvelope, plan app.WorkflowPlan) *app.WorkflowState {
	nodes := make(map[app.WorkflowNodeID]app.WorkflowNodeState, len(plan.Nodes))
	for _, node := range plan.Nodes {
		status := app.WorkflowNodePending
		if containsWorkflowNodeID(plan.InitialNodeIDs, node.ID) {
			status = app.WorkflowNodeActive
		}
		nodes[node.ID] = app.WorkflowNodeState{Status: status, Stage: node.InitialStage, CurrentScope: node.InitialScope, ScopeRevision: 1, TransitionActivations: map[app.TransitionID]int{}}
	}
	return &app.WorkflowState{
		SchemaVersion: 1, Route: route, ReturnRoute: returnRoute, Intent: intent, Plan: plan, PlanDigest: workflowPlanDigest(plan), Status: app.WorkflowStatusRunning,
		ActiveNodeIDs: append([]app.WorkflowNodeID(nil), plan.InitialNodeIDs...), Nodes: nodes,
	}
}

func prepareWorkflowState(profile workflowProfile, state *app.WorkflowState) error {
	transitionID, prepare, err := profile.Prepare(state)
	if err != nil || !prepare {
		return err
	}
	if len(state.ActiveNodeIDs) != 1 {
		return errors.New("deterministic workflow preparation requires one active node")
	}
	nodeID := state.ActiveNodeIDs[0]
	node, ok := workflowPlanNode(state.Plan, nodeID)
	if !ok {
		return errors.New("deterministic workflow node is missing from the frozen plan")
	}
	nodeState := state.Nodes[nodeID]
	for _, transition := range node.Transitions {
		if transition.ID != transitionID || !transition.Deterministic {
			continue
		}
		if nodeState.TransitionActivations[transition.ID] >= transition.MaxActivations {
			return errors.New("deterministic workflow transition is exhausted")
		}
		if transition.Replace != nil {
			nodeState.CurrentScope = *transition.Replace
		} else {
			nodeState.CurrentScope.Requirements = appendUniqueRequirements(nodeState.CurrentScope.Requirements, transition.Add...)
		}
		nodeState.TransitionActivations[transition.ID]++
		nodeState.ScopeRevision++
		nodeState.Stage = transition.NextStage
		nodeState.LastDirectory = nil
		nodeState.SelectedEntries = nil
		state.Nodes[nodeID] = nodeState
		return nil
	}
	return errors.New("profile requested an undeclared deterministic workflow transition")
}

func workflowPlanDigest(plan app.WorkflowPlan) string {
	payload, _ := json.Marshal(plan)
	sum := sha256.Sum256(payload)
	return "plan_" + hex.EncodeToString(sum[:])
}

func workflowHint(state *app.WorkflowState, taskType, evidenceNeed, dataScope, browserMode, reason string) workflowExecutionHint {
	nodeID := state.ActiveNodeIDs[0]
	nodeState := state.Nodes[nodeID]
	hint := workflowExecutionHint{
		TaskType: taskType, EvidenceNeed: evidenceNeed, DataScope: dataScope, ToolMode: "workflow_bounded", BrowserMode: browserMode,
		RequiresToolEvidence: true, EstimatedRisk: app.RiskRead, ModelLaneHint: workflowExecutionModelLane, Reason: reason,
		WorkflowID: state.Plan.ProfileID, WorkflowNodeID: nodeID, ScopeRevision: nodeState.ScopeRevision,
	}
	if len(nodeState.CurrentScope.Requirements) > 0 {
		hint.Capability = nodeState.CurrentScope.Requirements[0].Name
	}
	return hint
}

func (r Runtime) materializeActiveWorkflowTools(ctx context.Context, run app.AgentRun, actorRef string, hint *workflowExecutionHint) ([]app.ToolDefinition, error) {
	state := run.Workflow.Nodes[hint.WorkflowNodeID]
	view, err := r.exposure.Search(ctx, app.ExposureRequest{
		RunID: run.ID, WorkflowID: run.Workflow.Plan.ProfileID, NodeID: hint.WorkflowNodeID,
		ScopeRevision: state.ScopeRevision, ActorRef: actorRef, Limit: 32,
	})
	if err != nil {
		return nil, err
	}
	r.auditDirectorySearch(run, view)
	if len(view.Entries) == 0 {
		return nil, errors.New("no registered tool satisfies the active workflow scope")
	}
	if len(view.Entries) != 1 && !state.CurrentScope.MaterializeAll {
		return nil, errors.New("active workflow scope requires bounded directory selection")
	}
	entryIDs := make([]app.ToolDirectoryEntryID, 0, len(view.Entries))
	for _, entry := range view.Entries {
		entryIDs = append(entryIDs, entry.ID)
	}
	exposure, err := r.exposure.Materialize(ctx, app.MaterializeRequest{
		ViewID: view.ViewID, RunID: run.ID, WorkflowID: view.WorkflowID, NodeID: view.NodeID,
		ScopeRevision: view.ScopeRevision, EntryIDs: entryIDs, ActorRef: actorRef,
	})
	if err != nil {
		return nil, err
	}
	hint.ScopeRevision = view.ScopeRevision
	r.auditFixedWorkflowExposure(run, view, exposure.Definitions)
	return exposure.Definitions, nil
}

func (r Runtime) auditDirectorySearch(run app.AgentRun, view app.DirectoryView) {
	r.store.AddAudit(app.AuditEvent{SessionID: run.SessionID, RunID: run.ID, Actor: "runtime", Type: "tools.directory.searched", Summary: "Searched the active workflow capability scope", Fields: map[string]any{
		"workflow_id": view.WorkflowID, "node_id": view.NodeID, "scope_revision": view.ScopeRevision,
		"directory_revision": view.DirectoryRevision, "view_id": view.ViewID, "entry_ids": directoryEntryIDs(view.Entries),
	}})
}

func (r Runtime) auditFixedWorkflowExposure(run app.AgentRun, view app.DirectoryView, definitions []app.ToolDefinition) {
	r.store.AddAudit(app.AuditEvent{SessionID: run.SessionID, RunID: run.ID, Actor: "runtime", Type: "tools.exposure.fixed", Summary: "Materialized the workflow's fixed tool boundary", Fields: map[string]any{
		"workflow_id": view.WorkflowID, "view_id": view.ViewID, "entry_ids": directoryEntryIDs(view.Entries), "tools": visibleToolNames(definitions),
	}})
}

func (r Runtime) workflowActorRef(sessionID string) string {
	if session, ok := r.store.GetSession(sessionID); ok && session.OwnerID != "" {
		return session.OwnerID
	}
	return "owner"
}
