package agent

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

type historyQueryCountingRepository struct {
	Repository
	recentMessages int
	recentTools    int
	recentEpisodes int
}

func (r *historyQueryCountingRepository) ListRecentMessages(ctx context.Context, sessionID string, cutoff time.Time, excludeMessageID string, limit int) ([]app.Message, error) {
	r.recentMessages++
	return r.Repository.ListRecentMessages(ctx, sessionID, cutoff, excludeMessageID, limit)
}

func (r *historyQueryCountingRepository) ListRecentToolCalls(ctx context.Context, sessionID string, cutoff time.Time, excludeRunID string, limit int) ([]app.ToolCall, error) {
	r.recentTools++
	return r.Repository.ListRecentToolCalls(ctx, sessionID, cutoff, excludeRunID, limit)
}

func (r *historyQueryCountingRepository) ListRecentEpisodeSummaries(ctx context.Context, sessionID string, cutoff time.Time, limit int) ([]app.EpisodeSummary, error) {
	r.recentEpisodes++
	return r.Repository.ListRecentEpisodeSummaries(ctx, sessionID, cutoff, limit)
}

func (r *historyQueryCountingRepository) recentCounts() (int, int, int) {
	return r.recentMessages, r.recentTools, r.recentEpisodes
}

func TestInvocationHistoryUsesOnlyBoundedRepositoryMethods(t *testing.T) {
	source, err := os.ReadFile("context_snapshot.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{".ListMessages(", ".ListToolCalls(", ".ListEpisodeSummaries("} {
		if strings.Contains(string(source), forbidden) {
			t.Fatalf("invocation history reintroduced complete-list method %q", forbidden)
		}
	}
}

func TestHandleMessageBuildsOneInvocationHistory(t *testing.T) {
	cfg := agentTestConfig()
	base := store.NewMemoryStore()
	session := storetest.MustCreateSession(t, base, "one invocation history")
	tools := toolhub.New(cfg, base)
	defer tools.Close()
	runtime := NewRuntime(base, tools, policy.New(cfg), modelrouter.New(cfg), nil)
	counting := &historyQueryCountingRepository{Repository: runtime.store}
	runtime.store = counting

	result, err := runtime.HandleMessage(t.Context(), session.ID, "解释一下机会成本")
	if err != nil {
		t.Fatal(err)
	}
	if result.Run.State != "completed" {
		t.Fatalf("conversation did not complete: %#v", result.Run)
	}
	if messages, tools, episodes := counting.recentCounts(); messages != 1 || tools != 1 || episodes != 1 {
		t.Fatalf("history query counts = (%d,%d,%d), want one bounded acquisition", messages, tools, episodes)
	}
}

func TestExternalMCPInvocationHistorySkipsAllHistoryQueries(t *testing.T) {
	base := store.NewMemoryStore()
	session := storetest.MustCreateSession(t, base, "external MCP empty history")
	run := externalMCPPolicyTestRun(session, "run_external_history_counts")
	counting := &historyQueryCountingRepository{Repository: base}
	runtime := Runtime{store: counting}

	history, err := runtime.buildInvocationHistory(t.Context(), run, "source_message")
	if err != nil {
		t.Fatal(err)
	}
	if len(history.MessageCandidates) != 0 || len(history.ToolCandidates) != 0 || len(history.EpisodeCandidates) != 0 {
		t.Fatalf("external MCP received history: %#v", history)
	}
	if messages, tools, episodes := counting.recentCounts(); messages != 0 || tools != 0 || episodes != 0 {
		t.Fatalf("external MCP history query counts = (%d,%d,%d), want zero", messages, tools, episodes)
	}
}
