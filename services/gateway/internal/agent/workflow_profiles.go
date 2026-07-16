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

var errWorkflowDirectoryAmbiguous = errors.New("workflow directory requires an explicit bounded selection")

type webPublicResearchProfile struct{}

func (webPublicResearchProfile) ID() app.WorkflowID { return app.WorkflowWebPublicResearch }
func (webPublicResearchProfile) Revision() int      { return 1 }
func (webPublicResearchProfile) Match(intent app.IntentEnvelope) bool {
	return matchesSingleIntent(intent, app.IntentDomainWeb, app.IntentOperationSearch, app.TargetKindNone, app.DataScopePublic)
}

func (webPublicResearchProfile) Recognize(sourceTurnID, content string) (app.IntentEnvelope, bool) {
	lower := strings.ToLower(content)
	if hasUnmigratedDomainSignal(lower) || len(extractURLs(content)) != 0 || !shouldSearchWeb(lower) ||
		shouldLookupWeather(lower) || shouldUseBrowserAutomation(lower) {
		return app.IntentEnvelope{}, false
	}
	intent := singleObjectiveIntent(sourceTurnID, app.IntentDomainWeb, app.IntentOperationSearch, app.TargetRef{Kind: app.TargetKindNone}, app.DataScopePublic)
	if webSourceEvidenceRequested(lower) {
		intent.Constraints.EvidenceDepth = app.EvidenceDepthSource
	}
	return intent, true
}

func (p webPublicResearchProfile) Resolve(intent app.IntentEnvelope) (app.WorkflowPlan, error) {
	objective, err := singleProfileObjective(intent, app.IntentDomainWeb, app.IntentOperationSearch)
	if err != nil {
		return app.WorkflowPlan{}, err
	}
	nodeID := app.WorkflowNodeID("research")
	pageScope := app.CapabilityScope{Requirements: []app.CapabilityRequirement{{Name: "web.page.read"}}}
	return app.WorkflowPlan{
		SchemaVersion:   1,
		ProfileID:       p.ID(),
		ProfileRevision: p.Revision(),
		SkillIDs:        []string{"web_search"},
		InitialNodeIDs:  []app.WorkflowNodeID{nodeID},
		Completion:      app.CompletionEvidence,
		Nodes: []app.WorkflowNode{{
			ID:           nodeID,
			Goal:         app.NodeGoal{ObjectiveIDs: []string{objective.ID}, Summary: "Answer with sufficient public Web evidence", Completion: app.CompletionEvidence},
			InitialScope: app.CapabilityScope{Requirements: []app.CapabilityRequirement{{Name: "web.discovery"}}},
			Transitions: []app.ScopeTransition{{
				ID: "source_page",
				On: app.TransitionPredicate{
					OutcomeSignals: []app.OutcomeSignal{app.OutcomeSignalSourcePageAvailable},
					Assessments:    []app.AssessmentStatus{app.AssessmentNeedsMoreEvidence},
				},
				Replace:        &pageScope,
				MaxActivations: 1,
			}},
			ArgumentBindings: []app.ArgumentBinding{{
				Capability: "web.page.read", Argument: "url", ResourceKind: "url", Source: app.ArgumentBindingOutcomeRef,
			}},
			AllowedRisks: []app.RiskLevel{app.RiskRead},
			MaxAttempts:  2,
		}},
	}, nil
}

func (webPublicResearchProfile) Assess(state *app.WorkflowState, outcome app.ToolOutcome) app.NodeAssessment {
	assessment := baseNodeAssessment(outcome)
	if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalAuthenticationRequired) {
		assessment.Status = app.AssessmentBlocked
		assessment.ReasonCode = "authentication_required"
		return assessment
	}
	if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalContentAvailable) {
		assessment.Status = app.AssessmentComplete
		assessment.ReasonCode = "source_page_content_available"
		return assessment
	}
	if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalNoResults) {
		assessment.Status = app.AssessmentBlocked
		assessment.ReasonCode = "discovery_returned_no_results"
		return assessment
	}
	if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalResultsAvailable) {
		if state.Intent.Constraints.EvidenceDepth == app.EvidenceDepthSource {
			assessment.Status = app.AssessmentNeedsMoreEvidence
			assessment.Signals = []app.OutcomeSignal{app.OutcomeSignalSourcePageAvailable}
			assessment.ReasonCode = "source_page_required"
		} else {
			assessment.Status = app.AssessmentComplete
			assessment.ReasonCode = "discovery_evidence_sufficient"
		}
		return assessment
	}
	assessment.Status = app.AssessmentBlocked
	assessment.ReasonCode = "required_evidence_unavailable"
	return assessment
}

