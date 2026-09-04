package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMigratedOperationSpecsAreFiniteAndComplete(t *testing.T) {
	want := map[StoreOperation]operationSpec{
		OperationISCPOnboardingSave: {
			ID: OperationISCPOnboardingSave, Repository: "ISCPOnboardingRepository",
			Method: "SaveISCPOnboarding", Mode: operationWrite, Timeout: timeoutWrite,
		},
		OperationISCPOnboardingGet: {
			ID: OperationISCPOnboardingGet, Repository: "ISCPOnboardingRepository",
			Method: "GetISCPOnboarding", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationISCPOnboardingList: {
			ID: OperationISCPOnboardingList, Repository: "ISCPOnboardingRepository",
			Method: "ListISCPOnboardings", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationSessionCreate: {
			ID: OperationSessionCreate, Repository: "SessionRepository",
			Method: "CreateSession", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationSessionCreateWithScope: {
			ID: OperationSessionCreateWithScope, Repository: "SessionRepository",
			Method: "CreateSessionWithScope", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationSessionList: {
			ID: OperationSessionList, Repository: "SessionRepository",
			Method: "ListSessions", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationSessionGet: {
			ID: OperationSessionGet, Repository: "SessionRepository",
			Method: "GetSession", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationSessionUpdateTitle: {
			ID: OperationSessionUpdateTitle, Repository: "SessionRepository",
			Method: "UpdateSessionTitle", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationSessionDelete: {
			ID: OperationSessionDelete, Repository: "SessionRepository",
			Method: "DeleteSession", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationConversationAddMessage: {
			ID: OperationConversationAddMessage, Repository: "ConversationRepository",
			Method: "AddMessage", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationConversationListMessages: {
			ID: OperationConversationListMessages, Repository: "ConversationRepository",
			Method: "ListMessages", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationConversationListRecent: {
			ID: OperationConversationListRecent, Repository: "ConversationRepository",
			Method: "ListRecentMessages", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationConversationMessageHead: {
			ID: OperationConversationMessageHead, Repository: "ConversationRepository",
			Method: "MessageEventHead", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationConversationMessagesAfter: {
			ID: OperationConversationMessagesAfter, Repository: "ConversationRepository",
			Method: "MessageEventsAfter", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationRunFeedbackSave: {
			ID: OperationRunFeedbackSave, Repository: "RunRepository",
			Method: "SaveRunFeedback", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationRunFeedbackList: {
			ID: OperationRunFeedbackList, Repository: "RunRepository",
			Method: "ListRunFeedback", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationRunSave: {
			ID: OperationRunSave, Repository: "RunRepository",
			Method: "SaveRun", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationRunGet: {
			ID: OperationRunGet, Repository: "RunRepository",
			Method: "GetRun", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationRunList: {
			ID: OperationRunList, Repository: "RunRepository",
			Method: "ListRuns", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationModelCallSave: {
			ID: OperationModelCallSave, Repository: "RunRepository",
			Method: "SaveModelCall", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationModelCallList: {
			ID: OperationModelCallList, Repository: "RunRepository",
			Method: "ListModelCalls", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationToolCallSave: {
			ID: OperationToolCallSave, Repository: "RunRepository",
			Method: "SaveToolCall", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationToolCallGet: {
			ID: OperationToolCallGet, Repository: "RunRepository",
			Method: "GetToolCall", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationToolCallList: {
			ID: OperationToolCallList, Repository: "RunRepository",
			Method: "ListToolCalls", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationToolCallListRecent: {
			ID: OperationToolCallListRecent, Repository: "RunRepository",
			Method: "ListRecentToolCalls", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationEpisodeSummarySave: {
			ID: OperationEpisodeSummarySave, Repository: "RunRepository",
			Method: "SaveEpisodeSummary", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationEpisodeSummaryList: {
			ID: OperationEpisodeSummaryList, Repository: "RunRepository",
			Method: "ListEpisodeSummaries", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationEpisodeSummaryListRecent: {
			ID: OperationEpisodeSummaryListRecent, Repository: "RunRepository",
			Method: "ListRecentEpisodeSummaries", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationDocumentRecordSave: {
			ID: OperationDocumentRecordSave, Repository: "DocumentRepository",
			Method: "SaveDocumentRecord", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationDocumentRecordGet: {
			ID: OperationDocumentRecordGet, Repository: "DocumentRepository",
			Method: "GetDocumentRecord", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationDocumentRecordList: {
			ID: OperationDocumentRecordList, Repository: "DocumentRepository",
			Method: "ListDocumentRecords", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationApprovalSave: {
			ID: OperationApprovalSave, Repository: "ApprovalRepository",
			Method: "SaveApproval", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationApprovalGet: {
			ID: OperationApprovalGet, Repository: "ApprovalRepository",
			Method: "GetApproval", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationApprovalFindExternalRef: {
			ID: OperationApprovalFindExternalRef, Repository: "ApprovalRepository",
			Method: "FindApprovalByExternalRef", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationApprovalUpdatePending: {
			ID: OperationApprovalUpdatePending, Repository: "ApprovalRepository",
			Method: "UpdatePendingApproval", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationApprovalResolve: {
			ID: OperationApprovalResolve, Repository: "ApprovalRepository",
			Method: "ResolveApproval", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationApprovalList: {
			ID: OperationApprovalList, Repository: "ApprovalRepository",
			Method: "ListApprovals", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationAuditAdd: {
			ID: OperationAuditAdd, Repository: "AuditRepository",
			Method: "AddAudit", Mode: operationWrite, Timeout: timeoutWrite,
		},
		OperationAuditList: {
			ID: OperationAuditList, Repository: "AuditRepository",
			Method: "ListAudit", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationAuditEventsAfter: {
			ID: OperationAuditEventsAfter, Repository: "AuditRepository",
			Method: "EventsAfter", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationEvaluationSave: {
			ID: OperationEvaluationSave, Repository: "EvaluationRepository",
			Method: "SaveEvalRun", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationEvaluationGet: {
			ID: OperationEvaluationGet, Repository: "EvaluationRepository",
			Method: "GetEvalRun", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationEvaluationList: {
			ID: OperationEvaluationList, Repository: "EvaluationRepository",
			Method: "ListEvalRuns", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationArtifactMetadataSave: {
			ID: OperationArtifactMetadataSave, Repository: "ArtifactMetadataRepository",
			Method: "SaveArtifactObject", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationArtifactMetadataList: {
			ID: OperationArtifactMetadataList, Repository: "ArtifactMetadataRepository",
			Method: "ListArtifactObjects", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationArtifactMetadataFindByURI: {
			ID: OperationArtifactMetadataFindByURI, Repository: "ArtifactMetadataRepository",
			Method: "FindArtifactObjectByURI", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationBrowserAuthSave: {
			ID: OperationBrowserAuthSave, Repository: "BrowserStateRepository",
			Method: "SaveBrowserAuthRecord", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationBrowserAuthGet: {
			ID: OperationBrowserAuthGet, Repository: "BrowserStateRepository",
			Method: "GetBrowserAuthRecord", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationBrowserAuthFind: {
			ID: OperationBrowserAuthFind, Repository: "BrowserStateRepository",
			Method: "FindBrowserAuthRecord", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationBrowserAuthList: {
			ID: OperationBrowserAuthList, Repository: "BrowserStateRepository",
			Method: "ListBrowserAuthRecords", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationBrowserAuthRevoke: {
			ID: OperationBrowserAuthRevoke, Repository: "BrowserStateRepository",
			Method: "RevokeBrowserAuthRecord", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationBrowserLoginBlockSave: {
			ID: OperationBrowserLoginBlockSave, Repository: "BrowserStateRepository",
			Method: "SaveBrowserLoginBlock", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationBrowserLoginBlockUpdate: {
			ID: OperationBrowserLoginBlockUpdate, Repository: "BrowserStateRepository",
			Method: "UpdateBrowserLoginBlock", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationBrowserLoginBlockGet: {
			ID: OperationBrowserLoginBlockGet, Repository: "BrowserStateRepository",
			Method: "GetBrowserLoginBlock", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationBrowserLoginBlockFindActive: {
			ID: OperationBrowserLoginBlockFindActive, Repository: "BrowserStateRepository",
			Method: "FindActiveBrowserLoginBlock", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationBrowserLoginBlockList: {
			ID: OperationBrowserLoginBlockList, Repository: "BrowserStateRepository",
			Method: "ListBrowserLoginBlocks", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationOwnerProfileGet: {
			ID: OperationOwnerProfileGet, Repository: "OwnerRepository",
			Method: "GetOwnerProfile", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationOwnerProfileUpdate: {
			ID: OperationOwnerProfileUpdate, Repository: "OwnerRepository",
			Method: "UpdateOwnerProfile", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationOwnerProfileGetByID: {
			ID: OperationOwnerProfileGetByID, Repository: "OwnerRepository",
			Method: "GetOwnerProfileByID", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationOwnerProfileSave: {
			ID: OperationOwnerProfileSave, Repository: "OwnerRepository",
			Method: "SaveOwnerProfile", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationOwnerProfileList: {
			ID: OperationOwnerProfileList, Repository: "OwnerRepository",
			Method: "ListOwnerProfiles", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationOwnerProfileFindExternalRef: {
			ID: OperationOwnerProfileFindExternalRef, Repository: "OwnerRepository",
			Method: "FindOwnerProfileByExternalRef", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationClientGet: {
			ID: OperationClientGet, Repository: "ClientRepository",
			Method: "GetClient", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationClientList: {
			ID: OperationClientList, Repository: "ClientRepository",
			Method: "ListClients", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationClientRevoke: {
			ID: OperationClientRevoke, Repository: "ClientRepository",
			Method: "RevokeClient", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationClientFindTokenHash: {
			ID: OperationClientFindTokenHash, Repository: "ClientRepository",
			Method: "FindClientByTokenHash", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationClientTouch: {
			ID: OperationClientTouch, Repository: "ClientRepository",
			Method: "TouchClient", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationPairingCodeSave: {
			ID: OperationPairingCodeSave, Repository: "ClientRepository",
			Method: "SavePairingCode", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationPairingCodeGet: {
			ID: OperationPairingCodeGet, Repository: "ClientRepository",
			Method: "GetPairingCode", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationPairingCodeClaim: {
			ID: OperationPairingCodeClaim, Repository: "ClientRepository",
			Method: "ClaimPairingCode", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationCredentialSecretSave: {
			ID: OperationCredentialSecretSave, Repository: "CredentialRepository",
			Method: "SaveCredentialSecret", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationCredentialSecretGet: {
			ID: OperationCredentialSecretGet, Repository: "CredentialRepository",
			Method: "GetCredentialSecret", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationCredentialSecretDelete: {
			ID: OperationCredentialSecretDelete, Repository: "CredentialRepository",
			Method: "DeleteCredentialSecret", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationConnectorSettingGet: {
			ID: OperationConnectorSettingGet, Repository: "ConnectorRepository",
			Method: "GetConnectorSetting", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationConnectorSettingList: {
			ID: OperationConnectorSettingList, Repository: "ConnectorRepository",
			Method: "ListConnectorSettings", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationConnectorSettingListAll: {
			ID: OperationConnectorSettingListAll, Repository: "ConnectorRepository",
			Method: "ListAllConnectorSettings", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationConnectorSettingUpdate: {
			ID: OperationConnectorSettingUpdate, Repository: "ConnectorRepository",
			Method: "UpdateConnectorSetting", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationEmailProviderSettingGet: {
			ID: OperationEmailProviderSettingGet, Repository: "ConnectorRepository",
			Method: "GetEmailProviderSetting", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationEmailProviderSettingList: {
			ID: OperationEmailProviderSettingList, Repository: "ConnectorRepository",
			Method: "ListEmailProviderSettings", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationEmailProviderSettingUpdate: {
			ID: OperationEmailProviderSettingUpdate, Repository: "ConnectorRepository",
			Method: "UpdateEmailProviderSetting", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationNotificationBindingCreate: {
			ID: OperationNotificationBindingCreate, Repository: "ConnectorRepository",
			Method: "CreateNotificationBinding", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationNotificationBindingGet: {
			ID: OperationNotificationBindingGet, Repository: "ConnectorRepository",
			Method: "GetNotificationBinding", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationNotificationBindingList: {
			ID: OperationNotificationBindingList, Repository: "ConnectorRepository",
			Method: "ListNotificationBindings", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationNotificationBindingUpdate: {
			ID: OperationNotificationBindingUpdate, Repository: "ConnectorRepository",
			Method: "UpdateNotificationBinding", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationMemoryCandidateAdd: {
			ID: OperationMemoryCandidateAdd, Repository: "MemoryRepository",
			Method: "AddMemoryCandidate", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationMemoryCandidateResolve: {
			ID: OperationMemoryCandidateResolve, Repository: "MemoryRepository",
			Method: "ResolveMemoryCandidate", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationMemoryCandidateList: {
			ID: OperationMemoryCandidateList, Repository: "MemoryRepository",
			Method: "ListMemoryCandidates", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationMemorySearch: {
			ID: OperationMemorySearch, Repository: "MemoryRepository",
			Method: "SearchMemories", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationMemoryUpdate: {
			ID: OperationMemoryUpdate, Repository: "MemoryRepository",
			Method: "UpdateMemory", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationMemoryDelete: {
			ID: OperationMemoryDelete, Repository: "MemoryRepository",
			Method: "DeleteMemory", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationMemoryPrune: {
			ID: OperationMemoryPrune, Repository: "MemoryRepository",
			Method: "PruneMemories", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationReminderSave: {
			ID: OperationReminderSave, Repository: "ScheduleRepository",
			Method: "SaveReminder", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationReminderUpdatePending: {
			ID: OperationReminderUpdatePending, Repository: "ScheduleRepository",
			Method: "UpdatePendingReminder", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationReminderGet: {
			ID: OperationReminderGet, Repository: "ScheduleRepository",
			Method: "GetReminder", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationReminderList: {
			ID: OperationReminderList, Repository: "ScheduleRepository",
			Method: "ListReminders", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationReminderClaimDue: {
			ID: OperationReminderClaimDue, Repository: "ScheduleRepository",
			Method: "ClaimDueReminders", Mode: operationWrite, Timeout: timeoutWrite,
		},
		OperationReminderDeliverySave: {
			ID: OperationReminderDeliverySave, Repository: "ScheduleRepository",
			Method: "SaveReminderDelivery", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationReminderDeliveryList: {
			ID: OperationReminderDeliveryList, Repository: "ScheduleRepository",
			Method: "ListReminderDeliveries", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationPassiveNotificationCreate: {
			ID: OperationPassiveNotificationCreate, Repository: "PassiveNotificationRepository",
			Method: "CreatePassiveNotification", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationPassiveNotificationGet: {
			ID: OperationPassiveNotificationGet, Repository: "PassiveNotificationRepository",
			Method: "GetPassiveNotification", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationPassiveNotificationList: {
			ID: OperationPassiveNotificationList, Repository: "PassiveNotificationRepository",
			Method: "ListPassiveNotifications", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationPassiveNotificationCount: {
			ID: OperationPassiveNotificationCount, Repository: "PassiveNotificationRepository",
			Method: "CountUnreadPassiveNotifications", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationPassiveNotificationMarkRead: {
			ID: OperationPassiveNotificationMarkRead, Repository: "PassiveNotificationRepository",
			Method: "MarkPassiveNotificationRead", Mode: operationWrite, Timeout: timeoutWrite,
		},
		OperationPassiveNotificationMarkAll: {
			ID: OperationPassiveNotificationMarkAll, Repository: "PassiveNotificationRepository",
			Method: "MarkAllPassiveNotificationsRead", Mode: operationWrite, Timeout: timeoutWrite,
		},
		OperationPassiveNotificationPrune: {
			ID: OperationPassiveNotificationPrune, Repository: "PassiveNotificationRepository",
			Method: "PrunePassiveNotifications", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationPassiveNotificationRevision: {
			ID: OperationPassiveNotificationRevision, Repository: "PassiveNotificationRepository",
			Method: "PassiveNotificationRevision", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationMessageReceiveSave: {
			ID: OperationMessageReceiveSave, Repository: "DeliveryRecordRepository",
			Method: "SaveMessageReceive", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationMessageReceiveGet: {
			ID: OperationMessageReceiveGet, Repository: "DeliveryRecordRepository",
			Method: "GetMessageReceive", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationMessageReceiveFind: {
			ID: OperationMessageReceiveFind, Repository: "DeliveryRecordRepository",
			Method: "FindMessageReceive", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationMessageReceiveList: {
			ID: OperationMessageReceiveList, Repository: "DeliveryRecordRepository",
			Method: "ListMessageReceives", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationMessageDeliverySave: {
			ID: OperationMessageDeliverySave, Repository: "DeliveryRecordRepository",
			Method: "SaveMessageDelivery", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationMessageDeliveryGet: {
			ID: OperationMessageDeliveryGet, Repository: "DeliveryRecordRepository",
			Method: "GetMessageDelivery", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationMessageDeliveryFind: {
			ID: OperationMessageDeliveryFind, Repository: "DeliveryRecordRepository",
			Method: "FindMessageDeliveryByIdempotency", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationMessageDeliveryList: {
			ID: OperationMessageDeliveryList, Repository: "DeliveryRecordRepository",
			Method: "ListMessageDeliveries", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationChannelInboxUpdateSave: {
			ID: OperationChannelInboxUpdateSave, Repository: "DeliveryRecordRepository",
			Method: "SaveChannelInboxUpdate", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationChannelInboxUpdateGet: {
			ID: OperationChannelInboxUpdateGet, Repository: "DeliveryRecordRepository",
			Method: "GetChannelInboxUpdate", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationChannelInboxUpdateFind: {
			ID: OperationChannelInboxUpdateFind, Repository: "DeliveryRecordRepository",
			Method: "FindChannelInboxUpdate", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationChannelInboxUpdateList: {
			ID: OperationChannelInboxUpdateList, Repository: "DeliveryRecordRepository",
			Method: "ListChannelInboxUpdates", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationExternalChatSessionSave: {
			ID: OperationExternalChatSessionSave, Repository: "ExternalChatRepository",
			Method: "SaveExternalChatSession", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationExternalChatSessionGet: {
			ID: OperationExternalChatSessionGet, Repository: "ExternalChatRepository",
			Method: "GetExternalChatSession", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationExternalChatSessionList: {
			ID: OperationExternalChatSessionList, Repository: "ExternalChatRepository",
			Method: "ListExternalChatSessions", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationExternalChatSessionFind: {
			ID: OperationExternalChatSessionFind, Repository: "ExternalChatRepository",
			Method: "FindExternalChatSession", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationExternalChatSessionFindLink: {
			ID: OperationExternalChatSessionFindLink, Repository: "ExternalChatRepository",
			Method: "FindExternalChatSessionByLinkedSessionID", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationExternalChatMessageSave: {
			ID: OperationExternalChatMessageSave, Repository: "ExternalChatRepository",
			Method: "SaveExternalChatMessage", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationExternalChatMessageGet: {
			ID: OperationExternalChatMessageGet, Repository: "ExternalChatRepository",
			Method: "GetExternalChatMessage", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationExternalChatMessageFind: {
			ID: OperationExternalChatMessageFind, Repository: "ExternalChatRepository",
			Method: "FindExternalChatMessageByExternalID", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationExternalChatMessageList: {
			ID: OperationExternalChatMessageList, Repository: "ExternalChatRepository",
			Method: "ListExternalChatMessages", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationMCPAccessTicketSave:         {ID: OperationMCPAccessTicketSave, Repository: "MCPRepository", Method: "SaveMCPAccessTicket", Mode: operationWrite, Timeout: timeoutTransaction},
		OperationMCPAccessTicketGet:          {ID: OperationMCPAccessTicketGet, Repository: "MCPRepository", Method: "GetMCPAccessTicket", Mode: operationRead, Timeout: timeoutRead},
		OperationMCPAccessTicketFindHash:     {ID: OperationMCPAccessTicketFindHash, Repository: "MCPRepository", Method: "FindMCPAccessTicketBySecretHash", Mode: operationRead, Timeout: timeoutRead},
		OperationMCPAccessTicketList:         {ID: OperationMCPAccessTicketList, Repository: "MCPRepository", Method: "ListMCPAccessTickets", Mode: operationRead, Timeout: timeoutRead},
		OperationMCPAccessTicketRedeem:       {ID: OperationMCPAccessTicketRedeem, Repository: "MCPRepository", Method: "RedeemMCPAccessTicket", Mode: operationWrite, Timeout: timeoutTransaction},
		OperationMCPAccessTicketRevoke:       {ID: OperationMCPAccessTicketRevoke, Repository: "MCPRepository", Method: "RevokeMCPAccessTicket", Mode: operationWrite, Timeout: timeoutTransaction},
		OperationMCPAccessTicketDelete:       {ID: OperationMCPAccessTicketDelete, Repository: "MCPRepository", Method: "DeleteMCPAccessTicket", Mode: operationWrite, Timeout: timeoutTransaction},
		OperationMCPBindingGet:               {ID: OperationMCPBindingGet, Repository: "MCPRepository", Method: "GetMCPBinding", Mode: operationRead, Timeout: timeoutRead},
		OperationMCPBindingFindPeer:          {ID: OperationMCPBindingFindPeer, Repository: "MCPRepository", Method: "FindMCPBindingForPeer", Mode: operationRead, Timeout: timeoutRead},
		OperationMCPBindingList:              {ID: OperationMCPBindingList, Repository: "MCPRepository", Method: "ListMCPBindings", Mode: operationRead, Timeout: timeoutRead},
		OperationMCPBindingRevoke:            {ID: OperationMCPBindingRevoke, Repository: "MCPRepository", Method: "RevokeMCPBinding", Mode: operationWrite, Timeout: timeoutTransaction},
		OperationMCPBindingDelete:            {ID: OperationMCPBindingDelete, Repository: "MCPRepository", Method: "DeleteMCPBinding", Mode: operationWrite, Timeout: timeoutTransaction},
		OperationMCPAccessRecordsDelete:      {ID: OperationMCPAccessRecordsDelete, Repository: "MCPRepository", Method: "DeleteMCPAccessRecords", Mode: operationWrite, Timeout: timeoutTransaction},
		OperationMCPBindingTouch:             {ID: OperationMCPBindingTouch, Repository: "MCPRepository", Method: "TouchMCPBinding", Mode: operationWrite, Timeout: timeoutTransaction},
		OperationMCPOperationCreate:          {ID: OperationMCPOperationCreate, Repository: "MCPRepository", Method: "CreateMCPOperation", Mode: operationWrite, Timeout: timeoutTransaction},
		OperationMCPOperationGet:             {ID: OperationMCPOperationGet, Repository: "MCPRepository", Method: "GetMCPOperation", Mode: operationRead, Timeout: timeoutRead},
		OperationMCPOperationFindIdempotency: {ID: OperationMCPOperationFindIdempotency, Repository: "MCPRepository", Method: "FindMCPOperationByIdempotency", Mode: operationRead, Timeout: timeoutRead},
		OperationMCPOperationList:            {ID: OperationMCPOperationList, Repository: "MCPRepository", Method: "ListMCPOperations", Mode: operationRead, Timeout: timeoutRead},
		OperationMCPOperationUpdate:          {ID: OperationMCPOperationUpdate, Repository: "MCPRepository", Method: "UpdateMCPOperation", Mode: operationWrite, Timeout: timeoutTransaction},
	}
	if len(operationSpecs) != len(want) {
		t.Fatalf("operation spec count = %d, want %d", len(operationSpecs), len(want))
	}
	methods := map[string]struct{}{}
	for id, expected := range want {
		got, exists := operationSpecs[id]
		if !exists || got != expected {
			t.Errorf("operation %s = %#v, want %#v", id, got, expected)
		}
		if _, duplicate := methods[got.Method]; duplicate {
			t.Errorf("pilot method %s has duplicate operation specs", got.Method)
		}
		methods[got.Method] = struct{}{}
		if got.Timeout != timeoutRead && got.Timeout != timeoutWrite && got.Timeout != timeoutTransaction {
			t.Errorf("operation %s has unknown timeout class %q", id, got.Timeout)
		}
	}
}

func TestOperationContextUsesEarlierDeadlineAndTypedErrors(t *testing.T) {
	caller, cancelCaller := context.WithTimeout(context.Background(), time.Second)
	defer cancelCaller()
	effective, cancelEffective := operationContext(caller, OperationISCPOnboardingGet, OperationTimeouts{Read: time.Minute, Write: time.Minute})
	defer cancelEffective()
	callerDeadline, _ := caller.Deadline()
	effectiveDeadline, _ := effective.Deadline()
	if !effectiveDeadline.Equal(callerDeadline) {
		t.Fatalf("effective deadline = %s, want caller deadline %s", effectiveDeadline, callerDeadline)
	}
	fallbackStart := time.Now()
	fallback, cancelFallback := operationContext(context.Background(), OperationISCPOnboardingSave, OperationTimeouts{Read: time.Second, Write: 2 * time.Second})
	defer cancelFallback()
	fallbackDeadline, exists := fallback.Deadline()
	if !exists || fallbackDeadline.Before(fallbackStart.Add(1900*time.Millisecond)) || fallbackDeadline.After(fallbackStart.Add(2100*time.Millisecond)) {
		t.Fatalf("write fallback deadline = %s", fallbackDeadline)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	err := operationContextError(OperationISCPOnboardingGet, canceled)
	if StoreErrorCodeOf(err) != StoreErrorCanceled || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled classification = %v code=%q", err, StoreErrorCodeOf(err))
	}
	typed := &StoreError{Code: StoreErrorConflict, Operation: OperationISCPOnboardingSave, Err: ErrISCPOnboardingConflict}
	if !errors.Is(typed, ErrISCPOnboardingConflict) || StoreErrorCodeOf(typed) != StoreErrorConflict {
		t.Fatalf("StoreError did not preserve cause/code: %v", typed)
	}
}
