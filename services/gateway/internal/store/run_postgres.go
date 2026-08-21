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

func (s *PostgresStore) SaveRunFeedback(ctx context.Context, feedback app.RunFeedback) (app.RunFeedback, error) {
	ctx, cancel := operationContext(ctx, OperationRunFeedbackSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationRunFeedbackSave, ctx); err != nil {
		return app.RunFeedback{}, err
	}
	feedback, err := prepareRunFeedback(feedback, nil, time.Now().UTC())
	if err != nil {
		return app.RunFeedback{}, storeError(ctx, OperationRunFeedbackSave, StoreErrorInvalid, err)
	}
	session, err := s.runPostgres.Acquire(ctx)
	if err != nil {
		return app.RunFeedback{}, classifyPostgresPreTransaction(OperationRunFeedbackSave, ctx, err)
	}
	release := true
	defer func() {
		if release {
			session.Release()
		}
	}()
	transaction, err := session.Begin(ctx, pgx.TxOptions{})
	if err != nil {
		return app.RunFeedback{}, classifyPostgresPreTransaction(OperationRunFeedbackSave, ctx, err)
	}
	lockID := feedback.ID
	if feedback.MessageID != "" {
		lockID = feedback.RunID + "\x00message\x00" + feedback.MessageID
	}
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, runAdvisoryKey("run_feedback", lockID)); err != nil {
		return app.RunFeedback{}, finishRunPostgresPreCandidate(ctx, OperationRunFeedbackSave, session, transaction, &release, err)
	}
	var existing *app.RunFeedback
	current, err := scanRunFeedback(transaction.QueryRow(ctx, `
		SELECT id, session_id, run_id, coalesce(message_id, ''), rating, note, correction, created_at, updated_at
		FROM run_feedback
		WHERE id = $1 OR (run_id = $2 AND $3 <> '' AND message_id = $3)
		ORDER BY CASE WHEN id = $1 THEN 0 ELSE 1 END
		LIMIT 1
	`, feedback.ID, feedback.RunID, feedback.MessageID))
	if err == nil {
		existing = &current
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return app.RunFeedback{}, finishRunPostgresPreCandidate(ctx, OperationRunFeedbackSave, session, transaction, &release, err)
	}
	feedback, err = prepareRunFeedback(feedback, existing, time.Now().UTC())
	if err != nil {
		cause := rollbackPostgresTransaction(ctx, session, transaction, &release, err)
		return app.RunFeedback{}, storeError(ctx, OperationRunFeedbackSave, StoreErrorInvalid, cause)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO run_feedback (id, session_id, run_id, message_id, rating, note, correction, created_at, updated_at)
		VALUES ($1, $2, $3, nullif($4, ''), $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO UPDATE SET session_id=EXCLUDED.session_id, run_id=EXCLUDED.run_id,
			message_id=EXCLUDED.message_id, rating=EXCLUDED.rating, note=EXCLUDED.note,
			correction=EXCLUDED.correction, updated_at=EXCLUDED.updated_at
	`, feedback.ID, feedback.SessionID, feedback.RunID, feedback.MessageID, feedback.Rating, feedback.Note, feedback.Correction, feedback.CreatedAt, feedback.UpdatedAt); err != nil {
		return finishRunPostgresStatement(ctx, OperationRunFeedbackSave, feedback, session, transaction, &release, err)
	}
	if err := appendRunLifecycle(transaction, ctx, "run_feedback.saved", feedback.SessionID, feedback.RunID, "owner", feedback.Rating, map[string]any{
		"feedback_id": feedback.ID, "message_id": feedback.MessageID,
		"has_note": feedback.Note != "", "has_correction": feedback.Correction != "",
	}, feedback); err != nil {
		return finishRunPostgresStatement(ctx, OperationRunFeedbackSave, feedback, session, transaction, &release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		release = false
		return feedback, storeError(ctx, OperationRunFeedbackSave, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return feedback, nil
}

func (s *PostgresStore) ListRunFeedback(ctx context.Context, runID string) ([]app.RunFeedback, error) {
	ctx, cancel := operationContext(ctx, OperationRunFeedbackList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationRunFeedbackList, ctx); err != nil {
		return nil, err
	}
	rows, err := s.runPostgres.Query(ctx, `
		SELECT id, session_id, run_id, coalesce(message_id, ''), rating, note, correction, created_at, updated_at
		FROM run_feedback
		WHERE $1 = '' OR run_id = $1
		ORDER BY updated_at DESC, id ASC
	`, runID)
	if err != nil {
		return nil, classifyRunPostgresReadError(OperationRunFeedbackList, ctx, err)
	}
	defer rows.Close()
	out := []app.RunFeedback{}
	for rows.Next() {
		feedback, err := scanRunFeedback(rows)
		if err != nil {
			return nil, classifyRunPostgresReadError(OperationRunFeedbackList, ctx, err)
		}
		out = append(out, feedback)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyRunPostgresReadError(OperationRunFeedbackList, ctx, err)
	}
	return out, nil
}

func (s *PostgresStore) SaveRun(ctx context.Context, run app.AgentRun) (app.AgentRun, error) {
	run, err := prepareRun(run, time.Now().UTC())
	if err != nil {
		return app.AgentRun{}, storeError(ctx, OperationRunSave, StoreErrorInvalid, err)
	}
	workflowState := optionalJSON(run.Workflow)
	messageContext := optionalJSON(run.MessageContext)
	return runPostgresWrite(s, ctx, OperationRunSave, "run", run.ID, run, func(transaction onboardingPostgresTx, commandCtx context.Context) error {
		if _, err := transaction.Exec(commandCtx, `
			INSERT INTO agent_runs (id, session_id, state, model_lane, risk_level, summary, workflow_state, message_context, started_at, completed_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (id) DO UPDATE SET state=EXCLUDED.state, model_lane=EXCLUDED.model_lane,
				risk_level=EXCLUDED.risk_level, summary=EXCLUDED.summary, workflow_state=EXCLUDED.workflow_state,
				message_context=EXCLUDED.message_context, started_at=EXCLUDED.started_at, completed_at=EXCLUDED.completed_at
		`, run.ID, run.SessionID, run.State, run.ModelLane, string(run.Risk), run.Summary, workflowState, messageContext, run.StartedAt, run.CompletedAt); err != nil {
			return err
		}
		return appendRunEvent(transaction, commandCtx, "run."+run.State, run.SessionID, run.ID, run)
	})
}

func (s *PostgresStore) GetRun(ctx context.Context, id string) (app.AgentRun, bool, error) {
	ctx, cancel := operationContext(ctx, OperationRunGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationRunGet, ctx); err != nil {
		return app.AgentRun{}, false, err
	}
	row := s.runPostgres.QueryRow(ctx, `
		SELECT id, session_id, state, model_lane, risk_level, started_at, completed_at, coalesce(summary, ''), workflow_state, message_context
		FROM agent_runs
		WHERE id = $1
	`, id)
	run, err := scanRun(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.AgentRun{}, false, nil
	}
	if err != nil {
		return app.AgentRun{}, false, classifyRunPostgresReadError(OperationRunGet, ctx, err)
	}
	return run, true, nil
}

func (s *PostgresStore) ListRuns(ctx context.Context, sessionID string) ([]app.AgentRun, error) {
	ctx, cancel := operationContext(ctx, OperationRunList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationRunList, ctx); err != nil {
		return nil, err
	}
	rows, err := s.runPostgres.Query(ctx, `
		SELECT id, session_id, state, model_lane, risk_level, started_at, completed_at, coalesce(summary, ''), workflow_state, message_context
		FROM agent_runs
		WHERE $1 = '' OR session_id = $1
		ORDER BY started_at DESC, id ASC
	`, sessionID)
	if err != nil {
		return nil, classifyRunPostgresReadError(OperationRunList, ctx, err)
	}
	defer rows.Close()
	out := []app.AgentRun{}
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, classifyRunPostgresReadError(OperationRunList, ctx, err)
		}
		out = append(out, run)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyRunPostgresReadError(OperationRunList, ctx, err)
	}
	return out, nil
}

func (s *PostgresStore) SaveModelCall(ctx context.Context, call app.ModelCall) (app.ModelCall, error) {
	call, err := prepareModelCall(call, time.Now().UTC())
	if err != nil {
		return app.ModelCall{}, storeError(ctx, OperationModelCallSave, StoreErrorInvalid, err)
	}
	return runPostgresWrite(s, ctx, OperationModelCallSave, "model_call", call.ID, call, func(transaction onboardingPostgresTx, commandCtx context.Context) error {
		if _, err := transaction.Exec(commandCtx, `
			INSERT INTO model_calls (id, session_id, run_id, lane, profile, model, operation, mock, fallback, status,
				prompt_tokens, response_tokens, total_tokens, latency_ms, error, started_at, completed_at)
			VALUES ($1, nullif($2, ''), nullif($3, ''), $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, nullif($15, ''), $16, $17)
			ON CONFLICT (id) DO UPDATE SET session_id=EXCLUDED.session_id, run_id=EXCLUDED.run_id,
				lane=EXCLUDED.lane, profile=EXCLUDED.profile, model=EXCLUDED.model, operation=EXCLUDED.operation,
				mock=EXCLUDED.mock, fallback=EXCLUDED.fallback, status=EXCLUDED.status,
				prompt_tokens=EXCLUDED.prompt_tokens, response_tokens=EXCLUDED.response_tokens,
				total_tokens=EXCLUDED.total_tokens, latency_ms=EXCLUDED.latency_ms, error=EXCLUDED.error,
				started_at=EXCLUDED.started_at, completed_at=EXCLUDED.completed_at
		`, call.ID, call.SessionID, call.RunID, call.Lane, call.Profile, call.Model, call.Operation, call.Mock, call.Fallback, call.Status,
			call.PromptTokens, call.ResponseTokens, call.TotalTokens, call.LatencyMS, call.Error, call.StartedAt, call.CompletedAt); err != nil {
			return err
		}
		return appendRunLifecycle(transaction, commandCtx, "model_call."+call.Status, call.SessionID, call.RunID, "model-router", call.Model, map[string]any{
			"lane": call.Lane, "profile": call.Profile, "operation": call.Operation, "latency_ms": call.LatencyMS,
		}, call)
	})
}

func (s *PostgresStore) ListModelCalls(ctx context.Context, sessionID, runID string) ([]app.ModelCall, error) {
	ctx, cancel := operationContext(ctx, OperationModelCallList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationModelCallList, ctx); err != nil {
		return nil, err
	}
	rows, err := s.runPostgres.Query(ctx, `
		SELECT id, coalesce(session_id, ''), coalesce(run_id, ''), lane, profile, model, operation, mock, fallback,
			status, prompt_tokens, response_tokens, total_tokens, latency_ms, coalesce(error, ''), started_at, completed_at
		FROM model_calls
		WHERE ($1 = '' OR session_id = $1) AND ($2 = '' OR run_id = $2)
		ORDER BY started_at ASC, id ASC
	`, sessionID, runID)
	if err != nil {
		return nil, classifyRunPostgresReadError(OperationModelCallList, ctx, err)
	}
	defer rows.Close()
	out := []app.ModelCall{}
	for rows.Next() {
		call, err := scanModelCall(rows)
		if err != nil {
			return nil, classifyRunPostgresReadError(OperationModelCallList, ctx, err)
		}
		out = append(out, call)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyRunPostgresReadError(OperationModelCallList, ctx, err)
	}
	return out, nil
}

func (s *PostgresStore) SaveToolCall(ctx context.Context, call app.ToolCall) (app.ToolCall, error) {
	call, err := prepareToolCall(call, time.Now().UTC())
	if err != nil {
		return app.ToolCall{}, storeError(ctx, OperationToolCallSave, StoreErrorInvalid, err)
	}
	args := mustJSON(call.Arguments)
	result := optionalJSON(call.Result)
	policyContext := optionalJSON(call.PolicyContext)
	return runPostgresWrite(s, ctx, OperationToolCallSave, "tool_call", call.ID, call, func(transaction onboardingPostgresTx, commandCtx context.Context) error {
		if _, err := transaction.Exec(commandCtx, `
			INSERT INTO tool_calls (id, session_id, run_id, workflow_id, workflow_node_id, scope_revision, capability,
				tool, risk_level, status, arguments, result, error, error_code, approval_id, observation_ref,
				observation_summary, started_at, completed_at, policy_context)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, nullif($13, ''), nullif($14, ''), nullif($15, ''), nullif($16, ''), $17, $18, $19, $20)
			ON CONFLICT (id) DO UPDATE SET workflow_id=EXCLUDED.workflow_id, workflow_node_id=EXCLUDED.workflow_node_id,
				scope_revision=EXCLUDED.scope_revision, capability=EXCLUDED.capability, risk_level=EXCLUDED.risk_level,
				status=EXCLUDED.status, arguments=EXCLUDED.arguments, result=EXCLUDED.result, error=EXCLUDED.error,
				error_code=EXCLUDED.error_code, approval_id=EXCLUDED.approval_id,
				observation_ref=EXCLUDED.observation_ref, observation_summary=EXCLUDED.observation_summary,
				started_at=EXCLUDED.started_at, completed_at=EXCLUDED.completed_at, policy_context=EXCLUDED.policy_context
		`, call.ID, call.SessionID, call.RunID, string(call.WorkflowID), string(call.WorkflowNodeID), call.ScopeRevision, call.Capability,
			call.Tool, string(call.Risk), call.Status, args, result, call.Error, call.ErrorCode, call.ApprovalID, call.ObservationRef, call.ObservationSummary, call.StartedAt, call.CompletedAt, policyContext); err != nil {
			return err
		}
		return appendRunLifecycle(transaction, commandCtx, "tool_call."+call.Status, call.SessionID, call.RunID, "agent", call.Tool, map[string]any{
			"risk": call.Risk, "id": call.ID,
		}, call)
	})
}

func (s *PostgresStore) GetToolCall(ctx context.Context, id string) (app.ToolCall, bool, error) {
	ctx, cancel := operationContext(ctx, OperationToolCallGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationToolCallGet, ctx); err != nil {
		return app.ToolCall{}, false, err
	}
	row := s.runPostgres.QueryRow(ctx, `
		SELECT id, session_id, run_id, workflow_id, workflow_node_id, scope_revision, capability,
			tool, risk_level, status, arguments, result, coalesce(error, ''), coalesce(error_code, ''),
			coalesce(approval_id, ''), started_at, completed_at, coalesce(observation_ref, ''), coalesce(observation_summary, ''), policy_context
		FROM tool_calls
		WHERE id = $1
	`, id)
	call, err := scanToolCall(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.ToolCall{}, false, nil
	}
	if err != nil {
		return app.ToolCall{}, false, classifyRunPostgresReadError(OperationToolCallGet, ctx, err)
	}
	return call, true, nil
}

func (s *PostgresStore) ListToolCalls(ctx context.Context, sessionID string) ([]app.ToolCall, error) {
	ctx, cancel := operationContext(ctx, OperationToolCallList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationToolCallList, ctx); err != nil {
		return nil, err
	}
	rows, err := s.runPostgres.Query(ctx, `
		SELECT id, session_id, run_id, workflow_id, workflow_node_id, scope_revision, capability,
			tool, risk_level, status, arguments, result, coalesce(error, ''), coalesce(error_code, ''),
			coalesce(approval_id, ''), started_at, completed_at, coalesce(observation_ref, ''), coalesce(observation_summary, ''), policy_context
		FROM tool_calls
		WHERE $1 = '' OR session_id = $1
		ORDER BY started_at ASC, id ASC
	`, sessionID)
	if err != nil {
		return nil, classifyRunPostgresReadError(OperationToolCallList, ctx, err)
	}
	defer rows.Close()
	out := []app.ToolCall{}
	for rows.Next() {
		call, err := scanToolCall(rows)
		if err != nil {
			return nil, classifyRunPostgresReadError(OperationToolCallList, ctx, err)
		}
		out = append(out, call)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyRunPostgresReadError(OperationToolCallList, ctx, err)
	}
	return out, nil
}

func runPostgresWrite[T any](s *PostgresStore, parent context.Context, operation StoreOperation, kind, id string, candidate T, command func(onboardingPostgresTx, context.Context) error) (T, error) {
	var zero T
	ctx, cancel := operationContext(parent, operation, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(operation, ctx); err != nil {
		return zero, err
	}
	session, err := s.runPostgres.Acquire(ctx)
	if err != nil {
		return zero, classifyPostgresPreTransaction(operation, ctx, err)
	}
	release := true
	defer func() {
		if release {
			session.Release()
		}
	}()
	transaction, err := session.Begin(ctx, pgx.TxOptions{})
	if err != nil {
		return zero, classifyPostgresPreTransaction(operation, ctx, err)
	}
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, runAdvisoryKey(kind, id)); err != nil {
		return zero, finishRunPostgresPreCandidate(ctx, operation, session, transaction, &release, err)
	}
	if err := command(transaction, ctx); err != nil {
		return finishRunPostgresStatement(ctx, operation, candidate, session, transaction, &release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		release = false
		return candidate, storeError(ctx, operation, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return candidate, nil
}

func finishRunPostgresPreCandidate(ctx context.Context, operation StoreOperation, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	var postgresError *pgconn.PgError
	if errors.As(cause, &postgresError) || pgconn.SafeToRetry(cause) {
		cause = rollbackPostgresTransaction(ctx, session, transaction, release, cause)
		if postgresError != nil {
			return storeError(ctx, operation, StoreErrorInternal, cause)
		}
		return classifyPostgresPreTransaction(operation, ctx, cause)
	}
	*release = false
	return storeError(ctx, operation, StoreErrorUnknownOutcome, errors.Join(cause, session.Terminate(ctx)))
}

func finishRunPostgresStatement[T any](ctx context.Context, operation StoreOperation, candidate T, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) (T, error) {
	var zero T
	var postgresError *pgconn.PgError
	if errors.As(cause, &postgresError) || pgconn.SafeToRetry(cause) {
		cause = rollbackPostgresTransaction(ctx, session, transaction, release, cause)
		if postgresError != nil {
			return zero, storeError(ctx, operation, StoreErrorInternal, cause)
		}
		return zero, classifyPostgresPreTransaction(operation, ctx, cause)
	}
	*release = false
	return candidate, storeError(ctx, operation, StoreErrorUnknownOutcome, errors.Join(cause, session.Terminate(ctx)))
}

func appendRunLifecycle(transaction onboardingPostgresTx, ctx context.Context, typ, sessionID, runID, actor, summary string, fields map[string]any, payload any) error {
	if _, err := transaction.Exec(ctx, `
		INSERT INTO audit_events (id, happened_at, type, session_id, run_id, actor, summary, fields)
		VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''), $6, $7, $8)
	`, app.NewID("audit"), time.Now().UTC(), typ, sessionID, runID, actor, summary, optionalJSON(fields)); err != nil {
		return err
	}
	return appendRunEvent(transaction, ctx, typ, sessionID, runID, payload)
}

func appendRunEvent(transaction onboardingPostgresTx, ctx context.Context, typ, sessionID, runID string, payload any) error {
	_, err := transaction.Exec(ctx, `
		INSERT INTO events (id, happened_at, type, session_id, run_id, payload)
		VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''), $6)
	`, app.NewID("evt"), time.Now().UTC(), typ, sessionID, runID, mustJSON(payload))
	return err
}

func classifyRunPostgresReadError(operation StoreOperation, ctx context.Context, cause error) error {
	if errors.Is(cause, errRunJSONDecode) {
		return storeError(ctx, operation, StoreErrorCorrupt, cause)
	}
	return classifyPostgresReadError(operation, ctx, cause)
}

func runAdvisoryKey(kind, id string) int64 {
	digest := sha256.Sum256([]byte("sparkclaw/store/run/v1\x00" + kind + "\x00" + id))
	return int64(binary.BigEndian.Uint64(digest[:8]))
}

func (s *PostgresStore) SaveEpisodeSummary(ctx context.Context, summary app.EpisodeSummary) (app.EpisodeSummary, error) {
	summary, err := prepareEpisodeSummary(summary, time.Now().UTC())
	if err != nil {
		return app.EpisodeSummary{}, storeError(ctx, OperationEpisodeSummarySave, StoreErrorInvalid, err)
	}
	return runPostgresWrite(s, ctx, OperationEpisodeSummarySave, "episode_summary", summary.ID, summary, func(transaction onboardingPostgresTx, commandCtx context.Context) error {
		if _, err := transaction.Exec(commandCtx, `
			INSERT INTO episode_summaries (id, session_id, run_id, goal, outcome, risk_level, model_lane, tools, approvals,
				failures, repair_performed, summary, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
			ON CONFLICT (id) DO UPDATE SET goal=EXCLUDED.goal, outcome=EXCLUDED.outcome,
				risk_level=EXCLUDED.risk_level, model_lane=EXCLUDED.model_lane, tools=EXCLUDED.tools,
				approvals=EXCLUDED.approvals, failures=EXCLUDED.failures,
				repair_performed=EXCLUDED.repair_performed, summary=EXCLUDED.summary, created_at=EXCLUDED.created_at
		`, summary.ID, summary.SessionID, summary.RunID, summary.Goal, summary.Outcome, string(summary.Risk), summary.ModelLane,
			mustJSON(summary.Tools), mustJSON(summary.Approvals), mustJSON(summary.Failures), summary.RepairPerformed, summary.Summary, summary.CreatedAt); err != nil {
			return err
		}
		return appendRunLifecycle(transaction, commandCtx, "episode_summary.saved", summary.SessionID, summary.RunID, "runtime", summary.Outcome, map[string]any{
			"tools": summary.Tools, "repair_performed": summary.RepairPerformed,
		}, summary)
	})
}

func (s *PostgresStore) ListEpisodeSummaries(ctx context.Context, sessionID string) ([]app.EpisodeSummary, error) {
	ctx, cancel := operationContext(ctx, OperationEpisodeSummaryList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationEpisodeSummaryList, ctx); err != nil {
		return nil, err
	}
	rows, err := s.runPostgres.Query(ctx, `
		SELECT id, session_id, run_id, goal, outcome, risk_level, model_lane, tools, approvals,
			failures, repair_performed, summary, created_at
		FROM episode_summaries
		WHERE $1 = '' OR session_id = $1
		ORDER BY created_at DESC, id ASC
	`, sessionID)
	if err != nil {
		return nil, classifyRunPostgresReadError(OperationEpisodeSummaryList, ctx, err)
	}
	defer rows.Close()
	out := []app.EpisodeSummary{}
	for rows.Next() {
		summary, err := scanEpisodeSummary(rows)
		if err != nil {
			return nil, classifyRunPostgresReadError(OperationEpisodeSummaryList, ctx, err)
		}
		out = append(out, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyRunPostgresReadError(OperationEpisodeSummaryList, ctx, err)
	}
	return out, nil
}
