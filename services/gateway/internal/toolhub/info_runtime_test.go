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

func TestSharedInfoRuntimeFollowsCredentialChanges(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.Web.Search.Enabled = true
	parent := New(cfg, store.NewMemoryStore())
	local, _, session := executionScopeFixture(t, parent, nil)
	t.Cleanup(func() { _ = parent.Close(); _ = local.Close() })
	runs := integrationrun.New()
	parent.WithIntegrationRuns(runs)
	old := &blockingInfoSearch{started: make(chan struct{})}
	parent.ReplaceInfoAdapters(old, &weatherInfoStub{response: dedicatedWeatherResponse()})
	if !local.InfoConfigured() {
		t.Fatal("transient hub missed activated household credentials")
	}
	if _, err := local.Execute(t.Context(), "weather.lookup", map[string]any{"location": "杭州"}, session.ID, "weather"); err != nil {
		t.Fatal(err)
	}
	ctx, finish := runs.Begin(t.Context(), "transient-search")
	defer finish(false)
	result := make(chan error, 1)
	go func() {
		_, err := local.Execute(ctx, "web.search", map[string]any{"query": "test"}, session.ID, "transient-search")
		result <- err
	}()
	<-old.started
	parent.ReplaceInfoAdapters(nil, nil)
	if err := <-result; app.ToolErrorCodeFrom(err) != app.ToolErrorInfoCredentialsChanged {
		t.Fatalf("credential switch did not cancel transient call: %v", err)
	}
	if app.ToolErrorCodeFrom(context.Cause(ctx)) != app.ToolErrorInfoCredentialsChanged {
		t.Fatal("credential switch did not cancel transient run")
	}
	if _, err := local.Execute(t.Context(), "weather.lookup", map[string]any{"location": "杭州"}, session.ID, "new-weather"); app.ToolErrorCodeFrom(err) != app.ToolErrorInfoNotConfigured {
		t.Fatalf("cleared credentials still usable: %v", err)
	}
	parent.ReplaceInfoAdapters(old, &weatherInfoStub{response: dedicatedWeatherResponse()})
	betweenStages, finishStages := runs.Begin(t.Context(), "between-stages")
	defer finishStages(false)
	if _, err := local.Execute(betweenStages, "weather.lookup", map[string]any{"location": "杭州"}, session.ID, "between-stages"); err != nil {
		t.Fatal(err)
	}
	parent.ReplaceInfoAdapters(old, &weatherInfoStub{response: dedicatedWeatherResponse()})
	if app.ToolErrorCodeFrom(context.Cause(betweenStages)) != app.ToolErrorInfoCredentialsChanged {
		t.Fatal("credential switch did not stop the run between lookup and rendering")
	}
	if err := local.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := parent.Execute(t.Context(), "weather.lookup", map[string]any{"location": "杭州"}, "", "parent-weather"); err != nil {
		t.Fatalf("closing transient hub disrupted parent: %v", err)
	}
}
