package store

import (
	"context"
	"errors"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *PostgresStore) SaveDocumentRecord(ctx context.Context, record app.DocumentRecord) (app.DocumentRecord, error) {
	ctx, cancel := operationContext(ctx, OperationDocumentRecordSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationDocumentRecordSave, ctx); err != nil {
		return app.DocumentRecord{}, err
	}
	if record.ID == "" {
		record.ID = app.NewID("doc")
	}
	session, transaction, release, err := beginPostgresTransaction(ctx, OperationDocumentRecordSave, s.documentPostgres)
	if err != nil {
		return app.DocumentRecord{}, err
	}
	defer func() {
		if *release {
			session.Release()
		}
	}()

	record = prepareDocumentRecord(record, nil, time.Now())
	var persistedCreatedAt time.Time
	if err := transaction.QueryRow(ctx, `
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
		RETURNING created_at
	`, record.ID, record.OwnerID, record.SessionID, record.GovernedPath, record.Name,
		record.ContentType, record.Format, record.SizeBytes, record.SHA256, record.Status,
		record.Source, record.SourceMessageID, record.SourceRunID, record.SourceToolCallID,
		record.ParentDocumentID, record.LastActivity, record.LastActivityID,
		record.LastActivityAt, record.CreatedAt, record.UpdatedAt).Scan(&persistedCreatedAt); err != nil {
		return app.DocumentRecord{}, finishDocumentPostgresStatement(ctx, session, transaction, release, err)
	}
	record.CreatedAt = normalizeDocumentTime(persistedCreatedAt)
	if err := appendDocumentLifecycle(transaction, ctx, record); err != nil {
		return app.DocumentRecord{}, finishDocumentPostgresStatement(ctx, session, transaction, release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return record, storeError(ctx, OperationDocumentRecordSave, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return record, nil
}

func appendDocumentLifecycle(transaction onboardingPostgresTx, ctx context.Context, record app.DocumentRecord) error {
	fields := map[string]any{
		"document_id": record.ID,
		"path":        record.GovernedPath,
		"activity_id": record.LastActivityID,
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO audit_events (id, happened_at, type, session_id, run_id, actor, summary, fields)
		VALUES ($1, $2, 'document.saved', nullif($3, ''), nullif($4, ''), 'document_registry', $5, $6)
	`, app.NewID("audit"), normalizeDocumentTime(time.Now()), record.SessionID, record.SourceRunID, record.LastActivity, optionalJSON(fields)); err != nil {
		return err
	}
	_, err := transaction.Exec(ctx, `
		INSERT INTO events (id, happened_at, type, session_id, run_id, payload)
		VALUES ($1, $2, 'document.saved', nullif($3, ''), nullif($4, ''), $5)
	`, app.NewID("evt"), normalizeDocumentTime(time.Now()), record.SessionID, record.SourceRunID, mustJSON(record))
	return err
}

func (s *PostgresStore) GetDocumentRecord(ctx context.Context, id string) (app.DocumentRecord, bool, error) {
	ctx, cancel := operationContext(ctx, OperationDocumentRecordGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationDocumentRecordGet, ctx); err != nil {
		return app.DocumentRecord{}, false, err
	}
	row := s.documentPostgres.QueryRow(ctx, `
		SELECT id, owner_id, session_id, governed_path, name, content_type, format,
			size_bytes, sha256, status, source, source_message_id, source_run_id,
			source_tool_call_id, parent_document_id, last_activity, last_activity_id,
			last_activity_at, created_at, updated_at
		FROM document_records
		WHERE id = $1
	`, id)
	record, err := scanDocumentRecord(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.DocumentRecord{}, false, nil
	}
	if err != nil {
		return app.DocumentRecord{}, false, classifyDocumentPostgresError(OperationDocumentRecordGet, ctx, err)
	}
	return record, true, nil
}

func (s *PostgresStore) ListDocumentRecords(ctx context.Context, ownerID, sessionID string, limit int) ([]app.DocumentRecord, error) {
	ctx, cancel := operationContext(ctx, OperationDocumentRecordList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationDocumentRecordList, ctx); err != nil {
		return nil, err
	}
	limit = normalizeDocumentRecordLimit(limit)
	rows, err := s.documentPostgres.Query(ctx, `
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
		return nil, classifyDocumentPostgresError(OperationDocumentRecordList, ctx, err)
	}
	defer rows.Close()
	out := make([]app.DocumentRecord, 0)
	for rows.Next() {
		record, err := scanDocumentRecord(rows)
		if err != nil {
			return nil, classifyDocumentPostgresError(OperationDocumentRecordList, ctx, err)
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyDocumentPostgresError(OperationDocumentRecordList, ctx, err)
	}
	return out, nil
}

func finishDocumentPostgresStatement(ctx context.Context, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	cause = rollbackPostgresTransaction(ctx, session, transaction, release, cause)
	return classifyDocumentPostgresError(OperationDocumentRecordSave, ctx, cause)
}

func classifyDocumentPostgresError(operation StoreOperation, ctx context.Context, cause error) error {
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
