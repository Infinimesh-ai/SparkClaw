# Store S0 Contract Inventory

> Language: English | [简体中文](../zh-cn/docs/store-s0-contract-inventory.md)

> Status: S0 implementation candidate, 2026-08-20. The user authorized S0 to
> start on 2026-08-20. This inventory does not authorize S1, and the S0
> implementation review remains pending human acceptance.

This is the code-fact inventory for commit `df05cf5` plus the S0
characterization tests. The sources are `store.go`, `memory.go`, `file.go`,
`postgres.go`, the ISCP/MCP Store files, `migrations/0001_core.sql`, and every
production `store.Store` reference under `services/gateway`. Method ownership
is executable in `s0_contract_characterization_test.go`; per-repository
applicability/evidence is executable in `s0_repository_evidence_test.go` and
`s0_repository_characterization_test.go`; PostgreSQL source reconciliation is
executable in `s0_postgres_manifest_test.go`.

## Accepted Repository Catalog

S0 freezes 20 repositories. Transaction breadth, not assembly convenience,
determines ownership. In particular, `DeleteSession` remains a
`SessionRepository` command even though its transaction deletes records owned
by many other repositories.

| Repository | Methods | Responsibility |
|---|---:|---|
| `OwnerRepository` | 6 | Owner profiles and external-owner lookup |
| `ClientRepository` | 9 | Clients, token lookup, revocation, last-seen, and pairing codes |
| `ISCPOnboardingRepository` | 3 | Non-secret ISCP onboarding receipts |
| `CredentialRepository` | 3 | Encrypted credential secret metadata |
| `SessionRepository` | 6 | Session lifecycle, including the cross-record delete transaction |
| `ConversationRepository` | 4 | Messages and the bounded message-event stream |
| `RunRepository` | 12 | Agent runs, feedback, model/tool calls, and episode summaries |
| `DocumentRepository` | 3 | Durable document records and lineage metadata |
| `ApprovalRepository` | 6 | Approval creation, external lookup, pending update, and resolution |
| `ScheduleRepository` | 7 | Reminders, due claims, CAS, and delivery history |
| `ConnectorRepository` | 8 | Owner connector settings and notification bindings |
| `PassiveNotificationRepository` | 8 | Passive inbox, read state, pruning, and process-local revision |
| `ExternalChatRepository` | 9 | Provider-neutral external sessions and messages |
| `DeliveryRecordRepository` | 12 | Receive, send, inbox-update, and idempotency records |
| `MCPRepository` | 19 | Access tickets, bindings, operations, redemption, revocation, and deletion |
| `BrowserStateRepository` | 10 | Browser auth records and login-block lifecycle |
| `MemoryRepository` | 7 | Candidates, accepted memories, search, update, delete, and prune |
| `AuditRepository` | 3 | Audit records and the general session event stream |
| `EvaluationRepository` | 3 | Evaluation run save/get/list |
| `ArtifactMetadataRepository` | 3 | Artifact metadata save/list/URI lookup |

## Complete Method Ownership

The numbers are the source order in the current `Store` interface. Every name
below appears exactly once; the characterization test rejects an unassigned,
duplicate, added, or removed method.

| No. | Repository | Current methods |
|---:|---|---|
| 1-6 | `SessionRepository` | `CreateSession`, `CreateSessionWithScope`, `ListSessions`, `GetSession`, `UpdateSessionTitle`, `DeleteSession` |
| 7-12, 19-21 | `ClientRepository` | `SaveClient`, `GetClient`, `ListClients`, `RevokeClient`, `FindClientByTokenHash`, `TouchClient`, `SavePairingCode`, `GetPairingCode`, `ClaimPairingCode` |
| 13-18 | `OwnerRepository` | `GetOwnerProfile`, `UpdateOwnerProfile`, `GetOwnerProfileByID`, `SaveOwnerProfile`, `ListOwnerProfiles`, `FindOwnerProfileByExternalRef` |
| 22-24 | `ISCPOnboardingRepository` | `SaveISCPOnboarding`, `GetISCPOnboarding`, `ListISCPOnboardings` |
| 25-43 | `MCPRepository` | `SaveMCPAccessTicket`, `GetMCPAccessTicket`, `FindMCPAccessTicketBySecretHash`, `ListMCPAccessTickets`, `RedeemMCPAccessTicket`, `RevokeMCPAccessTicket`, `DeleteMCPAccessTicket`, `GetMCPBinding`, `FindMCPBindingForPeer`, `ListMCPBindings`, `RevokeMCPBinding`, `DeleteMCPBinding`, `DeleteMCPAccessRecords`, `TouchMCPBinding`, `CreateMCPOperation`, `GetMCPOperation`, `FindMCPOperationByIdempotency`, `ListMCPOperations`, `UpdateMCPOperation` |
| 44-45, 132-133 | `ConversationRepository` | `AddMessage`, `ListMessages`, `MessageEventHead`, `MessageEventsAfter` |
| 46-55, 140-141 | `RunRepository` | `SaveRunFeedback`, `ListRunFeedback`, `SaveRun`, `GetRun`, `ListRuns`, `SaveModelCall`, `ListModelCalls`, `SaveToolCall`, `GetToolCall`, `ListToolCalls`, `SaveEpisodeSummary`, `ListEpisodeSummaries` |
| 56-58 | `DocumentRepository` | `SaveDocumentRecord`, `GetDocumentRecord`, `ListDocumentRecords` |
| 59-64 | `ApprovalRepository` | `SaveApproval`, `GetApproval`, `FindApprovalByExternalRef`, `UpdatePendingApproval`, `ResolveApproval`, `ListApprovals` |
| 65-71 | `ScheduleRepository` | `SaveReminder`, `UpdatePendingReminder`, `GetReminder`, `ListReminders`, `ClaimDueReminders`, `SaveReminderDelivery`, `ListReminderDeliveries` |
| 72-79 | `ConnectorRepository` | `GetConnectorSetting`, `ListConnectorSettings`, `ListAllConnectorSettings`, `UpdateConnectorSetting`, `SaveNotificationBinding`, `GetNotificationBinding`, `ListNotificationBindings`, `RevokeNotificationBinding` |
| 80-87 | `PassiveNotificationRepository` | `CreatePassiveNotification`, `GetPassiveNotification`, `ListPassiveNotifications`, `CountUnreadPassiveNotifications`, `MarkPassiveNotificationRead`, `MarkAllPassiveNotificationsRead`, `PrunePassiveNotifications`, `PassiveNotificationRevision` |
| 88-96 | `ExternalChatRepository` | `SaveExternalChatSession`, `GetExternalChatSession`, `ListExternalChatSessions`, `FindExternalChatSession`, `FindExternalChatSessionByLinkedSessionID`, `SaveExternalChatMessage`, `GetExternalChatMessage`, `FindExternalChatMessageByExternalID`, `ListExternalChatMessages` |
| 97-108 | `DeliveryRecordRepository` | `SaveMessageReceive`, `GetMessageReceive`, `FindMessageReceive`, `ListMessageReceives`, `SaveMessageDelivery`, `GetMessageDelivery`, `FindMessageDeliveryByIdempotency`, `ListMessageDeliveries`, `SaveChannelInboxUpdate`, `GetChannelInboxUpdate`, `FindChannelInboxUpdate`, `ListChannelInboxUpdates` |
| 109-111 | `CredentialRepository` | `SaveCredentialSecret`, `GetCredentialSecret`, `DeleteCredentialSecret` |
| 112-121 | `BrowserStateRepository` | `SaveBrowserAuthRecord`, `GetBrowserAuthRecord`, `FindBrowserAuthRecord`, `ListBrowserAuthRecords`, `RevokeBrowserAuthRecord`, `SaveBrowserLoginBlock`, `UpdateBrowserLoginBlock`, `GetBrowserLoginBlock`, `FindActiveBrowserLoginBlock`, `ListBrowserLoginBlocks` |
| 122-128 | `MemoryRepository` | `AddMemoryCandidate`, `ResolveMemoryCandidate`, `ListMemoryCandidates`, `SearchMemories`, `UpdateMemory`, `DeleteMemory`, `PruneMemories` |
| 129-131 | `AuditRepository` | `AddAudit`, `ListAudit`, `EventsAfter` |
| 134-136 | `EvaluationRepository` | `SaveEvalRun`, `GetEvalRun`, `ListEvalRuns` |
| 137-139 | `ArtifactMetadataRepository` | `SaveArtifactObject`, `ListArtifactObjects`, `FindArtifactObjectByURI` |

