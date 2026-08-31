package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestRecentHistoryMemoryAndFileContract(t *testing.T) {
	for _, backend := range []string{"memory", "file"} {
		t.Run(backend, func(t *testing.T) {
			var repository testBackend
			var restart func() testBackend
			switch backend {
			case "memory":
				repository = NewMemoryStore()
			case "file":
				path := filepath.Join(t.TempDir(), "state.json")
				file, err := NewFileStore(path)
				if err != nil {
					t.Fatal(err)
				}
				repository = file
				restart = func() testBackend {
					reloaded, err := NewFileStore(path)
					if err != nil {
						t.Fatal(err)
					}
					return reloaded
				}
			}
			session := mustCreateSession(t, repository, "recent history")
			runRecentHistoryContract(t, repository, session.ID)
			if restart != nil {
				runRecentHistoryReadAssertions(t, restart(), session.ID)
			}
		})
	}
}

func TestRecentHistoryPostgresContract(t *testing.T) {
	dsn := os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set SPARKCLAW_TEST_POSTGRES_DSN to run postgres store integration tests")
	}
	repository, err := NewPostgresStore(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	truncatePostgresStore(t, repository)
	session := mustCreateSession(t, repository, "recent history postgres")
	runRecentHistoryContract(t, repository, session.ID)
}

var recentHistoryBase = time.Date(2026, 8, 31, 8, 0, 0, 0, time.UTC)

