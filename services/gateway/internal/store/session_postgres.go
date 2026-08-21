package store

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const sessionSelectSQL = `SELECT id, owner_id, workspace_root, title, source, hidden, created_at, updated_at FROM sessions`

const sessionDeleteClosureSQL = `SELECT NOT (
	EXISTS (SELECT 1 FROM messages WHERE session_id=$1) OR
	EXISTS (SELECT 1 FROM agent_runs WHERE session_id=$1) OR
	EXISTS (SELECT 1 FROM run_feedback WHERE session_id=$1) OR
	EXISTS (SELECT 1 FROM model_calls WHERE session_id=$1) OR
	EXISTS (SELECT 1 FROM tool_calls WHERE session_id=$1) OR
	EXISTS (SELECT 1 FROM document_records WHERE session_id=$1) OR
	EXISTS (SELECT 1 FROM approvals WHERE session_id=$1) OR
	EXISTS (SELECT 1 FROM reminders WHERE session_id=$1) OR
	EXISTS (SELECT 1 FROM memory_candidates WHERE session_id=$1) OR
	EXISTS (SELECT 1 FROM browser_login_blocks WHERE session_id=$1) OR
	EXISTS (SELECT 1 FROM artifact_objects WHERE session_id=$1) OR
	EXISTS (SELECT 1 FROM episode_summaries WHERE session_id=$1) OR
	EXISTS (SELECT 1 FROM external_chat_sessions WHERE linked_session_id=$1) OR
	EXISTS (SELECT 1 FROM weixin_chat_sessions WHERE linked_session_id=$1) OR
	EXISTS (SELECT 1 FROM audit_events WHERE session_id=$1) OR
	EXISTS (SELECT 1 FROM events WHERE session_id=$1) OR
	EXISTS (SELECT 1 FROM external_chat_messages AS message LEFT JOIN external_chat_sessions AS chat ON chat.id=message.chat_session_id WHERE chat.id IS NULL) OR
	EXISTS (SELECT 1 FROM weixin_chat_messages AS message LEFT JOIN weixin_chat_sessions AS chat ON chat.id=message.chat_session_id WHERE chat.id IS NULL)
)`

type sessionDeleteStatement struct {
	sql           string
	requireOneRow bool
}

var sessionDeleteStatements = []sessionDeleteStatement{
	{sql: `DELETE FROM reminder_deliveries WHERE reminder_id IN (SELECT id FROM reminders WHERE session_id=$1)`},
	{sql: `DELETE FROM run_feedback WHERE session_id=$1`},
	{sql: `DELETE FROM approvals WHERE session_id=$1`},
	{sql: `DELETE FROM document_records WHERE session_id=$1`},
	{sql: `DELETE FROM memory_candidates WHERE session_id=$1`},
	{sql: `DELETE FROM memories WHERE source_run_id IN (SELECT id FROM agent_runs WHERE session_id=$1)`},
	{sql: `DELETE FROM episode_summaries WHERE session_id=$1`},
	{sql: `DELETE FROM artifact_objects WHERE session_id=$1`},
	{sql: `DELETE FROM weixin_chat_messages WHERE chat_session_id IN (SELECT id FROM weixin_chat_sessions WHERE linked_session_id=$1)`},
	{sql: `DELETE FROM external_chat_messages WHERE chat_session_id IN (SELECT id FROM external_chat_sessions WHERE linked_session_id=$1)`},
	{sql: `DELETE FROM weixin_chat_sessions WHERE linked_session_id=$1`},
	{sql: `DELETE FROM external_chat_sessions WHERE linked_session_id=$1`},
	{sql: `DELETE FROM browser_login_blocks WHERE session_id=$1`},
	{sql: `DELETE FROM tool_calls WHERE session_id=$1`},
	{sql: `DELETE FROM model_calls WHERE session_id=$1`},
	{sql: `DELETE FROM reminders WHERE session_id=$1`},
	{sql: `DELETE FROM messages WHERE session_id=$1`},
	{sql: `DELETE FROM agent_runs WHERE session_id=$1`},
	{sql: `DELETE FROM audit_events WHERE session_id=$1`},
	{sql: `DELETE FROM events WHERE session_id=$1`},
	{sql: `DELETE FROM sessions WHERE id=$1`, requireOneRow: true},
}

