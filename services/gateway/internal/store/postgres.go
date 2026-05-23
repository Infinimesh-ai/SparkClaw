package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type PostgresStore struct {
	db *pgxpool.Pool
}

const postgresSchema = `
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
  indexed_at TIMESTAMPTZ NOT NULL DEFAULT now()
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
CREATE INDEX IF NOT EXISTS idx_episode_summaries_session_created ON episode_summaries(session_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_documents_source_root_rel ON documents(source, root, rel_path);
CREATE INDEX IF NOT EXISTS idx_document_chunks_source_root ON document_chunks(source, root);
CREATE INDEX IF NOT EXISTS idx_document_chunks_terms ON document_chunks USING GIN (terms);
`

const postgresVectorSchema = `
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
`

func NewPostgresStore(ctx context.Context, dsn string) (*PostgresStore, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("postgres state backend requires SPARKCLAW_STATE_DSN or SPARKCLAW_POSTGRES_DSN")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	st := &PostgresStore{db: pool}
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
	_, _ = s.db.Exec(ctx, postgresVectorSchema)
	return nil
}

func (s *PostgresStore) CreateSession(title string) app.Session {
	now := time.Now().UTC()
	if title == "" {
		title = "New SparkClaw Session"
	}
	session := app.Session{ID: app.NewID("s"), Title: title, CreatedAt: now, UpdatedAt: now}
	ctx := context.Background()
	_, _ = s.db.Exec(ctx, `
		INSERT INTO sessions (id, title, created_at, updated_at)
		VALUES ($1, $2, $3, $4)
	`, session.ID, session.Title, session.CreatedAt, session.UpdatedAt)
	s.appendAudit(ctx, "session.created", session.ID, "", "system", "Session created", map[string]any{"title": title})
	s.appendEvent(ctx, "session.created", session.ID, "", session)
	return session
}

func (s *PostgresStore) ListSessions() []app.Session {
	rows, err := s.db.Query(context.Background(), `
		SELECT id, title, created_at, updated_at
		FROM sessions
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
		SELECT id, title, created_at, updated_at
		FROM sessions
		WHERE id = $1
	`, id)
	session, err := scanSession(row)
	return session, err == nil
}

func (s *PostgresStore) SaveClient(client app.Client) {
	if client.ID == "" {
		client.ID = app.NewID("client")
	}
	if client.CreatedAt.IsZero() {
		client.CreatedAt = time.Now().UTC()
	}
	ctx := context.Background()
	_, _ = s.db.Exec(ctx, `
		INSERT INTO clients (id, name, token_hash, created_at, last_seen_at, revoked_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name,
			token_hash = EXCLUDED.token_hash,
			last_seen_at = EXCLUDED.last_seen_at,
			revoked_at = EXCLUDED.revoked_at
	`, client.ID, client.Name, client.TokenHash, client.CreatedAt, client.LastSeenAt, client.RevokedAt)
	s.appendAudit(ctx, "client.saved", "", "", "gateway", client.Name, map[string]any{"client_id": client.ID})
	s.appendEvent(ctx, "client.saved", "", "", client)
}

func (s *PostgresStore) GetClient(id string) (app.Client, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, name, token_hash, created_at, last_seen_at, revoked_at
		FROM clients
		WHERE id = $1
	`, id)
	client, err := scanClient(row)
	return client, err == nil
}

func (s *PostgresStore) ListClients() []app.Client {
	rows, err := s.db.Query(context.Background(), `
		SELECT id, name, token_hash, created_at, last_seen_at, revoked_at
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
		RETURNING id, name, token_hash, created_at, last_seen_at, revoked_at
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
		SELECT id, name, token_hash, created_at, last_seen_at, revoked_at
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
	row := s.db.QueryRow(context.Background(), `
		SELECT id, display_name, email, preferences, created_at, updated_at
		FROM owners
		WHERE id = $1
	`, app.DefaultOwnerID)
	profile, err := scanOwnerProfile(row)
	if err == nil {
		return profile
	}
	profile = app.DefaultOwnerProfile()
	_, _ = s.db.Exec(context.Background(), `
		INSERT INTO owners (id, display_name, email, preferences, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO NOTHING
	`, profile.ID, profile.DisplayName, profile.Email, mustJSON(profile.Preferences), profile.CreatedAt, profile.UpdatedAt)
	return profile
}

