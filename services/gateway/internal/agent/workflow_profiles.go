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
func (browserInternetSearchProfile) Revision() int      { return 2 }
func (browserInternetSearchProfile) Capability() app.CapabilityID {
	return app.CapabilityBrowserInternetSearch
}
func (browserInternetSearchProfile) RoutingSemantics() workflowRoutingSemantics {
	return workflowRoutingSemantics{Variants: []workflowRoutingVariant{{
		Key:   "search",
		Route: workflowRouteTemplate{Operation: app.RouteOperationSearch, FactScope: app.RouteFactScopeCurrentInternet},
		EmbedTexts: []string{
			"查一下今天的 AI 新闻", "What is the current gold price?", "现在美元兑人民币汇率是多少", "刚结束的比赛比分",
			"苹果官网目前在售的产品和价格", "联网查询最新信息", "比较北京和上海今天的天气", "查询天气预警",
		},
		TreeDescription: "Retrieve read-only facts whose answer depends on current Internet state, including news, prices, availability, sports, alerts, official current catalogs, or multi-source and multi-location comparisons.",
		HardNegatives: []string{
			"解释什么是黄金", "上海今天的天气卡片", "打开苹果官网", "点击网页上的下一步", "一分钟后查一下新闻",
		},
	}}}
}
func (browserInternetSearchProfile) Finalization() workflowFinalizationMode {
	return workflowFinalizationGrounded
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
func (browserInternetSearchProfile) Prepare(*app.WorkflowState) (workflowPreparation, error) {
	return workflowPreparation{}, nil
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
func (browserInternetSearchProfile) StageContext(state *app.WorkflowState) workflowStageContext {
	return workflowStageContextForState(state, "search", "web", "public", "", "Dispatched by the browser.internet_search workflow contract.")
}
func (browserInternetSearchProfile) TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string {
	return ""
}

type documentReadProfile struct{}

func (documentReadProfile) ID() app.WorkflowID           { return app.WorkflowDocumentRead }
func (documentReadProfile) Revision() int                { return 4 }
func (documentReadProfile) Capability() app.CapabilityID { return app.CapabilityDocumentRead }
func (documentReadProfile) RoutingSemantics() workflowRoutingSemantics {
	return documentReadRoutingSemantics()
}
func (documentReadProfile) Finalization() workflowFinalizationMode {
	return workflowFinalizationModel
}
func (p documentReadProfile) Resolve(route app.RouteDecision, sourceTurnID string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	target := app.TargetRef{Kind: app.TargetKindWorkspacePath, Ref: route.Slots.TargetRef}
	intent := singleObjectiveIntent(sourceTurnID, app.IntentDomainWorkspace, app.IntentOperationRead, target, app.DataScopeWorkspace)
	confirmNodeID := app.WorkflowNodeID("confirm_document_target")
	readNodeID := app.WorkflowNodeID("document_read")
	readScope := app.CapabilityScope{Requirements: []app.CapabilityRequirement{{
		Name: app.ToolCapabilityDocumentRead, Qualifiers: map[string]string{app.CapabilityQualifierFormat: route.Facts["document_format"]},
	}}}
	return intent, app.WorkflowPlan{
		SchemaVersion: 1, ProfileID: p.ID(), ProfileRevision: p.Revision(),
		InitialNodeIDs: []app.WorkflowNodeID{confirmNodeID}, Completion: app.CompletionEvidence,
		Nodes: []app.WorkflowNode{
			{
				ID: confirmNodeID, InitialStage: "confirm_document_target",
				Goal:         app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "Persist the exact governed document identity and frozen path", Completion: app.CompletionDeterministic},
				InitialScope: app.CapabilityScope{},
				AllowedRisks: []app.RiskLevel{app.RiskRead}, MaxAttempts: 1,
			},
			{
				ID: readNodeID, InitialStage: "read_by_type", DependsOn: []app.WorkflowNodeID{confirmNodeID},
				Goal:             app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "Read the exact governed path with its detected format", Completion: app.CompletionEvidence},
				InitialScope:     readScope,
				ArgumentBindings: []app.ArgumentBinding{{Capability: app.ToolCapabilityDocumentRead, Argument: "path", ResourceKind: "path", Source: app.ArgumentBindingRouteSlot, SourceKey: "target_ref"}},
				AllowedRisks:     []app.RiskLevel{app.RiskRead}, MaxAttempts: 1, InvocationMode: app.WorkflowInvocationDirectOnce,
			},
		},
	}, nil
}
func (documentReadProfile) Prepare(state *app.WorkflowState) (workflowPreparation, error) {
	return documentTargetPreparation(state)
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
func (documentReadProfile) StageContext(state *app.WorkflowState) workflowStageContext {
	return workflowStageContextForState(state, "inspect", "workspace", "workspace", "", "Dispatched by the document.read workflow contract.")
}
func (documentReadProfile) TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string {
	return ""
}

type documentEditProfile struct{}

func (documentEditProfile) ID() app.WorkflowID           { return app.WorkflowDocumentEdit }
func (documentEditProfile) Revision() int                { return 7 }
func (documentEditProfile) Capability() app.CapabilityID { return app.CapabilityDocumentEdit }
func (documentEditProfile) RoutingSemantics() workflowRoutingSemantics {
	return documentEditRoutingSemantics()
}
func (documentEditProfile) Finalization() workflowFinalizationMode {
	return workflowFinalizationGrounded
}
func (p documentEditProfile) Resolve(route app.RouteDecision, sourceTurnID string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	target := app.TargetRef{Kind: app.TargetKindWorkspacePath, Ref: route.Slots.TargetRef}
	intent := singleObjectiveIntent(sourceTurnID, app.IntentDomainWorkspace, app.IntentOperationProcess, target, app.DataScopeWorkspace)
	confirmNodeID := app.WorkflowNodeID("confirm_document_target")
	locateNodeID := app.WorkflowNodeID("document_locate_evidence")
	decisionNodeID := app.WorkflowNodeID("select_edit_operation")
	editNodeID := app.WorkflowNodeID("document_edit")
	readScope := app.CapabilityScope{Requirements: []app.CapabilityRequirement{{
		Name: app.ToolCapabilityDocumentRead, Qualifiers: map[string]string{app.CapabilityQualifierFormat: route.Facts["document_format"]},
	}}}
	editScope := app.CapabilityScope{Requirements: []app.CapabilityRequirement{{
		Name: app.ToolCapabilityDocumentEdit, Qualifiers: map[string]string{app.CapabilityQualifierFormat: route.Facts["document_format"]},
	}}}
	return intent, app.WorkflowPlan{
		SchemaVersion: 1, ProfileID: p.ID(), ProfileRevision: p.Revision(),
		InitialNodeIDs: []app.WorkflowNodeID{confirmNodeID}, Completion: app.CompletionEvidence, ResultProjection: app.WorkflowResultOutputsOnly,
		Nodes: []app.WorkflowNode{
			{
				ID: confirmNodeID, InitialStage: "confirm_document_target",
				Goal:         app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "Persist the exact governed document identity and frozen path", Completion: app.CompletionDeterministic},
				InitialScope: app.CapabilityScope{},
				AllowedRisks: []app.RiskLevel{app.RiskRead}, MaxAttempts: 1,
			},
			{
				ID: locateNodeID, InitialStage: "read_for_edit", DependsOn: []app.WorkflowNodeID{confirmNodeID},
				Goal:         app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "Read the governed document and locate structured evidence for the requested change", Completion: app.CompletionEvidence},
				InitialScope: readScope,
				ArgumentBindings: []app.ArgumentBinding{{
					Capability: app.ToolCapabilityDocumentRead, Argument: "path", ResourceKind: "path", Source: app.ArgumentBindingRouteSlot, SourceKey: "target_ref",
				}},
				AllowedRisks: []app.RiskLevel{app.RiskRead}, MaxAttempts: 1, InvocationMode: app.WorkflowInvocationDirectOnce,
			},
			{
				ID: decisionNodeID, InitialStage: "select_edit_operation", DependsOn: []app.WorkflowNodeID{locateNodeID},
				Goal:         app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "Choose exactly one registered editor operation from the located document evidence", Completion: app.CompletionDecision},
				InitialScope: editScope,
				AllowedRisks: []app.RiskLevel{app.RiskReversible}, MaxAttempts: 2,
			},
			{
				ID: editNodeID, InitialStage: "edit_by_type", DependsOn: []app.WorkflowNodeID{decisionNodeID},
				Goal:         app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "Apply the selected bounded edit to a governed output copy", Completion: app.CompletionEvidence},
				InitialScope: editScope,
				ArgumentBindings: []app.ArgumentBinding{
					{Capability: app.ToolCapabilityDocumentEdit, Argument: "path", ResourceKind: "path", Source: app.ArgumentBindingIntentTarget, TargetKinds: []app.TargetKind{app.TargetKindWorkspacePath}},
					{Capability: app.ToolCapabilityDocumentEdit, Argument: "output_path", ResourceKind: "path", Source: app.ArgumentBindingRouteFact, SourceKey: "output_path"},
				},
				AllowedRisks: []app.RiskLevel{app.RiskReversible}, MaxAttempts: 2,
			}},
	}, nil
}
func (documentEditProfile) Prepare(state *app.WorkflowState) (workflowPreparation, error) {
	return documentTargetPreparation(state)
}
func (documentEditProfile) Assess(state *app.WorkflowState, outcome app.ToolOutcome) app.NodeAssessment {
	assessment := baseNodeAssessment(outcome)
	if outcome.NodeID == "document_locate_evidence" && containsOutcomeSignal(outcome.Signals, app.OutcomeSignalContentAvailable) {
		assessment.Status, assessment.ReasonCode = app.AssessmentComplete, "document_evidence_located"
	} else if outcome.NodeID == "document_edit" && containsOutcomeSignal(outcome.Signals, app.OutcomeSignalEditCompleted) {
		assessment.Status, assessment.ReasonCode = app.AssessmentComplete, "document_edit_completed"
	} else {
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "document_edit_failed"
	}
	return assessment
}
func (documentEditProfile) StageContext(state *app.WorkflowState) workflowStageContext {
	operation := "modify"
	if len(state.ActiveNodeIDs) > 0 && state.ActiveNodeIDs[0] == "document_locate_evidence" {
		operation = "inspect"
	}
	stageContext := workflowStageContextForState(state, operation, "workspace", "workspace", "", "Dispatched by the staged document.edit workflow contract.")
	if len(state.ActiveNodeIDs) > 0 && state.ActiveNodeIDs[0] == "document_edit" {
		stageContext.EvidenceRequirements = []workflowEvidenceRequirement{{
			SourceNodeID: "document_locate_evidence", Mode: workflowEvidenceStructured,
		}}
	}
	return stageContext
}
func (documentEditProfile) TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string {
	return ""
}
func (documentEditProfile) DecisionRules(app.WorkflowNode) []string {
	return registeredAgentDocumentFormatPolicies().decisionRules()
}
func (documentEditProfile) DecisionResolvedInstruction(entry app.ToolDirectoryEntry) string {
	operation := strings.TrimSpace(entry.Capability.Qualifiers[app.CapabilityQualifierOperation])
	return "workflow_stage: edit_operation_selected operation=" + operation + ". Use the complete structured_document_v1 observation to edit only the requested stable block, paragraph, cell, row, slide, or pages. Call the single materialized editor with the frozen input/output paths; its reversible action must enter Policy approval rather than asking for conversational confirmation."
}

