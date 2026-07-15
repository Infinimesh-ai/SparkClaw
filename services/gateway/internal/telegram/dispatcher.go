package telegram

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/connectorruntime"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type Dispatcher struct {
	store       store.Store
	runtime     connectorruntime.AgentBridge
	client      BotAPI
	transcriber VoiceTranscriber
	normalizer  func(context.Context, string, string, int) error
	cfg         config.Config
	channelCfg  config.NotificationChannelConfig
}

func NewDispatcher(st store.Store, runtime connectorruntime.AgentRuntime, cfg config.Config, transcribers ...VoiceTranscriber) *Dispatcher {
	transcriber := VoiceTranscriber(DisabledVoiceTranscriber{})
	if len(transcribers) > 0 && transcribers[0] != nil {
		transcriber = transcribers[0]
	}
	return &Dispatcher{
		store:       st,
		runtime:     connectorruntime.NewAgentBridge(runtime),
		transcriber: transcriber,
		normalizer:  normalizeTelegramAudio,
		cfg:         cfg,
		channelCfg:  cfg.Tools.Notifications.Channels["telegram"],
	}
}

func (d *Dispatcher) WithAudioNormalizer(normalizer func(context.Context, string, string, int) error) *Dispatcher {
	copy := *d
	if normalizer != nil {
		copy.normalizer = normalizer
	}
	return &copy
}

func (d *Dispatcher) WithClient(client BotAPI) *Dispatcher {
	copy := *d
	copy.client = client
	return &copy
}

