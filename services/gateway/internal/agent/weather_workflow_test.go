package agent

import (
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/capability"
)

func TestWeatherRouteRecognizesGroundedLocations(t *testing.T) {
	registry := defaultWorkflowProfileRegistry()
	catalog := capability.MustDefaultCatalog()
	tests := []struct {
		query    string
		location string
	}{
		{"今天杭州天气", "杭州"},
		{"今天杭州天气 2026-07-17", "杭州"},
		{"2026-07-20杭州天气", "杭州"},
		{"杭州今天的天气", "杭州"},
		{"查一下上海天气", "上海"},
		{"杭州未来三小时天气", "杭州"},
		{"北京会下雨吗", "北京"},
		{"杭州今天会下雨吗", "杭州"},
		{"上海明天可能会下雪吗", "上海"},
		{"weather in Hangzhou", "Hangzhou"},
		{"Hangzhou forecast", "Hangzhou"},
	}
	for _, test := range tests {
		decision, err := registry.Recognize(catalog, workflowRecognitionContext{SourceTurnID: "turn", Content: test.query})
		if err != nil {
			t.Fatalf("recognize %q: %v", test.query, err)
		}
		if decision.Status != app.RouteMatched || len(decision.CapabilityPath) != 2 || decision.CapabilityPath[1] != app.CapabilityBrowserWeather ||
			decision.Slots.FactScope != app.RouteFactScopeWeatherSnapshot || decision.Slots.Query != test.query || decision.Slots.TargetKind != string(app.TargetKindLocation) ||
			decision.Slots.TargetRef != test.location || decision.Slots.Location != test.location {
			t.Fatalf("unexpected weather route for %q: %#v", test.query, decision)
		}
	}
}

func TestWeatherRouteClarifiesMissingLocation(t *testing.T) {
	decision, err := defaultWorkflowProfileRegistry().Recognize(capability.MustDefaultCatalog(), workflowRecognitionContext{SourceTurnID: "turn", Content: "今天天气"})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != app.RouteClarify || len(decision.CapabilityPath) != 2 || decision.CapabilityPath[1] != app.CapabilityBrowserWeather {
		t.Fatalf("missing location should clarify under browser.weather: %#v", decision)
	}
}

func TestWeatherResearchStaysOnInternetSearch(t *testing.T) {
	for _, query := range []string{"杭州天气预警官方来源", "杭州天气新闻", "对比三个网站的杭州天气", "杭州空气质量"} {
		decision, err := defaultWorkflowProfileRegistry().Recognize(capability.MustDefaultCatalog(), workflowRecognitionContext{SourceTurnID: "turn", Content: query})
		if err != nil {
			t.Fatalf("recognize %q: %v", query, err)
		}
		if decision.Status != app.RouteMatched || len(decision.CapabilityPath) != 2 || decision.CapabilityPath[1] != app.CapabilityBrowserInternetSearch {
			t.Fatalf("weather research should use internet search for %q: %#v", query, decision)
		}
	}
}

func TestWeatherWorkflowFreezesThreeSequentialScopes(t *testing.T) {
	catalog := capability.MustDefaultCatalog()
	route := app.RouteDecision{
		SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched, CatalogRevision: catalog.Revision(),
		CapabilityPath: []app.CapabilityID{"browser", app.CapabilityBrowserWeather},
		Slots:          app.RouteSlots{Operation: app.RouteOperationRead, FactScope: app.RouteFactScopeWeatherSnapshot, Query: "今天杭州天气", TargetKind: string(app.TargetKindLocation), TargetRef: "杭州", Location: "杭州", Format: "image"},
		Facts:          map[string]string{"location_source": "current_turn"},
	}
	resolved, err := defaultWorkflowProfileRegistry().Resolve(catalog, route, "turn")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Plan.ProfileID != app.WorkflowBrowserWeather || resolved.Plan.ResultProjection != app.WorkflowResultOutputsOnly || len(resolved.Plan.Nodes) != 1 {
		t.Fatalf("unexpected weather plan: %#v", resolved.Plan)
	}
	node := resolved.Plan.Nodes[0]
	if node.InitialStage != "query_info" || node.MaxAttempts != 3 || len(node.Transitions) != 2 || len(node.ArgumentBindings) != 4 {
		t.Fatalf("weather plan did not freeze three stages: %#v", node)
	}
	if node.InitialScope.Requirements[0].Name != app.ToolCapabilityInfoQuestion ||
		node.Transitions[0].Replace.Requirements[0].Name != app.ToolCapabilityWeatherStructure ||
		node.Transitions[1].Replace.Requirements[0].Name != app.ToolCapabilityWeatherRender {
		t.Fatalf("weather scopes are not sequential and exact: %#v", node)
	}
}

