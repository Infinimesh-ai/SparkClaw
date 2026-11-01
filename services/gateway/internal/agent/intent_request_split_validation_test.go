package agent

import (
	"strings"
	"testing"
)

func TestValidateRequestSplit(t *testing.T) {
	cases := []struct {
		name, text         string
		split              requestSplit
		externalMCP, valid bool
	}{
		{"business and return", "完善一下发给我", requestSplit{BusinessSpan: "完善一下", DeliveryMode: "current_route", DeliveryObjectKind: "business_result"}, false, true},
		{"pure file delivery", "把它发给我", requestSplit{DeliveryMode: "current_route", DeliveryObjectKind: "conversation_file", ObjectSpan: "把它"}, false, true},
		{"external delivery", "把报告通过微信发给张三", requestSplit{DeliveryMode: "explicit_external", DeliveryObjectKind: "conversation_file", ObjectSpan: "把报告", ProviderSpan: "微信", RecipientSpan: "张三"}, false, true},
		{"lost negation", "不要删除 report.md，只读一下", requestSplit{BusinessSpan: "删除 report.md", DeliveryMode: "current_route", DeliveryObjectKind: "business_result"}, false, false},
		{"invented span", "完善一下发给我", requestSplit{BusinessSpan: "完善报告", DeliveryMode: "current_route", DeliveryObjectKind: "business_result"}, false, false},
		{"overlap", "send report", requestSplit{BusinessSpan: "report", DeliveryMode: "current_route", DeliveryObjectKind: "named_locator", ObjectSpan: "report"}, false, false},
		{"unknown suffix", "sender report", requestSplit{BusinessSpan: "report", DeliveryMode: "current_route", DeliveryObjectKind: "business_result"}, false, false},
		{"deferred", "一分钟后把文件发给我", requestSplit{Deferred: true, BusinessSpan: "一分钟后把文件发给我", DeliveryMode: "none", DeliveryObjectKind: "business_result"}, false, true},
		{"deferred split", "一分钟后把文件发给我", requestSplit{Deferred: true, BusinessSpan: "把文件", DeliveryMode: "current_route", DeliveryObjectKind: "business_result"}, false, false},
		{"external mcp history", "发给我", requestSplit{DeliveryMode: "current_route", DeliveryObjectKind: "assistant_text"}, true, false},
		{"provider without external", "send via mail", requestSplit{BusinessSpan: "send", DeliveryMode: "none", DeliveryObjectKind: "business_result", ProviderSpan: "mail"}, false, false},
		{"pure business result", "发给我", requestSplit{DeliveryMode: "current_route", DeliveryObjectKind: "business_result"}, false, false},
		{"result pronoun", "Summarize report.pdf and send it to me", requestSplit{BusinessSpan: "Summarize report.pdf", DeliveryMode: "current_route", DeliveryObjectKind: "business_result", ObjectSpan: "it"}, false, true},
		// A missing channel or recipient is resolved or asked for by Message Control.
		{"missing provider", "send report to Alice", requestSplit{DeliveryMode: "explicit_external", DeliveryObjectKind: "conversation_file", ObjectSpan: "report", RecipientSpan: "Alice"}, false, true},
		{"missing recipient", "send report via mail", requestSplit{DeliveryMode: "explicit_external", DeliveryObjectKind: "conversation_file", ObjectSpan: "report", ProviderSpan: "mail"}, false, true},
		{"external channel only", "总结这份报告，通过微信发出去", requestSplit{BusinessSpan: "总结这份报告", DeliveryMode: "explicit_external", DeliveryObjectKind: "business_result", ProviderSpan: "微信"}, false, true},
		{"external pronoun residual", "Summarize report.md and send it via email to Alice", requestSplit{BusinessSpan: "Summarize report.md", DeliveryMode: "explicit_external", DeliveryObjectKind: "business_result", ProviderSpan: "email", RecipientSpan: "Alice"}, false, true},
		{"external without target", "send report", requestSplit{DeliveryMode: "explicit_external", DeliveryObjectKind: "conversation_file", ObjectSpan: "report"}, false, false},
		{"negation cannot become object", "不要删除 report.md，只读一下", requestSplit{BusinessSpan: "删除 report.md", DeliveryMode: "current_route", DeliveryObjectKind: "business_result", ObjectSpan: "不要"}, false, false},
		{"short negation cannot become object", "别删除 report.md", requestSplit{BusinessSpan: "删除 report.md", DeliveryMode: "current_route", DeliveryObjectKind: "business_result", ObjectSpan: "别"}, false, false},
		{"single negation cannot become object", "不删除 report.md", requestSplit{BusinessSpan: "删除 report.md", DeliveryMode: "current_route", DeliveryObjectKind: "business_result", ObjectSpan: "不"}, false, false},
		{"constraint cannot become object", "Read report.pdf and send without deleting it", requestSplit{BusinessSpan: "Read report.pdf", DeliveryMode: "current_route", DeliveryObjectKind: "business_result", ObjectSpan: "without deleting it"}, false, false},
		{"whole negative business", "不要删除 report.md，只读一下", requestSplit{BusinessSpan: "不要删除 report.md，只读一下", DeliveryMode: "none", DeliveryObjectKind: "business_result"}, false, true},
		{"repeated occurrence", "send send", requestSplit{BusinessSpan: "send", DeliveryMode: "current_route", DeliveryObjectKind: "business_result"}, false, false},
		{"utf8 bytes", "read \xff", requestSplit{BusinessSpan: "read \xff", DeliveryMode: "none", DeliveryObjectKind: "business_result"}, false, false},
		{"unicode rewrite", "Read café.pdf", requestSplit{BusinessSpan: "Read cafe\u0301.pdf", DeliveryMode: "none", DeliveryObjectKind: "business_result"}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRequestSplit(tc.text, tc.split, tc.externalMCP)
			if (err == nil) != tc.valid {
				t.Fatalf("validation error = %v, valid = %v", err, tc.valid)
			}
		})
	}
}