func (webPublicResearchProfile) Hint(state *app.WorkflowState) workflowExecutionHint {
	return workflowHint(state, "search", "web", "public", "", "Resolved from the stable public Web research profile.")
}

func (webPublicResearchProfile) TransitionInstruction(outcome app.ToolOutcome, assessment app.NodeAssessment) string {
	for _, ref := range outcome.Refs {
		if ref.Kind == "url" {
			return fmt.Sprintf("workflow_requirement: %s. The frozen profile requires source-page evidence. Use the materialized capability with source URL: %s", assessment.ReasonCode, ref.Ref)
		}
	}
	return "workflow_requirement: the frozen profile requires source-page evidence, but no governed URL reference is available"
}

type webExplicitURLProfile struct{}

func (webExplicitURLProfile) ID() app.WorkflowID { return app.WorkflowWebExplicitURL }
func (webExplicitURLProfile) Revision() int      { return 1 }
func (webExplicitURLProfile) Match(intent app.IntentEnvelope) bool {
	return matchesSingleIntent(intent, app.IntentDomainWeb, app.IntentOperationRead, app.TargetKindExplicitURL, app.DataScopePublic)
}

func (webExplicitURLProfile) Recognize(sourceTurnID, content string) (app.IntentEnvelope, bool) {
	lower := strings.ToLower(content)
	urls := extractURLs(content)
	if len(urls) != 1 || shouldUseBrowserAutomation(lower) || shouldUseLiveBrowserForURL(content, lower) {
		return app.IntentEnvelope{}, false
	}
	intent := singleObjectiveIntent(sourceTurnID, app.IntentDomainWeb, app.IntentOperationRead, app.TargetRef{Kind: app.TargetKindExplicitURL, Ref: urls[0]}, app.DataScopePublic)
	if webSourceEvidenceRequested(lower) {
		intent.Constraints.EvidenceDepth = app.EvidenceDepthSource
	}
	return intent, true
}

func (p webExplicitURLProfile) Resolve(intent app.IntentEnvelope) (app.WorkflowPlan, error) {
	objective, err := singleProfileObjective(intent, app.IntentDomainWeb, app.IntentOperationRead)
	if err != nil || objective.Target.Kind != app.TargetKindExplicitURL || objective.Target.Ref == "" {
		return app.WorkflowPlan{}, errors.New("explicit URL read requires a deterministic URL target")
	}
	nodeID := app.WorkflowNodeID("read")
	return app.WorkflowPlan{
		SchemaVersion:   1,
		ProfileID:       p.ID(),
		ProfileRevision: p.Revision(),
		SkillIDs:        []string{"browser_automation"},
		InitialNodeIDs:  []app.WorkflowNodeID{nodeID},
		Completion:      app.CompletionEvidence,
		Nodes: []app.WorkflowNode{{
			ID:           nodeID,
			Goal:         app.NodeGoal{ObjectiveIDs: []string{objective.ID}, Summary: "Answer from the supplied public URL", Completion: app.CompletionEvidence},
			InitialScope: app.CapabilityScope{Requirements: []app.CapabilityRequirement{{Name: "web.page.read"}}},
			ArgumentBindings: []app.ArgumentBinding{{
				Capability: "web.page.read", Argument: "url", ResourceKind: "url", Source: app.ArgumentBindingIntentTarget, TargetKinds: []app.TargetKind{app.TargetKindExplicitURL},
			}},
			AllowedRisks: []app.RiskLevel{app.RiskRead},
			MaxAttempts:  2,
		}},
	}, nil
}

func (webExplicitURLProfile) Assess(_ *app.WorkflowState, outcome app.ToolOutcome) app.NodeAssessment {
	assessment := baseNodeAssessment(outcome)
	if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalAuthenticationRequired) {
		assessment.Status = app.AssessmentBlocked
		assessment.ReasonCode = "authentication_required"
	} else if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalContentAvailable) {
		assessment.Status = app.AssessmentComplete
		assessment.ReasonCode = "explicit_page_content_available"
	} else {
		assessment.Status = app.AssessmentBlocked
		assessment.ReasonCode = "required_evidence_unavailable"
	}
	return assessment
}

func (webExplicitURLProfile) Hint(state *app.WorkflowState) workflowExecutionHint {
	return workflowHint(state, "inspect", "web", "public", "autonomous", "Resolved from the stable explicit URL read profile.")
}

