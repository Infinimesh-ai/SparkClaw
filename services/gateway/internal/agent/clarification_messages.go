package agent

import (
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
)

// Only owner-facing sentences reach the conversation. Diagnostic reason codes
// stay in the route and audit records.

// ownerFailureClass tells the owner what kind of failure stopped a request, so
// a missing capability is never presented as a temporary outage or as a
// request for more detail, and the reverse.
type ownerFailureClass string

const (
	// failureUnsupported: the request asks for something SparkClaw does not do
	// here; retrying or rephrasing will not help.
	failureUnsupported ownerFailureClass = "unsupported"
	// failureUnavailable: the capability exists but cannot run right now.
	failureUnavailable ownerFailureClass = "unavailable"
	// failureNeedsInput: the owner must clarify, choose, attach or set up
	// something before the request can proceed.
	failureNeedsInput ownerFailureClass = "needs_input"
)

type ownerFailureMessage struct {
	class   ownerFailureClass
	zh, en  string
	timerZH string
	timerEN string
}

func (m ownerFailureMessage) text(chinese, timer bool) string {
	switch {
	case timer && chinese && m.timerZH != "":
		return m.timerZH
	case timer && !chinese && m.timerEN != "":
		return m.timerEN
	case timer && chinese:
		return "这条定时任务未能执行：" + m.zh
	case timer:
		return "The scheduled task did not run: " + m.en
	case chinese:
		return m.zh
	default:
		return m.en
	}
}

