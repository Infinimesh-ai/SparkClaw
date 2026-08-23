package store

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const approvalSelectSQL = `
	SELECT id, source, external_id, external_context,
		coalesce(session_id, ''), coalesce(run_id, ''), coalesce(tool_call_id, ''),
		tool, risk_level, status, summary, reason, resources, arguments, created_at,
		resolved_at, coalesce(resolution_note, ''), policy_context, presentation
	FROM approvals`

func (s *PostgresStore) acquireApprovalCommand(ctx context.Context, operation StoreOperation) (func(), error) {
	if s.approvalCommandGate == nil {
		return func() {}, nil
	}
	if err := s.approvalCommandGate.Acquire(ctx, 1); err != nil {
		return nil, contextStoreError(operation, ctx, err)
	}
	return func() { s.approvalCommandGate.Release(1) }, nil
}

func approvalAdvisoryKey(id string) int64 {
	digest := sha256.Sum256([]byte("sparkclaw/store/approval/v1\x00" + id))
	return int64(binary.BigEndian.Uint64(digest[:8]))
}

func releasePostgresSession(session onboardingPostgresSession, release *bool) {
	if *release {
		session.Release()
	}
}

func approvalPostgresBusinessError(ctx context.Context, operation StoreOperation, code StoreErrorCode, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	return storeError(ctx, operation, code, rollbackPostgresTransaction(ctx, session, transaction, release, cause))
}

func finishApprovalPostgresStatement(ctx context.Context, operation StoreOperation, candidate app.Approval, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) (app.Approval, error) {
	var postgresError *pgconn.PgError
	if errors.As(cause, &postgresError) || pgconn.SafeToRetry(cause) || errors.Is(cause, errApprovalJSONDecode) {
		cause = rollbackPostgresTransaction(ctx, session, transaction, release, cause)
		if errors.Is(cause, errApprovalJSONDecode) {
			return app.Approval{}, storeError(ctx, operation, StoreErrorCorrupt, cause)
		}
		if postgresError != nil && postgresError.Code == "23505" {
			return app.Approval{}, storeError(ctx, operation, StoreErrorConflict, errors.Join(ErrApprovalConflict, cause))
		}
		if postgresError != nil {
			return app.Approval{}, storeError(ctx, operation, StoreErrorInternal, cause)
		}
		return app.Approval{}, classifyPostgresPreTransaction(operation, ctx, cause)
	}
	*release = false
	return candidate, storeError(ctx, operation, StoreErrorUnknownOutcome, errors.Join(cause, session.Terminate(ctx)))
}

