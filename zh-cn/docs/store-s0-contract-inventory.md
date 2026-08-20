# Store S0 契约清单

> 语言：[English](../../docs/store-s0-contract-inventory.md) | 简体中文

> 状态：S0 实现候选，2026-08-20。用户已于 2026-08-20 授权开始 S0。
> 本清单不授权 S1，S0 实现审查仍等待人工验收。

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
| `ClientRepository` | 9 | Client、token 查找、撤销、last-seen 和 pairing code |
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

## 后端与持久化映射

全部 141 个方法都有 Memory、File 和 PostgreSQL 实现。File 方法全部位于
`file.go`。普通 Memory/PostgreSQL 方法位于 `memory.go`/`postgres.go`；
方法 22-24 使用 `iscp_onboarding.go` 和 `iscp_onboarding_postgres.go`；方法
25-43 使用 `mcp_access.go` 和 `mcp_access_postgres.go`。`store.go` 的全局
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
20 行 repository 和 10 个维度，把每个证据引用解析到精确测试函数与文件，
拒绝无具体理由的 `N/A`，并要求本文档存在唯一对应行。
`TestS0BackendNeutralRepositorySuccessAndAbsence` 为每个 repository 运行具名
Memory/File 子测试；其余证据键指向相应的附加契约。

| Repository | 成功 | 正常缺失 | 排序/过滤/作用域 | 克隆/别名 | 重复/幂等 | CAS/冲突/删除 | 事件/审计/序列 | File 重启/snapshot | 并发/revision | PostgreSQL row/`rows.Err()` |
|---|---|---|---|---|---|---|---|---|---|---|
| `OwnerRepository` | `BASE` | `BASE` | `OWNER` | `CROSS` | `BASE` | N/A：没有删除、CAS 或冲突命令 | `OWNER` | `CROSS` | N/A：没有 repository revision 或幂等创建结果 | `PG` + `PG-ROWS` |
| `ClientRepository` | `BASE` | `BASE` | `BASE` | `ALIAS` 缺陷 | `BASE` | `BASE` claim/revoke | `BASE` | `FILE-ALL` | N/A：claim/revoke 暴露状态冲突而非 revision | `PG` + `PG-ROWS` |
| `ISCPOnboardingRepository` | `BASE` | `BASE` | `ISCP` | N/A：记录没有可变成员 | `BASE` 唯一 ID 冲突 | `BASE` 冲突 | N/A：lifecycle audit 由调用方拥有 | `ISCP` | N/A：唯一 ID 是边界且没有 revision | `ISCP-PG` + `PG-ROWS` |
| `CredentialRepository` | `BASE` | `BASE` | N/A：仅有精确 ref 查找 | N/A：记录没有可变成员 | `BASE` ref overwrite | `BASE` 删除 | `BASE` audit | `CREDENTIAL-FILE` | N/A：没有 revision/幂等创建结果 | N/A：仅 QueryRow/Exec，没有 row iterator |
| `SessionRepository` | `BASE` | `BASE` | `BASE` hidden 过滤 | N/A：记录没有可变成员 | N/A：create 始终分配新 ID | `BASE` + `BROWSER` 删除 | `BASE` | `CROSS` | N/A：没有 version/revision 契约 | `PG` + `BROWSER-PG` + `PG-ROWS` |
| `ConversationRepository` | `BASE` | `BASE` | `MESSAGE` | `ALIAS` 缺陷 | `BASE` message-ID reuse | N/A：append/cursor API 没有更新或删除 | `CROSS` + `MESSAGE` | `MESSAGE-FILE` + `CROSS` | N/A：event sequence 而非 CAS revision 对 append 排序 | `PG` |
| `RunRepository` | `BASE` | `BASE` | `RUN` | `ALIAS` 缺陷 | `RUN` feedback replacement | N/A：overwrite/append API 没有删除或 CAS | `RUN` | `FILE-ALL` + `RUN-FILE` | N/A：没有调用方可见的 winner revision | `PG` + `PG-ROWS` |
| `DocumentRepository` | `BASE` + `CROSS` | `BASE` + `CROSS` | `DOCUMENT` | N/A：记录没有可变成员 | `DOCUMENT` ID overwrite | N/A：没有删除、CAS 或冲突命令 | `DOCUMENT` | `DOCUMENT-FILE` | N/A：没有 revision/幂等创建结果 | `PG` + `PG-ROWS` |
| `ApprovalRepository` | `BASE` | `BASE` | `APPROVAL` | `ALIAS` 缺陷 | `APPROVAL` external-ref identity | `APPROVAL` pending-state conflict | `APPROVAL` | `APPROVAL-FILE` | N/A：pending 状态而非数字 revision 是前置条件 | `PG` + `PG-ROWS` |
| `ScheduleRepository` | `BASE` | `BASE` | `BASE` due 排序/过滤 | `ALIAS` 缺陷 | `BASE` ID overwrite | `SCHEDULE` CAS | `BASE` | `FILE-GAPS` | `SCHEDULE` CAS winner | `PG` + `PG-ROWS` |
| `ConnectorRepository` | `BASE` | `BASE` | `CONNECTOR` | `ALIAS` 缺陷 | `BASE` binding ID overwrite | `CONNECTOR` CAS | `CONNECTOR` | `CONNECTOR-FILE` + `FILE-GAPS` | `CONNECTOR` 数字 revision | `CONNECTOR-PG` |
| `PassiveNotificationRepository` | `BASE` + `PASSIVE` | `BASE` + `PASSIVE` | `PASSIVE` | `ALIAS` 缺陷 | `PASSIVE` 幂等 replay/conflict | `PASSIVE` prune/replay | `PASSIVE` revision/audit | `PASSIVE-FILE` | `PASSIVE` 进程内 revision | `PASSIVE-PG` + `PG-ROWS` |
| `ExternalChatRepository` | `BASE` + `EXTERNAL` | `BASE` | `EXTERNAL` | N/A：记录没有可变成员 | `EXTERNAL` ID/external-ID 行为 | N/A：没有删除、CAS 或类型化冲突命令 | `EXTERNAL` | `EXTERNAL-FILE` | N/A：external-ID lookup 是 reconciliation 而非 CAS | `EXTERNAL-PG` + `PG-ROWS` |
| `DeliveryRecordRepository` | `BASE` + `DELIVERY` | `BASE` | `DELIVERY` + `EXTERNAL` | `ALIAS` 缺陷 | `DELIVERY` + `EXTERNAL` dedupe key | N/A：lifecycle overwrite API 没有删除/CAS | `DELIVERY` | `DELIVERY-FILE` + `EXTERNAL-FILE` | N/A：dedupe key 序列化写入但没有数字 revision | `EXTERNAL-PG` + `PG-ROWS` |
| `MCPRepository` | `BASE` + `MCP` | `BASE` | `MCP` owner scope | `MCP-ALIAS` | `MCP` 幂等性 | `MCP` CAS/revoke/delete | N/A：lifecycle audit 由调用方拥有；operation version 是序列 | `MCP-FILE` + `CROSS` | `CROSS` 并发幂等/version | `MCP-PG` + `PG-ROWS` |
| `BrowserStateRepository` | `BASE` + `BROWSER` | `BASE` | `BROWSER` | `ALIAS` 缺陷 | `BROWSER` 规范化重复 ID | `BROWSER` CAS/delete | `BROWSER` | `BROWSER-FILE` + `FILE-ALL` | `BROWSER` 数字 revision | `BROWSER-PG` |
| `MemoryRepository` | `BASE` + `MEMORY` | `BASE` | `MEMORY` | `ALIAS` 缺陷 | N/A：candidate create 分配 ID；resolve 是状态转换 | `MEMORY` delete/prune | `MEMORY` | `FILE-ALL` + `MEMORY-FILE` | N/A：状态冲突没有数字 revision | `PG` + `PG-ROWS` |
| `AuditRepository` | `BASE` | `BASE` | `BASE` session/order | `ALIAS` 缺陷 | N/A：append 生成 ID 且没有幂等键 | N/A：仅 append，没有 update/delete/CAS | `BASE` supplied audit 加有序 event | `MEMORY-FILE` | N/A：event cursor sequence 是唯一排序 token | `PG` + `PG-ROWS` |
| `EvaluationRepository` | `BASE` | `BASE` | `BASE` newest-first | `ALIAS` 缺陷 | `BASE` ID overwrite | N/A：没有删除、CAS 或冲突命令 | `BASE` | `FILE-ALL` | N/A：没有 revision/幂等创建结果 | `PG` + `PG-ROWS` |
| `ArtifactMetadataRepository` | `BASE` + `ARTIFACT` | `BASE` + `ARTIFACT` | `ARTIFACT` | N/A：记录没有可变成员 | `ARTIFACT` ID/URI replacement | `ARTIFACT` session-delete cleanup | `ARTIFACT` | `FILE-ALL` | N/A：ID overwrite 原子替换 URI 索引且没有 revision | `PG` + `PG-ROWS` |

