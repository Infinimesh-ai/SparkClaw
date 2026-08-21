package gateway

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func mustAddMemoryCandidate(t testing.TB, repository store.MemoryRepository, candidate app.MemoryCandidate) app.MemoryCandidate {
	t.Helper()
	stored, err := repository.AddMemoryCandidate(t.Context(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func testResolveMemoryCandidate(t testing.TB, repository store.MemoryRepository, id, status string) (app.MemoryCandidate, *app.Memory, error) {
	t.Helper()
	return repository.ResolveMemoryCandidate(t.Context(), id, status)
}

func mustSearchMemories(t testing.TB, repository store.MemoryRepository, query string) []app.Memory {
	t.Helper()
	memories, err := repository.SearchMemories(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	return memories
}

func testUpdateMemory(t testing.TB, repository store.MemoryRepository, id, kind, content string) (app.Memory, error) {
	t.Helper()
	return repository.UpdateMemory(t.Context(), id, kind, content)
}

func mustListMemoryCandidates(t testing.TB, repository store.MemoryRepository, status string) []app.MemoryCandidate {
	t.Helper()
	candidates, err := repository.ListMemoryCandidates(t.Context(), status)
	if err != nil {
		t.Fatal(err)
	}
	return candidates
}
