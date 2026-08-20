# Store S0 Contract Inventory

> Language: English | [简体中文](../zh-cn/docs/store-s0-contract-inventory.md)

> Status: S0 implementation candidate, 2026-08-20. The user authorized S0 to
> start on 2026-08-20. This inventory does not authorize S1, and the S0
> implementation review remains pending human acceptance.

This is the code-fact inventory for commit `df05cf5` plus the S0
characterization tests. The sources are `store.go`, `memory.go`, `file.go`,
`postgres.go`, the ISCP/MCP Store files, `migrations/0001_core.sql`, and every
production `store.Store` reference under `services/gateway`. Method ownership
is executable in `s0_contract_characterization_test.go`; PostgreSQL source
reconciliation is executable in `s0_postgres_manifest_test.go`.

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

## Production Consumer Matrix

The matrix includes every production declaration that currently accepts or
stores `store.Store`, including constructor, field, helper, worker, and
assembly use. Repeated receiver methods use the composite listed for their
owning type.

| Package / symbol | Kind | Minimum repository or consumer-owned composite |
|---|---|---|
| `agent.Runtime`, `NewRuntime`, `NewRuntimeWithContext` | constructor + field | Session + Conversation + Run + Document + Approval + BrowserState + Memory + Audit + ArtifactMetadata |
| `agent.toolExposureEngine`, `newToolExposureEngine` | helper field + constructor | Session + Run + Approval + Audit |
| `store.ArchiveToolObservation` | helper | ArtifactMetadata |
| `credential.Vault`, `credential.New` | constructor + field | Credential |
| `gateway.Server`, `New`, `NewWithTrace` | constructor + field | Owner + Client + Session + Conversation + Run + Approval + Connector + PassiveNotification + ExternalChat + DeliveryRecord + MCP + Memory + Audit + Evaluation + ArtifactMetadata + Credential |
| `gateway.runHasPendingApproval` | helper | Approval |
| `happyapproval.Service`, `New` | polling worker | Approval |
| `iscpbridge.GatewayAdapter`, `NewGatewayAdapter` | adapter + field | Owner + Session + Conversation + Run + Approval + PassiveNotification + Audit |
| `iscppairing.Service`, `New` | service + field | ISCPOnboarding + Audit |
| `mcpaccess.Service`, `New` | service + field | MCP + Run + Approval + Audit |
| `mcpaccess.Provider`, `NewProvider` | provider + field | MCP + Run + ArtifactMetadata |
| `mcpaccess.updateOperationRecord`, `rejectPendingApprovals`, `finalizeRevokedOperations`, approval helpers | helpers | MCP + Run + Approval |
| `notification.SendWeixinText/Image/File/Typing` | helper entry points | Connector + Schedule + Credential |
| `notification.WeixinAdapter`, `NewWeixinAdapter` | adapter + optional field | Connector + Schedule + Credential |
| `reminder.Scheduler`, `NewMessageScheduler` | worker + field | Schedule |
| `remindertarget.Resolver`, `NewResolver` | resolver + field | ExternalChat + Connector |
| `telegram.Dispatcher`, `NewDispatcher` | worker + field | Owner + Session + Conversation + Run + Approval + Schedule + Connector + ExternalChat + DeliveryRecord + ArtifactMetadata + Audit |
| `telegram.Service`, `NewService`, `hasDefaultActiveBinding` | worker + helper | Connector |
| `telegram.NotificationAdapter`, `NewNotificationAdapter` | adapter + field | Connector + Credential |
| `toolhub.ToolHub`, `New` | constructor + field | Session + Run + Approval + Schedule + Connector + ExternalChat + Memory + Audit + ArtifactMetadata |
| `weixin.Dispatcher`, `NewDispatcher`, `NewDispatcherWithConfig` | worker + field | Owner + Session + Conversation + Run + Approval + Connector + ExternalChat + ArtifactMetadata + Audit |
| `weixin.Syncer`, `NewSyncer` | polling worker + field | Connector + Credential + ExternalChat |
| `weixin.MediaAdapter`, `NewMediaAdapter` | adapter + field | Session + ArtifactMetadata + Audit |
| `cmd/sparkclaw.newStore` | backend factory | all 20 repositories as one concrete backend result; no production consumer should receive this broad type after S4 |
| `cmd/sparkclaw.buildRuntime` / bootstrap assembly | assembly forwarding | Agent + ToolHub composites, ISCPOnboarding + Audit, and ArtifactMetadata |
| `cmd/sparkclaw.buildConnectors` | assembly forwarding | Connector + Credential + ExternalChat + DeliveryRecord + Session + Conversation + Owner + Run + Approval + Schedule + ArtifactMetadata + Audit |

No other production `store.Store` declaration exists. `artifact.Store` is a
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

The characterization tests label these as defect evidence. Each owning S1-S3
migration must replace its evidence assertion with a failure-contract test.

## Links

- [Store contract foundation](store-contract-foundation-design.md)
- [Store reliability roadmap](store-contract-reliability-migration-design.md)
- [S0 acceptance report](store-s0-acceptance-report.md)
- [S0 PostgreSQL reconciliation manifest](store-s0-postgresql-reconciliation-manifest.md)
- [PostgreSQL schema design](store-postgresql-schema-config-design.md)
- [File durability design](store-file-durability-design.md)
