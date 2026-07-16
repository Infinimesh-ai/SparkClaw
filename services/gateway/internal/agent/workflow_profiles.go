package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/skills"
)

type browserSearchProfile struct{}

func (browserSearchProfile) ID() app.WorkflowID           { return app.WorkflowBrowserSearch }
func (browserSearchProfile) Revision() int                { return 1 }
func (browserSearchProfile) Capability() app.CapabilityID { return app.CapabilityBrowserSearch }
func (p browserSearchProfile) Resolve(route app.RouteDecision, sourceTurnID string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	operation := app.IntentOperationSearch
	target := app.TargetRef{Kind: app.TargetKindNone}
	initialScope := app.CapabilityScope{Requirements: []app.CapabilityRequirement{{
		Name: string(app.CapabilityBrowserSearch), Qualifiers: map[string]string{app.CapabilityQualifierOperation: app.CapabilityOperationDiscover},
	}}}
	bindings := []app.ArgumentBinding(nil)
	transitions := []app.ScopeTransition(nil)
	if route.Slots.Operation == app.RouteOperationRead && route.Slots.TargetKind == "url" && strings.TrimSpace(route.Slots.TargetRef) != "" {
		operation = app.IntentOperationRead
		target = app.TargetRef{Kind: app.TargetKindExplicitURL, Ref: route.Slots.TargetRef}
		initialScope = app.CapabilityScope{Requirements: []app.CapabilityRequirement{{
			Name: string(app.CapabilityBrowserSearch), Qualifiers: map[string]string{app.CapabilityQualifierOperation: app.CapabilityOperationRead},
		}}}
		if len(route.Slots.TargetRefs) <= 1 {
			bindings = []app.ArgumentBinding{{
				Capability: string(app.CapabilityBrowserSearch), Argument: "url", ResourceKind: "url",
				Source: app.ArgumentBindingIntentTarget, TargetKinds: []app.TargetKind{app.TargetKindExplicitURL},
			}}
		} else {
			readScope := initialScope
			transitions = []app.ScopeTransition{{
				ID: "next_source", On: app.TransitionPredicate{Assessments: []app.AssessmentStatus{app.AssessmentNeedsMoreEvidence}},
				Replace: &readScope, MaxActivations: len(route.Slots.TargetRefs) - 1,
			}}
		}
	}
	intent := singleObjectiveIntent(sourceTurnID, app.IntentDomainWeb, operation, target, app.DataScopePublic)
	if webSourceEvidenceRequested(strings.ToLower(route.Slots.Query)) && operation == app.IntentOperationSearch {
		intent.Constraints.EvidenceDepth = app.EvidenceDepthSource
		readScope := app.CapabilityScope{Requirements: []app.CapabilityRequirement{{
			Name: string(app.CapabilityBrowserSearch), Qualifiers: map[string]string{app.CapabilityQualifierOperation: app.CapabilityOperationRead},
		}}}
		transitions = []app.ScopeTransition{{
			ID:      "source_page",
			On:      app.TransitionPredicate{OutcomeSignals: []app.OutcomeSignal{app.OutcomeSignalSourcePageAvailable}, Assessments: []app.AssessmentStatus{app.AssessmentNeedsMoreEvidence}},
			Replace: &readScope, MaxActivations: 1,
		}}
		bindings = []app.ArgumentBinding{{
			Capability: string(app.CapabilityBrowserSearch), Argument: "url", ResourceKind: "url", Source: app.ArgumentBindingOutcomeRef,
		}}
	}
	objective := intent.Objectives[0]
	nodeID := app.WorkflowNodeID("browser_search")
	plan := app.WorkflowPlan{
		SchemaVersion: 1, ProfileID: p.ID(), ProfileRevision: p.Revision(), SkillIDs: []string{"web_search"},
		InitialNodeIDs: []app.WorkflowNodeID{nodeID}, Completion: app.CompletionEvidence,
		Nodes: []app.WorkflowNode{{
			ID:           nodeID,
			Goal:         app.NodeGoal{ObjectiveIDs: []string{objective.ID}, Summary: "Answer from bounded public Internet evidence", Completion: app.CompletionEvidence},
			InitialScope: initialScope, Transitions: transitions, ArgumentBindings: bindings,
			AllowedRisks: []app.RiskLevel{app.RiskRead}, MaxAttempts: 2,
		}},
	}
	return intent, plan, nil
}

