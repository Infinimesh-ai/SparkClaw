package store

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type MemoryStore struct {
	mu                   sync.RWMutex
	sessions             map[string]app.Session
	clients              map[string]app.Client
	ownerProfile         app.OwnerProfile
	ownerProfiles        map[string]app.OwnerProfile
	pairingCodes         map[string]app.PairingCode
	messages             map[string][]app.Message
	runFeedback          map[string][]app.RunFeedback
	runs                 map[string]app.AgentRun
	modelCalls           map[string]app.ModelCall
	toolCalls            map[string]app.ToolCall
	approvals            map[string]app.Approval
	reminders            map[string]app.Reminder
	reminderDelivery     map[string]app.ReminderDelivery
	notificationBindings map[string]app.NotificationBinding
	weixinChatSessions   map[string]app.WeixinChatSession
	weixinChatMessages   map[string]app.WeixinChatMessage
	credentialSecrets    map[string]app.CredentialSecret
	browserAuthRecords   map[string]app.BrowserAuthRecord
	browserLoginBlocks   map[string]app.BrowserLoginBlock
	memories             map[string]app.Memory
	memoryCandidates     map[string]app.MemoryCandidate
	auditEvents          []app.AuditEvent
	events               []app.Event
	evalRuns             map[string]app.EvalRun
	artifactObjects      map[string]app.ArtifactObject
	episodeSummaries     map[string]app.EpisodeSummary
	documents            map[string]app.Document
	documentChunks       map[string]app.DocumentChunk
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		sessions:             map[string]app.Session{},
		clients:              map[string]app.Client{},
		ownerProfile:         app.DefaultOwnerProfile(),
		ownerProfiles:        map[string]app.OwnerProfile{app.DefaultOwnerID: app.DefaultOwnerProfile()},
		pairingCodes:         map[string]app.PairingCode{},
		messages:             map[string][]app.Message{},
		runFeedback:          map[string][]app.RunFeedback{},
		runs:                 map[string]app.AgentRun{},
		modelCalls:           map[string]app.ModelCall{},
		toolCalls:            map[string]app.ToolCall{},
		approvals:            map[string]app.Approval{},
		reminders:            map[string]app.Reminder{},
		reminderDelivery:     map[string]app.ReminderDelivery{},
		notificationBindings: map[string]app.NotificationBinding{},
		weixinChatSessions:   map[string]app.WeixinChatSession{},
		weixinChatMessages:   map[string]app.WeixinChatMessage{},
		credentialSecrets:    map[string]app.CredentialSecret{},
		browserAuthRecords:   map[string]app.BrowserAuthRecord{},
		browserLoginBlocks:   map[string]app.BrowserLoginBlock{},
		memories:             map[string]app.Memory{},
		memoryCandidates:     map[string]app.MemoryCandidate{},
		auditEvents:          []app.AuditEvent{},
		events:               []app.Event{},
		evalRuns:             map[string]app.EvalRun{},
		artifactObjects:      map[string]app.ArtifactObject{},
		episodeSummaries:     map[string]app.EpisodeSummary{},
		documents:            map[string]app.Document{},
		documentChunks:       map[string]app.DocumentChunk{},
	}
}

func (s *MemoryStore) snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Snapshot{
		Sessions:             cloneMap(s.sessions),
		Clients:              cloneMap(s.clients),
		OwnerProfile:         cloneOwnerProfile(s.ownerProfile),
		OwnerProfiles:        cloneOwnerProfileMap(s.ownerProfiles),
		PairingCodes:         cloneMap(s.pairingCodes),
		Messages:             cloneSliceMap(s.messages),
		RunFeedback:          cloneSliceMap(s.runFeedback),
		Runs:                 cloneMap(s.runs),
		ModelCalls:           cloneMap(s.modelCalls),
		ToolCalls:            cloneMap(s.toolCalls),
		Approvals:            cloneMap(s.approvals),
		Reminders:            cloneMap(s.reminders),
		ReminderDelivery:     cloneMap(s.reminderDelivery),
		NotificationBindings: cloneMap(s.notificationBindings),
		WeixinChatSessions:   cloneMap(s.weixinChatSessions),
		WeixinChatMessages:   cloneMap(s.weixinChatMessages),
		CredentialSecrets:    cloneMap(s.credentialSecrets),
		BrowserAuthRecords:   cloneMap(s.browserAuthRecords),
		BrowserLoginBlocks:   cloneMap(s.browserLoginBlocks),
		Memories:             cloneMap(s.memories),
		MemoryCandidates:     cloneMap(s.memoryCandidates),
		AuditEvents:          append([]app.AuditEvent(nil), s.auditEvents...),
		Events:               append([]app.Event(nil), s.events...),
		EvalRuns:             cloneMap(s.evalRuns),
		ArtifactObjects:      cloneMap(s.artifactObjects),
		EpisodeSummaries:     cloneMap(s.episodeSummaries),
		Documents:            cloneMap(s.documents),
		DocumentChunks:       cloneMap(s.documentChunks),
	}
}

func (s *MemoryStore) loadSnapshot(snapshot Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = ensureMap(snapshot.Sessions)
	s.clients = ensureMap(snapshot.Clients)
	s.ownerProfile = normalizeOwnerProfile(snapshot.OwnerProfile)
	s.ownerProfiles = ensureOwnerProfileMap(snapshot.OwnerProfiles, s.ownerProfile)
	s.pairingCodes = ensureMap(snapshot.PairingCodes)
	s.messages = ensureSliceMap(snapshot.Messages)
	s.runFeedback = ensureSliceMap(snapshot.RunFeedback)
	s.runs = ensureMap(snapshot.Runs)
	s.modelCalls = ensureMap(snapshot.ModelCalls)
	s.toolCalls = ensureMap(snapshot.ToolCalls)
	s.approvals = ensureMap(snapshot.Approvals)
	s.reminders = ensureMap(snapshot.Reminders)
	s.reminderDelivery = ensureMap(snapshot.ReminderDelivery)
	s.notificationBindings = ensureMap(snapshot.NotificationBindings)
	s.weixinChatSessions = ensureMap(snapshot.WeixinChatSessions)
	s.weixinChatMessages = ensureMap(snapshot.WeixinChatMessages)
	s.credentialSecrets = ensureMap(snapshot.CredentialSecrets)
	s.browserAuthRecords = ensureMap(snapshot.BrowserAuthRecords)
	s.browserLoginBlocks = ensureMap(snapshot.BrowserLoginBlocks)
	s.memories = ensureMap(snapshot.Memories)
	s.memoryCandidates = ensureMap(snapshot.MemoryCandidates)
	s.auditEvents = append([]app.AuditEvent(nil), snapshot.AuditEvents...)
	s.events = append([]app.Event(nil), snapshot.Events...)
	s.evalRuns = ensureMap(snapshot.EvalRuns)
	s.artifactObjects = ensureMap(snapshot.ArtifactObjects)
	s.episodeSummaries = ensureMap(snapshot.EpisodeSummaries)
	s.documents = ensureMap(snapshot.Documents)
	s.documentChunks = ensureMap(snapshot.DocumentChunks)
	s.hideLinkedWeixinSessionsLocked()
}

func (s *MemoryStore) CreateSession(title string) app.Session {
	return s.CreateSessionWithScope(title, app.DefaultOwnerID, "", "webchat", false)
}

