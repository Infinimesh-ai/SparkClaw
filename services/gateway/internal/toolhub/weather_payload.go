package toolhub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/websearch"
)

const weatherPayloadSchemaVersion = 2

const (
	weatherMissingCurrentCondition   = "current.condition"
	weatherMissingCurrentTemperature = "current.temperature_c"
	weatherMissingDaily              = "daily"
	weatherMissingHourly             = "hourly"
)

var weatherEvidenceNumberPattern = regexp.MustCompile(`[-+]?\d+(?:\.\d+)?`)

type weatherStructureInput struct {
	InfoAnswerRef string                 `json:"info_answer_ref"`
	Location      string                 `json:"location"`
	Current       weatherPayloadCurrent  `json:"current"`
	Hourly        []weatherPayloadHour   `json:"hourly,omitempty"`
	Daily         []weatherPayloadDay    `json:"daily,omitempty"`
	MissingFields []string               `json:"missing_fields"`
	Evidence      []weatherFieldEvidence `json:"evidence"`
}

type weatherPayload struct {
	Status        string                 `json:"status"`
	SchemaVersion int                    `json:"schema_version"`
	Location      string                 `json:"location"`
	RetrievedAt   string                 `json:"retrieved_at"`
	Current       weatherPayloadCurrent  `json:"current"`
	Hourly        []weatherPayloadHour   `json:"hourly,omitempty"`
	Daily         []weatherPayloadDay    `json:"daily,omitempty"`
	MissingFields []string               `json:"missing_fields"`
	Evidence      []weatherFieldEvidence `json:"evidence"`
	EvidenceCount int                    `json:"evidence_count"`
	SourceCallID  string                 `json:"source_call_id"`
	Untrusted     bool                   `json:"untrusted"`
}

