package agent

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const conversationPublishCandidateID = string(app.CapabilityConversationAnswer) + "#publish"

type conversationAnswerProfile struct{}

type conversationAnswerProfileV2 struct{ conversationAnswerProfile }
type conversationAnswerProfileV1 struct{ conversationAnswerProfileV2 }

func (conversationAnswerProfile) ID() app.WorkflowID { return app.WorkflowConversationAnswer }
func (conversationAnswerProfile) Revision() int      { return 3 }
func (conversationAnswerProfile) Capability() app.CapabilityID {
	return app.CapabilityConversationAnswer
}
func (conversationAnswerProfile) RoutingSemantics() workflowRoutingSemantics {
	return workflowRoutingSemantics{Variants: []workflowRoutingVariant{
		{
			Key:   "answer",
			Route: workflowRouteTemplate{Operation: app.RouteOperationAnswer},
			EmbedTexts: []string{
				"解释一下光合作用", "What is the capital of France?", "你好，很高兴见到你", "为什么天空是蓝色的",
				"说吃饭", "明天下午三点我参加项目复盘", "请直接回答这个常识问题", "谢谢你的帮助",
			},
			TreeDescription: "Respond conversationally to greetings, stable common knowledge, simple explanations, plain owner statements, or timer notification payloads using no current external facts, governed resources, or actions. A statement that the owner personally plans to do something later is conversation unless it explicitly asks the system to act.",
			HardNegatives: []string{
				"查一下今天的金价", "一分钟后提醒我吃饭", "打开这个网址", "读取这个文档", "今天上海天气怎么样", "发送这个文件",
			},
		},
		{
			Key:   "publish",
			Route: workflowRouteTemplate{Operation: app.RouteOperationPublish},
			EmbedTexts: []string{
				"发送这个文件", "把附件发出去", "转发这张图片", "Send this attachment", "Publish this message",
				"把这段文字作为消息发送", "投递这份演示文稿", "发送文件",
			},
			TreeDescription: "Publish the current owner-authored message as the ordinary message result. Preserve text-only content; when image, audio, or file parts exist, return only those ordered media parts without the command text. The destination is already carried separately by ReturnRoute and must not affect this business intent. Use only when the owner asks to send or publish the message parts themselves, not to inspect, summarize, edit, or transform an attachment.",
			HardNegatives: []string{
				"总结这个附件", "修改这份文档", "这张图片里有什么", "一分钟后发送这个文件", "为什么文件发送失败",
				"点击当前页面的发送按钮", "click Send on the current page",
			},
		},
	}}
}
func (conversationAnswerProfile) Finalization() workflowFinalizationMode {
	return workflowFinalizationModel
}
func (conversationAnswerProfile) Resolve(route app.RouteDecision, sourceTurnID string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	operation := app.IntentOperationAnswer
	completion := app.CompletionModelAnswer
	summary := "Answer the simple question without tools or external evidence"
	stage := "answer"
	output := app.OutputKindText
	if route.Slots.Operation == app.RouteOperationPublish {
		operation = app.IntentOperationPublish
		completion = app.CompletionMessage
		summary = "Publish the governed owner-authored message parts"
		stage = "publish_message"
		output = app.OutputKindMessage
	} else if route.Slots.Operation != app.RouteOperationAnswer {
		return app.IntentEnvelope{}, app.WorkflowPlan{}, errors.New("conversation workflow received an unsupported operation")
	}
	intent := singleObjectiveIntent(sourceTurnID, app.IntentDomainConversation, operation, app.TargetRef{Kind: app.TargetKindNone}, app.DataScopeLocal)
	intent.Objectives[0].Output = output
	detectNodeID := app.WorkflowNodeID("detect_response_media")
	answerNodeID := app.WorkflowNodeID("answer")
	return intent, app.WorkflowPlan{
		SchemaVersion: 1, ProfileID: app.WorkflowConversationAnswer, ProfileRevision: 3,
		InitialNodeIDs: []app.WorkflowNodeID{detectNodeID}, Completion: completion,
		Nodes: []app.WorkflowNode{
			{
				ID: detectNodeID, InitialStage: "detect_response_media",
				Goal:         app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "Freeze the response-media decision and governed workspace resources", Completion: app.CompletionDeterministic},
				InitialScope: app.CapabilityScope{}, AllowedRisks: []app.RiskLevel{app.RiskRead}, MaxAttempts: 1,
			}, {
				ID: answerNodeID, InitialStage: stage, DependsOn: []app.WorkflowNodeID{detectNodeID},
				Goal:         app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: summary, Completion: completion},
				InitialScope: app.CapabilityScope{}, AllowedRisks: []app.RiskLevel{app.RiskRead}, MaxAttempts: 1,
			},
		},
	}, nil
}

