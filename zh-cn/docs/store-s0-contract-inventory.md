# Store S0 契约清单

> 语言：[English](../../docs/store-s0-contract-inventory.md) | 简体中文

> 状态：S0 清单已于 2026-08-20 在
> `207462154fa2377ed786af671f41e0f353d11ba9` 验收。生产 schema、配置或
> Store 行为变更由 S1 负责。

已验收的 S0 基线包含 141 个方法。ClientRepository 迁移删除独立
`SaveClient`，并把 Client 创建并入原子 `ClaimPairingCode` 后，当前活动目录
包含 140 个方法；上述历史验收 revision 保持不变。

本文是 commit `df05cf5` 加 S0 行为刻画测试的代码事实清单。来源包括
`store.go`、`memory.go`、`file.go`、`postgres.go`、ISCP/MCP Store 文件、
`migrations/0001_core.sql`，以及 `services/gateway` 下每个生产
`store.Store` 引用。方法归属的可执行权威是
`s0_contract_characterization_test.go`；逐 repository 适用性/证据的可执行
权威是 `s0_repository_evidence_test.go` 和
`s0_repository_characterization_test.go`；PostgreSQL 源码协调的可执行权威是
`s0_postgres_manifest_test.go`。

## 已接受的 Repository 目录

S0 冻结 20 个 repository。归属由事务范围决定，而不是组装便利性。尤其是
`DeleteSession` 的事务虽然会删除多个其他 repository 拥有的记录，它仍然是
`SessionRepository` 命令。

| Repository | 方法数 | 职责 |
|---|---:|---|
| `OwnerRepository` | 6 | Owner profile 和外部 owner 查找 |
| `ClientRepository` | 8 | Client、token 查找、撤销、last-seen 和 pairing code |
| `ISCPOnboardingRepository` | 3 | 不含 secret 的 ISCP onboarding receipt |
| `CredentialRepository` | 3 | 加密 credential secret metadata |
| `SessionRepository` | 6 | Session 生命周期，包括跨记录删除事务 |
| `ConversationRepository` | 4 | Message 和有界 message-event stream |
| `RunRepository` | 12 | Agent run、feedback、model/tool call 和 episode summary |
| `DocumentRepository` | 3 | 持久 document record 和 lineage metadata |
| `ApprovalRepository` | 6 | Approval 创建、外部查找、pending 更新和解决 |
| `ScheduleRepository` | 7 | Reminder、due claim、CAS 和 delivery history |
| `ConnectorRepository` | 8 | Owner connector setting 和 notification binding |
| `PassiveNotificationRepository` | 8 | Passive inbox、read state、prune 和进程内 revision |
| `ExternalChatRepository` | 9 | Provider-neutral 外部 session 和 message |
| `DeliveryRecordRepository` | 12 | Receive、send、inbox-update 和幂等记录 |
| `MCPRepository` | 19 | Access ticket、binding、operation、兑换、撤销和删除 |
| `BrowserStateRepository` | 10 | Browser auth record 和 login-block 生命周期 |
| `MemoryRepository` | 7 | Candidate、已接受 memory、搜索、更新、删除和 prune |
| `AuditRepository` | 3 | Audit record 和通用 session event stream |
| `EvaluationRepository` | 3 | Evaluation run 保存/查找/列表 |
| `ArtifactMetadataRepository` | 3 | Artifact metadata 保存/列表/URI 查找 |

## 完整方法归属

序号是当前 `Store` 接口中的源码顺序。以下每个名称恰好出现一次；行为刻画
测试会拒绝未分配、重复分配、新增或删除的方法。

| 序号 | Repository | 当前方法 |
|---:|---|---|
| 1-6 | `SessionRepository` | `CreateSession`, `CreateSessionWithScope`, `ListSessions`, `GetSession`, `UpdateSessionTitle`, `DeleteSession` |
| 7-11, 18-20 | `ClientRepository` | `GetClient`, `ListClients`, `RevokeClient`, `FindClientByTokenHash`, `TouchClient`, `SavePairingCode`, `GetPairingCode`, `ClaimPairingCode` |
| 12-17 | `OwnerRepository` | `GetOwnerProfile`, `UpdateOwnerProfile`, `GetOwnerProfileByID`, `SaveOwnerProfile`, `ListOwnerProfiles`, `FindOwnerProfileByExternalRef` |
| 21-23 | `ISCPOnboardingRepository` | `SaveISCPOnboarding`, `GetISCPOnboarding`, `ListISCPOnboardings` |
| 24-42 | `MCPRepository` | `SaveMCPAccessTicket`, `GetMCPAccessTicket`, `FindMCPAccessTicketBySecretHash`, `ListMCPAccessTickets`, `RedeemMCPAccessTicket`, `RevokeMCPAccessTicket`, `DeleteMCPAccessTicket`, `GetMCPBinding`, `FindMCPBindingForPeer`, `ListMCPBindings`, `RevokeMCPBinding`, `DeleteMCPBinding`, `DeleteMCPAccessRecords`, `TouchMCPBinding`, `CreateMCPOperation`, `GetMCPOperation`, `FindMCPOperationByIdempotency`, `ListMCPOperations`, `UpdateMCPOperation` |
| 43-44, 131-132 | `ConversationRepository` | `AddMessage`, `ListMessages`, `MessageEventHead`, `MessageEventsAfter` |
| 45-54, 139-140 | `RunRepository` | `SaveRunFeedback`, `ListRunFeedback`, `SaveRun`, `GetRun`, `ListRuns`, `SaveModelCall`, `ListModelCalls`, `SaveToolCall`, `GetToolCall`, `ListToolCalls`, `SaveEpisodeSummary`, `ListEpisodeSummaries` |
| 55-57 | `DocumentRepository` | `SaveDocumentRecord`, `GetDocumentRecord`, `ListDocumentRecords` |
| 58-63 | `ApprovalRepository` | `SaveApproval`, `GetApproval`, `FindApprovalByExternalRef`, `UpdatePendingApproval`, `ResolveApproval`, `ListApprovals` |
| 64-70 | `ScheduleRepository` | `SaveReminder`, `UpdatePendingReminder`, `GetReminder`, `ListReminders`, `ClaimDueReminders`, `SaveReminderDelivery`, `ListReminderDeliveries` |
| 71-78 | `ConnectorRepository` | `GetConnectorSetting`, `ListConnectorSettings`, `ListAllConnectorSettings`, `UpdateConnectorSetting`, `SaveNotificationBinding`, `GetNotificationBinding`, `ListNotificationBindings`, `RevokeNotificationBinding` |
| 79-86 | `PassiveNotificationRepository` | `CreatePassiveNotification`, `GetPassiveNotification`, `ListPassiveNotifications`, `CountUnreadPassiveNotifications`, `MarkPassiveNotificationRead`, `MarkAllPassiveNotificationsRead`, `PrunePassiveNotifications`, `PassiveNotificationRevision` |
| 87-95 | `ExternalChatRepository` | `SaveExternalChatSession`, `GetExternalChatSession`, `ListExternalChatSessions`, `FindExternalChatSession`, `FindExternalChatSessionByLinkedSessionID`, `SaveExternalChatMessage`, `GetExternalChatMessage`, `FindExternalChatMessageByExternalID`, `ListExternalChatMessages` |
| 96-107 | `DeliveryRecordRepository` | `SaveMessageReceive`, `GetMessageReceive`, `FindMessageReceive`, `ListMessageReceives`, `SaveMessageDelivery`, `GetMessageDelivery`, `FindMessageDeliveryByIdempotency`, `ListMessageDeliveries`, `SaveChannelInboxUpdate`, `GetChannelInboxUpdate`, `FindChannelInboxUpdate`, `ListChannelInboxUpdates` |
| 108-110 | `CredentialRepository` | `SaveCredentialSecret`, `GetCredentialSecret`, `DeleteCredentialSecret` |
| 111-120 | `BrowserStateRepository` | `SaveBrowserAuthRecord`, `GetBrowserAuthRecord`, `FindBrowserAuthRecord`, `ListBrowserAuthRecords`, `RevokeBrowserAuthRecord`, `SaveBrowserLoginBlock`, `UpdateBrowserLoginBlock`, `GetBrowserLoginBlock`, `FindActiveBrowserLoginBlock`, `ListBrowserLoginBlocks` |
| 121-127 | `MemoryRepository` | `AddMemoryCandidate`, `ResolveMemoryCandidate`, `ListMemoryCandidates`, `SearchMemories`, `UpdateMemory`, `DeleteMemory`, `PruneMemories` |
| 128-130 | `AuditRepository` | `AddAudit`, `ListAudit`, `EventsAfter` |
| 133-135 | `EvaluationRepository` | `SaveEvalRun`, `GetEvalRun`, `ListEvalRuns` |
| 136-138 | `ArtifactMetadataRepository` | `SaveArtifactObject`, `ListArtifactObjects`, `FindArtifactObjectByURI` |