证据键解析到以下精确测试与位置：

- `BASE`：[`TestS0BackendNeutralRepositorySuccessAndAbsence`](../../services/gateway/internal/store/s0_repository_characterization_test.go)，使用 repository 具名 Memory/File 子测试。
- `CROSS`：[`TestS0BackendNeutralContractCharacterization`](../../services/gateway/internal/store/s0_contract_characterization_test.go)。
- `ALIAS`：[`TestS0DefectEvidenceMutableAliases`](../../services/gateway/internal/store/s0_repository_characterization_test.go)，使用 repository 具名 Memory/File 子测试。它记录当前不安全行为，不是期望契约。
- `FILE-GAPS`：[`TestS0FileRepositoryRestartGaps`](../../services/gateway/internal/store/s0_repository_characterization_test.go)；`FILE-ALL`：[`TestFileStorePersistsAndReloadsState`](../../services/gateway/internal/store/file_test.go)；`PG-ROWS`：[`TestS0DefectEvidencePostgresRowsErrIsNotChecked`](../../services/gateway/internal/store/s0_contract_characterization_test.go)。
- `OWNER`：[`TestMemoryStoreUpdatesOwnerProfile`](../../services/gateway/internal/store/memory_test.go)、[`TestMemoryStoreManagesMultipleOwnerProfiles`](../../services/gateway/internal/store/memory_test.go)。
- `ISCP`：[`TestFileStorePersistsOnlyISCPOnboardingReceipt`](../../services/gateway/internal/store/mcp_access_test.go)；`ISCP-PG`：[`TestPostgresStorePersistsOnlyISCPOnboardingReceipt`](../../services/gateway/internal/store/postgres_test.go)。
- `CREDENTIAL-FILE`：[`TestFileStoreEncryptsStateAtRest`](../../services/gateway/internal/store/file_test.go)。
- `MESSAGE`：[`TestMemoryMessageEventsAreBoundedAndSessionScoped`](../../services/gateway/internal/store/message_events_test.go)；`MESSAGE-FILE`：[`TestFileMessageEventsSurviveRestart`](../../services/gateway/internal/store/message_events_test.go)。
- `RUN`：[`TestMemoryStoreSavesRunFeedback`](../../services/gateway/internal/store/memory_test.go)；`RUN-FILE`：[`TestFileStorePersistsWorkflowStateAndToolBinding`](../../services/gateway/internal/store/file_test.go)。
- `DOCUMENT`：[`TestMemoryStoreDocumentRecordsAreRecentAndSessionScoped`](../../services/gateway/internal/store/memory_test.go)；`DOCUMENT-FILE`：[`TestFileStorePersistsDocumentRecords`](../../services/gateway/internal/store/file_test.go)。
- `APPROVAL`：[`TestMemoryStoreFindsExternalApprovalByStableReference`](../../services/gateway/internal/store/memory_test.go)；`APPROVAL-FILE`：[`TestFileStorePersistsExternalApprovalContext`](../../services/gateway/internal/store/file_test.go)、[`TestFileStorePersistsPolicyExecutionContext`](../../services/gateway/internal/store/file_test.go)。
- `SCHEDULE`：[`TestMemoryStoreUpdatePendingReminderUsesCompareAndSwap`](../../services/gateway/internal/store/memory_test.go)。
- `CONNECTOR`：[`TestMemoryStoreConnectorSettingUsesCASAndOwnerScope`](../../services/gateway/internal/store/connector_settings_test.go)、[`TestMemoryStoreListsAllConnectorSettingsInStableOwnerChannelOrder`](../../services/gateway/internal/store/connector_settings_test.go)；`CONNECTOR-FILE`：[`TestFileStorePersistsConnectorSettingVersion`](../../services/gateway/internal/store/connector_settings_test.go)；`CONNECTOR-PG`：[`TestPostgresStoreListsAllConnectorSettings`](../../services/gateway/internal/store/postgres_test.go)。
- `PASSIVE`：[`TestMemoryStorePassiveNotificationIdempotencyAndOwnerScope`](../../services/gateway/internal/store/passive_notifications_test.go)、[`TestMemoryStorePassiveNotificationIdempotentReingestionAtScale`](../../services/gateway/internal/store/passive_notifications_test.go)、[`TestPrunePassiveNotificationsRetentionSweep`](../../services/gateway/internal/store/passive_notifications_test.go)、[`TestPrunePassiveNotificationsCapEvictsReadOldestFirst`](../../services/gateway/internal/store/passive_notifications_test.go)、[`TestPassiveNotificationRevisionSignalsInboxChanges`](../../services/gateway/internal/store/passive_notifications_test.go)；`PASSIVE-FILE`：[`TestFileStorePassiveNotificationSurvivesRestart`](../../services/gateway/internal/store/passive_notifications_test.go)、[`TestFileStoreSnapshotRebuildsPassiveNotificationIndex`](../../services/gateway/internal/store/passive_notifications_test.go)；`PASSIVE-PG`：[`TestPostgresStorePassiveNotificationPruneAndRevision`](../../services/gateway/internal/store/postgres_test.go)。
- `EXTERNAL`：[`TestMemoryStoreExternalChatAndInboxParity`](../../services/gateway/internal/store/external_chat_test.go)；`EXTERNAL-FILE`：[`TestFileStoreExternalChatAndInboxParity`](../../services/gateway/internal/store/external_chat_test.go)；`EXTERNAL-PG`：[`TestPostgresStoreExternalChatAndInboxParity`](../../services/gateway/internal/store/postgres_test.go)。
- `DELIVERY`：[`TestMemoryStoreMessageLifecycleParity`](../../services/gateway/internal/store/message_lifecycle_test.go)；`DELIVERY-FILE`：[`TestFileStoreMessageLifecycleRoundTrip`](../../services/gateway/internal/store/message_lifecycle_test.go)。
- `MCP`：[`TestMCPAccessTicketRedemptionIsAtomicAndDeviceBound`](../../services/gateway/internal/store/mcp_access_test.go)、[`TestMCPOperationIdempotencyRejectsChangedRequest`](../../services/gateway/internal/store/mcp_access_test.go)、[`TestMCPBindingRevocationTerminatesOnlyNonterminalOperations`](../../services/gateway/internal/store/mcp_access_test.go)、[`TestMCPAccessRecordsCanBeDeletedIndividuallyAndByOwner`](../../services/gateway/internal/store/mcp_access_test.go)；`MCP-ALIAS`：[`TestMemoryStoreMCPRecordsCannotBeMutatedOutsideStore`](../../services/gateway/internal/store/mcp_access_test.go)；`MCP-FILE`：[`TestFileStorePersistsMCPAccessWithoutPlaintextSecret`](../../services/gateway/internal/store/mcp_access_test.go)；`MCP-PG`：[`TestPostgresStoreMCPAccessAtomicityIdempotencyAndRecovery`](../../services/gateway/internal/store/postgres_test.go)。
- `BROWSER`：[`TestMemoryStoreScopesBrowserAuthRecordsByOwnerProfileAndSite`](../../services/gateway/internal/store/memory_test.go)、[`TestMemoryStoreFindActiveBrowserLoginBlockPicksNewestStoredUpdate`](../../services/gateway/internal/store/memory_test.go)、[`TestMemoryStoreBrowserLoginBlockTrimsIDOnWrite`](../../services/gateway/internal/store/memory_test.go)、[`TestMemoryStoreBrowserHandoffCASPreservesRevisionTwoFields`](../../services/gateway/internal/store/memory_test.go)、[`TestMemoryStoreDeleteSessionRemovesBrowserLoginBlocks`](../../services/gateway/internal/store/memory_test.go)；`BROWSER-FILE`：[`TestFileStoreBrowserHandoffCASRoundTrip`](../../services/gateway/internal/store/file_test.go)；`BROWSER-PG`：[`TestPostgresStoreBrowserHandoffCASRoundTrip`](../../services/gateway/internal/store/postgres_test.go)、[`TestPostgresStoreFindActiveBrowserLoginBlockMatchesSharedActivePredicate`](../../services/gateway/internal/store/postgres_test.go)、[`TestPostgresStoreDeleteSessionRemovesBrowserLoginBlocks`](../../services/gateway/internal/store/postgres_test.go)。
- `MEMORY`：[`TestMemoryStoreUpdatesAndDeletesAcceptedMemory`](../../services/gateway/internal/store/memory_test.go)、[`TestMemoryStorePrunesExpiredMemories`](../../services/gateway/internal/store/memory_test.go)；`MEMORY-FILE`：[`TestFileStorePersistsMemoryRetentionPrune`](../../services/gateway/internal/store/file_test.go)。
- `ARTIFACT`：[`TestMemoryStoreListsArtifactObjectsNewestFirst`](../../services/gateway/internal/store/memory_test.go)、[`TestMemoryStoreFindsArtifactObjectByURI`](../../services/gateway/internal/store/memory_test.go)。
- `PG`：[`TestPostgresStoreRoundTrip`](../../services/gateway/internal/store/postgres_test.go)。所有 PostgreSQL 键继续受 DSN gate；未设置 DSN 时不能计为通过。

