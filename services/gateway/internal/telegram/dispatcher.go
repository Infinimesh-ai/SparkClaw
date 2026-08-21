package telegram

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/connectorruntime"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/messagecontrol"
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
	results     connectorruntime.ResultDeliverer
}

func (d *Dispatcher) WithResultDeliverer(deliverer connectorruntime.ResultDeliverer) *Dispatcher {
	copy := *d
	copy.results = deliverer
	return &copy
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
	chatSession, err := d.ensureChatSession(ctx, binding, *message.From, message.Chat.ID, message.MessageThreadID)
	if err != nil {
		return NewConnectorError(CodeBindingUnavailable, true, err)
	}
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
			approvals, err := d.store.ListApprovals(ctx, "pending")
			if err != nil {
				return NewConnectorError("approval_store_unavailable", true, err)
			}
			for _, approval := range approvals {
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
	endpoint, err := messagecontrol.NewEndpointRegistry(d.store).Get(ctx, app.EndpointID(chatSession.ID))
	if err != nil {
		return err
	}
	receives := messagecontrol.NewReceiveLifecycle(d.store)
	receive, freshReceive, err := receives.Begin(ctx, endpoint, externalID)
	if err != nil {
		return NewConnectorError("receive_store_unavailable", true, err)
	}
	if freshReceive {
		receive, err = receives.Advance(ctx, receive, "authorized", "", "")
		if err != nil {
			return NewConnectorError("receive_store_unavailable", true, err)
		}
	}
	existing, ok, err := d.store.FindExternalChatMessageByExternalID(ctx, chatSession.ID, externalID)
	if err != nil {
		return NewConnectorError("external_chat_store_unavailable", true, err)
	}
	if ok {
		switch existing.Status {
		case "processed":
			return d.resumeOutbound(ctx, binding, chatSession, message.Chat.ID, message.MessageThreadID, existing.LinkedRunID)
		case "needs_user_instruction":
			return nil
		}
	}
	approval, ok, err := d.pendingApproval(ctx, chatSession.LinkedSessionID)
	if err != nil {
		return NewConnectorError("approval_store_unavailable", true, err)
	}
	if ok {
		if decision, parsed := parseApprovalDecision(text); parsed {
			inbound, err := d.saveInbound(ctx, chatSession, binding, externalID, text, "received", approval.RunID)
			if err != nil {
				return NewConnectorError("external_chat_store_unavailable", true, err)
			}
			if _, err := receives.Advance(ctx, receive, "processed", inbound.ID, approval.RunID); err != nil {
				return NewConnectorError("receive_store_unavailable", true, err)
			}
			return d.resolveApproval(ctx, binding, chatSession, message.Chat.ID, message.MessageThreadID, approval, decision, "Telegram text reply")
		}
	}

	attachments, voiceText, err := d.messageAttachments(ctx, chatSession, message)
	if err != nil {
		if _, persistErr := receives.Advance(ctx, receive, "failed", "", ""); persistErr != nil {
			return NewConnectorError("receive_store_unavailable", true, persistErr)
		}
		return d.sendAndRecord(ctx, binding, chatSession, message.Chat.ID, message.MessageThreadID,
			attachmentErrorMessage(err), "attachment-error:"+externalID, "", nil)
	}
	if text == "" {
		text = strings.TrimSpace(voiceText)
	} else if strings.TrimSpace(voiceText) != "" {
		text += "\n\nVoice transcript: " + strings.TrimSpace(voiceText)
	}
	if text == "" && len(attachments) == 0 {
		if _, err := receives.Advance(ctx, receive, "rejected", "", ""); err != nil {
			return NewConnectorError("receive_store_unavailable", true, err)
		}
		return nil
	}
	receive, err = receives.Advance(ctx, receive, "normalized", "", "")
	if err != nil {
		return NewConnectorError("receive_store_unavailable", true, err)
	}
	if text == "" {
		contextText := attachmentContext(attachments)
		inbound, err := d.saveInbound(ctx, chatSession, binding, externalID, contextText, "needs_user_instruction", "")
		if err != nil {
			return NewConnectorError("external_chat_store_unavailable", true, err)
		}
		if _, err := d.store.AddMessage(ctx, app.Message{
			ID:          stableTelegramID("message", binding.ID, externalID),
			SessionID:   chatSession.LinkedSessionID,
			Role:        "user",
			Content:     contextText,
			Attachments: attachments,
			CreatedAt:   telegramMessageTime(message),
		}); err != nil {
			if _, persistErr := receives.Advance(ctx, receive, "failed", inbound.ID, ""); persistErr != nil {
				return NewConnectorError("receive_store_unavailable", true, persistErr)
			}
			return NewConnectorError("message_store_unavailable", true, err)
		}
		if _, err := receives.Advance(ctx, receive, "processed", inbound.ID, ""); err != nil {
			return NewConnectorError("receive_store_unavailable", true, err)
		}
		return d.sendAndRecord(ctx, binding, chatSession, message.Chat.ID, message.MessageThreadID,
			"I received the attachment. Tell me whether to read, summarize, extract, modify, or inspect it.", "attachment-help:"+externalID, "", nil)
	}

	runID := stableTelegramID("run", binding.ID, externalID)
	inbound, err := d.saveInbound(ctx, chatSession, binding, externalID, text, "processing", runID)
	if err != nil {
		return NewConnectorError("external_chat_store_unavailable", true, err)
	}
	receive, err = receives.Advance(ctx, receive, "routed", inbound.ID, runID)
	if err != nil {
		return NewConnectorError("receive_store_unavailable", true, err)
	}
	_ = d.client.SendChatAction(ctx, message.Chat.ID, message.MessageThreadID, "typing")
	ingress := telegramIngress(binding, chatSession, externalID, message.MessageThreadID)
	result, err := d.runtime.Handle(ctx, connectorruntime.AgentRequest{
		SessionID:   chatSession.LinkedSessionID,
		MessageID:   stableTelegramID("message", binding.ID, externalID),
		RunID:       runID,
		Text:        text,
		Attachments: attachments,
		Ingress:     &ingress,
	})
	if err != nil {
		inbound.Status = "failed"
		inbound.Error = connectorErrorCode(err)
		if _, persistErr := d.store.SaveExternalChatMessage(ctx, inbound); persistErr != nil {
			return errors.Join(err, NewConnectorError("external_chat_store_unavailable", true, persistErr))
		}
		if _, persistErr := receives.Advance(ctx, receive, "failed", inbound.ID, runID); persistErr != nil {
			return NewConnectorError("receive_store_unavailable", true, persistErr)
		}
		return err
	}
	inbound.Status = "processed"
	inbound.LinkedRunID = result.Run.ID
	if _, err := d.store.SaveExternalChatMessage(ctx, inbound); err != nil {
		return NewConnectorError("external_chat_store_unavailable", true, err)
	}
	if _, err := receives.Advance(ctx, receive, "processed", inbound.ID, result.Run.ID); err != nil {
		return NewConnectorError("receive_store_unavailable", true, err)
	}
	if len(result.Approvals) > 0 {
		approval := result.Approvals[len(result.Approvals)-1]
		return d.sendAndRecord(ctx, binding, chatSession, message.Chat.ID, message.MessageThreadID, approvalPrompt(approval), result.Run.ID, result.Run.ID, approvalKeyboard(approval.ID))
	}
	return d.deliverAgentResult(ctx, result, ingress)
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
	chatSession, err := d.ensureChatSession(ctx, binding, query.From, query.Message.Chat.ID, query.Message.MessageThreadID)
	if err != nil {
		return NewConnectorError(CodeBindingUnavailable, true, err)
	}
	approval, ok, err := d.approvalByID(ctx, parts[1])
	if err != nil {
		return NewConnectorError("approval_store_unavailable", true, err)
	}
	if !ok || approval.Status != "pending" || approval.SessionID != chatSession.LinkedSessionID {
		_ = d.client.AnswerCallbackQuery(ctx, query.ID, "This approval is no longer pending")
		return nil
	}
	if err := d.client.AnswerCallbackQuery(ctx, query.ID, "Received"); err != nil {
		return err
	}
	if _, err := d.saveInbound(ctx, chatSession, binding, "callback:"+query.ID, query.Data, "received", approval.RunID); err != nil {
		return NewConnectorError("external_chat_store_unavailable", true, err)
	}
	return d.resolveApproval(ctx, binding, chatSession, query.Message.Chat.ID, query.Message.MessageThreadID, approval, parts[2] == "approve", "Telegram button")
}

func (d *Dispatcher) resolveApproval(ctx context.Context, binding app.NotificationBinding, chatSession app.ExternalChatSession, chatID, threadID int64, approval app.Approval, approved bool, actor string) error {
	if approved {
		candidate, err := d.store.ResolveApproval(ctx, approval.ID, "approved", "approved from "+actor)
		resolved, err := store.ReconcileApprovalWrite(ctx, d.store, candidate, err)
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
			if len(result.Approvals) > 0 {
				next := result.Approvals[len(result.Approvals)-1]
				return d.sendAndRecord(ctx, binding, chatSession, chatID, threadID, approvalPrompt(next), "approval:"+approval.ID, result.Run.ID, approvalKeyboard(next.ID))
			}
			ingress := telegramIngress(binding, chatSession, "approval:"+approval.ID, threadID)
			return d.deliverAgentResult(ctx, result, ingress)
		}
		if err := d.runtime.CompleteRunIfApprovalsResolved(ctx, approval.RunID); err != nil {
			return err
		}
		return d.sendAndRecord(ctx, binding, chatSession, chatID, threadID, "Approved and executed.", "approval:"+approval.ID, approval.RunID, nil)
	}
	candidate, err := d.store.ResolveApproval(ctx, approval.ID, "rejected", "rejected from "+actor)
	resolved, err := store.ReconcileApprovalWrite(ctx, d.store, candidate, err)
	if err != nil {
		if approval.Status != "pending" {
			return nil
		}
		return err
	}
	if call, ok, err := d.store.GetToolCall(ctx, resolved.ToolCallID); err != nil {
		return err
	} else if ok {
		now := time.Now().UTC()
		call.Status = "rejected"
		call.Error = "user rejected approval from Telegram"
		call.CompletedAt = &now
		candidate, saveErr := d.store.SaveToolCall(ctx, call)
		if _, saveErr = store.ReconcileToolCallWrite(ctx, d.store, candidate, saveErr); saveErr != nil {
			return saveErr
		}
	}
	if err := d.runtime.CompleteRunIfApprovalsResolved(ctx, resolved.RunID); err != nil {
		return err
	}
	return d.sendAndRecord(ctx, binding, chatSession, chatID, threadID, "Canceled. The requested action was not executed.", "approval:"+approval.ID, resolved.RunID, nil)
}

