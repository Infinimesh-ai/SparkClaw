package mcpsafety

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpclient"
)

func TestProjectToolResultCanonicalizesRedactsAndBounds(t *testing.T) {
	base64Value := strings.Repeat("QUJD", 2048)
	result := mcpclient.ToolResult{
		StructuredContent: map[string]any{"result": map[string]any{
			"token": "secret-token", "url": "https://storage.test/file?X-Amz-Signature=signed",
			"base64": base64Value, "text": strings.Repeat("content ", 3000),
		}},
		Content: []mcpclient.ContentBlock{{"type": "text", "text": "authorization=secret-value"}},
	}
	projected := ProjectToolResult(result, "fixture", "read", Limits{StateMaxBytes: 16 << 10, ArchiveMaxBytes: 1 << 20})
	state := projected.Output.(map[string]any)
	archive := projected.ArchiveOutput.(map[string]any)
	for label, value := range map[string]any{"state": state, "archive": archive} {
		raw, _ := json.Marshal(value)
		text := string(raw)
		if strings.Contains(text, "secret-token") || strings.Contains(text, "secret-value") || strings.Contains(text, "signed") {
			t.Fatalf("%s projection leaked sensitive content: %s", label, text)
		}
	}
	if _, ok := state["base64"].(map[string]any); !ok {
		t.Fatalf("large base64 entered state: %#v", state["base64"])
	}
	structured := archive["structured_content"].(map[string]any)
	archivedResult := structured["result"].(map[string]any)
	if archivedResult["base64"] != base64Value {
		t.Fatal("bounded archive did not retain allowed base64 data")
	}
}

func TestCanonicalToolResultUsesStructuredAndTextFallbacks(t *testing.T) {
	for _, test := range []struct {
		name   string
		result mcpclient.ToolResult
		want   string
	}{
		{name: "wrapped result", result: mcpclient.ToolResult{StructuredContent: map[string]any{"result": "wrapped", "ignored": true}}, want: `"wrapped"`},
		{name: "structured content", result: mcpclient.ToolResult{StructuredContent: map[string]any{"value": "full"}}, want: `{"value":"full"}`},
		{name: "JSON text", result: mcpclient.ToolResult{Content: []mcpclient.ContentBlock{{"type": "text", "text": `{"value":"decoded"}`}}}, want: `{"value":"decoded"}`},
		{name: "plain text", result: mcpclient.ToolResult{Content: []mcpclient.ContentBlock{{"type": "text", "text": "plain"}}}, want: `"plain"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, _ := json.Marshal(CanonicalToolResult(test.result))
			if string(raw) != test.want {
				t.Fatalf("canonical result = %s, want %s", raw, test.want)
			}
		})
	}
}

func TestUnsafeForPersistenceRejectsExternalSecrets(t *testing.T) {
	for _, test := range []struct {
		name  string
		value any
		want  bool
	}{
		{name: "sensitive key", value: map[string]any{"api_token": "value"}, want: true},
		{name: "inline secret", value: map[string]any{"note": "password=hunter2"}, want: true},
		{name: "bearer", value: map[string]any{"header": "Bearer abc"}, want: true},
		{name: "signed URL", value: map[string]any{"url": "https://storage.test/a?signature=abc"}, want: true},
		{name: "large base64", value: map[string]any{"payload": strings.Repeat("QUJD", 2048)}, want: true},
		{name: "ordinary arguments", value: map[string]any{"taskId": "task-1", "title": "Build docs"}, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := UnsafeForPersistence(test.value); got != test.want {
				t.Fatalf("UnsafeForPersistence() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestBoundedProjectionUsesDigestRecord(t *testing.T) {
	projected := BoundedProjection(map[string]any{"items": make([]any, 1000)}, Archive, 1024).(map[string]any)
	if projected["truncated"] != true || projected["sha256"] == "" || projected["original_bytes"] == nil {
		t.Fatalf("missing truncation evidence: %#v", projected)
	}
}

func TestBoundedProjectionFailsClosedOnNonJSONValue(t *testing.T) {
	projected := BoundedProjection(map[string]any{"invalid": func() {}}, State, 1024).(map[string]any)
	if projected["truncated"] != true || projected["reason"] != "non_json_result" {
		t.Fatalf("non-JSON projection did not fail closed: %#v", projected)
	}
}
