package agent

import (
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const (
	localMindDelegationWaitLimit = 10 * time.Minute
	localMindPollWaitMS          = 30_000
	localMindPollAttempts        = 24
	localMindEndpointFact        = "localmind_endpoint_id"
	localMindSnapshotFact        = "localmind_snapshot_revision"
)

type localMindReadProfile struct{}
type localMindWriteProfile struct{}
type localMindQueryProfile struct{}
type localMindCancelProfile struct{}

func (localMindReadProfile) ID() app.WorkflowID           { return app.WorkflowLocalMindRead }
func (localMindReadProfile) Revision() int                { return 1 }
func (localMindReadProfile) Capability() app.CapabilityID { return app.CapabilityLocalMindRead }
func (localMindReadProfile) Finalization() workflowFinalizationMode {
	return workflowFinalizationGrounded
}
func (localMindReadProfile) RoutingSemantics() workflowRoutingSemantics {
	return localMindDelegationSemantics(false)
}
func (p localMindReadProfile) Resolve(route app.RouteDecision, sourceTurnID string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	return resolveLocalMindDelegation(p.ID(), p.Revision(), app.ToolCapabilityLocalMindDelegateRead, app.RiskRead, route, sourceTurnID)
}
func (localMindReadProfile) Prepare(*app.WorkflowState) (workflowPreparation, error) {
	return workflowPreparation{}, nil
}
func (localMindReadProfile) Assess(state *app.WorkflowState, outcome app.ToolOutcome) app.NodeAssessment {
	return assessLocalMindDelegation(state, outcome)
}
func (localMindReadProfile) StageContext(state *app.WorkflowState) workflowStageContext {
	return localMindStageContext(state)
}
func (localMindReadProfile) TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string {
	return ""
}
func (localMindReadProfile) DirectStage(*app.WorkflowState) bool { return true }
func (localMindReadProfile) DirectStageArguments(state *app.WorkflowState) map[string]any {
	return localMindDelegationArguments(state)
}
func (localMindReadProfile) alwaysDirectWorkflowProfile() {}

func (localMindWriteProfile) ID() app.WorkflowID           { return app.WorkflowLocalMindWrite }
func (localMindWriteProfile) Revision() int                { return 1 }
func (localMindWriteProfile) Capability() app.CapabilityID { return app.CapabilityLocalMindWrite }
func (localMindWriteProfile) Finalization() workflowFinalizationMode {
	return workflowFinalizationGrounded
}
func (localMindWriteProfile) RoutingSemantics() workflowRoutingSemantics {
	return localMindDelegationSemantics(true)
}
func (p localMindWriteProfile) Resolve(route app.RouteDecision, sourceTurnID string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	return resolveLocalMindDelegation(p.ID(), p.Revision(), app.ToolCapabilityLocalMindDelegateWrite, app.RiskDangerous, route, sourceTurnID)
}
func (localMindWriteProfile) Prepare(*app.WorkflowState) (workflowPreparation, error) {
	return workflowPreparation{}, nil
}
func (localMindWriteProfile) Assess(state *app.WorkflowState, outcome app.ToolOutcome) app.NodeAssessment {
	return assessLocalMindDelegation(state, outcome)
}
func (localMindWriteProfile) StageContext(state *app.WorkflowState) workflowStageContext {
	return localMindStageContext(state)
}
func (localMindWriteProfile) TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string {
	return ""
}
func (localMindWriteProfile) DirectStage(*app.WorkflowState) bool { return true }
func (localMindWriteProfile) DirectStageArguments(state *app.WorkflowState) map[string]any {
	return localMindDelegationArguments(state)
}
func (localMindWriteProfile) alwaysDirectWorkflowProfile() {}

func (localMindQueryProfile) ID() app.WorkflowID           { return app.WorkflowLocalMindQuery }
func (localMindQueryProfile) Revision() int                { return 1 }
func (localMindQueryProfile) Capability() app.CapabilityID { return app.CapabilityLocalMindQuery }
func (localMindQueryProfile) Finalization() workflowFinalizationMode {
	return workflowFinalizationGrounded
}
func (localMindQueryProfile) RoutingSemantics() workflowRoutingSemantics {
	return workflowRoutingSemantics{Variants: []workflowRoutingVariant{{
		Key: "query_task", Route: workflowRouteTemplate{Operation: app.RouteOperationRead},
		EmbedTexts: []string{
			"查询 LocalMind 任务 taskId: task-123 的状态", "LocalMind 刚才那个任务完成了吗", "get the latest LocalMind task status",
		},
		TreeDescription: "Read the current state or result of one LocalMind task. The owner must explicitly name LocalMind and identify a task ID or use an unambiguous same-session recent-task reference.",
		HardNegatives:   []string{"让 LocalMind 总结这份材料", "让 LocalMind 创建报告", "取消 LocalMind 任务", "查询本地定时任务"},
		SourceKinds:     localMindSourceKinds(),
	}}}
}
func (p localMindQueryProfile) Resolve(route app.RouteDecision, sourceTurnID string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	intent := singleObjectiveIntent(sourceTurnID, app.IntentDomainWorkspace, app.IntentOperationRead, app.TargetRef{Kind: app.TargetKindLocalMindTask, Ref: route.Slots.TargetRef}, app.DataScopeWorkspace)
	return intent, singleLocalMindTaskPlan(p.ID(), p.Revision(), "query_localmind_task", localMindCapabilityRequirement(app.ToolCapabilityLocalMindTaskStatus, route), app.RiskRead), nil
}
func (localMindQueryProfile) Prepare(*app.WorkflowState) (workflowPreparation, error) {
	return workflowPreparation{}, nil
}
func (localMindQueryProfile) Assess(_ *app.WorkflowState, outcome app.ToolOutcome) app.NodeAssessment {
	return assessLocalMindManagement(outcome, "localmind_task_queried", "localmind_task_query_failed")
}
func (localMindQueryProfile) StageContext(state *app.WorkflowState) workflowStageContext {
	return localMindStageContext(state)
}
func (localMindQueryProfile) TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string {
	return ""
}
func (localMindQueryProfile) DirectStage(*app.WorkflowState) bool { return true }
func (localMindQueryProfile) DirectStageArguments(state *app.WorkflowState) map[string]any {
	return map[string]any{"task_id": localMindRouteTaskID(state), "wait_ms": 0}
}
func (localMindQueryProfile) alwaysDirectWorkflowProfile() {}

func (localMindCancelProfile) ID() app.WorkflowID           { return app.WorkflowLocalMindCancel }
func (localMindCancelProfile) Revision() int                { return 1 }
func (localMindCancelProfile) Capability() app.CapabilityID { return app.CapabilityLocalMindCancel }
func (localMindCancelProfile) Finalization() workflowFinalizationMode {
	return workflowFinalizationGrounded
}
func (localMindCancelProfile) RoutingSemantics() workflowRoutingSemantics {
	return workflowRoutingSemantics{Variants: []workflowRoutingVariant{{
		Key: "cancel_task", Route: workflowRouteTemplate{Operation: app.RouteOperationDelete},
		EmbedTexts: []string{
			"取消 LocalMind 任务 taskId: task-123", "把 LocalMind 刚才那个任务停掉", "cancel the latest LocalMind task",
		},
		TreeDescription: "Request cancellation of one LocalMind task. The owner must explicitly name LocalMind and identify a task ID or use an unambiguous same-session recent-task reference.",
		HardNegatives:   []string{"查询 LocalMind 任务状态", "让 LocalMind 总结内容", "取消本地定时任务"},
		SourceKinds:     localMindSourceKinds(),
	}}}
}
func (p localMindCancelProfile) Resolve(route app.RouteDecision, sourceTurnID string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	intent := singleObjectiveIntent(sourceTurnID, app.IntentDomainWorkspace, app.IntentOperationDelete, app.TargetRef{Kind: app.TargetKindLocalMindTask, Ref: route.Slots.TargetRef}, app.DataScopeWorkspace)
	return intent, singleLocalMindTaskPlan(p.ID(), p.Revision(), "cancel_localmind_task", localMindCapabilityRequirement(app.ToolCapabilityLocalMindTaskCancel, route), app.RiskDangerous), nil
}
func (localMindCancelProfile) Prepare(*app.WorkflowState) (workflowPreparation, error) {
	return workflowPreparation{}, nil
}
func (localMindCancelProfile) Assess(_ *app.WorkflowState, outcome app.ToolOutcome) app.NodeAssessment {
	return assessLocalMindManagement(outcome, "localmind_task_cancel_requested", "localmind_task_cancel_failed")
}
func (localMindCancelProfile) StageContext(state *app.WorkflowState) workflowStageContext {
	return localMindStageContext(state)
}
func (localMindCancelProfile) TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string {
	return ""
}
func (localMindCancelProfile) DirectStage(*app.WorkflowState) bool { return true }
func (localMindCancelProfile) DirectStageArguments(state *app.WorkflowState) map[string]any {
	return map[string]any{"task_id": localMindRouteTaskID(state)}
}
func (localMindCancelProfile) alwaysDirectWorkflowProfile() {}

func localMindSourceKinds() []app.MessageSourceKind {
	return []app.MessageSourceKind{app.MessageSourceWeb, app.MessageSourceThirdPartyDevice, app.MessageSourceTimer}
}

func localMindDelegationSemantics(write bool) workflowRoutingSemantics {
	variant := workflowRoutingVariant{
		Key: "delegate_read", Route: workflowRouteTemplate{Operation: app.RouteOperationRead},
		EmbedTexts: []string{
			"请 LocalMind 阅读并总结当前这段文字", "让 LocalMind 阅读并总结这段内容", "请 LocalMind 调研这个问题并回答", "ask LocalMind to compare these options without changing files",
		},
		TreeDescription: "Explicitly assign LocalMind a non-mutating answer, reading, research, comparison, or summarization task. It must not create, update, rename, convert, export, or delete workspace content.",
		HardNegatives:   []string{"让 LocalMind 创建一份报告", "更新 LocalMind 里的文档", "查询 LocalMind 任务状态", "LocalMind 是什么"},
		SourceKinds:     localMindSourceKinds(),
	}
	if write {
		variant.Key = "delegate_write"
		variant.Route.Operation = app.RouteOperationEdit
		variant.EmbedTexts = []string{
			"让 LocalMind 创建一份项目报告", "请 LocalMind 更新那个文档", "ask LocalMind to rename and convert the file",
		}
		variant.TreeDescription = "Explicitly assign LocalMind work that creates, updates, renames, converts, exports, deletes, or otherwise mutates LocalMind workspace content. This route requires approval."
		variant.HardNegatives = []string{"让 LocalMind 阅读并总结", "让 LocalMind 调研后回答", "查询 LocalMind 任务状态", "取消 LocalMind 任务"}
	}
	return workflowRoutingSemantics{Variants: []workflowRoutingVariant{variant}}
}

func resolveLocalMindDelegation(profileID app.WorkflowID, revision int, delegateCapability string, delegateRisk app.RiskLevel, route app.RouteDecision, sourceTurnID string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	operation := app.IntentOperationRead
	if delegateRisk == app.RiskDangerous {
		operation = app.IntentOperationEdit
	}
	intent := singleObjectiveIntent(sourceTurnID, app.IntentDomainWorkspace, operation, app.TargetRef{Kind: app.TargetKindNone}, app.DataScopeWorkspace)
	delegateNodeID := app.WorkflowNodeID("delegate_to_localmind")
	queryNodeID := app.WorkflowNodeID("query_current_task")
	delegateRequirement := localMindCapabilityRequirement(delegateCapability, route)
	pollScope := app.CapabilityScope{Requirements: []app.CapabilityRequirement{localMindCapabilityRequirement(app.ToolCapabilityLocalMindTaskStatus, route)}}
	return intent, app.WorkflowPlan{
		SchemaVersion: 1, ProfileID: profileID, ProfileRevision: revision,
		InitialNodeIDs: []app.WorkflowNodeID{delegateNodeID}, Completion: app.CompletionEvidence,
		Nodes: []app.WorkflowNode{
			{
				ID: delegateNodeID, InitialStage: "delegate_to_localmind",
				Goal:         app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "Delegate exactly the current text request to LocalMind", Completion: app.CompletionEvidence},
				InitialScope: app.CapabilityScope{Requirements: []app.CapabilityRequirement{delegateRequirement}},
				AllowedRisks: []app.RiskLevel{delegateRisk}, MaxAttempts: 1,
			},
			{
				ID: queryNodeID, InitialStage: "query_current_task", DependsOn: []app.WorkflowNodeID{delegateNodeID},
				Goal:         app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "Wait for the delegated LocalMind task to reach a terminal state", Completion: app.CompletionEvidence},
				InitialScope: pollScope,
				Transitions: []app.ScopeTransition{{
					ID: "localmind_task_still_pending", On: app.TransitionPredicate{
						OutcomeSignals: []app.OutcomeSignal{app.OutcomeSignalLocalMindTaskPending},
						Assessments:    []app.AssessmentStatus{app.AssessmentNeedsMoreEvidence},
					}, NextStage: "query_current_task", Replace: &pollScope, MaxActivations: localMindPollAttempts - 1,
				}},
				AllowedRisks: []app.RiskLevel{app.RiskRead}, MaxAttempts: localMindPollAttempts,
			},
		},
	}, nil
}