func (conversationAnswerProfileV2) Revision() int { return 2 }
func (conversationAnswerProfileV2) Resolve(route app.RouteDecision, sourceTurnID string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	operation := app.IntentOperationAnswer
	completion := app.CompletionModelAnswer
	summary := "Answer the simple question without tools or external evidence"
	stage := "answer"
	output := app.OutputKindText
	if route.Slots.Operation == app.RouteOperationPublish {
		operation = app.IntentOperationPublish
		completion = app.CompletionMessage
		summary = "Publish the governed owner-authored message parts"
		stage = "publish_message"
		output = app.OutputKindMessage
	} else if route.Slots.Operation != app.RouteOperationAnswer {
		return app.IntentEnvelope{}, app.WorkflowPlan{}, errors.New("conversation workflow received an unsupported operation")
	}
	intent := singleObjectiveIntent(sourceTurnID, app.IntentDomainConversation, operation, app.TargetRef{Kind: app.TargetKindNone}, app.DataScopeLocal)
	intent.Objectives[0].Output = output
	nodeID := app.WorkflowNodeID("answer")
	return intent, app.WorkflowPlan{
		SchemaVersion: 1, ProfileID: app.WorkflowConversationAnswer, ProfileRevision: 2,
		InitialNodeIDs: []app.WorkflowNodeID{nodeID}, Completion: completion,
		Nodes: []app.WorkflowNode{{
			ID: nodeID, InitialStage: stage,
			Goal:         app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: summary, Completion: completion},
			InitialScope: app.CapabilityScope{}, AllowedRisks: []app.RiskLevel{app.RiskRead}, MaxAttempts: 1,
		}},
	}, nil
}