func (d *Dispatcher) deliverAgentResult(ctx context.Context, result agent.Result, ingress app.MessageIngressContext) error {
	if d.results == nil {
		return fmt.Errorf("telegram workflow result delivery is unavailable")
	}
	workflowResult, err := connectorruntime.WorkflowResultFromAgentResult(result, ingress)
	if err != nil {
		return err
	}
	_, err = d.results.DeliverWorkflowResult(ctx, workflowResult)
	return err
}

func telegramIngress(binding app.NotificationBinding, chatSession app.ExternalChatSession, nativeMessageID string, threadID int64) app.MessageIngressContext {
	ownerID := firstNonEmpty(chatSession.OwnerID, binding.OwnerID, app.DefaultOwnerID)
	nativeThreadRef := ""
	if threadID != 0 {
		nativeThreadRef = strconv.FormatInt(threadID, 10)
	}
	endpointID := app.EndpointID(chatSession.ID)
	return app.MessageIngressContext{
		Source: app.MessageSourceContext{
			Kind: app.MessageSourceThirdPartyDevice, Adapter: strings.ToLower(strings.TrimSpace(binding.Channel)),
			EndpointID: endpointID, NativeMessageID: nativeMessageID, NativeThreadRef: nativeThreadRef,
		},
		OwnerID: ownerID, Authorization: app.MessageAuthorization{PrincipalID: ownerID, Scope: app.EffectiveMessagingBindingScopes(binding.Scopes)},
		ReturnRoute: app.ReturnRoute{Mode: app.ReturnToSource, SourceEndpointID: endpointID, SourceAdmitted: true},
	}
}

