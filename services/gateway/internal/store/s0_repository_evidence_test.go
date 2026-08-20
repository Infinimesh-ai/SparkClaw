package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
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

func s0PostgresRowsErrTest(repository, function string) string {
	return "TestS0DefectEvidencePostgresRowsErrIsNotChecked/" + repository + "/" + function + "@s0_contract_characterization_test.go"
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

// Evidence references use TestName[/subtest]@file.go. The matrix is the
// executable authority for applicability; the bilingual inventory mirrors it.
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
		s0Tests("TestPostgresStoreRoundTrip@postgres_test.go", s0PostgresRowsErrTest("shared", "collectRows")),
	),
	"ClientRepository": s0EvidenceRow(
		s0RepositoryTest("ClientRepository", s0DimensionSuccess),
		s0RepositoryTest("ClientRepository", s0DimensionAbsence),
		s0RepositoryTest("ClientRepository", s0DimensionOrderScope),
		s0Tests("TestS0DefectEvidenceMutableAliases/ClientRepository@s0_repository_characterization_test.go"),
		s0RepositoryTest("ClientRepository", s0DimensionDuplicate),
		s0RepositoryTest("ClientRepository", s0DimensionConflictDeletion),
		s0LifecycleTest("ClientRepository"),
		s0Tests("TestFileStorePersistsAndReloadsState@file_test.go"),
		s0NA("Client and pairing commands expose no CAS/revision result; claim and revoke conflicts are covered separately from scheduling concurrency."),
		s0Tests("TestPostgresStoreRoundTrip@postgres_test.go", s0PostgresRowsErrTest("shared", "collectRows")),
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
		s0Tests("TestPostgresStorePersistsOnlyISCPOnboardingReceipt@postgres_test.go", s0PostgresRowsErrTest("ISCPOnboardingRepository", "ListISCPOnboardings")),
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
		s0Tests("TestPostgresStoreRoundTrip@postgres_test.go", "TestPostgresStoreDeleteSessionRemovesBrowserLoginBlocks@postgres_test.go", s0PostgresRowsErrTest("shared", "collectRows")),
	),
	"ConversationRepository": s0EvidenceRow(
		s0RepositoryTest("ConversationRepository", s0DimensionSuccess),
		s0RepositoryTest("ConversationRepository", s0DimensionAbsence),
		s0Tests("TestMemoryMessageEventsAreBoundedAndSessionScoped@message_events_test.go"),
		s0Tests("TestS0DefectEvidenceMutableAliases/ConversationRepository@s0_repository_characterization_test.go"),
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
		s0Tests("TestS0DefectEvidenceMutableAliases/RunRepository@s0_repository_characterization_test.go"),
		s0Tests("TestMemoryStoreSavesRunFeedback@memory_test.go"),
		s0NA("Run records are overwrite/append records and expose no repository delete, CAS, or typed conflict command."),
		s0Tests("TestS0BackendNeutralRepositoryLifecycleEvidence/RunRepository@s0_repository_lifecycle_test.go", "TestMemoryStoreSavesRunFeedback@memory_test.go"),
		s0Tests("TestFileStorePersistsAndReloadsState@file_test.go", "TestFileStorePersistsWorkflowStateAndToolBinding@file_test.go"),
		s0NA("Run methods expose no revision or idempotent-create result; backend locking serializes overwrites without a caller-visible winner contract."),
		s0Tests("TestPostgresStoreRoundTrip@postgres_test.go", s0PostgresRowsErrTest("shared", "collectRows")),
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
		s0Tests("TestPostgresStoreRoundTrip@postgres_test.go", s0PostgresRowsErrTest("shared", "collectRows")),
	),
	"ApprovalRepository": s0EvidenceRow(
		s0RepositoryTest("ApprovalRepository", s0DimensionSuccess),
		s0RepositoryTest("ApprovalRepository", s0DimensionAbsence),
		s0RepositoryTest("ApprovalRepository", s0DimensionOrderScope),
		s0Tests("TestS0DefectEvidenceMutableAliases/ApprovalRepository@s0_repository_characterization_test.go"),
		s0RepositoryTest("ApprovalRepository", s0DimensionDuplicate),
		s0Tests("TestMemoryStoreFindsExternalApprovalByStableReference@memory_test.go"),
		s0LifecycleTest("ApprovalRepository"),
		s0Tests("TestFileStorePersistsExternalApprovalContext@file_test.go", "TestFileStorePersistsPolicyExecutionContext@file_test.go"),
		s0NA("Approval uses pending-state conflict rather than a numeric revision; no concurrent winner is exposed beyond that state precondition."),
		s0Tests("TestPostgresStoreRoundTrip@postgres_test.go", s0PostgresRowsErrTest("shared", "collectRows")),
	),
	"ScheduleRepository": s0EvidenceRow(
		s0RepositoryTest("ScheduleRepository", s0DimensionSuccess),
		s0RepositoryTest("ScheduleRepository", s0DimensionAbsence),
		s0RepositoryTest("ScheduleRepository", s0DimensionOrderScope),
		s0Tests("TestS0DefectEvidenceMutableAliases/ScheduleRepository@s0_repository_characterization_test.go"),
		s0RepositoryTest("ScheduleRepository", s0DimensionDuplicate),
		s0Tests("TestMemoryStoreUpdatePendingReminderUsesCompareAndSwap@memory_test.go"),
		s0LifecycleTest("ScheduleRepository"),
		s0Tests("TestS0FileRepositoryRestartGaps/ScheduleRepository@s0_repository_characterization_test.go"),
		s0Tests("TestMemoryStoreUpdatePendingReminderUsesCompareAndSwap@memory_test.go"),
		s0Tests("TestPostgresStoreRoundTrip@postgres_test.go", s0PostgresRowsErrTest("shared", "collectRows")),
	),
	"ConnectorRepository": s0EvidenceRow(
		s0RepositoryTest("ConnectorRepository", s0DimensionSuccess),
		s0RepositoryTest("ConnectorRepository", s0DimensionAbsence),
		s0Tests("TestMemoryStoreListsAllConnectorSettingsInStableOwnerChannelOrder@connector_settings_test.go"),
		s0Tests("TestS0DefectEvidenceMutableAliases/ConnectorRepository@s0_repository_characterization_test.go"),
		s0RepositoryTest("ConnectorRepository", s0DimensionDuplicate),
		s0Tests("TestMemoryStoreConnectorSettingUsesCASAndOwnerScope@connector_settings_test.go"),
		s0LifecycleTest("ConnectorRepository"),
		s0Tests("TestFileStorePersistsConnectorSettingVersion@connector_settings_test.go", "TestS0FileRepositoryRestartGaps/ConnectorRepository@s0_repository_characterization_test.go"),
		s0Tests("TestMemoryStoreConnectorSettingUsesCASAndOwnerScope@connector_settings_test.go"),
		s0Tests("TestPostgresStoreListsAllConnectorSettings@postgres_test.go", s0PostgresRowsErrTest("shared", "collectRows")),
	),
	"PassiveNotificationRepository": s0EvidenceRow(
		s0Tests("TestS0BackendNeutralRepositoryCharacterization/PassiveNotificationRepository/success@s0_repository_characterization_test.go", "TestMemoryStorePassiveNotificationIdempotencyAndOwnerScope@passive_notifications_test.go"),
		s0Tests("TestS0BackendNeutralRepositoryCharacterization/PassiveNotificationRepository/normal_absence@s0_repository_characterization_test.go", "TestMemoryStorePassiveNotificationIdempotencyAndOwnerScope@passive_notifications_test.go"),
		s0Tests("TestFileStorePassiveNotificationSurvivesRestart@passive_notifications_test.go", "TestPrunePassiveNotificationsCapEvictsReadOldestFirst@passive_notifications_test.go"),
		s0Tests("TestS0DefectEvidenceMutableAliases/PassiveNotificationRepository@s0_repository_characterization_test.go"),
		s0Tests("TestMemoryStorePassiveNotificationIdempotencyAndOwnerScope@passive_notifications_test.go", "TestMemoryStorePassiveNotificationIdempotentReingestionAtScale@passive_notifications_test.go"),
		s0Tests("TestPrunePassiveNotificationsRetentionSweep@passive_notifications_test.go"),
		s0Tests("TestS0BackendNeutralRepositoryLifecycleEvidence/PassiveNotificationRepository@s0_repository_lifecycle_test.go", "TestPassiveNotificationRevisionSignalsInboxChanges@passive_notifications_test.go"),
		s0Tests("TestFileStorePassiveNotificationSurvivesRestart@passive_notifications_test.go", "TestFileStoreSnapshotRebuildsPassiveNotificationIndex@passive_notifications_test.go"),
		s0Tests("TestPassiveNotificationRevisionSignalsInboxChanges@passive_notifications_test.go"),
		s0Tests("TestPostgresStorePassiveNotificationPruneAndRevision@postgres_test.go", s0PostgresRowsErrTest("PassiveNotificationRepository", "PrunePassiveNotifications"), s0PostgresRowsErrTest("shared", "collectRows")),
	),
	"ExternalChatRepository": s0EvidenceRow(
		s0Tests("TestS0BackendNeutralRepositoryCharacterization/ExternalChatRepository/success@s0_repository_characterization_test.go", "TestMemoryStoreExternalChatAndInboxParity@external_chat_test.go"),
		s0RepositoryTest("ExternalChatRepository", s0DimensionAbsence),
		s0RepositoryTest("ExternalChatRepository", s0DimensionOrderScope),
		s0NA("ExternalChatSession and ExternalChatMessage contain only scalar and time values, so no mutable alias can cross the Store boundary."),
		s0Tests("TestMemoryStoreExternalChatAndInboxParity@external_chat_test.go"),
		s0NA("External chat records expose overwrite-by-ID but no delete, CAS, or typed conflict command."),
		s0LifecycleTest("ExternalChatRepository"),
		s0Tests("TestFileStoreExternalChatAndInboxParity@external_chat_test.go"),
		s0NA("External chat saves expose no revision or idempotent-create result; external-ID lookup is caller reconciliation, not CAS."),
		s0Tests("TestPostgresStoreExternalChatAndInboxParity@postgres_test.go", s0PostgresRowsErrTest("shared", "collectRows")),
	),
	"DeliveryRecordRepository": s0EvidenceRow(
		s0Tests("TestS0BackendNeutralRepositoryCharacterization/DeliveryRecordRepository/success@s0_repository_characterization_test.go", "TestMemoryStoreMessageLifecycleParity@message_lifecycle_test.go"),
		s0RepositoryTest("DeliveryRecordRepository", s0DimensionAbsence),
		s0Tests("TestMemoryStoreMessageLifecycleParity@message_lifecycle_test.go", "TestMemoryStoreExternalChatAndInboxParity@external_chat_test.go"),
		s0Tests("TestS0DefectEvidenceMutableAliases/DeliveryRecordRepository@s0_repository_characterization_test.go"),
		s0Tests("TestMemoryStoreMessageLifecycleParity@message_lifecycle_test.go", "TestMemoryStoreExternalChatAndInboxParity@external_chat_test.go"),
		s0NA("Delivery records expose lifecycle overwrite and idempotency lookup but no delete or caller-visible CAS command."),
		s0LifecycleTest("DeliveryRecordRepository"),
		s0Tests("TestFileStoreMessageLifecycleRoundTrip@message_lifecycle_test.go", "TestFileStoreExternalChatAndInboxParity@external_chat_test.go"),
		s0NA("Delivery records expose no numeric revision; source/native and owner/actor/idempotency keys are the serialized dedupe boundary."),
		s0Tests("TestPostgresStoreExternalChatAndInboxParity@postgres_test.go", s0PostgresRowsErrTest("DeliveryRecordRepository", "ListMessageReceives"), s0PostgresRowsErrTest("DeliveryRecordRepository", "ListMessageDeliveries")),
	),
	"MCPRepository": s0EvidenceRow(
		s0Tests("TestS0BackendNeutralRepositoryCharacterization/MCPRepository/success@s0_repository_characterization_test.go", "TestMCPAccessTicketRedemptionIsAtomicAndDeviceBound@mcp_access_test.go"),
		s0RepositoryTest("MCPRepository", s0DimensionAbsence),
		s0Tests("TestMCPAccessRecordsCanBeDeletedIndividuallyAndByOwner@mcp_access_test.go"),
		s0Tests("TestMemoryStoreMCPRecordsCannotBeMutatedOutsideStore@mcp_access_test.go", "TestS0BackendNeutralContractCharacterization/file/idempotency_cas_alias@s0_contract_characterization_test.go"),
		s0Tests("TestMCPOperationIdempotencyRejectsChangedRequest@mcp_access_test.go"),
		s0Tests("TestMCPBindingRevocationTerminatesOnlyNonterminalOperations@mcp_access_test.go", "TestMCPAccessRecordsCanBeDeletedIndividuallyAndByOwner@mcp_access_test.go", "TestS0BackendNeutralContractCharacterization/memory/idempotency_cas_alias@s0_contract_characterization_test.go", "TestS0BackendNeutralContractCharacterization/file/idempotency_cas_alias@s0_contract_characterization_test.go"),
		s0NA("MCP lifecycle audit is explicitly caller-owned; operation sequence is represented by versioned state rather than Store-created events."),
		s0Tests("TestFileStorePersistsMCPAccessWithoutPlaintextSecret@mcp_access_test.go", "TestS0BackendNeutralContractCharacterization/file/restart@s0_contract_characterization_test.go"),
		s0Tests("TestS0BackendNeutralContractCharacterization/memory/concurrency@s0_contract_characterization_test.go", "TestS0BackendNeutralContractCharacterization/file/concurrency@s0_contract_characterization_test.go"),
		s0Tests("TestPostgresStoreMCPAccessAtomicityIdempotencyAndRecovery@postgres_test.go", s0PostgresRowsErrTest("MCPRepository", "ListMCPAccessTickets"), s0PostgresRowsErrTest("MCPRepository", "ListMCPBindings"), s0PostgresRowsErrTest("MCPRepository", "ListMCPOperations")),
	),
	"BrowserStateRepository": s0EvidenceRow(
		s0Tests("TestS0BackendNeutralRepositoryCharacterization/BrowserStateRepository/success@s0_repository_characterization_test.go", "TestMemoryStoreScopesBrowserAuthRecordsByOwnerProfileAndSite@memory_test.go", "TestMemoryStoreTracksActiveBrowserLoginBlock@memory_test.go"),
		s0RepositoryTest("BrowserStateRepository", s0DimensionAbsence),
		s0Tests("TestMemoryStoreScopesBrowserAuthRecordsByOwnerProfileAndSite@memory_test.go", "TestMemoryStoreFindActiveBrowserLoginBlockPicksNewestStoredUpdate@memory_test.go"),
		s0Tests("TestS0DefectEvidenceMutableAliases/BrowserStateRepository@s0_repository_characterization_test.go"),
		s0Tests("TestMemoryStoreBrowserLoginBlockTrimsIDOnWrite@memory_test.go"),
		s0Tests("TestMemoryStoreBrowserHandoffCASPreservesRevisionTwoFields@memory_test.go", "TestMemoryStoreDeleteSessionRemovesBrowserLoginBlocks@memory_test.go"),
		s0LifecycleTest("BrowserStateRepository"),
		s0Tests("TestFileStoreBrowserHandoffCASRoundTrip@file_test.go", "TestFileStorePersistsAndReloadsState@file_test.go"),
		s0Tests("TestMemoryStoreBrowserHandoffCASPreservesRevisionTwoFields@memory_test.go"),
		s0Tests("TestPostgresStoreBrowserHandoffCASRoundTrip@postgres_test.go", "TestPostgresStoreFindActiveBrowserLoginBlockMatchesSharedActivePredicate@postgres_test.go", s0PostgresRowsErrTest("shared", "collectRows")),
	),
	"MemoryRepository": s0EvidenceRow(
		s0Tests("TestS0BackendNeutralRepositoryCharacterization/MemoryRepository/success@s0_repository_characterization_test.go", "TestMemoryStoreUpdatesAndDeletesAcceptedMemory@memory_test.go"),
		s0RepositoryTest("MemoryRepository", s0DimensionAbsence),
		s0Tests("TestMemoryStoreUpdatesAndDeletesAcceptedMemory@memory_test.go", "TestMemoryStorePrunesExpiredMemories@memory_test.go"),
		s0Tests("TestS0DefectEvidenceMutableAliases/MemoryRepository@s0_repository_characterization_test.go"),
		s0NA("Candidate creation always allocates a new ID; acceptance is a state transition rather than an idempotency-key API."),
		s0Tests("TestMemoryStoreUpdatesAndDeletesAcceptedMemory@memory_test.go", "TestMemoryStorePrunesExpiredMemories@memory_test.go"),
		s0LifecycleTest("MemoryRepository"),
		s0Tests("TestFileStorePersistsAndReloadsState@file_test.go", "TestFileStorePersistsMemoryRetentionPrune@file_test.go"),
		s0NA("Memory records expose state/existence conflicts but no numeric revision or idempotent-create result."),
		s0Tests("TestPostgresStoreRoundTrip@postgres_test.go", s0PostgresRowsErrTest("MemoryRepository", "PruneMemories"), s0PostgresRowsErrTest("shared", "collectRows")),
	),
	"AuditRepository": s0EvidenceRow(
		s0RepositoryTest("AuditRepository", s0DimensionSuccess),
		s0RepositoryTest("AuditRepository", s0DimensionAbsence),
		s0RepositoryTest("AuditRepository", s0DimensionOrderScope),
		s0Tests("TestS0DefectEvidenceMutableAliases/AuditRepository@s0_repository_characterization_test.go"),
		s0NA("AddAudit is append-only and generates an ID when absent; it exposes no caller idempotency key or duplicate-ID result."),
		s0NA("Audit is append-only and exposes no update, delete, CAS, or conflict command."),
		s0RepositoryTest("AuditRepository", s0DimensionEventSequence),
		s0Tests("TestFileStorePersistsMemoryRetentionPrune@file_test.go"),
		s0NA("Audit append exposes no revision; event cursors are ordered sequence reads owned by this repository."),
		s0Tests("TestPostgresStoreRoundTrip@postgres_test.go", s0PostgresRowsErrTest("shared", "collectRows")),
	),
	"EvaluationRepository": s0EvidenceRow(
		s0RepositoryTest("EvaluationRepository", s0DimensionSuccess),
		s0RepositoryTest("EvaluationRepository", s0DimensionAbsence),
		s0RepositoryTest("EvaluationRepository", s0DimensionOrderScope),
		s0Tests("TestS0DefectEvidenceMutableAliases/EvaluationRepository@s0_repository_characterization_test.go"),
		s0RepositoryTest("EvaluationRepository", s0DimensionDuplicate),
		s0NA("Evaluation runs expose overwrite-by-ID but no delete, CAS, or conflict command."),
		s0LifecycleTest("EvaluationRepository"),
		s0Tests("TestFileStorePersistsAndReloadsState@file_test.go"),
		s0NA("Evaluation runs expose no revision or idempotent-create result; writes are serialized ID overwrites."),
		s0Tests("TestPostgresStoreRoundTrip@postgres_test.go", s0PostgresRowsErrTest("shared", "collectRows")),
	),
	"ArtifactMetadataRepository": s0EvidenceRow(
		s0Tests("TestS0BackendNeutralRepositoryCharacterization/ArtifactMetadataRepository/success@s0_repository_characterization_test.go", "TestMemoryStoreFindsArtifactObjectByURI@memory_test.go"),
		s0Tests("TestS0BackendNeutralRepositoryCharacterization/ArtifactMetadataRepository/normal_absence@s0_repository_characterization_test.go", "TestMemoryStoreFindsArtifactObjectByURI@memory_test.go"),
		s0Tests("TestMemoryStoreListsArtifactObjectsNewestFirst@memory_test.go", "TestMemoryStoreFindsArtifactObjectByURI@memory_test.go"),
		s0NA("ArtifactObject contains only scalar and time values, so no mutable alias can cross the Store boundary."),
		s0Tests("TestMemoryStoreFindsArtifactObjectByURI@memory_test.go"),
		s0Tests("TestMemoryStoreFindsArtifactObjectByURI@memory_test.go"),
		s0LifecycleTest("ArtifactMetadataRepository"),
		s0Tests("TestFileStorePersistsAndReloadsState@file_test.go"),
		s0NA("Artifact metadata exposes no revision or idempotent-create result; ID overwrite atomically replaces the URI index entry."),
		s0Tests("TestPostgresStoreRoundTrip@postgres_test.go", s0PostgresRowsErrTest("shared", "collectRows")),
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
	assertS0InventoryMatrixRows(t)
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
		for _, dimension := range testCase.dimensions {
			repositoryPaths["TestS0BackendNeutralRepositoryCharacterization/"+repository+"/"+s0DimensionSubtestName(dimension)] = struct{}{}
		}
	}
	for repository := range s0MutableAliasChecks {
		repositoryPaths["TestS0DefectEvidenceMutableAliases/"+repository] = struct{}{}
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
	for _, testCase := range s0PostgresRowsErrCases {
		contractPaths["TestS0DefectEvidencePostgresRowsErrIsNotChecked/"+testCase.repository+"/"+testCase.function] = struct{}{}
	}
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

func assertS0InventoryMatrixRows(t *testing.T) {
	t.Helper()
	paths := []string{
		filepath.Join("..", "..", "..", "..", "docs", "store-s0-contract-inventory.md"),
		filepath.Join("..", "..", "..", "..", "zh-cn", "docs", "store-s0-contract-inventory.md"),
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		sectionStart := strings.Index(text, "## Per-Repository Characterization Evidence")
		if sectionStart < 0 {
			sectionStart = strings.Index(text, "## 逐 Repository Characterization 证据")
		}
		if sectionStart < 0 {
			t.Errorf("%s has no per-repository characterization section", path)
			continue
		}
		section := text[sectionStart:]
		if sectionEnd := strings.Index(section[len("## "):], "\n## "); sectionEnd >= 0 {
			section = section[:len("## ")+sectionEnd]
		}
		rows := map[string][]string{}
		for _, line := range strings.Split(section, "\n") {
			columns := strings.Split(line, "|")
			if len(columns) != len(s0CharacterizationDimensions)+3 {
				continue
			}
			repository := strings.Trim(strings.TrimSpace(columns[1]), "`")
			if _, ok := s0RepositoryMethods[repository]; !ok {
				continue
			}
			if _, duplicate := rows[repository]; duplicate {
				t.Errorf("%s contains duplicate characterization row %s", path, repository)
				continue
			}
			rows[repository] = columns[2 : len(columns)-1]
		}
		if len(rows) != len(s0RepositoryCharacterizationEvidence) {
			t.Errorf("%s contains %d characterization rows, want %d", path, len(rows), len(s0RepositoryCharacterizationEvidence))
		}
		for repository, evidence := range s0RepositoryCharacterizationEvidence {
			cells, ok := rows[repository]
			if !ok {
				t.Errorf("%s has no characterization row for %s", path, repository)
				continue
			}
			for index, dimension := range s0CharacterizationDimensions {
				documented := strings.TrimSpace(cells[index])
				cell := evidence[dimension]
				references := s0DocumentedTestReferences(documented)
				if cell.NA != "" {
					if !strings.HasPrefix(documented, "N/A:") || len([]rune(documented)) < 24 || len(references) != 0 {
						t.Errorf("%s %s %q must document a concrete N/A rationale and no test references: %q", path, repository, dimension, documented)
					}
					continue
				}
				if !reflect.DeepEqual(references, cell.Tests) {
					t.Errorf("%s %s %q evidence drift\n got: %#v\nwant: %#v", path, repository, dimension, references, cell.Tests)
				}
			}
		}
	}
}

func s0DocumentedTestReferences(cell string) []string {
	parts := strings.Split(cell, "`")
	references := []string{}
	for index := 1; index < len(parts); index += 2 {
		candidate := strings.TrimSpace(parts[index])
		if strings.Contains(candidate, "@") && strings.HasSuffix(candidate, ".go") {
			references = append(references, candidate)
		}
	}
	return references
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
