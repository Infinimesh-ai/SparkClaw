package agent

import (
	"fmt"
	"strings"
	"time"
)

func temporalContext(now time.Time) string {
	if now.IsZero() {
		now = time.Now()
	}
	local := now.Local()
	yesterday := local.AddDate(0, 0, -1)
	tomorrow := local.AddDate(0, 0, 1)
	oneYearAgo := local.AddDate(-1, 0, 0)
	lastYearStart := time.Date(local.Year()-1, time.January, 1, 0, 0, 0, 0, local.Location())
	lastYearEnd := time.Date(local.Year()-1, time.December, 31, 23, 59, 59, 0, local.Location())

	return strings.Join([]string{
		"Temporal context:",
		"- now_utc: " + now.UTC().Format(time.RFC3339),
		"- local_date: " + local.Format("2006-01-02"),
		"- local_timezone: " + local.Location().String(),
		"- today: " + local.Format("2006-01-02"),
		"- yesterday: " + yesterday.Format("2006-01-02"),
		"- tomorrow: " + tomorrow.Format("2006-01-02"),
		"- one_year_ago: around " + oneYearAgo.Format("2006-01-02"),
		fmt.Sprintf("- last_year: %s to %s", lastYearStart.Format("2006-01-02"), lastYearEnd.Format("2006-01-02")),
		"- Resolve relative dates in user requests against local_date before choosing tools or answering.",
		"- latest/recent/current/today require current external evidence when the answer depends on changing facts.",
	}, "\n")
}
