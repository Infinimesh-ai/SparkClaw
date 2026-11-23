package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const syncSessionColumns = `id, owner_id, workspace_root, title, source, hidden, created_at, updated_at, revision, last_seq, preview, archived, origin_device_id`

func scanSyncSession(row scanner) (app.Session, error) {
	var session app.Session
	err := row.Scan(&session.ID, &session.OwnerID, &session.WorkspaceRoot, &session.Title, &session.Source, &session.Hidden, &session.CreatedAt, &session.UpdatedAt, &session.Revision, &session.LastSeq, &session.Preview, &session.Archived, &session.OriginDeviceID)
	session.CreatedAt = session.CreatedAt.UTC()
	session.UpdatedAt = session.UpdatedAt.UTC()
	return session, err
}

const syncMessageColumns = `id, session_id, coalesce(run_id, ''), role, content, attachments, requested_media, created_at, clarification, files_unavailable, seq, coalesce(client_message_id, '')`

func scanSyncMessage(row scanner) (app.Message, error) {
	var seq int64
	var client string
	message, err := scanMessage(messageWithExtra{row, &seq, &client})
	message.Seq = seq
	message.ClientMessageID = client
	message.CreatedAt = message.CreatedAt.UTC()
	return message, err
}

// messageWithExtra lets scanMessage read its ten columns and appends the
// two sync columns.
type messageWithExtra struct {
	row    scanner
	seq    *int64
	client *string
}

func (m messageWithExtra) Scan(dest ...any) error {
	return m.row.Scan(append(dest, m.seq, m.client)...)
}

func (s *PostgresStore) SessionChanges(ctx context.Context, ownerID, deviceID string, cursor int64, limit int) ([]app.SessionChange, error) {
	ctx, cancel := operationContext(ctx, OperationConversationSyncChanges, s.operationTimeouts)
	defer cancel()
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.sessionPostgres.Query(ctx, `
		SELECT `+syncSessionColumns+`, false AS deleted, NULL::timestamptz AS deleted_at FROM sessions
		 WHERE owner_id = $1 AND ($4 = '*' OR origin_device_id = $4) AND hidden = false AND revision > $2
		UNION ALL
		SELECT session_id, owner_id, '', '', '', false, deleted_at, deleted_at, revision, 0, '', false, origin_device_id, true, deleted_at FROM session_tombstones
		 WHERE owner_id = $1 AND ($4 = '*' OR origin_device_id = $4) AND revision > $2
		ORDER BY 9
		LIMIT $3`, ownerID, cursor, limit, deviceID)
	if err != nil {
		return nil, classifyConversationPostgresError(OperationConversationSyncChanges, ctx, err)
	}
	defer rows.Close()
	out := make([]app.SessionChange, 0)
	for rows.Next() {
		var change app.SessionChange
		var deletedAt *time.Time
		sc := change.Session
		if err := rows.Scan(&sc.ID, &sc.OwnerID, &sc.WorkspaceRoot, &sc.Title, &sc.Source, &sc.Hidden, &sc.CreatedAt, &sc.UpdatedAt, &sc.Revision, &sc.LastSeq, &sc.Preview, &sc.Archived, &sc.OriginDeviceID, &change.Deleted, &deletedAt); err != nil {
			return nil, classifyConversationPostgresError(OperationConversationSyncChanges, ctx, err)
		}
		sc.CreatedAt = sc.CreatedAt.UTC()
		sc.UpdatedAt = sc.UpdatedAt.UTC()
		if deletedAt != nil {
			change.DeletedAt = deletedAt.UTC()
		}
		change.Session = sc
		out = append(out, change)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyConversationPostgresError(OperationConversationSyncChanges, ctx, err)
	}
	return out, nil
}