## 后端与持久化映射

当前全部 140 个方法都有 Memory、File 和 PostgreSQL 实现。File 方法全部位于
`file.go`。普通 Memory/PostgreSQL 方法位于 `memory.go`/`postgres.go`，已迁移的
Client PostgreSQL 方法位于 `client_postgres.go`，已迁移的 Approval PostgreSQL
方法位于 `approval_postgres.go`；方法 21-23 使用
`iscp_onboarding.go` 和 `iscp_onboarding_postgres.go`；方法 24-42 使用
`mcp_access.go` 和 `mcp_access_postgres.go`。`store.go` 的全局
编译断言和方法目录测试共同证明后端完整性。

下表把 repository 状态映射到全部 38 个序列化 `Snapshot` 字段和当前
PostgreSQL 对象。分号用于分隔兼容/派生状态与主要记录。

| Repository | File `Snapshot` 字段 | PostgreSQL 表 | 索引与约束 |
|---|---|---|---|
| Owner | `OwnerProfile`, `OwnerProfiles` | `owners` | PK `id`；`owners_external_ref_idx(source,external_ref)` 仅在 Go schema |
| Client | `Clients`, `PairingCodes` | `clients`, `pairing_codes` | PK；唯一 `token_hash` 和 `code_hash`；token 与 status/expiry 索引 |
| ISCP onboarding | `ISCPOnboardings` | `iscp_onboardings` | PK `id`；`iscp_onboardings_owner_created_idx` |
| Credential | `CredentialSecrets` | `credential_secrets` | PK `ref` |
| Session | `Sessions` | `sessions` | PK `id`；被 message/run/document/browser 行引用 |
| Conversation | `Messages`；message event 位于 `Events` | `messages`, `events` | message session/time 与 event session/sequence 索引；event `id` 唯一 |
| Run | `RunFeedback`, `Runs`, `ModelCalls`, `ToolCalls`, `EpisodeSummaries` | `run_feedback`, `agent_runs`, `model_calls`, `tool_calls`, `episode_summaries` | PK、run/time 索引和 session/run FK |
| Document | `DocumentRecords` | `document_records` | PK；owner/session/activity 与 session/path 索引 |
| Approval | `Approvals` | `approvals` | PK；status 索引；部分唯一 `(source,external_id)` |
| Schedule | `Reminders`, `ReminderDelivery` | `reminders`, `reminder_deliveries` | PK；status/due 与 reminder-delivery 索引；delivery FK |
| Connector | `ConnectorSettings`, `NotificationBindings` | `connector_settings`, `notification_bindings` | setting 组合 PK `(owner_id,channel)`；binding PK 与 channel/status 索引 |
| Passive notification | `PassiveNotifications`；revision 有意保持 volatile | `passive_notifications`；进程内 revision map | PK；唯一 `(endpoint_id,idempotency_key)`；owner/created 和 unread 部分索引 |
| External chat | `ExternalChatSessions`, `ExternalChatMessages`；旧 `WeixinChatSessions`, `WeixinChatMessages` | `external_chat_sessions`, `external_chat_messages`；兼容 `weixin_chat_sessions`, `weixin_chat_messages` | binding/chat、linked-session、external-message 和 created-time 索引 |
| Delivery record | `MessageReceives`, `MessageDeliveries`, `ChannelInboxUpdates` | `message_receive_records`, `message_delivery_records`, `channel_inbox_updates` | 唯一 source/native、owner/actor/idempotency、binding/external key，加列表索引 |
| MCP | `MCPAccessTickets`, `MCPBindings`, `MCPOperations`；兑换还创建 `Sessions` | `mcp_access_tickets`, `mcp_bindings`, `mcp_operations`；`sessions` | 唯一 secret hash、active peer 部分唯一索引、唯一 `(binding_id,idempotency_key)`、owner/status 与 update 索引 |
| Browser state | `BrowserAuthRecords`, `BrowserLoginBlocks` | `browser_auth_records`, `browser_login_blocks` | auth lookup 索引；active block session/status/update 索引；session/run FK |
| Memory | `MemoryCandidates`, `Memories` | `memory_candidates`, `memories` | PK；已接受 memory 的 run FK 和 created-time 索引 |
| Audit | `AuditEvents`, `Events` | `audit_events`, `events` | session/time 和 session/sequence 索引；event `id` 唯一 |
| Evaluation | `EvalRuns` | `eval_runs` | PK 和 started-time 索引 |
| Artifact metadata | `ArtifactObjects`；volatile URI-to-ID 索引在加载时重建 | `artifact_objects` | PK；created、run 和仅 Go 侧 URI 索引 |

## 逐 Repository Characterization 证据

下表是已接受的逐 repository 门禁，完整覆盖 20 个 repository 与 10 个维度。
`TestS0RepositoryCharacterizationMatrixCompleteness` 是可执行权威：它要求恰好
20 行 repository 和 10 个维度，把每个 `Test[/subtest]@file.go` token 解析到
完整子测试路径，拒绝无具体理由的 `N/A`，并逐单元格核对英文与中文矩阵和
可执行权威。每个具名维度键绑定自己的断言分支并在 Memory/File 上运行，
不会把一个 repository 级宽检查换名后重复执行；更丰富的契约或当前缺陷继续
链接到 focused test。

