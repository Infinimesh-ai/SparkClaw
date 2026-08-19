package toolhub

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/infinimeshinfo"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type weatherInfoStub struct {
	request  infinimeshinfo.WeatherRequest
	response infinimeshinfo.WeatherResponse
	err      error
}

func (s *weatherInfoStub) Weather(_ context.Context, request infinimeshinfo.WeatherRequest) (infinimeshinfo.WeatherResponse, error) {
	s.request = request
	return s.response, s.err
}

func weatherFloat(value float64) *float64 {
	return &value
}

func dedicatedWeatherResponse() infinimeshinfo.WeatherResponse {
	return infinimeshinfo.WeatherResponse{
		RequestID: "weather-request",
		Status:    "ok",
		Weather: infinimeshinfo.WeatherReport{
			Provider:   "caiyun_weather",
			Location:   infinimeshinfo.WeatherCoordinates{Name: "杭州市", Latitude: weatherFloat(30.2741), Longitude: weatherFloat(120.1551)},
			Timezone:   "Asia/Shanghai",
			ObservedAt: "2026-07-29T05:00:00Z",
			Current: &infinimeshinfo.WeatherCurrent{
				TemperatureC: weatherFloat(31.2), ApparentTemperatureC: weatherFloat(33),
				Condition: "partly_cloudy", HumidityPercent: weatherFloat(62),
				WindSpeedKPH: weatherFloat(12.6), PrecipitationMMH: weatherFloat(0),
			},
			Hourly: []infinimeshinfo.WeatherHour{{
				Time: "2026-07-29T06:00:00Z", TemperatureC: weatherFloat(32),
				Condition: "partly_cloudy", PrecipitationProbabilityPercent: weatherFloat(10),
			}},
			Daily: []infinimeshinfo.WeatherDay{{
				Date: "2026-07-29", TemperatureMinC: weatherFloat(27), TemperatureMaxC: weatherFloat(35),
				Condition: "partly_cloudy", PrecipitationProbabilityPercent: weatherFloat(20),
			}},
		},
		Sources: []infinimeshinfo.WeatherSource{{
			ID: "weather-source", SourceType: "weather", Provider: "caiyun_weather", RetrievedAt: "2026-07-29T05:00:01Z",
		}},
		Usage: infinimeshinfo.Usage{CostCredits: 1, TokenType: "info.basic", CacheHit: true},
	}
}

