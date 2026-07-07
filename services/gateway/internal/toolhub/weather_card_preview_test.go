package toolhub

import (
	"context"
	"image/png"
	"os"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

func TestWeatherCardPreview(t *testing.T) {
	hub := New(config.Default(), nil)
	data, ok := hub.openMeteoWeather(context.Background(), "杭州")
	if !ok {
		t.Fatal("open-meteo preview weather lookup failed")
	}
	img := drawWeatherCard(data)
	out, err := os.Create("/tmp/sparkclaw-weather-card-preview.png")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if err := png.Encode(out, img); err != nil {
		t.Fatal(err)
	}
}
