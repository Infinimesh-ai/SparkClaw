package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) SaveExternalChatSession(ctx context.Context, session app.ExternalChatSession) (app.ExternalChatSession, error) {
	ctx, cancel := operationContext(ctx, OperationExternalChatSessionSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationExternalChatSessionSave, ctx); err != nil {
		return app.ExternalChatSession{}, err
	}
	session = prepareExternalChatSession(session, time.Now())
	databaseSession, transaction, release, err := beginPostgresTransaction(ctx, OperationExternalChatSessionSave, s.externalChatPostgres)
	if err != nil {
		return app.ExternalChatSession{}, err
	}
	defer releasePostgresSession(databaseSession, release)
	var persistedCreatedAt time.Time
	if err := transaction.QueryRow(ctx, `
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
		RETURNING created_at
	`, session.ID, session.OwnerID, session.AuthorizedOwnerID, session.AuthorizedActorID, session.WorkspaceRoot, session.BindingID, session.Channel, session.Provider,
		session.ExternalUserID, session.ExternalChatID, session.ExternalThreadID, session.DisplayName,
		session.LinkedSessionID, session.Status, session.ProviderCursor, session.LastContextToken,
		session.CreatedAt, session.UpdatedAt).Scan(&persistedCreatedAt); err != nil {
		return app.ExternalChatSession{}, finishExternalChatPostgresStatement(ctx, OperationExternalChatSessionSave, databaseSession, transaction, release, err)
	}
	session.CreatedAt = normalizeExternalChatTime(persistedCreatedAt)
	if strings.TrimSpace(session.LinkedSessionID) != "" {
		sessionUpdatedAt := normalizeSessionTime(session.UpdatedAt)
		if _, err := transaction.Exec(ctx, `
			UPDATE sessions
			SET source = $5,
			    hidden = true,
			    owner_id = CASE WHEN $3 <> '' THEN $3 ELSE owner_id END,
			    workspace_root = CASE WHEN $4 <> '' THEN $4 ELSE workspace_root END,
			    title = CASE WHEN title = '' OR title = 'New SparkClaw Session' OR title = '微信会话' THEN $6 ELSE title END,
			    updated_at = greatest(updated_at + interval '1 microsecond', $2)
			WHERE id = $1
		`, session.LinkedSessionID, sessionUpdatedAt, session.OwnerID, session.WorkspaceRoot, session.Channel, externalChatSessionTitle(session.Channel)); err != nil {
			return app.ExternalChatSession{}, finishExternalChatPostgresStatement(ctx, OperationExternalChatSessionSave, databaseSession, transaction, release, err)
		}
	}
	if err := appendExternalChatSessionLifecycle(transaction, ctx, session); err != nil {
		return app.ExternalChatSession{}, finishExternalChatPostgresStatement(ctx, OperationExternalChatSessionSave, databaseSession, transaction, release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return session, storeError(ctx, OperationExternalChatSessionSave, StoreErrorUnknownOutcome, errors.Join(err, databaseSession.Terminate(ctx)))
	}
	return session, nil
}

func (s *PostgresStore) GetExternalChatSession(ctx context.Context, id string) (app.ExternalChatSession, bool, error) {
	ctx, cancel := operationContext(ctx, OperationExternalChatSessionGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationExternalChatSessionGet, ctx); err != nil {
		return app.ExternalChatSession{}, false, err
	}
	row := s.externalChatPostgres.QueryRow(ctx, `
		SELECT id, owner_id, authorized_owner_id, authorized_actor_id, workspace_root, binding_id, channel, provider, external_user_id,
		       external_chat_id, external_thread_id, display_name, linked_session_id, status,
		       provider_cursor, last_context_token, created_at, updated_at
		FROM external_chat_sessions
		WHERE id = $1
	`, id)
	session, err := scanExternalChatSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.ExternalChatSession{}, false, nil
	}
	if err != nil {
		return app.ExternalChatSession{}, false, classifyPostgresReadError(OperationExternalChatSessionGet, ctx, err)
	}
	return normalizeExternalChatSession(session), true, nil
}

