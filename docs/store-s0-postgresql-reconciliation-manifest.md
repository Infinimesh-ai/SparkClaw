# Store S0 PostgreSQL Reconciliation Manifest

> Language: English | [简体中文](../zh-cn/docs/store-s0-postgresql-reconciliation-manifest.md)

> Status: S0 implementation candidate, 2026-08-20. This is static source
> evidence only. It changes no schema and does not authorize S1. The S0
> implementation review remains pending human acceptance.

## Sources And Executable Authority

This manifest compares `migrations/0001_core.sql` at repository root with the
`postgresSchema` string in `services/gateway/internal/store/postgres.go`.
`TestS0PostgresReconciliationManifest` is the executable authority. It parses
and compares definitions, not only object counts.

The root migration contains 18 tables and 16 indexes. `postgresSchema`
contains the same objects plus 19 tables, 20 columns on six shared tables, and
26 indexes, for totals of 37 tables and 42 indexes. There is no root-only
table, column, or index.

## Parser Coverage And Evidence Labels

The executable parser covers:

- `CREATE TABLE` column type, inline `PRIMARY KEY`, `REFERENCES`, `UNIQUE`,
  `CHECK`, `NOT NULL`, and `DEFAULT` definitions;
- table-level `PRIMARY KEY`, `FOREIGN KEY`, `UNIQUE`, and `CHECK`, including an
  optional named `CONSTRAINT` prefix;
- `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` with its complete definition;
- `ALTER TABLE ... ALTER COLUMN ... DROP NOT NULL`;
- complete `CREATE [UNIQUE] INDEX IF NOT EXISTS` definitions, including table,
  column order, `ASC`/`DESC`, and partial-index predicates;
- every relevant `CREATE`, `ALTER`, or `DROP` statement: an unrecognized DDL
  statement fails the manifest instead of disappearing from the comparison.

Evidence labels in this document have strict meanings:

| Label | Meaning |
|---|---|
| **exact** | Both sources were parsed and their normalized definitions are equal |
| **Go-only** | The full parsed object or definition occurs only in `postgresSchema` |
| **none in either source** | The syntax category is parsed and its asserted set is empty |
| **not semantically covered** | The parser deliberately does not establish this fact; it is not a claim of no difference |

Normalization folds keyword/identifier case and insignificant whitespace. It
does not rewrite types, expressions, column order, sort order, predicates, or
constraint content.

## Shared Tables And Columns

For every shared column, the executable manifest compares the complete parsed
definition, including type, inline PK/FK/UNIQUE/CHECK, default, and
nullability. Every pre-existing shared column is **exact**. Every shared table's
table-level constraint set is also **exact**.

| Shared table | Result | Go-only columns |
|---|---|---|
| `owners` | Existing columns and constraints exact | `source`, `external_ref`, `workspace_root`, `default_channel`, `default_binding_id` |
| `clients` | Existing columns and constraints exact | `owner_id`, `actor_id` |
| `pairing_codes` | All columns and constraints exact | none |
| `sessions` | Existing columns and constraints exact | `owner_id`, `workspace_root`, `source`, `hidden` |
| `messages` | Existing columns and constraints exact | `attachments`, `requested_media` |
| `agent_runs` | All columns and constraints exact | none |
| `run_feedback` | All columns and constraints exact | none |
| `model_calls` | All columns and constraints exact | none |
| `tool_calls` | Existing columns and constraints exact | `workflow_id`, `workflow_node_id`, `scope_revision`, `capability`, `error_code`, `policy_context` |
| `approvals` | Existing columns and constraints exact | `policy_context` |
| `audit_events` | All columns and constraints exact | none |
| `events` | All columns and constraints exact | none |
| `memories` | All columns and constraints exact | none |
| `memory_candidates` | All columns and constraints exact | none |
| `eval_runs` | All columns and constraints exact | none |
| `artifact_objects` | All columns and constraints exact | none |
| `episode_summaries` | All columns and constraints exact | none |
| `reminders` | All columns and constraints exact after `ADD COLUMN` reconciliation | none |