func (d *Dispatcher) HandleUpdate(ctx context.Context, binding app.NotificationBinding, update Update) error {
	if d.client == nil {
		return NewConnectorError(CodeBindingUnavailable, false, nil)
	}
	if update.CallbackQuery != nil {
		return d.handleCallback(ctx, binding, update)
	}
	message := update.Message
	if message == nil || message.From == nil || message.Chat.Type != "private" {
		return nil
	}
	chatSession := d.ensureChatSession(binding, *message.From, message.Chat.ID, message.MessageThreadID)
	text := strings.TrimSpace(message.Text)
	if text == "" {
		text = strings.TrimSpace(message.Caption)
	}
	command, isCommand := telegramCommand(text)
	if isCommand {
		switch command {
		case "new":
			return d.resetConversation(ctx, binding, chatSession, message)
		case "status":
			pending := 0
			for _, approval := range d.store.ListApprovals("pending") {
				if approval.SessionID == chatSession.LinkedSessionID {
					pending++
				}
			}
			return d.sendAndRecord(ctx, binding, chatSession, message.Chat.ID, message.MessageThreadID,
				fmt.Sprintf("Telegram is connected. Pending approvals: %d.", pending), "cmd-status:"+strconv.FormatInt(message.MessageID, 10), "", nil)
		case "help", "start":
			return d.sendAndRecord(ctx, binding, chatSession, message.Chat.ID, message.MessageThreadID,
				"Commands:\n/new start a new conversation\n/status show connector status\n/help show help\n\nYou can send text, photos, supported documents, or voice notes.",
				"cmd-help:"+strconv.FormatInt(message.MessageID, 10), "", nil)
		}
	}

	externalID := inboundMessageExternalID(message)
	if existing, ok := d.store.FindExternalChatMessageByExternalID(chatSession.ID, externalID); ok {
		switch existing.Status {
		case "processed":
			return d.resumeOutbound(ctx, binding, chatSession, message.Chat.ID, message.MessageThreadID, existing.LinkedRunID)
		case "needs_user_instruction":
			return nil
		}
	}
	if approval, ok := d.pendingApproval(chatSession.LinkedSessionID); ok {
		if decision, parsed := parseApprovalDecision(text); parsed {
			d.saveInbound(chatSession, binding, externalID, text, "received", approval.RunID)
			return d.resolveApproval(ctx, binding, chatSession, message.Chat.ID, message.MessageThreadID, approval, decision, "Telegram text reply")
		}
	}

	attachments, voiceText, err := d.messageAttachments(ctx, chatSession, message)
	if err != nil {
		return d.sendAndRecord(ctx, binding, chatSession, message.Chat.ID, message.MessageThreadID,
			attachmentErrorMessage(err), "attachment-error:"+externalID, "", nil)
	}
	if text == "" {
		text = strings.TrimSpace(voiceText)
	} else if strings.TrimSpace(voiceText) != "" {
		text += "\n\nVoice transcript: " + strings.TrimSpace(voiceText)
	}
	if text == "" && len(attachments) == 0 {
		return nil
	}
	if text == "" {
		contextText := attachmentContext(attachments)
		d.saveInbound(chatSession, binding, externalID, contextText, "needs_user_instruction", "")
		d.store.AddMessage(app.Message{
			ID:          stableTelegramID("message", binding.ID, externalID),
			SessionID:   chatSession.LinkedSessionID,
			Role:        "user",
			Content:     contextText,
			Attachments: attachments,
			CreatedAt:   telegramMessageTime(message),
		})
		return d.sendAndRecord(ctx, binding, chatSession, message.Chat.ID, message.MessageThreadID,
			"I received the attachment. Tell me whether to read, summarize, extract, modify, or inspect it.", "attachment-help:"+externalID, "", nil)
	}

	runID := stableTelegramID("run", binding.ID, externalID)
	inbound := d.saveInbound(chatSession, binding, externalID, text, "processing", runID)
	_ = d.client.SendChatAction(ctx, message.Chat.ID, message.MessageThreadID, "typing")
	result, err := d.runtime.Handle(ctx, connectorruntime.AgentRequest{
		SessionID:   chatSession.LinkedSessionID,
		MessageID:   stableTelegramID("message", binding.ID, externalID),
		RunID:       runID,
		Text:        text,
		Attachments: attachments,
	})
	if err != nil {
		inbound.Status = "failed"
		inbound.Error = connectorErrorCode(err)
		d.store.SaveExternalChatMessage(inbound)
		return err
	}
	inbound.Status = "processed"
	inbound.LinkedRunID = result.Run.ID
	d.store.SaveExternalChatMessage(inbound)
	answer := strings.TrimSpace(result.Message.Content)
	var keyboard *InlineKeyboardMarkup
	if len(result.Approvals) > 0 {
		approval := result.Approvals[len(result.Approvals)-1]
		answer = approvalPrompt(approval)
		keyboard = approvalKeyboard(approval.ID)
	}
	if answer == "" {
		answer = "The message was processed, but there is no reply content to send."
	}
	return d.sendAndRecord(ctx, binding, chatSession, message.Chat.ID, message.MessageThreadID, answer, result.Run.ID, result.Run.ID, keyboard)
}

func (d *Dispatcher) handleCallback(ctx context.Context, binding app.NotificationBinding, update Update) error {
	query := update.CallbackQuery
	if query == nil || query.Message == nil {
		return nil
	}
	parts := strings.Split(query.Data, ":")
	if len(parts) != 3 || parts[0] != "approval" || (parts[2] != "approve" && parts[2] != "reject") {
		_ = d.client.AnswerCallbackQuery(ctx, query.ID, "Unsupported action")
		return nil
	}
	chatSession := d.ensureChatSession(binding, query.From, query.Message.Chat.ID, query.Message.MessageThreadID)
	approval, ok := d.approvalByID(parts[1])
	if !ok || approval.Status != "pending" || approval.SessionID != chatSession.LinkedSessionID {
		_ = d.client.AnswerCallbackQuery(ctx, query.ID, "This approval is no longer pending")
		return nil
	}
	if err := d.client.AnswerCallbackQuery(ctx, query.ID, "Received"); err != nil {
		return err
	}
	d.saveInbound(chatSession, binding, "callback:"+query.ID, query.Data, "received", approval.RunID)
	return d.resolveApproval(ctx, binding, chatSession, query.Message.Chat.ID, query.Message.MessageThreadID, approval, parts[2] == "approve", "Telegram button")
}

