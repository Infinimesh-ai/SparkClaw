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
	requested_media JSONB NOT NULL DEFAULT '[]',
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
	  completed_at TIMESTAMPTZ,
	  policy_context JSONB
	);
	ALTER TABLE tool_calls ADD COLUMN IF NOT EXISTS observation_ref TEXT;
	ALTER TABLE tool_calls ADD COLUMN IF NOT EXISTS observation_summary TEXT NOT NULL DEFAULT '';
	ALTER TABLE tool_calls ADD COLUMN IF NOT EXISTS workflow_id TEXT NOT NULL DEFAULT '';
	ALTER TABLE tool_calls ADD COLUMN IF NOT EXISTS workflow_node_id TEXT NOT NULL DEFAULT '';
	ALTER TABLE tool_calls ADD COLUMN IF NOT EXISTS scope_revision INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE tool_calls ADD COLUMN IF NOT EXISTS capability TEXT NOT NULL DEFAULT '';
	ALTER TABLE tool_calls ADD COLUMN IF NOT EXISTS error_code TEXT;
	ALTER TABLE tool_calls ADD COLUMN IF NOT EXISTS policy_context JSONB;

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
  resolution_note TEXT,
  policy_context JSONB
);
ALTER TABLE approvals ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'tool';
ALTER TABLE approvals ADD COLUMN IF NOT EXISTS external_id TEXT NOT NULL DEFAULT '';
ALTER TABLE approvals ADD COLUMN IF NOT EXISTS external_context JSONB;
ALTER TABLE approvals ADD COLUMN IF NOT EXISTS policy_context JSONB;
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
ALTER TABLE messages ADD COLUMN IF NOT EXISTS requested_media JSONB NOT NULL DEFAULT '[]';

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
