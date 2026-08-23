package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

const (
	s0DimensionSuccess             = "success"
	s0DimensionAbsence             = "normal absence"
	s0DimensionOrderScope          = "ordering/filtering/scope"
	s0DimensionCloneAlias          = "clone/alias"
	s0DimensionDuplicate           = "duplicate/idempotency"
	s0DimensionConflictDeletion    = "CAS/conflict/deletion"
	s0DimensionEventSequence       = "event/audit/sequence"
	s0DimensionFileRestart         = "File restart/snapshot"
	s0DimensionConcurrencyRevision = "concurrency/revision"
	s0DimensionPostgresRows        = "PostgreSQL row/rows.Err"
)

var s0CharacterizationDimensions = []string{
	s0DimensionSuccess,
	s0DimensionAbsence,
	s0DimensionOrderScope,
	s0DimensionCloneAlias,
	s0DimensionDuplicate,
	s0DimensionConflictDeletion,
	s0DimensionEventSequence,
	s0DimensionFileRestart,
	s0DimensionConcurrencyRevision,
	s0DimensionPostgresRows,
}

type s0EvidenceCell struct {
	Tests []string
	NA    string
}

func s0Tests(names ...string) s0EvidenceCell { return s0EvidenceCell{Tests: names} }
func s0NA(reason string) s0EvidenceCell      { return s0EvidenceCell{NA: reason} }

func s0RepositoryTest(repository, dimension string) s0EvidenceCell {
	return s0Tests("TestS0BackendNeutralRepositoryCharacterization/" + repository + "/" + s0DimensionSubtestName(dimension) + "@s0_repository_characterization_test.go")
}

func s0LifecycleTest(repository string) s0EvidenceCell {
	return s0Tests("TestS0BackendNeutralRepositoryLifecycleEvidence/" + repository + "@s0_repository_lifecycle_test.go")
}

func s0EvidenceRow(cells ...s0EvidenceCell) map[string]s0EvidenceCell {
	if len(cells) != len(s0CharacterizationDimensions) {
		panic("S0 evidence row has the wrong dimension count")
	}
	row := make(map[string]s0EvidenceCell, len(cells))
	for index, dimension := range s0CharacterizationDimensions {
		row[dimension] = cells[index]
	}
	return row
}

