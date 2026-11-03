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
	// Scope detection reads full-width digits and punctuation like ASCII; the
	// query itself is never rewritten.
	scope := foldFullWidth(query)
	if !queryHasPresentTimeAnchor(scope) || queryHasExplicitTimeScope(scope, date) {
		return query
	}
	return query + " " + date
}

var (
	presentTimeTermsCJK = []string{"今天", "今日", "今晚", "现在", "当前", "目前", "眼下", "此刻", "实时", "最新", "最近", "刚刚", "本周", "本月", "今年", "现任"}
	historicalTermsCJK  = []string{"历史", "史料", "古代", "当年", "当时", "过去", "昨天", "昨日", "前天", "去年", "前年", "上周", "上个月", "上月"}
	negationTermsCJK    = []string{"不要", "不用", "不需要", "无需", "不查", "别查", "并非", "而非"}
	// "最近" after a place cue means nearest; "过去" after a motion verb means going over.
	nearestCuesCJK = []string{"附近", "离", "距", "哪"}
	motionVerbsCJK = "走跑开飞送传寄坐骑游爬冲搬递扔拿转跳赶"
	nonTemporalCJK = map[string]func(prefix string) bool{
		"最近": func(prefix string) bool { return windowContainsAny(prefix, 6, nearestCuesCJK) },
		"过去": func(prefix string) bool {
			runes := []rune(prefix)
			return len(runes) > 0 && strings.ContainsRune(motionVerbsCJK, runes[len(runes)-1])
		},
	}

	presentTimePatternEN = regexp.MustCompile(`(?i)\b(today|tonight|now|current|currently|latest|recent|recently|real-time|realtime|this (?:week|month|year))\b`)
	historicalPatternEN  = regexp.MustCompile(`(?i)\b(historical|history|yesterday|in the past|last (?:week|month|year))\b`)
	negationPatternEN    = regexp.MustCompile(`(?i)\b(not|no|without|rather than|instead of)\s+(?:\w+\s+)?$`)
	electricPrefixEN     = regexp.MustCompile(`(?i)\b(alternating|direct|electric|electrical)\s+$`)
	// A dot-separated date needs all three parts so decimal amounts such as 1999.99 stay amounts.
	calendarDatePattern = regexp.MustCompile(`\b[0-9]{4}(?:[-/]([0-9]{1,2})(?:[-/][0-9]{1,2})?|\.([0-9]{1,2})\.[0-9]{1,2})\b|[0-9]{4}年([0-9]{1,2})月`)
)

func queryHasPresentTimeAnchor(query string) bool {
	for _, term := range presentTimeTermsCJK {
		if containsCJKTermExcept(query, term, func(prefix string) bool {
			return windowContainsAny(prefix, 4, negationTermsCJK) || nonTemporalCJK[term] != nil && nonTemporalCJK[term](prefix)
		}) {
			return true
		}
	}
	for _, match := range presentTimePatternEN.FindAllStringIndex(query, -1) {
		prefix := query[:match[0]]
		if negationPatternEN.MatchString(prefix) {
			continue
		}
		if strings.EqualFold(query[match[0]:match[1]], "current") && electricPrefixEN.MatchString(prefix) {
			continue
		}
		return true
	}
	return false
}

// containsCJKTermExcept reports whether some occurrence of term is not excluded by
// the text before it.
func containsCJKTermExcept(query, term string, excluded func(prefix string) bool) bool {
	for offset := 0; ; {
		index := strings.Index(query[offset:], term)
		if index < 0 {
			return false
		}
		index += offset
		if !excluded(query[:index]) {
			return true
		}
		offset = index + len(term)
	}
}

func windowContainsAny(prefix string, size int, terms []string) bool {
	runes := []rune(prefix)
	window := string(runes[max(0, len(runes)-size):])
	for _, term := range terms {
		if strings.Contains(window, term) {
			return true
		}
	}
	return false
}

// queryHasExplicitTimeScope conservatively preserves calendar dates, including
// earlier months of the current year, and years other than the client year.
// Four-digit amounts such as "1999元" or "1999.99", and model numbers such as
// "RTX 4090" or "GTX 1080", are not years. No date is inferred or rewritten.
func queryHasExplicitTimeScope(query, date string) bool {
	for _, match := range calendarDatePattern.FindAllStringSubmatch(query, -1) {
		for _, month := range match[1:] {
			if value, err := strconv.Atoi(month); err == nil && value >= 1 && value <= 12 {
				return true
			}
		}
	}
	for _, term := range historicalTermsCJK {
		if containsCJKTermExcept(query, term, func(prefix string) bool {
			return nonTemporalCJK[term] != nil && nonTemporalCJK[term](prefix)
		}) {
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
		if !isASCIIDigit(runes[start]) {
			start++
			continue
		}
		end := start
		for end < len(runes) && isASCIIDigit(runes[end]) {
			end++
		}
		if end-start == 4 && !continuesNumberOrModel(runes, start, end) {
			year, _ := strconv.Atoi(string(runes[start:end]))
			if end < len(runes) && runes[end] == '年' {
				if year >= 1000 && year != currentYear {
					return true
				}
			} else if isYearSuffix(runes, end) && !followsModelName(runes, start) && year >= 1000 && year <= currentYear+10 && year != currentYear {
				return true
			}
		}
		start = end
	}
	return false
}

func isASCIIDigit(r rune) bool { return r >= '0' && r <= '9' }

// continuesNumberOrModel reports a digit run that is part of a decimal number or
// glued to a Latin model name, such as 1999.99 or RTX4090.
func continuesNumberOrModel(runes []rune, start, end int) bool {
	if start > 0 && (runes[start-1] == '.' || runes[start-1] < unicode.MaxASCII && unicode.IsLetter(runes[start-1])) {
		return true
	}
	return end+1 < len(runes) && (runes[end] == '.' || runes[end] == ',') && isASCIIDigit(runes[end+1])
}

func isYearSuffix(runes []rune, end int) bool {
	if end == len(runes) {
		return true
	}
	r := runes[end]
	return r == 's' || unicode.IsSpace(r) || unicode.IsPunct(r)
}

// followsModelName reports a bare number after an upper-case product or standard
// series word such as RTX, GTX or ISO.
func followsModelName(runes []rune, start int) bool {
	end := start
	for end > 0 && (runes[end-1] == ' ' || runes[end-1] == '-') {
		end--
	}
	letters := 0
	for index := end - 1; index >= 0 && runes[index] < unicode.MaxASCII && unicode.IsLetter(runes[index]); index-- {
		if !unicode.IsUpper(runes[index]) {
			return false
		}
		letters++
	}
	return letters >= 2
}

// foldFullWidth maps full-width ASCII variants, such as ２０２５ or ．, to ASCII.
func foldFullWidth(text string) string {
	return strings.Map(func(r rune) rune {
		if r >= '！' && r <= '～' {
			return r - 0xFEE0
		}
		if r == '\u3000' {
			return ' '
		}
		return r
	}, text)
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
