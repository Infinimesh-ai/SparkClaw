package agent

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// Match the whole read-only phrase, not isolated keywords: negation, exclusions,
// multiple dates, analysis and time-of-day filters must remain model decisions.
// This extracts a date slot only after the normal semantic route admits a read.
var simpleScheduleRanges = []*regexp.Regexp{
	regexp.MustCompile(`^(?:请)?(?:帮我)?(?:显示|展示|打开|查看|列出|看看|看一下)(?:一下)?(?:(?:我|本机|本地)(?:的)?)?(今天|明天|后天|本周|这周|下周|本月|这个月|下个月)(?:的)?(?:日程|定时任务|计划任务|提醒)(?:列表)?[。！？?!]?$`),
	regexp.MustCompile(`^(?:please )?(?:show|open|view|list)(?: me)?(?: my| local)? (today|tomorrow|day after tomorrow|this week|next week|this month|next month)(?:'s)? (?:schedules?|scheduled tasks|reminders?)[.!?]?$`),
	regexp.MustCompile(`^(?:please )?(?:show|open|view|list)(?: me)?(?: my| local)? (?:schedules?|scheduled tasks|reminders?) for (today|tomorrow|day after tomorrow|this week|next week|this month|next month)[.!?]?$`),
}

func simpleClientScheduleRange(goal string) string {
	for _, pattern := range simpleScheduleRanges {
		match := pattern.FindStringSubmatch(strings.ToLower(strings.TrimSpace(goal)))
		if len(match) != 2 {
			continue
		}
		switch match[1] {
		case "今天", "today":
			return "today"
		case "明天", "tomorrow":
			return "tomorrow"
		case "后天", "day after tomorrow":
			return "day_after_tomorrow"
		case "本周", "这周", "this week":
			return "this_week"
		case "下周", "next week":
			return "next_week"
		case "本月", "这个月", "this month":
			return "this_month"
		case "下个月", "next month":
			return "next_month"
		}
	}
	return ""
}

// The model selects a calendar concept; arithmetic is bound to the original
// request's clock and timezone. In particular tomorrow cannot drift to today
// because a model computed a syntactically valid but incorrect ISO date.
func resolveClientScheduleDates(fields map[string]string, p app.ClientPresentation) error {
	invalid := errors.New("invalid client schedule range")
	mode, kind := fields["mode"], fields["range"]
	hasDates := fields["date_start"] != "" || fields["date_end"] != ""
	if mode == "clarify" || mode == "unsupported" {
		if kind != "none" || hasDates {
			return invalid
		}
		return nil
	}
	if mode != "show" || kind != "date" && kind != "dates" && hasDates {
		return invalid
	}
	location, err := time.LoadLocation(p.Timezone)
	if err != nil || p.RequestedAt.IsZero() {
		return invalid
	}
	ref := p.RequestedAt.In(location)
	start := time.Date(ref.Year(), ref.Month(), ref.Day(), 0, 0, 0, 0, location)
	days := 1
	switch kind {
	case "all":
		return nil
	case "today":
	case "tomorrow":
		start = start.AddDate(0, 0, 1)
	case "day_after_tomorrow":
		start = start.AddDate(0, 0, 2)
	case "this_week", "next_week":
		start = start.AddDate(0, 0, -((int(start.Weekday()) + 6) % 7))
		if kind == "next_week" {
			start = start.AddDate(0, 0, 7)
		}
		days = 7
	case "this_month", "next_month":
		start = time.Date(ref.Year(), ref.Month(), 1, 0, 0, 0, 0, location)
		if kind == "next_month" {
			start = start.AddDate(0, 1, 0)
		}
		fields["date_start"], fields["date_end"] = start.Format(time.DateOnly), start.AddDate(0, 1, 0).Format(time.DateOnly)
		return nil
	case "date":
		start, err = time.Parse(time.DateOnly, fields["date_start"])
		if err != nil || fields["date_end"] != "" {
			return invalid
		}
	case "dates":
		if fields["date_start"] == "" || !app.ValidClientScheduleDates(fields["date_start"], fields["date_end"]) {
			return invalid
		}
		return nil
	default:
		return invalid
	}
	fields["date_start"], fields["date_end"] = start.Format(time.DateOnly), start.AddDate(0, 0, days).Format(time.DateOnly)
	if !app.ValidClientScheduleDates(fields["date_start"], fields["date_end"]) {
		return invalid
	}
	return nil
}