// Evidence references use TestName[/subtest]@file.go. The matrix itself is the
// executable authority for applicability and reference completeness.
var s0RepositoryCharacterizationEvidence = map[string]map[string]s0EvidenceCell{
	"OwnerRepository": s0EvidenceRow(
		s0RepositoryTest("OwnerRepository", s0DimensionSuccess),
		s0RepositoryTest("OwnerRepository", s0DimensionAbsence),
		s0Tests("TestMemoryStoreManagesMultipleOwnerProfiles@memory_test.go"),
		s0Tests("TestS0BackendNeutralContractCharacterization/memory/success_absence_order_scope_clone@s0_contract_characterization_test.go", "TestS0BackendNeutralContractCharacterization/file/success_absence_order_scope_clone@s0_contract_characterization_test.go"),
		s0RepositoryTest("OwnerRepository", s0DimensionDuplicate),
		s0NA("Owner profiles expose overwrite-by-ID but no delete, CAS, or conflict command."),
		s0LifecycleTest("OwnerRepository"),
		s0Tests("TestS0BackendNeutralContractCharacterization/file/restart@s0_contract_characterization_test.go"),
		s0NA("Owner commands expose no repository revision or idempotent-create result; Memory/File serialize them through the backend lock."),
		s0Tests("TestPostgresStoreRoundTrip@postgres_test.go"),
	),
	"ClientRepository": s0EvidenceRow(
		s0RepositoryTest("ClientRepository", s0DimensionSuccess),
		s0RepositoryTest("ClientRepository", s0DimensionAbsence),
		s0RepositoryTest("ClientRepository", s0DimensionOrderScope),
		s0Tests("TestClientRepositoryPointerIsolation@client_contract_test.go"),
		s0RepositoryTest("ClientRepository", s0DimensionDuplicate),
		s0RepositoryTest("ClientRepository", s0DimensionConflictDeletion),
		s0LifecycleTest("ClientRepository"),
		s0Tests("TestFileStorePersistsAndReloadsState@file_test.go"),
		s0NA("Client and pairing commands expose no CAS/revision result; claim and revoke conflicts are covered separately from scheduling concurrency."),
		s0Tests("TestPostgresStoreRoundTrip@postgres_test.go"),
	),
	"ISCPOnboardingRepository": s0EvidenceRow(
		s0RepositoryTest("ISCPOnboardingRepository", s0DimensionSuccess),
		s0RepositoryTest("ISCPOnboardingRepository", s0DimensionAbsence),
		s0RepositoryTest("ISCPOnboardingRepository", s0DimensionOrderScope),
		s0NA("ISCPOnboarding contains only scalar and time values, so no mutable alias can cross the Store boundary."),
		s0RepositoryTest("ISCPOnboardingRepository", s0DimensionDuplicate),
		s0RepositoryTest("ISCPOnboardingRepository", s0DimensionConflictDeletion),
		s0NA("The repository intentionally owns no lifecycle event; iscppairing writes the caller-owned audit record."),
		s0Tests("TestFileStorePersistsOnlyISCPOnboardingReceipt@mcp_access_test.go"),
		s0NA("The unique-ID conflict is the concurrency boundary; there is no revision or compare-and-swap field."),
		s0Tests("TestPostgresStorePersistsOnlyISCPOnboardingReceipt@postgres_test.go", "TestPostgresOnboardingListReturnsRowsError@iscp_onboarding_postgres_test.go"),
	),
	"CredentialRepository": s0EvidenceRow(
		s0RepositoryTest("CredentialRepository", s0DimensionSuccess),
		s0RepositoryTest("CredentialRepository", s0DimensionAbsence),
		s0NA("CredentialRepository has exact-ref get only and no list, filter, ordering, or owner-scope query."),
		s0NA("CredentialSecret contains only scalar and time values, so no mutable alias can cross the Store boundary."),
		s0RepositoryTest("CredentialRepository", s0DimensionDuplicate),
		s0RepositoryTest("CredentialRepository", s0DimensionConflictDeletion),
		s0LifecycleTest("CredentialRepository"),
		s0Tests("TestFileStoreEncryptsStateAtRest@file_test.go"),
		s0NA("Credential save/delete has no CAS, revision, or idempotent-create result beyond serialized ref overwrite."),
		s0NA("PostgreSQL credential operations use Exec or QueryRow only; this repository has no multi-row iterator and therefore no rows.Err path."),
	),
	"SessionRepository": s0EvidenceRow(
		s0RepositoryTest("SessionRepository", s0DimensionSuccess),
		s0RepositoryTest("SessionRepository", s0DimensionAbsence),
		s0RepositoryTest("SessionRepository", s0DimensionOrderScope),
		s0NA("Session contains only scalar and time values, so no mutable alias can cross the Store boundary."),
		s0NA("Session creation always allocates a new ID; no caller-supplied duplicate or idempotency key exists."),
		s0Tests("TestS0BackendNeutralRepositoryCharacterization/SessionRepository/CAS_conflict_deletion@s0_repository_characterization_test.go", "TestMemoryStoreDeleteSessionRemovesBrowserLoginBlocks@memory_test.go"),
		s0LifecycleTest("SessionRepository"),
		s0Tests("TestS0BackendNeutralContractCharacterization/file/restart@s0_contract_characterization_test.go"),
		s0NA("Session commands expose existence conflicts but no version/revision or idempotent-create contract."),
		s0Tests("TestPostgresStoreRoundTrip@postgres_test.go", "TestPostgresStoreDeleteSessionRemovesBrowserLoginBlocks@postgres_test.go"),
	),
	"ConversationRepository": s0EvidenceRow(
		s0RepositoryTest("ConversationRepository", s0DimensionSuccess),
		s0RepositoryTest("ConversationRepository", s0DimensionAbsence),
		s0Tests("TestMemoryMessageEventsAreBoundedAndSessionScoped@message_events_test.go"),
		s0Tests("TestS0ConversationRepositoryMutableValuesAreIsolated@s0_repository_characterization_test.go"),
		s0RepositoryTest("ConversationRepository", s0DimensionDuplicate),
		s0NA("Conversation exposes append/idempotent message reuse and cursor validation, but no update, delete, or CAS command."),
		s0Tests("TestS0BackendNeutralRepositoryLifecycleEvidence/ConversationRepository@s0_repository_lifecycle_test.go", "TestMemoryMessageEventsAreBoundedAndSessionScoped@message_events_test.go"),
		s0Tests("TestFileMessageEventsSurviveRestart@message_events_test.go", "TestS0BackendNeutralContractCharacterization/file/restart@s0_contract_characterization_test.go"),
		s0NA("Message append has no repository revision; event sequence ordering, not a caller-visible CAS version, is its concurrency contract."),
		s0Tests("TestPostgresStoreRoundTrip@postgres_test.go"),
	),
	"RunRepository": s0EvidenceRow(
		s0RepositoryTest("RunRepository", s0DimensionSuccess),
		s0RepositoryTest("RunRepository", s0DimensionAbsence),
		s0RepositoryTest("RunRepository", s0DimensionOrderScope),
		s0Tests("TestS0RunRepositoryMutableValuesAreIsolated@s0_repository_characterization_test.go"),
		s0Tests("TestMemoryStoreSavesRunFeedback@memory_test.go"),
		s0NA("Run records are overwrite/append records and expose no repository delete, CAS, or typed conflict command."),
		s0Tests("TestS0BackendNeutralRepositoryLifecycleEvidence/RunRepository@s0_repository_lifecycle_test.go", "TestMemoryStoreSavesRunFeedback@memory_test.go"),
		s0Tests("TestFileStorePersistsAndReloadsState@file_test.go", "TestFileStorePersistsWorkflowStateAndToolBinding@file_test.go"),
		s0NA("Run methods expose no revision or idempotent-create result; backend locking serializes overwrites without a caller-visible winner contract."),
		s0Tests("TestPostgresStoreRoundTrip@postgres_test.go"),
	),
	"DocumentRepository": s0EvidenceRow(
		s0RepositoryTest("DocumentRepository", s0DimensionSuccess),
		s0RepositoryTest("DocumentRepository", s0DimensionAbsence),
		s0Tests("TestMemoryStoreDocumentRecordsAreRecentAndSessionScoped@memory_test.go"),
		s0NA("DocumentRecord contains only scalar and time values, so no mutable alias can cross the Store boundary."),
		s0RepositoryTest("DocumentRepository", s0DimensionDuplicate),
		s0NA("Document records expose overwrite-by-ID but no delete, CAS, or conflict command."),
		s0LifecycleTest("DocumentRepository"),
		s0Tests("TestFileStorePersistsDocumentRecords@file_test.go"),
		s0NA("Document records expose no revision or idempotent-create result; writes are serialized ID overwrites."),
		s0Tests("TestPostgresStoreRoundTrip@postgres_test.go"),
	),
	"ApprovalRepository": s0EvidenceRow(
		s0RepositoryTest("ApprovalRepository", s0DimensionSuccess),
		s0RepositoryTest("ApprovalRepository", s0DimensionAbsence),
		s0RepositoryTest("ApprovalRepository", s0DimensionOrderScope),
		s0Tests("TestS0ApprovalRepositoryMutableValuesAreIsolated@s0_repository_characterization_test.go"),
		s0RepositoryTest("ApprovalRepository", s0DimensionDuplicate),
		s0RepositoryTest("ApprovalRepository", s0DimensionConflictDeletion),
		s0Tests("TestApprovalRepositoryContract@approval_repository_contract_test.go"),
		s0Tests("TestFileApprovalDefiniteFailureRestoresRecordAndLifecycle@approval_repository_contract_test.go", "TestFileApprovalUnknownOutcomeReconcilesAndSurvivesRestart@approval_repository_contract_test.go"),
		s0Tests("TestPostgresApprovalConcurrentExternalRefAndPendingCAS@approval_repository_contract_test.go"),
		s0Tests("TestPostgresApprovalWritesAreAtomicLifecycleTransactions@approval_postgres_contract_test.go", "TestPostgresApprovalUnknownOutcomesReturnCandidateAndTerminate@approval_postgres_contract_test.go", "TestPostgresApprovalReadsClassifyQueryScanRowsAndCorruptJSON@approval_postgres_contract_test.go"),
	),
	"ScheduleRepository": s0EvidenceRow(
		s0Tests("TestScheduleRepositoryMemoryAndFileContract@schedule_repository_contract_test.go"),
		s0Tests("TestScheduleRepositoryMemoryAndFileContract@schedule_repository_contract_test.go"),
		s0Tests("TestScheduleRepositoryMemoryAndFileContract@schedule_repository_contract_test.go"),
		s0Tests("TestScheduleRepositoryMemoryAndFileContract@schedule_repository_contract_test.go", "TestS0ScheduleRepositoryMutableValuesAreIsolated@s0_repository_characterization_test.go"),
		s0Tests("TestScheduleRepositoryMemoryAndFileContract@schedule_repository_contract_test.go"),
		s0Tests("TestScheduleRepositoryMemoryAndFileContract@schedule_repository_contract_test.go", "TestMemoryStoreUpdatePendingReminderUsesCompareAndSwap@memory_test.go"),
		s0Tests("TestScheduleRepositoryMemoryAndFileContract@schedule_repository_contract_test.go", "TestPostgresScheduleWritesUseLifecycleTransactions@schedule_repository_contract_test.go"),
		s0Tests("TestScheduleRepositoryMemoryAndFileContract@schedule_repository_contract_test.go", "TestFileScheduleRepositoryDefiniteFailuresRestoreAggregate@schedule_repository_contract_test.go"),
		s0Tests("TestScheduleRepositoryMemoryAndFileContract@schedule_repository_contract_test.go", "TestMemoryStoreUpdatePendingReminderUsesCompareAndSwap@memory_test.go"),
		s0Tests("TestPostgresScheduleWritesUseLifecycleTransactions@schedule_repository_contract_test.go", "TestPostgresScheduleReadsClassifyQueryScanRowsAndCorruptJSON@schedule_repository_contract_test.go", "TestPostgresScheduleRepositoryConfiguredContract@schedule_repository_contract_test.go"),
	),
	"ConnectorRepository": s0EvidenceRow(
		s0RepositoryTest("ConnectorRepository", s0DimensionSuccess),
		s0RepositoryTest("ConnectorRepository", s0DimensionAbsence),
		s0Tests("TestMemoryStoreListsAllConnectorSettingsInStableOwnerChannelOrder@connector_settings_test.go"),
		s0Tests("TestS0ConnectorRepositoryMutableValuesAreIsolated@s0_repository_characterization_test.go"),
		s0RepositoryTest("ConnectorRepository", s0DimensionDuplicate),
		s0Tests("TestMemoryStoreConnectorSettingUsesCASAndOwnerScope@connector_settings_test.go"),
		s0LifecycleTest("ConnectorRepository"),
		s0Tests("TestFileStorePersistsConnectorSettingVersion@connector_settings_test.go", "TestS0FileRepositoryRestartGaps/ConnectorRepository@s0_repository_characterization_test.go"),
		s0Tests("TestMemoryStoreConnectorSettingUsesCASAndOwnerScope@connector_settings_test.go"),
		s0Tests("TestPostgresStoreListsAllConnectorSettings@postgres_test.go"),
	),
	"PassiveNotificationRepository": s0EvidenceRow(
		s0Tests("TestPassiveNotificationRepositoryMemoryAndFileContract@passive_notification_repository_contract_test.go"),
		s0Tests("TestPassiveNotificationRepositoryMemoryAndFileContract@passive_notification_repository_contract_test.go"),
		s0Tests("TestPassiveNotificationRepositoryMemoryAndFileContract@passive_notification_repository_contract_test.go"),
		s0Tests("TestPassiveNotificationRepositoryMemoryAndFileContract@passive_notification_repository_contract_test.go", "TestS0PassiveNotificationRepositoryMutableValuesAreIsolated@s0_repository_characterization_test.go"),
		s0Tests("TestPassiveNotificationRepositoryMemoryAndFileContract@passive_notification_repository_contract_test.go"),
		s0Tests("TestPassiveNotificationRepositoryMemoryAndFileContract@passive_notification_repository_contract_test.go"),
		s0Tests("TestPassiveNotificationRepositoryMemoryAndFileContract@passive_notification_repository_contract_test.go", "TestPostgresPassiveNotificationWritesUseLifecycleTransactions@passive_notification_repository_contract_test.go"),
		s0Tests("TestPassiveNotificationRepositoryMemoryAndFileContract@passive_notification_repository_contract_test.go", "TestFilePassiveNotificationRepositoryDefiniteFailuresRestoreAggregate@passive_notification_repository_contract_test.go"),
		s0Tests("TestPassiveNotificationRepositoryMemoryAndFileContract@passive_notification_repository_contract_test.go"),
		s0Tests("TestPostgresPassiveNotificationWritesUseLifecycleTransactions@passive_notification_repository_contract_test.go", "TestPostgresPassiveNotificationReadsPropagateBackendErrors@passive_notification_repository_contract_test.go", "TestPostgresPassiveNotificationRepositoryConfiguredContract@passive_notification_repository_contract_test.go"),
	),
	"ExternalChatRepository": s0EvidenceRow(
		s0Tests("TestExternalChatRepositoryMemoryAndFileContract@external_chat_repository_contract_test.go", "TestS0BackendNeutralRepositoryCharacterization/ExternalChatRepository/success@s0_repository_characterization_test.go"),
		s0Tests("TestExternalChatRepositoryMemoryAndFileContract@external_chat_repository_contract_test.go", "TestS0BackendNeutralRepositoryCharacterization/ExternalChatRepository/normal_absence@s0_repository_characterization_test.go"),
		s0Tests("TestExternalChatRepositoryMemoryAndFileContract@external_chat_repository_contract_test.go", "TestS0BackendNeutralRepositoryCharacterization/ExternalChatRepository/ordering_filtering_scope@s0_repository_characterization_test.go"),
		s0NA("ExternalChatSession and ExternalChatMessage contain only scalar and time values, so no mutable alias can cross the Store boundary."),
		s0Tests("TestExternalChatRepositoryMemoryAndFileContract@external_chat_repository_contract_test.go", "TestMemoryStoreExternalChatAndInboxParity@external_chat_test.go"),
		s0NA("External chat records expose overwrite-by-ID but no delete, CAS, or typed conflict command."),
		s0Tests("TestExternalChatRepositoryMemoryAndFileContract@external_chat_repository_contract_test.go", "TestPostgresExternalChatWritesUseTransactions@external_chat_repository_contract_test.go", "TestS0BackendNeutralRepositoryLifecycleEvidence/ExternalChatRepository@s0_repository_lifecycle_test.go"),
		s0Tests("TestExternalChatRepositoryMemoryAndFileContract@external_chat_repository_contract_test.go", "TestFileStoreExternalChatAndInboxParity@external_chat_test.go"),
		s0NA("External chat saves expose no revision or idempotent-create result; external-ID lookup is caller reconciliation, not CAS."),
		s0Tests("TestPostgresExternalChatRepositoryConfiguredContract@external_chat_repository_contract_test.go", "TestPostgresExternalChatWritesUseTransactions@external_chat_repository_contract_test.go", "TestPostgresExternalChatReadFailuresAreExplicit@external_chat_repository_contract_test.go"),
	),
	"DeliveryRecordRepository": s0EvidenceRow(
		s0Tests("TestS0BackendNeutralRepositoryCharacterization/DeliveryRecordRepository/success@s0_repository_characterization_test.go", "TestDeliveryRecordRepositoryReceiveContract@delivery_record_repository_contract_test.go", "TestDeliveryRecordRepositoryDeliveryContractAndIsolation@delivery_record_repository_contract_test.go", "TestDeliveryRecordRepositoryInboxContract@delivery_record_repository_contract_test.go"),
		s0RepositoryTest("DeliveryRecordRepository", s0DimensionAbsence),
		s0Tests("TestDeliveryRecordRepositoryReceiveContract@delivery_record_repository_contract_test.go", "TestDeliveryRecordRepositoryInboxContract@delivery_record_repository_contract_test.go"),
		s0Tests("TestDeliveryRecordRepositoryDeliveryContractAndIsolation@delivery_record_repository_contract_test.go", "TestS0DeliveryRecordRepositoryMutableValuesAreIsolated@s0_repository_characterization_test.go"),
		s0Tests("TestDeliveryRecordRepositoryReceiveContract@delivery_record_repository_contract_test.go", "TestDeliveryRecordRepositoryDeliveryContractAndIsolation@delivery_record_repository_contract_test.go", "TestDeliveryRecordRepositoryInboxContract@delivery_record_repository_contract_test.go"),
		s0Tests("TestDeliveryRecordRepositoryRejectsCrossBoundIdentity@delivery_record_repository_contract_test.go"),
		s0Tests("TestDeliveryRecordRepositoryWritesLifecycleAudit@delivery_record_repository_contract_test.go", "TestPostgresDeliveryRecordWritesUseOneTransactionAndBothIdentityLocks@delivery_record_postgres_contract_test.go"),
		s0Tests("TestFileDeliveryRecordDefiniteFailuresRestoreCompleteState@delivery_record_file_durability_test.go", "TestFileDeliveryRecordUnknownOutcomesReconcileAndSurviveRestart@delivery_record_file_durability_test.go"),
		s0Tests("TestDeliveryRecordRepositoryConcurrentIdempotency@delivery_record_repository_contract_test.go", "TestPostgresDeliveryRecordRepositoryConcurrentIdempotencyAndAuditAtomicity@delivery_record_postgres_contract_test.go"),
		s0Tests("TestPostgresDeliveryRecordWritesUseOneTransactionAndBothIdentityLocks@delivery_record_postgres_contract_test.go", "TestPostgresDeliveryRecordStatementAndCommitFailureSemantics@delivery_record_postgres_contract_test.go", "TestPostgresDeliveryRecordRejectsCrossBoundIdentity@delivery_record_postgres_contract_test.go", "TestPostgresDeliveryRecordReadFailureClassification@delivery_record_postgres_contract_test.go", "TestPostgresDeliveryRecordRepositoryConcurrentIdempotencyAndAuditAtomicity@delivery_record_postgres_contract_test.go"),
	),
	"MCPRepository": s0EvidenceRow(
		s0Tests("TestS0BackendNeutralRepositoryCharacterization/MCPRepository/success@s0_repository_characterization_test.go", "TestMCPAccessTicketRedemptionIsAtomicAndDeviceBound@mcp_access_test.go", "TestMCPRepositoryConcurrencyContract@mcp_repository_contract_test.go"),
		s0Tests("TestS0BackendNeutralRepositoryCharacterization/MCPRepository/normal_absence@s0_repository_characterization_test.go", "TestPostgresMCPReadsClassifyAbsenceTransportCorruptionAndRowsErrors@mcp_repository_postgres_contract_test.go"),
		s0Tests("TestMCPAccessRecordsCanBeDeletedIndividuallyAndByOwner@mcp_access_test.go", "TestMCPRepositoryConcurrencyContract@mcp_repository_contract_test.go"),
		s0Tests("TestMemoryStoreMCPRecordsCannotBeMutatedOutsideStore@mcp_access_test.go", "TestS0BackendNeutralContractCharacterization/file/idempotency_cas_alias@s0_contract_characterization_test.go"),
		s0Tests("TestMCPOperationIdempotencyRejectsChangedRequest@mcp_access_test.go", "TestMCPRepositoryConcurrencyContract@mcp_repository_contract_test.go"),
		s0Tests("TestMCPBindingRevocationTerminatesOnlyNonterminalOperations@mcp_access_test.go", "TestMCPAccessRecordsCanBeDeletedIndividuallyAndByOwner@mcp_access_test.go", "TestS0BackendNeutralContractCharacterization/memory/idempotency_cas_alias@s0_contract_characterization_test.go", "TestS0BackendNeutralContractCharacterization/file/idempotency_cas_alias@s0_contract_characterization_test.go", "TestMCPRepositoryConcurrencyContract@mcp_repository_contract_test.go"),
		s0NA("MCP lifecycle audit is explicitly caller-owned; operation sequence is represented by versioned state rather than Store-created events."),
		s0Tests("TestFileStorePersistsMCPAccessWithoutPlaintextSecret@mcp_access_test.go", "TestFileMCPDefiniteFailureRestoresMemoryState@mcp_repository_file_durability_test.go", "TestFileMCPUnknownOutcomesReconcileAndSurviveRestart@mcp_repository_file_durability_test.go", "TestFileMCPOperationCreateUnknownOutcomeReconciles@mcp_repository_file_durability_test.go", "TestFileMCPRedemptionUnknownOutcomeReconcilesAndSurvivesRestart@mcp_repository_file_durability_test.go", "TestFileMCPRemainingUnknownOutcomesReconcileAndSurviveRestart@mcp_repository_file_durability_test.go"),
		s0Tests("TestMCPRepositoryConcurrencyContract@mcp_repository_contract_test.go", "TestPostgresMCPRepositoryContract@mcp_repository_contract_test.go"),
		s0Tests("TestPostgresStoreMCPAccessAtomicityIdempotencyAndRecovery@postgres_test.go", "TestPostgresMCPRepositoryContract@mcp_repository_contract_test.go", "TestPostgresMCPReadsClassifyAbsenceTransportCorruptionAndRowsErrors@mcp_repository_postgres_contract_test.go", "TestPostgresMCPWriteFailureClassificationAndOwnership@mcp_repository_postgres_contract_test.go", "TestPostgresMCPWritesAndReconciliationReadsShareBarriers@mcp_repository_postgres_contract_test.go", "TestPostgresMCPMultiRecordWritesAreAtomic@mcp_repository_postgres_contract_test.go", "TestPostgresMCPOperationUnknownUpdateReturnsReconciliationCandidate@mcp_repository_postgres_contract_test.go", "TestPostgresMCPTransactionRowsErrorRollsBackWhenSafe@mcp_repository_postgres_contract_test.go"),
	),
	"BrowserStateRepository": s0EvidenceRow(
		s0Tests("TestS0BackendNeutralRepositoryCharacterization/BrowserStateRepository/success@s0_repository_characterization_test.go", "TestBrowserStateRepositoryMemoryAndFileContract@browser_state_repository_contract_test.go", "TestMemoryStoreScopesBrowserAuthRecordsByOwnerProfileAndSite@memory_test.go", "TestMemoryStoreTracksActiveBrowserLoginBlock@memory_test.go"),
		s0Tests("TestS0BackendNeutralRepositoryCharacterization/BrowserStateRepository/normal_absence@s0_repository_characterization_test.go", "TestBrowserStateRepositoryMemoryAndFileContract@browser_state_repository_contract_test.go"),
		s0Tests("TestBrowserStateRepositoryMemoryAndFileContract@browser_state_repository_contract_test.go", "TestMemoryStoreScopesBrowserAuthRecordsByOwnerProfileAndSite@memory_test.go", "TestMemoryStoreFindActiveBrowserLoginBlockPicksNewestStoredUpdate@memory_test.go"),
		s0Tests("TestBrowserStateRepositoryMemoryAndFileContract@browser_state_repository_contract_test.go", "TestS0BrowserStateRepositoryMutableValuesAreIsolated@s0_repository_characterization_test.go"),
		s0Tests("TestBrowserStateRepositoryMemoryAndFileContract@browser_state_repository_contract_test.go", "TestMemoryStoreBrowserLoginBlockTrimsIDOnWrite@memory_test.go"),
		s0Tests("TestBrowserStateRepositoryMemoryAndFileContract@browser_state_repository_contract_test.go", "TestMemoryStoreBrowserHandoffCASPreservesRevisionTwoFields@memory_test.go", "TestMemoryStoreDeleteSessionRemovesBrowserLoginBlocks@memory_test.go"),
		s0Tests("TestBrowserStateRepositoryMemoryAndFileContract@browser_state_repository_contract_test.go", "TestS0BackendNeutralRepositoryLifecycleEvidence/BrowserStateRepository@s0_repository_lifecycle_test.go"),
		s0Tests("TestBrowserStateRepositoryMemoryAndFileContract@browser_state_repository_contract_test.go", "TestFileStoreBrowserHandoffCASRoundTrip@file_test.go", "TestFileStorePersistsAndReloadsState@file_test.go"),
		s0Tests("TestBrowserStateRepositoryMemoryAndFileContract@browser_state_repository_contract_test.go", "TestMemoryStoreBrowserHandoffCASPreservesRevisionTwoFields@memory_test.go"),
		s0Tests("TestPostgresBrowserStateWritesUseAtomicLifecycleTransactions@browser_state_repository_contract_test.go", "TestPostgresBrowserStateReadsPropagateBackendAndDecodeErrors@browser_state_repository_contract_test.go", "TestPostgresBrowserStateRepositoryConfiguredContract@browser_state_repository_contract_test.go", "TestPostgresStoreBrowserHandoffCASRoundTrip@postgres_test.go", "TestPostgresStoreFindActiveBrowserLoginBlockMatchesSharedActivePredicate@postgres_test.go"),
	),
	"MemoryRepository": s0EvidenceRow(
		s0Tests("TestS0BackendNeutralRepositoryCharacterization/MemoryRepository/success@s0_repository_characterization_test.go", "TestMemoryRepositoryMemoryAndFileContract@memory_repository_contract_test.go", "TestMemoryStoreUpdatesAndDeletesAcceptedMemory@memory_test.go"),
		s0Tests("TestS0BackendNeutralRepositoryCharacterization/MemoryRepository/normal_absence@s0_repository_characterization_test.go", "TestMemoryRepositoryMemoryAndFileContract@memory_repository_contract_test.go"),
		s0Tests("TestMemoryRepositoryMemoryAndFileContract@memory_repository_contract_test.go", "TestMemoryStoreUpdatesAndDeletesAcceptedMemory@memory_test.go", "TestMemoryStorePrunesExpiredMemories@memory_test.go"),
		s0Tests("TestMemoryRepositoryMemoryAndFileContract@memory_repository_contract_test.go", "TestS0MemoryRepositoryMutableValuesAreIsolated@s0_repository_characterization_test.go"),
		s0Tests("TestMemoryRepositoryMemoryAndFileContract@memory_repository_contract_test.go"),
		s0Tests("TestMemoryRepositoryMemoryAndFileContract@memory_repository_contract_test.go", "TestMemoryStoreUpdatesAndDeletesAcceptedMemory@memory_test.go", "TestMemoryStorePrunesExpiredMemories@memory_test.go"),
		s0Tests("TestMemoryRepositoryMemoryAndFileContract@memory_repository_contract_test.go", "TestS0BackendNeutralRepositoryLifecycleEvidence/MemoryRepository@s0_repository_lifecycle_test.go"),
		s0Tests("TestMemoryRepositoryMemoryAndFileContract@memory_repository_contract_test.go", "TestFileStorePersistsAndReloadsState@file_test.go", "TestFileStorePersistsMemoryRetentionPrune@file_test.go"),
		s0NA("Memory records expose state/existence conflicts but no numeric revision or idempotent-create result."),
		s0Tests("TestPostgresMemoryWritesUseAtomicLifecycleTransactions@memory_repository_contract_test.go", "TestPostgresMemoryReadsPropagateBackendErrors@memory_repository_contract_test.go", "TestPostgresMemoryRepositoryConfiguredContract@memory_repository_contract_test.go", "TestPostgresStoreRoundTrip@postgres_test.go"),
	),
	"AuditRepository": s0EvidenceRow(
		s0RepositoryTest("AuditRepository", s0DimensionSuccess),
		s0RepositoryTest("AuditRepository", s0DimensionAbsence),
		s0RepositoryTest("AuditRepository", s0DimensionOrderScope),
		s0Tests("TestAuditRepositoryMemoryAndFileContract@audit_repository_contract_test.go", "TestAuditRepositoryEventSequenceAndTypedIsolation@audit_repository_contract_test.go"),
		s0NA("AddAudit is append-only and generates an ID when absent; it exposes no caller idempotency key or duplicate-ID result."),
		s0NA("Audit is append-only and exposes no update, delete, CAS, or conflict command."),
		s0Tests("TestAuditRepositoryEventSequenceAndTypedIsolation@audit_repository_contract_test.go"),
		s0Tests("TestAuditRepositoryMemoryAndFileContract@audit_repository_contract_test.go"),
		s0NA("Audit append exposes no revision; event cursors are ordered sequence reads owned by this repository."),
		s0Tests("TestPostgresAuditRepositoryPropagatesBackendFailures@audit_repository_contract_test.go", "TestPostgresAuditRepositoryMissingCursorIsEmpty@audit_repository_contract_test.go", "TestPostgresAuditRepositoryRestoresTypedEventPayload@audit_repository_contract_test.go", "TestPostgresAuditRepositoryConfiguredContract@audit_repository_contract_test.go"),
	),
	"EvaluationRepository": s0EvidenceRow(
		s0RepositoryTest("EvaluationRepository", s0DimensionSuccess),
		s0RepositoryTest("EvaluationRepository", s0DimensionAbsence),
		s0RepositoryTest("EvaluationRepository", s0DimensionOrderScope),
		s0Tests("TestS0EvaluationRepositoryMutableValuesAreIsolated@s0_repository_characterization_test.go", "TestEvaluationRepositoryMemoryAndFileContract@evaluation_repository_contract_test.go"),
		s0RepositoryTest("EvaluationRepository", s0DimensionDuplicate),
		s0NA("Evaluation runs expose overwrite-by-ID but no delete, CAS, or conflict command."),
		s0Tests("TestEvaluationRepositoryMemoryAndFileContract@evaluation_repository_contract_test.go"),
		s0Tests("TestEvaluationRepositoryMemoryAndFileContract@evaluation_repository_contract_test.go"),
		s0NA("Evaluation runs expose no revision or idempotent-create result; writes are serialized ID overwrites."),
		s0Tests("TestPostgresEvaluationSaveUsesAtomicLifecycleTransaction@evaluation_repository_contract_test.go", "TestPostgresEvaluationReadsPropagateBackendErrors@evaluation_repository_contract_test.go", "TestPostgresEvaluationRepositoryConfiguredContract@evaluation_repository_contract_test.go"),
	),
	"ArtifactMetadataRepository": s0EvidenceRow(
		s0Tests("TestS0BackendNeutralRepositoryCharacterization/ArtifactMetadataRepository/success@s0_repository_characterization_test.go", "TestArtifactMetadataRepositoryMemoryAndFileContract@artifact_metadata_repository_contract_test.go"),
		s0Tests("TestS0BackendNeutralRepositoryCharacterization/ArtifactMetadataRepository/normal_absence@s0_repository_characterization_test.go", "TestArtifactMetadataRepositoryMemoryAndFileContract@artifact_metadata_repository_contract_test.go"),
		s0Tests("TestArtifactMetadataRepositoryMemoryAndFileContract@artifact_metadata_repository_contract_test.go"),
		s0NA("ArtifactObject contains only scalar and time values, so no mutable alias can cross the Store boundary."),
		s0Tests("TestArtifactMetadataRepositoryMemoryAndFileContract@artifact_metadata_repository_contract_test.go"),
		s0Tests("TestArtifactMetadataRepositoryMemoryAndFileContract@artifact_metadata_repository_contract_test.go"),
		s0Tests("TestArtifactMetadataRepositoryMemoryAndFileContract@artifact_metadata_repository_contract_test.go"),
		s0Tests("TestArtifactMetadataRepositoryMemoryAndFileContract@artifact_metadata_repository_contract_test.go"),
		s0NA("Artifact metadata exposes no revision or idempotent-create result; ID overwrite atomically replaces the URI index entry."),
		s0Tests("TestPostgresArtifactMetadataSaveUsesAtomicLifecycleTransaction@artifact_metadata_repository_contract_test.go", "TestPostgresArtifactMetadataReadsPropagateBackendErrors@artifact_metadata_repository_contract_test.go", "TestPostgresArtifactMetadataRepositoryConfiguredContract@artifact_metadata_repository_contract_test.go"),
	),
}

