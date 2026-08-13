package infinimeshinfo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testWeatherRequest() WeatherRequest {
	return WeatherRequest{
		Location:    WeatherRequestLocation{Name: "杭州"},
		Granularity: []WeatherGranularity{WeatherGranularityCurrent, WeatherGranularityHourly, WeatherGranularityDaily},
		Days:        3,
		HourlySteps: 24,
		Units:       WeatherUnitsMetric,
		Language:    "zh-CN",
	}
}

func validWeatherResponse(requestID string) map[string]any {
	return map[string]any{
		"request_id": requestID,
		"status":     "ok",
		"weather": map[string]any{
			"provider":    "caiyun_weather",
			"location":    map[string]any{"lat": 30.2741, "lon": 120.1551, "name": "杭州市"},
			"timezone":    "Asia/Shanghai",
			"observed_at": "2026-07-29T05:00:00Z",
			"current": map[string]any{
				"temp_c": 31.2, "apparent_temp_c": 33.0, "condition": "partly_cloudy",
				"humidity_percent": 62, "precipitation_mm_h": 0, "wind_speed_kph": 12.6,
			},
			"hourly": []map[string]any{{
				"time": "2026-07-29T06:00:00Z", "temp_c": 32.0, "condition": "partly_cloudy",
				"precipitation_probability_percent": 10,
			}},
			"daily": []map[string]any{{
				"date": "2026-07-29", "temp_min_c": 27.0, "temp_max_c": 35.0,
				"condition": "partly_cloudy", "precipitation_probability_percent": 20,
			}},
		},
		"sources": []map[string]any{{
			"id": "src-weather", "source_type": "weather", "provider": "caiyun_weather",
			"retrieved_at": "2026-07-29T05:00:01Z",
		}},
		"usage": map[string]any{"cost_credits": 1, "token_type": "info.basic", "cache_hit": false},
	}
}

func TestClientWeatherUsesDedicatedContractAndFreshPrivateTokens(t *testing.T) {
	var mu sync.Mutex
	authorizations := []string{}
	requestIDs := []string{}
	var weatherCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case issueTokensPath:
			if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+testLicenseKey {
				t.Error("token issue request contract mismatch")
			}
			writeIssuedTokens(t, w, "weather-batch", 3)
		case weatherPath:
			if r.Method != http.MethodPost {
				t.Error("weather endpoint must use POST")
			}
			var body weatherRequestEnvelope
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				return
			}
			raw, _ := json.Marshal(body)
			if strings.Contains(string(raw), testLicenseID) || strings.Contains(string(raw), testLicenseKey) {
				t.Error("weather request leaked token issuance credentials")
			}
			if body.Location.Name != "杭州" || body.Location.Latitude != nil || body.Location.Longitude != nil ||
				len(body.Granularity) != 3 || body.Days != 3 || body.HourlySteps != 24 ||
				body.Units != WeatherUnitsMetric || body.Language != "zh-CN" {
				t.Errorf("weather request contract mismatch: %#v", body)
			}
			if body.RequestID == "" || r.Header.Get("X-Request-Id") != body.RequestID {
				t.Error("weather request ID contract mismatch")
			}
			mu.Lock()
			authorizations = append(authorizations, r.Header.Get("Authorization"))
			requestIDs = append(requestIDs, body.RequestID)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			if weatherCalls.Add(1) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"request_id": body.RequestID,
					"error": map[string]any{
						"code": "SERVICE_DEGRADED", "message": "secret response detail",
						"retryable": true, "details": map[string]any{},
					},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(validWeatherResponse(body.RequestID))
		default:
			t.Errorf("unexpected Info API path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(testClientConfig(server.URL), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.retryJitter = func(delay time.Duration) time.Duration { return delay }
	response, err := client.Weather(context.Background(), testWeatherRequest())
	if err != nil {
		t.Fatal(err)
	}
	if response.Weather.Current == nil || *response.Weather.Current.TemperatureC != 31.2 ||
		response.Weather.Current.Condition != "partly_cloudy" || len(response.Weather.Hourly) != 1 ||
		len(response.Weather.Daily) != 1 {
		t.Fatalf("typed weather response mismatch: %#v", response)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(authorizations) != 2 || authorizations[0] == authorizations[1] ||
		!strings.HasPrefix(authorizations[0], "PrivateToken ") ||
		!strings.HasPrefix(authorizations[1], "PrivateToken ") {
		t.Fatalf("weather retries did not reserve fresh private tokens: %#v", authorizations)
	}
	if requestIDs[0] == requestIDs[1] {
		t.Fatal("weather retry reused a request ID")
	}
}

func TestClientWeatherHTTPErrorIsClassifiedAndSanitized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == issueTokensPath {
			writeIssuedTokens(t, w, "weather-error", 1)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code": "POLICY_DENIED", "message": "denied " + testLicenseKey,
				"retryable": false, "details": map[string]any{"location": "杭州"},
			},
		})
	}))
	defer server.Close()

	cfg := testClientConfig(server.URL)
	cfg.MaxAttempts = 1
	client, err := NewClient(cfg, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Weather(context.Background(), testWeatherRequest())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Endpoint != weatherPath || apiErr.StatusCode != http.StatusForbidden ||
		apiErr.Code != "POLICY_DENIED" || apiErr.Retryable {
		t.Fatalf("unexpected weather API error: %#v", err)
	}
	if strings.Contains(err.Error(), testLicenseKey) || strings.Contains(err.Error(), "杭州") {
		t.Fatal("weather API error leaked response details")
	}
}