func (s *PostgresStore) UpdateOwnerProfile(profile app.OwnerProfile) app.OwnerProfile {
	current := s.GetOwnerProfile()
	now := time.Now().UTC()
	profile.ID = current.ID
	if profile.CreatedAt.IsZero() {
		profile.CreatedAt = current.CreatedAt
	}
	profile.UpdatedAt = now
	if profile.Preferences == nil {
		profile.Preferences = map[string]string{}
	}
	profile = normalizeOwnerProfile(profile)
	ctx := context.Background()
	_, _ = s.db.Exec(ctx, `
		INSERT INTO owners (id, display_name, email, preferences, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO UPDATE SET
			display_name = EXCLUDED.display_name,
			email = EXCLUDED.email,
			preferences = EXCLUDED.preferences,
			updated_at = EXCLUDED.updated_at
	`, profile.ID, profile.DisplayName, profile.Email, mustJSON(profile.Preferences), profile.CreatedAt, profile.UpdatedAt)
	s.appendAudit(ctx, "owner_profile.updated", "", "", "owner", profile.DisplayName, map[string]any{
		"owner_id":     profile.ID,
		"email_set":    profile.Email != "",
		"preferences":  len(profile.Preferences),
		"display_name": profile.DisplayName,
	})
	s.appendEvent(ctx, "owner_profile.updated", "", "", profile)
	return profile
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
	_, _ = s.db.Exec(ctx, `
		INSERT INTO messages (id, session_id, run_id, role, content, created_at)
		VALUES ($1, $2, nullif($3, ''), $4, $5, $6)
	`, message.ID, message.SessionID, message.RunID, message.Role, message.Content, message.CreatedAt)
	if session, ok := s.GetSession(message.SessionID); ok {
		session.UpdatedAt = message.CreatedAt
		if session.Title == "" || session.Title == "New SparkClaw Session" {
			session.Title = deriveTitle(message.Content)
		}
		_, _ = s.db.Exec(ctx, `
			UPDATE sessions
			SET title = $2, updated_at = $3
			WHERE id = $1
		`, session.ID, session.Title, session.UpdatedAt)
	}
	s.appendEvent(ctx, "message.created", message.SessionID, message.RunID, message)
	return message
}

