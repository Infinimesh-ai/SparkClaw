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
var ErrMessageReceiveConflict = errors.New("message receive identity conflicts with the persisted record")
var ErrMessageDeliveryConflict = errors.New("delivery idempotency key was reused with a different request")
var ErrChannelInboxUpdateConflict = errors.New("channel inbox identity conflicts with the persisted update")
var ErrApprovalConflict = errors.New("approval changed or was already resolved")
var ErrApprovalNotFound = errors.New("approval not found")
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

type RunRepository interface {
	SaveRunFeedback(context.Context, app.RunFeedback) (app.RunFeedback, error)
	ListRunFeedback(context.Context, string) ([]app.RunFeedback, error)
	SaveRun(context.Context, app.AgentRun) (app.AgentRun, error)
	GetRun(context.Context, string) (app.AgentRun, bool, error)
	ListRuns(context.Context, string) ([]app.AgentRun, error)
	SaveModelCall(context.Context, app.ModelCall) (app.ModelCall, error)
	ListModelCalls(context.Context, string, string) ([]app.ModelCall, error)
	SaveToolCall(context.Context, app.ToolCall) (app.ToolCall, error)
	GetToolCall(context.Context, string) (app.ToolCall, bool, error)
	ListToolCalls(context.Context, string) ([]app.ToolCall, error)
	SaveEpisodeSummary(context.Context, app.EpisodeSummary) (app.EpisodeSummary, error)
	ListEpisodeSummaries(context.Context, string) ([]app.EpisodeSummary, error)
}

type DocumentRepository interface {
	SaveDocumentRecord(context.Context, app.DocumentRecord) (app.DocumentRecord, error)
	GetDocumentRecord(context.Context, string) (app.DocumentRecord, bool, error)
	ListDocumentRecords(context.Context, string, string, int) ([]app.DocumentRecord, error)
}

type ApprovalRepository interface {
	SaveApproval(context.Context, app.Approval) (app.Approval, error)
	GetApproval(context.Context, string) (app.Approval, bool, error)
	FindApprovalByExternalRef(context.Context, app.ApprovalSource, string) (app.Approval, bool, error)
	UpdatePendingApproval(context.Context, ApprovalUpdateCommand) (app.Approval, error)
	ResolveApproval(context.Context, string, string, string) (app.Approval, error)
	ListApprovals(context.Context, string) ([]app.Approval, error)
}

type AuditRepository interface {
	AddAudit(context.Context, app.AuditEvent) error
	ListAudit(context.Context, string) ([]app.AuditEvent, error)
	EventsAfter(context.Context, string, string) ([]app.Event, error)
}

type EvaluationRepository interface {
	SaveEvalRun(context.Context, app.EvalRun) (app.EvalRun, error)
	GetEvalRun(context.Context, string) (app.EvalRun, bool, error)
	ListEvalRuns(context.Context) ([]app.EvalRun, error)
}

type ArtifactMetadataRepository interface {
	SaveArtifactObject(context.Context, app.ArtifactObject) (app.ArtifactObject, error)
	ListArtifactObjects(context.Context, int) ([]app.ArtifactObject, error)
	// FindArtifactObjectByURI returns the newest artifact object with the
	// given URI. An empty sessionID or runID matches any session or run.
	FindArtifactObjectByURI(context.Context, string, string, string) (app.ArtifactObject, bool, error)
}

type BrowserStateRepository interface {
	SaveBrowserAuthRecord(context.Context, app.BrowserAuthRecord) (app.BrowserAuthRecord, error)
	GetBrowserAuthRecord(context.Context, string) (app.BrowserAuthRecord, bool, error)
	FindBrowserAuthRecord(context.Context, string, string, string, string, string) (app.BrowserAuthRecord, bool, error)
	ListBrowserAuthRecords(context.Context, string, string) ([]app.BrowserAuthRecord, error)
	RevokeBrowserAuthRecord(context.Context, string, string) (app.BrowserAuthRecord, error)
	SaveBrowserLoginBlock(context.Context, app.BrowserLoginBlock) (app.BrowserLoginBlock, error)
	UpdateBrowserLoginBlock(context.Context, app.BrowserLoginBlock, int64) (app.BrowserLoginBlock, error)
	GetBrowserLoginBlock(context.Context, string) (app.BrowserLoginBlock, bool, error)
	FindActiveBrowserLoginBlock(context.Context, string) (app.BrowserLoginBlock, bool, error)
	ListBrowserLoginBlocks(context.Context, string, string) ([]app.BrowserLoginBlock, error)
}

