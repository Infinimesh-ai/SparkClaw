package browserautomation

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
		t.Fatalf("browser.type should map public text arg to Playwright fill value arg: %#v", rawArgs)
	}
	if _, ok := rawArgs["text"]; ok {
		t.Fatalf("browser.type should not forward public text arg to Playwright fill: %#v", rawArgs)
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
		t.Fatalf("browser.type should not forward uid to focused Playwright typing: %#v", rawArgs)
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
		t.Fatalf("browser.type should not forward ref alias to Playwright fill: %#v", rawArgs)
	}
}

func TestMapToolMapsNavigateToPlaywrightDriver(t *testing.T) {
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
		t.Fatalf("browser.navigate should not forward SparkClaw-only reason to Playwright: %#v", rawArgs)
	}
	for _, metadata := range []string{"browser_mode", "presentation", "surface_visible"} {
		if _, ok := rawArgs[metadata]; ok {
			t.Fatalf("browser.navigate should not forward SparkClaw-only %s to Playwright: %#v", metadata, rawArgs)
		}
	}
}

func TestMapToolNormalizesPageIDForFocusAndClose(t *testing.T) {
	rawTool, rawArgs, err := mapTool("browser.focus", map[string]any{"page_id": "1"})
	if err != nil {
		t.Fatal(err)
	}
	if rawTool != "select_page" || rawArgs["pageId"] != 1 || rawArgs["bringToFront"] != true {
		t.Fatalf("browser.focus should map string page_id to numeric Playwright pageId: tool=%q args=%#v", rawTool, rawArgs)
	}
	rawTool, rawArgs, err = mapTool("browser.close", map[string]any{"page_id": "page_2"})
	if err != nil {
		t.Fatal(err)
	}
	if rawTool != "close_page" || rawArgs["pageId"] != 2 {
		t.Fatalf("browser.close should map prefixed page_id to numeric Playwright pageId: tool=%q args=%#v", rawTool, rawArgs)
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
			t.Fatalf("SparkClaw-only %s must not reach Playwright: %#v", key, rawArgs)
		}
	}
}

