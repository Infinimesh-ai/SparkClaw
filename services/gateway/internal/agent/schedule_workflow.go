package agent

import (
	"errors"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type scheduleManageProfile struct{}

type ScheduleAction struct {
	Operation         app.RouteOperation
	ScheduleID        string
	ExpectedUpdatedAt string
	Text              *string
	DueTime           *string
	Timezone          *string
	Recurrence        *string
}

func (r Runtime) scheduleActionRoute(action ScheduleAction, canonical string) (app.RouteDecision, error) {
	if action.Operation != app.RouteOperationEdit && action.Operation != app.RouteOperationDelete {
		return app.RouteDecision{}, errors.New("schedule action operation must be edit or delete")
	}
	if strings.TrimSpace(action.ScheduleID) == "" || strings.TrimSpace(action.ExpectedUpdatedAt) == "" {
		return app.RouteDecision{}, errors.New("schedule action target and expected_updated_at are required")
	}
	path, err := r.capabilities.PathTo(app.CapabilityScheduleManage)
	if err != nil {
		return app.RouteDecision{}, err
	}
	facts := map[string]string{"schedule_expected_updated_at": strings.TrimSpace(action.ExpectedUpdatedAt)}
	for key, value := range map[string]*string{
		"schedule_text": action.Text, "schedule_due_time": action.DueTime,
		"schedule_timezone": action.Timezone, "schedule_recurrence": action.Recurrence,
	} {
		if value != nil {
			facts[key] = *value
		}
	}
	decision := app.RouteDecision{
		SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched, CatalogRevision: r.capabilities.Revision(),
		CapabilityPath: path, Slots: app.RouteSlots{Operation: action.Operation, Query: semanticRoutingContent(canonical), TargetRef: strings.TrimSpace(action.ScheduleID)},
		Facts: facts, Confidence: 1, Reason: "The owner submitted a typed scheduled-task action through WebChat.",
	}
	if err := r.capabilities.ValidateDecision(decision); err != nil {
		return app.RouteDecision{}, err
	}
	return decision, nil
}

func (scheduleManageProfile) ID() app.WorkflowID           { return app.WorkflowScheduleManage }
func (scheduleManageProfile) Revision() int                { return 2 }
func (scheduleManageProfile) Capability() app.CapabilityID { return app.CapabilityScheduleManage }
func (scheduleManageProfile) RoutingSemantics() workflowRoutingSemantics {
	return workflowRoutingSemantics{Variants: []workflowRoutingVariant{
		{
			Key: "create", Route: workflowRouteTemplate{Operation: app.RouteOperationCreate},
			EmbedTexts: []string{
				"一分钟后提醒我吃饭", "一分钟后告知我吃饭", "下午三点叫我开会", "Remind me to call Alice tomorrow",
				"半小时后查一下上海天气", "每天早上八点提醒我喝水", "到时候跟我说该出发了",
			},
			TreeDescription: "Create a future system-triggered message or action only when the owner explicitly asks the system to remind, tell, notify, query, or act later. A future-tense statement about what the owner will personally do is not a schedule command.",
			HardNegatives: []string{
				"一分钟后我会吃饭", "明天下午三点我参加会议", "提醒投递为什么失败", "删除这句话里的提醒两个字",
			},
		},
		{
			Key: "read", Route: workflowRouteTemplate{Operation: app.RouteOperationRead},
			EmbedTexts:      []string{"查看我的定时任务", "列出明天的提醒", "我现在有哪些计划任务", "Show my scheduled reminders"},
			TreeDescription: "List or inspect existing scheduled tasks or reminders without changing them.",
			HardNegatives:   []string{"为什么提醒没有触发", "查看聊天记录", "告诉我明天有什么会议"},
		},
		{
			Key: "edit", Route: workflowRouteTemplate{Operation: app.RouteOperationEdit},
			EmbedTexts:      []string{"把喝水提醒改到下午三点", "修改明天的定时任务", "将那个提醒推迟半小时", "Reschedule my call reminder"},
			TreeDescription: "Change the time, recurrence, or payload of one existing scheduled task after resolving the target from the task list.",
			HardNegatives:   []string{"修改文档里的提醒内容", "创建一个新提醒", "为什么提醒时间不对"},
		},
		{
			Key: "delete", Route: workflowRouteTemplate{Operation: app.RouteOperationDelete},
			EmbedTexts:      []string{"删除喝水提醒", "取消明天的定时任务", "不要再执行那个每周提醒", "Cancel my scheduled reminder"},
			TreeDescription: "Cancel or delete one existing scheduled task after resolving the target from the task list.",
			HardNegatives:   []string{"删除这句话里的提醒两个字", "删除文档", "提醒为什么被取消了"},
		},
	}}
}
func (scheduleManageProfile) Finalization() workflowFinalizationMode {
	return workflowFinalizationModel
}

func temporalChineseReminderCreateIntent(lower string) bool {
	reminderIndex := strings.Index(lower, "提醒")
	if reminderIndex <= 0 {
		return false
	}
	prefix := lower[:reminderIndex]
	if containsAny(prefix, "查看", "列出", "当前", "有哪些", "查询") {
		return false
	}
	return containsAny(prefix,
		"秒后", "分钟后", "小时后", "天后", "周后", "个月后", "以后", "一会", "稍后", "到时候",
		"今天", "今晚", "明天", "后天", "早上", "上午", "中午", "下午", "晚上", "凌晨", "每",
	)
}

func (p scheduleManageProfile) Resolve(route app.RouteDecision, sourceTurnID string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	operation := route.Slots.Operation
	intent := singleObjectiveIntent(sourceTurnID, app.IntentDomainSchedule, app.IntentOperation(operation), app.TargetRef{Kind: app.TargetKindNone}, app.DataScopeLocal)
	nodeID := app.WorkflowNodeID("schedule_manage")
	initialScope := app.CapabilityScope{Requirements: []app.CapabilityRequirement{{
		Name: app.ToolCapabilityScheduleManage, Qualifiers: map[string]string{app.CapabilityQualifierOperation: string(operation)},
	}}}
	initialStage := string(operation)
	maxAttempts := 1
	transitions := []app.ScopeTransition{}
	bindings := []app.ArgumentBinding{}
	if operation == app.RouteOperationEdit || operation == app.RouteOperationDelete {
		initialStage = "discover_target"
		initialScope = app.CapabilityScope{Requirements: []app.CapabilityRequirement{{
			Name: app.ToolCapabilityScheduleManage, Qualifiers: map[string]string{app.CapabilityQualifierOperation: string(operation), "stage": "discover"},
		}}}
		mutationScope := app.CapabilityScope{Requirements: []app.CapabilityRequirement{{
			Name: app.ToolCapabilityScheduleManage, Qualifiers: map[string]string{app.CapabilityQualifierOperation: string(operation), "stage": "mutate"},
		}}}
		transitions = []app.ScopeTransition{{
			ID: "schedule_target_resolved", NextStage: "mutate_schedule",
			On:      app.TransitionPredicate{OutcomeSignals: []app.OutcomeSignal{app.OutcomeSignalScheduleTargetResolved}, Assessments: []app.AssessmentStatus{app.AssessmentNeedsMoreEvidence}},
			Replace: &mutationScope, MaxActivations: 1,
		}}
		bindings = []app.ArgumentBinding{
			{Capability: app.ToolCapabilityScheduleManage, Argument: "reminder_id", ResourceKind: "schedule", Source: app.ArgumentBindingOutcomeRef},
			{Capability: app.ToolCapabilityScheduleManage, Argument: "expected_updated_at", ResourceKind: "schedule", Source: app.ArgumentBindingOutcomeRef, SourceKey: "updated_at"},
		}
		if operation == app.RouteOperationEdit {
			for _, binding := range []struct{ fact, argument string }{
				{"schedule_text", "text"}, {"schedule_due_time", "due_time"}, {"schedule_timezone", "timezone"}, {"schedule_recurrence", "recurrence"},
			} {
				if _, ok := route.Facts[binding.fact]; ok {
					bindings = append(bindings, app.ArgumentBinding{Capability: app.ToolCapabilityScheduleManage, Argument: binding.argument, ResourceKind: "schedule_patch", Source: app.ArgumentBindingRouteFact, SourceKey: binding.fact})
				}
			}
		}
		maxAttempts = 2
	}
	return intent, app.WorkflowPlan{
		SchemaVersion: 1, ProfileID: p.ID(), ProfileRevision: p.Revision(),
		InitialNodeIDs: []app.WorkflowNodeID{nodeID}, Completion: app.CompletionEvidence,
		Nodes: []app.WorkflowNode{{
			ID: nodeID, InitialStage: initialStage,
			Goal:         app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "Discover one owner-visible schedule before applying any requested mutation", Completion: app.CompletionEvidence},
			InitialScope: initialScope, Transitions: transitions, ArgumentBindings: bindings,
			AllowedRisks: []app.RiskLevel{app.RiskRead, app.RiskReversible}, MaxAttempts: maxAttempts,
		}},
	}, nil
}

