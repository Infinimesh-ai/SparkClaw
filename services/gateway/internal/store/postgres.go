package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	db *pgxpool.Pool
	// passiveNotificationRevs mirrors the memory backend's per-owner change
	// counter for SSE pollers. Process-local by design: the gateway is the
	// only writer of passive notifications, and callers only compare values
	// for equality, so a restart resetting it is harmless.
	passiveRevMu            sync.Mutex
	passiveNotificationRevs map[string]uint64
}

const postgresSchema = `
CREATE TABLE IF NOT EXISTS owners (
  id TEXT PRIMARY KEY,
  source TEXT NOT NULL DEFAULT '',
  external_ref TEXT NOT NULL DEFAULT '',
  workspace_root TEXT NOT NULL DEFAULT '',
  default_channel TEXT NOT NULL DEFAULT '',
  default_binding_id TEXT NOT NULL DEFAULT '',
  display_name TEXT NOT NULL,
  email TEXT NOT NULL DEFAULT '',
  preferences JSONB NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE owners ADD COLUMN IF NOT EXISTS email TEXT NOT NULL DEFAULT '';
ALTER TABLE owners ADD COLUMN IF NOT EXISTS preferences JSONB NOT NULL DEFAULT '{}';
ALTER TABLE owners ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();
ALTER TABLE owners ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT '';
ALTER TABLE owners ADD COLUMN IF NOT EXISTS external_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE owners ADD COLUMN IF NOT EXISTS workspace_root TEXT NOT NULL DEFAULT '';
ALTER TABLE owners ADD COLUMN IF NOT EXISTS default_channel TEXT NOT NULL DEFAULT '';
ALTER TABLE owners ADD COLUMN IF NOT EXISTS default_binding_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS owners_external_ref_idx ON owners(source, external_ref);

CREATE TABLE IF NOT EXISTS clients (
  id TEXT PRIMARY KEY,
	owner_id TEXT NOT NULL DEFAULT 'owner',
	actor_id TEXT NOT NULL DEFAULT 'owner',
  name TEXT NOT NULL,
  token_hash TEXT NOT NULL UNIQUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at TIMESTAMPTZ,
  revoked_at TIMESTAMPTZ
);
ALTER TABLE clients ADD COLUMN IF NOT EXISTS owner_id TEXT NOT NULL DEFAULT 'owner';
ALTER TABLE clients ADD COLUMN IF NOT EXISTS actor_id TEXT NOT NULL DEFAULT 'owner';

	CREATE TABLE IF NOT EXISTS pairing_codes (
  id TEXT PRIMARY KEY,
  code_hash TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  claimed_at TIMESTAMPTZ,
  client_id TEXT REFERENCES clients(id)
	);

	CREATE TABLE IF NOT EXISTS iscp_onboardings (
	  id TEXT PRIMARY KEY,
	  owner_id TEXT NOT NULL,
	  domain_id TEXT NOT NULL,
	  authority_ref TEXT NOT NULL,
	  ticket_id TEXT NOT NULL,
	  status TEXT NOT NULL,
	  created_at TIMESTAMPTZ NOT NULL,
	  payload JSONB NOT NULL
	);
	CREATE INDEX IF NOT EXISTS iscp_onboardings_owner_created_idx ON iscp_onboardings(owner_id, created_at DESC);

	CREATE TABLE IF NOT EXISTS mcp_access_tickets (
  id TEXT PRIMARY KEY,
  secret_hash TEXT NOT NULL UNIQUE,
  owner_id TEXT NOT NULL,
  domain_id TEXT NOT NULL,
  status TEXT NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  payload JSONB NOT NULL
);
CREATE INDEX IF NOT EXISTS mcp_access_tickets_owner_status_idx ON mcp_access_tickets(owner_id, status, expires_at DESC);

CREATE TABLE IF NOT EXISTS mcp_bindings (
  id TEXT PRIMARY KEY,
  owner_id TEXT NOT NULL,
  domain_id TEXT NOT NULL,
  requester_device_id TEXT NOT NULL,
  requester_key_thumbprint TEXT NOT NULL,
  status TEXT NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  payload JSONB NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS mcp_bindings_active_peer_idx
  ON mcp_bindings(domain_id, requester_device_id, requester_key_thumbprint) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS mcp_bindings_owner_status_idx ON mcp_bindings(owner_id, status, updated_at DESC);

CREATE TABLE IF NOT EXISTS mcp_operations (
  id TEXT PRIMARY KEY,
  binding_id TEXT NOT NULL REFERENCES mcp_bindings(id),
  idempotency_key TEXT NOT NULL,
  fingerprint TEXT NOT NULL,
  version BIGINT NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  payload JSONB NOT NULL,
  UNIQUE(binding_id, idempotency_key)
);
CREATE INDEX IF NOT EXISTS mcp_operations_binding_updated_idx ON mcp_operations(binding_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY,
  owner_id TEXT NOT NULL DEFAULT 'owner',
  workspace_root TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL,
  source TEXT NOT NULL DEFAULT 'webchat',
  hidden BOOLEAN NOT NULL DEFAULT false,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS messages (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES sessions(id),
  run_id TEXT,
  role TEXT NOT NULL,
  content TEXT NOT NULL,
  attachments JSONB NOT NULL DEFAULT '[]',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS agent_runs (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES sessions(id),
  state TEXT NOT NULL,
  model_lane TEXT NOT NULL,
  risk_level TEXT NOT NULL,
  summary TEXT,
	workflow_state JSONB,
	message_context JSONB,
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at TIMESTAMPTZ
);

ALTER TABLE agent_runs ADD COLUMN IF NOT EXISTS workflow_state JSONB;
ALTER TABLE agent_runs ADD COLUMN IF NOT EXISTS message_context JSONB;

CREATE TABLE IF NOT EXISTS run_feedback (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES sessions(id),
  run_id TEXT NOT NULL REFERENCES agent_runs(id),
  message_id TEXT,
  rating TEXT NOT NULL,
  note TEXT NOT NULL DEFAULT '',
  correction TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS model_calls (
  id TEXT PRIMARY KEY,
  session_id TEXT REFERENCES sessions(id),
  run_id TEXT REFERENCES agent_runs(id),
  lane TEXT NOT NULL,
  profile TEXT NOT NULL,
  model TEXT NOT NULL,
  operation TEXT NOT NULL,
  mock BOOLEAN NOT NULL DEFAULT false,
  fallback BOOLEAN NOT NULL DEFAULT false,
  status TEXT NOT NULL,
  prompt_tokens INTEGER NOT NULL DEFAULT 0,
  response_tokens INTEGER NOT NULL DEFAULT 0,
  total_tokens INTEGER NOT NULL DEFAULT 0,
  latency_ms BIGINT NOT NULL DEFAULT 0,
  error TEXT,
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS tool_calls (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES sessions(id),
  run_id TEXT NOT NULL REFERENCES agent_runs(id),
  workflow_id TEXT NOT NULL DEFAULT '',
  workflow_node_id TEXT NOT NULL DEFAULT '',
  scope_revision INTEGER NOT NULL DEFAULT 0,
  capability TEXT NOT NULL DEFAULT '',
  tool TEXT NOT NULL,
  risk_level TEXT NOT NULL,
  status TEXT NOT NULL,
  arguments JSONB NOT NULL,
	  result JSONB,
	  error TEXT,
	  error_code TEXT,
	  approval_id TEXT,
	  observation_ref TEXT,
	  observation_summary TEXT NOT NULL DEFAULT '',
	  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	  completed_at TIMESTAMPTZ
	);
	ALTER TABLE tool_calls ADD COLUMN IF NOT EXISTS observation_ref TEXT;
	ALTER TABLE tool_calls ADD COLUMN IF NOT EXISTS observation_summary TEXT NOT NULL DEFAULT '';
	ALTER TABLE tool_calls ADD COLUMN IF NOT EXISTS workflow_id TEXT NOT NULL DEFAULT '';
	ALTER TABLE tool_calls ADD COLUMN IF NOT EXISTS workflow_node_id TEXT NOT NULL DEFAULT '';
	ALTER TABLE tool_calls ADD COLUMN IF NOT EXISTS scope_revision INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE tool_calls ADD COLUMN IF NOT EXISTS capability TEXT NOT NULL DEFAULT '';
	ALTER TABLE tool_calls ADD COLUMN IF NOT EXISTS error_code TEXT;

	CREATE TABLE IF NOT EXISTS document_records (
	  id TEXT PRIMARY KEY,
	  owner_id TEXT NOT NULL DEFAULT 'owner',
	  session_id TEXT NOT NULL REFERENCES sessions(id),
	  governed_path TEXT NOT NULL,
	  name TEXT NOT NULL,
	  content_type TEXT NOT NULL DEFAULT '',
	  format TEXT NOT NULL DEFAULT '',
	  size_bytes BIGINT NOT NULL DEFAULT 0,
	  sha256 TEXT NOT NULL DEFAULT '',
	  status TEXT NOT NULL,
	  source TEXT NOT NULL,
	  source_message_id TEXT NOT NULL DEFAULT '',
	  source_run_id TEXT NOT NULL DEFAULT '',
	  source_tool_call_id TEXT NOT NULL DEFAULT '',
	  parent_document_id TEXT NOT NULL DEFAULT '',
	  last_activity TEXT NOT NULL,
	  last_activity_id TEXT NOT NULL,
	  last_activity_at TIMESTAMPTZ NOT NULL,
	  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
	);
	CREATE INDEX IF NOT EXISTS document_records_owner_session_activity_idx
	  ON document_records(owner_id, session_id, last_activity_at DESC);
	CREATE INDEX IF NOT EXISTS document_records_session_path_idx
	  ON document_records(session_id, governed_path);

	CREATE TABLE IF NOT EXISTS approvals (
  id TEXT PRIMARY KEY,
  source TEXT NOT NULL DEFAULT 'tool',
  external_id TEXT NOT NULL DEFAULT '',
  external_context JSONB,
  session_id TEXT REFERENCES sessions(id),
  run_id TEXT REFERENCES agent_runs(id),
  tool_call_id TEXT REFERENCES tool_calls(id),
  tool TEXT NOT NULL,
  risk_level TEXT NOT NULL,
  status TEXT NOT NULL,
  summary TEXT NOT NULL,
  reason TEXT NOT NULL,
  resources JSONB NOT NULL DEFAULT '[]',
  arguments JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  resolved_at TIMESTAMPTZ,
  resolution_note TEXT
);
ALTER TABLE approvals ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'tool';
ALTER TABLE approvals ADD COLUMN IF NOT EXISTS external_id TEXT NOT NULL DEFAULT '';
ALTER TABLE approvals ADD COLUMN IF NOT EXISTS external_context JSONB;
ALTER TABLE approvals ALTER COLUMN session_id DROP NOT NULL;
ALTER TABLE approvals ALTER COLUMN run_id DROP NOT NULL;
ALTER TABLE approvals ALTER COLUMN tool_call_id DROP NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS approvals_external_ref_idx
  ON approvals(source, external_id) WHERE external_id <> '';

CREATE TABLE IF NOT EXISTS reminders (
  id TEXT PRIMARY KEY,
  session_id TEXT,
  run_id TEXT,
  text TEXT NOT NULL,
  text_summary TEXT NOT NULL,
  due_time TIMESTAMPTZ NOT NULL,
  timezone TEXT NOT NULL,
  channel TEXT NOT NULL,
  recipient TEXT NOT NULL DEFAULT '',
  recurrence TEXT NOT NULL DEFAULT '',
  dedupe_key TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  last_delivery_id TEXT NOT NULL DEFAULT '',
  last_error TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  sent_at TIMESTAMPTZ,
  canceled_at TIMESTAMPTZ,
  delivery_attempt INTEGER NOT NULL DEFAULT 0
);
ALTER TABLE reminders ADD COLUMN IF NOT EXISTS recipient_binding TEXT NOT NULL DEFAULT '';
ALTER TABLE reminders ADD COLUMN IF NOT EXISTS binding_id TEXT NOT NULL DEFAULT '';
ALTER TABLE reminders ADD COLUMN IF NOT EXISTS credential_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE reminders ADD COLUMN IF NOT EXISTS base_url TEXT NOT NULL DEFAULT '';
ALTER TABLE reminders ADD COLUMN IF NOT EXISTS schedule_spec JSONB;

CREATE INDEX IF NOT EXISTS reminders_status_due_time_idx ON reminders (status, due_time);

CREATE TABLE IF NOT EXISTS reminder_deliveries (
  id TEXT PRIMARY KEY,
  reminder_id TEXT NOT NULL REFERENCES reminders(id),
  channel TEXT NOT NULL,
  provider TEXT NOT NULL,
  recipient TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  provider_status TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',
  retry_state TEXT NOT NULL DEFAULT '',
  attempt INTEGER NOT NULL DEFAULT 0,
  sent_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS reminder_deliveries_reminder_id_idx ON reminder_deliveries (reminder_id);

CREATE TABLE IF NOT EXISTS connector_settings (
  owner_id TEXT NOT NULL,
  channel TEXT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT false,
  iscp_enabled BOOLEAN NOT NULL DEFAULT false,
  lan_access_enabled BOOLEAN NOT NULL DEFAULT false,
  version BIGINT NOT NULL DEFAULT 1,
  updated_by TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (owner_id, channel)
);

ALTER TABLE connector_settings ADD COLUMN IF NOT EXISTS iscp_enabled BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE connector_settings ADD COLUMN IF NOT EXISTS lan_access_enabled BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE IF NOT EXISTS notification_bindings (
  id TEXT PRIMARY KEY,
  owner_id TEXT NOT NULL,
	actor_id TEXT NOT NULL DEFAULT '',
  channel TEXT NOT NULL,
  provider TEXT NOT NULL,
  status TEXT NOT NULL,
  display_name TEXT NOT NULL DEFAULT '',
  external_user_id TEXT NOT NULL DEFAULT '',
  external_chat_id TEXT NOT NULL DEFAULT '',
  external_thread_id TEXT NOT NULL DEFAULT '',
  account_id TEXT NOT NULL DEFAULT '',
  credential_ref TEXT NOT NULL DEFAULT '',
  base_url TEXT NOT NULL DEFAULT '',
  provider_session_id TEXT NOT NULL DEFAULT '',
  provider_state TEXT NOT NULL DEFAULT '',
  context_token TEXT NOT NULL DEFAULT '',
  provider_cursor TEXT NOT NULL DEFAULT '',
  qr_code_url TEXT NOT NULL DEFAULT '',
  qr_code_image TEXT NOT NULL DEFAULT '',
  default_for_channel BOOLEAN NOT NULL DEFAULT false,
  scopes JSONB NOT NULL DEFAULT '[]',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ,
  revoked_at TIMESTAMPTZ,
  last_error TEXT NOT NULL DEFAULT ''
);

ALTER TABLE notification_bindings ADD COLUMN IF NOT EXISTS actor_id TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS notification_bindings_channel_status_idx ON notification_bindings (channel, status);

ALTER TABLE notification_bindings ADD COLUMN IF NOT EXISTS provider_session_id TEXT NOT NULL DEFAULT '';
ALTER TABLE notification_bindings ADD COLUMN IF NOT EXISTS provider_state TEXT NOT NULL DEFAULT '';
ALTER TABLE notification_bindings ADD COLUMN IF NOT EXISTS context_token TEXT NOT NULL DEFAULT '';
ALTER TABLE notification_bindings ADD COLUMN IF NOT EXISTS provider_cursor TEXT NOT NULL DEFAULT '';
ALTER TABLE notification_bindings ADD COLUMN IF NOT EXISTS external_chat_id TEXT NOT NULL DEFAULT '';
ALTER TABLE notification_bindings ADD COLUMN IF NOT EXISTS external_thread_id TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'webchat';
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS hidden BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS owner_id TEXT NOT NULL DEFAULT 'owner';
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS workspace_root TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS weixin_chat_sessions (
  id TEXT PRIMARY KEY,
  owner_id TEXT NOT NULL DEFAULT '',
  workspace_root TEXT NOT NULL DEFAULT '',
  binding_id TEXT NOT NULL,
  channel TEXT NOT NULL DEFAULT 'weixin',
  provider TEXT NOT NULL DEFAULT '',
  external_user_id TEXT NOT NULL DEFAULT '',
  display_name TEXT NOT NULL DEFAULT '',
  linked_session_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  provider_cursor TEXT NOT NULL DEFAULT '',
  last_context_token TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS weixin_chat_sessions_binding_user_idx ON weixin_chat_sessions(binding_id, external_user_id);
CREATE INDEX IF NOT EXISTS weixin_chat_sessions_linked_session_idx ON weixin_chat_sessions(linked_session_id);

CREATE TABLE IF NOT EXISTS weixin_chat_messages (
  id TEXT PRIMARY KEY,
  chat_session_id TEXT NOT NULL,
  binding_id TEXT NOT NULL,
  direction TEXT NOT NULL,
  role TEXT NOT NULL,
  external_message_id TEXT NOT NULL DEFAULT '',
  content TEXT NOT NULL,
  context_token TEXT NOT NULL DEFAULT '',
  linked_run_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  error TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS weixin_chat_messages_external_idx ON weixin_chat_messages(chat_session_id, external_message_id);
CREATE INDEX IF NOT EXISTS weixin_chat_messages_chat_created_idx ON weixin_chat_messages(chat_session_id, created_at);

CREATE TABLE IF NOT EXISTS external_chat_sessions (
  id TEXT PRIMARY KEY,
  owner_id TEXT NOT NULL DEFAULT '',
	authorized_owner_id TEXT NOT NULL DEFAULT '',
	authorized_actor_id TEXT NOT NULL DEFAULT '',
  workspace_root TEXT NOT NULL DEFAULT '',
  binding_id TEXT NOT NULL,
  channel TEXT NOT NULL,
  provider TEXT NOT NULL DEFAULT '',
  external_user_id TEXT NOT NULL DEFAULT '',
  external_chat_id TEXT NOT NULL DEFAULT '',
  external_thread_id TEXT NOT NULL DEFAULT '',
  display_name TEXT NOT NULL DEFAULT '',
  linked_session_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  provider_cursor TEXT NOT NULL DEFAULT '',
  last_context_token TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE external_chat_sessions ADD COLUMN IF NOT EXISTS authorized_owner_id TEXT NOT NULL DEFAULT '';
ALTER TABLE external_chat_sessions ADD COLUMN IF NOT EXISTS authorized_actor_id TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS external_chat_sessions_binding_chat_idx
  ON external_chat_sessions(binding_id, external_chat_id, external_thread_id);
CREATE INDEX IF NOT EXISTS external_chat_sessions_linked_session_idx
  ON external_chat_sessions(linked_session_id);

CREATE TABLE IF NOT EXISTS external_chat_messages (
  id TEXT PRIMARY KEY,
  chat_session_id TEXT NOT NULL,
  binding_id TEXT NOT NULL,
  channel TEXT NOT NULL,
  direction TEXT NOT NULL,
  role TEXT NOT NULL,
  external_message_id TEXT NOT NULL DEFAULT '',
  content TEXT NOT NULL,
  context_token TEXT NOT NULL DEFAULT '',
  linked_run_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  error TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE external_chat_messages ADD COLUMN IF NOT EXISTS pending_reply_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE external_chat_messages ADD COLUMN IF NOT EXISTS pending_reply TEXT NOT NULL DEFAULT '';
ALTER TABLE external_chat_messages ADD COLUMN IF NOT EXISTS dispatch_attempts INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS external_chat_messages_external_idx
  ON external_chat_messages(chat_session_id, external_message_id);
CREATE INDEX IF NOT EXISTS external_chat_messages_chat_created_idx
  ON external_chat_messages(chat_session_id, created_at);

CREATE TABLE IF NOT EXISTS message_receive_records (
	id TEXT PRIMARY KEY,
	owner_id TEXT NOT NULL,
	actor_id TEXT NOT NULL,
	source_endpoint_id TEXT NOT NULL DEFAULT '',
	native_message_id TEXT NOT NULL,
	status TEXT NOT NULL,
	record JSONB NOT NULL,
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE(source_endpoint_id, native_message_id)
);

CREATE INDEX IF NOT EXISTS message_receive_owner_actor_idx
	ON message_receive_records(owner_id, actor_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS message_delivery_records (
	id TEXT PRIMARY KEY,
	owner_id TEXT NOT NULL,
	actor_id TEXT NOT NULL,
	idempotency_key TEXT NOT NULL,
	content_digest TEXT NOT NULL,
	status TEXT NOT NULL,
	record JSONB NOT NULL,
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE(owner_id, actor_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS message_delivery_owner_actor_idx
	ON message_delivery_records(owner_id, actor_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS channel_inbox_updates (
  id TEXT PRIMARY KEY,
  binding_id TEXT NOT NULL,
  channel TEXT NOT NULL,
  external_id TEXT NOT NULL,
  chat_key TEXT NOT NULL DEFAULT '',
  payload JSONB NOT NULL DEFAULT '{}',
  status TEXT NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0,
  available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_error TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(binding_id, external_id)
);

CREATE INDEX IF NOT EXISTS channel_inbox_updates_ready_idx
  ON channel_inbox_updates(channel, status, available_at, created_at);

CREATE TABLE IF NOT EXISTS passive_notifications (
  id TEXT PRIMARY KEY,
  owner_id TEXT NOT NULL,
  endpoint_id TEXT NOT NULL,
  idempotency_key TEXT NOT NULL,
  fingerprint TEXT NOT NULL,
  notification_id TEXT NOT NULL,
  source TEXT NOT NULL,
  kind TEXT NOT NULL,
  deep_link TEXT NOT NULL,
  occurred_at TIMESTAMPTZ NOT NULL,
  read_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(endpoint_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS passive_notifications_owner_created_idx
  ON passive_notifications(owner_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS passive_notifications_owner_unread_idx
  ON passive_notifications(owner_id, created_at DESC) WHERE read_at IS NULL;

INSERT INTO external_chat_sessions (
  id, owner_id, workspace_root, binding_id, channel, provider, external_user_id,
  external_chat_id, external_thread_id, display_name, linked_session_id, status,
  provider_cursor, last_context_token, created_at, updated_at
)
SELECT id, owner_id, workspace_root, binding_id, channel, provider, external_user_id,
       external_user_id, '', display_name, linked_session_id, status,
       provider_cursor, last_context_token, created_at, updated_at
FROM weixin_chat_sessions
ON CONFLICT (id) DO NOTHING;

INSERT INTO external_chat_messages (
  id, chat_session_id, binding_id, channel, direction, role, external_message_id,
  content, context_token, linked_run_id, status, error, created_at, updated_at
)
SELECT id, chat_session_id, binding_id, 'weixin', direction, role, external_message_id,
       content, context_token, linked_run_id, status, error, created_at, updated_at
FROM weixin_chat_messages
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS credential_secrets (
  ref TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  value TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS browser_auth_records (
  id TEXT PRIMARY KEY,
  owner_id TEXT NOT NULL,
  browser_profile_id TEXT NOT NULL,
  site_origin TEXT NOT NULL,
  site_realm TEXT NOT NULL DEFAULT '',
  account_hint TEXT NOT NULL DEFAULT '',
  auth_strategy TEXT NOT NULL,
  status TEXT NOT NULL,
  session_ref TEXT NOT NULL DEFAULT '',
  credential_ref TEXT NOT NULL DEFAULT '',
  cookie_jar_ref TEXT NOT NULL DEFAULT '',
  last_verified_at TIMESTAMPTZ,
  expires_at TIMESTAMPTZ,
  last_error TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  revoked_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS browser_auth_lookup_idx
  ON browser_auth_records(owner_id, browser_profile_id, site_origin, site_realm, account_hint, status, updated_at DESC);

CREATE TABLE IF NOT EXISTS browser_login_blocks (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES sessions(id),
  run_id TEXT NOT NULL REFERENCES agent_runs(id),
  schema_version INTEGER NOT NULL DEFAULT 2,
  version BIGINT NOT NULL DEFAULT 1,
  workflow_id TEXT NOT NULL DEFAULT '',
  workflow_revision INTEGER NOT NULL DEFAULT 0,
  workflow_node_id TEXT NOT NULL DEFAULT '',
  session_generation BIGINT NOT NULL DEFAULT 0,
  status TEXT NOT NULL,
  original_goal TEXT NOT NULL DEFAULT '',
  resume_tool TEXT NOT NULL DEFAULT 'browser.read',
  resume_args JSONB NOT NULL DEFAULT '{}',
  last_tool_call_id TEXT NOT NULL DEFAULT '',
  login_handoff_url TEXT NOT NULL DEFAULT '',
  login_handoff_page_id TEXT NOT NULL DEFAULT '',
  last_visible_page_id TEXT NOT NULL DEFAULT '',
  owner_id TEXT NOT NULL DEFAULT 'owner',
  browser_profile_id TEXT NOT NULL DEFAULT 'default',
  site_origin TEXT NOT NULL DEFAULT '',
  site_realm TEXT NOT NULL DEFAULT '',
  account_hint TEXT NOT NULL DEFAULT '',
  browser_auth_status TEXT NOT NULL DEFAULT '',
  target JSONB NOT NULL DEFAULT '{}',
  visible_evidence JSONB NOT NULL DEFAULT 'null',
  last_user_reply TEXT NOT NULL DEFAULT '',
  last_error TEXT NOT NULL DEFAULT '',
  transition_owner_id TEXT NOT NULL DEFAULT '',
  transition_lease_until TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  resolved_at TIMESTAMPTZ
);

ALTER TABLE browser_login_blocks
  ADD COLUMN IF NOT EXISTS login_handoff_page_id TEXT NOT NULL DEFAULT '';
ALTER TABLE browser_login_blocks
  ADD COLUMN IF NOT EXISTS last_visible_page_id TEXT NOT NULL DEFAULT '';
ALTER TABLE browser_login_blocks
  ADD COLUMN IF NOT EXISTS schema_version INTEGER NOT NULL DEFAULT 2;
ALTER TABLE browser_login_blocks
  ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 1;
ALTER TABLE browser_login_blocks
  ADD COLUMN IF NOT EXISTS workflow_id TEXT NOT NULL DEFAULT '';
ALTER TABLE browser_login_blocks
  ADD COLUMN IF NOT EXISTS workflow_revision INTEGER NOT NULL DEFAULT 0;
ALTER TABLE browser_login_blocks
  ADD COLUMN IF NOT EXISTS workflow_node_id TEXT NOT NULL DEFAULT '';
ALTER TABLE browser_login_blocks
  ADD COLUMN IF NOT EXISTS session_generation BIGINT NOT NULL DEFAULT 0;
ALTER TABLE browser_login_blocks
  ADD COLUMN IF NOT EXISTS target JSONB NOT NULL DEFAULT '{}';
ALTER TABLE browser_login_blocks
  ADD COLUMN IF NOT EXISTS visible_evidence JSONB NOT NULL DEFAULT 'null';
ALTER TABLE browser_login_blocks
  ADD COLUMN IF NOT EXISTS transition_owner_id TEXT NOT NULL DEFAULT '';
ALTER TABLE browser_login_blocks
  ADD COLUMN IF NOT EXISTS transition_lease_until TIMESTAMPTZ;

UPDATE browser_login_blocks SET status = 'waiting_owner', schema_version = 2
  WHERE status = 'waiting';
UPDATE browser_login_blocks SET status = 'validating_visible', schema_version = 2
  WHERE status = 'resuming';
UPDATE browser_login_blocks SET version = 1 WHERE version <= 0;

CREATE INDEX IF NOT EXISTS browser_login_blocks_active_idx
  ON browser_login_blocks(session_id, status, updated_at DESC);

CREATE TABLE IF NOT EXISTS audit_events (
  id TEXT PRIMARY KEY,
  happened_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  type TEXT NOT NULL,
  session_id TEXT,
  run_id TEXT,
  actor TEXT NOT NULL,
  summary TEXT NOT NULL,
  fields JSONB
);

CREATE TABLE IF NOT EXISTS events (
  seq BIGSERIAL PRIMARY KEY,
  id TEXT UNIQUE NOT NULL,
  happened_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  type TEXT NOT NULL,
  session_id TEXT,
  run_id TEXT,
  payload JSONB NOT NULL
);

CREATE TABLE IF NOT EXISTS memories (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  content TEXT NOT NULL,
  source_run_id TEXT NOT NULL REFERENCES agent_runs(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS memories_created_at_idx ON memories (created_at);

CREATE TABLE IF NOT EXISTS memory_candidates (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES sessions(id),
  run_id TEXT NOT NULL REFERENCES agent_runs(id),
  kind TEXT NOT NULL,
  content TEXT NOT NULL,
  sensitivity TEXT NOT NULL,
  status TEXT NOT NULL,
  reason TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  resolved_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS eval_runs (
  id TEXT PRIMARY KEY,
  profile TEXT NOT NULL,
  status TEXT NOT NULL,
  summary TEXT NOT NULL,
  cases JSONB NOT NULL DEFAULT '[]',
  failure_archives JSONB NOT NULL DEFAULT '[]',
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at TIMESTAMPTZ
);

ALTER TABLE eval_runs ADD COLUMN IF NOT EXISTS failure_archives JSONB NOT NULL DEFAULT '[]';
ALTER TABLE messages ADD COLUMN IF NOT EXISTS attachments JSONB NOT NULL DEFAULT '[]';

CREATE TABLE IF NOT EXISTS artifact_objects (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  run_id TEXT,
  eval_id TEXT,
  session_id TEXT,
  backend TEXT NOT NULL,
  bucket TEXT,
  object_key TEXT NOT NULL,
  uri TEXT NOT NULL,
  path TEXT,
  content_type TEXT NOT NULL,
  bytes INTEGER NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS episode_summaries (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES sessions(id),
  run_id TEXT NOT NULL REFERENCES agent_runs(id),
  goal TEXT NOT NULL,
  outcome TEXT NOT NULL,
  risk_level TEXT NOT NULL,
  model_lane TEXT NOT NULL,
  tools JSONB NOT NULL DEFAULT '[]',
  approvals JSONB NOT NULL DEFAULT '[]',
  failures JSONB NOT NULL DEFAULT '[]',
  repair_performed BOOLEAN NOT NULL DEFAULT false,
  summary TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_messages_session_created ON messages(session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_run_feedback_run_updated ON run_feedback(run_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_clients_token_hash ON clients(token_hash);
CREATE INDEX IF NOT EXISTS idx_pairing_codes_status_expires ON pairing_codes(status, expires_at);
CREATE INDEX IF NOT EXISTS idx_model_calls_session_run_started ON model_calls(session_id, run_id, started_at);
CREATE INDEX IF NOT EXISTS idx_tool_calls_run ON tool_calls(run_id);
CREATE INDEX IF NOT EXISTS idx_approvals_status ON approvals(status);
CREATE INDEX IF NOT EXISTS idx_audit_session_time ON audit_events(session_id, happened_at DESC);
CREATE INDEX IF NOT EXISTS idx_events_session_seq ON events(session_id, seq);
CREATE INDEX IF NOT EXISTS idx_eval_runs_started ON eval_runs(started_at DESC);
CREATE INDEX IF NOT EXISTS idx_artifact_objects_created ON artifact_objects(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_artifact_objects_run ON artifact_objects(run_id);
CREATE INDEX IF NOT EXISTS idx_artifact_objects_uri ON artifact_objects(uri);
CREATE INDEX IF NOT EXISTS idx_episode_summaries_session_created ON episode_summaries(session_id, created_at DESC);
`