func (s *MemoryStore) CreateSessionWithScope(title, ownerID, workspaceRoot, source string, hidden bool) app.Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if title == "" {
		title = "New SparkClaw Session"
	}
	if strings.TrimSpace(ownerID) == "" {
		ownerID = app.DefaultOwnerID
	}
	if strings.TrimSpace(source) == "" {
		source = "webchat"
	}
	session := app.Session{
		ID:            app.NewID("s"),
		OwnerID:       ownerID,
		WorkspaceRoot: strings.TrimSpace(workspaceRoot),
		Title:         title,
		Source:        source,
		Hidden:        hidden,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	s.sessions[session.ID] = session
	s.appendAuditLocked("session.created", session.ID, "", "system", "Session created", map[string]any{"title": title, "owner_id": ownerID})
	s.appendEventLocked("session.created", session.ID, "", session)
	return session
}

func (s *MemoryStore) hideLinkedWeixinSessionsLocked() {
	now := time.Now().UTC()
	for _, chatSession := range s.weixinChatSessions {
		if linked, ok := s.sessions[chatSession.LinkedSessionID]; ok {
			linked.Source = "weixin"
			linked.Hidden = true
			if strings.TrimSpace(chatSession.OwnerID) != "" {
				linked.OwnerID = chatSession.OwnerID
			}
			if strings.TrimSpace(chatSession.WorkspaceRoot) != "" {
				linked.WorkspaceRoot = chatSession.WorkspaceRoot
			}
			if linked.Title == "" || linked.Title == "New SparkClaw Session" {
				linked.Title = "微信会话"
			}
			if linked.UpdatedAt.IsZero() {
				linked.UpdatedAt = now
			}
			s.sessions[linked.ID] = linked
		}
	}
}

func (s *MemoryStore) ListSessions() []app.Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]app.Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		if session.Hidden {
			continue
		}
		out = append(out, session)
	}
	slices.SortFunc(out, func(a, b app.Session) int {
		return b.UpdatedAt.Compare(a.UpdatedAt)
	})
	return out
}

func (s *MemoryStore) GetSession(id string) (app.Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[id]
	return session, ok
}

func (s *MemoryStore) UpdateSessionTitle(id, title string) (app.Session, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return app.Session{}, errors.New("session title is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	if !ok {
		return app.Session{}, errors.New("session not found")
	}
	session.Title = title
	session.UpdatedAt = time.Now().UTC()
	s.sessions[id] = session
	s.appendAuditLocked("session.updated", id, "", "owner", "Session renamed", map[string]any{"title": title})
	s.appendEventLocked("session.updated", id, "", session)
	return session, nil
}

func (s *MemoryStore) DeleteSession(id string) (app.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	if !ok {
		return app.Session{}, errors.New("session not found")
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
		}
	}
	for episodeID, summary := range s.episodeSummaries {
		if summary.SessionID == id {
			delete(s.episodeSummaries, episodeID)
		}
	}
	s.auditEvents = filterAuditEvents(s.auditEvents, id)
	s.events = filterEvents(s.events, id)
	s.appendAuditLocked("session.deleted", "", "", "owner", "Session deleted", map[string]any{"session_id": id, "title": session.Title})
	s.appendEventLocked("session.deleted", "", "", session)
	return session, nil
}

func (s *MemoryStore) SaveClient(client app.Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if client.ID == "" {
		client.ID = app.NewID("client")
	}
	if client.CreatedAt.IsZero() {
		client.CreatedAt = time.Now().UTC()
	}
	s.clients[client.ID] = client
	s.appendAuditLocked("client.saved", "", "", "gateway", client.Name, map[string]any{"client_id": client.ID})
	s.appendEventLocked("client.saved", "", "", client)
}

func (s *MemoryStore) GetClient(id string) (app.Client, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	client, ok := s.clients[id]
	return client, ok
}

func (s *MemoryStore) ListClients() []app.Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.Client{}
	for _, client := range s.clients {
		out = append(out, client)
	}
	slices.SortFunc(out, func(a, b app.Client) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	return out
}

func (s *MemoryStore) RevokeClient(id string) (app.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	client, ok := s.clients[id]
	if !ok {
		return app.Client{}, errors.New("client not found")
	}
	now := time.Now().UTC()
	client.RevokedAt = &now
	s.clients[id] = client
	s.appendAuditLocked("client.revoked", "", "", "owner", client.Name, map[string]any{"client_id": client.ID})
	s.appendEventLocked("client.revoked", "", "", client)
	return client, nil
}

func (s *MemoryStore) FindClientByTokenHash(tokenHash string) (app.Client, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, client := range s.clients {
		if client.TokenHash == tokenHash && client.RevokedAt == nil {
			return client, true
		}
	}
	return app.Client{}, false
}

func (s *MemoryStore) TouchClient(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	client, ok := s.clients[id]
	if !ok {
		return
	}
	now := time.Now().UTC()
	client.LastSeenAt = &now
	s.clients[id] = client
}

func (s *MemoryStore) GetOwnerProfile() app.OwnerProfile {
	profile, _ := s.GetOwnerProfileByID(app.DefaultOwnerID)
	return profile
}

func (s *MemoryStore) UpdateOwnerProfile(profile app.OwnerProfile) app.OwnerProfile {
	profile.ID = app.DefaultOwnerID
	return s.SaveOwnerProfile(profile)
}

func (s *MemoryStore) GetOwnerProfileByID(id string) (app.OwnerProfile, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		id = app.DefaultOwnerID
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	profile, ok := s.ownerProfiles[id]
	if !ok && id == app.DefaultOwnerID {
		profile = s.ownerProfile
		ok = true
	}
	if !ok {
		return app.OwnerProfile{}, false
	}
	return cloneOwnerProfile(normalizeOwnerProfile(profile)), true
}

func (s *MemoryStore) SaveOwnerProfile(profile app.OwnerProfile) app.OwnerProfile {
	s.mu.Lock()
	defer s.mu.Unlock()
	profile.ID = strings.TrimSpace(profile.ID)
	if profile.ID == "" {
		profile.ID = app.DefaultOwnerID
	}
	current, ok := s.ownerProfiles[profile.ID]
	if !ok && profile.ID == app.DefaultOwnerID {
		current = s.ownerProfile
		ok = true
	}
	current = normalizeOwnerProfile(current)
	now := time.Now().UTC()
	if profile.CreatedAt.IsZero() {
		profile.CreatedAt = current.CreatedAt
	}
	if !ok || profile.CreatedAt.IsZero() {
		profile.CreatedAt = now
	}
	profile.UpdatedAt = now
	profile.Preferences = cloneStringMap(profile.Preferences)
	profile = normalizeOwnerProfile(profile)
	if s.ownerProfiles == nil {
		s.ownerProfiles = map[string]app.OwnerProfile{}
	}
	s.ownerProfiles[profile.ID] = profile
	if profile.ID == app.DefaultOwnerID {
		s.ownerProfile = profile
	}
	s.appendAuditLocked("owner_profile.updated", "", "", "owner", profile.DisplayName, map[string]any{
		"owner_id":     profile.ID,
		"source":       profile.Source,
		"external_ref": profile.ExternalRef != "",
		"email_set":    profile.Email != "",
		"preferences":  len(profile.Preferences),
		"display_name": profile.DisplayName,
	})
	s.appendEventLocked("owner_profile.updated", "", "", profile)
	return cloneOwnerProfile(profile)
}

func (s *MemoryStore) ListOwnerProfiles() []app.OwnerProfile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]app.OwnerProfile, 0, len(s.ownerProfiles))
	for _, profile := range s.ownerProfiles {
		out = append(out, cloneOwnerProfile(normalizeOwnerProfile(profile)))
	}
	slices.SortFunc(out, func(a, b app.OwnerProfile) int {
		return b.UpdatedAt.Compare(a.UpdatedAt)
	})
	return out
}

