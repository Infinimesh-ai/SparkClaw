package infinimeshinfo

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

const weatherPath = "/v1/info/weather"

var supportedWeatherConditions = map[WeatherCondition]struct{}{
	"clear": {}, "partly_cloudy": {}, "cloudy": {}, "haze": {}, "fog": {},
	"dust": {}, "sand": {}, "wind": {}, "light_rain": {}, "moderate_rain": {},
	"heavy_rain": {}, "storm_rain": {}, "light_snow": {}, "moderate_snow": {},
	"heavy_snow": {}, "storm_snow": {}, "unknown": {},
}

type weatherRequestEnvelope struct {
	RequestID   string                 `json:"request_id"`
	Location    weatherRequestLocation `json:"location"`
	Granularity []WeatherGranularity   `json:"granularity"`
	Days        int                    `json:"days"`
	HourlySteps int                    `json:"hourly_steps"`
	Units       WeatherUnits           `json:"units"`
	Language    string                 `json:"language"`
}

type weatherRequestLocation struct {
	Name      string   `json:"name,omitempty"`
	Latitude  *float64 `json:"lat,omitempty"`
	Longitude *float64 `json:"lon,omitempty"`
}

func (c *Client) Weather(ctx context.Context, request WeatherRequest) (WeatherResponse, error) {
	request, err := normalizeWeatherRequest(request)
	if err != nil {
		return WeatherResponse{}, err
	}
	var lastErr error
	for attempt := 1; attempt <= c.cfg.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return WeatherResponse{}, err
		}
		token, err := c.wallet.Reserve(ctx, TokenTypeBasic)
		if err != nil {
			return WeatherResponse{}, err
		}
		response, err := c.weatherOnce(ctx, token, request)
		if err == nil {
			return response, nil
		}
		lastErr = err
		if apiErrorCode(err) == "TOKEN_EXPIRED" {
			c.wallet.DiscardAll(TokenTypeBasic)
		}
		if attempt == c.cfg.MaxAttempts || !retryableRequestError(err) {
			break
		}
		if err := c.waitForRetry(ctx, attempt); err != nil {
			return WeatherResponse{}, err
		}
	}
	return WeatherResponse{}, lastErr
}

func (c *Client) weatherOnce(ctx context.Context, token string, request WeatherRequest) (WeatherResponse, error) {
	requestID, err := c.randomID()
	if err != nil {
		return WeatherResponse{}, errors.New("infinimesh info weather request ID generation failed")
	}
	payload := weatherRequestEnvelope{
		RequestID: requestID,
		Location: weatherRequestLocation{
			Name: request.Location.Name, Latitude: request.Location.Latitude, Longitude: request.Location.Longitude,
		},
		Granularity: request.Granularity,
		Days:        request.Days, HourlySteps: request.HourlySteps,
		Units: request.Units, Language: request.Language,
	}
	var response WeatherResponse
	if err := c.postJSON(ctx, weatherPath, "PrivateToken "+token, requestID, payload, &response); err != nil {
		return WeatherResponse{}, err
	}
	if err := validateWeatherResponse(response, requestID, request.Granularity); err != nil {
		return WeatherResponse{}, err
	}
	return response, nil
}

func normalizeWeatherRequest(request WeatherRequest) (WeatherRequest, error) {
	request.Location.Name = strings.TrimSpace(request.Location.Name)
	hasCoordinates := request.Location.Latitude != nil || request.Location.Longitude != nil
	if hasCoordinates && (request.Location.Latitude == nil || request.Location.Longitude == nil) {
		return WeatherRequest{}, errors.New("infinimesh info weather location requires both latitude and longitude")
	}
	if !hasCoordinates && request.Location.Name == "" {
		return WeatherRequest{}, errors.New("infinimesh info weather location is required")
	}
	if utf8.RuneCountInString(request.Location.Name) > 80 {
		return WeatherRequest{}, errors.New("infinimesh info weather location name exceeds 80 characters")
	}
	if hasCoordinates {
		if !finiteInRange(*request.Location.Latitude, -90, 90) || !finiteInRange(*request.Location.Longitude, -180, 180) {
			return WeatherRequest{}, errors.New("infinimesh info weather coordinates are outside the supported range")
		}
	}
	if len(request.Granularity) == 0 {
		request.Granularity = []WeatherGranularity{WeatherGranularityCurrent}
	}
	seen := map[WeatherGranularity]bool{}
	normalized := make([]WeatherGranularity, 0, len(request.Granularity))
	for _, granularity := range request.Granularity {
		switch granularity {
		case WeatherGranularityCurrent, WeatherGranularityHourly, WeatherGranularityDaily:
		default:
			return WeatherRequest{}, errors.New("infinimesh info weather granularity is not supported")
		}
		if !seen[granularity] {
			seen[granularity] = true
			normalized = append(normalized, granularity)
		}
	}
	request.Granularity = normalized
	if request.Days == 0 {
		request.Days = 3
	}
	if request.Days < 1 || request.Days > 7 {
		return WeatherRequest{}, errors.New("infinimesh info weather days must be between 1 and 7")
	}
	if request.HourlySteps == 0 {
		request.HourlySteps = 24
	}
	if request.HourlySteps < 1 || request.HourlySteps > 48 {
		return WeatherRequest{}, errors.New("infinimesh info weather hourly steps must be between 1 and 48")
	}
	if request.Units == "" {
		request.Units = WeatherUnitsMetric
	}
	if request.Units != WeatherUnitsMetric {
		return WeatherRequest{}, errors.New("infinimesh info weather supports metric units only")
	}
	request.Language = strings.TrimSpace(request.Language)
	if request.Language == "" {
		request.Language = "zh-CN"
	}
	return request, nil
}