func NewPostgresStore(ctx context.Context, dsn string) (*PostgresStore, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("postgres state backend requires SPARKCLAW_STATE_DSN or SPARKCLAW_POSTGRES_DSN")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	st := &PostgresStore{db: pool, passiveNotificationRevs: map[string]uint64{}}
	if err := st.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return st, nil
}

func (s *PostgresStore) Close() {
	s.db.Close()
}

func (s *PostgresStore) migrate(ctx context.Context) error {
	if _, err := s.db.Exec(ctx, postgresSchema); err != nil {
		return fmt.Errorf("migrate postgres store: %w", err)
	}
	return nil
}

func (s *PostgresStore) CreateSession(title string) app.Session {
	return s.CreateSessionWithScope(title, app.DefaultOwnerID, "", "webchat", false)
}

func (s *PostgresStore) CreateSessionWithScope(title, ownerID, workspaceRoot, source string, hidden bool) app.Session {
	now := time.Now().UTC()
	if title == "" {
		title = "New SparkClaw Session"
	}
	if strings.TrimSpace(ownerID) == "" {
		ownerID = app.DefaultOwnerID
	}
	if strings.TrimSpace(source) == "" {
		source = "webchat"
	}
	session := app.Session{ID: app.NewID("s"), OwnerID: ownerID, WorkspaceRoot: strings.TrimSpace(workspaceRoot), Title: title, Source: source, Hidden: hidden, CreatedAt: now, UpdatedAt: now}
	ctx := context.Background()
	_, _ = s.db.Exec(ctx, `
		INSERT INTO sessions (id, owner_id, workspace_root, title, source, hidden, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, session.ID, session.OwnerID, session.WorkspaceRoot, session.Title, session.Source, session.Hidden, session.CreatedAt, session.UpdatedAt)
	s.appendAudit(ctx, "session.created", session.ID, "", "system", "Session created", map[string]any{"title": title, "owner_id": ownerID})
	s.appendEvent(ctx, "session.created", session.ID, "", session)
	return session
}

func (s *PostgresStore) ListSessions() []app.Session {
	rows, err := s.db.Query(context.Background(), `
		SELECT id, owner_id, workspace_root, title, source, hidden, created_at, updated_at
		FROM sessions
		WHERE hidden = false
		ORDER BY updated_at DESC
	`)
	if err != nil {
		return []app.Session{}
	}
	defer rows.Close()
	return collectRows(rows, scanSession)
}

func (s *PostgresStore) GetSession(id string) (app.Session, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, owner_id, workspace_root, title, source, hidden, created_at, updated_at
		FROM sessions
		WHERE id = $1
	`, id)
	session, err := scanSession(row)
	return session, err == nil
}

func (s *PostgresStore) UpdateSessionTitle(id, title string) (app.Session, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return app.Session{}, errors.New("session title is required")
	}
	now := time.Now().UTC()
	ctx := context.Background()
	row := s.db.QueryRow(ctx, `
		UPDATE sessions
		SET title = $2, updated_at = $3
		WHERE id = $1
		RETURNING id, owner_id, workspace_root, title, source, hidden, created_at, updated_at
	`, id, title, now)
	session, err := scanSession(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.Session{}, errors.New("session not found")
		}
		return app.Session{}, err
	}
	s.appendAudit(ctx, "session.updated", id, "", "owner", "Session renamed", map[string]any{"title": title})
	s.appendEvent(ctx, "session.updated", id, "", session)
	return session, nil
}

func (s *PostgresStore) DeleteSession(id string) (app.Session, error) {
	session, ok := s.GetSession(id)
	if !ok {
		return app.Session{}, errors.New("session not found")
	}
	ctx := context.Background()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return app.Session{}, err
	}
	defer tx.Rollback(ctx)
	deleteStatements := []string{
		`DELETE FROM run_feedback WHERE session_id = $1`,
		`DELETE FROM approvals WHERE session_id = $1`,
		`DELETE FROM document_records WHERE session_id = $1`,
		`DELETE FROM memory_candidates WHERE session_id = $1`,
		`DELETE FROM memories WHERE source_run_id IN (SELECT id FROM agent_runs WHERE session_id = $1)`,
		`DELETE FROM episode_summaries WHERE session_id = $1`,
		`DELETE FROM artifact_objects WHERE session_id = $1`,
		`DELETE FROM external_chat_messages WHERE chat_session_id IN (SELECT id FROM external_chat_sessions WHERE linked_session_id = $1)`,
		`DELETE FROM external_chat_sessions WHERE linked_session_id = $1`,
		`DELETE FROM browser_login_blocks WHERE session_id = $1`,
		`DELETE FROM tool_calls WHERE session_id = $1`,
		`DELETE FROM model_calls WHERE session_id = $1`,
		`DELETE FROM messages WHERE session_id = $1`,
		`DELETE FROM agent_runs WHERE session_id = $1`,
		`DELETE FROM audit_events WHERE session_id = $1`,
		`DELETE FROM events WHERE session_id = $1`,
		`DELETE FROM sessions WHERE id = $1`,
	}
	for _, statement := range deleteStatements {
		if _, err := tx.Exec(ctx, statement, id); err != nil {
			return app.Session{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return app.Session{}, err
	}
	s.appendAudit(ctx, "session.deleted", "", "", "owner", "Session deleted", map[string]any{"session_id": id, "title": session.Title})
	s.appendEvent(ctx, "session.deleted", "", "", session)
	return session, nil
}

func (s *PostgresStore) SaveClient(client app.Client) {
	if client.ID == "" {
		client.ID = app.NewID("client")
	}
	if client.CreatedAt.IsZero() {
		client.CreatedAt = time.Now().UTC()
	}
	if strings.TrimSpace(client.OwnerID) == "" {
		client.OwnerID = app.DefaultOwnerID
	}
	if strings.TrimSpace(client.ActorID) == "" {
		client.ActorID = client.OwnerID
	}
	ctx := context.Background()
	_, _ = s.db.Exec(ctx, `
		INSERT INTO clients (id, owner_id, actor_id, name, token_hash, created_at, last_seen_at, revoked_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO UPDATE SET
			owner_id = EXCLUDED.owner_id,
			actor_id = EXCLUDED.actor_id,
			name = EXCLUDED.name,
			token_hash = EXCLUDED.token_hash,
			last_seen_at = EXCLUDED.last_seen_at,
			revoked_at = EXCLUDED.revoked_at
	`, client.ID, client.OwnerID, client.ActorID, client.Name, client.TokenHash, client.CreatedAt, client.LastSeenAt, client.RevokedAt)
	s.appendAudit(ctx, "client.saved", "", "", "gateway", client.Name, map[string]any{"client_id": client.ID})
	s.appendEvent(ctx, "client.saved", "", "", client)
}

func (s *PostgresStore) GetClient(id string) (app.Client, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, owner_id, actor_id, name, token_hash, created_at, last_seen_at, revoked_at
		FROM clients
		WHERE id = $1
	`, id)
	client, err := scanClient(row)
	return client, err == nil
}

func (s *PostgresStore) ListClients() []app.Client {
	rows, err := s.db.Query(context.Background(), `
		SELECT id, owner_id, actor_id, name, token_hash, created_at, last_seen_at, revoked_at
		FROM clients
		ORDER BY created_at DESC
	`)
	if err != nil {
		return []app.Client{}
	}
	defer rows.Close()
	return collectRows(rows, scanClient)
}

func (s *PostgresStore) RevokeClient(id string) (app.Client, error) {
	ctx := context.Background()
	now := time.Now().UTC()
	row := s.db.QueryRow(ctx, `
		UPDATE clients
		SET revoked_at = $2
		WHERE id = $1
		RETURNING id, owner_id, actor_id, name, token_hash, created_at, last_seen_at, revoked_at
	`, id, now)
	client, err := scanClient(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.Client{}, errors.New("client not found")
		}
		return app.Client{}, err
	}
	s.appendAudit(ctx, "client.revoked", "", "", "owner", client.Name, map[string]any{"client_id": client.ID})
	s.appendEvent(ctx, "client.revoked", "", "", client)
	return client, nil
}

func (s *PostgresStore) FindClientByTokenHash(tokenHash string) (app.Client, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, owner_id, actor_id, name, token_hash, created_at, last_seen_at, revoked_at
		FROM clients
		WHERE token_hash = $1 AND revoked_at IS NULL
	`, tokenHash)
	client, err := scanClient(row)
	return client, err == nil
}

func (s *PostgresStore) TouchClient(id string) {
	_, _ = s.db.Exec(context.Background(), `
		UPDATE clients
		SET last_seen_at = $2
		WHERE id = $1 AND revoked_at IS NULL
	`, id, time.Now().UTC())
}

func (s *PostgresStore) GetOwnerProfile() app.OwnerProfile {
	profile, _ := s.GetOwnerProfileByID(app.DefaultOwnerID)
	return profile
}

func (s *PostgresStore) UpdateOwnerProfile(profile app.OwnerProfile) app.OwnerProfile {
	profile.ID = app.DefaultOwnerID
	return s.SaveOwnerProfile(profile)
}

func (s *PostgresStore) GetOwnerProfileByID(id string) (app.OwnerProfile, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		id = app.DefaultOwnerID
	}
	row := s.db.QueryRow(context.Background(), `
		SELECT id, source, external_ref, workspace_root, default_channel, default_binding_id,
			display_name, email, preferences, created_at, updated_at
		FROM owners
		WHERE id = $1
	`, id)
	profile, err := scanOwnerProfile(row)
	if err == nil {
		return profile, true
	}
	if id != app.DefaultOwnerID {
		return app.OwnerProfile{}, false
	}
	profile = app.DefaultOwnerProfile()
	_, _ = s.db.Exec(context.Background(), `
		INSERT INTO owners (id, source, external_ref, workspace_root, default_channel, default_binding_id,
			display_name, email, preferences, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (id) DO NOTHING
	`, profile.ID, profile.Source, profile.ExternalRef, profile.WorkspaceRoot, profile.DefaultChannel,
		profile.DefaultBindingID, profile.DisplayName, profile.Email, mustJSON(profile.Preferences),
		profile.CreatedAt, profile.UpdatedAt)
	return profile, true
}

func (s *PostgresStore) SaveOwnerProfile(profile app.OwnerProfile) app.OwnerProfile {
	profile.ID = strings.TrimSpace(profile.ID)
	if profile.ID == "" {
		profile.ID = app.DefaultOwnerID
	}
	current, ok := s.GetOwnerProfileByID(profile.ID)
	now := time.Now().UTC()
	if profile.CreatedAt.IsZero() {
		profile.CreatedAt = current.CreatedAt
	}
	if !ok || profile.CreatedAt.IsZero() {
		profile.CreatedAt = now
	}
	profile.UpdatedAt = now
	if profile.Preferences == nil {
		profile.Preferences = map[string]string{}
	}
	profile = normalizeOwnerProfile(profile)
	ctx := context.Background()
	_, _ = s.db.Exec(ctx, `
		INSERT INTO owners (id, source, external_ref, workspace_root, default_channel, default_binding_id,
			display_name, email, preferences, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (id) DO UPDATE SET
			source = EXCLUDED.source,
			external_ref = EXCLUDED.external_ref,
			workspace_root = EXCLUDED.workspace_root,
			default_channel = EXCLUDED.default_channel,
			default_binding_id = EXCLUDED.default_binding_id,
			display_name = EXCLUDED.display_name,
			email = EXCLUDED.email,
			preferences = EXCLUDED.preferences,
			updated_at = EXCLUDED.updated_at
	`, profile.ID, profile.Source, profile.ExternalRef, profile.WorkspaceRoot, profile.DefaultChannel,
		profile.DefaultBindingID, profile.DisplayName, profile.Email, mustJSON(profile.Preferences),
		profile.CreatedAt, profile.UpdatedAt)
	s.appendAudit(ctx, "owner_profile.updated", "", "", "owner", profile.DisplayName, map[string]any{
		"owner_id":     profile.ID,
		"source":       profile.Source,
		"external_ref": profile.ExternalRef != "",
		"email_set":    profile.Email != "",
		"preferences":  len(profile.Preferences),
		"display_name": profile.DisplayName,
	})
	s.appendEvent(ctx, "owner_profile.updated", "", "", profile)
	return profile
}

func (s *PostgresStore) ListOwnerProfiles() []app.OwnerProfile {
	rows, err := s.db.Query(context.Background(), `
		SELECT id, source, external_ref, workspace_root, default_channel, default_binding_id,
			display_name, email, preferences, created_at, updated_at
		FROM owners
		ORDER BY updated_at DESC
	`)
	if err != nil {
		return []app.OwnerProfile{}
	}
	defer rows.Close()
	return collectRows(rows, scanOwnerProfile)
}

