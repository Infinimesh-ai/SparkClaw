package toolhub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestExtractReadableText(t *testing.T) {
	title, text := extractReadableText("<html><head><title>Test Page</title></head><body><h1>Hello</h1><script>ignore()</script></body></html>", "text/html")
	if title != "Test Page" {
		t.Fatalf("unexpected title: %q", title)
	}
	if text != "Test Page Hello" {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestBrowserReadRejectsLoopbackURL(t *testing.T) {
	hub := New(config.Default(), store.NewMemoryStore())
	if _, err := hub.Execute(context.Background(), "browser.read", map[string]any{"url": "http://127.0.0.1:8080"}, "s", "run"); err == nil {
		t.Fatal("expected loopback URL to be rejected")
	}
}

func TestBrowserReadAllowsExplicitFixtureHost(t *testing.T) {
	cfg := config.Default()
	cfg.Security.BrowserReadAllowHosts = []string{"127.0.0.1"}
	hub := New(cfg, store.NewMemoryStore())
	blocked, err := hub.isBlockedBrowserHost(context.Background(), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if blocked {
		t.Fatal("explicitly allowlisted fixture host was blocked")
	}
}

func TestBrowserReadArchivesRawSnapshot(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><head><title>Snapshot</title></head><body>Archive this raw page.</body></html>"))
	}))
	defer page.Close()

	root := t.TempDir()
	cfg := config.Default()
	cfg.Security.BrowserReadAllowHosts = []string{"127.0.0.1"}
	cfg.Storage.ArtifactDir = filepath.Join(root, "artifacts")
	st := store.NewMemoryStore()
	hub := New(cfg, st)

	result, err := hub.Execute(context.Background(), "browser.read", map[string]any{"url": page.URL}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	out := result.Output.(map[string]any)
	if out["snapshot_ref"] == "" || out["snapshot_object_key"] == "" {
		t.Fatalf("browser output missing snapshot reference: %#v", out)
	}
	objects := st.ListArtifactObjects(10)
	if !hasBrowserArtifactKind(objects, "browser_snapshot") {
		t.Fatalf("browser snapshot was not cataloged: %#v", objects)
	}
	snapshotPath := filepath.Join(cfg.Storage.ArtifactDir, cfg.Storage.ArtifactBucket, out["snapshot_object_key"].(string))
	raw, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Archive this raw page.") {
		t.Fatalf("snapshot file did not contain raw page: %s", raw)
	}
}

func TestBrowserReadUsesReadabilityForArticleText(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head><title>Browser Noise Fixture</title></head>
  <body>
    <nav>Navigation clutter should not be selected as the article body.</nav>
    <article>
      <h1>Readable Admission Notice</h1>
      <p>The official admission notice contains application dates, tuition notes, campus locations, and contact channels for applicants.</p>
      <p>This second paragraph gives the extractor enough meaningful article prose to score the content region over surrounding page chrome.</p>
      <p>Applicants should use the published school notice as evidence and ignore unrelated menu, footer, and sidebar text.</p>
    </article>
    <footer>Footer clutter should also stay out of the extracted article.</footer>
  </body>
</html>`))
	}))
	defer page.Close()

	cfg := config.Default()
	cfg.Security.BrowserReadAllowHosts = []string{"127.0.0.1"}
	hub := New(cfg, store.NewMemoryStore())

	result, err := hub.Execute(context.Background(), "browser.read", map[string]any{"url": page.URL}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	out := result.Output.(map[string]any)
	if out["extractor"] != "readability" || out["readability_status"] != "applied" {
		t.Fatalf("expected readability extraction, got %#v", out)
	}
	if out["needs_structure_snapshot"] != false {
		t.Fatalf("complete article read should not require structure snapshot: %#v", out)
	}
	text := out["text"].(string)
	if !strings.Contains(text, "Readable Admission Notice") ||
		!strings.Contains(text, "official admission notice contains application dates") {
		t.Fatalf("readability text missing article content: %q", text)
	}
	for _, noise := range []string{"Navigation clutter", "Footer clutter"} {
		if strings.Contains(text, noise) {
			t.Fatalf("readability text retained page chrome %q: %q", noise, text)
		}
	}
}

func TestBrowserReadSignalsStructureSnapshotForShortInteractivePage(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head><title>Short Interactive Notice</title></head>
  <body>
    <main>
      <article><h1>Short Interactive Notice</h1><p>Brief notice.</p></article>
      <button>展开更多</button>
      <a href="/download.pdf">下载附件</a>
    </main>
  </body>
</html>`))
	}))
	defer page.Close()

	cfg := config.Default()
	cfg.Security.BrowserReadAllowHosts = []string{"127.0.0.1"}
	hub := New(cfg, store.NewMemoryStore())

	result, err := hub.Execute(context.Background(), "browser.read", map[string]any{"url": page.URL}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	out := result.Output.(map[string]any)
	if out["needs_structure_snapshot"] != true {
		t.Fatalf("short interactive page should request structure snapshot: %#v", out)
	}
	if !strings.Contains(fmt.Sprint(out["structure_snapshot_reasons"]), "interactive_affordance_hint") {
		t.Fatalf("expected interactive snapshot reason, got %#v", out["structure_snapshot_reasons"])
	}
}

