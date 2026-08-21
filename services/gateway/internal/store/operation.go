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
	ID         StoreOperation
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
	if deadline, exists := parent.Deadline(); exists && time.Until(deadline) <= timeout {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, timeout)
}

func contextStoreError(operation StoreOperation, ctx context.Context, cause error) error {
	code := StoreErrorInternal
	if errors.Is(cause, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		code = StoreErrorCanceled
	} else if errors.Is(cause, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		code = StoreErrorTimeout
	}
	return &StoreError{Code: code, Operation: operation, Err: cause}
}

func operationContextError(operation StoreOperation, ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return contextStoreError(operation, ctx, err)
	}
	return nil
}

func storeError(operation StoreOperation, code StoreErrorCode, cause error) error {
	return &StoreError{Code: code, Operation: operation, Err: cause}
}