func commitApprovalPostgres(ctx context.Context, operation StoreOperation, candidate app.Approval, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool) (app.Approval, error) {
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return candidate, storeError(ctx, operation, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return candidate, nil
}

func classifyApprovalPostgresError(operation StoreOperation, ctx context.Context, cause error) error {
	if errors.Is(cause, errApprovalJSONDecode) {
		return storeError(ctx, operation, StoreErrorCorrupt, cause)
	}
	return classifyPostgresReadError(operation, ctx, cause)
}

func approvalActor(approval app.Approval) string {
	if approval.Source == app.ApprovalSourceTool {
		return "policy"
	}
	return "integration"
}

func approvalResolutionActor(status string) string {
	if status == "resolved_elsewhere" {
		return "integration"
	}
	return "owner"
}

func approvalUpdateActor(approval app.Approval) string {
	if approval.Source == app.ApprovalSourceHappyTeamPlan {
		return "integration"
	}
	return "owner"
}

func approvalLifecycleFields(approval app.Approval) map[string]any {
	return map[string]any{"tool": approval.Tool, "risk": approval.Risk, "note": approval.ResolutionNote}
}

func approvalUpdateFields(approval app.Approval, note string) map[string]any {
	return map[string]any{"tool": approval.Tool, "risk": approval.Risk, "note": note}
}

func appendApprovalLifecycle(transaction onboardingPostgresTx, ctx context.Context, approval app.Approval, actor string) error {
	if _, err := transaction.Exec(ctx, `
		INSERT INTO audit_events (id, happened_at, type, session_id, run_id, actor, summary, fields)
		VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''), $6, $7, $8)
	`, app.NewID("audit"), normalizeApprovalTime(time.Now()), "approval."+approval.Status, approval.SessionID,
		approval.RunID, actor, approval.Summary, optionalJSON(approvalLifecycleFields(approval))); err != nil {
		return err
	}
	return appendApprovalEvent(transaction, ctx, approval)
}

func appendApprovalUpdateLifecycle(transaction onboardingPostgresTx, ctx context.Context, approval app.Approval, note string) error {
	if _, err := transaction.Exec(ctx, `
		INSERT INTO audit_events (id, happened_at, type, session_id, run_id, actor, summary, fields)
		VALUES ($1, $2, 'approval.modified', nullif($3, ''), nullif($4, ''), $5, $6, $7)
	`, app.NewID("audit"), normalizeApprovalTime(time.Now()), approval.SessionID, approval.RunID,
		approvalUpdateActor(approval), approval.Summary, optionalJSON(approvalUpdateFields(approval, note))); err != nil {
		return err
	}
	return appendApprovalEvent(transaction, ctx, approval)
}

func appendApprovalEvent(transaction onboardingPostgresTx, ctx context.Context, approval app.Approval) error {
	_, err := transaction.Exec(ctx, `
		INSERT INTO events (id, happened_at, type, session_id, run_id, payload)
		VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''), $6)
	`, app.NewID("evt"), normalizeApprovalTime(time.Now()), "approval."+approval.Status,
		approval.SessionID, approval.RunID, mustJSON(approval))
	return err
}

func (s *PostgresStore) SaveApproval(ctx context.Context, approval app.Approval) (app.Approval, error) {
	ctx, cancel := operationContext(ctx, OperationApprovalSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationApprovalSave, ctx); err != nil {
		return app.Approval{}, err
	}
	releaseGate, err := s.acquireApprovalCommand(ctx, OperationApprovalSave)
	if err != nil {
		return app.Approval{}, err
	}
	defer releaseGate()
	session, transaction, release, err := beginPostgresTransaction(ctx, OperationApprovalSave, s.approvalPostgres)
	if err != nil {
		return app.Approval{}, err
	}
	defer releasePostgresSession(session, release)
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, approvalAdvisoryKey(approval.ID)); err != nil {
		return finishApprovalPostgresStatement(ctx, OperationApprovalSave, app.Approval{}, session, transaction, release, err)
	}
	var existing *app.Approval
	current, err := scanApproval(transaction.QueryRow(ctx, approvalSelectSQL+` WHERE id = $1 FOR UPDATE`, approval.ID))
	if err == nil {
		existing = &current
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return finishApprovalPostgresStatement(ctx, OperationApprovalSave, app.Approval{}, session, transaction, release, err)
	}
	approval, err = prepareApproval(approval, existing, time.Now())
	if err != nil {
		return app.Approval{}, approvalPostgresBusinessError(ctx, OperationApprovalSave, StoreErrorInvalid, session, transaction, release, err)
	}
	if existing != nil {
		if approvalsEqual(*existing, approval) {
			if err := rollbackPostgresTransaction(ctx, session, transaction, release, nil); err != nil {
				return app.Approval{}, classifyApprovalPostgresError(OperationApprovalSave, ctx, err)
			}
			return approval, nil
		}
		return app.Approval{}, approvalPostgresBusinessError(ctx, OperationApprovalSave, StoreErrorConflict, session, transaction, release, ErrApprovalConflict)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO approvals (
			id, source, external_id, external_context, session_id, run_id, tool_call_id,
			tool, risk_level, status, summary, reason, resources, arguments, created_at,
			resolved_at, resolution_note, policy_context, presentation
		)
		VALUES ($1, $2, $3, $4, nullif($5, ''), nullif($6, ''), nullif($7, ''), $8,
			$9, $10, $11, $12, $13, $14, $15, $16, nullif($17, ''), $18, $19)
	`, approval.ID, string(approval.Source), approval.ExternalID, mustJSON(approval.ExternalContext), approval.SessionID,
		approval.RunID, approval.ToolCallID, approval.Tool, string(approval.Risk), approval.Status, approval.Summary,
		approval.Reason, mustJSON(approval.Resources), mustJSON(approval.Arguments), approval.CreatedAt,
		approval.ResolvedAt, approval.ResolutionNote, optionalJSON(approval.PolicyContext), optionalJSON(approval.Presentation)); err != nil {
		return finishApprovalPostgresStatement(ctx, OperationApprovalSave, approval, session, transaction, release, err)
	}
	if err := appendApprovalLifecycle(transaction, ctx, approval, approvalActor(approval)); err != nil {
		return finishApprovalPostgresStatement(ctx, OperationApprovalSave, approval, session, transaction, release, err)
	}
	return commitApprovalPostgres(ctx, OperationApprovalSave, approval, session, transaction, release)
}

func (s *PostgresStore) GetApproval(ctx context.Context, id string) (app.Approval, bool, error) {
	ctx, cancel := operationContext(ctx, OperationApprovalGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationApprovalGet, ctx); err != nil {
		return app.Approval{}, false, err
	}
	approval, err := scanApproval(s.approvalPostgres.QueryRow(ctx, approvalSelectSQL+` WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.Approval{}, false, nil
	}
	if err != nil {
		return app.Approval{}, false, classifyApprovalPostgresError(OperationApprovalGet, ctx, err)
	}
	return approval, true, nil
}