| Repository | 成功 | 正常缺失 | 排序/过滤/作用域 | 克隆/别名 | 重复/幂等 | CAS/冲突/删除 | 事件/审计/序列 | File 重启/snapshot | 并发/revision | PostgreSQL row/`rows.Err()` |
|---|---|---|---|---|---|---|---|---|---|---|
| `ApprovalRepository` | `TestS0BackendNeutralRepositoryCharacterization/ApprovalRepository/success@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ApprovalRepository/normal_absence@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ApprovalRepository/ordering_filtering_scope@s0_repository_characterization_test.go` | `TestS0ApprovalRepositoryMutableValuesAreIsolated@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ApprovalRepository/duplicate_idempotency@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ApprovalRepository/CAS_conflict_deletion@s0_repository_characterization_test.go` | `TestApprovalRepositoryContract@approval_repository_contract_test.go` | `TestFileApprovalDefiniteFailureRestoresRecordAndLifecycle@approval_repository_contract_test.go`<br>`TestFileApprovalUnknownOutcomeReconcilesAndSurvivesRestart@approval_repository_contract_test.go` | `TestPostgresApprovalConcurrentExternalRefAndPendingCAS@approval_repository_contract_test.go` | `TestPostgresApprovalWritesAreAtomicLifecycleTransactions@approval_postgres_contract_test.go`<br>`TestPostgresApprovalUnknownOutcomesReturnCandidateAndTerminate@approval_postgres_contract_test.go`<br>`TestPostgresApprovalReadsClassifyQueryScanRowsAndCorruptJSON@approval_postgres_contract_test.go` |
| `ArtifactMetadataRepository` | `TestS0BackendNeutralRepositoryCharacterization/ArtifactMetadataRepository/success@s0_repository_characterization_test.go`<br>`TestMemoryStoreFindsArtifactObjectByURI@memory_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ArtifactMetadataRepository/normal_absence@s0_repository_characterization_test.go`<br>`TestMemoryStoreFindsArtifactObjectByURI@memory_test.go` | `TestMemoryStoreListsArtifactObjectsNewestFirst@memory_test.go`<br>`TestMemoryStoreFindsArtifactObjectByURI@memory_test.go` | N/A: ArtifactObject 只有 scalar/time 成员，不会跨 Store 边界泄漏可变别名。 | `TestMemoryStoreFindsArtifactObjectByURI@memory_test.go` | `TestMemoryStoreFindsArtifactObjectByURI@memory_test.go` | `TestS0BackendNeutralRepositoryLifecycleEvidence/ArtifactMetadataRepository@s0_repository_lifecycle_test.go` | `TestFileStorePersistsAndReloadsState@file_test.go` | N/A: metadata 没有 revision；ID overwrite 会原子替换 URI index。 | `TestPostgresStoreRoundTrip@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/shared/collectRows@s0_contract_characterization_test.go` |
| `AuditRepository` | `TestS0BackendNeutralRepositoryCharacterization/AuditRepository/success@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/AuditRepository/normal_absence@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/AuditRepository/ordering_filtering_scope@s0_repository_characterization_test.go` | `TestAuditRepositoryMemoryAndFileContract@audit_repository_contract_test.go`<br>`TestAuditRepositoryEventSequenceAndTypedIsolation@audit_repository_contract_test.go` | N/A: AddAudit 是 append-only 且缺失时生成 ID，没有调用方幂等键。 | N/A: Audit 仅 append，不提供 update、delete、CAS 或 conflict 命令。 | `TestAuditRepositoryEventSequenceAndTypedIsolation@audit_repository_contract_test.go` | `TestAuditRepositoryMemoryAndFileContract@audit_repository_contract_test.go` | N/A: audit append 没有 revision；event cursor sequence 是其排序 token。 | `TestPostgresAuditRepositoryPropagatesBackendFailures@audit_repository_contract_test.go`<br>`TestPostgresAuditRepositoryMissingCursorIsEmpty@audit_repository_contract_test.go`<br>`TestPostgresAuditRepositoryRestoresTypedEventPayload@audit_repository_contract_test.go`<br>`TestPostgresAuditRepositoryConfiguredContract@audit_repository_contract_test.go` |
| `BrowserStateRepository` | `TestS0BackendNeutralRepositoryCharacterization/BrowserStateRepository/success@s0_repository_characterization_test.go`<br>`TestMemoryStoreScopesBrowserAuthRecordsByOwnerProfileAndSite@memory_test.go`<br>`TestMemoryStoreTracksActiveBrowserLoginBlock@memory_test.go` | `TestS0BackendNeutralRepositoryCharacterization/BrowserStateRepository/normal_absence@s0_repository_characterization_test.go` | `TestMemoryStoreScopesBrowserAuthRecordsByOwnerProfileAndSite@memory_test.go`<br>`TestMemoryStoreFindActiveBrowserLoginBlockPicksNewestStoredUpdate@memory_test.go` | `TestS0DefectEvidenceMutableAliases/BrowserStateRepository@s0_repository_characterization_test.go` | `TestMemoryStoreBrowserLoginBlockTrimsIDOnWrite@memory_test.go` | `TestMemoryStoreBrowserHandoffCASPreservesRevisionTwoFields@memory_test.go`<br>`TestMemoryStoreDeleteSessionRemovesBrowserLoginBlocks@memory_test.go` | `TestS0BackendNeutralRepositoryLifecycleEvidence/BrowserStateRepository@s0_repository_lifecycle_test.go` | `TestFileStoreBrowserHandoffCASRoundTrip@file_test.go`<br>`TestFileStorePersistsAndReloadsState@file_test.go` | `TestMemoryStoreBrowserHandoffCASPreservesRevisionTwoFields@memory_test.go` | `TestPostgresStoreBrowserHandoffCASRoundTrip@postgres_test.go`<br>`TestPostgresStoreFindActiveBrowserLoginBlockMatchesSharedActivePredicate@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/shared/collectRows@s0_contract_characterization_test.go` |
| `ClientRepository` | `TestS0BackendNeutralRepositoryCharacterization/ClientRepository/success@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ClientRepository/normal_absence@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ClientRepository/ordering_filtering_scope@s0_repository_characterization_test.go` | `TestClientRepositoryPointerIsolation@client_contract_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ClientRepository/duplicate_idempotency@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ClientRepository/CAS_conflict_deletion@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryLifecycleEvidence/ClientRepository@s0_repository_lifecycle_test.go` | `TestFileStorePersistsAndReloadsState@file_test.go` | N/A: Client/pairing 命令没有 CAS/revision；claim/revoke 使用状态冲突。 | `TestPostgresStoreRoundTrip@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/shared/collectRows@s0_contract_characterization_test.go` |
| `ConnectorRepository` | `TestS0BackendNeutralRepositoryCharacterization/ConnectorRepository/success@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ConnectorRepository/normal_absence@s0_repository_characterization_test.go` | `TestMemoryStoreListsAllConnectorSettingsInStableOwnerChannelOrder@connector_settings_test.go` | `TestS0ConnectorRepositoryMutableValuesAreIsolated@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ConnectorRepository/duplicate_idempotency@s0_repository_characterization_test.go` | `TestMemoryStoreConnectorSettingUsesCASAndOwnerScope@connector_settings_test.go` | `TestS0BackendNeutralRepositoryLifecycleEvidence/ConnectorRepository@s0_repository_lifecycle_test.go` | `TestFileStorePersistsConnectorSettingVersion@connector_settings_test.go`<br>`TestS0FileRepositoryRestartGaps/ConnectorRepository@s0_repository_characterization_test.go` | `TestMemoryStoreConnectorSettingUsesCASAndOwnerScope@connector_settings_test.go` | `TestPostgresStoreListsAllConnectorSettings@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/shared/collectRows@s0_contract_characterization_test.go` |
| `ConversationRepository` | `TestS0BackendNeutralRepositoryCharacterization/ConversationRepository/success@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ConversationRepository/normal_absence@s0_repository_characterization_test.go` | `TestMemoryMessageEventsAreBoundedAndSessionScoped@message_events_test.go` | `TestS0ConversationRepositoryMutableValuesAreIsolated@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ConversationRepository/duplicate_idempotency@s0_repository_characterization_test.go` | N/A: append/cursor API 提供 message reuse 与 cursor 校验，但没有 update/delete/CAS。 | `TestS0BackendNeutralRepositoryLifecycleEvidence/ConversationRepository@s0_repository_lifecycle_test.go`<br>`TestMemoryMessageEventsAreBoundedAndSessionScoped@message_events_test.go` | `TestFileMessageEventsSurviveRestart@message_events_test.go`<br>`TestS0BackendNeutralContractCharacterization/file/restart@s0_contract_characterization_test.go` | N/A: append 由 event sequence 排序，不暴露 CAS revision。 | `TestPostgresStoreRoundTrip@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/shared/collectRows@s0_contract_characterization_test.go` |
| `CredentialRepository` | `TestS0BackendNeutralRepositoryCharacterization/CredentialRepository/success@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/CredentialRepository/normal_absence@s0_repository_characterization_test.go` | N/A: CredentialRepository 只有精确 ref get，没有 list/filter/order/owner scope query。 | N/A: CredentialSecret 只有 scalar/time 成员，不会跨 Store 边界泄漏可变别名。 | `TestS0BackendNeutralRepositoryCharacterization/CredentialRepository/duplicate_idempotency@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/CredentialRepository/CAS_conflict_deletion@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryLifecycleEvidence/CredentialRepository@s0_repository_lifecycle_test.go` | `TestFileStoreEncryptsStateAtRest@file_test.go` | N/A: credential save/delete 没有 CAS、revision 或幂等创建结果。 | N/A: PostgreSQL credential 仅使用 Exec/QueryRow，没有多行 iterator 或 rows.Err 路径。 |
| `DeliveryRecordRepository` | `TestS0BackendNeutralRepositoryCharacterization/DeliveryRecordRepository/success@s0_repository_characterization_test.go`<br>`TestMemoryStoreMessageLifecycleParity@message_lifecycle_test.go` | `TestS0BackendNeutralRepositoryCharacterization/DeliveryRecordRepository/normal_absence@s0_repository_characterization_test.go` | `TestMemoryStoreMessageLifecycleParity@message_lifecycle_test.go`<br>`TestMemoryStoreExternalChatAndInboxParity@external_chat_test.go` | `TestS0DefectEvidenceMutableAliases/DeliveryRecordRepository@s0_repository_characterization_test.go` | `TestMemoryStoreMessageLifecycleParity@message_lifecycle_test.go`<br>`TestMemoryStoreExternalChatAndInboxParity@external_chat_test.go` | N/A: lifecycle overwrite/idempotency lookup API 没有 delete 或调用方可见 CAS。 | `TestS0BackendNeutralRepositoryLifecycleEvidence/DeliveryRecordRepository@s0_repository_lifecycle_test.go` | `TestFileStoreMessageLifecycleRoundTrip@message_lifecycle_test.go`<br>`TestFileStoreExternalChatAndInboxParity@external_chat_test.go` | N/A: dedupe key 串行化写入，但不暴露数字 revision。 | `TestPostgresStoreExternalChatAndInboxParity@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/DeliveryRecordRepository/ListMessageReceives@s0_contract_characterization_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/DeliveryRecordRepository/ListMessageDeliveries@s0_contract_characterization_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/shared/collectRows@s0_contract_characterization_test.go` |
| `DocumentRepository` | `TestS0BackendNeutralRepositoryCharacterization/DocumentRepository/success@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/DocumentRepository/normal_absence@s0_repository_characterization_test.go` | `TestMemoryStoreDocumentRecordsAreRecentAndSessionScoped@memory_test.go` | N/A: DocumentRecord 只有 scalar/time 成员，不会跨 Store 边界泄漏可变别名。 | `TestS0BackendNeutralRepositoryCharacterization/DocumentRepository/duplicate_idempotency@s0_repository_characterization_test.go` | N/A: document record 是 ID overwrite，没有 delete、CAS 或 conflict 命令。 | `TestS0BackendNeutralRepositoryLifecycleEvidence/DocumentRepository@s0_repository_lifecycle_test.go` | `TestFileStorePersistsDocumentRecords@file_test.go` | N/A: document record 没有 revision 或幂等创建结果。 | `TestPostgresStoreRoundTrip@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/shared/collectRows@s0_contract_characterization_test.go` |
| `EvaluationRepository` | `TestS0BackendNeutralRepositoryCharacterization/EvaluationRepository/success@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/EvaluationRepository/normal_absence@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/EvaluationRepository/ordering_filtering_scope@s0_repository_characterization_test.go` | `TestS0EvaluationRepositoryMutableValuesAreIsolated@s0_repository_characterization_test.go`<br>`TestEvaluationRepositoryMemoryAndFileContract@evaluation_repository_contract_test.go` | `TestS0BackendNeutralRepositoryCharacterization/EvaluationRepository/duplicate_idempotency@s0_repository_characterization_test.go` | N/A: eval run 是 ID overwrite，没有 delete、CAS 或 conflict 命令。 | `TestEvaluationRepositoryMemoryAndFileContract@evaluation_repository_contract_test.go` | `TestEvaluationRepositoryMemoryAndFileContract@evaluation_repository_contract_test.go` | N/A: eval run 没有 revision 或幂等创建结果。 | `TestPostgresEvaluationSaveUsesAtomicLifecycleTransaction@evaluation_repository_contract_test.go`<br>`TestPostgresEvaluationReadsPropagateBackendErrors@evaluation_repository_contract_test.go`<br>`TestPostgresEvaluationRepositoryConfiguredContract@evaluation_repository_contract_test.go` |
| `ExternalChatRepository` | `TestS0BackendNeutralRepositoryCharacterization/ExternalChatRepository/success@s0_repository_characterization_test.go`<br>`TestMemoryStoreExternalChatAndInboxParity@external_chat_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ExternalChatRepository/normal_absence@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ExternalChatRepository/ordering_filtering_scope@s0_repository_characterization_test.go` | N/A: ExternalChatSession/Message 只有 scalar/time 成员，没有可变别名。 | `TestMemoryStoreExternalChatAndInboxParity@external_chat_test.go` | N/A: external chat record 是 ID overwrite，没有 delete、CAS 或 typed conflict。 | `TestS0BackendNeutralRepositoryLifecycleEvidence/ExternalChatRepository@s0_repository_lifecycle_test.go` | `TestFileStoreExternalChatAndInboxParity@external_chat_test.go` | N/A: external-ID lookup 是 reconciliation，不是 CAS/revision。 | `TestPostgresStoreExternalChatAndInboxParity@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/shared/collectRows@s0_contract_characterization_test.go` |
| `ISCPOnboardingRepository` | `TestS0BackendNeutralRepositoryCharacterization/ISCPOnboardingRepository/success@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ISCPOnboardingRepository/normal_absence@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ISCPOnboardingRepository/ordering_filtering_scope@s0_repository_characterization_test.go` | N/A: ISCPOnboarding 只有 scalar/time 成员，没有可变别名。 | `TestS0BackendNeutralRepositoryCharacterization/ISCPOnboardingRepository/duplicate_idempotency@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ISCPOnboardingRepository/CAS_conflict_deletion@s0_repository_characterization_test.go` | N/A: repository 不拥有 lifecycle event；iscppairing 调用方写 audit。 | `TestFileStorePersistsOnlyISCPOnboardingReceipt@mcp_access_test.go` | N/A: 唯一 ID conflict 是并发边界，没有 revision/CAS 字段。 | `TestPostgresStorePersistsOnlyISCPOnboardingReceipt@postgres_test.go`<br>`TestPostgresOnboardingListReturnsRowsError@iscp_onboarding_postgres_test.go` |
| `MCPRepository` | `TestS0BackendNeutralRepositoryCharacterization/MCPRepository/success@s0_repository_characterization_test.go`<br>`TestMCPAccessTicketRedemptionIsAtomicAndDeviceBound@mcp_access_test.go` | `TestS0BackendNeutralRepositoryCharacterization/MCPRepository/normal_absence@s0_repository_characterization_test.go` | `TestMCPAccessRecordsCanBeDeletedIndividuallyAndByOwner@mcp_access_test.go` | `TestMemoryStoreMCPRecordsCannotBeMutatedOutsideStore@mcp_access_test.go`<br>`TestS0BackendNeutralContractCharacterization/file/idempotency_cas_alias@s0_contract_characterization_test.go` | `TestMCPOperationIdempotencyRejectsChangedRequest@mcp_access_test.go` | `TestMCPBindingRevocationTerminatesOnlyNonterminalOperations@mcp_access_test.go`<br>`TestMCPAccessRecordsCanBeDeletedIndividuallyAndByOwner@mcp_access_test.go`<br>`TestS0BackendNeutralContractCharacterization/memory/idempotency_cas_alias@s0_contract_characterization_test.go`<br>`TestS0BackendNeutralContractCharacterization/file/idempotency_cas_alias@s0_contract_characterization_test.go` | N/A: MCP lifecycle audit 由调用方拥有；operation sequence 由 versioned state 表示。 | `TestFileStorePersistsMCPAccessWithoutPlaintextSecret@mcp_access_test.go`<br>`TestS0BackendNeutralContractCharacterization/file/restart@s0_contract_characterization_test.go` | `TestS0BackendNeutralContractCharacterization/memory/concurrency@s0_contract_characterization_test.go`<br>`TestS0BackendNeutralContractCharacterization/file/concurrency@s0_contract_characterization_test.go` | `TestPostgresStoreMCPAccessAtomicityIdempotencyAndRecovery@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/MCPRepository/ListMCPAccessTickets@s0_contract_characterization_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/MCPRepository/ListMCPBindings@s0_contract_characterization_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/MCPRepository/ListMCPOperations@s0_contract_characterization_test.go` |
| `MemoryRepository` | `TestS0BackendNeutralRepositoryCharacterization/MemoryRepository/success@s0_repository_characterization_test.go`<br>`TestMemoryStoreUpdatesAndDeletesAcceptedMemory@memory_test.go` | `TestS0BackendNeutralRepositoryCharacterization/MemoryRepository/normal_absence@s0_repository_characterization_test.go` | `TestMemoryStoreUpdatesAndDeletesAcceptedMemory@memory_test.go`<br>`TestMemoryStorePrunesExpiredMemories@memory_test.go` | `TestS0DefectEvidenceMutableAliases/MemoryRepository@s0_repository_characterization_test.go` | N/A: candidate create 分配新 ID；acceptance 是 state transition，不是幂等键 API。 | `TestMemoryStoreUpdatesAndDeletesAcceptedMemory@memory_test.go`<br>`TestMemoryStorePrunesExpiredMemories@memory_test.go` | `TestS0BackendNeutralRepositoryLifecycleEvidence/MemoryRepository@s0_repository_lifecycle_test.go` | `TestFileStorePersistsAndReloadsState@file_test.go`<br>`TestFileStorePersistsMemoryRetentionPrune@file_test.go` | N/A: memory state/existence conflict 没有数字 revision 或幂等创建结果。 | `TestPostgresStoreRoundTrip@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/MemoryRepository/PruneMemories@s0_contract_characterization_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/shared/collectRows@s0_contract_characterization_test.go` |
| `OwnerRepository` | `TestS0BackendNeutralRepositoryCharacterization/OwnerRepository/success@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/OwnerRepository/normal_absence@s0_repository_characterization_test.go` | `TestMemoryStoreManagesMultipleOwnerProfiles@memory_test.go` | `TestS0BackendNeutralContractCharacterization/memory/success_absence_order_scope_clone@s0_contract_characterization_test.go`<br>`TestS0BackendNeutralContractCharacterization/file/success_absence_order_scope_clone@s0_contract_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/OwnerRepository/duplicate_idempotency@s0_repository_characterization_test.go` | N/A: owner profile 仅按 ID overwrite，没有 delete、CAS 或 conflict 命令。 | `TestS0BackendNeutralRepositoryLifecycleEvidence/OwnerRepository@s0_repository_lifecycle_test.go` | `TestS0BackendNeutralContractCharacterization/file/restart@s0_contract_characterization_test.go` | N/A: owner 命令没有 repository revision 或幂等创建结果。 | `TestPostgresStoreRoundTrip@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/shared/collectRows@s0_contract_characterization_test.go` |
| `PassiveNotificationRepository` | `TestS0BackendNeutralRepositoryCharacterization/PassiveNotificationRepository/success@s0_repository_characterization_test.go`<br>`TestMemoryStorePassiveNotificationIdempotencyAndOwnerScope@passive_notifications_test.go` | `TestS0BackendNeutralRepositoryCharacterization/PassiveNotificationRepository/normal_absence@s0_repository_characterization_test.go`<br>`TestMemoryStorePassiveNotificationIdempotencyAndOwnerScope@passive_notifications_test.go` | `TestFileStorePassiveNotificationSurvivesRestart@passive_notifications_test.go`<br>`TestPrunePassiveNotificationsCapEvictsReadOldestFirst@passive_notifications_test.go` | `TestS0DefectEvidenceMutableAliases/PassiveNotificationRepository@s0_repository_characterization_test.go` | `TestMemoryStorePassiveNotificationIdempotencyAndOwnerScope@passive_notifications_test.go`<br>`TestMemoryStorePassiveNotificationIdempotentReingestionAtScale@passive_notifications_test.go` | `TestPrunePassiveNotificationsRetentionSweep@passive_notifications_test.go` | `TestS0BackendNeutralRepositoryLifecycleEvidence/PassiveNotificationRepository@s0_repository_lifecycle_test.go`<br>`TestPassiveNotificationRevisionSignalsInboxChanges@passive_notifications_test.go` | `TestFileStorePassiveNotificationSurvivesRestart@passive_notifications_test.go`<br>`TestFileStoreSnapshotRebuildsPassiveNotificationIndex@passive_notifications_test.go` | `TestPassiveNotificationRevisionSignalsInboxChanges@passive_notifications_test.go` | `TestPostgresStorePassiveNotificationPruneAndRevision@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/PassiveNotificationRepository/PrunePassiveNotifications@s0_contract_characterization_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/shared/collectRows@s0_contract_characterization_test.go` |
| `RunRepository` | `TestS0BackendNeutralRepositoryCharacterization/RunRepository/success@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/RunRepository/normal_absence@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/RunRepository/ordering_filtering_scope@s0_repository_characterization_test.go` | `TestS0RunRepositoryMutableValuesAreIsolated@s0_repository_characterization_test.go` | `TestMemoryStoreSavesRunFeedback@memory_test.go` | N/A: Run 仅 overwrite/append，不提供 repository delete、CAS 或 typed conflict。 | `TestS0BackendNeutralRepositoryLifecycleEvidence/RunRepository@s0_repository_lifecycle_test.go`<br>`TestMemoryStoreSavesRunFeedback@memory_test.go` | `TestFileStorePersistsAndReloadsState@file_test.go`<br>`TestFileStorePersistsWorkflowStateAndToolBinding@file_test.go` | N/A: Run 方法没有 revision/幂等创建结果或调用方可见 winner。 | `TestPostgresStoreRoundTrip@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/shared/collectRows@s0_contract_characterization_test.go` |
| `ScheduleRepository` | `TestS0BackendNeutralRepositoryCharacterization/ScheduleRepository/success@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ScheduleRepository/normal_absence@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ScheduleRepository/ordering_filtering_scope@s0_repository_characterization_test.go` | `TestS0DefectEvidenceMutableAliases/ScheduleRepository@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/ScheduleRepository/duplicate_idempotency@s0_repository_characterization_test.go` | `TestMemoryStoreUpdatePendingReminderUsesCompareAndSwap@memory_test.go` | `TestS0BackendNeutralRepositoryLifecycleEvidence/ScheduleRepository@s0_repository_lifecycle_test.go` | `TestS0FileRepositoryRestartGaps/ScheduleRepository@s0_repository_characterization_test.go` | `TestMemoryStoreUpdatePendingReminderUsesCompareAndSwap@memory_test.go` | `TestPostgresStoreRoundTrip@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/shared/collectRows@s0_contract_characterization_test.go` |
| `SessionRepository` | `TestS0BackendNeutralRepositoryCharacterization/SessionRepository/success@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/SessionRepository/normal_absence@s0_repository_characterization_test.go` | `TestS0BackendNeutralRepositoryCharacterization/SessionRepository/ordering_filtering_scope@s0_repository_characterization_test.go` | N/A: Session 只有 scalar/time 成员，不会跨 Store 边界泄漏可变别名。 | N/A: Session create 始终分配新 ID，没有调用方 duplicate/idempotency key。 | `TestS0BackendNeutralRepositoryCharacterization/SessionRepository/CAS_conflict_deletion@s0_repository_characterization_test.go`<br>`TestMemoryStoreDeleteSessionRemovesBrowserLoginBlocks@memory_test.go` | `TestS0BackendNeutralRepositoryLifecycleEvidence/SessionRepository@s0_repository_lifecycle_test.go` | `TestS0BackendNeutralContractCharacterization/file/restart@s0_contract_characterization_test.go` | N/A: Session 命令有 existence conflict，但没有 version/revision 契约。 | `TestPostgresStoreRoundTrip@postgres_test.go`<br>`TestPostgresStoreDeleteSessionRemovesBrowserLoginBlocks@postgres_test.go`<br>`TestS0DefectEvidencePostgresRowsErrIsNotChecked/shared/collectRows@s0_contract_characterization_test.go` |