func TestS0RepositoryCharacterizationMatrixCompleteness(t *testing.T) {
	if len(s0RepositoryCharacterizationEvidence) != len(s0RepositoryMethods) {
		t.Fatalf("repository evidence rows = %d, want %d", len(s0RepositoryCharacterizationEvidence), len(s0RepositoryMethods))
	}
	testsByFile := collectS0TestPaths(t)
	for repository := range s0RepositoryMethods {
		row, ok := s0RepositoryCharacterizationEvidence[repository]
		if !ok {
			t.Errorf("repository %s has no characterization evidence row", repository)
			continue
		}
		if len(row) != len(s0CharacterizationDimensions) {
			t.Errorf("repository %s has %d evidence cells, want %d", repository, len(row), len(s0CharacterizationDimensions))
		}
		for _, dimension := range s0CharacterizationDimensions {
			cell, ok := row[dimension]
			if !ok {
				t.Errorf("repository %s is missing dimension %q", repository, dimension)
				continue
			}
			if (len(cell.Tests) == 0) == (cell.NA == "") {
				t.Errorf("repository %s dimension %q must have tests or one N/A reason", repository, dimension)
				continue
			}
			if cell.NA != "" {
				if len(strings.Fields(cell.NA)) < 8 {
					t.Errorf("repository %s dimension %q has a non-specific N/A reason: %q", repository, dimension, cell.NA)
				}
				continue
			}
			for _, reference := range cell.Tests {
				assertS0TestReference(t, testsByFile, repository, dimension, reference)
			}
		}
	}
	for repository := range s0RepositoryCharacterizationEvidence {
		if _, ok := s0RepositoryMethods[repository]; !ok {
			t.Errorf("characterization evidence names unknown repository %s", repository)
		}
	}
}

