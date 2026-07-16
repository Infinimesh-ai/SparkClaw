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
	"regexp"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/connectorruntime"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/notification"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
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
	// A message whose previous dispatch failed may be delivered again by the
	// provider (the cursor is only advanced after successful dispatch); reuse
	// its record so the retry does not duplicate the chat history.
	retryID := ""
	if existing, ok := d.store.FindExternalChatMessageByExternalID(chatSession.ID, externalID); ok {
		if existing.Status != "failed" {
			return nil
		}
		retryID = existing.ID
	}
	receivedAt := inbound.CreatedAt
	if receivedAt.IsZero() {
		receivedAt = time.Now().UTC()
	}
	if text != "" && len(inbound.Attachments) == 0 && isClearConversationRequest(text) {
		return d.handleClearConversation(ctx, inbound, chatSession, externalID, text, receivedAt)
	}
	if text != "" && len(inbound.Attachments) == 0 {
		if handled, err := d.handleApprovalReply(ctx, inbound, chatSession, externalID, text, receivedAt); handled || err != nil {
			return err
		}
	}
	if text == "" && len(inbound.Attachments) > 0 {
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
		sendResult, sendErr := d.sendAssistantAnswer(ctx, inbound, answer, "")
		outbound := app.ExternalChatMessage{
			ChatSessionID: chatSession.ID,
			BindingID:     inbound.Binding.ID,
			Direction:     "outbound",
			Role:          "assistant",
			Content:       answer,
			ContextToken:  inbound.ContextToken,
			Status:        "sent",
		}
		if sendErr != nil {
			outbound.Status = "failed"
			outbound.Error = sendErr.Error()
		} else if sendResult.Status != "" {
			outbound.Status = sendResult.Status
		}
		d.store.SaveExternalChatMessage(outbound)
		_ = inboundMsg
		return sendErr
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
		return err
	}
	processing.LinkedRunID = result.Run.ID
	processing.Status = "processed"
	processing = d.store.SaveExternalChatMessage(processing)

	if len(result.Approvals) > 0 {
		answer := weixinApprovalPrompt(result.Approvals[len(result.Approvals)-1])
		return d.sendControlResult(ctx, inbound, chatSession, answer, result.Run.ID)
	}
	return d.deliverAgentResult(ctx, result, ingress)
}

