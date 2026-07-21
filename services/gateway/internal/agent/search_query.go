package agent

import (
	"strings"
	"time"
)

const (
	weatherCardQueryRequirementsZH = "；请提供当前天气状况和当前温度；可获得时提供当日最低/最高温度，以及从当前时刻起最多5个未来小时的具体日期时间、天气状况和温度；若任一项没有可靠数据，请明确说明未获取到，不得推测或用其他温度替代。"
	weatherCardQueryRequirementsEN = ". Include the current weather condition and current temperature. When available, include today's low/high temperatures and up to five future hourly entries from the current time, each with a specific date-time, weather condition, and temperature. Explicitly state when reliable data is unavailable; never infer it or substitute another temperature."
)

// canonicalizeSearchRoutingContent resolves freshness once, before route selection.
// MOCK response lines are retained so deterministic test-model controls still reach
// the model, while semantic routing uses only the canonical query.
func canonicalizeSearchRoutingContent(content, date string) (string, bool) {
	query, ok := browserInternetSearchQuery(content)
	weather := false
	if !ok && ordinaryWeatherRequest(content) {
		query, ok = semanticRoutingContent(content), true
		weather = true
	}
	if !ok {
		return content, false
	}
	canonical := canonicalizeWebSearchQuery(query, query, date)
	if weather {
		canonical = canonicalizeWeatherCardQuery(canonical)
	}
	if canonical == query {
		return content, false
	}
	if controls := mockResponseLines(content); len(controls) > 0 {
		return canonical + "\n" + strings.Join(controls, "\n"), true
	}
	return canonical, true
}

func canonicalizeWeatherCardQuery(query string) string {
	query = strings.TrimSpace(query)
	lower := strings.ToLower(query)
	if strings.Contains(query, "最多5个未来小时") || strings.Contains(lower, "up to five future hourly entries") {
		return query
	}
	if containsCJK(query) {
		return query + weatherCardQueryRequirementsZH
	}
	return query + weatherCardQueryRequirementsEN
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

func queryWithFreshnessIntent(goal, query, date string) string {
	return canonicalizeWebSearchQuery(goal, query, date)
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

func currentSearchDate() string {
	return time.Now().Local().Format("2006-01-02")
}

func containsCJK(value string) bool {
	for _, r := range value {
		if r >= '\u4e00' && r <= '\u9fff' {
			return true
		}
	}
	return false
}