func (d *Dispatcher) resetConversation(ctx context.Context, binding app.NotificationBinding, chatSession app.ExternalChatSession, message *Message) error {
	externalID := inboundMessageExternalID(message)
	if _, err := d.saveInbound(ctx, chatSession, binding, externalID, message.Text, "received", ""); err != nil {
		return NewConnectorError("external_chat_store_unavailable", true, err)
	}
	session, err := d.store.CreateSessionWithScope(ctx, "Telegram conversation", chatSession.OwnerID, chatSession.WorkspaceRoot, "telegram", true)
	if err != nil {
		return err
	}
	chatSession.LinkedSessionID = session.ID
	chatSession.Status = "active"
	chatSession.AuthorizedOwnerID = binding.OwnerID
	chatSession.AuthorizedActorID = binding.ActorID
	chatSession, err = d.store.SaveExternalChatSession(ctx, chatSession)
	if err != nil {
		return NewConnectorError("external_chat_store_unavailable", true, err)
	}
	return d.sendAndRecord(ctx, binding, chatSession, message.Chat.ID, message.MessageThreadID, "A new conversation has started.", "cmd-new:"+externalID, "", nil)
}

func (d *Dispatcher) sendAndRecord(ctx context.Context, binding app.NotificationBinding, chatSession app.ExternalChatSession, chatID, threadID int64, answer, sourceID, runID string, keyboard *InlineKeyboardMarkup) error {
	if sourceID == "" {
		sourceID = stableTelegramID("answer", binding.ID, answer)
	}
	if keyboard == nil {
		if sent, err := d.sendMediaAnswer(ctx, chatSession, chatID, threadID, answer); sent {
			return d.recordOutbound(ctx, chatSession, binding, "out:"+sourceID+":media", answer, runID, err)
		}
	}
	for index, chunk := range splitTelegramText(answer, 4000) {
		externalID := fmt.Sprintf("out:%s:%d", sourceID, index)
		existing, ok, err := d.store.FindExternalChatMessageByExternalID(ctx, chatSession.ID, externalID)
		if err != nil {
			return NewConnectorError("external_chat_store_unavailable", true, err)
		}
		if ok && existing.Status == "sent" {
			continue
		}
		markup := keyboard
		if index > 0 {
			markup = nil
		}
		_, err = d.client.SendMessage(ctx, chatID, threadID, chunk, markup)
		if recordErr := d.recordOutbound(ctx, chatSession, binding, externalID, chunk, runID, err); recordErr != nil {
			return recordErr
		}
	}
	return nil
}

