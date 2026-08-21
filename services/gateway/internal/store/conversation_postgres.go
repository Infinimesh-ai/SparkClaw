package store

import (
	"context"
	"errors"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *PostgresStore) AddMessage(ctx context.Context, message app.Message) (app.Message, error) {
	ctx, cancel := operationContext(ctx, OperationConversationAddMessage, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationConversationAddMessage, ctx); err != nil {
		return app.Message{}, err
	}
	candidate, err := prepareMessage(message, time.Now())
	if err != nil {
		return app.Message{}, storeError(ctx, OperationConversationAddMessage, StoreErrorInvalid, err)
	}
	if existing, err := scanMessage(s.conversationPostgres.QueryRow(ctx, `
		SELECT id, session_id, coalesce(run_id, ''), role, content, attachments, requested_media, created_at
		FROM messages WHERE id = $1
	`, candidate.ID)); err == nil {
		return cloneMessage(existing), nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return app.Message{}, classifyConversationPostgresError(OperationConversationAddMessage, ctx, err)
	}

	session, transaction, release, err := beginPostgresTransaction(ctx, OperationConversationAddMessage, s.conversationPostgres)
	if err != nil {
		return app.Message{}, err
	}
	defer func() {
		if *release {
			session.Release()
		}
	}()
	current, err := scanSession(transaction.QueryRow(ctx, sessionSelectSQL+` WHERE id=$1 FOR UPDATE`, candidate.SessionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.Message{}, conversationBusinessError(ctx, OperationConversationAddMessage, StoreErrorNotFound, session, transaction, release, errors.New("message session not found"))
	}
	if err != nil {
		return app.Message{}, finishConversationStatement(ctx, OperationConversationAddMessage, candidate, session, transaction, release, err)
	}
	if err := validatePersistedSession(candidate.SessionID, current); err != nil {
		return app.Message{}, conversationBusinessError(ctx, OperationConversationAddMessage, StoreErrorCorrupt, session, transaction, release, err)
	}
	stored, err := scanMessage(transaction.QueryRow(ctx, `
		INSERT INTO messages (id, session_id, run_id, role, content, attachments, requested_media, created_at)
		VALUES ($1, $2, nullif($3, ''), $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO NOTHING
		RETURNING id, session_id, coalesce(run_id, ''), role, content, attachments, requested_media, created_at
	`, candidate.ID, candidate.SessionID, candidate.RunID, candidate.Role, candidate.Content,
		mustJSON(candidate.Attachments), mustJSON(candidate.RequestedMedia), candidate.CreatedAt))
	if errors.Is(err, pgx.ErrNoRows) {
		existing, readErr := scanMessage(transaction.QueryRow(ctx, `
			SELECT id, session_id, coalesce(run_id, ''), role, content, attachments, requested_media, created_at
			FROM messages WHERE id = $1
		`, candidate.ID))
		if readErr != nil {
			return app.Message{}, finishConversationStatement(ctx, OperationConversationAddMessage, candidate, session, transaction, release, readErr)
		}
		if rollbackErr := rollbackPostgresTransaction(ctx, session, transaction, release, nil); rollbackErr != nil {
			return app.Message{}, classifyConversationPostgresError(OperationConversationAddMessage, ctx, rollbackErr)
		}
		return cloneMessage(existing), nil
	}
	if err != nil {
		return app.Message{}, finishConversationStatement(ctx, OperationConversationAddMessage, candidate, session, transaction, release, err)
	}
	current.UpdatedAt = nextSessionTime(stored.CreatedAt, current.UpdatedAt)
	if !current.Hidden && (current.Title == "" || current.Title == "New SparkClaw Session") {
		current.Title = deriveTitle(stored.Content)
	}
	if _, err := transaction.Exec(ctx, `UPDATE sessions SET title=$2, updated_at=$3 WHERE id=$1`, current.ID, current.Title, current.UpdatedAt); err != nil {
		return app.Message{}, finishConversationStatement(ctx, OperationConversationAddMessage, stored, session, transaction, release, err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO events (id, happened_at, type, session_id, run_id, payload)
		VALUES ($1, $2, 'message.created', $3, nullif($4, ''), $5)
	`, app.NewID("evt"), normalizeSessionTime(time.Now()), stored.SessionID, stored.RunID, mustJSON(stored)); err != nil {
		return app.Message{}, finishConversationStatement(ctx, OperationConversationAddMessage, stored, session, transaction, release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return cloneMessage(stored), storeError(ctx, OperationConversationAddMessage, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return cloneMessage(stored), nil
}

func (s *PostgresStore) ListMessages(ctx context.Context, sessionID string) ([]app.Message, error) {
	ctx, cancel := operationContext(ctx, OperationConversationListMessages, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationConversationListMessages, ctx); err != nil {
		return nil, err
	}
	rows, err := s.conversationPostgres.Query(ctx, `
		SELECT id, session_id, coalesce(run_id, ''), role, content, attachments, requested_media, created_at
		FROM messages
		WHERE session_id = $1
		ORDER BY created_at ASC, id ASC
	`, sessionID)
	if err != nil {
		return nil, classifyConversationPostgresError(OperationConversationListMessages, ctx, err)
	}
	defer rows.Close()
	out := make([]app.Message, 0)
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return nil, classifyConversationPostgresError(OperationConversationListMessages, ctx, err)
		}
		out = append(out, cloneMessage(message))
	}
	if err := rows.Err(); err != nil {
		return nil, classifyConversationPostgresError(OperationConversationListMessages, ctx, err)
	}
	return out, nil
}

func beginPostgresTransaction(ctx context.Context, operation StoreOperation, backend ownerPostgresOps) (onboardingPostgresSession, onboardingPostgresTx, *bool, error) {
	session, err := backend.Acquire(ctx)
	if err != nil {
		return nil, nil, nil, classifyPostgresPreTransaction(operation, ctx, err)
	}
	release := true
	transaction, err := session.Begin(ctx, pgx.TxOptions{})
	if err == nil {
		return session, transaction, &release, nil
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) || pgconn.SafeToRetry(err) {
		session.Release()
		if postgresError != nil {
			return nil, nil, nil, storeError(ctx, operation, StoreErrorInternal, err)
		}
		return nil, nil, nil, classifyPostgresPreTransaction(operation, ctx, err)
	}
	return nil, nil, nil, storeError(ctx, operation, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
}

func rollbackPostgresTransaction(ctx context.Context, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	if rollbackErr := transaction.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
		*release = false
		return errors.Join(cause, rollbackErr, session.Terminate(ctx))
	}
	return cause
}

func finishConversationStatement(ctx context.Context, operation StoreOperation, _ app.Message, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	cause = rollbackPostgresTransaction(ctx, session, transaction, release, cause)
	return classifyConversationPostgresError(operation, ctx, cause)
}

func conversationBusinessError(ctx context.Context, operation StoreOperation, code StoreErrorCode, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	return storeError(ctx, operation, code, rollbackPostgresTransaction(ctx, session, transaction, release, cause))
}

func classifyConversationPostgresError(operation StoreOperation, ctx context.Context, cause error) error {
	if errors.Is(cause, errMessageJSONDecode) || errors.Is(cause, errEventPayloadJSONDecode) {
		return storeError(ctx, operation, StoreErrorCorrupt, cause)
	}
	if errors.Is(cause, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) ||
		errors.Is(cause, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return contextStoreError(operation, ctx, cause)
	}
	var postgresError *pgconn.PgError
	if errors.As(cause, &postgresError) {
		return storeError(ctx, operation, StoreErrorInternal, cause)
	}
	return storeError(ctx, operation, StoreErrorUnavailable, cause)
}
