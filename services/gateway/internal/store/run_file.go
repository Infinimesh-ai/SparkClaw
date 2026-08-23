package store

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *FileStore) SaveRunFeedback(ctx context.Context, feedback app.RunFeedback) (app.RunFeedback, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationRunFeedbackSave, fileAdmissionCapacity)
	if err != nil {
		return app.RunFeedback{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationRunFeedbackSave, func(ctx context.Context) (app.RunFeedback, error) {
		return s.inner.SaveRunFeedback(ctx, feedback)
	})
}

func (s *FileStore) ListRunFeedback(ctx context.Context, runID string) ([]app.RunFeedback, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationRunFeedbackList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListRunFeedback(ctx, runID)
}

func (s *FileStore) SaveRun(ctx context.Context, run app.AgentRun) (app.AgentRun, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationRunSave, fileAdmissionCapacity)
	if err != nil {
		return app.AgentRun{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationRunSave, func(ctx context.Context) (app.AgentRun, error) {
		return s.inner.SaveRun(ctx, run)
	})
}

func (s *FileStore) GetRun(ctx context.Context, id string) (app.AgentRun, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationRunGet, 1)
	if err != nil {
		return app.AgentRun{}, false, err
	}
	defer release()
	return s.inner.GetRun(ctx, id)
}

func (s *FileStore) ListRuns(ctx context.Context, sessionID string) ([]app.AgentRun, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationRunList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListRuns(ctx, sessionID)
}

func (s *FileStore) SaveModelCall(ctx context.Context, call app.ModelCall) (app.ModelCall, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationModelCallSave, fileAdmissionCapacity)
	if err != nil {
		return app.ModelCall{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationModelCallSave, func(ctx context.Context) (app.ModelCall, error) {
		return s.inner.SaveModelCall(ctx, call)
	})
}

func (s *FileStore) ListModelCalls(ctx context.Context, sessionID, runID string) ([]app.ModelCall, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationModelCallList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListModelCalls(ctx, sessionID, runID)
}

func (s *FileStore) SaveToolCall(ctx context.Context, call app.ToolCall) (app.ToolCall, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationToolCallSave, fileAdmissionCapacity)
	if err != nil {
		return app.ToolCall{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationToolCallSave, func(ctx context.Context) (app.ToolCall, error) {
		return s.inner.SaveToolCall(ctx, call)
	})
}

func (s *FileStore) GetToolCall(ctx context.Context, id string) (app.ToolCall, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationToolCallGet, 1)
	if err != nil {
		return app.ToolCall{}, false, err
	}
	defer release()
	return s.inner.GetToolCall(ctx, id)
}

func (s *FileStore) ListToolCalls(ctx context.Context, sessionID string) ([]app.ToolCall, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationToolCallList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListToolCalls(ctx, sessionID)
}

func (s *FileStore) SaveEpisodeSummary(ctx context.Context, summary app.EpisodeSummary) (app.EpisodeSummary, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationEpisodeSummarySave, fileAdmissionCapacity)
	if err != nil {
		return app.EpisodeSummary{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationEpisodeSummarySave, func(ctx context.Context) (app.EpisodeSummary, error) {
		return s.inner.SaveEpisodeSummary(ctx, summary)
	})
}

func (s *FileStore) ListEpisodeSummaries(ctx context.Context, sessionID string) ([]app.EpisodeSummary, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationEpisodeSummaryList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListEpisodeSummaries(ctx, sessionID)
}