func (scheduleManageProfile) Prepare(*app.WorkflowState) (workflowPreparation, error) {
	return workflowPreparation{}, nil
}

func (scheduleManageProfile) Assess(state *app.WorkflowState, outcome app.ToolOutcome) app.NodeAssessment {
	assessment := baseNodeAssessment(outcome)
	node := state.Nodes[outcome.NodeID]
	switch node.Stage {
	case string(app.RouteOperationCreate):
		return terminalGenericAssessment(outcome, "schedule_created", "schedule_create_failed")
	case string(app.RouteOperationRead):
		if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalSchedulesListed) {
			assessment.Status, assessment.ReasonCode = app.AssessmentComplete, "schedules_listed"
		} else {
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "schedule_list_failed"
		}
	case "discover_target":
		selected, reason := selectScheduleTarget(state.Route, outcome.Refs)
		if selected == nil {
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, reason
			return assessment
		}
		assessment.Status, assessment.ReasonCode = app.AssessmentNeedsMoreEvidence, "schedule_target_resolved"
		assessment.Signals = []app.OutcomeSignal{app.OutcomeSignalScheduleTargetResolved}
		assessment.SelectedRefs = []app.ResourceRef{*selected}
	case "mutate_schedule":
		if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalScheduleChanged) {
			assessment.Status, assessment.ReasonCode = app.AssessmentComplete, "schedule_changed"
		} else {
			assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "schedule_change_failed"
		}
	default:
		assessment.Status, assessment.ReasonCode = app.AssessmentBlocked, "schedule_stage_invalid"
	}
	return assessment
}