上述 file token 相对于
[`services/gateway/internal/store`](../../services/gateway/internal/store) 解析。
PostgreSQL 测试继续受 DSN gate；未设置 DSN 时不能计为通过。

`ALIAS` 证据当前记录 Client、Conversation、Run、Schedule、
Connector、PassiveNotification、DeliveryRecord、BrowserState 和 Memory
记录在 Memory 以及运行中的 File decorator 中存在可变别名逃逸。S0 不修复
这些生产契约。Approval、Audit 和 Evaluation 现在已有正向隔离断言；其余
每个负责的 repository wave 必须用隔离断言替换相应 defect-evidence 单元格。

## 生产消费者矩阵

矩阵同时包含每个直接接受/保存 `store.Store` 的生产声明、方法集与 `Store`
相交的每个具名生产局部接口，以及每个匿名 Store-compatible helper 接口。它
覆盖 constructor、field、helper、worker、adapter、resolver 和 assembly 使用。
重复 receiver 方法使用其所属类型所列 composite。

`TestS0ProductionStoreConsumerInventory` 扫描 Gateway 中全部非测试 Go 文件。
它按文件和外围 symbol 冻结每个直接 `store.Store` 声明、Store package 内
helper，以及每个具名或匿名局部 Store-compatible 接口的展开 Store 方法集。
当前冻结权威包含 58 个直接声明、10 个具名接口和两个匿名接口。新增、删除或
重命名声明都会使可执行清单失败，并要求重新审查本矩阵。

