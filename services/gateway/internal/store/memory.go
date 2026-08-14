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
	iscpOnboardings      map[string]app.ISCPOnboarding
	mcpAccessTickets     map[string]app.MCPAccessTicket
	mcpBindings          map[string]app.MCPBinding
	mcpOperations        map[string]app.MCPOperation
	messages             map[string][]app.Message
	runFeedback          map[string][]app.RunFeedback
	runs                 map[string]app.AgentRun
	modelCalls           map[string]app.ModelCall
	toolCalls            map[string]app.ToolCall
	documentRecords      map[string]app.DocumentRecord
	approvals            map[string]app.Approval
	reminders            map[string]app.Reminder
	reminderDelivery     map[string]app.ReminderDelivery
	connectorSettings    map[string]app.ConnectorSetting
	notificationBindings map[string]app.NotificationBinding
	passiveNotifications map[string]app.PassiveNotification
	// passiveNotificationIDsByKey indexes passiveNotifications by
	// (endpoint_id, idempotency_key) so ingestion dedup is O(1) instead of a
	// scan. Derived data: never persisted, rebuilt from loadSnapshot.
	passiveNotificationIDsByKey map[string]string
	// passiveNotificationRevs increases per owner on every inbox change so
	// pollers can skip listing when nothing changed. Process-local only.
	passiveNotificationRevs map[string]uint64
	externalChatSessions    map[string]app.ExternalChatSession
	externalChatMessages    map[string]app.ExternalChatMessage
	messageReceives         map[string]app.MessageReceiveRecord
	messageDeliveries       map[string]app.MessageDeliveryRecord
	channelInboxUpdates     map[string]app.ChannelInboxUpdate
	credentialSecrets       map[string]app.CredentialSecret
	browserAuthRecords      map[string]app.BrowserAuthRecord
	browserLoginBlocks      map[string]app.BrowserLoginBlock
	memories                map[string]app.Memory
	memoryCandidates        map[string]app.MemoryCandidate
	auditEvents             []app.AuditEvent
	events                  []app.Event
	evalRuns                map[string]app.EvalRun
	artifactObjects         map[string]app.ArtifactObject
	// artifactObjectIDsByURI indexes artifactObjects by URI so lookups on the
	// observation.read path stay O(1) instead of scanning the full store.
	artifactObjectIDsByURI map[string]map[string]struct{}
	episodeSummaries       map[string]app.EpisodeSummary
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		sessions:                    map[string]app.Session{},
		clients:                     map[string]app.Client{},
		ownerProfile:                app.DefaultOwnerProfile(),
		ownerProfiles:               map[string]app.OwnerProfile{app.DefaultOwnerID: app.DefaultOwnerProfile()},
		pairingCodes:                map[string]app.PairingCode{},
		iscpOnboardings:             map[string]app.ISCPOnboarding{},
		mcpAccessTickets:            map[string]app.MCPAccessTicket{},
		mcpBindings:                 map[string]app.MCPBinding{},
		mcpOperations:               map[string]app.MCPOperation{},
		messages:                    map[string][]app.Message{},
		runFeedback:                 map[string][]app.RunFeedback{},
		runs:                        map[string]app.AgentRun{},
		modelCalls:                  map[string]app.ModelCall{},
		toolCalls:                   map[string]app.ToolCall{},
		documentRecords:             map[string]app.DocumentRecord{},
		approvals:                   map[string]app.Approval{},
		reminders:                   map[string]app.Reminder{},
		reminderDelivery:            map[string]app.ReminderDelivery{},
		connectorSettings:           map[string]app.ConnectorSetting{},
		notificationBindings:        map[string]app.NotificationBinding{},
		passiveNotifications:        map[string]app.PassiveNotification{},
		passiveNotificationIDsByKey: map[string]string{},
		passiveNotificationRevs:     map[string]uint64{},
		externalChatSessions:        map[string]app.ExternalChatSession{},
		externalChatMessages:        map[string]app.ExternalChatMessage{},
		messageReceives:             map[string]app.MessageReceiveRecord{},
		messageDeliveries:           map[string]app.MessageDeliveryRecord{},
		channelInboxUpdates:         map[string]app.ChannelInboxUpdate{},
		credentialSecrets:           map[string]app.CredentialSecret{},
		browserAuthRecords:          map[string]app.BrowserAuthRecord{},
		browserLoginBlocks:          map[string]app.BrowserLoginBlock{},
		memories:                    map[string]app.Memory{},
		memoryCandidates:            map[string]app.MemoryCandidate{},
		auditEvents:                 []app.AuditEvent{},
		events:                      []app.Event{},
		evalRuns:                    map[string]app.EvalRun{},
		artifactObjects:             map[string]app.ArtifactObject{},
		artifactObjectIDsByURI:      map[string]map[string]struct{}{},
		episodeSummaries:            map[string]app.EpisodeSummary{},
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
		ISCPOnboardings:      cloneMap(s.iscpOnboardings),
		MCPAccessTickets:     cloneMCPAccessTicketMap(s.mcpAccessTickets),
		MCPBindings:          cloneMCPBindingMap(s.mcpBindings),
		MCPOperations:        cloneMCPOperationMap(s.mcpOperations),
		Messages:             cloneSliceMap(s.messages),
		RunFeedback:          cloneSliceMap(s.runFeedback),
		Runs:                 cloneMap(s.runs),
		ModelCalls:           cloneMap(s.modelCalls),
		ToolCalls:            cloneMap(s.toolCalls),
		DocumentRecords:      cloneMap(s.documentRecords),
		Approvals:            cloneMap(s.approvals),
		Reminders:            cloneMap(s.reminders),
		ReminderDelivery:     cloneMap(s.reminderDelivery),
		ConnectorSettings:    cloneMap(s.connectorSettings),
		NotificationBindings: cloneMap(s.notificationBindings),
		PassiveNotifications: cloneMap(s.passiveNotifications),
		ExternalChatSessions: cloneMap(s.externalChatSessions),
		ExternalChatMessages: cloneMap(s.externalChatMessages),
		MessageReceives:      cloneMap(s.messageReceives),
		MessageDeliveries:    cloneMap(s.messageDeliveries),
		ChannelInboxUpdates:  cloneMap(s.channelInboxUpdates),
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
	}
}

