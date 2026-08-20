# Store S0 Contract Inventory

> Language: English | [简体中文](../zh-cn/docs/store-s0-contract-inventory.md)

> Status: accepted S0 inventory at
> `207462154fa2377ed786af671f41e0f353d11ba9`, 2026-08-20. S1 owns any
> production schema, configuration, or Store behavior change.

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
dimensions, resolves every `Test[/subtest]@file.go` token through the complete
subtest path, rejects an unreasoned `N/A`, and verifies every English and
Chinese cell against the executable matrix. Each named dimension key binds to
its own assertion branch and runs on Memory and File; a repository-wide check
is not replayed under multiple dimension names. Focused tests remain linked
where they capture a richer contract or current defect.

| Repository | Success | Absence | Order/filter/scope | Clone/alias | Duplicate/idempotency | CAS/conflict/delete | Event/audit/sequence | File restart/snapshot | Concurrency/revision | PostgreSQL row/`rows.Err()` |
|---|---|---|---|---|---|---|---|---|---|---|
| `ApprovalRepository` | `TestS0BackendNeutralRepositoryCharacterization/ApprovalRepository/success@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ApprovalRepository/normal_absence@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ApprovalRepository/ordering_filtering_scope@s0_repository_characterization_test.go` | `TestS0DefectEvidenceMutableAliases/ApprovalRepository@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ApprovalRepository/duplicate_idempotency@s0_repository_characterization_test.go` | `TestMemoryStoreFindsExternalApprovalByStableReference@memory_test.go` | `TestS0BackendNeutralRepositoryLifecycleEvidence/ApprovalRepository@s0_repository_lifecycle_test.go` | `TestFileStorePersistsExternalApprovalContext@file_test.go`<br>`TestFileStorePersistsPolicyExecutionContext@file_test.go` | N/A: Approval uses pending-state conflict rather than a numeric revision; no concurrent winner is exposed beyond that state precondition. | `TestPostgresStoreRoundTrip@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/shared/collectRows@s0_contract_characterization_test.go` |
| `ArtifactMetadataRepository` | `TestS0BackendNeutralRepositoryCharacterization/ArtifactMetadataRepository/success@s0_repository_characterization_test.go`<br>`TestMemoryStoreFindsArtifactObjectByURI@memory_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ArtifactMetadataRepository/normal_absence@s0_repository_characterization_test.go`<br>`TestMemoryStoreFindsArtifactObjectByURI@memory_test.go` | `TestMemoryStoreListsArtifactObjectsNewestFirst@memory_test.go`<br>`TestMemoryStoreFindsArtifactObjectByURI@memory_test.go` | N/A: ArtifactObject contains only scalar and time values, so no mutable alias can cross the Store boundary. | `TestMemoryStoreFindsArtifactObjectByURI@memory_test.go` | `TestMemoryStoreFindsArtifactObjectByURI@memory_test.go` | `TestS0BackendNeutralRepositoryLifecycleEvidence/ArtifactMetadataRepository@s0_repository_lifecycle_test.go` | `TestFileStorePersistsAndReloadsState@file_test.go` | N/A: Artifact metadata exposes no revision or idempotent-create result; ID overwrite atomically replaces the URI index entry. | `TestPostgresStoreRoundTrip@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/shared/collectRows@s0_contract_characterization_test.go` |
| `AuditRepository` | `TestS0BackendNeutralRepositoryCharacterization/AuditRepository/success@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/AuditRepository/normal_absence@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/AuditRepository/ordering_filtering_scope@s0_repository_characterization_test.go` | `TestS0DefectEvidenceMutableAliases/AuditRepository@s0_repository_characterization_test.go` | N/A: AddAudit is append-only and generates an ID when absent; it exposes no caller idempotency key or duplicate-ID result. | N/A: Audit is append-only and exposes no update, delete, CAS, or conflict command. | `TestS0BackendNeutralRepositoryCharacterization/AuditRepository/event_audit_sequence@s0_repository_characterization_test.go` | `TestFileStorePersistsMemoryRetentionPrune@file_test.go` | N/A: Audit append exposes no revision; event cursors are ordered sequence reads owned by this repository. | `TestPostgresStoreRoundTrip@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/shared/collectRows@s0_contract_characterization_test.go` |
| `BrowserStateRepository` | `TestS0BackendNeutralRepositoryCharacterization/BrowserStateRepository/success@s0_repository_characterization_test.go`<br>`TestMemoryStoreScopesBrowserAuthRecordsByOwnerProfileAndSite@memory_test.go`<br>`TestMemoryStoreTracksActiveBrowserLoginBlock@memory_test.go` | `TestS0BackendNeutralRepositoryCharacterization/BrowserStateRepository/normal_absence@s0_repository_characterization_test.go` | `TestMemoryStoreScopesBrowserAuthRecordsByOwnerProfileAndSite@memory_test.go`<br>`TestMemoryStoreFindActiveBrowserLoginBlockPicksNewestStoredUpdate@memory_test.go` | `TestS0DefectEvidenceMutableAliases/BrowserStateRepository@s0_repository_characterization_test.go` | `TestMemoryStoreBrowserLoginBlockTrimsIDOnWrite@memory_test.go` | `TestMemoryStoreBrowserHandoffCASPreservesRevisionTwoFields@memory_test.go`<br>`TestMemoryStoreDeleteSessionRemovesBrowserLoginBlocks@memory_test.go` | `TestS0BackendNeutralRepositoryLifecycleEvidence/BrowserStateRepository@s0_repository_lifecycle_test.go` | `TestFileStoreBrowserHandoffCASRoundTrip@file_test.go`<br>`TestFileStorePersistsAndReloadsState@file_test.go` | `TestMemoryStoreBrowserHandoffCASPreservesRevisionTwoFields@memory_test.go` | `TestPostgresStoreBrowserHandoffCASRoundTrip@postgres_test.go`<br>`TestPostgresStoreFindActiveBrowserLoginBlockMatchesSharedActivePredicate@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/shared/collectRows@s0_contract_characterization_test.go` |
| `ClientRepository` | `TestS0BackendNeutralRepositoryCharacterization/ClientRepository/success@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ClientRepository/normal_absence@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ClientRepository/ordering_filtering_scope@s0_repository_characterization_test.go` | `TestS0DefectEvidenceMutableAliases/ClientRepository@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ClientRepository/duplicate_idempotency@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ClientRepository/CAS_conflict_deletion@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryLifecycleEvidence/ClientRepository@s0_repository_lifecycle_test.go` | `TestFileStorePersistsAndReloadsState@file_test.go` | N/A: Client and pairing commands expose no CAS/revision result; claim and revoke conflicts are covered separately from scheduling concurrency. | `TestPostgresStoreRoundTrip@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/shared/collectRows@s0_contract_characterization_test.go` |
| `ConnectorRepository` | `TestS0BackendNeutralRepositoryCharacterization/ConnectorRepository/success@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ConnectorRepository/normal_absence@s0_repository_characterization_test.go` | `TestMemoryStoreListsAllConnectorSettingsInStableOwnerChannelOrder@connector_settings_test.go` | `TestS0DefectEvidenceMutableAliases/ConnectorRepository@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ConnectorRepository/duplicate_idempotency@s0_repository_characterization_test.go` | `TestMemoryStoreConnectorSettingUsesCASAndOwnerScope@connector_settings_test.go` | `TestS0BackendNeutralRepositoryLifecycleEvidence/ConnectorRepository@s0_repository_lifecycle_test.go` | `TestFileStorePersistsConnectorSettingVersion@connector_settings_test.go`<br>`TestS0FileRepositoryRestartGaps/ConnectorRepository@s0_repository_characterization_test.go` | `TestMemoryStoreConnectorSettingUsesCASAndOwnerScope@connector_settings_test.go` | `TestPostgresStoreListsAllConnectorSettings@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/shared/collectRows@s0_contract_characterization_test.go` |
| `ConversationRepository` | `TestS0BackendNeutralRepositoryCharacterization/ConversationRepository/success@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ConversationRepository/normal_absence@s0_repository_characterization_test.go` | `TestMemoryMessageEventsAreBoundedAndSessionScoped@message_events_test.go` | `TestS0DefectEvidenceMutableAliases/ConversationRepository@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ConversationRepository/duplicate_idempotency@s0_repository_characterization_test.go` | N/A: Conversation exposes append/idempotent message reuse and cursor validation, but no update, delete, or CAS command. | `TestS0BackendNeutralRepositoryLifecycleEvidence/ConversationRepository@s0_repository_lifecycle_test.go`<br>`TestMemoryMessageEventsAreBoundedAndSessionScoped@message_events_test.go` | `TestFileMessageEventsSurviveRestart@message_events_test.go`<br>`TestS0BackendNeutralContractCharacterization/file/restart@s0_contract_characterization_test.go` | N/A: Message append has no repository revision; event sequence ordering, not a caller-visible CAS version, is its concurrency contract. | `TestPostgresStoreRoundTrip@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/shared/collectRows@s0_contract_characterization_test.go` |
| `CredentialRepository` | `TestS0BackendNeutralRepositoryCharacterization/CredentialRepository/success@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/CredentialRepository/normal_absence@s0_repository_characterization_test.go` | N/A: CredentialRepository has exact-ref get only and no list, filter, ordering, or owner-scope query. | N/A: CredentialSecret contains only scalar and time values, so no mutable alias can cross the Store boundary. | `TestS0BackendNeutralRepositoryCharacterization/CredentialRepository/duplicate_idempotency@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/CredentialRepository/CAS_conflict_deletion@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryLifecycleEvidence/CredentialRepository@s0_repository_lifecycle_test.go` | `TestFileStoreEncryptsStateAtRest@file_test.go` | N/A: Credential save/delete has no CAS, revision, or idempotent-create result beyond serialized ref overwrite. | N/A: PostgreSQL credential operations use Exec or QueryRow only; this repository has no multi-row iterator and therefore no rows.Err path. |
| `DeliveryRecordRepository` | `TestS0BackendNeutralRepositoryCharacterization/DeliveryRecordRepository/success@s0_repository_characterization_test.go`<br>`TestMemoryStoreMessageLifecycleParity@message_lifecycle_test.go` | `TestS0BackendNeutralRepositoryCharacterization/DeliveryRecordRepository/normal_absence@s0_repository_characterization_test.go` | `TestMemoryStoreMessageLifecycleParity@message_lifecycle_test.go`<br>`TestMemoryStoreExternalChatAndInboxParity@external_chat_test.go` | `TestS0DefectEvidenceMutableAliases/DeliveryRecordRepository@s0_repository_characterization_test.go` | `TestMemoryStoreMessageLifecycleParity@message_lifecycle_test.go`<br>`TestMemoryStoreExternalChatAndInboxParity@external_chat_test.go` | N/A: Delivery records expose lifecycle overwrite and idempotency lookup but no delete or caller-visible CAS command. | `TestS0BackendNeutralRepositoryLifecycleEvidence/DeliveryRecordRepository@s0_repository_lifecycle_test.go` | `TestFileStoreMessageLifecycleRoundTrip@message_lifecycle_test.go`<br>`TestFileStoreExternalChatAndInboxParity@external_chat_test.go` | N/A: Delivery records expose no numeric revision; source/native and owner/actor/idempotency keys are the serialized dedupe boundary. | `TestPostgresStoreExternalChatAndInboxParity@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/DeliveryRecordRepository/ListMessageReceives@s0_contract_characterization_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/DeliveryRecordRepository/ListMessageDeliveries@s0_contract_characterization_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/shared/collectRows@s0_contract_characterization_test.go` |
| `DocumentRepository` | `TestS0BackendNeutralRepositoryCharacterization/DocumentRepository/success@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/DocumentRepository/normal_absence@s0_repository_characterization_test.go` | `TestMemoryStoreDocumentRecordsAreRecentAndSessionScoped@memory_test.go` | N/A: DocumentRecord contains only scalar and time values, so no mutable alias can cross the Store boundary. | `TestS0BackendNeutralRepositoryCharacterization/DocumentRepository/duplicate_idempotency@s0_repository_characterization_test.go` | N/A: Document records expose overwrite-by-ID but no delete, CAS, or conflict command. | `TestS0BackendNeutralRepositoryLifecycleEvidence/DocumentRepository@s0_repository_lifecycle_test.go` | `TestFileStorePersistsDocumentRecords@file_test.go` | N/A: Document records expose no revision or idempotent-create result; writes are serialized ID overwrites. | `TestPostgresStoreRoundTrip@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/shared/collectRows@s0_contract_characterization_test.go` |
| `EvaluationRepository` | `TestS0BackendNeutralRepositoryCharacterization/EvaluationRepository/success@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/EvaluationRepository/normal_absence@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/EvaluationRepository/ordering_filtering_scope@s0_repository_characterization_test.go` | `TestS0DefectEvidenceMutableAliases/EvaluationRepository@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/EvaluationRepository/duplicate_idempotency@s0_repository_characterization_test.go` | N/A: Evaluation runs expose overwrite-by-ID but no delete, CAS, or conflict command. | `TestS0BackendNeutralRepositoryLifecycleEvidence/EvaluationRepository@s0_repository_lifecycle_test.go` | `TestFileStorePersistsAndReloadsState@file_test.go` | N/A: Evaluation runs expose no revision or idempotent-create result; writes are serialized ID overwrites. | `TestPostgresStoreRoundTrip@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/shared/collectRows@s0_contract_characterization_test.go` |
| `ExternalChatRepository` | `TestS0BackendNeutralRepositoryCharacterization/ExternalChatRepository/success@s0_repository_characterization_test.go`<br>`TestMemoryStoreExternalChatAndInboxParity@external_chat_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ExternalChatRepository/normal_absence@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ExternalChatRepository/ordering_filtering_scope@s0_repository_characterization_test.go` | N/A: ExternalChatSession and ExternalChatMessage contain only scalar and time values, so no mutable alias can cross the Store boundary. | `TestMemoryStoreExternalChatAndInboxParity@external_chat_test.go` | N/A: External chat records expose overwrite-by-ID but no delete, CAS, or typed conflict command. | `TestS0BackendNeutralRepositoryLifecycleEvidence/ExternalChatRepository@s0_repository_lifecycle_test.go` | `TestFileStoreExternalChatAndInboxParity@external_chat_test.go` | N/A: External chat saves expose no revision or idempotent-create result; external-ID lookup is caller reconciliation, not CAS. | `TestPostgresStoreExternalChatAndInboxParity@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/shared/collectRows@s0_contract_characterization_test.go` |
| `ISCPOnboardingRepository` | `TestS0BackendNeutralRepositoryCharacterization/ISCPOnboardingRepository/success@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ISCPOnboardingRepository/normal_absence@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ISCPOnboardingRepository/ordering_filtering_scope@s0_repository_characterization_test.go` | N/A: ISCPOnboarding contains only scalar and time values, so no mutable alias can cross the Store boundary. | `TestS0BackendNeutralRepositoryCharacterization/ISCPOnboardingRepository/duplicate_idempotency@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ISCPOnboardingRepository/CAS_conflict_deletion@s0_repository_characterization_test.go` | N/A: The repository intentionally owns no lifecycle event; iscppairing writes the caller-owned audit record. | `TestFileStorePersistsOnlyISCPOnboardingReceipt@mcp_access_test.go` | N/A: The unique-ID conflict is the concurrency boundary; there is no revision or compare-and-swap field. | `TestPostgresStorePersistsOnlyISCPOnboardingReceipt@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/ISCPOnboardingRepository/ListISCPOnboardings@s0_contract_characterization_test.go` |
| `MCPRepository` | `TestS0BackendNeutralRepositoryCharacterization/MCPRepository/success@s0_repository_characterization_test.go`<br>`TestMCPAccessTicketRedemptionIsAtomicAndDeviceBound@mcp_access_test.go` | `TestS0BackendNeutralRepositoryCharacterization/MCPRepository/normal_absence@s0_repository_characterization_test.go` | `TestMCPAccessRecordsCanBeDeletedIndividuallyAndByOwner@mcp_access_test.go` | `TestMemoryStoreMCPRecordsCannotBeMutatedOutsideStore@mcp_access_test.go`<br>`TestS0BackendNeutralContractCharacterization/file/idempotency_cas_alias@s0_contract_characterization_test.go` | `TestMCPOperationIdempotencyRejectsChangedRequest@mcp_access_test.go` | `TestMCPBindingRevocationTerminatesOnlyNonterminalOperations@mcp_access_test.go`<br>`TestMCPAccessRecordsCanBeDeletedIndividuallyAndByOwner@mcp_access_test.go`<br>`TestS0BackendNeutralContractCharacterization/memory/idempotency_cas_alias@s0_contract_characterization_test.go`<br>`TestS0BackendNeutralContractCharacterization/file/idempotency_cas_alias@s0_contract_characterization_test.go` | N/A: MCP lifecycle audit is explicitly caller-owned; operation sequence is represented by versioned state rather than Store-created events. | `TestFileStorePersistsMCPAccessWithoutPlaintextSecret@mcp_access_test.go`<br>`TestS0BackendNeutralContractCharacterization/file/restart@s0_contract_characterization_test.go` | `TestS0BackendNeutralContractCharacterization/memory/concurrency@s0_contract_characterization_test.go`<br>`TestS0BackendNeutralContractCharacterization/file/concurrency@s0_contract_characterization_test.go` | `TestPostgresStoreMCPAccessAtomicityIdempotencyAndRecovery@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/MCPRepository/ListMCPAccessTickets@s0_contract_characterization_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/MCPRepository/ListMCPBindings@s0_contract_characterization_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/MCPRepository/ListMCPOperations@s0_contract_characterization_test.go` |
| `MemoryRepository` | `TestS0BackendNeutralRepositoryCharacterization/MemoryRepository/success@s0_repository_characterization_test.go`<br>`TestMemoryStoreUpdatesAndDeletesAcceptedMemory@memory_test.go` | `TestS0BackendNeutralRepositoryCharacterization/MemoryRepository/normal_absence@s0_repository_characterization_test.go` | `TestMemoryStoreUpdatesAndDeletesAcceptedMemory@memory_test.go`<br>`TestMemoryStorePrunesExpiredMemories@memory_test.go` | `TestS0DefectEvidenceMutableAliases/MemoryRepository@s0_repository_characterization_test.go` | N/A: Candidate creation always allocates a new ID; acceptance is a state transition rather than an idempotency-key API. | `TestMemoryStoreUpdatesAndDeletesAcceptedMemory@memory_test.go`<br>`TestMemoryStorePrunesExpiredMemories@memory_test.go` | `TestS0BackendNeutralRepositoryLifecycleEvidence/MemoryRepository@s0_repository_lifecycle_test.go` | `TestFileStorePersistsAndReloadsState@file_test.go`<br>`TestFileStorePersistsMemoryRetentionPrune@file_test.go` | N/A: Memory records expose state/existence conflicts but no numeric revision or idempotent-create result. | `TestPostgresStoreRoundTrip@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/MemoryRepository/PruneMemories@s0_contract_characterization_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/shared/collectRows@s0_contract_characterization_test.go` |
| `OwnerRepository` | `TestS0BackendNeutralRepositoryCharacterization/OwnerRepository/success@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/OwnerRepository/normal_absence@s0_repository_characterization_test.go` | `TestMemoryStoreManagesMultipleOwnerProfiles@memory_test.go` | `TestS0BackendNeutralContractCharacterization/memory/success_absence_order_scope_clone@s0_contract_characterization_test.go`<br>`TestS0BackendNeutralContractCharacterization/file/success_absence_order_scope_clone@s0_contract_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/OwnerRepository/duplicate_idempotency@s0_repository_characterization_test.go` | N/A: Owner profiles expose overwrite-by-ID but no delete, CAS, or conflict command. | `TestS0BackendNeutralRepositoryLifecycleEvidence/OwnerRepository@s0_repository_lifecycle_test.go` | `TestS0BackendNeutralContractCharacterization/file/restart@s0_contract_characterization_test.go` | N/A: Owner commands expose no repository revision or idempotent-create result; Memory/File serialize them through the backend lock. | `TestPostgresStoreRoundTrip@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/shared/collectRows@s0_contract_characterization_test.go` |
| `PassiveNotificationRepository` | `TestS0BackendNeutralRepositoryCharacterization/PassiveNotificationRepository/success@s0_repository_characterization_test.go`<br>`TestMemoryStorePassiveNotificationIdempotencyAndOwnerScope@passive_notifications_test.go` | `TestS0BackendNeutralRepositoryCharacterization/PassiveNotificationRepository/normal_absence@s0_repository_characterization_test.go`<br>`TestMemoryStorePassiveNotificationIdempotencyAndOwnerScope@passive_notifications_test.go` | `TestFileStorePassiveNotificationSurvivesRestart@passive_notifications_test.go`<br>`TestPrunePassiveNotificationsCapEvictsReadOldestFirst@passive_notifications_test.go` | `TestS0DefectEvidenceMutableAliases/PassiveNotificationRepository@s0_repository_characterization_test.go` | `TestMemoryStorePassiveNotificationIdempotencyAndOwnerScope@passive_notifications_test.go`<br>`TestMemoryStorePassiveNotificationIdempotentReingestionAtScale@passive_notifications_test.go` | `TestPrunePassiveNotificationsRetentionSweep@passive_notifications_test.go` | `TestS0BackendNeutralRepositoryLifecycleEvidence/PassiveNotificationRepository@s0_repository_lifecycle_test.go`<br>`TestPassiveNotificationRevisionSignalsInboxChanges@passive_notifications_test.go` | `TestFileStorePassiveNotificationSurvivesRestart@passive_notifications_test.go`<br>`TestFileStoreSnapshotRebuildsPassiveNotificationIndex@passive_notifications_test.go` | `TestPassiveNotificationRevisionSignalsInboxChanges@passive_notifications_test.go` | `TestPostgresStorePassiveNotificationPruneAndRevision@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/PassiveNotificationRepository/PrunePassiveNotifications@s0_contract_characterization_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/shared/collectRows@s0_contract_characterization_test.go` |
| `RunRepository` | `TestS0BackendNeutralRepositoryCharacterization/RunRepository/success@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/RunRepository/normal_absence@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/RunRepository/ordering_filtering_scope@s0_repository_characterization_test.go` | `TestS0DefectEvidenceMutableAliases/RunRepository@s0_repository_characterization_test.go` | `TestMemoryStoreSavesRunFeedback@memory_test.go` | N/A: Run records are overwrite/append records and expose no repository delete, CAS, or typed conflict command. | `TestS0BackendNeutralRepositoryLifecycleEvidence/RunRepository@s0_repository_lifecycle_test.go`<br>`TestMemoryStoreSavesRunFeedback@memory_test.go` | `TestFileStorePersistsAndReloadsState@file_test.go`<br>`TestFileStorePersistsWorkflowStateAndToolBinding@file_test.go` | N/A: Run methods expose no revision or idempotent-create result; backend locking serializes overwrites without a caller-visible winner contract. | `TestPostgresStoreRoundTrip@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/shared/collectRows@s0_contract_characterization_test.go` |
| `ScheduleRepository` | `TestS0BackendNeutralRepositoryCharacterization/ScheduleRepository/success@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ScheduleRepository/normal_absence@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ScheduleRepository/ordering_filtering_scope@s0_repository_characterization_test.go` | `TestS0DefectEvidenceMutableAliases/ScheduleRepository@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ScheduleRepository/duplicate_idempotency@s0_repository_characterization_test.go` | `TestMemoryStoreUpdatePendingReminderUsesCompareAndSwap@memory_test.go` | `TestS0BackendNeutralRepositoryLifecycleEvidence/ScheduleRepository@s0_repository_lifecycle_test.go` | `TestS0FileRepositoryRestartGaps/ScheduleRepository@s0_repository_characterization_test.go` | `TestMemoryStoreUpdatePendingReminderUsesCompareAndSwap@memory_test.go` | `TestPostgresStoreRoundTrip@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/shared/collectRows@s0_contract_characterization_test.go` |
| `SessionRepository` | `TestS0BackendNeutralRepositoryCharacterization/SessionRepository/success@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/SessionRepository/normal_absence@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/SessionRepository/ordering_filtering_scope@s0_repository_characterization_test.go` | N/A: Session contains only scalar and time values, so no mutable alias can cross the Store boundary. | N/A: Session creation always allocates a new ID; no caller-supplied duplicate or idempotency key exists. | `TestS0BackendNeutralRepositoryCharacterization/SessionRepository/CAS_conflict_deletion@s0_repository_characterization_test.go`<br>`TestMemoryStoreDeleteSessionRemovesBrowserLoginBlocks@memory_test.go` | `TestS0BackendNeutralRepositoryLifecycleEvidence/SessionRepository@s0_repository_lifecycle_test.go` | `TestS0BackendNeutralContractCharacterization/file/restart@s0_contract_characterization_test.go` | N/A: Session commands expose existence conflicts but no version/revision or idempotent-create contract. | `TestPostgresStoreRoundTrip@postgres_test.go`<br>`TestPostgresStoreDeleteSessionRemovesBrowserLoginBlocks@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/shared/collectRows@s0_contract_characterization_test.go` |