func (scheduleManageProfile) StageContext(state *app.WorkflowState) workflowStageContext {
	operation := string(state.Route.Slots.Operation)
	return workflowStageContextForState(state, operation, "schedule", "local", "", "Dispatched by the schedule.manage workflow contract.")
}

func (scheduleManageProfile) TransitionInstruction(_ app.ToolOutcome, assessment app.NodeAssessment) string {
	if assessment.ReasonCode != "schedule_target_resolved" || len(assessment.SelectedRefs) != 1 {
		return ""
	}
	ref := assessment.SelectedRefs[0]
	return "workflow_stage: mutate_schedule. The uniquely resolved schedule is " + ref.Ref + " at version " + ref.Attributes["updated_at"] + ". Apply only the requested edit or cancellation; reminder_id and expected_updated_at are runtime-bound."
}

func selectScheduleTarget(route app.RouteDecision, refs []app.ResourceRef) (*app.ResourceRef, string) {
	candidates := make([]app.ResourceRef, 0, len(refs))
	for _, ref := range refs {
		if ref.Kind == "schedule" && ref.Attributes["status"] == "pending" {
			candidates = append(candidates, ref)
		}
	}
	targetID := strings.TrimSpace(route.Slots.TargetRef)
	if targetID != "" {
		for i := range candidates {
			if candidates[i].Ref == targetID {
				if expected := strings.TrimSpace(route.Facts["schedule_expected_updated_at"]); expected != "" && expected != candidates[i].Attributes["updated_at"] {
					return nil, "schedule_target_changed"
				}
				return &candidates[i], ""
			}
		}
		return nil, "schedule_target_not_found"
	}
	query := strings.ToLower(strings.TrimSpace(route.Slots.Query))
	matches := []app.ResourceRef{}
	for _, candidate := range candidates {
		if strings.Contains(query, strings.ToLower(candidate.Ref)) {
			matches = append(matches, candidate)
			continue
		}
		for _, key := range []string{"text", "text_summary"} {
			value := strings.ToLower(strings.TrimSpace(candidate.Attributes[key]))
			if len([]rune(value)) >= 2 && strings.Contains(query, value) {
				matches = append(matches, candidate)
				break
			}
		}
	}
	if len(matches) == 1 {
		return &matches[0], ""
	}
	if len(matches) > 1 || len(candidates) > 1 {
		return nil, "schedule_target_ambiguous"
	}
	if len(candidates) == 1 {
		return &candidates[0], ""
	}
	return nil, "schedule_target_not_found"
}