func (browserSearchProfile) Assess(state *app.WorkflowState, outcome app.ToolOutcome) app.NodeAssessment {
	assessment := baseNodeAssessment(outcome)
	if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalAuthenticationRequired) {
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "authentication_required"
		return assessment
	}
	if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalContentAvailable) {
		if targets := state.Route.Slots.TargetRefs; len(targets) > 1 {
			node := state.Nodes[outcome.NodeID]
			if len(node.AppliedOutcomeIDs)+1 < len(targets) {
				assessment.Status, assessment.ReasonCode = app.AssessmentNeedsMoreEvidence, "additional_source_required"
				return assessment
			}
		}
		assessment.Status, assessment.ReasonCode = app.AssessmentComplete, "source_page_content_available"
		return assessment
	}
	if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalNoResults) {
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "discovery_returned_no_results"
		return assessment
	}
	if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalResultsAvailable) {
		if state.Intent.Constraints.EvidenceDepth == app.EvidenceDepthSource {
			assessment.Status = app.AssessmentNeedsMoreEvidence
			assessment.Signals = []app.OutcomeSignal{app.OutcomeSignalSourcePageAvailable}
			assessment.ReasonCode = "source_page_required"
		} else {
			assessment.Status, assessment.ReasonCode = app.AssessmentComplete, "discovery_evidence_sufficient"
		}
		return assessment
	}
	assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "required_web_evidence_unavailable"
	return assessment
}

func (browserSearchProfile) Hint(state *app.WorkflowState) workflowExecutionHint {
	return workflowHint(state, "search", "web", "public", "", "Dispatched by the browser.search workflow contract.")
}

func (browserSearchProfile) TransitionInstruction(outcome app.ToolOutcome, assessment app.NodeAssessment) string {
	for _, ref := range outcome.Refs {
		if ref.Kind == "url" {
			return fmt.Sprintf("workflow_requirement: %s. Read the governed source URL: %s", assessment.ReasonCode, ref.Ref)
		}
	}
	return "workflow_requirement: source-page evidence is required but no governed URL is available"
}

type browserAutomationProfile struct{}

func (browserAutomationProfile) ID() app.WorkflowID           { return app.WorkflowBrowserAutomation }
func (browserAutomationProfile) Revision() int                { return 1 }
func (browserAutomationProfile) Capability() app.CapabilityID { return app.CapabilityBrowserAutomation }
func (p browserAutomationProfile) Resolve(route app.RouteDecision, sourceTurnID string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	target := app.TargetRef{Kind: app.TargetKindNone}
	if route.Slots.TargetKind == "url" && route.Slots.TargetRef != "" {
		target = app.TargetRef{Kind: app.TargetKindExplicitURL, Ref: route.Slots.TargetRef}
	}
	intent := singleObjectiveIntent(sourceTurnID, app.IntentDomainWeb, app.IntentOperationAutomate, target, app.DataScopePublic)
	nodeID := app.WorkflowNodeID("browser_automation")
	automationScope := app.CapabilityScope{Requirements: []app.CapabilityRequirement{{Name: string(app.CapabilityBrowserAutomation)}}}
	return intent, app.WorkflowPlan{
		SchemaVersion: 1, ProfileID: p.ID(), ProfileRevision: p.Revision(), SkillIDs: []string{"browser_automation"},
		InitialNodeIDs: []app.WorkflowNodeID{nodeID}, Completion: app.CompletionEvidence,
		Nodes: []app.WorkflowNode{{
			ID:           nodeID,
			Goal:         app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "Perform the bounded browser interaction requested by the owner", Completion: app.CompletionEvidence},
			InitialScope: automationScope,
			Transitions: []app.ScopeTransition{{
				ID: "continue_interaction", On: app.TransitionPredicate{Assessments: []app.AssessmentStatus{app.AssessmentNeedsMoreEvidence}},
				Replace: &automationScope, MaxActivations: 5,
			}},
			AllowedRisks: []app.RiskLevel{app.RiskRead, app.RiskDraft, app.RiskReversible}, MaxAttempts: 6,
		}},
	}, nil
}
func (browserAutomationProfile) Assess(state *app.WorkflowState, outcome app.ToolOutcome) app.NodeAssessment {
	assessment := terminalGenericAssessment(outcome, "browser_automation_completed", "browser_automation_failed")
	query := strings.ToLower(state.Route.Slots.Query)
	needsScreenshot := containsAny(query, "screenshot", "截图")
	if assessment.Status == app.AssessmentComplete && needsScreenshot && outcome.Tool != "browser.screenshot" {
		assessment.Status, assessment.ReasonCode = app.AssessmentNeedsMoreEvidence, "browser_screenshot_required"
	}
	return assessment
}
func (browserAutomationProfile) Hint(state *app.WorkflowState) workflowExecutionHint {
	hint := workflowHint(state, "browse", "web", "public", "collaborative", "Dispatched by the browser.automation workflow contract.")
	query := state.Route.Slots.Query
	if shouldUseAuthenticatedBrowserSession(query, strings.ToLower(query)) {
		hint.EvidenceNeed = "personal_data"
		hint.DataScope = "owner"
		hint.ModelLaneHint = "deep"
	}
	return hint
}
func (browserAutomationProfile) TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string {
	return ""
}

