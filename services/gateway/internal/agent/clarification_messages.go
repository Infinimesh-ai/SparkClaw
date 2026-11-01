package agent

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/app"

// Only owner-facing sentences reach the conversation. Diagnostic reason codes
// stay in the route and audit records.
func routingOwnerMessage(reason string, chinese, timer bool) (string, bool) {
	en, zh := "", ""
	switch reason {
	case "top_candidates_compete", "required_grounded_target_missing", "required_grounded_target_ambiguous", "document_operation_variant_conflicts_with_format":
		en = "Please specify what you would like me to do and which file or page I should use."
		zh = "请说明你希望怎样处理，以及要使用哪个文件或页面。"
	case "clarification_binding_changed":
		en = "That choice no longer matches the original request. Please restate what you want to do."
		zh = "这个选项已无法对应原来的请求，请重新说明你希望怎样处理。"
	case "clarification_already_used":
		en = "That choice has already been submitted. Check the original task for its result."
		zh = "这个选择已经提交过，请查看原任务的结果。"
	case "delivery_confirmation_required", "delivery_object_missing":
		en = "Please specify the content to deliver and its destination."
		zh = "请说明要交付的内容以及接收位置。"
	case "delivery_split_unavailable", "external_delivery_requires_healthy_semantic_pipeline":
		en = "I could not safely determine what to send. Please restate the content and recipient."
		zh = "我暂时无法确定要发送的内容，请重新说明内容和收件人。"
	case "deferred_file_unavailable":
		en = "This conversation file cannot be retained for a future task. Use a persistent file available to the scheduled task."
		zh = "这个会话文件无法保留到将来的任务，请使用定时任务届时能够访问的持久文件。"
	case "deferred_request_requires_schedule":
		en = "Please specify a supported schedule and the action to perform when it is due."
		zh = "请说明执行时间，以及到时需要完成的操作。"
	case "publication_unavailable":
		en = "Message delivery is unavailable for this request source."
		zh = "当前请求来源暂不支持消息交付。"
	case "mutation_requires_healthy_semantic_pipeline", "semantic_channels_unavailable":
		en = "I could not reliably determine this action. Please try again later."
		zh = "我暂时无法可靠判断这项操作，请稍后重试。"
	default:
		return "", false
	}
	if timer {
		en = "The scheduled task could not be completed. Please review its content."
		zh = "这条定时任务未能完成，请检查任务内容。"
	}
	if chinese {
		return zh, true
	}
	return en, true
}

func ownerWorkflowFailureMessage(code workflowFailureCode, run app.AgentRun, goal string) string {
	if code == workflowFailureRequiredToolNotCalled && run.Workflow != nil && run.Workflow.Plan.ProfileID == app.WorkflowDocumentEdit {
		if chineseRequest(goal) {
			return "这次未能生成有效的文档修改，原文件保持不变。请说明要修改的段落，或给出替换前后的文字。"
		}
		return "I could not produce a valid document edit; the original file is unchanged. Please specify the passage to edit or provide the text to find and its replacement."
	}
	return publicWorkflowFailureMessage(code)
}