func singleLocalMindTaskPlan(profileID app.WorkflowID, revision int, nodeID app.WorkflowNodeID, requirement app.CapabilityRequirement, risk app.RiskLevel) app.WorkflowPlan {
	return app.WorkflowPlan{
		SchemaVersion: 1, ProfileID: profileID, ProfileRevision: revision,
		InitialNodeIDs: []app.WorkflowNodeID{nodeID}, Completion: app.CompletionEvidence,
		Nodes: []app.WorkflowNode{{
			ID: nodeID, InitialStage: string(nodeID),
			Goal:         app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "Invoke the single bound LocalMind task operation", Completion: app.CompletionEvidence},
			InitialScope: app.CapabilityScope{Requirements: []app.CapabilityRequirement{requirement}},
			AllowedRisks: []app.RiskLevel{risk}, MaxAttempts: 1,
		}},
	}
}

func localMindCapabilityRequirement(name string, route app.RouteDecision) app.CapabilityRequirement {
	requirement := app.CapabilityRequirement{Name: name}
	endpointID := strings.TrimSpace(route.Facts[localMindEndpointFact])
	snapshotRevision := strings.TrimSpace(route.Facts[localMindSnapshotFact])
	if endpointID != "" && snapshotRevision != "" {
		requirement.Qualifiers = map[string]string{
			app.CapabilityQualifierEndpointID:       endpointID,
			app.CapabilityQualifierSnapshotRevision: snapshotRevision,
		}
	}
	return requirement
}