func s0CallsFunction(node ast.Node, name string) bool {
	found := false
	ast.Inspect(node, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		identifier, ok := call.Fun.(*ast.Ident)
		if ok && identifier.Name == name {
			found = true
			return false
		}
		return true
	})
	return found
}

func s0ContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func collectS0TestPaths(t *testing.T) map[string]map[string]struct{} {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]struct{}{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), entry.Name(), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		paths := map[string]struct{}{}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if ok && strings.HasPrefix(function.Name.Name, "Test") {
				paths[function.Name.Name] = struct{}{}
				collectS0StaticSubtestPaths(function.Body, function.Name.Name, paths)
			}
		}
		out[entry.Name()] = paths
	}
	addS0DynamicTestPaths(out)
	return out
}

func collectS0StaticSubtestPaths(node ast.Node, prefix string, paths map[string]struct{}) {
	ast.Inspect(node, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) < 2 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Run" {
			return true
		}
		literal, ok := call.Args[0].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		name, err := strconv.Unquote(literal.Value)
		if err != nil {
			return true
		}
		path := prefix + "/" + name
		paths[path] = struct{}{}
		if body, ok := call.Args[1].(*ast.FuncLit); ok {
			collectS0StaticSubtestPaths(body.Body, path, paths)
		}
		return false
	})
}