func (s *PostgresStore) FindOwnerProfileByExternalRef(source, externalRef string) (app.OwnerProfile, bool) {
	source = strings.TrimSpace(source)
	externalRef = strings.TrimSpace(externalRef)
	if source == "" || externalRef == "" {
		return app.OwnerProfile{}, false
	}
	row := s.db.QueryRow(context.Background(), `
		SELECT id, source, external_ref, workspace_root, default_channel, default_binding_id,
			display_name, email, preferences, created_at, updated_at
		FROM owners
		WHERE source = $1 AND external_ref = $2
		ORDER BY updated_at DESC
		LIMIT 1
	`, source, externalRef)
	profile, err := scanOwnerProfile(row)
	if err != nil {
		return app.OwnerProfile{}, false
	}
	return profile, true
}

func (s *PostgresStore) SavePairingCode(code app.PairingCode) {
	if code.ID == "" {
		code.ID = app.NewID("pair")
	}
	if code.CreatedAt.IsZero() {
		code.CreatedAt = time.Now().UTC()
	}
	if code.Status == "" {
		code.Status = "pending"
	}
	ctx := context.Background()
	_, _ = s.db.Exec(ctx, `
		INSERT INTO pairing_codes (id, code_hash, status, expires_at, created_at, claimed_at, client_id)
		VALUES ($1, $2, $3, $4, $5, $6, nullif($7, ''))
		ON CONFLICT (id) DO UPDATE SET
			code_hash = EXCLUDED.code_hash,
			status = EXCLUDED.status,
			expires_at = EXCLUDED.expires_at,
			claimed_at = EXCLUDED.claimed_at,
			client_id = EXCLUDED.client_id
	`, code.ID, code.CodeHash, code.Status, code.ExpiresAt, code.CreatedAt, code.ClaimedAt, code.ClientID)
	s.appendAudit(ctx, "pairing_code.created", "", "", "gateway", "Pairing code created", map[string]any{"pairing_id": code.ID})
	s.appendEvent(ctx, "pairing_code.created", "", "", code)
}

func (s *PostgresStore) GetPairingCode(id string) (app.PairingCode, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, code_hash, status, expires_at, created_at, claimed_at, coalesce(client_id, '')
		FROM pairing_codes
		WHERE id = $1
	`, id)
	code, err := scanPairingCode(row)
	return code, err == nil
}

func (s *PostgresStore) ClaimPairingCode(id, clientID string) (app.PairingCode, error) {
	ctx := context.Background()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return app.PairingCode{}, err
	}
	defer rollbackTx(ctx, tx)
	row := tx.QueryRow(ctx, `
		SELECT id, code_hash, status, expires_at, created_at, claimed_at, coalesce(client_id, '')
		FROM pairing_codes
		WHERE id = $1
		FOR UPDATE
	`, id)
	code, err := scanPairingCode(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.PairingCode{}, errors.New("pairing code not found")
		}
		return app.PairingCode{}, err
	}
	if code.Status != "pending" {
		return app.PairingCode{}, errors.New("pairing code is not pending")
	}
	now := time.Now().UTC()
	if now.After(code.ExpiresAt) {
		_, _ = tx.Exec(ctx, `UPDATE pairing_codes SET status = 'expired' WHERE id = $1`, id)
		if err := tx.Commit(ctx); err != nil {
			return app.PairingCode{}, err
		}
		return app.PairingCode{}, errors.New("pairing code expired")
	}
	code.Status = "claimed"
	code.ClaimedAt = &now
	code.ClientID = clientID
	if _, err := tx.Exec(ctx, `
		UPDATE pairing_codes
		SET status = $2, claimed_at = $3, client_id = $4
		WHERE id = $1
	`, code.ID, code.Status, code.ClaimedAt, code.ClientID); err != nil {
		return app.PairingCode{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return app.PairingCode{}, err
	}
	s.appendAudit(ctx, "pairing_code.claimed", "", "", "gateway", "Pairing code claimed", map[string]any{"pairing_id": code.ID, "client_id": clientID})
	s.appendEvent(ctx, "pairing_code.claimed", "", "", code)
	return code, nil
}

func (s *PostgresStore) AddMessage(message app.Message) app.Message {
	if message.ID == "" {
		message.ID = app.NewID("m")
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = time.Now().UTC()
	}
	ctx := context.Background()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return message
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM messages WHERE id = $1)`, message.ID).Scan(&exists); err != nil || exists {
		return message
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO messages (id, session_id, run_id, role, content, attachments, created_at)
		VALUES ($1, $2, nullif($3, ''), $4, $5, $6, $7)
	`, message.ID, message.SessionID, message.RunID, message.Role, message.Content, mustJSON(message.Attachments), message.CreatedAt); err != nil {
		return message
	}

	var session app.Session
	if err := tx.QueryRow(ctx, `
		SELECT id, owner_id, workspace_root, title, source, hidden, created_at, updated_at
		FROM sessions WHERE id = $1
	`, message.SessionID).Scan(&session.ID, &session.OwnerID, &session.WorkspaceRoot, &session.Title, &session.Source, &session.Hidden, &session.CreatedAt, &session.UpdatedAt); err == nil {
		if !session.Hidden && (session.Title == "" || session.Title == "New SparkClaw Session") {
			session.Title = deriveTitle(message.Content)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE sessions
			SET title = $2, updated_at = $3
			WHERE id = $1
		`, session.ID, session.Title, message.CreatedAt); err != nil {
			return message
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO events (id, happened_at, type, session_id, run_id, payload)
		VALUES ($1, $2, 'message.created', $3, nullif($4, ''), $5)
	`, app.NewID("evt"), time.Now().UTC(), message.SessionID, message.RunID, mustJSON(message)); err != nil {
		return message
	}
	if err := tx.Commit(ctx); err != nil {
		return message
	}
	return message
}

func (s *PostgresStore) ListMessages(sessionID string) []app.Message {
	rows, err := s.db.Query(context.Background(), `
		SELECT id, session_id, coalesce(run_id, ''), role, content, attachments, created_at
		FROM messages
		WHERE session_id = $1
		ORDER BY created_at ASC
	`, sessionID)
	if err != nil {
		return []app.Message{}
	}
	defer rows.Close()
	return collectRows(rows, scanMessage)
}

func (s *PostgresStore) SaveRunFeedback(feedback app.RunFeedback) app.RunFeedback {
	if feedback.ID == "" {
		feedback.ID = app.NewID("fb")
	}
	now := time.Now().UTC()
	if feedback.CreatedAt.IsZero() {
		feedback.CreatedAt = now
	}
	feedback.UpdatedAt = now
	feedback.Rating = strings.TrimSpace(feedback.Rating)
	feedback.Note = strings.TrimSpace(feedback.Note)
	feedback.Correction = strings.TrimSpace(feedback.Correction)
	ctx := context.Background()
	if feedback.MessageID != "" {
		row := s.db.QueryRow(ctx, `
			SELECT id, session_id, run_id, coalesce(message_id, ''), rating, note, correction, created_at, updated_at
			FROM run_feedback
			WHERE run_id = $1 AND message_id = $2
			LIMIT 1
		`, feedback.RunID, feedback.MessageID)
		if existing, err := scanRunFeedback(row); err == nil {
			feedback.ID = existing.ID
			feedback.CreatedAt = existing.CreatedAt
		}
	}
	_, _ = s.db.Exec(ctx, `
		INSERT INTO run_feedback (
			id, session_id, run_id, message_id, rating, note, correction, created_at, updated_at
		)
		VALUES ($1, $2, $3, nullif($4, ''), $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO UPDATE SET
			session_id = EXCLUDED.session_id,
			run_id = EXCLUDED.run_id,
			message_id = EXCLUDED.message_id,
			rating = EXCLUDED.rating,
			note = EXCLUDED.note,
			correction = EXCLUDED.correction,
			updated_at = EXCLUDED.updated_at
	`, feedback.ID, feedback.SessionID, feedback.RunID, feedback.MessageID, feedback.Rating, feedback.Note, feedback.Correction, feedback.CreatedAt, feedback.UpdatedAt)
	s.appendAudit(ctx, "run_feedback.saved", feedback.SessionID, feedback.RunID, "owner", feedback.Rating, map[string]any{
		"feedback_id":    feedback.ID,
		"message_id":     feedback.MessageID,
		"has_note":       feedback.Note != "",
		"has_correction": feedback.Correction != "",
	})
	s.appendEvent(ctx, "run_feedback.saved", feedback.SessionID, feedback.RunID, feedback)
	return feedback
}

func (s *PostgresStore) ListRunFeedback(runID string) []app.RunFeedback {
	rows, err := s.db.Query(context.Background(), `
		SELECT id, session_id, run_id, coalesce(message_id, ''), rating, note, correction, created_at, updated_at
		FROM run_feedback
		WHERE $1 = '' OR run_id = $1
		ORDER BY updated_at DESC
	`, runID)
	if err != nil {
		return []app.RunFeedback{}
	}
	defer rows.Close()
	return collectRows(rows, scanRunFeedback)
}

func (s *PostgresStore) SaveRun(run app.AgentRun) {
	ctx := context.Background()
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now().UTC()
	}
	workflowState := optionalJSON(run.Workflow)
	messageContext := optionalJSON(run.MessageContext)
	_, _ = s.db.Exec(ctx, `
		INSERT INTO agent_runs (id, session_id, state, model_lane, risk_level, summary, workflow_state, message_context, started_at, completed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (id) DO UPDATE SET
			state = EXCLUDED.state,
			model_lane = EXCLUDED.model_lane,
			risk_level = EXCLUDED.risk_level,
			summary = EXCLUDED.summary,
			workflow_state = EXCLUDED.workflow_state,
			message_context = EXCLUDED.message_context,
			started_at = EXCLUDED.started_at,
			completed_at = EXCLUDED.completed_at
	`, run.ID, run.SessionID, run.State, run.ModelLane, string(run.Risk), run.Summary, workflowState, messageContext, run.StartedAt, run.CompletedAt)
	s.appendEvent(ctx, "run."+run.State, run.SessionID, run.ID, run)
}

func (s *PostgresStore) GetRun(id string) (app.AgentRun, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, session_id, state, model_lane, risk_level, started_at, completed_at, coalesce(summary, ''), workflow_state, message_context
		FROM agent_runs
		WHERE id = $1
	`, id)
	run, err := scanRun(row)
	return run, err == nil
}

func (s *PostgresStore) ListRuns(sessionID string) []app.AgentRun {
	rows, err := s.db.Query(context.Background(), `
		SELECT id, session_id, state, model_lane, risk_level, started_at, completed_at, coalesce(summary, ''), workflow_state, message_context
		FROM agent_runs
		WHERE $1 = '' OR session_id = $1
		ORDER BY started_at DESC
	`, sessionID)
	if err != nil {
		return []app.AgentRun{}
	}
	defer rows.Close()
	return collectRows(rows, scanRun)
}

func (s *PostgresStore) SaveModelCall(call app.ModelCall) {
	ctx := context.Background()
	if call.ID == "" {
		call.ID = app.NewID("mc")
	}
	if call.StartedAt.IsZero() {
		call.StartedAt = time.Now().UTC()
	}
	_, _ = s.db.Exec(ctx, `
		INSERT INTO model_calls (
			id, session_id, run_id, lane, profile, model, operation, mock, fallback, status,
			prompt_tokens, response_tokens, total_tokens, latency_ms, error, started_at, completed_at
		)
		VALUES ($1, nullif($2, ''), nullif($3, ''), $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, nullif($15, ''), $16, $17)
		ON CONFLICT (id) DO UPDATE SET
			session_id = EXCLUDED.session_id,
			run_id = EXCLUDED.run_id,
			lane = EXCLUDED.lane,
			profile = EXCLUDED.profile,
			model = EXCLUDED.model,
			operation = EXCLUDED.operation,
			mock = EXCLUDED.mock,
			fallback = EXCLUDED.fallback,
			status = EXCLUDED.status,
			prompt_tokens = EXCLUDED.prompt_tokens,
			response_tokens = EXCLUDED.response_tokens,
			total_tokens = EXCLUDED.total_tokens,
			latency_ms = EXCLUDED.latency_ms,
			error = EXCLUDED.error,
			started_at = EXCLUDED.started_at,
			completed_at = EXCLUDED.completed_at
	`, call.ID, call.SessionID, call.RunID, call.Lane, call.Profile, call.Model, call.Operation, call.Mock, call.Fallback, call.Status,
		call.PromptTokens, call.ResponseTokens, call.TotalTokens, call.LatencyMS, call.Error, call.StartedAt, call.CompletedAt)
	s.appendAudit(ctx, "model_call."+call.Status, call.SessionID, call.RunID, "model-router", call.Model, map[string]any{
		"lane":       call.Lane,
		"profile":    call.Profile,
		"operation":  call.Operation,
		"latency_ms": call.LatencyMS,
	})
	s.appendEvent(ctx, "model_call."+call.Status, call.SessionID, call.RunID, call)
}

func (s *PostgresStore) ListModelCalls(sessionID, runID string) []app.ModelCall {
	rows, err := s.db.Query(context.Background(), `
		SELECT id, coalesce(session_id, ''), coalesce(run_id, ''), lane, profile, model, operation, mock, fallback,
			status, prompt_tokens, response_tokens, total_tokens, latency_ms, coalesce(error, ''), started_at, completed_at
		FROM model_calls
		WHERE ($1 = '' OR session_id = $1) AND ($2 = '' OR run_id = $2)
		ORDER BY started_at ASC
	`, sessionID, runID)
	if err != nil {
		return []app.ModelCall{}
	}
	defer rows.Close()
	return collectRows(rows, scanModelCall)
}

func (s *PostgresStore) SaveToolCall(call app.ToolCall) {
	ctx := context.Background()
	if call.StartedAt.IsZero() {
		call.StartedAt = time.Now().UTC()
	}
	args := mustJSON(call.Arguments)
	result := optionalJSON(call.Result)
	_, _ = s.db.Exec(ctx, `
		INSERT INTO tool_calls (
			id, session_id, run_id, workflow_id, workflow_node_id, scope_revision, capability,
			tool, risk_level, status, arguments, result, error, error_code,
			approval_id, observation_ref, observation_summary, started_at, completed_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, nullif($13, ''), nullif($14, ''), nullif($15, ''), nullif($16, ''), $17, $18, $19)
		ON CONFLICT (id) DO UPDATE SET
			workflow_id = EXCLUDED.workflow_id,
			workflow_node_id = EXCLUDED.workflow_node_id,
			scope_revision = EXCLUDED.scope_revision,
			capability = EXCLUDED.capability,
			risk_level = EXCLUDED.risk_level,
			status = EXCLUDED.status,
			arguments = EXCLUDED.arguments,
			result = EXCLUDED.result,
			error = EXCLUDED.error,
			error_code = EXCLUDED.error_code,
			approval_id = EXCLUDED.approval_id,
			observation_ref = EXCLUDED.observation_ref,
			observation_summary = EXCLUDED.observation_summary,
			started_at = EXCLUDED.started_at,
			completed_at = EXCLUDED.completed_at
	`, call.ID, call.SessionID, call.RunID, string(call.WorkflowID), string(call.WorkflowNodeID), call.ScopeRevision, call.Capability,
		call.Tool, string(call.Risk), call.Status, args, result, call.Error, call.ErrorCode, call.ApprovalID, call.ObservationRef, call.ObservationSummary, call.StartedAt, call.CompletedAt)
	s.appendAudit(ctx, "tool_call."+call.Status, call.SessionID, call.RunID, "agent", call.Tool, map[string]any{
		"risk": call.Risk,
		"id":   call.ID,
	})
	s.appendEvent(ctx, "tool_call."+call.Status, call.SessionID, call.RunID, call)
}

func (s *PostgresStore) GetToolCall(id string) (app.ToolCall, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, session_id, run_id, workflow_id, workflow_node_id, scope_revision, capability,
			tool, risk_level, status, arguments, result, coalesce(error, ''), coalesce(error_code, ''),
			coalesce(approval_id, ''), started_at, completed_at, coalesce(observation_ref, ''), coalesce(observation_summary, '')
		FROM tool_calls
		WHERE id = $1
	`, id)
	call, err := scanToolCall(row)
	return call, err == nil
}

func (s *PostgresStore) ListToolCalls(sessionID string) []app.ToolCall {
	rows, err := s.db.Query(context.Background(), `
		SELECT id, session_id, run_id, workflow_id, workflow_node_id, scope_revision, capability,
			tool, risk_level, status, arguments, result, coalesce(error, ''), coalesce(error_code, ''),
			coalesce(approval_id, ''), started_at, completed_at, coalesce(observation_ref, ''), coalesce(observation_summary, '')
		FROM tool_calls
		WHERE $1 = '' OR session_id = $1
		ORDER BY started_at ASC
	`, sessionID)
	if err != nil {
		return []app.ToolCall{}
	}
	defer rows.Close()
	return collectRows(rows, scanToolCall)
}

func (s *PostgresStore) SaveDocumentRecord(record app.DocumentRecord) app.DocumentRecord {
	ctx := context.Background()
	now := time.Now().UTC()
	if record.ID == "" {
		record.ID = app.NewID("doc")
	}
	if record.OwnerID == "" {
		record.OwnerID = app.DefaultOwnerID
	}
	if record.Status == "" {
		record.Status = app.DocumentStatusAvailable
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	if record.LastActivityAt.IsZero() {
		record.LastActivityAt = now
	}
	if record.LastActivityID == "" {
		record.LastActivityID = record.ID
	}
	record.UpdatedAt = now
	_, _ = s.db.Exec(ctx, `
		INSERT INTO document_records (
			id, owner_id, session_id, governed_path, name, content_type, format,
			size_bytes, sha256, status, source, source_message_id, source_run_id,
			source_tool_call_id, parent_document_id, last_activity, last_activity_id,
			last_activity_at, created_at, updated_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
			$11, $12, $13, $14, $15, $16, $17, $18, $19, $20
		)
		ON CONFLICT (id) DO UPDATE SET
			owner_id = EXCLUDED.owner_id,
			session_id = EXCLUDED.session_id,
			governed_path = EXCLUDED.governed_path,
			name = EXCLUDED.name,
			content_type = EXCLUDED.content_type,
			format = EXCLUDED.format,
			size_bytes = EXCLUDED.size_bytes,
			sha256 = EXCLUDED.sha256,
			status = EXCLUDED.status,
			source = EXCLUDED.source,
			source_message_id = EXCLUDED.source_message_id,
			source_run_id = EXCLUDED.source_run_id,
			source_tool_call_id = EXCLUDED.source_tool_call_id,
			parent_document_id = EXCLUDED.parent_document_id,
			last_activity = EXCLUDED.last_activity,
			last_activity_id = EXCLUDED.last_activity_id,
			last_activity_at = EXCLUDED.last_activity_at,
			updated_at = EXCLUDED.updated_at
	`, record.ID, record.OwnerID, record.SessionID, record.GovernedPath, record.Name,
		record.ContentType, record.Format, record.SizeBytes, record.SHA256, record.Status,
		record.Source, record.SourceMessageID, record.SourceRunID, record.SourceToolCallID,
		record.ParentDocumentID, record.LastActivity, record.LastActivityID,
		record.LastActivityAt, record.CreatedAt, record.UpdatedAt)
	s.appendAudit(ctx, "document.saved", record.SessionID, record.SourceRunID, "document_registry", record.LastActivity, map[string]any{
		"document_id": record.ID,
		"path":        record.GovernedPath,
		"activity_id": record.LastActivityID,
	})
	s.appendEvent(ctx, "document.saved", record.SessionID, record.SourceRunID, record)
	return record
}

func (s *PostgresStore) GetDocumentRecord(id string) (app.DocumentRecord, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, owner_id, session_id, governed_path, name, content_type, format,
			size_bytes, sha256, status, source, source_message_id, source_run_id,
			source_tool_call_id, parent_document_id, last_activity, last_activity_id,
			last_activity_at, created_at, updated_at
		FROM document_records
		WHERE id = $1
	`, id)
	record, err := scanDocumentRecord(row)
	return record, err == nil
}

func (s *PostgresStore) ListDocumentRecords(ownerID, sessionID string, limit int) []app.DocumentRecord {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(context.Background(), `
		SELECT id, owner_id, session_id, governed_path, name, content_type, format,
			size_bytes, sha256, status, source, source_message_id, source_run_id,
			source_tool_call_id, parent_document_id, last_activity, last_activity_id,
			last_activity_at, created_at, updated_at
		FROM document_records
		WHERE ($1 = '' OR owner_id = $1) AND ($2 = '' OR session_id = $2)
		ORDER BY last_activity_at DESC, updated_at DESC, id ASC
		LIMIT $3
	`, ownerID, sessionID, limit)
	if err != nil {
		return []app.DocumentRecord{}
	}
	defer rows.Close()
	return collectRows(rows, scanDocumentRecord)
}

func (s *PostgresStore) SaveApproval(approval app.Approval) {
	ctx := context.Background()
	approval = normalizeApproval(approval)
	if approval.CreatedAt.IsZero() {
		approval.CreatedAt = time.Now().UTC()
	}
	_, _ = s.db.Exec(ctx, `
		INSERT INTO approvals (
			id, source, external_id, external_context, session_id, run_id, tool_call_id,
			tool, risk_level, status, summary, reason, resources, arguments, created_at,
			resolved_at, resolution_note
		)
		VALUES ($1, $2, $3, $4, nullif($5, ''), nullif($6, ''), nullif($7, ''), $8,
			$9, $10, $11, $12, $13, $14, $15, $16, nullif($17, ''))
		ON CONFLICT (id) DO UPDATE SET
			source = EXCLUDED.source,
			external_id = EXCLUDED.external_id,
			external_context = EXCLUDED.external_context,
			status = EXCLUDED.status,
			summary = EXCLUDED.summary,
			reason = EXCLUDED.reason,
			resources = EXCLUDED.resources,
			arguments = EXCLUDED.arguments,
			resolved_at = EXCLUDED.resolved_at,
			resolution_note = EXCLUDED.resolution_note
	`, approval.ID, string(approval.Source), approval.ExternalID, mustJSON(approval.ExternalContext), approval.SessionID, approval.RunID, approval.ToolCallID, approval.Tool, string(approval.Risk), approval.Status, approval.Summary, approval.Reason, mustJSON(approval.Resources), mustJSON(approval.Arguments), approval.CreatedAt, approval.ResolvedAt, approval.ResolutionNote)
	actor := "policy"
	if approval.Source != app.ApprovalSourceTool {
		actor = "integration"
	}
	s.appendAudit(ctx, "approval."+approval.Status, approval.SessionID, approval.RunID, actor, approval.Summary, map[string]any{
		"tool": approval.Tool,
		"risk": approval.Risk,
	})
	s.appendEvent(ctx, "approval."+approval.Status, approval.SessionID, approval.RunID, approval)
}