## Backend And Persistence Map

All 141 methods have Memory, File, and PostgreSQL implementations. File methods
are all in `file.go`. The ordinary Memory/PostgreSQL methods are in
`memory.go`/`postgres.go`; methods 22-24 use `iscp_onboarding.go` and
`iscp_onboarding_postgres.go`; methods 25-43 use `mcp_access.go` and
`mcp_access_postgres.go`. The global compile assertions in `store.go` and the
method catalog test prove backend completeness.

The following table maps repository state to all 38 serialized `Snapshot`
fields and current PostgreSQL objects. A semicolon separates compatibility or
derived state from the primary record.

| Repository | File `Snapshot` fields | PostgreSQL tables | Indexes and constraints |
|---|---|---|---|
| Owner | `OwnerProfile`, `OwnerProfiles` | `owners` | PK `id`; `owners_external_ref_idx(source,external_ref)` exists only in Go schema |
| Client | `Clients`, `PairingCodes` | `clients`, `pairing_codes` | PKs; unique `token_hash` and `code_hash`; token and status/expiry indexes |
| ISCP onboarding | `ISCPOnboardings` | `iscp_onboardings` | PK `id`; `iscp_onboardings_owner_created_idx` |
| Credential | `CredentialSecrets` | `credential_secrets` | PK `ref` |
| Session | `Sessions` | `sessions` | PK `id`; referenced by message/run/document/browser rows |
| Conversation | `Messages`; message events are in `Events` | `messages`, `events` | message session/time and event session/sequence indexes; unique event `id` |
| Run | `RunFeedback`, `Runs`, `ModelCalls`, `ToolCalls`, `EpisodeSummaries` | `run_feedback`, `agent_runs`, `model_calls`, `tool_calls`, `episode_summaries` | PKs, run/time indexes, and session/run FKs |
| Document | `DocumentRecords` | `document_records` | PK; owner/session/activity and session/path indexes |
| Approval | `Approvals` | `approvals` | PK; status index; partial unique `(source,external_id)` |
| Schedule | `Reminders`, `ReminderDelivery` | `reminders`, `reminder_deliveries` | PKs; status/due and reminder-delivery indexes; delivery FK |
| Connector | `ConnectorSettings`, `NotificationBindings` | `connector_settings`, `notification_bindings` | composite setting PK `(owner_id,channel)`; binding PK and channel/status index |
| Passive notification | `PassiveNotifications`; revision is intentionally volatile | `passive_notifications`; process-local revision map | PK; unique `(endpoint_id,idempotency_key)`; owner/created and unread partial indexes |
| External chat | `ExternalChatSessions`, `ExternalChatMessages`; legacy `WeixinChatSessions`, `WeixinChatMessages` | `external_chat_sessions`, `external_chat_messages`; compatibility `weixin_chat_sessions`, `weixin_chat_messages` | binding/chat, linked-session, external-message, and created-time indexes |
| Delivery record | `MessageReceives`, `MessageDeliveries`, `ChannelInboxUpdates` | `message_receive_records`, `message_delivery_records`, `channel_inbox_updates` | unique source/native, owner/actor/idempotency, and binding/external keys plus listing indexes |
| MCP | `MCPAccessTickets`, `MCPBindings`, `MCPOperations`; redemption also creates `Sessions` | `mcp_access_tickets`, `mcp_bindings`, `mcp_operations`; `sessions` | unique secret hash, active peer partial unique index, unique `(binding_id,idempotency_key)`, owner/status and update indexes |
| Browser state | `BrowserAuthRecords`, `BrowserLoginBlocks` | `browser_auth_records`, `browser_login_blocks` | auth lookup index; active block session/status/update index; session/run FKs |
| Memory | `MemoryCandidates`, `Memories` | `memory_candidates`, `memories` | PKs; accepted memory run FK and created-time index |
| Audit | `AuditEvents`, `Events` | `audit_events`, `events` | session/time and session/sequence indexes; unique event `id` |
| Evaluation | `EvalRuns` | `eval_runs` | PK and started-time index |
| Artifact metadata | `ArtifactObjects`; volatile URI-to-ID index is rebuilt on load | `artifact_objects` | PK; created, run, and Go-only URI indexes |

## Per-Repository Characterization Evidence

