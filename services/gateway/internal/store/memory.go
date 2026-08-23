package store

import (
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
