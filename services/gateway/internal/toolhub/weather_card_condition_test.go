package toolhub

import (
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/infinimeshinfo"
)

func TestWeatherConditionDisplayCoversContract(t *testing.T) {
	validKinds := map[string]bool{"clear": true, "partly": true, "rain": true, "snow": true, "unknown": true}
	if len(weatherConditionDisplays) != len(infinimeshinfo.AllWeatherConditions) {
		t.Fatalf("card display map has %d entries, contract has %d conditions",
			len(weatherConditionDisplays), len(infinimeshinfo.AllWeatherConditions))
	}
	for _, condition := range infinimeshinfo.AllWeatherConditions {
		display, ok := weatherConditionDisplays[condition]
		if !ok {
			t.Fatalf("contract condition %q has no card display entry", condition)
		}
		if strings.TrimSpace(display.label) == "" {
			t.Fatalf("contract condition %q has an empty card label", condition)
		}
		if !validKinds[display.kind] {
			t.Fatalf("contract condition %q maps to unhandled icon kind %q", condition, display.kind)
		}
	}
}

func TestWeatherConditionDisplayFallsBackToUnknown(t *testing.T) {
	for _, condition := range []string{"", "  ", "drizzle", "晴"} {
		display := weatherConditionDisplayFor(condition)
		if display.kind != "unknown" || display.label != "暂无数据" {
			t.Fatalf("non-contract condition %q did not fall back to unknown: %#v", condition, display)
		}
	}
}

func TestWeatherKindDrivesSeverityLabels(t *testing.T) {
	tests := []struct {
		condition infinimeshinfo.WeatherCondition
		label     string
		kind      string
	}{
		{infinimeshinfo.WeatherConditionLightRain, "小雨", "rain"},
		{infinimeshinfo.WeatherConditionStormRain, "暴雨", "rain"},
		{infinimeshinfo.WeatherConditionHaze, "霾", "partly"},
		{infinimeshinfo.WeatherConditionFog, "雾", "partly"},
		{infinimeshinfo.WeatherConditionDust, "浮尘", "partly"},
		{infinimeshinfo.WeatherConditionSand, "沙尘", "partly"},
		{infinimeshinfo.WeatherConditionWind, "大风", "partly"},
		{infinimeshinfo.WeatherConditionStormSnow, "暴雪", "snow"},
	}
	for _, test := range tests {
		if got := displayCondition(string(test.condition)); got != test.label {
			t.Fatalf("condition %q rendered label %q, want %q", test.condition, got, test.label)
		}
		if got := classifyWeatherKind(weatherCardData{Condition: string(test.condition)}); got != test.kind {
			t.Fatalf("condition %q classified as kind %q, want %q", test.condition, got, test.kind)
		}
	}
}