func (s *PostgresStore) FindApprovalByExternalRef(ctx context.Context, source app.ApprovalSource, externalID string) (app.Approval, bool, error) {
	ctx, cancel := operationContext(ctx, OperationApprovalFindExternalRef, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationApprovalFindExternalRef, ctx); err != nil {
		return app.Approval{}, false, err
	}
	approval, err := scanApproval(s.approvalPostgres.QueryRow(ctx, approvalSelectSQL+`
		WHERE source = $1 AND external_id = $2
		ORDER BY created_at DESC, id ASC
		LIMIT 1
	`, source, externalID))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.Approval{}, false, nil
	}
	if err != nil {
		return app.Approval{}, false, classifyApprovalPostgresError(OperationApprovalFindExternalRef, ctx, err)
	}
	return approval, true, nil
}

func (s *PostgresStore) UpdatePendingApproval(ctx context.Context, command ApprovalUpdateCommand) (app.Approval, error) {
	ctx, cancel := operationContext(ctx, OperationApprovalUpdatePending, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationApprovalUpdatePending, ctx); err != nil {
		return app.Approval{}, err
	}
	releaseGate, err := s.acquireApprovalCommand(ctx, OperationApprovalUpdatePending)
	if err != nil {
		return app.Approval{}, err
	}
	defer releaseGate()
	session, transaction, release, err := beginPostgresTransaction(ctx, OperationApprovalUpdatePending, s.approvalPostgres)
	if err != nil {
		return app.Approval{}, err
	}
	defer releasePostgresSession(session, release)
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, approvalAdvisoryKey(command.Candidate.ID)); err != nil {
		return finishApprovalPostgresStatement(ctx, OperationApprovalUpdatePending, app.Approval{}, session, transaction, release, err)
	}
	current, err := scanApproval(transaction.QueryRow(ctx, approvalSelectSQL+` WHERE id = $1 FOR UPDATE`, command.Candidate.ID))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.Approval{}, approvalPostgresBusinessError(ctx, OperationApprovalUpdatePending, StoreErrorNotFound, session, transaction, release, ErrApprovalNotFound)
	}
	if err != nil {
		return finishApprovalPostgresStatement(ctx, OperationApprovalUpdatePending, app.Approval{}, session, transaction, release, err)
	}
	approval, err := preparePendingApprovalUpdate(command, current)
	if err != nil {
		code := StoreErrorInvalid
		if errors.Is(err, ErrApprovalConflict) {
			code = StoreErrorConflict
		}
		return app.Approval{}, approvalPostgresBusinessError(ctx, OperationApprovalUpdatePending, code, session, transaction, release, err)
	}
	if _, err := transaction.Exec(ctx, `
		UPDATE approvals SET external_context = $2, summary = $3, reason = $4, resources = $5, arguments = $6
		WHERE id = $1 AND status = 'pending'
	`, approval.ID, mustJSON(approval.ExternalContext), approval.Summary, approval.Reason, mustJSON(approval.Resources), mustJSON(approval.Arguments)); err != nil {
		return finishApprovalPostgresStatement(ctx, OperationApprovalUpdatePending, approval, session, transaction, release, err)
	}
	if err := appendApprovalUpdateLifecycle(transaction, ctx, approval, command.Note); err != nil {
		return finishApprovalPostgresStatement(ctx, OperationApprovalUpdatePending, approval, session, transaction, release, err)
	}
	return commitApprovalPostgres(ctx, OperationApprovalUpdatePending, approval, session, transaction, release)
}

