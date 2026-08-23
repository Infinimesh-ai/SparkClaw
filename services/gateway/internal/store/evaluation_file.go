package store

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *FileStore) SaveEvalRun(ctx context.Context, run app.EvalRun) (app.EvalRun, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationEvaluationSave, fileAdmissionCapacity)
	if err != nil {
		return app.EvalRun{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationEvaluationSave, func(ctx context.Context) (app.EvalRun, error) {
		return s.inner.SaveEvalRun(ctx, run)
	})
}

func (s *FileStore) GetEvalRun(ctx context.Context, id string) (app.EvalRun, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationEvaluationGet, 1)
	if err != nil {
		return app.EvalRun{}, false, err
	}
	defer release()
	return s.inner.GetEvalRun(ctx, id)
}

func (s *FileStore) ListEvalRuns(ctx context.Context) ([]app.EvalRun, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationEvaluationList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListEvalRuns(ctx)
}
