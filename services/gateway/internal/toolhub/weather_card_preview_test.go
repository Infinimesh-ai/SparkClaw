package toolhub

import (
	"image/png"
	"os"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/infinimeshinfo"
)

func TestWeatherCardPreview(t *testing.T) {
	data := weatherCardData{
		Location: "杭州", UpdatedAt: "2026-07-17 10:00", Condition: string(infinimeshinfo.WeatherConditionPartlyCloudy), Temperature: "31°C",
		FeelsLike: "33°C", Humidity: "62%", Wind: "12 km/h", Precip: "0mm", Source: "Infinimesh Info",
		Hourly: []weatherForecastHour{
			{Time: "11:00", Temp: "32°C", Condition: string(infinimeshinfo.WeatherConditionPartlyCloudy)},
			{Time: "12:00", Temp: "33°C", Condition: string(infinimeshinfo.WeatherConditionClear)},
		},
	}
	data.Suggestion = weatherSuggestion(data)
	writeWeatherCardPreview(t, "/tmp/sparkclaw-weather-card-preview.png", data)

	missing := weatherCardData{
		Location: "杭州", UpdatedAt: "2026-07-17 18:00", MissingData: true, Source: "Infinimesh Info",
	}
	missing.Suggestion = weatherSuggestion(missing)
	writeWeatherCardPreview(t, "/tmp/sparkclaw-weather-card-missing-preview.png", missing)
}

func writeWeatherCardPreview(t *testing.T, path string, data weatherCardData) {
	t.Helper()
	img := drawWeatherCard(data)
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if err := png.Encode(out, img); err != nil {
		t.Fatal(err)
	}
}
