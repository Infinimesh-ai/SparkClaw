package weixin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/connectorruntime"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/messagecontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/notification"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

const (
	// statusDeliveryFailed marks an inbound message whose reply was produced
	// but not handed to the provider; a redelivery retries only the send.
	statusDeliveryFailed = "delivery_failed"
	// statusDeliveryBlocked marks a message whose delivery failed for a
	// reason that cannot heal on its own (binding revoked, connector
	// disabled, payload rejected). The message is terminal: it is never
	// retried and does not hold the binding's cursor back.
	statusDeliveryBlocked = "delivery_blocked"

	// Pending reply kinds persisted on delivery_failed records. The kind
	// selects the retry action and the record status after a successful retry.
	pendingReplyWorkflowResult   = "workflow_result"
	pendingReplyControlText      = "control_text"
	pendingReplyAttachmentPrompt = "attachment_prompt"
)

type Dispatcher struct {
	store             store.Store
	runtime           connectorruntime.AgentBridge
	cfg               config.NotificationChannelConfig
	workspaceBaseRoot string
	results           connectorruntime.ResultDeliverer
}

func (d *Dispatcher) WithResultDeliverer(deliverer connectorruntime.ResultDeliverer) *Dispatcher {
	copy := *d
	copy.results = deliverer
	return &copy
}

type InboundMessage struct {
	Binding        app.NotificationBinding
	FromUserID     string
	ContextToken   string
	Text           string
	Attachments    []app.MessageAttachment
	ExternalID     string
	ProviderCursor string
	CreatedAt      time.Time
	ReceiveRecord  app.MessageReceiveRecord
}

func NewDispatcher(st store.Store, runtime connectorruntime.AgentRuntime, cfg config.NotificationChannelConfig) *Dispatcher {
	return &Dispatcher{store: st, runtime: connectorruntime.NewAgentBridge(runtime), cfg: cfg}
}

func NewDispatcherWithConfig(st store.Store, runtime connectorruntime.AgentRuntime, cfg config.Config) *Dispatcher {
	return &Dispatcher{
		store:             st,
		runtime:           connectorruntime.NewAgentBridge(runtime),
		cfg:               cfg.Tools.Notifications.Channels["weixin"],
		workspaceBaseRoot: strings.TrimSpace(cfg.Workspaces.DefaultRoot),
	}
}