func assessLocalMindDelegation(state *app.WorkflowState, outcome app.ToolOutcome) app.NodeAssessment {
	assessment := baseNodeAssessment(outcome)
	if outcome.NodeID == "delegate_to_localmind" {
		if toolCallCompletedStatus(outcome.Status) && localMindTaskRef(outcome.Refs) != nil {
			assessment.Status, assessment.ReasonCode = app.AssessmentComplete, "localmind_task_delegated"
		} else {
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "localmind_task_delegation_failed"
		}
		return assessment
	}
	ref := localMindTaskRef(outcome.Refs)
	if ref == nil || !toolCallCompletedStatus(outcome.Status) {
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "localmind_task_query_failed"
		return assessment
	}
	switch {
	case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalLocalMindTaskCompleted):
		assessment.Status, assessment.ReasonCode = app.AssessmentComplete, "localmind_task_completed"
	case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalLocalMindTaskFailed):
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "localmind_task_failed"
	case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalLocalMindTaskCancelled):
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "localmind_task_cancelled"
	case containsOutcomeSignal(outcome.Signals, app.OutcomeSignalLocalMindTaskPending):
		if localMindDelegationDeadlineExpired(state) && ref.Attributes["wait_ms"] == "0" {
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "localmind_task_wait_timeout"
		} else {
			assessment.Status, assessment.ReasonCode = app.AssessmentNeedsMoreEvidence, "localmind_task_pending"
		}
	default:
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "localmind_task_state_invalid"
	}
	return assessment
}