type MemoryRepository interface {
	AddMemoryCandidate(context.Context, app.MemoryCandidate) (app.MemoryCandidate, error)
	ResolveMemoryCandidate(context.Context, string, string) (app.MemoryCandidate, *app.Memory, error)
	ListMemoryCandidates(context.Context, string) ([]app.MemoryCandidate, error)
	SearchMemories(context.Context, string) ([]app.Memory, error)
	UpdateMemory(context.Context, string, string, string) (app.Memory, error)
	DeleteMemory(context.Context, string) (app.Memory, error)
	PruneMemories(context.Context, time.Time) ([]app.Memory, error)
}

type ScheduleRepository interface {
	SaveReminder(context.Context, app.Reminder) (app.Reminder, error)
	UpdatePendingReminder(context.Context, app.Reminder, time.Time) (app.Reminder, error)
	GetReminder(context.Context, string) (app.Reminder, bool, error)
	ListReminders(context.Context, app.ReminderFilter) ([]app.Reminder, error)
	ClaimDueReminders(context.Context, time.Time, time.Time, int) ([]app.Reminder, error)
	SaveReminderDelivery(context.Context, app.ReminderDelivery) (app.ReminderDelivery, error)
	ListReminderDeliveries(context.Context, string) ([]app.ReminderDelivery, error)
}

type PassiveNotificationRepository interface {
	CreatePassiveNotification(context.Context, app.PassiveNotification) (app.PassiveNotification, bool, error)
	GetPassiveNotification(context.Context, string, string) (app.PassiveNotification, bool, error)
	ListPassiveNotifications(context.Context, string, string, int) ([]app.PassiveNotification, error)
	CountUnreadPassiveNotifications(context.Context, string) (int, error)
	MarkPassiveNotificationRead(context.Context, string, string, time.Time) (app.PassiveNotification, error)
	MarkAllPassiveNotificationsRead(context.Context, string, time.Time) (int, error)
	// PrunePassiveNotifications bounds the durable inbox: notifications created
	// before cutoff are removed (a zero cutoff disables the retention sweep),
	// and each owner is trimmed to maxPerOwner records (zero or negative
	// disables the cap), evicting read notifications oldest-first before
	// touching unread ones. Pruned idempotency keys become replayable; the
	// dedup window intentionally equals the retention window.
	PrunePassiveNotifications(context.Context, time.Time, int) (int, error)
	// PassiveNotificationRevision reports a counter that increases whenever the
	// owner's inbox changes (create, mark-read, prune). It is process-local and
	// resets on restart; callers may only compare values for equality.
	PassiveNotificationRevision(context.Context, string) (uint64, error)
}

type DeliveryRecordRepository interface {
	SaveMessageReceive(context.Context, app.MessageReceiveRecord) (app.MessageReceiveRecord, error)
	GetMessageReceive(context.Context, string) (app.MessageReceiveRecord, bool, error)
	FindMessageReceive(context.Context, app.EndpointID, string) (app.MessageReceiveRecord, bool, error)
	ListMessageReceives(context.Context, string, string, int) ([]app.MessageReceiveRecord, error)
	SaveMessageDelivery(context.Context, app.MessageDeliveryRecord) (app.MessageDeliveryRecord, error)
	GetMessageDelivery(context.Context, app.DeliveryID) (app.MessageDeliveryRecord, bool, error)
	FindMessageDeliveryByIdempotency(context.Context, string, string, string) (app.MessageDeliveryRecord, bool, error)
	ListMessageDeliveries(context.Context, string, string, int) ([]app.MessageDeliveryRecord, error)
	SaveChannelInboxUpdate(context.Context, app.ChannelInboxUpdate) (app.ChannelInboxUpdate, error)
	GetChannelInboxUpdate(context.Context, string) (app.ChannelInboxUpdate, bool, error)
	FindChannelInboxUpdate(context.Context, string, string) (app.ChannelInboxUpdate, bool, error)
	ListChannelInboxUpdates(context.Context, string, string, time.Time, int) ([]app.ChannelInboxUpdate, error)
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
	RunRepository
	DocumentRepository
	ApprovalRepository
	AuditRepository
	EvaluationRepository
	ArtifactMetadataRepository
	BrowserStateRepository
	MemoryRepository
	ScheduleRepository
	PassiveNotificationRepository
	DeliveryRecordRepository
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
	SaveExternalChatSession(session app.ExternalChatSession) app.ExternalChatSession
	GetExternalChatSession(id string) (app.ExternalChatSession, bool)
	ListExternalChatSessions(channel, status string) []app.ExternalChatSession
	FindExternalChatSession(bindingID, externalChatID, externalThreadID string) (app.ExternalChatSession, bool)
	FindExternalChatSessionByLinkedSessionID(sessionID string) (app.ExternalChatSession, bool)
	SaveExternalChatMessage(message app.ExternalChatMessage) app.ExternalChatMessage
	GetExternalChatMessage(id string) (app.ExternalChatMessage, bool)
	FindExternalChatMessageByExternalID(chatSessionID, externalMessageID string) (app.ExternalChatMessage, bool)
	ListExternalChatMessages(chatSessionID string, limit int) []app.ExternalChatMessage
}