func TestBrowserReadUsesBrowserSessionWhenEnabled(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.BrowserAutomation.Enabled = true
	cfg.Security.BrowserReadAllowHosts = []string{"example.com"}
	readArgs := map[string]any{}
	hub := New(cfg, store.NewMemoryStore()).WithBrowserAutomationAdapter(fakePageReadAdapter{readArgs: &readArgs})

	result, err := hub.Execute(context.Background(), "browser.read", map[string]any{
		"url":             "https://example.com/rendered",
		"browser_mode":    "collaborative",
		"presentation":    "visible",
		"surface_visible": true,
	}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	if readArgs["browser_mode"] != "collaborative" || readArgs["presentation"] != "visible" || readArgs["surface_visible"] != true {
		t.Fatalf("browser.read should pass collaborative visible metadata to adapter: %#v", readArgs)
	}
	out := result.Output.(map[string]any)
	if out["browser_mode"] != "collaborative" || out["presentation"] != "visible" || out["surface_visible"] != true {
		t.Fatalf("browser.read output should include collaborative metadata: %#v", out)
	}
	if out["read_mode"] != "browser_session" || out["rendered"] != true {
		t.Fatalf("expected rendered browser session read, got %#v", out)
	}
	if out["browser_provider"] != "fake-browser" {
		t.Fatalf("expected fake browser provider, got %#v", out["browser_provider"])
	}
	if out["extractor"] != "readability" || out["readability_status"] != "applied" {
		t.Fatalf("expected readability over rendered HTML, got %#v", out)
	}
	if out["needs_structure_snapshot"] != false {
		t.Fatalf("complete rendered article read should not require structure snapshot: %#v", out)
	}
	text := out["text"].(string)
	if !strings.Contains(text, "Browser-rendered content loaded after JavaScript execution") || strings.Contains(text, "Hidden static fallback") {
		t.Fatalf("browser read did not use rendered article content: %q", text)
	}
	for _, action := range out["browser_actions"].([]string) {
		if action == "take_snapshot" {
			t.Fatalf("browser.read should not force structure snapshot: %#v", out["browser_actions"])
		}
	}
	if snapshot, ok := out["browser_snapshot_text"]; ok && strings.TrimSpace(fmt.Sprint(snapshot)) != "" {
		t.Fatalf("browser.read should not include snapshot text by default, got %q", snapshot)
	}
}

func TestBrowserReadAutonomousHiddenUsesHiddenBrowserSessionWhenEnabled(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.BrowserAutomation.Enabled = true
	cfg.Security.BrowserReadAllowHosts = []string{"example.com"}
	readArgs := map[string]any{}
	hub := New(cfg, store.NewMemoryStore()).WithBrowserAutomationAdapter(fakePageReadAdapter{readArgs: &readArgs})

	result, err := hub.Execute(context.Background(), "browser.read", map[string]any{
		"url":             "https://example.com/rendered",
		"browser_mode":    "autonomous",
		"presentation":    "hidden",
		"surface_visible": false,
	}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	if readArgs["browser_mode"] != "autonomous" || readArgs["presentation"] != "hidden" || readArgs["surface_visible"] != false {
		t.Fatalf("autonomous hidden browser.read should pass hidden metadata to adapter: %#v", readArgs)
	}
	out := result.Output.(map[string]any)
	if out["read_mode"] != "hidden_browser_session" || out["rendered"] != true {
		t.Fatalf("expected hidden rendered browser session read, got %#v", out)
	}
	if out["browser_mode"] != "autonomous" || out["presentation"] != "hidden" || out["surface_visible"] != false {
		t.Fatalf("browser.read output should preserve autonomous hidden metadata: %#v", out)
	}
	if out["browser_provider"] != "fake-browser" {
		t.Fatalf("expected fake browser provider, got %#v", out["browser_provider"])
	}
	actions := fmt.Sprint(out["browser_actions"])
	if !strings.Contains(actions, "new_hidden_page") || strings.Contains(actions, "take_snapshot") {
		t.Fatalf("hidden read should use hidden page without forcing snapshot: %#v", out["browser_actions"])
	}
	if !strings.Contains(fmt.Sprint(out["text"]), "Browser-rendered content loaded after JavaScript execution") {
		t.Fatalf("hidden browser read did not extract rendered text: %#v", out)
	}
}

func TestBrowserReadAutonomousAuthChallengeStartsVisibleHandoff(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.BrowserAutomation.Enabled = true
	cfg.Security.BrowserReadAllowHosts = []string{"example.com"}
	cfg.Tools.BrowserAutomation.Profile = "default"
	st := store.NewMemoryStore()
	adapter := &authFlowBrowserAdapter{
		readResult: browserautomation.PageReadResult{
			URL:                   "https://example.com/private",
			FinalURL:              "https://example.com/private",
			Title:                 "Login Required",
			HTML:                  "<html><body><input type=\"password\"><p>Please sign in to continue</p></body></html>",
			Text:                  "Please sign in to continue",
			Rendered:              true,
			AuthChallengeDetected: true,
			ReadMode:              "hidden_browser_session",
			BrowserMode:           "autonomous",
			Presentation:          "hidden",
			Provider:              "fake-browser",
			Actions:               []string{"new_hidden_page", "evaluate_script"},
			Untrusted:             true,
		},
	}
	hub := New(cfg, st).WithBrowserAutomationAdapter(adapter)
	result, err := hub.Execute(context.Background(), "browser.read", map[string]any{
		"url":             "https://example.com/private",
		"browser_mode":    "autonomous",
		"presentation":    "hidden",
		"surface_visible": false,
	}, "", "run_auth")
	if err != nil {
		t.Fatal(err)
	}
	out := result.Output.(map[string]any)
	if stringArg(out, "browser_auth_status", "") != "handoff_waiting" || !boolArg(out, "login_handoff_opened", false) {
		t.Fatalf("expected visible handoff output, got %#v", out)
	}
	if adapter.callTool != "browser.open" || stringArg(adapter.callArgs, "browser_mode", "") != "collaborative" || !boolArg(adapter.callArgs, "surface_visible", false) {
		t.Fatalf("handoff did not open visible browser page: tool=%s args=%#v", adapter.callTool, adapter.callArgs)
	}
	if !hasToolhubAuditType(st.ListAudit(""), "browser_auth.challenge_detected") || !hasToolhubAuditType(st.ListAudit(""), "browser_auth.handoff_started") {
		t.Fatalf("missing browser auth audit events: %#v", st.ListAudit(""))
	}
}

func TestBrowserReadRestoresStoredAuthBeforeHiddenRead(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.BrowserAutomation.Enabled = true
	cfg.Security.BrowserReadAllowHosts = []string{"example.com"}
	cfg.Tools.BrowserAutomation.Profile = "default"
	st := store.NewMemoryStore()
	authState := browserautomation.AuthState{
		Origin:       "https://example.com",
		URL:          "https://example.com/private",
		Cookie:       "fixture=stored",
		LocalStorage: map[string]string{"logged_in": "yes"},
	}
	rawState, _ := json.Marshal(authState)
	st.SaveCredentialSecret(app.CredentialSecret{Ref: "browser-auth:stored", Kind: "browser-auth-state", Value: string(rawState)})
	st.SaveBrowserAuthRecord(app.BrowserAuthRecord{
		ID:               "auth_stored",
		OwnerID:          app.DefaultOwnerID,
		BrowserProfileID: "default",
		SiteOrigin:       "https://example.com",
		CredentialRef:    "browser-auth:stored",
		CookieJarRef:     "browser-auth:stored",
	})
	adapter := &authFlowBrowserAdapter{readResult: authenticatedPageReadResult("https://example.com/private")}
	hub := New(cfg, st).WithBrowserAutomationAdapter(adapter)
	result, err := hub.Execute(context.Background(), "browser.read", map[string]any{
		"url":             "https://example.com/private",
		"browser_mode":    "autonomous",
		"presentation":    "hidden",
		"surface_visible": false,
	}, "", "run_auth")
	if err != nil {
		t.Fatal(err)
	}
	out := result.Output.(map[string]any)
	if stringArg(out, "browser_auth_status", "") != "restored" || !boolArg(out, "browser_auth_restore_succeeded", false) {
		t.Fatalf("expected restored auth output, got %#v", out)
	}
	if adapter.importedState == nil || adapter.importedState.Cookie != "fixture=stored" || stringArg(adapter.importArgs, "presentation", "") != "hidden" {
		t.Fatalf("stored auth state was not imported into hidden browser: state=%#v args=%#v", adapter.importedState, adapter.importArgs)
	}
	if adapter.callTool == "browser.open" {
		t.Fatalf("stored auth restore should not open visible handoff")
	}
}

func TestBrowserReadCompletedHandoffSavesAuthAndContinuesHidden(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.BrowserAutomation.Enabled = true
	cfg.Security.BrowserReadAllowHosts = []string{"example.com"}
	cfg.Tools.BrowserAutomation.Profile = "default"
	st := store.NewMemoryStore()
	adapter := &authFlowBrowserAdapter{
		readResult: authenticatedPageReadResult("https://example.com/private"),
		exportState: browserautomation.AuthState{
			Origin:       "https://example.com",
			URL:          "https://example.com/private",
			Cookie:       "fixture=visible",
			LocalStorage: map[string]string{"logged_in": "yes"},
		},
	}
	hub := New(cfg, st).WithBrowserAutomationAdapter(adapter)
	result, err := hub.Execute(context.Background(), "browser.read", map[string]any{
		"url":                     "https://example.com/private",
		"browser_mode":            "autonomous",
		"presentation":            "hidden",
		"surface_visible":         false,
		"login_handoff_completed": true,
	}, "", "run_auth")
	if err != nil {
		t.Fatal(err)
	}
	out := result.Output.(map[string]any)
	if !boolArg(out, "browser_auth_record_saved", false) || stringArg(out, "browser_auth_record_id", "") == "" {
		t.Fatalf("expected saved browser auth record output, got %#v", out)
	}
	record, ok := st.FindBrowserAuthRecord(app.DefaultOwnerID, "default", "https://example.com", "", "")
	if !ok || record.Status != app.BrowserAuthStatusActive || record.CredentialRef == "" {
		t.Fatalf("saved browser auth record missing: %#v ok=%v", record, ok)
	}
	secret, ok := st.GetCredentialSecret(record.CredentialRef)
	if !ok || !strings.Contains(secret.Value, "fixture=visible") {
		t.Fatalf("saved browser auth secret missing: %#v ok=%v", secret, ok)
	}
	if adapter.importedState == nil || adapter.importedState.Cookie != "fixture=visible" {
		t.Fatalf("completed handoff auth state was not imported into hidden browser: %#v", adapter.importedState)
	}
}

func TestBrowserReadAutonomousHiddenFallsBackToDirectHTTPWhenBrowserSessionFails(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>Hidden autonomous fallback</title></head><body><article><h1>Hidden autonomous fallback</h1><p>Direct fallback still returns public page evidence when the hidden browser provider is unavailable.</p><p>The fallback path keeps autonomous hidden metadata and marks the session error explicitly.</p></article></body></html>`))
	}))
	defer page.Close()

	cfg := config.Default()
	cfg.Tools.BrowserAutomation.Enabled = true
	cfg.Security.BrowserReadAllowHosts = []string{"127.0.0.1"}
	readArgs := map[string]any{}
	hub := New(cfg, store.NewMemoryStore()).WithBrowserAutomationAdapter(fakePageReadAdapter{
		readArgs: &readArgs,
		readErr:  fmt.Errorf("hidden browser unavailable"),
	})

	result, err := hub.Execute(context.Background(), "browser.read", map[string]any{
		"url":             page.URL,
		"browser_mode":    "autonomous",
		"presentation":    "hidden",
		"surface_visible": false,
	}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	if readArgs["browser_mode"] != "autonomous" || readArgs["presentation"] != "hidden" {
		t.Fatalf("browser.read should attempt hidden browser session before fallback: %#v", readArgs)
	}
	out := result.Output.(map[string]any)
	if out["read_mode"] != "direct_http_fallback" || out["rendered"] != false {
		t.Fatalf("expected direct HTTP fallback read, got %#v", out)
	}
	if out["browser_mode"] != "autonomous" || out["presentation"] != "hidden" || out["surface_visible"] != false {
		t.Fatalf("fallback output should preserve autonomous hidden metadata: %#v", out)
	}
	if !strings.Contains(fmt.Sprint(out["browser_session_error"]), "hidden browser unavailable") {
		t.Fatalf("fallback should expose compact browser session error: %#v", out)
	}
	if !strings.Contains(fmt.Sprint(out["text"]), "Direct fallback still returns public page evidence") {
		t.Fatalf("direct fallback did not extract page text: %#v", out)
	}
}

func hasBrowserArtifactKind(objects []app.ArtifactObject, kind string) bool {
	for _, object := range objects {
		if object.Kind == kind {
			return true
		}
	}
	return false
}

type fakePageReadAdapter struct {
	readArgs *map[string]any
	callArgs *map[string]any
	readErr  error
}

func (fakePageReadAdapter) Health(ctx context.Context) (browserautomation.Result, error) {
	return browserautomation.Result{Tool: "browser.status", Output: map[string]any{"ok": true}, Provider: "fake-browser", Untrusted: true}, nil
}

func (f fakePageReadAdapter) Call(ctx context.Context, tool string, args map[string]any) (browserautomation.Result, error) {
	if f.callArgs != nil {
		*f.callArgs = cloneTestArgs(args)
	}
	return browserautomation.Result{Tool: tool, RawTool: strings.TrimPrefix(tool, "browser."), Output: map[string]any{"ok": true}, Provider: "fake-browser", Untrusted: true}, nil
}

func (f fakePageReadAdapter) ReadPage(ctx context.Context, url string, args map[string]any) (browserautomation.PageReadResult, error) {
	if f.readArgs != nil {
		*f.readArgs = cloneTestArgs(args)
	}
	if f.readErr != nil {
		return browserautomation.PageReadResult{}, f.readErr
	}
	readMode := "browser_session"
	actions := []string{"new_page", "evaluate_script"}
	if fmt.Sprint(args["browser_mode"]) == "autonomous" && fmt.Sprint(args["presentation"]) == "hidden" && !boolArg(args, "surface_visible", false) {
		readMode = "hidden_browser_session"
		actions = []string{"new_hidden_page", "evaluate_script"}
	}
	return browserautomation.PageReadResult{
		URL:            url,
		FinalURL:       "https://example.com/rendered#loaded",
		Title:          "Rendered Admission Article",
		HTML:           renderedBrowserFixtureHTML(),
		Text:           "Rendered Admission Article Browser-rendered content loaded after JavaScript execution.",
		Rendered:       true,
		ReadMode:       readMode,
		BrowserMode:    fmt.Sprint(args["browser_mode"]),
		Presentation:   fmt.Sprint(args["presentation"]),
		SurfaceVisible: boolArg(args, "surface_visible", false),
		Provider:       "fake-browser",
		Actions:        actions,
		Untrusted:      true,
	}, nil
}

func (fakePageReadAdapter) Close() error { return nil }

type authFlowBrowserAdapter struct {
	readResult    browserautomation.PageReadResult
	exportState   browserautomation.AuthState
	callTool      string
	callArgs      map[string]any
	importedState *browserautomation.AuthState
	importArgs    map[string]any
}

func (a *authFlowBrowserAdapter) Health(ctx context.Context) (browserautomation.Result, error) {
	return browserautomation.Result{Tool: "browser.status", Output: map[string]any{"ok": true}, Provider: "fake-browser", Untrusted: true}, nil
}

func (a *authFlowBrowserAdapter) Call(ctx context.Context, tool string, args map[string]any) (browserautomation.Result, error) {
	a.callTool = tool
	a.callArgs = cloneTestArgs(args)
	return browserautomation.Result{Tool: tool, RawTool: strings.TrimPrefix(tool, "browser."), Output: map[string]any{"ok": true}, Provider: "fake-browser", Untrusted: true}, nil
}

func (a *authFlowBrowserAdapter) ReadPage(ctx context.Context, url string, args map[string]any) (browserautomation.PageReadResult, error) {
	result := a.readResult
	result.URL = url
	if result.FinalURL == "" {
		result.FinalURL = url
	}
	if result.BrowserMode == "" {
		result.BrowserMode = fmt.Sprint(args["browser_mode"])
	}
	if result.Presentation == "" {
		result.Presentation = fmt.Sprint(args["presentation"])
	}
	result.SurfaceVisible = boolArg(args, "surface_visible", false)
	return result, nil
}

func (a *authFlowBrowserAdapter) ExportAuthState(ctx context.Context, args map[string]any) (browserautomation.AuthState, error) {
	state := a.exportState
	if state.Origin == "" {
		state.Origin = "https://example.com"
	}
	return state, nil
}

func (a *authFlowBrowserAdapter) ImportAuthState(ctx context.Context, state browserautomation.AuthState, args map[string]any) error {
	a.importedState = &state
	a.importArgs = cloneTestArgs(args)
	return nil
}

func (a *authFlowBrowserAdapter) Close() error { return nil }

func authenticatedPageReadResult(rawURL string) browserautomation.PageReadResult {
	return browserautomation.PageReadResult{
		URL:          rawURL,
		FinalURL:     rawURL,
		Title:        "Private Page",
		HTML:         "<html><body><article><h1>Private Page</h1><p>Authenticated content.</p></article></body></html>",
		Text:         "Private Page Authenticated content.",
		Rendered:     true,
		ReadMode:     "hidden_browser_session",
		BrowserMode:  "autonomous",
		Presentation: "hidden",
		Provider:     "fake-browser",
		Actions:      []string{"new_hidden_page", "evaluate_script"},
		Untrusted:    true,
	}
}

func hasToolhubAuditType(events []app.AuditEvent, typ string) bool {
	for _, event := range events {
		if event.Type == typ {
			return true
		}
	}
	return false
}

func cloneTestArgs(args map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range args {
		out[key] = value
	}
	return out
}

func renderedBrowserFixtureHTML() string {
	return `<!doctype html>
<html>
  <head><title>Rendered Admission Article</title></head>
  <body>
    <template>Hidden static fallback should not be read.</template>
    <nav>Navigation chrome should be ignored.</nav>
    <article>
      <h1>Rendered Admission Article</h1>
      <p>Browser-rendered content loaded after JavaScript execution includes official dates, campus locations, application requirements, and contact channels.</p>
      <p>The second rendered paragraph makes this article substantial enough for Readability to prefer it over surrounding navigation and footer chrome.</p>
      <p>The browser session has already handled page rendering before Readability receives this HTML.</p>
    </article>
    <footer>Footer chrome should be ignored.</footer>
  </body>
</html>`
}