func (s *MemoryStore) FindOwnerProfileByExternalRef(source, externalRef string) (app.OwnerProfile, bool) {
	source = strings.TrimSpace(source)
	externalRef = strings.TrimSpace(externalRef)
	if source == "" || externalRef == "" {
		return app.OwnerProfile{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, profile := range s.ownerProfiles {
		if strings.TrimSpace(profile.Source) == source && strings.TrimSpace(profile.ExternalRef) == externalRef {
			return cloneOwnerProfile(normalizeOwnerProfile(profile)), true
		}
	}
	return app.OwnerProfile{}, false
}

func (s *MemoryStore) SavePairingCode(code app.PairingCode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if code.ID == "" {
		code.ID = app.NewID("pair")
	}
	if code.CreatedAt.IsZero() {
		code.CreatedAt = time.Now().UTC()
	}
	if code.Status == "" {
		code.Status = "pending"
	}
	s.pairingCodes[code.ID] = code
	s.appendAuditLocked("pairing_code.created", "", "", "gateway", "Pairing code created", map[string]any{"pairing_id": code.ID})
	s.appendEventLocked("pairing_code.created", "", "", code)
}

func (s *MemoryStore) GetPairingCode(id string) (app.PairingCode, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	code, ok := s.pairingCodes[id]
	return code, ok
}

func (s *MemoryStore) ClaimPairingCode(id, clientID string) (app.PairingCode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	code, ok := s.pairingCodes[id]
	if !ok {
		return app.PairingCode{}, errors.New("pairing code not found")
	}
	if code.Status != "pending" {
		return app.PairingCode{}, errors.New("pairing code is not pending")
	}
	now := time.Now().UTC()
	if now.After(code.ExpiresAt) {
		code.Status = "expired"
		s.pairingCodes[id] = code
		return app.PairingCode{}, errors.New("pairing code expired")
	}
	code.Status = "claimed"
	code.ClaimedAt = &now
	code.ClientID = clientID
	s.pairingCodes[id] = code
	s.appendAuditLocked("pairing_code.claimed", "", "", "gateway", "Pairing code claimed", map[string]any{"pairing_id": code.ID, "client_id": clientID})
	s.appendEventLocked("pairing_code.claimed", "", "", code)
	return code, nil
}

func (s *MemoryStore) AddMessage(message app.Message) app.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	if message.ID == "" {
		message.ID = app.NewID("m")
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = time.Now().UTC()
	}
	s.messages[message.SessionID] = append(s.messages[message.SessionID], message)
	if session, ok := s.sessions[message.SessionID]; ok {
		session.UpdatedAt = message.CreatedAt
		if !session.Hidden && (session.Title == "" || session.Title == "New SparkClaw Session") {
			session.Title = deriveTitle(message.Content)
		}
		s.sessions[message.SessionID] = session
	}
	s.appendEventLocked("message.created", message.SessionID, message.RunID, message)
	return message
}

func (s *MemoryStore) ListMessages(sessionID string) []app.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	messages := s.messages[sessionID]
	if len(messages) == 0 {
		return []app.Message{}
	}
	return append([]app.Message{}, messages...)
}

func (s *MemoryStore) SaveRunFeedback(feedback app.RunFeedback) app.RunFeedback {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if feedback.ID == "" {
		feedback.ID = app.NewID("fb")
	}
	if feedback.CreatedAt.IsZero() {
		feedback.CreatedAt = now
	}
	feedback.UpdatedAt = now
	feedback.Rating = strings.TrimSpace(feedback.Rating)
	feedback.Note = strings.TrimSpace(feedback.Note)
	feedback.Correction = strings.TrimSpace(feedback.Correction)
	items := s.runFeedback[feedback.RunID]
	replaced := false
	for i, existing := range items {
		if existing.ID == feedback.ID || existing.MessageID != "" && existing.MessageID == feedback.MessageID {
			feedback.ID = existing.ID
			feedback.CreatedAt = existing.CreatedAt
			items[i] = feedback
			replaced = true
			break
		}
	}
	if !replaced {
		items = append(items, feedback)
	}
	s.runFeedback[feedback.RunID] = items
	s.appendAuditLocked("run_feedback.saved", feedback.SessionID, feedback.RunID, "owner", feedback.Rating, map[string]any{
		"feedback_id":    feedback.ID,
		"message_id":     feedback.MessageID,
		"has_note":       feedback.Note != "",
		"has_correction": feedback.Correction != "",
	})
	s.appendEventLocked("run_feedback.saved", feedback.SessionID, feedback.RunID, feedback)
	return feedback
}

func (s *MemoryStore) ListRunFeedback(runID string) []app.RunFeedback {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.RunFeedback{}
	if runID != "" {
		out = append(out, s.runFeedback[runID]...)
	} else {
		for _, items := range s.runFeedback {
			out = append(out, items...)
		}
	}
	slices.SortFunc(out, func(a, b app.RunFeedback) int {
		return b.UpdatedAt.Compare(a.UpdatedAt)
	})
	return out
}

func (s *MemoryStore) SaveRun(run app.AgentRun) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs[run.ID] = run
	s.appendEventLocked("run."+run.State, run.SessionID, run.ID, run)
}

func (s *MemoryStore) GetRun(id string) (app.AgentRun, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	run, ok := s.runs[id]
	return run, ok
}

func (s *MemoryStore) ListRuns(sessionID string) []app.AgentRun {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.AgentRun{}
	for _, run := range s.runs {
		if sessionID == "" || run.SessionID == sessionID {
			out = append(out, run)
		}
	}
	slices.SortFunc(out, func(a, b app.AgentRun) int {
		return b.StartedAt.Compare(a.StartedAt)
	})
	return out
}

func (s *MemoryStore) SaveModelCall(call app.ModelCall) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if call.ID == "" {
		call.ID = app.NewID("mc")
	}
	if call.StartedAt.IsZero() {
		call.StartedAt = time.Now().UTC()
	}
	s.modelCalls[call.ID] = call
	s.appendAuditLocked("model_call."+call.Status, call.SessionID, call.RunID, "model-router", call.Model, map[string]any{
		"lane":       call.Lane,
		"profile":    call.Profile,
		"operation":  call.Operation,
		"latency_ms": call.LatencyMS,
	})
	s.appendEventLocked("model_call."+call.Status, call.SessionID, call.RunID, call)
}

func (s *MemoryStore) ListModelCalls(sessionID, runID string) []app.ModelCall {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.ModelCall{}
	for _, call := range s.modelCalls {
		if (sessionID == "" || call.SessionID == sessionID) && (runID == "" || call.RunID == runID) {
			out = append(out, call)
		}
	}
	slices.SortFunc(out, func(a, b app.ModelCall) int {
		return a.StartedAt.Compare(b.StartedAt)
	})
	return out
}

func (s *MemoryStore) SaveToolCall(call app.ToolCall) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolCalls[call.ID] = call
	s.appendAuditLocked("tool_call."+call.Status, call.SessionID, call.RunID, "agent", call.Tool, map[string]any{
		"risk": call.Risk,
		"id":   call.ID,
	})
	s.appendEventLocked("tool_call."+call.Status, call.SessionID, call.RunID, call)
}

func (s *MemoryStore) GetToolCall(id string) (app.ToolCall, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	call, ok := s.toolCalls[id]
	return call, ok
}

func (s *MemoryStore) ListToolCalls(sessionID string) []app.ToolCall {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.ToolCall{}
	for _, call := range s.toolCalls {
		if sessionID == "" || call.SessionID == sessionID {
			out = append(out, call)
		}
	}
	slices.SortFunc(out, func(a, b app.ToolCall) int {
		return a.StartedAt.Compare(b.StartedAt)
	})
	return out
}

func (s *MemoryStore) SaveApproval(approval app.Approval) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approvals[approval.ID] = approval
	s.appendAuditLocked("approval."+approval.Status, approval.SessionID, approval.RunID, "policy", approval.Summary, map[string]any{
		"tool": approval.Tool,
		"risk": approval.Risk,
	})
	s.appendEventLocked("approval."+approval.Status, approval.SessionID, approval.RunID, approval)
}