type weatherPayloadCurrent struct {
	Condition       string   `json:"condition,omitempty"`
	TemperatureC    *float64 `json:"temperature_c,omitempty"`
	FeelsLikeC      *float64 `json:"feels_like_c,omitempty"`
	HumidityPct     *float64 `json:"humidity_pct,omitempty"`
	WindKMH         *float64 `json:"wind_kmh,omitempty"`
	PrecipitationMM *float64 `json:"precipitation_mm,omitempty"`
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

type weatherFieldEvidence struct {
	FieldPath    string `json:"field_path"`
	EvidenceRef  string `json:"evidence_ref"`
	EvidenceText string `json:"evidence_text"`
}

type infoEvidenceResult struct {
	RequestID   string              `json:"request_id"`
	Query       string              `json:"query"`
	Summary     string              `json:"summary"`
	KeyFacts    []websearch.KeyFact `json:"key_facts"`
	Sources     []websearch.Item    `json:"sources"`
	Citations   []string            `json:"citations"`
	RetrievedAt string              `json:"retrieved_at"`
	Untrusted   bool                `json:"untrusted"`
}

type weatherExpectedEvidence struct {
	text   string
	date   string
	number *float64
	units  []string
}

var (
	weatherTemperatureUnits   = []string{"°c", "℃", "摄氏", "celsius", "°", "number:度"}
	weatherPercentageUnits    = []string{"%", "百分之"}
	weatherWindSpeedUnits     = []string{"km/h", "kmh", "公里/小时", "公里每小时", "千米/小时", "千米每小时"}
	weatherPrecipitationUnits = []string{"mm", "毫米"}
)

func weatherStructureDefinition() app.ToolDefinition {
	current := strictObjectSchema([]string{}, map[string]any{
		"condition":        nullableBoundedStringSchema(1, 80),
		"temperature_c":    nullableRangedNumberSchema(-100, 80),
		"feels_like_c":     nullableRangedNumberSchema(-100, 80),
		"humidity_pct":     nullableRangedNumberSchema(0, 100),
		"wind_kmh":         nullableRangedNumberSchema(0, 500),
		"precipitation_mm": nullableRangedNumberSchema(0, 1000),
	})
	hour := strictObjectSchema([]string{"time", "condition", "temperature_c"}, map[string]any{
		"time":                          boundedStringSchema(1, 64),
		"condition":                     boundedStringSchema(1, 80),
		"temperature_c":                 rangedNumberSchema(-100, 80),
		"precipitation_probability_pct": nullableRangedNumberSchema(0, 100),
	})
	day := strictObjectSchema([]string{"date", "min_temperature_c", "max_temperature_c"}, map[string]any{
		"date":                          boundedStringSchema(1, 64),
		"condition":                     nullableBoundedStringSchema(1, 80),
		"min_temperature_c":             rangedNumberSchema(-100, 80),
		"max_temperature_c":             rangedNumberSchema(-100, 80),
		"precipitation_probability_pct": nullableRangedNumberSchema(0, 100),
	})
	evidence := strictObjectSchema([]string{"field_path", "evidence_ref", "evidence_text"}, map[string]any{
		"field_path":    boundedStringSchema(1, 128),
		"evidence_ref":  boundedStringSchema(1, 128),
		"evidence_text": boundedStringSchema(1, 1200),
	})
	missingField := map[string]any{"enum": []any{
		weatherMissingCurrentCondition,
		weatherMissingCurrentTemperature,
		weatherMissingDaily,
		weatherMissingHourly,
	}}
	return app.ToolDefinition{
		Name:        "weather.structure_payload",
		Description: "Persist only weather values supported by the bound Info evidence directory, with explicit missing markers for unavailable requested data.",
		InputSchema: strictObjectSchema([]string{"info_answer_ref", "location", "current", "missing_fields", "evidence"}, map[string]any{
			"info_answer_ref": boundedStringSchema(1, 128),
			"location":        boundedStringSchema(1, 120),
			"current":         current,
			"hourly":          boundedArraySchema(hour, 0, 5),
			"daily":           boundedArraySchema(day, 0, 7),
			"missing_fields":  boundedArraySchema(missingField, 0, 4),
			"evidence":        boundedArraySchema(evidence, 0, 64),
		}),
		OutputSchema: objectSchema([]string{"status", "schema_version", "location", "retrieved_at", "current", "missing_fields", "evidence", "evidence_count", "source_call_id", "untrusted"}, map[string]any{
			"status":         stringSchema(),
			"schema_version": integerSchema(),
			"location":       stringSchema(),
			"retrieved_at":   stringSchema(),
			"current":        objectValueSchema(),
			"hourly":         arraySchema(objectValueSchema()),
			"daily":          arraySchema(objectValueSchema()),
			"missing_fields": stringArraySchema(),
			"evidence":       arraySchema(objectValueSchema()),
			"evidence_count": integerSchema(),
			"source_call_id": stringSchema(),
			"untrusted":      booleanSchema(),
		}),
		Risk:             app.RiskDraft,
		RequiresApproval: false,
		Idempotent:       true,
		TimeoutMS:        1000,
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

func (h *ToolHub) structureWeatherPayload(_ context.Context, args map[string]any, sessionID, runID string) (Result, error) {
	if h.store == nil {
		return Result{}, errors.New("weather payload state is unavailable")
	}
	var input weatherStructureInput
	if err := decodeJSONValue(args, &input); err != nil {
		return Result{}, fmt.Errorf("weather payload arguments are invalid: %w", err)
	}
	input.InfoAnswerRef = strings.TrimSpace(input.InfoAnswerRef)
	input.Location = strings.TrimSpace(input.Location)
	input.Current.Condition = strings.TrimSpace(input.Current.Condition)
	input.Evidence = weatherEvidenceForAvailableFields(input.Evidence, input.MissingFields)
	call, ok := h.store.GetToolCall(input.InfoAnswerRef)
	if !ok || call.SessionID != sessionID || call.RunID != runID || call.Tool != "info.query" || call.Status != "completed" {
		return Result{}, errors.New("weather payload requires the bound completed Info evidence")
	}
	var infoResult infoEvidenceResult
	if err := decodeJSONValue(call.Result, &infoResult); err != nil {
		return Result{}, errors.New("bound Info evidence is invalid")
	}
	directory := infoEvidenceDirectory(infoResult)
	if !infoResult.Untrusted || !websearch.InfoEvidenceProjectionHasEvidence(directory) {
		return Result{}, errors.New("bound Info answer has no usable untrusted evidence")
	}
	input = downgradeInvalidForecastSections(input, directory)
	if err := validateWeatherStructure(input, directory); err != nil {
		return Result{}, err
	}
	payload := weatherPayload{
		Status:        "completed",
		SchemaVersion: weatherPayloadSchemaVersion,
		Location:      input.Location,
		RetrievedAt:   strings.TrimSpace(infoResult.RetrievedAt),
		Current:       input.Current,
		Hourly:        input.Hourly,
		Daily:         input.Daily,
		MissingFields: append([]string{}, input.MissingFields...),
		Evidence:      input.Evidence,
		EvidenceCount: len(input.Evidence),
		SourceCallID:  call.ID,
		Untrusted:     true,
	}
	if payload.RetrievedAt == "" {
		if call.CompletedAt == nil {
			return Result{}, errors.New("bound Info evidence has no retrieval time")
		}
		payload.RetrievedAt = call.CompletedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	return Result{Output: payload}, nil
}

func validateWeatherStructure(input weatherStructureInput, directory websearch.InfoEvidenceProjection) error {
	if input.Location == "" {
		return errors.New("weather payload location is required")
	}
	if err := validateWeatherAvailability(input.Current, input.Hourly, input.Daily, input.MissingFields); err != nil {
		return err
	}
	if err := validateWeatherRanges(input); err != nil {
		return err
	}
	evidenceIndex := websearch.InfoEvidenceTextIndex(directory)
	expected := expectedWeatherEvidence(input)
	seen := map[string]bool{}
	for _, evidence := range input.Evidence {
		path := strings.TrimSpace(evidence.FieldPath)
		want, ok := expected[path]
		if !ok {
			return fmt.Errorf("weather evidence field %q is unknown", path)
		}
		if seen[path] {
			return fmt.Errorf("weather evidence field %q is duplicated", path)
		}
		evidenceText, ok := evidenceIndex[strings.TrimSpace(evidence.EvidenceRef)]
		text := strings.TrimSpace(evidence.EvidenceText)
		if !ok || text == "" || !weatherEvidenceContainsQuote(evidenceText, text) {
			return fmt.Errorf("weather evidence for %q is not present in the bound Info answer", path)
		}
		if want.number != nil {
			if !weatherEvidenceContainsNumber(text, *want.number) {
				return fmt.Errorf("weather evidence for %q does not contain the submitted value", path)
			}
			if path == weatherMissingCurrentTemperature && len(weatherEvidenceNumberPattern.FindAllString(text, -1)) != 1 {
				return fmt.Errorf("weather evidence for %q is not a standalone current reading", path)
			}
			unitMatches := weatherEvidenceContainsUnit(text, *want.number, want.units)
			if !unitMatches && strings.HasPrefix(path, "daily[") {
				unitMatches = weatherEvidenceRangeContainsUnit(text, *want.number, want.units)
			}
			if !unitMatches {
				return fmt.Errorf("weather evidence for %q does not contain the required unit", path)
			}
		} else if want.date != "" && !weatherEvidenceContainsDate(text, want.date) {
			return fmt.Errorf("weather evidence for %q does not contain the submitted date", path)
		} else if want.text != "" && !strings.Contains(strings.ToLower(text), strings.ToLower(want.text)) {
			return fmt.Errorf("weather evidence for %q does not contain the submitted text", path)
		}
		seen[path] = true
	}
	for path := range expected {
		if !seen[path] {
			return fmt.Errorf("weather field %q has no bound Info evidence", path)
		}
	}
	return nil
}

func weatherEvidenceContainsQuote(source, quote string) bool {
	if strings.Contains(source, quote) {
		return true
	}
	normalize := func(value string) string {
		value = strings.NewReplacer("*", "", "_", "", "`", "", "\\", "").Replace(value)
		return strings.Join(strings.Fields(value), " ")
	}
	normalizedQuote := normalize(quote)
	return normalizedQuote != "" && strings.Contains(normalize(source), normalizedQuote)
}

func weatherEvidenceForAvailableFields(evidence []weatherFieldEvidence, missingFields []string) []weatherFieldEvidence {
	missing := make(map[string]bool, len(missingFields))
	for _, field := range missingFields {
		missing[strings.TrimSpace(field)] = true
	}
	filtered := make([]weatherFieldEvidence, 0, len(evidence))
	for _, item := range evidence {
		path := strings.TrimSpace(item.FieldPath)
		if missing[path] || (missing[weatherMissingDaily] && strings.HasPrefix(path, "daily[")) ||
			(missing[weatherMissingHourly] && strings.HasPrefix(path, "hourly[")) {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func downgradeInvalidForecastSections(input weatherStructureInput, directory websearch.InfoEvidenceProjection) weatherStructureInput {
	sections := []struct {
		missingField string
		prefix       string
		available    func() bool
		candidate    func() weatherStructureInput
		clear        func()
	}{
		{
			missingField: weatherMissingHourly,
			prefix:       "hourly[",
			available:    func() bool { return len(input.Hourly) > 0 },
			candidate: func() weatherStructureInput {
				return weatherStructureInput{
					Location: input.Location, Hourly: input.Hourly,
					MissingFields: []string{weatherMissingCurrentCondition, weatherMissingCurrentTemperature, weatherMissingDaily},
					Evidence:      weatherEvidenceWithPrefix(input.Evidence, "hourly["),
				}
			},
			clear: func() { input.Hourly = nil },
		},
		{
			missingField: weatherMissingDaily,
			prefix:       "daily[",
			available:    func() bool { return len(input.Daily) > 0 },
			candidate: func() weatherStructureInput {
				return weatherStructureInput{
					Location: input.Location, Daily: input.Daily,
					MissingFields: []string{weatherMissingCurrentCondition, weatherMissingCurrentTemperature, weatherMissingHourly},
					Evidence:      weatherEvidenceWithPrefix(input.Evidence, "daily["),
				}
			},
			clear: func() { input.Daily = nil },
		},
	}
	for _, section := range sections {
		if !section.available() || containsStringValue(input.MissingFields, section.missingField) {
			continue
		}
		if err := validateWeatherStructure(section.candidate(), directory); err == nil {
			continue
		}
		section.clear()
		input.MissingFields = append(input.MissingFields, section.missingField)
		input.Evidence = weatherEvidenceWithoutPrefix(input.Evidence, section.prefix)
	}
	return input
}

func weatherEvidenceWithPrefix(evidence []weatherFieldEvidence, prefix string) []weatherFieldEvidence {
	filtered := make([]weatherFieldEvidence, 0, len(evidence))
	for _, item := range evidence {
		if strings.HasPrefix(strings.TrimSpace(item.FieldPath), prefix) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func weatherEvidenceWithoutPrefix(evidence []weatherFieldEvidence, prefix string) []weatherFieldEvidence {
	filtered := make([]weatherFieldEvidence, 0, len(evidence))
	for _, item := range evidence {
		if !strings.HasPrefix(strings.TrimSpace(item.FieldPath), prefix) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func containsStringValue(values []string, expected string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == expected {
			return true
		}
	}
	return false
}

func validateWeatherAvailability(current weatherPayloadCurrent, hourly []weatherPayloadHour, daily []weatherPayloadDay, missingFields []string) error {
	allowed := map[string]bool{
		weatherMissingCurrentCondition:   true,
		weatherMissingCurrentTemperature: true,
		weatherMissingDaily:              true,
		weatherMissingHourly:             true,
	}
	missing := map[string]bool{}
	for _, raw := range missingFields {
		path := strings.TrimSpace(raw)
		if !allowed[path] {
			return fmt.Errorf("weather missing field %q is unknown", path)
		}
		if missing[path] {
			return fmt.Errorf("weather missing field %q is duplicated", path)
		}
		missing[path] = true
	}
	availability := []struct {
		path      string
		available bool
	}{
		{weatherMissingCurrentCondition, strings.TrimSpace(current.Condition) != ""},
		{weatherMissingCurrentTemperature, current.TemperatureC != nil},
		{weatherMissingDaily, len(daily) > 0},
		{weatherMissingHourly, len(hourly) > 0},
	}
	for _, item := range availability {
		switch {
		case item.available && missing[item.path]:
			return fmt.Errorf("weather field %q cannot contain data and be marked missing", item.path)
		case !item.available && !missing[item.path]:
			return fmt.Errorf("weather field %q has neither Info data nor an explicit missing marker", item.path)
		}
	}
	return nil
}

func validateWeatherRanges(input weatherStructureInput) error {
	if input.Current.TemperatureC != nil && (*input.Current.TemperatureC < -100 || *input.Current.TemperatureC > 80) {
		return errors.New("current temperature is outside the supported range")
	}
	for name, value := range map[string]*float64{
		"feels_like_c": input.Current.FeelsLikeC, "humidity_pct": input.Current.HumidityPct,
		"wind_kmh": input.Current.WindKMH, "precipitation_mm": input.Current.PrecipitationMM,
	} {
		if value == nil {
			continue
		}
		min, max := -100.0, 1000.0
		switch name {
		case "feels_like_c":
			max = 80
		case "humidity_pct":
			min, max = 0, 100
		case "wind_kmh":
			min, max = 0, 500
		case "precipitation_mm":
			min, max = 0, 1000
		}
		if *value < min || *value > max {
			return fmt.Errorf("%s is outside the supported range", name)
		}
	}
	for index, hour := range input.Hourly {
		if strings.TrimSpace(hour.Time) == "" || strings.TrimSpace(hour.Condition) == "" || hour.TemperatureC < -100 || hour.TemperatureC > 80 {
			return fmt.Errorf("hourly weather entry %d is invalid", index)
		}
		if hour.PrecipitationProbabilityPct != nil && (*hour.PrecipitationProbabilityPct < 0 || *hour.PrecipitationProbabilityPct > 100) {
			return fmt.Errorf("hourly weather entry %d has an invalid precipitation probability", index)
		}
	}
	for index, day := range input.Daily {
		if strings.TrimSpace(day.Date) == "" || day.MinTemperatureC < -100 || day.MaxTemperatureC > 80 || day.MinTemperatureC > day.MaxTemperatureC {
			return fmt.Errorf("daily weather entry %d is invalid", index)
		}
		if day.PrecipitationProbabilityPct != nil && (*day.PrecipitationProbabilityPct < 0 || *day.PrecipitationProbabilityPct > 100) {
			return fmt.Errorf("daily weather entry %d has an invalid precipitation probability", index)
		}
	}
	return nil
}

func infoEvidenceDirectory(result infoEvidenceResult) websearch.InfoEvidenceProjection {
	return websearch.CompleteInfoEvidenceDirectory(websearch.Result{
		RequestID: result.RequestID,
		Query:     result.Query,
		Summary:   result.Summary,
		KeyFacts:  result.KeyFacts,
		Results:   result.Sources,
		Citations: result.Citations,
		Untrusted: result.Untrusted,
	})
}

func expectedWeatherEvidence(input weatherStructureInput) map[string]weatherExpectedEvidence {
	out := map[string]weatherExpectedEvidence{}
	if condition := strings.TrimSpace(input.Current.Condition); condition != "" {
		out[weatherMissingCurrentCondition] = weatherExpectedEvidence{text: condition}
	}
	addOptionalNumberEvidence(out, weatherMissingCurrentTemperature, input.Current.TemperatureC, weatherTemperatureUnits)
	addOptionalNumberEvidence(out, "current.feels_like_c", input.Current.FeelsLikeC, weatherTemperatureUnits)
	addOptionalNumberEvidence(out, "current.humidity_pct", input.Current.HumidityPct, weatherPercentageUnits)
	addOptionalNumberEvidence(out, "current.wind_kmh", input.Current.WindKMH, weatherWindSpeedUnits)
	addOptionalNumberEvidence(out, "current.precipitation_mm", input.Current.PrecipitationMM, weatherPrecipitationUnits)
	for index := range input.Hourly {
		hour := &input.Hourly[index]
		out[fmt.Sprintf("hourly[%d].time", index)] = weatherExpectedEvidence{text: strings.TrimSpace(hour.Time)}
		out[fmt.Sprintf("hourly[%d].condition", index)] = weatherExpectedEvidence{text: strings.TrimSpace(hour.Condition)}
		out[fmt.Sprintf("hourly[%d].temperature_c", index)] = numericWeatherEvidence(&hour.TemperatureC, weatherTemperatureUnits)
		addOptionalNumberEvidence(out, fmt.Sprintf("hourly[%d].precipitation_probability_pct", index), hour.PrecipitationProbabilityPct, weatherPercentageUnits)
	}
	for index := range input.Daily {
		day := &input.Daily[index]
		out[fmt.Sprintf("daily[%d].date", index)] = weatherExpectedEvidence{date: strings.TrimSpace(day.Date)}
		if condition := strings.TrimSpace(day.Condition); condition != "" {
			out[fmt.Sprintf("daily[%d].condition", index)] = weatherExpectedEvidence{text: condition}
		}
		out[fmt.Sprintf("daily[%d].min_temperature_c", index)] = numericWeatherEvidence(&day.MinTemperatureC, weatherTemperatureUnits)
		out[fmt.Sprintf("daily[%d].max_temperature_c", index)] = numericWeatherEvidence(&day.MaxTemperatureC, weatherTemperatureUnits)
		addOptionalNumberEvidence(out, fmt.Sprintf("daily[%d].precipitation_probability_pct", index), day.PrecipitationProbabilityPct, weatherPercentageUnits)
	}
	return out
}

func numericWeatherEvidence(value *float64, units []string) weatherExpectedEvidence {
	return weatherExpectedEvidence{number: value, units: units}
}

func addOptionalNumberEvidence(values map[string]weatherExpectedEvidence, path string, value *float64, units []string) {
	if value != nil {
		values[path] = numericWeatherEvidence(value, units)
	}
}

func weatherEvidenceContainsNumber(text string, expected float64) bool {
	for _, raw := range weatherEvidenceNumberPattern.FindAllString(text, -1) {
		value, err := strconv.ParseFloat(raw, 64)
		if err == nil && value == expected {
			return true
		}
	}
	return false
}

func weatherEvidenceContainsUnit(text string, expected float64, units []string) bool {
	if len(units) == 0 {
		return true
	}
	lower := strings.ToLower(text)
	for _, location := range weatherEvidenceNumberPattern.FindAllStringIndex(lower, -1) {
		value, err := strconv.ParseFloat(lower[location[0]:location[1]], 64)
		if err != nil || value != expected {
			continue
		}
		prefix := strings.TrimSpace(lower[:location[0]])
		suffix := strings.TrimSpace(lower[location[1]:])
		for _, unit := range units {
			if unit == "number:度" {
				if strings.HasPrefix(suffix, "度") {
					return true
				}
				continue
			}
			if strings.HasPrefix(suffix, unit) || strings.HasSuffix(prefix, unit) {
				return true
			}
		}
	}
	return false
}

func weatherEvidenceRangeContainsUnit(text string, expected float64, units []string) bool {
	numbers := weatherEvidenceNumberPattern.FindAllString(text, -1)
	if len(numbers) < 2 || !strings.ContainsAny(text, "~～/-至到") {
		return false
	}
	foundExpected := false
	for _, raw := range numbers {
		value, err := strconv.ParseFloat(raw, 64)
		if err == nil && value == expected {
			foundExpected = true
			break
		}
	}
	if !foundExpected {
		return false
	}
	lower := strings.ToLower(text)
	for _, unit := range units {
		if unit == "number:度" {
			unit = "度"
		}
		if strings.Contains(lower, unit) {
			return true
		}
	}
	return false
}

func weatherEvidenceContainsDate(text, expected string) bool {
	expectedDate, ok := parseWeatherEvidenceDate(expected)
	if !ok {
		return strings.Contains(strings.ToLower(text), strings.ToLower(expected))
	}
	actualDate, ok := parseWeatherEvidenceDate(text)
	return ok && actualDate.Equal(expectedDate)
}

func parseWeatherEvidenceDate(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{
		"2006-01-02",
		"2006年01月02日",
		"2006年1月2日",
		"Monday, January 2, 2006",
		"Mon, January 2, 2006",
		"January 2, 2006",
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func weatherPayloadFromCall(call app.ToolCall) (weatherPayload, error) {
	if call.Tool != "weather.structure_payload" || call.Status != "completed" {
		return weatherPayload{}, errors.New("weather card requires a completed structured weather payload")
	}
	var payload weatherPayload
	if err := decodeJSONValue(call.Result, &payload); err != nil {
		return weatherPayload{}, errors.New("structured weather payload is invalid")
	}
	if payload.Status != "completed" || payload.SchemaVersion != weatherPayloadSchemaVersion || strings.TrimSpace(payload.Location) == "" {
		return weatherPayload{}, errors.New("structured weather payload contract is incomplete")
	}
	if err := validateWeatherAvailability(payload.Current, payload.Hourly, payload.Daily, payload.MissingFields); err != nil {
		return weatherPayload{}, fmt.Errorf("structured weather payload contract is incomplete: %w", err)
	}
	return payload, nil
}

func weatherCardDataFromPayload(payload weatherPayload) weatherCardData {
	data := weatherCardData{
		Location:  payload.Location,
		UpdatedAt: payload.RetrievedAt,
		Condition: payload.Current.Condition,
		MissingData: containsWeatherMissingField(payload.MissingFields, weatherMissingCurrentCondition) ||
			containsWeatherMissingField(payload.MissingFields, weatherMissingCurrentTemperature),
		Source: "Infinimesh Info",
	}
	if payload.Current.TemperatureC != nil {
		data.Temperature = formatWeatherMeasure(*payload.Current.TemperatureC, "°C")
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
		data.Precip = formatWeatherMeasure(*payload.Current.PrecipitationMM, "mm")
	}
	for index, hour := range payload.Hourly {
		if index >= 5 {
			break
		}
		forecast := weatherForecastHour{Time: hour.Time, Temp: formatWeatherMeasure(hour.TemperatureC, "°C"), Condition: hour.Condition}
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

func containsWeatherMissingField(fields []string, want string) bool {
	for _, field := range fields {
		if strings.TrimSpace(field) == want {
			return true
		}
	}
	return false
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

func nullableBoundedStringSchema(min, max int) map[string]any {
	return map[string]any{"type": []any{"string", "null"}, "minLength": min, "maxLength": max}
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