func (s *PostgresStore) ResolveApproval(ctx context.Context, id, status, note string) (app.Approval, error) {
	ctx, cancel := operationContext(ctx, OperationApprovalResolve, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationApprovalResolve, ctx); err != nil {
		return app.Approval{}, err
	}
	releaseGate, err := s.acquireApprovalCommand(ctx, OperationApprovalResolve)
	if err != nil {
		return app.Approval{}, err
	}
	defer releaseGate()
	session, transaction, release, err := beginPostgresTransaction(ctx, OperationApprovalResolve, s.approvalPostgres)
	if err != nil {
		return app.Approval{}, err
	}
	defer releasePostgresSession(session, release)
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, approvalAdvisoryKey(id)); err != nil {
		return finishApprovalPostgresStatement(ctx, OperationApprovalResolve, app.Approval{}, session, transaction, release, err)
	}
	current, err := scanApproval(transaction.QueryRow(ctx, approvalSelectSQL+` WHERE id = $1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.Approval{}, approvalPostgresBusinessError(ctx, OperationApprovalResolve, StoreErrorNotFound, session, transaction, release, ErrApprovalNotFound)
	}
	if err != nil {
		return finishApprovalPostgresStatement(ctx, OperationApprovalResolve, app.Approval{}, session, transaction, release, err)
	}
	approval, replay, err := prepareApprovalResolution(current, status, note, time.Now())
	if err != nil {
		code := StoreErrorInvalid
		if errors.Is(err, ErrApprovalConflict) {
			code = StoreErrorConflict
		}
		return app.Approval{}, approvalPostgresBusinessError(ctx, OperationApprovalResolve, code, session, transaction, release, err)
	}
	if replay {
		if err := rollbackPostgresTransaction(ctx, session, transaction, release, nil); err != nil {
			return app.Approval{}, classifyApprovalPostgresError(OperationApprovalResolve, ctx, err)
		}
		return approval, nil
	}
	if _, err := transaction.Exec(ctx, `
		UPDATE approvals SET status = $2, resolved_at = $3, resolution_note = nullif($4, '')
		WHERE id = $1 AND status = 'pending'
	`, approval.ID, approval.Status, approval.ResolvedAt, approval.ResolutionNote); err != nil {
		return finishApprovalPostgresStatement(ctx, OperationApprovalResolve, approval, session, transaction, release, err)
	}
	if err := appendApprovalLifecycle(transaction, ctx, approval, approvalResolutionActor(approval.Status)); err != nil {
		return finishApprovalPostgresStatement(ctx, OperationApprovalResolve, approval, session, transaction, release, err)
	}
	return commitApprovalPostgres(ctx, OperationApprovalResolve, approval, session, transaction, release)
}

func (s *PostgresStore) ListApprovals(ctx context.Context, status string) ([]app.Approval, error) {
	ctx, cancel := operationContext(ctx, OperationApprovalList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationApprovalList, ctx); err != nil {
		return nil, err
	}
	rows, err := s.approvalPostgres.Query(ctx, approvalSelectSQL+`
		WHERE $1 = '' OR status = $1
		ORDER BY created_at DESC, id ASC
	`, status)
	if err != nil {
		return nil, classifyApprovalPostgresError(OperationApprovalList, ctx, err)
	}
	defer rows.Close()
	out := make([]app.Approval, 0)
	for rows.Next() {
		approval, err := scanApproval(rows)
		if err != nil {
			return nil, classifyApprovalPostgresError(OperationApprovalList, ctx, err)
		}
		out = append(out, approval)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyApprovalPostgresError(OperationApprovalList, ctx, err)
	}
	return out, nil
}