func (conversationAnswerProfileV1) Revision() int { return 1 }
func (conversationAnswerProfileV1) RoutingSemantics() workflowRoutingSemantics {
	semantics := (conversationAnswerProfile{}).RoutingSemantics()
	semantics.Variants = semantics.Variants[:1]
	semantics.Variants[0].HardNegatives = semantics.Variants[0].HardNegatives[:5]
	return semantics
}
func (conversationAnswerProfileV1) Resolve(route app.RouteDecision, sourceTurnID string) (app.IntentEnvelope, app.WorkflowPlan, error) {
	if route.Slots.Operation != app.RouteOperationAnswer {
		return app.IntentEnvelope{}, app.WorkflowPlan{}, errors.New("conversation r1 only supports answer operations")
	}
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
func (conversationAnswerProfile) StageContext(state *app.WorkflowState) workflowStageContext {
	nodeID := state.ActiveNodeIDs[0]
	node := state.Nodes[nodeID]
	if state.Route.Slots.Operation == app.RouteOperationPublish {
		return workflowStageContext{
			TaskType: "publish", EvidenceNeed: "message_content", DataScope: "local", ToolMode: "none",
			RequiresToolEvidence: false, EstimatedRisk: app.RiskRead, ModelLaneHint: workflowExecutionModelLane,
			Reason:     "Publish only the governed owner-authored message parts; delivery destination and approval remain outside the business workflow.",
			WorkflowID: app.WorkflowConversationAnswer, WorkflowNodeID: nodeID, ScopeRevision: node.ScopeRevision,
		}
	}
	return workflowStageContext{
		TaskType: "answer", EvidenceNeed: "none", DataScope: "local", ToolMode: "none",
		RequiresToolEvidence: false, EstimatedRisk: app.RiskRead, ModelLaneHint: workflowExecutionModelLane,
		Reason:     "Answer only from the owner request and conversation context; no tools or external evidence are allowed.",
		WorkflowID: app.WorkflowConversationAnswer, WorkflowNodeID: nodeID, ScopeRevision: node.ScopeRevision,
	}
}
func (conversationAnswerProfile) TransitionInstruction(app.ToolOutcome, app.NodeAssessment) string {
	return ""
}

func (r Runtime) runWorkflowMessageContentStep(run app.AgentRun) workflowExecutionResult {
	if run.Workflow == nil || run.Workflow.Route.Slots.Operation != app.RouteOperationPublish || run.MessageContext == nil {
		return workflowExecutionResult{Halted: true, FinalAnswer: "The ordinary message workflow lost its normalized request content."}
	}
	if run.Workflow.Plan.ProfileRevision == 3 {
		return r.runConversationResponseContentStep(run)
	}
	content, err := r.governWorkflowRequestContent(run)
	if err != nil {
		return workflowExecutionResult{Halted: true, FinalAnswer: "The ordinary message could not be prepared for delivery: " + err.Error()}
	}
	run.MessageContext.RequestContent = content
	r.store.SaveRun(run)
	r.store.AddAudit(app.AuditEvent{
		SessionID: run.SessionID, RunID: run.ID, Actor: "workflow_dispatcher", Type: "workflow.message_content_governed",
		Summary: "Prepared normalized multipart message content for the shared result path",
		Fields:  map[string]any{"part_count": len(content.Parts), "content_kinds": messageContentKinds(content)},
	})
	return workflowExecutionResult{FinalAnswer: publishedMessageSummary(content), Completed: true}
}

func publishedMessageSummary(content app.MessageContent) string {
	for _, part := range content.Parts {
		if part.Kind == app.MessagePartText && strings.TrimSpace(part.Text) != "" {
			return strings.TrimSpace(part.Text)
		}
	}
	return "The multipart message is ready for delivery."
}

func messageContentKinds(content app.MessageContent) []string {
	kinds := []string{}
	for _, part := range content.Parts {
		kind := string(part.Kind)
		if kind != "" && !slices.Contains(kinds, kind) {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

func isOrdinaryMediaPublication(run app.AgentRun) bool {
	if run.Workflow == nil || run.MessageContext == nil ||
		run.Workflow.Plan.ProfileID != app.WorkflowConversationAnswer ||
		(run.Workflow.Plan.ProfileRevision != 2 && run.Workflow.Plan.ProfileRevision != 3) ||
		run.Workflow.Route.Slots.Operation != app.RouteOperationPublish ||
		len(run.MessageContext.RequestContent.Parts) == 0 {
		return false
	}
	for _, part := range run.MessageContext.RequestContent.Parts {
		if !isMediaMessagePart(part.Kind) {
			return false
		}
	}
	return true
}

func isMediaMessagePart(kind app.MessagePartKind) bool {
	switch kind {
	case app.MessagePartImage, app.MessagePartAudio, app.MessagePartFile:
		return true
	default:
		return false
	}
}

func (r Runtime) runWorkflowModelAnswerStep(ctx context.Context, sessionID string, run app.AgentRun, content string, emit StreamHandler) workflowExecutionResult {
	if run.Workflow != nil && run.Workflow.Plan.ProfileID == app.WorkflowConversationAnswer && run.Workflow.Plan.ProfileRevision == 3 && run.MessageContext != nil && run.MessageContext.ResponseMedia != nil {
		switch run.MessageContext.ResponseMedia.Status {
		case app.ResponseMediaClarify:
			return workflowExecutionResult{FinalAnswer: responseMediaClarification(), Completed: true}
		case app.ResponseMediaBlocked:
			return workflowExecutionResult{FinalAnswer: responseMediaBlockedMessage(run.MessageContext.ResponseMedia.ReasonCode), Completed: true}
		case app.ResponseMediaSelected:
			if err := r.revalidateFrozenResponseMedia(&run); err != nil {
				return workflowExecutionResult{Halted: true, FinalAnswer: "Blocked: response media changed after it was selected."}
			}
		}
	}
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
