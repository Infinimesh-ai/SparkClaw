package agent

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/app"

type mcdReadProfile struct{}

func (mcdReadProfile) ID() app.WorkflowID                     { return app.WorkflowMCDRead }
func (mcdReadProfile) Revision() int                          { return 1 }
func (mcdReadProfile) Capability() app.CapabilityID           { return app.CapabilityMCDRead }
func (mcdReadProfile) Finalization() workflowFinalizationMode { return workflowFinalizationModel }
func (mcdReadProfile) RoutingSemantics() workflowRoutingSemantics {
	return workflowRoutingSemantics{Variants: []workflowRoutingVariant{{Key: "read", ClarifyLabel: workflowClarifyLabel{ZH: "查询麦当劳门店、菜单或优惠", EN: "Browse McDonald's restaurants, menus or offers"}, Route: workflowRouteTemplate{Operation: app.RouteOperationRead},
		EmbedTexts:      []string{"查询麦当劳佳宝科创中心店的取餐时间", "看看我的麦当劳优惠券", "查看麦当劳购物车", "Browse the menu at my McDonald's restaurant"},
		TreeDescription: "Read the owner's connected McDonald's restaurants, exact pickup options, menus, product details, benefits or cart. This read-only capability cannot add food, clear the cart, create orders or pay. Exact pickup context must come from the owner or official evidence.",
		HardNegatives:   []string{"帮我提交麦当劳订单并付款", "清空麦当劳购物车", "Add a burger to my cart", "研究麦当劳的历史"}}}}
}
func (p mcdReadProfile) Resolve(route app.RouteDecision, turn string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	intent := singleObjectiveIntent(turn, app.IntentDomainWeb, app.IntentOperationRead, app.TargetRef{Kind: app.TargetKindNone}, app.DataScopeLocal)
	id := app.WorkflowNodeID("mcd_read")
	return intent, app.WorkflowPlan{SchemaVersion: 1, ProfileID: p.ID(), ProfileRevision: p.Revision(), InitialNodeIDs: []app.WorkflowNodeID{id}, Completion: app.CompletionEvidence,
		Nodes: []app.WorkflowNode{{ID: id, InitialStage: "mcd_read", Goal: app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "Return bounded official McDonald's evidence for the owner's exact query", Completion: app.CompletionEvidence},
			InitialScope: app.CapabilityScope{Requirements: []app.CapabilityRequirement{{Name: app.ToolCapabilityMCDSelection}}}, AllowedRisks: []app.RiskLevel{app.RiskRead}, MaxAttempts: 1}}}, nil
}
func (mcdReadProfile) Prepare(*app.WorkflowState) (workflowPreparation, error) {
	return workflowPreparation{}, nil
}
func (mcdReadProfile) Assess(_ *app.WorkflowState, outcome app.ToolOutcome) app.NodeAssessment {
	a := baseNodeAssessment(outcome)
	if outcome.Status == app.ToolCallStatusCompleted {
		a.Status = app.AssessmentComplete
		a.ReasonCode = "mcd_read_returned"
	} else {
		a.Status = app.AssessmentBlocked
		a.ReasonCode = "mcd_read_unavailable"
	}
	return a
}
func (mcdReadProfile) StageContext(state *app.WorkflowState) workflowStageContext {
	return workflowStageContextForState(state, "read", "mcd", "local", "", "Use only the connected McDonald's read session. Require exact store, pickup time, method and amount ceiling for context-dependent reads. Never infer missing fields, mutate a cart or claim checkout is enabled.")
}
func (mcdReadProfile) TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string { return "" }
