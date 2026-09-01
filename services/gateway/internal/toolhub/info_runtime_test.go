package toolhub

import (
	"context"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/integrationrun"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/websearch"
)

type blockingInfoSearch struct {
	started chan struct{}
}

func (a *blockingInfoSearch) Search(ctx context.Context, _ websearch.Request) (websearch.Result, error) {
	close(a.started)
	<-ctx.Done()
	return websearch.Result{}, ctx.Err()
}

func TestInfoCredentialSwitchCancelsActiveRunWithTypedCause(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.Web.Search.Enabled = true
	hub := New(cfg, store.NewMemoryStore())
	t.Cleanup(func() { _ = hub.Close() })
	runs := integrationrun.New()
	hub.WithIntegrationRuns(runs)
	old := &blockingInfoSearch{started: make(chan struct{})}
	hub.ReplaceInfoAdapters(old, nil)

	runCtx, endRun := runs.Begin(t.Context(), "run-info")
	defer endRun(false)
	result := make(chan error, 1)
	go func() {
		_, err := hub.Execute(runCtx, "web.search", map[string]any{"query": "test"}, "", "run-info")
		result <- err
	}()
	<-old.started
	hub.ReplaceInfoAdapters(nil, nil)
	if err := <-result; app.ToolErrorCodeFrom(err) != app.ToolErrorInfoCredentialsChanged {
		t.Fatalf("switch error=%v code=%q", err, app.ToolErrorCodeFrom(err))
	}
	if cause := context.Cause(runCtx); app.ToolErrorCodeFrom(cause) != app.ToolErrorInfoCredentialsChanged {
		t.Fatalf("run cause=%v code=%q", cause, app.ToolErrorCodeFrom(cause))
	}
}

func TestInfoToolsRemainRegisteredAndFailTypedWithoutCredentials(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.Web.Search.Enabled = true
	hub := New(cfg, store.NewMemoryStore())
	t.Cleanup(func() { _ = hub.Close() })
	for _, name := range []string{"web.search", "weather.lookup"} {
		if _, ok := hub.Definition(name); !ok {
			t.Fatalf("%s was not registered", name)
		}
	}
	if _, err := hub.Execute(t.Context(), "web.search", map[string]any{"query": "test"}, "", "manual"); app.ToolErrorCodeFrom(err) != app.ToolErrorInfoNotConfigured {
		t.Fatalf("web.search error=%v code=%q", err, app.ToolErrorCodeFrom(err))
	}
	if _, err := hub.Execute(t.Context(), "weather.lookup", map[string]any{"location": "Shanghai"}, "", "manual"); app.ToolErrorCodeFrom(err) != app.ToolErrorInfoNotConfigured {
		t.Fatalf("weather.lookup error=%v code=%q", err, app.ToolErrorCodeFrom(err))
	}
}