The accepted per-repository gate is represented by the complete 20 by 10
matrix below. `TestS0RepositoryCharacterizationMatrixCompleteness` is the
executable authority: it requires exactly these 20 repository rows and ten
dimensions, resolves every evidence reference to an exact test function and
file, rejects an unreasoned `N/A`, and requires one matching row in this
document. `TestS0BackendNeutralRepositorySuccessAndAbsence` runs a named
Memory/File subtest for every repository; the other evidence keys identify the
additional applicable contract.

| Repository | Success | Absence | Order/filter/scope | Clone/alias | Duplicate/idempotency | CAS/conflict/delete | Event/audit/sequence | File restart/snapshot | Concurrency/revision | PostgreSQL row/`rows.Err()` |
|---|---|---|---|---|---|---|---|---|---|---|
| `OwnerRepository` | `BASE` | `BASE` | `OWNER` | `CROSS` | `BASE` | N/A: no delete, CAS, or conflict command | `OWNER` | `CROSS` | N/A: no repository revision or idempotent-create result | `PG` + `PG-ROWS` |
| `ClientRepository` | `BASE` | `BASE` | `BASE` | `ALIAS` defect | `BASE` | `BASE` claim/revoke | `BASE` | `FILE-ALL` | N/A: claim/revoke expose state conflict, not a revision | `PG` + `PG-ROWS` |
| `ISCPOnboardingRepository` | `BASE` | `BASE` | `ISCP` | N/A: record has no mutable members | `BASE` unique-ID conflict | `BASE` conflict | N/A: lifecycle audit is caller-owned | `ISCP` | N/A: unique ID is the boundary; no revision | `ISCP-PG` + `PG-ROWS` |
| `CredentialRepository` | `BASE` | `BASE` | N/A: exact-ref lookup only | N/A: record has no mutable members | `BASE` ref overwrite | `BASE` delete | `BASE` audit | `CREDENTIAL-FILE` | N/A: no revision/idempotent-create result | N/A: QueryRow/Exec only; no row iterator |
| `SessionRepository` | `BASE` | `BASE` | `BASE` hidden filtering | N/A: record has no mutable members | N/A: create always allocates an ID | `BASE` + `BROWSER` delete | `BASE` | `CROSS` | N/A: no version/revision contract | `PG` + `BROWSER-PG` + `PG-ROWS` |
| `ConversationRepository` | `BASE` | `BASE` | `MESSAGE` | `ALIAS` defect | `BASE` message-ID reuse | N/A: append/cursor API has no update or delete | `CROSS` + `MESSAGE` | `MESSAGE-FILE` + `CROSS` | N/A: event sequence, not CAS revision, orders appends | `PG` |
| `RunRepository` | `BASE` | `BASE` | `RUN` | `ALIAS` defect | `RUN` feedback replacement | N/A: overwrite/append API has no delete or CAS | `RUN` | `FILE-ALL` + `RUN-FILE` | N/A: no caller-visible winner revision | `PG` + `PG-ROWS` |
| `DocumentRepository` | `BASE` + `CROSS` | `BASE` + `CROSS` | `DOCUMENT` | N/A: record has no mutable members | `DOCUMENT` ID overwrite | N/A: no delete, CAS, or conflict command | `DOCUMENT` | `DOCUMENT-FILE` | N/A: no revision/idempotent-create result | `PG` + `PG-ROWS` |
| `ApprovalRepository` | `BASE` | `BASE` | `APPROVAL` | `ALIAS` defect | `APPROVAL` external-ref identity | `APPROVAL` pending-state conflict | `APPROVAL` | `APPROVAL-FILE` | N/A: pending state, not numeric revision, is the precondition | `PG` + `PG-ROWS` |
| `ScheduleRepository` | `BASE` | `BASE` | `BASE` due ordering/filter | `ALIAS` defect | `BASE` ID overwrite | `SCHEDULE` CAS | `BASE` | `FILE-GAPS` | `SCHEDULE` CAS winner | `PG` + `PG-ROWS` |
| `ConnectorRepository` | `BASE` | `BASE` | `CONNECTOR` | `ALIAS` defect | `BASE` binding ID overwrite | `CONNECTOR` CAS | `CONNECTOR` | `CONNECTOR-FILE` + `FILE-GAPS` | `CONNECTOR` numeric revision | `CONNECTOR-PG` |
| `PassiveNotificationRepository` | `BASE` + `PASSIVE` | `BASE` + `PASSIVE` | `PASSIVE` | `ALIAS` defect | `PASSIVE` idempotent replay/conflict | `PASSIVE` prune/replay | `PASSIVE` revision/audit | `PASSIVE-FILE` | `PASSIVE` process-local revision | `PASSIVE-PG` + `PG-ROWS` |
| `ExternalChatRepository` | `BASE` + `EXTERNAL` | `BASE` | `EXTERNAL` | N/A: records have no mutable members | `EXTERNAL` ID/external-ID behavior | N/A: no delete, CAS, or typed conflict command | `EXTERNAL` | `EXTERNAL-FILE` | N/A: external-ID lookup is reconciliation, not CAS | `EXTERNAL-PG` + `PG-ROWS` |
| `DeliveryRecordRepository` | `BASE` + `DELIVERY` | `BASE` | `DELIVERY` + `EXTERNAL` | `ALIAS` defect | `DELIVERY` + `EXTERNAL` dedupe keys | N/A: lifecycle overwrite API has no delete/CAS | `DELIVERY` | `DELIVERY-FILE` + `EXTERNAL-FILE` | N/A: dedupe keys serialize writes without a numeric revision | `EXTERNAL-PG` + `PG-ROWS` |
| `MCPRepository` | `BASE` + `MCP` | `BASE` | `MCP` owner scope | `MCP-ALIAS` | `MCP` idempotency | `MCP` CAS/revoke/delete | N/A: lifecycle audit is caller-owned; operation version is the sequence | `MCP-FILE` + `CROSS` | `CROSS` concurrent idempotency/version | `MCP-PG` + `PG-ROWS` |
| `BrowserStateRepository` | `BASE` + `BROWSER` | `BASE` | `BROWSER` | `ALIAS` defect | `BROWSER` normalized duplicate ID | `BROWSER` CAS/delete | `BROWSER` | `BROWSER-FILE` + `FILE-ALL` | `BROWSER` numeric revision | `BROWSER-PG` |
| `MemoryRepository` | `BASE` + `MEMORY` | `BASE` | `MEMORY` | `ALIAS` defect | N/A: candidate create allocates an ID; resolution is a state transition | `MEMORY` delete/prune | `MEMORY` | `FILE-ALL` + `MEMORY-FILE` | N/A: state conflicts have no numeric revision | `PG` + `PG-ROWS` |
| `AuditRepository` | `BASE` | `BASE` | `BASE` session/order | `ALIAS` defect | N/A: append generates IDs and has no idempotency key | N/A: append-only, with no update/delete/CAS | `BASE` supplied audit plus ordered events | `MEMORY-FILE` | N/A: event cursor sequence is the only ordering token | `PG` + `PG-ROWS` |
| `EvaluationRepository` | `BASE` | `BASE` | `BASE` newest-first | `ALIAS` defect | `BASE` ID overwrite | N/A: no delete, CAS, or conflict command | `BASE` | `FILE-ALL` | N/A: no revision/idempotent-create result | `PG` + `PG-ROWS` |
| `ArtifactMetadataRepository` | `BASE` + `ARTIFACT` | `BASE` + `ARTIFACT` | `ARTIFACT` | N/A: record has no mutable members | `ARTIFACT` ID/URI replacement | `ARTIFACT` session-delete cleanup | `ARTIFACT` | `FILE-ALL` | N/A: ID overwrite atomically replaces URI index, with no revision | `PG` + `PG-ROWS` |

