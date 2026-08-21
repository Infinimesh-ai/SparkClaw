package store

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *MemoryStore) SaveEvalRun(ctx context.Context, run app.EvalRun) (app.EvalRun, error) {
	ctx, cancel := operationContext(ctx, OperationEvaluationSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationEvaluationSave, ctx); err != nil {
		return app.EvalRun{}, err
	}
	prepared := prepareEvalRun(run, time.Now().UTC())
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationEvaluationSave, ctx); err != nil {
		return app.EvalRun{}, err
	}
	s.evalRuns[prepared.ID] = cloneEvalRun(prepared)
	s.appendAuditLocked("eval."+prepared.Status, "", "", "evaluator", prepared.Summary, map[string]any{
		"profile":          prepared.Profile,
		"id":               prepared.ID,
		"failure_archives": len(prepared.FailureArchives),
	})
	s.appendEventLocked("eval."+prepared.Status, "", prepared.ID, prepared)
	return cloneEvalRun(prepared), nil
}

func (s *MemoryStore) GetEvalRun(ctx context.Context, id string) (app.EvalRun, bool, error) {
	ctx, cancel := operationContext(ctx, OperationEvaluationGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationEvaluationGet, ctx); err != nil {
		return app.EvalRun{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationEvaluationGet, ctx); err != nil {
		return app.EvalRun{}, false, err
	}
	run, ok := s.evalRuns[id]
	return cloneEvalRun(run), ok, nil
}

func (s *MemoryStore) ListEvalRuns(ctx context.Context) ([]app.EvalRun, error) {
	ctx, cancel := operationContext(ctx, OperationEvaluationList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationEvaluationList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationEvaluationList, ctx); err != nil {
		return nil, err
	}
	out := []app.EvalRun{}
	for _, run := range s.evalRuns {
		out = append(out, cloneEvalRun(run))
	}
	slices.SortFunc(out, func(a, b app.EvalRun) int {
		if order := b.StartedAt.Compare(a.StartedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}