func addS0DynamicTestPaths(pathsByFile map[string]map[string]struct{}) {
	repositoryPaths := pathsByFile["s0_repository_characterization_test.go"]
	for repository, testCase := range s0RepositoryCharacterizationCases {
		for dimension := range testCase.checks {
			repositoryPaths["TestS0BackendNeutralRepositoryCharacterization/"+repository+"/"+s0DimensionSubtestName(dimension)] = struct{}{}
		}
	}
	lifecyclePaths := pathsByFile["s0_repository_lifecycle_test.go"]
	for repository := range s0RepositoryLifecycleCases {
		lifecyclePaths["TestS0BackendNeutralRepositoryLifecycleEvidence/"+repository] = struct{}{}
	}

	contractPaths := pathsByFile["s0_contract_characterization_test.go"]
	for _, backend := range []string{"memory", "file"} {
		for _, subtest := range []string{"success_absence_order_scope_clone", "idempotency_cas_alias", "events", "concurrency"} {
			contractPaths["TestS0BackendNeutralContractCharacterization/"+backend+"/"+subtest] = struct{}{}
		}
	}
	contractPaths["TestS0BackendNeutralContractCharacterization/file/restart"] = struct{}{}
}

func assertS0TestReference(t *testing.T, testsByFile map[string]map[string]struct{}, repository, dimension, reference string) {
	t.Helper()
	testPath, file, ok := strings.Cut(reference, "@")
	if !ok || testPath == "" || file == "" || strings.Contains(file, "/") {
		t.Errorf("repository %s dimension %q has invalid test reference %q", repository, dimension, reference)
		return
	}
	if _, ok := testsByFile[file][testPath]; !ok {
		t.Errorf("repository %s dimension %q references missing test path %s in %s", repository, dimension, testPath, file)
	}
}

func TestS0CharacterizationDimensionNamesAreStable(t *testing.T) {
	want := []string{
		"success", "normal absence", "ordering/filtering/scope", "clone/alias", "duplicate/idempotency",
		"CAS/conflict/deletion", "event/audit/sequence", "File restart/snapshot", "concurrency/revision", "PostgreSQL row/rows.Err",
	}
	if !reflect.DeepEqual(s0CharacterizationDimensions, want) {
		t.Fatalf("S0 characterization dimensions = %#v, want %#v", s0CharacterizationDimensions, want)
	}
}
