package store

import (
	"context"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *FileStore) AddMemoryCandidate(ctx context.Context, candidate app.MemoryCandidate) (app.MemoryCandidate, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMemoryCandidateAdd, fileAdmissionCapacity)
	if err != nil {
		return app.MemoryCandidate{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationMemoryCandidateAdd, func(ctx context.Context) (app.MemoryCandidate, error) {
		return s.inner.AddMemoryCandidate(ctx, candidate)
	})
}

func (s *FileStore) ResolveMemoryCandidate(ctx context.Context, id, status string) (app.MemoryCandidate, *app.Memory, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMemoryCandidateResolve, fileAdmissionCapacity)
	if err != nil {
		return app.MemoryCandidate{}, nil, err
	}
	defer release()
	type result struct {
		candidate app.MemoryCandidate
		memory    *app.Memory
	}
	out, err := runFileCommand(s, ctx, OperationMemoryCandidateResolve, func(ctx context.Context) (result, error) {
		candidate, memory, err := s.inner.ResolveMemoryCandidate(ctx, id, status)
		return result{candidate: candidate, memory: memory}, err
	})
	return out.candidate, out.memory, err
}

func (s *FileStore) ListMemoryCandidates(ctx context.Context, status string) ([]app.MemoryCandidate, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMemoryCandidateList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListMemoryCandidates(ctx, status)
}

func (s *FileStore) SearchMemories(ctx context.Context, query string) ([]app.Memory, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMemorySearch, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.SearchMemories(ctx, query)
}

func (s *FileStore) UpdateMemory(ctx context.Context, id, kind, content string) (app.Memory, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMemoryUpdate, fileAdmissionCapacity)
	if err != nil {
		return app.Memory{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationMemoryUpdate, func(ctx context.Context) (app.Memory, error) {
		return s.inner.UpdateMemory(ctx, id, kind, content)
	})
}

func (s *FileStore) DeleteMemory(ctx context.Context, id string) (app.Memory, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMemoryDelete, fileAdmissionCapacity)
	if err != nil {
		return app.Memory{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationMemoryDelete, func(ctx context.Context) (app.Memory, error) {
		return s.inner.DeleteMemory(ctx, id)
	})
}

func (s *FileStore) PruneMemories(ctx context.Context, cutoff time.Time) ([]app.Memory, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMemoryPrune, fileAdmissionCapacity)
	if err != nil {
		return nil, err
	}
	defer release()
	out, _, err := runFileOptionalCommand(s, ctx, OperationMemoryPrune, func(ctx context.Context) ([]app.Memory, bool, error) {
		pruned, err := s.inner.PruneMemories(ctx, cutoff)
		return pruned, len(pruned) > 0, err
	})
	return out, err
}