func (d *Dispatcher) HandleInbound(ctx context.Context, inbound InboundMessage) error {
	text := strings.TrimSpace(inbound.Text)
	if text == "" && len(inbound.Attachments) == 0 {
		return nil
	}
	chatSession := d.ensureChatSession(inbound)
	externalID := strings.TrimSpace(inbound.ExternalID)
	if externalID == "" {
		externalID = stableInboundID(inbound)
	}
	receives := messagecontrol.NewReceiveLifecycle(d.store)
	receive := inbound.ReceiveRecord
	if receive.ID == "" {
		endpoint, err := messagecontrol.NewEndpointRegistry(d.store).Get(ctx, app.EndpointID(chatSession.ID))
		if err != nil {
			return err
		}
		receive, _ = receives.Begin(endpoint, externalID)
		receive = receives.Advance(receive, "authorized", "", "")
	}
	// A message whose previous dispatch failed may be delivered again by the
	// provider (the cursor is only advanced after successful dispatch); reuse
	// its record so the retry does not duplicate the chat history.
	retryID := ""
	if existing, ok := d.store.FindExternalChatMessageByExternalID(chatSession.ID, externalID); ok {
		if existing.Status != "failed" && existing.Status != statusDeliveryFailed {
			return nil
		}
		// The reply for this message was already produced and persisted; only
		// the provider hand-off failed. Retry just that step — re-entering
		// the runtime would replay the workflow's side effects (tool calls,
		// browser actions, file writes, outbound sends).
		if existing.Status == statusDeliveryFailed && existing.PendingReplyKind != "" {
			return d.retryPendingReply(ctx, inbound, chatSession, existing, receives, receive)
		}
		retryID = existing.ID
	}
	receivedAt := inbound.CreatedAt
	if receivedAt.IsZero() {
		receivedAt = time.Now().UTC()
	}
	receive = receives.Advance(receive, "normalized", "", "")
	if text != "" && len(inbound.Attachments) == 0 {
		if handled, err := d.handleControlText(ctx, inbound, chatSession, externalID, retryID, text, receivedAt); handled {
			status := "processed"
			if err != nil {
				status = "failed"
			}
			receives.Advance(receive, status, "", "")
			return err
		}
	}
	if text == "" && len(inbound.Attachments) > 0 {
		inboundMsg, err := d.handleAttachmentOnlyInbound(ctx, inbound, chatSession, externalID, retryID, receivedAt)
		receives.Advance(receive, inboundMsg.Status, inboundMsg.ID, "")
		return err
	}
	inboundMsg := d.store.SaveExternalChatMessage(app.ExternalChatMessage{
		ID:                retryID,
		ChatSessionID:     chatSession.ID,
		BindingID:         inbound.Binding.ID,
		Direction:         "inbound",
		Role:              "user",
		ExternalMessageID: externalID,
		Content:           text,
		ContextToken:      inbound.ContextToken,
		Status:            "received",
		CreatedAt:         receivedAt,
	})
	processing := inboundMsg
	processing.Status = "processing"
	processing = d.store.SaveExternalChatMessage(processing)
	receive = receives.Advance(receive, "routed", inboundMsg.ID, "")
	recipient := d.replyRecipient(inbound)
	if _, err := notification.SendWeixinTyping(ctx, d.store, d.cfg,
		recipient,
		inbound.ContextToken,
		inbound.Binding.CredentialRef,
		inbound.Binding.BaseURL,
		notification.TypingStatusTyping,
	); err != nil {
		d.auditTypingFailure(chatSession, inbound, "start", err)
	}
	defer func() {
		if _, err := notification.SendWeixinTyping(context.Background(), d.store, d.cfg,
			recipient,
			inbound.ContextToken,
			inbound.Binding.CredentialRef,
			inbound.Binding.BaseURL,
			notification.TypingStatusCancel,
		); err != nil {
			d.auditTypingFailure(chatSession, inbound, "cancel", err)
		}
	}()

	ingress := weixinIngress(inbound, chatSession, externalID)
	result, err := d.runtime.Handle(ctx, connectorruntime.AgentRequest{
		SessionID:   chatSession.LinkedSessionID,
		MessageID:   stableWeixinAgentID("message", inbound.Binding.ID, externalID),
		RunID:       stableWeixinAgentID("run", inbound.Binding.ID, externalID),
		Text:        text,
		Attachments: inbound.Attachments,
		Ingress:     &ingress,
	})
	if err != nil {
		processing.Status = "failed"
		processing.Error = err.Error()
		d.store.SaveExternalChatMessage(processing)
		receives.Advance(receive, "failed", inboundMsg.ID, "")
		return err
	}
	processing.LinkedRunID = result.Run.ID
	var deliveryErr error
	if len(result.Approvals) > 0 {
		answer := weixinApprovalPrompt(result.Approvals[len(result.Approvals)-1])
		processing, deliveryErr = d.finishControlReply(ctx, inbound, chatSession, processing, answer, result.Run.ID, "processed")
	} else {
		processing, deliveryErr = d.finishWorkflowReply(ctx, processing, result, ingress)
	}
	receives.Advance(receive, processing.Status, inboundMsg.ID, result.Run.ID)
	return deliveryErr
}

// finishControlReply sends a control answer (approval prompt or confirmation,
// clear-conversation notice, attachment clarification) and applies the shared
// delivery bookkeeping to the inbound record, so a failed send is retried on
// the provider's redelivery — and nothing else is.
func (d *Dispatcher) finishControlReply(ctx context.Context, inbound InboundMessage, chatSession app.ExternalChatSession, record app.ExternalChatMessage, answer, runID, successStatus string) (app.ExternalChatMessage, error) {
	sendErr := d.sendControlResult(ctx, inbound, chatSession, answer, runID)
	if runID != "" {
		record.LinkedRunID = runID
	}
	kind := pendingReplyControlText
	if successStatus == "needs_user_instruction" {
		kind = pendingReplyAttachmentPrompt
	}
	record = d.recordReplyOutcome(record, kind, answer, successStatus, sendErr)
	return record, sendErr
}

