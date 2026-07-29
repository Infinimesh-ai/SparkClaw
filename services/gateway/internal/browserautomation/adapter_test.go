package browserautomation

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

func TestAgentBrowserTabIDMapsPublicPageID(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{{"1", "t1"}, {"page_2", "t2"}} {
		got, err := agentBrowserTabID(map[string]any{"page_id": test.input})
		if err != nil || got != test.want {
			t.Fatalf("page id %q: got %q, err=%v", test.input, got, err)
		}
	}
	if _, err := agentBrowserTabID(map[string]any{"page_id": "main"}); err == nil {
		t.Fatal("non-numeric page ids must be rejected")
	}
}

func TestNormalizeAgentBrowserTabsPreservesActiveTab(t *testing.T) {
	pages := normalizeAgentBrowserTabs(map[string]any{"tabs": []any{
		map[string]any{"tabId": "t1", "active": false, "url": "about:blank", "title": ""},
		map[string]any{"tabId": "t2", "active": true, "url": "https://example.com", "title": "Example"},
	}})
	if len(pages) != 2 {
		t.Fatalf("expected two pages, got %#v", pages)
	}
	active := mapValue(pages[1])
	if firstStringValue(active, "page_id") != "page_2" || !boolValue(active["selected"]) {
		t.Fatalf("active agent-browser tab was not normalized: %#v", active)
	}
}

func TestAgentBrowserOpenReusesOnlySoleBlankPage(t *testing.T) {
	for _, test := range []struct {
		name  string
		pages []any
		want  bool
	}{
		{name: "sole blank page", pages: []any{map[string]any{"url": "about:blank"}}, want: true},
		{name: "existing target page", pages: []any{map[string]any{"url": "https://wx.mail.qq.com/home/index#/list/4"}}},
		{name: "blank plus existing page", pages: []any{map[string]any{"url": "about:blank"}, map[string]any{"url": "https://example.com/"}}},
		{name: "no pages", pages: nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := canReuseAgentBrowserBlankPage(test.pages); got != test.want {
				t.Fatalf("unexpected blank-page reuse decision: got=%t want=%t pages=%#v", got, test.want, test.pages)
			}
		})
	}
}

func TestAgentBrowserObservedTabsAreClonedAndConsumedOnce(t *testing.T) {
	adapter := &AgentBrowserAdapter{}
	pages := []any{map[string]any{"page_id": "page_1", "url": "about:blank", "selected": true}}
	adapter.rememberObservedTabsLocked(pages)
	mapValue(pages[0])["url"] = "https://unrelated.example/"

	observed, ok := adapter.takeObservedTabsLocked()
	if !ok || !canReuseAgentBrowserBlankPage(observed) {
		t.Fatalf("verified blank-tab observation was not isolated from caller mutation: %#v", observed)
	}
	if second, ok := adapter.takeObservedTabsLocked(); ok || second != nil {
		t.Fatalf("tab observation was reusable more than once: %#v ok=%t", second, ok)
	}
}

func TestFreshVisibleSessionOpensTargetBeforeTabDiscovery(t *testing.T) {
	adapter := &AgentBrowserAdapter{freshSession: true, activePresentation: "visible"}
	if !adapter.shouldOpenFreshVisibleSessionDirect(true) {
		t.Fatal("fresh visible browser.open should navigate directly to its target")
	}
	adapter.activePresentation = "hidden"
	if adapter.shouldOpenFreshVisibleSessionDirect(true) {
		t.Fatal("hidden sessions must keep the existing automation path")
	}
	adapter.activePresentation = "visible"
	adapter.freshSession = false
	if adapter.shouldOpenFreshVisibleSessionDirect(true) {
		t.Fatal("an established visible session must inspect existing tabs before opening another")
	}
}

