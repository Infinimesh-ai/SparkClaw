package agent

import (
	"context"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type mcdOrderProfile struct{}

func (mcdOrderProfile) ID() app.WorkflowID                     { return app.WorkflowMCDOrder }
func (mcdOrderProfile) Capability() app.CapabilityID           { return app.CapabilityMCDOrder }
func (mcdOrderProfile) Revision() int                          { return 1 }
func (mcdOrderProfile) Finalization() workflowFinalizationMode { return workflowFinalizationGrounded }
func (mcdOrderProfile) Prepare(*app.WorkflowState) (workflowPreparation, error) {
	return workflowPreparation{}, nil
}
func (mcdOrderProfile) TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string { return "" }
func (mcdOrderProfile) StageContext(s *app.WorkflowState) workflowStageContext {
	return workflowStageContextForState(s, "order", "mcd", "local", "", "")
}
func (mcdOrderProfile) RoutingSemantics() workflowRoutingSemantics {
	return workflowRoutingSemantics{Variants: []workflowRoutingVariant{{Key: "order", ClarifyLabel: workflowClarifyLabel{ZH: "麦当劳点餐", EN: "Order McDonald's"}, Route: workflowRouteTemplate{Operation: app.RouteOperationDraft},
		TreeDescription: "Order a McDonald's meal in one continuous workflow: always confirm the store, resolve exact menu choices, apply the best usable discount, then request immutable wallet payment approval and return this order's pickup code. Requires an explicit new ordering intent. Never route brand questions, menu/offer browsing, cart-only changes or checking a previous order here.",
		EmbedTexts:      []string{"帮我点一份麦辣鸡腿堡套餐", "公司楼下麦当劳买两个巨无霸，12点取", "用麦钱包买早餐，30元以内", "Order a McSpicy chicken burger meal for me", "Buy two Big Macs at McDonald's for pickup at noon"},
		HardNegatives:   []string{"麦当劳的历史", "麦当劳热量多少", "附近有什么快餐", "麦当劳上一单成功了吗", "只看看麦当劳菜单", "查询麦当劳优惠券", "清空麦当劳购物车", "Did my last McDonald's order go through?", "Show me the McDonald's menu without ordering"}}}}
}
func (p mcdOrderProfile) Resolve(_ app.RouteDecision, turn string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	names := []string{"guard", "intent", "context", "menu", "offer", "cart", "quote", "pay", "settle"}
	scopes := [][]string{{"mcd.purchase.status"}, {}, {app.ToolCapabilityMCDSelection}, {app.ToolCapabilityMCDSelection}, {app.ToolCapabilityMCDSelection}, {app.ToolCapabilityMCDSelection, "mcd.cart.read", "mcd.cart.write"}, {"mcd.cart.read", "mcd.cart.write", "mcd.cart.quote"}, {"mcd.purchase.commit"}, {"mcd.purchase.status", "mcd.cart.quote"}}
	nodes := make([]app.WorkflowNode, 0, len(names))
	for i, name := range names {
		// The input capability remains registered when ordering is unavailable so
		// guard can return its fixed disabled result without invoking a model.
		scope := app.CapabilityScope{MaterializeAll: true, Requirements: []app.CapabilityRequirement{{Name: app.ToolCapabilityWorkflowInput}}}
		for _, cap := range scopes[i] {
			scope.Requirements = append(scope.Requirements, app.CapabilityRequirement{Name: cap})
		}
		node := app.WorkflowNode{ID: app.WorkflowNodeID(name), InitialStage: name, Goal: app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "Complete the bound McDonald's ordering stage", Completion: app.CompletionEvidence}, InitialScope: scope, AllowedRisks: []app.RiskLevel{app.RiskRead, app.RiskDraft, app.RiskReversible, app.RiskDangerous}, MaxAttempts: 20}
		if i > 0 {
			node.DependsOn = []app.WorkflowNodeID{app.WorkflowNodeID(names[i-1])}
		}
		node.Transitions = []app.ScopeTransition{{ID: app.TransitionID("mcd_" + name + "_answer"), NextStage: name, On: app.TransitionPredicate{Assessments: []app.AssessmentStatus{app.AssessmentNeedsMoreEvidence}}, Replace: &scope, MaxActivations: 19}}
		nodes = append(nodes, node)
	}
	return singleObjectiveIntent(turn, app.IntentDomainWeb, app.IntentOperationDraft, app.TargetRef{Kind: app.TargetKindNone}, app.DataScopeLocal), app.WorkflowPlan{SchemaVersion: 1, ProfileID: p.ID(), ProfileRevision: 1, InitialNodeIDs: []app.WorkflowNodeID{"guard"}, Completion: app.CompletionEvidence, Nodes: nodes}, nil
}
func (mcdOrderProfile) Assess(_ *app.WorkflowState, o app.ToolOutcome) app.NodeAssessment {
	a := baseNodeAssessment(o)
	a.Status = app.AssessmentNeedsMoreEvidence
	a.ReasonCode = "mcd_resume_original_stage"
	if o.Tool == "" {
		a.Status = app.AssessmentComplete
		a.ReasonCode = "mcd_stage_completed"
	}
	return a
}
func (mcdOrderProfile) RunStage(ctx context.Context, r Runtime, run app.AgentRun, stage workflowStageContext, tools []app.ToolDefinition, obs []workflowObservation, budget *workflowRunBudget) workflowExecutionResult {
	return r.runMCDOrderStage(ctx, run, stage, tools, obs, budget)
}

func (mcdOrderProfile) DirectStage(*app.WorkflowState) bool                    { return true }
func (mcdOrderProfile) DirectStageArguments(*app.WorkflowState) map[string]any { return nil }
func (mcdOrderProfile) alwaysDirectWorkflowProfile()                           {}
func (mcdOrderProfile) AllowUnavailableTools() bool                            { return true }
