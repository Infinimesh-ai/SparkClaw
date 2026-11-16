package agent

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/app"

type mcdCartProfile struct{ quote, purchase bool }

func (p mcdCartProfile) ID() app.WorkflowID {
	if p.purchase {
		return "dining.mcd_purchase"
	}
	if p.quote {
		return "dining.mcd_quote"
	}
	return "dining.mcd_cart"
}
func (p mcdCartProfile) Capability() app.CapabilityID         { return app.CapabilityID(p.ID()) }
func (mcdCartProfile) Revision() int                          { return 1 }
func (mcdCartProfile) Finalization() workflowFinalizationMode { return workflowFinalizationModel }
func (p mcdCartProfile) RoutingSemantics() workflowRoutingSemantics {
	description := "Apply an explicitly selected McDonald's cart change: exact simple/configured products and benefits, deliberate removal, or explicit pickup method. This cannot submit an order or pay."
	examples := []string{"把我选好的麦辣鸡腿堡加入麦当劳购物车", "按这份餐单修改我的麦当劳购物车", "Clear my McDonald's cart after reviewing its contents"}
	label := workflowClarifyLabel{ZH: "修改麦当劳购物车", EN: "Change my McDonald's cart"}
	if p.quote {
		description = "Prepare and review a complete official McDonald's quote for the exact cart, store, pickup time and pickup method, and validate it on the originating Mac. Selected products may be added only with explicit cart intent and Policy approval. No order or payment."
		examples = []string{"报价这份麦当劳套餐，先不要付款", "帮我选好麦当劳餐单并查看到店取餐价格", "Prepare a McDonald's pickup quote for my selected meal"}
		label = workflowClarifyLabel{ZH: "准备麦当劳报价", EN: "Prepare a McDonald's quote"}
	}
	if p.purchase {
		description = "Purchase one explicitly selected McDonald's meal with the McDonald's wallet on the originating Mac. Read exact products and pickup details, prepare the complete quote, obtain immutable approval, and commit once. Any unknown write requires original-operation status only."
		examples = []string{"下单这份麦当劳餐单并用麦钱包付款", "Pay for my selected McDonald's meal with my McDonald's wallet"}
		label = workflowClarifyLabel{ZH: "麦当劳下单与麦钱包付款", EN: "Purchase with McDonald's wallet"}
	}
	negatives := []string{"提交麦当劳订单并付款", "Pay with my McDonald's wallet", "研究麦当劳历史"}
	if p.purchase {
		negatives = []string{"先报价不要下单付款", "只看看麦当劳菜单", "查询原来订单的结果，不要重试"}
	}
	return workflowRoutingSemantics{Variants: []workflowRoutingVariant{{Key: "select", ClarifyLabel: label, Route: workflowRouteTemplate{Operation: app.RouteOperationDraft}, EmbedTexts: examples, TreeDescription: description, HardNegatives: negatives}}}
}
func (p mcdCartProfile) Resolve(_ app.RouteDecision, turn string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	intent := singleObjectiveIntent(turn, app.IntentDomainWeb, app.IntentOperationDraft, app.TargetRef{Kind: app.TargetKindNone}, app.DataScopeLocal)
	scope := app.CapabilityScope{Requirements: []app.CapabilityRequirement{{Name: app.ToolCapabilityMCDSelection}, {Name: "mcd.cart.read"}, {Name: "mcd.cart.write"}}}
	if p.quote || p.purchase {
		scope.Requirements = append(scope.Requirements, app.CapabilityRequirement{Name: "mcd.cart.quote"})
	}
	if p.purchase {
		scope.Requirements = append(scope.Requirements, app.CapabilityRequirement{Name: "mcd.purchase.commit"}, app.CapabilityRequirement{Name: "mcd.purchase.status"})
	}
	risks := []app.RiskLevel{app.RiskRead, app.RiskReversible}
	if p.purchase {
		risks = append(risks, app.RiskDangerous)
	}
	goal := "Use exact official evidence to complete the owner's selected cart or quote without purchasing"
	if p.purchase {
		goal = "Prepare the exact meal and execute one immutable-approved Mac wallet purchase; reconcile unknown results without retries"
	}
	id := app.WorkflowNodeID("mcd_selection")
	return intent, app.WorkflowPlan{SchemaVersion: 1, ProfileID: p.ID(), ProfileRevision: 1, InitialNodeIDs: []app.WorkflowNodeID{id}, Completion: app.CompletionEvidence, Nodes: []app.WorkflowNode{{ID: id, InitialStage: "selection", Goal: app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: goal, Completion: app.CompletionEvidence}, InitialScope: scope, Transitions: []app.ScopeTransition{{ID: "mcd_more_selection_evidence", NextStage: "selection", On: app.TransitionPredicate{Assessments: []app.AssessmentStatus{app.AssessmentNeedsMoreEvidence}}, Replace: &scope, MaxActivations: 7}}, AllowedRisks: risks, MaxAttempts: 8}}}, nil
}
func (mcdCartProfile) Prepare(*app.WorkflowState) (workflowPreparation, error) {
	return workflowPreparation{}, nil
}
func (p mcdCartProfile) Assess(_ *app.WorkflowState, outcome app.ToolOutcome) app.NodeAssessment {
	a := baseNodeAssessment(outcome)
	if outcome.Status != app.ToolCallStatusCompleted && outcome.Status != app.ToolCallStatusCompletedAfterApproval {
		a.Status = app.AssessmentBlocked
		a.ReasonCode = "mcd_requires_recovery"
		return a
	}
	if p.purchase && (outcome.Tool == "mcd.purchase.commit" || outcome.Tool == "mcd.purchase.status") || !p.purchase && (p.quote && outcome.Tool == "mcd.quote.prepare" || !p.quote && outcome.Tool == "mcd.cart.write") {
		a.Status = app.AssessmentComplete
		a.ReasonCode = "mcd_selection_completed"
		return a
	}
	a.Status = app.AssessmentNeedsMoreEvidence
	a.ReasonCode = "mcd_exact_evidence_required"
	return a
}
func (p mcdCartProfile) StageContext(state *app.WorkflowState) workflowStageContext {
	if p.purchase {
		return workflowStageContextForState(state, "purchase", "mcd", "local", "", "Use exact official cart and pickup evidence. Never invent store/time/items, add extras or clear without explicit intent. Prepare the complete quote on the original Mac. Then request mcd.purchase.commit: Runtime replaces its arguments with the exact prepared content and requires the existing approval flow. Commit once with ARCHCARD only. Unknown results are not success: query mcd.purchase.status, never retry or use a new purchase key. No payment channel substitution.")
	}

	return workflowStageContextForState(state, "selection", "mcd", "local", "", "Read exact products, benefits and pickup context first. Never invent identifiers, configuration, store, time, method or amount ceiling. Cart writes require the user's actual change intent and existing approval flow. Never clear to make room without removal intent. Snapshot before clear/pickup switch. Use source_quote_ref from the pickup result for quote import. Stop on unknown and query the original operation; do not replace keys or retry writes. No orders or payments.")
}
func (mcdCartProfile) TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string {
	return "Continue only the original selection intent using returned official evidence; stop on unresolved writes."
}