func TestRebaseFreshVisibleURLFragmentUsesRedirectedSessionURL(t *testing.T) {
	target := "https://wx.mail.qq.com/home/index?sid=stale#/list/4"
	current := "https://wx.mail.qq.com/home/index?sid=fresh#/list/1/1"
	got, ok := rebaseFreshVisibleURLFragment(target, current)
	if !ok || got != "https://wx.mail.qq.com/home/index?sid=fresh#/list/4" {
		t.Fatalf("unexpected rebased URL: got=%q ok=%t", got, ok)
	}

	for _, test := range []struct {
		name    string
		target  string
		current string
	}{
		{
			name:    "already at target fragment",
			target:  "https://example.com/app#/drafts",
			current: "https://example.com/app?session=fresh#/drafts",
		},
		{
			name:    "cross origin redirect",
			target:  "https://example.com/app#/drafts",
			current: "https://login.example.net/app#/inbox",
		},
		{
			name:    "target without fragment",
			target:  "https://example.com/drafts",
			current: "https://example.com/inbox",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if rebound, ok := rebaseFreshVisibleURLFragment(test.target, test.current); ok || rebound != "" {
				t.Fatalf("unexpected rebase: got=%q ok=%t", rebound, ok)
			}
		})
	}
}

func TestDecodeAgentBrowserToolResultUsesStructuredResponse(t *testing.T) {
	result, err := decodeAgentBrowserToolResult("agent_browser_get_url", map[string]any{
		"content": []any{map[string]any{"type": "text", "text": "https://example.com"}},
		"structuredContent": map[string]any{"response": map[string]any{
			"success": true,
			"data":    map[string]any{"url": "https://example.com"},
		}},
	})
	if err != nil || firstStringValue(mapValue(result.Data), "url") != "https://example.com" {
		t.Fatalf("unexpected MCP result: %#v err=%v", result, err)
	}
	if _, err := decodeAgentBrowserToolResult("agent_browser_click", map[string]any{
		"structuredContent": map[string]any{"response": map[string]any{"success": false, "error": "missing ref"}},
	}); err == nil || !isAgentBrowserActionError(err) {
		t.Fatalf("business failure should remain an action error: %v", err)
	}
}

func TestAgentBrowserSnapshotRankingAndWrappedRefs(t *testing.T) {
	refs := map[string]any{
		"e1": map[string]any{"role": "link", "name": "Inbox"},
		"e2": map[string]any{"role": "link", "name": "Drafts"},
		"e3": map[string]any{"role": "button", "name": "Compose"},
	}
	ranked := buildAgentBrowserSnapshotRefs(refs, "Open Drafts")
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Score > ranked[j].Score })
	if ranked[0].RawRef != "e2" {
		t.Fatalf("interaction goal should rank Drafts first: %#v", ranked)
	}
	ranked[0].ExternalRef = "snapshot_1_1:e2:abc"
	projection := projectAgentBrowserTreeRefs(ranked[:1])
	if !strings.Contains(projection, "[ref=snapshot_1_1:e2:abc]") || strings.Contains(projection, "ref=e1") {
		t.Fatalf("only wrapped, returned refs should remain executable: %q", projection)
	}
}

func TestQQMailSnapshotPreservesUTF8ChineseEvidence(t *testing.T) {
	source := map[string]any{
		"text": "QQ邮箱 收件箱 草稿箱 已发送 垃圾箱 联系人 邮件正文 中文主题 安全退出",
		"refs": map[string]any{
			"e1": map[string]any{"role": "navigation", "name": "邮箱导航"},
			"e2": map[string]any{"role": "link", "name": "收件箱"},
			"e3": map[string]any{"role": "link", "name": "草稿箱"},
			"e4": map[string]any{"role": "button", "name": "安全退出"},
		},
	}
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	text := firstStringValue(decoded, "text")
	refs := mapValue(decoded["refs"])
	descriptors := buildAgentBrowserSnapshotRefs(refs, "打开草稿箱")
	projection := projectAgentBrowserTreeRefs(descriptors)
	if !utf8.ValidString(text) || !utf8.ValidString(projection) ||
		strings.ContainsRune(text+projection, utf8.RuneError) {
		t.Fatalf("QQ Mail snapshot contains invalid UTF-8: text=%q projection=%q", text, projection)
	}
	for _, want := range []string{"收件箱", "草稿箱", "安全退出"} {
		if !strings.Contains(text+projection, want) {
			t.Fatalf("QQ Mail snapshot lost Chinese label %q: text=%q projection=%q", want, text, projection)
		}
	}
	metadata := inferAgentBrowserSnapshotAuth(map[string]any{"text": text}, "QQ邮箱", "https://wx.mail.qq.com/home/index#/list/1/1", descriptors)
	if firstStringValue(metadata, "authState") != "authenticated" ||
		!containsString(firstStringSliceValue(metadata["authSignals"]), "visible_sign_out_control") {
		t.Fatalf("Chinese authenticated mailbox evidence was not recognized: %#v", metadata)
	}
}

