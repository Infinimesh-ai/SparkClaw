package store

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type MemoryStore struct {
	mu                                sync.RWMutex
	operationTimeouts                 OperationTimeouts
	sessions                          map[string]app.Session
	sessionWriteHighWater             map[string]time.Time
	sessionNow                        func() time.Time
	clients                           map[string]app.Client
	clientWriteHighWater              map[string]time.Time
	pairingWriteHighWater             map[string]time.Time
	clientNow                         func() time.Time
	ownerProfile                      app.OwnerProfile
	ownerProfiles                     map[string]app.OwnerProfile
	ownerWriteHighWater               map[string]time.Time
	ownerNow                          func() time.Time
	pairingCodes                      map[string]app.PairingCode
	iscpOnboardings                   map[string]app.ISCPOnboarding
	mcpAccessTickets                  map[string]app.MCPAccessTicket
	mcpBindings                       map[string]app.MCPBinding
	mcpOperations                     map[string]app.MCPOperation
	messages                          map[string][]app.Message
	runFeedback                       map[string][]app.RunFeedback
	runs                              map[string]app.AgentRun
	modelCalls                        map[string]app.ModelCall
	toolCalls                         map[string]app.ToolCall
	documentRecords                   map[string]app.DocumentRecord
	approvals                         map[string]app.Approval
	reminders                         map[string]app.Reminder
	reminderDelivery                  map[string]app.ReminderDelivery
	connectorSettings                 map[string]app.ConnectorSetting
	notificationBindings              map[string]app.NotificationBinding
	connectorSettingWriteHighWater    map[string]time.Time
	notificationBindingWriteHighWater map[string]time.Time
	connectorNow                      func() time.Time
	passiveNotifications              map[string]app.PassiveNotification
	// passiveNotificationIDsByKey indexes passiveNotifications by
	// (endpoint_id, idempotency_key) so ingestion dedup is O(1) instead of a
	// scan. Derived data: never persisted, rebuilt from loadSnapshot.
	passiveNotificationIDsByKey map[string]string
	// passiveNotificationRevs increases per owner on every inbox change so
	// pollers can skip listing when nothing changed. Process-local only.
	passiveNotificationRevs  map[string]uint64
	externalChatSessions     map[string]app.ExternalChatSession
	externalChatMessages     map[string]app.ExternalChatMessage
	messageReceives          map[string]app.MessageReceiveRecord
	messageDeliveries        map[string]app.MessageDeliveryRecord
	channelInboxUpdates      map[string]app.ChannelInboxUpdate
	credentialSecrets        map[string]app.CredentialSecret
	credentialWriteHighWater map[string]time.Time
	credentialNow            func() time.Time
	browserAuthRecords       map[string]app.BrowserAuthRecord
	browserLoginBlocks       map[string]app.BrowserLoginBlock
	memories                 map[string]app.Memory
	memoryCandidates         map[string]app.MemoryCandidate
	auditEvents              []app.AuditEvent
	events                   []app.Event
	evalRuns                 map[string]app.EvalRun
	artifactObjects          map[string]app.ArtifactObject
	// artifactObjectIDsByURI indexes artifactObjects by URI so lookups on the
	// observation.read path stay O(1) instead of scanning the full store.
	artifactObjectIDsByURI map[string]map[string]struct{}
	episodeSummaries       map[string]app.EpisodeSummary
}

func NewMemoryStore() *MemoryStore {
	return NewMemoryStoreWithOptions(defaultOperationTimeouts)
}

func NewMemoryStoreWithOptions(timeouts OperationTimeouts) *MemoryStore {
	defaultOwner := app.DefaultOwnerProfile()
	return &MemoryStore{
		operationTimeouts:                 normalizeOperationTimeouts(timeouts),
		sessions:                          map[string]app.Session{},
		sessionWriteHighWater:             map[string]time.Time{},
		sessionNow:                        time.Now,
		clients:                           map[string]app.Client{},
		clientWriteHighWater:              map[string]time.Time{},
		pairingWriteHighWater:             map[string]time.Time{},
		clientNow:                         time.Now,
		ownerProfile:                      cloneOwnerProfile(defaultOwner),
		ownerProfiles:                     map[string]app.OwnerProfile{app.DefaultOwnerID: cloneOwnerProfile(defaultOwner)},
		ownerWriteHighWater:               map[string]time.Time{app.DefaultOwnerID: defaultOwner.UpdatedAt},
		ownerNow:                          time.Now,
		pairingCodes:                      map[string]app.PairingCode{},
		iscpOnboardings:                   map[string]app.ISCPOnboarding{},
		mcpAccessTickets:                  map[string]app.MCPAccessTicket{},
		mcpBindings:                       map[string]app.MCPBinding{},
		mcpOperations:                     map[string]app.MCPOperation{},
		messages:                          map[string][]app.Message{},
		runFeedback:                       map[string][]app.RunFeedback{},
		runs:                              map[string]app.AgentRun{},
		modelCalls:                        map[string]app.ModelCall{},
		toolCalls:                         map[string]app.ToolCall{},
		documentRecords:                   map[string]app.DocumentRecord{},
		approvals:                         map[string]app.Approval{},
		reminders:                         map[string]app.Reminder{},
		reminderDelivery:                  map[string]app.ReminderDelivery{},
		connectorSettings:                 map[string]app.ConnectorSetting{},
		notificationBindings:              map[string]app.NotificationBinding{},
		connectorSettingWriteHighWater:    map[string]time.Time{},
		notificationBindingWriteHighWater: map[string]time.Time{},
		connectorNow:                      time.Now,
		passiveNotifications:              map[string]app.PassiveNotification{},
		passiveNotificationIDsByKey:       map[string]string{},
		passiveNotificationRevs:           map[string]uint64{},
		externalChatSessions:              map[string]app.ExternalChatSession{},
		externalChatMessages:              map[string]app.ExternalChatMessage{},
		messageReceives:                   map[string]app.MessageReceiveRecord{},
		messageDeliveries:                 map[string]app.MessageDeliveryRecord{},
		channelInboxUpdates:               map[string]app.ChannelInboxUpdate{},
		credentialSecrets:                 map[string]app.CredentialSecret{},
		credentialWriteHighWater:          map[string]time.Time{},
		credentialNow:                     time.Now,
		browserAuthRecords:                map[string]app.BrowserAuthRecord{},
		browserLoginBlocks:                map[string]app.BrowserLoginBlock{},
		memories:                          map[string]app.Memory{},
		memoryCandidates:                  map[string]app.MemoryCandidate{},
		auditEvents:                       []app.AuditEvent{},
		events:                            []app.Event{},
		evalRuns:                          map[string]app.EvalRun{},
		artifactObjects:                   map[string]app.ArtifactObject{},
		artifactObjectIDsByURI:            map[string]map[string]struct{}{},
		episodeSummaries:                  map[string]app.EpisodeSummary{},
	}
}

func (s *MemoryStore) snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Snapshot{
		Sessions:             cloneMap(s.sessions),
		Clients:              cloneClientMap(s.clients),
		OwnerProfile:         cloneOwnerProfile(s.ownerProfile),
		OwnerProfiles:        cloneOwnerProfileMap(s.ownerProfiles),
		PairingCodes:         clonePairingCodeMap(s.pairingCodes),
		ISCPOnboardings:      cloneMap(s.iscpOnboardings),
		MCPAccessTickets:     cloneMCPAccessTicketMap(s.mcpAccessTickets),
		MCPBindings:          cloneMCPBindingMap(s.mcpBindings),
		MCPOperations:        cloneMCPOperationMap(s.mcpOperations),
		Messages:             cloneMessageMap(s.messages),
		RunFeedback:          cloneSliceMap(s.runFeedback),
		Runs:                 cloneMap(s.runs),
		ModelCalls:           cloneMap(s.modelCalls),
		ToolCalls:            cloneMap(s.toolCalls),
		DocumentRecords:      cloneMap(s.documentRecords),
		Approvals:            cloneMap(s.approvals),
		Reminders:            cloneReminderMap(s.reminders),
		ReminderDelivery:     cloneMap(s.reminderDelivery),
		ConnectorSettings:    cloneMap(s.connectorSettings),
		NotificationBindings: cloneNotificationBindingMap(s.notificationBindings),
		PassiveNotifications: clonePassiveNotificationMap(s.passiveNotifications),
		ExternalChatSessions: cloneMap(s.externalChatSessions),
		ExternalChatMessages: cloneMap(s.externalChatMessages),
		MessageReceives:      cloneMessageReceiveMap(s.messageReceives),
		MessageDeliveries:    cloneMessageDeliveryMap(s.messageDeliveries),
		ChannelInboxUpdates:  cloneChannelInboxUpdateMap(s.channelInboxUpdates),
		CredentialSecrets:    cloneMap(s.credentialSecrets),
		BrowserAuthRecords:   cloneBrowserAuthRecordMap(s.browserAuthRecords),
		BrowserLoginBlocks:   cloneBrowserLoginBlockMap(s.browserLoginBlocks),
		Memories:             cloneMap(s.memories),
		MemoryCandidates:     cloneMemoryCandidateMap(s.memoryCandidates),
		AuditEvents:          cloneAuditEventsBestEffort(s.auditEvents),
		Events:               cloneClientLifecycleEvents(s.events),
		EvalRuns:             cloneMap(s.evalRuns),
		ArtifactObjects:      cloneMap(s.artifactObjects),
		EpisodeSummaries:     cloneMap(s.episodeSummaries),
	}
}

