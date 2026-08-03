package toolhub

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/infinimeshinfo"
	"golang.org/x/image/font/sfnt"
)

func TestWeatherCardFontPathsIncludeGatewayNotoCJK(t *testing.T) {
	want := "/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc"
	if !slices.Contains(weatherCardFontPaths(), want) {
		t.Fatalf("weather card font candidates do not include the Gateway image font %q", want)
	}
}

func TestWeatherCardFontLoaderRejectsMissingCJKFont(t *testing.T) {
	if _, err := loadWeatherCardFaces([]string{t.TempDir() + "/missing-font.ttc"}); err == nil {
		t.Fatal("weather card font loader silently accepted a missing CJK font")
	}
}

func TestWeatherCardFontLoaderSelectsSimplifiedChineseFace(t *testing.T) {
	path := "/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc"
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Skip("Gateway Noto Sans CJK font is not installed on this host")
	}
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseWeatherCardFont(raw)
	if err != nil {
		t.Fatal(err)
	}
	var buf sfnt.Buffer
	name, err := parsed.Name(&buf, sfnt.NameIDFamily)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(name, "CJK SC") {
		t.Fatalf("weather card selected %q from Noto collection, want the Simplified Chinese face", name)
	}
}

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
