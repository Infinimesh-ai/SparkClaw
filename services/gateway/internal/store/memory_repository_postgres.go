package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) AddMemoryCandidate(ctx context.Context, candidate app.MemoryCandidate) (app.MemoryCandidate, error) {
	ctx, cancel := operationContext(ctx, OperationMemoryCandidateAdd, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMemoryCandidateAdd, ctx); err != nil {
		return app.MemoryCandidate{}, err
	}
	candidate = prepareMemoryCandidate(candidate, time.Now().UTC())
	session, transaction, release, err := beginPostgresTransaction(ctx, OperationMemoryCandidateAdd, s.memoryPostgres)
	if err != nil {
		return app.MemoryCandidate{}, err
	}
	defer releasePostgresSession(session, release)
	if _, err := transaction.Exec(ctx, `
		INSERT INTO memory_candidates (
			id, session_id, run_id, kind, content, sensitivity, status, reason, created_at, resolved_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (id) DO UPDATE SET
			session_id = EXCLUDED.session_id,
			run_id = EXCLUDED.run_id,
			kind = EXCLUDED.kind,
			content = EXCLUDED.content,
			sensitivity = EXCLUDED.sensitivity,
			status = EXCLUDED.status,
			reason = EXCLUDED.reason,
			created_at = EXCLUDED.created_at,
			resolved_at = EXCLUDED.resolved_at
	`, candidate.ID, candidate.SessionID, candidate.RunID, candidate.Kind, candidate.Content, candidate.Sensitivity, candidate.Status, candidate.Reason, candidate.CreatedAt, candidate.ResolvedAt); err != nil {
		return app.MemoryCandidate{}, finishMemoryPostgresStatement(ctx, OperationMemoryCandidateAdd, session, transaction, release, err)
	}
	if err := appendMemoryLifecycle(transaction, ctx, "memory_candidate.created", candidate.SessionID, candidate.RunID, "agent", candidate.Content, map[string]any{"kind": candidate.Kind}, candidate); err != nil {
		return app.MemoryCandidate{}, finishMemoryPostgresStatement(ctx, OperationMemoryCandidateAdd, session, transaction, release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return candidate, storeError(ctx, OperationMemoryCandidateAdd, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return cloneMemoryCandidate(candidate), nil
}

func (s *PostgresStore) ResolveMemoryCandidate(ctx context.Context, id, status string) (app.MemoryCandidate, *app.Memory, error) {
	ctx, cancel := operationContext(ctx, OperationMemoryCandidateResolve, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMemoryCandidateResolve, ctx); err != nil {
		return app.MemoryCandidate{}, nil, err
	}
	session, transaction, release, err := beginPostgresTransaction(ctx, OperationMemoryCandidateResolve, s.memoryPostgres)
	if err != nil {
		return app.MemoryCandidate{}, nil, err
	}
	defer releasePostgresSession(session, release)
	candidate, err := scanMemoryCandidate(transaction.QueryRow(ctx, `
		SELECT id, session_id, run_id, kind, content, sensitivity, status, reason, created_at, resolved_at
		FROM memory_candidates
		WHERE id = $1
		FOR UPDATE
	`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.MemoryCandidate{}, nil, memoryPostgresBusinessError(ctx, OperationMemoryCandidateResolve, StoreErrorNotFound, session, transaction, release, errors.New("memory candidate not found"))
	}
	if err != nil {
		return app.MemoryCandidate{}, nil, finishMemoryPostgresStatement(ctx, OperationMemoryCandidateResolve, session, transaction, release, err)
	}
	candidate = normalizeMemoryCandidate(candidate)
	if candidate.Status != "pending" {
		return app.MemoryCandidate{}, nil, memoryPostgresBusinessError(ctx, OperationMemoryCandidateResolve, StoreErrorConflict, session, transaction, release, errors.New("memory candidate already resolved"))
	}
	now := postgresTime(time.Now().UTC())
	candidate.Status = status
	candidate.ResolvedAt = &now
	if _, err := transaction.Exec(ctx, `
		UPDATE memory_candidates
		SET status = $2, resolved_at = $3
		WHERE id = $1
	`, candidate.ID, candidate.Status, candidate.ResolvedAt); err != nil {
		return app.MemoryCandidate{}, nil, finishMemoryPostgresStatement(ctx, OperationMemoryCandidateResolve, session, transaction, release, err)
	}
	var memory *app.Memory
	if status == "accepted" {
		accepted := normalizeMemory(app.Memory{
			ID: app.NewID("mem"), Kind: candidate.Kind, Content: candidate.Content,
			SourceID: candidate.RunID, CreatedAt: now,
		})
		if _, err := transaction.Exec(ctx, `
			INSERT INTO memories (id, kind, content, source_run_id, created_at)
			VALUES ($1, $2, $3, $4, $5)
		`, accepted.ID, accepted.Kind, accepted.Content, accepted.SourceID, accepted.CreatedAt); err != nil {
			return app.MemoryCandidate{}, nil, finishMemoryPostgresStatement(ctx, OperationMemoryCandidateResolve, session, transaction, release, err)
		}
		memory = &accepted
	}
	if err := appendMemoryLifecycle(transaction, ctx, "memory_candidate."+status, candidate.SessionID, candidate.RunID, "owner", candidate.Content, nil, candidate); err != nil {
		return app.MemoryCandidate{}, nil, finishMemoryPostgresStatement(ctx, OperationMemoryCandidateResolve, session, transaction, release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return candidate, memory, storeError(ctx, OperationMemoryCandidateResolve, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return cloneMemoryCandidate(candidate), memory, nil
}

func (s *PostgresStore) ListMemoryCandidates(ctx context.Context, status string) ([]app.MemoryCandidate, error) {
	ctx, cancel := operationContext(ctx, OperationMemoryCandidateList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMemoryCandidateList, ctx); err != nil {
		return nil, err
	}
	rows, err := s.memoryPostgres.Query(ctx, `
		SELECT id, session_id, run_id, kind, content, sensitivity, status, reason, created_at, resolved_at
		FROM memory_candidates
		WHERE $1 = '' OR status = $1
		ORDER BY created_at DESC, id ASC
	`, status)
	if err != nil {
		return nil, classifyMemoryPostgresError(OperationMemoryCandidateList, ctx, err)
	}
	defer rows.Close()
	out := []app.MemoryCandidate{}
	for rows.Next() {
		candidate, err := scanMemoryCandidate(rows)
		if err != nil {
			return nil, classifyMemoryPostgresError(OperationMemoryCandidateList, ctx, err)
		}
		out = append(out, cloneMemoryCandidate(normalizeMemoryCandidate(candidate)))
	}
	if err := rows.Err(); err != nil {
		return nil, classifyMemoryPostgresError(OperationMemoryCandidateList, ctx, err)
	}
	return out, nil
}

func (s *PostgresStore) SearchMemories(ctx context.Context, query string) ([]app.Memory, error) {
	ctx, cancel := operationContext(ctx, OperationMemorySearch, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMemorySearch, ctx); err != nil {
		return nil, err
	}
	rows, err := s.memoryPostgres.Query(ctx, `
		SELECT id, kind, content, source_run_id, created_at
		FROM memories
		WHERE $1 = ''
			OR lower(content) LIKE '%' || lower($1) || '%'
			OR lower(kind) LIKE '%' || lower($1) || '%'
		ORDER BY created_at DESC, id ASC
	`, query)
	if err != nil {
		return nil, classifyMemoryPostgresError(OperationMemorySearch, ctx, err)
	}
	defer rows.Close()
	out := []app.Memory{}
	for rows.Next() {
		memory, err := scanMemory(rows)
		if err != nil {
			return nil, classifyMemoryPostgresError(OperationMemorySearch, ctx, err)
		}
		out = append(out, normalizeMemory(memory))
	}
	if err := rows.Err(); err != nil {
		return nil, classifyMemoryPostgresError(OperationMemorySearch, ctx, err)
	}
	return out, nil
}

func (s *PostgresStore) UpdateMemory(ctx context.Context, id, kind, content string) (app.Memory, error) {
	return s.updateOrDeleteMemory(ctx, OperationMemoryUpdate, id, kind, content)
}

func (s *PostgresStore) DeleteMemory(ctx context.Context, id string) (app.Memory, error) {
	return s.updateOrDeleteMemory(ctx, OperationMemoryDelete, id, "", "")
}

func (s *PostgresStore) updateOrDeleteMemory(ctx context.Context, operation StoreOperation, id, kind, content string) (app.Memory, error) {
	ctx, cancel := operationContext(ctx, operation, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(operation, ctx); err != nil {
		return app.Memory{}, err
	}
	session, transaction, release, err := beginPostgresTransaction(ctx, operation, s.memoryPostgres)
	if err != nil {
		return app.Memory{}, err
	}
	defer releasePostgresSession(session, release)
	query := `
		WITH changed AS (
			UPDATE memories
			SET kind = $2, content = $3
			WHERE id = $1
			RETURNING id, kind, content, source_run_id, created_at
		)
		SELECT changed.id, changed.kind, changed.content, changed.source_run_id, changed.created_at, run.session_id
		FROM changed
		JOIN agent_runs AS run ON run.id = changed.source_run_id
	`
	arguments := []any{id, kind, content}
	eventType := "memory.updated"
	if operation == OperationMemoryDelete {
		query = `
			WITH changed AS (
				DELETE FROM memories
				WHERE id = $1
				RETURNING id, kind, content, source_run_id, created_at
			)
			SELECT changed.id, changed.kind, changed.content, changed.source_run_id, changed.created_at, run.session_id
			FROM changed
			JOIN agent_runs AS run ON run.id = changed.source_run_id
		`
		arguments = []any{id}
		eventType = "memory.deleted"
	}
	memory, sessionID, err := scanMemoryWithSession(transaction.QueryRow(ctx, query, arguments...))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.Memory{}, memoryPostgresBusinessError(ctx, operation, StoreErrorNotFound, session, transaction, release, errors.New("memory not found"))
	}
	if err != nil {
		return app.Memory{}, finishMemoryPostgresStatement(ctx, operation, session, transaction, release, err)
	}
	memory = normalizeMemory(memory)
	if err := appendMemoryLifecycle(transaction, ctx, eventType, sessionID, memory.SourceID, "owner", memory.Content, map[string]any{"memory_id": memory.ID, "kind": memory.Kind}, memory); err != nil {
		return app.Memory{}, finishMemoryPostgresStatement(ctx, operation, session, transaction, release, err)
	}
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return memory, storeError(ctx, operation, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return memory, nil
}

func (s *PostgresStore) PruneMemories(ctx context.Context, cutoff time.Time) ([]app.Memory, error) {
	ctx, cancel := operationContext(ctx, OperationMemoryPrune, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMemoryPrune, ctx); err != nil {
		return nil, err
	}
	if cutoff.IsZero() {
		return []app.Memory{}, nil
	}
	cutoff = postgresTime(cutoff)
	session, transaction, release, err := beginPostgresTransaction(ctx, OperationMemoryPrune, s.memoryPostgres)
	if err != nil {
		return nil, err
	}
	defer releasePostgresSession(session, release)
	rows, err := transaction.Query(ctx, `
		WITH pruned AS (
			DELETE FROM memories
			WHERE created_at < $1
			RETURNING id, kind, content, source_run_id, created_at
		)
		SELECT pruned.id, pruned.kind, pruned.content, pruned.source_run_id, pruned.created_at, run.session_id
		FROM pruned
		JOIN agent_runs AS run ON run.id = pruned.source_run_id
	`, cutoff)
	if err != nil {
		return nil, finishMemoryPostgresStatement(ctx, OperationMemoryPrune, session, transaction, release, err)
	}
	type prunedMemory struct {
		memory    app.Memory
		sessionID string
	}
	pruned := []prunedMemory{}
	for rows.Next() {
		memory, sessionID, err := scanMemoryWithSession(rows)
		if err != nil {
			rows.Close()
			return nil, finishMemoryPostgresStatement(ctx, OperationMemoryPrune, session, transaction, release, err)
		}
		pruned = append(pruned, prunedMemory{memory: normalizeMemory(memory), sessionID: sessionID})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, finishMemoryPostgresStatement(ctx, OperationMemoryPrune, session, transaction, release, err)
	}
	rows.Close()
	out := make([]app.Memory, 0, len(pruned))
	for _, item := range pruned {
		out = append(out, item.memory)
		if err := appendMemoryLifecycle(transaction, ctx, "memory.pruned", item.sessionID, item.memory.SourceID, "memory-retention", item.memory.Kind, map[string]any{
			"memory_id": item.memory.ID, "cutoff": cutoff.Format(time.RFC3339),
		}, item.memory); err != nil {
			return nil, finishMemoryPostgresStatement(ctx, OperationMemoryPrune, session, transaction, release, err)
		}
	}
	slices.SortFunc(out, func(a, b app.Memory) int {
		if order := b.CreatedAt.Compare(a.CreatedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	if err := transaction.Commit(ctx); err != nil {
		*release = false
		return out, storeError(ctx, OperationMemoryPrune, StoreErrorUnknownOutcome, errors.Join(err, session.Terminate(ctx)))
	}
	return out, nil
}

func appendMemoryLifecycle(transaction onboardingPostgresTx, ctx context.Context, eventType, sessionID, runID, actor, summary string, fields map[string]any, payload any) error {
	at := postgresTime(time.Now().UTC())
	if _, err := transaction.Exec(ctx, `
		INSERT INTO audit_events (id, happened_at, type, session_id, run_id, actor, summary, fields)
		VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''), $6, $7, $8)
	`, app.NewID("audit"), at, eventType, sessionID, runID, actor, summary, optionalJSON(fields)); err != nil {
		return err
	}
	_, err := transaction.Exec(ctx, `
		INSERT INTO events (id, happened_at, type, session_id, run_id, payload)
		VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''), $6)
	`, app.NewID("evt"), at, eventType, sessionID, runID, mustJSON(payload))
	return err
}

func finishMemoryPostgresStatement(ctx context.Context, operation StoreOperation, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	cause = rollbackPostgresTransaction(ctx, session, transaction, release, cause)
	return classifyMemoryPostgresError(operation, ctx, cause)
}

func memoryPostgresBusinessError(ctx context.Context, operation StoreOperation, code StoreErrorCode, session onboardingPostgresSession, transaction onboardingPostgresTx, release *bool, cause error) error {
	return storeError(ctx, operation, code, rollbackPostgresTransaction(ctx, session, transaction, release, cause))
}

func classifyMemoryPostgresError(operation StoreOperation, ctx context.Context, cause error) error {
	return classifyPostgresReadError(operation, ctx, cause)
}
