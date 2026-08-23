package store

import (
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func mustAddMemoryCandidate(t testing.TB, repository MemoryRepository, candidate app.MemoryCandidate) app.MemoryCandidate {
	t.Helper()
	stored, err := repository.AddMemoryCandidate(t.Context(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func testResolveMemoryCandidate(t testing.TB, repository MemoryRepository, id, status string) (app.MemoryCandidate, *app.Memory, error) {
	t.Helper()
	return repository.ResolveMemoryCandidate(t.Context(), id, status)
}

func mustListMemoryCandidates(t testing.TB, repository MemoryRepository, status string) []app.MemoryCandidate {
	t.Helper()
	candidates, err := repository.ListMemoryCandidates(t.Context(), status)
	if err != nil {
		t.Fatal(err)
	}
	return candidates
}

func mustSearchMemories(t testing.TB, repository MemoryRepository, query string) []app.Memory {
	t.Helper()
	memories, err := repository.SearchMemories(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	return memories
}

func testUpdateMemory(t testing.TB, repository MemoryRepository, id, kind, content string) (app.Memory, error) {
	t.Helper()
	return repository.UpdateMemory(t.Context(), id, kind, content)
}

func testDeleteMemory(t testing.TB, repository MemoryRepository, id string) (app.Memory, error) {
	t.Helper()
	return repository.DeleteMemory(t.Context(), id)
}

func mustPruneMemories(t testing.TB, repository MemoryRepository, cutoff time.Time) []app.Memory {
	t.Helper()
	memories, err := repository.PruneMemories(t.Context(), cutoff)
	if err != nil {
		t.Fatal(err)
	}
	return memories
}