// finishWorkflowReply hands a produced workflow result to the delivery layer
// and applies the shared bookkeeping so a failed hand-off retries only the
// delivery step, never the workflow that produced it.
func (d *Dispatcher) finishWorkflowReply(ctx context.Context, record app.ExternalChatMessage, result agent.Result, ingress app.MessageIngressContext) (app.ExternalChatMessage, error) {
	workflowResult, err := connectorruntime.WorkflowResultFromAgentResult(result, ingress)
	if err != nil {
		// Nothing deliverable was produced; without a payload a retry falls
		// back to the full (idempotent) dispatch path.
		return d.recordReplyOutcome(record, "", "", "processed", err), err
	}
	kind, payload := "", ""
	if raw, marshalErr := json.Marshal(workflowResult); marshalErr == nil {
		kind, payload = pendingReplyWorkflowResult, string(raw)
	}
	record.LinkedRunID = result.Run.ID
	deliveryErr := d.deliverWorkflowResult(ctx, workflowResult)
	record = d.recordReplyOutcome(record, kind, payload, "processed", deliveryErr)
	return record, deliveryErr
}

// retryPendingReply re-sends a reply that a previous dispatch of the same
// message already produced. The linked run has finished and its side effects
// must not run twice, so only the provider hand-off is repeated.
func (d *Dispatcher) retryPendingReply(ctx context.Context, inbound InboundMessage, chatSession app.ExternalChatSession, msg app.ExternalChatMessage, receives messagecontrol.ReceiveLifecycle, receive app.MessageReceiveRecord) error {
	var deliveryErr error
	switch msg.PendingReplyKind {
	case pendingReplyWorkflowResult:
		var pending app.WorkflowResult
		if err := json.Unmarshal([]byte(msg.PendingReply), &pending); err != nil {
			deliveryErr = fmt.Errorf("persisted weixin reply payload is unreadable: %w", err)
		} else {
			deliveryErr = d.deliverWorkflowResult(ctx, pending)
		}
	default:
		deliveryErr = d.sendControlResult(ctx, inbound, chatSession, msg.PendingReply, msg.LinkedRunID)
	}
	msg = d.recordReplyOutcome(msg, msg.PendingReplyKind, msg.PendingReply, retrySuccessStatus(msg.PendingReplyKind), deliveryErr)
	receives.Advance(receive, msg.Status, msg.ID, msg.LinkedRunID)
	return deliveryErr
}

// retrySuccessStatus maps a pending reply kind to the record status after a
// successful delivery retry. Attachment prompts keep waiting for the user's
// instruction; every other reply completes the message.
func retrySuccessStatus(kind string) string {
	if kind == pendingReplyAttachmentPrompt {
		return "needs_user_instruction"
	}
	return "processed"
}

// recordReplyOutcome persists the delivery outcome of a produced reply on the
// inbound message record. Retryable failures keep the reply payload so the
// next redelivery retries only the provider send; blocked failures are
// terminal and record the reason.
func (d *Dispatcher) recordReplyOutcome(msg app.ExternalChatMessage, kind, payload, successStatus string, deliveryErr error) app.ExternalChatMessage {
	switch {
	case deliveryErr == nil:
		msg.Status = successStatus
		msg.Error = ""
		msg.PendingReplyKind, msg.PendingReply = "", ""
		msg.DispatchAttempts = 0
	case delivery.IsBlocked(deliveryErr):
		msg.Status = statusDeliveryBlocked
		msg.Error = deliveryErr.Error()
		msg.PendingReplyKind, msg.PendingReply = "", ""
	default:
		msg.Status = statusDeliveryFailed
		msg.Error = deliveryErr.Error()
		msg.PendingReplyKind, msg.PendingReply = kind, payload
	}
	return d.store.SaveExternalChatMessage(msg)
}

// handleControlText intercepts text-only messages that must not reach the
// agent: clear-conversation commands and replies to a pending approval.
func (d *Dispatcher) handleControlText(ctx context.Context, inbound InboundMessage, chatSession app.ExternalChatSession, externalID, retryID, text string, receivedAt time.Time) (bool, error) {
	if isClearConversationRequest(text) {
		return true, d.handleClearConversation(ctx, inbound, chatSession, externalID, retryID, text, receivedAt)
	}
	handled, err := d.handleApprovalReply(ctx, inbound, chatSession, externalID, retryID, text, receivedAt)
	return handled || err != nil, err
}