The file tokens above resolve relative to
[`services/gateway/internal/store`](../services/gateway/internal/store). The
PostgreSQL tests remain DSN-gated and are not counted as passed when the DSN is
absent.

The `ALIAS` evidence currently records mutable alias escape for Client,
Conversation, Run, Approval, Schedule, Connector, PassiveNotification,
DeliveryRecord, BrowserState, Memory, Audit, and Evaluation records in both
Memory and the live File decorator. S0 does not repair those production
contracts. Each owning repository wave must replace its defect-evidence cell
with an isolation assertion.

## Production Consumer Matrix

The matrix includes every production declaration that directly accepts or
stores `store.Store`, every named production-local interface whose method set
intersects `Store`, and every anonymous Store-compatible helper interface. It
covers constructor, field, helper, worker, adapter, resolver, and assembly use.
Repeated receiver methods use the composite listed for their owning type.

`TestS0ProductionStoreConsumerInventory` scans all non-test Gateway Go files.
It freezes every direct `store.Store` declaration by file and enclosing symbol,
the Store-package helper, and every named or anonymous local Store-compatible
interface with its flattened Store method set. The frozen authority currently
contains 58 direct declarations, 10 named interfaces, and two anonymous
interfaces. A new, removed, or renamed declaration fails the executable
inventory and requires this matrix to be reviewed.

