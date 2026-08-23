package agent

import (
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// materializeRoutedQuery adds leaf-specific execution requirements only after
// semantic fusion has selected one registered capability.
func materializeRoutedQuery(capability app.CapabilityID, content, date string) string {
	query := strings.TrimSpace(content)
	switch capability {
	case app.CapabilityBrowserInternetSearch:
		return canonicalizeWebSearchQuery(query, query, date)
	default:
		return query
	}
}

func mockResponseLines(content string) []string {
	lines := strings.Split(content, "\n")
	controls := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "MOCK_") && strings.Contains(trimmed, "_RESPONSE:") {
			controls = append(controls, line)
		}
	}
	return controls
}

func canonicalizeWebSearchQuery(goal, query, date string) string {
	query = strings.TrimSpace(query)
	if query == "" || !goalNeedsFreshWeb(goal) {
		return query
	}
	if !queryHasFreshnessIntent(query, date) {
		terms := []string{"latest", "current"}
		if containsCJK(goal) || containsCJK(query) {
			terms = []string{"最新", "当前"}
		}
		query = strings.TrimSpace(query + " " + strings.Join(terms, " "))
	}
	if date = strings.TrimSpace(date); date != "" && !strings.Contains(query, date) {
		query = strings.TrimSpace(query + " " + date)
	}
	return query
}

func goalNeedsFreshWeb(goal string) bool {
	lower := strings.ToLower(goal)
	freshTerms := []string{
		"latest", "recent", "current", "today", "tonight", "now", "this week", "this month", "this year", "real-time", "realtime",
		"最新", "最近", "当前", "今天", "今日", "今晚", "现在", "实时", "本周", "本月", "今年", "刚刚",
		"typhoon", "hurricane", "storm", "weather", "forecast", "台风", "飓风", "风暴", "天气", "预报", "气象", "路径",
		"news", "price", "schedule", "policy", "新闻", "价格", "行情", "日程", "赛程", "政策",
	}
	for _, term := range freshTerms {
		if strings.Contains(lower, term) {
			return true
		}
	}
	return false
}

func queryHasFreshnessIntent(query, date string) bool {
	lower := strings.ToLower(query)
	for _, term := range []string{"latest", "recent", "current", "today", "now", "real-time", "realtime", "最新", "最近", "当前", "今天", "今日", "实时", "现在"} {
		if strings.Contains(lower, term) {
			return true
		}
	}
	return strings.TrimSpace(date) != "" && strings.Contains(query, strings.TrimSpace(date))
}

func currentSearchDateForTimezone(now time.Time, timezone string) string {
	if now.IsZero() {
		now = time.Now()
	}
	return now.In(clientTimeLocation(timezone)).Format("2006-01-02")
}

func containsCJK(value string) bool {
	for _, r := range value {
		if r >= '\u4e00' && r <= '\u9fff' {
			return true
		}
	}
	return false
}