func TestAgentBrowserFullSnapshotEnrichesGenericClickableFromDescendantText(t *testing.T) {
	refs := map[string]any{"e14": map[string]any{"role": "generic"}}
	tree := `- generic
  - generic [ref=e14] clickable [cursor:pointer, onclick]
    - image
    - StaticText "Drafts"`
	enrichAgentBrowserRefsFromTree(refs, tree)
	values := mapValue(refs["e14"])
	if firstStringValue(values, "name") != "Drafts" || !boolValue(values["clickable"]) {
		t.Fatalf("native generic clickable was not enriched from its interaction-tree label: %#v", values)
	}
	descriptors := buildAgentBrowserSnapshotRefs(refs, "Open Drafts")
	if len(descriptors) != 1 || descriptors[0].RawRef != "e14" || descriptors[0].Name != "Drafts" || descriptors[0].Score < 200 {
		t.Fatalf("enriched native ref was not executable and goal-relevant: %#v", descriptors)
	}
}

func TestAgentBrowserDescendantTextDoesNotBorrowNestedControlLabel(t *testing.T) {
	refs := map[string]any{
		"e1": map[string]any{"role": "generic"},
		"e2": map[string]any{"role": "button", "name": "Delete"},
	}
	tree := `- generic [ref=e1] clickable
  - button "Delete" [ref=e2]
    - StaticText "Delete"
- StaticText "Outside"`
	enrichAgentBrowserRefsFromTree(refs, tree)
	if name := firstStringValue(mapValue(refs["e1"]), "name"); name != "" {
		t.Fatalf("parent clickable borrowed nested control label %q", name)
	}
}

func TestAgentBrowserSnapshotUsesFullCompactNativeTree(t *testing.T) {
	args := agentBrowserSnapshotRawArgs()
	if boolValue(args["interactive"]) || !boolValue(args["compact"]) || !boolValue(args["includeUrls"]) {
		t.Fatalf("snapshot acquisition must retain generic clickable refs using the full compact native tree: %#v", args)
	}
}

func TestAgentBrowserSnapshotControlKeepsExecutionIdentityCompact(t *testing.T) {
	descriptor := &agentBrowserSnapshotRef{
		ExternalRef: "snapshot_1_1:e14:eaff13c7b5515f03",
		Fingerprint: "eaff13c7b5515f0359bada71f37fb55ed44f1d5a71a35a67c7ae10903c839ab6",
		Role:        "generic",
		Name:        "Drafts",
		Clickable:   true,
		Ordinal:     1,
	}
	control := agentBrowserSnapshotControl(descriptor)
	if firstStringValue(control, "ref") != descriptor.ExternalRef || firstStringValue(control, "fingerprint") != descriptor.Fingerprint[:16] {
		t.Fatalf("compact control lost its current-snapshot identity: %#v", control)
	}
	for _, redundant := range []string{"uid", "clickable", "ordinal"} {
		if _, exists := control[redundant]; exists {
			t.Fatalf("compact control repeated private execution field %q: %#v", redundant, control)
		}
	}
}

func TestAgentBrowserSnapshotFingerprintSurvivesRawRefRenumbering(t *testing.T) {
	before := buildAgentBrowserSnapshotRefs(map[string]any{
		"e1": map[string]any{"role": "link", "name": "Inbox"},
		"e2": map[string]any{"role": "link", "name": "Drafts"},
	}, "")
	after := buildAgentBrowserSnapshotRefs(map[string]any{
		"e7": map[string]any{"role": "link", "name": "Inbox"},
		"e8": map[string]any{"role": "link", "name": "Drafts"},
	}, "")
	if before[1].RawRef == after[1].RawRef || before[1].Fingerprint != after[1].Fingerprint {
		t.Fatalf("semantic fingerprint should survive agent-browser ref renumbering: before=%#v after=%#v", before[1], after[1])
	}
}