type documentInformationProfile struct{}

func (documentInformationProfile) ID() app.WorkflowID { return app.WorkflowDocumentInformation }
func (documentInformationProfile) Revision() int      { return 1 }
func (documentInformationProfile) Capability() app.CapabilityID {
	return app.CapabilityDocumentInformation
}
func (p documentInformationProfile) Resolve(route app.RouteDecision, sourceTurnID string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	operation := app.IntentOperationSearch
	target := app.TargetRef{Kind: app.TargetKindNone}
	bindings := []app.ArgumentBinding(nil)
	if route.Slots.Operation == app.RouteOperationRead && route.Slots.TargetKind == "workspace_path" && route.Slots.TargetRef != "" {
		operation = app.IntentOperationRead
		target = app.TargetRef{Kind: app.TargetKindWorkspacePath, Ref: route.Slots.TargetRef}
		bindings = []app.ArgumentBinding{{
			Capability: string(app.CapabilityDocumentInformation), Argument: "path", ResourceKind: "path",
			Source: app.ArgumentBindingIntentTarget, TargetKinds: []app.TargetKind{app.TargetKindWorkspacePath},
		}}
	}
	intent := singleObjectiveIntent(sourceTurnID, app.IntentDomainWorkspace, operation, target, app.DataScopeWorkspace)
	nodeID := app.WorkflowNodeID("document_information")
	return intent, app.WorkflowPlan{
		SchemaVersion: 1, ProfileID: p.ID(), ProfileRevision: p.Revision(), SkillIDs: []string{"local_files", "document_assistant"},
		InitialNodeIDs: []app.WorkflowNodeID{nodeID}, Completion: app.CompletionEvidence,
		Nodes: []app.WorkflowNode{{
			ID:               nodeID,
			Goal:             app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "Discover or read governed document information", Completion: app.CompletionEvidence},
			InitialScope:     app.CapabilityScope{Requirements: []app.CapabilityRequirement{{Name: string(app.CapabilityDocumentInformation)}}},
			ArgumentBindings: bindings, AllowedRisks: []app.RiskLevel{app.RiskRead}, MaxAttempts: 2,
		}},
	}, nil
}
func (documentInformationProfile) Assess(_ *app.WorkflowState, outcome app.ToolOutcome) app.NodeAssessment {
	assessment := baseNodeAssessment(outcome)
	switch {
	case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalContentAvailable), containsOutcomeSignal(outcome.Signals, app.OutcomeSignalResultsAvailable), containsOutcomeSignal(outcome.Signals, app.OutcomeSignalNoResults):
		assessment.Status, assessment.ReasonCode = app.AssessmentComplete, "document_information_available"
	default:
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "document_information_failed"
	}
	return assessment
}
func (documentInformationProfile) Hint(state *app.WorkflowState) workflowExecutionHint {
	return workflowHint(state, "inspect", "workspace", "workspace", "", "Dispatched by the document.information workflow contract.")
}
func (documentInformationProfile) TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string {
	return ""
}

type documentProcessingProfile struct{}