func TestParseRequestSplitRequiresExactFields(t *testing.T) {
	valid := `{"deferred":false,"business_span":"完善","delivery_mode":"current_route","delivery_object_kind":"business_result","object_span":"","provider_span":"","recipient_span":""}`
	cases := []struct {
		name, raw string
		valid     bool
	}{
		{"valid", valid, true},
		{"missing", `{"deferred":false}`, false},
		{"duplicate", `{"deferred":false,"deferred":true,"business_span":"完善","delivery_mode":"current_route","delivery_object_kind":"business_result","object_span":"","provider_span":"","recipient_span":""}`, false},
		{"null", `{"deferred":null,"business_span":"完善","delivery_mode":"current_route","delivery_object_kind":"business_result","object_span":"","provider_span":"","recipient_span":""}`, false},
		{"extra", valid[:len(valid)-1] + `,"extra":1}`, false},
		{"trailing", valid + ` {}`, false},
		{"wrong type", `{"deferred":"false","business_span":"完善","delivery_mode":"current_route","delivery_object_kind":"business_result","object_span":"","provider_span":"","recipient_span":""}`, false},
		{"unpaired high surrogate", strings.Replace(valid, "完善", `\ud800`, 1), false},
		{"unpaired low surrogate", strings.Replace(valid, "完善", `\udc00`, 1), false},
		{"surrogate pair", strings.Replace(valid, "完善", `\ud83d\ude00`, 1), true},
		{"literal escaped backslash", strings.Replace(valid, "完善", `\\ud800`, 1), true},
		{"invalid utf8", strings.Replace(valid, "完善", "\xff", 1), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseRequestSplit(tc.raw)
			if (err == nil) != tc.valid {
				t.Fatalf("parse error = %v", err)
			}
		})
	}
}
