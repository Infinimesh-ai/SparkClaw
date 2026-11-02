package agent

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// materializeRoutedQuery adds leaf-specific execution requirements only after
// semantic fusion has selected one registered capability.
func materializeRoutedQuery(capability app.CapabilityID, content, date string) string {
	query := strings.TrimSpace(content)
	switch capability {
	case app.CapabilityBrowserInternetSearch:
		return canonicalizeWebSearchQuery(query, date)
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

// canonicalizeWebSearchQuery resolves only the owner's own relative present-time
// wording to the client date. Search need and requested time scope are separate:
// topic words such as weather, price, or policy never add freshness, and an
// explicit calendar date, historical scope, or other year keeps the query as written.
func canonicalizeWebSearchQuery(query, date string) string {
	query = strings.TrimSpace(query)
	date = strings.TrimSpace(date)
	if query == "" || date == "" || strings.Contains(query, date) {
		return query
	}
	if !queryHasPresentTimeAnchor(query) || queryHasExplicitTimeScope(query, date) {
		return query
	}
	return query + " " + date
}

var (
	presentTimeTermsCJK = []string{"今天", "今日", "今晚", "现在", "当前", "目前", "眼下", "此刻", "实时", "最新", "最近", "刚刚", "本周", "本月", "今年", "现任"}
	historicalTermsCJK  = []string{"历史", "史料", "古代", "当年", "当时", "过去", "昨天", "昨日", "前天", "去年", "前年", "上周", "上个月", "上月"}
	negationTermsCJK    = []string{"不要", "不用", "不需要", "无需", "不查", "别查", "并非", "而非"}

	presentTimePatternEN = regexp.MustCompile(`(?i)\b(today|tonight|now|current|currently|latest|recent|recently|real-time|realtime|this (?:week|month|year))\b`)
	historicalPatternEN  = regexp.MustCompile(`(?i)\b(historical|history|yesterday|in the past|last (?:week|month|year))\b`)
	negationPatternEN    = regexp.MustCompile(`(?i)\b(not|no|without|rather than|instead of)\s+(?:\w+\s+)?$`)
	calendarDatePattern  = regexp.MustCompile(`\b[0-9]{4}[-/.][0-9]{1,2}(?:[-/.][0-9]{1,2})?\b|[0-9]{4}年[0-9]{1,2}月`)
)

func queryHasPresentTimeAnchor(query string) bool {
	for _, term := range presentTimeTermsCJK {
		for offset := 0; ; {
			index := strings.Index(query[offset:], term)
			if index < 0 {
				break
			}
			index += offset
			if !precededByCJKNegation(query[:index]) {
				return true
			}
			offset = index + len(term)
		}
	}
	for _, match := range presentTimePatternEN.FindAllStringIndex(query, -1) {
		if !negationPatternEN.MatchString(query[:match[0]]) {
			return true
		}
	}
	return false
}

func precededByCJKNegation(prefix string) bool {
	runes := []rune(prefix)
	window := string(runes[max(0, len(runes)-4):])
	for _, term := range negationTermsCJK {
		if strings.Contains(window, term) {
			return true
		}
	}
	return false
}

// queryHasExplicitTimeScope conservatively preserves calendar dates, including
// earlier months of the current year, and years other than the client year.
// Four-digit amounts such as "1999元" are not years. No date is inferred or rewritten.
func queryHasExplicitTimeScope(query, date string) bool {
	if calendarDatePattern.MatchString(query) {
		return true
	}
	for _, term := range historicalTermsCJK {
		if strings.Contains(query, term) {
			return true
		}
	}
	if historicalPatternEN.MatchString(query) {
		return true
	}
	currentYear, err := strconv.Atoi(date[:min(4, len(date))])
	if err != nil {
		return false
	}
	runes := []rune(query)
	for start := 0; start < len(runes); {
		if !unicode.IsDigit(runes[start]) {
			start++
			continue
		}
		end := start
		for end < len(runes) && unicode.IsDigit(runes[end]) {
			end++
		}
		if end-start == 4 && (start == 0 || runes[start-1] != '.') && (end == len(runes) || isYearSuffix(runes[end])) {
			if year, _ := strconv.Atoi(string(runes[start:end])); year >= 1000 && year != currentYear {
				return true
			}
		}
		start = end
	}
	return false
}

func isYearSuffix(r rune) bool {
	return r == '年' || r == 's' || unicode.IsSpace(r) || unicode.IsPunct(r)
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