func assessLocalMindManagement(outcome app.ToolOutcome, completedReason, failedReason string) app.NodeAssessment {
	assessment := baseNodeAssessment(outcome)
	if toolCallCompletedStatus(outcome.Status) && localMindTaskRef(outcome.Refs) != nil {
		assessment.Status, assessment.ReasonCode = app.AssessmentComplete, completedReason
	} else {
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, failedReason
	}
	return assessment
}

func toolCallCompletedStatus(status app.ToolCallStatus) bool {
	return status.Completed()
}

func localMindStageContext(state *app.WorkflowState) workflowStageContext {
	return workflowStageContextForState(state, "localmind_task", "external", "workspace", "", "Dispatched by an explicit LocalMind Workflow contract.")
}

func localMindDelegationArguments(state *app.WorkflowState) map[string]any {
	if state == nil || len(state.ActiveNodeIDs) != 1 {
		return nil
	}
	if state.ActiveNodeIDs[0] == "delegate_to_localmind" {
		return map[string]any{"request": state.Route.Slots.Query}
	}
	task := latestLocalMindWorkflowTask(state)
	args := map[string]any{"task_id": task.Ref, "wait_ms": localMindTaskWaitMS(state)}
	if version := strings.TrimSpace(task.Attributes["state_version"]); version != "" {
		args["known_state_version"] = version
	}
	return args
}