func (webExplicitURLProfile) TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string {
	return ""
}

type workspaceFileSearchProfile struct{}

func (workspaceFileSearchProfile) ID() app.WorkflowID { return app.WorkflowWorkspaceSearch }
func (workspaceFileSearchProfile) Revision() int      { return 1 }
func (workspaceFileSearchProfile) Match(intent app.IntentEnvelope) bool {
	return matchesSingleIntent(intent, app.IntentDomainWorkspace, app.IntentOperationSearch, app.TargetKindNone, app.DataScopeWorkspace)
}

func (workspaceFileSearchProfile) Recognize(sourceTurnID, content string) (app.IntentEnvelope, bool) {
	lower := strings.ToLower(content)
	explicitWorkspace := containsAny(lower, "workspace", "project files", "local files", "工作区", "项目文件", "本地文件")
	if len(extractURLs(content)) != 0 || len(extractPaths(content)) != 0 || workspaceMutationRequested(lower) ||
		containsEnglishSemanticTerm(lower, "knowledge", "rag", "image", "images", "screenshot", "screenshots") || containsAny(lower, "知识库", "文档库", "图片", "截图") ||
		domainSpecificSearch(lower) || isCodeTask(lower) || shouldUseBrowserAutomation(lower) ||
		(shouldSearchWeb(lower) && !explicitWorkspace) ||
		!containsAny(lower, "search", "find", "locate", "搜索", "查找", "找出", "定位") {
		return app.IntentEnvelope{}, false
	}
	return singleObjectiveIntent(sourceTurnID, app.IntentDomainWorkspace, app.IntentOperationSearch, app.TargetRef{Kind: app.TargetKindNone}, app.DataScopeWorkspace), true
}

func (p workspaceFileSearchProfile) Resolve(intent app.IntentEnvelope) (app.WorkflowPlan, error) {
	objective, err := singleProfileObjective(intent, app.IntentDomainWorkspace, app.IntentOperationSearch)
	if err != nil {
		return app.WorkflowPlan{}, err
	}
	nodeID := app.WorkflowNodeID("search")
	return app.WorkflowPlan{
		SchemaVersion: 1, ProfileID: p.ID(), ProfileRevision: p.Revision(), SkillIDs: []string{"local_files"},
		InitialNodeIDs: []app.WorkflowNodeID{nodeID}, Completion: app.CompletionEvidence,
		Nodes: []app.WorkflowNode{{
			ID: nodeID, Goal: app.NodeGoal{ObjectiveIDs: []string{objective.ID}, Summary: "Find matching files in the configured workspace", Completion: app.CompletionEvidence},
			InitialScope: app.CapabilityScope{Requirements: []app.CapabilityRequirement{{Name: "workspace.file.search"}}},
			AllowedRisks: []app.RiskLevel{app.RiskRead}, MaxAttempts: 1,
		}},
	}, nil
}

func (workspaceFileSearchProfile) Assess(_ *app.WorkflowState, outcome app.ToolOutcome) app.NodeAssessment {
	assessment := baseNodeAssessment(outcome)
	if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalResultsAvailable) {
		assessment.Status = app.AssessmentComplete
		assessment.ReasonCode = "workspace_matches_available"
	} else if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalNoResults) {
		assessment.Status = app.AssessmentComplete
		assessment.ReasonCode = "workspace_search_completed_without_matches"
	} else {
		assessment.Status = app.AssessmentBlocked
		assessment.ReasonCode = "workspace_search_failed"
	}
	return assessment
}

func (workspaceFileSearchProfile) Hint(state *app.WorkflowState) workflowExecutionHint {
	return workflowHint(state, "search", "workspace", "workspace", "", "Resolved from the stable workspace file-search profile.")
}

func (workspaceFileSearchProfile) TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string {
	return ""
}

type workspaceFileReadProfile struct{}

func (workspaceFileReadProfile) ID() app.WorkflowID { return app.WorkflowWorkspaceRead }
func (workspaceFileReadProfile) Revision() int      { return 1 }
func (workspaceFileReadProfile) Match(intent app.IntentEnvelope) bool {
	return matchesSingleIntent(intent, app.IntentDomainWorkspace, app.IntentOperationRead, app.TargetKindWorkspacePath, app.DataScopeWorkspace)
}