func (s *MemoryStore) loadSnapshot(snapshot Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = ensureMap(snapshot.Sessions)
	if s.sessionWriteHighWater == nil {
		s.sessionWriteHighWater = map[string]time.Time{}
	}
	for id, session := range s.sessions {
		if strings.TrimSpace(session.OwnerID) == "" {
			session.OwnerID = app.DefaultOwnerID
		}
		if strings.TrimSpace(session.Source) == "" {
			session.Source = "webchat"
		}
		if !session.CreatedAt.IsZero() && !session.UpdatedAt.IsZero() && !session.UpdatedAt.Before(session.CreatedAt) {
			session.CreatedAt = normalizeSessionTime(session.CreatedAt)
			session.UpdatedAt = normalizeSessionTime(session.UpdatedAt)
		}
		s.sessions[id] = session
		if session.UpdatedAt.After(s.sessionWriteHighWater[id]) {
			s.sessionWriteHighWater[id] = session.UpdatedAt
		}
	}
	s.clients = cloneClientMap(ensureMap(snapshot.Clients))
	if s.clientWriteHighWater == nil {
		s.clientWriteHighWater = map[string]time.Time{}
	}
	for id, client := range s.clients {
		if strings.TrimSpace(client.OwnerID) == "" {
			client.OwnerID = app.DefaultOwnerID
		}
		if strings.TrimSpace(client.ActorID) == "" {
			client.ActorID = client.OwnerID
		}
		s.clients[id] = cloneClient(client)
		highWater := client.CreatedAt
		if client.LastSeenAt != nil && client.LastSeenAt.After(highWater) {
			highWater = *client.LastSeenAt
		}
		if client.RevokedAt != nil && client.RevokedAt.After(highWater) {
			highWater = *client.RevokedAt
		}
		if highWater.After(s.clientWriteHighWater[id]) {
			s.clientWriteHighWater[id] = highWater
		}
	}
	s.ownerProfile = cloneOwnerProfile(snapshot.OwnerProfile)
	s.ownerProfiles = cloneOwnerProfileMap(snapshot.OwnerProfiles)
	if s.ownerWriteHighWater == nil {
		s.ownerWriteHighWater = map[string]time.Time{}
	}
	for id, profile := range s.ownerProfiles {
		if profile.UpdatedAt.After(s.ownerWriteHighWater[id]) {
			s.ownerWriteHighWater[id] = profile.UpdatedAt
		}
	}
	s.pairingCodes = clonePairingCodeMap(ensureMap(snapshot.PairingCodes))
	if s.pairingWriteHighWater == nil {
		s.pairingWriteHighWater = map[string]time.Time{}
	}
	for id, code := range s.pairingCodes {
		highWater := code.CreatedAt
		if code.ClaimedAt != nil && code.ClaimedAt.After(highWater) {
			highWater = *code.ClaimedAt
		}
		if highWater.After(s.pairingWriteHighWater[id]) {
			s.pairingWriteHighWater[id] = highWater
		}
	}
	s.iscpOnboardings = ensureMap(snapshot.ISCPOnboardings)
	s.mcpAccessTickets = cloneMCPAccessTicketMap(ensureMap(snapshot.MCPAccessTickets))
	s.mcpBindings = cloneMCPBindingMap(ensureMap(snapshot.MCPBindings))
	s.mcpOperations = cloneMCPOperationMap(ensureMap(snapshot.MCPOperations))
	s.messages = cloneMessageMap(ensureSliceMap(snapshot.Messages))
	s.runFeedback = ensureSliceMap(snapshot.RunFeedback)
	s.runs = ensureMap(snapshot.Runs)
	s.modelCalls = ensureMap(snapshot.ModelCalls)
	s.toolCalls = ensureMap(snapshot.ToolCalls)
	s.documentRecords = ensureMap(snapshot.DocumentRecords)
	for id, record := range s.documentRecords {
		s.documentRecords[id] = normalizePersistedDocumentRecord(record)
	}
	s.approvals = ensureMap(snapshot.Approvals)
	for id, approval := range s.approvals {
		if normalized, err := normalizePersistedApproval(approval); err == nil {
			s.approvals[id] = normalized
		}
	}
	s.reminders = ensureMap(snapshot.Reminders)
	for id, reminder := range s.reminders {
		s.reminders[id] = cloneReminder(normalizeReminder(reminder))
	}
	s.reminderDelivery = ensureMap(snapshot.ReminderDelivery)
	for id, delivery := range s.reminderDelivery {
		s.reminderDelivery[id] = normalizeReminderDelivery(delivery)
	}
	s.connectorSettings = ensureMap(snapshot.ConnectorSettings)
	s.notificationBindings = cloneNotificationBindingMap(ensureMap(snapshot.NotificationBindings))
	if s.connectorSettingWriteHighWater == nil {
		s.connectorSettingWriteHighWater = map[string]time.Time{}
	}
	for key, setting := range s.connectorSettings {
		if setting.UpdatedAt.After(s.connectorSettingWriteHighWater[key]) {
			s.connectorSettingWriteHighWater[key] = setting.UpdatedAt
		}
	}
	if s.notificationBindingWriteHighWater == nil {
		s.notificationBindingWriteHighWater = map[string]time.Time{}
	}
	s.passiveNotifications = ensureMap(snapshot.PassiveNotifications)
	// The idempotency index is derived state: older snapshots never carried it,
	// so it is always rebuilt from the notifications themselves.
	s.passiveNotificationIDsByKey = make(map[string]string, len(s.passiveNotifications))
	for id, notification := range s.passiveNotifications {
		notification = normalizePassiveNotification(notification)
		s.passiveNotifications[id] = clonePassiveNotification(notification)
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
		s.notificationBindings[id] = cloneNotificationBinding(binding)
		highWater := latestNotificationBindingTime(binding)
		if highWater.After(s.notificationBindingWriteHighWater[id]) {
			s.notificationBindingWriteHighWater[id] = highWater
		}
	}
	s.externalChatSessions = ensureMap(snapshot.ExternalChatSessions)
	for id, session := range snapshot.WeixinChatSessions {
		if _, exists := s.externalChatSessions[id]; !exists {
			s.externalChatSessions[id] = session
		}
	}
	s.externalChatMessages = ensureMap(snapshot.ExternalChatMessages)
	s.messageReceives = cloneMessageReceiveMap(ensureMap(snapshot.MessageReceives))
	s.messageDeliveries = cloneMessageDeliveryMap(ensureMap(snapshot.MessageDeliveries))
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
	s.channelInboxUpdates = cloneChannelInboxUpdateMap(ensureMap(snapshot.ChannelInboxUpdates))
	s.credentialSecrets = ensureMap(snapshot.CredentialSecrets)
	if s.credentialWriteHighWater == nil {
		s.credentialWriteHighWater = map[string]time.Time{}
	}
	for ref, secret := range s.credentialSecrets {
		highWater := latestCredentialTime(secret)
		if highWater.After(s.credentialWriteHighWater[ref]) {
			s.credentialWriteHighWater[ref] = highWater
		}
	}
	s.browserAuthRecords = ensureMap(snapshot.BrowserAuthRecords)
	for id, record := range s.browserAuthRecords {
		s.browserAuthRecords[id] = migrateLegacyBrowserAuthRecord(record)
	}
	s.browserLoginBlocks = ensureMap(snapshot.BrowserLoginBlocks)
	for id, block := range s.browserLoginBlocks {
		s.browserLoginBlocks[id] = migrateLegacyBrowserLoginBlock(block)
	}
	s.memories = ensureMap(snapshot.Memories)
	for id, memory := range s.memories {
		s.memories[id] = normalizeMemory(memory)
	}
	s.memoryCandidates = ensureMap(snapshot.MemoryCandidates)
	for id, candidate := range s.memoryCandidates {
		s.memoryCandidates[id] = cloneMemoryCandidate(normalizeMemoryCandidate(candidate))
	}
	s.auditEvents = cloneAuditEventsBestEffort(snapshot.AuditEvents)
	s.events = cloneClientLifecycleEvents(snapshot.Events)
	s.evalRuns = ensureMap(snapshot.EvalRuns)
	s.artifactObjects = ensureMap(snapshot.ArtifactObjects)
	s.artifactObjectIDsByURI = map[string]map[string]struct{}{}
	for _, object := range s.artifactObjects {
		s.indexArtifactObjectLocked(object)
	}
	s.episodeSummaries = ensureMap(snapshot.EpisodeSummaries)
	s.normalizeLinkedMCPSessionsLocked()
	s.hideLinkedExternalChatSessionsLocked()
}

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
		return app.Session{}, storeError(operation, StoreErrorInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(operation, ctx); err != nil {
		return app.Session{}, err
	}
	if _, exists := s.sessions[session.ID]; exists {
		return app.Session{}, storeError(operation, StoreErrorConflict, errors.New("session ID already exists"))
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
			return nil, storeError(OperationSessionList, StoreErrorCorrupt, err)
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
		return app.Session{}, false, storeError(OperationSessionGet, StoreErrorCorrupt, err)
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
		return app.Session{}, storeError(OperationSessionUpdateTitle, StoreErrorInvalid, errors.New("session ID is required"))
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return app.Session{}, storeError(OperationSessionUpdateTitle, StoreErrorInvalid, errors.New("session title is required"))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationSessionUpdateTitle, ctx); err != nil {
		return app.Session{}, err
	}
	session, ok := s.sessions[id]
	if !ok {
		return app.Session{}, storeError(OperationSessionUpdateTitle, StoreErrorNotFound, errors.New("session not found"))
	}
	if err := validatePersistedSession(id, session); err != nil {
		return app.Session{}, storeError(OperationSessionUpdateTitle, StoreErrorCorrupt, err)
	}
	if strings.TrimSpace(session.Source) == "mcp" {
		return app.Session{}, storeError(OperationSessionUpdateTitle, StoreErrorConflict, errors.New("MCP session title is binding-owned"))
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
		return app.Session{}, storeError(OperationSessionDelete, StoreErrorInvalid, errors.New("session ID is required"))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationSessionDelete, ctx); err != nil {
		return app.Session{}, err
	}
	session, ok := s.sessions[id]
	if !ok {
		return app.Session{}, storeError(OperationSessionDelete, StoreErrorNotFound, errors.New("session not found"))
	}
	if err := validatePersistedSession(id, session); err != nil {
		return app.Session{}, storeError(OperationSessionDelete, StoreErrorCorrupt, err)
	}
	if strings.TrimSpace(session.Source) == "mcp" {
		return app.Session{}, storeError(OperationSessionDelete, StoreErrorConflict, errors.New("MCP session history is binding-owned"))
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

func (s *MemoryStore) GetClient(ctx context.Context, id string) (app.Client, bool, error) {
	ctx, cancel := operationContext(ctx, OperationClientGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationClientGet, ctx); err != nil {
		return app.Client{}, false, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return app.Client{}, false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationClientGet, ctx); err != nil {
		return app.Client{}, false, err
	}
	client, ok := s.clients[id]
	if !ok {
		return app.Client{}, false, nil
	}
	if err := validatePersistedClient(client); err != nil {
		return app.Client{}, false, storeError(OperationClientGet, StoreErrorCorrupt, err)
	}
	return cloneClient(client), true, nil
}

func (s *MemoryStore) ListClients(ctx context.Context) ([]app.Client, error) {
	ctx, cancel := operationContext(ctx, OperationClientList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationClientList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationClientList, ctx); err != nil {
		return nil, err
	}
	out := make([]app.Client, 0, len(s.clients))
	for _, client := range s.clients {
		if err := validatePersistedClient(client); err != nil {
			return nil, storeError(OperationClientList, StoreErrorCorrupt, err)
		}
		out = append(out, cloneClient(client))
	}
	slices.SortFunc(out, compareClients)
	return out, nil
}

func compareClients(left, right app.Client) int {
	if ordered := right.CreatedAt.Compare(left.CreatedAt); ordered != 0 {
		return ordered
	}
	return strings.Compare(left.ID, right.ID)
}

func (s *MemoryStore) RevokeClient(ctx context.Context, id string) (app.Client, error) {
	ctx, cancel := operationContext(ctx, OperationClientRevoke, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationClientRevoke, ctx); err != nil {
		return app.Client{}, err
	}
	id = strings.TrimSpace(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationClientRevoke, ctx); err != nil {
		return app.Client{}, err
	}
	client, ok := s.clients[id]
	if !ok {
		return app.Client{}, storeError(OperationClientRevoke, StoreErrorNotFound, errors.New("client not found"))
	}
	if err := validatePersistedClient(client); err != nil {
		return app.Client{}, storeError(OperationClientRevoke, StoreErrorCorrupt, err)
	}
	now := nextRepositoryTime(s.clientNow(), s.clientWriteHighWater[id], client.CreatedAt, timePointerValue(client.LastSeenAt), timePointerValue(client.RevokedAt))
	client.RevokedAt = &now
	s.clientWriteHighWater[id] = now
	s.clients[id] = cloneClient(client)
	s.appendAuditLockedAt(now, "client.revoked", "", "", "owner", client.Name, map[string]any{"client_id": client.ID})
	s.appendEventLockedAt(now, "client.revoked", "", "", cloneClient(client))
	return cloneClient(client), nil
}

func (s *MemoryStore) FindClientByTokenHash(ctx context.Context, tokenHash string) (app.Client, bool, error) {
	ctx, cancel := operationContext(ctx, OperationClientFindTokenHash, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationClientFindTokenHash, ctx); err != nil {
		return app.Client{}, false, err
	}
	tokenHash = strings.TrimSpace(tokenHash)
	if tokenHash == "" {
		return app.Client{}, false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationClientFindTokenHash, ctx); err != nil {
		return app.Client{}, false, err
	}
	for _, client := range s.clients {
		if client.TokenHash != tokenHash {
			continue
		}
		if err := validatePersistedClient(client); err != nil {
			return app.Client{}, false, storeError(OperationClientFindTokenHash, StoreErrorCorrupt, err)
		}
		if client.RevokedAt == nil {
			return cloneClient(client), true, nil
		}
	}
	return app.Client{}, false, nil
}

func (s *MemoryStore) TouchClient(ctx context.Context, id string) (app.Client, bool, error) {
	ctx, cancel := operationContext(ctx, OperationClientTouch, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationClientTouch, ctx); err != nil {
		return app.Client{}, false, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return app.Client{}, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationClientTouch, ctx); err != nil {
		return app.Client{}, false, err
	}
	client, ok := s.clients[id]
	if !ok {
		return app.Client{}, false, nil
	}
	if err := validatePersistedClient(client); err != nil {
		return app.Client{}, false, storeError(OperationClientTouch, StoreErrorCorrupt, err)
	}
	if client.RevokedAt != nil {
		return app.Client{}, false, nil
	}
	now := nextRepositoryTime(s.clientNow(), s.clientWriteHighWater[id], client.CreatedAt, timePointerValue(client.LastSeenAt))
	client.LastSeenAt = &now
	s.clientWriteHighWater[id] = now
	s.clients[id] = cloneClient(client)
	return cloneClient(client), true, nil
}

func (s *MemoryStore) GetOwnerProfile(ctx context.Context) (app.OwnerProfile, error) {
	ctx, cancel := operationContext(ctx, OperationOwnerProfileGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationOwnerProfileGet, ctx); err != nil {
		return app.OwnerProfile{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationOwnerProfileGet, ctx); err != nil {
		return app.OwnerProfile{}, err
	}
	profile, ok := s.ownerProfiles[app.DefaultOwnerID]
	if !ok {
		return app.OwnerProfile{}, storeError(OperationOwnerProfileGet, StoreErrorCorrupt, errors.New("default owner profile is missing"))
	}
	return cloneOwnerProfile(profile), nil
}

func (s *MemoryStore) UpdateOwnerProfile(ctx context.Context, profile app.OwnerProfile) (app.OwnerProfile, error) {
	profile.ID = app.DefaultOwnerID
	return s.saveOwnerProfile(ctx, OperationOwnerProfileUpdate, profile)
}

func (s *MemoryStore) GetOwnerProfileByID(ctx context.Context, id string) (app.OwnerProfile, bool, error) {
	ctx, cancel := operationContext(ctx, OperationOwnerProfileGetByID, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationOwnerProfileGetByID, ctx); err != nil {
		return app.OwnerProfile{}, false, err
	}
	id = normalizeOwnerProfileID(id)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationOwnerProfileGetByID, ctx); err != nil {
		return app.OwnerProfile{}, false, err
	}
	profile, ok := s.ownerProfiles[id]
	if !ok {
		return app.OwnerProfile{}, false, nil
	}
	return cloneOwnerProfile(profile), true, nil
}

func (s *MemoryStore) SaveOwnerProfile(ctx context.Context, profile app.OwnerProfile) (app.OwnerProfile, error) {
	return s.saveOwnerProfile(ctx, OperationOwnerProfileSave, profile)
}

func (s *MemoryStore) saveOwnerProfile(ctx context.Context, operation StoreOperation, profile app.OwnerProfile) (app.OwnerProfile, error) {
	ctx, cancel := operationContext(ctx, operation, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(operation, ctx); err != nil {
		return app.OwnerProfile{}, err
	}
	profile.ID = normalizeOwnerProfileID(profile.ID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(operation, ctx); err != nil {
		return app.OwnerProfile{}, err
	}
	current, exists := s.ownerProfiles[profile.ID]
	candidate := prepareOwnerProfile(profile, current, exists, s.ownerNow(), s.ownerWriteHighWater[profile.ID])
	s.ownerWriteHighWater[candidate.ID] = candidate.UpdatedAt
	s.ownerProfiles[candidate.ID] = cloneOwnerProfile(candidate)
	if candidate.ID == app.DefaultOwnerID {
		s.ownerProfile = cloneOwnerProfile(candidate)
	}
	s.appendAuditLocked("owner_profile.updated", "", "", "owner", candidate.DisplayName, ownerProfileAuditFields(candidate))
	s.appendEventLocked("owner_profile.updated", "", "", candidate)
	return cloneOwnerProfile(candidate), nil
}

func (s *MemoryStore) ListOwnerProfiles(ctx context.Context) ([]app.OwnerProfile, error) {
	ctx, cancel := operationContext(ctx, OperationOwnerProfileList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationOwnerProfileList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationOwnerProfileList, ctx); err != nil {
		return nil, err
	}
	out := make([]app.OwnerProfile, 0, len(s.ownerProfiles))
	for _, profile := range s.ownerProfiles {
		out = append(out, cloneOwnerProfile(profile))
	}
	slices.SortFunc(out, compareOwnerProfiles)
	return out, nil
}

func (s *MemoryStore) FindOwnerProfileByExternalRef(ctx context.Context, source, externalRef string) (app.OwnerProfile, bool, error) {
	ctx, cancel := operationContext(ctx, OperationOwnerProfileFindExternalRef, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationOwnerProfileFindExternalRef, ctx); err != nil {
		return app.OwnerProfile{}, false, err
	}
	source = strings.TrimSpace(source)
	externalRef = strings.TrimSpace(externalRef)
	if source == "" || externalRef == "" {
		return app.OwnerProfile{}, false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationOwnerProfileFindExternalRef, ctx); err != nil {
		return app.OwnerProfile{}, false, err
	}
	var matches []app.OwnerProfile
	for _, profile := range s.ownerProfiles {
		if profile.Source == source && profile.ExternalRef == externalRef {
			matches = append(matches, cloneOwnerProfile(profile))
		}
	}
	if len(matches) == 0 {
		return app.OwnerProfile{}, false, nil
	}
	slices.SortFunc(matches, compareOwnerProfiles)
	return matches[0], true, nil
}

func (s *MemoryStore) SavePairingCode(ctx context.Context, code app.PairingCode) (app.PairingCode, error) {
	ctx, cancel := operationContext(ctx, OperationPairingCodeSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPairingCodeSave, ctx); err != nil {
		return app.PairingCode{}, err
	}
	code, err := normalizePairingSave(code)
	if err != nil {
		return app.PairingCode{}, storeError(OperationPairingCodeSave, StoreErrorInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationPairingCodeSave, ctx); err != nil {
		return app.PairingCode{}, err
	}
	if _, exists := s.pairingCodes[code.ID]; exists {
		return app.PairingCode{}, storeError(OperationPairingCodeSave, StoreErrorConflict, errors.New("pairing ID already exists"))
	}
	for _, existing := range s.pairingCodes {
		if strings.TrimSpace(existing.CodeHash) != "" && existing.CodeHash == code.CodeHash {
			return app.PairingCode{}, storeError(OperationPairingCodeSave, StoreErrorConflict, errors.New("pairing code hash already exists"))
		}
	}
	createdAt := nextRepositoryTime(s.clientNow(), s.pairingWriteHighWater[code.ID])
	code.CreatedAt = createdAt
	s.pairingWriteHighWater[code.ID] = createdAt
	s.pairingCodes[code.ID] = clonePairingCode(code)
	s.appendAuditLockedAt(createdAt, "pairing_code.created", "", "", "gateway", "Pairing code created", map[string]any{"pairing_id": code.ID})
	s.appendEventLockedAt(createdAt, "pairing_code.created", "", "", clonePairingCode(code))
	return clonePairingCode(code), nil
}

func (s *MemoryStore) GetPairingCode(ctx context.Context, id string) (app.PairingCode, bool, error) {
	ctx, cancel := operationContext(ctx, OperationPairingCodeGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPairingCodeGet, ctx); err != nil {
		return app.PairingCode{}, false, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return app.PairingCode{}, false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationPairingCodeGet, ctx); err != nil {
		return app.PairingCode{}, false, err
	}
	code, ok := s.pairingCodes[id]
	if !ok {
		return app.PairingCode{}, false, nil
	}
	if err := validatePersistedPairingCode(code, s.clients); err != nil {
		return app.PairingCode{}, false, storeError(OperationPairingCodeGet, StoreErrorCorrupt, err)
	}
	return clonePairingCode(code), true, nil
}

func (s *MemoryStore) ClaimPairingCode(ctx context.Context, id string, client app.Client) (app.PairingCode, app.Client, error) {
	ctx, cancel := operationContext(ctx, OperationPairingCodeClaim, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPairingCodeClaim, ctx); err != nil {
		return app.PairingCode{}, app.Client{}, err
	}
	client, err := normalizeClaimClient(client)
	if err != nil {
		return app.PairingCode{}, app.Client{}, storeError(OperationPairingCodeClaim, StoreErrorInvalid, err)
	}
	id = strings.TrimSpace(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationPairingCodeClaim, ctx); err != nil {
		return app.PairingCode{}, app.Client{}, err
	}
	code, ok := s.pairingCodes[id]
	if !ok {
		return app.PairingCode{}, app.Client{}, storeError(OperationPairingCodeClaim, StoreErrorNotFound, errors.New("pairing code not found"))
	}
	if err := validatePersistedPairingCode(code, s.clients); err != nil {
		return app.PairingCode{}, app.Client{}, storeError(OperationPairingCodeClaim, StoreErrorCorrupt, err)
	}
	now := postgresTime(s.clientNow())
	if code.Status != "pending" || strings.TrimSpace(code.CodeHash) == "" || !code.ExpiresAt.After(now) {
		return app.PairingCode{}, app.Client{}, storeError(OperationPairingCodeClaim, StoreErrorConflict, errors.New("pairing code is not claimable"))
	}
	if _, exists := s.clients[client.ID]; exists {
		return app.PairingCode{}, app.Client{}, storeError(OperationPairingCodeClaim, StoreErrorConflict, errors.New("client ID already exists"))
	}
	for _, existing := range s.clients {
		if strings.TrimSpace(existing.TokenHash) != "" && existing.TokenHash == client.TokenHash {
			return app.PairingCode{}, app.Client{}, storeError(OperationPairingCodeClaim, StoreErrorConflict, errors.New("client token hash already exists"))
		}
	}
	commandAt := nextRepositoryTime(now, s.pairingWriteHighWater[id], s.clientWriteHighWater[client.ID], code.CreatedAt, timePointerValue(code.ClaimedAt))
	client.CreatedAt = commandAt
	code.Status = "claimed"
	code.ClaimedAt = cloneTimePointer(&commandAt)
	code.ClientID = client.ID
	s.clientWriteHighWater[client.ID] = commandAt
	s.pairingWriteHighWater[id] = commandAt
	s.clients[client.ID] = cloneClient(client)
	s.pairingCodes[id] = clonePairingCode(code)
	s.appendAuditLockedAt(commandAt, "client.saved", "", "", "gateway", client.Name, map[string]any{"client_id": client.ID})
	s.appendAuditLockedAt(commandAt, "pairing_code.claimed", "", "", "gateway", "Pairing code claimed", map[string]any{"pairing_id": code.ID, "client_id": client.ID})
	s.appendEventLockedAt(commandAt, "client.saved", "", "", cloneClient(client))
	s.appendEventLockedAt(commandAt, "pairing_code.claimed", "", "", clonePairingCode(code))
	return clonePairingCode(code), cloneClient(client), nil
}

func (s *MemoryStore) AddMessage(ctx context.Context, message app.Message) (app.Message, error) {
	ctx, cancel := operationContext(ctx, OperationConversationAddMessage, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationConversationAddMessage, ctx); err != nil {
		return app.Message{}, err
	}
	message, err := prepareMessage(message, time.Now())
	if err != nil {
		return app.Message{}, storeError(OperationConversationAddMessage, StoreErrorInvalid, err)
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
		return app.Message{}, storeError(OperationConversationAddMessage, StoreErrorNotFound, errors.New("message session not found"))
	}
	if err := validatePersistedSession(message.SessionID, session); err != nil {
		return app.Message{}, storeError(OperationConversationAddMessage, StoreErrorCorrupt, err)
	}
	s.messages[message.SessionID] = append(s.messages[message.SessionID], cloneMessage(message))
	session.UpdatedAt = nextSessionTime(message.CreatedAt, session.UpdatedAt, s.sessionWriteHighWater[session.ID])
	s.sessionWriteHighWater[session.ID] = session.UpdatedAt
	if !session.Hidden && (session.Title == "" || session.Title == "New SparkClaw Session") {
		session.Title = deriveTitle(message.Content)
	}
	s.sessions[message.SessionID] = session
	s.appendEventLocked("message.created", message.SessionID, message.RunID, cloneMessage(message))
	return cloneMessage(message), nil
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
	slices.SortFunc(messages, func(left, right app.Message) int {
		if compared := left.CreatedAt.Compare(right.CreatedAt); compared != 0 {
			return compared
		}
		return strings.Compare(left.ID, right.ID)
	})
	return messages, nil
}

func (s *MemoryStore) SaveRunFeedback(ctx context.Context, feedback app.RunFeedback) (app.RunFeedback, error) {
	ctx, cancel := operationContext(ctx, OperationRunFeedbackSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationRunFeedbackSave, ctx); err != nil {
		return app.RunFeedback{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationRunFeedbackSave, ctx); err != nil {
		return app.RunFeedback{}, err
	}
	items := s.runFeedback[feedback.RunID]
	var existing *app.RunFeedback
	existingIndex := -1
	for i, current := range items {
		if current.ID == feedback.ID || current.MessageID != "" && current.MessageID == feedback.MessageID {
			existingCopy := current
			existing = &existingCopy
			existingIndex = i
			break
		}
	}
	feedback, err := prepareRunFeedback(feedback, existing, time.Now().UTC())
	if err != nil {
		return app.RunFeedback{}, storeError(OperationRunFeedbackSave, StoreErrorInvalid, err)
	}
	if existingIndex >= 0 {
		items[existingIndex] = feedback
	} else {
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
	return feedback, nil
}

func (s *MemoryStore) ListRunFeedback(ctx context.Context, runID string) ([]app.RunFeedback, error) {
	ctx, cancel := operationContext(ctx, OperationRunFeedbackList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationRunFeedbackList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationRunFeedbackList, ctx); err != nil {
		return nil, err
	}
	out := []app.RunFeedback{}
	if runID != "" {
		out = append(out, s.runFeedback[runID]...)
	} else {
		for _, items := range s.runFeedback {
			out = append(out, items...)
		}
	}
	slices.SortFunc(out, func(a, b app.RunFeedback) int {
		if order := b.UpdatedAt.Compare(a.UpdatedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return cloneRunFeedback(out), nil
}

func (s *MemoryStore) SaveRun(ctx context.Context, run app.AgentRun) (app.AgentRun, error) {
	ctx, cancel := operationContext(ctx, OperationRunSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationRunSave, ctx); err != nil {
		return app.AgentRun{}, err
	}
	run, err := prepareRun(run, time.Now().UTC())
	if err != nil {
		return app.AgentRun{}, storeError(OperationRunSave, StoreErrorInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationRunSave, ctx); err != nil {
		return app.AgentRun{}, err
	}
	s.runs[run.ID] = run
	s.appendEventLocked("run."+run.State, run.SessionID, run.ID, run)
	return cloneRun(run)
}

func (s *MemoryStore) GetRun(ctx context.Context, id string) (app.AgentRun, bool, error) {
	ctx, cancel := operationContext(ctx, OperationRunGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationRunGet, ctx); err != nil {
		return app.AgentRun{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationRunGet, ctx); err != nil {
		return app.AgentRun{}, false, err
	}
	run, ok := s.runs[id]
	if !ok {
		return app.AgentRun{}, false, nil
	}
	cloned, err := cloneRun(run)
	if err != nil {
		return app.AgentRun{}, false, storeError(OperationRunGet, StoreErrorCorrupt, err)
	}
	return cloned, true, nil
}

func (s *MemoryStore) ListRuns(ctx context.Context, sessionID string) ([]app.AgentRun, error) {
	ctx, cancel := operationContext(ctx, OperationRunList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationRunList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationRunList, ctx); err != nil {
		return nil, err
	}
	out := []app.AgentRun{}
	for _, run := range s.runs {
		if sessionID == "" || run.SessionID == sessionID {
			cloned, err := cloneRun(run)
			if err != nil {
				return nil, storeError(OperationRunList, StoreErrorCorrupt, err)
			}
			out = append(out, cloned)
		}
	}
	slices.SortFunc(out, func(a, b app.AgentRun) int {
		if order := b.StartedAt.Compare(a.StartedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (s *MemoryStore) SaveModelCall(ctx context.Context, call app.ModelCall) (app.ModelCall, error) {
	ctx, cancel := operationContext(ctx, OperationModelCallSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationModelCallSave, ctx); err != nil {
		return app.ModelCall{}, err
	}
	call, err := prepareModelCall(call, time.Now().UTC())
	if err != nil {
		return app.ModelCall{}, storeError(OperationModelCallSave, StoreErrorInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationModelCallSave, ctx); err != nil {
		return app.ModelCall{}, err
	}
	s.modelCalls[call.ID] = call
	s.appendAuditLocked("model_call."+call.Status, call.SessionID, call.RunID, "model-router", call.Model, map[string]any{
		"lane":       call.Lane,
		"profile":    call.Profile,
		"operation":  call.Operation,
		"latency_ms": call.LatencyMS,
	})
	s.appendEventLocked("model_call."+call.Status, call.SessionID, call.RunID, call)
	return call, nil
}

func (s *MemoryStore) ListModelCalls(ctx context.Context, sessionID, runID string) ([]app.ModelCall, error) {
	ctx, cancel := operationContext(ctx, OperationModelCallList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationModelCallList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationModelCallList, ctx); err != nil {
		return nil, err
	}
	out := []app.ModelCall{}
	for _, call := range s.modelCalls {
		if (sessionID == "" || call.SessionID == sessionID) && (runID == "" || call.RunID == runID) {
			out = append(out, call)
		}
	}
	slices.SortFunc(out, func(a, b app.ModelCall) int {
		if order := a.StartedAt.Compare(b.StartedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (s *MemoryStore) SaveToolCall(ctx context.Context, call app.ToolCall) (app.ToolCall, error) {
	ctx, cancel := operationContext(ctx, OperationToolCallSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationToolCallSave, ctx); err != nil {
		return app.ToolCall{}, err
	}
	call, err := prepareToolCall(call, time.Now().UTC())
	if err != nil {
		return app.ToolCall{}, storeError(OperationToolCallSave, StoreErrorInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationToolCallSave, ctx); err != nil {
		return app.ToolCall{}, err
	}
	s.toolCalls[call.ID] = call
	s.appendAuditLocked("tool_call."+call.Status, call.SessionID, call.RunID, "agent", call.Tool, map[string]any{
		"risk": call.Risk,
		"id":   call.ID,
	})
	s.appendEventLocked("tool_call."+call.Status, call.SessionID, call.RunID, call)
	return cloneToolCall(call)
}

func (s *MemoryStore) GetToolCall(ctx context.Context, id string) (app.ToolCall, bool, error) {
	ctx, cancel := operationContext(ctx, OperationToolCallGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationToolCallGet, ctx); err != nil {
		return app.ToolCall{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationToolCallGet, ctx); err != nil {
		return app.ToolCall{}, false, err
	}
	call, ok := s.toolCalls[id]
	if !ok {
		return app.ToolCall{}, false, nil
	}
	cloned, err := cloneToolCall(call)
	if err != nil {
		return app.ToolCall{}, false, storeError(OperationToolCallGet, StoreErrorCorrupt, err)
	}
	return cloned, true, nil
}

func (s *MemoryStore) ListToolCalls(ctx context.Context, sessionID string) ([]app.ToolCall, error) {
	ctx, cancel := operationContext(ctx, OperationToolCallList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationToolCallList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationToolCallList, ctx); err != nil {
		return nil, err
	}
	out := []app.ToolCall{}
	for _, call := range s.toolCalls {
		if sessionID == "" || call.SessionID == sessionID {
			cloned, err := cloneToolCall(call)
			if err != nil {
				return nil, storeError(OperationToolCallList, StoreErrorCorrupt, err)
			}
			out = append(out, cloned)
		}
	}
	slices.SortFunc(out, func(a, b app.ToolCall) int {
		if order := a.StartedAt.Compare(b.StartedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (s *MemoryStore) SaveDocumentRecord(ctx context.Context, record app.DocumentRecord) (app.DocumentRecord, error) {
	ctx, cancel := operationContext(ctx, OperationDocumentRecordSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationDocumentRecordSave, ctx); err != nil {
		return app.DocumentRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationDocumentRecordSave, ctx); err != nil {
		return app.DocumentRecord{}, err
	}
	var existing *app.DocumentRecord
	if current, ok := s.documentRecords[record.ID]; ok {
		existing = &current
	}
	record = prepareDocumentRecord(record, existing, time.Now())
	s.documentRecords[record.ID] = record
	s.appendAuditLocked("document.saved", record.SessionID, record.SourceRunID, "document_registry", record.LastActivity, map[string]any{
		"document_id": record.ID,
		"path":        record.GovernedPath,
		"activity_id": record.LastActivityID,
	})
	s.appendEventLocked("document.saved", record.SessionID, record.SourceRunID, record)
	return record, nil
}

func (s *MemoryStore) GetDocumentRecord(ctx context.Context, id string) (app.DocumentRecord, bool, error) {
	ctx, cancel := operationContext(ctx, OperationDocumentRecordGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationDocumentRecordGet, ctx); err != nil {
		return app.DocumentRecord{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationDocumentRecordGet, ctx); err != nil {
		return app.DocumentRecord{}, false, err
	}
	record, ok := s.documentRecords[id]
	return record, ok, nil
}

func (s *MemoryStore) ListDocumentRecords(ctx context.Context, ownerID, sessionID string, limit int) ([]app.DocumentRecord, error) {
	ctx, cancel := operationContext(ctx, OperationDocumentRecordList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationDocumentRecordList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationDocumentRecordList, ctx); err != nil {
		return nil, err
	}
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
	limit = normalizeDocumentRecordLimit(limit)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *MemoryStore) SaveApproval(ctx context.Context, approval app.Approval) (app.Approval, error) {
	ctx, cancel := operationContext(ctx, OperationApprovalSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationApprovalSave, ctx); err != nil {
		return app.Approval{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationApprovalSave, ctx); err != nil {
		return app.Approval{}, err
	}
	var existing *app.Approval
	if current, ok := s.approvals[strings.TrimSpace(approval.ID)]; ok {
		existing = &current
	}
	approval, err := prepareApproval(approval, existing, time.Now())
	if err != nil {
		return app.Approval{}, storeError(OperationApprovalSave, StoreErrorInvalid, err)
	}
	if existing != nil {
		if approvalsEqual(*existing, approval) {
			return cloneApproval(approval)
		}
		return app.Approval{}, storeError(OperationApprovalSave, StoreErrorConflict, ErrApprovalConflict)
	}
	if approval.ExternalID != "" {
		for id, current := range s.approvals {
			if id != approval.ID && current.Source == approval.Source && current.ExternalID == approval.ExternalID {
				return app.Approval{}, storeError(OperationApprovalSave, StoreErrorConflict, ErrApprovalConflict)
			}
		}
	}
	s.approvals[approval.ID] = approval
	s.appendAuditLocked("approval."+approval.Status, approval.SessionID, approval.RunID, approvalActor(approval), approval.Summary, approvalLifecycleFields(approval))
	s.appendEventLocked("approval."+approval.Status, approval.SessionID, approval.RunID, approval)
	return cloneApproval(approval)
}

func (s *MemoryStore) GetApproval(ctx context.Context, id string) (app.Approval, bool, error) {
	ctx, cancel := operationContext(ctx, OperationApprovalGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationApprovalGet, ctx); err != nil {
		return app.Approval{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationApprovalGet, ctx); err != nil {
		return app.Approval{}, false, err
	}
	approval, ok := s.approvals[id]
	if !ok {
		return app.Approval{}, false, nil
	}
	approval, err := normalizePersistedApproval(approval)
	if err != nil {
		return app.Approval{}, false, storeError(OperationApprovalGet, StoreErrorCorrupt, err)
	}
	return approval, true, nil
}

func (s *MemoryStore) FindApprovalByExternalRef(ctx context.Context, source app.ApprovalSource, externalID string) (app.Approval, bool, error) {
	ctx, cancel := operationContext(ctx, OperationApprovalFindExternalRef, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationApprovalFindExternalRef, ctx); err != nil {
		return app.Approval{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationApprovalFindExternalRef, ctx); err != nil {
		return app.Approval{}, false, err
	}
	var matched app.Approval
	found := false
	for _, approval := range s.approvals {
		if approval.Source == source && approval.ExternalID == externalID {
			if !found || approval.CreatedAt.After(matched.CreatedAt) || (approval.CreatedAt.Equal(matched.CreatedAt) && approval.ID < matched.ID) {
				matched = approval
				found = true
			}
		}
	}
	if !found {
		return app.Approval{}, false, nil
	}
	matched, err := normalizePersistedApproval(matched)
	if err != nil {
		return app.Approval{}, false, storeError(OperationApprovalFindExternalRef, StoreErrorCorrupt, err)
	}
	return matched, true, nil
}

func (s *MemoryStore) UpdatePendingApproval(ctx context.Context, command ApprovalUpdateCommand) (app.Approval, error) {
	ctx, cancel := operationContext(ctx, OperationApprovalUpdatePending, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationApprovalUpdatePending, ctx); err != nil {
		return app.Approval{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationApprovalUpdatePending, ctx); err != nil {
		return app.Approval{}, err
	}
	current, ok := s.approvals[command.Candidate.ID]
	if !ok {
		return app.Approval{}, storeError(OperationApprovalUpdatePending, StoreErrorNotFound, ErrApprovalNotFound)
	}
	approval, err := preparePendingApprovalUpdate(command, current)
	if err != nil {
		code := StoreErrorInvalid
		if errors.Is(err, ErrApprovalConflict) {
			code = StoreErrorConflict
		}
		return app.Approval{}, storeError(OperationApprovalUpdatePending, code, err)
	}
	s.approvals[approval.ID] = approval
	s.appendAuditLocked("approval.modified", approval.SessionID, approval.RunID, approvalUpdateActor(approval), approval.Summary, approvalUpdateFields(approval, command.Note))
	s.appendEventLocked("approval.pending", approval.SessionID, approval.RunID, approval)
	return cloneApproval(approval)
}

func (s *MemoryStore) ResolveApproval(ctx context.Context, id, status, note string) (app.Approval, error) {
	ctx, cancel := operationContext(ctx, OperationApprovalResolve, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationApprovalResolve, ctx); err != nil {
		return app.Approval{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationApprovalResolve, ctx); err != nil {
		return app.Approval{}, err
	}
	approval, ok := s.approvals[id]
	if !ok {
		return app.Approval{}, storeError(OperationApprovalResolve, StoreErrorNotFound, ErrApprovalNotFound)
	}
	approval, replay, err := prepareApprovalResolution(approval, status, note, time.Now())
	if err != nil {
		code := StoreErrorInvalid
		if errors.Is(err, ErrApprovalConflict) {
			code = StoreErrorConflict
		}
		return app.Approval{}, storeError(OperationApprovalResolve, code, err)
	}
	if replay {
		return cloneApproval(approval)
	}
	s.approvals[id] = approval
	s.appendAuditLocked("approval."+status, approval.SessionID, approval.RunID, approvalResolutionActor(status), approval.Summary, approvalLifecycleFields(approval))
	s.appendEventLocked("approval."+status, approval.SessionID, approval.RunID, approval)
	return cloneApproval(approval)
}

func (s *MemoryStore) ListApprovals(ctx context.Context, status string) ([]app.Approval, error) {
	ctx, cancel := operationContext(ctx, OperationApprovalList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationApprovalList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationApprovalList, ctx); err != nil {
		return nil, err
	}
	out := []app.Approval{}
	for _, approval := range s.approvals {
		approval, err := normalizePersistedApproval(approval)
		if err != nil {
			return nil, storeError(OperationApprovalList, StoreErrorCorrupt, err)
		}
		if status == "" || approval.Status == status {
			out = append(out, approval)
		}
	}
	sortApprovals(out)
	return out, nil
}

func (s *MemoryStore) SaveReminder(ctx context.Context, reminder app.Reminder) (app.Reminder, error) {
	ctx, cancel := operationContext(ctx, OperationReminderSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationReminderSave, ctx); err != nil {
		return app.Reminder{}, err
	}
	reminder = prepareReminder(reminder, time.Now().UTC())
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationReminderSave, ctx); err != nil {
		return app.Reminder{}, err
	}
	s.reminders[reminder.ID] = cloneReminder(reminder)
	s.appendAuditLocked("reminder."+reminder.Status, reminder.SessionID, reminder.RunID, "toolhub", reminder.TextSummary, map[string]any{
		"reminder_id": reminder.ID,
		"due_time":    reminder.DueTime.UTC().Format(time.RFC3339),
		"channel":     reminder.Channel,
	})
	s.appendEventLocked("reminder."+reminder.Status, reminder.SessionID, reminder.RunID, reminder)
	return cloneReminder(reminder), nil
}

func (s *MemoryStore) UpdatePendingReminder(ctx context.Context, reminder app.Reminder, expectedUpdatedAt time.Time) (app.Reminder, error) {
	ctx, cancel := operationContext(ctx, OperationReminderUpdatePending, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationReminderUpdatePending, ctx); err != nil {
		return app.Reminder{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationReminderUpdatePending, ctx); err != nil {
		return app.Reminder{}, err
	}
	current, ok := s.reminders[reminder.ID]
	if !ok || current.Status != "pending" || !current.UpdatedAt.Equal(postgresTime(expectedUpdatedAt)) {
		return app.Reminder{}, storeError(OperationReminderUpdatePending, StoreErrorConflict, ErrReminderConflict)
	}
	reminder = prepareReminderUpdate(reminder, current, time.Now().UTC())
	s.reminders[reminder.ID] = cloneReminder(reminder)
	s.appendAuditLocked("reminder."+reminder.Status, reminder.SessionID, reminder.RunID, "toolhub", reminder.TextSummary, map[string]any{
		"reminder_id": reminder.ID,
		"due_time":    reminder.DueTime.UTC().Format(time.RFC3339),
		"channel":     reminder.Channel,
	})
	s.appendEventLocked("reminder."+reminder.Status, reminder.SessionID, reminder.RunID, reminder)
	return cloneReminder(reminder), nil
}

func (s *MemoryStore) GetReminder(ctx context.Context, id string) (app.Reminder, bool, error) {
	ctx, cancel := operationContext(ctx, OperationReminderGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationReminderGet, ctx); err != nil {
		return app.Reminder{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationReminderGet, ctx); err != nil {
		return app.Reminder{}, false, err
	}
	reminder, ok := s.reminders[id]
	return cloneReminder(reminder), ok, nil
}

func (s *MemoryStore) ListReminders(ctx context.Context, filter app.ReminderFilter) ([]app.Reminder, error) {
	ctx, cancel := operationContext(ctx, OperationReminderList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationReminderList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationReminderList, ctx); err != nil {
		return nil, err
	}
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
		out = append(out, cloneReminder(reminder))
	}
	sortReminders(out)
	limit := normalizeReminderQueryLimit(filter.Limit)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ClaimDueReminders atomically flips due pending reminders to "sending" and
// returns them, so overlapping ticks cannot deliver the same reminder twice.
// Reminders left in "sending" since before staleBefore (a crashed or hung
// delivery) are reclaimed.
func (s *MemoryStore) ClaimDueReminders(ctx context.Context, now, staleBefore time.Time, limit int) ([]app.Reminder, error) {
	ctx, cancel := operationContext(ctx, OperationReminderClaimDue, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationReminderClaimDue, ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationReminderClaimDue, ctx); err != nil {
		return nil, err
	}
	now = postgresTime(now)
	staleBefore = postgresTime(staleBefore)
	claimed := []app.Reminder{}
	for _, reminder := range s.reminders {
		switch reminder.Status {
		case "pending":
			if reminder.DueTime.After(now) {
				continue
			}
		case "sending":
			if reminder.UpdatedAt.After(staleBefore) {
				continue
			}
		default:
			continue
		}
		claimed = append(claimed, cloneReminder(reminder))
	}
	sortReminders(claimed)
	limit = normalizeReminderQueryLimit(limit)
	if len(claimed) > limit {
		claimed = claimed[:limit]
	}
	for i, reminder := range claimed {
		reminder.Status = "sending"
		reminder.UpdatedAt = now
		s.reminders[reminder.ID] = cloneReminder(reminder)
		claimed[i] = cloneReminder(reminder)
	}
	return claimed, nil
}

func (s *MemoryStore) SaveReminderDelivery(ctx context.Context, delivery app.ReminderDelivery) (app.ReminderDelivery, error) {
	ctx, cancel := operationContext(ctx, OperationReminderDeliverySave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationReminderDeliverySave, ctx); err != nil {
		return app.ReminderDelivery{}, err
	}
	now := postgresTime(time.Now().UTC())
	delivery = prepareReminderDelivery(delivery, now)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationReminderDeliverySave, ctx); err != nil {
		return app.ReminderDelivery{}, err
	}
	reminder, ok := s.reminders[delivery.ReminderID]
	if !ok {
		return app.ReminderDelivery{}, storeError(OperationReminderDeliverySave, StoreErrorNotFound, errors.New("reminder not found"))
	}
	s.reminderDelivery[delivery.ID] = delivery
	reminder.LastDeliveryID = delivery.ID
	reminder.LastError = delivery.Error
	reminder.DeliveryAttempt = delivery.Attempt
	if delivery.Status == "sent" {
		reminder.SentAt = cloneTimePointer(&delivery.SentAt)
		reminder.Status = "sent"
	} else if delivery.Status == "failed" {
		reminder.Status = "failed"
	}
	reminder.UpdatedAt = nextRepositoryTime(now, reminder.UpdatedAt)
	s.reminders[reminder.ID] = cloneReminder(reminder)
	s.appendAuditLocked("reminder_delivery."+delivery.Status, "", "", "scheduler", delivery.ProviderStatus, map[string]any{
		"delivery_id": delivery.ID,
		"reminder_id": delivery.ReminderID,
		"channel":     delivery.Channel,
		"provider":    delivery.Provider,
		"attempt":     delivery.Attempt,
	})
	s.appendEventLocked("reminder_delivery."+delivery.Status, "", delivery.ReminderID, delivery)
	return delivery, nil
}

func (s *MemoryStore) ListReminderDeliveries(ctx context.Context, reminderID string) ([]app.ReminderDelivery, error) {
	ctx, cancel := operationContext(ctx, OperationReminderDeliveryList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationReminderDeliveryList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationReminderDeliveryList, ctx); err != nil {
		return nil, err
	}
	out := []app.ReminderDelivery{}
	for _, delivery := range s.reminderDelivery {
		if reminderID == "" || delivery.ReminderID == reminderID {
			out = append(out, delivery)
		}
	}
	sortReminderDeliveries(out)
	return out, nil
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

func (s *MemoryStore) CreatePassiveNotification(ctx context.Context, notification app.PassiveNotification) (app.PassiveNotification, bool, error) {
	ctx, cancel := operationContext(ctx, OperationPassiveNotificationCreate, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPassiveNotificationCreate, ctx); err != nil {
		return app.PassiveNotification{}, false, err
	}
	var err error
	notification, err = preparePassiveNotification(notification, time.Now().UTC())
	if err != nil {
		return app.PassiveNotification{}, false, storeError(OperationPassiveNotificationCreate, StoreErrorInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationPassiveNotificationCreate, ctx); err != nil {
		return app.PassiveNotification{}, false, err
	}
	if existingID, ok := s.passiveNotificationIDsByKey[passiveNotificationKey(notification.EndpointID, notification.IdempotencyKey)]; ok {
		existing := s.passiveNotifications[existingID]
		if !passiveNotificationsEqualForReplay(existing, notification) {
			return app.PassiveNotification{}, false, storeError(OperationPassiveNotificationCreate, StoreErrorConflict, ErrPassiveNotificationConflict)
		}
		return clonePassiveNotification(existing), false, nil
	}
	if _, exists := s.passiveNotifications[notification.ID]; exists {
		return app.PassiveNotification{}, false, storeError(OperationPassiveNotificationCreate, StoreErrorConflict, ErrPassiveNotificationConflict)
	}
	s.passiveNotifications[notification.ID] = clonePassiveNotification(notification)
	s.passiveNotificationIDsByKey[passiveNotificationKey(notification.EndpointID, notification.IdempotencyKey)] = notification.ID
	s.passiveNotificationRevs[notification.OwnerID]++
	s.appendAuditLocked("notification.received", "", "", notification.OwnerID, notification.Source, map[string]any{
		"notification_id": notification.ID,
		"endpoint_id":     notification.EndpointID,
		"kind":            notification.Kind,
	})
	return clonePassiveNotification(notification), true, nil
}

func (s *MemoryStore) GetPassiveNotification(ctx context.Context, ownerID, id string) (app.PassiveNotification, bool, error) {
	ctx, cancel := operationContext(ctx, OperationPassiveNotificationGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPassiveNotificationGet, ctx); err != nil {
		return app.PassiveNotification{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationPassiveNotificationGet, ctx); err != nil {
		return app.PassiveNotification{}, false, err
	}
	notification, ok := s.passiveNotifications[id]
	if !ok || notification.OwnerID != ownerID {
		return app.PassiveNotification{}, false, nil
	}
	return clonePassiveNotification(notification), true, nil
}

func (s *MemoryStore) ListPassiveNotifications(ctx context.Context, ownerID, after string, limit int) ([]app.PassiveNotification, error) {
	ctx, cancel := operationContext(ctx, OperationPassiveNotificationList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPassiveNotificationList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationPassiveNotificationList, ctx); err != nil {
		return nil, err
	}
	limit = normalizePassiveNotificationLimit(limit)
	var cursor app.PassiveNotification
	if after != "" {
		var ok bool
		cursor, ok = s.passiveNotifications[after]
		if !ok || cursor.OwnerID != ownerID {
			return []app.PassiveNotification{}, nil
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
		out = append(out, clonePassiveNotification(notification))
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
	return out, nil
}

func (s *MemoryStore) CountUnreadPassiveNotifications(ctx context.Context, ownerID string) (int, error) {
	ctx, cancel := operationContext(ctx, OperationPassiveNotificationCount, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPassiveNotificationCount, ctx); err != nil {
		return 0, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationPassiveNotificationCount, ctx); err != nil {
		return 0, err
	}
	count := 0
	for _, notification := range s.passiveNotifications {
		if notification.OwnerID == ownerID && notification.ReadAt == nil {
			count++
		}
	}
	return count, nil
}

func (s *MemoryStore) MarkPassiveNotificationRead(ctx context.Context, ownerID, id string, readAt time.Time) (app.PassiveNotification, error) {
	ctx, cancel := operationContext(ctx, OperationPassiveNotificationMarkRead, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPassiveNotificationMarkRead, ctx); err != nil {
		return app.PassiveNotification{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationPassiveNotificationMarkRead, ctx); err != nil {
		return app.PassiveNotification{}, err
	}
	notification, ok := s.passiveNotifications[id]
	if !ok || notification.OwnerID != ownerID {
		return app.PassiveNotification{}, storeError(OperationPassiveNotificationMarkRead, StoreErrorNotFound, ErrPassiveNotificationNotFound)
	}
	if notification.ReadAt != nil {
		return clonePassiveNotification(notification), nil
	}
	if readAt.IsZero() {
		readAt = time.Now().UTC()
	} else {
		readAt = postgresTime(readAt)
	}
	readAt = postgresTime(readAt)
	notification.ReadAt = &readAt
	notification.UpdatedAt = readAt
	s.passiveNotifications[id] = notification
	s.passiveNotificationRevs[notification.OwnerID]++
	return clonePassiveNotification(notification), nil
}

func (s *MemoryStore) MarkAllPassiveNotificationsRead(ctx context.Context, ownerID string, readAt time.Time) (int, error) {
	ctx, cancel := operationContext(ctx, OperationPassiveNotificationMarkAll, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPassiveNotificationMarkAll, ctx); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationPassiveNotificationMarkAll, ctx); err != nil {
		return 0, err
	}
	if readAt.IsZero() {
		readAt = time.Now().UTC()
	} else {
		readAt = postgresTime(readAt)
	}
	readAt = postgresTime(readAt)
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

func (s *MemoryStore) PrunePassiveNotifications(ctx context.Context, cutoff time.Time, maxPerOwner int) (int, error) {
	ctx, cancel := operationContext(ctx, OperationPassiveNotificationPrune, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPassiveNotificationPrune, ctx); err != nil {
		return 0, err
	}
	cutoff = postgresTime(cutoff)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationPassiveNotificationPrune, ctx); err != nil {
		return 0, err
	}
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
	return removed, nil
}

func (s *MemoryStore) removePassiveNotificationLocked(id string, notification app.PassiveNotification) {
	delete(s.passiveNotifications, id)
	delete(s.passiveNotificationIDsByKey, passiveNotificationKey(notification.EndpointID, notification.IdempotencyKey))
	s.passiveNotificationRevs[notification.OwnerID]++
}

func (s *MemoryStore) PassiveNotificationRevision(ctx context.Context, ownerID string) (uint64, error) {
	ctx, cancel := operationContext(ctx, OperationPassiveNotificationRevision, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationPassiveNotificationRevision, ctx); err != nil {
		return 0, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationPassiveNotificationRevision, ctx); err != nil {
		return 0, err
	}
	return s.passiveNotificationRevs[ownerID], nil
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

func (s *MemoryStore) SaveMessageReceive(ctx context.Context, record app.MessageReceiveRecord) (app.MessageReceiveRecord, error) {
	ctx, cancel := operationContext(ctx, OperationMessageReceiveSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMessageReceiveSave, ctx); err != nil {
		return app.MessageReceiveRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationMessageReceiveSave, ctx); err != nil {
		return app.MessageReceiveRecord{}, err
	}
	record.ID = strings.TrimSpace(record.ID)
	record.SourceEndpointID = app.EndpointID(strings.TrimSpace(string(record.SourceEndpointID)))
	record.NativeMessageID = strings.TrimSpace(record.NativeMessageID)
	current, exists := s.messageReceives[record.ID]
	for _, candidate := range s.messageReceives {
		if candidate.SourceEndpointID != record.SourceEndpointID || candidate.NativeMessageID != record.NativeMessageID {
			continue
		}
		if exists && current.ID != candidate.ID {
			return app.MessageReceiveRecord{}, storeError(OperationMessageReceiveSave, StoreErrorConflict, ErrMessageReceiveConflict)
		}
		current, exists = candidate, true
		break
	}
	prepared, err := prepareMessageReceive(record, current, time.Now().UTC())
	if err != nil {
		code := StoreErrorInvalid
		if errors.Is(err, ErrMessageReceiveConflict) {
			code = StoreErrorConflict
		}
		return app.MessageReceiveRecord{}, storeError(OperationMessageReceiveSave, code, err)
	}
	s.messageReceives[prepared.ID] = cloneMessageReceive(prepared)
	s.appendAuditLocked("message.receive."+prepared.Status, "", prepared.LinkedRunID, "gateway", prepared.ProviderKey, map[string]any{
		"receive_id": prepared.ID, "endpoint_id": prepared.SourceEndpointID,
	})
	return cloneMessageReceive(prepared), nil
}

func (s *MemoryStore) GetMessageReceive(ctx context.Context, id string) (app.MessageReceiveRecord, bool, error) {
	ctx, cancel := operationContext(ctx, OperationMessageReceiveGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMessageReceiveGet, ctx); err != nil {
		return app.MessageReceiveRecord{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationMessageReceiveGet, ctx); err != nil {
		return app.MessageReceiveRecord{}, false, err
	}
	record, ok := s.messageReceives[strings.TrimSpace(id)]
	return cloneMessageReceive(record), ok, nil
}

func (s *MemoryStore) FindMessageReceive(ctx context.Context, sourceEndpointID app.EndpointID, nativeMessageID string) (app.MessageReceiveRecord, bool, error) {
	ctx, cancel := operationContext(ctx, OperationMessageReceiveFind, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMessageReceiveFind, ctx); err != nil {
		return app.MessageReceiveRecord{}, false, err
	}
	sourceEndpointID = app.EndpointID(strings.TrimSpace(string(sourceEndpointID)))
	nativeMessageID = strings.TrimSpace(nativeMessageID)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationMessageReceiveFind, ctx); err != nil {
		return app.MessageReceiveRecord{}, false, err
	}
	for _, record := range s.messageReceives {
		if record.SourceEndpointID == sourceEndpointID && record.NativeMessageID == nativeMessageID {
			return cloneMessageReceive(record), true, nil
		}
	}
	return app.MessageReceiveRecord{}, false, nil
}

func (s *MemoryStore) ListMessageReceives(ctx context.Context, ownerID, actorID string, limit int) ([]app.MessageReceiveRecord, error) {
	ctx, cancel := operationContext(ctx, OperationMessageReceiveList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMessageReceiveList, ctx); err != nil {
		return nil, err
	}
	ownerID, actorID = strings.TrimSpace(ownerID), strings.TrimSpace(actorID)
	limit = normalizeDeliveryRecordLimit(limit)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationMessageReceiveList, ctx); err != nil {
		return nil, err
	}
	out := []app.MessageReceiveRecord{}
	for _, record := range s.messageReceives {
		if ownerID != "" && record.OwnerID != ownerID {
			continue
		}
		if actorID != "" && record.ActorID != actorID {
			continue
		}
		out = append(out, cloneMessageReceive(record))
	}
	slices.SortFunc(out, func(a, b app.MessageReceiveRecord) int {
		if order := b.UpdatedAt.Compare(a.UpdatedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *MemoryStore) SaveMessageDelivery(ctx context.Context, record app.MessageDeliveryRecord) (app.MessageDeliveryRecord, error) {
	ctx, cancel := operationContext(ctx, OperationMessageDeliverySave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMessageDeliverySave, ctx); err != nil {
		return app.MessageDeliveryRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationMessageDeliverySave, ctx); err != nil {
		return app.MessageDeliveryRecord{}, err
	}
	record.ID = app.DeliveryID(strings.TrimSpace(string(record.ID)))
	current, exists := s.messageDeliveries[string(record.ID)]
	for _, candidate := range s.messageDeliveries {
		if candidate.OwnerID != strings.TrimSpace(record.OwnerID) || candidate.ActorID != strings.TrimSpace(record.ActorID) ||
			candidate.Request.IdempotencyKey != strings.TrimSpace(record.Request.IdempotencyKey) {
			continue
		}
		if exists && current.ID != candidate.ID {
			return app.MessageDeliveryRecord{}, storeError(OperationMessageDeliverySave, StoreErrorConflict, ErrMessageDeliveryConflict)
		}
		if candidate.ID != record.ID {
			if !messageDeliveryIdentityEqual(candidate, record) {
				return app.MessageDeliveryRecord{}, storeError(OperationMessageDeliverySave, StoreErrorConflict, ErrMessageDeliveryConflict)
			}
			return cloneMessageDelivery(candidate), nil
		}
		current, exists = candidate, true
		break
	}
	prepared, err := prepareMessageDelivery(record, current, time.Now().UTC())
	if err != nil {
		code := StoreErrorInvalid
		if errors.Is(err, ErrMessageDeliveryConflict) {
			code = StoreErrorConflict
		}
		return app.MessageDeliveryRecord{}, storeError(OperationMessageDeliverySave, code, err)
	}
	s.messageDeliveries[string(prepared.ID)] = cloneMessageDelivery(prepared)
	s.appendAuditLocked("message.send."+string(prepared.Status), "", prepared.Request.RunID, prepared.ActorID, prepared.SoftwareDisplayName, map[string]any{
		"delivery_id": prepared.ID, "endpoint_id": prepared.Request.Target, "origin": prepared.Origin,
	})
	return cloneMessageDelivery(prepared), nil
}

func (s *MemoryStore) GetMessageDelivery(ctx context.Context, id app.DeliveryID) (app.MessageDeliveryRecord, bool, error) {
	ctx, cancel := operationContext(ctx, OperationMessageDeliveryGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMessageDeliveryGet, ctx); err != nil {
		return app.MessageDeliveryRecord{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationMessageDeliveryGet, ctx); err != nil {
		return app.MessageDeliveryRecord{}, false, err
	}
	record, ok := s.messageDeliveries[strings.TrimSpace(string(id))]
	return cloneMessageDelivery(record), ok, nil
}

func (s *MemoryStore) FindMessageDeliveryByIdempotency(ctx context.Context, ownerID, actorID, idempotencyKey string) (app.MessageDeliveryRecord, bool, error) {
	ctx, cancel := operationContext(ctx, OperationMessageDeliveryFind, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMessageDeliveryFind, ctx); err != nil {
		return app.MessageDeliveryRecord{}, false, err
	}
	ownerID, actorID, idempotencyKey = strings.TrimSpace(ownerID), strings.TrimSpace(actorID), strings.TrimSpace(idempotencyKey)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationMessageDeliveryFind, ctx); err != nil {
		return app.MessageDeliveryRecord{}, false, err
	}
	for _, record := range s.messageDeliveries {
		if record.OwnerID == ownerID && record.ActorID == actorID && record.Request.IdempotencyKey == idempotencyKey {
			return cloneMessageDelivery(record), true, nil
		}
	}
	return app.MessageDeliveryRecord{}, false, nil
}

func (s *MemoryStore) ListMessageDeliveries(ctx context.Context, ownerID, actorID string, limit int) ([]app.MessageDeliveryRecord, error) {
	ctx, cancel := operationContext(ctx, OperationMessageDeliveryList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMessageDeliveryList, ctx); err != nil {
		return nil, err
	}
	ownerID, actorID = strings.TrimSpace(ownerID), strings.TrimSpace(actorID)
	limit = normalizeDeliveryRecordLimit(limit)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationMessageDeliveryList, ctx); err != nil {
		return nil, err
	}
	out := []app.MessageDeliveryRecord{}
	for _, record := range s.messageDeliveries {
		if ownerID != "" && record.OwnerID != ownerID {
			continue
		}
		if actorID != "" && record.ActorID != actorID {
			continue
		}
		out = append(out, cloneMessageDelivery(record))
	}
	slices.SortFunc(out, func(a, b app.MessageDeliveryRecord) int {
		if order := b.UpdatedAt.Compare(a.UpdatedAt); order != 0 {
			return order
		}
		return strings.Compare(string(a.ID), string(b.ID))
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *MemoryStore) SaveChannelInboxUpdate(ctx context.Context, update app.ChannelInboxUpdate) (app.ChannelInboxUpdate, error) {
	ctx, cancel := operationContext(ctx, OperationChannelInboxUpdateSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationChannelInboxUpdateSave, ctx); err != nil {
		return app.ChannelInboxUpdate{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationChannelInboxUpdateSave, ctx); err != nil {
		return app.ChannelInboxUpdate{}, err
	}
	update.ID = strings.TrimSpace(update.ID)
	update.BindingID = strings.TrimSpace(update.BindingID)
	update.Channel = strings.ToLower(strings.TrimSpace(update.Channel))
	update.ExternalID = strings.TrimSpace(update.ExternalID)
	current, exists := s.channelInboxUpdates[update.ID]
	for _, candidate := range s.channelInboxUpdates {
		if candidate.BindingID != update.BindingID || candidate.ExternalID != update.ExternalID {
			continue
		}
		if exists && current.ID != candidate.ID {
			return app.ChannelInboxUpdate{}, storeError(OperationChannelInboxUpdateSave, StoreErrorConflict, ErrChannelInboxUpdateConflict)
		}
		if candidate.ID != update.ID {
			if candidate.Channel != update.Channel {
				return app.ChannelInboxUpdate{}, storeError(OperationChannelInboxUpdateSave, StoreErrorConflict, ErrChannelInboxUpdateConflict)
			}
			return cloneChannelInboxUpdate(candidate), nil
		}
		current, exists = candidate, true
		break
	}
	prepared, err := prepareChannelInboxUpdate(update, current, time.Now().UTC())
	if err != nil {
		code := StoreErrorInvalid
		if errors.Is(err, ErrChannelInboxUpdateConflict) {
			code = StoreErrorConflict
		}
		return app.ChannelInboxUpdate{}, storeError(OperationChannelInboxUpdateSave, code, err)
	}
	s.channelInboxUpdates[prepared.ID] = cloneChannelInboxUpdate(prepared)
	return cloneChannelInboxUpdate(prepared), nil
}

func (s *MemoryStore) GetChannelInboxUpdate(ctx context.Context, id string) (app.ChannelInboxUpdate, bool, error) {
	ctx, cancel := operationContext(ctx, OperationChannelInboxUpdateGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationChannelInboxUpdateGet, ctx); err != nil {
		return app.ChannelInboxUpdate{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationChannelInboxUpdateGet, ctx); err != nil {
		return app.ChannelInboxUpdate{}, false, err
	}
	update, ok := s.channelInboxUpdates[strings.TrimSpace(id)]
	return cloneChannelInboxUpdate(update), ok, nil
}

func (s *MemoryStore) FindChannelInboxUpdate(ctx context.Context, bindingID, externalID string) (app.ChannelInboxUpdate, bool, error) {
	ctx, cancel := operationContext(ctx, OperationChannelInboxUpdateFind, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationChannelInboxUpdateFind, ctx); err != nil {
		return app.ChannelInboxUpdate{}, false, err
	}
	bindingID, externalID = strings.TrimSpace(bindingID), strings.TrimSpace(externalID)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationChannelInboxUpdateFind, ctx); err != nil {
		return app.ChannelInboxUpdate{}, false, err
	}
	for _, update := range s.channelInboxUpdates {
		if update.BindingID == bindingID && update.ExternalID == externalID {
			return cloneChannelInboxUpdate(update), true, nil
		}
	}
	return app.ChannelInboxUpdate{}, false, nil
}

func (s *MemoryStore) ListChannelInboxUpdates(ctx context.Context, channel, status string, readyBefore time.Time, limit int) ([]app.ChannelInboxUpdate, error) {
	ctx, cancel := operationContext(ctx, OperationChannelInboxUpdateList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationChannelInboxUpdateList, ctx); err != nil {
		return nil, err
	}
	channel, status = strings.ToLower(strings.TrimSpace(channel)), strings.TrimSpace(status)
	readyBefore = postgresTime(readyBefore)
	limit = normalizeDeliveryRecordLimit(limit)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationChannelInboxUpdateList, ctx); err != nil {
		return nil, err
	}
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
		out = append(out, cloneChannelInboxUpdate(update))
	}
	slices.SortFunc(out, func(a, b app.ChannelInboxUpdate) int {
		if order := a.CreatedAt.Compare(b.CreatedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func externalChatSessionTitle(channel string) string {
	if strings.EqualFold(strings.TrimSpace(channel), "telegram") {
		return "Telegram 会话"
	}
	return "微信会话"
}

func (s *MemoryStore) SaveCredentialSecret(ctx context.Context, command CredentialSaveCommand) (app.CredentialSecret, error) {
	ctx, cancel := operationContext(ctx, OperationCredentialSecretSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationCredentialSecretSave, ctx); err != nil {
		return app.CredentialSecret{}, err
	}
	command, err := normalizeCredentialSaveCommand(command)
	if err != nil {
		return app.CredentialSecret{}, storeError(OperationCredentialSecretSave, StoreErrorInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationCredentialSecretSave, ctx); err != nil {
		return app.CredentialSecret{}, err
	}
	current, exists := s.credentialSecrets[command.secret.Ref]
	if exists {
		current, err = normalizePersistedCredentialSecret(current)
		if err != nil {
			return app.CredentialSecret{}, storeError(OperationCredentialSecretSave, StoreErrorCorrupt, err)
		}
	}
	if command.mode == credentialSaveCreate {
		if exists {
			return app.CredentialSecret{}, storeError(OperationCredentialSecretSave, StoreErrorConflict, errors.New("credential already exists"))
		}
	} else if !exists || credentialSecretDigest(current) != command.expected {
		return app.CredentialSecret{}, storeError(OperationCredentialSecretSave, StoreErrorConflict, errors.New("credential changed"))
	}
	commandAt := nextRepositoryTime(s.credentialNow(), s.credentialWriteHighWater[command.secret.Ref], latestCredentialTime(current))
	candidate := command.secret
	if exists {
		candidate.CreatedAt = current.CreatedAt
		candidate.UpdatedAt = commandAt
	} else {
		candidate.CreatedAt = commandAt
		candidate.UpdatedAt = commandAt
	}
	s.credentialWriteHighWater[candidate.Ref] = commandAt
	s.credentialSecrets[candidate.Ref] = candidate
	s.appendAuditLockedAt(commandAt, "credential_secret.saved", "", "", "gateway", candidate.Kind, map[string]any{
		"ref": candidate.Ref, "kind": candidate.Kind,
	})
	return candidate, nil
}

func (s *MemoryStore) GetCredentialSecret(ctx context.Context, ref string) (app.CredentialSecret, bool, error) {
	ctx, cancel := operationContext(ctx, OperationCredentialSecretGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationCredentialSecretGet, ctx); err != nil {
		return app.CredentialSecret{}, false, err
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return app.CredentialSecret{}, false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationCredentialSecretGet, ctx); err != nil {
		return app.CredentialSecret{}, false, err
	}
	secret, ok := s.credentialSecrets[ref]
	if !ok {
		return app.CredentialSecret{}, false, nil
	}
	secret, err := normalizePersistedCredentialSecret(secret)
	if err != nil {
		return app.CredentialSecret{}, false, storeError(OperationCredentialSecretGet, StoreErrorCorrupt, err)
	}
	return secret, true, nil
}

func (s *MemoryStore) DeleteCredentialSecret(ctx context.Context, condition CredentialDeleteCondition) (app.CredentialSecret, error) {
	ctx, cancel := operationContext(ctx, OperationCredentialSecretDelete, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationCredentialSecretDelete, ctx); err != nil {
		return app.CredentialSecret{}, err
	}
	condition, err := normalizeCredentialDeleteCondition(condition)
	if err != nil {
		return app.CredentialSecret{}, storeError(OperationCredentialSecretDelete, StoreErrorInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationCredentialSecretDelete, ctx); err != nil {
		return app.CredentialSecret{}, err
	}
	secret, ok := s.credentialSecrets[condition.ref]
	if !ok {
		return app.CredentialSecret{}, storeError(OperationCredentialSecretDelete, StoreErrorNotFound, errors.New("credential not found"))
	}
	secret, err = normalizePersistedCredentialSecret(secret)
	if err != nil {
		return app.CredentialSecret{}, storeError(OperationCredentialSecretDelete, StoreErrorCorrupt, err)
	}
	if credentialSecretDigest(secret) != condition.expected {
		return app.CredentialSecret{}, storeError(OperationCredentialSecretDelete, StoreErrorConflict, errors.New("credential changed"))
	}
	commandAt := nextRepositoryTime(s.credentialNow(), s.credentialWriteHighWater[secret.Ref], latestCredentialTime(secret))
	s.credentialWriteHighWater[secret.Ref] = commandAt
	delete(s.credentialSecrets, secret.Ref)
	s.appendAuditLockedAt(commandAt, "credential_secret.deleted", "", "", "gateway", "credential deleted", map[string]any{"ref": secret.Ref})
	return secret, nil
}

func (s *MemoryStore) SaveBrowserAuthRecord(ctx context.Context, record app.BrowserAuthRecord) (app.BrowserAuthRecord, error) {
	ctx, cancel := operationContext(ctx, OperationBrowserAuthSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationBrowserAuthSave, ctx); err != nil {
		return app.BrowserAuthRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationBrowserAuthSave, ctx); err != nil {
		return app.BrowserAuthRecord{}, err
	}
	record.ID = strings.TrimSpace(record.ID)
	record = normalizeBrowserAuthRecord(record, s.browserAuthRecords[record.ID])
	s.browserAuthRecords[record.ID] = cloneBrowserAuthRecord(record)
	s.appendAuditLocked("browser_auth.record_saved", "", "", "gateway", record.SiteOrigin, browserAuthAuditFields(record, nil))
	s.appendEventLocked("browser_auth.record_saved", "", "", record)
	return cloneBrowserAuthRecord(record), nil
}

func (s *MemoryStore) GetBrowserAuthRecord(ctx context.Context, id string) (app.BrowserAuthRecord, bool, error) {
	ctx, cancel := operationContext(ctx, OperationBrowserAuthGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationBrowserAuthGet, ctx); err != nil {
		return app.BrowserAuthRecord{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationBrowserAuthGet, ctx); err != nil {
		return app.BrowserAuthRecord{}, false, err
	}
	record, ok := s.browserAuthRecords[strings.TrimSpace(id)]
	if !ok {
		return app.BrowserAuthRecord{}, false, nil
	}
	return cloneBrowserAuthRecord(record), true, nil
}

func (s *MemoryStore) FindBrowserAuthRecord(ctx context.Context, ownerID, browserProfileID, siteOrigin, siteRealm, accountHint string) (app.BrowserAuthRecord, bool, error) {
	ctx, cancel := operationContext(ctx, OperationBrowserAuthFind, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationBrowserAuthFind, ctx); err != nil {
		return app.BrowserAuthRecord{}, false, err
	}
	ownerID, browserProfileID, siteOrigin, siteRealm, accountHint = normalizeBrowserAuthLookup(ownerID, browserProfileID, siteOrigin, siteRealm, accountHint)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationBrowserAuthFind, ctx); err != nil {
		return app.BrowserAuthRecord{}, false, err
	}
	matches := []app.BrowserAuthRecord{}
	now := time.Now().UTC()
	for _, record := range s.browserAuthRecords {
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
		return app.BrowserAuthRecord{}, false, nil
	}
	slices.SortFunc(matches, func(a, b app.BrowserAuthRecord) int {
		if order := b.UpdatedAt.Compare(a.UpdatedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return cloneBrowserAuthRecord(matches[0]), true, nil
}

func (s *MemoryStore) ListBrowserAuthRecords(ctx context.Context, ownerID, browserProfileID string) ([]app.BrowserAuthRecord, error) {
	ctx, cancel := operationContext(ctx, OperationBrowserAuthList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationBrowserAuthList, ctx); err != nil {
		return nil, err
	}
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
	if err := operationContextError(OperationBrowserAuthList, ctx); err != nil {
		return nil, err
	}
	out := []app.BrowserAuthRecord{}
	for _, record := range s.browserAuthRecords {
		if ownerID != "" && record.OwnerID != ownerID {
			continue
		}
		if browserProfileID != "" && record.BrowserProfileID != browserProfileID {
			continue
		}
		out = append(out, cloneBrowserAuthRecord(record))
	}
	slices.SortFunc(out, func(a, b app.BrowserAuthRecord) int {
		if order := b.UpdatedAt.Compare(a.UpdatedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (s *MemoryStore) RevokeBrowserAuthRecord(ctx context.Context, id, reason string) (app.BrowserAuthRecord, error) {
	ctx, cancel := operationContext(ctx, OperationBrowserAuthRevoke, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationBrowserAuthRevoke, ctx); err != nil {
		return app.BrowserAuthRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationBrowserAuthRevoke, ctx); err != nil {
		return app.BrowserAuthRecord{}, err
	}
	id = strings.TrimSpace(id)
	record, ok := s.browserAuthRecords[id]
	if !ok {
		return app.BrowserAuthRecord{}, storeError(OperationBrowserAuthRevoke, StoreErrorNotFound, errors.New("browser auth record not found"))
	}
	now := postgresTime(time.Now().UTC())
	record.Status = app.BrowserAuthStatusRevoked
	record.RevokedAt = &now
	record.UpdatedAt = now
	record.LastError = strings.TrimSpace(reason)
	s.browserAuthRecords[id] = cloneBrowserAuthRecord(record)
	s.appendAuditLocked("browser_auth.record_revoked", "", "", "owner", record.SiteOrigin, browserAuthAuditFields(record, map[string]any{"reason": record.LastError}))
	s.appendEventLocked("browser_auth.record_revoked", "", "", record)
	return cloneBrowserAuthRecord(record), nil
}

func (s *MemoryStore) SaveBrowserLoginBlock(ctx context.Context, block app.BrowserLoginBlock) (app.BrowserLoginBlock, error) {
	ctx, cancel := operationContext(ctx, OperationBrowserLoginBlockSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationBrowserLoginBlockSave, ctx); err != nil {
		return app.BrowserLoginBlock{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationBrowserLoginBlockSave, ctx); err != nil {
		return app.BrowserLoginBlock{}, err
	}
	block.ID = strings.TrimSpace(block.ID)
	block = normalizeBrowserLoginBlock(block, s.browserLoginBlocks[block.ID])
	s.browserLoginBlocks[block.ID] = cloneBrowserLoginBlock(block)
	s.appendAuditLocked("browser_login_block."+block.Status, block.SessionID, block.RunID, "runtime", block.SiteOrigin, browserLoginBlockAuditFields(block, nil))
	s.appendEventLocked("browser_login_block."+block.Status, block.SessionID, block.RunID, block)
	return cloneBrowserLoginBlock(block), nil
}

func (s *MemoryStore) UpdateBrowserLoginBlock(ctx context.Context, block app.BrowserLoginBlock, expectedVersion int64) (app.BrowserLoginBlock, error) {
	ctx, cancel := operationContext(ctx, OperationBrowserLoginBlockUpdate, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationBrowserLoginBlockUpdate, ctx); err != nil {
		return app.BrowserLoginBlock{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationBrowserLoginBlockUpdate, ctx); err != nil {
		return app.BrowserLoginBlock{}, err
	}
	current, ok := s.browserLoginBlocks[strings.TrimSpace(block.ID)]
	if !ok || current.Version != expectedVersion {
		return app.BrowserLoginBlock{}, storeError(OperationBrowserLoginBlockUpdate, StoreErrorConflict, ErrBrowserHandoffConflict)
	}
	block.Version = expectedVersion + 1
	block = normalizeBrowserLoginBlock(block, current)
	s.browserLoginBlocks[block.ID] = cloneBrowserLoginBlock(block)
	s.appendAuditLocked("browser_login_block."+block.Status, block.SessionID, block.RunID, "runtime", block.SiteOrigin, browserLoginBlockAuditFields(block, nil))
	s.appendEventLocked("browser_login_block."+block.Status, block.SessionID, block.RunID, block)
	return cloneBrowserLoginBlock(block), nil
}

func (s *MemoryStore) GetBrowserLoginBlock(ctx context.Context, id string) (app.BrowserLoginBlock, bool, error) {
	ctx, cancel := operationContext(ctx, OperationBrowserLoginBlockGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationBrowserLoginBlockGet, ctx); err != nil {
		return app.BrowserLoginBlock{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationBrowserLoginBlockGet, ctx); err != nil {
		return app.BrowserLoginBlock{}, false, err
	}
	block, ok := s.browserLoginBlocks[strings.TrimSpace(id)]
	if !ok {
		return app.BrowserLoginBlock{}, false, nil
	}
	return cloneBrowserLoginBlock(block), true, nil
}

func (s *MemoryStore) FindActiveBrowserLoginBlock(ctx context.Context, sessionID string) (app.BrowserLoginBlock, bool, error) {
	ctx, cancel := operationContext(ctx, OperationBrowserLoginBlockFindActive, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationBrowserLoginBlockFindActive, ctx); err != nil {
		return app.BrowserLoginBlock{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationBrowserLoginBlockFindActive, ctx); err != nil {
		return app.BrowserLoginBlock{}, false, err
	}
	blocks := s.listBrowserLoginBlocksLocked(sessionID, "")
	for _, block := range blocks {
		if app.BrowserHandoffStatusActive(block.Status) {
			return block, true, nil
		}
	}
	return app.BrowserLoginBlock{}, false, nil
}

func (s *MemoryStore) ListBrowserLoginBlocks(ctx context.Context, sessionID, status string) ([]app.BrowserLoginBlock, error) {
	ctx, cancel := operationContext(ctx, OperationBrowserLoginBlockList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationBrowserLoginBlockList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationBrowserLoginBlockList, ctx); err != nil {
		return nil, err
	}
	return s.listBrowserLoginBlocksLocked(sessionID, status), nil
}

func (s *MemoryStore) listBrowserLoginBlocksLocked(sessionID, status string) []app.BrowserLoginBlock {
	sessionID = strings.TrimSpace(sessionID)
	status = strings.TrimSpace(status)
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
		out = append(out, cloneBrowserLoginBlock(block))
	}
	slices.SortFunc(out, func(a, b app.BrowserLoginBlock) int {
		if c := b.UpdatedAt.Compare(a.UpdatedAt); c != 0 {
			return c
		}
		return strings.Compare(b.ID, a.ID)
	})
	return out
}

func (s *MemoryStore) AddMemoryCandidate(ctx context.Context, candidate app.MemoryCandidate) (app.MemoryCandidate, error) {
	ctx, cancel := operationContext(ctx, OperationMemoryCandidateAdd, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMemoryCandidateAdd, ctx); err != nil {
		return app.MemoryCandidate{}, err
	}
	candidate = prepareMemoryCandidate(candidate, time.Now().UTC())
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationMemoryCandidateAdd, ctx); err != nil {
		return app.MemoryCandidate{}, err
	}
	s.memoryCandidates[candidate.ID] = cloneMemoryCandidate(candidate)
	s.appendAuditLocked("memory_candidate.created", candidate.SessionID, candidate.RunID, "agent", candidate.Content, map[string]any{"kind": candidate.Kind})
	s.appendEventLocked("memory_candidate.created", candidate.SessionID, candidate.RunID, candidate)
	return cloneMemoryCandidate(candidate), nil
}

func (s *MemoryStore) ResolveMemoryCandidate(ctx context.Context, id, status string) (app.MemoryCandidate, *app.Memory, error) {
	ctx, cancel := operationContext(ctx, OperationMemoryCandidateResolve, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMemoryCandidateResolve, ctx); err != nil {
		return app.MemoryCandidate{}, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationMemoryCandidateResolve, ctx); err != nil {
		return app.MemoryCandidate{}, nil, err
	}
	candidate, ok := s.memoryCandidates[id]
	if !ok {
		return app.MemoryCandidate{}, nil, storeError(OperationMemoryCandidateResolve, StoreErrorNotFound, errors.New("memory candidate not found"))
	}
	if candidate.Status != "pending" {
		return app.MemoryCandidate{}, nil, storeError(OperationMemoryCandidateResolve, StoreErrorConflict, errors.New("memory candidate already resolved"))
	}
	now := postgresTime(time.Now().UTC())
	candidate.Status = status
	candidate.ResolvedAt = &now
	s.memoryCandidates[id] = cloneMemoryCandidate(candidate)
	var memory *app.Memory
	if status == "accepted" {
		accepted := normalizeMemory(app.Memory{
			ID: app.NewID("mem"), Kind: candidate.Kind, Content: candidate.Content,
			SourceID: candidate.RunID, CreatedAt: now,
		})
		s.memories[accepted.ID] = accepted
		memory = &accepted
	}
	s.appendAuditLocked("memory_candidate."+status, candidate.SessionID, candidate.RunID, "owner", candidate.Content, nil)
	s.appendEventLocked("memory_candidate."+status, candidate.SessionID, candidate.RunID, candidate)
	return cloneMemoryCandidate(candidate), memory, nil
}

func (s *MemoryStore) ListMemoryCandidates(ctx context.Context, status string) ([]app.MemoryCandidate, error) {
	ctx, cancel := operationContext(ctx, OperationMemoryCandidateList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMemoryCandidateList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationMemoryCandidateList, ctx); err != nil {
		return nil, err
	}
	out := []app.MemoryCandidate{}
	for _, candidate := range s.memoryCandidates {
		if status == "" || candidate.Status == status {
			out = append(out, cloneMemoryCandidate(candidate))
		}
	}
	slices.SortFunc(out, func(a, b app.MemoryCandidate) int {
		if order := b.CreatedAt.Compare(a.CreatedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (s *MemoryStore) SearchMemories(ctx context.Context, query string) ([]app.Memory, error) {
	ctx, cancel := operationContext(ctx, OperationMemorySearch, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMemorySearch, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationMemorySearch, ctx); err != nil {
		return nil, err
	}
	out := []app.Memory{}
	q := strings.ToLower(query)
	for _, memory := range s.memories {
		if q == "" || strings.Contains(strings.ToLower(memory.Content), q) || strings.Contains(strings.ToLower(memory.Kind), q) {
			out = append(out, memory)
		}
	}
	slices.SortFunc(out, func(a, b app.Memory) int {
		if order := b.CreatedAt.Compare(a.CreatedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (s *MemoryStore) UpdateMemory(ctx context.Context, id, kind, content string) (app.Memory, error) {
	ctx, cancel := operationContext(ctx, OperationMemoryUpdate, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMemoryUpdate, ctx); err != nil {
		return app.Memory{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationMemoryUpdate, ctx); err != nil {
		return app.Memory{}, err
	}
	memory, ok := s.memories[id]
	if !ok {
		return app.Memory{}, storeError(OperationMemoryUpdate, StoreErrorNotFound, errors.New("memory not found"))
	}
	memory.Kind = kind
	memory.Content = content
	s.memories[id] = memory
	sessionID := s.sessionIDForRunLocked(memory.SourceID)
	s.appendAuditLocked("memory.updated", sessionID, memory.SourceID, "owner", memory.Content, map[string]any{"memory_id": memory.ID, "kind": memory.Kind})
	s.appendEventLocked("memory.updated", sessionID, memory.SourceID, memory)
	return memory, nil
}

func (s *MemoryStore) DeleteMemory(ctx context.Context, id string) (app.Memory, error) {
	ctx, cancel := operationContext(ctx, OperationMemoryDelete, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMemoryDelete, ctx); err != nil {
		return app.Memory{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationMemoryDelete, ctx); err != nil {
		return app.Memory{}, err
	}
	memory, ok := s.memories[id]
	if !ok {
		return app.Memory{}, storeError(OperationMemoryDelete, StoreErrorNotFound, errors.New("memory not found"))
	}
	delete(s.memories, id)
	sessionID := s.sessionIDForRunLocked(memory.SourceID)
	s.appendAuditLocked("memory.deleted", sessionID, memory.SourceID, "owner", memory.Content, map[string]any{"memory_id": memory.ID, "kind": memory.Kind})
	s.appendEventLocked("memory.deleted", sessionID, memory.SourceID, memory)
	return memory, nil
}

func (s *MemoryStore) PruneMemories(ctx context.Context, cutoff time.Time) ([]app.Memory, error) {
	ctx, cancel := operationContext(ctx, OperationMemoryPrune, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMemoryPrune, ctx); err != nil {
		return nil, err
	}
	if cutoff.IsZero() {
		return []app.Memory{}, nil
	}
	cutoff = postgresTime(cutoff)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationMemoryPrune, ctx); err != nil {
		return nil, err
	}
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
			"cutoff":    cutoff.Format(time.RFC3339),
		})
		s.appendEventLocked("memory.pruned", sessionID, memory.SourceID, memory)
	}
	slices.SortFunc(pruned, func(a, b app.Memory) int {
		if order := b.CreatedAt.Compare(a.CreatedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return pruned, nil
}

func (s *MemoryStore) AddAudit(ctx context.Context, event app.AuditEvent) error {
	ctx, cancel := operationContext(ctx, OperationAuditAdd, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationAuditAdd, ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationAuditAdd, ctx); err != nil {
		return err
	}
	prepared, err := prepareAuditEvent(event, time.Now().UTC())
	if err != nil {
		return storeError(OperationAuditAdd, StoreErrorInvalid, err)
	}
	s.auditEvents = append(s.auditEvents, prepared)
	return nil
}

func (s *MemoryStore) ListAudit(ctx context.Context, sessionID string) ([]app.AuditEvent, error) {
	ctx, cancel := operationContext(ctx, OperationAuditList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationAuditList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationAuditList, ctx); err != nil {
		return nil, err
	}
	out := []app.AuditEvent{}
	for _, event := range s.auditEvents {
		if sessionID == "" || event.SessionID == sessionID {
			cloned, err := cloneAuditEvent(event)
			if err != nil {
				return nil, storeError(OperationAuditList, StoreErrorCorrupt, err)
			}
			out = append(out, cloned)
		}
	}
	slices.SortFunc(out, func(a, b app.AuditEvent) int {
		if order := b.Time.Compare(a.Time); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (s *MemoryStore) EventsAfter(ctx context.Context, sessionID, after string) ([]app.Event, error) {
	ctx, cancel := operationContext(ctx, OperationAuditEventsAfter, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationAuditEventsAfter, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationAuditEventsAfter, ctx); err != nil {
		return nil, err
	}
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
			out = append(out, cloneClientLifecycleEvent(event))
		}
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
				return MessageEventPage{}, storeError(OperationConversationMessagesAfter, StoreErrorInvalid, ErrMessageEventCursorInvalid)
			}
			start = index + 1
			break
		}
		if start < 0 {
			return MessageEventPage{}, storeError(OperationConversationMessagesAfter, StoreErrorInvalid, ErrMessageEventCursorInvalid)
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

func (s *MemoryStore) SaveEvalRun(ctx context.Context, run app.EvalRun) (app.EvalRun, error) {
	ctx, cancel := operationContext(ctx, OperationEvaluationSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationEvaluationSave, ctx); err != nil {
		return app.EvalRun{}, err
	}
	prepared := prepareEvalRun(run, time.Now().UTC())
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationEvaluationSave, ctx); err != nil {
		return app.EvalRun{}, err
	}
	s.evalRuns[prepared.ID] = cloneEvalRun(prepared)
	s.appendAuditLocked("eval."+prepared.Status, "", "", "evaluator", prepared.Summary, map[string]any{
		"profile":          prepared.Profile,
		"id":               prepared.ID,
		"failure_archives": len(prepared.FailureArchives),
	})
	s.appendEventLocked("eval."+prepared.Status, "", prepared.ID, prepared)
	return cloneEvalRun(prepared), nil
}

func (s *MemoryStore) GetEvalRun(ctx context.Context, id string) (app.EvalRun, bool, error) {
	ctx, cancel := operationContext(ctx, OperationEvaluationGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationEvaluationGet, ctx); err != nil {
		return app.EvalRun{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationEvaluationGet, ctx); err != nil {
		return app.EvalRun{}, false, err
	}
	run, ok := s.evalRuns[id]
	return cloneEvalRun(run), ok, nil
}

func (s *MemoryStore) ListEvalRuns(ctx context.Context) ([]app.EvalRun, error) {
	ctx, cancel := operationContext(ctx, OperationEvaluationList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationEvaluationList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationEvaluationList, ctx); err != nil {
		return nil, err
	}
	out := []app.EvalRun{}
	for _, run := range s.evalRuns {
		out = append(out, cloneEvalRun(run))
	}
	slices.SortFunc(out, func(a, b app.EvalRun) int {
		if order := b.StartedAt.Compare(a.StartedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (s *MemoryStore) SaveArtifactObject(ctx context.Context, object app.ArtifactObject) (app.ArtifactObject, error) {
	ctx, cancel := operationContext(ctx, OperationArtifactMetadataSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationArtifactMetadataSave, ctx); err != nil {
		return app.ArtifactObject{}, err
	}
	object = prepareArtifactObject(object, time.Now().UTC())
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationArtifactMetadataSave, ctx); err != nil {
		return app.ArtifactObject{}, err
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
	return object, nil
}

func (s *MemoryStore) ListArtifactObjects(ctx context.Context, limit int) ([]app.ArtifactObject, error) {
	ctx, cancel := operationContext(ctx, OperationArtifactMetadataList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationArtifactMetadataList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationArtifactMetadataList, ctx); err != nil {
		return nil, err
	}
	out := []app.ArtifactObject{}
	for _, object := range s.artifactObjects {
		out = append(out, object)
	}
	slices.SortFunc(out, func(a, b app.ArtifactObject) int {
		if order := b.CreatedAt.Compare(a.CreatedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *MemoryStore) FindArtifactObjectByURI(ctx context.Context, uri, sessionID, runID string) (app.ArtifactObject, bool, error) {
	ctx, cancel := operationContext(ctx, OperationArtifactMetadataFindByURI, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationArtifactMetadataFindByURI, ctx); err != nil {
		return app.ArtifactObject{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationArtifactMetadataFindByURI, ctx); err != nil {
		return app.ArtifactObject{}, false, err
	}
	var newest app.ArtifactObject
	found := false
	for id := range s.artifactObjectIDsByURI[uri] {
		object, ok := s.artifactObjects[id]
		if !ok || (sessionID != "" && object.SessionID != sessionID) || (runID != "" && object.RunID != runID) {
			continue
		}
		if !found || object.CreatedAt.After(newest.CreatedAt) || object.CreatedAt.Equal(newest.CreatedAt) && object.ID < newest.ID {
			newest = object
			found = true
		}
	}
	return newest, found, nil
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

func (s *MemoryStore) SaveEpisodeSummary(ctx context.Context, summary app.EpisodeSummary) (app.EpisodeSummary, error) {
	ctx, cancel := operationContext(ctx, OperationEpisodeSummarySave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationEpisodeSummarySave, ctx); err != nil {
		return app.EpisodeSummary{}, err
	}
	summary, err := prepareEpisodeSummary(summary, time.Now().UTC())
	if err != nil {
		return app.EpisodeSummary{}, storeError(OperationEpisodeSummarySave, StoreErrorInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationEpisodeSummarySave, ctx); err != nil {
		return app.EpisodeSummary{}, err
	}
	s.episodeSummaries[summary.ID] = summary
	s.appendAuditLocked("episode_summary.saved", summary.SessionID, summary.RunID, "runtime", summary.Outcome, map[string]any{
		"tools":            summary.Tools,
		"repair_performed": summary.RepairPerformed,
	})
	s.appendEventLocked("episode_summary.saved", summary.SessionID, summary.RunID, summary)
	return cloneEpisodeSummary(summary), nil
}

func (s *MemoryStore) ListEpisodeSummaries(ctx context.Context, sessionID string) ([]app.EpisodeSummary, error) {
	ctx, cancel := operationContext(ctx, OperationEpisodeSummaryList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationEpisodeSummaryList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationEpisodeSummaryList, ctx); err != nil {
		return nil, err
	}
	out := []app.EpisodeSummary{}
	for _, summary := range s.episodeSummaries {
		if sessionID == "" || summary.SessionID == sessionID {
			out = append(out, cloneEpisodeSummary(summary))
		}
	}
	slices.SortFunc(out, func(a, b app.EpisodeSummary) int {
		if order := b.CreatedAt.Compare(a.CreatedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (s *MemoryStore) appendAuditLocked(typ, sessionID, runID, actor, summary string, fields map[string]any) {
	s.appendAuditLockedAt(time.Now().UTC(), typ, sessionID, runID, actor, summary, fields)
}

func (s *MemoryStore) appendAuditLockedAt(at time.Time, typ, sessionID, runID, actor, summary string, fields map[string]any) {
	clonedFields, err := cloneAuditFields(fields)
	if err != nil {
		clonedFields = maps.Clone(fields)
	}
	s.auditEvents = append(s.auditEvents, app.AuditEvent{
		ID:        app.NewID("audit"),
		Time:      at,
		Type:      typ,
		SessionID: sessionID,
		RunID:     runID,
		Actor:     actor,
		Summary:   summary,
		Fields:    clonedFields,
	})
}

func (s *MemoryStore) appendEventLocked(typ, sessionID, runID string, payload any) {
	s.appendEventLockedAt(time.Now().UTC(), typ, sessionID, runID, payload)
}

func (s *MemoryStore) appendEventLockedAt(at time.Time, typ, sessionID, runID string, payload any) {
	s.events = append(s.events, app.Event{
		ID:        app.NewID("evt"),
		Time:      at,
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

func normalizeOwnerProfileID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return app.DefaultOwnerID
	}
	return id
}

func prepareOwnerProfile(profile, current app.OwnerProfile, exists bool, now, lastIssued time.Time) app.OwnerProfile {
	profile.ID = normalizeOwnerProfileID(profile.ID)
	profile.Source = strings.TrimSpace(profile.Source)
	profile.ExternalRef = strings.TrimSpace(profile.ExternalRef)
	profile.WorkspaceRoot = strings.TrimSpace(profile.WorkspaceRoot)
	profile.DefaultChannel = strings.TrimSpace(profile.DefaultChannel)
	profile.DefaultBindingID = strings.TrimSpace(profile.DefaultBindingID)
	profile.DisplayName = strings.TrimSpace(profile.DisplayName)
	profile.Email = strings.TrimSpace(profile.Email)
	if profile.Source == "" && profile.ID == app.DefaultOwnerID {
		profile.Source = "web"
	}
	if profile.DisplayName == "" {
		profile.DisplayName = "Owner"
	}
	profile.Preferences = cloneStringMap(profile.Preferences)
	now = now.UTC().Truncate(time.Microsecond)
	if exists {
		profile.CreatedAt = current.CreatedAt
	} else if profile.CreatedAt.IsZero() {
		profile.CreatedAt = now
	} else {
		profile.CreatedAt = profile.CreatedAt.UTC().Truncate(time.Microsecond)
	}
	floor := current.UpdatedAt
	if lastIssued.After(floor) {
		floor = lastIssued
	}
	profile.UpdatedAt = now
	if profile.CreatedAt.After(profile.UpdatedAt) {
		profile.UpdatedAt = profile.CreatedAt.UTC().Truncate(time.Microsecond)
	}
	if !profile.UpdatedAt.After(floor) {
		profile.UpdatedAt = floor.UTC().Truncate(time.Microsecond).Add(time.Microsecond)
	}
	return profile
}

func compareOwnerProfiles(a, b app.OwnerProfile) int {
	if compared := b.UpdatedAt.Compare(a.UpdatedAt); compared != 0 {
		return compared
	}
	return strings.Compare(a.ID, b.ID)
}

func ownerProfileAuditFields(profile app.OwnerProfile) map[string]any {
	return map[string]any{
		"owner_id": profile.ID, "source": profile.Source,
		"external_ref": profile.ExternalRef != "", "email_set": profile.Email != "",
		"preferences": len(profile.Preferences), "display_name": profile.DisplayName,
	}
}

func OwnerProfilesEqual(a, b app.OwnerProfile) bool {
	return a.ID == b.ID && a.Source == b.Source && a.ExternalRef == b.ExternalRef &&
		a.WorkspaceRoot == b.WorkspaceRoot && a.DefaultChannel == b.DefaultChannel &&
		a.DefaultBindingID == b.DefaultBindingID && a.DisplayName == b.DisplayName &&
		a.Email == b.Email && a.CreatedAt.Equal(b.CreatedAt) && a.UpdatedAt.Equal(b.UpdatedAt) &&
		maps.Equal(a.Preferences, b.Preferences)
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
	now := postgresTime(time.Now().UTC())
	record = migrateLegacyBrowserAuthRecord(record)
	record.ID = strings.TrimSpace(record.ID)
	if record.ID == "" {
		record.ID = app.NewID("bauth")
	}
	if !current.CreatedAt.IsZero() {
		record.CreatedAt = current.CreatedAt
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.CreatedAt = postgresTime(record.CreatedAt)
	record.UpdatedAt = now
	return cloneBrowserAuthRecord(record)
}

func migrateLegacyBrowserAuthRecord(record app.BrowserAuthRecord) app.BrowserAuthRecord {
	record.ID = strings.TrimSpace(record.ID)
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
	if !record.LastVerifiedAt.IsZero() {
		record.LastVerifiedAt = postgresTime(record.LastVerifiedAt)
	}
	if !record.CreatedAt.IsZero() {
		record.CreatedAt = postgresTime(record.CreatedAt)
	}
	if !record.UpdatedAt.IsZero() {
		record.UpdatedAt = postgresTime(record.UpdatedAt)
	}
	record.ExpiresAt = normalizeBrowserTimePointer(record.ExpiresAt)
	record.RevokedAt = normalizeBrowserTimePointer(record.RevokedAt)
	return cloneBrowserAuthRecord(record)
}

// Legacy schema-v1 browser login block status strings, kept only so
// previously persisted snapshots can be migrated at load time.
const (
	legacyBrowserHandoffStatusWaiting  = "waiting"
	legacyBrowserHandoffStatusResuming = "resuming"
)

// migrateLegacyBrowserLoginBlock upgrades a schema-v1 block persisted by an
// older build to the v2 shape. It runs once at snapshot load — never on read
// paths — and preserves the stored time points while canonicalizing their UTC
// microsecond representation. The postgres schema performs the same status
// mapping in SQL; keep the two in sync.
func migrateLegacyBrowserLoginBlock(block app.BrowserLoginBlock) app.BrowserLoginBlock {
	block = cloneBrowserLoginBlock(block)
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
	if !block.CreatedAt.IsZero() {
		block.CreatedAt = postgresTime(block.CreatedAt)
	}
	if !block.UpdatedAt.IsZero() {
		block.UpdatedAt = postgresTime(block.UpdatedAt)
	}
	block.TransitionLeaseUntil = normalizeBrowserTimePointer(block.TransitionLeaseUntil)
	block.ResolvedAt = normalizeBrowserTimePointer(block.ResolvedAt)
	return cloneBrowserLoginBlock(block)
}

// normalizeBrowserLoginBlock is a WRITE-path helper (Save/Update in every
// backend): it stamps SchemaVersion, bumps Version past current, and sets
// UpdatedAt to now. It must never run on read paths — see
// migrateLegacyBrowserLoginBlock for the one-time snapshot-load fix-up.
func normalizeBrowserLoginBlock(block app.BrowserLoginBlock, current app.BrowserLoginBlock) app.BrowserLoginBlock {
	now := postgresTime(time.Now().UTC())
	block = cloneBrowserLoginBlock(block)
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
	if !current.CreatedAt.IsZero() {
		block.CreatedAt = current.CreatedAt
	}
	if block.CreatedAt.IsZero() {
		block.CreatedAt = now
	}
	block.CreatedAt = postgresTime(block.CreatedAt)
	block.TransitionLeaseUntil = normalizeBrowserTimePointer(block.TransitionLeaseUntil)
	block.ResolvedAt = normalizeBrowserTimePointer(block.ResolvedAt)
	if !app.BrowserHandoffStatusActive(block.Status) && block.ResolvedAt == nil {
		block.ResolvedAt = normalizeBrowserTimePointer(current.ResolvedAt)
	}
	block.UpdatedAt = now
	return cloneBrowserLoginBlock(block)
}

func normalizeBrowserTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := postgresTime(*value)
	return &normalized
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

func cloneStringMap(in map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range in {
		out[key] = value
	}
	return out
}
