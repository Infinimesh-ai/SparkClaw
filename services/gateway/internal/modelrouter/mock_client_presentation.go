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
	mode, kind, start := "show", "all", ""
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
		kind = "day_after_tomorrow"
	case contains("明天", "tomorrow"):
		kind = "tomorrow"
	case contains("今天", "today"):
		kind = "today"
	case contains("下周", "next week", "本周", "这周", "this week"):
		kind = "this_week"
		if contains("下周", "next week") {
			kind = "next_week"
		}
	case contains("下个月", "next month", "本月", "这个月", "this month"):
		kind = "this_month"
		if contains("下个月", "next month") {
			kind = "next_month"
		}
	default:
		date := regexp.MustCompile(`\d{4}-\d{2}-\d{2}`).FindString(text)
		if date != "" {
			_, err := time.Parse(time.DateOnly, date)
			if err != nil {
				mode = "clarify"
			} else {
				kind, start = "date", date
			}
		}
	}
	if mode != "show" {
		kind = "none"
	}
	raw, _ := json.Marshal(map[string]string{"mode": mode, "range": kind, "date_start": start, "date_end": ""})
	return string(raw)
}
