CREATE TABLE IF NOT EXISTS owners (
  id TEXT PRIMARY KEY,
  display_name TEXT NOT NULL,
  email TEXT NOT NULL DEFAULT '',
  preferences JSONB NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE owners ADD COLUMN IF NOT EXISTS email TEXT NOT NULL DEFAULT '';
ALTER TABLE owners ADD COLUMN IF NOT EXISTS preferences JSONB NOT NULL DEFAULT '{}';
ALTER TABLE owners ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

CREATE TABLE IF NOT EXISTS clients (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  token_hash TEXT NOT NULL UNIQUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at TIMESTAMPTZ,
  revoked_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS pairing_codes (
  id TEXT PRIMARY KEY,
  code_hash TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  claimed_at TIMESTAMPTZ,
  client_id TEXT REFERENCES clients(id)
);

CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS messages (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES sessions(id),
  run_id TEXT,
  role TEXT NOT NULL,
  content TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS agent_runs (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES sessions(id),
  state TEXT NOT NULL,
  model_lane TEXT NOT NULL,
  risk_level TEXT NOT NULL,
  summary TEXT,
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at TIMESTAMPTZ
);

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
  tool TEXT NOT NULL,
  risk_level TEXT NOT NULL,
  status TEXT NOT NULL,
  arguments JSONB NOT NULL,
  result JSONB,
  error TEXT,
  approval_id TEXT,
  observation_ref TEXT,
  observation_summary TEXT NOT NULL DEFAULT '',
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at TIMESTAMPTZ
);

ALTER TABLE tool_calls ADD COLUMN IF NOT EXISTS observation_ref TEXT;
ALTER TABLE tool_calls ADD COLUMN IF NOT EXISTS observation_summary TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS approvals (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES sessions(id),
  run_id TEXT NOT NULL REFERENCES agent_runs(id),
  tool_call_id TEXT NOT NULL REFERENCES tool_calls(id),
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

CREATE TABLE IF NOT EXISTS documents (
  id TEXT PRIMARY KEY,
  source TEXT NOT NULL,
  root TEXT NOT NULL,
  path TEXT NOT NULL,
  rel_path TEXT NOT NULL,
  object_key TEXT,
  content_hash TEXT NOT NULL,
  bytes INTEGER NOT NULL,
  indexed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (source, root, rel_path)
);

CREATE TABLE IF NOT EXISTS document_chunks (
  id TEXT PRIMARY KEY,
  document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
  source TEXT NOT NULL,
  root TEXT NOT NULL,
  path TEXT NOT NULL,
  rel_path TEXT NOT NULL,
  start_line INTEGER NOT NULL,
  end_line INTEGER NOT NULL,
  text TEXT NOT NULL,
  terms TEXT[] NOT NULL DEFAULT '{}',
  content_hash TEXT NOT NULL,
  embedding_json JSONB,
  embedding_model TEXT,
  embedding_dim INTEGER NOT NULL DEFAULT 0,
  indexed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

DO $$
BEGIN
  CREATE EXTENSION IF NOT EXISTS vector;
EXCEPTION
  WHEN undefined_file THEN
    RAISE NOTICE 'pgvector extension is not installed; document_chunks.embedding will be skipped';
END
$$;

DO $$
BEGIN
  ALTER TABLE document_chunks ADD COLUMN IF NOT EXISTS embedding vector;
EXCEPTION
  WHEN undefined_object THEN
    RAISE NOTICE 'pgvector type is unavailable; using document_chunks.embedding_json fallback only';
END
$$;

DO $$
BEGIN
  CREATE INDEX IF NOT EXISTS idx_document_chunks_embedding_hnsw_1024
    ON document_chunks
    USING hnsw ((embedding::vector(1024)) vector_cosine_ops)
    WHERE embedding IS NOT NULL AND vector_dims(embedding) = 1024;
EXCEPTION
  WHEN undefined_object OR undefined_column THEN
    RAISE NOTICE 'pgvector HNSW index unavailable; vector search will use exact scan or JSON fallback';
END
$$;

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
CREATE INDEX IF NOT EXISTS idx_episode_summaries_session_created ON episode_summaries(session_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_documents_source_root_rel ON documents(source, root, rel_path);
CREATE INDEX IF NOT EXISTS idx_document_chunks_source_root ON document_chunks(source, root);
CREATE INDEX IF NOT EXISTS idx_document_chunks_embedding_model_dim ON document_chunks(embedding_model, embedding_dim);
CREATE INDEX IF NOT EXISTS idx_document_chunks_terms ON document_chunks USING GIN (terms);

UPDATE document_chunks
SET embedding_dim = jsonb_array_length(embedding_json)
WHERE embedding_dim = 0
  AND jsonb_typeof(embedding_json) = 'array';