func TestInferAgentBrowserSnapshotAuthUsesGenericInteractionTreeEvidence(t *testing.T) {
	metadata := inferAgentBrowserSnapshotAuth(map[string]any{
		"authState":      "unknown",
		"authConfidence": "insufficient",
	}, "Account access", "https://portal.example.test/login", []*agentBrowserSnapshotRef{
		{Role: "tab", Name: "Email sign in"},
		{Role: "iframe", Name: "Single sign-on login"},
	})
	if firstStringValue(metadata, "authState") != "challenged" ||
		firstStringValue(metadata, "authConfidence") != "accessibility_tree" ||
		!boolValue(metadata["authChallengeDetected"]) {
		t.Fatalf("generic login gate was not inferred from snapshot evidence: %#v", metadata)
	}
	signals := firstStringSliceValue(metadata["authSignals"])
	for _, want := range []string{"snapshot_auth_route", "snapshot_auth_controls", "snapshot_auth_frame"} {
		if !containsString(signals, want) {
			t.Fatalf("snapshot auth inference missing %q: %#v", want, metadata)
		}
	}
}

func TestInferAgentBrowserSnapshotAuthDoesNotTreatDocumentationAsLoginGate(t *testing.T) {
	metadata := inferAgentBrowserSnapshotAuth(map[string]any{
		"authState":      "unknown",
		"authConfidence": "insufficient",
	}, "Login API documentation", "https://docs.example.test/authentication", []*agentBrowserSnapshotRef{
		{Role: "link", Name: "Sign in example"},
		{Role: "link", Name: "Log out example"},
	})
	if firstStringValue(metadata, "authState") != "unknown" || boolValue(metadata["authChallengeDetected"]) {
		t.Fatalf("documentation page must remain inconclusive: %#v", metadata)
	}
}

func TestInferAgentBrowserSnapshotAuthRecognizesApplicationContinuity(t *testing.T) {
	metadata := inferAgentBrowserSnapshotAuth(map[string]any{
		"text": "Authenticated application workspace with navigation, account settings, current records, messages, drafts, and sent items.",
	}, "Mailbox", "https://mail.example.test/app", []*agentBrowserSnapshotRef{
		{Role: "button", Name: "owner@example.test", Clickable: true},
		{Role: "navigation", Name: "Mailbox navigation"},
		{Role: "link", Name: "Inbox", Clickable: true},
		{Role: "link", Name: "Drafts", Clickable: true},
		{Role: "link", Name: "Sent", Clickable: true},
	})
	if firstStringValue(metadata, "authState") != "authenticated" ||
		firstStringValue(metadata, "authConfidence") != "application_continuity" ||
		!containsString(firstStringSliceValue(metadata["authSignals"]), "visible_identity_control") {
		t.Fatalf("native accessibility evidence did not preserve authenticated continuity: %#v", metadata)
	}
}

func TestLimitAgentBrowserPageText(t *testing.T) {
	got, truncated := limitAgentBrowserPageText("页面正文 ABC", 4)
	if got != "页面正文" || !truncated {
		t.Fatalf("native page text limit should count runes: got=%q truncated=%t", got, truncated)
	}
	got, truncated = limitAgentBrowserPageText("short", 120)
	if got != "short" || truncated {
		t.Fatalf("short native page text changed: got=%q truncated=%t", got, truncated)
	}
}

func TestFirstStringSliceValue(t *testing.T) {
	got := firstStringSliceValue(nil, []any{"usable_application_shell", "", 7})
	if len(got) != 2 || got[0] != "usable_application_shell" || got[1] != "7" {
		t.Fatalf("unexpected string slice conversion: %#v", got)
	}
}

