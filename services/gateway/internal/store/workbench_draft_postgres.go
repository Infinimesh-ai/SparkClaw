package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
)

func scanWorkbenchDraft(row onboardingPostgresRow) (app.WorkbenchDraft, error) {
	var draft app.WorkbenchDraft
	var attachments []byte
	if err := row.Scan(&draft.Content, &attachments, &draft.Revision, &draft.UpdatedAt); err != nil {
		return app.WorkbenchDraft{}, err
	}
	if err := json.Unmarshal(attachments, &draft.AttachmentIDs); err != nil {
		return app.WorkbenchDraft{}, err
	}
	draft.UpdatedAt = postgresTime(draft.UpdatedAt)
	return cloneWorkbenchDraft(draft), nil
}

func (s *PostgresStore) GetWorkbenchDraft(ctx context.Context, ownerID, sessionID string) (app.WorkbenchDraft, error) {
	ctx, cancel := operationContext(ctx, OperationWorkbenchDraftGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationWorkbenchDraftGet, ctx); err != nil {
		return app.WorkbenchDraft{}, err
	}
	if err := validateWorkbenchDraftScope(ownerID, sessionID); err != nil {
		return app.WorkbenchDraft{}, storeError(ctx, OperationWorkbenchDraftGet, StoreErrorInvalid, err)
	}
	if sessionID != "" {
		session, ok, err := s.GetSession(ctx, sessionID)
		if err != nil {
			return app.WorkbenchDraft{}, err
		}
		if !ok || session.OwnerID != ownerID || session.Source == "mcp" {
			return app.WorkbenchDraft{}, storeError(ctx, OperationWorkbenchDraftGet, StoreErrorNotFound, errors.New("draft session not found"))
		}
	}
	draft, err := scanWorkbenchDraft(s.sessionPostgres.QueryRow(ctx, `SELECT content, attachment_ids, revision, updated_at FROM workbench_drafts WHERE owner_id=$1 AND session_id=$2`, ownerID, sessionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return cloneWorkbenchDraft(app.WorkbenchDraft{}), nil
	}
	if err != nil {
		return app.WorkbenchDraft{}, classifyPostgresReadError(OperationWorkbenchDraftGet, ctx, err)
	}
	return draft, nil
}

func (s *PostgresStore) SaveWorkbenchDraft(ctx context.Context, ownerID, sessionID string, draft app.WorkbenchDraft) (app.WorkbenchDraft, error) {
	ctx, cancel := operationContext(ctx, OperationWorkbenchDraftSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationWorkbenchDraftSave, ctx); err != nil {
		return app.WorkbenchDraft{}, err
	}
	if err := errors.Join(validateWorkbenchDraftScope(ownerID, sessionID), validateWorkbenchDraft(draft)); err != nil {
		return app.WorkbenchDraft{}, storeError(ctx, OperationWorkbenchDraftSave, StoreErrorInvalid, err)
	}
	session, transaction, release, err := beginPostgresTransaction(ctx, OperationWorkbenchDraftSave, s.sessionPostgres)
	if err != nil {
		return app.WorkbenchDraft{}, err
	}
	defer releasePostgresSession(session, release)
	if sessionID != "" {
		var existingOwner, source string
		err := transaction.QueryRow(ctx, `SELECT owner_id, source FROM sessions WHERE id=$1 FOR KEY SHARE`, sessionID).Scan(&existingOwner, &source)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && (existingOwner != ownerID || source == "mcp")) {
			return app.WorkbenchDraft{}, sessionBusinessError(ctx, OperationWorkbenchDraftSave, StoreErrorNotFound, session, transaction, release, errors.New("draft session not found"))
		}
		if err != nil {
			return app.WorkbenchDraft{}, finishDraftPostgres(ctx, session, transaction, release, err)
		}
	}
	updated, err := scanWorkbenchDraft(transaction.QueryRow(ctx, `
		INSERT INTO workbench_drafts (owner_id, session_id, content, attachment_ids, revision, updated_at)
		SELECT $1,$2,$3,$4,$5::bigint+1,$6
		WHERE $5=0 OR EXISTS (SELECT 1 FROM workbench_drafts WHERE owner_id=$1 AND session_id=$2 AND revision=$5)
		ON CONFLICT (owner_id,session_id) DO UPDATE SET content=EXCLUDED.content, attachment_ids=EXCLUDED.attachment_ids,
			revision=EXCLUDED.revision, updated_at=GREATEST(EXCLUDED.updated_at, workbench_drafts.updated_at + interval '1 microsecond')
		WHERE workbench_drafts.revision=$5
		RETURNING content, attachment_ids, revision, updated_at
	`, ownerID, sessionID, draft.Content, mustJSON(cloneWorkbenchDraft(draft).AttachmentIDs), draft.Revision, postgresTime(time.Now().UTC())))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.WorkbenchDraft{}, sessionBusinessError(ctx, OperationWorkbenchDraftSave, StoreErrorConflict, session, transaction, release, ErrWorkbenchDraftConflict)
	}
	if err != nil {
		return app.WorkbenchDraft{}, finishDraftPostgres(ctx, session, transaction, release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return updated, storeError(ctx, OperationWorkbenchDraftSave, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return updated, nil
}

func finishDraftPostgres(ctx context.Context, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, err error) error {
	err = rollbackPostgresTransaction(ctx, session, transaction, release, err)
	return classifyPostgresReadError(OperationWorkbenchDraftSave, ctx, err)
}