| Package / symbol | Kind | Minimum repository or consumer-owned composite |
|---|---|---|
| `agent.Runtime`, `NewRuntime`, `NewRuntimeWithContext` | constructor + field | Session + Conversation + Run + Document + Approval + BrowserState + Memory + Audit + ArtifactMetadata |
| `agent.toolExposureEngine`, `newToolExposureEngine` | helper field + constructor | Run |
| `store.ArchiveToolObservation` | helper | ArtifactMetadata |
| `credential.Vault`, `credential.New` | constructor + field | Credential |
| `gateway.Server`, `New`, `NewWithTrace` | constructor + field | Owner + Client + Session + Conversation + Run + Approval + Schedule + Connector + PassiveNotification + ExternalChat + DeliveryRecord + MCP + Memory + Audit + Evaluation + ArtifactMetadata + Credential |
| `gateway.runHasPendingApproval` | helper | Approval |
| `happyapproval.Service`, `New` | polling worker | Approval |
| `iscpbridge.GatewayAdapter`, `NewGatewayAdapter` | adapter + field | Owner + Session + Conversation + Run + Approval + PassiveNotification + Audit |
| `iscppairing.Service`, `New` | service + field | ISCPOnboarding + Audit |
| `mcpaccess.Service`, `New` | service + field | MCP + Run + Approval + Audit |
| `mcpaccess.Provider`, `NewProvider` | provider + field | MCP + Run + Session + ExternalChat + Audit + ArtifactMetadata |
| `mcpaccess.updateOperationRecord` | helper | MCP |
| `mcpaccess.rejectPendingApprovals` | helper | Run + Approval |
| `mcpaccess.finalizeRevokedOperations` | helper | MCP + Run + Approval + Audit |
| `mcpaccess.runHasApprovedApproval` | helper | Approval |
| `mcpaccess.runHasPendingApproval` | helper | Approval |
| `notification.SendWeixinText/Image/File/Typing` | helper entry points | Credential; these entry points call only `Send`, `SendImage`, `SendFile`, or `SendTyping`, not `Deliver` |
| `notification.WeixinAdapter`, `NewWeixinAdapter` | adapter + optional field | Connector + Schedule + Credential + Session + ExternalChat + ArtifactMetadata |
| `reminder.Scheduler`, `NewMessageScheduler` | worker + field | Schedule |
| `remindertarget.Resolver`, `NewResolver` | resolver + field | ExternalChat + Connector |
| `telegram.Dispatcher`, `NewDispatcher` | worker + field | Owner + Session + Conversation + Run + Approval + ExternalChat + DeliveryRecord + ArtifactMetadata + Audit |
| `telegram.Service`, `NewService`, `hasDefaultActiveBinding` | worker + helper | Connector + DeliveryRecord + Audit |
| `telegram.NotificationAdapter`, `NewNotificationAdapter` | adapter + field | Connector + Schedule + Session + ExternalChat + ArtifactMetadata; credentials come from its separate `CredentialVault`, not its Store parameter |
| `toolhub.ToolHub`, `New` | constructor + field | Session + Run + Approval + Schedule + Connector + ExternalChat + Memory + Audit + ArtifactMetadata |
| `weixin.Dispatcher`, `NewDispatcher`, `NewDispatcherWithConfig` | worker + field | Owner + Session + Conversation + Run + Approval + ExternalChat + DeliveryRecord + ArtifactMetadata + Audit |
| `weixin.Syncer`, `NewSyncer`, `WithConfig` | polling worker + field + adapter assembly | Session + Connector + Credential + ExternalChat + DeliveryRecord + ArtifactMetadata + Audit |
| `weixin.MediaAdapter`, `NewMediaAdapter` | adapter + field | Session + ArtifactMetadata + Audit |
| `cmd/sparkclaw.newStore` | backend factory | Owner + Client + ISCPOnboarding + Credential + Session + Conversation + Run + Document + Approval + Schedule + Connector + PassiveNotification + ExternalChat + DeliveryRecord + MCP + BrowserState + Memory + Audit + Evaluation + ArtifactMetadata; one concrete backend result, and no production consumer should receive this broad type after S4 |
| `cmd/sparkclaw.newGatewayServices` | assembly forwarding | Owner + Client + ISCPOnboarding + Credential + Session + Conversation + Run + Document + Approval + Schedule + Connector + PassiveNotification + ExternalChat + DeliveryRecord + MCP + BrowserState + Memory + Audit + Evaluation + ArtifactMetadata |
| `cmd/sparkclaw.newISCPPairingService` | assembly forwarding | ISCPOnboarding + Audit |
| `cmd/sparkclaw.newConnectorAssembly` | assembly forwarding | Owner + Credential + Session + Conversation + Run + Approval + Schedule + Connector + ExternalChat + DeliveryRecord + MCP + Audit + ArtifactMetadata |

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
| `mcpaccess.auditOperationStore` anonymous interface | helper-local anonymous composite | MCP + Audit |
| `mcpaccess.operationSessionID` anonymous interface | helper-local anonymous interface | MCP |

No other direct, named-local, or anonymous Store-compatible production
declaration exists. `delivery.EndpointRegistry` and `delivery.WebDelivery` are
delivery-domain interfaces rather than Store-compatible interfaces.
`artifact.Store` is a separate artifact-object interface and is not part of
this migration.

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
