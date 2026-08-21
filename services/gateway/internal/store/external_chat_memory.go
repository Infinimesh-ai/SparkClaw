package store

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *MemoryStore) SaveExternalChatSession(ctx context.Context, session app.ExternalChatSession) (app.ExternalChatSession, error) {
	ctx, cancel := operationContext(ctx, OperationExternalChatSessionSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationExternalChatSessionSave, ctx); err != nil {
		return app.ExternalChatSession{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationExternalChatSessionSave, ctx); err != nil {
		return app.ExternalChatSession{}, err
	}
	now := normalizeExternalChatTime(time.Now())
	current, exists := s.externalChatSessions[session.ID]
	session = prepareExternalChatSession(session, now)
	if exists {
		session.CreatedAt = normalizeExternalChatTime(current.CreatedAt)
	}
	if linked, ok := s.sessions[session.LinkedSessionID]; ok {
		linked.Source = session.Channel
		linked.Hidden = true
		if strings.TrimSpace(session.OwnerID) != "" {
			linked.OwnerID = session.OwnerID
		}
		if strings.TrimSpace(session.WorkspaceRoot) != "" {
			linked.WorkspaceRoot = session.WorkspaceRoot
		}
		linked.UpdatedAt = nextSessionTime(now, linked.UpdatedAt, s.sessionWriteHighWater[linked.ID])
		s.sessionWriteHighWater[linked.ID] = linked.UpdatedAt
		if linked.Title == "" || linked.Title == "New SparkClaw Session" || linked.Title == "微信会话" {
			linked.Title = externalChatSessionTitle(session.Channel)
		}
		s.sessions[linked.ID] = linked
	}
	s.externalChatSessions[session.ID] = session
	s.appendAuditLocked("external_chat_session."+session.Status, session.LinkedSessionID, "", "gateway", redactExternalID(session.ExternalUserID), map[string]any{
		"chat_session_id": session.ID,
		"binding_id":      session.BindingID,
		"channel":         session.Channel,
		"provider":        session.Provider,
	})
	s.appendEventLocked("external_chat_session."+session.Status, session.LinkedSessionID, "", session)
	return session, nil
}

func (s *MemoryStore) GetExternalChatSession(ctx context.Context, id string) (app.ExternalChatSession, bool, error) {
	ctx, cancel := operationContext(ctx, OperationExternalChatSessionGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationExternalChatSessionGet, ctx); err != nil {
		return app.ExternalChatSession{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.externalChatSessions[id]
	return normalizeExternalChatSession(session), ok, nil
}

func (s *MemoryStore) ListExternalChatSessions(ctx context.Context, channel, status string) ([]app.ExternalChatSession, error) {
	ctx, cancel := operationContext(ctx, OperationExternalChatSessionList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationExternalChatSessionList, ctx); err != nil {
		return nil, err
	}
	channel = strings.ToLower(strings.TrimSpace(channel))
	status = strings.TrimSpace(status)
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.ExternalChatSession{}
	for _, session := range s.externalChatSessions {
		if channel != "" && strings.ToLower(strings.TrimSpace(session.Channel)) != channel {
			continue
		}
		if status != "" && session.Status != status {
			continue
		}
		out = append(out, normalizeExternalChatSession(session))
	}
	slices.SortFunc(out, func(a, b app.ExternalChatSession) int {
		if order := b.UpdatedAt.Compare(a.UpdatedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (s *MemoryStore) FindExternalChatSession(ctx context.Context, bindingID, externalChatID, externalThreadID string) (app.ExternalChatSession, bool, error) {
	ctx, cancel := operationContext(ctx, OperationExternalChatSessionFind, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationExternalChatSessionFind, ctx); err != nil {
		return app.ExternalChatSession{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var found app.ExternalChatSession
	for _, session := range s.externalChatSessions {
		chatID := session.ExternalChatID
		if chatID == "" {
			chatID = session.ExternalUserID
		}
		if session.BindingID == bindingID && chatID == externalChatID && session.ExternalThreadID == externalThreadID {
			if found.ID == "" || session.UpdatedAt.After(found.UpdatedAt) || (session.UpdatedAt.Equal(found.UpdatedAt) && session.ID < found.ID) {
				found = session
			}
		}
	}
	return normalizeExternalChatSession(found), found.ID != "", nil
}

func (s *MemoryStore) FindExternalChatSessionByLinkedSessionID(ctx context.Context, sessionID string) (app.ExternalChatSession, bool, error) {
	ctx, cancel := operationContext(ctx, OperationExternalChatSessionFindLink, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationExternalChatSessionFindLink, ctx); err != nil {
		return app.ExternalChatSession{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var found app.ExternalChatSession
	for _, session := range s.externalChatSessions {
		if session.LinkedSessionID == sessionID && (found.ID == "" || session.UpdatedAt.After(found.UpdatedAt) || (session.UpdatedAt.Equal(found.UpdatedAt) && session.ID < found.ID)) {
			found = session
		}
	}
	return normalizeExternalChatSession(found), found.ID != "", nil
}

func (s *MemoryStore) SaveExternalChatMessage(ctx context.Context, message app.ExternalChatMessage) (app.ExternalChatMessage, error) {
	ctx, cancel := operationContext(ctx, OperationExternalChatMessageSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationExternalChatMessageSave, ctx); err != nil {
		return app.ExternalChatMessage{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationExternalChatMessageSave, ctx); err != nil {
		return app.ExternalChatMessage{}, err
	}
	current, exists := s.externalChatMessages[message.ID]
	channel := ""
	if session, ok := s.externalChatSessions[message.ChatSessionID]; ok {
		channel = session.Channel
	}
	message = prepareExternalChatMessage(message, channel, time.Now())
	if exists {
		message.CreatedAt = normalizeExternalChatTime(current.CreatedAt)
	}
	s.externalChatMessages[message.ID] = message
	s.appendAuditLocked("external_chat_message."+message.Status, "", message.LinkedRunID, "gateway", message.Direction, map[string]any{
		"message_id":      message.ID,
		"chat_session_id": message.ChatSessionID,
		"binding_id":      message.BindingID,
		"channel":         message.Channel,
		"direction":       message.Direction,
		"role":            message.Role,
	})
	s.appendEventLocked("external_chat_message."+message.Status, "", message.LinkedRunID, message)
	return message, nil
}

func (s *MemoryStore) GetExternalChatMessage(ctx context.Context, id string) (app.ExternalChatMessage, bool, error) {
	ctx, cancel := operationContext(ctx, OperationExternalChatMessageGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationExternalChatMessageGet, ctx); err != nil {
		return app.ExternalChatMessage{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	message, ok := s.externalChatMessages[id]
	return normalizeExternalChatMessage(message), ok, nil
}

func (s *MemoryStore) FindExternalChatMessageByExternalID(ctx context.Context, chatSessionID, externalMessageID string) (app.ExternalChatMessage, bool, error) {
	ctx, cancel := operationContext(ctx, OperationExternalChatMessageFind, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationExternalChatMessageFind, ctx); err != nil {
		return app.ExternalChatMessage{}, false, err
	}
	if strings.TrimSpace(externalMessageID) == "" {
		return app.ExternalChatMessage{}, false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var found app.ExternalChatMessage
	for _, message := range s.externalChatMessages {
		if message.ChatSessionID == chatSessionID && message.ExternalMessageID == externalMessageID &&
			(found.ID == "" || message.CreatedAt.After(found.CreatedAt) || (message.CreatedAt.Equal(found.CreatedAt) && message.ID < found.ID)) {
			found = message
		}
	}
	return normalizeExternalChatMessage(found), found.ID != "", nil
}

func (s *MemoryStore) ListExternalChatMessages(ctx context.Context, chatSessionID string, limit int) ([]app.ExternalChatMessage, error) {
	ctx, cancel := operationContext(ctx, OperationExternalChatMessageList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationExternalChatMessageList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.ExternalChatMessage{}
	for _, message := range s.externalChatMessages {
		if chatSessionID == "" || message.ChatSessionID == chatSessionID {
			out = append(out, normalizeExternalChatMessage(message))
		}
	}
	slices.SortFunc(out, func(a, b app.ExternalChatMessage) int {
		if order := a.CreatedAt.Compare(b.CreatedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	if limit > 0 && len(out) > limit {
		return out[len(out)-limit:], nil
	}
	return out, nil
}