func (s *MemoryStore) loadSnapshot(snapshot Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = ensureMap(snapshot.Sessions)
	s.clients = ensureMap(snapshot.Clients)
	for id, client := range s.clients {
		if strings.TrimSpace(client.OwnerID) == "" {
			client.OwnerID = app.DefaultOwnerID
		}
		if strings.TrimSpace(client.ActorID) == "" {
			client.ActorID = client.OwnerID
		}
		s.clients[id] = client
	}
	s.ownerProfile = normalizeOwnerProfile(snapshot.OwnerProfile)
	s.ownerProfiles = ensureOwnerProfileMap(snapshot.OwnerProfiles, s.ownerProfile)
	s.pairingCodes = ensureMap(snapshot.PairingCodes)
	s.iscpOnboardings = ensureMap(snapshot.ISCPOnboardings)
	s.mcpAccessTickets = cloneMCPAccessTicketMap(ensureMap(snapshot.MCPAccessTickets))
	s.mcpBindings = cloneMCPBindingMap(ensureMap(snapshot.MCPBindings))
	s.mcpOperations = cloneMCPOperationMap(ensureMap(snapshot.MCPOperations))
	s.messages = ensureSliceMap(snapshot.Messages)
	s.runFeedback = ensureSliceMap(snapshot.RunFeedback)
	s.runs = ensureMap(snapshot.Runs)
	s.modelCalls = ensureMap(snapshot.ModelCalls)
	s.toolCalls = ensureMap(snapshot.ToolCalls)
	s.documentRecords = ensureMap(snapshot.DocumentRecords)
	s.approvals = ensureMap(snapshot.Approvals)
	s.reminders = ensureMap(snapshot.Reminders)
	s.reminderDelivery = ensureMap(snapshot.ReminderDelivery)
	s.connectorSettings = ensureMap(snapshot.ConnectorSettings)
	s.notificationBindings = ensureMap(snapshot.NotificationBindings)
	s.passiveNotifications = ensureMap(snapshot.PassiveNotifications)
	// The idempotency index is derived state: older snapshots never carried it,
	// so it is always rebuilt from the notifications themselves.
	s.passiveNotificationIDsByKey = make(map[string]string, len(s.passiveNotifications))
	for id, notification := range s.passiveNotifications {
		s.passiveNotificationIDsByKey[passiveNotificationKey(notification.EndpointID, notification.IdempotencyKey)] = id
	}
	s.passiveNotificationRevs = map[string]uint64{}
	for id, binding := range s.notificationBindings {
		if strings.TrimSpace(binding.OwnerID) == "" {
			binding.OwnerID = app.DefaultOwnerID
		}
		if strings.TrimSpace(binding.ActorID) == "" {
			binding.ActorID = binding.OwnerID
		}
		s.notificationBindings[id] = binding
	}
	s.externalChatSessions = ensureMap(snapshot.ExternalChatSessions)
	for id, session := range snapshot.WeixinChatSessions {
		if _, exists := s.externalChatSessions[id]; !exists {
			s.externalChatSessions[id] = session
		}
	}
	s.externalChatMessages = ensureMap(snapshot.ExternalChatMessages)
	s.messageReceives = ensureMap(snapshot.MessageReceives)
	s.messageDeliveries = ensureMap(snapshot.MessageDeliveries)
	for id, message := range snapshot.WeixinChatMessages {
		if _, exists := s.externalChatMessages[id]; !exists {
			s.externalChatMessages[id] = message
		}
	}
	for id, session := range s.externalChatSessions {
		if session.Channel == "" {
			session.Channel = "weixin"
		}
		if session.ExternalChatID == "" {
			session.ExternalChatID = session.ExternalUserID
		}
		if strings.TrimSpace(session.AuthorizedOwnerID) == "" {
			if binding, ok := s.notificationBindings[session.BindingID]; ok {
				session.AuthorizedOwnerID = binding.OwnerID
				session.AuthorizedActorID = binding.ActorID
			}
			if session.AuthorizedOwnerID == "" {
				session.AuthorizedOwnerID = session.OwnerID
			}
		}
		if strings.TrimSpace(session.AuthorizedActorID) == "" {
			session.AuthorizedActorID = session.AuthorizedOwnerID
		}
		s.externalChatSessions[id] = session
	}
	for id, message := range s.externalChatMessages {
		if message.Channel == "" {
			if session, ok := s.externalChatSessions[message.ChatSessionID]; ok {
				message.Channel = session.Channel
			}
		}
		s.externalChatMessages[id] = message
	}
	s.channelInboxUpdates = ensureMap(snapshot.ChannelInboxUpdates)
	s.credentialSecrets = ensureMap(snapshot.CredentialSecrets)
	s.browserAuthRecords = ensureMap(snapshot.BrowserAuthRecords)
	s.browserLoginBlocks = ensureMap(snapshot.BrowserLoginBlocks)
	for id, block := range s.browserLoginBlocks {
		s.browserLoginBlocks[id] = migrateLegacyBrowserLoginBlock(block)
	}
	s.memories = ensureMap(snapshot.Memories)
	s.memoryCandidates = ensureMap(snapshot.MemoryCandidates)
	s.auditEvents = append([]app.AuditEvent(nil), snapshot.AuditEvents...)
	s.events = append([]app.Event(nil), snapshot.Events...)
	s.evalRuns = ensureMap(snapshot.EvalRuns)
	s.artifactObjects = ensureMap(snapshot.ArtifactObjects)
	s.artifactObjectIDsByURI = map[string]map[string]struct{}{}
	for _, object := range s.artifactObjects {
		s.indexArtifactObjectLocked(object)
	}
	s.episodeSummaries = ensureMap(snapshot.EpisodeSummaries)
	s.hideLinkedExternalChatSessionsLocked()
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

func (s *MemoryStore) hideLinkedExternalChatSessionsLocked() {
	now := time.Now().UTC()
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

func (s *MemoryStore) SaveClient(client app.Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if client.ID == "" {
		client.ID = app.NewID("client")
	}
	if client.CreatedAt.IsZero() {
		client.CreatedAt = time.Now().UTC()
	}
	if strings.TrimSpace(client.OwnerID) == "" {
		client.OwnerID = app.DefaultOwnerID
	}
	if strings.TrimSpace(client.ActorID) == "" {
		client.ActorID = client.OwnerID
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
	} else {
		for _, existing := range s.messages[message.SessionID] {
			if existing.ID == message.ID {
				return existing
			}
		}
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

func (s *MemoryStore) SaveDocumentRecord(record app.DocumentRecord) app.DocumentRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if record.ID == "" {
		record.ID = app.NewID("doc")
	}
	if record.OwnerID == "" {
		record.OwnerID = app.DefaultOwnerID
	}
	if record.Status == "" {
		record.Status = app.DocumentStatusAvailable
	}
	if record.CreatedAt.IsZero() {
		if existing, ok := s.documentRecords[record.ID]; ok && !existing.CreatedAt.IsZero() {
			record.CreatedAt = existing.CreatedAt
		} else {
			record.CreatedAt = now
		}
	}
	if record.LastActivityAt.IsZero() {
		record.LastActivityAt = now
	}
	if record.LastActivityID == "" {
		record.LastActivityID = record.ID
	}
	record.UpdatedAt = now
	s.documentRecords[record.ID] = record
	s.appendAuditLocked("document.saved", record.SessionID, record.SourceRunID, "document_registry", record.LastActivity, map[string]any{
		"document_id": record.ID,
		"path":        record.GovernedPath,
		"activity_id": record.LastActivityID,
	})
	s.appendEventLocked("document.saved", record.SessionID, record.SourceRunID, record)
	return record
}

func (s *MemoryStore) GetDocumentRecord(id string) (app.DocumentRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.documentRecords[id]
	return record, ok
}

func (s *MemoryStore) ListDocumentRecords(ownerID, sessionID string, limit int) []app.DocumentRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]app.DocumentRecord, 0)
	for _, record := range s.documentRecords {
		if (ownerID == "" || record.OwnerID == ownerID) && (sessionID == "" || record.SessionID == sessionID) {
			out = append(out, record)
		}
	}
	slices.SortFunc(out, func(a, b app.DocumentRecord) int {
		if order := b.LastActivityAt.Compare(a.LastActivityAt); order != 0 {
			return order
		}
		if order := b.UpdatedAt.Compare(a.UpdatedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (s *MemoryStore) SaveApproval(approval app.Approval) {
	s.mu.Lock()
	defer s.mu.Unlock()
	approval = normalizeApproval(approval)
	s.approvals[approval.ID] = approval
	actor := "policy"
	if approval.Source != app.ApprovalSourceTool {
		actor = "integration"
	}
	s.appendAuditLocked("approval."+approval.Status, approval.SessionID, approval.RunID, actor, approval.Summary, map[string]any{
		"tool": approval.Tool,
		"risk": approval.Risk,
	})
	s.appendEventLocked("approval."+approval.Status, approval.SessionID, approval.RunID, approval)
}

func (s *MemoryStore) GetApproval(id string) (app.Approval, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	approval, ok := s.approvals[id]
	return normalizeApproval(approval), ok
}

func (s *MemoryStore) FindApprovalByExternalRef(source app.ApprovalSource, externalID string) (app.Approval, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, approval := range s.approvals {
		approval = normalizeApproval(approval)
		if approval.Source == source && approval.ExternalID == externalID {
			return approval, true
		}
	}
	return app.Approval{}, false
}

func (s *MemoryStore) UpdatePendingApproval(approval app.Approval) (app.Approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.approvals[approval.ID]
	if !ok {
		return app.Approval{}, errors.New("approval not found")
	}
	current = normalizeApproval(current)
	if current.Status != "pending" {
		return app.Approval{}, errors.New("approval already resolved")
	}
	approval = normalizeApproval(approval)
	approval.Status = "pending"
	approval.ResolvedAt = nil
	approval.ResolutionNote = ""
	s.approvals[approval.ID] = approval
	s.appendEventLocked("approval.pending", approval.SessionID, approval.RunID, approval)
	return normalizeApproval(approval), nil
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
	actor := "owner"
	if status == "resolved_elsewhere" {
		actor = "integration"
	}
	s.appendAuditLocked("approval."+status, approval.SessionID, approval.RunID, actor, approval.Summary, map[string]any{"note": note})
	s.appendEventLocked("approval."+status, approval.SessionID, approval.RunID, approval)
	return approval, nil
}

func (s *MemoryStore) ListApprovals(status string) []app.Approval {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.Approval{}
	for _, approval := range s.approvals {
		approval = normalizeApproval(approval)
		if status == "" || approval.Status == status {
			out = append(out, approval)
		}
	}
	slices.SortFunc(out, func(a, b app.Approval) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	return out
}

func normalizeApproval(approval app.Approval) app.Approval {
	if approval.Source == "" {
		approval.Source = app.ApprovalSourceTool
	}
	if approval.Resources == nil {
		approval.Resources = []string{}
	}
	if approval.Arguments == nil {
		approval.Arguments = map[string]any{}
	}
	if approval.ExternalContext != nil {
		contextCopy := *approval.ExternalContext
		approval.ExternalContext = &contextCopy
	}
	return approval
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

func (s *MemoryStore) UpdatePendingReminder(reminder app.Reminder, expectedUpdatedAt time.Time) (app.Reminder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.reminders[reminder.ID]
	if !ok || current.Status != "pending" || !current.UpdatedAt.Equal(expectedUpdatedAt.UTC()) {
		return app.Reminder{}, ErrReminderConflict
	}
	if reminder.CreatedAt.IsZero() {
		reminder.CreatedAt = current.CreatedAt
	}
	if reminder.UpdatedAt.IsZero() {
		reminder.UpdatedAt = time.Now().UTC()
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
	return reminder, nil
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

func (s *MemoryStore) GetConnectorSetting(ownerID, channel string) (app.ConnectorSetting, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	setting, ok := s.connectorSettings[connectorSettingKey(ownerID, channel)]
	return setting, ok
}

func (s *MemoryStore) ListConnectorSettings(ownerID string) []app.ConnectorSetting {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ownerID = normalizeConnectorOwner(ownerID)
	out := []app.ConnectorSetting{}
	for _, setting := range s.connectorSettings {
		if setting.OwnerID == ownerID {
			out = append(out, setting)
		}
	}
	slices.SortFunc(out, func(a, b app.ConnectorSetting) int {
		return strings.Compare(a.Channel, b.Channel)
	})
	return out
}

func (s *MemoryStore) UpdateConnectorSetting(setting app.ConnectorSetting, expectedVersion int64) (app.ConnectorSetting, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	setting.OwnerID = normalizeConnectorOwner(setting.OwnerID)
	setting.Channel = normalizeConnectorChannel(setting.Channel)
	if setting.Channel == "" || expectedVersion < 0 {
		return app.ConnectorSetting{}, ErrConnectorSettingConflict
	}
	key := connectorSettingKey(setting.OwnerID, setting.Channel)
	current, exists := s.connectorSettings[key]
	if (!exists && expectedVersion != 0) || (exists && current.Version != expectedVersion) {
		return app.ConnectorSetting{}, ErrConnectorSettingConflict
	}
	setting.Version = expectedVersion + 1
	setting.UpdatedBy = strings.TrimSpace(setting.UpdatedBy)
	if setting.UpdatedBy == "" {
		setting.UpdatedBy = setting.OwnerID
	}
	setting.UpdatedAt = time.Now().UTC()
	s.connectorSettings[key] = setting
	auditType := connectorSettingAuditType(exists, current.Enabled, current.ISCPEnabled, current.LANAccessEnabled, setting)
	s.appendAuditLocked(auditType, "", "", setting.UpdatedBy, setting.Channel, map[string]any{
		"owner_id":           setting.OwnerID,
		"channel":            setting.Channel,
		"enabled":            setting.Enabled,
		"iscp_enabled":       setting.ISCPEnabled,
		"lan_access_enabled": setting.LANAccessEnabled,
		"version":            setting.Version,
	})
	s.appendEventLocked(auditType, "", "", setting)
	return setting, nil
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
	if binding.ActorID == "" {
		binding.ActorID = binding.OwnerID
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

func normalizeConnectorOwner(ownerID string) string {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return app.DefaultOwnerID
	}
	return ownerID
}

func normalizeConnectorChannel(channel string) string {
	return strings.ToLower(strings.TrimSpace(channel))
}

func connectorSettingKey(ownerID, channel string) string {
	return normalizeConnectorOwner(ownerID) + "\x1f" + normalizeConnectorChannel(channel)
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

func (s *MemoryStore) CreatePassiveNotification(notification app.PassiveNotification) (app.PassiveNotification, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	notification.OwnerID = strings.TrimSpace(notification.OwnerID)
	notification.EndpointID = strings.TrimSpace(notification.EndpointID)
	notification.IdempotencyKey = strings.TrimSpace(notification.IdempotencyKey)
	if notification.OwnerID == "" || notification.EndpointID == "" || notification.IdempotencyKey == "" || strings.TrimSpace(notification.Fingerprint) == "" {
		return app.PassiveNotification{}, false, errors.New("notification owner, endpoint, idempotency key, and fingerprint are required")
	}
	if existingID, ok := s.passiveNotificationIDsByKey[passiveNotificationKey(notification.EndpointID, notification.IdempotencyKey)]; ok {
		existing := s.passiveNotifications[existingID]
		if existing.OwnerID != notification.OwnerID || existing.Fingerprint != notification.Fingerprint {
			return app.PassiveNotification{}, false, ErrPassiveNotificationConflict
		}
		return existing, false, nil
	}
	now := time.Now().UTC()
	if notification.ID == "" {
		notification.ID = app.NewID("notification")
	}
	if notification.CreatedAt.IsZero() {
		notification.CreatedAt = now
	}
	notification.UpdatedAt = now
	s.passiveNotifications[notification.ID] = notification
	s.passiveNotificationIDsByKey[passiveNotificationKey(notification.EndpointID, notification.IdempotencyKey)] = notification.ID
	s.passiveNotificationRevs[notification.OwnerID]++
	s.appendAuditLocked("notification.received", "", "", notification.OwnerID, notification.Source, map[string]any{
		"notification_id": notification.ID,
		"endpoint_id":     notification.EndpointID,
		"kind":            notification.Kind,
	})
	return notification, true, nil
}

func (s *MemoryStore) GetPassiveNotification(ownerID, id string) (app.PassiveNotification, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	notification, ok := s.passiveNotifications[id]
	return notification, ok && notification.OwnerID == ownerID
}

func (s *MemoryStore) ListPassiveNotifications(ownerID, after string, limit int) []app.PassiveNotification {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var cursor app.PassiveNotification
	if after != "" {
		var ok bool
		cursor, ok = s.passiveNotifications[after]
		if !ok || cursor.OwnerID != ownerID {
			return []app.PassiveNotification{}
		}
	}
	out := make([]app.PassiveNotification, 0)
	for _, notification := range s.passiveNotifications {
		if notification.OwnerID != ownerID {
			continue
		}
		if after != "" && (notification.CreatedAt.Before(cursor.CreatedAt) || (notification.CreatedAt.Equal(cursor.CreatedAt) && notification.ID <= cursor.ID)) {
			continue
		}
		out = append(out, notification)
	}
	slices.SortFunc(out, func(a, b app.PassiveNotification) int {
		order := a.CreatedAt.Compare(b.CreatedAt)
		if order == 0 {
			order = strings.Compare(a.ID, b.ID)
		}
		if after == "" {
			return -order
		}
		return order
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (s *MemoryStore) CountUnreadPassiveNotifications(ownerID string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for _, notification := range s.passiveNotifications {
		if notification.OwnerID == ownerID && notification.ReadAt == nil {
			count++
		}
	}
	return count
}

func (s *MemoryStore) MarkPassiveNotificationRead(ownerID, id string, readAt time.Time) (app.PassiveNotification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	notification, ok := s.passiveNotifications[id]
	if !ok || notification.OwnerID != ownerID {
		return app.PassiveNotification{}, ErrPassiveNotificationNotFound
	}
	if notification.ReadAt != nil {
		return notification, nil
	}
	if readAt.IsZero() {
		readAt = time.Now().UTC()
	} else {
		readAt = readAt.UTC()
	}
	notification.ReadAt = &readAt
	notification.UpdatedAt = readAt
	s.passiveNotifications[id] = notification
	s.passiveNotificationRevs[notification.OwnerID]++
	return notification, nil
}

func (s *MemoryStore) MarkAllPassiveNotificationsRead(ownerID string, readAt time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if readAt.IsZero() {
		readAt = time.Now().UTC()
	} else {
		readAt = readAt.UTC()
	}
	count := 0
	for id, notification := range s.passiveNotifications {
		if notification.OwnerID != ownerID || notification.ReadAt != nil {
			continue
		}
		notification.ReadAt = &readAt
		notification.UpdatedAt = readAt
		s.passiveNotifications[id] = notification
		count++
	}
	if count > 0 {
		s.passiveNotificationRevs[ownerID]++
	}
	return count, nil
}

func (s *MemoryStore) PrunePassiveNotifications(cutoff time.Time, maxPerOwner int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	removedByOwner := map[string]int{}
	if !cutoff.IsZero() {
		for id, notification := range s.passiveNotifications {
			if notification.CreatedAt.Before(cutoff) {
				s.removePassiveNotificationLocked(id, notification)
				removedByOwner[notification.OwnerID]++
			}
		}
	}
	if maxPerOwner > 0 {
		byOwner := map[string][]app.PassiveNotification{}
		for _, notification := range s.passiveNotifications {
			byOwner[notification.OwnerID] = append(byOwner[notification.OwnerID], notification)
		}
		for ownerID, notifications := range byOwner {
			excess := len(notifications) - maxPerOwner
			if excess <= 0 {
				continue
			}
			slices.SortFunc(notifications, passiveNotificationEvictionOrder)
			for _, notification := range notifications[:excess] {
				s.removePassiveNotificationLocked(notification.ID, notification)
				removedByOwner[ownerID]++
			}
		}
	}
	removed := 0
	for ownerID, count := range removedByOwner {
		removed += count
		s.appendAuditLocked("notification.pruned", "", "", "notification-retention", ownerID, map[string]any{
			"removed":       count,
			"max_per_owner": maxPerOwner,
			"cutoff":        cutoff.UTC().Format(time.RFC3339),
		})
	}
	return removed
}

func (s *MemoryStore) removePassiveNotificationLocked(id string, notification app.PassiveNotification) {
	delete(s.passiveNotifications, id)
	delete(s.passiveNotificationIDsByKey, passiveNotificationKey(notification.EndpointID, notification.IdempotencyKey))
	s.passiveNotificationRevs[notification.OwnerID]++
}

func (s *MemoryStore) PassiveNotificationRevision(ownerID string) uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.passiveNotificationRevs[ownerID]
}

// passiveNotificationEvictionOrder ranks cap evictions: read notifications go
// first (oldest first), then unread oldest-first, so an over-cap inbox keeps
// the newest unread records.
func passiveNotificationEvictionOrder(a, b app.PassiveNotification) int {
	aRead, bRead := a.ReadAt != nil, b.ReadAt != nil
	if aRead != bRead {
		if aRead {
			return -1
		}
		return 1
	}
	if order := a.CreatedAt.Compare(b.CreatedAt); order != 0 {
		return order
	}
	return strings.Compare(a.ID, b.ID)
}

func passiveNotificationKey(endpointID, idempotencyKey string) string {
	return endpointID + "\x00" + idempotencyKey
}

func (s *MemoryStore) SaveExternalChatSession(session app.ExternalChatSession) app.ExternalChatSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if session.ID == "" {
		session.ID = app.NewID("extchat")
	}
	if session.Channel == "" {
		session.Channel = "weixin"
	}
	if session.ExternalChatID == "" {
		session.ExternalChatID = session.ExternalUserID
	}
	if strings.TrimSpace(session.AuthorizedOwnerID) == "" {
		if binding, ok := s.notificationBindings[session.BindingID]; ok {
			session.AuthorizedOwnerID = binding.OwnerID
			session.AuthorizedActorID = binding.ActorID
		}
		if session.AuthorizedOwnerID == "" {
			session.AuthorizedOwnerID = session.OwnerID
		}
	}
	if strings.TrimSpace(session.AuthorizedActorID) == "" {
		session.AuthorizedActorID = session.AuthorizedOwnerID
	}
	if session.Status == "" {
		session.Status = "active"
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	session.UpdatedAt = now
	if linked, ok := s.sessions[session.LinkedSessionID]; ok {
		linked.Source = session.Channel
		linked.Hidden = true
		if strings.TrimSpace(session.OwnerID) != "" {
			linked.OwnerID = session.OwnerID
		}
		if strings.TrimSpace(session.WorkspaceRoot) != "" {
			linked.WorkspaceRoot = session.WorkspaceRoot
		}
		linked.UpdatedAt = now
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
	return session
}

func (s *MemoryStore) GetExternalChatSession(id string) (app.ExternalChatSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.externalChatSessions[id]
	return session, ok
}

func (s *MemoryStore) ListExternalChatSessions(channel, status string) []app.ExternalChatSession {
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
		out = append(out, session)
	}
	slices.SortFunc(out, func(a, b app.ExternalChatSession) int {
		return b.UpdatedAt.Compare(a.UpdatedAt)
	})
	return out
}

func (s *MemoryStore) FindExternalChatSession(bindingID, externalChatID, externalThreadID string) (app.ExternalChatSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, session := range s.externalChatSessions {
		chatID := session.ExternalChatID
		if chatID == "" {
			chatID = session.ExternalUserID
		}
		if session.BindingID == bindingID && chatID == externalChatID && session.ExternalThreadID == externalThreadID {
			return session, true
		}
	}
	return app.ExternalChatSession{}, false
}

func (s *MemoryStore) FindExternalChatSessionByLinkedSessionID(sessionID string) (app.ExternalChatSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, session := range s.externalChatSessions {
		if session.LinkedSessionID == sessionID {
			return session, true
		}
	}
	return app.ExternalChatSession{}, false
}

func (s *MemoryStore) SaveExternalChatMessage(message app.ExternalChatMessage) app.ExternalChatMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if message.ID == "" {
		message.ID = app.NewID("extmsg")
	}
	if message.Channel == "" {
		if session, ok := s.externalChatSessions[message.ChatSessionID]; ok {
			message.Channel = session.Channel
		}
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = now
	}
	message.UpdatedAt = now
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
	return message
}

func (s *MemoryStore) GetExternalChatMessage(id string) (app.ExternalChatMessage, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	message, ok := s.externalChatMessages[id]
	return message, ok
}

func (s *MemoryStore) FindExternalChatMessageByExternalID(chatSessionID, externalMessageID string) (app.ExternalChatMessage, bool) {
	if strings.TrimSpace(externalMessageID) == "" {
		return app.ExternalChatMessage{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, message := range s.externalChatMessages {
		if message.ChatSessionID == chatSessionID && message.ExternalMessageID == externalMessageID {
			return message, true
		}
	}
	return app.ExternalChatMessage{}, false
}

func (s *MemoryStore) ListExternalChatMessages(chatSessionID string, limit int) []app.ExternalChatMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.ExternalChatMessage{}
	for _, message := range s.externalChatMessages {
		if chatSessionID == "" || message.ChatSessionID == chatSessionID {
			out = append(out, message)
		}
	}
	slices.SortFunc(out, func(a, b app.ExternalChatMessage) int {
		return a.CreatedAt.Compare(b.CreatedAt)
	})
	if limit > 0 && len(out) > limit {
		return out[len(out)-limit:]
	}
	return out
}

func (s *MemoryStore) SaveMessageReceive(record app.MessageReceiveRecord) app.MessageReceiveRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	for _, existing := range s.messageReceives {
		if record.SourceEndpointID == "" || strings.TrimSpace(record.NativeMessageID) == "" {
			break
		}
		if existing.SourceEndpointID == record.SourceEndpointID && existing.NativeMessageID == record.NativeMessageID && existing.ID != record.ID {
			record.ID = existing.ID
			record.CreatedAt = existing.CreatedAt
			record.Transitions = append([]app.MessageLifecycleTransition(nil), existing.Transitions...)
			break
		}
	}
	if record.ID == "" {
		record.ID = app.NewID("recv")
	}
	if record.Direction == "" {
		record.Direction = app.MessageDirectionReceive
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	if len(record.Transitions) == 0 || record.Transitions[len(record.Transitions)-1].Status != record.Status {
		record.Transitions = append(record.Transitions, app.MessageLifecycleTransition{Status: record.Status, At: now})
	}
	s.messageReceives[record.ID] = record
	s.appendAuditLocked("message.receive."+record.Status, "", record.LinkedRunID, "gateway", record.ProviderKey, map[string]any{
		"receive_id": record.ID, "endpoint_id": record.SourceEndpointID,
	})
	return record
}

func (s *MemoryStore) GetMessageReceive(id string) (app.MessageReceiveRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.messageReceives[id]
	return record, ok
}

func (s *MemoryStore) FindMessageReceive(sourceEndpointID app.EndpointID, nativeMessageID string) (app.MessageReceiveRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, record := range s.messageReceives {
		if record.SourceEndpointID == sourceEndpointID && record.NativeMessageID == nativeMessageID {
			return record, true
		}
	}
	return app.MessageReceiveRecord{}, false
}

func (s *MemoryStore) ListMessageReceives(ownerID, actorID string, limit int) []app.MessageReceiveRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.MessageReceiveRecord{}
	for _, record := range s.messageReceives {
		if ownerID != "" && record.OwnerID != ownerID {
			continue
		}
		if actorID != "" && record.ActorID != actorID {
			continue
		}
		out = append(out, record)
	}
	slices.SortFunc(out, func(a, b app.MessageReceiveRecord) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (s *MemoryStore) SaveMessageDelivery(record app.MessageDeliveryRecord) app.MessageDeliveryRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if record.ID == "" {
		record.ID = app.DeliveryID(app.NewID("del"))
	}
	if record.Direction == "" {
		record.Direction = app.MessageDirectionSend
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	s.messageDeliveries[string(record.ID)] = record
	s.appendAuditLocked("message.send."+string(record.Status), "", record.Request.RunID, record.ActorID, record.SoftwareDisplayName, map[string]any{
		"delivery_id": record.ID, "endpoint_id": record.Request.Target, "origin": record.Origin,
	})
	return record
}

func (s *MemoryStore) GetMessageDelivery(id app.DeliveryID) (app.MessageDeliveryRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.messageDeliveries[string(id)]
	return record, ok
}

func (s *MemoryStore) FindMessageDeliveryByIdempotency(ownerID, actorID, idempotencyKey string) (app.MessageDeliveryRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, record := range s.messageDeliveries {
		if record.OwnerID == ownerID && record.ActorID == actorID && record.Request.IdempotencyKey == idempotencyKey {
			return record, true
		}
	}
	return app.MessageDeliveryRecord{}, false
}

func (s *MemoryStore) ListMessageDeliveries(ownerID, actorID string, limit int) []app.MessageDeliveryRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.MessageDeliveryRecord{}
	for _, record := range s.messageDeliveries {
		if ownerID != "" && record.OwnerID != ownerID {
			continue
		}
		if actorID != "" && record.ActorID != actorID {
			continue
		}
		out = append(out, record)
	}
	slices.SortFunc(out, func(a, b app.MessageDeliveryRecord) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (s *MemoryStore) SaveChannelInboxUpdate(update app.ChannelInboxUpdate) app.ChannelInboxUpdate {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	for _, existing := range s.channelInboxUpdates {
		if existing.BindingID == update.BindingID && existing.ExternalID == update.ExternalID && existing.ID != update.ID {
			existing.Payload = append([]byte(nil), existing.Payload...)
			return existing
		}
	}
	if update.ID == "" {
		update.ID = app.NewID("inbox")
	}
	if update.Status == "" {
		update.Status = "pending"
	}
	if update.AvailableAt.IsZero() {
		update.AvailableAt = now
	}
	if update.CreatedAt.IsZero() {
		update.CreatedAt = now
	}
	update.UpdatedAt = now
	update.Payload = append([]byte(nil), update.Payload...)
	s.channelInboxUpdates[update.ID] = update
	return update
}

func (s *MemoryStore) GetChannelInboxUpdate(id string) (app.ChannelInboxUpdate, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	update, ok := s.channelInboxUpdates[id]
	update.Payload = append([]byte(nil), update.Payload...)
	return update, ok
}

func (s *MemoryStore) FindChannelInboxUpdate(bindingID, externalID string) (app.ChannelInboxUpdate, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, update := range s.channelInboxUpdates {
		if update.BindingID == bindingID && update.ExternalID == externalID {
			update.Payload = append([]byte(nil), update.Payload...)
			return update, true
		}
	}
	return app.ChannelInboxUpdate{}, false
}

func (s *MemoryStore) ListChannelInboxUpdates(channel, status string, readyBefore time.Time, limit int) []app.ChannelInboxUpdate {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.ChannelInboxUpdate{}
	for _, update := range s.channelInboxUpdates {
		if channel != "" && update.Channel != channel {
			continue
		}
		if status != "" && update.Status != status {
			continue
		}
		if !readyBefore.IsZero() && update.AvailableAt.After(readyBefore) {
			continue
		}
		update.Payload = append([]byte(nil), update.Payload...)
		out = append(out, update)
	}
	slices.SortFunc(out, func(a, b app.ChannelInboxUpdate) int {
		return a.CreatedAt.Compare(b.CreatedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func externalChatSessionTitle(channel string) string {
	if strings.EqualFold(strings.TrimSpace(channel), "telegram") {
		return "Telegram 会话"
	}
	return "微信会话"
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
	block.ID = strings.TrimSpace(block.ID)
	block = normalizeBrowserLoginBlock(block, s.browserLoginBlocks[block.ID])
	s.browserLoginBlocks[block.ID] = block
	s.appendAuditLocked("browser_login_block."+block.Status, block.SessionID, block.RunID, "runtime", block.SiteOrigin, browserLoginBlockAuditFields(block, nil))
	s.appendEventLocked("browser_login_block."+block.Status, block.SessionID, block.RunID, block)
	return block
}

func (s *MemoryStore) UpdateBrowserLoginBlock(block app.BrowserLoginBlock, expectedVersion int64) (app.BrowserLoginBlock, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.browserLoginBlocks[strings.TrimSpace(block.ID)]
	if !ok || current.Version != expectedVersion {
		return app.BrowserLoginBlock{}, ErrBrowserHandoffConflict
	}
	block.Version = expectedVersion + 1
	block = normalizeBrowserLoginBlock(block, current)
	s.browserLoginBlocks[block.ID] = block
	s.appendAuditLocked("browser_login_block."+block.Status, block.SessionID, block.RunID, "runtime", block.SiteOrigin, browserLoginBlockAuditFields(block, nil))
	s.appendEventLocked("browser_login_block."+block.Status, block.SessionID, block.RunID, block)
	return block, nil
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
		if app.BrowserHandoffStatusActive(block.Status) {
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
	// Read path: return stored values verbatim. Normalization that stamps
	// SchemaVersion/Version/UpdatedAt happens only on write (and once at
	// snapshot load), otherwise reads would destroy migration evidence,
	// degrade UpdatedAt ordering, and break CAS against the stored Version.
	for _, block := range s.browserLoginBlocks {
		if sessionID != "" && block.SessionID != sessionID {
			continue
		}
		if status != "" && block.Status != status {
			continue
		}
		out = append(out, block)
	}
	slices.SortFunc(out, func(a, b app.BrowserLoginBlock) int {
		if c := b.UpdatedAt.Compare(a.UpdatedAt); c != 0 {
			return c
		}
		return strings.Compare(b.ID, a.ID)
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

func (s *MemoryStore) MessageEventHead(sessionID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for index := len(s.events) - 1; index >= 0; index-- {
		event := s.events[index]
		if event.SessionID == sessionID && event.Type == "message.created" {
			return event.ID, nil
		}
	}
	return "", nil
}

func (s *MemoryStore) MessageEventsAfter(sessionID, after string, limit int) (MessageEventPage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
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
				return MessageEventPage{}, ErrMessageEventCursorInvalid
			}
			start = index + 1
			break
		}
		if start < 0 {
			return MessageEventPage{}, ErrMessageEventCursorInvalid
		}
	}

	matching := make([]app.Event, 0, limit+1)
	for _, event := range s.events[start:] {
		if event.SessionID == sessionID && event.Type == "message.created" {
			matching = append(matching, event)
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
	if existing, ok := s.artifactObjects[object.ID]; ok && existing.URI != object.URI {
		s.unindexArtifactObjectLocked(existing)
	}
	s.artifactObjects[object.ID] = object
	s.indexArtifactObjectLocked(object)
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

func (s *MemoryStore) FindArtifactObjectByURI(uri, sessionID, runID string) (app.ArtifactObject, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var newest app.ArtifactObject
	found := false
	for id := range s.artifactObjectIDsByURI[uri] {
		object, ok := s.artifactObjects[id]
		if !ok || (sessionID != "" && object.SessionID != sessionID) || (runID != "" && object.RunID != runID) {
			continue
		}
		if !found || object.CreatedAt.After(newest.CreatedAt) {
			newest = object
			found = true
		}
	}
	return newest, found
}

func (s *MemoryStore) indexArtifactObjectLocked(object app.ArtifactObject) {
	ids := s.artifactObjectIDsByURI[object.URI]
	if ids == nil {
		ids = map[string]struct{}{}
		s.artifactObjectIDsByURI[object.URI] = ids
	}
	ids[object.ID] = struct{}{}
}

func (s *MemoryStore) unindexArtifactObjectLocked(object app.ArtifactObject) {
	ids := s.artifactObjectIDsByURI[object.URI]
	delete(ids, object.ID)
	if len(ids) == 0 {
		delete(s.artifactObjectIDsByURI, object.URI)
	}
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

// Legacy schema-v1 browser login block status strings, kept only so
// previously persisted snapshots can be migrated at load time.
const (
	legacyBrowserHandoffStatusWaiting  = "waiting"
	legacyBrowserHandoffStatusResuming = "resuming"
)

// migrateLegacyBrowserLoginBlock upgrades a schema-v1 block persisted by an
// older build to the v2 shape. It runs once at snapshot load — never on read
// paths — and deliberately leaves CreatedAt/UpdatedAt untouched so stored
// ordering and migration evidence survive. The postgres schema performs the
// same status mapping in SQL; keep the two in sync.
func migrateLegacyBrowserLoginBlock(block app.BrowserLoginBlock) app.BrowserLoginBlock {
	switch strings.TrimSpace(block.Status) {
	case legacyBrowserHandoffStatusWaiting:
		block.Status = app.BrowserHandoffStatusWaitingOwner
	case legacyBrowserHandoffStatusResuming:
		block.Status = app.BrowserHandoffStatusValidatingVisible
	}
	if block.SchemaVersion <= 0 {
		block.SchemaVersion = app.BrowserHandoffSchemaVersion
	}
	if block.Version <= 0 {
		block.Version = 1
	}
	// Read paths no longer normalize, so the resume defaults formerly
	// injected on read must be materialized here for legacy rows.
	if strings.TrimSpace(block.ResumeTool) == "" {
		block.ResumeTool = "browser.read"
	}
	if block.ResumeArgs == nil {
		block.ResumeArgs = map[string]any{}
	}
	return block
}

// normalizeBrowserLoginBlock is a WRITE-path helper (Save/Update in every
// backend): it stamps SchemaVersion, bumps Version past current, and sets
// UpdatedAt to now. It must never run on read paths — see
// migrateLegacyBrowserLoginBlock for the one-time snapshot-load fix-up.
func normalizeBrowserLoginBlock(block app.BrowserLoginBlock, current app.BrowserLoginBlock) app.BrowserLoginBlock {
	now := time.Now().UTC()
	if block.SchemaVersion <= 0 {
		block.SchemaVersion = app.BrowserHandoffSchemaVersion
	}
	if block.Version <= current.Version {
		block.Version = current.Version + 1
	}
	if block.Version <= 0 {
		block.Version = 1
	}
	block.SessionID = strings.TrimSpace(block.SessionID)
	block.RunID = strings.TrimSpace(block.RunID)
	block.TransitionOwnerID = strings.TrimSpace(block.TransitionOwnerID)
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
	block.LoginHandoffPageID = strings.TrimSpace(block.LoginHandoffPageID)
	block.LastVisiblePageID = strings.TrimSpace(block.LastVisiblePageID)
	block.OwnerID = normalizeBrowserAuthOwnerID(block.OwnerID)
	block.BrowserProfileID = normalizeBrowserProfileID(block.BrowserProfileID)
	block.SiteOrigin = normalizeSiteOrigin(block.SiteOrigin)
	block.SiteRealm = strings.TrimSpace(block.SiteRealm)
	block.AccountHint = strings.ToLower(strings.TrimSpace(block.AccountHint))
	block.BrowserAuthStatus = strings.TrimSpace(block.BrowserAuthStatus)
	block.LastUserReply = strings.TrimSpace(block.LastUserReply)
	block.LastError = strings.TrimSpace(block.LastError)
	if block.Status == app.BrowserHandoffStatusWaitingOwner || !app.BrowserHandoffStatusActive(block.Status) {
		block.TransitionOwnerID = ""
		block.TransitionLeaseUntil = nil
	}
	block.ID = strings.TrimSpace(block.ID)
	if block.ID == "" {
		block.ID = app.NewID("blogin")
	}
	if block.CreatedAt.IsZero() {
		block.CreatedAt = current.CreatedAt
	}
	if block.CreatedAt.IsZero() {
		block.CreatedAt = now
	}
	if !app.BrowserHandoffStatusActive(block.Status) && block.ResolvedAt == nil {
		block.ResolvedAt = current.ResolvedAt
	}
	block.UpdatedAt = now
	return block
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
		"block_id":              block.ID,
		"run_id":                block.RunID,
		"status":                block.Status,
		"resume_tool":           block.ResumeTool,
		"last_tool_call_id":     block.LastToolCallID,
		"login_handoff_page_id": block.LoginHandoffPageID,
		"last_visible_page_id":  block.LastVisiblePageID,
		"owner_id":              block.OwnerID,
		"browser_profile_id":    block.BrowserProfileID,
		"site_origin":           block.SiteOrigin,
		"site_realm":            block.SiteRealm,
		"account_hint":          block.AccountHint,
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
