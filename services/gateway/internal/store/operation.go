package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type StoreErrorCode string

const (
	StoreErrorNotFound       StoreErrorCode = "not_found"
	StoreErrorConflict       StoreErrorCode = "conflict"
	StoreErrorInvalid        StoreErrorCode = "invalid"
	StoreErrorCanceled       StoreErrorCode = "canceled"
	StoreErrorTimeout        StoreErrorCode = "timeout"
	StoreErrorUnavailable    StoreErrorCode = "unavailable"
	StoreErrorDurability     StoreErrorCode = "durability_failed"
	StoreErrorUnknownOutcome StoreErrorCode = "unknown_outcome"
	StoreErrorCorrupt        StoreErrorCode = "corrupt"
	StoreErrorInternal       StoreErrorCode = "internal"
)

type StoreOperation string

const (
	OperationISCPOnboardingSave          StoreOperation = "iscp_onboarding.save"
	OperationISCPOnboardingGet           StoreOperation = "iscp_onboarding.get"
	OperationISCPOnboardingList          StoreOperation = "iscp_onboarding.list"
	OperationSessionCreate               StoreOperation = "session.create"
	OperationSessionCreateWithScope      StoreOperation = "session.create_with_scope"
	OperationSessionList                 StoreOperation = "session.list"
	OperationSessionGet                  StoreOperation = "session.get"
	OperationSessionUpdateTitle          StoreOperation = "session.update_title"
	OperationSessionDelete               StoreOperation = "session.delete"
	OperationConversationAddMessage      StoreOperation = "conversation.add_message"
	OperationConversationListMessages    StoreOperation = "conversation.list_messages"
	OperationConversationMessageHead     StoreOperation = "conversation.message_event_head"
	OperationConversationMessagesAfter   StoreOperation = "conversation.message_events_after"
	OperationRunFeedbackSave             StoreOperation = "run_feedback.save"
	OperationRunFeedbackList             StoreOperation = "run_feedback.list"
	OperationRunSave                     StoreOperation = "run.save"
	OperationRunGet                      StoreOperation = "run.get"
	OperationRunList                     StoreOperation = "run.list"
	OperationModelCallSave               StoreOperation = "model_call.save"
	OperationModelCallList               StoreOperation = "model_call.list"
	OperationToolCallSave                StoreOperation = "tool_call.save"
	OperationToolCallGet                 StoreOperation = "tool_call.get"
	OperationToolCallList                StoreOperation = "tool_call.list"
	OperationEpisodeSummarySave          StoreOperation = "episode_summary.save"
	OperationEpisodeSummaryList          StoreOperation = "episode_summary.list"
	OperationDocumentRecordSave          StoreOperation = "document_record.save"
	OperationDocumentRecordGet           StoreOperation = "document_record.get"
	OperationDocumentRecordList          StoreOperation = "document_record.list"
	OperationApprovalSave                StoreOperation = "approval.save"
	OperationApprovalGet                 StoreOperation = "approval.get"
	OperationApprovalFindExternalRef     StoreOperation = "approval.find_external_ref"
	OperationApprovalUpdatePending       StoreOperation = "approval.update_pending"
	OperationApprovalResolve             StoreOperation = "approval.resolve"
	OperationApprovalList                StoreOperation = "approval.list"
	OperationAuditAdd                    StoreOperation = "audit.add"
	OperationAuditList                   StoreOperation = "audit.list"
	OperationAuditEventsAfter            StoreOperation = "audit.events_after"
	OperationEvaluationSave              StoreOperation = "evaluation.save"
	OperationEvaluationGet               StoreOperation = "evaluation.get"
	OperationEvaluationList              StoreOperation = "evaluation.list"
	OperationArtifactMetadataSave        StoreOperation = "artifact_metadata.save"
	OperationArtifactMetadataList        StoreOperation = "artifact_metadata.list"
	OperationArtifactMetadataFindByURI   StoreOperation = "artifact_metadata.find_by_uri"
	OperationBrowserAuthSave             StoreOperation = "browser_auth.save"
	OperationBrowserAuthGet              StoreOperation = "browser_auth.get"
	OperationBrowserAuthFind             StoreOperation = "browser_auth.find"
	OperationBrowserAuthList             StoreOperation = "browser_auth.list"
	OperationBrowserAuthRevoke           StoreOperation = "browser_auth.revoke"
	OperationBrowserLoginBlockSave       StoreOperation = "browser_login_block.save"
	OperationBrowserLoginBlockUpdate     StoreOperation = "browser_login_block.update"
	OperationBrowserLoginBlockGet        StoreOperation = "browser_login_block.get"
	OperationBrowserLoginBlockFindActive StoreOperation = "browser_login_block.find_active"
	OperationBrowserLoginBlockList       StoreOperation = "browser_login_block.list"
	OperationMemoryCandidateAdd          StoreOperation = "memory_candidate.add"
	OperationMemoryCandidateResolve      StoreOperation = "memory_candidate.resolve"
	OperationMemoryCandidateList         StoreOperation = "memory_candidate.list"
	OperationMemorySearch                StoreOperation = "memory.search"
	OperationMemoryUpdate                StoreOperation = "memory.update"
	OperationMemoryDelete                StoreOperation = "memory.delete"
	OperationMemoryPrune                 StoreOperation = "memory.prune"
	OperationReminderSave                StoreOperation = "reminder.save"
	OperationReminderUpdatePending       StoreOperation = "reminder.update_pending"
	OperationReminderGet                 StoreOperation = "reminder.get"
	OperationReminderList                StoreOperation = "reminder.list"
	OperationReminderClaimDue            StoreOperation = "reminder.claim_due"
	OperationReminderDeliverySave        StoreOperation = "reminder_delivery.save"
	OperationReminderDeliveryList        StoreOperation = "reminder_delivery.list"
	OperationPassiveNotificationCreate   StoreOperation = "passive_notification.create"
	OperationPassiveNotificationGet      StoreOperation = "passive_notification.get"
	OperationPassiveNotificationList     StoreOperation = "passive_notification.list"
	OperationPassiveNotificationCount    StoreOperation = "passive_notification.count_unread"
	OperationPassiveNotificationMarkRead StoreOperation = "passive_notification.mark_read"
	OperationPassiveNotificationMarkAll  StoreOperation = "passive_notification.mark_all_read"
	OperationPassiveNotificationPrune    StoreOperation = "passive_notification.prune"
	OperationPassiveNotificationRevision StoreOperation = "passive_notification.revision"
	OperationMessageReceiveSave          StoreOperation = "message_receive.save"
	OperationMessageReceiveGet           StoreOperation = "message_receive.get"
	OperationMessageReceiveFind          StoreOperation = "message_receive.find"
	OperationMessageReceiveList          StoreOperation = "message_receive.list"
	OperationMessageDeliverySave         StoreOperation = "message_delivery.save"
	OperationMessageDeliveryGet          StoreOperation = "message_delivery.get"
	OperationMessageDeliveryFind         StoreOperation = "message_delivery.find_idempotency"
	OperationMessageDeliveryList         StoreOperation = "message_delivery.list"
	OperationChannelInboxUpdateSave      StoreOperation = "channel_inbox_update.save"
	OperationChannelInboxUpdateGet       StoreOperation = "channel_inbox_update.get"
	OperationChannelInboxUpdateFind      StoreOperation = "channel_inbox_update.find"
	OperationChannelInboxUpdateList      StoreOperation = "channel_inbox_update.list"
	OperationExternalChatSessionSave     StoreOperation = "external_chat_session.save"
	OperationExternalChatSessionGet      StoreOperation = "external_chat_session.get"
	OperationExternalChatSessionList     StoreOperation = "external_chat_session.list"
	OperationExternalChatSessionFind     StoreOperation = "external_chat_session.find"
	OperationExternalChatSessionFindLink StoreOperation = "external_chat_session.find_linked_session"
	OperationExternalChatMessageSave     StoreOperation = "external_chat_message.save"
	OperationExternalChatMessageGet      StoreOperation = "external_chat_message.get"
	OperationExternalChatMessageFind     StoreOperation = "external_chat_message.find_external_id"
	OperationExternalChatMessageList     StoreOperation = "external_chat_message.list"
	OperationMCPAccessTicketSave         StoreOperation = "mcp_access_ticket.save"
	OperationMCPAccessTicketGet          StoreOperation = "mcp_access_ticket.get"
	OperationMCPAccessTicketFindHash     StoreOperation = "mcp_access_ticket.find_secret_hash"
	OperationMCPAccessTicketList         StoreOperation = "mcp_access_ticket.list"
	OperationMCPAccessTicketRedeem       StoreOperation = "mcp_access_ticket.redeem"
	OperationMCPAccessTicketRevoke       StoreOperation = "mcp_access_ticket.revoke"
	OperationMCPAccessTicketDelete       StoreOperation = "mcp_access_ticket.delete"
	OperationMCPBindingGet               StoreOperation = "mcp_binding.get"
	OperationMCPBindingFindPeer          StoreOperation = "mcp_binding.find_peer"
	OperationMCPBindingList              StoreOperation = "mcp_binding.list"
	OperationMCPBindingRevoke            StoreOperation = "mcp_binding.revoke"
	OperationMCPBindingDelete            StoreOperation = "mcp_binding.delete"
	OperationMCPAccessRecordsDelete      StoreOperation = "mcp_access_records.delete"
	OperationMCPBindingTouch             StoreOperation = "mcp_binding.touch"
	OperationMCPOperationCreate          StoreOperation = "mcp_operation.create"
	OperationMCPOperationGet             StoreOperation = "mcp_operation.get"
	OperationMCPOperationFindIdempotency StoreOperation = "mcp_operation.find_idempotency"
	OperationMCPOperationList            StoreOperation = "mcp_operation.list"
	OperationMCPOperationUpdate          StoreOperation = "mcp_operation.update"
	OperationOwnerProfileGet             StoreOperation = "owner_profile.get"
	OperationOwnerProfileUpdate          StoreOperation = "owner_profile.update"
	OperationOwnerProfileGetByID         StoreOperation = "owner_profile.get_by_id"
	OperationOwnerProfileSave            StoreOperation = "owner_profile.save"
	OperationOwnerProfileList            StoreOperation = "owner_profile.list"
	OperationOwnerProfileFindExternalRef StoreOperation = "owner_profile.find_external_ref"
	OperationClientGet                   StoreOperation = "client.get"
	OperationClientList                  StoreOperation = "client.list"
	OperationClientRevoke                StoreOperation = "client.revoke"
	OperationClientFindTokenHash         StoreOperation = "client.find_token_hash"
	OperationClientTouch                 StoreOperation = "client.touch"
	OperationPairingCodeSave             StoreOperation = "pairing_code.save"
	OperationPairingCodeGet              StoreOperation = "pairing_code.get"
	OperationPairingCodeClaim            StoreOperation = "pairing_code.claim"
	OperationCredentialSecretSave        StoreOperation = "credential_secret.save"
	OperationCredentialSecretGet         StoreOperation = "credential_secret.get"
	OperationCredentialSecretDelete      StoreOperation = "credential_secret.delete"
	OperationConnectorSettingGet         StoreOperation = "connector_setting.get"
	OperationConnectorSettingList        StoreOperation = "connector_setting.list"
	OperationConnectorSettingListAll     StoreOperation = "connector_setting.list_all"
	OperationConnectorSettingUpdate      StoreOperation = "connector_setting.update"
	OperationNotificationBindingCreate   StoreOperation = "notification_binding.create"
	OperationNotificationBindingGet      StoreOperation = "notification_binding.get"
	OperationNotificationBindingList     StoreOperation = "notification_binding.list"
	OperationNotificationBindingUpdate   StoreOperation = "notification_binding.update"
)