func TestWeatherLookupMapsDedicatedTypedResponseWithoutCoordinates(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.Web.Search.Enabled = true
	configureTestInfoCredentials(&cfg)
	stub := &weatherInfoStub{response: dedicatedWeatherResponse()}
	hub := New(cfg, store.NewMemoryStore()).WithWeatherInfoAdapter(stub)

	result, err := hub.Execute(context.Background(), "weather.lookup", map[string]any{"location": " 杭州 "}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	if stub.request.Location.Name != "杭州" || len(stub.request.Granularity) != 3 ||
		stub.request.Days != 3 || stub.request.HourlySteps != 24 ||
		stub.request.Units != infinimeshinfo.WeatherUnitsMetric ||
		stub.request.Language != cfg.Plugins.Entries.InfinimeshInfo.Config.Language {
		t.Fatalf("weather lookup did not preserve the dedicated request contract: %#v", stub.request)
	}
	payload, ok := result.Output.(weatherPayload)
	if !ok || payload.SchemaVersion != WeatherPayloadSchemaVersion || payload.RequestID != "weather-request" ||
		payload.Location != "杭州市" || payload.Provider != "caiyun_weather" ||
		payload.Current.TemperatureC == nil || *payload.Current.TemperatureC != 31.2 ||
		len(payload.Hourly) != 1 || len(payload.Daily) != 1 || payload.SourceCount != 1 ||
		!payload.CacheHit || !payload.Untrusted {
		t.Fatalf("weather lookup did not produce the typed Workflow payload: %#v", result.Output)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"lat"`) || strings.Contains(string(raw), `"lon"`) ||
		strings.Contains(string(raw), "30.2741") || strings.Contains(string(raw), "120.1551") {
		t.Fatalf("weather Workflow payload leaked provider coordinates: %s", raw)
	}
}

func TestRenderWeatherCardCreatesMediaPNGFromDedicatedLookup(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Tools.Web.Search.Enabled = true
	configureTestInfoCredentials(&cfg)
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	st, err := store.NewFileStore(filepath.Join(root, "gateway-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	session := st.CreateSessionWithScope("weather", app.DefaultOwnerID, root, "web", false)
	runID := "run_weather"
	st.SaveRun(app.AgentRun{
		ID: runID, SessionID: session.ID,
		MessageContext: &app.MessageRunContext{ClientTimezone: "America/New_York"},
	})
	hub := New(cfg, st).WithWeatherInfoAdapter(&weatherInfoStub{response: dedicatedWeatherResponse()})
	if got := hub.weatherCardDisplayLocation(runID, "Asia/Shanghai").String(); got != "America/New_York" {
		t.Fatalf("weather display timezone = %q; want client timezone", got)
	}
	lookup, err := hub.Execute(context.Background(), "weather.lookup", map[string]any{"location": "杭州"}, session.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	st.SaveToolCall(app.ToolCall{
		ID: "tc_weather", SessionID: session.ID, RunID: runID,
		Tool: "weather.lookup", Status: "completed", Result: lookup.Output,
	})

	result, err := hub.Execute(context.Background(), "media.render_weather_card", map[string]any{
		"weather_payload_ref": "tc_weather",
	}, session.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf("unexpected output type %T", result.Output)
	}
	mediaPath, _ := output["media_path"].(string)
	if !strings.HasPrefix(mediaPath, "media/") || !strings.HasSuffix(mediaPath, ".png") {
		t.Fatalf("unexpected media path: %#v", output)
	}
	if output["content_type"] != "image/png" || output["width"] != weatherCardWidth || output["height"] != weatherCardHeight {
		t.Fatalf("unexpected weather card metadata: %#v", output)
	}
	if _, err := os.Stat(filepath.Join(root, mediaPath)); err != nil {
		t.Fatalf("weather card was not written: %v", err)
	}
	reloaded, err := store.NewFileStore(filepath.Join(root, "gateway-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if call, ok := reloaded.GetToolCall("tc_weather"); !ok || call.Tool != "weather.lookup" {
		t.Fatalf("default file backend did not persist the weather lookup boundary: %#v ok=%t", call, ok)
	}
}

func TestRenderWeatherCardRejectsIncompleteOrLegacyPayloadReferences(t *testing.T) {
	st := store.NewMemoryStore()
	session := st.CreateSessionWithScope("weather invalid", app.DefaultOwnerID, t.TempDir(), "web", false)
	st.SaveToolCall(app.ToolCall{
		ID: "tc_incomplete", SessionID: session.ID, RunID: "run", Tool: "weather.lookup", Status: "completed",
		Result: weatherPayload{Status: "completed", SchemaVersion: WeatherPayloadSchemaVersion, RequestID: "request", Location: "杭州"},
	})
	st.SaveToolCall(app.ToolCall{
		ID: "tc_wrong_tool", SessionID: session.ID, RunID: "run", Tool: "web.search", Status: "completed",
		Result: dedicatedWeatherResponse(),
	})
	hub := New(config.Default(), st)
	for _, ref := range []string{"tc_incomplete", "tc_wrong_tool"} {
		_, err := hub.Execute(context.Background(), "media.render_weather_card", map[string]any{
			"weather_payload_ref": ref,
		}, session.ID, "run")
		if err == nil {
			t.Fatalf("weather card accepted invalid payload reference %q", ref)
		}
	}
}

func TestRenderWeatherCardRejectsLegacyWeatherInputs(t *testing.T) {
	hub := New(config.Default(), store.NewMemoryStore())
	for _, field := range []string{"location", "raw_json", "raw_json_ref", "snapshot_ref"} {
		err := hub.Validate("media.render_weather_card", map[string]any{
			"weather_payload_ref": "tc_payload",
			field:                 `{"current_condition":[{"temp_C":"30"}]}`,
		})
		if err == nil {
			t.Fatalf("expected legacy weather input %q to be rejected", field)
		}
	}
}

func TestSplitTemperatureDisplayDropsCelsiusLetter(t *testing.T) {
	cases := []string{
		"30°C",
		"30°c",
		"30° C",
		"30 ℃",
		"30 C",
		"30c",
		"30 摄氏度",
		"30 celsius",
	}
	for _, input := range cases {
		number, unit := splitTemperatureDisplay(input)
		if number != "30" || unit != "°" {
			t.Fatalf("splitTemperatureDisplay(%q) = %q, %q; want 30, °", input, number, unit)
		}
	}
}

func TestWeatherForecastSlotsUseHoursOnly(t *testing.T) {
	slots := weatherForecastSlots(weatherCardData{
		Temperature: "35°C",
		Condition:   "Partly cloudy",
		UpdatedAt:   "2026-07-03 16:30",
		Hourly: []weatherForecastHour{
			{Time: "1700", Temp: "35°C", Condition: "Partly cloudy"},
			{Time: "1800", Temp: "32°C", Condition: "Cloudy"},
			{Time: "1900", Temp: "30°C", Condition: "Sunny"},
		},
		Forecast: []weatherForecastDay{
			{Date: "2026-07-04", MaxTemp: "36°C", Condition: "Rain"},
		},
	}, "partly")
	labels := []string{}
	for _, slot := range slots {
		labels = append(labels, slot.Label)
	}
	joined := strings.Join(labels, ",")
	if strings.Contains(joined, "明日") || strings.Contains(joined, "后天") {
		t.Fatalf("hourly slots must not include daily labels: %v", labels)
	}
	for _, want := range []string{"现在", "17时", "18时", "19时"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing hourly label %q in %v", want, labels)
		}
	}
}

func TestWeatherForecastSlotsFilterPastHours(t *testing.T) {
	slots := weatherForecastSlots(weatherCardData{
		Temperature: "35°C",
		Condition:   "Partly cloudy",
		UpdatedAt:   "2026-07-03 17:04",
		Hourly: []weatherForecastHour{
			{Time: "1700", Temp: "35°C", Condition: "Partly cloudy"},
			{Time: "1800", Temp: "32°C", Condition: "Cloudy"},
			{Time: "1900", Temp: "30°C", Condition: "Sunny"},
		},
	}, "partly")
	labels := []string{}
	for _, slot := range slots {
		labels = append(labels, slot.Label)
	}
	joined := strings.Join(labels, ",")
	if strings.Contains(joined, "17时") {
		t.Fatalf("past hourly label should be filtered using updated_at: %v", labels)
	}
	for _, want := range []string{"现在", "18时", "19时"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing hourly label %q in %v", want, labels)
		}
	}
}

func TestWeatherForecastSlotsSupportCrossMidnightTimestampsAndCapAtFive(t *testing.T) {
	slots := weatherForecastSlots(weatherCardData{
		Temperature:     "30°C",
		Condition:       "Cloudy",
		UpdatedAt:       "2026-07-17T23:30:00+08:00",
		displayLocation: time.FixedZone("Asia/Shanghai", 8*60*60),
		Hourly: []weatherForecastHour{
			{Time: "2026-07-17 23:00", Temp: "30°C", Condition: "Cloudy"},
			{Time: "2026-07-18 00:00", Temp: "29°C", Condition: "Cloudy"},
			{Time: "2026-07-18 01:00", Temp: "28°C", Condition: "Cloudy"},
			{Time: "2026-07-18 02:00", Temp: "27°C", Condition: "Cloudy"},
			{Time: "2026-07-18 03:00", Temp: "26°C", Condition: "Cloudy"},
			{Time: "2026-07-18 04:00", Temp: "25°C", Condition: "Cloudy"},
			{Time: "2026-07-18 05:00", Temp: "24°C", Condition: "Cloudy"},
		},
	}, "partly")
	if len(slots) != 6 {
		t.Fatalf("expected current plus five future hours, got %#v", slots)
	}
	labels := make([]string, 0, len(slots))
	for _, slot := range slots {
		labels = append(labels, slot.Label)
	}
	if got := strings.Join(labels, ","); got != "现在,0时,1时,2时,3时,4时" {
		t.Fatalf("unexpected cross-midnight forecast slots: %s", got)
	}
}

func TestWeatherTempRangeHidesConflictingRange(t *testing.T) {
	low, high := weatherTempRange(weatherCardData{
		Temperature:     "35°C",
		UpdatedAt:       "2026-07-17T10:00:00+08:00",
		displayLocation: time.FixedZone("Asia/Shanghai", 8*60*60),
		Forecast: []weatherForecastDay{
			{Date: "2026-07-17", MinTemp: "24°C", MaxTemp: "31°C"},
		},
	})
	if low != "" || high != "" {
		t.Fatalf("conflicting range should be hidden, got %q/%q", low, high)
	}

	low, high = weatherTempRange(weatherCardData{
		Temperature:     "28°C",
		UpdatedAt:       "2026-07-17T10:00:00+08:00",
		displayLocation: time.FixedZone("Asia/Shanghai", 8*60*60),
		Forecast: []weatherForecastDay{
			{Date: "2026-07-18", MinTemp: "25°C", MaxTemp: "36°C"},
			{Date: "2026-07-17", MinTemp: "24°C", MaxTemp: "35°C"},
		},
	})
	if low != "24°" || high != "35°" {
		t.Fatalf("valid range should render, got %q/%q", low, high)
	}

	low, high = weatherTempRange(weatherCardData{
		Temperature: "28°C", FeelsLike: "31°C", UpdatedAt: "2026-07-17T10:00:00+08:00",
		displayLocation: time.FixedZone("Asia/Shanghai", 8*60*60),
	})
	if low != "" || high != "" {
		t.Fatalf("missing daily range must not fall back to current/feels-like values, got %q/%q", low, high)
	}
}

func TestWeatherTimeDisplaysUseClientTimezone(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	minute, ok := weatherReferenceMinute("2026-07-03T09:04:00Z", location)
	if !ok {
		t.Fatal("expected RFC3339 updated_at to parse")
	}
	if minute != 5*60+4 {
		t.Fatalf("weatherReferenceMinute RFC3339 = %d; want New York 05:04", minute)
	}

	if got := displayUpdateTime("2026-07-03T09:04:00Z", location); got != "05:04" {
		t.Fatalf("displayUpdateTime RFC3339 = %q; want New York 05:04", got)
	}
	if got := displayHourLabel("2026-07-03T09:04:00Z", location); got != "5时" {
		t.Fatalf("displayHourLabel RFC3339 = %q; want New York 5时", got)
	}
}