// Route reasons may carry a diagnostic after the first colon
// ("document_format_read_only: document format \"md\" is read-only"); only the
// stable code before it selects the owner message.
var routingFailureMessages = map[string]ownerFailureMessage{
	// Unsupported: no capability serves this request in this place.
	"document_operation_variant_conflicts_with_format": {class: failureUnsupported,
		zh: "这种文件格式不支持这项操作。",
		en: "This file format does not support the requested operation."},
	"document_format_read_only": {class: failureUnsupported,
		zh: "这种文件格式只支持读取，不支持修改。",
		en: "This file format can be read but is not supported for editing."},
	"document_format_unrecognized": {class: failureUnsupported,
		zh: "不支持这个文件的格式，或文件内容与扩展名不一致。",
		en: "This file format is not supported, or the file content does not match its extension."},
	"document_path_outside_workspace": {class: failureUnsupported,
		zh: "只支持处理工作区内的普通文件，不支持工作区以外的路径或符号链接。",
		en: "Paths outside the workspace and symbolic links are not supported; use a regular file inside the workspace."},
	"document_preflight_not_authorized": {class: failureUnsupported,
		zh: "当前入口不支持读取这类文件。",
		en: "Reading this kind of file is not supported from this entry point."},
	"pptx_edit_target_unsupported": {class: failureUnsupported,
		zh: "演示文稿目前不支持修改智能图形、动画、图表数据、母版或宏。",
		en: "Editing SmartArt, animations, chart data, slide masters or macros in presentations is not supported."},
	"localmind_text_only_input_required": {class: failureUnsupported,
		zh: "LocalMind 目前只支持文字请求，不支持附带的图片或文件。",
		en: "LocalMind supports text requests only; attached images or files are not supported."},
	"deferred_file_unavailable": {class: failureUnsupported,
		zh: "定时任务不支持使用会话里的临时文件，请改用定时任务执行时仍能访问的工作区文件。",
		en: "Scheduled tasks do not support temporary conversation files. Use a workspace file the task can still access when it runs."},
	"publication_unavailable": {class: failureUnsupported,
		zh: "当前请求来源不支持发送消息。",
		en: "Sending messages is not supported from this request source."},
	"email_operation_unsupported": {class: failureUnsupported,
		zh: "邮件目前只支持读取和发送，不支持这项操作。",
		en: "Email supports reading and sending only; this operation is not supported."},
	"email_unsupported_here": {class: failureUnsupported,
		zh: "当前入口不支持处理邮件。",
		en: "Email is not supported from this entry point."},

	// Unavailable: the capability exists but cannot run right now. A model
	// outage says so directly rather than naming the pipeline stage it hit.
	"model_unavailable": {class: failureUnavailable,
		zh: "模型暂时不可用，请稍后重试。",
		en: "The model is temporarily unavailable. Please try again later."},
	"semantic_channels_unavailable": {class: failureUnavailable,
		zh: "模型暂时不可用，请稍后重试。",
		en: "The model is temporarily unavailable. Please try again later."},
	"semantic_degraded_uncertain": {class: failureUnavailable,
		zh: "模型暂时不可用，无法确定如何处理这条请求，请稍后重试。",
		en: "The model is temporarily unavailable, so this request cannot be understood reliably. Please try again later."},
	"mutation_requires_healthy_semantic_pipeline": {class: failureUnavailable,
		zh: "模型暂时不可用，为避免误操作，暂不执行会修改内容的操作，请稍后重试。",
		en: "The model is temporarily unavailable, so actions that change content are paused to avoid mistakes. Please try again later."},
	"external_delivery_requires_healthy_semantic_pipeline": {class: failureUnavailable,
		zh: "模型暂时不可用，为避免发错，暂不执行发送，请稍后重试。",
		en: "The model is temporarily unavailable, so sending is paused to avoid mistakes. Please try again later."},
	"routing_failed": {class: failureUnavailable,
		zh: "暂时无法处理这条请求，请稍后重试。",
		en: "This request cannot be processed right now. Please try again later."},
	"delivery_target_resolution_failed": {class: failureUnavailable,
		zh: "暂时无法确定发送目标，请稍后重试。",
		en: "The delivery destination cannot be resolved right now. Please try again later."},
	"delivery_endpoint_unavailable": {class: failureUnavailable,
		zh: "要发送到的账号或会话暂时不可用，请检查它的连接状态后再试。",
		en: "The destination account or chat is unavailable right now. Check its connection and try again."},
	"document_workspace_unavailable": {class: failureUnavailable,
		zh: "工作区暂时不可用，请稍后重试。",
		en: "The workspace is unavailable right now. Please try again later."},
	"document_preflight_failed": {class: failureUnavailable,
		zh: "暂时无法读取这个文件，请稍后重试。",
		en: "This file cannot be read right now. Please try again later."},
	"email_admission_unavailable": {class: failureUnavailable,
		zh: "暂时无法检查邮箱状态，请稍后重试。",
		en: "The mailbox status cannot be checked right now. Please try again later."},
	string(app.ToolErrorEmailProviderUnavailable): {class: failureUnavailable,
		zh: "邮箱暂时无法连接，请稍后重试。",
		en: "The mailbox cannot be reached right now. Please try again later."},
	string(app.ToolErrorEmailScriptTimeout): {class: failureUnavailable,
		zh: "邮箱暂时没有响应，请稍后重试。",
		en: "The mailbox is not responding right now. Please try again later."},
	string(app.ToolErrorEmailBrowserBusy): {class: failureUnavailable,
		zh: "浏览器暂时在执行其他邮件操作，请稍后重试。",
		en: "The browser is busy with another email operation. Please try again later."},
	string(app.ToolErrorEmailBrowserCleanupPending): {class: failureUnavailable,
		zh: "浏览器暂时还在清理上一次邮件操作，请稍后重试。",
		en: "The browser is still cleaning up the previous email operation. Please try again later."},
	string(app.ToolErrorEmailPageContractChanged): {class: failureUnavailable,
		zh: "邮箱网页版有变化，暂时无法自动操作，需要更新适配后才能继续使用。",
		en: "The webmail page has changed, so it cannot be operated automatically for now; the adapter needs an update."},

	// Needs input: the owner can unblock the request.
	"semantic_coverage_low": {class: failureNeedsInput,
		zh:      "我不确定你希望如何处理，请再说明一下你的需求。",
		en:      "I am not sure how you would like me to proceed. Please clarify your request.",
		timerZH: "这条定时任务的内容无法确定如何执行，请修改任务后再试。",
		timerEN: "The scheduled request could not be understood. Please revise the schedule."},
	"explicit_localmind_assignment_required": {class: failureNeedsInput,
		zh: "我不确定你希望如何处理。如果要交给 LocalMind，请在请求里明确写出 LocalMind。",
		en: "I am not sure how you would like me to proceed. To hand this to LocalMind, please name LocalMind explicitly in your request."},
	"top_candidates_compete": {class: failureNeedsInput,
		zh:      "这条请求有几种可能的理解，请具体说明你想让我做什么。",
		en:      "This request could mean several things. Please say specifically what you want me to do.",
		timerZH: "这条定时任务的内容有几种可能的理解，请把任务改得更具体后再试。",
		timerEN: "The scheduled task could mean several things. Please make it more specific."},
	"required_grounded_target_missing": {class: failureNeedsInput,
		zh: "请告诉我要处理哪个文件或页面。",
		en: "Please tell me which file or page to use."},
	"required_grounded_target_ambiguous": {class: failureNeedsInput,
		zh: "有多个文件或页面都可能是你要处理的，请指明是哪一个。",
		en: "Several files or pages could match. Please say which one to use."},
	"pptx_edit_scope_unspecified": {class: failureNeedsInput,
		zh: "请说明要修改演示文稿的哪一页，或给出要替换的原文和新文字。",
		en: "Please say which slide to edit, or give the exact text to find and its replacement."},
	"document_path_unavailable": {class: failureNeedsInput,
		zh: "找不到要处理的文件，请确认文件名和位置后重试。",
		en: "The file could not be found. Please check its name and location and try again."},
	"conversation_file_unavailable": {class: failureNeedsInput,
		zh: "之前的会话文件已失效，请重新附加文件后再试。",
		en: "The previous conversation file is no longer available. Please attach it again and retry."},
	"clarification_binding_changed": {class: failureNeedsInput,
		zh: "这个选项已无法对应原来的请求，请重新说明你希望怎样处理。",
		en: "That choice no longer matches the original request. Please restate what you want to do."},
	"clarification_already_used": {class: failureNeedsInput,
		zh: "这个选择已经提交过，请查看原任务的结果。",
		en: "That choice has already been submitted. Check the original task for its result."},
	"delivery_confirmation_required": {class: failureNeedsInput,
		zh: "请说明要交付的内容以及接收位置。",
		en: "Please specify the content to deliver and its destination."},
	"delivery_object_missing": {class: failureNeedsInput,
		zh: "请说明要发送什么内容。",
		en: "Please specify what to send."},
	"delivery_split_unavailable": {class: failureNeedsInput,
		zh: "我无法确定要发送的内容和收件人，请重新说明。",
		en: "I could not determine what to send and to whom. Please restate the content and recipient."},
	"delivery_channel_required": {class: failureNeedsInput,
		zh: "请说明要通过哪个软件或渠道发送。",
		en: "Please say which app or channel to send through."},
	"delivery_recipient_required": {class: failureNeedsInput,
		zh: "请说明要发给谁。",
		en: "Please say who to send it to."},
	"delivery_recipient_ambiguous": {class: failureNeedsInput,
		zh: "找到了多个可能的收件人，请指明具体的账号或会话。",
		en: "Several recipients could match. Please name the exact account or chat."},
	"deferred_request_requires_schedule": {class: failureNeedsInput,
		zh: "请说明执行时间，以及到时需要完成的操作。",
		en: "Please specify a supported schedule and the action to perform when it is due."},
	string(app.ToolErrorEmailNotConfigured): {class: failureNeedsInput,
		zh: "还没有可用的邮箱，请先在设置中启用并登录邮箱。",
		en: "No mailbox is set up yet. Enable and sign in to a mailbox in Settings first."},
	string(app.ToolErrorEmailLoginRequired): {class: failureNeedsInput,
		zh: "邮箱需要重新登录，请先在设置中登录后再试。",
		en: "The mailbox needs you to sign in again. Sign in from Settings and retry."},
	string(app.ToolErrorEmailAccountAmbiguous): {class: failureNeedsInput,
		zh: "请说明要使用哪个邮箱。",
		en: "Please say which mailbox to use."},
}