func (s *PostgresStore) ListExternalChatSessions(ctx context.Context, channel, status string) ([]app.ExternalChatSession, error) {
	ctx, cancel := operationContext(ctx, OperationExternalChatSessionList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationExternalChatSessionList, ctx); err != nil {
		return nil, err
	}
	channel = strings.ToLower(strings.TrimSpace(channel))
	status = strings.TrimSpace(status)
	rows, err := s.externalChatPostgres.Query(ctx, `
		SELECT id, owner_id, authorized_owner_id, authorized_actor_id, workspace_root, binding_id, channel, provider, external_user_id,
		       external_chat_id, external_thread_id, display_name, linked_session_id, status,
		       provider_cursor, last_context_token, created_at, updated_at
		FROM external_chat_sessions
		WHERE ($1 = '' OR channel = $1) AND ($2 = '' OR status = $2)
		ORDER BY updated_at DESC, id ASC
	`, channel, status)
	if err != nil {
		return nil, classifyPostgresReadError(OperationExternalChatSessionList, ctx, err)
	}
	defer rows.Close()
	out := make([]app.ExternalChatSession, 0)
	for rows.Next() {
		session, err := scanExternalChatSession(rows)
		if err != nil {
			return nil, classifyPostgresReadError(OperationExternalChatSessionList, ctx, err)
		}
		out = append(out, normalizeExternalChatSession(session))
	}
	if err := rows.Err(); err != nil {
		return nil, classifyPostgresReadError(OperationExternalChatSessionList, ctx, err)
	}
	return out, nil
}

func (s *PostgresStore) FindExternalChatSession(ctx context.Context, bindingID, externalChatID, externalThreadID string) (app.ExternalChatSession, bool, error) {
	ctx, cancel := operationContext(ctx, OperationExternalChatSessionFind, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationExternalChatSessionFind, ctx); err != nil {
		return app.ExternalChatSession{}, false, err
	}
	row := s.externalChatPostgres.QueryRow(ctx, `
		SELECT id, owner_id, authorized_owner_id, authorized_actor_id, workspace_root, binding_id, channel, provider, external_user_id,
		       external_chat_id, external_thread_id, display_name, linked_session_id, status,
		       provider_cursor, last_context_token, created_at, updated_at
		FROM external_chat_sessions
		WHERE binding_id = $1 AND external_chat_id = $2 AND external_thread_id = $3
		ORDER BY updated_at DESC, id ASC
		LIMIT 1
	`, bindingID, externalChatID, externalThreadID)
	session, err := scanExternalChatSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.ExternalChatSession{}, false, nil
	}
	if err != nil {
		return app.ExternalChatSession{}, false, classifyPostgresReadError(OperationExternalChatSessionFind, ctx, err)
	}
	return normalizeExternalChatSession(session), true, nil
}

func (s *PostgresStore) FindExternalChatSessionByLinkedSessionID(ctx context.Context, sessionID string) (app.ExternalChatSession, bool, error) {
	ctx, cancel := operationContext(ctx, OperationExternalChatSessionFindLink, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationExternalChatSessionFindLink, ctx); err != nil {
		return app.ExternalChatSession{}, false, err
	}
	row := s.externalChatPostgres.QueryRow(ctx, `
		SELECT id, owner_id, authorized_owner_id, authorized_actor_id, workspace_root, binding_id, channel, provider, external_user_id,
		       external_chat_id, external_thread_id, display_name, linked_session_id, status,
		       provider_cursor, last_context_token, created_at, updated_at
		FROM external_chat_sessions
		WHERE linked_session_id = $1
		ORDER BY updated_at DESC, id ASC
		LIMIT 1
	`, sessionID)
	session, err := scanExternalChatSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.ExternalChatSession{}, false, nil
	}
	if err != nil {
		return app.ExternalChatSession{}, false, classifyPostgresReadError(OperationExternalChatSessionFindLink, ctx, err)
	}
	return normalizeExternalChatSession(session), true, nil
}

