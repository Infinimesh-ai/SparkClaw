package agent

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/app"

type mcdStatusProfile struct{ mcdReadProfile }

func (mcdStatusProfile) ID() app.WorkflowID           { return "dining.mcd_status" }
func (mcdStatusProfile) Capability() app.CapabilityID { return "dining.mcd_status" }
func (mcdStatusProfile) RoutingSemantics() workflowRoutingSemantics {
	return workflowRoutingSemantics{Variants: []workflowRoutingVariant{{Key: "status", ClarifyLabel: workflowClarifyLabel{ZH: "核对麦当劳原订单状态", EN: "Check the original McDonald's purchase"}, Route: workflowRouteTemplate{Operation: app.RouteOperationRead}, EmbedTexts: []string{"查询麦当劳原订单付款结果，不要再次付款", "麦当劳上次报价失败了，恢复原操作状态", "Check my original McDonald's order and pickup code"}, TreeDescription: "Reconcile the original McDonald's preparation or unknown purchase with the originating Mac; otherwise read its original order journal. This never submits orders or payment.", HardNegatives: []string{"创建新订单并付款", "只查询麦当劳菜单"}}}}
}
func (p mcdStatusProfile) Resolve(route app.RouteDecision, turn string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	intent, plan, err := p.mcdReadProfile.Resolve(route, turn)
	plan.ProfileID = p.ID()
	plan.Nodes[0].InitialScope = app.CapabilityScope{Requirements: []app.CapabilityRequirement{{Name: "mcd.purchase.status"}}}
	plan.Nodes[0].Goal.Summary = "Query the original purchase or abort proven undispatched preparation without reissuing writes"
	return intent, plan, err
}
func (mcdStatusProfile) StageContext(state *app.WorkflowState) workflowStageContext {
	return workflowStageContextForState(state, "status", "mcd", "local", "", "Use only mcd.purchase.status. It resolves original durable operations or lists local orders. Do not describe local history as fresh official verification. Unknown or not_found never means no charge and never permits a retry.")
}
