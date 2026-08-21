package store

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
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
	return storeError(operation, code, rollbackPostgresTransaction(ctx, session, transaction, release, cause))
}

func finishApprovalPostgresStatement(ctx context.Context, operation StoreOperation, candidate app.Approval, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) (app.Approval, error) {
	var postgresError *pgconn.PgError
	if errors.As(cause, &postgresError) || pgconn.SafeToRetry(cause) || errors.Is(cause, errApprovalJSONDecode) {
		cause = rollbackPostgresTransaction(ctx, session, transaction, release, cause)
		if errors.Is(cause, errApprovalJSONDecode) {
			return app.Approval{}, storeError(operation, StoreErrorCorrupt, cause)
		}
		if postgresError != nil && postgresError.Code == "23505" {
			return app.Approval{}, storeError(operation, StoreErrorConflict, errors.Join(ErrApprovalConflict, cause))
		}
		if postgresError != nil {
			return app.Approval{}, storeError(operation, StoreErrorInternal, cause)
		}
		return app.Approval{}, classifyPostgresPreTransaction(operation, ctx, cause)
	}
	*release = false
	return candidate, storeError(operation, StoreErrorUnknownOutcome, errors.Join(cause, session.Terminate(ctx)))
}

func commitApprovalPostgres(ctx context.Context, operation StoreOperation, candidate app.Approval, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool) (app.Approval, error) {
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return candidate, storeError(operation, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return candidate, nil
}

func classifyApprovalPostgresError(operation StoreOperation, ctx context.Context, cause error) error {
	if errors.Is(cause, errApprovalJSONDecode) {
		return storeError(operation, StoreErrorCorrupt, cause)
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