Evidence keys resolve to these exact tests and locations:

- `BASE`: [`TestS0BackendNeutralRepositorySuccessAndAbsence`](../services/gateway/internal/store/s0_repository_characterization_test.go), using the repository-named Memory and File subtest.
- `CROSS`: [`TestS0BackendNeutralContractCharacterization`](../services/gateway/internal/store/s0_contract_characterization_test.go).
- `ALIAS`: [`TestS0DefectEvidenceMutableAliases`](../services/gateway/internal/store/s0_repository_characterization_test.go), using the repository-named Memory and File subtest. It records current unsafe behavior; it is not a desired contract.
- `FILE-GAPS`: [`TestS0FileRepositoryRestartGaps`](../services/gateway/internal/store/s0_repository_characterization_test.go); `FILE-ALL`: [`TestFileStorePersistsAndReloadsState`](../services/gateway/internal/store/file_test.go); `PG-ROWS`: [`TestS0DefectEvidencePostgresRowsErrIsNotChecked`](../services/gateway/internal/store/s0_contract_characterization_test.go).
- `OWNER`: [`TestMemoryStoreUpdatesOwnerProfile`](../services/gateway/internal/store/memory_test.go), [`TestMemoryStoreManagesMultipleOwnerProfiles`](../services/gateway/internal/store/memory_test.go).
- `ISCP`: [`TestFileStorePersistsOnlyISCPOnboardingReceipt`](../services/gateway/internal/store/mcp_access_test.go); `ISCP-PG`: [`TestPostgresStorePersistsOnlyISCPOnboardingReceipt`](../services/gateway/internal/store/postgres_test.go).
- `CREDENTIAL-FILE`: [`TestFileStoreEncryptsStateAtRest`](../services/gateway/internal/store/file_test.go).
- `MESSAGE`: [`TestMemoryMessageEventsAreBoundedAndSessionScoped`](../services/gateway/internal/store/message_events_test.go); `MESSAGE-FILE`: [`TestFileMessageEventsSurviveRestart`](../services/gateway/internal/store/message_events_test.go).
- `RUN`: [`TestMemoryStoreSavesRunFeedback`](../services/gateway/internal/store/memory_test.go); `RUN-FILE`: [`TestFileStorePersistsWorkflowStateAndToolBinding`](../services/gateway/internal/store/file_test.go).
- `DOCUMENT`: [`TestMemoryStoreDocumentRecordsAreRecentAndSessionScoped`](../services/gateway/internal/store/memory_test.go); `DOCUMENT-FILE`: [`TestFileStorePersistsDocumentRecords`](../services/gateway/internal/store/file_test.go).
- `APPROVAL`: [`TestMemoryStoreFindsExternalApprovalByStableReference`](../services/gateway/internal/store/memory_test.go); `APPROVAL-FILE`: [`TestFileStorePersistsExternalApprovalContext`](../services/gateway/internal/store/file_test.go), [`TestFileStorePersistsPolicyExecutionContext`](../services/gateway/internal/store/file_test.go).
- `SCHEDULE`: [`TestMemoryStoreUpdatePendingReminderUsesCompareAndSwap`](../services/gateway/internal/store/memory_test.go).
- `CONNECTOR`: [`TestMemoryStoreConnectorSettingUsesCASAndOwnerScope`](../services/gateway/internal/store/connector_settings_test.go), [`TestMemoryStoreListsAllConnectorSettingsInStableOwnerChannelOrder`](../services/gateway/internal/store/connector_settings_test.go); `CONNECTOR-FILE`: [`TestFileStorePersistsConnectorSettingVersion`](../services/gateway/internal/store/connector_settings_test.go); `CONNECTOR-PG`: [`TestPostgresStoreListsAllConnectorSettings`](../services/gateway/internal/store/postgres_test.go).
- `PASSIVE`: [`TestMemoryStorePassiveNotificationIdempotencyAndOwnerScope`](../services/gateway/internal/store/passive_notifications_test.go), [`TestMemoryStorePassiveNotificationIdempotentReingestionAtScale`](../services/gateway/internal/store/passive_notifications_test.go), [`TestPrunePassiveNotificationsRetentionSweep`](../services/gateway/internal/store/passive_notifications_test.go), [`TestPrunePassiveNotificationsCapEvictsReadOldestFirst`](../services/gateway/internal/store/passive_notifications_test.go), [`TestPassiveNotificationRevisionSignalsInboxChanges`](../services/gateway/internal/store/passive_notifications_test.go); `PASSIVE-FILE`: [`TestFileStorePassiveNotificationSurvivesRestart`](../services/gateway/internal/store/passive_notifications_test.go), [`TestFileStoreSnapshotRebuildsPassiveNotificationIndex`](../services/gateway/internal/store/passive_notifications_test.go); `PASSIVE-PG`: [`TestPostgresStorePassiveNotificationPruneAndRevision`](../services/gateway/internal/store/postgres_test.go).
- `EXTERNAL`: [`TestMemoryStoreExternalChatAndInboxParity`](../services/gateway/internal/store/external_chat_test.go); `EXTERNAL-FILE`: [`TestFileStoreExternalChatAndInboxParity`](../services/gateway/internal/store/external_chat_test.go); `EXTERNAL-PG`: [`TestPostgresStoreExternalChatAndInboxParity`](../services/gateway/internal/store/postgres_test.go).
- `DELIVERY`: [`TestMemoryStoreMessageLifecycleParity`](../services/gateway/internal/store/message_lifecycle_test.go); `DELIVERY-FILE`: [`TestFileStoreMessageLifecycleRoundTrip`](../services/gateway/internal/store/message_lifecycle_test.go).
- `MCP`: [`TestMCPAccessTicketRedemptionIsAtomicAndDeviceBound`](../services/gateway/internal/store/mcp_access_test.go), [`TestMCPOperationIdempotencyRejectsChangedRequest`](../services/gateway/internal/store/mcp_access_test.go), [`TestMCPBindingRevocationTerminatesOnlyNonterminalOperations`](../services/gateway/internal/store/mcp_access_test.go), [`TestMCPAccessRecordsCanBeDeletedIndividuallyAndByOwner`](../services/gateway/internal/store/mcp_access_test.go); `MCP-ALIAS`: [`TestMemoryStoreMCPRecordsCannotBeMutatedOutsideStore`](../services/gateway/internal/store/mcp_access_test.go); `MCP-FILE`: [`TestFileStorePersistsMCPAccessWithoutPlaintextSecret`](../services/gateway/internal/store/mcp_access_test.go); `MCP-PG`: [`TestPostgresStoreMCPAccessAtomicityIdempotencyAndRecovery`](../services/gateway/internal/store/postgres_test.go).
- `BROWSER`: [`TestMemoryStoreScopesBrowserAuthRecordsByOwnerProfileAndSite`](../services/gateway/internal/store/memory_test.go), [`TestMemoryStoreFindActiveBrowserLoginBlockPicksNewestStoredUpdate`](../services/gateway/internal/store/memory_test.go), [`TestMemoryStoreBrowserLoginBlockTrimsIDOnWrite`](../services/gateway/internal/store/memory_test.go), [`TestMemoryStoreBrowserHandoffCASPreservesRevisionTwoFields`](../services/gateway/internal/store/memory_test.go), [`TestMemoryStoreDeleteSessionRemovesBrowserLoginBlocks`](../services/gateway/internal/store/memory_test.go); `BROWSER-FILE`: [`TestFileStoreBrowserHandoffCASRoundTrip`](../services/gateway/internal/store/file_test.go); `BROWSER-PG`: [`TestPostgresStoreBrowserHandoffCASRoundTrip`](../services/gateway/internal/store/postgres_test.go), [`TestPostgresStoreFindActiveBrowserLoginBlockMatchesSharedActivePredicate`](../services/gateway/internal/store/postgres_test.go), [`TestPostgresStoreDeleteSessionRemovesBrowserLoginBlocks`](../services/gateway/internal/store/postgres_test.go).
- `MEMORY`: [`TestMemoryStoreUpdatesAndDeletesAcceptedMemory`](../services/gateway/internal/store/memory_test.go), [`TestMemoryStorePrunesExpiredMemories`](../services/gateway/internal/store/memory_test.go); `MEMORY-FILE`: [`TestFileStorePersistsMemoryRetentionPrune`](../services/gateway/internal/store/file_test.go).
- `ARTIFACT`: [`TestMemoryStoreListsArtifactObjectsNewestFirst`](../services/gateway/internal/store/memory_test.go), [`TestMemoryStoreFindsArtifactObjectByURI`](../services/gateway/internal/store/memory_test.go).
- `PG`: [`TestPostgresStoreRoundTrip`](../services/gateway/internal/store/postgres_test.go). All PostgreSQL keys remain DSN-gated and are not counted as passed when the DSN is absent.

