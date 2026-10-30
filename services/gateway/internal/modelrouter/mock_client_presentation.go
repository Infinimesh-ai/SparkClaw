package modelrouter

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// This is synthetic-model behavior only. Production interpretation uses the
// strict client_schedule_view model schema, after semantic intent routing.
func mockClientScheduleView(user string) string {
	var request map[string]string
	if json.Unmarshal([]byte(user), &request) != nil {
		return `{}`
	}
	text := strings.ToLower(request["owner_text"])
	if injected := mockInjectedResponse(request["owner_text"], "MOCK_CLIENT_SCHEDULE_RESPONSE:"); injected != "" {
		return injected
	}
	mode, start, end := "show", "", ""
	base, _ := time.Parse(time.DateOnly, request["reference_date"])
	contains := func(terms ...string) bool {
		for _, term := range terms {
			if strings.Contains(text, term) {
				return true
			}
		}
		return false
	}
	switch {
	case contains("分析", "冲突", "统计", "多少", "总结", "修改", "删除", "创建", "发送", "analy", "conflict", "count", "summary", "delete", "modify", "create", "send"):
		mode = "unsupported"
	case contains("那天", "某天", "下午", "上午", "晚上", "待办", "已完成", "取消的", "会议日程", "that day", "afternoon", "pending", "completed"):
		mode = "clarify"
	case contains("全部", "所有", "all schedule"):
	case contains("后天", "day after tomorrow"):
		start = base.AddDate(0, 0, 2).Format(time.DateOnly)
		end = base.AddDate(0, 0, 3).Format(time.DateOnly)
	case contains("明天", "tomorrow"):
		start = base.AddDate(0, 0, 1).Format(time.DateOnly)
		end = base.AddDate(0, 0, 2).Format(time.DateOnly)
	case contains("今天", "today"):
		start = base.Format(time.DateOnly)
		end = base.AddDate(0, 0, 1).Format(time.DateOnly)
	case contains("下周", "next week", "本周", "这周", "this week"):
		offset := (int(base.Weekday()) + 6) % 7
		base = base.AddDate(0, 0, -offset)
		if contains("下周", "next week") {
			base = base.AddDate(0, 0, 7)
		}
		start = base.Format(time.DateOnly)
		end = base.AddDate(0, 0, 7).Format(time.DateOnly)
	case contains("下个月", "next month", "本月", "这个月", "this month"):
		base = time.Date(base.Year(), base.Month(), 1, 0, 0, 0, 0, time.UTC)
		if contains("下个月", "next month") {
			base = base.AddDate(0, 1, 0)
		}
		start = base.Format(time.DateOnly)
		end = base.AddDate(0, 1, 0).Format(time.DateOnly)
	default:
		date := regexp.MustCompile(`\d{4}-\d{2}-\d{2}`).FindString(text)
		if date != "" {
			d, err := time.Parse(time.DateOnly, date)
			if err != nil {
				mode = "clarify"
			} else {
				start = date
				end = d.AddDate(0, 0, 1).Format(time.DateOnly)
			}
		}
	}
	raw, _ := json.Marshal(map[string]string{"mode": mode, "date_start": start, "date_end": end})
	return string(raw)
}
