package store

import (
	"context"
	"errors"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) SaveEvalRun(ctx context.Context, run app.EvalRun) (app.EvalRun, error) {
	ctx, cancel := operationContext(ctx, OperationEvaluationSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationEvaluationSave, ctx); err != nil {
		return app.EvalRun{}, err
	}
	prepared := prepareEvalRun(run, time.Now().UTC())
	session, transaction, release, err := beginPostgresTransaction(ctx, OperationEvaluationSave, s.evaluationPostgres)
	if err != nil {
		return app.EvalRun{}, err
	}
	defer releasePostgresSession(session, release)
	if _, err := transaction.Exec(ctx, `
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
	`, prepared.ID, prepared.Profile, prepared.Status, prepared.Summary, mustJSON(prepared.Cases), mustJSON(prepared.FailureArchives), prepared.StartedAt, prepared.CompletedAt); err != nil {
		return app.EvalRun{}, finishEvaluationPostgresStatement(ctx, session, transaction, release, err)
	}
	if err := appendEvaluationLifecycle(transaction, ctx, prepared); err != nil {
		return app.EvalRun{}, finishEvaluationPostgresStatement(ctx, session, transaction, release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return prepared, storeError(ctx, OperationEvaluationSave, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return cloneEvalRun(prepared), nil
}

func appendEvaluationLifecycle(transaction onboardingPostgresTx, ctx context.Context, run app.EvalRun) error {
	at := postgresTime(time.Now().UTC())
	if _, err := transaction.Exec(ctx, `
		INSERT INTO audit_events (id, happened_at, type, session_id, run_id, actor, summary, fields)
		VALUES ($1, $2, $3, NULL, NULL, 'evaluator', $4, $5)
	`, app.NewID("audit"), at, "eval."+run.Status, run.Summary, optionalJSON(map[string]any{
		"profile": run.Profile, "id": run.ID, "failure_archives": len(run.FailureArchives),
	})); err != nil {
		return err
	}
	_, err := transaction.Exec(ctx, `
		INSERT INTO events (id, happened_at, type, session_id, run_id, payload)
		VALUES ($1, $2, $3, NULL, $4, $5)
	`, app.NewID("evt"), at, "eval."+run.Status, run.ID, mustJSON(run))
	return err
}

func (s *PostgresStore) GetEvalRun(ctx context.Context, id string) (app.EvalRun, bool, error) {
	ctx, cancel := operationContext(ctx, OperationEvaluationGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationEvaluationGet, ctx); err != nil {
		return app.EvalRun{}, false, err
	}
	row := s.evaluationPostgres.QueryRow(ctx, `
		SELECT id, profile, status, summary, cases, failure_archives, started_at, completed_at
		FROM eval_runs
		WHERE id = $1
	`, id)
	run, err := scanEvalRun(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.EvalRun{}, false, nil
	}
	if err != nil {
		return app.EvalRun{}, false, classifyEvaluationPostgresError(OperationEvaluationGet, ctx, err)
	}
	return run, true, nil
}

func (s *PostgresStore) ListEvalRuns(ctx context.Context) ([]app.EvalRun, error) {
	ctx, cancel := operationContext(ctx, OperationEvaluationList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationEvaluationList, ctx); err != nil {
		return nil, err
	}
	rows, err := s.evaluationPostgres.Query(ctx, `
		SELECT id, profile, status, summary, cases, failure_archives, started_at, completed_at
		FROM eval_runs
		ORDER BY started_at DESC, id ASC
	`)
	if err != nil {
		return nil, classifyEvaluationPostgresError(OperationEvaluationList, ctx, err)
	}
	defer rows.Close()
	runs := []app.EvalRun{}
	for rows.Next() {
		run, err := scanEvalRun(rows)
		if err != nil {
			return nil, classifyEvaluationPostgresError(OperationEvaluationList, ctx, err)
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyEvaluationPostgresError(OperationEvaluationList, ctx, err)
	}
	return runs, nil
}

func finishEvaluationPostgresStatement(ctx context.Context, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	cause = rollbackPostgresTransaction(ctx, session, transaction, release, cause)
	return classifyEvaluationPostgresError(OperationEvaluationSave, ctx, cause)
}

func classifyEvaluationPostgresError(operation StoreOperation, ctx context.Context, cause error) error {
	if errors.Is(cause, errEvalRunJSONDecode) {
		return storeError(ctx, operation, StoreErrorCorrupt, cause)
	}
	return classifyPostgresReadError(operation, ctx, cause)
}