The `ALIAS` evidence currently records mutable alias escape for Client,
Conversation, Run, Approval, Schedule, Connector, PassiveNotification,
DeliveryRecord, BrowserState, Memory, Audit, and Evaluation records in both
Memory and the live File decorator. S0 does not repair those production
contracts. Each owning repository wave must replace its defect-evidence cell
with an isolation assertion.

## Production Consumer Matrix

The matrix includes both every production declaration that directly accepts or
stores `store.Store` and every named production-local interface whose method
set intersects `Store`. It covers constructor, field, helper, worker, adapter,
resolver, and assembly use. Repeated receiver methods use the composite listed
for their owning type.

`TestS0ProductionStoreConsumerInventory` scans all non-test Gateway Go files.
It freezes every direct `store.Store` declaration by file and enclosing symbol,
the Store-package helper, and every named local Store-compatible interface with
its flattened Store method set. A new, removed, or renamed declaration fails
the executable inventory and requires this matrix to be reviewed.

| Package / symbol | Kind | Minimum repository or consumer-owned composite |
|---|---|---|
| `agent.Runtime`, `NewRuntime`, `NewRuntimeWithContext` | constructor + field | Session + Conversation + Run + Document + Approval + BrowserState + Memory + Audit + ArtifactMetadata |
| `agent.toolExposureEngine`, `newToolExposureEngine` | helper field + constructor | Session + Run + Approval + Audit |
| `store.ArchiveToolObservation` | helper | ArtifactMetadata |
| `credential.Vault`, `credential.New` | constructor + field | Credential |
| `gateway.Server`, `New`, `NewWithTrace` | constructor + field | Owner + Client + Session + Conversation + Run + Approval + Schedule + Connector + PassiveNotification + ExternalChat + DeliveryRecord + MCP + Memory + Audit + Evaluation + ArtifactMetadata + Credential |
| `gateway.runHasPendingApproval` | helper | Approval |
| `happyapproval.Service`, `New` | polling worker | Approval |
| `iscpbridge.GatewayAdapter`, `NewGatewayAdapter` | adapter + field | Owner + Session + Conversation + Run + Approval + PassiveNotification + Audit |
| `iscppairing.Service`, `New` | service + field | ISCPOnboarding + Audit |
| `mcpaccess.Service`, `New` | service + field | MCP + Run + Approval + Audit |
| `mcpaccess.Provider`, `NewProvider` | provider + field | MCP + Run + Session + ExternalChat + Audit + ArtifactMetadata |
| `mcpaccess.updateOperationRecord`, `rejectPendingApprovals`, `finalizeRevokedOperations`, approval helpers | helpers | MCP + Run + Approval |
| `notification.SendWeixinText/Image/File/Typing` | helper entry points | Connector + Schedule + Credential |
| `notification.WeixinAdapter`, `NewWeixinAdapter` | adapter + optional field | Connector + Schedule + Credential + Session + ExternalChat + ArtifactMetadata |
| `reminder.Scheduler`, `NewMessageScheduler` | worker + field | Schedule |
| `remindertarget.Resolver`, `NewResolver` | resolver + field | ExternalChat + Connector |
| `telegram.Dispatcher`, `NewDispatcher` | worker + field | Owner + Session + Conversation + Run + Approval + Schedule + Connector + ExternalChat + DeliveryRecord + ArtifactMetadata + Audit |
| `telegram.Service`, `NewService`, `hasDefaultActiveBinding` | worker + helper | Connector |
| `telegram.NotificationAdapter`, `NewNotificationAdapter` | adapter + field | Connector + Schedule + Session + ExternalChat + ArtifactMetadata; credentials come from its separate `CredentialVault`, not its Store parameter |
| `toolhub.ToolHub`, `New` | constructor + field | Session + Run + Approval + Schedule + Connector + ExternalChat + Memory + Audit + ArtifactMetadata |
| `weixin.Dispatcher`, `NewDispatcher`, `NewDispatcherWithConfig` | worker + field | Owner + Session + Conversation + Run + Approval + Connector + ExternalChat + DeliveryRecord + ArtifactMetadata + Audit |
| `weixin.Syncer`, `NewSyncer`, `WithConfig` | polling worker + field + adapter assembly | Session + Connector + Credential + ExternalChat + DeliveryRecord + ArtifactMetadata + Audit |
| `weixin.MediaAdapter`, `NewMediaAdapter` | adapter + field | Session + ArtifactMetadata + Audit |
| `cmd/sparkclaw.newStore` | backend factory | all 20 repositories as one concrete backend result; no production consumer should receive this broad type after S4 |
| `cmd/sparkclaw.buildRuntime` / bootstrap assembly | assembly forwarding | Agent + ToolHub composites, ISCPOnboarding + Audit, and ArtifactMetadata |
| `cmd/sparkclaw.buildConnectors` | assembly forwarding | Connector + Credential + ExternalChat + DeliveryRecord + Session + Conversation + Owner + Run + Approval + Schedule + ArtifactMetadata + Audit |

