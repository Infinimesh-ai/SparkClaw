package toolhub

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func mustListMemoryCandidates(t testing.TB, repository store.MemoryRepository, status string) []app.MemoryCandidate {
	t.Helper()
	candidates, err := repository.ListMemoryCandidates(t.Context(), status)
	if err != nil {
		t.Fatal(err)
	}
	return candidates
}

func mustSearchMemories(t testing.TB, repository store.MemoryRepository, query string) []app.Memory {
	t.Helper()
	memories, err := repository.SearchMemories(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	return memories
}