func (d *Dispatcher) recordOutbound(ctx context.Context, chatSession app.ExternalChatSession, binding app.NotificationBinding, externalID, content, runID string, sendErr error) error {
	status := "sent"
	errorCode := ""
	if sendErr != nil {
		status = "failed"
		errorCode = connectorErrorCode(sendErr)
	}
	_, persistErr := d.store.SaveExternalChatMessage(ctx, app.ExternalChatMessage{
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
	return errors.Join(sendErr, persistErr)
}

func (d *Dispatcher) resumeOutbound(ctx context.Context, binding app.NotificationBinding, chatSession app.ExternalChatSession, chatID, threadID int64, runID string) error {
	messages, err := d.store.ListExternalChatMessages(ctx, chatSession.ID, 100)
	if err != nil {
		return NewConnectorError("external_chat_store_unavailable", true, err)
	}
	for _, message := range messages {
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

func (d *Dispatcher) saveInbound(ctx context.Context, chatSession app.ExternalChatSession, binding app.NotificationBinding, externalID, content, status, runID string) (app.ExternalChatMessage, error) {
	return d.store.SaveExternalChatMessage(ctx, app.ExternalChatMessage{
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

func (d *Dispatcher) ensureChatSession(ctx context.Context, binding app.NotificationBinding, user User, chatID, threadID int64) (app.ExternalChatSession, error) {
	externalChatID := strconv.FormatInt(chatID, 10)
	externalThreadID := threadIDString(threadID)
	existing, ok, err := d.store.FindExternalChatSession(ctx, binding.ID, externalChatID, externalThreadID)
	if err != nil {
		return app.ExternalChatSession{}, err
	}
	if ok {
		return existing, nil
	}
	ownerID := strings.TrimSpace(binding.OwnerID)
	if ownerID == "" {
		ownerID = app.DefaultOwnerID
	}
	profile, ok, err := d.store.GetOwnerProfileByID(ctx, ownerID)
	if err != nil {
		return app.ExternalChatSession{}, err
	}
	if !ok {
		profile, err = d.store.GetOwnerProfile(ctx)
		if err != nil {
			return app.ExternalChatSession{}, err
		}
	}
	workspaceRoot := strings.TrimSpace(profile.WorkspaceRoot)
	if workspaceRoot == "" {
		workspaceRoot = strings.TrimSpace(d.cfg.Workspaces.DefaultRoot)
	}
	session, err := d.store.CreateSessionWithScope(ctx, "Telegram conversation", profile.ID, workspaceRoot, "telegram", true)
	if err != nil {
		return app.ExternalChatSession{}, err
	}
	return d.store.SaveExternalChatSession(ctx, app.ExternalChatSession{
		OwnerID:           profile.ID,
		AuthorizedOwnerID: binding.OwnerID,
		AuthorizedActorID: binding.ActorID,
		WorkspaceRoot:     workspaceRoot,
		BindingID:         binding.ID,
		Channel:           "telegram",
		Provider:          binding.Provider,
		ExternalUserID:    strconv.FormatInt(user.ID, 10),
		ExternalChatID:    externalChatID,
		ExternalThreadID:  externalThreadID,
		DisplayName:       user.DisplayName(),
		LinkedSessionID:   session.ID,
		Status:            "active",
		ProviderCursor:    binding.ProviderCursor,
		LastContextToken:  binding.ContextToken,
	})
}

func (d *Dispatcher) pendingApproval(ctx context.Context, sessionID string) (app.Approval, bool, error) {
	approvals, err := d.store.ListApprovals(ctx, "pending")
	if err != nil {
		return app.Approval{}, false, err
	}
	for index := len(approvals) - 1; index >= 0; index-- {
		if approvals[index].SessionID == sessionID {
			return approvals[index], true, nil
		}
	}
	return app.Approval{}, false, nil
}

func (d *Dispatcher) approvalByID(ctx context.Context, id string) (app.Approval, bool, error) {
	approval, found, err := d.store.GetApproval(ctx, id)
	if err != nil {
		return app.Approval{}, false, err
	}
	if found {
		if approval.ID == id {
			return approval, true, nil
		}
	}
	return app.Approval{}, false, nil
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