The production-local Store-compatible consumers are:

| Package / symbol | Kind | Accepted repository or composite |
|---|---|---|
| `connector.Registry`, `connectorStore`, `NewRegistry` | field + local interface + constructor | Connector |
| `messagecontrol.EndpointRegistry`, `endpointStore`, `NewEndpointRegistry` | field + local interface + constructor | Session + Connector + ExternalChat |
| `messagecontrol.mcpEndpointStore` in `EndpointRegistry.get` | optional local type assertion | MCP; current optional discovery is recorded behavior and must become an explicit consumer-owned composite during migration |
| `messagecontrol.ScheduleRegistry`, `scheduleStore`, `NewScheduleRegistry` | field + local interface + constructor | Schedule + Session + Connector + ExternalChat |
| `messagecontrol.ReceiveLifecycle`, `receiveStore`, `NewReceiveLifecycle` | field + local interface + constructor | DeliveryRecord |
| `delivery.PersistentWebDelivery`, `webMessageStore`, `NewPersistentWebDelivery` | field + local interface + constructor | Conversation |
| `delivery.EndpointResourceResolver`, `endpointResourceStore`, `NewEndpointResourceResolver` | resolver + local composite + constructor | Session + ArtifactMetadata |
| `delivery.StoreResourceResolver`, `artifactStore`, `NewStoreResourceResolver` | resolver + local interface + constructor | ArtifactMetadata |
| `delivery.ResolveBrowserContent`, `governedArtifactStore` | helper + local composite | Session + ArtifactMetadata |
| `delivery.RecordExternalDelivery`, `externalDeliveryStore` | helper + local composite | ExternalChat |

No other direct or named local Store-compatible production declaration exists.
`delivery.EndpointRegistry` and `delivery.WebDelivery` are delivery-domain
interfaces rather than Store-compatible interfaces. `artifact.Store` is a
separate artifact-object interface and is not part of this migration.

## Mutation And Command Matrix

`M`, `F`, and `P` mean Memory lock mutation, File atomic replacement, and
PostgreSQL respectively. `F rename` is the future durable submission point;
today 48 legacy File call sites discard the result. `P Exec` is the autocommit
submission point and `P Commit` is the transaction submission point; today 33
PostgreSQL `Exec` results are explicitly discarded. Required lifecycle rows
must join the same future transaction even where PostgreSQL currently appends
them afterward and ignores failure.