func (d *Dispatcher) resolveApproval(ctx context.Context, binding app.NotificationBinding, chatSession app.ExternalChatSession, chatID, threadID int64, approval app.Approval, approved bool, actor string) error {
	if approved {
		resolved, err := d.store.ResolveApproval(approval.ID, "approved", "approved from "+actor)
		if err != nil {
			if approval.Status != "pending" {
				return nil
			}
			return err
		}
		if _, err := d.runtime.ExecuteApprovedToolCall(ctx, resolved); err != nil {
			return d.sendAndRecord(ctx, binding, chatSession, chatID, threadID, "The approved action failed.", "approval-failed:"+approval.ID, approval.RunID, nil)
		}
		if result, resumed, err := d.runtime.ResumeRunAfterApproval(ctx, approval.SessionID, approval.RunID); err != nil {
			return err
		} else if resumed {
			answer := strings.TrimSpace(result.Message.Content)
			var keyboard *InlineKeyboardMarkup
			if len(result.Approvals) > 0 {
				next := result.Approvals[len(result.Approvals)-1]
				answer = approvalPrompt(next)
				keyboard = approvalKeyboard(next.ID)
			}
			if answer == "" {
				answer = "Approved and continued."
			}
			return d.sendAndRecord(ctx, binding, chatSession, chatID, threadID, answer, "approval:"+approval.ID, result.Run.ID, keyboard)
		}
		d.runtime.CompleteRunIfApprovalsResolved(approval.RunID)
		return d.sendAndRecord(ctx, binding, chatSession, chatID, threadID, "Approved and executed.", "approval:"+approval.ID, approval.RunID, nil)
	}
	resolved, err := d.store.ResolveApproval(approval.ID, "rejected", "rejected from "+actor)
	if err != nil {
		if approval.Status != "pending" {
			return nil
		}
		return err
	}
	if call, ok := d.store.GetToolCall(resolved.ToolCallID); ok {
		now := time.Now().UTC()
		call.Status = "rejected"
		call.Error = "user rejected approval from Telegram"
		call.CompletedAt = &now
		d.store.SaveToolCall(call)
	}
	d.runtime.CompleteRunIfApprovalsResolved(resolved.RunID)
	return d.sendAndRecord(ctx, binding, chatSession, chatID, threadID, "Canceled. The requested action was not executed.", "approval:"+approval.ID, resolved.RunID, nil)
}

func (d *Dispatcher) resetConversation(ctx context.Context, binding app.NotificationBinding, chatSession app.ExternalChatSession, message *Message) error {
	externalID := inboundMessageExternalID(message)
	d.saveInbound(chatSession, binding, externalID, message.Text, "received", "")
	session := d.store.CreateSessionWithScope("Telegram conversation", chatSession.OwnerID, chatSession.WorkspaceRoot, "telegram", true)
	chatSession.LinkedSessionID = session.ID
	chatSession.Status = "active"
	d.store.SaveExternalChatSession(chatSession)
	return d.sendAndRecord(ctx, binding, chatSession, message.Chat.ID, message.MessageThreadID, "A new conversation has started.", "cmd-new:"+externalID, "", nil)
}

func (d *Dispatcher) sendAndRecord(ctx context.Context, binding app.NotificationBinding, chatSession app.ExternalChatSession, chatID, threadID int64, answer, sourceID, runID string, keyboard *InlineKeyboardMarkup) error {
	if sourceID == "" {
		sourceID = stableTelegramID("answer", binding.ID, answer)
	}
	if keyboard == nil {
		if sent, err := d.sendMediaAnswer(ctx, chatSession, chatID, threadID, answer); sent {
			return d.recordOutbound(chatSession, binding, "out:"+sourceID+":media", answer, runID, err)
		}
	}
	for index, chunk := range splitTelegramText(answer, 4000) {
		externalID := fmt.Sprintf("out:%s:%d", sourceID, index)
		if existing, ok := d.store.FindExternalChatMessageByExternalID(chatSession.ID, externalID); ok && existing.Status == "sent" {
			continue
		}
		markup := keyboard
		if index > 0 {
			markup = nil
		}
		_, err := d.client.SendMessage(ctx, chatID, threadID, chunk, markup)
		if recordErr := d.recordOutbound(chatSession, binding, externalID, chunk, runID, err); recordErr != nil {
			return recordErr
		}
	}
	return nil
}