func (s *PostgresStore) ListMessages(sessionID string) []app.Message {
	rows, err := s.db.Query(context.Background(), `
		SELECT id, session_id, coalesce(run_id, ''), role, content, created_at
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
	_, _ = s.db.Exec(ctx, `
		INSERT INTO agent_runs (id, session_id, state, model_lane, risk_level, summary, started_at, completed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO UPDATE SET
			state = EXCLUDED.state,
			model_lane = EXCLUDED.model_lane,
			risk_level = EXCLUDED.risk_level,
			summary = EXCLUDED.summary,
			started_at = EXCLUDED.started_at,
			completed_at = EXCLUDED.completed_at
	`, run.ID, run.SessionID, run.State, run.ModelLane, string(run.Risk), run.Summary, run.StartedAt, run.CompletedAt)
	s.appendEvent(ctx, "run."+run.State, run.SessionID, run.ID, run)
}

func (s *PostgresStore) GetRun(id string) (app.AgentRun, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, session_id, state, model_lane, risk_level, started_at, completed_at, coalesce(summary, '')
		FROM agent_runs
		WHERE id = $1
	`, id)
	run, err := scanRun(row)
	return run, err == nil
}

func (s *PostgresStore) ListRuns(sessionID string) []app.AgentRun {
	rows, err := s.db.Query(context.Background(), `
		SELECT id, session_id, state, model_lane, risk_level, started_at, completed_at, coalesce(summary, '')
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
			id, session_id, run_id, tool, risk_level, status, arguments, result, error,
			approval_id, observation_ref, observation_summary, started_at, completed_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, nullif($9, ''), nullif($10, ''), nullif($11, ''), $12, $13, $14)
		ON CONFLICT (id) DO UPDATE SET
			risk_level = EXCLUDED.risk_level,
			status = EXCLUDED.status,
			arguments = EXCLUDED.arguments,
			result = EXCLUDED.result,
			error = EXCLUDED.error,
			approval_id = EXCLUDED.approval_id,
			observation_ref = EXCLUDED.observation_ref,
			observation_summary = EXCLUDED.observation_summary,
			started_at = EXCLUDED.started_at,
			completed_at = EXCLUDED.completed_at
	`, call.ID, call.SessionID, call.RunID, call.Tool, string(call.Risk), call.Status, args, result, call.Error, call.ApprovalID, call.ObservationRef, call.ObservationSummary, call.StartedAt, call.CompletedAt)
	s.appendAudit(ctx, "tool_call."+call.Status, call.SessionID, call.RunID, "agent", call.Tool, map[string]any{
		"risk": call.Risk,
		"id":   call.ID,
	})
	s.appendEvent(ctx, "tool_call."+call.Status, call.SessionID, call.RunID, call)
}

func (s *PostgresStore) GetToolCall(id string) (app.ToolCall, bool) {
	row := s.db.QueryRow(context.Background(), `
		SELECT id, session_id, run_id, tool, risk_level, status, arguments, result, coalesce(error, ''),
			coalesce(approval_id, ''), started_at, completed_at, coalesce(observation_ref, ''), coalesce(observation_summary, '')
		FROM tool_calls
		WHERE id = $1
	`, id)
	call, err := scanToolCall(row)
	return call, err == nil
}

func (s *PostgresStore) ListToolCalls(sessionID string) []app.ToolCall {
	rows, err := s.db.Query(context.Background(), `
		SELECT id, session_id, run_id, tool, risk_level, status, arguments, result, coalesce(error, ''),
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

func (s *PostgresStore) SaveApproval(approval app.Approval) {
	ctx := context.Background()
	if approval.CreatedAt.IsZero() {
		approval.CreatedAt = time.Now().UTC()
	}
	_, _ = s.db.Exec(ctx, `
		INSERT INTO approvals (
			id, session_id, run_id, tool_call_id, tool, risk_level, status, summary, reason,
			resources, arguments, created_at, resolved_at, resolution_note
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, nullif($14, ''))
		ON CONFLICT (id) DO UPDATE SET
			status = EXCLUDED.status,
			summary = EXCLUDED.summary,
			reason = EXCLUDED.reason,
			resources = EXCLUDED.resources,
			arguments = EXCLUDED.arguments,
			resolved_at = EXCLUDED.resolved_at,
			resolution_note = EXCLUDED.resolution_note
	`, approval.ID, approval.SessionID, approval.RunID, approval.ToolCallID, approval.Tool, string(approval.Risk), approval.Status, approval.Summary, approval.Reason, mustJSON(approval.Resources), mustJSON(approval.Arguments), approval.CreatedAt, approval.ResolvedAt, approval.ResolutionNote)
	s.appendAudit(ctx, "approval."+approval.Status, approval.SessionID, approval.RunID, "policy", approval.Summary, map[string]any{
		"tool": approval.Tool,
		"risk": approval.Risk,
	})
	s.appendEvent(ctx, "approval."+approval.Status, approval.SessionID, approval.RunID, approval)
}

func (s *PostgresStore) ResolveApproval(id, status, note string) (app.Approval, error) {
	ctx := context.Background()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return app.Approval{}, err
	}
	defer rollbackTx(ctx, tx)
	row := tx.QueryRow(ctx, `
		SELECT id, session_id, run_id, tool_call_id, tool, risk_level, status, summary, reason,
			resources, arguments, created_at, resolved_at, coalesce(resolution_note, '')
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
	s.appendAudit(ctx, "approval."+status, approval.SessionID, approval.RunID, "owner", approval.Summary, map[string]any{"note": note})
	s.appendEvent(ctx, "approval."+status, approval.SessionID, approval.RunID, approval)
	return approval, nil
}

func (s *PostgresStore) ListApprovals(status string) []app.Approval {
	rows, err := s.db.Query(context.Background(), `
		SELECT id, session_id, run_id, tool_call_id, tool, risk_level, status, summary, reason,
			resources, arguments, created_at, resolved_at, coalesce(resolution_note, '')
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

func (s *PostgresStore) ReplaceDocumentChunks(root string, documents []app.Document, chunks []app.DocumentChunk) (app.DocumentIndexSummary, error) {
	ctx := context.Background()
	now := time.Now().UTC()
	source := "workspace"
	if len(documents) > 0 && strings.TrimSpace(documents[0].Source) != "" {
		source = documents[0].Source
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return app.DocumentIndexSummary{}, err
	}
	defer rollbackTx(ctx, tx)
	if _, err := tx.Exec(ctx, `
		DELETE FROM documents
		WHERE source = $1 AND root = $2
	`, source, root); err != nil {
		return app.DocumentIndexSummary{}, err
	}
	for _, doc := range documents {
		if doc.IndexedAt.IsZero() {
			doc.IndexedAt = now
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO documents (
				id, source, root, path, rel_path, object_key, content_hash, bytes, indexed_at
			)
			VALUES ($1, $2, $3, $4, $5, nullif($6, ''), $7, $8, $9)
		`, doc.ID, doc.Source, doc.Root, doc.Path, doc.RelPath, doc.ObjectKey, doc.ContentHash, doc.Bytes, doc.IndexedAt); err != nil {
			return app.DocumentIndexSummary{}, err
		}
	}
	vectorColumn := s.hasVectorColumn(ctx)
	for _, chunk := range chunks {
		if chunk.IndexedAt.IsZero() {
			chunk.IndexedAt = now
		}
		if vectorColumn && len(chunk.Embedding) > 0 {
			if _, err := tx.Exec(ctx, `
				INSERT INTO document_chunks (
					id, document_id, source, root, path, rel_path, start_line, end_line, text,
					terms, content_hash, embedding_json, embedding, embedding_model, indexed_at
				)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13::vector, nullif($14, ''), $15)
			`, chunk.ID, chunk.DocumentID, chunk.Source, chunk.Root, chunk.Path, chunk.RelPath, chunk.StartLine, chunk.EndLine, chunk.Text, chunk.Terms, chunk.ContentHash, optionalJSON(chunk.Embedding), vectorLiteral(chunk.Embedding), chunk.EmbeddingModel, chunk.IndexedAt); err != nil {
				return app.DocumentIndexSummary{}, err
			}
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO document_chunks (
				id, document_id, source, root, path, rel_path, start_line, end_line, text,
				terms, content_hash, embedding_json, embedding_model, indexed_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, nullif($13, ''), $14)
		`, chunk.ID, chunk.DocumentID, chunk.Source, chunk.Root, chunk.Path, chunk.RelPath, chunk.StartLine, chunk.EndLine, chunk.Text, chunk.Terms, chunk.ContentHash, optionalJSON(chunk.Embedding), chunk.EmbeddingModel, chunk.IndexedAt); err != nil {
			return app.DocumentIndexSummary{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return app.DocumentIndexSummary{}, err
	}
	embeddingModel := ""
	vectorEnabled := false
	for _, chunk := range chunks {
		if len(chunk.Embedding) > 0 {
			vectorEnabled = true
			embeddingModel = chunk.EmbeddingModel
			break
		}
	}
	s.appendAudit(ctx, "documents.indexed", "", "", "toolhub", "Workspace knowledge indexed", map[string]any{
		"root":            root,
		"documents":       len(documents),
		"chunks":          len(chunks),
		"vector_enabled":  vectorEnabled,
		"embedding_model": embeddingModel,
	})
	s.appendEvent(ctx, "documents.indexed", "", "", map[string]any{
		"root":            root,
		"documents":       len(documents),
		"chunks":          len(chunks),
		"vector_enabled":  vectorEnabled,
		"embedding_model": embeddingModel,
	})
	backend := "postgres"
	if vectorEnabled && vectorColumn {
		backend = "postgres_pgvector"
	}
	return app.DocumentIndexSummary{
		Backend:        backend,
		Root:           root,
		Documents:      len(documents),
		Chunks:         len(chunks),
		VectorEnabled:  vectorEnabled,
		EmbeddingModel: embeddingModel,
		IndexedAt:      now,
	}, nil
}

func (s *PostgresStore) SearchDocumentChunks(query string, embedding []float32, maxResults int) ([]app.DocumentChunkHit, error) {
	if maxResults <= 0 || maxResults > 50 {
		maxResults = 8
	}
	ctx := context.Background()
	if len(embedding) > 0 && s.hasVectorColumn(ctx) {
		hits, err := s.searchDocumentChunksWithVector(ctx, query, embedding, maxResults)
		if err == nil {
			return hits, nil
		}
	}
	return s.searchDocumentChunksGeneric(ctx, query, embedding, maxResults)
}

type dbDocumentChunk struct {
	ID             string
	Path           string
	RelPath        string
	StartLine      int
	EndLine        int
	Text           string
	Terms          []string
	Embedding      []float32
	EmbeddingModel string
	VectorScore    float64
	Backend        string
}

func (s *PostgresStore) searchDocumentChunksWithVector(ctx context.Context, query string, embedding []float32, maxResults int) ([]app.DocumentChunkHit, error) {
	limit := maxResults * 8
	if limit < 50 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, path, rel_path, start_line, end_line, text, terms, embedding_json,
			coalesce(embedding_model, ''), 1 - (embedding <=> $1::vector) AS vector_score
		FROM document_chunks
		WHERE embedding IS NOT NULL
		ORDER BY embedding <=> $1::vector
		LIMIT $2
	`, vectorLiteral(embedding), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	candidates := map[string]dbDocumentChunk{}
	for rows.Next() {
		chunk, err := scanDBDocumentChunk(rows, "postgres_pgvector")
		if err == nil {
			candidates[chunk.ID] = chunk
		}
	}
	for _, chunk := range s.keywordDocumentCandidates(ctx, query, limit) {
		if existing, ok := candidates[chunk.ID]; ok {
			if existing.VectorScore != 0 {
				chunk.VectorScore = existing.VectorScore
			}
		}
		candidates[chunk.ID] = chunk
	}
	return rankDocumentChunks(query, embedding, mapValues(candidates), maxResults), nil
}

func (s *PostgresStore) searchDocumentChunksGeneric(ctx context.Context, query string, embedding []float32, maxResults int) ([]app.DocumentChunkHit, error) {
	limit := maxResults * 20
	if limit < 200 {
		limit = 200
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, path, rel_path, start_line, end_line, text, terms, embedding_json,
			coalesce(embedding_model, ''), 0::double precision AS vector_score
		FROM document_chunks
		ORDER BY indexed_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	candidates := []dbDocumentChunk{}
	for rows.Next() {
		chunk, err := scanDBDocumentChunk(rows, "postgres_json")
		if err == nil {
			candidates = append(candidates, chunk)
		}
	}
	return rankDocumentChunks(query, embedding, candidates, maxResults), nil
}

func (s *PostgresStore) keywordDocumentCandidates(ctx context.Context, query string, limit int) []dbDocumentChunk {
	queryTerms := documentTerms(query)
	if len(queryTerms) == 0 {
		return nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, path, rel_path, start_line, end_line, text, terms, embedding_json,
			coalesce(embedding_model, ''), 0::double precision AS vector_score
		FROM document_chunks
		WHERE terms && $1
			OR lower(text) LIKE '%' || lower($2) || '%'
			OR lower(rel_path) LIKE '%' || lower($2) || '%'
		ORDER BY indexed_at DESC
		LIMIT $3
	`, queryTerms, query, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	candidates := []dbDocumentChunk{}
	for rows.Next() {
		chunk, err := scanDBDocumentChunk(rows, "postgres_pgvector")
		if err == nil {
			candidates = append(candidates, chunk)
		}
	}
	return candidates
}

func rankDocumentChunks(query string, embedding []float32, chunks []dbDocumentChunk, maxResults int) []app.DocumentChunkHit {
	queryTerms := documentTerms(query)
	hits := []app.DocumentChunkHit{}
	for _, chunk := range chunks {
		keywordScore := scoreDocumentChunk(queryTerms, chunk)
		vectorScore := chunk.VectorScore
		if vectorScore == 0 && len(embedding) > 0 && len(chunk.Embedding) > 0 {
			vectorScore = cosine(embedding, chunk.Embedding)
		}
		score := float64(keywordScore) + vectorScore*6
		if score <= 0 {
			continue
		}
		hits = append(hits, app.DocumentChunkHit{
			Path:           chunk.Path,
			RelPath:        chunk.RelPath,
			StartLine:      chunk.StartLine,
			EndLine:        chunk.EndLine,
			Citation:       documentCitation(chunk.RelPath, chunk.StartLine, chunk.EndLine),
			Score:          score,
			KeywordScore:   keywordScore,
			VectorScore:    vectorScore,
			Snippet:        documentSnippet(chunk.Text, queryTerms),
			Terms:          intersectDocumentTerms(queryTerms, chunk.Terms),
			EmbeddingModel: chunk.EmbeddingModel,
			Backend:        chunk.Backend,
		})
	}
	slices.SortFunc(hits, func(a, b app.DocumentChunkHit) int {
		if a.Score != b.Score {
			if a.Score > b.Score {
				return -1
			}
			return 1
		}
		return strings.Compare(a.RelPath, b.RelPath)
	})
	if len(hits) > maxResults {
		hits = hits[:maxResults]
	}
	return hits
}

func scanDBDocumentChunk(row scanner, backend string) (dbDocumentChunk, error) {
	var chunk dbDocumentChunk
	var embeddingJSON []byte
	err := row.Scan(&chunk.ID, &chunk.Path, &chunk.RelPath, &chunk.StartLine, &chunk.EndLine, &chunk.Text, &chunk.Terms, &embeddingJSON, &chunk.EmbeddingModel, &chunk.VectorScore)
	if err != nil {
		return dbDocumentChunk{}, err
	}
	if len(embeddingJSON) > 0 {
		_ = json.Unmarshal(embeddingJSON, &chunk.Embedding)
	}
	chunk.Backend = backend
	return chunk, nil
}

func (s *PostgresStore) hasVectorColumn(ctx context.Context) bool {
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = 'public'
				AND table_name = 'document_chunks'
				AND column_name = 'embedding'
		)
	`).Scan(&exists)
	return err == nil && exists
}

func vectorLiteral(vector []float32) string {
	parts := make([]string, 0, len(vector))
	for _, value := range vector {
		parts = append(parts, fmt.Sprintf("%.8g", value))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func documentTerms(text string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, term := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9') && (r < 0x4e00 || r > 0x9fff)
	}) {
		if len(term) < 2 || seen[term] {
			continue
		}
		seen[term] = true
		out = append(out, term)
	}
	return out
}

func scoreDocumentChunk(queryTerms []string, chunk dbDocumentChunk) int {
	score := 0
	text := strings.ToLower(chunk.Text)
	rel := strings.ToLower(chunk.RelPath)
	for _, term := range queryTerms {
		if slices.Contains(chunk.Terms, term) {
			score += 3
		}
		if strings.Contains(rel, term) {
			score += 2
		}
		if strings.Contains(text, term) {
			score++
		}
	}
	return score
}

func intersectDocumentTerms(queryTerms, chunkTerms []string) []string {
	out := []string{}
	for _, term := range queryTerms {
		if slices.Contains(chunkTerms, term) {
			out = append(out, term)
		}
	}
	return out
}

func documentSnippet(text string, terms []string) string {
	lower := strings.ToLower(text)
	idx := -1
	queryLen := 0
	for _, term := range terms {
		if found := strings.Index(lower, term); found >= 0 && (idx < 0 || found < idx) {
			idx = found
			queryLen = len(term)
		}
	}
	if idx < 0 {
		return compactText(text, 240)
	}
	start := idx - 80
	if start < 0 {
		start = 0
	}
	end := idx + queryLen + 160
	if end > len(text) {
		end = len(text)
	}
	return strings.TrimSpace(text[start:end])
}

func documentCitation(relPath string, startLine, endLine int) string {
	relPath = strings.TrimSpace(relPath)
	if relPath == "" {
		return ""
	}
	if startLine <= 0 {
		return relPath
	}
	if endLine <= 0 || endLine < startLine || endLine == startLine {
		return fmt.Sprintf("%s:L%d", relPath, startLine)
	}
	return fmt.Sprintf("%s:L%d-L%d", relPath, startLine, endLine)
}

func compactText(value string, max int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= max {
		return value
	}
	return value[:max] + "..."
}

func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		av := float64(a[i])
		bv := float64(b[i])
		dot += av * bv
		normA += av * av
		normB += bv * bv
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
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
	err := row.Scan(&session.ID, &session.Title, &session.CreatedAt, &session.UpdatedAt)
	return session, err
}

func scanClient(row scanner) (app.Client, error) {
	var client app.Client
	err := row.Scan(&client.ID, &client.Name, &client.TokenHash, &client.CreatedAt, &client.LastSeenAt, &client.RevokedAt)
	return client, err
}

func scanOwnerProfile(row scanner) (app.OwnerProfile, error) {
	var profile app.OwnerProfile
	var preferences []byte
	err := row.Scan(&profile.ID, &profile.DisplayName, &profile.Email, &preferences, &profile.CreatedAt, &profile.UpdatedAt)
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
	err := row.Scan(&message.ID, &message.SessionID, &message.RunID, &message.Role, &message.Content, &message.CreatedAt)
	return message, err
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
	err := row.Scan(&run.ID, &run.SessionID, &run.State, &run.ModelLane, &risk, &run.StartedAt, &run.CompletedAt, &run.Summary)
	run.Risk = app.RiskLevel(risk)
	return run, err
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
	err := row.Scan(&call.ID, &call.SessionID, &call.RunID, &call.Tool, &risk, &call.Status, &args, &result, &call.Error, &call.ApprovalID, &call.StartedAt, &call.CompletedAt, &call.ObservationRef, &call.ObservationSummary)
	if err != nil {
		return app.ToolCall{}, err
	}
	call.Risk = app.RiskLevel(risk)
	call.Arguments = map[string]any{}
	_ = json.Unmarshal(args, &call.Arguments)
	call.Result = decodeJSON(result)
	return call, nil
}

func scanApproval(row scanner) (app.Approval, error) {
	var approval app.Approval
	var risk string
	var resources []byte
	var args []byte
	err := row.Scan(&approval.ID, &approval.SessionID, &approval.RunID, &approval.ToolCallID, &approval.Tool, &risk, &approval.Status, &approval.Summary, &approval.Reason, &resources, &args, &approval.CreatedAt, &approval.ResolvedAt, &approval.ResolutionNote)
	if err != nil {
		return app.Approval{}, err
	}
	approval.Risk = app.RiskLevel(risk)
	approval.Resources = []string{}
	_ = json.Unmarshal(resources, &approval.Resources)
	approval.Arguments = map[string]any{}
	_ = json.Unmarshal(args, &approval.Arguments)
	return approval, nil
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

func rollbackTx(ctx context.Context, tx pgx.Tx) {
	_ = tx.Rollback(ctx)
}