| Repository / command | Record and derived-index mutation | Required event/audit | Atomic boundary and effect submission | Idempotency / CAS | Reconciliation read |
|---|---|---|---|---|---|
| Session `CreateSession*` | session | `session.created` audit + event | session + lifecycle; M lock / F rename / P Exec | generated ID | `GetSession` |
| Session `UpdateSessionTitle` | session title/update time | `session.updated` audit + event | session + lifecycle; M lock / F rename / P Exec | current existence | `GetSession` |
| Session `DeleteSession` | session plus messages, runs, feedback, model/tool calls, documents, approvals, reminders/deliveries, candidates/memories, browser blocks, artifacts/URI index, episode summaries, linked external chat, old session audit/events | replacement `session.deleted` audit + event | one cross-repository transaction; M lock / F rename / P Commit | target existence | `GetSession` absent plus scoped list reads |
| Client `SaveClient` / `RevokeClient` | client and token lookup state | `client.saved` / `client.revoked` audit + event | record + lifecycle; M lock / F rename / P Exec | ID overwrite; revoke requires target | `GetClient`, `FindClientByTokenHash` |
| Client `TouchClient` | last-seen | none | one record; M lock / F rename / P Exec | missing target is current no-op | `GetClient` |
| Client `SavePairingCode` / `ClaimPairingCode` | pairing code status, claim time, client link | created/claimed audit + event | record + lifecycle; M lock / F rename / P Commit for claim state check | pending + expiry state | `GetPairingCode` |
| Owner `SaveOwnerProfile` / `UpdateOwnerProfile` | owner map/default owner | `owner_profile.updated` audit + event | record + lifecycle; M lock / F rename / P Exec | ID overwrite | `GetOwnerProfileByID` |
| ISCP `SaveISCPOnboarding` | onboarding receipt | caller currently writes audit; repository owns no lifecycle row | one receipt; M lock / F rename with rollback / P Exec | unique ID -> `ErrISCPOnboardingConflict` | `GetISCPOnboarding` |
| MCP `SaveMCPAccessTicket` | ticket and secret-hash lookup | caller audit | one ticket; M lock / F rename with rollback / P Exec | unique ID/secret hash | `GetMCPAccessTicket`, secret-hash lookup |
| MCP `RedeemMCPAccessTicket` | ticket consumed, binding created, visible linked session created | caller audit | all three records; M lock / F rename with rollback / P Commit | single-use ticket + peer identity | ticket lookup + `FindMCPBindingForPeer` + `GetSession` |
| MCP ticket revoke/delete | ticket status or removal | caller audit | one ticket; M lock / F rename with rollback / P Exec/Commit | current state and owner scope | `GetMCPAccessTicket` |
| MCP `RevokeMCPBinding` | binding plus every nonterminal operation | caller audit | binding + operation set; M lock / F rename with rollback / P Commit | terminal operations unchanged | `GetMCPBinding`, `ListMCPOperations` |
| MCP binding/access deletion | binding/ticket/operation sets | caller audit | complete owner/binding set; M lock / F rename with rollback / P Commit | owner scope | list tickets/bindings/operations |
| MCP `TouchMCPBinding` | latest ISCP session and last-used/update times | none | one active binding; M lock / F rename with rollback / P Exec | active-state precondition | `GetMCPBinding` |
| MCP `CreateMCPOperation` | operation and `(binding,idempotency)` lookup | none | one operation; M lock / F rename with rollback / P Exec | same fingerprint reuses; changed fingerprint conflicts | idempotency lookup |
| MCP `UpdateMCPOperation` | operation/version | none | one operation; M lock / F rename with rollback / P CAS Exec | expected version | `GetMCPOperation` |
| Conversation `AddMessage` | message list, visible session title/update time | `message.created` event | message + session + event; M lock / F rename / P Commit | duplicate message ID reuses current record | `ListMessages`, `MessageEventHead` |
| Run `SaveRunFeedback` | feedback by run/message | feedback audit + event | feedback + lifecycle; M lock / F rename / P Exec | same ID or message replaces | `ListRunFeedback` |
| Run `SaveRun` | run | `run.<state>` event | run + event; M lock / F rename / P Exec | ID overwrite | `GetRun` |
| Run `SaveModelCall` / `SaveToolCall` | call record | status audit + event | call + lifecycle; M lock / F rename / P Exec | ID overwrite | model/tool list or `GetToolCall` |
| Run `SaveEpisodeSummary` | episode summary | saved audit + event | summary + lifecycle; M lock / F rename / P Exec | ID overwrite | `ListEpisodeSummaries` |
| Document `SaveDocumentRecord` | document record | `document.saved` audit + event | record + lifecycle; M lock / F rename / P Exec | ID overwrite; preserve created time | `GetDocumentRecord` |
| Approval `SaveApproval` | approval and external-ref lookup | status audit + event | approval + lifecycle; M lock / F rename / P Exec | partial unique external ref in P | ID/external-ref lookup |
| Approval `UpdatePendingApproval` | pending approval body | `approval.pending` event | approval + event; M lock / F rename / P CAS-like Exec | target must remain pending | `GetApproval` |
| Approval `ResolveApproval` | terminal status, note, time | terminal audit + event | approval + lifecycle; M lock / F rename / P Exec | pending-state precondition | `GetApproval` |
| Schedule `SaveReminder` / `UpdatePendingReminder` | reminder | status audit + event | reminder + lifecycle; M lock / F rename / P Exec | update uses exact `UpdatedAt` CAS | `GetReminder` |
| Schedule `ClaimDueReminders` | ordered due/stale reminders -> sending | none | claimed set; M lock / F rename / P atomic claim query | status/time lease | `GetReminder`, filtered list |
| Schedule `SaveReminderDelivery` | delivery plus reminder status/attempt/error/last delivery | delivery audit + event | delivery + reminder + lifecycle; M lock / F rename / P transaction required | delivery ID overwrite | delivery list + `GetReminder` |
| Connector `UpdateConnectorSetting` | `(owner,channel)` setting/version | enable-state audit + event | setting + lifecycle; M lock / F rename / P Commit | expected numeric version | `GetConnectorSetting` |
| Connector binding save/revoke | binding | status audit + event | binding + lifecycle; M lock / F rename / P Exec | ID overwrite; revoke target exists | `GetNotificationBinding` |
| Passive `CreatePassiveNotification` | record, dedupe key, owner revision | received audit | record + revision/audit; M lock / F rename / P Exec | same fingerprint reuses; changed fingerprint conflicts | owner/id lookup |
| Passive mark-read / mark-all | read time and owner revision | none | affected owner rows + revision; M lock / F rename / P Exec | already-read is stable | get/list/count |
| Passive `PrunePassiveNotifications` | retained rows, dedupe window, owner revisions | per-owner prune audit | full prune result + audit; M lock / F rename / P transaction required | cutoff/cap deterministic | owner list/count |
| External chat session/message saves | record and provider lookup keys | status audit + event | record + lifecycle; M lock / F rename / P Exec | ID overwrite; caller performs external-ID lookup | exact get/find methods |
| Delivery receive save | receive record and source/native key | receive audit | record + audit; M lock / F rename / P Exec | unique source/native | `FindMessageReceive` |
| Delivery send save | delivery record and owner/actor/idempotency key | send audit | record + audit; M lock / F rename / P Exec | unique owner/actor/key; content digest checked by caller | idempotency lookup |
| Delivery inbox update save | update and binding/external key | none | one update; M lock / F rename / P Exec | unique binding/external | `FindChannelInboxUpdate` |
| Credential save/delete | secret record | saved/deleted audit | secret + audit; M lock / F rename / P Exec | ref overwrite/delete | `GetCredentialSecret` |
| Browser auth save/revoke | auth record and lookup fields | saved/revoked audit + event | record + lifecycle; M lock / F rename / P Exec | ID; revoke target exists | get/find lookup |
| Browser login block save/update | block/version and active lookup | status audit + event | block + lifecycle; M lock / F rename / P Exec | update expected version | `GetBrowserLoginBlock`, active lookup |
| Memory candidate add/resolve | candidate; accepted resolve may create memory | candidate audit + event | candidate + optional memory + lifecycle; M lock / F rename / P transaction required | pending-state precondition | candidate list + memory search |
| Memory update/delete/prune | accepted memory set | action audit + event | record/set + lifecycle; M lock / F rename / P transaction required | target existence; cutoff | search plus exact ID in returned set |
| Audit `AddAudit` | audit append | the supplied audit is the record | one append; M lock / F rename / P Exec | ID generated if absent | `ListAudit` |
| Evaluation `SaveEvalRun` | eval run | status audit + event | run + lifecycle; M lock / F rename / P Exec | ID overwrite | `GetEvalRun` |
| Artifact `SaveArtifactObject` | object plus URI-to-ID derived index | saved audit + event | object + lifecycle/index; M lock / F rename / P Exec | ID overwrite and index replacement | `FindArtifactObjectByURI` |

