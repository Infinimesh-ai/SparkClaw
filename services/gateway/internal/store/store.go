package store

import (
	"context"
	"errors"
	"strings"
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
var ErrMessageEventCursorInvalid = errors.New("message event cursor is not valid for this session")

const MessageEventPageLimit = 100

type MessageEventPage struct {
	Events     []app.Event
	NextCursor string
	HasMore    bool
}

type ISCPOnboardingRepository interface {
	SaveISCPOnboarding(context.Context, app.ISCPOnboarding) (app.ISCPOnboarding, error)
	GetISCPOnboarding(context.Context, string) (app.ISCPOnboarding, bool, error)
	ListISCPOnboardings(context.Context, string) ([]app.ISCPOnboarding, error)
}

type OwnerRepository interface {
	GetOwnerProfile(context.Context) (app.OwnerProfile, error)
	UpdateOwnerProfile(context.Context, app.OwnerProfile) (app.OwnerProfile, error)
	GetOwnerProfileByID(context.Context, string) (app.OwnerProfile, bool, error)
	SaveOwnerProfile(context.Context, app.OwnerProfile) (app.OwnerProfile, error)
	ListOwnerProfiles(context.Context) ([]app.OwnerProfile, error)
	FindOwnerProfileByExternalRef(context.Context, string, string) (app.OwnerProfile, bool, error)
}

type ClientRepository interface {
	GetClient(context.Context, string) (app.Client, bool, error)
	ListClients(context.Context) ([]app.Client, error)
	RevokeClient(context.Context, string) (app.Client, error)
	FindClientByTokenHash(context.Context, string) (app.Client, bool, error)
	TouchClient(context.Context, string) (app.Client, bool, error)
	SavePairingCode(context.Context, app.PairingCode) (app.PairingCode, error)
	GetPairingCode(context.Context, string) (app.PairingCode, bool, error)
	ClaimPairingCode(context.Context, string, app.Client) (app.PairingCode, app.Client, error)
}

type CredentialRepository interface {
	SaveCredentialSecret(context.Context, CredentialSaveCommand) (app.CredentialSecret, error)
	GetCredentialSecret(context.Context, string) (app.CredentialSecret, bool, error)
	DeleteCredentialSecret(context.Context, CredentialDeleteCondition) (app.CredentialSecret, error)
}

type ConnectorRepository interface {
	GetConnectorSetting(context.Context, string, string) (app.ConnectorSetting, bool, error)
	ListConnectorSettings(context.Context, string) ([]app.ConnectorSetting, error)
	ListAllConnectorSettings(context.Context) ([]app.ConnectorSetting, error)
	UpdateConnectorSetting(context.Context, app.ConnectorSetting, int64) (app.ConnectorSetting, error)
	CreateNotificationBinding(context.Context, app.NotificationBinding) (app.NotificationBinding, error)
	GetNotificationBinding(context.Context, string) (app.NotificationBinding, bool, error)
	ListNotificationBindings(context.Context, string, string) ([]app.NotificationBinding, error)
	UpdateNotificationBinding(context.Context, NotificationBindingUpdateCommand) (app.NotificationBinding, error)
}

type SessionRepository interface {
	CreateSession(context.Context, string) (app.Session, error)
	CreateSessionWithScope(context.Context, string, string, string, string, bool) (app.Session, error)
	ListSessions(context.Context) ([]app.Session, error)
	GetSession(context.Context, string) (app.Session, bool, error)
	UpdateSessionTitle(context.Context, string, string) (app.Session, error)
	DeleteSession(context.Context, string) (app.Session, error)
}

type ConversationRepository interface {
	AddMessage(context.Context, app.Message) (app.Message, error)
	ListMessages(context.Context, string) ([]app.Message, error)
	MessageEventHead(context.Context, string) (string, error)
	MessageEventsAfter(context.Context, string, string, int) (MessageEventPage, error)
}

func ReconcileOwnerProfileWrite(ctx context.Context, repository OwnerRepository, candidate app.OwnerProfile, writeErr error) (app.OwnerProfile, error) {
	if writeErr == nil {
		return candidate, nil
	}
	if StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome || strings.TrimSpace(candidate.ID) == "" {
		return app.OwnerProfile{}, writeErr
	}
	profile, found, err := repository.GetOwnerProfileByID(ctx, candidate.ID)
	if err != nil {
		return app.OwnerProfile{}, errors.Join(writeErr, err)
	}
	if found && OwnerProfilesEqual(profile, candidate) {
		return profile, nil
	}
	return app.OwnerProfile{}, writeErr
}

type Store interface {
	ISCPOnboardingRepository
	OwnerRepository
	ClientRepository
	CredentialRepository
	ConnectorRepository
	SessionRepository
	ConversationRepository
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
	_ ISCPOnboardingRepository = (*MemoryStore)(nil)
	_ ISCPOnboardingRepository = (*FileStore)(nil)
	_ ISCPOnboardingRepository = (*PostgresStore)(nil)
	_ OwnerRepository          = (*MemoryStore)(nil)
	_ OwnerRepository          = (*FileStore)(nil)
	_ OwnerRepository          = (*PostgresStore)(nil)
	_ ClientRepository         = (*MemoryStore)(nil)
	_ ClientRepository         = (*FileStore)(nil)
	_ ClientRepository         = (*PostgresStore)(nil)
	_ CredentialRepository     = (*MemoryStore)(nil)
	_ CredentialRepository     = (*FileStore)(nil)
	_ CredentialRepository     = (*PostgresStore)(nil)
	_ ConnectorRepository      = (*MemoryStore)(nil)
	_ ConnectorRepository      = (*FileStore)(nil)
	_ ConnectorRepository      = (*PostgresStore)(nil)
	_ ConversationRepository   = (*MemoryStore)(nil)
	_ ConversationRepository   = (*FileStore)(nil)
	_ ConversationRepository   = (*PostgresStore)(nil)
	_ Store                    = (*MemoryStore)(nil)
	_ Store                    = (*FileStore)(nil)
	_ Store                    = (*PostgresStore)(nil)
)
