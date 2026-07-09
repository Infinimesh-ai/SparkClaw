package toolhub

import (
	"context"
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
