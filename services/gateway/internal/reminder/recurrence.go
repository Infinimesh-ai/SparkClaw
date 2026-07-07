package reminder

import (
	"strconv"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// recurrenceRule is a fixed interval between occurrences. Calendar units
// advance with AddDate in the reminder's timezone so wall-clock times survive
// DST shifts and variable month lengths.
type recurrenceRule struct {
	years    int
	months   int
	days     int
	duration time.Duration
}

func (r recurrenceRule) advance(t time.Time) time.Time {
	if r.years != 0 || r.months != 0 || r.days != 0 {
		t = t.AddDate(r.years, r.months, r.days)
	}
	return t.Add(r.duration)
}

// nextOccurrence computes the first occurrence strictly after now for a
// recurring reminder, stepping forward from its current DueTime. It returns
// false when the recurrence string is not recognized.
func nextOccurrence(reminder app.Reminder, now time.Time) (time.Time, bool) {
	rule, ok := parseRecurrence(reminder.Recurrence)
	if !ok {
		return time.Time{}, false
	}
	loc := time.UTC
	if reminder.Timezone != "" {
		if l, err := time.LoadLocation(reminder.Timezone); err == nil {
			loc = l
		}
	}
	next := reminder.DueTime.In(loc)
	for i := 0; i < 10000; i++ {
		next = rule.advance(next)
		if next.After(now) {
			return next.UTC(), true
		}
	}
	// DueTime is pathologically far behind; schedule one interval from now.
	return rule.advance(now.In(loc)).UTC(), true
}

func parseRecurrence(value string) (recurrenceRule, bool) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "":
		return recurrenceRule{}, false
	case "minutely", "every minute", "每分钟":
		return recurrenceRule{duration: time.Minute}, true
	case "hourly", "every hour", "每小时":
		return recurrenceRule{duration: time.Hour}, true
	case "daily", "every day", "everyday", "每天", "每日":
		return recurrenceRule{days: 1}, true
	case "weekly", "every week", "每周", "每星期":
		return recurrenceRule{days: 7}, true
	case "monthly", "every month", "每月":
		return recurrenceRule{months: 1}, true
	case "yearly", "annually", "every year", "每年":
		return recurrenceRule{years: 1}, true
	}
	if fields := strings.Fields(normalized); len(fields) == 3 && fields[0] == "every" {
		if n, err := strconv.Atoi(fields[1]); err == nil && n > 0 {
			switch strings.TrimSuffix(fields[2], "s") {
			case "minute", "min":
				return recurrenceRule{duration: time.Duration(n) * time.Minute}, true
			case "hour":
				return recurrenceRule{duration: time.Duration(n) * time.Hour}, true
			case "day":
				return recurrenceRule{days: n}, true
			case "week":
				return recurrenceRule{days: 7 * n}, true
			case "month":
				return recurrenceRule{months: n}, true
			case "year":
				return recurrenceRule{years: n}, true
			}
		}
	}
	if d, err := time.ParseDuration(normalized); err == nil && d > 0 {
		return recurrenceRule{duration: d}, true
	}
	return recurrenceRule{}, false
}