func (s *PostgresStore) GetApproval(id string) (app.Approval, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, source, external_id, external_context,
			coalesce(session_id, ''), coalesce(run_id, ''), coalesce(tool_call_id, ''),
			tool, risk_level, status, summary, reason, resources, arguments, created_at,
			resolved_at, coalesce(resolution_note, '')
		FROM approvals
		WHERE id = $1
	`, id)
	approval, err := scanApproval(row)
	return approval, err == nil
}

func (s *PostgresStore) FindApprovalByExternalRef(source app.ApprovalSource, externalID string) (app.Approval, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, source, external_id, external_context,
			coalesce(session_id, ''), coalesce(run_id, ''), coalesce(tool_call_id, ''),
			tool, risk_level, status, summary, reason, resources, arguments, created_at,
			resolved_at, coalesce(resolution_note, '')
		FROM approvals
		WHERE source = $1 AND external_id = $2
	`, source, externalID)
	approval, err := scanApproval(row)
	return approval, err == nil
}

func (s *PostgresStore) UpdatePendingApproval(approval app.Approval) (app.Approval, error) {
	approval = normalizeApproval(approval)
	approval.Status = "pending"
	approval.ResolvedAt = nil
	approval.ResolutionNote = ""
	command, err := s.db.Exec(context.Background(), `
		UPDATE approvals SET
			source = $2, external_id = $3, external_context = $4, summary = $5,
			reason = $6, resources = $7, arguments = $8
		WHERE id = $1 AND status = 'pending'
	`, approval.ID, string(approval.Source), approval.ExternalID, mustJSON(approval.ExternalContext),
		approval.Summary, approval.Reason, mustJSON(approval.Resources), mustJSON(approval.Arguments))
	if err != nil {
		return app.Approval{}, err
	}
	if command.RowsAffected() == 0 {
		if _, ok := s.GetApproval(approval.ID); !ok {
			return app.Approval{}, errors.New("approval not found")
		}
		return app.Approval{}, errors.New("approval already resolved")
	}
	s.appendEvent(context.Background(), "approval.pending", approval.SessionID, approval.RunID, approval)
	return approval, nil
}

func (s *PostgresStore) ResolveApproval(id, status, note string) (app.Approval, error) {
	ctx := context.Background()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return app.Approval{}, err
	}
	defer rollbackTx(ctx, tx)
	row := tx.QueryRow(ctx, `
		SELECT id, source, external_id, external_context,
			coalesce(session_id, ''), coalesce(run_id, ''), coalesce(tool_call_id, ''),
			tool, risk_level, status, summary, reason, resources, arguments, created_at,
			resolved_at, coalesce(resolution_note, '')
		FROM approvals
		WHERE id = $1
		FOR UPDATE
	`, id)
	approval, err := scanApproval(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.Approval{}, errors.New("approval not found")
		}
		return app.Approval{}, err
	}
	if approval.Status != "pending" {
		return app.Approval{}, errors.New("approval already resolved")
	}
	now := time.Now().UTC()
	approval.Status = status
	approval.ResolvedAt = &now
	approval.ResolutionNote = note
	if _, err := tx.Exec(ctx, `
		UPDATE approvals
		SET status = $2, resolved_at = $3, resolution_note = nullif($4, '')
		WHERE id = $1
	`, approval.ID, approval.Status, approval.ResolvedAt, approval.ResolutionNote); err != nil {
		return app.Approval{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return app.Approval{}, err
	}
	actor := "owner"
	if status == "resolved_elsewhere" {
		actor = "integration"
	}
	s.appendAudit(ctx, "approval."+status, approval.SessionID, approval.RunID, actor, approval.Summary, map[string]any{"note": note})
	s.appendEvent(ctx, "approval."+status, approval.SessionID, approval.RunID, approval)
	return approval, nil
}

func (s *PostgresStore) ListApprovals(status string) []app.Approval {
	rows, err := s.db.Query(context.Background(), `
		SELECT id, source, external_id, external_context,
			coalesce(session_id, ''), coalesce(run_id, ''), coalesce(tool_call_id, ''),
			tool, risk_level, status, summary, reason, resources, arguments, created_at,
			resolved_at, coalesce(resolution_note, '')
		FROM approvals
		WHERE $1 = '' OR status = $1
		ORDER BY created_at DESC
	`, status)
	if err != nil {
		return []app.Approval{}
	}
	defer rows.Close()
	return collectRows(rows, scanApproval)
}

func (s *PostgresStore) SaveReminder(reminder app.Reminder) app.Reminder {
	now := time.Now().UTC()
	if reminder.ID == "" {
		reminder.ID = app.NewID("rem")
	}
	if reminder.CreatedAt.IsZero() {
		reminder.CreatedAt = now
	}
	if reminder.UpdatedAt.IsZero() {
		reminder.UpdatedAt = now
	}
	if reminder.Status == "" {
		reminder.Status = "pending"
	}
	if reminder.TextSummary == "" {
		reminder.TextSummary = summarizeReminderText(reminder.Text)
	}
	ctx := context.Background()
	_, _ = s.db.Exec(ctx, `
		INSERT INTO reminders (
			id, session_id, run_id, text, text_summary, due_time, timezone, channel, recipient,
			recipient_binding, binding_id, credential_ref, base_url, recurrence, dedupe_key, status,
			last_delivery_id, last_error, created_at, updated_at, sent_at, canceled_at, delivery_attempt, schedule_spec
		)
		VALUES ($1, nullif($2, ''), nullif($3, ''), $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24)
		ON CONFLICT (id) DO UPDATE SET
			text = EXCLUDED.text,
			text_summary = EXCLUDED.text_summary,
			due_time = EXCLUDED.due_time,
			timezone = EXCLUDED.timezone,
			channel = EXCLUDED.channel,
			recipient = EXCLUDED.recipient,
			recipient_binding = EXCLUDED.recipient_binding,
			binding_id = EXCLUDED.binding_id,
			credential_ref = EXCLUDED.credential_ref,
			base_url = EXCLUDED.base_url,
			recurrence = EXCLUDED.recurrence,
			dedupe_key = EXCLUDED.dedupe_key,
			status = EXCLUDED.status,
			last_delivery_id = EXCLUDED.last_delivery_id,
			last_error = EXCLUDED.last_error,
			updated_at = EXCLUDED.updated_at,
			sent_at = EXCLUDED.sent_at,
			canceled_at = EXCLUDED.canceled_at,
			delivery_attempt = EXCLUDED.delivery_attempt,
			schedule_spec = EXCLUDED.schedule_spec
	`, reminder.ID, reminder.SessionID, reminder.RunID, reminder.Text, reminder.TextSummary, reminder.DueTime, reminder.Timezone, reminder.Channel, reminder.Recipient,
		reminder.RecipientBinding, reminder.BindingID, reminder.CredentialRef, reminder.BaseURL, reminder.Recurrence, reminder.DedupeKey, reminder.Status, reminder.LastDeliveryID, reminder.LastError, reminder.CreatedAt, reminder.UpdatedAt,
		reminder.SentAt, reminder.CanceledAt, reminder.DeliveryAttempt, mustJSON(reminder.ScheduleSpec))
	s.appendAudit(ctx, "reminder."+reminder.Status, reminder.SessionID, reminder.RunID, "toolhub", reminder.TextSummary, map[string]any{
		"reminder_id": reminder.ID,
		"due_time":    reminder.DueTime.UTC().Format(time.RFC3339),
		"channel":     reminder.Channel,
	})
	s.appendEvent(ctx, "reminder."+reminder.Status, reminder.SessionID, reminder.RunID, reminder)
	return reminder
}

func (s *PostgresStore) UpdatePendingReminder(reminder app.Reminder, expectedUpdatedAt time.Time) (app.Reminder, error) {
	if reminder.UpdatedAt.IsZero() {
		reminder.UpdatedAt = time.Now().UTC()
	}
	if reminder.TextSummary == "" {
		reminder.TextSummary = summarizeReminderText(reminder.Text)
	}
	ctx := context.Background()
	row := s.db.QueryRow(ctx, `
		UPDATE reminders SET
			session_id = nullif($2, ''), run_id = nullif($3, ''), text = $4, text_summary = $5,
			due_time = $6, timezone = $7, channel = $8, recipient = $9,
			recipient_binding = $10, binding_id = $11, credential_ref = $12, base_url = $13,
			recurrence = $14, dedupe_key = $15, status = $16, last_delivery_id = $17,
			last_error = $18, updated_at = $19, sent_at = $20, canceled_at = $21,
			delivery_attempt = $22, schedule_spec = $23
		WHERE id = $1 AND status = 'pending' AND updated_at = $24
		RETURNING id, coalesce(session_id, ''), coalesce(run_id, ''), text, text_summary, due_time, timezone,
			channel, recipient, recipient_binding, binding_id, credential_ref, base_url, recurrence, dedupe_key, status, last_delivery_id, last_error,
			created_at, updated_at, sent_at, canceled_at, delivery_attempt, schedule_spec
	`, reminder.ID, reminder.SessionID, reminder.RunID, reminder.Text, reminder.TextSummary,
		reminder.DueTime, reminder.Timezone, reminder.Channel, reminder.Recipient,
		reminder.RecipientBinding, reminder.BindingID, reminder.CredentialRef, reminder.BaseURL,
		reminder.Recurrence, reminder.DedupeKey, reminder.Status, reminder.LastDeliveryID,
		reminder.LastError, reminder.UpdatedAt, reminder.SentAt, reminder.CanceledAt,
		reminder.DeliveryAttempt, mustJSON(reminder.ScheduleSpec), expectedUpdatedAt.UTC())
	updated, err := scanReminder(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.Reminder{}, ErrReminderConflict
	}
	if err != nil {
		return app.Reminder{}, err
	}
	s.appendAudit(ctx, "reminder."+updated.Status, updated.SessionID, updated.RunID, "toolhub", updated.TextSummary, map[string]any{
		"reminder_id": updated.ID,
		"due_time":    updated.DueTime.UTC().Format(time.RFC3339),
		"channel":     updated.Channel,
	})
	s.appendEvent(ctx, "reminder."+updated.Status, updated.SessionID, updated.RunID, updated)
	return updated, nil
}

func (s *PostgresStore) GetReminder(id string) (app.Reminder, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, coalesce(session_id, ''), coalesce(run_id, ''), text, text_summary, due_time, timezone,
			channel, recipient, recipient_binding, binding_id, credential_ref, base_url, recurrence, dedupe_key, status, last_delivery_id, last_error,
			created_at, updated_at, sent_at, canceled_at, delivery_attempt, schedule_spec
		FROM reminders
		WHERE id = $1
	`, id)
	reminder, err := scanReminder(row)
	return reminder, err == nil
}

func (s *PostgresStore) ListReminders(filter app.ReminderFilter) []app.Reminder {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	var from, to any
	if filter.From != nil {
		from = *filter.From
	}
	if filter.To != nil {
		to = *filter.To
	}
	rows, err := s.db.Query(context.Background(), `
		SELECT id, coalesce(session_id, ''), coalesce(run_id, ''), text, text_summary, due_time, timezone,
			channel, recipient, recipient_binding, binding_id, credential_ref, base_url, recurrence, dedupe_key, status, last_delivery_id, last_error,
			created_at, updated_at, sent_at, canceled_at, delivery_attempt, schedule_spec
		FROM reminders
		WHERE ($1 = '' OR status = $1)
			AND ($2::timestamptz IS NULL OR due_time >= $2::timestamptz)
			AND ($3::timestamptz IS NULL OR due_time <= $3::timestamptz)
		ORDER BY due_time ASC
		LIMIT $4
	`, filter.Status, from, to, limit)
	if err != nil {
		return []app.Reminder{}
	}
	defer rows.Close()
	return collectRows(rows, scanReminder)
}

func (s *PostgresStore) ClaimDueReminders(now, staleBefore time.Time, limit int) []app.Reminder {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(context.Background(), `
		UPDATE reminders
		SET status = 'sending', updated_at = $1
		WHERE id IN (
			SELECT id FROM reminders
			WHERE (status = 'pending' AND due_time <= $1)
				OR (status = 'sending' AND updated_at <= $2)
			ORDER BY due_time ASC
			LIMIT $3
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, coalesce(session_id, ''), coalesce(run_id, ''), text, text_summary, due_time, timezone,
			channel, recipient, recipient_binding, binding_id, credential_ref, base_url, recurrence, dedupe_key, status, last_delivery_id, last_error,
			created_at, updated_at, sent_at, canceled_at, delivery_attempt, schedule_spec
	`, now.UTC(), staleBefore.UTC(), limit)
	if err != nil {
		return []app.Reminder{}
	}
	defer rows.Close()
	return collectRows(rows, scanReminder)
}

func (s *PostgresStore) SaveReminderDelivery(delivery app.ReminderDelivery) app.ReminderDelivery {
	now := time.Now().UTC()
	if delivery.ID == "" {
		delivery.ID = app.NewID("rdel")
	}
	if delivery.CreatedAt.IsZero() {
		delivery.CreatedAt = now
	}
	ctx := context.Background()
	_, _ = s.db.Exec(ctx, `
		INSERT INTO reminder_deliveries (
			id, reminder_id, channel, provider, recipient, status, provider_status, error,
			retry_state, attempt, sent_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (id) DO UPDATE SET
			channel = EXCLUDED.channel,
			provider = EXCLUDED.provider,
			recipient = EXCLUDED.recipient,
			status = EXCLUDED.status,
			provider_status = EXCLUDED.provider_status,
			error = EXCLUDED.error,
			retry_state = EXCLUDED.retry_state,
			attempt = EXCLUDED.attempt,
			sent_at = EXCLUDED.sent_at,
			created_at = EXCLUDED.created_at
	`, delivery.ID, delivery.ReminderID, delivery.Channel, delivery.Provider, delivery.Recipient, delivery.Status, delivery.ProviderStatus, delivery.Error,
		delivery.RetryState, delivery.Attempt, zeroTimeToNil(delivery.SentAt), delivery.CreatedAt)
	_, _ = s.db.Exec(ctx, `
		UPDATE reminders
		SET last_delivery_id = $1,
			last_error = $2,
			status = CASE WHEN $3 = 'sent' THEN 'sent' WHEN $3 = 'failed' THEN 'failed' ELSE status END,
			sent_at = CASE WHEN $3 = 'sent' THEN $4 ELSE sent_at END,
			delivery_attempt = $5,
			updated_at = $6
		WHERE id = $7
	`, delivery.ID, delivery.Error, delivery.Status, zeroTimeToNil(delivery.SentAt), delivery.Attempt, now, delivery.ReminderID)
	s.appendAudit(ctx, "reminder_delivery."+delivery.Status, "", delivery.ReminderID, "scheduler", delivery.ProviderStatus, map[string]any{
		"delivery_id": delivery.ID,
		"reminder_id": delivery.ReminderID,
		"channel":     delivery.Channel,
		"provider":    delivery.Provider,
		"attempt":     delivery.Attempt,
	})
	s.appendEvent(ctx, "reminder_delivery."+delivery.Status, "", delivery.ReminderID, delivery)
	return delivery
}

func (s *PostgresStore) ListReminderDeliveries(reminderID string) []app.ReminderDelivery {
	rows, err := s.db.Query(context.Background(), `
		SELECT id, reminder_id, channel, provider, recipient, status, provider_status, error,
			retry_state, attempt, sent_at, created_at
		FROM reminder_deliveries
		WHERE $1 = '' OR reminder_id = $1
		ORDER BY created_at ASC
	`, reminderID)
	if err != nil {
		return []app.ReminderDelivery{}
	}
	defer rows.Close()
	return collectRows(rows, scanReminderDelivery)
}

func (s *PostgresStore) GetConnectorSetting(ownerID, channel string) (app.ConnectorSetting, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT owner_id, channel, enabled, iscp_enabled, lan_access_enabled, version, updated_by, updated_at
		FROM connector_settings
		WHERE owner_id = $1 AND channel = $2
	`, normalizeConnectorOwner(ownerID), normalizeConnectorChannel(channel))
	setting, err := scanConnectorSetting(row)
	return setting, err == nil
}

func (s *PostgresStore) ListConnectorSettings(ownerID string) []app.ConnectorSetting {
	rows, err := s.db.Query(context.Background(), `
		SELECT owner_id, channel, enabled, iscp_enabled, lan_access_enabled, version, updated_by, updated_at
		FROM connector_settings
		WHERE owner_id = $1
		ORDER BY channel ASC
	`, normalizeConnectorOwner(ownerID))
	if err != nil {
		return []app.ConnectorSetting{}
	}
	defer rows.Close()
	return collectRows(rows, scanConnectorSetting)
}

func (s *PostgresStore) UpdateConnectorSetting(setting app.ConnectorSetting, expectedVersion int64) (app.ConnectorSetting, error) {
	setting.OwnerID = normalizeConnectorOwner(setting.OwnerID)
	setting.Channel = normalizeConnectorChannel(setting.Channel)
	setting.UpdatedBy = strings.TrimSpace(setting.UpdatedBy)
	if setting.UpdatedBy == "" {
		setting.UpdatedBy = setting.OwnerID
	}
	if setting.Channel == "" || expectedVersion < 0 {
		return app.ConnectorSetting{}, ErrConnectorSettingConflict
	}
	ctx := context.Background()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return app.ConnectorSetting{}, err
	}
	defer tx.Rollback(ctx)
	var currentVersion int64
	var currentEnabled, currentISCPEnabled, currentLANAccessEnabled bool
	err = tx.QueryRow(ctx, `
		SELECT version, enabled, iscp_enabled, lan_access_enabled FROM connector_settings
		WHERE owner_id = $1 AND channel = $2
		FOR UPDATE
	`, setting.OwnerID, setting.Channel).Scan(&currentVersion, &currentEnabled, &currentISCPEnabled, &currentLANAccessEnabled)
	exists := err == nil
	switch {
	case errors.Is(err, pgx.ErrNoRows) && expectedVersion != 0:
		return app.ConnectorSetting{}, ErrConnectorSettingConflict
	case err != nil && !errors.Is(err, pgx.ErrNoRows):
		return app.ConnectorSetting{}, err
	case err == nil && currentVersion != expectedVersion:
		return app.ConnectorSetting{}, ErrConnectorSettingConflict
	}
	setting.Version = expectedVersion + 1
	setting.UpdatedAt = time.Now().UTC()
	if expectedVersion == 0 {
		_, err = tx.Exec(ctx, `
			INSERT INTO connector_settings (owner_id, channel, enabled, iscp_enabled, lan_access_enabled, version, updated_by, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		`, setting.OwnerID, setting.Channel, setting.Enabled, setting.ISCPEnabled, setting.LANAccessEnabled, setting.Version, setting.UpdatedBy, setting.UpdatedAt)
	} else {
		result, updateErr := tx.Exec(ctx, `
			UPDATE connector_settings
			SET enabled = $3, iscp_enabled = $4, lan_access_enabled = $5, version = $6, updated_by = $7, updated_at = $8
			WHERE owner_id = $1 AND channel = $2 AND version = $9
		`, setting.OwnerID, setting.Channel, setting.Enabled, setting.ISCPEnabled, setting.LANAccessEnabled, setting.Version, setting.UpdatedBy, setting.UpdatedAt, expectedVersion)
		err = updateErr
		if err == nil && result.RowsAffected() != 1 {
			return app.ConnectorSetting{}, ErrConnectorSettingConflict
		}
	}
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate key") {
			return app.ConnectorSetting{}, ErrConnectorSettingConflict
		}
		return app.ConnectorSetting{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return app.ConnectorSetting{}, err
	}
	auditType := connectorSettingAuditType(exists, currentEnabled, currentISCPEnabled, currentLANAccessEnabled, setting)
	s.appendAudit(ctx, auditType, "", "", setting.UpdatedBy, setting.Channel, map[string]any{
		"owner_id":           setting.OwnerID,
		"channel":            setting.Channel,
		"enabled":            setting.Enabled,
		"iscp_enabled":       setting.ISCPEnabled,
		"lan_access_enabled": setting.LANAccessEnabled,
		"version":            setting.Version,
	})
	s.appendEvent(ctx, auditType, "", "", setting)
	return setting, nil
}

func (s *PostgresStore) SaveNotificationBinding(binding app.NotificationBinding) app.NotificationBinding {
	now := time.Now().UTC()
	if binding.ID == "" {
		binding.ID = app.NewID("bind")
	}
	if binding.OwnerID == "" {
		binding.OwnerID = app.DefaultOwnerID
	}
	if binding.ActorID == "" {
		binding.ActorID = binding.OwnerID
	}
	if binding.CreatedAt.IsZero() {
		binding.CreatedAt = now
	}
	binding.UpdatedAt = now
	if binding.Status == "" {
		binding.Status = "waiting_scan"
	}
	ctx := context.Background()
	if binding.DefaultForChannel {
		_, _ = s.db.Exec(ctx, `
			UPDATE notification_bindings
			SET default_for_channel = false, updated_at = $1
			WHERE owner_id = $2 AND channel = $3 AND id <> $4
		`, now, binding.OwnerID, binding.Channel, binding.ID)
	}
	_, _ = s.db.Exec(ctx, `
		INSERT INTO notification_bindings (
			id, owner_id, actor_id, channel, provider, status, display_name, external_user_id, external_chat_id, external_thread_id, account_id,
			credential_ref, base_url, provider_session_id, provider_state, context_token, provider_cursor, qr_code_url, qr_code_image, default_for_channel, scopes,
			created_at, updated_at, expires_at, revoked_at, last_error
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26)
		ON CONFLICT (id) DO UPDATE SET
			owner_id = EXCLUDED.owner_id,
			actor_id = EXCLUDED.actor_id,
			provider = EXCLUDED.provider,
			status = EXCLUDED.status,
			display_name = EXCLUDED.display_name,
			external_user_id = EXCLUDED.external_user_id,
			external_chat_id = EXCLUDED.external_chat_id,
			external_thread_id = EXCLUDED.external_thread_id,
			account_id = EXCLUDED.account_id,
			credential_ref = EXCLUDED.credential_ref,
			base_url = EXCLUDED.base_url,
			provider_session_id = EXCLUDED.provider_session_id,
			provider_state = EXCLUDED.provider_state,
			context_token = EXCLUDED.context_token,
			provider_cursor = EXCLUDED.provider_cursor,
			qr_code_url = EXCLUDED.qr_code_url,
			qr_code_image = EXCLUDED.qr_code_image,
			default_for_channel = EXCLUDED.default_for_channel,
			scopes = EXCLUDED.scopes,
			updated_at = EXCLUDED.updated_at,
			expires_at = EXCLUDED.expires_at,
			revoked_at = EXCLUDED.revoked_at,
			last_error = EXCLUDED.last_error
	`, binding.ID, binding.OwnerID, binding.ActorID, binding.Channel, binding.Provider, binding.Status, binding.DisplayName,
		binding.ExternalUserID, binding.ExternalChatID, binding.ExternalThreadID, binding.AccountID, binding.CredentialRef, binding.BaseURL, binding.ProviderSessionID,
		binding.ProviderState, binding.ContextToken, binding.ProviderCursor, binding.QRCodeURL, binding.QRCodeImage, binding.DefaultForChannel, mustJSON(binding.Scopes), binding.CreatedAt, binding.UpdatedAt,
		binding.ExpiresAt, binding.RevokedAt, binding.LastError)
	s.appendAudit(ctx, "notification_binding."+binding.Status, "", "", "owner", binding.Channel, map[string]any{
		"binding_id": binding.ID,
		"channel":    binding.Channel,
		"provider":   binding.Provider,
		"default":    binding.DefaultForChannel,
	})
	s.appendEvent(ctx, "notification_binding."+binding.Status, "", "", binding)
	return binding
}