The 20 Go-only column definitions, including defaults and nullability, are:

| Table | Column | Complete definition |
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
| `tool_calls` | `error_code` | `TEXT` (nullable, no default) |
| `tool_calls` | `policy_context` | `JSONB` (nullable, no default) |
| `approvals` | `policy_context` | `JSONB` (nullable, no default) |

The three approval `DROP NOT NULL` statements are parsed and applied in both
sources. Their final shared definitions are therefore **exact**, rather than
being ignored as ALTER syntax.

## Shared Indexes

All 16 shared indexes have **exact** complete definitions in both sources:

| Index | Parsed definition |
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

## Go-Only Tables And Constraints

Each row below is wholly **Go-only**, so every column type, default, and
nullability definition in that table is also Go-only. The table lists all
statically declared PK, FK, UNIQUE, and CHECK differences. `PK column` means an
inline primary key; other forms are written explicitly.

| Go-only table | PK | FK | UNIQUE / CHECK |
|---|---|---|---|
| `iscp_onboardings` | `id` | none | none |
| `mcp_access_tickets` | `id` | none | inline UNIQUE `secret_hash` |
| `mcp_bindings` | `id` | none | partial unique index on active peer identity |
| `mcp_operations` | `id` | `binding_id -> mcp_bindings(id)` | table UNIQUE `(binding_id,idempotency_key)` |
| `document_records` | `id` | `session_id -> sessions(id)` | none |
| `reminder_deliveries` | `id` | `reminder_id -> reminders(id)` | none |
| `connector_settings` | table PK `(owner_id,channel)` | none | none |
| `notification_bindings` | `id` | none | none |
| `weixin_chat_sessions` | `id` | none | none |
| `weixin_chat_messages` | `id` | none | none |
| `external_chat_sessions` | `id` | none | none |
| `external_chat_messages` | `id` | none | none |
| `message_receive_records` | `id` | none | table UNIQUE `(source_endpoint_id,native_message_id)` |
| `message_delivery_records` | `id` | none | table UNIQUE `(owner_id,actor_id,idempotency_key)` |
| `channel_inbox_updates` | `id` | none | table UNIQUE `(binding_id,external_id)` |
| `passive_notifications` | `id` | none | table UNIQUE `(endpoint_id,idempotency_key)` |
| `credential_secrets` | `ref` | none | none |
| `browser_auth_records` | `id` | none | none |
| `browser_login_blocks` | `id` | `session_id -> sessions(id)`; `run_id -> agent_runs(id)` | none |

There are **no CHECK constraints in either source**, **no named constraints in
either source**, and **no table-level foreign keys in either source**. Those
are parsed empty categories, not parser omissions. There is also no
`ALTER TABLE` statement adding or dropping a PK, FK, UNIQUE, CHECK, or named
constraint in either source.

## Go-Only Indexes

All 26 definitions are frozen in the executable manifest, including the two
partial unique/unread predicates.

| Index | Parsed definition |
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

## Not Semantically Covered

The following are explicitly **not semantically covered** and must not be read
as “no difference”:

- the two `INSERT ... SELECT` compatibility copies and three `UPDATE` data
  normalizations in `postgresSchema`; the test freezes their count as five but
  does not prove their data effects;
- PostgreSQL-generated names for unnamed inline/table constraints;
- the actual schema or data in any deployed database;
- the adoption behavior of `IF NOT EXISTS` when an existing object has a
  conflicting definition;
- PostgreSQL execution validity, locking, privileges, transactional behavior,
  or migration duration.

S1 must cover those facts with fresh-database, adopted-database, restart,
checksum, privilege, rollback, and configured DSN evidence. Until then,
PostgreSQL runtime reconciliation remains **not run**.

## Links

- [S0 contract inventory](store-s0-contract-inventory.md)
- [S0 baseline and acceptance report](store-s0-acceptance-report.md)
- [PostgreSQL schema and Store configuration design](store-postgresql-schema-config-design.md)
- [Store contract foundation](store-contract-foundation-design.md)