// handleAttachmentOnlyInbound records an attachment-only message as pending
// context and asks the user what to do with it instead of invoking the agent.
func (d *Dispatcher) handleAttachmentOnlyInbound(ctx context.Context, inbound InboundMessage, chatSession app.ExternalChatSession, externalID, retryID string, receivedAt time.Time) (app.ExternalChatMessage, error) {
	inboundContent := pendingAttachmentContext(inbound.Attachments)
	inboundMsg := d.store.SaveExternalChatMessage(app.ExternalChatMessage{
		ID:                retryID,
		ChatSessionID:     chatSession.ID,
		BindingID:         inbound.Binding.ID,
		Direction:         "inbound",
		Role:              "user",
		ExternalMessageID: externalID,
		Content:           inboundContent,
		ContextToken:      inbound.ContextToken,
		Status:            "needs_user_instruction",
		CreatedAt:         receivedAt,
	})
	d.store.AddMessage(app.Message{
		SessionID:   chatSession.LinkedSessionID,
		Role:        "user",
		Content:     inboundContent,
		Attachments: inbound.Attachments,
		CreatedAt:   receivedAt,
	})
	answer := attachmentClarificationPrompt(inbound.Attachments)
	return d.finishControlReply(ctx, inbound, chatSession, inboundMsg, answer, "", "needs_user_instruction")
}

func (d *Dispatcher) handleClearConversation(ctx context.Context, inbound InboundMessage, chatSession app.ExternalChatSession, externalID, retryID, text string, receivedAt time.Time) error {
	oldSessionID := chatSession.LinkedSessionID
	inboundMsg := d.store.SaveExternalChatMessage(app.ExternalChatMessage{
		ID:                retryID,
		ChatSessionID:     chatSession.ID,
		BindingID:         inbound.Binding.ID,
		Direction:         "inbound",
		Role:              "user",
		ExternalMessageID: externalID,
		Content:           text,
		ContextToken:      inbound.ContextToken,
		Status:            "received",
		CreatedAt:         receivedAt,
	})
	session := d.store.CreateSessionWithScope("微信会话", chatSession.OwnerID, chatSession.WorkspaceRoot, "weixin", true)
	chatSession.LinkedSessionID = session.ID
	chatSession.Status = "active"
	if inbound.ContextToken != "" {
		chatSession.LastContextToken = inbound.ContextToken
	}
	chatSession = d.store.SaveExternalChatSession(chatSession)
	d.store.AddAudit(app.AuditEvent{
		SessionID: session.ID,
		Actor:     "gateway",
		Type:      "weixin_chat.cleared",
		Summary:   "Weixin chat linked to a fresh Agent session",
		Fields: map[string]any{
			"binding_id":         inbound.Binding.ID,
			"chat_session_id":    chatSession.ID,
			"old_agent_session":  oldSessionID,
			"new_agent_session":  session.ID,
			"external_user_id":   chatSession.ExternalUserID,
			"old_context_hidden": true,
		},
	})
	answer := "对话已清空。后续消息会从新的上下文开始。"
	_, sendErr := d.finishControlReply(ctx, inbound, chatSession, inboundMsg, answer, "", "processed")
	return sendErr
}

func (d *Dispatcher) sendAssistantAnswer(ctx context.Context, inbound InboundMessage, answer, runID string) (notification.Result, error) {
	recipient := d.replyRecipient(inbound)
	if mediaPath, ok := singleMediaMarkdownPath(answer); ok {
		if imagePath, ok := d.workspaceMediaPath(mediaPath, inbound); ok {
			return notification.SendWeixinImage(ctx, d.store, d.cfg,
				recipient,
				inbound.ContextToken,
				inbound.Binding.CredentialRef,
				inbound.Binding.BaseURL,
				imagePath,
				"",
				runID,
			)
		}
	}
	if filePath, fileName, ok := d.workspaceFilePath(answer, inbound); ok {
		return notification.SendWeixinFile(ctx, d.store, d.cfg,
			recipient,
			inbound.ContextToken,
			inbound.Binding.CredentialRef,
			inbound.Binding.BaseURL,
			filePath,
			fileName,
			"",
			runID,
		)
	}
	return notification.SendWeixinText(ctx, d.store, d.cfg,
		recipient,
		inbound.ContextToken,
		inbound.Binding.CredentialRef,
		inbound.Binding.BaseURL,
		answer,
		runID,
	)
}