func TestResolveChromiumExecutableUsesSystemChromium(t *testing.T) {
	temp := t.TempDir()
	chromium := filepath.Join(temp, "chromium")
	if err := os.WriteFile(chromium, []byte("stub"), 0o700); err != nil {
		t.Fatal(err)
	}
	executable, err := resolveChromiumExecutableFromCandidates([]string{filepath.Join(temp, "missing"), chromium})
	if err != nil {
		t.Fatal(err)
	}
	if executable != chromium {
		t.Fatalf("expected system Chromium %q, got %q", chromium, executable)
	}
}

func TestRealBrowserExecutableIsChromium(t *testing.T) {
	if os.Getenv("SPARKCLAW_RUN_REAL_BROWSER_SCENARIOS") != "1" {
		t.Skip("set SPARKCLAW_RUN_REAL_BROWSER_SCENARIOS=1 to validate the real Chromium executable")
	}
	executable, err := resolveChromiumExecutable("")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, executable, "--version").CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("read Chromium version timed out for %q", executable)
	}
	if err != nil {
		t.Fatalf("read Chromium version: %v: %s", err, output)
	}
	if version := strings.TrimSpace(string(output)); !strings.HasPrefix(version, "Chromium ") {
		t.Fatalf("browser tests require Chromium, got %q from %q", version, executable)
	}
}

func TestHiddenTabLifecycleDoesNotProbeOrReopenAfterClose(t *testing.T) {
	for _, tool := range []string{"browser.close", "browser.list_tabs"} {
		if shouldAttachHiddenPageState(tool, true) {
			t.Fatalf("%s must not probe page_state because that can create a replacement blank tab", tool)
		}
	}
	if !shouldAttachHiddenPageState("browser.snapshot", true) {
		t.Fatal("ordinary hidden browser actions should retain page-state metadata")
	}
	if shouldAttachHiddenPageState("browser.snapshot", false) {
		t.Fatal("visible Chromium actions should not attach hidden page state")
	}
}

