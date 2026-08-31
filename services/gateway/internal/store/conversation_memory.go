package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *MemoryStore) AddMessage(ctx context.Context, message app.Message) (app.Message, error) {
	ctx, cancel := operationContext(ctx, OperationConversationAddMessage, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationConversationAddMessage, ctx); err != nil {
		return app.Message{}, err
	}
	message, err := prepareMessage(message, time.Now())
	if err != nil {
		return app.Message{}, storeError(ctx, OperationConversationAddMessage, StoreErrorInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationConversationAddMessage, ctx); err != nil {
		return app.Message{}, err
	}
	if existing, found := findMessageByID(s.messages, message.ID); found {
		return existing, nil
	}
	session, ok := s.sessions[message.SessionID]
	if !ok {
		return app.Message{}, storeError(ctx, OperationConversationAddMessage, StoreErrorNotFound, errors.New("message session not found"))
	}
	if err := validatePersistedSession(message.SessionID, session); err != nil {
		return app.Message{}, storeError(ctx, OperationConversationAddMessage, StoreErrorCorrupt, err)
	}
	s.messages[message.SessionID] = append(s.messages[message.SessionID], cloneMessage(message))
	slices.SortFunc(s.messages[message.SessionID], compareMessagesAscending)
	session.UpdatedAt = nextSessionTime(message.CreatedAt, session.UpdatedAt, s.sessionWriteHighWater[session.ID])
	s.sessionWriteHighWater[session.ID] = session.UpdatedAt
	if !session.Hidden && (session.Title == "" || session.Title == "New SparkClaw Session") {
		session.Title = deriveTitle(message.Content)
	}
	s.sessions[message.SessionID] = session
	s.appendEventLocked("message.created", message.SessionID, message.RunID, cloneMessage(message))
	return cloneMessage(message), nil
}

func compareMessagesAscending(left, right app.Message) int {
	if compared := left.CreatedAt.Compare(right.CreatedAt); compared != 0 {
		return compared
	}
	return strings.Compare(left.ID, right.ID)
}

func (s *MemoryStore) ListMessages(ctx context.Context, sessionID string) ([]app.Message, error) {
	ctx, cancel := operationContext(ctx, OperationConversationListMessages, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationConversationListMessages, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationConversationListMessages, ctx); err != nil {
		return nil, err
	}
	messages := cloneMessages(s.messages[sessionID])
	if len(messages) == 0 {
		return []app.Message{}, nil
	}
	slices.SortFunc(messages, compareMessagesAscending)
	return messages, nil
}

func (s *MemoryStore) ListRecentMessages(ctx context.Context, sessionID string, cutoff time.Time, excludeMessageID string, scanLimit int) ([]app.Message, error) {
	ctx, cancel := operationContext(ctx, OperationConversationListRecent, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationConversationListRecent, ctx); err != nil {
		return nil, err
	}
	if err := validateRecentHistoryQuery(sessionID, cutoff, scanLimit); err != nil {
		return nil, storeError(ctx, OperationConversationListRecent, StoreErrorInvalid, err)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationConversationListRecent, ctx); err != nil {
		return nil, err
	}
	messages := s.messages[sessionID]
	out := make([]app.Message, 0, min(scanLimit, len(messages)))
	for index := len(messages) - 1; index >= 0 && len(out) < scanLimit; index-- {
		message := messages[index]
		if message.CreatedAt.After(cutoff) || message.ID == excludeMessageID {
			continue
		}
		out = append(out, cloneMessage(message))
	}
	return out, nil
}

func (s *MemoryStore) MessageEventHead(ctx context.Context, sessionID string) (string, error) {
	ctx, cancel := operationContext(ctx, OperationConversationMessageHead, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationConversationMessageHead, ctx); err != nil {
		return "", err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationConversationMessageHead, ctx); err != nil {
		return "", err
	}
	for index := len(s.events) - 1; index >= 0; index-- {
		event := s.events[index]
		if event.SessionID == sessionID && event.Type == "message.created" {
			return event.ID, nil
		}
	}
	return "", nil
}

func (s *MemoryStore) MessageEventsAfter(ctx context.Context, sessionID, after string, limit int) (MessageEventPage, error) {
	ctx, cancel := operationContext(ctx, OperationConversationMessagesAfter, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationConversationMessagesAfter, ctx); err != nil {
		return MessageEventPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationConversationMessagesAfter, ctx); err != nil {
		return MessageEventPage{}, err
	}
	if limit <= 0 || limit > MessageEventPageLimit {
		limit = MessageEventPageLimit
	}

	start := 0
	if after != "" {
		start = -1
		for index, event := range s.events {
			if event.ID != after {
				continue
			}
			if event.SessionID != sessionID || event.Type != "message.created" {
				return MessageEventPage{}, storeError(ctx, OperationConversationMessagesAfter, StoreErrorInvalid, ErrMessageEventCursorInvalid)
			}
			start = index + 1
			break
		}
		if start < 0 {
			return MessageEventPage{}, storeError(ctx, OperationConversationMessagesAfter, StoreErrorInvalid, ErrMessageEventCursorInvalid)
		}
	}

	matching := make([]app.Event, 0, limit+1)
	for _, event := range s.events[start:] {
		if event.SessionID == sessionID && event.Type == "message.created" {
			matching = append(matching, cloneClientLifecycleEvent(event))
			if len(matching) == limit+1 {
				break
			}
		}
	}
	hasMore := len(matching) > limit
	if hasMore {
		matching = matching[:limit]
	}
	next := after
	if len(matching) > 0 {
		next = matching[len(matching)-1].ID
	}
	return MessageEventPage{Events: matching, NextCursor: next, HasMore: hasMore}, nil
}