func (s *PostgresStore) SessionHeader(ctx context.Context, id string) (app.Session, bool, error) {
	ctx, cancel := operationContext(ctx, OperationConversationSyncHeader, s.operationTimeouts)
	defer cancel()
	session, err := scanSyncSession(s.sessionPostgres.QueryRow(ctx, `SELECT `+syncSessionColumns+` FROM sessions WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.Session{}, false, nil
	}
	if err != nil {
		return app.Session{}, false, classifyConversationPostgresError(OperationConversationSyncHeader, ctx, err)
	}
	return session, true, nil
}

func (s *PostgresStore) ListMessagePage(ctx context.Context, sessionID string, beforeSeq, afterSeq int64, limit int) ([]app.Message, bool, error) {
	ctx, cancel := operationContext(ctx, OperationConversationSyncPage, s.operationTimeouts)
	defer cancel()
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var rows onboardingPostgresRows
	var err error
	if afterSeq > 0 {
		rows, err = s.conversationPostgres.Query(ctx, `SELECT `+syncMessageColumns+` FROM messages WHERE session_id = $1 AND seq > $2 ORDER BY seq ASC LIMIT $3`, sessionID, afterSeq, limit)
	} else if beforeSeq > 0 {
		rows, err = s.conversationPostgres.Query(ctx, `SELECT * FROM (SELECT `+syncMessageColumns+` FROM messages WHERE session_id = $1 AND seq < $2 ORDER BY seq DESC LIMIT $3) page ORDER BY 11 ASC`, sessionID, beforeSeq, limit+1)
	} else {
		rows, err = s.conversationPostgres.Query(ctx, `SELECT * FROM (SELECT `+syncMessageColumns+` FROM messages WHERE session_id = $1 ORDER BY seq DESC LIMIT $2) page ORDER BY 11 ASC`, sessionID, limit+1)
	}
	if err != nil {
		return nil, false, classifyConversationPostgresError(OperationConversationSyncPage, ctx, err)
	}
	defer rows.Close()
	out := make([]app.Message, 0)
	for rows.Next() {
		message, err := scanSyncMessage(rows)
		if err != nil {
			return nil, false, classifyConversationPostgresError(OperationConversationSyncPage, ctx, err)
		}
		out = append(out, message)
	}
	if err := rows.Err(); err != nil {
		return nil, false, classifyConversationPostgresError(OperationConversationSyncPage, ctx, err)
	}
	if afterSeq > 0 {
		return out, false, nil
	}
	more := len(out) > limit
	if more {
		out = out[1:]
	}
	return out, more, nil
}

func (s *PostgresStore) SetSessionArchived(ctx context.Context, id string, archived bool) (app.Session, error) {
	ctx, cancel := operationContext(ctx, OperationConversationSyncArchive, s.operationTimeouts)
	defer cancel()
	session, err := scanSyncSession(s.sessionPostgres.QueryRow(ctx, `UPDATE sessions SET archived = $2 WHERE id = $1 RETURNING `+syncSessionColumns, id, archived))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.Session{}, storeError(ctx, OperationConversationSyncArchive, StoreErrorNotFound, errors.New("session not found"))
	}
	if err != nil {
		return app.Session{}, classifyConversationPostgresError(OperationConversationSyncArchive, ctx, err)
	}
	return session, nil
}

func (s *PostgresStore) FindMessageByClientID(ctx context.Context, sessionID, clientMessageID string) (app.Message, bool, error) {
	if strings.TrimSpace(clientMessageID) == "" {
		return app.Message{}, false, nil
	}
	ctx, cancel := operationContext(ctx, OperationConversationSyncFindClient, s.operationTimeouts)
	defer cancel()
	message, err := scanSyncMessage(s.conversationPostgres.QueryRow(ctx, `SELECT `+syncMessageColumns+` FROM messages WHERE session_id = $1 AND client_message_id = $2`, sessionID, clientMessageID))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.Message{}, false, nil
	}
	if err != nil {
		return app.Message{}, false, classifyConversationPostgresError(OperationConversationSyncFindClient, ctx, err)
	}
	return message, true, nil
}
