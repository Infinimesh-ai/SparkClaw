# Store S0 PostgreSQL 协调清单

> 语言：[English](../../docs/store-s0-postgresql-reconciliation-manifest.md) | 简体中文

> 状态：S0 静态源码证据已于 2026-08-20 在
> `207462154fa2377ed786af671f41e0f353d11ba9` 验收。本文不修改 schema；
> runtime migration 和配置后的 PostgreSQL 证据由 S1 负责。

## 来源与可执行权威

本清单比较仓库根目录的 `migrations/0001_core.sql` 与
`services/gateway/internal/store/postgres.go` 中的 `postgresSchema` 字符串。
`TestS0PostgresReconciliationManifest` 是可执行权威；它解析并比较定义，而不
只比较对象数量。

根 migration 包含 18 张表和 16 个索引。`postgresSchema` 包含相同对象，另有
19 张表、六张共享表上的 20 个列和 26 个索引，共 37 张表和 42 个索引。
不存在仅根 migration 所有的表、列或索引。

## 解析覆盖与证据标签

可执行解析器覆盖：

- `CREATE TABLE` 列类型、inline `PRIMARY KEY`、`REFERENCES`、`UNIQUE`、
  `CHECK`、`NOT NULL` 和 `DEFAULT` 定义；
- 表级 `PRIMARY KEY`、`FOREIGN KEY`、`UNIQUE`、`CHECK`，包括可选的命名
  `CONSTRAINT` 前缀；
