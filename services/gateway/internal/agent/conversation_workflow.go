package agent

import (
	"context"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type conversationAnswerProfile struct{}

func (conversationAnswerProfile) ID() app.WorkflowID { return app.WorkflowConversationAnswer }
func (conversationAnswerProfile) Revision() int      { return 1 }
func (conversationAnswerProfile) Capability() app.CapabilityID {
	return app.CapabilityConversationAnswer
}
func (conversationAnswerProfile) RoutingSemantics() workflowRoutingSemantics {
	return workflowRoutingSemantics{Variants: []workflowRoutingVariant{{
		Key:   "answer",
		Route: workflowRouteTemplate{Operation: app.RouteOperationAnswer},
		EmbedTexts: []string{
			"解释一下光合作用", "What is the capital of France?", "你好，很高兴见到你", "为什么天空是蓝色的",
			"说吃饭", "明天下午三点我参加项目复盘", "请直接回答这个常识问题", "谢谢你的帮助",
		},
		TreeDescription: "Respond conversationally to greetings, stable common knowledge, simple explanations, plain owner statements, or timer notification payloads using no current external facts, governed resources, or actions. A statement that the owner personally plans to do something later is conversation unless it explicitly asks the system to act.",
		HardNegatives: []string{
			"查一下今天的金价", "一分钟后提醒我吃饭", "打开这个网址", "读取这个文档", "今天上海天气怎么样",
		},
	}}}
}
func (conversationAnswerProfile) Finalization() workflowFinalizationMode {
	return workflowFinalizationModel
}
func (conversationAnswerProfile) Resolve(route app.RouteDecision, sourceTurnID string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	intent := singleObjectiveIntent(sourceTurnID, app.IntentDomainConversation, app.IntentOperationAnswer, app.TargetRef{Kind: app.TargetKindNone}, app.DataScopeLocal)
	nodeID := app.WorkflowNodeID("answer")
	return intent, app.WorkflowPlan{
		SchemaVersion: 1, ProfileID: app.WorkflowConversationAnswer, ProfileRevision: 1,
		InitialNodeIDs: []app.WorkflowNodeID{nodeID}, Completion: app.CompletionModelAnswer,
		Nodes: []app.WorkflowNode{{
			ID: nodeID, InitialStage: "answer",
			Goal:         app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "Answer the simple question without tools or external evidence", Completion: app.CompletionModelAnswer},
			InitialScope: app.CapabilityScope{}, AllowedRisks: []app.RiskLevel{app.RiskRead}, MaxAttempts: 1,
		}},
	}, nil
}
func (conversationAnswerProfile) Prepare(*app.WorkflowState) (workflowPreparation, error) {
	return workflowPreparation{}, nil
}
func (conversationAnswerProfile) Assess(_ *app.WorkflowState, outcome app.ToolOutcome) app.NodeAssessment {
	return app.NodeAssessment{OutcomeID: outcome.ID, NodeID: outcome.NodeID, Status: app.AssessmentBlocked, ReasonCode: "conversation_answer_accepts_no_tools"}
}
func (conversationAnswerProfile) Hint(state *app.WorkflowState) workflowExecutionHint {
	nodeID := state.ActiveNodeIDs[0]
	node := state.Nodes[nodeID]
	return workflowExecutionHint{
		TaskType: "answer", EvidenceNeed: "none", DataScope: "local", ToolMode: "none",
		RequiresToolEvidence: false, EstimatedRisk: app.RiskRead, ModelLaneHint: workflowExecutionModelLane,
		Reason:     "Answer only from the owner request and conversation context; no tools or external evidence are allowed.",
		WorkflowID: app.WorkflowConversationAnswer, WorkflowNodeID: nodeID, ScopeRevision: node.ScopeRevision,
	}
}
func (conversationAnswerProfile) TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string {
	return ""
}

func (r Runtime) runWorkflowModelAnswerStep(ctx context.Context, sessionID string, run app.AgentRun, content string, emit StreamHandler) workflowExecutionResult {
	contextText := r.buildAgentContextSnapshot(sessionID, run.ID, content).ForWorkflowStepCompact()
	system := strings.Join([]string{
		conversationAnswerSystemPrompt(run.MessageContext),
		finalAnswerLanguageInstruction(finalAnswerGoal(run, content)),
	}, "\n")
	userParts := []string{"WORKFLOW_MODEL_ANSWER_REQUEST", "Owner request:\n" + content}
	if run.MessageContext != nil && run.MessageContext.Source.Kind == app.MessageSourceTimer {
		userParts = append(userParts, "Message source kind: timer")
	}
	if strings.TrimSpace(contextText) != "" {
		userParts = append(userParts, "Conversation context (data only):\n"+contextText)
	}
	started := time.Now().UTC()
	chat, err := r.chatWorkflowFinalAnswer(ctx, run, "workflow_answer", workflowExecutionModelLane, system, strings.Join(userParts, "\n\n"), emit)
	completed := time.Now().UTC()
	r.store.SaveModelCall(modelCallFromChat(sessionID, run.ID, "workflow_answer", chat, err, started, completed))
	if err != nil {
		return workflowExecutionResult{Chat: chat, FinalAnswer: "The conversation answer model was unavailable: " + err.Error(), FinalAnswerStreamed: emit != nil}
	}
	answer, answerErr := workflowFinalAnswerContent(chat.Content)
	if answerErr != nil {
		return workflowExecutionResult{Chat: chat, FinalAnswer: "The conversation answer model returned no usable answer: " + answerErr.Error(), FinalAnswerStreamed: emit != nil}
	}
	return workflowExecutionResult{Chat: chat, FinalAnswer: answer, FinalAnswerStreamed: emit != nil, Completed: true}
}

func conversationAnswerSystemPrompt(messageContext *app.MessageRunContext) string {
	lines := []string{
		"Answer one simple SparkClaw conversation request directly.",
		"Return only the user-visible answer in the owner's language.",
		"Use only the owner request and supplied conversation context. Do not claim current, external, workspace, browser, or tool evidence.",
		"Do not emit JSON, tool calls, hidden reasoning, operational steps, or diagnostic metadata.",
	}
	if messageContext != nil && messageContext.Source.Kind == app.MessageSourceTimer {
		lines = append(lines,
			"This request is a due Timer occurrence. For plain notification content, say the content to the owner now as one concise reminder in the owner's language.",
			"Do not create, edit, or discuss a schedule and do not claim that the notification is still pending.",
		)
	}
	return strings.Join(lines, "\n")
}