// Compile-time checks that every backend implements the full Store interface.
var (
	_ ISCPOnboardingRepository   = (*MemoryStore)(nil)
	_ ISCPOnboardingRepository   = (*FileStore)(nil)
	_ ISCPOnboardingRepository   = (*PostgresStore)(nil)
	_ OwnerRepository            = (*MemoryStore)(nil)
	_ OwnerRepository            = (*FileStore)(nil)
	_ OwnerRepository            = (*PostgresStore)(nil)
	_ ClientRepository           = (*MemoryStore)(nil)
	_ ClientRepository           = (*FileStore)(nil)
	_ ClientRepository           = (*PostgresStore)(nil)
	_ CredentialRepository       = (*MemoryStore)(nil)
	_ CredentialRepository       = (*FileStore)(nil)
	_ CredentialRepository       = (*PostgresStore)(nil)
	_ ConnectorRepository        = (*MemoryStore)(nil)
	_ ConnectorRepository        = (*FileStore)(nil)
	_ ConnectorRepository        = (*PostgresStore)(nil)
	_ ConversationRepository     = (*MemoryStore)(nil)
	_ ConversationRepository     = (*FileStore)(nil)
	_ ConversationRepository     = (*PostgresStore)(nil)
	_ RunRepository              = (*MemoryStore)(nil)
	_ RunRepository              = (*FileStore)(nil)
	_ RunRepository              = (*PostgresStore)(nil)
	_ DocumentRepository         = (*MemoryStore)(nil)
	_ DocumentRepository         = (*FileStore)(nil)
	_ DocumentRepository         = (*PostgresStore)(nil)
	_ ApprovalRepository         = (*MemoryStore)(nil)
	_ ApprovalRepository         = (*FileStore)(nil)
	_ ApprovalRepository         = (*PostgresStore)(nil)
	_ AuditRepository            = (*MemoryStore)(nil)
	_ AuditRepository            = (*FileStore)(nil)
	_ AuditRepository            = (*PostgresStore)(nil)
	_ EvaluationRepository       = (*MemoryStore)(nil)
	_ EvaluationRepository       = (*FileStore)(nil)
	_ EvaluationRepository       = (*PostgresStore)(nil)
	_ ArtifactMetadataRepository = (*MemoryStore)(nil)
	_ ArtifactMetadataRepository = (*FileStore)(nil)
	_ ArtifactMetadataRepository = (*PostgresStore)(nil)
	_ BrowserStateRepository     = (*MemoryStore)(nil)
	_ BrowserStateRepository     = (*FileStore)(nil)
	_ BrowserStateRepository     = (*PostgresStore)(nil)
	_ MemoryRepository           = (*MemoryStore)(nil)
	_ MemoryRepository           = (*FileStore)(nil)
	_ MemoryRepository           = (*PostgresStore)(nil)
	_ ScheduleRepository         = (*MemoryStore)(nil)
	_ ScheduleRepository         = (*FileStore)(nil)
	_ ScheduleRepository         = (*PostgresStore)(nil)
	_ DeliveryRecordRepository   = (*MemoryStore)(nil)
	_ DeliveryRecordRepository   = (*FileStore)(nil)
	_ DeliveryRecordRepository   = (*PostgresStore)(nil)
	_ Store                      = (*MemoryStore)(nil)
	_ Store                      = (*FileStore)(nil)
	_ Store                      = (*PostgresStore)(nil)
)
