package agent

import (
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/aichatexport"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type aiChatProfile struct{ provider string }

func (p aiChatProfile) ID() app.WorkflowID                   { return app.WorkflowID("ai_chat." + p.provider) }
func (p aiChatProfile) Capability() app.CapabilityID         { return app.CapabilityID(p.ID()) }
func (aiChatProfile) Revision() int                          { return 1 }
func (aiChatProfile) Finalization() workflowFinalizationMode { return workflowFinalizationGrounded }
func (p aiChatProfile) RoutingSemantics() workflowRoutingSemantics {
	return workflowRoutingSemantics{Variants: []workflowRoutingVariant{{Key: "read", Route: workflowRouteTemplate{Operation: app.RouteOperationRead}, EmbedTexts: []string{"导出 " + p.provider + " 平台已有对话并保存到工作区", "导出 " + p.provider + " 历史聊天的这条对话", "Export this " + p.provider + " conversation to workspace"}, TreeDescription: "Export one existing " + p.provider + " conversation using RevivalStack, then save original JSON in workspace. Requires an explicit conversation URL; clarify if missing. This is not public webpage summarization or sending a prompt.", HardNegatives: []string{"打开AI网站", "向AI提问", "总结本地文件", "搜索最新消息", "和我聊聊AI", "登录AI平台", "检测平台登录状态"}}}}
}
func (p aiChatProfile) Resolve(route app.RouteDecision, turn string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	if _, err := aichatexport.ConversationID(p.provider, route.Slots.TargetRef); err != nil {
		return app.IntentEnvelope{}, app.WorkflowPlan{}, err
	}
	intent := singleObjectiveIntent(turn, app.IntentDomainWeb, app.IntentOperationRead, app.TargetRef{Kind: app.TargetKindExplicitURL, Ref: route.Slots.TargetRef}, app.DataScopeLocal)
	return intent, app.WorkflowPlan{SchemaVersion: 1, ProfileID: p.ID(), ProfileRevision: 1, InitialNodeIDs: []app.WorkflowNodeID{"export"}, Completion: app.CompletionEvidence, ResultProjection: app.WorkflowResultOutputsOnly, Nodes: []app.WorkflowNode{{ID: "export", InitialStage: "capture", Goal: app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "Save original RevivalStack JSON and return its workspace receipt", Completion: app.CompletionEvidence}, InitialScope: app.CapabilityScope{Requirements: []app.CapabilityRequirement{{Name: "ai_chat.export"}}}, ArgumentBindings: []app.ArgumentBinding{{Capability: "ai_chat.export", Argument: "url", ResourceKind: "url", Source: app.ArgumentBindingRouteSlot, SourceKey: "target_ref"}}, AllowedRisks: []app.RiskLevel{app.RiskDraft}, MaxAttempts: 1}}}, nil
}
func (aiChatProfile) Prepare(*app.WorkflowState) (workflowPreparation, error) {
	return workflowPreparation{}, nil
}
func (aiChatProfile) DirectStage(*app.WorkflowState) bool { return true }
func (aiChatProfile) alwaysDirectWorkflowProfile()        {}
func (p aiChatProfile) DirectStageArguments(*app.WorkflowState) map[string]any {
	return map[string]any{"provider": p.provider}
}
func (aiChatProfile) Assess(_ *app.WorkflowState, out app.ToolOutcome) app.NodeAssessment {
	a := baseNodeAssessment(out)
	a.Status = app.AssessmentBlocked
	a.ReasonCode = "ai_chat_export_failed"
	if out.Status == app.ToolCallStatusCompleted && len(out.Refs) > 0 {
		a.Status = app.AssessmentComplete
		a.ReasonCode = "ai_chat_saved"
		a.SelectedRefs = out.Refs
	}
	return a
}
func (aiChatProfile) StageContext(s *app.WorkflowState) workflowStageContext {
	return workflowStageContextForState(s, "export", "ai_conversation", "private", "", "Save the original file and report its path and coverage. Do not summarize or create memories.")
}
func (aiChatProfile) TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string {
	return "Report only the verified workspace receipt and coverage limitations."
}