## PostgreSQL Reconciliation Manifest

The complete constraint-aware comparison, parser coverage, and explicit
not-covered categories are recorded in the
[PostgreSQL reconciliation manifest](store-s0-postgresql-reconciliation-manifest.md).
The executable test compares normalized definitions, not only counts.

The root migration has 18 tables and 16 indexes. `postgresSchema` has all of
them plus 19 tables and 26 indexes. It also adds 20 columns to six common
tables. There is no root-only table, column, or index.

| Common table | Columns present only in `postgresSchema` |
|---|---|
| `owners` | `source`, `external_ref`, `workspace_root`, `default_channel`, `default_binding_id` |
| `clients` | `owner_id`, `actor_id` |
| `sessions` | `owner_id`, `workspace_root`, `source`, `hidden` |
| `messages` | `attachments`, `requested_media` |
| `tool_calls` | `workflow_id`, `workflow_node_id`, `scope_revision`, `capability`, `error_code`, `policy_context` |
| `approvals` | `policy_context` |

Go-only tables are `iscp_onboardings`, `mcp_access_tickets`, `mcp_bindings`,
`mcp_operations`, `document_records`, `reminder_deliveries`,
`connector_settings`, `notification_bindings`, `weixin_chat_sessions`,
`weixin_chat_messages`, `external_chat_sessions`, `external_chat_messages`,
`message_receive_records`, `message_delivery_records`,
`channel_inbox_updates`, `passive_notifications`, `credential_secrets`,
`browser_auth_records`, and `browser_login_blocks`.

Go-only indexes are `owners_external_ref_idx`,
`iscp_onboardings_owner_created_idx`, `mcp_access_tickets_owner_status_idx`,
`mcp_bindings_active_peer_idx`, `mcp_bindings_owner_status_idx`,
`mcp_operations_binding_updated_idx`,
`document_records_owner_session_activity_idx`,
`document_records_session_path_idx`, `reminder_deliveries_reminder_id_idx`,
`notification_bindings_channel_status_idx`,
`weixin_chat_sessions_binding_user_idx`,
`weixin_chat_sessions_linked_session_idx`,
`weixin_chat_messages_external_idx`, `weixin_chat_messages_chat_created_idx`,
`external_chat_sessions_binding_chat_idx`,
`external_chat_sessions_linked_session_idx`,
`external_chat_messages_external_idx`,
`external_chat_messages_chat_created_idx`,
`message_receive_owner_actor_idx`, `message_delivery_owner_actor_idx`,
`channel_inbox_updates_ready_idx`,
`passive_notifications_owner_created_idx`,
`passive_notifications_owner_unread_idx`, `browser_auth_lookup_idx`,
`browser_login_blocks_active_idx`, and `idx_artifact_objects_uri`.

Constraint reconciliation must retain the Go-only unique contracts for MCP
ticket secret hash, active MCP peer, MCP operation idempotency, connector
setting composite primary key, receive source/native ID, delivery
owner/actor/idempotency, inbox binding/external ID, and passive notification
endpoint/idempotency. The S1 migration must also retain existing FKs and the
partial approval external-ref uniqueness. The compatibility Weixin tables and
their copy-forward statements are deliberate migration inputs, not new
repository owners.

For shared objects, complete column definitions (type, inline PK/FK/UNIQUE,
CHECK, default, and nullability), table-level constraints, and full index
definitions are identical except for the 20 explicitly listed Go-only columns.
There are no CHECK constraints, named constraints, table-level FKs, or
constraint-altering DDL statements in either source; these are parsed empty
categories. Five Go-side DML statements, generated constraint names, deployed
database state, and `IF NOT EXISTS` adoption behavior are explicitly not
semantically covered and are not claims of “no difference.”

## S2 Pilot Decision

`ISCPOnboardingRepository` is the selected S2 pilot.

- It has only three methods and one business service consumer
  (`iscppairing.Service`); the second dependency is the caller-owned audit
  write.
- `SaveISCPOnboarding` already returns an error and maps duplicate PostgreSQL
  ID to `ErrISCPOnboardingConflict`.
- File already holds its mutex, returns `persistSnapshotLocked` failure, and
  removes the tentative receipt on failure. That is a narrow starting point
  for replacing hand-written rollback with the S2 full-snapshot state machine.
- Its state is one Snapshot field, one PostgreSQL table, and one owner/time
  index. It stores a non-secret receipt only.
- Its effect submission is one durable receipt and its reconciliation read is
  exactly `GetISCPOnboarding(id)`.

MCP was rejected as pilot because 19 methods cross tickets, bindings,
operations, linked sessions, approval/run helpers, multiple consumers,
idempotency, CAS, and multi-record revocation. It remains a later repository
wave after the File gate is proven.

## Recorded Defect Evidence

S0 intentionally does not fix these behaviors:

- 48 File mutation paths call `persist()`; `persist()` executes
  `_ = s.persistSnapshot()`. A caller can receive success after a failed
  snapshot, and later success may persist tentative state.
- PostgreSQL source contains 33 explicit discarded `Exec` results. The generic
  `collectRows` path and eight named list/prune functions do not propagate
  scan/iteration completion failure; there are 10 row loops without a matching
  `rows.Err()` check.
- Many PostgreSQL lookups translate every scan/decode failure to `found=false`,
  and many list methods translate query failure to an empty list.
- PostgreSQL lifecycle appends are commonly outside the command transaction
  and their errors are discarded.
- Twelve repository record families expose mutable aliases from Memory and the
  live File decorator, so callers can mutate retained maps, slices, or pointers
  without another Store command; the guarded evidence matrix names every owner.

The characterization tests label these as defect evidence. Each owning S1-S3
migration must replace its evidence assertion with a failure-contract test.

## Links

- [Store contract foundation](store-contract-foundation-design.md)
- [Store reliability roadmap](store-contract-reliability-migration-design.md)
- [S0 acceptance report](store-s0-acceptance-report.md)
- [S0 PostgreSQL reconciliation manifest](store-s0-postgresql-reconciliation-manifest.md)
- [PostgreSQL schema design](store-postgresql-schema-config-design.md)
- [File durability design](store-file-durability-design.md)