func (s *MemoryStore) ResolveApproval(id, status, note string) (app.Approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	approval, ok := s.approvals[id]
	if !ok {
		return app.Approval{}, errors.New("approval not found")
	}
	if approval.Status != "pending" {
		return app.Approval{}, errors.New("approval already resolved")
	}
	now := time.Now().UTC()
	approval.Status = status
	approval.ResolvedAt = &now
	approval.ResolutionNote = note
	s.approvals[id] = approval
	s.appendAuditLocked("approval."+status, approval.SessionID, approval.RunID, "owner", approval.Summary, map[string]any{"note": note})
	s.appendEventLocked("approval."+status, approval.SessionID, approval.RunID, approval)
	return approval, nil
}

func (s *MemoryStore) ListApprovals(status string) []app.Approval {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.Approval{}
	for _, approval := range s.approvals {
		if status == "" || approval.Status == status {
			out = append(out, approval)
		}
	}
	slices.SortFunc(out, func(a, b app.Approval) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	return out
}

func (s *MemoryStore) SaveReminder(reminder app.Reminder) app.Reminder {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if reminder.ID == "" {
		reminder.ID = app.NewID("rem")
	}
	if reminder.CreatedAt.IsZero() {
		reminder.CreatedAt = now
	}
	if reminder.UpdatedAt.IsZero() {
		reminder.UpdatedAt = now
	}
	if reminder.Status == "" {
		reminder.Status = "pending"
	}
	if reminder.TextSummary == "" {
		reminder.TextSummary = summarizeReminderText(reminder.Text)
	}
	s.reminders[reminder.ID] = reminder
	s.appendAuditLocked("reminder."+reminder.Status, reminder.SessionID, reminder.RunID, "toolhub", reminder.TextSummary, map[string]any{
		"reminder_id": reminder.ID,
		"due_time":    reminder.DueTime.UTC().Format(time.RFC3339),
		"channel":     reminder.Channel,
	})
	s.appendEventLocked("reminder."+reminder.Status, reminder.SessionID, reminder.RunID, reminder)
	return reminder
}

func (s *MemoryStore) GetReminder(id string) (app.Reminder, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	reminder, ok := s.reminders[id]
	return reminder, ok
}

func (s *MemoryStore) ListReminders(filter app.ReminderFilter) []app.Reminder {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.Reminder{}
	for _, reminder := range s.reminders {
		if filter.Status != "" && reminder.Status != filter.Status {
			continue
		}
		if filter.From != nil && reminder.DueTime.Before(filter.From.UTC()) {
			continue
		}
		if filter.To != nil && reminder.DueTime.After(filter.To.UTC()) {
			continue
		}
		out = append(out, reminder)
	}
	slices.SortFunc(out, func(a, b app.Reminder) int {
		return a.DueTime.Compare(b.DueTime)
	})
	if filter.Limit > 0 && len(out) > filter.Limit {
		return out[:filter.Limit]
	}
	return out
}

// ClaimDueReminders atomically flips due pending reminders to "sending" and
// returns them, so overlapping ticks cannot deliver the same reminder twice.
// Reminders left in "sending" since before staleBefore (a crashed or hung
// delivery) are reclaimed.
func (s *MemoryStore) ClaimDueReminders(now, staleBefore time.Time, limit int) []app.Reminder {
	s.mu.Lock()
	defer s.mu.Unlock()
	now = now.UTC()
	claimed := []app.Reminder{}
	for _, reminder := range s.reminders {
		switch reminder.Status {
		case "pending":
			if reminder.DueTime.After(now) {
				continue
			}
		case "sending":
			if reminder.UpdatedAt.After(staleBefore.UTC()) {
				continue
			}
		default:
			continue
		}
		claimed = append(claimed, reminder)
	}
	slices.SortFunc(claimed, func(a, b app.Reminder) int {
		return a.DueTime.Compare(b.DueTime)
	})
	if limit > 0 && len(claimed) > limit {
		claimed = claimed[:limit]
	}
	for i, reminder := range claimed {
		reminder.Status = "sending"
		reminder.UpdatedAt = now
		s.reminders[reminder.ID] = reminder
		claimed[i] = reminder
	}
	return claimed
}

func (s *MemoryStore) SaveReminderDelivery(delivery app.ReminderDelivery) app.ReminderDelivery {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if delivery.ID == "" {
		delivery.ID = app.NewID("rdel")
	}
	if delivery.CreatedAt.IsZero() {
		delivery.CreatedAt = now
	}
	s.reminderDelivery[delivery.ID] = delivery
	if reminder, ok := s.reminders[delivery.ReminderID]; ok {
		reminder.LastDeliveryID = delivery.ID
		reminder.LastError = delivery.Error
		reminder.DeliveryAttempt = delivery.Attempt
		if delivery.Status == "sent" {
			sentAt := delivery.SentAt
			if sentAt.IsZero() {
				sentAt = now
			}
			reminder.SentAt = &sentAt
			reminder.Status = "sent"
		} else if delivery.Status == "failed" {
			reminder.Status = "failed"
		}
		reminder.UpdatedAt = now
		s.reminders[reminder.ID] = reminder
	}
	s.appendAuditLocked("reminder_delivery."+delivery.Status, "", "", "scheduler", delivery.ProviderStatus, map[string]any{
		"delivery_id": delivery.ID,
		"reminder_id": delivery.ReminderID,
		"channel":     delivery.Channel,
		"provider":    delivery.Provider,
		"attempt":     delivery.Attempt,
	})
	s.appendEventLocked("reminder_delivery."+delivery.Status, "", delivery.ReminderID, delivery)
	return delivery
}

func (s *MemoryStore) ListReminderDeliveries(reminderID string) []app.ReminderDelivery {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.ReminderDelivery{}
	for _, delivery := range s.reminderDelivery {
		if reminderID == "" || delivery.ReminderID == reminderID {
			out = append(out, delivery)
		}
	}
	slices.SortFunc(out, func(a, b app.ReminderDelivery) int {
		return a.CreatedAt.Compare(b.CreatedAt)
	})
	return out
}

func (s *MemoryStore) SaveNotificationBinding(binding app.NotificationBinding) app.NotificationBinding {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if binding.ID == "" {
		binding.ID = app.NewID("bind")
	}
	if binding.OwnerID == "" {
		binding.OwnerID = app.DefaultOwnerID
	}
	if binding.CreatedAt.IsZero() {
		binding.CreatedAt = now
	}
	binding.UpdatedAt = now
	if binding.Status == "" {
		binding.Status = "waiting_scan"
	}
	if binding.DefaultForChannel {
		for id, existing := range s.notificationBindings {
			if existing.OwnerID == binding.OwnerID && existing.Channel == binding.Channel && existing.ID != binding.ID {
				existing.DefaultForChannel = false
				existing.UpdatedAt = now
				s.notificationBindings[id] = existing
			}
		}
	}
	s.notificationBindings[binding.ID] = binding
	s.appendAuditLocked("notification_binding."+binding.Status, "", "", "owner", binding.Channel, map[string]any{
		"binding_id": binding.ID,
		"channel":    binding.Channel,
		"provider":   binding.Provider,
		"default":    binding.DefaultForChannel,
	})
	s.appendEventLocked("notification_binding."+binding.Status, "", "", binding)
	return binding
}

func (s *MemoryStore) GetNotificationBinding(id string) (app.NotificationBinding, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	binding, ok := s.notificationBindings[id]
	return binding, ok
}

func (s *MemoryStore) ListNotificationBindings(channel, status string) []app.NotificationBinding {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.NotificationBinding{}
	for _, binding := range s.notificationBindings {
		if channel != "" && binding.Channel != channel {
			continue
		}
		if status != "" && binding.Status != status {
			continue
		}
		out = append(out, binding)
	}
	slices.SortFunc(out, func(a, b app.NotificationBinding) int {
		return b.UpdatedAt.Compare(a.UpdatedAt)
	})
	return out
}

func (s *MemoryStore) RevokeNotificationBinding(id string) (app.NotificationBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	binding, ok := s.notificationBindings[id]
	if !ok {
		return app.NotificationBinding{}, errors.New("notification binding not found")
	}
	if binding.Status == "revoked" {
		return binding, nil
	}
	now := time.Now().UTC()
	binding.Status = "revoked"
	binding.RevokedAt = &now
	binding.UpdatedAt = now
	binding.DefaultForChannel = false
	s.notificationBindings[id] = binding
	s.appendAuditLocked("notification_binding.revoked", "", "", "owner", binding.Channel, map[string]any{
		"binding_id": binding.ID,
		"channel":    binding.Channel,
		"provider":   binding.Provider,
	})
	s.appendEventLocked("notification_binding.revoked", "", "", binding)
	return binding, nil
}

func (s *MemoryStore) SaveWeixinChatSession(session app.WeixinChatSession) app.WeixinChatSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if session.ID == "" {
		session.ID = app.NewID("wxchat")
	}
	if session.Channel == "" {
		session.Channel = "weixin"
	}
	if session.Status == "" {
		session.Status = "active"
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	session.UpdatedAt = now
	if linked, ok := s.sessions[session.LinkedSessionID]; ok {
		linked.Source = "weixin"
		linked.Hidden = true
		if strings.TrimSpace(session.OwnerID) != "" {
			linked.OwnerID = session.OwnerID
		}
		if strings.TrimSpace(session.WorkspaceRoot) != "" {
			linked.WorkspaceRoot = session.WorkspaceRoot
		}
		linked.UpdatedAt = now
		if linked.Title == "" || linked.Title == "微信会话" {
			linked.Title = "微信会话"
		}
		s.sessions[linked.ID] = linked
	}
	s.weixinChatSessions[session.ID] = session
	s.appendAuditLocked("weixin_chat_session."+session.Status, session.LinkedSessionID, "", "gateway", redactExternalID(session.ExternalUserID), map[string]any{
		"chat_session_id": session.ID,
		"binding_id":      session.BindingID,
		"provider":        session.Provider,
	})
	s.appendEventLocked("weixin_chat_session."+session.Status, session.LinkedSessionID, "", session)
	return session
}

func (s *MemoryStore) GetWeixinChatSession(id string) (app.WeixinChatSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.weixinChatSessions[id]
	return session, ok
}

func (s *MemoryStore) FindWeixinChatSession(bindingID, externalUserID string) (app.WeixinChatSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, session := range s.weixinChatSessions {
		if session.BindingID == bindingID && session.ExternalUserID == externalUserID {
			return session, true
		}
	}
	return app.WeixinChatSession{}, false
}

func (s *MemoryStore) FindWeixinChatSessionByLinkedSessionID(sessionID string) (app.WeixinChatSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, session := range s.weixinChatSessions {
		if session.LinkedSessionID == sessionID {
			return session, true
		}
	}
	return app.WeixinChatSession{}, false
}

func (s *MemoryStore) SaveWeixinChatMessage(message app.WeixinChatMessage) app.WeixinChatMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if message.ID == "" {
		message.ID = app.NewID("wxmsg")
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = now
	}
	message.UpdatedAt = now
	s.weixinChatMessages[message.ID] = message
	s.appendAuditLocked("weixin_chat_message."+message.Status, "", message.LinkedRunID, "gateway", message.Direction, map[string]any{
		"message_id":      message.ID,
		"chat_session_id": message.ChatSessionID,
		"binding_id":      message.BindingID,
		"direction":       message.Direction,
		"role":            message.Role,
	})
	s.appendEventLocked("weixin_chat_message."+message.Status, "", message.LinkedRunID, message)
	return message
}