func (d *Dispatcher) recordOutbound(chatSession app.ExternalChatSession, binding app.NotificationBinding, externalID, content, runID string, sendErr error) error {
	status := "sent"
	errorCode := ""
	if sendErr != nil {
		status = "failed"
		errorCode = connectorErrorCode(sendErr)
	}
	d.store.SaveExternalChatMessage(app.ExternalChatMessage{
		ID:                stableTelegramID("outbound", chatSession.ID, externalID),
		ChatSessionID:     chatSession.ID,
		BindingID:         binding.ID,
		Channel:           "telegram",
		Direction:         "outbound",
		Role:              "assistant",
		ExternalMessageID: externalID,
		Content:           content,
		LinkedRunID:       runID,
		Status:            status,
		Error:             errorCode,
	})
	return sendErr
}

func (d *Dispatcher) resumeOutbound(ctx context.Context, binding app.NotificationBinding, chatSession app.ExternalChatSession, chatID, threadID int64, runID string) error {
	for _, message := range d.store.ListExternalChatMessages(chatSession.ID, 100) {
		if message.Direction != "outbound" || message.LinkedRunID != runID || message.Status == "sent" {
			continue
		}
		sourceID := strings.TrimPrefix(message.ExternalMessageID, "out:")
		if separator := strings.LastIndex(sourceID, ":"); separator > 0 {
			sourceID = sourceID[:separator]
		}
		return d.sendAndRecord(ctx, binding, chatSession, chatID, threadID, message.Content, sourceID, runID, nil)
	}
	return nil
}

func (d *Dispatcher) saveInbound(chatSession app.ExternalChatSession, binding app.NotificationBinding, externalID, content, status, runID string) app.ExternalChatMessage {
	return d.store.SaveExternalChatMessage(app.ExternalChatMessage{
		ID:                stableTelegramID("inbound", chatSession.ID, externalID),
		ChatSessionID:     chatSession.ID,
		BindingID:         binding.ID,
		Channel:           "telegram",
		Direction:         "inbound",
		Role:              "user",
		ExternalMessageID: externalID,
		Content:           content,
		ContextToken:      binding.ContextToken,
		LinkedRunID:       runID,
		Status:            status,
	})
}

func (d *Dispatcher) ensureChatSession(binding app.NotificationBinding, user User, chatID, threadID int64) app.ExternalChatSession {
	externalChatID := strconv.FormatInt(chatID, 10)
	externalThreadID := threadIDString(threadID)
	if existing, ok := d.store.FindExternalChatSession(binding.ID, externalChatID, externalThreadID); ok {
		return existing
	}
	ownerID := strings.TrimSpace(binding.OwnerID)
	if ownerID == "" {
		ownerID = app.DefaultOwnerID
	}
	profile, ok := d.store.GetOwnerProfileByID(ownerID)
	if !ok {
		profile = d.store.GetOwnerProfile()
	}
	workspaceRoot := strings.TrimSpace(profile.WorkspaceRoot)
	if workspaceRoot == "" {
		workspaceRoot = strings.TrimSpace(d.cfg.Workspaces.DefaultRoot)
	}
	session := d.store.CreateSessionWithScope("Telegram conversation", profile.ID, workspaceRoot, "telegram", true)
	return d.store.SaveExternalChatSession(app.ExternalChatSession{
		OwnerID:          profile.ID,
		WorkspaceRoot:    workspaceRoot,
		BindingID:        binding.ID,
		Channel:          "telegram",
		Provider:         binding.Provider,
		ExternalUserID:   strconv.FormatInt(user.ID, 10),
		ExternalChatID:   externalChatID,
		ExternalThreadID: externalThreadID,
		DisplayName:      user.DisplayName(),
		LinkedSessionID:  session.ID,
		Status:           "active",
		ProviderCursor:   binding.ProviderCursor,
		LastContextToken: binding.ContextToken,
	})
}