func TestWeatherStructureInstructionOmitsEvidenceForMissingFields(t *testing.T) {
	instruction := browserWeatherProfile{}.TransitionInstruction(app.ToolOutcome{
		Signals: []app.OutcomeSignal{app.OutcomeSignalInfoAnswerAvailable},
	}, app.NodeAssessment{})
	if !strings.Contains(instruction, "do not submit a value or evidence entry for a marked category") {
		t.Fatalf("weather structure instruction does not define missing-field evidence behavior: %q", instruction)
	}
	for _, ref := range []string{"summary:0", "fact:N", "source:N:snippet:M"} {
		if !strings.Contains(instruction, ref) {
			t.Fatalf("weather structure instruction does not expose Info ref %q: %q", ref, instruction)
		}
	}
	for _, shape := range []string{"standalone current reading", "if only a daily range is available", "complete range such as 28~34℃", "without reformatting", "strictly after the current system time", "mark hourly missing", "current may contain only condition, temperature_c, feels_like_c, humidity_pct, wind_kmh, and precipitation_mm", "never submit humidity_percent", "every hourly entry uses exactly time, condition, and temperature_c, never datetime", "all three hourly leaves require separate evidence entries", "daily are arrays", "min_temperature_c", "field_path, evidence_ref, and evidence_text", "Unknown optional values may be omitted or null", "daily[0].date", "hourly[0].condition", "never an unsupported field", "Never use field/ref/value aliases"} {
		if !strings.Contains(instruction, shape) {
			t.Fatalf("weather structure instruction does not enforce %q: %q", shape, instruction)
		}
	}
}

func TestInfoAnswerOutcomeAcceptsStructuredEvidenceWithoutSummary(t *testing.T) {
	tests := []struct {
		name   string
		result map[string]any
	}{
		{name: "fact", result: map[string]any{"summary": "", "key_facts": []any{map[string]any{"claim": "杭州当前多云。"}}}},
		{name: "source snippet", result: map[string]any{"sources": []any{map[string]any{"snippets": []string{"杭州当前气温31°C。"}}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			outcome := adaptInfoAnswerWorkflowOutcome(app.ToolCall{
				ID: "tc_info", Tool: "info.query", Status: "completed", Result: test.result,
			}, "weather")
			if !containsOutcomeSignal(outcome.Signals, app.OutcomeSignalInfoAnswerAvailable) || len(outcome.Refs) != 1 || outcome.Refs[0].Ref != "tc_info" {
				t.Fatalf("structured Info evidence should advance the workflow without summary: %#v", outcome)
			}
		})
	}
}

func TestLegacyTaskHintDoesNotSelectWeatherTools(t *testing.T) {
	hint := heuristicTaskHint("杭州今天天气怎么样")
	if containsString(hint.CandidateSkills, "weather_lookup") || containsString(hint.CandidateTools, "media.render_weather_card") || containsString(hint.CandidateTools, "info.query") {
		t.Fatalf("weather routing leaked back into TaskHint: %#v", hint)
	}
}