func (s *PostgresStore) GetNotificationBinding(id string) (app.NotificationBinding, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, owner_id, actor_id, channel, provider, status, display_name, external_user_id, external_chat_id, external_thread_id, account_id,
			credential_ref, base_url, provider_session_id, provider_state, context_token, provider_cursor, qr_code_url, qr_code_image, default_for_channel, scopes,
			created_at, updated_at, expires_at, revoked_at, last_error
		FROM notification_bindings
		WHERE id = $1
	`, id)
	binding, err := scanNotificationBinding(row)
	return binding, err == nil
}

func (s *PostgresStore) ListNotificationBindings(channel, status string) []app.NotificationBinding {
	rows, err := s.db.Query(context.Background(), `
		SELECT id, owner_id, actor_id, channel, provider, status, display_name, external_user_id, external_chat_id, external_thread_id, account_id,
			credential_ref, base_url, provider_session_id, provider_state, context_token, provider_cursor, qr_code_url, qr_code_image, default_for_channel, scopes,
			created_at, updated_at, expires_at, revoked_at, last_error
		FROM notification_bindings
		WHERE ($1 = '' OR channel = $1) AND ($2 = '' OR status = $2)
		ORDER BY updated_at DESC
	`, channel, status)
	if err != nil {
		return []app.NotificationBinding{}
	}
	defer rows.Close()
	return collectRows(rows, scanNotificationBinding)
}

func (s *PostgresStore) RevokeNotificationBinding(id string) (app.NotificationBinding, error) {
	binding, ok := s.GetNotificationBinding(id)
	if !ok {
		return app.NotificationBinding{}, errors.New("notification binding not found")
	}
	if binding.Status == "revoked" {
		return binding, nil
	}
	now := time.Now().UTC()
	binding.Status = "revoked"
	binding.RevokedAt = &now
	binding.UpdatedAt = now
	binding.DefaultForChannel = false
	return s.SaveNotificationBinding(binding), nil
}

func (s *PostgresStore) CreatePassiveNotification(notification app.PassiveNotification) (app.PassiveNotification, bool, error) {
	notification.OwnerID = strings.TrimSpace(notification.OwnerID)
	notification.EndpointID = strings.TrimSpace(notification.EndpointID)
	notification.IdempotencyKey = strings.TrimSpace(notification.IdempotencyKey)
	if notification.OwnerID == "" || notification.EndpointID == "" || notification.IdempotencyKey == "" || strings.TrimSpace(notification.Fingerprint) == "" {
		return app.PassiveNotification{}, false, errors.New("notification owner, endpoint, idempotency key, and fingerprint are required")
	}
	now := time.Now().UTC()
	if notification.ID == "" {
		notification.ID = app.NewID("notification")
	}
	if notification.CreatedAt.IsZero() {
		notification.CreatedAt = now
	}
	notification.UpdatedAt = now
	row := s.db.QueryRow(context.Background(), `
		INSERT INTO passive_notifications (
			id, owner_id, endpoint_id, idempotency_key, fingerprint, notification_id,
			source, kind, deep_link, occurred_at, read_at, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT DO NOTHING
		RETURNING id, owner_id, endpoint_id, idempotency_key, fingerprint, notification_id,
		          source, kind, deep_link, occurred_at, read_at, created_at, updated_at
	`, notification.ID, notification.OwnerID, notification.EndpointID, notification.IdempotencyKey,
		notification.Fingerprint, notification.NotificationID, notification.Source, notification.Kind,
		notification.DeepLink, notification.OccurredAt, notification.ReadAt, notification.CreatedAt, notification.UpdatedAt)
	inserted, err := scanPassiveNotification(row)
	if err == nil {
		s.bumpPassiveNotificationRev(notification.OwnerID)
		s.appendAudit(context.Background(), "notification.received", "", "", notification.OwnerID, notification.Source, map[string]any{
			"notification_id": notification.ID,
			"endpoint_id":     notification.EndpointID,
			"kind":            notification.Kind,
		})
		return inserted, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return app.PassiveNotification{}, false, err
	}
	existingRow := s.db.QueryRow(context.Background(), `
		SELECT id, owner_id, endpoint_id, idempotency_key, fingerprint, notification_id,
		       source, kind, deep_link, occurred_at, read_at, created_at, updated_at
		FROM passive_notifications WHERE endpoint_id = $1 AND idempotency_key = $2
	`, notification.EndpointID, notification.IdempotencyKey)
	existing, err := scanPassiveNotification(existingRow)
	if err != nil {
		return app.PassiveNotification{}, false, err
	}
	if existing.OwnerID != notification.OwnerID || existing.Fingerprint != notification.Fingerprint {
		return app.PassiveNotification{}, false, ErrPassiveNotificationConflict
	}
	return existing, false, nil
}

func (s *PostgresStore) GetPassiveNotification(ownerID, id string) (app.PassiveNotification, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, owner_id, endpoint_id, idempotency_key, fingerprint, notification_id,
		       source, kind, deep_link, occurred_at, read_at, created_at, updated_at
		FROM passive_notifications WHERE owner_id = $1 AND id = $2
	`, ownerID, id)
	notification, err := scanPassiveNotification(row)
	return notification, err == nil
}

func (s *PostgresStore) ListPassiveNotifications(ownerID, after string, limit int) []app.PassiveNotification {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	ctx := context.Background()
	var rows pgx.Rows
	var err error
	if after == "" {
		rows, err = s.db.Query(ctx, `
			SELECT id, owner_id, endpoint_id, idempotency_key, fingerprint, notification_id,
			       source, kind, deep_link, occurred_at, read_at, created_at, updated_at
			FROM passive_notifications WHERE owner_id = $1
			ORDER BY created_at DESC, id DESC LIMIT $2
		`, ownerID, limit)
	} else {
		cursor, ok := s.GetPassiveNotification(ownerID, after)
		if !ok {
			return []app.PassiveNotification{}
		}
		rows, err = s.db.Query(ctx, `
			SELECT id, owner_id, endpoint_id, idempotency_key, fingerprint, notification_id,
			       source, kind, deep_link, occurred_at, read_at, created_at, updated_at
			FROM passive_notifications
			WHERE owner_id = $1 AND (created_at > $2 OR (created_at = $2 AND id > $3))
			ORDER BY created_at ASC, id ASC LIMIT $4
		`, ownerID, cursor.CreatedAt, cursor.ID, limit)
	}
	if err != nil {
		return []app.PassiveNotification{}
	}
	defer rows.Close()
	return collectRows(rows, scanPassiveNotification)
}

func (s *PostgresStore) CountUnreadPassiveNotifications(ownerID string) int {
	var count int
	if err := s.db.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM passive_notifications WHERE owner_id = $1 AND read_at IS NULL
	`, ownerID).Scan(&count); err != nil {
		return 0
	}
	return count
}

func (s *PostgresStore) MarkPassiveNotificationRead(ownerID, id string, readAt time.Time) (app.PassiveNotification, error) {
	if readAt.IsZero() {
		readAt = time.Now().UTC()
	} else {
		readAt = readAt.UTC()
	}
	row := s.db.QueryRow(context.Background(), `
		UPDATE passive_notifications SET read_at = COALESCE(read_at, $3), updated_at = CASE WHEN read_at IS NULL THEN $3 ELSE updated_at END
		WHERE owner_id = $1 AND id = $2
		RETURNING id, owner_id, endpoint_id, idempotency_key, fingerprint, notification_id,
		          source, kind, deep_link, occurred_at, read_at, created_at, updated_at
	`, ownerID, id, readAt)
	notification, err := scanPassiveNotification(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.PassiveNotification{}, ErrPassiveNotificationNotFound
	}
	if err == nil {
		s.bumpPassiveNotificationRev(ownerID)
	}
	return notification, err
}

func (s *PostgresStore) MarkAllPassiveNotificationsRead(ownerID string, readAt time.Time) (int, error) {
	if readAt.IsZero() {
		readAt = time.Now().UTC()
	} else {
		readAt = readAt.UTC()
	}
	result, err := s.db.Exec(context.Background(), `
		UPDATE passive_notifications SET read_at = $2, updated_at = $2
		WHERE owner_id = $1 AND read_at IS NULL
	`, ownerID, readAt)
	if err != nil {
		return 0, err
	}
	if result.RowsAffected() > 0 {
		s.bumpPassiveNotificationRev(ownerID)
	}
	return int(result.RowsAffected()), nil
}

func (s *PostgresStore) PrunePassiveNotifications(cutoff time.Time, maxPerOwner int) int {
	ctx := context.Background()
	removedByOwner := map[string]int{}
	if !cutoff.IsZero() {
		rows, err := s.db.Query(ctx, `
			DELETE FROM passive_notifications WHERE created_at < $1 RETURNING owner_id
		`, cutoff)
		if err == nil {
			for rows.Next() {
				var ownerID string
				if rows.Scan(&ownerID) == nil {
					removedByOwner[ownerID]++
				}
			}
			rows.Close()
		}
	}
	if maxPerOwner > 0 {
		type ownerExcess struct {
			ownerID string
			excess  int
		}
		var over []ownerExcess
		ownerRows, err := s.db.Query(ctx, `
			SELECT owner_id, COUNT(*) FROM passive_notifications
			GROUP BY owner_id HAVING COUNT(*) > $1
		`, maxPerOwner)
		if err == nil {
			for ownerRows.Next() {
				var ownerID string
				var count int
				if ownerRows.Scan(&ownerID, &count) == nil {
					over = append(over, ownerExcess{ownerID: ownerID, excess: count - maxPerOwner})
				}
			}
			ownerRows.Close()
		}
		for _, entry := range over {
			// Evict read notifications oldest-first before unread ones so an
			// over-cap inbox keeps the newest unread records.
			result, err := s.db.Exec(ctx, `
				DELETE FROM passive_notifications WHERE id IN (
					SELECT id FROM passive_notifications WHERE owner_id = $1
					ORDER BY (read_at IS NOT NULL) DESC, created_at ASC, id ASC
					LIMIT $2
				)
			`, entry.ownerID, entry.excess)
			if err == nil && result.RowsAffected() > 0 {
				removedByOwner[entry.ownerID] += int(result.RowsAffected())
			}
		}
	}
	removed := 0
	for ownerID, count := range removedByOwner {
		removed += count
		s.bumpPassiveNotificationRev(ownerID)
		s.appendAudit(ctx, "notification.pruned", "", "", "notification-retention", ownerID, map[string]any{
			"removed":       count,
			"max_per_owner": maxPerOwner,
			"cutoff":        cutoff.UTC().Format(time.RFC3339),
		})
	}
	return removed
}

func (s *PostgresStore) PassiveNotificationRevision(ownerID string) uint64 {
	s.passiveRevMu.Lock()
	defer s.passiveRevMu.Unlock()
	return s.passiveNotificationRevs[ownerID]
}

func (s *PostgresStore) bumpPassiveNotificationRev(ownerID string) {
	s.passiveRevMu.Lock()
	defer s.passiveRevMu.Unlock()
	s.passiveNotificationRevs[ownerID]++
}

func (s *PostgresStore) SaveExternalChatSession(session app.ExternalChatSession) app.ExternalChatSession {
	now := time.Now().UTC()
	if session.ID == "" {
		session.ID = app.NewID("extchat")
	}
	if session.Channel == "" {
		session.Channel = "weixin"
	}
	if session.ExternalChatID == "" {
		session.ExternalChatID = session.ExternalUserID
	}
	if strings.TrimSpace(session.AuthorizedOwnerID) == "" {
		if binding, ok := s.GetNotificationBinding(session.BindingID); ok {
			session.AuthorizedOwnerID = binding.OwnerID
			session.AuthorizedActorID = binding.ActorID
		}
		if session.AuthorizedOwnerID == "" {
			session.AuthorizedOwnerID = session.OwnerID
		}
	}
	if strings.TrimSpace(session.AuthorizedActorID) == "" {
		session.AuthorizedActorID = session.AuthorizedOwnerID
	}
	if session.Status == "" {
		session.Status = "active"
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	session.UpdatedAt = now
	_, _ = s.db.Exec(context.Background(), `
		INSERT INTO external_chat_sessions (
			id, owner_id, authorized_owner_id, authorized_actor_id, workspace_root, binding_id, channel, provider, external_user_id,
			external_chat_id, external_thread_id, display_name, linked_session_id, status,
			provider_cursor, last_context_token, created_at, updated_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		ON CONFLICT (id) DO UPDATE SET
			owner_id = EXCLUDED.owner_id,
			authorized_owner_id = EXCLUDED.authorized_owner_id,
			authorized_actor_id = EXCLUDED.authorized_actor_id,
			workspace_root = EXCLUDED.workspace_root,
			binding_id = EXCLUDED.binding_id,
			channel = EXCLUDED.channel,
			provider = EXCLUDED.provider,
			external_user_id = EXCLUDED.external_user_id,
			external_chat_id = EXCLUDED.external_chat_id,
			external_thread_id = EXCLUDED.external_thread_id,
			display_name = EXCLUDED.display_name,
			linked_session_id = EXCLUDED.linked_session_id,
			status = EXCLUDED.status,
			provider_cursor = EXCLUDED.provider_cursor,
			last_context_token = EXCLUDED.last_context_token,
			updated_at = EXCLUDED.updated_at
	`, session.ID, session.OwnerID, session.AuthorizedOwnerID, session.AuthorizedActorID, session.WorkspaceRoot, session.BindingID, session.Channel, session.Provider,
		session.ExternalUserID, session.ExternalChatID, session.ExternalThreadID, session.DisplayName,
		session.LinkedSessionID, session.Status, session.ProviderCursor, session.LastContextToken,
		session.CreatedAt, session.UpdatedAt)
	if strings.TrimSpace(session.LinkedSessionID) != "" {
		_, _ = s.db.Exec(context.Background(), `
			UPDATE sessions
			SET source = $5,
			    hidden = true,
			    owner_id = CASE WHEN $3 <> '' THEN $3 ELSE owner_id END,
			    workspace_root = CASE WHEN $4 <> '' THEN $4 ELSE workspace_root END,
			    title = CASE WHEN title = '' OR title = 'New SparkClaw Session' OR title = '微信会话' THEN $6 ELSE title END,
			    updated_at = $2
			WHERE id = $1
		`, session.LinkedSessionID, now, session.OwnerID, session.WorkspaceRoot, session.Channel, externalChatSessionTitle(session.Channel))
	}
	s.appendAudit(context.Background(), "external_chat_session."+session.Status, session.LinkedSessionID, "", "gateway", redactPostgresExternalID(session.ExternalUserID), map[string]any{
		"chat_session_id": session.ID,
		"binding_id":      session.BindingID,
		"channel":         session.Channel,
		"provider":        session.Provider,
	})
	s.appendEvent(context.Background(), "external_chat_session."+session.Status, session.LinkedSessionID, "", session)
	return session
}

func (s *PostgresStore) GetExternalChatSession(id string) (app.ExternalChatSession, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, owner_id, authorized_owner_id, authorized_actor_id, workspace_root, binding_id, channel, provider, external_user_id,
		       external_chat_id, external_thread_id, display_name, linked_session_id, status,
		       provider_cursor, last_context_token, created_at, updated_at
		FROM external_chat_sessions
		WHERE id = $1
	`, id)
	session, err := scanExternalChatSession(row)
	return session, err == nil
}

func (s *PostgresStore) ListExternalChatSessions(channel, status string) []app.ExternalChatSession {
	rows, err := s.db.Query(context.Background(), `
		SELECT id, owner_id, authorized_owner_id, authorized_actor_id, workspace_root, binding_id, channel, provider, external_user_id,
		       external_chat_id, external_thread_id, display_name, linked_session_id, status,
		       provider_cursor, last_context_token, created_at, updated_at
		FROM external_chat_sessions
		WHERE ($1 = '' OR channel = $1) AND ($2 = '' OR status = $2)
		ORDER BY updated_at DESC
	`, channel, status)
	if err != nil {
		return []app.ExternalChatSession{}
	}
	defer rows.Close()
	return collectRows(rows, scanExternalChatSession)
}

func (s *PostgresStore) FindExternalChatSession(bindingID, externalChatID, externalThreadID string) (app.ExternalChatSession, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, owner_id, authorized_owner_id, authorized_actor_id, workspace_root, binding_id, channel, provider, external_user_id,
		       external_chat_id, external_thread_id, display_name, linked_session_id, status,
		       provider_cursor, last_context_token, created_at, updated_at
		FROM external_chat_sessions
		WHERE binding_id = $1 AND external_chat_id = $2 AND external_thread_id = $3
		ORDER BY updated_at DESC
		LIMIT 1
	`, bindingID, externalChatID, externalThreadID)
	session, err := scanExternalChatSession(row)
	return session, err == nil
}

func (s *PostgresStore) FindExternalChatSessionByLinkedSessionID(sessionID string) (app.ExternalChatSession, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, owner_id, authorized_owner_id, authorized_actor_id, workspace_root, binding_id, channel, provider, external_user_id,
		       external_chat_id, external_thread_id, display_name, linked_session_id, status,
		       provider_cursor, last_context_token, created_at, updated_at
		FROM external_chat_sessions
		WHERE linked_session_id = $1
		ORDER BY updated_at DESC
		LIMIT 1
	`, sessionID)
	session, err := scanExternalChatSession(row)
	return session, err == nil
}

func (s *PostgresStore) SaveExternalChatMessage(message app.ExternalChatMessage) app.ExternalChatMessage {
	now := time.Now().UTC()
	if message.ID == "" {
		message.ID = app.NewID("extmsg")
	}
	if message.Channel == "" {
		if session, ok := s.GetExternalChatSession(message.ChatSessionID); ok {
			message.Channel = session.Channel
		}
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = now
	}
	message.UpdatedAt = now
	_, _ = s.db.Exec(context.Background(), `
		INSERT INTO external_chat_messages (
			id, chat_session_id, binding_id, channel, direction, role, external_message_id,
			content, context_token, linked_run_id, status, error,
			pending_reply_kind, pending_reply, dispatch_attempts, created_at, updated_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		ON CONFLICT (id) DO UPDATE SET
			chat_session_id = EXCLUDED.chat_session_id,
			binding_id = EXCLUDED.binding_id,
			channel = EXCLUDED.channel,
			direction = EXCLUDED.direction,
			role = EXCLUDED.role,
			external_message_id = EXCLUDED.external_message_id,
			content = EXCLUDED.content,
			context_token = EXCLUDED.context_token,
			linked_run_id = EXCLUDED.linked_run_id,
			status = EXCLUDED.status,
			error = EXCLUDED.error,
			pending_reply_kind = EXCLUDED.pending_reply_kind,
			pending_reply = EXCLUDED.pending_reply,
			dispatch_attempts = EXCLUDED.dispatch_attempts,
			updated_at = EXCLUDED.updated_at
	`, message.ID, message.ChatSessionID, message.BindingID, message.Channel, message.Direction, message.Role,
		message.ExternalMessageID, message.Content, message.ContextToken, message.LinkedRunID,
		message.Status, message.Error, message.PendingReplyKind, message.PendingReply, message.DispatchAttempts,
		message.CreatedAt, message.UpdatedAt)
	s.appendAudit(context.Background(), "external_chat_message."+message.Status, "", message.LinkedRunID, "gateway", message.Direction, map[string]any{
		"message_id":      message.ID,
		"chat_session_id": message.ChatSessionID,
		"binding_id":      message.BindingID,
		"channel":         message.Channel,
		"direction":       message.Direction,
		"role":            message.Role,
	})
	s.appendEvent(context.Background(), "external_chat_message."+message.Status, "", message.LinkedRunID, message)
	return message
}