func isClearConversationRequest(text string) bool {
	value := strings.TrimSpace(strings.ToLower(text))
	value = strings.Trim(value, " \t\r\n。.!！,，")
	switch value {
	case "清空对话", "清空会话", "清除对话", "清除会话", "重置对话", "重置会话", "重新开始", "开始新对话", "新对话", "clear chat", "clear conversation", "reset chat", "reset conversation", "new chat":
		return true
	default:
		return false
	}
}

func (d *Dispatcher) deliverAgentResult(ctx context.Context, result agent.Result, ingress app.MessageIngressContext) error {
	workflowResult, err := connectorruntime.WorkflowResultFromAgentResult(result, ingress)
	if err != nil {
		return err
	}
	return d.deliverWorkflowResult(ctx, workflowResult)
}

func (d *Dispatcher) deliverWorkflowResult(ctx context.Context, result app.WorkflowResult) error {
	if d.results == nil {
		return errors.New("weixin workflow result delivery is unavailable")
	}
	_, err := d.results.DeliverWorkflowResult(ctx, result)
	return err
}

func (d *Dispatcher) sendControlResult(ctx context.Context, inbound InboundMessage, chatSession app.ExternalChatSession, answer, runID string) error {
	sendResult, sendErr := d.sendAssistantAnswer(ctx, inbound, answer, runID)
	outbound := app.ExternalChatMessage{
		ChatSessionID: chatSession.ID, BindingID: inbound.Binding.ID, Direction: "outbound", Role: "assistant",
		Content: answer, ContextToken: inbound.ContextToken, LinkedRunID: runID, Status: "sent",
	}
	if sendErr != nil {
		outbound.Status, outbound.Error = "failed", sendErr.Error()
	} else if sendResult.Status != "" {
		outbound.Status = sendResult.Status
	}
	d.store.SaveExternalChatMessage(outbound)
	return sendErr
}

func weixinIngress(inbound InboundMessage, chatSession app.ExternalChatSession, nativeMessageID string) app.MessageIngressContext {
	ownerID := firstNonEmpty(chatSession.OwnerID, inbound.Binding.OwnerID, app.DefaultOwnerID)
	endpointID := app.EndpointID(chatSession.ID)
	return app.MessageIngressContext{
		Source: app.MessageSourceContext{
			Kind: app.MessageSourceThirdPartyDevice, Adapter: strings.ToLower(strings.TrimSpace(inbound.Binding.Channel)),
			EndpointID: endpointID, NativeMessageID: nativeMessageID, NativeThreadRef: inbound.ContextToken,
		},
		OwnerID: ownerID, Authorization: app.MessageAuthorization{PrincipalID: ownerID, Scope: app.EffectiveMessagingBindingScopes(inbound.Binding.Scopes)},
		ReturnRoute: app.ReturnRoute{Mode: app.ReturnToSource, SourceEndpointID: endpointID, SourceAdmitted: true},
	}
}

func stableWeixinAgentID(prefix, bindingID, externalID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(bindingID) + "\x00" + strings.TrimSpace(externalID)))
	return prefix + "_wx_" + hex.EncodeToString(sum[:])[:32]
}

func attachmentClarificationPrompt(attachments []app.MessageAttachment) string {
	hasImage := false
	hasDocument := false
	hasOther := false
	for _, attachment := range attachments {
		switch {
		case isImageAttachment(attachment):
			hasImage = true
		case isDocumentAttachment(attachment):
			hasDocument = true
		default:
			hasOther = true
		}
	}
	switch {
	case hasDocument && !hasImage && !hasOther:
		return "我已收到文档。你想让我对它做什么？\n\n我可以帮你：总结内容、提取关键信息、按问题查找答案、检查风险点、修改 Word/Excel/PPT/PDF 并把新文件发回。"
	case hasImage && !hasDocument && !hasOther:
		return "我已收到图片。你想让我对它做什么？\n\n我可以帮你：描述图片内容、直接读出图片内原文、整理要点、生成说明，或按你的要求继续处理。"
	case hasDocument:
		return "我已收到附件。你想让我对它们做什么？\n\n我可以读取文档内容、查看图片、总结/提取/问答，也可以按要求修改文档并把新文件发回。"
	default:
		return "我已收到附件。你想让我对它们做什么？\n\n请直接回复你的要求，我会根据附件类型选择可用的读取、分析或处理工具。"
	}
}