func TestRealVisibleBrowserOpenReusesStartupPage(t *testing.T) {
	if os.Getenv("SPARKCLAW_RUN_REAL_BROWSER_SCENARIOS") != "1" {
		t.Skip("set SPARKCLAW_RUN_REAL_BROWSER_SCENARIOS=1 to run the real visible Chromium smoke test")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/authenticated" {
			_, _ = w.Write([]byte(`<!doctype html><title>Authenticated Portal</title>
<header><button>owner@example.com</button></header>
<nav><a href="#inbox">Inbox</a><a href="#drafts">Drafts</a><a href="#sent">Sent</a></nav>
<main>Authenticated application workspace with current records, account settings, navigation, and resource labels including software activation guidance.</main>`))
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

	visibleArgs := map[string]any{
		"url":                server.URL,
		"browser_mode":       "collaborative",
		"presentation":       "visible",
		"surface_visible":    true,
		"visible_browser":    true,
		"owner_id":           "owner-test",
		"browser_profile_id": "direct-open",
	}
	health, err := adapter.Health(context.Background(), visibleArgs)
	if err != nil {
		t.Fatal(err)
	}
	healthOutput := mapValue(health.Output)
	if health.Provider != "agent-browser-visible" || health.Presentation != "visible" || !health.SurfaceVisible ||
		!boolValue(healthOutput["ok"]) || firstStringValue(healthOutput, "status") != "ok" {
		t.Fatalf("visible browser health did not preserve execution presentation: %#v", health)
	}
	result, err := adapter.Call(context.Background(), "browser.open", visibleArgs)
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
		t.Fatalf("string page_id should map to an agent-browser tab: %v", err)
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
	if page.AuthState != "authenticated" || page.AuthConfidence != "application_continuity" {
		t.Fatalf("authenticated application identity was not recognized: state=%q confidence=%q signals=%#v", page.AuthState, page.AuthConfidence, page.AuthSignals)
	}
	closeAllChromiumTabs(t, adapter, map[string]any{
		"browser_mode":       "autonomous",
		"presentation":       "hidden",
		"surface_visible":    false,
		"owner_id":           "owner-test",
		"browser_profile_id": "direct-open",
	})
}

func TestRealChromiumSnapshotRecognizesGenericLoginGate(t *testing.T) {
	if os.Getenv("SPARKCLAW_RUN_REAL_BROWSER_SCENARIOS") != "1" {
		t.Skip("set SPARKCLAW_RUN_REAL_BROWSER_SCENARIOS=1 to validate snapshot login-gate normalization")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>Account access</title></head><body>
<main><button>Email sign in</button><button>Single sign-on login</button></main>
</body></html>`))
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.Adapters.BrowserAutomation.ProfileDir = t.TempDir()
	cfg.Adapters.BrowserAutomation.TimeoutMS = 30000
	adapter := NewAdapter(cfg)
	t.Cleanup(func() { _ = adapter.Close() })
	args := map[string]any{
		"url":                server.URL + "/login",
		"browser_mode":       "autonomous",
		"presentation":       "hidden",
		"surface_visible":    false,
		"owner_id":           "owner-test",
		"browser_profile_id": "login-gate",
	}
	result, err := adapter.Call(context.Background(), "browser.snapshot", args)
	if err != nil {
		t.Fatal(err)
	}
	output := mapValue(result.Output)
	if firstStringValue(output, "browser_page_auth_state") != "challenged" ||
		firstStringValue(output, "browser_page_auth_confidence") != "accessibility_tree" ||
		!boolValue(output["auth_challenge_detected"]) {
		t.Fatalf("real agent-browser snapshot did not normalize the login gate: %#v", output)
	}
	closeSelectedChromiumTab(t, adapter, args)
}

func TestRealChromiumSnapshotAndLocatorInteractions(t *testing.T) {
	if os.Getenv("SPARKCLAW_RUN_REAL_BROWSER_SCENARIOS") != "1" {
		t.Skip("set SPARKCLAW_RUN_REAL_BROWSER_SCENARIOS=1 to run the real agent-browser interaction smoke test")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><html><body>
<header><div style="cursor:pointer">owner@example.com</div></header>
<nav><div style="cursor:pointer" onclick="location.hash='drafts'"><span>Drafts</span></div></nav>
<main>
<label>Name <input aria-label="Name"></label>
<label>Choice <select aria-label="Choice"><option value="A">A</option><option value="B">B</option></select></label>
<button onclick="window.count=(window.count||0)+1; document.querySelector('#result').textContent=document.querySelector('input').value+' / '+document.querySelector('select').value+' / '+window.count">Increment</button>
<div id="result">Waiting</div>
<p>Authenticated application workspace with navigation, account identity, settings, and current records.</p>
</main>
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
	interactionSnapshotArgs["interaction_goal"] = "Open Drafts"
	interactionSnapshot, err := adapter.Call(context.Background(), "browser.snapshot", interactionSnapshotArgs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(interactionSnapshot.Text, "ref=e") {
		t.Fatalf("raw agent-browser refs crossed the adapter boundary: %q", interactionSnapshot.Text)
	}
	if strings.Contains(interactionSnapshot.Text, "Interactive refs:") {
		t.Fatalf("snapshot text duplicated the structured control projection: %q", interactionSnapshot.Text)
	}
	interactionOutput, ok := interactionSnapshot.Output.(map[string]any)
	if !ok {
		t.Fatalf("interaction snapshot output is not structured: %#v", interactionSnapshot.Output)
	}
	interactionPayload, ok := interactionOutput["snapshot"].(map[string]any)
	if !ok || stringValue(interactionPayload["schema_version"]) != "browser_interaction_snapshot_v1" || stringValue(interactionPayload["page_id"]) != pageID {
		t.Fatalf("interaction snapshot contract is incomplete: %#v", interactionOutput)
	}
	if stringValue(interactionOutput["browser_page_auth_state"]) != "authenticated" ||
		stringValue(interactionOutput["browser_page_auth_confidence"]) != "application_continuity" {
		t.Fatalf("authenticated application shell was not recognized: %#v", interactionOutput)
	}
	interactionSnapshotID := stringValue(interactionPayload["snapshot_id"])
	if interactionSnapshotID == "" {
		t.Fatalf("interaction snapshot identity is missing: %#v", interactionPayload)
	}
	draftsRef := snapshotRefNamed(interactionSnapshot.Text, "Drafts")
	if draftsRef == "" {
		t.Fatalf("snapshot did not expose a stable pointer-derived Drafts ref: %q", interactionSnapshot.Text)
	}
	draftsClickArgs := cloneArgs(common)
	draftsClickArgs["uid"], draftsClickArgs["page_id"], draftsClickArgs["snapshot_id"] = draftsRef, pageID, interactionSnapshotID
	draftsClick, err := adapter.Call(context.Background(), "browser.click", draftsClickArgs)
	if err != nil {
		t.Fatal(err)
	}
	draftsClickOutput, ok := draftsClick.Output.(map[string]any)
	if !ok || !strings.HasSuffix(stringValue(draftsClickOutput["url"]), "#drafts") {
		t.Fatalf("pointer-derived Drafts ref clicked the wrong target: %#v", draftsClick.Output)
	}

	actionSnapshotArgs := cloneArgs(interactionSnapshotArgs)
	actionSnapshotArgs["interaction_goal"] = "Increment the counter"
	actionSnapshot, err := adapter.Call(context.Background(), "browser.snapshot", actionSnapshotArgs)
	if err != nil {
		t.Fatal(err)
	}
	actionOutput, ok := actionSnapshot.Output.(map[string]any)
	if !ok {
		t.Fatalf("post-Drafts snapshot output is not structured: %#v", actionSnapshot.Output)
	}
	actionPayload, ok := actionOutput["snapshot"].(map[string]any)
	if !ok || stringValue(actionPayload["previous_snapshot_id"]) != interactionSnapshotID {
		t.Fatalf("post-Drafts snapshot did not follow the pointer click: before=%#v after=%#v", interactionPayload, actionPayload)
	}
	actionSnapshotID := stringValue(actionPayload["snapshot_id"])
	nameRef := snapshotRefNamed(actionSnapshot.Text, "Name")
	if actionSnapshotID == "" || nameRef == "" {
		t.Fatalf("post-Drafts snapshot did not expose stable action refs: %q", actionSnapshot.Text)
	}
	typeArgs := cloneArgs(common)
	typeArgs["uid"], typeArgs["text"] = nameRef, "Alice"
	if _, err := adapter.Call(context.Background(), "browser.type", typeArgs); err != nil {
		t.Fatal(err)
	}
	selectSnapshotArgs := cloneArgs(interactionSnapshotArgs)
	selectSnapshotArgs["interaction_goal"] = "Select Choice B"
	selectSnapshot, err := adapter.Call(context.Background(), "browser.snapshot", selectSnapshotArgs)
	if err != nil {
		t.Fatal(err)
	}
	selectPayload := mapValue(mapValue(selectSnapshot.Output)["snapshot"])
	selectSnapshotID := firstStringValue(selectPayload, "snapshot_id")
	choiceRef := snapshotRefNamed(selectSnapshot.Text, "Choice")
	if choiceRef == "" || firstStringValue(selectPayload, "previous_snapshot_id") != actionSnapshotID {
		t.Fatalf("fill did not invalidate and link its snapshot: %#v", selectPayload)
	}
	selectArgs := cloneArgs(common)
	selectArgs["uid"], selectArgs["value"], selectArgs["snapshot_id"] = choiceRef, "B", selectSnapshotID
	if _, err := adapter.Call(context.Background(), "browser.select", selectArgs); err != nil {
		t.Fatal(err)
	}
	clickSnapshotArgs := cloneArgs(interactionSnapshotArgs)
	clickSnapshotArgs["interaction_goal"] = "Increment the counter"
	clickSnapshot, err := adapter.Call(context.Background(), "browser.snapshot", clickSnapshotArgs)
	if err != nil {
		t.Fatal(err)
	}
	clickPayload := mapValue(mapValue(clickSnapshot.Output)["snapshot"])
	clickSnapshotID := firstStringValue(clickPayload, "snapshot_id")
	buttonRef := snapshotRefNamed(clickSnapshot.Text, "Increment")
	if buttonRef == "" || firstStringValue(clickPayload, "previous_snapshot_id") != selectSnapshotID {
		t.Fatalf("select did not invalidate and link its snapshot: %#v", clickPayload)
	}
	clickArgs := cloneArgs(common)
	clickArgs["uid"], clickArgs["page_id"], clickArgs["snapshot_id"] = buttonRef, pageID, clickSnapshotID
	if _, err := adapter.Call(context.Background(), "browser.click", clickArgs); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Call(context.Background(), "browser.click", clickArgs); err == nil || !strings.Contains(strings.ToLower(err.Error()), "stale") {
		t.Fatalf("a successful click did not invalidate its snapshot ref: %v", err)
	}
	postSnapshotArgs := cloneArgs(actionSnapshotArgs)
	postSnapshot, err := adapter.Call(context.Background(), "browser.snapshot", postSnapshotArgs)
	if err != nil {
		t.Fatal(err)
	}
	postOutput, ok := postSnapshot.Output.(map[string]any)
	if !ok {
		t.Fatalf("post-click snapshot output is not structured: %#v", postSnapshot.Output)
	}
	postPayload, ok := postOutput["snapshot"].(map[string]any)
	if !ok || stringValue(postPayload["previous_snapshot_id"]) != clickSnapshotID ||
		stringValue(postPayload["digest"]) == stringValue(clickPayload["digest"]) || boolValue(postPayload["repeated"]) {
		t.Fatalf("post-click snapshot did not prove a changed state: before=%#v after=%#v", clickPayload, postPayload)
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
	closeSelectedChromiumTab(t, adapter, common)
}

func closeSelectedChromiumTab(t *testing.T, adapter Adapter, common map[string]any) {
	t.Helper()
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
		t.Fatalf("Chromium cleanup could not identify the selected tab: %#v", tabs.Pages)
	}
	closeArgs := cloneArgs(common)
	closeArgs["page_id"] = pageID
	if _, err := adapter.Call(context.Background(), "browser.close", closeArgs); err != nil {
		t.Fatalf("close Chromium tab %s: %v", pageID, err)
	}
	remaining, err := adapter.Call(context.Background(), "browser.list_tabs", common)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining.Pages) != 0 {
		t.Fatalf("Chromium tab remained open after browser.close: %#v", remaining.Pages)
	}
}

func closeAllChromiumTabs(t *testing.T, adapter Adapter, common map[string]any) {
	t.Helper()
	for range 16 {
		tabs, err := adapter.Call(context.Background(), "browser.list_tabs", common)
		if err != nil {
			t.Fatal(err)
		}
		if len(tabs.Pages) == 0 {
			return
		}
		pageID := ""
		for _, raw := range tabs.Pages {
			page, ok := raw.(map[string]any)
			if ok && boolValue(page["selected"]) {
				pageID = stringValue(page["page_id"])
			}
		}
		if pageID == "" {
			t.Fatalf("Chromium cleanup could not identify the selected tab: %#v", tabs.Pages)
		}
		closeArgs := cloneArgs(common)
		closeArgs["page_id"] = pageID
		if _, err := adapter.Call(context.Background(), "browser.close", closeArgs); err != nil {
			t.Fatalf("close Chromium tab %s: %v", pageID, err)
		}
	}
	t.Fatal("Chromium cleanup exceeded 16 tabs")
}

func snapshotRefNamed(snapshot, name string) string {
	want := `"` + name + `"`
	for _, line := range strings.Split(snapshot, "\n") {
		if !strings.Contains(line, want) {
			continue
		}
		const marker = "[ref="
		start := strings.Index(line, marker)
		if start < 0 {
			continue
		}
		ref := line[start+len(marker):]
		if end := strings.IndexByte(ref, ']'); end >= 0 {
			return ref[:end]
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

func TestAgentBrowserIdentifiersFitPinnedUnixSocketPath(t *testing.T) {
	socketPath := filepath.Join(
		"/opt/agent-browser/.agent-browser/namespaces",
		newAgentBrowserNamespace(),
		"run",
		agentBrowserSessionName("owner\x00default", "visible")+".sock",
	)
	if got, limit := len([]byte(socketPath)), 103; got > limit {
		t.Fatalf("agent-browser socket path is %d bytes (max %d): %s", got, limit, socketPath)
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