func (d *Dispatcher) pendingApproval(sessionID string) (app.Approval, bool) {
	approvals := d.store.ListApprovals("pending")
	for index := len(approvals) - 1; index >= 0; index-- {
		if approvals[index].SessionID == sessionID {
			return approvals[index], true
		}
	}
	return app.Approval{}, false
}

func (d *Dispatcher) approvalByID(id string) (app.Approval, bool) {
	for _, approval := range d.store.ListApprovals("") {
		if approval.ID == id {
			return approval, true
		}
	}
	return app.Approval{}, false
}

func telegramCommand(text string) (string, bool) {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return "", false
	}
	command := strings.TrimPrefix(strings.ToLower(strings.SplitN(fields[0], "@", 2)[0]), "/")
	return command, command != ""
}

func parseApprovalDecision(text string) (bool, bool) {
	value := strings.Trim(strings.ToLower(strings.TrimSpace(text)), " \t\r\n.!?,")
	switch value {
	case "yes", "y", "ok", "approve", "confirm", "确认", "同意", "执行", "继续":
		return true, true
	case "no", "n", "reject", "cancel", "取消", "拒绝", "停止":
		return false, true
	default:
		return false, false
	}
}

func approvalPrompt(approval app.Approval) string {
	action := strings.TrimSpace(approval.Summary)
	if action == "" {
		action = approval.Tool
	}
	return "Approval required before execution:\n" + action
}

func approvalKeyboard(approvalID string) *InlineKeyboardMarkup {
	return &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{{
		{Text: "Confirm", CallbackData: "approval:" + approvalID + ":approve"},
		{Text: "Cancel", CallbackData: "approval:" + approvalID + ":reject"},
	}}}
}

func splitTelegramText(text string, maxRunes int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return []string{" "}
	}
	runes := []rune(text)
	chunks := []string{}
	for len(runes) > maxRunes {
		cut := maxRunes
		for index := maxRunes; index > maxRunes/2; index-- {
			if runes[index-1] == '\n' {
				cut = index
				break
			}
		}
		chunks = append(chunks, strings.TrimSpace(string(runes[:cut])))
		runes = runes[cut:]
	}
	if tail := strings.TrimSpace(string(runes)); tail != "" {
		chunks = append(chunks, tail)
	}
	return chunks
}

func attachmentContext(attachments []app.MessageAttachment) string {
	lines := []string{"Telegram attachment received without an instruction:"}
	for _, attachment := range attachments {
		lines = append(lines, fmt.Sprintf("- name=%s path=%s content_type=%s bytes=%d", attachment.Name, attachment.RelPath, attachment.ContentType, attachment.Bytes))
	}
	return strings.Join(lines, "\n")
}

func attachmentErrorMessage(err error) string {
	switch connectorErrorCode(err) {
	case CodeAttachmentTooLarge:
		return "The attachment is too large for this Telegram connector."
	case CodeAttachmentUnsupported:
		return "This attachment type is not supported."
	case CodeVoiceUnavailable:
		return "Voice transcription is unavailable. Please send text instead."
	default:
		return "The attachment could not be processed."
	}
}

func inboundMessageExternalID(message *Message) string {
	return strconv.FormatInt(message.Chat.ID, 10) + ":" + strconv.FormatInt(message.MessageID, 10)
}

func telegramMessageTime(message *Message) time.Time {
	if message.Date <= 0 {
		return time.Now().UTC()
	}
	return time.Unix(message.Date, 0).UTC()
}

func stableTelegramID(prefix string, parts ...string) string {
	seed := strings.Join(parts, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return prefix + "_" + hex.EncodeToString(sum[:])[:32]
}

func workspacePath(root, relPath string) (string, bool) {
	root, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil || root == "" {
		return "", false
	}
	path, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(relPath)))
	return path, err == nil && strings.HasPrefix(path, root+string(filepath.Separator))
}