func (s *PostgresStore) GetExternalChatMessage(id string) (app.ExternalChatMessage, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, chat_session_id, binding_id, channel, direction, role, external_message_id,
		       content, context_token, linked_run_id, status, error,
		       pending_reply_kind, pending_reply, dispatch_attempts, created_at, updated_at
		FROM external_chat_messages
		WHERE id = $1
	`, id)
	message, err := scanExternalChatMessage(row)
	return message, err == nil
}

func (s *PostgresStore) FindExternalChatMessageByExternalID(chatSessionID, externalMessageID string) (app.ExternalChatMessage, bool) {
	if strings.TrimSpace(externalMessageID) == "" {
		return app.ExternalChatMessage{}, false
	}
	row := s.db.QueryRow(context.Background(), `
		SELECT id, chat_session_id, binding_id, channel, direction, role, external_message_id,
		       content, context_token, linked_run_id, status, error,
		       pending_reply_kind, pending_reply, dispatch_attempts, created_at, updated_at
		FROM external_chat_messages
		WHERE chat_session_id = $1 AND external_message_id = $2
		ORDER BY created_at DESC
		LIMIT 1
	`, chatSessionID, externalMessageID)
	message, err := scanExternalChatMessage(row)
	return message, err == nil
}

func (s *PostgresStore) ListExternalChatMessages(chatSessionID string, limit int) []app.ExternalChatMessage {
	query := `
		SELECT id, chat_session_id, binding_id, channel, direction, role, external_message_id,
		       content, context_token, linked_run_id, status, error,
		       pending_reply_kind, pending_reply, dispatch_attempts, created_at, updated_at
		FROM external_chat_messages
		WHERE ($1 = '' OR chat_session_id = $1)
		ORDER BY created_at ASC
	`
	args := []any{chatSessionID}
	if limit > 0 {
		query = `
			SELECT * FROM (
				SELECT id, chat_session_id, binding_id, channel, direction, role, external_message_id,
				       content, context_token, linked_run_id, status, error,
				       pending_reply_kind, pending_reply, dispatch_attempts, created_at, updated_at
				FROM external_chat_messages
				WHERE ($1 = '' OR chat_session_id = $1)
				ORDER BY created_at DESC
				LIMIT $2
			) recent
			ORDER BY created_at ASC
		`
		args = append(args, limit)
	}
	rows, err := s.db.Query(context.Background(), query, args...)
	if err != nil {
		return []app.ExternalChatMessage{}
	}
	defer rows.Close()
	return collectRows(rows, scanExternalChatMessage)
}

func (s *PostgresStore) SaveMessageReceive(record app.MessageReceiveRecord) app.MessageReceiveRecord {
	now := time.Now().UTC()
	if record.ID == "" {
		record.ID = app.NewID("recv")
	}
	if record.Direction == "" {
		record.Direction = app.MessageDirectionReceive
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	if len(record.Transitions) == 0 || record.Transitions[len(record.Transitions)-1].Status != record.Status {
		record.Transitions = append(record.Transitions, app.MessageLifecycleTransition{Status: record.Status, At: now})
	}
	if existing, ok := s.FindMessageReceive(record.SourceEndpointID, record.NativeMessageID); ok && existing.ID != record.ID {
		record.ID = existing.ID
		record.CreatedAt = existing.CreatedAt
		record.Transitions = append(existing.Transitions, app.MessageLifecycleTransition{Status: record.Status, At: now})
	}
	_, _ = s.db.Exec(context.Background(), `
		INSERT INTO message_receive_records (id, owner_id, actor_id, source_endpoint_id, native_message_id, status, record, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (id) DO UPDATE SET
			owner_id = EXCLUDED.owner_id,
			actor_id = EXCLUDED.actor_id,
			source_endpoint_id = EXCLUDED.source_endpoint_id,
			native_message_id = EXCLUDED.native_message_id,
			status = EXCLUDED.status,
			record = EXCLUDED.record,
			updated_at = EXCLUDED.updated_at
	`, record.ID, record.OwnerID, record.ActorID, record.SourceEndpointID, record.NativeMessageID, record.Status, mustJSON(record), record.UpdatedAt)
	return record
}

func (s *PostgresStore) GetMessageReceive(id string) (app.MessageReceiveRecord, bool) {
	return s.queryMessageReceive(`SELECT record FROM message_receive_records WHERE id = $1`, id)
}

func (s *PostgresStore) FindMessageReceive(sourceEndpointID app.EndpointID, nativeMessageID string) (app.MessageReceiveRecord, bool) {
	return s.queryMessageReceive(`SELECT record FROM message_receive_records WHERE source_endpoint_id = $1 AND native_message_id = $2`, sourceEndpointID, nativeMessageID)
}

func (s *PostgresStore) queryMessageReceive(query string, args ...any) (app.MessageReceiveRecord, bool) {
	var raw []byte
	if err := s.db.QueryRow(context.Background(), query, args...).Scan(&raw); err != nil {
		return app.MessageReceiveRecord{}, false
	}
	var record app.MessageReceiveRecord
	if json.Unmarshal(raw, &record) != nil {
		return app.MessageReceiveRecord{}, false
	}
	return record, true
}

func (s *PostgresStore) ListMessageReceives(ownerID, actorID string, limit int) []app.MessageReceiveRecord {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(context.Background(), `
		SELECT record FROM message_receive_records
		WHERE ($1 = '' OR owner_id = $1) AND ($2 = '' OR actor_id = $2)
		ORDER BY updated_at DESC LIMIT $3
	`, ownerID, actorID, limit)
	if err != nil {
		return []app.MessageReceiveRecord{}
	}
	defer rows.Close()
	out := []app.MessageReceiveRecord{}
	for rows.Next() {
		var raw []byte
		var record app.MessageReceiveRecord
		if rows.Scan(&raw) == nil && json.Unmarshal(raw, &record) == nil {
			out = append(out, record)
		}
	}
	return out
}

func (s *PostgresStore) SaveMessageDelivery(record app.MessageDeliveryRecord) app.MessageDeliveryRecord {
	now := time.Now().UTC()
	if record.ID == "" {
		record.ID = app.DeliveryID(app.NewID("del"))
	}
	if record.Direction == "" {
		record.Direction = app.MessageDirectionSend
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	_, _ = s.db.Exec(context.Background(), `
		INSERT INTO message_delivery_records (id, owner_id, actor_id, idempotency_key, content_digest, status, record, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (id) DO UPDATE SET
			status = EXCLUDED.status,
			record = EXCLUDED.record,
			updated_at = EXCLUDED.updated_at
	`, record.ID, record.OwnerID, record.ActorID, record.Request.IdempotencyKey, record.ContentDigest, record.Status, mustJSON(record), record.UpdatedAt)
	return record
}

func (s *PostgresStore) GetMessageDelivery(id app.DeliveryID) (app.MessageDeliveryRecord, bool) {
	return s.queryMessageDelivery(`SELECT record FROM message_delivery_records WHERE id = $1`, id)
}

func (s *PostgresStore) FindMessageDeliveryByIdempotency(ownerID, actorID, idempotencyKey string) (app.MessageDeliveryRecord, bool) {
	return s.queryMessageDelivery(`SELECT record FROM message_delivery_records WHERE owner_id = $1 AND actor_id = $2 AND idempotency_key = $3`, ownerID, actorID, idempotencyKey)
}

func (s *PostgresStore) queryMessageDelivery(query string, args ...any) (app.MessageDeliveryRecord, bool) {
	var raw []byte
	if err := s.db.QueryRow(context.Background(), query, args...).Scan(&raw); err != nil {
		return app.MessageDeliveryRecord{}, false
	}
	var record app.MessageDeliveryRecord
	if json.Unmarshal(raw, &record) != nil {
		return app.MessageDeliveryRecord{}, false
	}
	return record, true
}

func (s *PostgresStore) ListMessageDeliveries(ownerID, actorID string, limit int) []app.MessageDeliveryRecord {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(context.Background(), `
		SELECT record FROM message_delivery_records
		WHERE ($1 = '' OR owner_id = $1) AND ($2 = '' OR actor_id = $2)
		ORDER BY updated_at DESC LIMIT $3
	`, ownerID, actorID, limit)
	if err != nil {
		return []app.MessageDeliveryRecord{}
	}
	defer rows.Close()
	out := []app.MessageDeliveryRecord{}
	for rows.Next() {
		var raw []byte
		var record app.MessageDeliveryRecord
		if rows.Scan(&raw) == nil && json.Unmarshal(raw, &record) == nil {
			out = append(out, record)
		}
	}
	return out
}

func (s *PostgresStore) SaveChannelInboxUpdate(update app.ChannelInboxUpdate) app.ChannelInboxUpdate {
	now := time.Now().UTC()
	if update.ID == "" {
		if existing, ok := s.FindChannelInboxUpdate(update.BindingID, update.ExternalID); ok {
			return existing
		}
	}
	if update.ID == "" {
		update.ID = app.NewID("inbox")
	}
	if update.Status == "" {
		update.Status = "pending"
	}
	if update.AvailableAt.IsZero() {
		update.AvailableAt = now
	}
	if update.CreatedAt.IsZero() {
		update.CreatedAt = now
	}
	update.UpdatedAt = now
	_, err := s.db.Exec(context.Background(), `
		INSERT INTO channel_inbox_updates (
			id, binding_id, channel, external_id, chat_key, payload, status, attempts,
			available_at, last_error, created_at, updated_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (binding_id, external_id) DO UPDATE SET
			chat_key = CASE WHEN channel_inbox_updates.id = EXCLUDED.id THEN EXCLUDED.chat_key ELSE channel_inbox_updates.chat_key END,
			payload = CASE WHEN channel_inbox_updates.id = EXCLUDED.id THEN EXCLUDED.payload ELSE channel_inbox_updates.payload END,
			status = CASE WHEN channel_inbox_updates.id = EXCLUDED.id THEN EXCLUDED.status ELSE channel_inbox_updates.status END,
			attempts = CASE WHEN channel_inbox_updates.id = EXCLUDED.id THEN EXCLUDED.attempts ELSE channel_inbox_updates.attempts END,
			available_at = CASE WHEN channel_inbox_updates.id = EXCLUDED.id THEN EXCLUDED.available_at ELSE channel_inbox_updates.available_at END,
			last_error = CASE WHEN channel_inbox_updates.id = EXCLUDED.id THEN EXCLUDED.last_error ELSE channel_inbox_updates.last_error END,
			updated_at = CASE WHEN channel_inbox_updates.id = EXCLUDED.id THEN EXCLUDED.updated_at ELSE channel_inbox_updates.updated_at END
	`, update.ID, update.BindingID, update.Channel, update.ExternalID, update.ChatKey,
		mustJSONRaw(update.Payload), update.Status, update.Attempts, update.AvailableAt,
		update.LastError, update.CreatedAt, update.UpdatedAt)
	if err == nil {
		if saved, ok := s.FindChannelInboxUpdate(update.BindingID, update.ExternalID); ok {
			return saved
		}
	}
	return update
}

func (s *PostgresStore) GetChannelInboxUpdate(id string) (app.ChannelInboxUpdate, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, binding_id, channel, external_id, chat_key, payload, status, attempts,
		       available_at, last_error, created_at, updated_at
		FROM channel_inbox_updates WHERE id = $1
	`, id)
	update, err := scanChannelInboxUpdate(row)
	return update, err == nil
}

func (s *PostgresStore) FindChannelInboxUpdate(bindingID, externalID string) (app.ChannelInboxUpdate, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, binding_id, channel, external_id, chat_key, payload, status, attempts,
		       available_at, last_error, created_at, updated_at
		FROM channel_inbox_updates WHERE binding_id = $1 AND external_id = $2
	`, bindingID, externalID)
	update, err := scanChannelInboxUpdate(row)
	return update, err == nil
}

func (s *PostgresStore) ListChannelInboxUpdates(channel, status string, readyBefore time.Time, limit int) []app.ChannelInboxUpdate {
	if limit <= 0 {
		limit = 100
	}
	query := `
		SELECT id, binding_id, channel, external_id, chat_key, payload, status, attempts,
		       available_at, last_error, created_at, updated_at
		FROM channel_inbox_updates
		WHERE ($1 = '' OR channel = $1)
		  AND ($2 = '' OR status = $2)
	`
	args := []any{channel, status}
	if !readyBefore.IsZero() {
		query += ` AND available_at <= $3 ORDER BY created_at ASC LIMIT $4`
		args = append(args, readyBefore, limit)
	} else {
		query += ` ORDER BY created_at ASC LIMIT $3`
		args = append(args, limit)
	}
	rows, err := s.db.Query(context.Background(), query, args...)
	if err != nil {
		return []app.ChannelInboxUpdate{}
	}
	defer rows.Close()
	return collectRows(rows, scanChannelInboxUpdate)
}

func (s *PostgresStore) SaveCredentialSecret(secret app.CredentialSecret) app.CredentialSecret {
	now := time.Now().UTC()
	if secret.CreatedAt.IsZero() {
		secret.CreatedAt = now
	}
	secret.UpdatedAt = now
	ctx := context.Background()
	_, _ = s.db.Exec(ctx, `
		INSERT INTO credential_secrets (ref, kind, value, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (ref) DO UPDATE SET
			kind = EXCLUDED.kind,
			value = EXCLUDED.value,
			updated_at = EXCLUDED.updated_at
	`, secret.Ref, secret.Kind, secret.Value, secret.CreatedAt, secret.UpdatedAt)
	s.appendAudit(ctx, "credential_secret.saved", "", "", "gateway", secret.Kind, map[string]any{
		"ref":  secret.Ref,
		"kind": secret.Kind,
	})
	return secret
}

func (s *PostgresStore) GetCredentialSecret(ref string) (app.CredentialSecret, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT ref, kind, value, created_at, updated_at
		FROM credential_secrets
		WHERE ref = $1
	`, ref)
	secret, err := scanCredentialSecret(row)
	return secret, err == nil
}

func (s *PostgresStore) DeleteCredentialSecret(ref string) error {
	tag, err := s.db.Exec(context.Background(), `DELETE FROM credential_secrets WHERE ref = $1`, ref)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("credential secret not found")
	}
	s.appendAudit(context.Background(), "credential_secret.deleted", "", "", "gateway", "credential deleted", map[string]any{"ref": ref})
	return nil
}

func (s *PostgresStore) SaveBrowserAuthRecord(record app.BrowserAuthRecord) app.BrowserAuthRecord {
	current, _ := s.GetBrowserAuthRecord(record.ID)
	record = normalizeBrowserAuthRecord(record, current)
	ctx := context.Background()
	_, _ = s.db.Exec(ctx, `
		INSERT INTO browser_auth_records (
			id, owner_id, browser_profile_id, site_origin, site_realm, account_hint, auth_strategy, status,
			session_ref, credential_ref, cookie_jar_ref, last_verified_at, expires_at, last_error, created_at, updated_at, revoked_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
		ON CONFLICT (id) DO UPDATE SET
			owner_id = EXCLUDED.owner_id,
			browser_profile_id = EXCLUDED.browser_profile_id,
			site_origin = EXCLUDED.site_origin,
			site_realm = EXCLUDED.site_realm,
			account_hint = EXCLUDED.account_hint,
			auth_strategy = EXCLUDED.auth_strategy,
			status = EXCLUDED.status,
			session_ref = EXCLUDED.session_ref,
			credential_ref = EXCLUDED.credential_ref,
			cookie_jar_ref = EXCLUDED.cookie_jar_ref,
			last_verified_at = EXCLUDED.last_verified_at,
			expires_at = EXCLUDED.expires_at,
			last_error = EXCLUDED.last_error,
			updated_at = EXCLUDED.updated_at,
			revoked_at = EXCLUDED.revoked_at
	`, record.ID, record.OwnerID, record.BrowserProfileID, record.SiteOrigin, record.SiteRealm, record.AccountHint, record.AuthStrategy,
		record.Status, record.SessionRef, record.CredentialRef, record.CookieJarRef, zeroTimeToNil(record.LastVerifiedAt),
		record.ExpiresAt, record.LastError, record.CreatedAt, record.UpdatedAt, record.RevokedAt)
	s.appendAudit(ctx, "browser_auth.record_saved", "", "", "gateway", record.SiteOrigin, browserAuthAuditFields(record, nil))
	s.appendEvent(ctx, "browser_auth.record_saved", "", "", record)
	return record
}

func (s *PostgresStore) GetBrowserAuthRecord(id string) (app.BrowserAuthRecord, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, owner_id, browser_profile_id, site_origin, site_realm, account_hint, auth_strategy, status,
			session_ref, credential_ref, cookie_jar_ref, last_verified_at, expires_at, last_error, created_at, updated_at, revoked_at
		FROM browser_auth_records
		WHERE id = $1
	`, strings.TrimSpace(id))
	record, err := scanBrowserAuthRecord(row)
	return record, err == nil
}

func (s *PostgresStore) FindBrowserAuthRecord(ownerID, browserProfileID, siteOrigin, siteRealm, accountHint string) (app.BrowserAuthRecord, bool) {
	ownerID, browserProfileID, siteOrigin, siteRealm, accountHint = normalizeBrowserAuthLookup(ownerID, browserProfileID, siteOrigin, siteRealm, accountHint)
	row := s.db.QueryRow(context.Background(), `
		SELECT id, owner_id, browser_profile_id, site_origin, site_realm, account_hint, auth_strategy, status,
			session_ref, credential_ref, cookie_jar_ref, last_verified_at, expires_at, last_error, created_at, updated_at, revoked_at
		FROM browser_auth_records
		WHERE owner_id = $1
		  AND browser_profile_id = $2
		  AND site_origin = $3
		  AND site_realm = $4
		  AND account_hint = $5
		  AND status = $6
		  AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > now())
		ORDER BY updated_at DESC
		LIMIT 1
	`, ownerID, browserProfileID, siteOrigin, siteRealm, accountHint, app.BrowserAuthStatusActive)
	record, err := scanBrowserAuthRecord(row)
	return record, err == nil
}

func (s *PostgresStore) ListBrowserAuthRecords(ownerID, browserProfileID string) []app.BrowserAuthRecord {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID != "" {
		ownerID = normalizeBrowserAuthOwnerID(ownerID)
	}
	browserProfileID = strings.TrimSpace(browserProfileID)
	if browserProfileID != "" {
		browserProfileID = normalizeBrowserProfileID(browserProfileID)
	}
	rows, err := s.db.Query(context.Background(), `
		SELECT id, owner_id, browser_profile_id, site_origin, site_realm, account_hint, auth_strategy, status,
			session_ref, credential_ref, cookie_jar_ref, last_verified_at, expires_at, last_error, created_at, updated_at, revoked_at
		FROM browser_auth_records
		WHERE ($1 = '' OR owner_id = $1) AND ($2 = '' OR browser_profile_id = $2)
		ORDER BY updated_at DESC
	`, ownerID, browserProfileID)
	if err != nil {
		return []app.BrowserAuthRecord{}
	}
	defer rows.Close()
	return collectRows(rows, scanBrowserAuthRecord)
}

func (s *PostgresStore) RevokeBrowserAuthRecord(id, reason string) (app.BrowserAuthRecord, error) {
	record, ok := s.GetBrowserAuthRecord(id)
	if !ok {
		return app.BrowserAuthRecord{}, errors.New("browser auth record not found")
	}
	now := time.Now().UTC()
	record.Status = app.BrowserAuthStatusRevoked
	record.RevokedAt = &now
	record.UpdatedAt = now
	record.LastError = strings.TrimSpace(reason)
	record = s.SaveBrowserAuthRecord(record)
	s.appendAudit(context.Background(), "browser_auth.record_revoked", "", "", "owner", record.SiteOrigin, browserAuthAuditFields(record, map[string]any{"reason": record.LastError}))
	s.appendEvent(context.Background(), "browser_auth.record_revoked", "", "", record)
	return record, nil
}

func (s *PostgresStore) SaveBrowserLoginBlock(block app.BrowserLoginBlock) app.BrowserLoginBlock {
	current, _ := s.GetBrowserLoginBlock(block.ID)
	block = normalizeBrowserLoginBlock(block, current)
	ctx := context.Background()
	_, _ = s.db.Exec(ctx, `
		INSERT INTO browser_login_blocks (
			id, session_id, run_id, schema_version, version, workflow_id, workflow_revision,
			workflow_node_id, session_generation, status, original_goal, resume_tool, resume_args,
			last_tool_call_id, login_handoff_url, login_handoff_page_id, last_visible_page_id,
			owner_id, browser_profile_id, site_origin,
			site_realm, account_hint, browser_auth_status, target, visible_evidence, last_user_reply, last_error,
			transition_owner_id, transition_lease_until, created_at, updated_at, resolved_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31, $32)
		ON CONFLICT (id) DO UPDATE SET
			session_id = EXCLUDED.session_id,
			run_id = EXCLUDED.run_id,
			schema_version = EXCLUDED.schema_version,
			version = EXCLUDED.version,
			workflow_id = EXCLUDED.workflow_id,
			workflow_revision = EXCLUDED.workflow_revision,
			workflow_node_id = EXCLUDED.workflow_node_id,
			session_generation = EXCLUDED.session_generation,
			status = EXCLUDED.status,
			original_goal = EXCLUDED.original_goal,
			resume_tool = EXCLUDED.resume_tool,
			resume_args = EXCLUDED.resume_args,
			last_tool_call_id = EXCLUDED.last_tool_call_id,
			login_handoff_url = EXCLUDED.login_handoff_url,
			login_handoff_page_id = EXCLUDED.login_handoff_page_id,
			last_visible_page_id = EXCLUDED.last_visible_page_id,
			owner_id = EXCLUDED.owner_id,
			browser_profile_id = EXCLUDED.browser_profile_id,
			site_origin = EXCLUDED.site_origin,
			site_realm = EXCLUDED.site_realm,
			account_hint = EXCLUDED.account_hint,
			browser_auth_status = EXCLUDED.browser_auth_status,
			target = EXCLUDED.target,
			visible_evidence = EXCLUDED.visible_evidence,
			last_user_reply = EXCLUDED.last_user_reply,
			last_error = EXCLUDED.last_error,
			transition_owner_id = EXCLUDED.transition_owner_id,
			transition_lease_until = EXCLUDED.transition_lease_until,
			updated_at = EXCLUDED.updated_at,
			resolved_at = EXCLUDED.resolved_at
	`, block.ID, block.SessionID, block.RunID, block.SchemaVersion, block.Version, block.WorkflowID, block.WorkflowRevision,
		block.WorkflowNodeID, block.SessionGeneration, block.Status, block.OriginalGoal, block.ResumeTool, mustJSON(block.ResumeArgs),
		block.LastToolCallID, block.LoginHandoffURL, block.LoginHandoffPageID, block.LastVisiblePageID,
		block.OwnerID, block.BrowserProfileID, block.SiteOrigin,
		block.SiteRealm, block.AccountHint, block.BrowserAuthStatus, mustJSON(block.Target), mustJSON(block.VisibleEvidence), block.LastUserReply, block.LastError,
		block.TransitionOwnerID, block.TransitionLeaseUntil, block.CreatedAt, block.UpdatedAt, block.ResolvedAt)
	s.appendAudit(ctx, "browser_login_block."+block.Status, block.SessionID, block.RunID, "runtime", block.SiteOrigin, browserLoginBlockAuditFields(block, nil))
	s.appendEvent(ctx, "browser_login_block."+block.Status, block.SessionID, block.RunID, block)
	return block
}

func (s *PostgresStore) UpdateBrowserLoginBlock(block app.BrowserLoginBlock, expectedVersion int64) (app.BrowserLoginBlock, error) {
	current, ok := s.GetBrowserLoginBlock(block.ID)
	if !ok || current.Version != expectedVersion {
		return app.BrowserLoginBlock{}, ErrBrowserHandoffConflict
	}
	block.Version = expectedVersion + 1
	block = normalizeBrowserLoginBlock(block, current)
	ctx := context.Background()
	result, err := s.db.Exec(ctx, `
		UPDATE browser_login_blocks SET
			session_id = $2, run_id = $3, schema_version = $4, version = $5,
			workflow_id = $6, workflow_revision = $7, workflow_node_id = $8,
			session_generation = $9, status = $10, original_goal = $11,
			resume_tool = $12, resume_args = $13, last_tool_call_id = $14,
			login_handoff_url = $15, login_handoff_page_id = $16,
			last_visible_page_id = $17, owner_id = $18, browser_profile_id = $19,
			site_origin = $20, site_realm = $21, account_hint = $22,
			browser_auth_status = $23, target = $24, visible_evidence = $25,
			last_user_reply = $26, last_error = $27, transition_owner_id = $28,
			transition_lease_until = $29, created_at = $30,
			updated_at = $31, resolved_at = $32
		WHERE id = $1 AND version = $33
	`, block.ID, block.SessionID, block.RunID, block.SchemaVersion, block.Version,
		block.WorkflowID, block.WorkflowRevision, block.WorkflowNodeID, block.SessionGeneration,
		block.Status, block.OriginalGoal, block.ResumeTool, mustJSON(block.ResumeArgs),
		block.LastToolCallID, block.LoginHandoffURL, block.LoginHandoffPageID, block.LastVisiblePageID,
		block.OwnerID, block.BrowserProfileID, block.SiteOrigin, block.SiteRealm, block.AccountHint,
		block.BrowserAuthStatus, mustJSON(block.Target), mustJSON(block.VisibleEvidence),
		block.LastUserReply, block.LastError, block.TransitionOwnerID, block.TransitionLeaseUntil,
		block.CreatedAt, block.UpdatedAt, block.ResolvedAt,
		expectedVersion)
	if err != nil {
		return app.BrowserLoginBlock{}, err
	}
	affected := result.RowsAffected()
	if affected != 1 {
		return app.BrowserLoginBlock{}, ErrBrowserHandoffConflict
	}
	s.appendAudit(ctx, "browser_login_block."+block.Status, block.SessionID, block.RunID, "runtime", block.SiteOrigin, browserLoginBlockAuditFields(block, nil))
	s.appendEvent(ctx, "browser_login_block."+block.Status, block.SessionID, block.RunID, block)
	return block, nil
}

func (s *PostgresStore) GetBrowserLoginBlock(id string) (app.BrowserLoginBlock, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, session_id, run_id, schema_version, version, workflow_id, workflow_revision,
			workflow_node_id, session_generation, status, original_goal, resume_tool, resume_args,
			last_tool_call_id, login_handoff_url, login_handoff_page_id, last_visible_page_id,
			owner_id, browser_profile_id, site_origin,
			site_realm, account_hint, browser_auth_status, target, visible_evidence, last_user_reply, last_error,
			transition_owner_id, transition_lease_until, created_at, updated_at, resolved_at
		FROM browser_login_blocks
		WHERE id = $1
	`, strings.TrimSpace(id))
	block, err := scanBrowserLoginBlock(row)
	return block, err == nil
}