func (s *MemoryStore) GetWeixinChatMessage(id string) (app.WeixinChatMessage, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	message, ok := s.weixinChatMessages[id]
	return message, ok
}

func (s *MemoryStore) FindWeixinChatMessageByExternalID(chatSessionID, externalMessageID string) (app.WeixinChatMessage, bool) {
	if strings.TrimSpace(externalMessageID) == "" {
		return app.WeixinChatMessage{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, message := range s.weixinChatMessages {
		if message.ChatSessionID == chatSessionID && message.ExternalMessageID == externalMessageID {
			return message, true
		}
	}
	return app.WeixinChatMessage{}, false
}

func (s *MemoryStore) ListWeixinChatMessages(chatSessionID string, limit int) []app.WeixinChatMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.WeixinChatMessage{}
	for _, message := range s.weixinChatMessages {
		if chatSessionID == "" || message.ChatSessionID == chatSessionID {
			out = append(out, message)
		}
	}
	slices.SortFunc(out, func(a, b app.WeixinChatMessage) int {
		return a.CreatedAt.Compare(b.CreatedAt)
	})
	if limit > 0 && len(out) > limit {
		return out[len(out)-limit:]
	}
	return out
}

func (s *MemoryStore) SaveCredentialSecret(secret app.CredentialSecret) app.CredentialSecret {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if secret.CreatedAt.IsZero() {
		secret.CreatedAt = now
	}
	secret.UpdatedAt = now
	s.credentialSecrets[secret.Ref] = secret
	s.appendAuditLocked("credential_secret.saved", "", "", "gateway", secret.Kind, map[string]any{
		"ref":  secret.Ref,
		"kind": secret.Kind,
	})
	return secret
}

func (s *MemoryStore) GetCredentialSecret(ref string) (app.CredentialSecret, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	secret, ok := s.credentialSecrets[ref]
	return secret, ok
}

func (s *MemoryStore) DeleteCredentialSecret(ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.credentialSecrets[ref]; !ok {
		return errors.New("credential secret not found")
	}
	delete(s.credentialSecrets, ref)
	s.appendAuditLocked("credential_secret.deleted", "", "", "gateway", "credential deleted", map[string]any{"ref": ref})
	return nil
}

func (s *MemoryStore) SaveBrowserAuthRecord(record app.BrowserAuthRecord) app.BrowserAuthRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	record = normalizeBrowserAuthRecord(record, s.browserAuthRecords[record.ID])
	s.browserAuthRecords[record.ID] = record
	s.appendAuditLocked("browser_auth.record_saved", "", "", "gateway", record.SiteOrigin, browserAuthAuditFields(record, nil))
	s.appendEventLocked("browser_auth.record_saved", "", "", record)
	return record
}

func (s *MemoryStore) GetBrowserAuthRecord(id string) (app.BrowserAuthRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.browserAuthRecords[strings.TrimSpace(id)]
	if !ok {
		return app.BrowserAuthRecord{}, false
	}
	return record, true
}

func (s *MemoryStore) FindBrowserAuthRecord(ownerID, browserProfileID, siteOrigin, siteRealm, accountHint string) (app.BrowserAuthRecord, bool) {
	ownerID, browserProfileID, siteOrigin, siteRealm, accountHint = normalizeBrowserAuthLookup(ownerID, browserProfileID, siteOrigin, siteRealm, accountHint)
	s.mu.RLock()
	defer s.mu.RUnlock()
	matches := []app.BrowserAuthRecord{}
	now := time.Now().UTC()
	for _, record := range s.browserAuthRecords {
		record = normalizeBrowserAuthRecord(record, app.BrowserAuthRecord{})
		if record.OwnerID != ownerID || record.BrowserProfileID != browserProfileID || record.SiteOrigin != siteOrigin || record.SiteRealm != siteRealm || record.AccountHint != accountHint {
			continue
		}
		if record.Status != app.BrowserAuthStatusActive || record.RevokedAt != nil {
			continue
		}
		if record.ExpiresAt != nil && !record.ExpiresAt.After(now) {
			continue
		}
		matches = append(matches, record)
	}
	if len(matches) == 0 {
		return app.BrowserAuthRecord{}, false
	}
	slices.SortFunc(matches, func(a, b app.BrowserAuthRecord) int {
		return b.UpdatedAt.Compare(a.UpdatedAt)
	})
	return matches[0], true
}

