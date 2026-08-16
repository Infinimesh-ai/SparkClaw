package store

import (
	"errors"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

var ErrReminderConflict = errors.New("pending reminder changed or is no longer available")
var ErrBrowserHandoffConflict = errors.New("browser handoff changed or is no longer available")
var ErrConnectorSettingConflict = errors.New("connector setting changed")

func connectorSettingAuditType(exists, currentEnabled, currentISCPEnabled, currentLANAccessEnabled bool, setting app.ConnectorSetting) string {
	if !exists || currentEnabled != setting.Enabled {
		if setting.Enabled {
			return "connector.enabled"
		}
		return "connector.disabled"
	}
	if currentISCPEnabled != setting.ISCPEnabled || currentLANAccessEnabled != setting.LANAccessEnabled {
		return "connector.transport_updated"
	}
	return "connector.updated"
}

var ErrPassiveNotificationConflict = errors.New("notification idempotency key was reused with a different payload")
var ErrPassiveNotificationNotFound = errors.New("notification not found")
var ErrMCPAccessTicketInvalid = errors.New("MCP access ticket is invalid or unavailable")
var ErrISCPOnboardingConflict = errors.New("ISCP onboarding already exists")
var ErrMCPBindingUnavailable = errors.New("MCP binding is unavailable")
var ErrMCPOperationConflict = errors.New("MCP operation idempotency key was reused with a different request")
var ErrMCPOperationVersionConflict = errors.New("MCP operation changed")

type Store interface {
	CreateSession(title string) app.Session
	CreateSessionWithScope(title, ownerID, workspaceRoot, source string, hidden bool) app.Session
	ListSessions() []app.Session
	GetSession(id string) (app.Session, bool)
	UpdateSessionTitle(id, title string) (app.Session, error)
	DeleteSession(id string) (app.Session, error)
	SaveClient(client app.Client)
	GetClient(id string) (app.Client, bool)
	ListClients() []app.Client
	RevokeClient(id string) (app.Client, error)
	FindClientByTokenHash(tokenHash string) (app.Client, bool)
	TouchClient(id string)
	GetOwnerProfile() app.OwnerProfile
	UpdateOwnerProfile(profile app.OwnerProfile) app.OwnerProfile
	GetOwnerProfileByID(id string) (app.OwnerProfile, bool)
	SaveOwnerProfile(profile app.OwnerProfile) app.OwnerProfile
	ListOwnerProfiles() []app.OwnerProfile
	FindOwnerProfileByExternalRef(source, externalRef string) (app.OwnerProfile, bool)
	SavePairingCode(code app.PairingCode)
	GetPairingCode(id string) (app.PairingCode, bool)
	ClaimPairingCode(id, clientID string) (app.PairingCode, error)
	SaveISCPOnboarding(onboarding app.ISCPOnboarding) (app.ISCPOnboarding, error)
	GetISCPOnboarding(id string) (app.ISCPOnboarding, bool)
	ListISCPOnboardings(ownerID string) []app.ISCPOnboarding
	SaveMCPAccessTicket(ticket app.MCPAccessTicket) (app.MCPAccessTicket, error)
	GetMCPAccessTicket(id string) (app.MCPAccessTicket, bool)
	FindMCPAccessTicketBySecretHash(secretHash string) (app.MCPAccessTicket, bool)
	ListMCPAccessTickets(ownerID string) []app.MCPAccessTicket
	RedeemMCPAccessTicket(secretHash string, peer app.MCPPeerIdentity, now time.Time) (app.MCPBinding, error)
	RevokeMCPAccessTicket(id string, now time.Time) (app.MCPAccessTicket, error)
	DeleteMCPAccessTicket(ownerID, id string) (app.MCPAccessTicket, error)
	GetMCPBinding(id string) (app.MCPBinding, bool)
	FindMCPBindingForPeer(domainID, deviceID, thumbprint string) (app.MCPBinding, bool)
	ListMCPBindings(ownerID string) []app.MCPBinding
	RevokeMCPBinding(id string, now time.Time) (app.MCPBinding, error)
	DeleteMCPBinding(ownerID, id string) (app.MCPBinding, error)
	DeleteMCPAccessRecords(ownerID string) (MCPAccessRecordDeletion, error)
	TouchMCPBinding(id, iscpSessionID string, now time.Time) error
	CreateMCPOperation(operation app.MCPOperation) (app.MCPOperation, bool, error)
	GetMCPOperation(id string) (app.MCPOperation, bool)
	FindMCPOperationByIdempotency(bindingID, idempotencyKey string) (app.MCPOperation, bool)
	ListMCPOperations(bindingID string) []app.MCPOperation
	UpdateMCPOperation(operation app.MCPOperation, expectedVersion int64) (app.MCPOperation, error)
	AddMessage(message app.Message) app.Message
	ListMessages(sessionID string) []app.Message
	SaveRunFeedback(feedback app.RunFeedback) app.RunFeedback
	ListRunFeedback(runID string) []app.RunFeedback
	SaveRun(run app.AgentRun)
	GetRun(id string) (app.AgentRun, bool)
	ListRuns(sessionID string) []app.AgentRun
	SaveModelCall(call app.ModelCall)
	ListModelCalls(sessionID, runID string) []app.ModelCall
	SaveToolCall(call app.ToolCall)
	GetToolCall(id string) (app.ToolCall, bool)
	ListToolCalls(sessionID string) []app.ToolCall
	SaveDocumentRecord(record app.DocumentRecord) app.DocumentRecord
	GetDocumentRecord(id string) (app.DocumentRecord, bool)
	ListDocumentRecords(ownerID, sessionID string, limit int) []app.DocumentRecord
	SaveApproval(approval app.Approval)
	GetApproval(id string) (app.Approval, bool)
	FindApprovalByExternalRef(source app.ApprovalSource, externalID string) (app.Approval, bool)
	UpdatePendingApproval(approval app.Approval) (app.Approval, error)
	ResolveApproval(id, status, note string) (app.Approval, error)
	ListApprovals(status string) []app.Approval
	SaveReminder(reminder app.Reminder) app.Reminder
	UpdatePendingReminder(reminder app.Reminder, expectedUpdatedAt time.Time) (app.Reminder, error)
	GetReminder(id string) (app.Reminder, bool)
	ListReminders(filter app.ReminderFilter) []app.Reminder
	ClaimDueReminders(now, staleBefore time.Time, limit int) []app.Reminder
	SaveReminderDelivery(delivery app.ReminderDelivery) app.ReminderDelivery
	ListReminderDeliveries(reminderID string) []app.ReminderDelivery
	GetConnectorSetting(ownerID, channel string) (app.ConnectorSetting, bool)
	ListConnectorSettings(ownerID string) []app.ConnectorSetting
	UpdateConnectorSetting(setting app.ConnectorSetting, expectedVersion int64) (app.ConnectorSetting, error)
	SaveNotificationBinding(binding app.NotificationBinding) app.NotificationBinding
	GetNotificationBinding(id string) (app.NotificationBinding, bool)
	ListNotificationBindings(channel, status string) []app.NotificationBinding
	RevokeNotificationBinding(id string) (app.NotificationBinding, error)
	CreatePassiveNotification(notification app.PassiveNotification) (app.PassiveNotification, bool, error)
	GetPassiveNotification(ownerID, id string) (app.PassiveNotification, bool)
	ListPassiveNotifications(ownerID, after string, limit int) []app.PassiveNotification
	CountUnreadPassiveNotifications(ownerID string) int
	MarkPassiveNotificationRead(ownerID, id string, readAt time.Time) (app.PassiveNotification, error)
	MarkAllPassiveNotificationsRead(ownerID string, readAt time.Time) (int, error)
	// PrunePassiveNotifications bounds the durable inbox: notifications created
	// before cutoff are removed (a zero cutoff disables the retention sweep),
	// and each owner is trimmed to maxPerOwner records (zero or negative
	// disables the cap), evicting read notifications oldest-first before
	// touching unread ones. Pruned idempotency keys become replayable; the
	// dedup window intentionally equals the retention window.
	PrunePassiveNotifications(cutoff time.Time, maxPerOwner int) int
	// PassiveNotificationRevision reports a counter that increases whenever the
	// owner's inbox changes (create, mark-read, prune). It is process-local and
	// resets on restart; callers may only compare values for equality.
	PassiveNotificationRevision(ownerID string) uint64
	SaveExternalChatSession(session app.ExternalChatSession) app.ExternalChatSession
	GetExternalChatSession(id string) (app.ExternalChatSession, bool)
	ListExternalChatSessions(channel, status string) []app.ExternalChatSession
	FindExternalChatSession(bindingID, externalChatID, externalThreadID string) (app.ExternalChatSession, bool)
	FindExternalChatSessionByLinkedSessionID(sessionID string) (app.ExternalChatSession, bool)
	SaveExternalChatMessage(message app.ExternalChatMessage) app.ExternalChatMessage
	GetExternalChatMessage(id string) (app.ExternalChatMessage, bool)
	FindExternalChatMessageByExternalID(chatSessionID, externalMessageID string) (app.ExternalChatMessage, bool)
	ListExternalChatMessages(chatSessionID string, limit int) []app.ExternalChatMessage
	SaveMessageReceive(record app.MessageReceiveRecord) app.MessageReceiveRecord
	GetMessageReceive(id string) (app.MessageReceiveRecord, bool)
	FindMessageReceive(sourceEndpointID app.EndpointID, nativeMessageID string) (app.MessageReceiveRecord, bool)
	ListMessageReceives(ownerID, actorID string, limit int) []app.MessageReceiveRecord
	SaveMessageDelivery(record app.MessageDeliveryRecord) app.MessageDeliveryRecord
	GetMessageDelivery(id app.DeliveryID) (app.MessageDeliveryRecord, bool)
	FindMessageDeliveryByIdempotency(ownerID, actorID, idempotencyKey string) (app.MessageDeliveryRecord, bool)
	ListMessageDeliveries(ownerID, actorID string, limit int) []app.MessageDeliveryRecord
	SaveChannelInboxUpdate(update app.ChannelInboxUpdate) app.ChannelInboxUpdate
	GetChannelInboxUpdate(id string) (app.ChannelInboxUpdate, bool)
	FindChannelInboxUpdate(bindingID, externalID string) (app.ChannelInboxUpdate, bool)
	ListChannelInboxUpdates(channel, status string, readyBefore time.Time, limit int) []app.ChannelInboxUpdate
	SaveCredentialSecret(secret app.CredentialSecret) app.CredentialSecret
	GetCredentialSecret(ref string) (app.CredentialSecret, bool)
	DeleteCredentialSecret(ref string) error
	SaveBrowserAuthRecord(record app.BrowserAuthRecord) app.BrowserAuthRecord
	GetBrowserAuthRecord(id string) (app.BrowserAuthRecord, bool)
	FindBrowserAuthRecord(ownerID, browserProfileID, siteOrigin, siteRealm, accountHint string) (app.BrowserAuthRecord, bool)
	ListBrowserAuthRecords(ownerID, browserProfileID string) []app.BrowserAuthRecord
	RevokeBrowserAuthRecord(id, reason string) (app.BrowserAuthRecord, error)
	SaveBrowserLoginBlock(block app.BrowserLoginBlock) app.BrowserLoginBlock
	UpdateBrowserLoginBlock(block app.BrowserLoginBlock, expectedVersion int64) (app.BrowserLoginBlock, error)
	GetBrowserLoginBlock(id string) (app.BrowserLoginBlock, bool)
	FindActiveBrowserLoginBlock(sessionID string) (app.BrowserLoginBlock, bool)
	ListBrowserLoginBlocks(sessionID, status string) []app.BrowserLoginBlock
	AddMemoryCandidate(candidate app.MemoryCandidate) app.MemoryCandidate
	ResolveMemoryCandidate(id, status string) (app.MemoryCandidate, *app.Memory, error)
	ListMemoryCandidates(status string) []app.MemoryCandidate
	SearchMemories(query string) []app.Memory
	UpdateMemory(id, kind, content string) (app.Memory, error)
	DeleteMemory(id string) (app.Memory, error)
	PruneMemories(cutoff time.Time) []app.Memory
	AddAudit(event app.AuditEvent)
	ListAudit(sessionID string) []app.AuditEvent
	EventsAfter(sessionID, after string) []app.Event
	SaveEvalRun(run app.EvalRun)
	GetEvalRun(id string) (app.EvalRun, bool)
	ListEvalRuns() []app.EvalRun
	SaveArtifactObject(object app.ArtifactObject)
	ListArtifactObjects(limit int) []app.ArtifactObject
	// FindArtifactObjectByURI returns the newest artifact object with the
	// given URI. An empty sessionID or runID matches any session or run.
	FindArtifactObjectByURI(uri, sessionID, runID string) (app.ArtifactObject, bool)
	SaveEpisodeSummary(summary app.EpisodeSummary)
	ListEpisodeSummaries(sessionID string) []app.EpisodeSummary
}

// Compile-time checks that every backend implements the full Store interface.
var (
	_ Store = (*MemoryStore)(nil)
	_ Store = (*FileStore)(nil)
	_ Store = (*PostgresStore)(nil)
)