func TestMapToolForwardsPageIDAndGoalToSnapshot(t *testing.T) {
	rawTool, rawArgs, err := mapTool("browser.snapshot", map[string]any{
		"page_id":          "page_1",
		"interaction_goal": "click Next",
		"reason":           "inspect current page",
		"verbose":          true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rawTool != "take_snapshot" {
		t.Fatalf("browser.snapshot should map to take_snapshot, got %q", rawTool)
	}
	if rawArgs["page_id"] != "page_1" || rawArgs["interaction_goal"] != "click Next" {
		t.Fatalf("browser.snapshot should bind the requested managed page and goal: %#v", rawArgs)
	}
	if _, ok := rawArgs["reason"]; ok {
		t.Fatalf("browser.snapshot should not forward reason to take_snapshot: %#v", rawArgs)
	}
	if rawArgs["verbose"] != true {
		t.Fatalf("browser.snapshot should preserve supported verbose arg: %#v", rawArgs)
	}
}

func TestNormalizeListTabsPreservesPlaywrightPages(t *testing.T) {
	output := normalizeOutput("browser.list_tabs", map[string]any{
		"pages": []any{map[string]any{"page_id": "page_1", "url": "https://example.com", "title": "Example"}},
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
	output := normalizeOutput("browser.list_tabs", map[string]any{"pages": []any{}})
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

func TestEvaluatePayloadMapExtractsFencedDriverJSON(t *testing.T) {
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
		t.Fatal("expected fenced driver payload")
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

func TestBrowserReadEvaluateFunctionEmbedsMaxCharsWithoutExternalArgs(t *testing.T) {
	fn := browserReadEvaluateFunction(120000)
	if !strings.Contains(fn, "const limit = 120000;") {
		t.Fatalf("browser read script should embed max chars limit:\n%s", fn)
	}
	if strings.Contains(fn, "maxChars") {
		t.Fatalf("browser read script should not depend on external args as generic parameters:\n%s", fn)
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

func TestEmbeddedDriverUsesPersistentPlaywrightWithoutCDP(t *testing.T) {
	for _, required := range []string{"require(\"playwright\")", "launchPersistentContext", "page.goto", "locator.click", "locator.fill", "locator.selectOption", "ariaSnapshot"} {
		if !strings.Contains(playwrightDriverScript, required) {
			t.Fatalf("embedded Playwright driver is missing %q", required)
		}
	}
	for _, forbidden := range []string{"connectOverCDP", "remote-debugging-port", "chrome-devtools-mcp"} {
		if strings.Contains(playwrightDriverScript, forbidden) {
			t.Fatalf("embedded Playwright driver must not contain %q", forbidden)
		}
	}
}

func TestResolvePlaywrightRuntimeDirFindsPinnedDependency(t *testing.T) {
	dir, err := resolvePlaywrightRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "node_modules", "playwright", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"version": "1.61.1"`) {
		t.Fatalf("Playwright dependency is not pinned to 1.61.1: %s", raw)
	}
}

func TestResolveChromiumExecutableUsesPlaywrightDefaultWhenUnset(t *testing.T) {
	executable, err := resolveChromiumExecutable("")
	if err != nil {
		t.Fatal(err)
	}
	if executable != "" {
		t.Fatalf("unset custom executable should let Playwright resolve its matching Chromium, got %q", executable)
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

	cfg := config.Default()
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
	selectedID := ""
	for _, raw := range result.Pages {
		entry, ok := raw.(map[string]any)
		if ok && boolValue(entry["selected"]) {
			selectedID = stringValue(entry["page_id"])
		}
	}
	if selectedID == "" {
		t.Fatalf("visible open should return a selected page id: %#v", result.Pages)
	}
	if _, err := adapter.Call(context.Background(), "browser.focus", map[string]any{
		"page_id":            selectedID,
		"browser_mode":       "collaborative",
		"presentation":       "visible",
		"surface_visible":    true,
		"owner_id":           "owner-test",
		"browser_profile_id": "direct-open",
	}); err != nil {
		t.Fatalf("string page_id should map to Playwright pageId: %v", err)
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

func TestRealPlaywrightSnapshotAndLocatorInteractions(t *testing.T) {
	if os.Getenv("SPARKCLAW_RUN_REAL_BROWSER_SCENARIOS") != "1" {
		t.Skip("set SPARKCLAW_RUN_REAL_BROWSER_SCENARIOS=1 to run the real Playwright interaction smoke test")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><html><body>
<label>Name <input aria-label="Name"></label>
<label>Choice <select aria-label="Choice"><option value="A">A</option><option value="B">B</option></select></label>
<button onclick="window.count=(window.count||0)+1; document.querySelector('#result').textContent=document.querySelector('input').value+' / '+document.querySelector('select').value+' / '+window.count">Increment</button>
<div id="result">Waiting</div>
</body></html>`))
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.Adapters.BrowserAutomation.ProfileDir = t.TempDir()
	cfg.Adapters.BrowserAutomation.TimeoutMS = 30000
	adapter := NewAdapter(cfg)
	t.Cleanup(func() { _ = adapter.Close() })
	common := map[string]any{
		"browser_mode":       "autonomous",
		"presentation":       "hidden",
		"surface_visible":    false,
		"owner_id":           "owner-test",
		"browser_profile_id": "locator-actions",
	}
	snapshotArgs := cloneArgs(common)
	snapshotArgs["url"] = server.URL
	if _, err := adapter.Call(context.Background(), "browser.snapshot", snapshotArgs); err != nil {
		t.Fatal(err)
	}
	tabs, err := adapter.Call(context.Background(), "browser.list_tabs", common)
	if err != nil {
		t.Fatal(err)
	}
	pageID := ""
	for _, raw := range tabs.Pages {
		page, ok := raw.(map[string]any)
		if ok && boolValue(page["selected"]) {
			pageID = stringValue(page["page_id"])
		}
	}
	if pageID == "" {
		t.Fatalf("snapshot page was not selected: %#v", tabs.Pages)
	}
	interactionSnapshotArgs := cloneArgs(common)
	interactionSnapshotArgs["page_id"] = pageID
	interactionSnapshotArgs["interaction_goal"] = "Increment the counter"
	interactionSnapshot, err := adapter.Call(context.Background(), "browser.snapshot", interactionSnapshotArgs)
	if err != nil {
		t.Fatal(err)
	}
	interactionOutput, ok := interactionSnapshot.Output.(map[string]any)
	if !ok {
		t.Fatalf("interaction snapshot output is not structured: %#v", interactionSnapshot.Output)
	}
	interactionPayload, ok := interactionOutput["snapshot"].(map[string]any)
	if !ok || stringValue(interactionPayload["schema_version"]) != "browser_interaction_snapshot_v1" || stringValue(interactionPayload["page_id"]) != pageID {
		t.Fatalf("interaction snapshot contract is incomplete: %#v", interactionOutput)
	}
	interactionSnapshotID := stringValue(interactionPayload["snapshot_id"])
	if interactionSnapshotID == "" {
		t.Fatalf("interaction snapshot identity is missing: %#v", interactionPayload)
	}
	nameRef := snapshotRefNamed(interactionSnapshot.Text, "Name")
	choiceRef := snapshotRefNamed(interactionSnapshot.Text, "Choice")
	buttonRef := snapshotRefNamed(interactionSnapshot.Text, "Increment")
	if nameRef == "" || choiceRef == "" || buttonRef == "" {
		t.Fatalf("snapshot did not expose stable refs: %q", interactionSnapshot.Text)
	}
	typeArgs := cloneArgs(common)
	typeArgs["uid"], typeArgs["text"] = nameRef, "Alice"
	if _, err := adapter.Call(context.Background(), "browser.type", typeArgs); err != nil {
		t.Fatal(err)
	}
	selectArgs := cloneArgs(common)
	selectArgs["uid"], selectArgs["value"] = choiceRef, "B"
	if _, err := adapter.Call(context.Background(), "browser.select", selectArgs); err != nil {
		t.Fatal(err)
	}
	clickArgs := cloneArgs(common)
	clickArgs["uid"], clickArgs["page_id"], clickArgs["snapshot_id"] = buttonRef, pageID, interactionSnapshotID
	if _, err := adapter.Call(context.Background(), "browser.click", clickArgs); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Call(context.Background(), "browser.click", clickArgs); err == nil || !strings.Contains(strings.ToLower(err.Error()), "stale") {
		t.Fatalf("a successful click did not invalidate its snapshot ref: %v", err)
	}
	postSnapshotArgs := cloneArgs(interactionSnapshotArgs)
	postSnapshot, err := adapter.Call(context.Background(), "browser.snapshot", postSnapshotArgs)
	if err != nil {
		t.Fatal(err)
	}
	postOutput, ok := postSnapshot.Output.(map[string]any)
	if !ok {
		t.Fatalf("post-click snapshot output is not structured: %#v", postSnapshot.Output)
	}
	postPayload, ok := postOutput["snapshot"].(map[string]any)
	if !ok || stringValue(postPayload["previous_snapshot_id"]) != interactionSnapshotID ||
		stringValue(postPayload["digest"]) == stringValue(interactionPayload["digest"]) || boolValue(postPayload["repeated"]) {
		t.Fatalf("post-click snapshot did not prove a changed state: before=%#v after=%#v", interactionPayload, postPayload)
	}
	waitArgs := cloneArgs(common)
	waitArgs["text"] = "Alice / B / 1"
	if _, err := adapter.Call(context.Background(), "browser.wait", waitArgs); err != nil {
		t.Fatalf("%v\npost-click snapshot:\n%s", err, postSnapshot.Text)
	}
	screenshot, err := adapter.Call(context.Background(), "browser.screenshot", common)
	if err != nil {
		t.Fatal(err)
	}
	output, ok := screenshot.Output.(map[string]any)
	if !ok {
		t.Fatalf("screenshot output should be structured: %#v", screenshot.Output)
	}
	content, ok := output["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("screenshot should return one image part: %#v", output)
	}
	part, ok := content[0].(map[string]any)
	if !ok || stringValue(part["mimeType"]) != "image/png" {
		t.Fatalf("screenshot should return a PNG image part: %#v", content)
	}
	png, err := base64.StdEncoding.DecodeString(stringValue(part["data"]))
	if err != nil || !bytes.HasPrefix(png, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("screenshot payload is not a valid PNG header: err=%v bytes=%x", err, png)
	}
}

func snapshotRefNamed(snapshot, name string) string {
	want := `name="` + name + `"`
	for _, line := range strings.Split(snapshot, "\n") {
		if !strings.Contains(line, want) {
			continue
		}
		for _, field := range strings.Fields(line) {
			if strings.HasPrefix(field, "ref=") {
				return strings.TrimPrefix(field, "ref=")
			}
		}
	}
	return ""
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