func documentTargetPreparation(state *app.WorkflowState) (workflowPreparation, error) {
	if state == nil {
		return workflowPreparation{}, errors.New("document target confirmation requires workflow state")
	}
	documentID := strings.TrimSpace(state.Route.Facts["document_id"])
	path := strings.TrimSpace(state.Route.Slots.TargetRef)
	format := strings.TrimSpace(state.Route.Facts["document_format"])
	if documentID == "" || path == "" || format == "" {
		return workflowPreparation{}, errors.New("document target confirmation requires a durable document ID, frozen path, and format")
	}
	sourceID := firstNonEmptyString(
		strings.TrimSpace(state.Route.Facts["document_source_id"]),
		documentID,
	)
	return workflowPreparation{
		CompleteNode: true,
		OutcomeRefs: []app.ResourceRef{{
			Kind:       "document",
			Ref:        documentID,
			Provenance: sourceID,
			Attributes: map[string]string{
				"path":       path,
				"format":     format,
				"source":     strings.TrimSpace(state.Route.Facts["document_source"]),
				"source_id":  sourceID,
				"activity":   strings.TrimSpace(state.Route.Facts["document_activity"]),
				"provenance": strings.TrimSpace(state.Route.Facts["target_provenance"]),
			},
		}},
	}, nil
}