func (s *PostgresStore) FindActiveBrowserLoginBlock(sessionID string) (app.BrowserLoginBlock, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, session_id, run_id, schema_version, version, workflow_id, workflow_revision,
			workflow_node_id, session_generation, status, original_goal, resume_tool, resume_args,
			last_tool_call_id, login_handoff_url, login_handoff_page_id, last_visible_page_id,
			owner_id, browser_profile_id, site_origin,
			site_realm, account_hint, browser_auth_status, target, visible_evidence, last_user_reply, last_error,
			transition_owner_id, transition_lease_until, created_at, updated_at, resolved_at
		FROM browser_login_blocks
		WHERE session_id = $1 AND status = ANY($2)
		ORDER BY updated_at DESC, id DESC
		LIMIT 1
	`, strings.TrimSpace(sessionID), app.BrowserHandoffActiveStatuses())
	block, err := scanBrowserLoginBlock(row)
	return block, err == nil
}

func (s *PostgresStore) ListBrowserLoginBlocks(sessionID, status string) []app.BrowserLoginBlock {
	rows, err := s.db.Query(context.Background(), `
		SELECT id, session_id, run_id, schema_version, version, workflow_id, workflow_revision,
			workflow_node_id, session_generation, status, original_goal, resume_tool, resume_args,
			last_tool_call_id, login_handoff_url, login_handoff_page_id, last_visible_page_id,
			owner_id, browser_profile_id, site_origin,
			site_realm, account_hint, browser_auth_status, target, visible_evidence, last_user_reply, last_error,
			transition_owner_id, transition_lease_until, created_at, updated_at, resolved_at
		FROM browser_login_blocks
		WHERE ($1 = '' OR session_id = $1) AND ($2 = '' OR status = $2)
		ORDER BY updated_at DESC, id DESC
	`, strings.TrimSpace(sessionID), strings.TrimSpace(status))
	if err != nil {
		return []app.BrowserLoginBlock{}
	}
	defer rows.Close()
	return collectRows(rows, scanBrowserLoginBlock)
}

func (s *PostgresStore) AddMemoryCandidate(candidate app.MemoryCandidate) app.MemoryCandidate {
	if candidate.ID == "" {
		candidate.ID = app.NewID("mc")
	}
	if candidate.CreatedAt.IsZero() {
		candidate.CreatedAt = time.Now().UTC()
	}
	if candidate.Status == "" {
		candidate.Status = "pending"
	}
	ctx := context.Background()
	_, _ = s.db.Exec(ctx, `
		INSERT INTO memory_candidates (
			id, session_id, run_id, kind, content, sensitivity, status, reason, created_at, resolved_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (id) DO UPDATE SET
			kind = EXCLUDED.kind,
			content = EXCLUDED.content,
			sensitivity = EXCLUDED.sensitivity,
			status = EXCLUDED.status,
			reason = EXCLUDED.reason,
			resolved_at = EXCLUDED.resolved_at
	`, candidate.ID, candidate.SessionID, candidate.RunID, candidate.Kind, candidate.Content, candidate.Sensitivity, candidate.Status, candidate.Reason, candidate.CreatedAt, candidate.ResolvedAt)
	s.appendAudit(ctx, "memory_candidate.created", candidate.SessionID, candidate.RunID, "agent", candidate.Content, map[string]any{"kind": candidate.Kind})
	s.appendEvent(ctx, "memory_candidate.created", candidate.SessionID, candidate.RunID, candidate)
	return candidate
}

func (s *PostgresStore) ResolveMemoryCandidate(id, status string) (app.MemoryCandidate, *app.Memory, error) {
	ctx := context.Background()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return app.MemoryCandidate{}, nil, err
	}
	defer rollbackTx(ctx, tx)
	row := tx.QueryRow(ctx, `
		SELECT id, session_id, run_id, kind, content, sensitivity, status, reason, created_at, resolved_at
		FROM memory_candidates
		WHERE id = $1
		FOR UPDATE
	`, id)
	candidate, err := scanMemoryCandidate(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.MemoryCandidate{}, nil, errors.New("memory candidate not found")
		}
		return app.MemoryCandidate{}, nil, err
	}
	if candidate.Status != "pending" {
		return app.MemoryCandidate{}, nil, errors.New("memory candidate already resolved")
	}
	now := time.Now().UTC()
	candidate.Status = status
	candidate.ResolvedAt = &now
	if _, err := tx.Exec(ctx, `
		UPDATE memory_candidates
		SET status = $2, resolved_at = $3
		WHERE id = $1
	`, candidate.ID, candidate.Status, candidate.ResolvedAt); err != nil {
		return app.MemoryCandidate{}, nil, err
	}
	var memory *app.Memory
	if status == "accepted" {
		m := app.Memory{
			ID:        app.NewID("mem"),
			Kind:      candidate.Kind,
			Content:   candidate.Content,
			SourceID:  candidate.RunID,
			CreatedAt: now,
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO memories (id, kind, content, source_run_id, created_at)
			VALUES ($1, $2, $3, $4, $5)
		`, m.ID, m.Kind, m.Content, m.SourceID, m.CreatedAt); err != nil {
			return app.MemoryCandidate{}, nil, err
		}
		memory = &m
	}
	if err := tx.Commit(ctx); err != nil {
		return app.MemoryCandidate{}, nil, err
	}
	s.appendAudit(ctx, "memory_candidate."+status, candidate.SessionID, candidate.RunID, "owner", candidate.Content, nil)
	s.appendEvent(ctx, "memory_candidate."+status, candidate.SessionID, candidate.RunID, candidate)
	return candidate, memory, nil
}

func (s *PostgresStore) ListMemoryCandidates(status string) []app.MemoryCandidate {
	rows, err := s.db.Query(context.Background(), `
		SELECT id, session_id, run_id, kind, content, sensitivity, status, reason, created_at, resolved_at
		FROM memory_candidates
		WHERE $1 = '' OR status = $1
		ORDER BY created_at DESC
	`, status)
	if err != nil {
		return []app.MemoryCandidate{}
	}
	defer rows.Close()
	return collectRows(rows, scanMemoryCandidate)
}

func (s *PostgresStore) SearchMemories(query string) []app.Memory {
	rows, err := s.db.Query(context.Background(), `
		SELECT id, kind, content, source_run_id, created_at
		FROM memories
		WHERE $1 = ''
			OR lower(content) LIKE '%' || lower($1) || '%'
			OR lower(kind) LIKE '%' || lower($1) || '%'
		ORDER BY created_at DESC
	`, query)
	if err != nil {
		return []app.Memory{}
	}
	defer rows.Close()
	return collectRows(rows, scanMemory)
}

func (s *PostgresStore) UpdateMemory(id, kind, content string) (app.Memory, error) {
	ctx := context.Background()
	row := s.db.QueryRow(ctx, `
		UPDATE memories AS memory
		SET kind = $2, content = $3
		FROM agent_runs AS run
		WHERE memory.id = $1 AND run.id = memory.source_run_id
		RETURNING memory.id, memory.kind, memory.content, memory.source_run_id, memory.created_at, run.session_id
	`, id, kind, content)
	memory, sessionID, err := scanMemoryWithSession(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.Memory{}, errors.New("memory not found")
		}
		return app.Memory{}, err
	}
	s.appendAudit(ctx, "memory.updated", sessionID, memory.SourceID, "owner", memory.Content, map[string]any{"memory_id": memory.ID, "kind": memory.Kind})
	s.appendEvent(ctx, "memory.updated", sessionID, memory.SourceID, memory)
	return memory, nil
}

func (s *PostgresStore) DeleteMemory(id string) (app.Memory, error) {
	ctx := context.Background()
	row := s.db.QueryRow(ctx, `
		DELETE FROM memories AS memory
		USING agent_runs AS run
		WHERE memory.id = $1 AND run.id = memory.source_run_id
		RETURNING memory.id, memory.kind, memory.content, memory.source_run_id, memory.created_at, run.session_id
	`, id)
	memory, sessionID, err := scanMemoryWithSession(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return app.Memory{}, errors.New("memory not found")
		}
		return app.Memory{}, err
	}
	s.appendAudit(ctx, "memory.deleted", sessionID, memory.SourceID, "owner", memory.Content, map[string]any{"memory_id": memory.ID, "kind": memory.Kind})
	s.appendEvent(ctx, "memory.deleted", sessionID, memory.SourceID, memory)
	return memory, nil
}

func (s *PostgresStore) PruneMemories(cutoff time.Time) []app.Memory {
	if cutoff.IsZero() {
		return []app.Memory{}
	}
	ctx := context.Background()
	rows, err := s.db.Query(ctx, `
		DELETE FROM memories AS memory
		USING agent_runs AS run
		WHERE memory.source_run_id = run.id
			AND memory.created_at < $1
		RETURNING memory.id, memory.kind, memory.content, memory.source_run_id, memory.created_at, run.session_id
	`, cutoff)
	if err != nil {
		return []app.Memory{}
	}
	defer rows.Close()
	type prunedMemory struct {
		memory    app.Memory
		sessionID string
	}
	pruned := []prunedMemory{}
	for rows.Next() {
		memory, sessionID, err := scanMemoryWithSession(rows)
		if err != nil {
			continue
		}
		pruned = append(pruned, prunedMemory{memory: memory, sessionID: sessionID})
	}
	out := make([]app.Memory, 0, len(pruned))
	for _, item := range pruned {
		out = append(out, item.memory)
		s.appendAudit(ctx, "memory.pruned", item.sessionID, item.memory.SourceID, "memory-retention", item.memory.Kind, map[string]any{
			"memory_id": item.memory.ID,
			"cutoff":    cutoff.UTC().Format(time.RFC3339),
		})
		s.appendEvent(ctx, "memory.pruned", item.sessionID, item.memory.SourceID, item.memory)
	}
	slices.SortFunc(out, func(a, b app.Memory) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	return out
}

func (s *PostgresStore) AddAudit(event app.AuditEvent) {
	if event.ID == "" {
		event.ID = app.NewID("audit")
	}
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	_, _ = s.db.Exec(context.Background(), `
		INSERT INTO audit_events (id, happened_at, type, session_id, run_id, actor, summary, fields)
		VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''), $6, $7, $8)
	`, event.ID, event.Time, event.Type, event.SessionID, event.RunID, event.Actor, event.Summary, optionalJSON(event.Fields))
}

func (s *PostgresStore) ListAudit(sessionID string) []app.AuditEvent {
	rows, err := s.db.Query(context.Background(), `
		SELECT id, happened_at, type, coalesce(session_id, ''), coalesce(run_id, ''), actor, summary, fields
		FROM audit_events
		WHERE $1 = '' OR session_id = $1
		ORDER BY happened_at DESC
	`, sessionID)
	if err != nil {
		return []app.AuditEvent{}
	}
	defer rows.Close()
	return collectRows(rows, scanAuditEvent)
}

func (s *PostgresStore) EventsAfter(sessionID, after string) []app.Event {
	var afterSeq int64
	if after != "" {
		_ = s.db.QueryRow(context.Background(), `SELECT seq FROM events WHERE id = $1`, after).Scan(&afterSeq)
	}
	rows, err := s.db.Query(context.Background(), `
		SELECT id, happened_at, type, coalesce(session_id, ''), coalesce(run_id, ''), payload
		FROM events
		WHERE seq > $1 AND ($2 = '' OR session_id = $2)
		ORDER BY seq ASC
	`, afterSeq, sessionID)
	if err != nil {
		return []app.Event{}
	}
	defer rows.Close()
	return collectRows(rows, scanEvent)
}

func (s *PostgresStore) MessageEventHead(sessionID string) (string, error) {
	var cursor string
	err := s.db.QueryRow(context.Background(), `
		SELECT id
		FROM events
		WHERE session_id = $1 AND type = 'message.created'
		ORDER BY seq DESC
		LIMIT 1
	`, sessionID).Scan(&cursor)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return cursor, err
}

func (s *PostgresStore) MessageEventsAfter(sessionID, after string, limit int) (MessageEventPage, error) {
	if limit <= 0 || limit > MessageEventPageLimit {
		limit = MessageEventPageLimit
	}
	ctx := context.Background()
	var afterSeq int64
	if after != "" {
		var cursorSessionID, cursorType string
		err := s.db.QueryRow(ctx, `
			SELECT seq, coalesce(session_id, ''), type
			FROM events
			WHERE id = $1
		`, after).Scan(&afterSeq, &cursorSessionID, &cursorType)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && (cursorSessionID != sessionID || cursorType != "message.created") {
			return MessageEventPage{}, ErrMessageEventCursorInvalid
		}
		if err != nil {
			return MessageEventPage{}, err
		}
	}

	rows, err := s.db.Query(ctx, `
		SELECT id, happened_at, type, coalesce(session_id, ''), coalesce(run_id, ''), payload
		FROM events
		WHERE seq > $1 AND session_id = $2 AND type = 'message.created'
		ORDER BY seq ASC
		LIMIT $3
	`, afterSeq, sessionID, limit+1)
	if err != nil {
		return MessageEventPage{}, err
	}
	defer rows.Close()
	events := make([]app.Event, 0, limit+1)
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return MessageEventPage{}, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return MessageEventPage{}, err
	}
	hasMore := len(events) > limit
	if hasMore {
		events = events[:limit]
	}
	next := after
	if len(events) > 0 {
		next = events[len(events)-1].ID
	}
	return MessageEventPage{Events: events, NextCursor: next, HasMore: hasMore}, nil
}

func (s *PostgresStore) SaveEvalRun(run app.EvalRun) {
	if run.ID == "" {
		run.ID = app.NewID("eval")
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now().UTC()
	}
	ctx := context.Background()
	_, _ = s.db.Exec(ctx, `
		INSERT INTO eval_runs (id, profile, status, summary, cases, failure_archives, started_at, completed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO UPDATE SET
			profile = EXCLUDED.profile,
			status = EXCLUDED.status,
			summary = EXCLUDED.summary,
			cases = EXCLUDED.cases,
			failure_archives = EXCLUDED.failure_archives,
			started_at = EXCLUDED.started_at,
			completed_at = EXCLUDED.completed_at
	`, run.ID, run.Profile, run.Status, run.Summary, mustJSON(run.Cases), mustJSON(run.FailureArchives), run.StartedAt, run.CompletedAt)
	s.appendAudit(ctx, "eval."+run.Status, "", "", "evaluator", run.Summary, map[string]any{
		"profile":          run.Profile,
		"id":               run.ID,
		"failure_archives": len(run.FailureArchives),
	})
	s.appendEvent(ctx, "eval."+run.Status, "", run.ID, run)
}

func (s *PostgresStore) GetEvalRun(id string) (app.EvalRun, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, profile, status, summary, cases, failure_archives, started_at, completed_at
		FROM eval_runs
		WHERE id = $1
	`, id)
	run, err := scanEvalRun(row)
	return run, err == nil
}

func (s *PostgresStore) ListEvalRuns() []app.EvalRun {
	rows, err := s.db.Query(context.Background(), `
		SELECT id, profile, status, summary, cases, failure_archives, started_at, completed_at
		FROM eval_runs
		ORDER BY started_at DESC
	`)
	if err != nil {
		return []app.EvalRun{}
	}
	defer rows.Close()
	return collectRows(rows, scanEvalRun)
}

func (s *PostgresStore) SaveArtifactObject(object app.ArtifactObject) {
	if object.ID == "" {
		object.ID = app.NewID("obj")
	}
	if object.CreatedAt.IsZero() {
		object.CreatedAt = time.Now().UTC()
	}
	ctx := context.Background()
	_, _ = s.db.Exec(ctx, `
		INSERT INTO artifact_objects (
			id, kind, run_id, eval_id, session_id, backend, bucket, object_key, uri, path,
			content_type, bytes, created_at
		)
		VALUES ($1, $2, nullif($3, ''), nullif($4, ''), nullif($5, ''), $6, nullif($7, ''), $8, $9, nullif($10, ''), $11, $12, $13)
		ON CONFLICT (id) DO UPDATE SET
			kind = EXCLUDED.kind,
			run_id = EXCLUDED.run_id,
			eval_id = EXCLUDED.eval_id,
			session_id = EXCLUDED.session_id,
			backend = EXCLUDED.backend,
			bucket = EXCLUDED.bucket,
			object_key = EXCLUDED.object_key,
			uri = EXCLUDED.uri,
			path = EXCLUDED.path,
			content_type = EXCLUDED.content_type,
			bytes = EXCLUDED.bytes,
			created_at = EXCLUDED.created_at
	`, object.ID, object.Kind, object.RunID, object.EvalID, object.SessionID, object.Backend, object.Bucket, object.Key, object.URI, object.Path, object.ContentType, object.Bytes, object.CreatedAt)
	s.appendAudit(ctx, "artifact.saved", object.SessionID, object.RunID, "artifact-store", object.URI, map[string]any{
		"kind":    object.Kind,
		"backend": object.Backend,
		"key":     object.Key,
		"bytes":   object.Bytes,
		"eval_id": object.EvalID,
	})
	s.appendEvent(ctx, "artifact.saved", object.SessionID, object.RunID, object)
}

func (s *PostgresStore) ListArtifactObjects(limit int) []app.ArtifactObject {
	query := `
		SELECT id, kind, coalesce(run_id, ''), coalesce(eval_id, ''), coalesce(session_id, ''),
			backend, coalesce(bucket, ''), object_key, uri, coalesce(path, ''), content_type, bytes, created_at
		FROM artifact_objects
		ORDER BY created_at DESC
	`
	var rows pgx.Rows
	var err error
	if limit > 0 {
		rows, err = s.db.Query(context.Background(), query+` LIMIT $1`, limit)
	} else {
		rows, err = s.db.Query(context.Background(), query)
	}
	if err != nil {
		return []app.ArtifactObject{}
	}
	defer rows.Close()
	return collectRows(rows, scanArtifactObject)
}

func (s *PostgresStore) FindArtifactObjectByURI(uri, sessionID, runID string) (app.ArtifactObject, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, kind, coalesce(run_id, ''), coalesce(eval_id, ''), coalesce(session_id, ''),
			backend, coalesce(bucket, ''), object_key, uri, coalesce(path, ''), content_type, bytes, created_at
		FROM artifact_objects
		WHERE uri = $1
		  AND ($2 = '' OR session_id = $2)
		  AND ($3 = '' OR run_id = $3)
		ORDER BY created_at DESC
		LIMIT 1
	`, uri, sessionID, runID)
	object, err := scanArtifactObject(row)
	return object, err == nil
}