`ALIAS` 证据当前记录 Client、Conversation、Run、Approval、Schedule、
Connector、PassiveNotification、DeliveryRecord、BrowserState、Memory、Audit
和 Evaluation 记录在 Memory 以及运行中的 File decorator 中存在可变别名逃逸。
S0 不修复这些生产契约。每个负责的 repository wave 必须用隔离断言替换相应
defect-evidence 单元格。

## 生产消费者矩阵

矩阵同时包含每个直接接受/保存 `store.Store` 的生产声明，以及方法集与
`Store` 相交的每个具名生产局部接口。它覆盖 constructor、field、helper、
worker、adapter、resolver 和 assembly 使用。重复 receiver 方法使用其所属
类型所列 composite。

`TestS0ProductionStoreConsumerInventory` 扫描 Gateway 中全部非测试 Go 文件。
它按文件和外围 symbol 冻结每个直接 `store.Store` 声明、Store package 内
helper，以及每个具名局部 Store-compatible 接口的展开 Store 方法集。新增、
删除或重命名声明都会使可执行清单失败，并要求重新审查本矩阵。

| Package / symbol | 类型 | 最小 repository 或消费者自有 composite |
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
| `notification.SendWeixinText/Image/File/Typing` | helper 入口 | Connector + Schedule + Credential |
| `notification.WeixinAdapter`, `NewWeixinAdapter` | adapter + optional field | Connector + Schedule + Credential + Session + ExternalChat + ArtifactMetadata |
| `reminder.Scheduler`, `NewMessageScheduler` | worker + field | Schedule |
| `remindertarget.Resolver`, `NewResolver` | resolver + field | ExternalChat + Connector |
| `telegram.Dispatcher`, `NewDispatcher` | worker + field | Owner + Session + Conversation + Run + Approval + Schedule + Connector + ExternalChat + DeliveryRecord + ArtifactMetadata + Audit |
| `telegram.Service`, `NewService`, `hasDefaultActiveBinding` | worker + helper | Connector |
| `telegram.NotificationAdapter`, `NewNotificationAdapter` | adapter + field | Connector + Schedule + Session + ExternalChat + ArtifactMetadata；credential 来自独立 `CredentialVault`，不归其 Store 参数 |
| `toolhub.ToolHub`, `New` | constructor + field | Session + Run + Approval + Schedule + Connector + ExternalChat + Memory + Audit + ArtifactMetadata |
| `weixin.Dispatcher`, `NewDispatcher`, `NewDispatcherWithConfig` | worker + field | Owner + Session + Conversation + Run + Approval + Connector + ExternalChat + DeliveryRecord + ArtifactMetadata + Audit |
| `weixin.Syncer`, `NewSyncer`, `WithConfig` | polling worker + field + adapter assembly | Session + Connector + Credential + ExternalChat + DeliveryRecord + ArtifactMetadata + Audit |
| `weixin.MediaAdapter`, `NewMediaAdapter` | adapter + field | Session + ArtifactMetadata + Audit |
| `cmd/sparkclaw.newStore` | backend factory | 20 个 repository 组成的单一 concrete backend 结果；S4 后任何生产消费者都不应再接收此宽类型 |
| `cmd/sparkclaw.buildRuntime` / bootstrap assembly | assembly forwarding | Agent + ToolHub composite、ISCPOnboarding + Audit，以及 ArtifactMetadata |
| `cmd/sparkclaw.buildConnectors` | assembly forwarding | Connector + Credential + ExternalChat + DeliveryRecord + Session + Conversation + Owner + Run + Approval + Schedule + ArtifactMetadata + Audit |

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

