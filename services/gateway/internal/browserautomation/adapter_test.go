package browserautomation

import (
	"strings"
	"testing"
)

func TestMapToolMapsBrowserTypeToFillByDefault(t *testing.T) {
	rawTool, rawArgs, err := mapTool("browser.type", map[string]any{"uid": "1", "text": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if rawTool != "fill" {
		t.Fatalf("browser.type should prefer fill by default, got %q", rawTool)
	}
	if rawArgs["value"] != "hello" {
		t.Fatalf("browser.type should map public text arg to MCP fill value arg: %#v", rawArgs)
	}
	if _, ok := rawArgs["text"]; ok {
		t.Fatalf("browser.type should not forward public text arg to MCP fill: %#v", rawArgs)
	}
}

func TestMapToolMapsBrowserTypeToTypeTextForFocusedInputs(t *testing.T) {
	rawTool, rawArgs, err := mapTool("browser.type", map[string]any{"text": "hello", "mode": "chat"})
	if err != nil {
		t.Fatal(err)
	}
	if rawTool != "type_text" {
		t.Fatalf("browser.type should use type_text for chat/focused input, got %q", rawTool)
	}
	if _, ok := rawArgs["uid"]; ok {
		t.Fatalf("browser.type should not forward uid to MCP type_text: %#v", rawArgs)
	}
}

func TestMapToolMapsBrowserTypeWithRefToFillEvenWhenModeHintsTypeText(t *testing.T) {
	rawTool, rawArgs, err := mapTool("browser.type", map[string]any{
		"ref":  "6_7",
		"text": "hello",
		"mode": "search",
	})
	if err != nil {
		t.Fatal(err)
	}
	if rawTool != "fill" {
		t.Fatalf("browser.type with explicit ref should use fill, got %q", rawTool)
	}
	if rawArgs["uid"] != "6_7" || rawArgs["value"] != "hello" {
		t.Fatalf("browser.type should normalize ref/text to uid/value for fill: %#v", rawArgs)
	}
	if _, ok := rawArgs["ref"]; ok {
		t.Fatalf("browser.type should not forward ref alias to MCP fill: %#v", rawArgs)
	}
}

func TestMapToolMapsNavigateToChromeDevToolsTool(t *testing.T) {
	rawTool, rawArgs, err := mapTool("browser.navigate", map[string]any{"url": "https://example.com", "reason": "test"})
	if err != nil {
		t.Fatal(err)
	}
	if rawTool != "navigate_page" {
		t.Fatalf("browser.navigate should map to navigate_page, got %q", rawTool)
	}
	if _, ok := rawArgs["reason"]; ok {
		t.Fatalf("browser.navigate should not forward SparkClaw-only reason to MCP: %#v", rawArgs)
	}
}

func TestMapToolDoesNotForwardPageIDToSnapshot(t *testing.T) {
	rawTool, rawArgs, err := mapTool("browser.snapshot", map[string]any{
		"page_id": "page_1",
		"reason":  "inspect current page",
		"verbose": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rawTool != "take_snapshot" {
		t.Fatalf("browser.snapshot should map to take_snapshot, got %q", rawTool)
	}
	if _, ok := rawArgs["page_id"]; ok {
		t.Fatalf("browser.snapshot should not forward page_id to take_snapshot: %#v", rawArgs)
	}
	if _, ok := rawArgs["reason"]; ok {
		t.Fatalf("browser.snapshot should not forward reason to take_snapshot: %#v", rawArgs)
	}
	if rawArgs["verbose"] != true {
		t.Fatalf("browser.snapshot should preserve supported verbose arg: %#v", rawArgs)
	}
}

func TestMCPToolErrorDetectsIsError(t *testing.T) {
	err := mcpToolError("navigate_page", map[string]any{
		"isError": true,
		"content": []any{map[string]any{
			"type": "text",
			"text": "Unknown argument for tool",
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "Unknown argument") {
		t.Fatalf("expected MCP isError to become error, got %v", err)
	}
}

func TestNormalizeListTabsExtractsPagesFromMCPContentText(t *testing.T) {
	output := normalizeOutput("browser.list_tabs", map[string]any{
		"content": []any{
			map[string]any{
				"type": "text",
				"text": `{"pages":[{"page_id":"page_1","url":"https://example.com","title":"Example"}]}`,
			},
		},
	})
	result, ok := output.(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %#v", output)
	}
	pages, ok := result["pages"].([]any)
	if !ok || len(pages) != 1 {
		t.Fatalf("expected normalized pages, got %#v", result["pages"])
	}
}

func TestNormalizeListTabsReturnsEmptyPagesForNoOpenPages(t *testing.T) {
	output := normalizeOutput("browser.list_tabs", map[string]any{"content": []any{}})
	result, ok := output.(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %#v", output)
	}
	pages, ok := result["pages"].([]any)
	if !ok {
		t.Fatalf("expected pages array, got %#v", result["pages"])
	}
	if len(pages) != 0 {
		t.Fatalf("expected empty pages, got %#v", pages)
	}
}