| Package / symbol | 类型 | 最小 repository 或消费者自有 composite |
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
| `notification.SendWeixinText/Image/File/Typing` | helper 入口 | Credential；这些入口只调用 `Send`、`SendImage`、`SendFile` 或 `SendTyping`，不调用 `Deliver` |
| `notification.WeixinAdapter`, `NewWeixinAdapter` | adapter + optional field | Connector + Schedule + Credential + Session + ExternalChat + ArtifactMetadata |
| `reminder.Scheduler`, `NewMessageScheduler` | worker + field | Schedule |
| `remindertarget.Resolver`, `NewResolver` | resolver + field | ExternalChat + Connector |
| `telegram.Dispatcher`, `NewDispatcher` | worker + field | Owner + Session + Conversation + Run + Approval + ExternalChat + DeliveryRecord + ArtifactMetadata + Audit |
| `telegram.Service`, `NewService`, `hasDefaultActiveBinding` | worker + helper | Connector + DeliveryRecord + Audit |
| `telegram.NotificationAdapter`, `NewNotificationAdapter` | adapter + field | Connector + Schedule + Session + ExternalChat + ArtifactMetadata；credential 来自独立 `CredentialVault`，不归其 Store 参数 |
| `toolhub.ToolHub`, `New` | constructor + field | Session + Run + Approval + Schedule + Connector + ExternalChat + Memory + Audit + ArtifactMetadata |
| `weixin.Dispatcher`, `NewDispatcher`, `NewDispatcherWithConfig` | worker + field | Owner + Session + Conversation + Run + Approval + ExternalChat + DeliveryRecord + ArtifactMetadata + Audit |
| `weixin.Syncer`, `NewSyncer`, `WithConfig` | polling worker + field + adapter assembly | Session + Connector + Credential + ExternalChat + DeliveryRecord + ArtifactMetadata + Audit |
| `weixin.MediaAdapter`, `NewMediaAdapter` | adapter + field | Session + ArtifactMetadata + Audit |
| `cmd/sparkclaw.newStore` | backend factory | Owner + Client + ISCPOnboarding + Credential + Session + Conversation + Run + Document + Approval + Schedule + Connector + PassiveNotification + ExternalChat + DeliveryRecord + MCP + BrowserState + Memory + Audit + Evaluation + ArtifactMetadata；组成单一 concrete backend 结果，S4 后任何生产消费者都不应再接收此宽类型 |
| `cmd/sparkclaw.newGatewayServices` | assembly forwarding | Owner + Client + ISCPOnboarding + Credential + Session + Conversation + Run + Document + Approval + Schedule + Connector + PassiveNotification + ExternalChat + DeliveryRecord + MCP + BrowserState + Memory + Audit + Evaluation + ArtifactMetadata |
| `cmd/sparkclaw.newISCPPairingService` | assembly forwarding | ISCPOnboarding + Audit |
| `cmd/sparkclaw.newConnectorAssembly` | assembly forwarding | Owner + Credential + Session + Conversation + Run + Approval + Schedule + Connector + ExternalChat + DeliveryRecord + MCP + Audit + ArtifactMetadata |

