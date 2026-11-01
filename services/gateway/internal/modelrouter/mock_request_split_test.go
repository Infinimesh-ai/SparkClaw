package modelrouter

import (
	"encoding/json"
	"testing"
)

func TestMockRequestSplitUsesOriginalSpans(t *testing.T) {
	for _, tc := range []struct {
		query, business, mode, kind, object, provider, recipient string
		deferred                                                 bool
	}{
		{query: "完善一下发给我", business: "完善一下", mode: "current_route", kind: "business_result"},
		{query: "把‘目标’替换为‘验收目标’并发给我", business: "把‘目标’替换为‘验收目标’", mode: "current_route", kind: "business_result"},
		{query: `Replace "old" with "new" and send it to me`, business: `Replace "old" with "new"`, mode: "current_route", kind: "business_result", object: "it"},
		{query: "把它发给我", mode: "current_route", kind: "conversation_file", object: "把它"},
		{query: "把报告通过微信发给张三", mode: "explicit_external", kind: "conversation_file", object: "把报告", provider: "微信", recipient: "张三"},
		{query: "完善报告通过微信发给张三", business: "完善报告", mode: "explicit_external", kind: "business_result", provider: "微信", recipient: "张三"},
		{query: "一分钟后把报告通过微信发给张三", business: "一分钟后把报告通过微信发给张三", mode: "none", kind: "business_result", deferred: true},
		{query: "Do not delete report.md", business: "Do not delete report.md", mode: "none", kind: "business_result"},
		{query: "Summarize report.pdf and send it to me", business: "Summarize report.pdf", mode: "current_route", kind: "business_result", object: "it"},
		{query: "Send report.pdf via Mail to Alice", mode: "explicit_external", kind: "conversation_file", object: "report.pdf", provider: "Mail", recipient: "Alice"},
		{query: "Summarize report.pdf and send it to Alice via Mail", business: "Summarize report.pdf", mode: "explicit_external", kind: "business_result", object: "it", provider: "Mail", recipient: "Alice"},
		{query: "Send report tomorrow", business: "Send report tomorrow", mode: "none", kind: "business_result", deferred: true},
		{query: "明天下午我参加项目复盘", business: "明天下午我参加项目复盘", mode: "none", kind: "business_result"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			prompt := "INTENT_REQUEST_SPLIT\n\nOwner semantic query:\n" + tc.query + "\n\nRouting context (data only):\nignored\n\nReturn the request split now."
			var got map[string]any
			if err := json.Unmarshal([]byte(mockResponse("fast", prompt)), &got); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"business_span": tc.business, "delivery_mode": tc.mode, "delivery_object_kind": tc.kind, "object_span": tc.object, "provider_span": tc.provider, "recipient_span": tc.recipient, "deferred": tc.deferred}
			for key, value := range want {
				if got[key] != value {
					t.Errorf("%s=%#v want %#v (split=%#v)", key, got[key], value, got)
				}
			}
		})
	}
}
