package store

import (
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func prepareMemoryCandidate(candidate app.MemoryCandidate, now time.Time) app.MemoryCandidate {
	if candidate.ID == "" {
		candidate.ID = app.NewID("mc")
	}
	if candidate.CreatedAt.IsZero() {
		candidate.CreatedAt = now
	}
	if candidate.Status == "" {
		candidate.Status = "pending"
	}
	return normalizeMemoryCandidate(candidate)
}

func normalizeMemoryCandidate(candidate app.MemoryCandidate) app.MemoryCandidate {
	candidate.CreatedAt = postgresTime(candidate.CreatedAt)
	if candidate.ResolvedAt != nil {
		resolvedAt := postgresTime(*candidate.ResolvedAt)
		candidate.ResolvedAt = &resolvedAt
	}
	return candidate
}

func cloneMemoryCandidate(candidate app.MemoryCandidate) app.MemoryCandidate {
	candidate.ResolvedAt = cloneTimePointer(candidate.ResolvedAt)
	return candidate
}

func cloneMemoryCandidateMap(values map[string]app.MemoryCandidate) map[string]app.MemoryCandidate {
	out := make(map[string]app.MemoryCandidate, len(values))
	for id, candidate := range values {
		out[id] = cloneMemoryCandidate(candidate)
	}
	return out
}

func normalizeMemory(memory app.Memory) app.Memory {
	memory.CreatedAt = postgresTime(memory.CreatedAt)
	return memory
}