func validateWeatherResponse(response WeatherResponse, requestID string, requested []WeatherGranularity) error {
	if strings.TrimSpace(response.Status) != "ok" {
		return errors.New("infinimesh info weather returned an invalid status")
	}
	if response.RequestID != requestID {
		return errors.New("infinimesh info weather returned a mismatched request ID")
	}
	if strings.TrimSpace(response.Weather.Provider) == "" || response.Weather.Location.Latitude == nil ||
		response.Weather.Location.Longitude == nil || strings.TrimSpace(response.Weather.ObservedAt) == "" {
		return errors.New("infinimesh info weather response is incomplete")
	}
	if !finiteInRange(*response.Weather.Location.Latitude, -90, 90) ||
		!finiteInRange(*response.Weather.Location.Longitude, -180, 180) {
		return errors.New("infinimesh info weather response contains invalid coordinates")
	}
	if _, err := time.Parse(time.RFC3339, response.Weather.ObservedAt); err != nil {
		return errors.New("infinimesh info weather response contains an invalid observation time")
	}
	for _, granularity := range requested {
		switch granularity {
		case WeatherGranularityCurrent:
			if response.Weather.Current == nil {
				return errors.New("infinimesh info weather response is missing current conditions")
			}
		case WeatherGranularityHourly:
			if len(response.Weather.Hourly) == 0 {
				return errors.New("infinimesh info weather response is missing hourly forecast")
			}
		case WeatherGranularityDaily:
			if len(response.Weather.Daily) == 0 {
				return errors.New("infinimesh info weather response is missing daily forecast")
			}
		}
	}
	if err := validateWeatherCurrent(response.Weather.Current); err != nil {
		return err
	}
	if len(response.Weather.Hourly) > 48 || len(response.Weather.Daily) > 7 {
		return errors.New("infinimesh info weather response exceeds forecast limits")
	}
	for _, hour := range response.Weather.Hourly {
		if hour.TemperatureC == nil || !validWeatherTemperature(*hour.TemperatureC) ||
			!validWeatherCondition(hour.Condition) {
			return errors.New("infinimesh info weather response contains an incomplete hourly forecast")
		}
		if _, err := time.Parse(time.RFC3339, hour.Time); err != nil {
			return errors.New("infinimesh info weather response contains an invalid hourly time")
		}
		if !optionalInRange(hour.PrecipitationProbabilityPercent, 0, 100) ||
			!optionalInRange(hour.HumidityPercent, 0, 100) {
			return errors.New("infinimesh info weather response contains an invalid hourly value")
		}
	}
	for _, day := range response.Weather.Daily {
		if day.TemperatureMinC == nil || day.TemperatureMaxC == nil ||
			!validWeatherTemperature(*day.TemperatureMinC) || !validWeatherTemperature(*day.TemperatureMaxC) ||
			*day.TemperatureMinC > *day.TemperatureMaxC || !validWeatherCondition(day.Condition) {
			return errors.New("infinimesh info weather response contains an incomplete daily forecast")
		}
		if _, err := time.Parse("2006-01-02", day.Date); err != nil {
			return errors.New("infinimesh info weather response contains an invalid daily date")
		}
		if !optionalInRange(day.PrecipitationProbabilityPercent, 0, 100) ||
			!optionalInRange(day.HumidityPercent, 0, 100) {
			return errors.New("infinimesh info weather response contains an invalid daily value")
		}
	}
	if len(response.Sources) == 0 {
		return errors.New("infinimesh info weather response has no source")
	}
	for _, source := range response.Sources {
		if strings.TrimSpace(source.ID) == "" || source.SourceType != "weather" ||
			strings.TrimSpace(source.Provider) == "" {
			return errors.New("infinimesh info weather response contains an incomplete source")
		}
		if _, err := time.Parse(time.RFC3339, source.RetrievedAt); err != nil {
			return errors.New("infinimesh info weather response contains an invalid source time")
		}
	}
	if response.Usage.TokenType != string(TokenTypeBasic) || response.Usage.CostCredits < 0 {
		return errors.New("infinimesh info weather response contains invalid usage")
	}
	return nil
}

func validateWeatherCurrent(current *WeatherCurrent) error {
	if current == nil {
		return nil
	}
	if current.TemperatureC == nil || !validWeatherTemperature(*current.TemperatureC) ||
		!validWeatherCondition(current.Condition) {
		return errors.New("infinimesh info weather response contains incomplete current conditions")
	}
	if !optionalInRange(current.ApparentTemperatureC, -100, 80) ||
		!optionalInRange(current.HumidityPercent, 0, 100) ||
		!optionalInRange(current.CloudCoverPercent, 0, 100) ||
		!optionalInRange(current.PrecipitationMMH, 0, 1000) ||
		!optionalInRange(current.WindSpeedKPH, 0, 500) ||
		!optionalInRange(current.WindDirectionDegrees, 0, 360) ||
		!optionalInRange(current.PressureHPa, 100, 1200) ||
		!optionalInRange(current.VisibilityKM, 0, 1000) {
		return errors.New("infinimesh info weather response contains an invalid current value")
	}
	return nil
}

func validWeatherCondition(condition WeatherCondition) bool {
	_, ok := supportedWeatherConditions[condition]
	return ok
}

func validWeatherTemperature(value float64) bool {
	return finiteInRange(value, -100, 80)
}

func optionalInRange(value *float64, min, max float64) bool {
	return value == nil || finiteInRange(*value, min, max)
}

func finiteInRange(value, min, max float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= min && value <= max
}
