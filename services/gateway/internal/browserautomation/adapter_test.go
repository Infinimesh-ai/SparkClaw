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
	rawTool, rawArgs, err := mapTool("browser.navigate", map[string]any{
		"url":             "https://example.com",
		"reason":          "test",
		"browser_mode":    "collaborative",
		"presentation":    "visible",
		"surface_visible": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rawTool != "navigate_page" {
		t.Fatalf("browser.navigate should map to navigate_page, got %q", rawTool)
	}
	if _, ok := rawArgs["reason"]; ok {
		t.Fatalf("browser.navigate should not forward SparkClaw-only reason to MCP: %#v", rawArgs)
	}
	for _, metadata := range []string{"browser_mode", "presentation", "surface_visible"} {
		if _, ok := rawArgs[metadata]; ok {
			t.Fatalf("browser.navigate should not forward SparkClaw-only %s to MCP: %#v", metadata, rawArgs)
		}
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

func TestEvaluatePayloadMapExtractsRenderedPageFields(t *testing.T) {
	output := map[string]any{
		"content": []any{
			map[string]any{
				"type": "text",
				"text": `{"url":"https://example.com/rendered","title":"Rendered","html":"<article>Body</article>","rendered":true,"readyState":"complete"}`,
			},
		},
	}
	payload := evaluatePayloadMap(output)
	if payload == nil {
		t.Fatal("expected payload")
	}
	if firstStringValue(payload, "url") != "https://example.com/rendered" ||
		firstStringValue(payload, "title") != "Rendered" ||
		!boolValue(payload["rendered"]) {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestEvaluatePayloadMapExtractsFencedMCPJSON(t *testing.T) {
	output := map[string]any{
		"content": []any{
			map[string]any{
				"type": "text",
				"text": "Script ran on page and returned:\n```json\n{\"url\":\"https://example.com/next\",\"title\":\"Next page\",\"readyState\":\"complete\"}\n```",
			},
		},
	}
	payload := evaluatePayloadMap(output)
	if payload == nil {
		t.Fatal("expected fenced MCP payload")
	}
	if firstStringValue(payload, "url") != "https://example.com/next" ||
		firstStringValue(payload, "title") != "Next page" ||
		firstStringValue(payload, "readyState") != "complete" {
		t.Fatalf("unexpected fenced payload: %#v", payload)
	}
}

func TestReadPageSnapshotRequestDefaultsFalse(t *testing.T) {
	if readPageSnapshotRequested(nil) {
		t.Fatal("browser.read should not request take_snapshot by default")
	}
	if readPageSnapshotRequested(map[string]any{"max_chars": 120000}) {
		t.Fatal("max_chars-only browser.read should not request take_snapshot")
	}
	if !readPageSnapshotRequested(map[string]any{"include_snapshot": true}) {
		t.Fatal("include_snapshot should request take_snapshot")
	}
}

func TestBrowserReadEvaluateFunctionEmbedsMaxCharsWithoutMCPArgs(t *testing.T) {
	fn := browserReadEvaluateFunction(120000)
	if !strings.Contains(fn, "const limit = 120000;") {
		t.Fatalf("browser read script should embed max chars limit:\n%s", fn)
	}
	if strings.Contains(fn, "maxChars") {
		t.Fatalf("browser read script should not depend on MCP args as generic parameters:\n%s", fn)
	}
	fallback := browserReadEvaluateFunction(0)
	if !strings.Contains(fallback, "const limit = 120000;") {
		t.Fatalf("expected default max chars limit, got:\n%s", fallback)
	}
}

func TestHiddenMCPArgsAppendHeadlessIsolationFlags(t *testing.T) {
	args := hiddenMCPArgs([]string{"-y", "chrome-devtools-mcp@latest"})
	for _, want := range []string{"--headless", "--isolated", "--viewport=" + hiddenBrowserViewport, "--no-usage-statistics"} {
		if !containsString(args, want) {
			t.Fatalf("hidden MCP args missing %q: %#v", want, args)
		}
	}
}

func TestHiddenMCPArgsDoNotDuplicateConfiguredFlags(t *testing.T) {
	args := hiddenMCPArgs([]string{
		"-y",
		"chrome-devtools-mcp@latest",
		"--headless",
		"--isolated",
		"--viewport=1280x720",
		"--no-usage-statistics",
	})
	for _, want := range []string{"--headless", "--isolated", "--no-usage-statistics"} {
		if countString(args, want) != 1 {
			t.Fatalf("hidden MCP args should keep one %q: %#v", want, args)
		}
	}
	if countPrefix(args, "--viewport") != 1 || !containsString(args, "--viewport=1280x720") {
		t.Fatalf("hidden MCP args should preserve configured viewport: %#v", args)
	}
}

func TestHiddenMCPArgsRejectVisibleOrUserProfileLaunchHints(t *testing.T) {
	for _, args := range [][]string{
		{"-y", "chrome-devtools-mcp@latest", "--browserUrl=http://127.0.0.1:9222"},
		{"-y", "chrome-devtools-mcp@latest", "--userDataDir=/Users/dev/Library/Application Support/Google/Chrome"},
	} {
		if hiddenMCPArgsUnsafeFlag(args) == "" {
			t.Fatalf("hidden MCP args should reject visible/user-profile launch hints: %#v", args)
		}
	}
}

func TestShouldUseHiddenBrowserSessionOnlyForAutonomousHidden(t *testing.T) {
	if !shouldUseHiddenBrowserSession(browserModeFields{
		BrowserMode:    "autonomous",
		Presentation:   "hidden",
		SurfaceVisible: false,
	}, nil) {
		t.Fatal("autonomous hidden mode should select hidden browser session")
	}
	if shouldUseHiddenBrowserSession(browserModeFields{
		BrowserMode:    "collaborative",
		Presentation:   "visible",
		SurfaceVisible: true,
	}, nil) {
		t.Fatal("collaborative visible mode should not select hidden browser session")
	}
	if shouldUseHiddenBrowserSession(browserModeFields{
		BrowserMode:    "autonomous",
		Presentation:   "hidden",
		SurfaceVisible: false,
	}, map[string]any{"disable_hidden_browser": true}) {
		t.Fatal("disable_hidden_browser should force visible/direct routing")
	}
}

func TestShouldUseHiddenAutomationToolAllowsOnlySafeFollowups(t *testing.T) {
	metadata := browserModeFields{
		BrowserMode:    "autonomous",
		Presentation:   "hidden",
		SurfaceVisible: false,
	}
	for _, tool := range []string{"browser.open", "browser.navigate", "browser.click", "browser.wait"} {
		if !shouldUseHiddenAutomationTool(tool, metadata, nil) {
			t.Fatalf("%s should route to hidden follow-up session", tool)
		}
	}
	for _, tool := range []string{"browser.screenshot", "browser.type", "browser.select", "browser.close"} {
		if shouldUseHiddenAutomationTool(tool, metadata, nil) {
			t.Fatalf("%s should not route to hidden follow-up session", tool)
		}
	}
	if shouldUseHiddenAutomationTool("browser.navigate", browserModeFields{
		BrowserMode:    "collaborative",
		Presentation:   "visible",
		SurfaceVisible: true,
	}, nil) {
		t.Fatal("collaborative navigate should stay on visible session")
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func countString(values []string, want string) int {
	count := 0
	for _, value := range values {
		if value == want {
			count++
		}
	}
	return count
}

func countPrefix(values []string, prefix string) int {
	count := 0
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			count++
		}
	}
	return count
}
