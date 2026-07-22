package agent

import (
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type scheduleManageProfile struct{}

func (scheduleManageProfile) ID() app.WorkflowID           { return app.WorkflowScheduleManage }
func (scheduleManageProfile) Revision() int                { return 1 }
func (scheduleManageProfile) Capability() app.CapabilityID { return app.CapabilityScheduleManage }
func (scheduleManageProfile) Finalization() workflowFinalizationMode {
	return workflowFinalizationModel
}

func (scheduleManageProfile) Recognize(input workflowRecognitionContext) (workflowRecognition, bool) {
	content := semanticRoutingContent(input.Content)
	lower := strings.ToLower(content)
	if strings.TrimSpace(content) == "" || !scheduleManagementIntent(lower) {
		return workflowRecognition{}, false
	}
	operation := scheduleManagementOperation(lower)
	return workflowRecognition{
		Slots:      app.RouteSlots{Operation: operation, Query: content},
		Confidence: 0.95,
		Reason:     "The request explicitly manages scheduled tasks through the registered schedule workflow.",
	}, true
}

func scheduleManagementIntent(lower string) bool {
	scheduleNoun := containsEnglishSemanticTerm(lower, "schedule", "schedules", "scheduled", "remind", "reminder", "reminders") ||
		containsAny(lower, "定时任务", "计划任务", "定时消息", "提醒事项", "提醒")
	if !scheduleNoun {
		return false
	}
	return containsEnglishSemanticTerm(lower, "create", "add", "set", "remind", "list", "show", "view", "read", "manage", "update", "edit", "change", "reschedule", "postpone", "cancel", "delete", "remove", "current") ||
		containsAny(lower, "创建", "新建", "添加", "设置", "查看", "列出", "当前", "有哪些", "管理", "修改", "更新", "改为", "推迟", "提前", "取消", "删除", "移除", "提醒我")
}

func scheduleManagementOperation(lower string) app.RouteOperation {
	switch {
	case containsEnglishSemanticTerm(lower, "cancel", "delete", "remove") || containsAny(lower, "取消", "删除", "移除"):
		return app.RouteOperationDelete
	case containsEnglishSemanticTerm(lower, "update", "edit", "change", "reschedule", "postpone") || containsAny(lower, "修改", "更新", "改为", "推迟", "提前"):
		return app.RouteOperationEdit
	case containsEnglishSemanticTerm(lower, "create", "add", "set", "remind") || containsAny(lower, "创建", "新建", "添加", "设置", "提醒我"):
		return app.RouteOperationCreate
	default:
		return app.RouteOperationRead
	}
}

func (p scheduleManageProfile) Resolve(route app.RouteDecision, sourceTurnID string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	operation := route.Slots.Operation
	intent := singleObjectiveIntent(sourceTurnID, app.IntentDomainSchedule, app.IntentOperation(operation), app.TargetRef{Kind: app.TargetKindNone}, app.DataScopeLocal)
	nodeID := app.WorkflowNodeID("schedule_manage")
	return intent, app.WorkflowPlan{
		SchemaVersion: 1, ProfileID: p.ID(), ProfileRevision: p.Revision(),
		InitialNodeIDs: []app.WorkflowNodeID{nodeID}, Completion: app.CompletionEvidence,
		Nodes: []app.WorkflowNode{{
			ID: nodeID, InitialStage: string(operation),
			Goal: app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "Execute exactly one scheduled-task operation through the existing Schedule Registry tools", Completion: app.CompletionEvidence},
			InitialScope: app.CapabilityScope{Requirements: []app.CapabilityRequirement{{
				Name: app.ToolCapabilityScheduleManage, Qualifiers: map[string]string{app.CapabilityQualifierOperation: string(operation)},
			}}},
			AllowedRisks: []app.RiskLevel{app.RiskRead, app.RiskReversible}, MaxAttempts: 1,
		}},
	}, nil
}

func (scheduleManageProfile) Prepare(*app.WorkflowState) (app.TransitionID, bool, error) {
	return "", false, nil
}

func (scheduleManageProfile) Assess(_ *app.WorkflowState, outcome app.ToolOutcome) app.NodeAssessment {
	return terminalGenericAssessment(outcome, "schedule_management_completed", "schedule_management_failed")
}

func (scheduleManageProfile) Hint(state *app.WorkflowState) workflowExecutionHint {
	operation := string(state.Route.Slots.Operation)
	return workflowHint(state, operation, "schedule", "local", "", "Dispatched by the schedule.manage workflow contract.")
}

func (scheduleManageProfile) TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string {
	return ""
}
