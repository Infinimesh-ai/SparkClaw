package store

import (
	"context"
	"errors"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *PostgresStore) AddAudit(ctx context.Context, event app.AuditEvent) error {
	ctx, cancel := operationContext(ctx, OperationAuditAdd, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationAuditAdd, ctx); err != nil {
		return err
	}
	prepared, err := prepareAuditEvent(event, time.Now().UTC())
	if err != nil {
		return storeError(ctx, OperationAuditAdd, StoreErrorInvalid, err)
	}
	_, err = s.auditPostgres.Exec(ctx, `
		INSERT INTO audit_events (id, happened_at, type, session_id, run_id, actor, summary, fields)
		VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''), $6, $7, $8)
	`, prepared.ID, prepared.Time, prepared.Type, prepared.SessionID, prepared.RunID, prepared.Actor, prepared.Summary, optionalJSON(prepared.Fields))
	if err != nil {
		return classifyAuditPostgresError(OperationAuditAdd, ctx, err)
	}
	return nil
}

func (s *PostgresStore) ListAudit(ctx context.Context, sessionID string) ([]app.AuditEvent, error) {
	ctx, cancel := operationContext(ctx, OperationAuditList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationAuditList, ctx); err != nil {
		return nil, err
	}
	rows, err := s.auditPostgres.Query(ctx, `
		SELECT id, happened_at, type, coalesce(session_id, ''), coalesce(run_id, ''), actor, summary, fields
		FROM audit_events
		WHERE $1 = '' OR session_id = $1
		ORDER BY happened_at DESC, id ASC
	`, sessionID)
	if err != nil {
		return nil, classifyAuditPostgresError(OperationAuditList, ctx, err)
	}
	defer rows.Close()
	events := []app.AuditEvent{}
	for rows.Next() {
		event, err := scanAuditEvent(rows)
		if err != nil {
			return nil, classifyAuditPostgresError(OperationAuditList, ctx, err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyAuditPostgresError(OperationAuditList, ctx, err)
	}
	return events, nil
}

func (s *PostgresStore) EventsAfter(ctx context.Context, sessionID, after string) ([]app.Event, error) {
	ctx, cancel := operationContext(ctx, OperationAuditEventsAfter, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationAuditEventsAfter, ctx); err != nil {
		return nil, err
	}
	var afterSeq int64
	if after != "" {
		err := s.auditPostgres.QueryRow(ctx, `SELECT seq FROM events WHERE id = $1`, after).Scan(&afterSeq)
		if errors.Is(err, pgx.ErrNoRows) {
			return []app.Event{}, nil
		}
		if err != nil {
			return nil, classifyAuditPostgresError(OperationAuditEventsAfter, ctx, err)
		}
	}
	rows, err := s.auditPostgres.Query(ctx, `
		SELECT id, happened_at, type, coalesce(session_id, ''), coalesce(run_id, ''), payload
		FROM events
		WHERE seq > $1 AND ($2 = '' OR session_id = $2)
		ORDER BY seq ASC
	`, afterSeq, sessionID)
	if err != nil {
		return nil, classifyAuditPostgresError(OperationAuditEventsAfter, ctx, err)
	}
	defer rows.Close()
	events := []app.Event{}
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, classifyAuditPostgresError(OperationAuditEventsAfter, ctx, err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyAuditPostgresError(OperationAuditEventsAfter, ctx, err)
	}
	return events, nil
}

func classifyAuditPostgresError(operation StoreOperation, ctx context.Context, cause error) error {
	if errors.Is(cause, errAuditFieldsJSONDecode) || errors.Is(cause, errEventPayloadJSONDecode) {
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

func (s *PostgresStore) MessageEventHead(ctx context.Context, sessionID string) (string, error) {
	ctx, cancel := operationContext(ctx, OperationConversationMessageHead, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationConversationMessageHead, ctx); err != nil {
		return "", err
	}
	var cursor string
	err := s.conversationPostgres.QueryRow(ctx, `
		SELECT id
		FROM events
		WHERE session_id = $1 AND type = 'message.created'
		ORDER BY seq DESC
		LIMIT 1
	`, sessionID).Scan(&cursor)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", classifyConversationPostgresError(OperationConversationMessageHead, ctx, err)
	}
	return cursor, nil
}

func (s *PostgresStore) MessageEventsAfter(ctx context.Context, sessionID, after string, limit int) (MessageEventPage, error) {
	ctx, cancel := operationContext(ctx, OperationConversationMessagesAfter, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationConversationMessagesAfter, ctx); err != nil {
		return MessageEventPage{}, err
	}
	if limit <= 0 || limit > MessageEventPageLimit {
		limit = MessageEventPageLimit
	}
	var afterSeq int64
	if after != "" {
		var cursorSessionID, cursorType string
		err := s.conversationPostgres.QueryRow(ctx, `
			SELECT seq, coalesce(session_id, ''), type
			FROM events
			WHERE id = $1
		`, after).Scan(&afterSeq, &cursorSessionID, &cursorType)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && (cursorSessionID != sessionID || cursorType != "message.created") {
			return MessageEventPage{}, storeError(ctx, OperationConversationMessagesAfter, StoreErrorInvalid, ErrMessageEventCursorInvalid)
		}
		if err != nil {
			return MessageEventPage{}, classifyConversationPostgresError(OperationConversationMessagesAfter, ctx, err)
		}
	}

	rows, err := s.conversationPostgres.Query(ctx, `
		SELECT id, happened_at, type, coalesce(session_id, ''), coalesce(run_id, ''), payload
		FROM events
		WHERE seq > $1 AND session_id = $2 AND type = 'message.created'
		ORDER BY seq ASC
		LIMIT $3
	`, afterSeq, sessionID, limit+1)
	if err != nil {
		return MessageEventPage{}, classifyConversationPostgresError(OperationConversationMessagesAfter, ctx, err)
	}
	defer rows.Close()
	events := make([]app.Event, 0, limit+1)
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return MessageEventPage{}, classifyConversationPostgresError(OperationConversationMessagesAfter, ctx, err)
		}
		events = append(events, cloneClientLifecycleEvent(event))
	}
	if err := rows.Err(); err != nil {
		return MessageEventPage{}, classifyConversationPostgresError(OperationConversationMessagesAfter, ctx, err)
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
