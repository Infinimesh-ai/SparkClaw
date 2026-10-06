package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/artifact"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/infinimeshinfo"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/integrationrun"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

type transientWeatherAdapter struct {
	location string
}

func (a *transientWeatherAdapter) Weather(_ context.Context, request infinimeshinfo.WeatherRequest) (infinimeshinfo.WeatherResponse, error) {
	a.location = request.Location.Name
	temperature := 23.0
	return infinimeshinfo.WeatherResponse{
		RequestID: "transient-weather", Status: "ok",
		Weather: infinimeshinfo.WeatherReport{
			Provider: "fixture", Location: infinimeshinfo.WeatherCoordinates{Name: "杭州"},
			Timezone: "Asia/Shanghai", ObservedAt: "2026-10-06T06:00:00Z",
			Current: &infinimeshinfo.WeatherCurrent{TemperatureC: &temperature, Condition: infinimeshinfo.WeatherConditionClear},
			Hourly:  []infinimeshinfo.WeatherHour{{Time: "2026-10-06T07:00:00Z", TemperatureC: &temperature, Condition: infinimeshinfo.WeatherConditionClear}},
			Daily:   []infinimeshinfo.WeatherDay{{Date: "2026-10-06", TemperatureMinC: &temperature, TemperatureMaxC: &temperature, Condition: infinimeshinfo.WeatherConditionClear}},
		},
		Sources: []infinimeshinfo.WeatherSource{{ID: "weather-source", SourceType: "weather", Provider: "fixture", RetrievedAt: "2026-10-06T06:00:00Z"}},
	}, nil
}

func TestTransientWeatherUsesActiveInfoWithoutPersistentContent(t *testing.T) {
	parent, persistent, _, closeParent := newWorkflowE2ERuntime(t, nil)
	defer closeParent()
	weather := &transientWeatherAdapter{}
	parent.tools.ReplaceInfoAdapters(nil, weather)
	runs := integrationrun.New()
	parent.tools.WithIntegrationRuns(runs)
	parent = parent.WithIntegrationRuns(runs)
	cfg := parent.tools.Config()
	if cfg.Plugins.Entries.InfinimeshInfo.Config.Configured() {
		t.Fatal("fixture must reproduce household credentials absent from startup config")
	}
	root := t.TempDir()
	cfg.Workspaces.DefaultRoot, cfg.Workspaces.Allowlist = root, []string{root}
	cfg.Storage.ArtifactDir = filepath.Join(root, "artifacts")
	local := store.NewMemoryStore()
	artifacts := artifact.NewStore(cfg.Storage)
	runtime, release, err := parent.WithExecutionScope(local, toolhub.ExecutionResources{OwnerID: app.DefaultOwnerID, WorkspaceRoot: root, Artifacts: artifacts})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := release(t.Context()); err != nil {
			t.Error(err)
		}
	}()
	session := storetest.MustCreateSessionWithScope(t, local, "transient weather", app.DefaultOwnerID, root, "web", false)
	result, err := runtime.HandleMessage(t.Context(), session.ID, "今日杭州的天气")
	if err != nil {
		t.Fatal(err)
	}
	assertWorkflowClosure(t, result, local, session.ID, app.CapabilityBrowserWeather, app.WorkflowBrowserWeather,
		[]string{"weather.lookup", "media.render_weather_card"},
		[]string{app.ToolCapabilityInfoQuestion, app.ToolCapabilityWeatherRender})
	if weather.location != "杭州" || len(result.Message.Attachments) != 1 {
		t.Fatalf("weather card missing: location=%q attachments=%#v", weather.location, result.Message.Attachments)
	}
	raw, err := os.ReadFile(filepath.Join(root, result.Message.Attachments[0].RelPath))
	if err != nil || len(raw) < 8 || string(raw[:8]) != "\x89PNG\r\n\x1a\n" {
		t.Fatalf("transient weather did not produce a PNG: %v", err)
	}
	if _, found, err := persistent.GetRun(t.Context(), result.Run.ID); err != nil || found {
		t.Fatalf("transient run leaked into persistent repository: found=%t err=%v", found, err)
	}
	if messages, err := persistent.ListMessages(t.Context(), session.ID); err != nil || len(messages) != 0 {
		t.Fatalf("transient messages leaked: count=%d err=%v", len(messages), err)
	}
	if calls, err := persistent.ListToolCalls(t.Context(), session.ID); err != nil || len(calls) != 0 {
		t.Fatalf("transient tool evidence leaked: count=%d err=%v", len(calls), err)
	}

	// A stopped R3 approval can leave a suspended dependency. Release must
	// remove it even when no later approval-resume call takes place.
	ctx, finish := runs.Begin(t.Context(), result.Run.ID)
	if err := runs.Use(result.Run.ID, "infinimesh-info", 1); err != nil {
		t.Fatal(err)
	}
	finish(true)
	if err := release(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, finish = runs.Begin(t.Context(), result.Run.ID)
	defer finish(false)
	if err := runs.Use(result.Run.ID, "infinimesh-info", 2); err != nil || context.Cause(ctx) != nil {
		t.Fatalf("released transient run retained credential dependencies: %v", err)
	}
}