func (s *PostgresStore) validateSessionState(ctx context.Context) error {
	startupCtx, cancel := postgresMigrationStartupContext(ctx)
	defer cancel()
	rows, err := s.sessionPostgres.Query(startupCtx, sessionSelectSQL)
	if err != nil {
		return fmt.Errorf("validate session state: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return fmt.Errorf("validate session state: %w", err)
		}
		if err := validatePersistedSession(session.ID, session); err != nil {
			return fmt.Errorf("validate session state: %w", err)
		}
		if session.UpdatedAt.After(s.sessionWriteHighWater[session.ID]) {
			s.sessionWriteHighWater[session.ID] = session.UpdatedAt
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("validate session state: %w", err)
	}
	return nil
}

func (s *PostgresStore) CreateSession(ctx context.Context, title string) (app.Session, error) {
	return s.createPostgresSession(ctx, OperationSessionCreate, title, app.DefaultOwnerID, "", "webchat", false)
}

func (s *PostgresStore) CreateSessionWithScope(ctx context.Context, title, ownerID, workspaceRoot, source string, hidden bool) (app.Session, error) {
	return s.createPostgresSession(ctx, OperationSessionCreateWithScope, title, ownerID, workspaceRoot, source, hidden)
}

func (s *PostgresStore) createPostgresSession(ctx context.Context, operation StoreOperation, title, ownerID, workspaceRoot, source string, hidden bool) (app.Session, error) {
	ctx, cancel := operationContext(ctx, operation, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(operation, ctx); err != nil {
		return app.Session{}, err
	}
	releaseCommand, err := s.acquireSessionCommand(ctx, operation)
	if err != nil {
		return app.Session{}, err
	}
	defer releaseCommand()
	candidate, err := prepareSession(title, ownerID, workspaceRoot, source, hidden, s.sessionNow())
	if err != nil {
		return app.Session{}, storeError(operation, StoreErrorInvalid, err)
	}
	s.sessionWriteHighWater[candidate.ID] = candidate.UpdatedAt
	session, transaction, release, err := s.beginSessionTransaction(ctx, operation, pgx.TxOptions{})
	if err != nil {
		return app.Session{}, err
	}
	defer func() {
		if *release {
			session.Release()
		}
	}()
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, sessionAdvisoryKey(candidate.ID)); err != nil {
		return finishPostgresSessionStatement(ctx, operation, candidate, session, transaction, release, err)
	}
	stored, err := scanSession(transaction.QueryRow(ctx, `
		INSERT INTO sessions (id,owner_id,workspace_root,title,source,hidden,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id,owner_id,workspace_root,title,source,hidden,created_at,updated_at
	`, candidate.ID, candidate.OwnerID, candidate.WorkspaceRoot, candidate.Title, candidate.Source,
		candidate.Hidden, candidate.CreatedAt, candidate.UpdatedAt))
	if err != nil {
		return finishPostgresSessionStatement(ctx, operation, candidate, session, transaction, release, err)
	}
	if err := validatePersistedSession(candidate.ID, stored); err != nil || !sessionsEqual(candidate, stored) {
		if err == nil {
			err = errors.New("created session does not equal submitted candidate")
		}
		return app.Session{}, sessionBusinessError(ctx, operation, StoreErrorCorrupt, session, transaction, release, err)
	}
	if err := insertSessionLifecycle(ctx, transaction, "session.created", candidate.ID, "system", "Session created", map[string]any{
		"title": candidate.Title, "owner_id": candidate.OwnerID,
	}, candidate); err != nil {
		return finishPostgresSessionStatement(ctx, operation, candidate, session, transaction, release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return candidate, storeError(operation, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return candidate, nil
}

func (s *PostgresStore) ListSessions(ctx context.Context) ([]app.Session, error) {
	ctx, cancel := operationContext(ctx, OperationSessionList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationSessionList, ctx); err != nil {
		return nil, err
	}
	session, transaction, release, err := s.beginSessionTransaction(ctx, OperationSessionList, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() {
		if *release {
			session.Release()
		}
	}()
	rows, err := transaction.Query(ctx, sessionSelectSQL+` WHERE hidden=false ORDER BY updated_at DESC, id ASC`)
	if err != nil {
		return nil, finishPostgresSessionRead(ctx, OperationSessionList, session, transaction, release, err)
	}
	defer rows.Close()
	out := make([]app.Session, 0)
	for rows.Next() {
		candidate, err := scanSession(rows)
		if err != nil {
			return nil, finishPostgresSessionRead(ctx, OperationSessionList, session, transaction, release, err)
		}
		if err := validatePersistedSession(candidate.ID, candidate); err != nil {
			return nil, sessionBusinessError(ctx, OperationSessionList, StoreErrorCorrupt, session, transaction, release, err)
		}
		out = append(out, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, finishPostgresSessionRead(ctx, OperationSessionList, session, transaction, release, err)
	}
	if err := commitPostgresSessionRead(ctx, OperationSessionList, session, transaction, release); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *PostgresStore) GetSession(ctx context.Context, id string) (app.Session, bool, error) {
	ctx, cancel := operationContext(ctx, OperationSessionGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationSessionGet, ctx); err != nil {
		return app.Session{}, false, err
	}
	if id == "" {
		return app.Session{}, false, nil
	}
	session, transaction, release, err := s.beginSessionTransaction(ctx, OperationSessionGet, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return app.Session{}, false, err
	}
	defer func() {
		if *release {
			session.Release()
		}
	}()
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, sessionAdvisoryKey(id)); err != nil {
		return app.Session{}, false, finishPostgresSessionRead(ctx, OperationSessionGet, session, transaction, release, err)
	}
	candidate, err := scanSession(transaction.QueryRow(ctx, sessionSelectSQL+` WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		var complete bool
		if err := transaction.QueryRow(ctx, sessionDeleteClosureSQL, id).Scan(&complete); err != nil {
			return app.Session{}, false, finishPostgresSessionRead(ctx, OperationSessionGet, session, transaction, release, err)
		}
		if !complete {
			return app.Session{}, false, sessionBusinessError(ctx, OperationSessionGet, StoreErrorCorrupt, session, transaction, release, errors.New("session deletion closure is incomplete"))
		}
		if err := commitPostgresSessionRead(ctx, OperationSessionGet, session, transaction, release); err != nil {
			return app.Session{}, false, err
		}
		return app.Session{}, false, nil
	}
	if err != nil {
		return app.Session{}, false, finishPostgresSessionRead(ctx, OperationSessionGet, session, transaction, release, err)
	}
	if err := validatePersistedSession(id, candidate); err != nil {
		return app.Session{}, false, sessionBusinessError(ctx, OperationSessionGet, StoreErrorCorrupt, session, transaction, release, err)
	}
	if err := commitPostgresSessionRead(ctx, OperationSessionGet, session, transaction, release); err != nil {
		return app.Session{}, false, err
	}
	return candidate, true, nil
}

func (s *PostgresStore) UpdateSessionTitle(ctx context.Context, id, title string) (app.Session, error) {
	ctx, cancel := operationContext(ctx, OperationSessionUpdateTitle, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationSessionUpdateTitle, ctx); err != nil {
		return app.Session{}, err
	}
	if strings.TrimSpace(id) == "" {
		return app.Session{}, storeError(OperationSessionUpdateTitle, StoreErrorInvalid, errors.New("session ID is required"))
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return app.Session{}, storeError(OperationSessionUpdateTitle, StoreErrorInvalid, errors.New("session title is required"))
	}
	releaseCommand, err := s.acquireSessionCommand(ctx, OperationSessionUpdateTitle)
	if err != nil {
		return app.Session{}, err
	}
	defer releaseCommand()
	session, transaction, release, err := s.beginSessionTransaction(ctx, OperationSessionUpdateTitle, pgx.TxOptions{})
	if err != nil {
		return app.Session{}, err
	}
	defer func() {
		if *release {
			session.Release()
		}
	}()
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, sessionAdvisoryKey(id)); err != nil {
		return finishPostgresSessionStatement(ctx, OperationSessionUpdateTitle, app.Session{}, session, transaction, release, err)
	}
	current, err := scanSession(transaction.QueryRow(ctx, sessionSelectSQL+` WHERE id=$1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.Session{}, sessionBusinessError(ctx, OperationSessionUpdateTitle, StoreErrorNotFound, session, transaction, release, errors.New("session not found"))
	}
	if err != nil {
		return finishPostgresSessionStatement(ctx, OperationSessionUpdateTitle, app.Session{}, session, transaction, release, err)
	}
	if err := validatePersistedSession(id, current); err != nil {
		return app.Session{}, sessionBusinessError(ctx, OperationSessionUpdateTitle, StoreErrorCorrupt, session, transaction, release, err)
	}
	if current.Source == "mcp" {
		return app.Session{}, sessionBusinessError(ctx, OperationSessionUpdateTitle, StoreErrorConflict, session, transaction, release, errors.New("MCP session title is binding-owned"))
	}
	candidate := current
	candidate.Title = title
	candidate.UpdatedAt = nextSessionTime(s.sessionNow(), current.UpdatedAt, s.sessionWriteHighWater[id])
	s.sessionWriteHighWater[id] = candidate.UpdatedAt
	stored, err := scanSession(transaction.QueryRow(ctx, `
		UPDATE sessions SET title=$2,updated_at=$3 WHERE id=$1
		RETURNING id,owner_id,workspace_root,title,source,hidden,created_at,updated_at
	`, id, candidate.Title, candidate.UpdatedAt))
	if err != nil {
		return finishPostgresSessionStatement(ctx, OperationSessionUpdateTitle, candidate, session, transaction, release, err)
	}
	if !sessionsEqual(candidate, stored) {
		return app.Session{}, sessionBusinessError(ctx, OperationSessionUpdateTitle, StoreErrorCorrupt, session, transaction, release, errors.New("updated session does not equal submitted candidate"))
	}
	if err := insertSessionLifecycle(ctx, transaction, "session.updated", candidate.ID, "owner", "Session renamed", map[string]any{"title": candidate.Title}, candidate); err != nil {
		return finishPostgresSessionStatement(ctx, OperationSessionUpdateTitle, candidate, session, transaction, release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return candidate, storeError(OperationSessionUpdateTitle, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return candidate, nil
}

func (s *PostgresStore) DeleteSession(ctx context.Context, id string) (app.Session, error) {
	ctx, cancel := operationContext(ctx, OperationSessionDelete, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationSessionDelete, ctx); err != nil {
		return app.Session{}, err
	}
	if strings.TrimSpace(id) == "" {
		return app.Session{}, storeError(OperationSessionDelete, StoreErrorInvalid, errors.New("session ID is required"))
	}
	releaseCommand, err := s.acquireSessionCommand(ctx, OperationSessionDelete)
	if err != nil {
		return app.Session{}, err
	}
	defer releaseCommand()
	session, transaction, release, err := s.beginSessionTransaction(ctx, OperationSessionDelete, pgx.TxOptions{})
	if err != nil {
		return app.Session{}, err
	}
	defer func() {
		if *release {
			session.Release()
		}
	}()
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, sessionAdvisoryKey(id)); err != nil {
		return finishPostgresSessionStatement(ctx, OperationSessionDelete, app.Session{}, session, transaction, release, err)
	}
	candidate, err := scanSession(transaction.QueryRow(ctx, sessionSelectSQL+` WHERE id=$1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.Session{}, sessionBusinessError(ctx, OperationSessionDelete, StoreErrorNotFound, session, transaction, release, errors.New("session not found"))
	}
	if err != nil {
		return finishPostgresSessionStatement(ctx, OperationSessionDelete, app.Session{}, session, transaction, release, err)
	}
	if err := validatePersistedSession(id, candidate); err != nil {
		return app.Session{}, sessionBusinessError(ctx, OperationSessionDelete, StoreErrorCorrupt, session, transaction, release, err)
	}
	if candidate.Source == "mcp" {
		return app.Session{}, sessionBusinessError(ctx, OperationSessionDelete, StoreErrorConflict, session, transaction, release, errors.New("MCP session history is binding-owned"))
	}
	for _, statement := range sessionDeleteStatements {
		tag, err := transaction.Exec(ctx, statement.sql, id)
		if err != nil {
			return finishPostgresSessionStatement(ctx, OperationSessionDelete, candidate, session, transaction, release, err)
		}
		if statement.requireOneRow && tag.RowsAffected() != 1 {
			return app.Session{}, sessionBusinessError(ctx, OperationSessionDelete, StoreErrorInternal, session, transaction, release, errors.New("session delete affected an unexpected row count"))
		}
	}
	if err := insertSessionLifecycle(ctx, transaction, "session.deleted", "", "owner", "Session deleted", map[string]any{
		"session_id": candidate.ID, "title": candidate.Title,
	}, candidate); err != nil {
		return finishPostgresSessionStatement(ctx, OperationSessionDelete, candidate, session, transaction, release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return candidate, storeError(OperationSessionDelete, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return candidate, nil
}

func insertSessionLifecycle(ctx context.Context, transaction onboardingPostgresTx, typ, sessionID, actor, summary string, fields map[string]any, payload app.Session) error {
	at := payload.UpdatedAt
	if typ == "session.deleted" {
		at = normalizeSessionTime(payload.UpdatedAt)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO audit_events (id,happened_at,type,session_id,run_id,actor,summary,fields)
		VALUES ($1,$2,$3,NULLIF($4,''),NULL,$5,$6,$7)
	`, app.NewID("audit"), at, typ, sessionID, actor, summary, optionalJSON(fields)); err != nil {
		return err
	}
	_, err := transaction.Exec(ctx, `
		INSERT INTO events (id,happened_at,type,session_id,run_id,payload)
		VALUES ($1,$2,$3,NULLIF($4,''),NULL,$5)
	`, app.NewID("evt"), at, typ, sessionID, mustJSON(payload))
	return err
}

func (s *PostgresStore) acquireSessionCommand(ctx context.Context, operation StoreOperation) (func(), error) {
	if err := s.sessionCommandGate.Acquire(ctx, 1); err != nil {
		if contextErr := operationContextError(operation, ctx); contextErr != nil {
			return nil, contextErr
		}
		return nil, storeError(operation, StoreErrorUnavailable, err)
	}
	if err := operationContextError(operation, ctx); err != nil {
		s.sessionCommandGate.Release(1)
		return nil, err
	}
	return func() { s.sessionCommandGate.Release(1) }, nil
}

func (s *PostgresStore) beginSessionTransaction(ctx context.Context, operation StoreOperation, options pgx.TxOptions) (onboardingPostgresSession, onboardingPostgresTx, *bool, error) {
	session, err := s.sessionPostgres.Acquire(ctx)
	if err != nil {
		return nil, nil, nil, classifyPostgresPreTransaction(operation, ctx, err)
	}
	release := true
	transaction, err := session.Begin(ctx, options)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) || pgconn.SafeToRetry(err) {
			session.Release()
			if postgresError != nil {
				return nil, nil, nil, storeError(operation, StoreErrorInternal, err)
			}
			return nil, nil, nil, classifyPostgresPreTransaction(operation, ctx, err)
		}
		return nil, nil, nil, storeError(operation, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return session, transaction, &release, nil
}

func commitPostgresSessionRead(ctx context.Context, operation StoreOperation, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool) error {
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return storeError(operation, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return nil
}

func finishPostgresSessionRead(ctx context.Context, operation StoreOperation, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	cause = rollbackPostgresOnboardingRead(ctx, session, transaction, release, cause)
	var postgresError *pgconn.PgError
	if errors.As(cause, &postgresError) {
		return storeError(operation, StoreErrorInternal, cause)
	}
	return classifyPostgresReadError(operation, ctx, cause)
}

func finishPostgresSessionStatement(ctx context.Context, operation StoreOperation, candidate app.Session, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) (app.Session, error) {
	var postgresError *pgconn.PgError
	if errors.As(cause, &postgresError) || pgconn.SafeToRetry(cause) {
		cause = rollbackPostgresOnboardingRead(ctx, session, transaction, release, cause)
		if postgresError != nil && postgresError.Code == "23505" {
			return app.Session{}, storeError(operation, StoreErrorConflict, cause)
		}
		if postgresError != nil {
			return app.Session{}, storeError(operation, StoreErrorInternal, cause)
		}
		return app.Session{}, classifyPostgresPreTransaction(operation, ctx, cause)
	}
	*release = false
	return candidate, storeError(operation, StoreErrorUnknownOutcome, errors.Join(cause, session.Terminate(ctx)))
}

func sessionBusinessError(ctx context.Context, operation StoreOperation, code StoreErrorCode, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	return storeError(operation, code, rollbackPostgresOnboardingRead(ctx, session, transaction, release, cause))
}

func sessionAdvisoryKey(id string) int64 {
	digest := sha256.Sum256([]byte("sparkclaw/store/session/v1\x00" + id))
	return int64(binary.BigEndian.Uint64(digest[:8]))
}