func (d *Dispatcher) handleClearConversation(ctx context.Context, inbound InboundMessage, chatSession app.ExternalChatSession, externalID, text string, receivedAt time.Time) error {
	oldSessionID := chatSession.LinkedSessionID
	d.store.SaveExternalChatMessage(app.ExternalChatMessage{
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
	sendResult, sendErr := d.sendAssistantAnswer(ctx, inbound, answer, "")
	outbound := app.ExternalChatMessage{
		ChatSessionID: chatSession.ID,
		BindingID:     inbound.Binding.ID,
		Direction:     "outbound",
		Role:          "assistant",
		Content:       answer,
		ContextToken:  inbound.ContextToken,
		Status:        "sent",
		CreatedAt:     time.Now().UTC(),
	}
	if sendErr != nil {
		outbound.Status = "failed"
		outbound.Error = sendErr.Error()
	} else if sendResult.Status != "" {
		outbound.Status = sendResult.Status
	}
	d.store.SaveExternalChatMessage(outbound)
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

func (d *Dispatcher) handleApprovalReply(ctx context.Context, inbound InboundMessage, chatSession app.ExternalChatSession, externalID, text string, receivedAt time.Time) (bool, error) {
	approval, ok := d.pendingApprovalForChatSession(chatSession)
	if !ok {
		return false, nil
	}
	decision, ok := parseApprovalReply(text)
	if !ok {
		answer := weixinApprovalPrompt(approval)
		return true, d.sendApprovalReplyResult(ctx, inbound, chatSession, externalID, text, receivedAt, answer, approval.RunID, "needs_clear_approval_reply")
	}
	inboundMsg := d.store.SaveExternalChatMessage(app.ExternalChatMessage{
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
	_ = inboundMsg
	if decision {
		resolved, err := d.store.ResolveApproval(approval.ID, "approved", "confirmed from vx")
		if err != nil {
			return true, err
		}
		if _, err := d.runtime.ExecuteApprovedToolCall(ctx, resolved); err != nil {
			return true, d.sendApprovalReplyResult(ctx, inbound, chatSession, externalID, text, receivedAt, "确认失败："+err.Error(), approval.RunID, "failed")
		}
		if result, resumed, err := d.runtime.ResumeRunAfterApproval(ctx, approval.SessionID, approval.RunID); err != nil {
			return true, err
		} else if resumed {
			if len(result.Approvals) > 0 {
				return true, d.sendApprovalReplyResult(ctx, inbound, chatSession, externalID, text, receivedAt, weixinApprovalPrompt(result.Approvals[len(result.Approvals)-1]), result.Run.ID, "")
			}
			ingress := weixinIngress(inbound, chatSession, "approval:"+approval.ID)
			return true, d.deliverAgentResult(ctx, result, ingress)
		}
		d.runtime.CompleteRunIfApprovalsResolved(approval.RunID)
		if call, ok := d.store.GetToolCall(resolved.ToolCallID); ok {
			if answer := weixinApprovedToolAnswer(call); answer != "" {
				return true, d.sendApprovalReplyResult(ctx, inbound, chatSession, externalID, text, receivedAt, answer, approval.RunID, "")
			}
		}
		return true, d.sendApprovalReplyResult(ctx, inbound, chatSession, externalID, text, receivedAt, "已确认并执行。", approval.RunID, "")
	}
	resolved, err := d.store.ResolveApproval(approval.ID, "rejected", "rejected from vx")
	if err != nil {
		return true, err
	}
	if call, ok := d.store.GetToolCall(resolved.ToolCallID); ok {
		now := time.Now().UTC()
		call.Status = "rejected"
		call.Error = "user rejected approval from vx"
		call.CompletedAt = &now
		d.store.SaveToolCall(call)
	}
	d.runtime.CompleteRunIfApprovalsResolved(resolved.RunID)
	answer := "已取消，本次需要确认的操作没有执行。"
	return true, d.sendApprovalReplyResult(ctx, inbound, chatSession, externalID, text, receivedAt, answer, resolved.RunID, "")
}

func (d *Dispatcher) deliverAgentResult(ctx context.Context, result agent.Result, ingress app.MessageIngressContext) error {
	if d.results == nil {
		return errors.New("weixin workflow result delivery is unavailable")
	}
	workflowResult, err := connectorruntime.WorkflowResultFromAgentResult(result, ingress)
	if err != nil {
		return err
	}
	_, err = d.results.DeliverWorkflowResult(ctx, workflowResult)
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
		OwnerID: ownerID, Authorization: app.MessageAuthorization{PrincipalID: ownerID, Scope: append([]string(nil), inbound.Binding.Scopes...)},
		ReturnRoute: app.ReturnRoute{Mode: app.ReturnToSource, SourceEndpointID: endpointID},
	}
}

func stableWeixinAgentID(prefix, bindingID, externalID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(bindingID) + "\x00" + strings.TrimSpace(externalID)))
	return prefix + "_wx_" + hex.EncodeToString(sum[:])[:32]
}

func (d *Dispatcher) sendApprovalReplyResult(ctx context.Context, inbound InboundMessage, chatSession app.ExternalChatSession, externalID, text string, receivedAt time.Time, answer, runID, inboundStatus string) error {
	if inboundStatus != "" && inboundStatus != "received" {
		d.store.SaveExternalChatMessage(app.ExternalChatMessage{
			ChatSessionID:     chatSession.ID,
			BindingID:         inbound.Binding.ID,
			Direction:         "inbound",
			Role:              "user",
			ExternalMessageID: externalID,
			Content:           text,
			ContextToken:      inbound.ContextToken,
			LinkedRunID:       runID,
			Status:            inboundStatus,
			CreatedAt:         receivedAt,
		})
	}
	sendResult, sendErr := d.sendAssistantAnswer(ctx, inbound, answer, runID)
	outbound := app.ExternalChatMessage{
		ChatSessionID: chatSession.ID,
		BindingID:     inbound.Binding.ID,
		Direction:     "outbound",
		Role:          "assistant",
		Content:       answer,
		ContextToken:  inbound.ContextToken,
		LinkedRunID:   runID,
		Status:        "sent",
	}
	if sendErr != nil {
		outbound.Status = "failed"
		outbound.Error = sendErr.Error()
	} else if sendResult.Status != "" {
		outbound.Status = sendResult.Status
	}
	d.store.SaveExternalChatMessage(outbound)
	return sendErr
}

func (d *Dispatcher) pendingApprovalForChatSession(chatSession app.ExternalChatSession) (app.Approval, bool) {
	if strings.TrimSpace(chatSession.LinkedSessionID) == "" {
		return app.Approval{}, false
	}
	approvals := d.store.ListApprovals("pending")
	for i := len(approvals) - 1; i >= 0; i-- {
		approval := approvals[i]
		if approval.SessionID == chatSession.LinkedSessionID {
			return approval, true
		}
	}
	return app.Approval{}, false
}

func parseApprovalReply(text string) (bool, bool) {
	value := strings.TrimSpace(strings.ToLower(text))
	value = strings.Trim(value, " \t\r\n。.!！,，")
	switch value {
	case "是", "确认", "同意", "可以", "执行", "继续", "好", "好的", "yes", "y", "ok", "approve", "approved", "confirm":
		return true, true
	case "否", "不", "不要", "取消", "拒绝", "不同意", "停止", "no", "n", "reject", "rejected", "cancel":
		return false, true
	default:
		return false, false
	}
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
		return "我已收到图片。你想让我对它做什么？\n\n我可以帮你：描述图片内容、提取文字、整理要点、生成说明，或按你的要求继续处理。"
	case hasDocument:
		return "我已收到附件。你想让我对它们做什么？\n\n我可以读取文档内容、查看图片、总结/提取/问答，也可以按要求修改文档并把新文件发回。"
	default:
		return "我已收到附件。你想让我对它们做什么？\n\n请直接回复你的要求，我会根据附件类型选择可用的读取、分析或处理工具。"
	}
}

func weixinApprovalPrompt(approval app.Approval) string {
	lines := []string{"需要你确认后才能执行：", "操作：" + approvalActionText(approval)}
	if path := cleanWeixinString(approval.Arguments["path"]); path != "" {
		lines = append(lines, "文件："+path)
	}
	if outputPath := cleanWeixinString(approval.Arguments["output_path"]); outputPath != "" {
		lines = append(lines, "将生成："+outputPath)
	}
	if paragraphIndex := intLikeWeixinValue(approval.Arguments["paragraph_index"]); paragraphIndex > 0 {
		lines = append(lines, fmt.Sprintf("目标段落：第 %d 段", paragraphIndex))
	}
	if oldText := cleanWeixinString(approval.Arguments["old_text"]); oldText != "" {
		lines = append(lines, "原文："+trimWeixinText(oldText, 160))
	}
	if text := cleanWeixinString(approval.Arguments["text"]); text != "" {
		lines = append(lines, "修改为："+trimWeixinText(text, 220))
	}
	if command := cleanWeixinString(approval.Arguments["command"]); command != "" {
		lines = append(lines, "命令："+command)
	}
	lines = append(lines, "", "请回复“是”确认执行，或回复“否”取消执行。")
	return strings.Join(lines, "\n")
}

func approvalActionText(approval app.Approval) string {
	if summary := strings.TrimSpace(approval.Summary); summary != "" && !strings.HasPrefix(summary, "Approve ") {
		return summary
	}
	switch approval.Tool {
	case "docx.replace_paragraph":
		return "修改 Word 文档中的一段正文"
	case "docx.insert_paragraph":
		return "在 Word 文档中插入一段正文"
	case "docx.delete_paragraph":
		return "删除 Word 文档中的一段正文"
	case "docx.set_text_style":
		return "调整 Word 文档段落样式"
	case "office.replace_text":
		return "替换 Office 文档中的指定文本"
	case "shell.exec_sandboxed":
		return "执行一条沙箱命令"
	case "code.apply_patch":
		return "应用代码补丁"
	case "file.delete":
		return "删除文件到回收区"
	case "memory.write_sensitive":
		return "写入敏感记忆"
	default:
		return "执行一个需要确认的操作"
	}
}

func weixinApprovedToolAnswer(call app.ToolCall) string {
	if !isDocumentMutationToolName(call.Tool) {
		return ""
	}
	result, ok := call.Result.(map[string]any)
	if !ok {
		return ""
	}
	outputPath := cleanWeixinString(result["output_path"])
	if outputPath == "" {
		return ""
	}
	return "修改好的文件：" + outputPath
}

func isDocumentMutationToolName(tool string) bool {
	return strings.HasPrefix(tool, "docx.") ||
		strings.HasPrefix(tool, "pptx.") ||
		strings.HasPrefix(tool, "xlsx.") ||
		tool == "office.replace_text" ||
		tool == "pdf.transform"
}

func cleanWeixinString(value any) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" || text == "<nil>" {
		return ""
	}
	return text
}