- 带完整定义的 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`；
- `ALTER TABLE ... ALTER COLUMN ... DROP NOT NULL`；
- 完整的 `CREATE [UNIQUE] INDEX IF NOT EXISTS` 定义，包括表、列顺序、
  `ASC`/`DESC` 和部分索引谓词；
- 每条相关 `CREATE`、`ALTER` 或 `DROP` 语句：无法识别的 DDL 会使清单测试
  失败，不会从比较中静默消失。

本文的证据标签有严格含义：

| 标签 | 含义 |
|---|---|
| **完全一致** | 两个来源均已解析，规范化定义相同 |
| **仅 Go 侧** | 完整解析对象或定义只出现在 `postgresSchema` |
| **两个来源均不存在** | 该语法类别已解析，且断言后的集合为空 |
| **未做语义解析** | 解析器明确不证明此事实；这不代表“没有差异” |

规范化会统一关键字/标识符大小写和无意义空白，但不会改写类型、表达式、列
顺序、排序方向、谓词或约束内容。

## 共享表与列

可执行清单比较每个共享列的完整解析定义，包括类型、inline
PK/FK/UNIQUE/CHECK、default 和 nullability。全部原有共享列都**完全一致**；
每张共享表的表级约束集合也**完全一致**。

| 共享表 | 结果 | 仅 Go 侧列 |
|---|---|---|
| `owners` | 原有列和约束完全一致 | `source`、`external_ref`、`workspace_root`、`default_channel`、`default_binding_id` |
| `clients` | 原有列和约束完全一致 | `owner_id`、`actor_id` |
| `pairing_codes` | 全部列和约束完全一致 | 无 |
| `sessions` | 原有列和约束完全一致 | `owner_id`、`workspace_root`、`source`、`hidden` |
| `messages` | 原有列和约束完全一致 | `attachments`、`requested_media` |
| `agent_runs` | 全部列和约束完全一致 | 无 |
| `run_feedback` | 全部列和约束完全一致 | 无 |
| `model_calls` | 全部列和约束完全一致 | 无 |
| `tool_calls` | 原有列和约束完全一致 | `workflow_id`、`workflow_node_id`、`scope_revision`、`capability`、`error_code`、`policy_context` |
| `approvals` | 原有列和约束完全一致 | `policy_context` |
| `audit_events` | 全部列和约束完全一致 | 无 |
| `events` | 全部列和约束完全一致 | 无 |
| `memories` | 全部列和约束完全一致 | 无 |
| `memory_candidates` | 全部列和约束完全一致 | 无 |
| `eval_runs` | 全部列和约束完全一致 | 无 |
| `artifact_objects` | 全部列和约束完全一致 | 无 |
| `episode_summaries` | 全部列和约束完全一致 | 无 |
| `reminders` | 经过 `ADD COLUMN` 协调后，全部列和约束完全一致 | 无 |

20 个仅 Go 侧列的完整定义（包括 default 和 nullability）如下：

| 表 | 列 | 完整定义 |
|---|---|---|
| `owners` | `source` | `TEXT NOT NULL DEFAULT ''` |
| `owners` | `external_ref` | `TEXT NOT NULL DEFAULT ''` |
| `owners` | `workspace_root` | `TEXT NOT NULL DEFAULT ''` |
| `owners` | `default_channel` | `TEXT NOT NULL DEFAULT ''` |
| `owners` | `default_binding_id` | `TEXT NOT NULL DEFAULT ''` |
| `clients` | `owner_id` | `TEXT NOT NULL DEFAULT 'owner'` |
| `clients` | `actor_id` | `TEXT NOT NULL DEFAULT 'owner'` |
| `sessions` | `owner_id` | `TEXT NOT NULL DEFAULT 'owner'` |
| `sessions` | `workspace_root` | `TEXT NOT NULL DEFAULT ''` |
| `sessions` | `source` | `TEXT NOT NULL DEFAULT 'webchat'` |
| `sessions` | `hidden` | `BOOLEAN NOT NULL DEFAULT false` |
| `messages` | `attachments` | `JSONB NOT NULL DEFAULT '[]'` |
| `messages` | `requested_media` | `JSONB NOT NULL DEFAULT '[]'` |
| `tool_calls` | `workflow_id` | `TEXT NOT NULL DEFAULT ''` |
| `tool_calls` | `workflow_node_id` | `TEXT NOT NULL DEFAULT ''` |
| `tool_calls` | `scope_revision` | `INTEGER NOT NULL DEFAULT 0` |
| `tool_calls` | `capability` | `TEXT NOT NULL DEFAULT ''` |
| `tool_calls` | `error_code` | `TEXT`（可空、无 default） |
| `tool_calls` | `policy_context` | `JSONB`（可空、无 default） |
| `approvals` | `policy_context` | `JSONB`（可空、无 default） |

三个 approval `DROP NOT NULL` 语句在两个来源中都已解析并应用。因此其最终
共享定义是**完全一致**，而不是被当作 ALTER 语法忽略。

## 共享索引

两个来源中全部 16 个共享索引的完整定义都**完全一致**：

| 索引 | 解析后的定义 |
|---|---|
| `approvals_external_ref_idx` | `UNIQUE ON approvals(source,external_id) WHERE external_id <> ''` |
| `idx_approvals_status` | `ON approvals(status)` |
| `idx_artifact_objects_created` | `ON artifact_objects(created_at DESC)` |
| `idx_artifact_objects_run` | `ON artifact_objects(run_id)` |
| `idx_audit_session_time` | `ON audit_events(session_id,happened_at DESC)` |
| `idx_clients_token_hash` | `ON clients(token_hash)` |
| `idx_episode_summaries_session_created` | `ON episode_summaries(session_id,created_at DESC)` |
| `idx_eval_runs_started` | `ON eval_runs(started_at DESC)` |
| `idx_events_session_seq` | `ON events(session_id,seq)` |
| `idx_messages_session_created` | `ON messages(session_id,created_at)` |
| `idx_model_calls_session_run_started` | `ON model_calls(session_id,run_id,started_at)` |
| `idx_pairing_codes_status_expires` | `ON pairing_codes(status,expires_at)` |
| `idx_run_feedback_run_updated` | `ON run_feedback(run_id,updated_at DESC)` |
| `idx_tool_calls_run` | `ON tool_calls(run_id)` |
| `memories_created_at_idx` | `ON memories(created_at)` |
| `reminders_status_due_time_idx` | `ON reminders(status,due_time)` |

## 仅 Go 侧表与约束

以下内容冻结每张仅 Go 侧表的完整规范化定义。每项包含全部列名与类型、
inline PK/FK/UNIQUE/CHECK 标记、default、nullability，以及全部 table-level
constraint。因此，缺少某项属性表示解析后的确不存在，而不是摘要遗漏。

```text
browser_auth_records: column account_hint text not null default '' | column auth_strategy text not null | column browser_profile_id text not null | column cookie_jar_ref text not null default '' | column created_at timestamptz not null default now() | column credential_ref text not null default '' | column expires_at timestamptz | column id text primary key | column last_error text not null default '' | column last_verified_at timestamptz | column owner_id text not null | column revoked_at timestamptz | column session_ref text not null default '' | column site_origin text not null | column site_realm text not null default '' | column status text not null | column updated_at timestamptz not null default now()
browser_login_blocks: column account_hint text not null default '' | column browser_auth_status text not null default '' | column browser_profile_id text not null default 'default' | column created_at timestamptz not null default now() | column id text primary key | column last_error text not null default '' | column last_tool_call_id text not null default '' | column last_user_reply text not null default '' | column last_visible_page_id text not null default '' | column login_handoff_page_id text not null default '' | column login_handoff_url text not null default '' | column original_goal text not null default '' | column owner_id text not null default 'owner' | column resolved_at timestamptz | column resume_args jsonb not null default '{}' | column resume_tool text not null default 'browser.read' | column run_id text not null references agent_runs(id) | column schema_version integer not null default 2 | column session_generation bigint not null default 0 | column session_id text not null references sessions(id) | column site_origin text not null default '' | column site_realm text not null default '' | column status text not null | column target jsonb not null default '{}' | column transition_lease_until timestamptz | column transition_owner_id text not null default '' | column updated_at timestamptz not null default now() | column version bigint not null default 1 | column visible_evidence jsonb not null default 'null' | column workflow_id text not null default '' | column workflow_node_id text not null default '' | column workflow_revision integer not null default 0
channel_inbox_updates: column attempts integer not null default 0 | column available_at timestamptz not null default now() | column binding_id text not null | column channel text not null | column chat_key text not null default '' | column created_at timestamptz not null default now() | column external_id text not null | column id text primary key | column last_error text not null default '' | column payload jsonb not null default '{}' | column status text not null | column updated_at timestamptz not null default now() | constraint unique(binding_id,external_id)
connector_settings: column channel text not null | column enabled boolean not null default false | column iscp_enabled boolean not null default false | column lan_access_enabled boolean not null default false | column owner_id text not null | column updated_at timestamptz not null default now() | column updated_by text not null default '' | column version bigint not null default 1 | constraint primary key(owner_id,channel)
credential_secrets: column created_at timestamptz not null default now() | column kind text not null | column ref text primary key | column updated_at timestamptz not null default now() | column value text not null
document_records: column content_type text not null default '' | column created_at timestamptz not null default now() | column format text not null default '' | column governed_path text not null | column id text primary key | column last_activity text not null | column last_activity_at timestamptz not null | column last_activity_id text not null | column name text not null | column owner_id text not null default 'owner' | column parent_document_id text not null default '' | column session_id text not null references sessions(id) | column sha256 text not null default '' | column size_bytes bigint not null default 0 | column source text not null | column source_message_id text not null default '' | column source_run_id text not null default '' | column source_tool_call_id text not null default '' | column status text not null | column updated_at timestamptz not null default now()
external_chat_messages: column binding_id text not null | column channel text not null | column chat_session_id text not null | column content text not null | column context_token text not null default '' | column created_at timestamptz not null default now() | column direction text not null | column dispatch_attempts integer not null default 0 | column error text not null default '' | column external_message_id text not null default '' | column id text primary key | column linked_run_id text not null default '' | column pending_reply text not null default '' | column pending_reply_kind text not null default '' | column role text not null | column status text not null | column updated_at timestamptz not null default now()
external_chat_sessions: column authorized_actor_id text not null default '' | column authorized_owner_id text not null default '' | column binding_id text not null | column channel text not null | column created_at timestamptz not null default now() | column display_name text not null default '' | column external_chat_id text not null default '' | column external_thread_id text not null default '' | column external_user_id text not null default '' | column id text primary key | column last_context_token text not null default '' | column linked_session_id text not null default '' | column owner_id text not null default '' | column provider text not null default '' | column provider_cursor text not null default '' | column status text not null | column updated_at timestamptz not null default now() | column workspace_root text not null default ''
iscp_onboardings: column authority_ref text not null | column created_at timestamptz not null | column domain_id text not null | column id text primary key | column owner_id text not null | column payload jsonb not null | column status text not null | column ticket_id text not null
mcp_access_tickets: column domain_id text not null | column expires_at timestamptz not null | column id text primary key | column owner_id text not null | column payload jsonb not null | column secret_hash text not null unique | column status text not null
mcp_bindings: column domain_id text not null | column id text primary key | column owner_id text not null | column payload jsonb not null | column requester_device_id text not null | column requester_key_thumbprint text not null | column status text not null | column updated_at timestamptz not null
mcp_operations: column binding_id text not null references mcp_bindings(id) | column fingerprint text not null | column id text primary key | column idempotency_key text not null | column payload jsonb not null | column updated_at timestamptz not null | column version bigint not null | constraint unique(binding_id,idempotency_key)
message_delivery_records: column actor_id text not null | column content_digest text not null | column id text primary key | column idempotency_key text not null | column owner_id text not null | column record jsonb not null | column status text not null | column updated_at timestamptz not null default now() | constraint unique(owner_id,actor_id,idempotency_key)
message_receive_records: column actor_id text not null | column id text primary key | column native_message_id text not null | column owner_id text not null | column record jsonb not null | column source_endpoint_id text not null default '' | column status text not null | column updated_at timestamptz not null default now() | constraint unique(source_endpoint_id,native_message_id)
notification_bindings: column account_id text not null default '' | column actor_id text not null default '' | column base_url text not null default '' | column channel text not null | column context_token text not null default '' | column created_at timestamptz not null default now() | column credential_ref text not null default '' | column default_for_channel boolean not null default false | column display_name text not null default '' | column expires_at timestamptz | column external_chat_id text not null default '' | column external_thread_id text not null default '' | column external_user_id text not null default '' | column id text primary key | column last_error text not null default '' | column owner_id text not null | column provider text not null | column provider_cursor text not null default '' | column provider_session_id text not null default '' | column provider_state text not null default '' | column qr_code_image text not null default '' | column qr_code_url text not null default '' | column revoked_at timestamptz | column scopes jsonb not null default '[]' | column status text not null | column updated_at timestamptz not null default now()
passive_notifications: column created_at timestamptz not null default now() | column deep_link text not null | column endpoint_id text not null | column fingerprint text not null | column id text primary key | column idempotency_key text not null | column kind text not null | column notification_id text not null | column occurred_at timestamptz not null | column owner_id text not null | column read_at timestamptz | column source text not null | column updated_at timestamptz not null default now() | constraint unique(endpoint_id,idempotency_key)
reminder_deliveries: column attempt integer not null default 0 | column channel text not null | column created_at timestamptz not null default now() | column error text not null default '' | column id text primary key | column provider text not null | column provider_status text not null default '' | column recipient text not null default '' | column reminder_id text not null references reminders(id) | column retry_state text not null default '' | column sent_at timestamptz | column status text not null
weixin_chat_messages: column binding_id text not null | column chat_session_id text not null | column content text not null | column context_token text not null default '' | column created_at timestamptz not null default now() | column direction text not null | column error text not null default '' | column external_message_id text not null default '' | column id text primary key | column linked_run_id text not null default '' | column role text not null | column status text not null | column updated_at timestamptz not null default now()
weixin_chat_sessions: column binding_id text not null | column channel text not null default 'weixin' | column created_at timestamptz not null default now() | column display_name text not null default '' | column external_user_id text not null default '' | column id text primary key | column last_context_token text not null default '' | column linked_session_id text not null default '' | column owner_id text not null default '' | column provider text not null default '' | column provider_cursor text not null default '' | column status text not null | column updated_at timestamptz not null default now() | column workspace_root text not null default ''
```

下表每一行都完整地**仅在 Go 侧存在**，因此该表中的所有列类型、default 和
nullability 定义也都只在 Go 侧存在。表格列出所有静态声明的 PK、FK、UNIQUE
和 CHECK 差异。`PK 列`表示 inline primary key；其他形式会显式写出。

| 仅 Go 侧表 | PK | FK | UNIQUE / CHECK |
|---|---|---|---|
| `iscp_onboardings` | `id` | 无 | 无 |
| `mcp_access_tickets` | `id` | 无 | inline UNIQUE `secret_hash` |
| `mcp_bindings` | `id` | 无 | active peer identity 上的部分唯一索引 |
| `mcp_operations` | `id` | `binding_id -> mcp_bindings(id)` | 表级 UNIQUE `(binding_id,idempotency_key)` |
| `document_records` | `id` | `session_id -> sessions(id)` | 无 |
| `reminder_deliveries` | `id` | `reminder_id -> reminders(id)` | 无 |
| `connector_settings` | 表级 PK `(owner_id,channel)` | 无 | 无 |
| `notification_bindings` | `id` | 无 | 无 |
| `weixin_chat_sessions` | `id` | 无 | 无 |
| `weixin_chat_messages` | `id` | 无 | 无 |
| `external_chat_sessions` | `id` | 无 | 无 |
| `external_chat_messages` | `id` | 无 | 无 |
| `message_receive_records` | `id` | 无 | 表级 UNIQUE `(source_endpoint_id,native_message_id)` |
| `message_delivery_records` | `id` | 无 | 表级 UNIQUE `(owner_id,actor_id,idempotency_key)` |
| `channel_inbox_updates` | `id` | 无 | 表级 UNIQUE `(binding_id,external_id)` |
| `passive_notifications` | `id` | 无 | 表级 UNIQUE `(endpoint_id,idempotency_key)` |
| `credential_secrets` | `ref` | 无 | 无 |
| `browser_auth_records` | `id` | 无 | 无 |
| `browser_login_blocks` | `id` | `session_id -> sessions(id)`；`run_id -> agent_runs(id)` | 无 |

两个来源中都**没有 CHECK 约束**、**没有命名约束**、**没有表级外键**。这些是
已解析且为空的类别，并非解析器遗漏。两个来源中也都没有通过 `ALTER TABLE`
增加或删除 PK、FK、UNIQUE、CHECK 或命名约束的语句。

## 仅 Go 侧索引

可执行清单冻结了全部 26 个定义，包括两个部分索引的唯一/未读谓词。

| 索引 | 解析后的定义 |
|---|---|
| `owners_external_ref_idx` | `ON owners(source,external_ref)` |
| `iscp_onboardings_owner_created_idx` | `ON iscp_onboardings(owner_id,created_at DESC)` |
| `mcp_access_tickets_owner_status_idx` | `ON mcp_access_tickets(owner_id,status,expires_at DESC)` |
| `mcp_bindings_active_peer_idx` | `UNIQUE ON mcp_bindings(domain_id,requester_device_id,requester_key_thumbprint) WHERE status = 'active'` |
| `mcp_bindings_owner_status_idx` | `ON mcp_bindings(owner_id,status,updated_at DESC)` |
| `mcp_operations_binding_updated_idx` | `ON mcp_operations(binding_id,updated_at DESC)` |
| `document_records_owner_session_activity_idx` | `ON document_records(owner_id,session_id,last_activity_at DESC)` |
| `document_records_session_path_idx` | `ON document_records(session_id,governed_path)` |
| `reminder_deliveries_reminder_id_idx` | `ON reminder_deliveries(reminder_id)` |
| `notification_bindings_channel_status_idx` | `ON notification_bindings(channel,status)` |
| `weixin_chat_sessions_binding_user_idx` | `ON weixin_chat_sessions(binding_id,external_user_id)` |
| `weixin_chat_sessions_linked_session_idx` | `ON weixin_chat_sessions(linked_session_id)` |
| `weixin_chat_messages_external_idx` | `ON weixin_chat_messages(chat_session_id,external_message_id)` |
| `weixin_chat_messages_chat_created_idx` | `ON weixin_chat_messages(chat_session_id,created_at)` |
| `external_chat_sessions_binding_chat_idx` | `ON external_chat_sessions(binding_id,external_chat_id,external_thread_id)` |
| `external_chat_sessions_linked_session_idx` | `ON external_chat_sessions(linked_session_id)` |
| `external_chat_messages_external_idx` | `ON external_chat_messages(chat_session_id,external_message_id)` |
| `external_chat_messages_chat_created_idx` | `ON external_chat_messages(chat_session_id,created_at)` |
| `message_receive_owner_actor_idx` | `ON message_receive_records(owner_id,actor_id,updated_at DESC)` |
| `message_delivery_owner_actor_idx` | `ON message_delivery_records(owner_id,actor_id,updated_at DESC)` |
| `channel_inbox_updates_ready_idx` | `ON channel_inbox_updates(channel,status,available_at,created_at)` |
| `passive_notifications_owner_created_idx` | `ON passive_notifications(owner_id,created_at DESC,id DESC)` |
| `passive_notifications_owner_unread_idx` | `ON passive_notifications(owner_id,created_at DESC) WHERE read_at IS NULL` |
| `browser_auth_lookup_idx` | `ON browser_auth_records(owner_id,browser_profile_id,site_origin,site_realm,account_hint,status,updated_at DESC)` |
| `browser_login_blocks_active_idx` | `ON browser_login_blocks(session_id,status,updated_at DESC)` |
| `idx_artifact_objects_uri` | `ON artifact_objects(uri)` |

## 未做语义解析

以下内容明确**未做语义解析**，不得理解为“没有差异”：

- `postgresSchema` 中两条 `INSERT ... SELECT` 兼容复制和三条 `UPDATE` 数据
  规范化；测试冻结其总数为五，但不证明数据效果；
- PostgreSQL 为未命名 inline/表级约束生成的名称；
- 任一已部署数据库的实际 schema 或数据；
- 已存在对象定义冲突时 `IF NOT EXISTS` 的接管行为；
- PostgreSQL 执行有效性、锁、权限、事务行为或 migration 时长。

S1 必须通过全新数据库、已接管数据库、重启、checksum、权限、回滚和配置
DSN 的证据覆盖这些事实。在此之前，PostgreSQL runtime reconciliation
保持**未运行**。

## 链接

- [S0 契约清单](store-s0-contract-inventory.md)
- [S0 基线与验收报告](store-s0-acceptance-report.md)
- [PostgreSQL schema 与 Store 配置设计](store-postgresql-schema-config-design.md)
- [Store 契约基础](store-contract-foundation-design.md)