生产局部 Store-compatible 消费者如下：

| Package / symbol | 类型 | 已接受 repository 或 composite |
|---|---|---|
| `connector.Registry`, `connectorStore`, `NewRegistry` | field + 局部接口 + constructor | Connector |
| `messagecontrol.EndpointRegistry`, `endpointStore`, `NewEndpointRegistry` | field + 局部接口 + constructor | Session + Connector + ExternalChat |
| `messagecontrol.mcpEndpointStore`（位于 `EndpointRegistry.get`） | optional 局部 type assertion | MCP；当前 optional discovery 是已记录行为，迁移时必须改为显式的消费者自有 composite |
| `messagecontrol.ScheduleRegistry`, `scheduleStore`, `NewScheduleRegistry` | field + 局部接口 + constructor | Schedule + Session + Connector + ExternalChat |
| `messagecontrol.ReceiveLifecycle`, `receiveStore`, `NewReceiveLifecycle` | field + 局部接口 + constructor | DeliveryRecord |
| `delivery.PersistentWebDelivery`, `webMessageStore`, `NewPersistentWebDelivery` | field + 局部接口 + constructor | Conversation |
| `delivery.EndpointResourceResolver`, `endpointResourceStore`, `NewEndpointResourceResolver` | resolver + 局部 composite + constructor | Session + ArtifactMetadata |
| `delivery.StoreResourceResolver`, `artifactStore`, `NewStoreResourceResolver` | resolver + 局部接口 + constructor | ArtifactMetadata |
| `delivery.ResolveBrowserContent`, `governedArtifactStore` | helper + 局部 composite | Session + ArtifactMetadata |
| `delivery.RecordExternalDelivery`, `externalDeliveryStore` | helper + 局部 composite | ExternalChat |
| `mcpaccess.auditOperationStore` 匿名接口 | helper 局部匿名 composite | MCP + Audit |
| `mcpaccess.operationSessionID` 匿名接口 | helper 局部匿名接口 | MCP |

不存在其他直接、具名局部或匿名 Store-compatible 生产声明。
`delivery.EndpointRegistry` 和 `delivery.WebDelivery` 是 delivery 领域接口，不是
Store-compatible 接口。`artifact.Store` 是独立的 artifact-object 接口，不属于
本次迁移。

## Mutation 与命令矩阵

`M`、`F`、`P` 分别表示 Memory 锁内 mutation、File 原子替换和 PostgreSQL。
`F rename` 是未来的持久 effect submission point；当前 41 个旧 File 调用点会
丢弃结果。`P Exec` 是 autocommit submission point，`P Commit` 是事务
submission point；当前 27 个 PostgreSQL `Exec` 结果被显式丢弃。未来必需的
生命周期行必须加入同一事务，即使 PostgreSQL 当前是在命令后追加且忽略失败。