func terminalGenericAssessment(outcome app.ToolOutcome, completedReason, failedReason string) app.NodeAssessment {
	assessment := baseNodeAssessment(outcome)
	if outcome.Status.Completed() {
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
	preparation, err := profile.Prepare(state)
	if err != nil {
		return err
	}
	if preparation.CompleteNode {
		if preparation.TransitionID != "" {
			return errors.New("deterministic workflow preparation cannot complete a node and apply a transition together")
		}
		if len(state.ActiveNodeIDs) != 1 {
			return errors.New("deterministic workflow completion requires one active node")
		}
		nodeID := state.ActiveNodeIDs[0]
		node, ok := workflowPlanNode(state.Plan, nodeID)
		if !ok || node.Goal.Completion != app.CompletionDeterministic {
			return errors.New("deterministic workflow completion requires a declared deterministic node")
		}
		if len(preparation.OutcomeRefs) == 0 {
			return errors.New("deterministic workflow completion requires persisted outcome references")
		}
		nodeState := state.Nodes[nodeID]
		nodeState.Status = app.WorkflowNodeSucceeded
		nodeState.Attempts = 1
		nodeState.OutcomeRefs = appendUniqueResourceRefs(nodeState.OutcomeRefs, preparation.OutcomeRefs...)
		state.Nodes[nodeID] = nodeState
		state.ActiveNodeIDs = removeWorkflowNodeID(state.ActiveNodeIDs, nodeID)
		activateReadyWorkflowNodes(state)
		if allWorkflowNodesSucceeded(state) {
			state.Status = app.WorkflowStatusSucceeded
		} else if len(state.ActiveNodeIDs) == 0 {
			state.Status = app.WorkflowStatusBlocked
			return errors.New("deterministic workflow completion did not activate a dependent node")
		}
		return nil
	}
	if preparation.TransitionID == "" {
		return nil
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
		if transition.ID != preparation.TransitionID || !transition.Deterministic {
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
		if nodeState.TransitionActivations == nil {
			nodeState.TransitionActivations = make(map[app.TransitionID]int)
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

func workflowStageContextForState(state *app.WorkflowState, taskType, evidenceNeed, dataScope, browserMode, reason string) workflowStageContext {
	nodeID := state.ActiveNodeIDs[0]
	nodeState := state.Nodes[nodeID]
	stageContext := workflowStageContext{
		TaskType: taskType, EvidenceNeed: evidenceNeed, DataScope: dataScope, ToolMode: "workflow_bounded", BrowserMode: browserMode,
		RequiresToolEvidence: true, EstimatedRisk: app.RiskRead, ModelLaneHint: workflowModelLaneForProfile(state.Plan.ProfileID), Reason: reason,
		WorkflowID: state.Plan.ProfileID, WorkflowNodeID: nodeID, ScopeRevision: nodeState.ScopeRevision,
	}
	if node, ok := workflowPlanNode(state.Plan, nodeID); ok {
		if capabilities, gated := workflowStageCapabilityNames(node, nodeState.Stage); gated && len(capabilities) > 0 {
			stageContext.Capability = capabilities[0]
		}
	}
	if stageContext.Capability == "" && len(nodeState.CurrentScope.Requirements) > 0 {
		stageContext.Capability = nodeState.CurrentScope.Requirements[0].Name
	}
	return stageContext
}

func (r Runtime) materializeActiveWorkflowTools(ctx context.Context, run app.AgentRun, actorRef string, stageContext *workflowStageContext) ([]app.ToolDefinition, error) {
	state := run.Workflow.Nodes[stageContext.WorkflowNodeID]
	node, ok := workflowPlanNode(run.Workflow.Plan, stageContext.WorkflowNodeID)
	if ok && node.Goal.Completion == app.CompletionDecision {
		return nil, errors.New("workflow decision node must be resolved before tool materialization")
	}
	if ok && (node.Goal.Completion == app.CompletionModelAnswer || node.Goal.Completion == app.CompletionMessage) {
		if len(state.CurrentScope.Requirements) != 0 {
			return nil, errors.New("no-tool workflow node cannot materialize tools")
		}
		stageContext.Capability = ""
		if node.Goal.Completion == app.CompletionMessage {
			stageContext.SemanticVariables = []string{"message_content"}
		} else {
			stageContext.SemanticVariables = []string{"answer_content"}
		}
		r.addAudit(ctx, app.AuditEvent{
			SessionID: run.SessionID, RunID: run.ID, Actor: "tool-exposure", Type: "tools.exposure.none",
			Summary: "No-tool workflow intentionally exposes no tools",
			Fields: map[string]any{
				"workflow_id": run.Workflow.Plan.ProfileID, "node_id": stageContext.WorkflowNodeID, "scope_revision": state.ScopeRevision,
			},
		})

		return []app.ToolDefinition{}, nil
	}
	profile, err := r.profiles.Get(run.Workflow.Plan.ProfileID, run.Workflow.Plan.ProfileRevision)
	if err != nil {
		return nil, err
	}
	view, err := r.exposure.Search(ctx, app.ExposureRequest{
		RunID: run.ID, WorkflowID: run.Workflow.Plan.ProfileID, NodeID: stageContext.WorkflowNodeID,
		ScopeRevision: state.ScopeRevision, ActorRef: actorRef, Limit: workflowProfileDirectoryLimit(profile),
	})
	if err != nil {
		return nil, err
	}
	r.auditDirectorySearch(ctx, run, view)
	if len(view.Entries) == 0 {
		return nil, errors.New("no registered tool satisfies the active workflow scope")
	}
	entryIDs, err := workflowDirectorySelection(run, state, view)
	if err != nil {
		return nil, err
	}
	exposure, err := r.exposure.Materialize(ctx, app.MaterializeRequest{
		ViewID: view.ViewID, RunID: run.ID, WorkflowID: view.WorkflowID, NodeID: view.NodeID,
		ScopeRevision: view.ScopeRevision, EntryIDs: entryIDs, ActorRef: actorRef,
	})
	if err != nil {
		return nil, err
	}
	stageContext.ScopeRevision = view.ScopeRevision
	r.auditFixedWorkflowExposure(ctx, run, view, entryIDs, exposure.Definitions)
	includeSupport := workflowStageUsesModel(profile, run.Workflow, node, state)
	visibleDefinitions, capabilities, err := workflowStageVisibleTools(run, stageContext.WorkflowNodeID, exposure.Definitions, includeSupport)
	if err != nil {
		return nil, err
	}
	primaryEntryIDs := workflowEntryIDsMatchingRequirements(view, entryIDs, state.CurrentScope.Requirements)
	visibleDefinitions = materializeDocumentOperationSchemas(visibleDefinitions, view, primaryEntryIDs)
	visibleDefinitions = materializeBrowserGoalAssessmentSchemas(visibleDefinitions, run, stageContext.WorkflowNodeID)
	visibleDefinitions = materializeBrowserFormDraftSchemas(visibleDefinitions, run, stageContext.WorkflowNodeID)
	visibleDefinitions = workflowModelToolProjection(run, entryIDs, visibleDefinitions)
	stageContext.SemanticVariables = workflowModelSemanticVariables(visibleDefinitions)
	r.auditWorkflowStageExposure(ctx, run, stageContext.WorkflowNodeID, state.Stage, capabilities, visibleDefinitions)
	return visibleDefinitions, nil
}

func workflowEntryIDsMatchingRequirements(view app.DirectoryView, selected []app.ToolDirectoryEntryID, requirements []app.CapabilityRequirement) []app.ToolDirectoryEntryID {
	out := make([]app.ToolDirectoryEntryID, 0, len(selected))
	for _, entryID := range selected {
		entry, ok := directoryViewEntry(view, entryID)
		if ok && matchesAnyRequirement(entry.Capability, requirements) {
			out = append(out, entryID)
		}
	}
	return out
}

func workflowStageUsesModel(profile workflowProfile, workflow *app.WorkflowState, node app.WorkflowNode, state app.WorkflowNodeState) bool {
	if node.Goal.Completion != app.CompletionEvidence || node.InvocationMode == app.WorkflowInvocationDirectOnce {
		return false
	}
	if direct, ok := profile.(workflowDirectStageProfile); ok && direct.DirectStage(workflow) {
		return false
	}
	return true
}

func workflowStageVisibleTools(run app.AgentRun, nodeID app.WorkflowNodeID, definitions []app.ToolDefinition, includeSupport bool) ([]app.ToolDefinition, []string, error) {
	if run.Workflow == nil {
		return nil, nil, errors.New("workflow state is unavailable while projecting stage tools")
	}
	node, ok := workflowPlanNode(run.Workflow.Plan, nodeID)
	if !ok {
		return nil, nil, errors.New("active workflow node is missing from the frozen plan")
	}
	state, ok := run.Workflow.Nodes[nodeID]
	if !ok {
		return nil, nil, errors.New("active workflow node state is unavailable")
	}
	capabilities, gated := workflowStageCapabilityNames(node, state.Stage)
	if !gated {
		if includeSupport {
			return definitions, nil, nil
		}
		return workflowDefinitionsMatchingRequirements(definitions, state.CurrentScope.Requirements), nil, nil
	}
	if len(capabilities) == 0 {
		return nil, nil, errors.New("active workflow stage has no capability rule")
	}
	allowed := make(map[string]bool, len(capabilities))
	for _, capability := range capabilities {
		allowed[capability] = true
	}
	visible := make([]app.ToolDefinition, 0, len(definitions))
	for _, definition := range definitions {
		for _, capability := range definition.Capabilities {
			if allowed[capability.Name] || includeSupport && matchesAnyRequirement(capability, state.CurrentScope.SupportRequirements) {
				visible = append(visible, definition)
				break
			}
		}
	}
	if len(visible) == 0 {
		return nil, nil, errors.New("no materialized tool is valid in the active workflow stage")
	}
	return visible, capabilities, nil
}

func workflowDefinitionsMatchingRequirements(definitions []app.ToolDefinition, requirements []app.CapabilityRequirement) []app.ToolDefinition {
	visible := make([]app.ToolDefinition, 0, len(definitions))
	for _, definition := range definitions {
		for _, capability := range definition.Capabilities {
			if matchesAnyRequirement(capability, requirements) {
				visible = append(visible, definition)
				break
			}
		}
	}
	return visible
}

func workflowStageCapabilityNames(node app.WorkflowNode, stage string) ([]string, bool) {
	if len(node.StageCapabilities) == 0 {
		return nil, false
	}
	for _, rule := range node.StageCapabilities {
		if rule.Stage == stage {
			return append([]string(nil), rule.Capabilities...), true
		}
	}
	return nil, true
}

func (r Runtime) auditDirectorySearch(ctx context.Context, run app.AgentRun, view app.DirectoryView) {
	r.addAudit(ctx, app.AuditEvent{SessionID: run.SessionID, RunID: run.ID, Actor: "runtime", Type: "tools.directory.searched", Summary: "Searched the active workflow capability scope", Fields: map[string]any{
		"workflow_id": view.WorkflowID, "node_id": view.NodeID, "scope_revision": view.ScopeRevision,
		"directory_revision": view.DirectoryRevision, "view_id": view.ViewID, "entry_ids": directoryEntryIDs(view.Entries),
	}})

}

func (r Runtime) auditFixedWorkflowExposure(ctx context.Context, run app.AgentRun, view app.DirectoryView, entryIDs []app.ToolDirectoryEntryID, definitions []app.ToolDefinition) {
	r.addAudit(ctx, app.AuditEvent{SessionID: run.SessionID, RunID: run.ID, Actor: "runtime", Type: "tools.exposure.fixed", Summary: "Materialized the workflow's fixed tool boundary", Fields: map[string]any{
		"workflow_id": view.WorkflowID, "view_id": view.ViewID, "entry_ids": entryIDs, "tools": visibleToolNames(definitions),
	}})

}

func (r Runtime) auditWorkflowStageExposure(ctx context.Context, run app.AgentRun, nodeID app.WorkflowNodeID, stage string, capabilities []string, definitions []app.ToolDefinition) {
	r.addAudit(ctx, app.AuditEvent{SessionID: run.SessionID, RunID: run.ID, Actor: "runtime", Type: "tools.exposure.stage_filtered", Summary: "Projected the materialized tool boundary for the active workflow stage", Fields: map[string]any{
		"workflow_id": run.Workflow.Plan.ProfileID, "node_id": nodeID, "stage": stage, "capabilities": capabilities, "tools": visibleToolNames(definitions),
	}})

}

func (r Runtime) workflowActorRef(run app.AgentRun) string {
	if run.MessageContext != nil && strings.TrimSpace(run.MessageContext.OwnerID) != "" {
		return strings.TrimSpace(run.MessageContext.OwnerID)
	}
	return "owner"
}