func pendingAttachmentContext(attachments []app.MessageAttachment) string {
	lines := []string{"VX attachment received without user instruction. Ask the user what to do before processing; use these attachment paths if the next user message refers to this attachment:"}
	for _, attachment := range attachments {
		relPath := filepath.ToSlash(strings.TrimSpace(attachment.RelPath))
		if relPath == "" {
			continue
		}
		name := strings.TrimSpace(attachment.Name)
		if name == "" {
			name = filepath.Base(relPath)
		}
		fields := []string{
			"- name=" + name,
			"path=" + relPath,
		}
		if attachment.ContentType != "" {
			fields = append(fields, "content_type="+attachment.ContentType)
		}
		if attachment.Bytes > 0 {
			fields = append(fields, fmt.Sprintf("bytes=%d", attachment.Bytes))
		}
		lines = append(lines, strings.Join(fields, " "))
	}
	return strings.Join(lines, "\n")
}

func isImageAttachment(attachment app.MessageAttachment) bool {
	contentType := strings.ToLower(strings.TrimSpace(attachment.ContentType))
	if strings.HasPrefix(contentType, "image/") {
		return true
	}
	relPath := strings.ToLower(filepath.ToSlash(strings.TrimSpace(attachment.RelPath)))
	switch filepath.Ext(relPath) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp":
		return true
	default:
		return false
	}
}

func isDocumentAttachment(attachment app.MessageAttachment) bool {
	contentType := strings.ToLower(strings.TrimSpace(attachment.ContentType))
	if strings.Contains(contentType, "pdf") ||
		strings.Contains(contentType, "word") ||
		strings.Contains(contentType, "excel") ||
		strings.Contains(contentType, "powerpoint") ||
		strings.Contains(contentType, "spreadsheet") ||
		strings.Contains(contentType, "presentation") ||
		strings.HasPrefix(contentType, "text/") {
		return true
	}
	relPath := strings.ToLower(filepath.ToSlash(strings.TrimSpace(attachment.RelPath)))
	switch filepath.Ext(relPath) {
	case ".txt", ".md", ".csv", ".tsv", ".pdf", ".docx", ".xlsx", ".pptx":
		return true
	default:
		return false
	}
}

func (d *Dispatcher) replyRecipient(inbound InboundMessage) string {
	if from := strings.TrimSpace(inbound.FromUserID); from != "" {
		return from
	}
	return strings.TrimSpace(inbound.Binding.ExternalUserID)
}

func (d *Dispatcher) auditTypingFailure(chatSession app.ExternalChatSession, inbound InboundMessage, phase string, err error) {
	d.store.AddAudit(app.AuditEvent{
		SessionID: chatSession.LinkedSessionID,
		Actor:     "gateway",
		Type:      "weixin_chat.typing_failed",
		Summary:   "Weixin sendtyping failed; continuing message handling",
		Fields: map[string]any{
			"binding_id":      inbound.Binding.ID,
			"chat_session_id": chatSession.ID,
			"phase":           phase,
			"error":           err.Error(),
		},
	})
}

