package toolhub

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/infinimeshinfo"
)

// WeatherPayloadSchemaVersion is the source of truth for the weather.lookup
// payload schema version. The agent workflow adapter gates the card-render
// stage on this exact value, so it must reference this constant.
const WeatherPayloadSchemaVersion = 3

type weatherPayload struct {
	Status        string                `json:"status"`
	SchemaVersion int                   `json:"schema_version"`
	RequestID     string                `json:"request_id"`
	Location      string                `json:"location"`
	Provider      string                `json:"provider"`
	Timezone      string                `json:"timezone,omitempty"`
	RetrievedAt   string                `json:"retrieved_at"`
	Current       weatherPayloadCurrent `json:"current"`
	Hourly        []weatherPayloadHour  `json:"hourly"`
	Daily         []weatherPayloadDay   `json:"daily"`
	SourceCount   int                   `json:"source_count"`
	CacheHit      bool                  `json:"cache_hit"`
	Untrusted     bool                  `json:"untrusted"`
}

type weatherPayloadCurrent struct {
	Condition       string   `json:"condition"`
	TemperatureC    *float64 `json:"temperature_c"`
	FeelsLikeC      *float64 `json:"feels_like_c,omitempty"`
	HumidityPct     *float64 `json:"humidity_pct,omitempty"`
	WindKMH         *float64 `json:"wind_kmh,omitempty"`
	PrecipitationMM *float64 `json:"precipitation_mm_h,omitempty"`
}

type weatherPayloadHour struct {
	Time                        string   `json:"time"`
	Condition                   string   `json:"condition"`
	TemperatureC                float64  `json:"temperature_c"`
	PrecipitationProbabilityPct *float64 `json:"precipitation_probability_pct,omitempty"`
}

type weatherPayloadDay struct {
	Date                        string   `json:"date"`
	Condition                   string   `json:"condition"`
	MinTemperatureC             float64  `json:"min_temperature_c"`
	MaxTemperatureC             float64  `json:"max_temperature_c"`
	PrecipitationProbabilityPct *float64 `json:"precipitation_probability_pct,omitempty"`
}

func weatherLookupDefinition() app.ToolDefinition {
	current := objectSchema([]string{"condition", "temperature_c"}, map[string]any{
		"condition":          boundedStringSchema(1, 32),
		"temperature_c":      rangedNumberSchema(-100, 80),
		"feels_like_c":       nullableRangedNumberSchema(-100, 80),
		"humidity_pct":       nullableRangedNumberSchema(0, 100),
		"wind_kmh":           nullableRangedNumberSchema(0, 500),
		"precipitation_mm_h": nullableRangedNumberSchema(0, 1000),
	})
	hour := objectSchema([]string{"time", "condition", "temperature_c"}, map[string]any{
		"time":                          boundedStringSchema(1, 64),
		"condition":                     boundedStringSchema(1, 32),
		"temperature_c":                 rangedNumberSchema(-100, 80),
		"precipitation_probability_pct": nullableRangedNumberSchema(0, 100),
	})
	day := objectSchema([]string{"date", "condition", "min_temperature_c", "max_temperature_c"}, map[string]any{
		"date":                          boundedStringSchema(10, 10),
		"condition":                     boundedStringSchema(1, 32),
		"min_temperature_c":             rangedNumberSchema(-100, 80),
		"max_temperature_c":             rangedNumberSchema(-100, 80),
		"precipitation_probability_pct": nullableRangedNumberSchema(0, 100),
	})
	return app.ToolDefinition{
		Name:        "weather.lookup",
		Description: "Query the dedicated Infinimesh Info weather endpoint for one bound city and return a validated metric payload ready for card rendering.",
		InputSchema: strictObjectSchema([]string{"location"}, map[string]any{
			"location": boundedStringSchema(1, 80),
		}),
		OutputSchema: objectSchema(
			[]string{"status", "schema_version", "request_id", "location", "provider", "retrieved_at", "current", "hourly", "daily", "source_count", "cache_hit", "untrusted"},
			map[string]any{
				"status":         stringSchema(),
				"schema_version": integerSchema(),
				"request_id":     stringSchema(),
				"location":       boundedStringSchema(1, 80),
				"provider":       stringSchema(),
				"timezone":       stringSchema(),
				"retrieved_at":   stringSchema(),
				"current":        current,
				"hourly":         boundedArraySchema(hour, 1, infinimeshinfo.MaxHourlyForecastHours),
				"daily":          boundedArraySchema(day, 1, infinimeshinfo.MaxDailyForecastDays),
				"source_count":   integerSchema(),
				"cache_hit":      booleanSchema(),
				"untrusted":      booleanSchema(),
			},
		),
		Risk:             app.RiskRead,
		RequiresApproval: false,
		Idempotent:       true,
		TimeoutMS:        30000,
		Sandbox:          "forbidden",
		Audit:            "always",
	}
}