func (s *PostgresStore) SaveExternalChatMessage(ctx context.Context, message app.ExternalChatMessage) (app.ExternalChatMessage, error) {
	ctx, cancel := operationContext(ctx, OperationExternalChatMessageSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationExternalChatMessageSave, ctx); err != nil {
		return app.ExternalChatMessage{}, err
	}
	databaseSession, transaction, release, err := beginPostgresTransaction(ctx, OperationExternalChatMessageSave, s.externalChatPostgres)
	if err != nil {
		return app.ExternalChatMessage{}, err
	}
	defer releasePostgresSession(databaseSession, release)
	channel := ""
	if message.Channel == "" && strings.TrimSpace(message.ChatSessionID) != "" {
		row := transaction.QueryRow(ctx, `SELECT channel FROM external_chat_sessions WHERE id = $1`, message.ChatSessionID)
		if err := row.Scan(&channel); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return app.ExternalChatMessage{}, finishExternalChatPostgresStatement(ctx, OperationExternalChatMessageSave, databaseSession, transaction, release, err)
		}
	}
	message = prepareExternalChatMessage(message, channel, time.Now())
	var persistedCreatedAt time.Time
	if err := transaction.QueryRow(ctx, `
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
		RETURNING created_at
	`, message.ID, message.ChatSessionID, message.BindingID, message.Channel, message.Direction, message.Role,
		message.ExternalMessageID, message.Content, message.ContextToken, message.LinkedRunID,
		message.Status, message.Error, message.PendingReplyKind, message.PendingReply, message.DispatchAttempts,
		message.CreatedAt, message.UpdatedAt).Scan(&persistedCreatedAt); err != nil {
		return app.ExternalChatMessage{}, finishExternalChatPostgresStatement(ctx, OperationExternalChatMessageSave, databaseSession, transaction, release, err)
	}
	message.CreatedAt = normalizeExternalChatTime(persistedCreatedAt)
	if err := appendExternalChatMessageLifecycle(transaction, ctx, message); err != nil {
		return app.ExternalChatMessage{}, finishExternalChatPostgresStatement(ctx, OperationExternalChatMessageSave, databaseSession, transaction, release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return message, storeError(ctx, OperationExternalChatMessageSave, StoreErrorUnknownOutcome, errors.Join(err, databaseSession.Terminate(ctx)))
	}
	return message, nil
}

func (s *PostgresStore) GetExternalChatMessage(ctx context.Context, id string) (app.ExternalChatMessage, bool, error) {
	ctx, cancel := operationContext(ctx, OperationExternalChatMessageGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationExternalChatMessageGet, ctx); err != nil {
		return app.ExternalChatMessage{}, false, err
	}
	row := s.externalChatPostgres.QueryRow(ctx, `
		SELECT id, chat_session_id, binding_id, channel, direction, role, external_message_id,
		       content, context_token, linked_run_id, status, error,
		       pending_reply_kind, pending_reply, dispatch_attempts, created_at, updated_at
		FROM external_chat_messages
		WHERE id = $1
	`, id)
	message, err := scanExternalChatMessage(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.ExternalChatMessage{}, false, nil
	}
	if err != nil {
		return app.ExternalChatMessage{}, false, classifyPostgresReadError(OperationExternalChatMessageGet, ctx, err)
	}
	return normalizeExternalChatMessage(message), true, nil
}

func (s *PostgresStore) FindExternalChatMessageByExternalID(ctx context.Context, chatSessionID, externalMessageID string) (app.ExternalChatMessage, bool, error) {
	ctx, cancel := operationContext(ctx, OperationExternalChatMessageFind, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationExternalChatMessageFind, ctx); err != nil {
		return app.ExternalChatMessage{}, false, err
	}
	if strings.TrimSpace(externalMessageID) == "" {
		return app.ExternalChatMessage{}, false, nil
	}
	row := s.externalChatPostgres.QueryRow(ctx, `
		SELECT id, chat_session_id, binding_id, channel, direction, role, external_message_id,
		       content, context_token, linked_run_id, status, error,
		       pending_reply_kind, pending_reply, dispatch_attempts, created_at, updated_at
		FROM external_chat_messages
		WHERE chat_session_id = $1 AND external_message_id = $2
		ORDER BY created_at DESC, id ASC
		LIMIT 1
	`, chatSessionID, externalMessageID)
	message, err := scanExternalChatMessage(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.ExternalChatMessage{}, false, nil
	}
	if err != nil {
		return app.ExternalChatMessage{}, false, classifyPostgresReadError(OperationExternalChatMessageFind, ctx, err)
	}
	return normalizeExternalChatMessage(message), true, nil
}

