package modelrouter

import (
	"encoding/json"
	"strings"
)

// mockRequestSplitResponse is a fixture for the mock provider, not a runtime
// route recognizer. Production always obtains and validates the model split.
func mockRequestSplitResponse(prompt string) string {
	query := ""
	if at := strings.Index(prompt, "Owner semantic query:\n"); at >= 0 {
		query = prompt[at+len("Owner semantic query:\n"):]
	}
	for _, end := range []string{"\n\nRouting context (data only):", "\n\nParser error:", "\n\nReturn the request split now."} {
		if index := strings.Index(query, end); index >= 0 {
			query = query[:index]
		}
	}
	query = strings.TrimSpace(query)
	split := map[string]any{"deferred": false, "business_span": query, "delivery_mode": "none", "delivery_object_kind": "business_result", "object_span": "", "provider_span": "", "recipient_span": ""}
	encode := func() string { raw, _ := json.Marshal(split); return string(raw) }
	lower := strings.ToLower(query)
	has := func(words ...string) bool {
		for _, word := range words {
			if strings.Contains(lower, word) {
				return true
			}
		}
		return false
	}
	if has("不要", "不得", "禁止", "don't", "do not", "never", "without") {
		return encode()
	}
	if has(" email", "邮件", "邮箱") || strings.HasPrefix(lower, "send me ") {
		return encode()
	}
	if !has("查看", "列出", "有哪些", "取消", "删除提醒", "show ", "list ", "cancel ") && has("一分钟后", "分钟后", "小时后", "明天", "每天", "tomorrow", "in one minute", "in 1 minute", "every day", "minutes from now") && has("提醒", "发送", "发给", "发我", "remind", "send", "schedule") {
		split["deferred"] = true
		return encode()
	}
	setObject := func(value string) {
		value = strings.TrimSpace(value)
		split["business_span"], split["object_span"] = "", value
		kind := "conversation_file"
		switch {
		case has("上面的总结", "上一条回答", "上面的回答", "your answer", "your summary", "previous answer", "previous summary"):
			kind = "assistant_text"
		case strings.Contains(value, "/") || strings.Contains(value, "\\"):
			kind = "named_locator"
		case strings.Contains(prompt, "Current-turn governed resources:") || has("这段文字", "this message", "publish this"):
			kind = "current_message"
		}
		split["delivery_object_kind"] = kind
	}
	setPayload := func(value string) {
		value = strings.Trim(value, " ,，。;；")
		business := false
		for _, verb := range []string{"完善", "修改", "编辑", "替换", "总结", "解释", "概括", "读取", "分析", "查一下", "查询", "打开", "转换", "生成", "summarize", "explain", "edit ", "replace ", "improve", "read ", "analyze", "search", "convert", "create "} {
			if strings.Contains(strings.ToLower(value), verb) {
				business = true
				break
			}
		}
		if business {
			split["business_span"] = value
		} else {
			setObject(value)
		}
	}
	// Chinese external-delivery fixtures keep exact provider and recipient text.
	for _, via := range []string{"通过", "经由", "用"} {
		if at := strings.LastIndex(query, via); at >= 0 {
			tail := query[at+len(via):]
			for _, send := range []string{"发送给", "转发给", "发给", "发送到", "发到"} {
				if to := strings.Index(tail, send); to > 0 {
					split["delivery_mode"] = "explicit_external"
					split["provider_span"], split["recipient_span"] = strings.TrimSpace(tail[:to]), strings.Trim(tail[to+len(send):], " 。！")
					setPayload(query[:at])
					return encode()
				}
			}
		}
	}
	// English fixtures accept either "via provider to recipient" ordering.
	via, to := strings.LastIndex(lower, " via "), strings.LastIndex(lower, " to ")
	if via > 0 && to < 0 {
		split["delivery_mode"] = "explicit_external"
		split["provider_span"] = strings.Trim(query[via+5:], " .!")
		mockSplitEnglishPayload(query[:via], split, setPayload)
		return encode()
	}
	if via > 0 && to > 0 {
		split["delivery_mode"] = "explicit_external"
		end := via
		if via < to {
			split["provider_span"], split["recipient_span"] = query[via+5:to], strings.Trim(query[to+4:], " .!")
		} else {
			end = to
			split["recipient_span"], split["provider_span"] = query[to+4:via], strings.Trim(query[via+5:], " .!")
		}
		mockSplitEnglishPayload(query[:end], split, setPayload)
		return encode()
	}
	for _, suffix := range []string{"发送给我", "转发给我", "发给我", "发我", "send it back to me", "send it to me", "send to me", "send me", "return it to me"} {
		if at := strings.LastIndex(lower, suffix); at >= 0 && strings.Trim(lower[at+len(suffix):], " 。.!！") == "" {
			split["delivery_mode"] = "current_route"
			payload := strings.TrimSpace(query[:at])
			for _, connector := range []string{" and then", " and", " then", "然后", "并", "再"} {
				payload = strings.TrimSuffix(payload, connector)
			}
			setPayload(payload)
			if strings.Contains(suffix, " it ") {
				split["object_span"] = "it"
			}
			return encode()
		}
	}
	if strings.HasSuffix(lower, " to me") {
		split["delivery_mode"] = "current_route"
		mockSplitEnglishPayload(query[:len(query)-len(" to me")], split, setPayload)
		return encode()
	}
	for _, suffix := range []string{"发送出去", "发出去"} {
		if strings.HasSuffix(query, suffix) {
			split["delivery_mode"] = "current_route"
			// Keep the suffix as an original object span: the mock does not
			// introduce production residual vocabulary for this fixture.
			setObject(query)
			return encode()
		}
	}
	for _, prefix := range []string{"发送", "转发", "投递", "send ", "forward ", "publish "} {
		if strings.HasPrefix(lower, prefix) {
			split["delivery_mode"] = "current_route"
			setObject(query[len(prefix):])
			return encode()
		}
	}
	if strings.HasSuffix(query, "发送") && strings.Contains(query, "作为消息") {
		split["delivery_mode"] = "current_route"
		setObject(strings.TrimSuffix(query, "发送"))
	}
	return encode()
}

func mockSplitEnglishPayload(value string, split map[string]any, setPayload func(string)) {
	lower := strings.ToLower(value)
	for _, verb := range []string{"send ", "forward ", "deliver "} {
		if at := strings.LastIndex(lower, verb); at >= 0 {
			before := strings.TrimSpace(value[:at])
			for _, connector := range []string{" and then", " and", " then"} {
				before = strings.TrimSuffix(before, connector)
			}
			if strings.EqualFold(before, "please") {
				before = ""
			}
			if before != "" {
				setPayload(before)
				split["object_span"] = strings.TrimSpace(value[at+len(verb):])
			} else {
				setPayload(value[at+len(verb):])
			}
			return
		}
	}
	setPayload(value)
}