var (
	routingNeedsInputFallback = ownerFailureMessage{class: failureNeedsInput,
		zh: "请再具体说明一下你的需求。",
		en: "Please describe your request in more detail."}
	routingUnavailableFallback = routingFailureMessages["routing_failed"]
)

// routingFailureReason names a routing error by its cause; a model outage is
// reported as such instead of as a generic routing failure.
func routingFailureReason(err error) string {
	if modelrouter.IsUnavailable(err) {
		return "model_unavailable: " + err.Error()
	}
	return "routing_failed: " + err.Error()
}

func routeReasonCode(reason string) string {
	code, _, _ := strings.Cut(strings.TrimSpace(reason), ":")
	return strings.TrimSpace(code)
}

// routingFailure returns the owner message for a route that stopped before
// any workflow ran. Every route resolves to a message: an unmapped reason falls
// back by route status, never to the raw diagnostic.
func routingFailure(route app.RouteDecision) ownerFailureMessage {
	if message, ok := groundedTargetFailure(route.Reason); ok {
		return message
	}
	if message, ok := routingFailureMessages[routeReasonCode(route.Reason)]; ok {
		return message
	}
	if route.Status == app.RouteClarify || route.Status == app.RouteUnmatched {
		return routingNeedsInputFallback
	}
	return routingUnavailableFallback
}