func (s *PostgresStore) ListExternalChatMessages(ctx context.Context, chatSessionID string, limit int) ([]app.ExternalChatMessage, error) {
	ctx, cancel := operationContext(ctx, OperationExternalChatMessageList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationExternalChatMessageList, ctx); err != nil {
		return nil, err
	}
	query := `
		SELECT id, chat_session_id, binding_id, channel, direction, role, external_message_id,
		       content, context_token, linked_run_id, status, error,
		       pending_reply_kind, pending_reply, dispatch_attempts, created_at, updated_at
		FROM external_chat_messages
		WHERE ($1 = '' OR chat_session_id = $1)
		ORDER BY created_at ASC, id ASC
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
				ORDER BY created_at DESC, id DESC
				LIMIT $2
			) recent
			ORDER BY created_at ASC, id ASC
		`
		args = append(args, limit)
	}
	rows, err := s.externalChatPostgres.Query(ctx, query, args...)
	if err != nil {
		return nil, classifyPostgresReadError(OperationExternalChatMessageList, ctx, err)
	}
	defer rows.Close()
	out := make([]app.ExternalChatMessage, 0)
	for rows.Next() {
		message, err := scanExternalChatMessage(rows)
		if err != nil {
			return nil, classifyPostgresReadError(OperationExternalChatMessageList, ctx, err)
		}
		out = append(out, normalizeExternalChatMessage(message))
	}
	if err := rows.Err(); err != nil {
		return nil, classifyPostgresReadError(OperationExternalChatMessageList, ctx, err)
	}
	return out, nil
}

func appendExternalChatSessionLifecycle(transaction onboardingPostgresTx, ctx context.Context, session app.ExternalChatSession) error {
	eventType := "external_chat_session." + session.Status
	at := normalizeExternalChatTime(time.Now())
	if _, err := transaction.Exec(ctx, `
		INSERT INTO audit_events (id, happened_at, type, session_id, run_id, actor, summary, fields)
		VALUES ($1, $2, $3, nullif($4, ''), NULL, 'gateway', $5, $6)
	`, app.NewID("audit"), at, eventType, session.LinkedSessionID, redactPostgresExternalID(session.ExternalUserID), optionalJSON(map[string]any{
		"chat_session_id": session.ID, "binding_id": session.BindingID, "channel": session.Channel, "provider": session.Provider,
	})); err != nil {
		return err
	}
	_, err := transaction.Exec(ctx, `
		INSERT INTO events (id, happened_at, type, session_id, run_id, payload)
		VALUES ($1, $2, $3, nullif($4, ''), NULL, $5)
	`, app.NewID("evt"), at, eventType, session.LinkedSessionID, mustJSON(session))
	return err
}

func appendExternalChatMessageLifecycle(transaction onboardingPostgresTx, ctx context.Context, message app.ExternalChatMessage) error {
	eventType := "external_chat_message." + message.Status
	at := normalizeExternalChatTime(time.Now())
	if _, err := transaction.Exec(ctx, `
		INSERT INTO audit_events (id, happened_at, type, session_id, run_id, actor, summary, fields)
		VALUES ($1, $2, $3, NULL, nullif($4, ''), 'gateway', $5, $6)
	`, app.NewID("audit"), at, eventType, message.LinkedRunID, message.Direction, optionalJSON(map[string]any{
		"message_id": message.ID, "chat_session_id": message.ChatSessionID, "binding_id": message.BindingID,
		"channel": message.Channel, "direction": message.Direction, "role": message.Role,
	})); err != nil {
		return err
	}
	_, err := transaction.Exec(ctx, `
		INSERT INTO events (id, happened_at, type, session_id, run_id, payload)
		VALUES ($1, $2, $3, NULL, nullif($4, ''), $5)
	`, app.NewID("evt"), at, eventType, message.LinkedRunID, mustJSON(message))
	return err
}

func finishExternalChatPostgresStatement(ctx context.Context, operation StoreOperation, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	cause = rollbackPostgresTransaction(ctx, session, transaction, release, cause)
	return classifyPostgresReadError(operation, ctx, cause)
}