func localMindTaskWaitMS(state *app.WorkflowState) int {
	acceptedAt, ok := localMindDelegationAcceptedAt(state)
	if !ok {
		return 0
	}
	remaining := time.Until(acceptedAt.Add(localMindDelegationWaitLimit))
	if remaining <= 0 {
		return 0
	}
	wait := int(remaining / time.Millisecond)
	if wait > localMindPollWaitMS {
		return localMindPollWaitMS
	}
	if wait < 0 {
		return 0
	}
	return wait
}

func localMindDelegationDeadlineExpired(state *app.WorkflowState) bool {
	acceptedAt, ok := localMindDelegationAcceptedAt(state)
	return ok && !time.Now().Before(acceptedAt.Add(localMindDelegationWaitLimit))
}

func localMindDelegationAcceptedAt(state *app.WorkflowState) (time.Time, bool) {
	if state == nil {
		return time.Time{}, false
	}
	for _, ref := range state.Nodes["delegate_to_localmind"].OutcomeRefs {
		if ref.Kind != string(app.TargetKindLocalMindTask) {
			continue
		}
		acceptedAt, err := time.Parse(time.RFC3339Nano, ref.Attributes["accepted_at"])
		if err == nil {
			return acceptedAt, true
		}
	}
	return time.Time{}, false
}

func latestLocalMindWorkflowTask(state *app.WorkflowState) app.ResourceRef {
	if state == nil {
		return app.ResourceRef{}
	}
	for _, nodeID := range []app.WorkflowNodeID{"query_current_task", "delegate_to_localmind"} {
		refs := state.Nodes[nodeID].OutcomeRefs
		for index := len(refs) - 1; index >= 0; index-- {
			if refs[index].Kind == string(app.TargetKindLocalMindTask) {
				return refs[index]
			}
		}
	}
	return app.ResourceRef{}
}

func localMindRouteTaskID(state *app.WorkflowState) string {
	if state == nil {
		return ""
	}
	return strings.TrimSpace(state.Route.Slots.TargetRef)
}

func localMindTaskRef(refs []app.ResourceRef) *app.ResourceRef {
	for index := len(refs) - 1; index >= 0; index-- {
		if refs[index].Kind == string(app.TargetKindLocalMindTask) && strings.TrimSpace(refs[index].Ref) != "" {
			ref := refs[index]
			return &ref
		}
	}
	return nil
}
