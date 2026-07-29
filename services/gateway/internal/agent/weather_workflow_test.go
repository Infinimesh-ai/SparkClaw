package agent

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/capability"
)

func TestWeatherRouteRecognizesGroundedLocations(t *testing.T) {
	runtime, _, _, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
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
		decision := mustRouteIntent(t, runtime, test.query)
		if decision.Status != app.RouteMatched || len(decision.CapabilityPath) != 2 || decision.CapabilityPath[1] != app.CapabilityBrowserWeather ||
			decision.Slots.FactScope != app.RouteFactScopeWeatherSnapshot || decision.Slots.Query != materializeRoutedQuery(app.CapabilityBrowserWeather, test.query, currentSearchDate()) || decision.Slots.TargetKind != string(app.TargetKindLocation) ||
			decision.Slots.TargetRef != test.location || decision.Slots.Location != test.location {
			t.Fatalf("unexpected weather route for %q: %#v", test.query, decision)
		}
	}
}

func TestWeatherRouteClarifiesMissingLocation(t *testing.T) {
	runtime, _, _, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	decision := mustRouteIntent(t, runtime, "今天天气")
	if decision.Status != app.RouteClarify || len(decision.CapabilityPath) != 2 || decision.CapabilityPath[1] != app.CapabilityBrowserWeather {
		t.Fatalf("missing location should clarify under browser.weather: %#v", decision)
	}
}

func TestWeatherResearchStaysOnInternetSearch(t *testing.T) {
	runtime, _, _, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	for _, query := range []string{"杭州天气预警官方来源", "杭州天气新闻", "对比三个网站的杭州天气", "杭州空气质量"} {
		decision := mustRouteIntent(t, runtime, query)
		if decision.Status != app.RouteMatched || len(decision.CapabilityPath) != 2 || decision.CapabilityPath[1] != app.CapabilityBrowserInternetSearch {
			t.Fatalf("weather research should use internet search for %q: %#v", query, decision)
		}
	}
}

func TestWeatherWorkflowFreezesDedicatedLookupAndRenderScopes(t *testing.T) {
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
	if node.InitialStage != "lookup_weather" || node.MaxAttempts != 3 || len(node.Transitions) != 1 || len(node.ArgumentBindings) != 2 {
		t.Fatalf("weather plan did not freeze the dedicated two-stage path: %#v", node)
	}
	if node.InitialScope.Requirements[0].Name != app.ToolCapabilityInfoQuestion ||
		node.InitialScope.Requirements[0].Qualifiers[app.CapabilityQualifierProvider] != app.CapabilityProviderInfo ||
		node.Transitions[0].Replace.Requirements[0].Name != app.ToolCapabilityWeatherRender {
		t.Fatalf("weather scopes are not sequential and exact: %#v", node)
	}
	if node.ArgumentBindings[0].Capability != app.ToolCapabilityInfoQuestion ||
		node.ArgumentBindings[0].Argument != "location" ||
		node.ArgumentBindings[0].Source != app.ArgumentBindingRouteSlot ||
		node.ArgumentBindings[0].SourceKey != "target_ref" ||
		node.ArgumentBindings[1].Capability != app.ToolCapabilityWeatherRender ||
		node.ArgumentBindings[1].Argument != "weather_payload_ref" ||
		node.ArgumentBindings[1].Source != app.ArgumentBindingOutcomeRef {
		t.Fatalf("weather arguments are not bound to the frozen route and lookup outcome: %#v", node.ArgumentBindings)
	}
}

func TestWeatherTransitionInstructionOnlyRendersBoundTypedPayload(t *testing.T) {
	instruction := browserWeatherProfile{}.TransitionInstruction(app.ToolOutcome{
		Signals: []app.OutcomeSignal{app.OutcomeSignalWeatherPayloadAvailable},
	}, app.NodeAssessment{})
	if instruction != "workflow_requirement: Render the bound typed weather payload. Call media.render_weather_card without adding, rewriting, or reinterpreting weather fields." {
		t.Fatalf("weather transition instruction allows payload reinterpretation: %q", instruction)
	}
}

func TestWeatherPayloadOutcomeRequiresDedicatedTypedBoundary(t *testing.T) {
	valid := adaptWeatherPayloadWorkflowOutcome(app.ToolCall{
		ID: "tc_weather", Tool: "weather.lookup", Status: "completed",
		Result: map[string]any{"schema_version": 3, "request_id": "request", "location": "杭州"},
	}, "weather")
	if !containsOutcomeSignal(valid.Signals, app.OutcomeSignalWeatherPayloadAvailable) ||
		len(valid.Refs) != 1 || valid.Refs[0].Kind != "weather_payload" || valid.Refs[0].Ref != "tc_weather" {
		t.Fatalf("dedicated weather payload did not advance the workflow: %#v", valid)
	}
	for _, result := range []map[string]any{
		{"schema_version": 2, "request_id": "request", "location": "杭州"},
		{"schema_version": 3, "location": "杭州"},
		{"schema_version": 3, "request_id": "request"},
	} {
		outcome := adaptWeatherPayloadWorkflowOutcome(app.ToolCall{
			ID: "tc_invalid", Tool: "weather.lookup", Status: "completed", Result: result,
		}, "weather")
		if containsOutcomeSignal(outcome.Signals, app.OutcomeSignalWeatherPayloadAvailable) {
			t.Fatalf("incomplete weather payload advanced the workflow: %#v", outcome)
		}
	}
}

func TestLegacyTaskHintDoesNotSelectWeatherTools(t *testing.T) {
	hint := heuristicTaskHint("杭州今天天气怎么样")
	if containsString(hint.CandidateSkills, "weather_lookup") || containsString(hint.CandidateTools, "media.render_weather_card") || containsString(hint.CandidateTools, "info.query") {
		t.Fatalf("weather routing leaked back into TaskHint: %#v", hint)
	}
}