func (d *Dispatcher) ensureChatSession(inbound InboundMessage) app.ExternalChatSession {
	externalUserID := strings.TrimSpace(inbound.FromUserID)
	if externalUserID == "" {
		externalUserID = strings.TrimSpace(inbound.Binding.ExternalUserID)
	}
	profile := d.ensureOwnerProfile(inbound, externalUserID)
	ownerID := profile.ID
	workspaceRoot := strings.TrimSpace(profile.WorkspaceRoot)
	if existing, ok := d.store.FindExternalChatSession(inbound.Binding.ID, externalUserID, ""); ok {
		changed := false
		if existing.OwnerID != ownerID && ownerID != "" {
			existing.OwnerID = ownerID
			changed = true
		}
		if existing.WorkspaceRoot != workspaceRoot && workspaceRoot != "" {
			existing.WorkspaceRoot = workspaceRoot
			changed = true
		}
		if inbound.ContextToken != "" && existing.LastContextToken != inbound.ContextToken {
			existing.LastContextToken = inbound.ContextToken
			changed = true
		}
		if inbound.ProviderCursor != "" && existing.ProviderCursor != inbound.ProviderCursor {
			existing.ProviderCursor = inbound.ProviderCursor
			changed = true
		}
		if changed {
			return d.store.SaveExternalChatSession(existing)
		}
		return existing
	}
	session := d.store.CreateSessionWithScope("微信会话", ownerID, workspaceRoot, "weixin", true)
	return d.store.SaveExternalChatSession(app.ExternalChatSession{
		OwnerID:          ownerID,
		WorkspaceRoot:    workspaceRoot,
		BindingID:        inbound.Binding.ID,
		Channel:          "weixin",
		Provider:         inbound.Binding.Provider,
		ExternalUserID:   externalUserID,
		ExternalChatID:   externalUserID,
		DisplayName:      inbound.Binding.DisplayName,
		LinkedSessionID:  session.ID,
		Status:           "active",
		ProviderCursor:   inbound.ProviderCursor,
		LastContextToken: inbound.ContextToken,
	})
}

func (d *Dispatcher) ensureOwnerProfile(inbound InboundMessage, externalUserID string) app.OwnerProfile {
	externalRef := weixinExternalRef(inbound.Binding.ID, externalUserID)
	if profile, ok := d.store.FindOwnerProfileByExternalRef("weixin", externalRef); ok {
		if strings.TrimSpace(profile.WorkspaceRoot) == "" {
			profile.WorkspaceRoot = d.weixinWorkspaceRoot(profile.ID)
			profile = d.store.SaveOwnerProfile(profile)
		}
		return profile
	}
	ownerID := weixinOwnerID(inbound.Binding.ID, externalUserID)
	displayName := strings.TrimSpace(inbound.Binding.DisplayName)
	if displayName == "" {
		displayName = "微信用户"
	}
	return d.store.SaveOwnerProfile(app.OwnerProfile{
		ID:               ownerID,
		Source:           "weixin",
		ExternalRef:      externalRef,
		WorkspaceRoot:    d.weixinWorkspaceRoot(ownerID),
		DefaultChannel:   "weixin",
		DefaultBindingID: inbound.Binding.ID,
		DisplayName:      displayName,
		Preferences:      map[string]string{},
	})
}

func weixinExternalRef(bindingID, externalUserID string) string {
	return strings.TrimSpace(bindingID) + ":" + strings.TrimSpace(externalUserID)
}

func weixinOwnerID(bindingID, externalUserID string) string {
	seed := strings.TrimSpace(bindingID) + "\x00" + strings.TrimSpace(externalUserID)
	sum := sha256.Sum256([]byte(seed))
	return "wx_" + hex.EncodeToString(sum[:])[:24]
}

func (d *Dispatcher) weixinWorkspaceRoot(ownerID string) string {
	base := strings.TrimSpace(d.workspaceBaseRoot)
	if base == "" || strings.TrimSpace(ownerID) == "" {
		return ""
	}
	root := filepath.Join(base, "users", ownerID)
	_ = os.MkdirAll(root, 0o755)
	return root
}

func stableInboundID(inbound InboundMessage) string {
	parts := []string{
		inbound.Binding.ID,
		strings.TrimSpace(inbound.FromUserID),
		strings.TrimSpace(inbound.ContextToken),
		strings.TrimSpace(inbound.Text),
	}
	for _, attachment := range inbound.Attachments {
		parts = append(parts,
			strings.TrimSpace(attachment.RelPath),
			strings.TrimSpace(attachment.SHA256),
			fmt.Sprint(attachment.Bytes),
		)
	}
	if !inbound.CreatedAt.IsZero() {
		parts = append(parts, inbound.CreatedAt.UTC().Format(time.RFC3339Nano))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "wxin_" + hex.EncodeToString(sum[:])[:32]
}
