package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestAggregateReadsMemoryAndFileContract(t *testing.T) {
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
			want := runAggregateReadContract(t, repository)
			if restart != nil {
				assertAggregateReads(t, restart(), want)
			}
		})
	}
}

func TestAggregateReadsPostgresContract(t *testing.T) {
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
	runAggregateReadContract(t, repository)
}

// aggregateReads is the tuple of bounded telemetry reads every backend must
// agree on.
type aggregateReads struct {
	messages   int
	runs       int
	toolCalls  int
	episodes   int
	modelCalls app.ModelCallStats
}

var aggregateReadBase = time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)

// runAggregateReadContract seeds visible and hidden session activity, checks
// every aggregate against the list-based computation it replaces, and
// returns the expected final aggregates for post-restart assertions.
func runAggregateReadContract(t *testing.T, repository testBackend) aggregateReads {
	t.Helper()
	ctx := t.Context()
	assertAggregateReads(t, repository, aggregateReads{})

	visible := mustCreateSession(t, repository, "visible session")
	hidden := mustCreateSessionWithScope(t, repository, "hidden session", app.DefaultOwnerID, t.TempDir(), "weixin", true)
	for index, message := range []app.Message{
		{ID: "msg-visible-1", SessionID: visible.ID, Role: "user", Content: "hello"},
		{ID: "msg-visible-2", SessionID: visible.ID, Role: "assistant", Content: "hi"},
		{ID: "msg-hidden-1", SessionID: hidden.ID, Role: "user", Content: "hidden"},
	} {
		message.CreatedAt = aggregateReadBase.Add(time.Duration(index) * time.Second)
		mustAddMessage(t, repository, message)
	}
	for _, run := range []app.AgentRun{
		{ID: "run-visible-1", SessionID: visible.ID, State: "completed", ModelLane: "fast", StartedAt: aggregateReadBase},
		{ID: "run-visible-2", SessionID: visible.ID, State: "running", ModelLane: "fast", StartedAt: aggregateReadBase.Add(time.Minute)},
		{ID: "run-hidden-1", SessionID: hidden.ID, State: "completed", ModelLane: "fast", StartedAt: aggregateReadBase.Add(2 * time.Minute)},
	} {
		if _, err := repository.SaveRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	for _, call := range []app.ToolCall{
		{ID: "tool-visible", SessionID: visible.ID, RunID: "run-visible-1", Tool: "files.read", Status: app.ToolCallStatusCompleted, Arguments: map[string]any{}, StartedAt: aggregateReadBase},
		{ID: "tool-hidden", SessionID: hidden.ID, RunID: "run-hidden-1", Tool: "files.read", Status: app.ToolCallStatusFailed, Arguments: map[string]any{}, StartedAt: aggregateReadBase},
	} {
		if _, err := repository.SaveToolCall(ctx, call); err != nil {
			t.Fatal(err)
		}
	}
	for _, summary := range []app.EpisodeSummary{
		{ID: "ep-visible", SessionID: visible.ID, RunID: "run-visible-1", Goal: "g", Outcome: "ok", ModelLane: "fast", Tools: []string{}, Approvals: []string{}, Summary: "s", CreatedAt: aggregateReadBase},
		{ID: "ep-hidden", SessionID: hidden.ID, RunID: "run-hidden-1", Goal: "g", Outcome: "ok", ModelLane: "fast", Tools: []string{}, Approvals: []string{}, Summary: "s", CreatedAt: aggregateReadBase},
	} {
		if _, err := repository.SaveEpisodeSummary(ctx, summary); err != nil {
			t.Fatal(err)
		}
	}
	for _, call := range []app.ModelCall{
		{ID: "mc-visible", SessionID: visible.ID, RunID: "run-visible-1", Lane: "fast", Profile: "fast", Model: "m", Operation: "chat", Status: app.ModelCallStatusCompleted, TotalTokens: 30, LatencyMS: 120, StartedAt: aggregateReadBase},
		{ID: "mc-hidden", SessionID: hidden.ID, RunID: "run-hidden-1", Lane: "fast", Profile: "fast", Model: "m", Operation: "chat", Status: app.ModelCallStatusFailed, Error: "boom", LatencyMS: 80, StartedAt: aggregateReadBase},
		{ID: "mc-unscoped", Lane: "embedding", Profile: "embedding", Model: "e", Operation: "embed", Status: app.ModelCallStatusCompleted, TotalTokens: 12, LatencyMS: 50, StartedAt: aggregateReadBase},
	} {
		if _, err := repository.SaveModelCall(ctx, call); err != nil {
			t.Fatal(err)
		}
	}
	assertAggregateReads(t, repository, aggregateReads{
		messages: 2, runs: 2, toolCalls: 2, episodes: 2,
		modelCalls: app.ModelCallStats{Count: 3, FailedCount: 1, LatencyMSTotal: 250, TotalTokens: 42},
	})

	// Re-saving an existing record replaces it: the aggregate follows the
	// persisted state and never double counts an upsert.
	if _, err := repository.SaveModelCall(ctx, app.ModelCall{
		ID: "mc-visible", SessionID: visible.ID, RunID: "run-visible-1", Lane: "fast", Profile: "fast", Model: "m", Operation: "chat",
		Status: app.ModelCallStatusFailed, Error: "retry failed", TotalTokens: 30, LatencyMS: 220, StartedAt: aggregateReadBase,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.SaveRun(ctx, app.AgentRun{ID: "run-visible-2", SessionID: visible.ID, State: "completed", ModelLane: "fast", StartedAt: aggregateReadBase.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	want := aggregateReads{
		messages: 2, runs: 2, toolCalls: 2, episodes: 2,
		modelCalls: app.ModelCallStats{Count: 3, FailedCount: 2, LatencyMSTotal: 350, TotalTokens: 42},
	}
	assertAggregateReads(t, repository, want)

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := repository.CountVisibleMessages(cancelled); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("canceled CountVisibleMessages error = %v", err)
	}
	if _, err := repository.CountVisibleRuns(cancelled); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("canceled CountVisibleRuns error = %v", err)
	}
	if _, err := repository.ModelCallStats(cancelled); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("canceled ModelCallStats error = %v", err)
	}
	if _, err := repository.CountToolCalls(cancelled); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("canceled CountToolCalls error = %v", err)
	}
	if _, err := repository.CountEpisodeSummaries(cancelled); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("canceled CountEpisodeSummaries error = %v", err)
	}
	return want
}

