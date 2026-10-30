package agent

import (
	"errors"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

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