func (s *MemoryStore) ListBrowserAuthRecords(ownerID, browserProfileID string) []app.BrowserAuthRecord {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID != "" {
		ownerID = normalizeBrowserAuthOwnerID(ownerID)
	}
	browserProfileID = strings.TrimSpace(browserProfileID)
	if browserProfileID != "" {
		browserProfileID = normalizeBrowserProfileID(browserProfileID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.BrowserAuthRecord{}
	for _, record := range s.browserAuthRecords {
		record = normalizeBrowserAuthRecord(record, app.BrowserAuthRecord{})
		if ownerID != "" && record.OwnerID != ownerID {
			continue
		}
		if browserProfileID != "" && record.BrowserProfileID != browserProfileID {
			continue
		}
		out = append(out, record)
	}
	slices.SortFunc(out, func(a, b app.BrowserAuthRecord) int {
		return b.UpdatedAt.Compare(a.UpdatedAt)
	})
	return out
}

func (s *MemoryStore) RevokeBrowserAuthRecord(id, reason string) (app.BrowserAuthRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id = strings.TrimSpace(id)
	record, ok := s.browserAuthRecords[id]
	if !ok {
		return app.BrowserAuthRecord{}, errors.New("browser auth record not found")
	}
	now := time.Now().UTC()
	record.Status = app.BrowserAuthStatusRevoked
	record.RevokedAt = &now
	record.UpdatedAt = now
	record.LastError = strings.TrimSpace(reason)
	s.browserAuthRecords[id] = record
	s.appendAuditLocked("browser_auth.record_revoked", "", "", "owner", record.SiteOrigin, browserAuthAuditFields(record, map[string]any{"reason": record.LastError}))
	s.appendEventLocked("browser_auth.record_revoked", "", "", record)
	return record, nil
}

func (s *MemoryStore) SaveBrowserLoginBlock(block app.BrowserLoginBlock) app.BrowserLoginBlock {
	s.mu.Lock()
	defer s.mu.Unlock()
	block = normalizeBrowserLoginBlock(block, s.browserLoginBlocks[block.ID])
	s.browserLoginBlocks[block.ID] = block
	s.appendAuditLocked("browser_login_block."+block.Status, block.SessionID, block.RunID, "runtime", block.SiteOrigin, browserLoginBlockAuditFields(block, nil))
	s.appendEventLocked("browser_login_block."+block.Status, block.SessionID, block.RunID, block)
	return block
}

func (s *MemoryStore) GetBrowserLoginBlock(id string) (app.BrowserLoginBlock, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	block, ok := s.browserLoginBlocks[strings.TrimSpace(id)]
	if !ok {
		return app.BrowserLoginBlock{}, false
	}
	return block, true
}

func (s *MemoryStore) FindActiveBrowserLoginBlock(sessionID string) (app.BrowserLoginBlock, bool) {
	blocks := s.ListBrowserLoginBlocks(sessionID, "")
	for _, block := range blocks {
		if browserLoginBlockActive(block.Status) {
			return block, true
		}
	}
	return app.BrowserLoginBlock{}, false
}

func (s *MemoryStore) ListBrowserLoginBlocks(sessionID, status string) []app.BrowserLoginBlock {
	sessionID = strings.TrimSpace(sessionID)
	status = strings.TrimSpace(status)
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.BrowserLoginBlock{}
	for _, block := range s.browserLoginBlocks {
		block = normalizeBrowserLoginBlock(block, app.BrowserLoginBlock{})
		if sessionID != "" && block.SessionID != sessionID {
			continue
		}
		if status != "" && block.Status != status {
			continue
		}
		out = append(out, block)
	}
	slices.SortFunc(out, func(a, b app.BrowserLoginBlock) int {
		return b.UpdatedAt.Compare(a.UpdatedAt)
	})
	return out
}

func (s *MemoryStore) AddMemoryCandidate(candidate app.MemoryCandidate) app.MemoryCandidate {
	s.mu.Lock()
	defer s.mu.Unlock()
	if candidate.ID == "" {
		candidate.ID = app.NewID("mc")
	}
	if candidate.CreatedAt.IsZero() {
		candidate.CreatedAt = time.Now().UTC()
	}
	if candidate.Status == "" {
		candidate.Status = "pending"
	}
	s.memoryCandidates[candidate.ID] = candidate
	s.appendAuditLocked("memory_candidate.created", candidate.SessionID, candidate.RunID, "agent", candidate.Content, map[string]any{"kind": candidate.Kind})
	s.appendEventLocked("memory_candidate.created", candidate.SessionID, candidate.RunID, candidate)
	return candidate
}

func (s *MemoryStore) ResolveMemoryCandidate(id, status string) (app.MemoryCandidate, *app.Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate, ok := s.memoryCandidates[id]
	if !ok {
		return app.MemoryCandidate{}, nil, errors.New("memory candidate not found")
	}
	if candidate.Status != "pending" {
		return app.MemoryCandidate{}, nil, errors.New("memory candidate already resolved")
	}
	now := time.Now().UTC()
	candidate.Status = status
	candidate.ResolvedAt = &now
	s.memoryCandidates[id] = candidate
	var memory *app.Memory
	if status == "accepted" {
		m := app.Memory{
			ID:        app.NewID("mem"),
			Kind:      candidate.Kind,
			Content:   candidate.Content,
			SourceID:  candidate.RunID,
			CreatedAt: now,
		}
		s.memories[m.ID] = m
		memory = &m
	}
	s.appendAuditLocked("memory_candidate."+status, candidate.SessionID, candidate.RunID, "owner", candidate.Content, nil)
	s.appendEventLocked("memory_candidate."+status, candidate.SessionID, candidate.RunID, candidate)
	return candidate, memory, nil
}

func (s *MemoryStore) ListMemoryCandidates(status string) []app.MemoryCandidate {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.MemoryCandidate{}
	for _, candidate := range s.memoryCandidates {
		if status == "" || candidate.Status == status {
			out = append(out, candidate)
		}
	}
	slices.SortFunc(out, func(a, b app.MemoryCandidate) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	return out
}

func (s *MemoryStore) SearchMemories(query string) []app.Memory {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.Memory{}
	q := strings.ToLower(query)
	for _, memory := range s.memories {
		if q == "" || strings.Contains(strings.ToLower(memory.Content), q) || strings.Contains(strings.ToLower(memory.Kind), q) {
			out = append(out, memory)
		}
	}
	slices.SortFunc(out, func(a, b app.Memory) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	return out
}

func (s *MemoryStore) UpdateMemory(id, kind, content string) (app.Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	memory, ok := s.memories[id]
	if !ok {
		return app.Memory{}, errors.New("memory not found")
	}
	memory.Kind = kind
	memory.Content = content
	s.memories[id] = memory
	sessionID := s.sessionIDForRunLocked(memory.SourceID)
	s.appendAuditLocked("memory.updated", sessionID, memory.SourceID, "owner", memory.Content, map[string]any{"memory_id": memory.ID, "kind": memory.Kind})
	s.appendEventLocked("memory.updated", sessionID, memory.SourceID, memory)
	return memory, nil
}

func (s *MemoryStore) DeleteMemory(id string) (app.Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	memory, ok := s.memories[id]
	if !ok {
		return app.Memory{}, errors.New("memory not found")
	}
	delete(s.memories, id)
	sessionID := s.sessionIDForRunLocked(memory.SourceID)
	s.appendAuditLocked("memory.deleted", sessionID, memory.SourceID, "owner", memory.Content, map[string]any{"memory_id": memory.ID, "kind": memory.Kind})
	s.appendEventLocked("memory.deleted", sessionID, memory.SourceID, memory)
	return memory, nil
}

func (s *MemoryStore) PruneMemories(cutoff time.Time) []app.Memory {
	if cutoff.IsZero() {
		return []app.Memory{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pruned := []app.Memory{}
	for id, memory := range s.memories {
		if memory.CreatedAt.IsZero() || !memory.CreatedAt.Before(cutoff) {
			continue
		}
		delete(s.memories, id)
		pruned = append(pruned, memory)
		sessionID := s.sessionIDForRunLocked(memory.SourceID)
		s.appendAuditLocked("memory.pruned", sessionID, memory.SourceID, "memory-retention", memory.Kind, map[string]any{
			"memory_id": memory.ID,
			"cutoff":    cutoff.UTC().Format(time.RFC3339),
		})
		s.appendEventLocked("memory.pruned", sessionID, memory.SourceID, memory)
	}
	slices.SortFunc(pruned, func(a, b app.Memory) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	return pruned
}

func (s *MemoryStore) AddAudit(event app.AuditEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if event.ID == "" {
		event.ID = app.NewID("audit")
	}
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	s.auditEvents = append(s.auditEvents, event)
}

func (s *MemoryStore) ListAudit(sessionID string) []app.AuditEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.AuditEvent{}
	for _, event := range s.auditEvents {
		if sessionID == "" || event.SessionID == sessionID {
			out = append(out, event)
		}
	}
	slices.SortFunc(out, func(a, b app.AuditEvent) int {
		return b.Time.Compare(a.Time)
	})
	return out
}

func (s *MemoryStore) EventsAfter(sessionID, after string) []app.Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.Event{}
	started := after == ""
	for _, event := range s.events {
		if !started {
			if event.ID == after {
				started = true
			}
			continue
		}
		if sessionID == "" || event.SessionID == sessionID {
			out = append(out, event)
		}
	}
	return out
}

func (s *MemoryStore) SaveEvalRun(run app.EvalRun) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if run.ID == "" {
		run.ID = app.NewID("eval")
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now().UTC()
	}
	s.evalRuns[run.ID] = run
	s.appendAuditLocked("eval."+run.Status, "", "", "evaluator", run.Summary, map[string]any{
		"profile":          run.Profile,
		"id":               run.ID,
		"failure_archives": len(run.FailureArchives),
	})
	s.appendEventLocked("eval."+run.Status, "", run.ID, run)
}

func (s *MemoryStore) GetEvalRun(id string) (app.EvalRun, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	run, ok := s.evalRuns[id]
	return run, ok
}

func (s *MemoryStore) ListEvalRuns() []app.EvalRun {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.EvalRun{}
	for _, run := range s.evalRuns {
		out = append(out, run)
	}
	slices.SortFunc(out, func(a, b app.EvalRun) int {
		return b.StartedAt.Compare(a.StartedAt)
	})
	return out
}

func (s *MemoryStore) SaveArtifactObject(object app.ArtifactObject) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if object.ID == "" {
		object.ID = app.NewID("obj")
	}
	if object.CreatedAt.IsZero() {
		object.CreatedAt = time.Now().UTC()
	}
	s.artifactObjects[object.ID] = object
	s.appendAuditLocked("artifact.saved", object.SessionID, object.RunID, "artifact-store", object.URI, map[string]any{
		"kind":    object.Kind,
		"backend": object.Backend,
		"key":     object.Key,
		"bytes":   object.Bytes,
		"eval_id": object.EvalID,
	})
	s.appendEventLocked("artifact.saved", object.SessionID, object.RunID, object)
}

func (s *MemoryStore) ListArtifactObjects(limit int) []app.ArtifactObject {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.ArtifactObject{}
	for _, object := range s.artifactObjects {
		out = append(out, object)
	}
	slices.SortFunc(out, func(a, b app.ArtifactObject) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	if limit > 0 && len(out) > limit {
		return out[:limit]
	}
	return out
}

func (s *MemoryStore) SaveEpisodeSummary(summary app.EpisodeSummary) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if summary.ID == "" {
		summary.ID = app.NewID("ep")
	}
	if summary.CreatedAt.IsZero() {
		summary.CreatedAt = time.Now().UTC()
	}
	s.episodeSummaries[summary.ID] = summary
	s.appendAuditLocked("episode_summary.saved", summary.SessionID, summary.RunID, "runtime", summary.Outcome, map[string]any{
		"tools":            summary.Tools,
		"repair_performed": summary.RepairPerformed,
	})
	s.appendEventLocked("episode_summary.saved", summary.SessionID, summary.RunID, summary)
}

func (s *MemoryStore) ListEpisodeSummaries(sessionID string) []app.EpisodeSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.EpisodeSummary{}
	for _, summary := range s.episodeSummaries {
		if sessionID == "" || summary.SessionID == sessionID {
			out = append(out, summary)
		}
	}
	slices.SortFunc(out, func(a, b app.EpisodeSummary) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	return out
}

func (s *MemoryStore) appendAuditLocked(typ, sessionID, runID, actor, summary string, fields map[string]any) {
	s.auditEvents = append(s.auditEvents, app.AuditEvent{
		ID:        app.NewID("audit"),
		Time:      time.Now().UTC(),
		Type:      typ,
		SessionID: sessionID,
		RunID:     runID,
		Actor:     actor,
		Summary:   summary,
		Fields:    fields,
	})
}

func (s *MemoryStore) appendEventLocked(typ, sessionID, runID string, payload any) {
	s.events = append(s.events, app.Event{
		ID:        app.NewID("evt"),
		Time:      time.Now().UTC(),
		Type:      typ,
		SessionID: sessionID,
		RunID:     runID,
		Payload:   payload,
	})
}

func (s *MemoryStore) sessionIDForRunLocked(runID string) string {
	if run, ok := s.runs[runID]; ok {
		return run.SessionID
	}
	return ""
}

func deriveTitle(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return "New SparkClaw Session"
	}
	runes := []rune(content)
	if len(runes) > 42 {
		return string(runes[:42]) + "..."
	}
	return content
}

func summarizeReminderText(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return "Reminder"
	}
	runes := []rune(content)
	if len(runes) > 80 {
		return string(runes[:80]) + "..."
	}
	return content
}

func redactExternalID(value string) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= 6 {
		return value
	}
	return string(runes[:3]) + "***" + string(runes[len(runes)-2:])
}