| Repository / 命令 | 记录和派生索引 mutation | 必需 event/audit | 原子边界和 effect submission | 幂等 / CAS | Reconciliation read |
|---|---|---|---|---|---|
| Session `CreateSession*` | session | `session.created` audit + event | session + lifecycle；M lock / F rename / P Exec | 生成 ID | `GetSession` |
| Session `UpdateSessionTitle` | session title/update time | `session.updated` audit + event | session + lifecycle；M lock / F rename / P Exec | 当前必须存在 | `GetSession` |
| Session `DeleteSession` | session，以及 message、run、feedback、model/tool call、document、approval、reminder/delivery、candidate/memory、browser block、artifact/URI 索引、episode summary、关联 external chat、旧 session audit/event | 替换后的 `session.deleted` audit + event | 单个跨 repository 事务；M lock / F rename / P Commit | 目标必须存在 | `GetSession` 缺失加各 scope 列表读取 |
| Client `RevokeClient` | client 撤销与 token 认证状态 | `client.revoked` audit + event | record + lifecycle；M lock / F rename / P Commit | 目标必须存在 | `GetClient`, `FindClientByTokenHash` |
| Client `TouchClient` | last-seen | 无 | 单记录；M lock / F rename / P Commit | 缺失或已撤销目标为正常不存在 | `GetClient` |
| Client `SavePairingCode` | 新 pending pairing code | `pairing_code.created` audit + event | record + lifecycle；M lock / F rename / P Commit | pairing ID 和非空 code hash 唯一 | `GetPairingCode` |
| Client `ClaimPairingCode` | 原子创建 client/token 索引，再 claim 并关联 pairing code | `client.saved` 后接 `pairing_code.claimed` 的 event 序列，以及同类型的无序 audit 集合 | client + pairing + lifecycle；M lock / F rename / P Commit | pending 且未过期的 code，加唯一 client ID/token hash | `GetPairingCode`, `GetClient`, `FindClientByTokenHash` |
| Owner `SaveOwnerProfile` / `UpdateOwnerProfile` | owner map/default owner | `owner_profile.updated` audit + event | record + lifecycle；M lock / F rename / P Exec | ID overwrite | `GetOwnerProfileByID` |
| ISCP `SaveISCPOnboarding` | onboarding receipt | 当前由 caller 写 audit；repository 不拥有 lifecycle row | 单 receipt；M lock / 带 rollback 的 F rename / P Exec | 唯一 ID -> `ErrISCPOnboardingConflict` | `GetISCPOnboarding` |
| MCP `SaveMCPAccessTicket` | ticket 和 secret-hash lookup | caller audit | 单 ticket；M lock / 带 rollback 的 F rename / P Exec | 唯一 ID/secret hash | `GetMCPAccessTicket`、secret-hash lookup |
| MCP `RedeemMCPAccessTicket` | 消耗 ticket、新建 binding、新建可见关联 session | caller audit | 三条记录全部；M lock / 带 rollback 的 F rename / P Commit | ticket 单次使用 + peer identity | ticket lookup + `FindMCPBindingForPeer` + `GetSession` |
| MCP ticket revoke/delete | ticket status 或删除 | caller audit | 单 ticket；M lock / 带 rollback 的 F rename / P Exec/Commit | 当前状态与 owner scope | `GetMCPAccessTicket` |
| MCP `RevokeMCPBinding` | binding 加全部非 terminal operation | caller audit | binding + operation 集；M lock / 带 rollback 的 F rename / P Commit | terminal operation 不变 | `GetMCPBinding`, `ListMCPOperations` |
| MCP binding/access deletion | binding/ticket/operation 集 | caller audit | 完整 owner/binding 集；M lock / 带 rollback 的 F rename / P Commit | owner scope | ticket/binding/operation 列表 |
| MCP `TouchMCPBinding` | 最新 ISCP session 与 last-used/update time | 无 | 单个 active binding；M lock / 带 rollback 的 F rename / P Exec | active-state 前置条件 | `GetMCPBinding` |
| MCP `CreateMCPOperation` | operation 和 `(binding,idempotency)` lookup | 无 | 单 operation；M lock / 带 rollback 的 F rename / P Exec | 相同 fingerprint 复用；变化则 conflict | idempotency lookup |
| MCP `UpdateMCPOperation` | operation/version | 无 | 单 operation；M lock / 带 rollback 的 F rename / P CAS Exec | expected version | `GetMCPOperation` |
| Conversation `AddMessage` | message list、可见 session title/update time | `message.created` event | message + session + event；M lock / F rename / P Commit | 重复 message ID 复用当前记录 | `ListMessages`, `MessageEventHead` |
| Run `SaveRunFeedback` | 按 run/message 的 feedback | feedback audit + event | feedback + lifecycle；M lock / F rename / P Exec | 同 ID 或 message 替换 | `ListRunFeedback` |
| Run `SaveRun` | run | `run.<state>` event | run + event；M lock / F rename / P Exec | ID overwrite | `GetRun` |
| Run `SaveModelCall` / `SaveToolCall` | call record | status audit + event | call + lifecycle；M lock / F rename / P Exec | ID overwrite | model/tool list 或 `GetToolCall` |
| Run `SaveEpisodeSummary` | episode summary | saved audit + event | summary + lifecycle；M lock / F rename / P Exec | ID overwrite | `ListEpisodeSummaries` |
| Document `SaveDocumentRecord` | document record | `document.saved` audit + event | record + lifecycle；M lock / F rename / P Exec | ID overwrite；保留 created time | `GetDocumentRecord` |
| Approval `SaveApproval` | approval 和 external-ref lookup | status audit + event | approval + lifecycle；M lock / 带 rollback 或 unknown-outcome fence 的 F rename / P Commit | 精确 ID replay 成功；ID payload 变化或 external-ref 冲突会失败 | `GetApproval`、external-ref lookup、`ReconcileApprovalWrite` |
| Approval `UpdatePendingApproval` | pending approval body | `approval.modified` audit + `approval.pending` event | approval + lifecycle；M lock / 带 rollback 或 unknown-outcome fence 的 F rename / P Commit | 精确 expected-record CAS；authority 字段不可变 | `GetApproval`、`ReconcileApprovalWrite` |
| Approval `ResolveApproval` | terminal status、note、time | terminal audit + event | approval + lifecycle；M lock / 带 rollback 或 unknown-outcome fence 的 F rename / P Commit | 相同 terminal status/note 可 replay；不同决策冲突 | `GetApproval`、`ReconcileApprovalWrite` |
| Schedule `SaveReminder` / `UpdatePendingReminder` | reminder | status audit + event | reminder + lifecycle；M lock / F rename / P Exec | update 使用精确 `UpdatedAt` CAS | `GetReminder` |
| Schedule `ClaimDueReminders` | 有序 due/stale reminder -> sending | 无 | claimed 集；M lock / F rename / P atomic claim query | status/time lease | `GetReminder`、过滤列表 |
| Schedule `SaveReminderDelivery` | delivery 加 reminder status/attempt/error/last delivery | delivery audit + event | delivery + reminder + lifecycle；M lock / F rename / P 要求事务 | delivery ID overwrite | delivery list + `GetReminder` |
| Connector `UpdateConnectorSetting` | `(owner,channel)` setting/version | enable-state audit + event | setting + lifecycle；M lock / F rename / P Commit | expected numeric version | `GetConnectorSetting` |
| Connector binding save/revoke | binding | status audit + event | binding + lifecycle；M lock / F rename / P Exec | ID overwrite；revoke 目标存在 | `GetNotificationBinding` |
| Passive `CreatePassiveNotification` | record、dedupe key、owner revision | received audit | record + revision/audit；M lock / F rename / P Exec | 相同 fingerprint 复用；变化则 conflict | owner/id lookup |
| Passive mark-read / mark-all | read time 和 owner revision | 无 | 受影响 owner 行 + revision；M lock / F rename / P Exec | 已读状态稳定 | get/list/count |
| Passive `PrunePassiveNotifications` | 保留行、dedupe window、owner revision | 每 owner prune audit | 完整 prune 结果 + audit；M lock / F rename / P 要求事务 | cutoff/cap 确定 | owner list/count |
| External chat session/message save | record 和 provider lookup key | status audit + event | record + lifecycle；M lock / F rename / P Exec | ID overwrite；caller 做 external-ID lookup | 精确 get/find 方法 |
| Delivery receive save | receive record 和 source/native key | receive audit | record + audit；M lock / F rename / P Exec | 唯一 source/native | `FindMessageReceive` |
| Delivery send save | delivery record 和 owner/actor/idempotency key | send audit | record + audit；M lock / F rename / P Exec | 唯一 owner/actor/key；caller 检查 content digest | idempotency lookup |
| Delivery inbox update save | update 和 binding/external key | 无 | 单 update；M lock / F rename / P Exec | 唯一 binding/external | `FindChannelInboxUpdate` |
| Credential save/delete | secret record | saved/deleted audit | secret + audit；M lock / F rename / P Exec | ref overwrite/delete | `GetCredentialSecret` |
| Browser auth save/revoke | auth record 和 lookup fields | saved/revoked audit + event | record + lifecycle；M lock / F rename / P Exec | ID；revoke 目标存在 | get/find lookup |
| Browser login block save/update | block/version 和 active lookup | status audit + event | block + lifecycle；M lock / F rename / P Exec | update expected version | `GetBrowserLoginBlock`、active lookup |
| Memory candidate add/resolve | candidate；接受时可能创建 memory | candidate audit + event | candidate + optional memory + lifecycle；M lock / F rename / P 要求事务 | pending-state 前置条件 | candidate list + memory search |
| Memory update/delete/prune | 已接受 memory 集 | action audit + event | record/set + lifecycle；M lock / F rename / P 要求事务 | 目标存在；cutoff | search 加返回集内精确 ID |
| Audit `AddAudit` | audit append | 提供的 audit 就是记录 | 单 append；M lock / F rename / P Exec | 缺少时生成 ID | `ListAudit` |
| Evaluation `SaveEvalRun` | eval run | status audit + event | run + lifecycle；M lock / F rename / P transaction | ID overwrite | `GetEvalRun` |
| Artifact `SaveArtifactObject` | object 加 URI-to-ID 派生索引 | saved audit + event | object + lifecycle/index；M lock / F rename / P Exec | ID overwrite 和索引替换 | `FindArtifactObjectByURI` |