func TestClientWeatherTimeoutIsTransportError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == issueTokensPath {
			writeIssuedTokens(t, w, "weather-timeout", 1)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	cfg := testClientConfig(server.URL)
	cfg.MaxAttempts = 1
	cfg.RequestTimeout = 10 * time.Millisecond
	client, err := NewClient(cfg, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Weather(context.Background(), testWeatherRequest())
	var transportErr *TransportError
	if !errors.As(err, &transportErr) || transportErr.Endpoint != weatherPath {
		t.Fatalf("timeout was not classified as weather transport failure: %v", err)
	}
}

func TestClientWeatherRejectsMalformedAndIncompleteResponses(t *testing.T) {
	tests := []struct {
		name  string
		write func(http.ResponseWriter, string)
	}{
		{
			name: "malformed JSON",
			write: func(w http.ResponseWriter, _ string) {
				_, _ = w.Write([]byte(`{"request_id":`))
			},
		},
		{
			name: "missing current",
			write: func(w http.ResponseWriter, requestID string) {
				response := validWeatherResponse(requestID)
				response["weather"].(map[string]any)["current"] = nil
				_ = json.NewEncoder(w).Encode(response)
			},
		},
		{
			name: "unknown condition",
			write: func(w http.ResponseWriter, requestID string) {
				response := validWeatherResponse(requestID)
				response["weather"].(map[string]any)["current"].(map[string]any)["condition"] = "surprise_storm"
				_ = json.NewEncoder(w).Encode(response)
			},
		},
		{
			name: "missing source",
			write: func(w http.ResponseWriter, requestID string) {
				response := validWeatherResponse(requestID)
				response["sources"] = []any{}
				_ = json.NewEncoder(w).Encode(response)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == issueTokensPath {
					writeIssuedTokens(t, w, "weather-invalid", 1)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				var request weatherRequestEnvelope
				_ = json.NewDecoder(r.Body).Decode(&request)
				test.write(w, request.RequestID)
			}))
			defer server.Close()
			cfg := testClientConfig(server.URL)
			cfg.MaxAttempts = 1
			client, err := NewClient(cfg, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Weather(context.Background(), testWeatherRequest()); err == nil {
				t.Fatal("invalid weather response was accepted")
			}
		})
	}
}

func TestClientWeatherRejectsOversizedResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == issueTokensPath {
			writeIssuedTokens(t, w, "weather-oversized", 1)
			return
		}
		_, _ = w.Write([]byte(strings.Repeat("x", 1024)))
	}))
	defer server.Close()

	cfg := testClientConfig(server.URL)
	cfg.MaxAttempts = 1
	cfg.ResponseBodyMaxBytes = 512
	client, err := NewClient(cfg, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Weather(context.Background(), testWeatherRequest()); err == nil ||
		!strings.Contains(err.Error(), "response read failed") ||
		strings.Contains(err.Error(), strings.Repeat("x", 32)) {
		t.Fatalf("oversized weather response did not fail safely: %v", err)
	}
}

func TestClientWeatherValidatesCityAndUnitBoundariesBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()
	client, err := NewClient(testClientConfig(server.URL), server.Client())
	if err != nil {
		t.Fatal(err)
	}

	latitude := 91.0
	tests := []WeatherRequest{
		{},
		{Location: WeatherRequestLocation{Name: strings.Repeat("城", 81)}},
		{Location: WeatherRequestLocation{Name: "杭州"}, Units: "imperial"},
		{Location: WeatherRequestLocation{Name: "杭州"}, Days: 8},
		{Location: WeatherRequestLocation{Name: "杭州"}, HourlySteps: 49},
		{Location: WeatherRequestLocation{Latitude: &latitude}},
	}
	for _, request := range tests {
		if _, err := client.Weather(context.Background(), request); err == nil {
			t.Fatalf("invalid weather request was accepted: %#v", request)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid weather requests reached the network %d time(s)", calls.Load())
	}

	latitude, longitude := 90.0, -180.0
	normalized, err := normalizeWeatherRequest(WeatherRequest{
		Location: WeatherRequestLocation{
			Name: strings.Repeat("城", 80), Latitude: &latitude, Longitude: &longitude,
		},
	})
	if err != nil {
		t.Fatalf("valid city and coordinate boundaries were rejected: %v", err)
	}
	if normalized.Units != WeatherUnitsMetric || normalized.Language != "zh-CN" ||
		normalized.Days != 3 || normalized.HourlySteps != 24 ||
		len(normalized.Granularity) != 1 || normalized.Granularity[0] != WeatherGranularityCurrent {
		t.Fatalf("weather defaults changed at the valid boundary: %#v", normalized)
	}
}