func cloneMap[T any](in map[string]T) map[string]T {
	out := map[string]T{}
	for key, value := range in {
		out[key] = value
	}
	return out
}

func filterAuditEvents(events []app.AuditEvent, sessionID string) []app.AuditEvent {
	out := events[:0]
	for _, event := range events {
		if event.SessionID != sessionID {
			out = append(out, event)
		}
	}
	return out
}

func filterEvents(events []app.Event, sessionID string) []app.Event {
	out := events[:0]
	for _, event := range events {
		if event.SessionID != sessionID {
			out = append(out, event)
		}
	}
	return out
}

func cloneSliceMap[T any](in map[string][]T) map[string][]T {
	out := map[string][]T{}
	for key, value := range in {
		out[key] = append([]T(nil), value...)
	}
	return out
}

func ensureMap[T any](in map[string]T) map[string]T {
	if in == nil {
		return map[string]T{}
	}
	return in
}

func ensureSliceMap[T any](in map[string][]T) map[string][]T {
	if in == nil {
		return map[string][]T{}
	}
	return in
}

func normalizeOwnerProfile(profile app.OwnerProfile) app.OwnerProfile {
	if profile.ID == "" && profile.DisplayName == "" && profile.CreatedAt.IsZero() && profile.UpdatedAt.IsZero() {
		return app.DefaultOwnerProfile()
	}
	now := time.Now().UTC()
	if profile.ID == "" {
		profile.ID = app.DefaultOwnerID
	}
	if strings.TrimSpace(profile.Source) == "" && profile.ID == app.DefaultOwnerID {
		profile.Source = "web"
	}
	if strings.TrimSpace(profile.DisplayName) == "" {
		profile.DisplayName = "Owner"
	}
	if profile.Preferences == nil {
		profile.Preferences = map[string]string{}
	} else {
		profile.Preferences = cloneStringMap(profile.Preferences)
	}
	if profile.CreatedAt.IsZero() {
		profile.CreatedAt = now
	}
	if profile.UpdatedAt.IsZero() {
		profile.UpdatedAt = profile.CreatedAt
	}
	return profile
}