func (workspaceFileReadProfile) Recognize(sourceTurnID, content string) (app.IntentEnvelope, bool) {
	lower := strings.ToLower(content)
	paths := extractPaths(content)
	if len(extractURLs(content)) != 0 || len(paths) != 1 || workspaceMutationRequested(lower) ||
		containsEnglishSemanticTerm(lower, "image", "images", "photo", "photos", "screenshot", "screenshots", "ocr") || containsAny(lower, "图片", "照片", "截图", "看图") ||
		(!containsEnglishSemanticTerm(lower, "read", "summarize", "inspect", "explain") && !containsAny(lower, "读取", "阅读", "总结", "概括", "查看内容")) {
		return app.IntentEnvelope{}, false
	}
	return singleObjectiveIntent(sourceTurnID, app.IntentDomainWorkspace, app.IntentOperationRead, app.TargetRef{Kind: app.TargetKindWorkspacePath, Ref: paths[0]}, app.DataScopeWorkspace), true
}

func (p workspaceFileReadProfile) Resolve(intent app.IntentEnvelope) (app.WorkflowPlan, error) {
	objective, err := singleProfileObjective(intent, app.IntentDomainWorkspace, app.IntentOperationRead)
	if err != nil || objective.Target.Kind != app.TargetKindWorkspacePath || objective.Target.Ref == "" {
		return app.WorkflowPlan{}, errors.New("workspace file read requires one deterministic path target")
	}
	nodeID := app.WorkflowNodeID("read")
	return app.WorkflowPlan{
		SchemaVersion: 1, ProfileID: p.ID(), ProfileRevision: p.Revision(), SkillIDs: []string{"local_files"},
		InitialNodeIDs: []app.WorkflowNodeID{nodeID}, Completion: app.CompletionEvidence,
		Nodes: []app.WorkflowNode{{
			ID: nodeID, Goal: app.NodeGoal{ObjectiveIDs: []string{objective.ID}, Summary: "Read the explicitly named workspace file", Completion: app.CompletionEvidence},
			InitialScope: app.CapabilityScope{Requirements: []app.CapabilityRequirement{{Name: "workspace.file.read"}}},
			ArgumentBindings: []app.ArgumentBinding{{
				Capability: "workspace.file.read", Argument: "path", ResourceKind: "path", Source: app.ArgumentBindingIntentTarget, TargetKinds: []app.TargetKind{app.TargetKindWorkspacePath},
			}},
			AllowedRisks: []app.RiskLevel{app.RiskRead}, MaxAttempts: 1,
		}},
	}, nil
}

func (workspaceFileReadProfile) Assess(_ *app.WorkflowState, outcome app.ToolOutcome) app.NodeAssessment {
	assessment := baseNodeAssessment(outcome)
	if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalContentAvailable) {
		assessment.Status = app.AssessmentComplete
		assessment.ReasonCode = "workspace_file_content_available"
	} else {
		assessment.Status = app.AssessmentBlocked
		assessment.ReasonCode = "workspace_file_content_unavailable"
	}
	return assessment
}

func (workspaceFileReadProfile) Hint(state *app.WorkflowState) workflowExecutionHint {
	return workflowHint(state, "summarize", "workspace", "workspace", "", "Resolved from the stable workspace file-read profile.")
}

func (workspaceFileReadProfile) TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string {
	return ""
}

func singleObjectiveIntent(sourceTurnID string, domain app.IntentDomain, operation app.IntentOperation, target app.TargetRef, scope app.DataScope) app.IntentEnvelope {
	return app.IntentEnvelope{
		Version: 1, SourceTurnID: sourceTurnID,
		Objectives:  []app.Objective{{ID: "objective_1", Domain: domain, Operation: operation, Target: target, Output: app.OutputKindText, Explicit: true}},
		Constraints: app.IntentConstraints{DataScope: scope, EvidenceDepth: app.EvidenceDepthSummary},
		Resolution:  app.IntentResolution{Status: app.IntentResolved},
	}
}

func singleProfileObjective(intent app.IntentEnvelope, domain app.IntentDomain, operation app.IntentOperation) (app.Objective, error) {
	if len(intent.Objectives) != 1 || intent.Objectives[0].Domain != domain || intent.Objectives[0].Operation != operation {
		return app.Objective{}, errors.New("intent does not match the workflow profile")
	}
	return intent.Objectives[0], nil
}

func matchesSingleIntent(intent app.IntentEnvelope, domain app.IntentDomain, operation app.IntentOperation, targetKind app.TargetKind, dataScope app.DataScope) bool {
	return len(intent.Objectives) == 1 && intent.Objectives[0].Domain == domain && intent.Objectives[0].Operation == operation &&
		intent.Objectives[0].Target.Kind == targetKind && intent.Constraints.DataScope == dataScope && intent.Resolution.Status == app.IntentResolved
}

