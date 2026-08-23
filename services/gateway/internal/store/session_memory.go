package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *MemoryStore) CreateSession(ctx context.Context, title string) (app.Session, error) {
	return s.createSession(ctx, OperationSessionCreate, title, app.DefaultOwnerID, "", "webchat", false)
}

func (s *MemoryStore) CreateSessionWithScope(ctx context.Context, title, ownerID, workspaceRoot, source string, hidden bool) (app.Session, error) {
	return s.createSession(ctx, OperationSessionCreateWithScope, title, ownerID, workspaceRoot, source, hidden)
}

func (s *MemoryStore) createSession(ctx context.Context, operation StoreOperation, title, ownerID, workspaceRoot, source string, hidden bool) (app.Session, error) {
	ctx, cancel := operationContext(ctx, operation, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(operation, ctx); err != nil {
		return app.Session{}, err
	}
	session, err := prepareSession(title, ownerID, workspaceRoot, source, hidden, s.sessionNow())
	if err != nil {
		return app.Session{}, storeError(ctx, operation, StoreErrorInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(operation, ctx); err != nil {
		return app.Session{}, err
	}
	if _, exists := s.sessions[session.ID]; exists {
		return app.Session{}, storeError(ctx, operation, StoreErrorConflict, errors.New("session ID already exists"))
	}
	s.sessionWriteHighWater[session.ID] = session.UpdatedAt
	s.sessions[session.ID] = session
	s.appendAuditLocked("session.created", session.ID, "", "system", "Session created", map[string]any{"title": session.Title, "owner_id": session.OwnerID})
	s.appendEventLocked("session.created", session.ID, "", session)
	return session, nil
}

func (s *MemoryStore) hideLinkedExternalChatSessionsLocked() {
	now := normalizeSessionTime(s.sessionNow())
	for _, chatSession := range s.externalChatSessions {
		if linked, ok := s.sessions[chatSession.LinkedSessionID]; ok {
			linked.Source = chatSession.Channel
			linked.Hidden = true
			if strings.TrimSpace(chatSession.OwnerID) != "" {
				linked.OwnerID = chatSession.OwnerID
			}
			if strings.TrimSpace(chatSession.WorkspaceRoot) != "" {
				linked.WorkspaceRoot = chatSession.WorkspaceRoot
			}
			if linked.Title == "" || linked.Title == "New SparkClaw Session" {
				linked.Title = externalChatSessionTitle(chatSession.Channel)
			}
			if linked.UpdatedAt.IsZero() {
				linked.UpdatedAt = now
			}
			linked.CreatedAt = normalizeSessionTime(linked.CreatedAt)
			linked.UpdatedAt = normalizeSessionTime(linked.UpdatedAt)
			if linked.UpdatedAt.Before(linked.CreatedAt) {
				linked.UpdatedAt = linked.CreatedAt
			}
			s.sessionWriteHighWater[linked.ID] = linked.UpdatedAt
			s.sessions[linked.ID] = linked
		}
	}
}

func (s *MemoryStore) normalizeLinkedMCPSessionsLocked() {
	now := normalizeSessionTime(s.sessionNow())
	for _, binding := range s.mcpBindings {
		if strings.TrimSpace(binding.LinkedSessionID) == "" {
			continue
		}
		linked := s.sessions[binding.LinkedSessionID]
		linked.ID = binding.LinkedSessionID
		linked.OwnerID = binding.OwnerID
		linked.Title = mcpSessionTitle(binding.RequesterDeviceID)
		linked.Source = "mcp"
		linked.Hidden = false
		if linked.CreatedAt.IsZero() {
			linked.CreatedAt = firstNonZeroTime(binding.CreatedAt, now)
		}
		if linked.UpdatedAt.IsZero() {
			linked.UpdatedAt = firstNonZeroTime(binding.UpdatedAt, linked.CreatedAt)
		}
		linked.CreatedAt = normalizeSessionTime(linked.CreatedAt)
		linked.UpdatedAt = normalizeSessionTime(linked.UpdatedAt)
		if linked.UpdatedAt.Before(linked.CreatedAt) {
			linked.UpdatedAt = linked.CreatedAt
		}
		s.sessionWriteHighWater[linked.ID] = linked.UpdatedAt
		s.sessions[linked.ID] = linked
	}
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func (s *MemoryStore) ListSessions(ctx context.Context) ([]app.Session, error) {
	ctx, cancel := operationContext(ctx, OperationSessionList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationSessionList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationSessionList, ctx); err != nil {
		return nil, err
	}
	out := make([]app.Session, 0, len(s.sessions))
	for id, session := range s.sessions {
		if err := validatePersistedSession(id, session); err != nil {
			return nil, storeError(ctx, OperationSessionList, StoreErrorCorrupt, err)
		}
		if session.Hidden {
			continue
		}
		out = append(out, session)
	}
	slices.SortFunc(out, func(a, b app.Session) int {
		if byUpdated := b.UpdatedAt.Compare(a.UpdatedAt); byUpdated != 0 {
			return byUpdated
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (s *MemoryStore) GetSession(ctx context.Context, id string) (app.Session, bool, error) {
	ctx, cancel := operationContext(ctx, OperationSessionGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationSessionGet, ctx); err != nil {
		return app.Session{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationSessionGet, ctx); err != nil {
		return app.Session{}, false, err
	}
	session, ok := s.sessions[id]
	if !ok {
		return app.Session{}, false, nil
	}
	if err := validatePersistedSession(id, session); err != nil {
		return app.Session{}, false, storeError(ctx, OperationSessionGet, StoreErrorCorrupt, err)
	}
	return session, true, nil
}

func (s *MemoryStore) UpdateSessionTitle(ctx context.Context, id, title string) (app.Session, error) {
	ctx, cancel := operationContext(ctx, OperationSessionUpdateTitle, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationSessionUpdateTitle, ctx); err != nil {
		return app.Session{}, err
	}
	if strings.TrimSpace(id) == "" {
		return app.Session{}, storeError(ctx, OperationSessionUpdateTitle, StoreErrorInvalid, errors.New("session ID is required"))
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return app.Session{}, storeError(ctx, OperationSessionUpdateTitle, StoreErrorInvalid, errors.New("session title is required"))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationSessionUpdateTitle, ctx); err != nil {
		return app.Session{}, err
	}
	session, ok := s.sessions[id]
	if !ok {
		return app.Session{}, storeError(ctx, OperationSessionUpdateTitle, StoreErrorNotFound, errors.New("session not found"))
	}
	if err := validatePersistedSession(id, session); err != nil {
		return app.Session{}, storeError(ctx, OperationSessionUpdateTitle, StoreErrorCorrupt, err)
	}
	if strings.TrimSpace(session.Source) == "mcp" {
		return app.Session{}, storeError(ctx, OperationSessionUpdateTitle, StoreErrorConflict, errors.New("MCP session title is binding-owned"))
	}
	session.Title = title
	session.UpdatedAt = nextSessionTime(s.sessionNow(), session.UpdatedAt, s.sessionWriteHighWater[id])
	s.sessionWriteHighWater[id] = session.UpdatedAt
	s.sessions[id] = session
	s.appendAuditLocked("session.updated", id, "", "owner", "Session renamed", map[string]any{"title": title})
	s.appendEventLocked("session.updated", id, "", session)
	return session, nil
}

func (s *MemoryStore) DeleteSession(ctx context.Context, id string) (app.Session, error) {
	ctx, cancel := operationContext(ctx, OperationSessionDelete, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationSessionDelete, ctx); err != nil {
		return app.Session{}, err
	}
	if strings.TrimSpace(id) == "" {
		return app.Session{}, storeError(ctx, OperationSessionDelete, StoreErrorInvalid, errors.New("session ID is required"))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationSessionDelete, ctx); err != nil {
		return app.Session{}, err
	}
	session, ok := s.sessions[id]
	if !ok {
		return app.Session{}, storeError(ctx, OperationSessionDelete, StoreErrorNotFound, errors.New("session not found"))
	}
	if err := validatePersistedSession(id, session); err != nil {
		return app.Session{}, storeError(ctx, OperationSessionDelete, StoreErrorCorrupt, err)
	}
	if strings.TrimSpace(session.Source) == "mcp" {
		return app.Session{}, storeError(ctx, OperationSessionDelete, StoreErrorConflict, errors.New("MCP session history is binding-owned"))
	}
	runIDs := map[string]bool{}
	for runID, run := range s.runs {
		if run.SessionID == id {
			runIDs[runID] = true
		}
	}
	delete(s.sessions, id)
	delete(s.messages, id)
	for runID := range runIDs {
		delete(s.runFeedback, runID)
		delete(s.runs, runID)
	}
	for blockID, block := range s.browserLoginBlocks {
		if block.SessionID == id {
			delete(s.browserLoginBlocks, blockID)
		}
	}
	for feedbackRunID, feedback := range s.runFeedback {
		filtered := feedback[:0]
		for _, item := range feedback {
			if item.SessionID != id {
				filtered = append(filtered, item)
			}
		}
		if len(filtered) == 0 {
			delete(s.runFeedback, feedbackRunID)
		} else {
			s.runFeedback[feedbackRunID] = filtered
		}
	}
	for memoryID, memory := range s.memories {
		if runIDs[memory.SourceID] {
			delete(s.memories, memoryID)
		}
	}
	for runID, run := range s.runs {
		if run.SessionID == id {
			delete(s.runs, runID)
		}
	}
	for callID, call := range s.modelCalls {
		if call.SessionID == id {
			delete(s.modelCalls, callID)
		}
	}
	for callID, call := range s.toolCalls {
		if call.SessionID == id {
			delete(s.toolCalls, callID)
		}
	}
	for documentID, record := range s.documentRecords {
		if record.SessionID == id {
			delete(s.documentRecords, documentID)
		}
	}
	for approvalID, approval := range s.approvals {
		if approval.SessionID == id {
			delete(s.approvals, approvalID)
		}
	}
	deletedReminderIDs := map[string]bool{}
	for reminderID, reminder := range s.reminders {
		if reminder.SessionID == id {
			deletedReminderIDs[reminderID] = true
			delete(s.reminders, reminderID)
		}
	}
	for deliveryID, delivery := range s.reminderDelivery {
		if deletedReminderIDs[delivery.ReminderID] {
			delete(s.reminderDelivery, deliveryID)
		}
	}
	for candidateID, candidate := range s.memoryCandidates {
		if candidate.SessionID == id {
			delete(s.memoryCandidates, candidateID)
		}
	}
	for objectID, object := range s.artifactObjects {
		if object.SessionID == id {
			delete(s.artifactObjects, objectID)
			s.unindexArtifactObjectLocked(object)
		}
	}
	for episodeID, summary := range s.episodeSummaries {
		if summary.SessionID == id {
			delete(s.episodeSummaries, episodeID)
		}
	}
	deletedChatSessions := map[string]bool{}
	for chatSessionID, chatSession := range s.externalChatSessions {
		if chatSession.LinkedSessionID == id {
			deletedChatSessions[chatSessionID] = true
			delete(s.externalChatSessions, chatSessionID)
		}
	}
	for messageID, message := range s.externalChatMessages {
		if deletedChatSessions[message.ChatSessionID] {
			delete(s.externalChatMessages, messageID)
		}
	}
	s.auditEvents = filterAuditEvents(s.auditEvents, id)
	s.events = filterEvents(s.events, id)
	s.appendAuditLocked("session.deleted", "", "", "owner", "Session deleted", map[string]any{"session_id": id, "title": session.Title})
	s.appendEventLocked("session.deleted", "", "", session)
	return session, nil
}
