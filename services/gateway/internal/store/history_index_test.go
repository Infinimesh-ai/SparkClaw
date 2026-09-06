package store

import (
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// TestHistoryIndexesStayOrderedUnderOutOfOrderWritesAndResaves pins the
// per-session derived ordering that ListRecentToolCalls and
// ListRecentEpisodeSummaries walk. Records arrive in shuffled order, every
// tool call is saved twice (started, then completed) as the runtime does, one
// record moves to a new StartedAt on re-save, and episodes tie on CreatedAt so
// the ID tie-break is exercised. The File backend reloads the snapshot so the
// rebuilt index is checked as well.
func TestHistoryIndexesStayOrderedUnderOutOfOrderWritesAndResaves(t *testing.T) {
	for _, backend := range []struct {
		name  string
		count int
	}{{name: "memory", count: 3000}, {name: "file", count: 40}} {
		t.Run(backend.name, func(t *testing.T) {
			var repository testBackend
			var reopen func() testBackend
			switch backend.name {
			case "memory":
				repository = NewMemoryStore()
			case "file":
				path := filepath.Join(t.TempDir(), "state.json")
				file, err := NewFileStore(path)
				if err != nil {
					t.Fatal(err)
				}
				repository = file
				reopen = func() testBackend {
					reloaded, err := NewFileStore(path)
					if err != nil {
						t.Fatal(err)
					}
					return reloaded
				}
			}
			session := mustCreateSession(t, repository, "history index")
			moved := writeShuffledHistory(t, repository, session.ID, backend.count)
			assertHistoryIndexOrder(t, repository, session.ID, backend.count, moved)
			if reopen != nil {
				assertHistoryIndexOrder(t, reopen(), session.ID, backend.count, moved)
			}
		})
	}
}

func writeShuffledHistory(t testing.TB, repository testBackend, sessionID string, count int) app.ToolCall {
	t.Helper()
	ctx := t.Context()
	rng := rand.New(rand.NewPCG(7, 11))
	for _, n := range rng.Perm(count) {
		started := recentHistoryBase.Add(time.Duration(n) * time.Second)
		call := app.ToolCall{
			ID: fmt.Sprintf("tool-%05d", n), SessionID: sessionID, RunID: fmt.Sprintf("run-%05d", n),
			Tool: "files.read", Status: app.ToolCallStatusStarted, StartedAt: started,
		}
		if _, err := repository.SaveToolCall(ctx, call); err != nil {
			t.Fatal(err)
		}
		completed := started.Add(time.Second)
		call.Status, call.CompletedAt = app.ToolCallStatusCompleted, &completed
		if _, err := repository.SaveToolCall(ctx, call); err != nil {
			t.Fatal(err)
		}
		if _, err := repository.SaveEpisodeSummary(ctx, app.EpisodeSummary{
			ID: fmt.Sprintf("episode-%05d", n), SessionID: sessionID, RunID: call.RunID,
			Goal: call.ID, Outcome: "completed", CreatedAt: started,
		}); err != nil {
			t.Fatal(err)
		}
	}
	tied := recentHistoryBase.Add(time.Duration(count/2) * time.Second)
	if _, err := repository.SaveEpisodeSummary(ctx, app.EpisodeSummary{
		ID: "episode-00000-tie", SessionID: sessionID, RunID: "run-tie", Goal: "tie", Outcome: "completed", CreatedAt: tied,
	}); err != nil {
		t.Fatal(err)
	}
	moved := app.ToolCall{
		ID: "tool-00010", SessionID: sessionID, RunID: "run-00010", Tool: "files.read",
		Status: app.ToolCallStatusCompleted, StartedAt: recentHistoryBase.Add(time.Duration(count+5) * time.Second),
	}
	movedDone := moved.StartedAt.Add(time.Second)
	moved.CompletedAt = &movedDone
	if _, err := repository.SaveToolCall(ctx, moved); err != nil {
		t.Fatal(err)
	}
	return moved
}

func assertHistoryIndexOrder(t testing.TB, repository testBackend, sessionID string, count int, moved app.ToolCall) {
	t.Helper()
	cutoff := recentHistoryBase.Add(24 * time.Hour)
	tools, err := repository.ListRecentToolCalls(t.Context(), sessionID, cutoff, "", count+10)
	if err != nil || len(tools) != count {
		t.Fatalf("recent tools len=%d err=%v", len(tools), err)
	}
	if tools[0].ID != moved.ID || !tools[0].StartedAt.Equal(moved.StartedAt) {
		t.Fatalf("re-saved tool call did not move to its new position: %#v", tools[0])
	}
	seen := map[string]struct{}{}
	for index, call := range tools {
		if _, duplicate := seen[call.ID]; duplicate {
			t.Fatalf("tool call %s appears twice in recent history", call.ID)
		}
		seen[call.ID] = struct{}{}
		if index == 0 {
			continue
		}
		previous := tools[index-1]
		if call.StartedAt.After(previous.StartedAt) || (call.StartedAt.Equal(previous.StartedAt) && call.ID > previous.ID) {
			t.Fatalf("recent tools out of order at %d: %s@%s after %s@%s", index, call.ID, call.StartedAt, previous.ID, previous.StartedAt)
		}
	}
	episodes, err := repository.ListRecentEpisodeSummaries(t.Context(), sessionID, cutoff, count+10)
	if err != nil || len(episodes) != count+1 {
		t.Fatalf("recent episodes len=%d err=%v", len(episodes), err)
	}
	for index := 1; index < len(episodes); index++ {
		previous, current := episodes[index-1], episodes[index]
		if current.CreatedAt.After(previous.CreatedAt) || (current.CreatedAt.Equal(previous.CreatedAt) && current.ID < previous.ID) {
			t.Fatalf("recent episodes out of order at %d: %s@%s after %s@%s", index, current.ID, current.CreatedAt, previous.ID, previous.CreatedAt)
		}
	}
}

// BenchmarkMemoryStoreSaveToolCallLongSession measures the per-save cost of
// maintaining the per-session tool-call index as one session grows. Each
// iteration saves a started record and then its completed re-save.
func BenchmarkMemoryStoreSaveToolCallLongSession(b *testing.B) {
	repository := NewMemoryStore()
	session := mustCreateSession(b, repository, "benchmark")
	ctx := b.Context()
	for index := 0; b.Loop(); index++ {
		started := recentHistoryBase.Add(time.Duration(index) * time.Second)
		call := app.ToolCall{
			ID: fmt.Sprintf("tool-%08d", index), SessionID: session.ID, RunID: fmt.Sprintf("run-%08d", index),
			Tool: "files.read", Status: app.ToolCallStatusStarted, StartedAt: started,
		}
		if _, err := repository.SaveToolCall(ctx, call); err != nil {
			b.Fatal(err)
		}
		completed := started.Add(time.Second)
		call.Status, call.CompletedAt = app.ToolCallStatusCompleted, &completed
		if _, err := repository.SaveToolCall(ctx, call); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkMemoryStoreRebuildHistoryIndexes measures the snapshot-load path
// the File backend runs at startup for a session with 4000 tool calls and
// 4000 episode summaries.
func BenchmarkMemoryStoreRebuildHistoryIndexes(b *testing.B) {
	repository := NewMemoryStore()
	session := mustCreateSession(b, repository, "benchmark")
	ctx := b.Context()
	for index := range 4000 {
		started := recentHistoryBase.Add(time.Duration(index) * time.Second)
		if _, err := repository.SaveToolCall(ctx, app.ToolCall{
			ID: fmt.Sprintf("tool-%08d", index), SessionID: session.ID, RunID: fmt.Sprintf("run-%08d", index),
			Tool: "files.read", Status: app.ToolCallStatusCompleted, StartedAt: started, CompletedAt: &started,
		}); err != nil {
			b.Fatal(err)
		}
		if _, err := repository.SaveEpisodeSummary(ctx, app.EpisodeSummary{
			ID: fmt.Sprintf("episode-%08d", index), SessionID: session.ID, RunID: fmt.Sprintf("run-%08d", index),
			Goal: "goal", Outcome: "completed", CreatedAt: started,
		}); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for b.Loop() {
		repository.mu.Lock()
		repository.rebuildHistoryIndexesLocked()
		repository.mu.Unlock()
	}
}

// TestRebuildHistoryIndexesHandlesLongSessionsOnLoad loads one session with
// 60000 tool calls and 60000 episode summaries through the snapshot path the
// File backend uses at startup. Re-sorting the per-session index once per
// record made this quadratic and exceeded the default test timeout; the
// collect-then-sort rebuild finishes in well under a second.
func TestRebuildHistoryIndexesHandlesLongSessionsOnLoad(t *testing.T) {
	const count = 60000
	sessionID := "session-long"
	snapshot := Snapshot{
		Sessions:         map[string]app.Session{sessionID: {ID: sessionID, Title: "long", CreatedAt: recentHistoryBase, UpdatedAt: recentHistoryBase}},
		ToolCalls:        make(map[string]app.ToolCall, count),
		EpisodeSummaries: make(map[string]app.EpisodeSummary, count),
	}
	for index := range count {
		started := recentHistoryBase.Add(time.Duration(index) * time.Second)
		id := fmt.Sprintf("tool-%08d", index)
		snapshot.ToolCalls[id] = app.ToolCall{
			ID: id, SessionID: sessionID, RunID: fmt.Sprintf("run-%08d", index), Tool: "files.read",
			Status: app.ToolCallStatusCompleted, StartedAt: started, CompletedAt: &started,
		}
		episodeID := fmt.Sprintf("episode-%08d", index)
		snapshot.EpisodeSummaries[episodeID] = app.EpisodeSummary{
			ID: episodeID, SessionID: sessionID, RunID: fmt.Sprintf("run-%08d", index), Goal: "goal", Outcome: "completed", CreatedAt: started,
		}
	}
	repository := NewMemoryStore()
	repository.loadSnapshot(snapshot)

	cutoff := recentHistoryBase.Add(24 * time.Hour)
	tools, err := repository.ListRecentToolCalls(t.Context(), sessionID, cutoff, "", 3)
	if err != nil || len(tools) != 3 || tools[0].ID != fmt.Sprintf("tool-%08d", count-1) || tools[2].ID != fmt.Sprintf("tool-%08d", count-3) {
		t.Fatalf("recent tools after load = %#v err=%v", tools, err)
	}
	episodes, err := repository.ListRecentEpisodeSummaries(t.Context(), sessionID, cutoff, 3)
	if err != nil || len(episodes) != 3 || episodes[0].ID != fmt.Sprintf("episode-%08d", count-1) || episodes[2].ID != fmt.Sprintf("episode-%08d", count-3) {
		t.Fatalf("recent episodes after load = %#v err=%v", episodes, err)
	}
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	if len(repository.toolCallIDsBySession[sessionID]) != count || len(repository.episodeIDsBySession[sessionID]) != count {
		t.Fatalf("rebuilt index sizes = %d tools, %d episodes", len(repository.toolCallIDsBySession[sessionID]), len(repository.episodeIDsBySession[sessionID]))
	}
}