type StoreError struct {
	Code      StoreErrorCode
	Operation StoreOperation
	Err       error
}

func (e *StoreError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return fmt.Sprintf("store %s failed: %s", e.Operation, e.Code)
	}
	return fmt.Sprintf("store %s failed: %s: %v", e.Operation, e.Code, e.Err)
}

func (e *StoreError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func StoreErrorCodeOf(err error) StoreErrorCode {
	var storeError *StoreError
	if errors.As(err, &storeError) {
		return storeError.Code
	}
	return ""
}

type OperationTimeouts struct {
	Read        time.Duration
	Write       time.Duration
	Transaction time.Duration
	supervisor  *Supervisor
}

var defaultOperationTimeouts = OperationTimeouts{
	Read:        10 * time.Second,
	Write:       30 * time.Second,
	Transaction: 60 * time.Second,
}

type operationMode string
type operationTimeoutClass string

const (
	operationRead  operationMode = "read"
	operationWrite operationMode = "write"

	timeoutRead        operationTimeoutClass = "read"
	timeoutWrite       operationTimeoutClass = "write"
	timeoutTransaction operationTimeoutClass = "transaction"
)

type operationSpec struct {
	ID               StoreOperation
	Repository string
	Method     string
	Mode       operationMode
	Timeout    operationTimeoutClass
}

var operationSpecs = map[StoreOperation]operationSpec{
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
	OperationEpisodeSummarySave: {
		ID: OperationEpisodeSummarySave, Repository: "RunRepository",
		Method: "SaveEpisodeSummary", Mode: operationWrite, Timeout: timeoutTransaction,
	},
	OperationEpisodeSummaryList: {
		ID: OperationEpisodeSummaryList, Repository: "RunRepository",
		Method: "ListEpisodeSummaries", Mode: operationRead, Timeout: timeoutRead,
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
	OperationMCPAccessTicketSave: {
		ID: OperationMCPAccessTicketSave, Repository: "MCPRepository", Method: "SaveMCPAccessTicket", Mode: operationWrite, Timeout: timeoutTransaction,
	},
	OperationMCPAccessTicketGet: {
		ID: OperationMCPAccessTicketGet, Repository: "MCPRepository", Method: "GetMCPAccessTicket", Mode: operationRead, Timeout: timeoutRead,
	},
	OperationMCPAccessTicketFindHash: {
		ID: OperationMCPAccessTicketFindHash, Repository: "MCPRepository", Method: "FindMCPAccessTicketBySecretHash", Mode: operationRead, Timeout: timeoutRead,
	},
	OperationMCPAccessTicketList: {
		ID: OperationMCPAccessTicketList, Repository: "MCPRepository", Method: "ListMCPAccessTickets", Mode: operationRead, Timeout: timeoutRead,
	},
	OperationMCPAccessTicketRedeem: {
		ID: OperationMCPAccessTicketRedeem, Repository: "MCPRepository", Method: "RedeemMCPAccessTicket", Mode: operationWrite, Timeout: timeoutTransaction,
	},
	OperationMCPAccessTicketRevoke: {
		ID: OperationMCPAccessTicketRevoke, Repository: "MCPRepository", Method: "RevokeMCPAccessTicket", Mode: operationWrite, Timeout: timeoutTransaction,
	},
	OperationMCPAccessTicketDelete: {
		ID: OperationMCPAccessTicketDelete, Repository: "MCPRepository", Method: "DeleteMCPAccessTicket", Mode: operationWrite, Timeout: timeoutTransaction,
	},
	OperationMCPBindingGet: {
		ID: OperationMCPBindingGet, Repository: "MCPRepository", Method: "GetMCPBinding", Mode: operationRead, Timeout: timeoutRead,
	},
	OperationMCPBindingFindPeer: {
		ID: OperationMCPBindingFindPeer, Repository: "MCPRepository", Method: "FindMCPBindingForPeer", Mode: operationRead, Timeout: timeoutRead,
	},
	OperationMCPBindingList: {
		ID: OperationMCPBindingList, Repository: "MCPRepository", Method: "ListMCPBindings", Mode: operationRead, Timeout: timeoutRead,
	},
	OperationMCPBindingRevoke: {
		ID: OperationMCPBindingRevoke, Repository: "MCPRepository", Method: "RevokeMCPBinding", Mode: operationWrite, Timeout: timeoutTransaction,
	},
	OperationMCPBindingDelete: {
		ID: OperationMCPBindingDelete, Repository: "MCPRepository", Method: "DeleteMCPBinding", Mode: operationWrite, Timeout: timeoutTransaction,
	},
	OperationMCPAccessRecordsDelete: {
		ID: OperationMCPAccessRecordsDelete, Repository: "MCPRepository", Method: "DeleteMCPAccessRecords", Mode: operationWrite, Timeout: timeoutTransaction,
	},
	OperationMCPBindingTouch: {
		ID: OperationMCPBindingTouch, Repository: "MCPRepository", Method: "TouchMCPBinding", Mode: operationWrite, Timeout: timeoutTransaction,
	},
	OperationMCPOperationCreate: {
		ID: OperationMCPOperationCreate, Repository: "MCPRepository", Method: "CreateMCPOperation", Mode: operationWrite, Timeout: timeoutTransaction,
	},
	OperationMCPOperationGet: {
		ID: OperationMCPOperationGet, Repository: "MCPRepository", Method: "GetMCPOperation", Mode: operationRead, Timeout: timeoutRead,
	},
	OperationMCPOperationFindIdempotency: {
		ID: OperationMCPOperationFindIdempotency, Repository: "MCPRepository", Method: "FindMCPOperationByIdempotency", Mode: operationRead, Timeout: timeoutRead,
	},
	OperationMCPOperationList: {
		ID: OperationMCPOperationList, Repository: "MCPRepository", Method: "ListMCPOperations", Mode: operationRead, Timeout: timeoutRead,
	},
	OperationMCPOperationUpdate: {
		ID: OperationMCPOperationUpdate, Repository: "MCPRepository", Method: "UpdateMCPOperation", Mode: operationWrite, Timeout: timeoutTransaction,
	},
}

func normalizeOperationTimeouts(timeouts OperationTimeouts) OperationTimeouts {
	if timeouts.Read <= 0 {
		timeouts.Read = defaultOperationTimeouts.Read
	}
	if timeouts.Write <= 0 {
		timeouts.Write = defaultOperationTimeouts.Write
	}
	if timeouts.Transaction <= 0 {
		timeouts.Transaction = defaultOperationTimeouts.Transaction
	}
	return timeouts
}

func operationContext(parent context.Context, operation StoreOperation, timeouts OperationTimeouts) (context.Context, context.CancelFunc) {
	spec := operationSpecs[operation]
	timeout := timeouts.Read
	if spec.Timeout == timeoutWrite {
		timeout = timeouts.Write
	} else if spec.Timeout == timeoutTransaction {
		timeout = timeouts.Transaction
	}
	var ctx context.Context
	var cancel context.CancelFunc
	if deadline, exists := parent.Deadline(); exists && time.Until(deadline) <= timeout {
		ctx, cancel = context.WithCancel(parent)
	} else {
		ctx, cancel = context.WithTimeout(parent, timeout)
	}
	if timeouts.supervisor == nil {
		return ctx, cancel
	}
	if parentSpan, _ := parent.Value(operationSpanContextKey{}).(*operationSpan); parentSpan != nil &&
		parentSpan.supervisor == timeouts.supervisor && parentSpan.operation == operation {
		return ctx, cancel
	}
	span, admitted := timeouts.supervisor.begin(operation)
	if !admitted {
		return context.WithValue(ctx, operationDeniedContextKey{}, ErrRuntimeClosing), cancel
	}
	ctx = context.WithValue(ctx, operationSpanContextKey{}, span)
	return ctx, func() {
		span.finish(ctx)
		cancel()
	}
}

func contextStoreError(operation StoreOperation, ctx context.Context, cause error) error {
	code := StoreErrorInternal
	if errors.Is(cause, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		code = StoreErrorCanceled
	} else if errors.Is(cause, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		code = StoreErrorTimeout
	}
	markOperationOutcome(ctx, code)
	return &StoreError{Code: code, Operation: operation, Err: cause}
}

func operationContextError(operation StoreOperation, ctx context.Context) error {
	if cause, denied := ctx.Value(operationDeniedContextKey{}).(error); denied {
		return storeError(ctx, operation, StoreErrorUnavailable, cause)
	}
	if err := ctx.Err(); err != nil {
		return contextStoreError(operation, ctx, err)
	}
	return nil
}

func storeError(ctx context.Context, operation StoreOperation, code StoreErrorCode, cause error) error {
	markOperationOutcome(ctx, code)
	return &StoreError{Code: code, Operation: operation, Err: cause}
}