func cloneOwnerProfile(profile app.OwnerProfile) app.OwnerProfile {
	profile.Preferences = cloneStringMap(profile.Preferences)
	return profile
}

func cloneOwnerProfileMap(in map[string]app.OwnerProfile) map[string]app.OwnerProfile {
	if in == nil {
		return map[string]app.OwnerProfile{}
	}
	out := make(map[string]app.OwnerProfile, len(in))
	for id, profile := range in {
		out[id] = cloneOwnerProfile(profile)
	}
	return out
}

func normalizeBrowserAuthRecord(record app.BrowserAuthRecord, current app.BrowserAuthRecord) app.BrowserAuthRecord {
	now := time.Now().UTC()
	record.OwnerID = normalizeBrowserAuthOwnerID(record.OwnerID)
	record.BrowserProfileID = normalizeBrowserProfileID(record.BrowserProfileID)
	record.SiteOrigin = normalizeSiteOrigin(record.SiteOrigin)
	record.SiteRealm = strings.TrimSpace(record.SiteRealm)
	record.AccountHint = strings.ToLower(strings.TrimSpace(record.AccountHint))
	record.AuthStrategy = strings.TrimSpace(record.AuthStrategy)
	if record.AuthStrategy == "" {
		record.AuthStrategy = "session_restore"
	}
	record.Status = strings.TrimSpace(record.Status)
	if record.Status == "" {
		record.Status = app.BrowserAuthStatusActive
	}
	if record.ID == "" {
		record.ID = app.NewID("bauth")
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = current.CreatedAt
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	return record
}

func normalizeBrowserLoginBlock(block app.BrowserLoginBlock, current app.BrowserLoginBlock) app.BrowserLoginBlock {
	now := time.Now().UTC()
	block.SessionID = strings.TrimSpace(block.SessionID)
	block.RunID = strings.TrimSpace(block.RunID)
	block.Status = strings.TrimSpace(block.Status)
	if block.Status == "" {
		block.Status = app.BrowserLoginBlockStatusWaiting
	}
	block.OriginalGoal = strings.TrimSpace(block.OriginalGoal)
	block.ResumeTool = strings.TrimSpace(block.ResumeTool)
	if block.ResumeTool == "" {
		block.ResumeTool = "browser.read"
	}
	if block.ResumeArgs == nil {
		block.ResumeArgs = map[string]any{}
	}
	block.LastToolCallID = strings.TrimSpace(block.LastToolCallID)
	block.LoginHandoffURL = strings.TrimSpace(block.LoginHandoffURL)
	block.OwnerID = normalizeBrowserAuthOwnerID(block.OwnerID)
	block.BrowserProfileID = normalizeBrowserProfileID(block.BrowserProfileID)
	block.SiteOrigin = normalizeSiteOrigin(block.SiteOrigin)
	block.SiteRealm = strings.TrimSpace(block.SiteRealm)
	block.AccountHint = strings.ToLower(strings.TrimSpace(block.AccountHint))
	block.BrowserAuthStatus = strings.TrimSpace(block.BrowserAuthStatus)
	block.LastUserReply = strings.TrimSpace(block.LastUserReply)
	block.LastError = strings.TrimSpace(block.LastError)
	if block.ID == "" {
		block.ID = app.NewID("blogin")
	}
	if block.CreatedAt.IsZero() {
		block.CreatedAt = current.CreatedAt
	}
	if block.CreatedAt.IsZero() {
		block.CreatedAt = now
	}
	if !browserLoginBlockActive(block.Status) && block.ResolvedAt == nil {
		block.ResolvedAt = current.ResolvedAt
	}
	block.UpdatedAt = now
	return block
}

func browserLoginBlockActive(status string) bool {
	switch strings.TrimSpace(status) {
	case app.BrowserLoginBlockStatusWaiting, app.BrowserLoginBlockStatusResuming:
		return true
	default:
		return false
	}
}

func normalizeBrowserAuthLookup(ownerID, browserProfileID, siteOrigin, siteRealm, accountHint string) (string, string, string, string, string) {
	return normalizeBrowserAuthOwnerID(ownerID),
		normalizeBrowserProfileID(browserProfileID),
		normalizeSiteOrigin(siteOrigin),
		strings.TrimSpace(siteRealm),
		strings.ToLower(strings.TrimSpace(accountHint))
}

func normalizeBrowserAuthOwnerID(ownerID string) string {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return app.DefaultOwnerID
	}
	return ownerID
}

func normalizeBrowserProfileID(profileID string) string {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return "default"
	}
	return profileID
}

func normalizeSiteOrigin(origin string) string {
	origin = strings.TrimSpace(origin)
	origin = strings.TrimRight(origin, "/")
	return strings.ToLower(origin)
}

func browserAuthAuditFields(record app.BrowserAuthRecord, extra map[string]any) map[string]any {
	fields := map[string]any{
		"record_id":          record.ID,
		"owner_id":           record.OwnerID,
		"browser_profile_id": record.BrowserProfileID,
		"site_origin":        record.SiteOrigin,
		"site_realm":         record.SiteRealm,
		"account_hint":       record.AccountHint,
		"auth_strategy":      record.AuthStrategy,
		"status":             record.Status,
		"credential_ref_set": strings.TrimSpace(record.CredentialRef) != "",
		"cookie_jar_ref_set": strings.TrimSpace(record.CookieJarRef) != "",
	}
	for key, value := range extra {
		fields[key] = value
	}
	return fields
}

func browserLoginBlockAuditFields(block app.BrowserLoginBlock, extra map[string]any) map[string]any {
	fields := map[string]any{
		"block_id":           block.ID,
		"run_id":             block.RunID,
		"status":             block.Status,
		"resume_tool":        block.ResumeTool,
		"last_tool_call_id":  block.LastToolCallID,
		"owner_id":           block.OwnerID,
		"browser_profile_id": block.BrowserProfileID,
		"site_origin":        block.SiteOrigin,
		"site_realm":         block.SiteRealm,
		"account_hint":       block.AccountHint,
	}
	for key, value := range extra {
		fields[key] = value
	}
	return fields
}

func ensureOwnerProfileMap(in map[string]app.OwnerProfile, fallback app.OwnerProfile) map[string]app.OwnerProfile {
	out := map[string]app.OwnerProfile{}
	for id, profile := range in {
		profile = normalizeOwnerProfile(profile)
		if strings.TrimSpace(id) == "" {
			id = profile.ID
		}
		out[id] = profile
	}
	if _, ok := out[app.DefaultOwnerID]; !ok {
		fallback = normalizeOwnerProfile(fallback)
		out[app.DefaultOwnerID] = fallback
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range in {
		out[key] = value
	}
	return out
}