func runRecentHistoryContract(t *testing.T, repository testBackend, sessionID string) {
	t.Helper()
	ctx := t.Context()
	cutoff := recentHistoryBase.Add(3 * time.Minute)
	for _, message := range []app.Message{
		{ID: "message-old", SessionID: sessionID, Role: "user", Content: "old", CreatedAt: recentHistoryBase},
		{ID: "message-a", SessionID: sessionID, Role: "assistant", Content: "a", CreatedAt: recentHistoryBase.Add(time.Minute)},
		{ID: "message-b", SessionID: sessionID, Role: "assistant", Content: "b", CreatedAt: recentHistoryBase.Add(time.Minute)},
		{ID: "message-new", SessionID: sessionID, Role: "user", Content: "new", CreatedAt: recentHistoryBase.Add(2 * time.Minute)},
		{ID: "message-current", SessionID: sessionID, Role: "user", Content: "current", CreatedAt: cutoff},
		{ID: "message-future", SessionID: sessionID, Role: "assistant", Content: "future", CreatedAt: cutoff.Add(time.Minute)},
	} {
		if _, err := repository.AddMessage(ctx, message); err != nil {
			t.Fatal(err)
		}
	}
	for _, run := range []app.AgentRun{
		{ID: "run-old", SessionID: sessionID, State: "completed", StartedAt: recentHistoryBase},
		{ID: "run-a", SessionID: sessionID, State: "completed", StartedAt: recentHistoryBase.Add(2 * time.Minute)},
		{ID: "run-b", SessionID: sessionID, State: "completed", StartedAt: recentHistoryBase.Add(2 * time.Minute)},
		{ID: "run-current", SessionID: sessionID, State: "completed", StartedAt: recentHistoryBase.Add(2*time.Minute + time.Second)},
		{ID: "run-incomplete", SessionID: sessionID, State: "executing", StartedAt: recentHistoryBase.Add(2*time.Minute + 2*time.Second)},
		{ID: "run-late", SessionID: sessionID, State: "completed", StartedAt: recentHistoryBase.Add(time.Minute)},
		{ID: "run-future", SessionID: sessionID, State: "completed", StartedAt: cutoff.Add(time.Minute)},
	} {
		if _, err := repository.SaveRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	completedAt := recentHistoryBase.Add(2*time.Minute + 30*time.Second)
	afterCutoff := cutoff.Add(time.Second)
	futureDone := cutoff.Add(2 * time.Minute)
	for _, call := range []app.ToolCall{
		{ID: "tool-old", SessionID: sessionID, RunID: "run-old", Tool: "files.read", Status: app.ToolCallStatusCompleted, StartedAt: recentHistoryBase, CompletedAt: &completedAt},
		{ID: "tool-a", SessionID: sessionID, RunID: "run-a", Tool: "files.read", Status: app.ToolCallStatusCompleted, StartedAt: recentHistoryBase.Add(2 * time.Minute), CompletedAt: &completedAt},
		{ID: "tool-b", SessionID: sessionID, RunID: "run-b", Tool: "files.read", Status: app.ToolCallStatusCompleted, StartedAt: recentHistoryBase.Add(2 * time.Minute), CompletedAt: &completedAt},
		{ID: "tool-current", SessionID: sessionID, RunID: "run-current", Tool: "files.read", Status: app.ToolCallStatusCompleted, StartedAt: recentHistoryBase.Add(2*time.Minute + time.Second), CompletedAt: &completedAt},
		{ID: "tool-incomplete", SessionID: sessionID, RunID: "run-incomplete", Tool: "files.read", Status: app.ToolCallStatusStarted, StartedAt: recentHistoryBase.Add(2*time.Minute + 2*time.Second)},
		{ID: "tool-late-completion", SessionID: sessionID, RunID: "run-late", Tool: "files.read", Status: app.ToolCallStatusCompleted, StartedAt: recentHistoryBase.Add(time.Minute), CompletedAt: &afterCutoff},
		{ID: "tool-future", SessionID: sessionID, RunID: "run-future", Tool: "files.read", Status: app.ToolCallStatusCompleted, StartedAt: cutoff.Add(time.Minute), CompletedAt: &futureDone},
	} {
		if _, err := repository.SaveToolCall(ctx, call); err != nil {
			t.Fatal(err)
		}
	}
	for _, summary := range []app.EpisodeSummary{
		{ID: "episode-old", SessionID: sessionID, RunID: "run-old", Goal: "old", Outcome: "completed", CreatedAt: recentHistoryBase},
		{ID: "episode-a", SessionID: sessionID, RunID: "run-a", Goal: "a", Outcome: "completed", CreatedAt: recentHistoryBase.Add(2 * time.Minute)},
		{ID: "episode-b", SessionID: sessionID, RunID: "run-b", Goal: "b", Outcome: "completed", CreatedAt: recentHistoryBase.Add(2 * time.Minute)},
		{ID: "episode-future", SessionID: sessionID, RunID: "run-future", Goal: "future", Outcome: "completed", CreatedAt: cutoff.Add(time.Minute)},
	} {
		if _, err := repository.SaveEpisodeSummary(ctx, summary); err != nil {
			t.Fatal(err)
		}
	}
	runRecentHistoryReadAssertions(t, repository, sessionID)

	if _, err := repository.ListRecentMessages(ctx, "", cutoff, "", 1); StoreErrorCodeOf(err) != StoreErrorInvalid {
		t.Fatalf("missing recent-message session error = %v", err)
	}
	if _, err := repository.ListRecentToolCalls(ctx, sessionID, time.Time{}, "", 1); StoreErrorCodeOf(err) != StoreErrorInvalid {
		t.Fatalf("missing recent-tool cutoff error = %v", err)
	}
	if _, err := repository.ListRecentEpisodeSummaries(ctx, sessionID, cutoff, 0); StoreErrorCodeOf(err) != StoreErrorInvalid {
		t.Fatalf("invalid recent-episode limit error = %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := repository.ListRecentMessages(cancelled, sessionID, cutoff, "", 1); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("canceled recent-message error = %v", err)
	}
}

func runRecentHistoryReadAssertions(t *testing.T, repository testBackend, sessionID string) {
	t.Helper()
	cutoff := recentHistoryBase.Add(3 * time.Minute)
	messages, err := repository.ListRecentMessages(t.Context(), sessionID, cutoff, "message-current", 3)
	if err != nil || len(messages) != 3 || messages[0].ID != "message-new" || messages[1].ID != "message-b" || messages[2].ID != "message-a" {
		t.Fatalf("recent messages = %#v err=%v", messages, err)
	}
	messages[0].Content = "mutated"
	again, err := repository.ListRecentMessages(t.Context(), sessionID, cutoff, "message-current", 1)
	if err != nil || len(again) != 1 || again[0].Content != "new" {
		t.Fatalf("recent messages returned mutable aliases: %#v err=%v", again, err)
	}
	tools, err := repository.ListRecentToolCalls(t.Context(), sessionID, cutoff, "run-current", 2)
	if err != nil || len(tools) != 2 || tools[0].ID != "tool-b" || tools[1].ID != "tool-a" {
		t.Fatalf("recent tools = %#v err=%v", tools, err)
	}
	episodes, err := repository.ListRecentEpisodeSummaries(t.Context(), sessionID, cutoff, 2)
	if err != nil || len(episodes) != 2 || episodes[0].ID != "episode-a" || episodes[1].ID != "episode-b" {
		t.Fatalf("recent episodes = %#v err=%v", episodes, err)
	}
}