// groundedTargetFailure asks for the kind of target the capability accepts
// ("required_grounded_target_missing: location" asks for a place, not a file).
func groundedTargetFailure(reason string) (ownerFailureMessage, bool) {
	code, kinds, _ := strings.Cut(strings.TrimSpace(reason), ":")
	code = strings.TrimSpace(code)
	if code != "required_grounded_target_missing" && code != "required_grounded_target_ambiguous" {
		return ownerFailureMessage{}, false
	}
	missing := code == "required_grounded_target_missing"
	kind := ""
	for _, candidate := range strings.Split(kinds, ",") {
		switch strings.TrimSpace(candidate) {
		case string(app.TargetKindLocation):
			candidate = "location"
		case string(app.TargetKindWorkspacePath):
			candidate = "file"
		case "url", string(app.TargetKindExplicitURL), string(app.TargetKindBrowserCurrentTab), string(app.TargetKindPublicNamedTarget):
			candidate = "page"
		case string(app.TargetKindLocalMindTask):
			candidate = "localmind_task"
		default:
			candidate = ""
		}
		if candidate == "" || kind != "" && kind != candidate {
			kind = "mixed"
			break
		}
		kind = candidate
	}
	targets := map[string][2]ownerFailureMessage{
		"location": {
			{class: failureNeedsInput, zh: "请告诉我要查询哪个城市或地区。", en: "Please tell me which city or area to check."},
			{class: failureNeedsInput, zh: "有多个地点都可能是你要查询的，请指明是哪一个。", en: "Several places could match. Please say which one to check."},
		},
		"file": {
			{class: failureNeedsInput, zh: "请告诉我要处理哪个文件。", en: "Please tell me which file to use."},
			{class: failureNeedsInput, zh: "有多个文件都可能是你要处理的，请指明是哪一个。", en: "Several files could match. Please say which one to use."},
		},
		"page": {
			{class: failureNeedsInput, zh: "请告诉我要打开或处理哪个网页。", en: "Please tell me which web page to open or use."},
			{class: failureNeedsInput, zh: "有多个网页都可能是你要处理的，请指明是哪一个。", en: "Several web pages could match. Please say which one to use."},
		},
		"localmind_task": {
			{class: failureNeedsInput, zh: "请告诉我要查看哪个 LocalMind 任务。", en: "Please tell me which LocalMind task to use."},
			{class: failureNeedsInput, zh: "有多个 LocalMind 任务都可能是你要查看的，请指明是哪一个。", en: "Several LocalMind tasks could match. Please say which one to use."},
		},
	}
	pair, ok := targets[kind]
	if !ok {
		return routingFailureMessages[code], true
	}
	if missing {
		return pair[0], true
	}
	return pair[1], true
}

