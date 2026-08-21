package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *MemoryStore) AddMemoryCandidate(ctx context.Context, candidate app.MemoryCandidate) (app.MemoryCandidate, error) {
	ctx, cancel := operationContext(ctx, OperationMemoryCandidateAdd, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMemoryCandidateAdd, ctx); err != nil {
		return app.MemoryCandidate{}, err
	}
	candidate = prepareMemoryCandidate(candidate, time.Now().UTC())
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationMemoryCandidateAdd, ctx); err != nil {
		return app.MemoryCandidate{}, err
	}
	s.memoryCandidates[candidate.ID] = cloneMemoryCandidate(candidate)
	s.appendAuditLocked("memory_candidate.created", candidate.SessionID, candidate.RunID, "agent", candidate.Content, map[string]any{"kind": candidate.Kind})
	s.appendEventLocked("memory_candidate.created", candidate.SessionID, candidate.RunID, candidate)
	return cloneMemoryCandidate(candidate), nil
}

func (s *MemoryStore) ResolveMemoryCandidate(ctx context.Context, id, status string) (app.MemoryCandidate, *app.Memory, error) {
	ctx, cancel := operationContext(ctx, OperationMemoryCandidateResolve, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMemoryCandidateResolve, ctx); err != nil {
		return app.MemoryCandidate{}, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationMemoryCandidateResolve, ctx); err != nil {
		return app.MemoryCandidate{}, nil, err
	}
	candidate, ok := s.memoryCandidates[id]
	if !ok {
		return app.MemoryCandidate{}, nil, storeError(ctx, OperationMemoryCandidateResolve, StoreErrorNotFound, errors.New("memory candidate not found"))
	}
	if candidate.Status != "pending" {
		return app.MemoryCandidate{}, nil, storeError(ctx, OperationMemoryCandidateResolve, StoreErrorConflict, errors.New("memory candidate already resolved"))
	}
	now := postgresTime(time.Now().UTC())
	candidate.Status = status
	candidate.ResolvedAt = &now
	s.memoryCandidates[id] = cloneMemoryCandidate(candidate)
	var memory *app.Memory
	if status == "accepted" {
		accepted := normalizeMemory(app.Memory{
			ID: app.NewID("mem"), Kind: candidate.Kind, Content: candidate.Content,
			SourceID: candidate.RunID, CreatedAt: now,
		})
		s.memories[accepted.ID] = accepted
		memory = &accepted
	}
	s.appendAuditLocked("memory_candidate."+status, candidate.SessionID, candidate.RunID, "owner", candidate.Content, nil)
	s.appendEventLocked("memory_candidate."+status, candidate.SessionID, candidate.RunID, candidate)
	return cloneMemoryCandidate(candidate), memory, nil
}

func (s *MemoryStore) ListMemoryCandidates(ctx context.Context, status string) ([]app.MemoryCandidate, error) {
	ctx, cancel := operationContext(ctx, OperationMemoryCandidateList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMemoryCandidateList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationMemoryCandidateList, ctx); err != nil {
		return nil, err
	}
	out := []app.MemoryCandidate{}
	for _, candidate := range s.memoryCandidates {
		if status == "" || candidate.Status == status {
			out = append(out, cloneMemoryCandidate(candidate))
		}
	}
	slices.SortFunc(out, func(a, b app.MemoryCandidate) int {
		if order := b.CreatedAt.Compare(a.CreatedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (s *MemoryStore) SearchMemories(ctx context.Context, query string) ([]app.Memory, error) {
	ctx, cancel := operationContext(ctx, OperationMemorySearch, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMemorySearch, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationMemorySearch, ctx); err != nil {
		return nil, err
	}
	out := []app.Memory{}
	q := strings.ToLower(query)
	for _, memory := range s.memories {
		if q == "" || strings.Contains(strings.ToLower(memory.Content), q) || strings.Contains(strings.ToLower(memory.Kind), q) {
			out = append(out, memory)
		}
	}
	slices.SortFunc(out, func(a, b app.Memory) int {
		if order := b.CreatedAt.Compare(a.CreatedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (s *MemoryStore) UpdateMemory(ctx context.Context, id, kind, content string) (app.Memory, error) {
	ctx, cancel := operationContext(ctx, OperationMemoryUpdate, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMemoryUpdate, ctx); err != nil {
		return app.Memory{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationMemoryUpdate, ctx); err != nil {
		return app.Memory{}, err
	}
	memory, ok := s.memories[id]
	if !ok {
		return app.Memory{}, storeError(ctx, OperationMemoryUpdate, StoreErrorNotFound, errors.New("memory not found"))
	}
	memory.Kind = kind
	memory.Content = content
	s.memories[id] = memory
	sessionID := s.sessionIDForRunLocked(memory.SourceID)
	s.appendAuditLocked("memory.updated", sessionID, memory.SourceID, "owner", memory.Content, map[string]any{"memory_id": memory.ID, "kind": memory.Kind})
	s.appendEventLocked("memory.updated", sessionID, memory.SourceID, memory)
	return memory, nil
}

func (s *MemoryStore) DeleteMemory(ctx context.Context, id string) (app.Memory, error) {
	ctx, cancel := operationContext(ctx, OperationMemoryDelete, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMemoryDelete, ctx); err != nil {
		return app.Memory{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationMemoryDelete, ctx); err != nil {
		return app.Memory{}, err
	}
	memory, ok := s.memories[id]
	if !ok {
		return app.Memory{}, storeError(ctx, OperationMemoryDelete, StoreErrorNotFound, errors.New("memory not found"))
	}
	delete(s.memories, id)
	sessionID := s.sessionIDForRunLocked(memory.SourceID)
	s.appendAuditLocked("memory.deleted", sessionID, memory.SourceID, "owner", memory.Content, map[string]any{"memory_id": memory.ID, "kind": memory.Kind})
	s.appendEventLocked("memory.deleted", sessionID, memory.SourceID, memory)
	return memory, nil
}

func (s *MemoryStore) PruneMemories(ctx context.Context, cutoff time.Time) ([]app.Memory, error) {
	ctx, cancel := operationContext(ctx, OperationMemoryPrune, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMemoryPrune, ctx); err != nil {
		return nil, err
	}
	if cutoff.IsZero() {
		return []app.Memory{}, nil
	}
	cutoff = postgresTime(cutoff)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationMemoryPrune, ctx); err != nil {
		return nil, err
	}
	pruned := []app.Memory{}
	for id, memory := range s.memories {
		if memory.CreatedAt.IsZero() || !memory.CreatedAt.Before(cutoff) {
			continue
		}
		delete(s.memories, id)
		pruned = append(pruned, memory)
		sessionID := s.sessionIDForRunLocked(memory.SourceID)
		s.appendAuditLocked("memory.pruned", sessionID, memory.SourceID, "memory-retention", memory.Kind, map[string]any{
			"memory_id": memory.ID,
			"cutoff":    cutoff.Format(time.RFC3339),
		})
		s.appendEventLocked("memory.pruned", sessionID, memory.SourceID, memory)
	}
	slices.SortFunc(pruned, func(a, b app.Memory) int {
		if order := b.CreatedAt.Compare(a.CreatedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return pruned, nil
}
