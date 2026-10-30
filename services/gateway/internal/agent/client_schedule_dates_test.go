package agent

import (
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestSimpleClientScheduleRangeKeepsModifiersAndNegationForModel(t *testing.T) {
	for goal, want := range map[string]string{
		"显示明天的日程": "tomorrow", "请帮我查看下周的日程。": "next_week",
		"Show tomorrow's schedule": "tomorrow", "please show my schedules for next month": "next_month",
		"不要显示明天的日程": "", "显示明天的日程并分析冲突": "", "显示明天下午日程": "",
		"显示今天和明天的日程": "", "显示除明天外的日程": "", "显示2030-01-02的日程": "",
	} {
		if got := simpleClientScheduleRange(goal); got != want {
			t.Errorf("%q: got %q, want %q", goal, got, want)
		}
	}
}

func TestClientScheduleCalendarRanges(t *testing.T) {
	for _, tc := range []struct{ kind, zone, requested, start, end string }{
		{"tomorrow", "Asia/Shanghai", "2026-10-29T18:00:00Z", "2026-10-31", "2026-11-01"},
		{"tomorrow", "America/New_York", "2026-11-01T03:30:00Z", "2026-11-01", "2026-11-02"},
		{"today", "America/New_York", "2026-11-01T05:30:00Z", "2026-11-01", "2026-11-02"},
		{"day_after_tomorrow", "Asia/Shanghai", "2026-12-30T16:00:00Z", "2027-01-02", "2027-01-03"},
		{"this_week", "Asia/Shanghai", "2026-11-01T00:00:00Z", "2026-10-26", "2026-11-02"},
		{"next_week", "Asia/Shanghai", "2026-11-01T00:00:00Z", "2026-11-02", "2026-11-09"},
		{"this_month", "UTC", "2028-02-29T10:00:00Z", "2028-02-01", "2028-03-01"},
		{"next_month", "Asia/Shanghai", "2026-12-30T16:00:00Z", "2027-01-01", "2027-02-01"},
	} {
		t.Run(tc.kind+"/"+tc.zone, func(t *testing.T) {
			p := clientPresentationForTest()
			p.Timezone = tc.zone
			p.RequestedAt, _ = time.Parse(time.RFC3339, tc.requested)
			fields := map[string]string{"mode": "show", "range": tc.kind, "date_start": "", "date_end": ""}
			if err := resolveClientScheduleDates(fields, p); err != nil || fields["date_start"] != tc.start || fields["date_end"] != tc.end {
				t.Fatalf("resolved=%v err=%v", fields, err)
			}
		})
	}
}

func TestClientScheduleViewRejectsModelArithmeticForRelativeDates(t *testing.T) {
	runtime, _, _, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	for _, raw := range []string{
		`{"mode":"show","range":"tomorrow","date_start":"2026-10-30","date_end":"2026-10-31"}`,
		`{"mode":"show","range":"all","date_start":"2026-10-30","date_end":"2026-10-31"}`,
		`{"mode":"show","range":"date","date_start":"2026-02-30","date_end":""}`,
		`{"mode":"show","range":"dates","date_start":"","date_end":""}`,
		`{"mode":"show","range":"dates","date_start":"2026-10-30","date_end":"2026-10-29"}`,
		`{"mode":"clarify","range":"all","date_start":"","date_end":""}`,
		`{"mode":"show","range":"unknown","date_start":"","date_end":""}`,
		`{"mode":"show","range":null,"date_start":"","date_end":""}`,
	} {
		_, err := runtime.selectClientScheduleView(t.Context(), app.AgentRun{}, clientPresentationForTest(), "显示明天日程\nMOCK_CLIENT_SCHEDULE_RESPONSE:"+raw)
		if err == nil {
			t.Fatalf("accepted invalid model range: %s", raw)
		}
	}
}
