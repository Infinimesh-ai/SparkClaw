// Grounded summary builders for the weather-card strategy.
package agent

import (
	"path/filepath"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func groundedWeatherCardSummary(calls []app.ToolCall) (string, bool) {
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		if call.Tool != "media.render_weather_card" || !toolCallCompleted(call) {
			continue
		}
		result, ok := anyMap(call.Result)
		if !ok {
			continue
		}
		mediaPath := strings.TrimSpace(stringValue(result["media_path"]))
		if mediaPath == "" {
			mediaPath = strings.TrimSpace(stringValue(result["uri"]))
			mediaPath = strings.TrimPrefix(mediaPath, "workspace://")
		}
		if mediaPath == "" || !strings.HasPrefix(filepath.ToSlash(mediaPath), "media/") {
			continue
		}
		return "天气卡片已生成。", true
	}
	return "", false
}

func groundedWeatherFailureSummary(calls []app.ToolCall) (string, bool) {
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		if call.Tool != "media.render_weather_card" {
			continue
		}
		if toolCallCompleted(call) {
			return "", false
		}
		if !strings.Contains(call.Status, "failed") && call.Error == "" {
			return "", false
		}
		reason := strings.TrimSpace(call.Error)
		if reason == "" {
			reason = "Info weather evidence or card rendering failed"
		}
		return "天气查询失败：" + reason, true
	}
	return "", false
}