var workflowFailureMessages = map[workflowFailureCode]ownerFailureMessage{
	workflowFailurePromptFixedOversized: {class: failureUnsupported,
		zh: "当前配置的模型上下文容量不足，不支持处理这类请求，需要换用上下文更大的模型。",
		en: "The configured model's context window is too small; this kind of request is not supported until a larger model is configured."},

	workflowFailureModelUnavailable: {class: failureUnavailable,
		zh: "模型暂时不可用，请稍后重试。",
		en: "The model is temporarily unavailable. Please try again later."},
	workflowFailureConversationOutputInvalid: {class: failureUnavailable,
		zh: "模型暂时不可用：这次没有返回可用的回答，请稍后重试。",
		en: "The model is temporarily unavailable: it returned no usable answer this time. Please try again later."},
	workflowFailureEvidenceUnavailable: {class: failureUnavailable,
		zh: "完成这项任务所需的资料暂时取不到，请稍后重试。",
		en: "The information this task needs is unavailable right now. Please try again later."},
	workflowFailureRequiredToolNotCalled: {class: failureUnavailable,
		zh: "这次处理没有执行到所需的操作，暂时没能完成，请稍后重试，或把要求说得更具体。",
		en: "This attempt did not perform the required action. Please try again later, or make the request more specific."},
	workflowFailureToolOutsideActiveScope: {class: failureUnavailable,
		zh: "这次处理尝试了当前任务不允许的操作，已停止且没有做任何改动，请稍后重试，或把要求说得更具体。",
		en: "This attempt tried an action outside the current task and was stopped without changes. Please try again later, or make the request more specific."},
	workflowFailureSemanticPreflight: {class: failureUnavailable,
		zh: "执行前的检查暂时没有通过，操作没有执行，请稍后重试，或补充更具体的要求。",
		en: "The pre-execution check did not pass and nothing was executed. Please try again later, or add more specific requirements."},
	workflowFailureSemanticOutputInvalid: {class: failureUnavailable,
		zh: "这次生成的操作不符合要求，已停止执行，请稍后重试，或补充更具体的要求。",
		en: "The generated action did not meet its requirements and was not executed. Please try again later, or add more specific requirements."},
	workflowFailureDirectToolInvocationInvalid: {class: failureUnavailable,
		zh: "这项功能的执行配置有误，暂时无法使用。",
		en: "This feature's execution configuration is invalid, so it is unavailable for now."},
	workflowFailureSetup: {class: failureUnavailable,
		zh: "这项功能暂时无法启动，请稍后重试。",
		en: "This feature cannot start right now. Please try again later."},
	workflowFailureOutcomeInvalid: {class: failureUnavailable,
		zh: "暂时无法确认这次操作的结果，请先检查实际结果，再决定是否重试。",
		en: "The result of this action cannot be confirmed right now. Check the actual result before deciding whether to retry."},
	workflowFailureTransitionFailed: {class: failureUnavailable,
		zh: "暂时无法记录这次操作的结果，任务已停止，请先检查实际结果，再决定是否重试。",
		en: "The result of this action cannot be recorded right now, so the task stopped. Check the actual result before deciding whether to retry."},
	workflowFailureFinalizationFailed: {class: failureUnavailable,
		zh: "任务已执行，但结果暂时无法展示，请先检查实际结果，不要直接重复执行。",
		en: "The task ran, but its result cannot be shown right now. Check the actual result instead of running it again."},

	workflowFailureObservationReadLimit: {class: failureNeedsInput,
		zh: "这项任务需要读取的内容超过了单次处理的上限，请缩小范围后重试。",
		en: "This task needs to read more than a single run allows. Please narrow the scope and retry."},
	workflowFailureStateInvalid: {class: failureNeedsInput,
		zh: "这项任务保存的进度已失效，无法继续，请重新发起请求。",
		en: "This task's saved progress is no longer valid. Please send the request again."},
	workflowFailureMessageContentInvalid: {class: failureNeedsInput,
		zh: "要发送的内容无法整理成可发送的消息，请检查内容后重试。",
		en: "The content could not be prepared as a message. Please check it and retry."},
}

var workflowFailureFallback = ownerFailureMessage{class: failureUnavailable,
	zh: "这次处理暂时没能完成，请稍后重试。",
	en: "This request could not be completed right now. Please try again later."}

func workflowFailure(code workflowFailureCode) ownerFailureMessage {
	if message, ok := workflowFailureMessages[code]; ok {
		return message
	}
	return workflowFailureFallback
}

func ownerWorkflowFailureMessage(code workflowFailureCode, run app.AgentRun, goal string) string {
	chinese := chineseRequest(goal)
	if code == workflowFailureRequiredToolNotCalled && run.Workflow != nil && run.Workflow.Plan.ProfileID == app.WorkflowDocumentEdit {
		if chinese {
			return "这次未能生成有效的文档修改，原文件保持不变。请说明要修改的段落，或给出替换前后的文字。"
		}
		return "I could not produce a valid document edit; the original file is unchanged. Please specify the passage to edit or provide the text to find and its replacement."
	}
	// A failed workflow did start, so timer runs keep the plain sentence rather
	// than the "did not run" prefix used for routing failures.
	return workflowFailure(code).text(chinese, false)
}
