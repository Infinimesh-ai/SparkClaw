package store

import (
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *MemoryStore) snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Snapshot{
		Sessions:              cloneMap(s.sessions),
		Clients:               cloneClientMap(s.clients),
		OwnerProfile:          cloneOwnerProfile(s.ownerProfile),
		OwnerProfiles:         cloneOwnerProfileMap(s.ownerProfiles),
		PairingCodes:          clonePairingCodeMap(s.pairingCodes),
		ISCPOnboardings:       cloneMap(s.iscpOnboardings),
		MCPAccessTickets:      cloneMCPAccessTicketMap(s.mcpAccessTickets),
		MCPBindings:           cloneMCPBindingMap(s.mcpBindings),
		MCPOperations:         cloneMCPOperationMap(s.mcpOperations),
		Messages:              cloneMessageMap(s.messages),
		RunFeedback:           cloneSliceMap(s.runFeedback),
		Runs:                  cloneMap(s.runs),
		ModelCalls:            cloneMap(s.modelCalls),
		ToolCalls:             cloneMap(s.toolCalls),
		DocumentRecords:       cloneMap(s.documentRecords),
		Approvals:             cloneMap(s.approvals),
		Reminders:             cloneReminderMap(s.reminders),
		ReminderDelivery:      cloneMap(s.reminderDelivery),
		ConnectorSettings:     cloneMap(s.connectorSettings),
		EmailProviderSettings: cloneEmailProviderSettingMap(s.emailProviderSettings),
		NotificationBindings:  cloneNotificationBindingMap(s.notificationBindings),
		PassiveNotifications:  clonePassiveNotificationMap(s.passiveNotifications),
		ExternalChatSessions:  cloneMap(s.externalChatSessions),
		ExternalChatMessages:  cloneMap(s.externalChatMessages),
		MessageReceives:       cloneMessageReceiveMap(s.messageReceives),
		MessageDeliveries:     cloneMessageDeliveryMap(s.messageDeliveries),
		ChannelInboxUpdates:   cloneChannelInboxUpdateMap(s.channelInboxUpdates),
		CredentialSecrets:     cloneMap(s.credentialSecrets),
		BrowserAuthRecords:    cloneBrowserAuthRecordMap(s.browserAuthRecords),
		BrowserLoginBlocks:    cloneBrowserLoginBlockMap(s.browserLoginBlocks),
		Memories:              cloneMap(s.memories),
		MemoryCandidates:      cloneMemoryCandidateMap(s.memoryCandidates),
		AuditEvents:           cloneAuditEventsBestEffort(s.auditEvents),
		Events:                cloneClientLifecycleEvents(s.events),
		EvalRuns:              cloneMap(s.evalRuns),
		ArtifactObjects:       cloneMap(s.artifactObjects),
		EpisodeSummaries:      cloneMap(s.episodeSummaries),
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
	s.emailProviderSettings = cloneEmailProviderSettingMap(ensureMap(snapshot.EmailProviderSettings))
	s.notificationBindings = cloneNotificationBindingMap(ensureMap(snapshot.NotificationBindings))
	if s.connectorSettingWriteHighWater == nil {
		s.connectorSettingWriteHighWater = map[string]time.Time{}
	}
	for key, setting := range s.connectorSettings {
		if setting.UpdatedAt.After(s.connectorSettingWriteHighWater[key]) {
			s.connectorSettingWriteHighWater[key] = setting.UpdatedAt
		}
	}
	if s.emailProviderWriteHighWater == nil {
		s.emailProviderWriteHighWater = map[string]time.Time{}
	}
	for key, setting := range s.emailProviderSettings {
		if setting.UpdatedAt.After(s.emailProviderWriteHighWater[key]) {
			s.emailProviderWriteHighWater[key] = setting.UpdatedAt
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
	s.rebuildHistoryIndexesLocked()
	s.normalizeLinkedMCPSessionsLocked()
	s.hideLinkedExternalChatSessionsLocked()
}

func (s *MemoryStore) rebuildHistoryIndexesLocked() {
	for sessionID := range s.messages {
		slices.SortFunc(s.messages[sessionID], compareMessagesAscending)
	}
	s.toolCallIDsBySession = map[string][]string{}
	for _, call := range s.toolCalls {
		s.indexToolCallLocked(call)
	}
	s.episodeIDsBySession = map[string][]string{}
	for _, summary := range s.episodeSummaries {
		s.indexEpisodeSummaryLocked(summary)
	}
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