func baseNodeAssessment(outcome app.ToolOutcome) app.NodeAssessment {
	return app.NodeAssessment{OutcomeID: outcome.ID, NodeID: outcome.NodeID}
}

func workspaceMutationRequested(lower string) bool {
	return containsEnglishSemanticTerm(lower, "edit", "modify", "write", "create", "delete", "remove", "patch", "rename", "move") ||
		containsAny(lower, "编辑", "修改", "写入", "创建", "删除", "移除", "补丁", "重命名", "移动", "完善")
}

func newWorkflowState(intent app.IntentEnvelope, plan app.WorkflowPlan) *app.WorkflowState {
	nodes := make(map[app.WorkflowNodeID]app.WorkflowNodeState, len(plan.Nodes))
	for _, node := range plan.Nodes {
		status := app.WorkflowNodePending
		if containsWorkflowNodeID(plan.InitialNodeIDs, node.ID) {
			status = app.WorkflowNodeActive
		}
		nodes[node.ID] = app.WorkflowNodeState{
			Status: status, CurrentScope: node.InitialScope, ScopeRevision: 1,
			TransitionActivations: map[app.TransitionID]int{},
		}
	}
	return &app.WorkflowState{
		SchemaVersion: 1, Intent: intent, Plan: plan, PlanDigest: workflowPlanDigest(plan), Status: app.WorkflowStatusRunning,
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
		TaskType: taskType, EvidenceNeed: evidenceNeed, DataScope: dataScope, ToolMode: "read_only", BrowserMode: browserMode,
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
		ScopeRevision: state.ScopeRevision, ActorRef: actorRef, Limit: 8,
	})
	if err != nil {
		return nil, err
	}
	r.auditDirectorySearch(run, view)
	if len(view.Entries) == 0 {
		return nil, errors.New("no registered tool satisfies the active workflow scope")
	}
	entry, automatic, err := r.selectDirectoryEntry(ctx, run, view)
	if err != nil {
		return nil, err
	}
	exposure, err := r.exposure.Materialize(ctx, app.MaterializeRequest{
		ViewID: view.ViewID, RunID: run.ID, WorkflowID: view.WorkflowID, NodeID: view.NodeID,
		ScopeRevision: view.ScopeRevision, EntryIDs: []app.ToolDirectoryEntryID{entry.ID}, ActorRef: actorRef,
	})
	if err != nil {
		return nil, err
	}
	hint.ScopeRevision = view.ScopeRevision
	hint.Capability = entry.Capability.Name
	r.auditDirectorySelection(run, view, entry, automatic, exposure.Definitions)
	return exposure.Definitions, nil
}

func (r Runtime) auditDirectorySearch(run app.AgentRun, view app.DirectoryView) {
	r.store.AddAudit(app.AuditEvent{SessionID: run.SessionID, RunID: run.ID, Actor: "runtime", Type: "tools.directory.searched", Summary: "Searched the active workflow capability scope", Fields: map[string]any{
		"workflow_id": view.WorkflowID, "node_id": view.NodeID, "scope_revision": view.ScopeRevision,
		"directory_revision": view.DirectoryRevision, "view_id": view.ViewID, "entry_ids": directoryEntryIDs(view.Entries),
	}})
}

func (r Runtime) auditDirectorySelection(run app.AgentRun, view app.DirectoryView, entry app.ToolDirectoryEntry, automatic bool, definitions []app.ToolDefinition) {
	r.store.AddAudit(app.AuditEvent{SessionID: run.SessionID, RunID: run.ID, Actor: "runtime", Type: "tools.directory.selected", Summary: "Selected a bounded directory entry", Fields: map[string]any{
		"view_id": view.ViewID, "entry_id": entry.ID, "automatic": automatic,
	}})
	r.store.AddAudit(app.AuditEvent{SessionID: run.SessionID, RunID: run.ID, Actor: "runtime", Type: "tools.exposure.materialized", Summary: "Materialized concrete ToolDefinitions", Fields: map[string]any{
		"view_id": view.ViewID, "tools": visibleToolNames(definitions),
	}})
}

func (r Runtime) workflowActorRef(sessionID string) string {
	if session, ok := r.store.GetSession(sessionID); ok && session.OwnerID != "" {
		return session.OwnerID
	}
	return "owner"
}