func intLikeWeixinValue(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	default:
		var out int
		_, _ = fmt.Sscanf(fmt.Sprint(value), "%d", &out)
		return out
	}
}

func trimWeixinText(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	runes := []rune(value)
	if limit <= 0 || len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
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

func singleMediaMarkdownPath(answer string) (string, bool) {
	answer = strings.TrimSpace(answer)
	if !strings.HasPrefix(answer, "![") {
		return "", false
	}
	closeAlt := strings.Index(answer, "](")
	if closeAlt < 2 || !strings.HasSuffix(answer, ")") {
		return "", false
	}
	path := strings.TrimSpace(answer[closeAlt+2 : len(answer)-1])
	cleaned, ok := cleanMediaMarkdownTarget(path)
	if !ok {
		return "", false
	}
	switch strings.ToLower(filepath.Ext(cleaned)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return cleaned, true
	default:
		return "", false
	}
}

func cleanMediaMarkdownTarget(path string) (string, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", false
	}
	if strings.HasPrefix(path, "workspace://") {
		path = strings.TrimPrefix(path, "workspace://")
		path = strings.TrimLeft(path, "/")
		cleaned := filepath.ToSlash(filepath.Clean(path))
		if cleaned == "." || strings.HasPrefix(cleaned, "../") || !strings.HasPrefix(cleaned, "media/") {
			return "", false
		}
		return cleaned, true
	}
	if filepath.IsAbs(path) {
		cleaned := filepath.Clean(path)
		slash := filepath.ToSlash(cleaned)
		if !strings.Contains(slash, "/media/") {
			return "", false
		}
		return cleaned, true
	}
	path = strings.TrimLeft(path, "/")
	cleaned := filepath.ToSlash(filepath.Clean(path))
	if cleaned == "." || strings.HasPrefix(cleaned, "../") || !strings.HasPrefix(cleaned, "media/") {
		return "", false
	}
	return cleaned, true
}