func weatherRenderDefinition() app.ToolDefinition {
	return app.ToolDefinition{
		Name:        "media.render_weather_card",
		Description: "Render the bound validated weather payload into a PNG card and persist it under workspace media/.",
		InputSchema: strictObjectSchema([]string{"weather_payload_ref"}, map[string]any{
			"weather_payload_ref": boundedStringSchema(1, 128),
		}),
		OutputSchema: objectSchema([]string{"status", "media_path", "path", "content_type", "bytes", "width", "height", "summary", "untrusted"}, map[string]any{
			"status":       stringSchema(),
			"media_path":   stringSchema(),
			"path":         stringSchema(),
			"uri":          stringSchema(),
			"artifact_id":  stringSchema(),
			"content_type": stringSchema(),
			"bytes":        integerSchema(),
			"width":        integerSchema(),
			"height":       integerSchema(),
			"sha256":       stringSchema(),
			"summary":      stringSchema(),
			"untrusted":    booleanSchema(),
		}),
		Risk:             app.RiskDraft,
		RequiresApproval: false,
		Idempotent:       false,
		TimeoutMS:        5000,
		Sandbox:          "forbidden",
		Audit:            "always",
	}
}

func (h *ToolHub) lookupWeather(ctx context.Context, args map[string]any) (Result, error) {
	location := strings.TrimSpace(stringArg(args, "location", ""))
	if location == "" {
		return Result{}, errors.New("weather location is required")
	}
	if h.weatherInfo == nil {
		return Result{}, errors.New("infinimesh info weather adapter is unavailable")
	}
	response, err := h.weatherInfo.Weather(ctx, infinimeshinfo.WeatherRequest{
		Location: infinimeshinfo.WeatherRequestLocation{Name: location},
		Granularity: []infinimeshinfo.WeatherGranularity{
			infinimeshinfo.WeatherGranularityCurrent,
			infinimeshinfo.WeatherGranularityHourly,
			infinimeshinfo.WeatherGranularityDaily,
		},
		Days:        3,
		HourlySteps: 24,
		Units:       infinimeshinfo.WeatherUnitsMetric,
		Language:    h.cfg.Plugins.Entries.InfinimeshInfo.Config.Language,
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Output: weatherPayloadFromResponse(location, response)}, nil
}

func weatherPayloadFromResponse(requestedLocation string, response infinimeshinfo.WeatherResponse) weatherPayload {
	current := response.Weather.Current
	payload := weatherPayload{
		Status:        "completed",
		SchemaVersion: WeatherPayloadSchemaVersion,
		RequestID:     response.RequestID,
		Location:      strings.TrimSpace(response.Weather.Location.Name),
		Provider:      strings.TrimSpace(response.Weather.Provider),
		Timezone:      strings.TrimSpace(response.Weather.Timezone),
		RetrievedAt:   response.Weather.ObservedAt,
		SourceCount:   len(response.Sources),
		CacheHit:      response.Usage.CacheHit,
		Untrusted:     true,
		Current: weatherPayloadCurrent{
			Condition:       string(current.Condition),
			TemperatureC:    current.TemperatureC,
			FeelsLikeC:      current.ApparentTemperatureC,
			HumidityPct:     current.HumidityPercent,
			WindKMH:         current.WindSpeedKPH,
			PrecipitationMM: current.PrecipitationMMH,
		},
	}
	if payload.Location == "" {
		payload.Location = strings.TrimSpace(requestedLocation)
	}
	for _, hour := range response.Weather.Hourly {
		payload.Hourly = append(payload.Hourly, weatherPayloadHour{
			Time: hour.Time, Condition: string(hour.Condition), TemperatureC: *hour.TemperatureC,
			PrecipitationProbabilityPct: hour.PrecipitationProbabilityPercent,
		})
	}
	for _, day := range response.Weather.Daily {
		payload.Daily = append(payload.Daily, weatherPayloadDay{
			Date: day.Date, Condition: string(day.Condition),
			MinTemperatureC: *day.TemperatureMinC, MaxTemperatureC: *day.TemperatureMaxC,
			PrecipitationProbabilityPct: day.PrecipitationProbabilityPercent,
		})
	}
	return payload
}

func weatherPayloadFromCall(call app.ToolCall) (weatherPayload, error) {
	if call.Tool != "weather.lookup" || call.Status != "completed" {
		return weatherPayload{}, errors.New("weather card requires a completed dedicated weather lookup")
	}
	var payload weatherPayload
	if err := decodeJSONValue(call.Result, &payload); err != nil {
		return weatherPayload{}, errors.New("dedicated weather payload is invalid")
	}
	if payload.Status != "completed" || payload.SchemaVersion != WeatherPayloadSchemaVersion ||
		strings.TrimSpace(payload.RequestID) == "" || strings.TrimSpace(payload.Location) == "" ||
		strings.TrimSpace(payload.Provider) == "" || strings.TrimSpace(payload.RetrievedAt) == "" ||
		payload.Current.TemperatureC == nil || strings.TrimSpace(payload.Current.Condition) == "" ||
		len(payload.Hourly) == 0 || len(payload.Daily) == 0 || payload.SourceCount < 1 || !payload.Untrusted {
		return weatherPayload{}, errors.New("dedicated weather payload contract is incomplete")
	}
	if _, err := time.Parse(time.RFC3339, payload.RetrievedAt); err != nil {
		return weatherPayload{}, errors.New("dedicated weather payload has an invalid observation time")
	}
	return payload, nil
}

func weatherCardDataFromPayload(payload weatherPayload) weatherCardData {
	data := weatherCardData{
		Location:    payload.Location,
		UpdatedAt:   payload.RetrievedAt,
		Condition:   payload.Current.Condition,
		Temperature: formatWeatherMeasure(*payload.Current.TemperatureC, "°C"),
		Source:      "Infinimesh Info",
	}
	if payload.Current.FeelsLikeC != nil {
		data.FeelsLike = formatWeatherMeasure(*payload.Current.FeelsLikeC, "°C")
	}
	if payload.Current.HumidityPct != nil {
		data.Humidity = formatWeatherMeasure(*payload.Current.HumidityPct, "%")
	}
	if payload.Current.WindKMH != nil {
		data.Wind = formatWeatherMeasure(*payload.Current.WindKMH, " km/h")
	}
	if payload.Current.PrecipitationMM != nil {
		data.Precip = formatWeatherMeasure(*payload.Current.PrecipitationMM, " mm/h")
	}
	for _, hour := range payload.Hourly {
		forecast := weatherForecastHour{
			Time: hour.Time, Temp: formatWeatherMeasure(hour.TemperatureC, "°C"), Condition: hour.Condition,
		}
		if hour.PrecipitationProbabilityPct != nil {
			forecast.Rain = formatWeatherMeasure(*hour.PrecipitationProbabilityPct, "%")
		}
		data.Hourly = append(data.Hourly, forecast)
	}
	for _, day := range payload.Daily {
		forecast := weatherForecastDay{
			Date: day.Date, MinTemp: formatWeatherMeasure(day.MinTemperatureC, "°C"),
			MaxTemp: formatWeatherMeasure(day.MaxTemperatureC, "°C"), Condition: day.Condition,
		}
		if day.PrecipitationProbabilityPct != nil {
			forecast.Rain = formatWeatherMeasure(*day.PrecipitationProbabilityPct, "%")
		}
		data.Forecast = append(data.Forecast, forecast)
	}
	data.Suggestion = weatherSuggestion(data)
	return data
}

func formatWeatherMeasure(value float64, suffix string) string {
	return strconv.FormatFloat(value, 'f', -1, 64) + suffix
}

func decodeJSONValue(input any, output any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, output)
}

func boundedStringSchema(min, max int) map[string]any {
	return map[string]any{"type": "string", "minLength": min, "maxLength": max}
}

func rangedNumberSchema(min, max float64) map[string]any {
	return map[string]any{"type": "number", "minimum": min, "maximum": max}
}

func nullableRangedNumberSchema(min, max float64) map[string]any {
	return map[string]any{"type": []any{"number", "null"}, "minimum": min, "maximum": max}
}

func boundedArraySchema(items map[string]any, min, max int) map[string]any {
	return map[string]any{"type": "array", "items": items, "minItems": min, "maxItems": max}
}