func (s *PostgresStore) SaveEpisodeSummary(summary app.EpisodeSummary) {
	if summary.ID == "" {
		summary.ID = app.NewID("ep")
	}
	if summary.CreatedAt.IsZero() {
		summary.CreatedAt = time.Now().UTC()
	}
	ctx := context.Background()
	_, _ = s.db.Exec(ctx, `
		INSERT INTO episode_summaries (
			id, session_id, run_id, goal, outcome, risk_level, model_lane, tools, approvals,
			failures, repair_performed, summary, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (id) DO UPDATE SET
			goal = EXCLUDED.goal,
			outcome = EXCLUDED.outcome,
			risk_level = EXCLUDED.risk_level,
			model_lane = EXCLUDED.model_lane,
			tools = EXCLUDED.tools,
			approvals = EXCLUDED.approvals,
			failures = EXCLUDED.failures,
			repair_performed = EXCLUDED.repair_performed,
			summary = EXCLUDED.summary,
			created_at = EXCLUDED.created_at
	`, summary.ID, summary.SessionID, summary.RunID, summary.Goal, summary.Outcome, string(summary.Risk), summary.ModelLane, mustJSON(summary.Tools), mustJSON(summary.Approvals), mustJSON(summary.Failures), summary.RepairPerformed, summary.Summary, summary.CreatedAt)
	s.appendAudit(ctx, "episode_summary.saved", summary.SessionID, summary.RunID, "runtime", summary.Outcome, map[string]any{
		"tools":            summary.Tools,
		"repair_performed": summary.RepairPerformed,
	})
	s.appendEvent(ctx, "episode_summary.saved", summary.SessionID, summary.RunID, summary)
}

func (s *PostgresStore) ListEpisodeSummaries(sessionID string) []app.EpisodeSummary {
	rows, err := s.db.Query(context.Background(), `
		SELECT id, session_id, run_id, goal, outcome, risk_level, model_lane, tools, approvals,
			failures, repair_performed, summary, created_at
		FROM episode_summaries
		WHERE $1 = '' OR session_id = $1
		ORDER BY created_at DESC
	`, sessionID)
	if err != nil {
		return []app.EpisodeSummary{}
	}
	defer rows.Close()
	return collectRows(rows, scanEpisodeSummary)
}

func mapValues[K comparable, V any](values map[K]V) []V {
	out := make([]V, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func (s *PostgresStore) appendAudit(ctx context.Context, typ, sessionID, runID, actor, summary string, fields map[string]any) {
	_, _ = s.db.Exec(ctx, `
		INSERT INTO audit_events (id, happened_at, type, session_id, run_id, actor, summary, fields)
		VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''), $6, $7, $8)
	`, app.NewID("audit"), time.Now().UTC(), typ, sessionID, runID, actor, summary, optionalJSON(fields))
}

func (s *PostgresStore) appendEvent(ctx context.Context, typ, sessionID, runID string, payload any) {
	_, _ = s.db.Exec(ctx, `
		INSERT INTO events (id, happened_at, type, session_id, run_id, payload)
		VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''), $6)
	`, app.NewID("evt"), time.Now().UTC(), typ, sessionID, runID, mustJSON(payload))
}

type scanner interface {
	Scan(dest ...any) error
}

func collectRows[T any](rows pgx.Rows, scan func(scanner) (T, error)) []T {
	out := []T{}
	for rows.Next() {
		value, err := scan(rows)
		if err == nil {
			out = append(out, value)
		}
	}
	return out
}

func scanSession(row scanner) (app.Session, error) {
	var session app.Session
	err := row.Scan(&session.ID, &session.OwnerID, &session.WorkspaceRoot, &session.Title, &session.Source, &session.Hidden, &session.CreatedAt, &session.UpdatedAt)
	return session, err
}

func scanClient(row scanner) (app.Client, error) {
	var client app.Client
	err := row.Scan(&client.ID, &client.OwnerID, &client.ActorID, &client.Name, &client.TokenHash, &client.CreatedAt, &client.LastSeenAt, &client.RevokedAt)
	return client, err
}

func scanOwnerProfile(row scanner) (app.OwnerProfile, error) {
	var profile app.OwnerProfile
	var preferences []byte
	err := row.Scan(&profile.ID, &profile.Source, &profile.ExternalRef, &profile.WorkspaceRoot,
		&profile.DefaultChannel, &profile.DefaultBindingID, &profile.DisplayName, &profile.Email,
		&preferences, &profile.CreatedAt, &profile.UpdatedAt)
	if err != nil {
		return app.OwnerProfile{}, err
	}
	profile.Preferences = map[string]string{}
	_ = json.Unmarshal(preferences, &profile.Preferences)
	if profile.Preferences == nil {
		profile.Preferences = map[string]string{}
	}
	return profile, nil
}

func scanPairingCode(row scanner) (app.PairingCode, error) {
	var code app.PairingCode
	err := row.Scan(&code.ID, &code.CodeHash, &code.Status, &code.ExpiresAt, &code.CreatedAt, &code.ClaimedAt, &code.ClientID)
	return code, err
}

func scanMessage(row scanner) (app.Message, error) {
	var message app.Message
	var attachments []byte
	err := row.Scan(&message.ID, &message.SessionID, &message.RunID, &message.Role, &message.Content, &attachments, &message.CreatedAt)
	if len(attachments) > 0 {
		_ = json.Unmarshal(attachments, &message.Attachments)
	}
	return message, err
}

func scanExternalChatSession(row scanner) (app.ExternalChatSession, error) {
	var session app.ExternalChatSession
	err := row.Scan(
		&session.ID,
		&session.OwnerID,
		&session.AuthorizedOwnerID,
		&session.AuthorizedActorID,
		&session.WorkspaceRoot,
		&session.BindingID,
		&session.Channel,
		&session.Provider,
		&session.ExternalUserID,
		&session.ExternalChatID,
		&session.ExternalThreadID,
		&session.DisplayName,
		&session.LinkedSessionID,
		&session.Status,
		&session.ProviderCursor,
		&session.LastContextToken,
		&session.CreatedAt,
		&session.UpdatedAt,
	)
	return session, err
}

func scanExternalChatMessage(row scanner) (app.ExternalChatMessage, error) {
	var message app.ExternalChatMessage
	err := row.Scan(
		&message.ID,
		&message.ChatSessionID,
		&message.BindingID,
		&message.Channel,
		&message.Direction,
		&message.Role,
		&message.ExternalMessageID,
		&message.Content,
		&message.ContextToken,
		&message.LinkedRunID,
		&message.Status,
		&message.Error,
		&message.PendingReplyKind,
		&message.PendingReply,
		&message.DispatchAttempts,
		&message.CreatedAt,
		&message.UpdatedAt,
	)
	return message, err
}

func scanChannelInboxUpdate(row scanner) (app.ChannelInboxUpdate, error) {
	var update app.ChannelInboxUpdate
	var payload []byte
	err := row.Scan(
		&update.ID,
		&update.BindingID,
		&update.Channel,
		&update.ExternalID,
		&update.ChatKey,
		&payload,
		&update.Status,
		&update.Attempts,
		&update.AvailableAt,
		&update.LastError,
		&update.CreatedAt,
		&update.UpdatedAt,
	)
	update.Payload = append([]byte(nil), payload...)
	return update, err
}

func scanPassiveNotification(row scanner) (app.PassiveNotification, error) {
	var notification app.PassiveNotification
	err := row.Scan(
		&notification.ID,
		&notification.OwnerID,
		&notification.EndpointID,
		&notification.IdempotencyKey,
		&notification.Fingerprint,
		&notification.NotificationID,
		&notification.Source,
		&notification.Kind,
		&notification.DeepLink,
		&notification.OccurredAt,
		&notification.ReadAt,
		&notification.CreatedAt,
		&notification.UpdatedAt,
	)
	return notification, err
}

func scanRunFeedback(row scanner) (app.RunFeedback, error) {
	var feedback app.RunFeedback
	err := row.Scan(
		&feedback.ID,
		&feedback.SessionID,
		&feedback.RunID,
		&feedback.MessageID,
		&feedback.Rating,
		&feedback.Note,
		&feedback.Correction,
		&feedback.CreatedAt,
		&feedback.UpdatedAt,
	)
	return feedback, err
}

func scanRun(row scanner) (app.AgentRun, error) {
	var run app.AgentRun
	var risk string
	var workflowState []byte
	var messageContext []byte
	err := row.Scan(&run.ID, &run.SessionID, &run.State, &run.ModelLane, &risk, &run.StartedAt, &run.CompletedAt, &run.Summary, &workflowState, &messageContext)
	if err != nil {
		return app.AgentRun{}, err
	}
	run.Risk = app.RiskLevel(risk)
	if len(workflowState) > 0 {
		var workflow app.WorkflowState
		if err := json.Unmarshal(workflowState, &workflow); err != nil {
			return app.AgentRun{}, fmt.Errorf("decode workflow state: %w", err)
		}
		run.Workflow = &workflow
	}
	if len(messageContext) > 0 {
		var context app.MessageRunContext
		if err := json.Unmarshal(messageContext, &context); err != nil {
			return app.AgentRun{}, fmt.Errorf("decode message run context: %w", err)
		}
		run.MessageContext = &context
	}
	return run, nil
}

func scanModelCall(row scanner) (app.ModelCall, error) {
	var call app.ModelCall
	err := row.Scan(
		&call.ID,
		&call.SessionID,
		&call.RunID,
		&call.Lane,
		&call.Profile,
		&call.Model,
		&call.Operation,
		&call.Mock,
		&call.Fallback,
		&call.Status,
		&call.PromptTokens,
		&call.ResponseTokens,
		&call.TotalTokens,
		&call.LatencyMS,
		&call.Error,
		&call.StartedAt,
		&call.CompletedAt,
	)
	return call, err
}

func scanToolCall(row scanner) (app.ToolCall, error) {
	var call app.ToolCall
	var risk string
	var args []byte
	var result []byte
	err := row.Scan(&call.ID, &call.SessionID, &call.RunID, &call.WorkflowID, &call.WorkflowNodeID, &call.ScopeRevision, &call.Capability,
		&call.Tool, &risk, &call.Status, &args, &result, &call.Error, &call.ErrorCode, &call.ApprovalID, &call.StartedAt, &call.CompletedAt, &call.ObservationRef, &call.ObservationSummary)
	if err != nil {
		return app.ToolCall{}, err
	}
	call.Risk = app.RiskLevel(risk)
	call.Arguments = map[string]any{}
	_ = json.Unmarshal(args, &call.Arguments)
	call.Result = decodeJSON(result)
	return call, nil
}

func scanDocumentRecord(row scanner) (app.DocumentRecord, error) {
	var record app.DocumentRecord
	err := row.Scan(
		&record.ID,
		&record.OwnerID,
		&record.SessionID,
		&record.GovernedPath,
		&record.Name,
		&record.ContentType,
		&record.Format,
		&record.SizeBytes,
		&record.SHA256,
		&record.Status,
		&record.Source,
		&record.SourceMessageID,
		&record.SourceRunID,
		&record.SourceToolCallID,
		&record.ParentDocumentID,
		&record.LastActivity,
		&record.LastActivityID,
		&record.LastActivityAt,
		&record.CreatedAt,
		&record.UpdatedAt,
	)
	return record, err
}

func scanApproval(row scanner) (app.Approval, error) {
	var approval app.Approval
	var source string
	var risk string
	var externalContext []byte
	var resources []byte
	var args []byte
	err := row.Scan(&approval.ID, &source, &approval.ExternalID, &externalContext,
		&approval.SessionID, &approval.RunID, &approval.ToolCallID, &approval.Tool, &risk,
		&approval.Status, &approval.Summary, &approval.Reason, &resources, &args,
		&approval.CreatedAt, &approval.ResolvedAt, &approval.ResolutionNote)
	if err != nil {
		return app.Approval{}, err
	}
	approval.Source = app.ApprovalSource(source)
	approval.Risk = app.RiskLevel(risk)
	if len(externalContext) > 0 && string(externalContext) != "null" {
		approval.ExternalContext = &app.ExternalApprovalContext{}
		_ = json.Unmarshal(externalContext, approval.ExternalContext)
	}
	approval.Resources = []string{}
	_ = json.Unmarshal(resources, &approval.Resources)
	approval.Arguments = map[string]any{}
	_ = json.Unmarshal(args, &approval.Arguments)
	return normalizeApproval(approval), nil
}

func scanReminder(row scanner) (app.Reminder, error) {
	var reminder app.Reminder
	var scheduleSpec []byte
	err := row.Scan(&reminder.ID, &reminder.SessionID, &reminder.RunID, &reminder.Text, &reminder.TextSummary,
		&reminder.DueTime, &reminder.Timezone, &reminder.Channel, &reminder.Recipient, &reminder.RecipientBinding,
		&reminder.BindingID, &reminder.CredentialRef, &reminder.BaseURL, &reminder.Recurrence,
		&reminder.DedupeKey, &reminder.Status, &reminder.LastDeliveryID, &reminder.LastError,
		&reminder.CreatedAt, &reminder.UpdatedAt, &reminder.SentAt, &reminder.CanceledAt, &reminder.DeliveryAttempt, &scheduleSpec)
	if err == nil && len(scheduleSpec) > 0 && string(scheduleSpec) != "null" {
		var spec app.ScheduleSpec
		if json.Unmarshal(scheduleSpec, &spec) == nil && spec.SchemaVersion != 0 {
			reminder.ScheduleSpec = &spec
		}
	}
	return reminder, err
}

func scanReminderDelivery(row scanner) (app.ReminderDelivery, error) {
	var delivery app.ReminderDelivery
	err := row.Scan(&delivery.ID, &delivery.ReminderID, &delivery.Channel, &delivery.Provider, &delivery.Recipient,
		&delivery.Status, &delivery.ProviderStatus, &delivery.Error, &delivery.RetryState, &delivery.Attempt,
		&delivery.SentAt, &delivery.CreatedAt)
	return delivery, err
}

func scanConnectorSetting(row scanner) (app.ConnectorSetting, error) {
	var setting app.ConnectorSetting
	err := row.Scan(&setting.OwnerID, &setting.Channel, &setting.Enabled, &setting.ISCPEnabled, &setting.LANAccessEnabled, &setting.Version, &setting.UpdatedBy, &setting.UpdatedAt)
	return setting, err
}

func scanNotificationBinding(row scanner) (app.NotificationBinding, error) {
	var binding app.NotificationBinding
	var scopes []byte
	err := row.Scan(&binding.ID, &binding.OwnerID, &binding.ActorID, &binding.Channel, &binding.Provider, &binding.Status,
		&binding.DisplayName, &binding.ExternalUserID, &binding.ExternalChatID, &binding.ExternalThreadID, &binding.AccountID, &binding.CredentialRef,
		&binding.BaseURL, &binding.ProviderSessionID, &binding.ProviderState, &binding.ContextToken,
		&binding.ProviderCursor, &binding.QRCodeURL, &binding.QRCodeImage, &binding.DefaultForChannel,
		&scopes, &binding.CreatedAt, &binding.UpdatedAt, &binding.ExpiresAt, &binding.RevokedAt,
		&binding.LastError)
	if err != nil {
		return app.NotificationBinding{}, err
	}
	binding.Scopes = []string{}
	_ = json.Unmarshal(scopes, &binding.Scopes)
	return binding, nil
}

func scanCredentialSecret(row scanner) (app.CredentialSecret, error) {
	var secret app.CredentialSecret
	err := row.Scan(&secret.Ref, &secret.Kind, &secret.Value, &secret.CreatedAt, &secret.UpdatedAt)
	return secret, err
}

func scanBrowserAuthRecord(row scanner) (app.BrowserAuthRecord, error) {
	var record app.BrowserAuthRecord
	var lastVerifiedAt *time.Time
	err := row.Scan(
		&record.ID,
		&record.OwnerID,
		&record.BrowserProfileID,
		&record.SiteOrigin,
		&record.SiteRealm,
		&record.AccountHint,
		&record.AuthStrategy,
		&record.Status,
		&record.SessionRef,
		&record.CredentialRef,
		&record.CookieJarRef,
		&lastVerifiedAt,
		&record.ExpiresAt,
		&record.LastError,
		&record.CreatedAt,
		&record.UpdatedAt,
		&record.RevokedAt,
	)
	if lastVerifiedAt != nil {
		record.LastVerifiedAt = *lastVerifiedAt
	}
	return record, err
}

func scanBrowserLoginBlock(row scanner) (app.BrowserLoginBlock, error) {
	var block app.BrowserLoginBlock
	var args, target, visibleEvidence []byte
	err := row.Scan(
		&block.ID,
		&block.SessionID,
		&block.RunID,
		&block.SchemaVersion,
		&block.Version,
		&block.WorkflowID,
		&block.WorkflowRevision,
		&block.WorkflowNodeID,
		&block.SessionGeneration,
		&block.Status,
		&block.OriginalGoal,
		&block.ResumeTool,
		&args,
		&block.LastToolCallID,
		&block.LoginHandoffURL,
		&block.LoginHandoffPageID,
		&block.LastVisiblePageID,
		&block.OwnerID,
		&block.BrowserProfileID,
		&block.SiteOrigin,
		&block.SiteRealm,
		&block.AccountHint,
		&block.BrowserAuthStatus,
		&target,
		&visibleEvidence,
		&block.LastUserReply,
		&block.LastError,
		&block.TransitionOwnerID,
		&block.TransitionLeaseUntil,
		&block.CreatedAt,
		&block.UpdatedAt,
		&block.ResolvedAt,
	)
	if len(args) > 0 {
		_ = json.Unmarshal(args, &block.ResumeArgs)
	}
	if len(target) > 0 {
		_ = json.Unmarshal(target, &block.Target)
	}
	if len(visibleEvidence) > 0 && string(visibleEvidence) != "null" {
		_ = json.Unmarshal(visibleEvidence, &block.VisibleEvidence)
	}
	if block.ResumeArgs == nil {
		block.ResumeArgs = map[string]any{}
	}
	return block, err
}

func scanMemoryCandidate(row scanner) (app.MemoryCandidate, error) {
	var candidate app.MemoryCandidate
	err := row.Scan(&candidate.ID, &candidate.SessionID, &candidate.RunID, &candidate.Kind, &candidate.Content, &candidate.Sensitivity, &candidate.Status, &candidate.Reason, &candidate.CreatedAt, &candidate.ResolvedAt)
	return candidate, err
}

func scanMemory(row scanner) (app.Memory, error) {
	var memory app.Memory
	err := row.Scan(&memory.ID, &memory.Kind, &memory.Content, &memory.SourceID, &memory.CreatedAt)
	return memory, err
}

func scanMemoryWithSession(row scanner) (app.Memory, string, error) {
	var memory app.Memory
	var sessionID string
	err := row.Scan(&memory.ID, &memory.Kind, &memory.Content, &memory.SourceID, &memory.CreatedAt, &sessionID)
	return memory, sessionID, err
}

func scanAuditEvent(row scanner) (app.AuditEvent, error) {
	var event app.AuditEvent
	var fields []byte
	err := row.Scan(&event.ID, &event.Time, &event.Type, &event.SessionID, &event.RunID, &event.Actor, &event.Summary, &fields)
	if err != nil {
		return app.AuditEvent{}, err
	}
	if len(fields) > 0 {
		event.Fields = map[string]any{}
		_ = json.Unmarshal(fields, &event.Fields)
	}
	return event, nil
}

func scanEvent(row scanner) (app.Event, error) {
	var event app.Event
	var payload []byte
	err := row.Scan(&event.ID, &event.Time, &event.Type, &event.SessionID, &event.RunID, &payload)
	if err != nil {
		return app.Event{}, err
	}
	event.Payload = decodeJSON(payload)
	return event, nil
}

func scanEvalRun(row scanner) (app.EvalRun, error) {
	var run app.EvalRun
	var cases []byte
	var failureArchives []byte
	err := row.Scan(&run.ID, &run.Profile, &run.Status, &run.Summary, &cases, &failureArchives, &run.StartedAt, &run.CompletedAt)
	if err != nil {
		return app.EvalRun{}, err
	}
	run.Cases = []app.EvalCase{}
	_ = json.Unmarshal(cases, &run.Cases)
	run.FailureArchives = []app.EvalArtifact{}
	_ = json.Unmarshal(failureArchives, &run.FailureArchives)
	return run, nil
}

func scanArtifactObject(row scanner) (app.ArtifactObject, error) {
	var object app.ArtifactObject
	err := row.Scan(
		&object.ID,
		&object.Kind,
		&object.RunID,
		&object.EvalID,
		&object.SessionID,
		&object.Backend,
		&object.Bucket,
		&object.Key,
		&object.URI,
		&object.Path,
		&object.ContentType,
		&object.Bytes,
		&object.CreatedAt,
	)
	return object, err
}

func scanEpisodeSummary(row scanner) (app.EpisodeSummary, error) {
	var summary app.EpisodeSummary
	var risk string
	var tools []byte
	var approvals []byte
	var failures []byte
	err := row.Scan(&summary.ID, &summary.SessionID, &summary.RunID, &summary.Goal, &summary.Outcome, &risk, &summary.ModelLane, &tools, &approvals, &failures, &summary.RepairPerformed, &summary.Summary, &summary.CreatedAt)
	if err != nil {
		return app.EpisodeSummary{}, err
	}
	summary.Risk = app.RiskLevel(risk)
	_ = json.Unmarshal(tools, &summary.Tools)
	_ = json.Unmarshal(approvals, &summary.Approvals)
	_ = json.Unmarshal(failures, &summary.Failures)
	return summary, nil
}

func mustJSON(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		return []byte(`null`)
	}
	return raw
}

func mustJSONRaw(value json.RawMessage) []byte {
	if len(value) == 0 || !json.Valid(value) {
		return []byte(`{}`)
	}
	return append([]byte(nil), value...)
}

func optionalJSON(value any) []byte {
	if value == nil {
		return nil
	}
	return mustJSON(value)
}

func decodeJSON(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

func zeroTimeToNil(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func redactPostgresExternalID(value string) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= 6 {
		return value
	}
	return string(runes[:3]) + "***" + string(runes[len(runes)-2:])
}

func rollbackTx(ctx context.Context, tx pgx.Tx) {
	_ = tx.Rollback(ctx)
}