func (d *Dispatcher) replyRecipient(inbound InboundMessage) string {
	if from := strings.TrimSpace(inbound.FromUserID); from != "" {
		return from
	}
	return strings.TrimSpace(inbound.Binding.ExternalUserID)
}

func (d *Dispatcher) workspaceMediaPath(mediaPath string, inbound InboundMessage) (string, bool) {
	mediaPath = strings.TrimSpace(mediaPath)
	if mediaPath == "" {
		return "", false
	}
	relPath := ""
	if !filepath.IsAbs(mediaPath) {
		relPath = filepath.ToSlash(strings.TrimSpace(mediaPath))
	}
	absPath := ""
	if filepath.IsAbs(mediaPath) {
		cleaned, err := filepath.Abs(mediaPath)
		if err != nil {
			return "", false
		}
		absPath = filepath.Clean(cleaned)
	}
	for _, object := range d.store.ListArtifactObjects(200) {
		key := filepath.ToSlash(object.Key)
		objectPath := strings.TrimSpace(object.Path)
		if !strings.HasPrefix(key, "media/") || objectPath == "" {
			continue
		}
		if relPath != "" && key == relPath {
			return object.Path, true
		}
		if absPath != "" {
			if objectAbs, err := filepath.Abs(objectPath); err == nil && filepath.Clean(objectAbs) == absPath {
				return object.Path, true
			}
		}
	}
	if relPath != "" {
		if path, ok := d.workspaceSessionPath(relPath, inbound); ok {
			return path, true
		}
	}
	return "", false
}