func (documentProcessingProfile) ID() app.WorkflowID { return app.WorkflowDocumentProcessing }
func (documentProcessingProfile) Revision() int      { return 1 }
func (documentProcessingProfile) Capability() app.CapabilityID {
	return app.CapabilityDocumentProcessing
}
func (p documentProcessingProfile) Resolve(route app.RouteDecision, sourceTurnID string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	target := app.TargetRef{Kind: app.TargetKindNone}
	if route.Slots.TargetKind == "workspace_path" && route.Slots.TargetRef != "" {
		target = app.TargetRef{Kind: app.TargetKindWorkspacePath, Ref: route.Slots.TargetRef}
	}
	intent := singleObjectiveIntent(sourceTurnID, app.IntentDomainWorkspace, app.IntentOperationProcess, target, app.DataScopeWorkspace)
	nodeID := app.WorkflowNodeID("document_processing")
	processingScope := app.CapabilityScope{Requirements: []app.CapabilityRequirement{
		{Name: string(app.CapabilityDocumentInformation)},
		{Name: string(app.CapabilityDocumentProcessing)},
	}}
	return intent, app.WorkflowPlan{
		SchemaVersion: 1, ProfileID: p.ID(), ProfileRevision: p.Revision(), SkillIDs: []string{"local_files", "document_assistant"},
		InitialNodeIDs: []app.WorkflowNodeID{nodeID}, Completion: app.CompletionEvidence,
		Nodes: []app.WorkflowNode{{
			ID:           nodeID,
			Goal:         app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "Create, edit, transform, or delete a governed document", Completion: app.CompletionEvidence},
			InitialScope: processingScope,
			Transitions: []app.ScopeTransition{{
				ID: "continue_processing", On: app.TransitionPredicate{Assessments: []app.AssessmentStatus{app.AssessmentNeedsMoreEvidence}},
				Replace: &processingScope, MaxActivations: 5,
			}},
			AllowedRisks: []app.RiskLevel{app.RiskRead, app.RiskDraft, app.RiskReversible, app.RiskDangerous}, MaxAttempts: 6,
		}},
	}, nil
}
func (documentProcessingProfile) Assess(_ *app.WorkflowState, outcome app.ToolOutcome) app.NodeAssessment {
	assessment := terminalGenericAssessment(outcome, "document_processing_completed", "document_processing_failed")
	if assessment.Status == app.AssessmentComplete && (outcome.Tool == "files.search" || outcome.Tool == "files.read" || outcome.Tool == "pdf.extract_text") {
		assessment.Status, assessment.ReasonCode = app.AssessmentNeedsMoreEvidence, "document_mutation_required"
	}
	return assessment
}
func (documentProcessingProfile) Hint(state *app.WorkflowState) workflowExecutionHint {
	return workflowHint(state, "modify", "workspace", "workspace", "", "Dispatched by the document.processing workflow contract.")
}
func (documentProcessingProfile) TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string {
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
		nodes[node.ID] = app.WorkflowNodeState{Status: status, CurrentScope: node.InitialScope, ScopeRevision: 1, TransitionActivations: map[app.TransitionID]int{}}
	}
	return &app.WorkflowState{
		SchemaVersion: 1, Route: route, ReturnRoute: returnRoute, Intent: intent, Plan: plan, PlanDigest: workflowPlanDigest(plan), Status: app.WorkflowStatusRunning,
		ActiveNodeIDs: append([]app.WorkflowNodeID(nil), plan.InitialNodeIDs...), Nodes: nodes,
	}
}

func workflowPlanDigest(plan app.WorkflowPlan) string {
	payload, _ := json.Marshal(plan)
	sum := sha256.Sum256(payload)
	return "plan_" + hex.EncodeToString(sum[:])
}

func workflowHint(state *app.WorkflowState, taskType, evidenceNeed, dataScope, browserMode, reason string) workflowExecutionHint {
	nodeID := state.ActiveNodeIDs[0]
	nodeState := state.Nodes[nodeID]
	return workflowExecutionHint{
		TaskType: taskType, EvidenceNeed: evidenceNeed, DataScope: dataScope, ToolMode: "workflow_bounded", BrowserMode: browserMode,
		RequiresToolEvidence: true, EstimatedRisk: app.RiskRead, ModelLaneHint: "fast", Reason: reason,
		WorkflowID: state.Plan.ProfileID, WorkflowNodeID: nodeID, ScopeRevision: nodeState.ScopeRevision,
	}
}

func (r Runtime) exactWorkflowSkills(skillIDs []string) []skills.Skill {
	out := make([]skills.Skill, 0, len(skillIDs))
	for _, skillID := range skillIDs {
		skill, ok, err := r.skills.Get(skillID)
		if err == nil && ok {
			out = append(out, skill)
		}
	}
	return out
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
	entryIDs := directoryEntryIDs(view.Entries)
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
