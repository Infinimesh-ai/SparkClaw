package agent

import (
	"context"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type medicineBoxQueryProfile struct{}
type medicineBoxEditProfile struct{}
type medicineDrugInfoProfile struct{}

func (medicineBoxQueryProfile) ID() app.WorkflowID           { return app.WorkflowMedicineBoxQuery }
func (medicineBoxEditProfile) ID() app.WorkflowID            { return app.WorkflowMedicineBoxEdit }
func (medicineDrugInfoProfile) ID() app.WorkflowID           { return app.WorkflowMedicineDrugInfo }
func (medicineBoxQueryProfile) Revision() int                { return 1 }
func (medicineBoxEditProfile) Revision() int                 { return 1 }
func (medicineDrugInfoProfile) Revision() int                { return 1 }
func (medicineBoxQueryProfile) Capability() app.CapabilityID { return app.CapabilityMedicineBoxQuery }
func (medicineBoxEditProfile) Capability() app.CapabilityID  { return app.CapabilityMedicineBoxEdit }
func (medicineDrugInfoProfile) Capability() app.CapabilityID { return app.CapabilityMedicineDrugInfo }
func (medicineBoxQueryProfile) Finalization() workflowFinalizationMode {
	return workflowFinalizationModel
}
func (medicineBoxEditProfile) Finalization() workflowFinalizationMode {
	return workflowFinalizationGrounded
}
func (medicineDrugInfoProfile) Finalization() workflowFinalizationMode {
	return workflowFinalizationGrounded
}
func (medicineBoxQueryProfile) Prepare(*app.WorkflowState) (workflowPreparation, error) {
	return workflowPreparation{}, nil
}
func (medicineBoxEditProfile) Prepare(*app.WorkflowState) (workflowPreparation, error) {
	return workflowPreparation{}, nil
}
func (medicineDrugInfoProfile) Prepare(*app.WorkflowState) (workflowPreparation, error) {
	return workflowPreparation{}, nil
}
func (medicineBoxQueryProfile) TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string {
	return ""
}
func (medicineBoxEditProfile) TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string {
	return ""
}
func (medicineDrugInfoProfile) TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string {
	return ""
}
func (medicineBoxQueryProfile) StageContext(s *app.WorkflowState) workflowStageContext {
	return medicineStageContext(s)
}
func (medicineBoxEditProfile) StageContext(s *app.WorkflowState) workflowStageContext {
	return medicineStageContext(s)
}
func (medicineDrugInfoProfile) StageContext(s *app.WorkflowState) workflowStageContext {
	return medicineStageContext(s)
}
func medicineStageContext(s *app.WorkflowState) workflowStageContext {
	return workflowStageContextForState(s, "medicine", "records", "local", "", "Use the frozen medicine Workflow and exact owner records.")
}

func medicineVariant(key, zh, en string, op app.RouteOperation, description string, examples, negatives []string) workflowRoutingVariant {
	return workflowRoutingVariant{Key: key, ClarifyLabel: workflowClarifyLabel{ZH: zh, EN: en}, Route: workflowRouteTemplate{Operation: op}, TreeDescription: description, EmbedTexts: examples, HardNegatives: negatives}
}
func (medicineBoxQueryProfile) RoutingSemantics() workflowRoutingSemantics {
	read := medicineVariant("read", "查看我的药箱记录", "Check my medicine box", app.RouteOperationRead,
		"Read only the owner's private medicine inventory: stocked medicines, remaining quantities, expiry and storage location. Not general medical knowledge or changes.",
		[]string{"药箱里还有退烧药吗", "我的布洛芬还剩多少", "有哪些药快过期了", "感冒药放在哪里", "家里还有没有创可贴", "药箱里过期的药有哪些", "What do I have for allergies in my medicine box?"},
		[]string{"布洛芬有什么副作用", "把布洛芬减一片", "帮我添加一盒感冒灵", "打开药箱", "一小时后提醒我吃药"})
	advise := medicineVariant("advise", "根据药箱给用药建议", "Advise from my medicine box", app.RouteOperationAnswer,
		"The owner describes symptoms and asks what medicine or care to use: consult their medicine box even when not explicitly mentioned. Also applies when asking whether a medicine IN THEIR BOX can be taken or combined. A question about whether named drugs can be combined belongs to medicine.drug_info unless the private medicine box is explicitly part of the request. A report of current medication alone does not refer to box inventory. Not a reminder.",
		[]string{"我发烧了吃什么药", "头疼得厉害有什么药能吃", "孩子咳嗽家里的药能用吗", "拉肚子了要不要去买药", "被划了个口子怎么处理", "I have a sore throat, what should I take?", "我药箱里的布洛芬能和感冒灵一起吃吗"},
		[]string{"布洛芬和对乙酰氨基酚有什么区别", "正在吃华法林，可以同时服用布洛芬吗", "发烧是怎么引起的", "明天早上提醒我吃退烧药", "添加一盒退烧药"})
	read.Route.FactScope, advise.Route.FactScope = app.RouteFactScopeMedicineBox, app.RouteFactScopeMedicineBox
	return workflowRoutingSemantics{Variants: []workflowRoutingVariant{read, advise}}
}
func (medicineBoxEditProfile) RoutingSemantics() workflowRoutingSemantics {
	return workflowRoutingSemantics{Variants: []workflowRoutingVariant{
		medicineVariant("create", "往药箱添加药品", "Add medicines to my box", app.RouteOperationCreate,
			"Explicitly add one or several medicines to the owner's inventory, including incomplete fields that need owner input. A whole list is one batch, not separate capabilities. Not photo recognition alone, inventory queries or prices.",
			[]string{"药箱里加一盒布洛芬，24 粒，2027 年 5 月过期", "新买了一瓶碘伏放在卫生间，记到药箱", "把这盒感冒灵记到药箱里", "把布洛芬、感冒灵和碘伏一起记到药箱", "Add loratadine and bandages to my medicine box"}, []string{"药箱里有布洛芬吗", "识别这张药盒照片", "布洛芬多少钱"}),
		medicineVariant("edit", "修改药箱里的药品", "Change medicines in my box", app.RouteOperationEdit,
			"Explicitly update inventory quantity, expiry, location, notes or other fields for one or several medicines. A reported past intake alone is not a request to change records; an explicit instruction to deduct it is. Not dosage advice or future reminders.",
			[]string{"布洛芬吃了两粒，帮我减掉", "把感冒灵的数量改成 5 袋", "创可贴挪到客厅抽屉了，更新药箱", "阿莫西林的有效期改成 2026 年 12 月", "给碘伏加个备注开封于 11 月", "布洛芬减两粒，感冒灵减一袋，帮我一起更新"}, []string{"布洛芬还剩多少", "布洛芬一次吃几粒", "提醒我每天吃两粒布洛芬", "我刚吃了布洛芬"}),
		medicineVariant("delete", "从药箱删除药品", "Remove a medicine from my box", app.RouteOperationDelete,
			"Explicitly remove one medicine record from the owner's inventory. Bulk deletion is unavailable; never merely list expired stock or delete schedules/files.",
			[]string{"把过期的阿莫西林删掉", "药箱里的感冒灵扔了，删除记录", "Remove the expired ibuprofen from my box"}, []string{"有哪些药过期了", "删除这个定时任务", "删除这个文件"}),
	}}
}
func (medicineDrugInfoProfile) RoutingSemantics() workflowRoutingSemantics {
	return workflowRoutingSemantics{Variants: []workflowRoutingVariant{medicineVariant("answer", "查询药品信息", "Look up medicine information", app.RouteOperationAnswer,
		"General information about a named medicine: ingredients, indications, dose precautions, adverse effects, interactions, price, availability or recalls. Includes stable knowledge, explicit no-search questions and questions about drugs the owner is currently taking. Medicine prices/availability always belong here; source freshness and permission are decided inside the Workflow. Does not inspect the private box; symptom-based what-to-take questions belong to medicine.box_query advise.",
		[]string{"布洛芬和对乙酰氨基酚有什么区别", "阿莫西林饭前还是饭后吃", "连花清瘟的成分是什么", "孕妇能吃氯雷他定吗", "头孢和酒精一起会怎样", "感康现在多少钱", "这个药最近有没有召回", "What are the side effects of ibuprofen?", "正在吃华法林，可以同时服用布洛芬吗", "不要联网，解释一下布洛芬的作用原理", "布洛芬属于哪一类药物", "碘伏目前多少钱一瓶"},
		[]string{"我刚吃了布洛芬", "我药箱里有布洛芬吗", "我发烧了吃什么药", "解释一下免疫系统", "今天有什么医疗新闻", "提醒我八点吃药"})}}
}

func medicineNode(id app.WorkflowNodeID, capability string, risk app.RiskLevel) app.WorkflowNode {
	return app.WorkflowNode{ID: id, InitialStage: string(id), Goal: app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "Execute the bound medicine operation", Completion: app.CompletionEvidence}, InitialScope: app.CapabilityScope{Requirements: []app.CapabilityRequirement{{Name: capability}}}, AllowedRisks: []app.RiskLevel{risk}, MaxAttempts: 1}
}
func medicinePlan(route app.RouteDecision, turn string, id app.WorkflowID, nodes ...app.WorkflowNode) (app.IntentEnvelope, app.WorkflowPlan, error) {
	return singleObjectiveIntent(turn, app.IntentDomainMedicine, app.IntentOperation(route.Slots.Operation), app.TargetRef{Kind: app.TargetKindNone}, app.DataScopeLocal), app.WorkflowPlan{SchemaVersion: 1, ProfileID: id, ProfileRevision: 1, InitialNodeIDs: []app.WorkflowNodeID{nodes[0].ID}, Completion: app.CompletionEvidence, Nodes: nodes}, nil
}
func (p medicineBoxQueryProfile) Resolve(route app.RouteDecision, turn string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	n := medicineNode("read_box", app.ToolCapabilityMedicineRead, app.RiskRead)
	n.InvocationMode = app.WorkflowInvocationDirectOnce
	return medicinePlan(route, turn, p.ID(), n)
}
func (p medicineBoxEditProfile) Resolve(route app.RouteDecision, turn string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	read := medicineNode("read_box", app.ToolCapabilityMedicineRead, app.RiskRead)
	read.InvocationMode = app.WorkflowInvocationDirectOnce
	edit := medicineNode("edit_box", app.ToolCapabilityMedicineWrite, app.RiskReversible)
	edit.DependsOn = []app.WorkflowNodeID{read.ID}
	edit.MaxAttempts = 2
	edit.InitialScope.MaterializeAll = true
	edit.InitialScope.Requirements = append(edit.InitialScope.Requirements, app.CapabilityRequirement{Name: app.ToolCapabilityWorkflowInput})
	edit.AllowedRisks = []app.RiskLevel{app.RiskRead, app.RiskDraft, app.RiskReversible}
	writeScope := app.CapabilityScope{Requirements: []app.CapabilityRequirement{{Name: app.ToolCapabilityMedicineWrite}}}
	edit.Transitions = []app.ScopeTransition{{ID: "medicine_fields_completed", NextStage: "apply_batch", On: app.TransitionPredicate{Assessments: []app.AssessmentStatus{app.AssessmentNeedsMoreEvidence}}, Replace: &writeScope, MaxActivations: 1}}
	return medicinePlan(route, turn, p.ID(), read, edit)
}
func (p medicineDrugInfoProfile) Resolve(route app.RouteDecision, turn string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	n := medicineNode("drug_info", app.ToolCapabilityMedicineInfo, app.RiskRead)
	n.ArgumentBindings = []app.ArgumentBinding{{Capability: app.ToolCapabilityMedicineInfo, Argument: "query", ResourceKind: "query", Source: app.ArgumentBindingRouteSlot, SourceKey: "query"}}
	return medicinePlan(route, turn, p.ID(), n)
}
func (medicineBoxQueryProfile) Assess(_ *app.WorkflowState, o app.ToolOutcome) app.NodeAssessment {
	return terminalGenericAssessment(o, "medicine_box_read", "medicine_box_read_failed")
}
func (medicineDrugInfoProfile) Assess(_ *app.WorkflowState, o app.ToolOutcome) app.NodeAssessment {
	return terminalGenericAssessment(o, "medicine_info_answered", "medicine_info_failed")
}
func (medicineBoxEditProfile) Assess(_ *app.WorkflowState, o app.ToolOutcome) app.NodeAssessment {
	a := terminalGenericAssessment(o, "medicine_batch_committed", "medicine_batch_failed")
	if o.Tool == "notify.ask_approval" && (o.Status == app.ToolCallStatusCompletedAfterApproval) {
		a.Status = app.AssessmentNeedsMoreEvidence
		a.ReasonCode = "medicine_fields_completed"
	}
	return a
}
func (medicineBoxEditProfile) RunStage(ctx context.Context, r Runtime, run app.AgentRun, stage workflowStageContext, tools []app.ToolDefinition, obs []workflowObservation, budget *workflowRunBudget) workflowExecutionResult {
	if stage.WorkflowNodeID == "read_box" {
		return r.runWorkflowDirectToolOnce(ctx, run.SessionID, run, stage, tools, obs, budget)
	}
	return r.runMedicineEditStage(ctx, run, stage, tools, obs, budget)
}
func (medicineDrugInfoProfile) RunStage(ctx context.Context, r Runtime, run app.AgentRun, stage workflowStageContext, tools []app.ToolDefinition, obs []workflowObservation, budget *workflowRunBudget) workflowExecutionResult {
	result := r.runWorkflowDirectStage(ctx, run.SessionID, run, stage, tools, obs, map[string]any{"query": run.Workflow.Route.Slots.Query, "today": currentSearchDateForTimezone(time.Now(), workflowClientTimezone(run))}, budget)
	if len(result.ToolCalls) == 1 && toolCallCompleted(result.ToolCalls[0]) {
		if m, ok := anyMap(result.ToolCalls[0].Result); ok {
			result.FinalAnswer = medicineString(m["answer"])
			result.Completed = result.FinalAnswer != ""
		}
	}
	return result
}