不存在其他直接或具名局部 Store-compatible 生产声明。`delivery.EndpointRegistry`
和 `delivery.WebDelivery` 是 delivery 领域接口，不是 Store-compatible 接口。
`artifact.Store` 是独立的 artifact-object 接口，不属于本次迁移。

## Mutation 与命令矩阵

`M`、`F`、`P` 分别表示 Memory 锁内 mutation、File 原子替换和 PostgreSQL。
`F rename` 是未来的持久 effect submission point；当前 48 个旧 File 调用点会
丢弃结果。`P Exec` 是 autocommit submission point，`P Commit` 是事务
submission point；当前 33 个 PostgreSQL `Exec` 结果被显式丢弃。未来必需的
生命周期行必须加入同一事务，即使 PostgreSQL 当前是在命令后追加且忽略失败。

| Repository / 命令 | 记录和派生索引 mutation | 必需 event/audit | 原子边界和 effect submission | 幂等 / CAS | Reconciliation read |
|---|---|---|---|---|---|
| Session `CreateSession*` | session | `session.created` audit + event | session + lifecycle；M lock / F rename / P Exec | 生成 ID | `GetSession` |
| Session `UpdateSessionTitle` | session title/update time | `session.updated` audit + event | session + lifecycle；M lock / F rename / P Exec | 当前必须存在 | `GetSession` |
| Session `DeleteSession` | session，以及 message、run、feedback、model/tool call、document、approval、reminder/delivery、candidate/memory、browser block、artifact/URI 索引、episode summary、关联 external chat、旧 session audit/event | 替换后的 `session.deleted` audit + event | 单个跨 repository 事务；M lock / F rename / P Commit | 目标必须存在 | `GetSession` 缺失加各 scope 列表读取 |
| Client `SaveClient` / `RevokeClient` | client 和 token lookup state | `client.saved` / `client.revoked` audit + event | record + lifecycle；M lock / F rename / P Exec | ID overwrite；revoke 要求目标 | `GetClient`, `FindClientByTokenHash` |
| Client `TouchClient` | last-seen | 无 | 单记录；M lock / F rename / P Exec | 目标缺失当前为 no-op | `GetClient` |
| Client `SavePairingCode` / `ClaimPairingCode` | pairing code status、claim time、client link | created/claimed audit + event | record + lifecycle；M lock / F rename / claim state check 使用 P Commit | pending + expiry state | `GetPairingCode` |
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
| Approval `SaveApproval` | approval 和 external-ref lookup | status audit + event | approval + lifecycle；M lock / F rename / P Exec | P 中部分唯一 external ref | ID/external-ref lookup |
| Approval `UpdatePendingApproval` | pending approval body | `approval.pending` event | approval + event；M lock / F rename / P CAS-like Exec | 目标必须保持 pending | `GetApproval` |
| Approval `ResolveApproval` | terminal status、note、time | terminal audit + event | approval + lifecycle；M lock / F rename / P Exec | pending-state 前置条件 | `GetApproval` |
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
| Evaluation `SaveEvalRun` | eval run | status audit + event | run + lifecycle；M lock / F rename / P Exec | ID overwrite | `GetEvalRun` |
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
