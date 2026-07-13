package browserautomation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
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

func TestMapToolNormalizesPageIDForFocusAndClose(t *testing.T) {
	rawTool, rawArgs, err := mapTool("browser.focus", map[string]any{"page_id": "1"})
	if err != nil {
		t.Fatal(err)
	}
	if rawTool != "select_page" || rawArgs["pageId"] != 1 || rawArgs["bringToFront"] != true {
		t.Fatalf("browser.focus should map string page_id to numeric MCP pageId: tool=%q args=%#v", rawTool, rawArgs)
	}
	rawTool, rawArgs, err = mapTool("browser.close", map[string]any{"page_id": "page_2"})
	if err != nil {
		t.Fatal(err)
	}
	if rawTool != "close_page" || rawArgs["pageId"] != 2 {
		t.Fatalf("browser.close should map prefixed page_id to numeric MCP pageId: tool=%q args=%#v", rawTool, rawArgs)
	}
}

func TestMapToolStripsSparkClawVisibilityHints(t *testing.T) {
	_, rawArgs, err := mapTool("browser.open", map[string]any{
		"url":                    "https://example.com",
		"visible_browser":        true,
		"disable_hidden_browser": true,
		"presentation":           "visible",
		"surface_visible":        true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"visible_browser", "disable_hidden_browser", "presentation", "surface_visible"} {
		if _, ok := rawArgs[key]; ok {
			t.Fatalf("SparkClaw-only %s must not reach MCP: %#v", key, rawArgs)
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

func TestNormalizeBrowserOpenExtractsPagesFromMCPContentText(t *testing.T) {
	output := normalizeOutput("browser.open", map[string]any{
		"content": []any{map[string]any{
			"type": "text",
			"text": "## Pages\n2: Example (https://example.com/) [selected]",
		}},
	})
	result, ok := output.(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %#v", output)
	}
	pages, ok := result["pages"].([]any)
	if !ok || len(pages) != 1 {
		t.Fatalf("browser.open should expose normalized pages: %#v", result)
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

func TestBrowserReadEvaluateFunctionUsesVisibleAuthControlsAndAuthenticatedEvidence(t *testing.T) {
	fn := browserReadEvaluateFunction(120000)
	for _, want := range []string{
		"excludedTextTags",
		"visibleTextFor",
		"credentialHasLoginContext",
		"visibleLogoutControl",
		"visibleAccountControl",
		"usableApplicationShell",
		"authState",
		"authConfidence",
		"authSignals",
		`authChallengeDetected = authState === "challenged"`,
	} {
		if !strings.Contains(fn, want) {
			t.Fatalf("browser auth evaluation missing %q:\n%s", want, fn)
		}
	}
	for _, excluded := range []string{"document.body.innerText", "qq.com", "xmail_uin"} {
		if strings.Contains(strings.ToLower(fn), excluded) {
			t.Fatalf("browser auth evaluation must remain domain-neutral and exclude raw body/style text %q:\n%s", excluded, fn)
		}
	}
}

func TestFirstStringSliceValue(t *testing.T) {
	got := firstStringSliceValue(nil, []any{"usable_application_shell", "", 7})
	if len(got) != 2 || got[0] != "usable_application_shell" || got[1] != "7" {
		t.Fatalf("unexpected string slice conversion: %#v", got)
	}
}

func TestSharedProfileMCPArgsAppendHiddenFlags(t *testing.T) {
	args := sharedProfileMCPArgs([]string{"-y", "chrome-devtools-mcp@latest"}, true, "/opt/chromium", "/tmp/profile", "")
	for _, want := range []string{"--headless", "--viewport=" + hiddenBrowserViewport, "--no-usage-statistics", "--executablePath=/opt/chromium", "--userDataDir=/tmp/profile"} {
		if !containsString(args, want) {
			t.Fatalf("shared profile MCP args missing %q: %#v", want, args)
		}
	}
	if containsString(args, "--isolated") {
		t.Fatalf("shared profile must not use isolated browser state: %#v", args)
	}
}

func TestSharedProfileMCPArgsPreserveConfiguredViewportAndUsageFlag(t *testing.T) {
	args := sharedProfileMCPArgs([]string{
		"-y",
		"chrome-devtools-mcp@latest",
		"--viewport=1280x720",
		"--no-usage-statistics",
	}, true, "/opt/chromium", "/tmp/profile", "")
	for _, want := range []string{"--headless", "--no-usage-statistics"} {
		if countString(args, want) != 1 {
			t.Fatalf("shared profile MCP args should keep one %q: %#v", want, args)
		}
	}
	if countPrefix(args, "--viewport") != 1 || !containsString(args, "--viewport=1280x720") {
		t.Fatalf("hidden MCP args should preserve configured viewport: %#v", args)
	}
}

func TestSharedProfileMCPArgsUseSameChromiumAndProfile(t *testing.T) {
	base := []string{"-y", "chrome-devtools-mcp@latest"}
	executable := "/opt/chromium/chromium"
	profileDir := "/var/lib/sparkclaw/browser-profile"
	visible := sharedProfileMCPArgs(base, false, executable, profileDir, "")
	hidden := sharedProfileMCPArgs(base, true, executable, profileDir, "")
	for _, args := range [][]string{visible, hidden} {
		if !containsString(args, "--executablePath="+executable) || !containsString(args, "--userDataDir="+profileDir) {
			t.Fatalf("shared profile args missing Chromium/profile: %#v", args)
		}
		if containsString(args, "--isolated") {
			t.Fatalf("shared profile must not be isolated: %#v", args)
		}
	}
	if containsString(visible, "--headless") {
		t.Fatalf("visible verification must not be headless: %#v", visible)
	}
	if !containsString(hidden, "--headless") || !containsString(hidden, "--viewport="+hiddenBrowserViewport) {
		t.Fatalf("hidden shared profile must be headless with stable viewport: %#v", hidden)
	}
}

func TestSharedProfileMCPArgsRejectCallerOwnedBrowserState(t *testing.T) {
	for _, args := range [][]string{
		{"--isolated"},
		{"--headless"},
		{"--userDataDir=/tmp/user"},
		{"--executablePath=/tmp/chrome"},
		{"--browserUrl=http://127.0.0.1:9222"},
		{"--autoConnect"},
		{"--chromeArg=https://example.com"},
	} {
		if sharedProfileMCPArgsUnsafeFlag(args) == "" {
			t.Fatalf("shared profile args should reject caller-owned launch state: %#v", args)
		}
	}
}

func TestSharedProfileMCPArgsLaunchKnownTargetWithoutAboutBlank(t *testing.T) {
	args := sharedProfileMCPArgs(
		[]string{"-y", "chrome-devtools-mcp@latest"},
		false,
		"/opt/chromium",
		"/tmp/profile",
		"https://example.com/target",
	)
	if !containsString(args, "--chromeArg=https://example.com/target") {
		t.Fatalf("fresh browser session should launch the target URL directly: %#v", args)
	}
}

func TestMCPPageEntriesRecognizeSelectedAndBlankPages(t *testing.T) {
	output := map[string]any{"content": []any{map[string]any{
		"type": "text",
		"text": "## Pages\n1: about:blank\n2: Example (https://example.com/) [selected]",
	}}}
	entries := mcpPageEntries(output)
	if len(entries) != 2 || entries[0].ID != 1 || entries[0].URL != "about:blank" || entries[1].ID != 2 || !entries[1].Selected {
		t.Fatalf("unexpected MCP page entries: %#v", entries)
	}
}

func TestRealVisibleBrowserOpenReusesStartupPage(t *testing.T) {
	if os.Getenv("SPARKCLAW_RUN_REAL_BROWSER_SCENARIOS") != "1" {
		t.Skip("set SPARKCLAW_RUN_REAL_BROWSER_SCENARIOS=1 to run the real visible Chromium smoke test")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/authenticated" {
			_, _ = w.Write([]byte("<!doctype html><title>Authenticated Portal</title><nav>退出登录</nav><main>电子资源导航 软件正版化（激活需登录SSLVPN）</main>"))
			return
		}
		_, _ = w.Write([]byte("<!doctype html><title>Direct Target</title><main>DIRECT_VISIBLE_TARGET</main>"))
	}))
	defer server.Close()

	executable, err := resolveChromiumExecutable("")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Adapters.BrowserAutomation.ChromiumExecutable = executable
	cfg.Adapters.BrowserAutomation.ProfileDir = t.TempDir()
	cfg.Adapters.BrowserAutomation.TimeoutMS = 30000
	adapter := NewAdapter(cfg)
	t.Cleanup(func() { _ = adapter.Close() })

	result, err := adapter.Call(context.Background(), "browser.open", map[string]any{
		"url":                server.URL,
		"browser_mode":       "collaborative",
		"presentation":       "visible",
		"surface_visible":    true,
		"visible_browser":    true,
		"owner_id":           "owner-test",
		"browser_profile_id": "direct-open",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(result.Text), "about:blank") || !strings.Contains(result.Text, server.URL) {
		t.Fatalf("visible open should reuse the startup page for the target: %q", result.Text)
	}
	entries := mcpPageEntries(result.Output)
	selectedID := ""
	for _, entry := range entries {
		if entry.Selected {
			selectedID = stringValue(entry.ID)
		}
	}
	if selectedID == "" {
		t.Fatalf("visible open should return a selected page id: %#v", entries)
	}
	if _, err := adapter.Call(context.Background(), "browser.focus", map[string]any{
		"page_id":            selectedID,
		"browser_mode":       "collaborative",
		"presentation":       "visible",
		"surface_visible":    true,
		"owner_id":           "owner-test",
		"browser_profile_id": "direct-open",
	}); err != nil {
		t.Fatalf("string page_id should map to MCP pageId: %v", err)
	}
	page, err := adapter.ReadPage(context.Background(), server.URL+"/authenticated", map[string]any{
		"browser_mode":       "autonomous",
		"presentation":       "hidden",
		"surface_visible":    false,
		"owner_id":           "owner-test",
		"browser_profile_id": "direct-open",
		"timeout_ms":         30000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.AuthChallengeDetected {
		t.Fatalf("authenticated page text must not be classified as a login wall: title=%q text=%q", page.Title, page.Text)
	}
}

func TestResolveSharedProfileDirSeparatesLogicalProfiles(t *testing.T) {
	root := t.TempDir()
	work, err := resolveSharedProfileDir(root, "owner-a\x00work")
	if err != nil {
		t.Fatal(err)
	}
	personal, err := resolveSharedProfileDir(root, "owner-a\x00personal")
	if err != nil {
		t.Fatal(err)
	}
	otherOwner, err := resolveSharedProfileDir(root, "owner-b\x00work")
	if err != nil {
		t.Fatal(err)
	}
	if work == personal || work == otherOwner || personal == otherOwner {
		t.Fatalf("owners and logical browser profiles must use separate directories: work=%q personal=%q other=%q", work, personal, otherOwner)
	}
	for _, path := range []string{work, personal, otherOwner} {
		if !strings.HasPrefix(path, root+string(os.PathSeparator)) {
			t.Fatalf("profile escaped configured root: root=%q path=%q", root, path)
		}
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			t.Fatalf("profile directory was not created: path=%q info=%#v err=%v", path, info, err)
		}
	}
}

func TestShouldUseHiddenBrowserSessionForAnyHiddenPresentation(t *testing.T) {
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
	if !shouldUseHiddenBrowserSession(browserModeFields{
		BrowserMode:    "collaborative",
		Presentation:   "hidden",
		SurfaceVisible: false,
	}, nil) {
		t.Fatal("ordinary collaborative mode should stay on the hidden browser session")
	}
	if shouldUseHiddenBrowserSession(browserModeFields{
		BrowserMode:    "autonomous",
		Presentation:   "hidden",
		SurfaceVisible: false,
	}, map[string]any{"disable_hidden_browser": true}) {
		t.Fatal("disable_hidden_browser should force visible/direct routing")
	}
}

func TestShouldUseHiddenAutomationToolFollowsPresentation(t *testing.T) {
	metadata := browserModeFields{
		BrowserMode:    "autonomous",
		Presentation:   "hidden",
		SurfaceVisible: false,
	}
	for _, tool := range []string{"browser.open", "browser.navigate", "browser.click", "browser.wait", "browser.screenshot", "browser.type", "browser.select", "browser.close", "browser.list_tabs"} {
		if !shouldUseHiddenAutomationTool(tool, metadata, nil) {
			t.Fatalf("%s should route to the hidden session", tool)
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