## PostgreSQL 协调清单

完整的约束级比较、解析覆盖和显式未覆盖类别记录在
[PostgreSQL 协调清单](store-s0-postgresql-reconciliation-manifest.md)。可执行
测试比较规范化定义，而不只比较数量。

根 migration 有 18 张表和 16 个索引。`postgresSchema` 包含全部这些对象，
另有 19 张表、26 个索引，并向六张共享表增加 20 个列。不存在仅根侧所有的
表、列或索引。

| 共享表 | 仅 `postgresSchema` 存在的列 |
|---|---|
| `owners` | `source`, `external_ref`, `workspace_root`, `default_channel`, `default_binding_id` |
| `clients` | `owner_id`, `actor_id` |
| `sessions` | `owner_id`, `workspace_root`, `source`, `hidden` |
| `messages` | `attachments`, `requested_media` |
| `tool_calls` | `workflow_id`, `workflow_node_id`, `scope_revision`, `capability`, `error_code`, `policy_context` |
| `approvals` | `policy_context` |

仅 Go 侧表为 `iscp_onboardings`、`mcp_access_tickets`、`mcp_bindings`、
`mcp_operations`、`document_records`、`reminder_deliveries`、
`connector_settings`、`notification_bindings`、`weixin_chat_sessions`、
`weixin_chat_messages`、`external_chat_sessions`、`external_chat_messages`、
`message_receive_records`、`message_delivery_records`、
`channel_inbox_updates`、`passive_notifications`、`credential_secrets`、
`browser_auth_records` 和 `browser_login_blocks`。

仅 Go 侧索引为 `owners_external_ref_idx`、
`iscp_onboardings_owner_created_idx`、`mcp_access_tickets_owner_status_idx`、
`mcp_bindings_active_peer_idx`、`mcp_bindings_owner_status_idx`、
`mcp_operations_binding_updated_idx`、
`document_records_owner_session_activity_idx`、
`document_records_session_path_idx`、`reminder_deliveries_reminder_id_idx`、
`notification_bindings_channel_status_idx`、
`weixin_chat_sessions_binding_user_idx`、
`weixin_chat_sessions_linked_session_idx`、
`weixin_chat_messages_external_idx`、`weixin_chat_messages_chat_created_idx`、
`external_chat_sessions_binding_chat_idx`、
`external_chat_sessions_linked_session_idx`、
`external_chat_messages_external_idx`、
`external_chat_messages_chat_created_idx`、
`message_receive_owner_actor_idx`、`message_delivery_owner_actor_idx`、
`channel_inbox_updates_ready_idx`、
`passive_notifications_owner_created_idx`、
`passive_notifications_owner_unread_idx`、`browser_auth_lookup_idx`、
`browser_login_blocks_active_idx` 和 `idx_artifact_objects_uri`。

约束协调必须保留仅 Go 侧的 MCP ticket secret hash、active MCP peer、MCP
operation idempotency、connector setting 组合 primary key、receive
source/native ID、delivery owner/actor/idempotency、inbox binding/external ID
和 passive notification endpoint/idempotency 唯一契约。S1 migration 还必须
保留现有 FK 和 approval external-ref 部分唯一性。兼容 Weixin 表及其
copy-forward 语句是有意的 migration 输入，不是新的 repository owner。

对于共享对象，除了明确列出的 20 个仅 Go 侧列，完整列定义（类型、inline
PK/FK/UNIQUE、CHECK、default、nullability）、表级约束和完整索引定义都
一致。两个来源中都没有 CHECK、命名约束、表级 FK 或修改约束的 DDL；这些是
已解析的空类别。五条 Go 侧 DML、生成的约束名、已部署数据库状态和
`IF NOT EXISTS` 接管行为明确未做语义解析，不能解释为“没有差异”。

## S2 Pilot 决策

选择 `ISCPOnboardingRepository` 作为 S2 pilot。

- 它只有三个方法和一个业务 service 消费者（`iscppairing.Service`）；第二个
  依赖是 caller 自己负责的 audit 写入。
- `SaveISCPOnboarding` 已返回 error，并把 PostgreSQL 重复 ID 映射到
  `ErrISCPOnboardingConflict`。
- File 已持有 mutex、返回 `persistSnapshotLocked` 失败，并在失败时移除暂存
  receipt。这为使用 S2 完整 snapshot 状态机替换手写 rollback 提供了窄入口。
- 状态只有一个 Snapshot 字段、一张 PostgreSQL 表和一个 owner/time 索引，
  且只存储不含 secret 的 receipt。
- effect submission 是一个持久 receipt，reconciliation read 恰好是
  `GetISCPOnboarding(id)`。

未选择 MCP 作为 pilot，因为其 19 个方法跨越 ticket、binding、operation、
linked session、approval/run helper、多个消费者、幂等、CAS 和多记录撤销。
它保留在 File gate 得到证明后的后续 repository 波次。

## 已记录的缺陷证据

S0 有意不修复以下行为：

- 48 条 File mutation 路径调用 `persist()`；`persist()` 执行
  `_ = s.persistSnapshot()`。snapshot 失败后 caller 仍可能收到成功，之后的
  成功操作还可能持久化暂存状态。
- PostgreSQL 源码包含 33 个显式丢弃的 `Exec` 结果。通用 `collectRows` 路径
  和八个具名 list/prune 函数不会传播 scan/iteration 完成失败；共有 10 个
  row loop 没有对应 `rows.Err()` 检查。
- 多个 PostgreSQL lookup 把任意 scan/decode 失败转换为 `found=false`，多个
  list 方法把 query 失败转换为空列表。
- PostgreSQL 生命周期 append 通常位于命令事务之外，且错误被丢弃。
- 12 个 repository 记录族会从 Memory 和运行中的 File decorator 暴露可变
  alias，使调用方无需另一个 Store 命令即可修改保留的 map、slice 或 pointer；
  受守卫证据矩阵逐项列出所属 owner。

行为刻画测试把这些标记为缺陷证据。S1-S3 中每个负责的迁移必须用失败契约
测试替换对应证据断言。

## 链接

- [Store 契约基础](store-contract-foundation-design.md)
- [Store 可靠性路线图](store-contract-reliability-migration-design.md)
- [S0 验收报告](store-s0-acceptance-report.md)
- [S0 PostgreSQL 协调清单](store-s0-postgresql-reconciliation-manifest.md)
- [PostgreSQL schema 设计](store-postgresql-schema-config-design.md)
- [File 持久性设计](store-file-durability-design.md)