var workspaceFilePathPattern = regexp.MustCompile(`(?:workspace://)?((?:outputs|uploads)/[A-Za-z0-9._~!$&'()*+,;=:@%/\-]+\.(?:docx|xlsx|pptx|pdf|txt|md|csv|tsv))`)

func (d *Dispatcher) workspaceFilePath(answer string, inbound InboundMessage) (string, string, bool) {
	matches := workspaceFilePathPattern.FindAllStringSubmatch(answer, -1)
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		relPath := filepath.ToSlash(filepath.Clean(strings.TrimSpace(match[1])))
		if relPath == "." || strings.HasPrefix(relPath, "../") {
			continue
		}
		if strings.HasPrefix(relPath, "uploads/") && !isLikelyOutputFileAnswer(answer) {
			continue
		}
		if absPath, ok := d.workspaceObjectPath(relPath); ok {
			return absPath, filepath.Base(relPath), true
		}
		if absPath, ok := d.workspaceSessionPath(relPath, inbound); ok {
			return absPath, filepath.Base(relPath), true
		}
	}
	return "", "", false
}

func isLikelyOutputFileAnswer(answer string) bool {
	lower := strings.ToLower(answer)
	return strings.Contains(lower, "output_path") ||
		strings.Contains(lower, "output file") ||
		strings.Contains(answer, "输出文件") ||
		strings.Contains(answer, "修改好的文件") ||
		strings.Contains(answer, "已完成") ||
		strings.Contains(answer, "修改后")
}

func (d *Dispatcher) workspaceObjectPath(relPath string) (string, bool) {
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	for _, object := range d.store.ListArtifactObjects(200) {
		if filepath.ToSlash(object.Key) == relPath && strings.TrimSpace(object.Path) != "" {
			return object.Path, true
		}
	}
	return "", false
}

func (d *Dispatcher) workspaceSessionPath(relPath string, inbound InboundMessage) (string, bool) {
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	if relPath == "" || strings.HasPrefix(relPath, "../") {
		return "", false
	}
	externalUserID := strings.TrimSpace(inbound.FromUserID)
	if externalUserID == "" {
		externalUserID = strings.TrimSpace(inbound.Binding.ExternalUserID)
	}
	root := ""
	if chatSession, ok := d.store.FindExternalChatSession(inbound.Binding.ID, externalUserID, ""); ok {
		root = strings.TrimSpace(chatSession.WorkspaceRoot)
		if root == "" {
			if session, ok := d.store.GetSession(chatSession.LinkedSessionID); ok {
				root = strings.TrimSpace(session.WorkspaceRoot)
			}
		}
	}
	if root == "" {
		return "", false
	}
	absPath := filepath.Join(root, relPath)
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return "", false
	}
	cleanPath, err := filepath.Abs(absPath)
	if err != nil || !strings.HasPrefix(cleanPath, cleanRoot+string(os.PathSeparator)) {
		return "", false
	}
	if info, err := os.Stat(cleanPath); err == nil && !info.IsDir() {
		return cleanPath, true
	}
	return "", false
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