func assertAggregateReads(t *testing.T, repository testBackend, want aggregateReads) {
	t.Helper()
	got := readAggregates(t, repository)
	if got != want {
		t.Fatalf("aggregate reads = %+v, want %+v", got, want)
	}
	if legacy := listAggregates(t, repository); legacy != got {
		t.Fatalf("aggregate reads %+v disagree with the list-based computation %+v", got, legacy)
	}
}

func readAggregates(t *testing.T, repository testBackend) aggregateReads {
	t.Helper()
	ctx := t.Context()
	var out aggregateReads
	var err error
	if out.messages, err = repository.CountVisibleMessages(ctx); err != nil {
		t.Fatal(err)
	}
	if out.runs, err = repository.CountVisibleRuns(ctx); err != nil {
		t.Fatal(err)
	}
	if out.toolCalls, err = repository.CountToolCalls(ctx); err != nil {
		t.Fatal(err)
	}
	if out.episodes, err = repository.CountEpisodeSummaries(ctx); err != nil {
		t.Fatal(err)
	}
	if out.modelCalls, err = repository.ModelCallStats(ctx); err != nil {
		t.Fatal(err)
	}
	return out
}

// listAggregates computes the same tuple the way the metrics handler did
// before the bounded reads existed: listed sessions, then every list read.
func listAggregates(t *testing.T, repository testBackend) aggregateReads {
	t.Helper()
	ctx := t.Context()
	var out aggregateReads
	sessions, err := repository.ListSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range sessions {
		messages, err := repository.ListMessages(ctx, session.ID)
		if err != nil {
			t.Fatal(err)
		}
		out.messages += len(messages)
		runs, err := repository.ListRuns(ctx, session.ID)
		if err != nil {
			t.Fatal(err)
		}
		out.runs += len(runs)
	}
	toolCalls, err := repository.ListToolCalls(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	out.toolCalls = len(toolCalls)
	episodes, err := repository.ListEpisodeSummaries(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	out.episodes = len(episodes)
	modelCalls, err := repository.ListModelCalls(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range modelCalls {
		out.modelCalls.Count++
		if modelCallFailed(call) {
			out.modelCalls.FailedCount++
		}
		out.modelCalls.LatencyMSTotal += call.LatencyMS
		out.modelCalls.TotalTokens += call.TotalTokens
	}
	return out
}
