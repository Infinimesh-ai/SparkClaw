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

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestBrowserReadScenarioFixturesDirectDiagnostics(t *testing.T) {
	server := newBrowserScenarioFixtureServer(t)
	defer server.Close()

	hub := newBrowserScenarioHub(t)
	cases := []struct {
		name        string
		path        string
		reasons     []string
		authBlocked bool
		contains    string
	}{
		{
			name:     "index page exposes links before complete article evidence",
			path:     "list.html",
			reasons:  []string{"interactive_affordance_hint"},
			contains: "Admission detail notice",
		},
		{
			name:     "javascript shell asks for rendered read or structure",
			path:     "js-rendered.html",
			reasons:  []string{"dynamic_rendering_hint"},
			contains: "STATIC_SHELL_BEFORE_JS_RENDER",
		},
		{
			name:     "short body in a larger page asks for structure",
			path:     "short-body.html",
			reasons:  []string{"article_text_short", "interactive_affordance_hint"},
			contains: "SHORT_BODY_ONLY_BRIEF_NOTICE",
		},
		{
			name:        "login wall is explicit blocked evidence",
			path:        "login-wall.html",
			reasons:     []string{"auth_challenge_detected"},
			authBlocked: true,
			contains:    "LOGIN_WALL_BLOCKED_CONTENT",
		},
		{
			name:        "captcha and 2fa page is explicit blocked evidence",
			path:        "captcha.html",
			reasons:     []string{"auth_challenge_detected"},
			authBlocked: true,
			contains:    "CAPTCHA_BLOCKED_CONTENT",
		},
		{
			name:     "paginated article exposes next page affordance",
			path:     "paginated-1.html",
			reasons:  []string{"interactive_affordance_hint"},
			contains: "PAGINATED_PART_ONE",
		},
		{
			name:     "attachment page exposes download affordance",
			path:     "download.html",
			reasons:  []string{"interactive_affordance_hint"},
			contains: "DOWNLOAD_ATTACHMENT_NOTICE",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := executeBrowserScenarioRead(t, hub, server.URL+"/"+tc.path, nil)
			if out["read_mode"] != "direct_http" || out["rendered"] != false {
				t.Fatalf("expected direct HTTP read before browser session, got %#v", out)
			}
			if out["browser_mode"] != "autonomous" || out["presentation"] != "hidden" || out["surface_visible"] != false {
				t.Fatalf("default browser mode metadata missing: %#v", out)
			}
			if out["needs_structure_snapshot"] != true {
				t.Fatalf("scenario should request structure snapshot: %#v", out)
			}
			reasonText := fmt.Sprint(out["structure_snapshot_reasons"])
			for _, reason := range tc.reasons {
				if !strings.Contains(reasonText, reason) {
					t.Fatalf("expected structure reason %q in %#v", reason, out["structure_snapshot_reasons"])
				}
			}
			if boolArg(out, "auth_challenge_detected", false) != tc.authBlocked {
				t.Fatalf("auth challenge mismatch: got %#v want %v in %#v", out["auth_challenge_detected"], tc.authBlocked, out)
			}
			if !strings.Contains(fmt.Sprint(out["text"]), tc.contains) {
				t.Fatalf("scenario evidence missing %q in text: %q", tc.contains, out["text"])
			}
			for _, key := range []string{"snapshot_ref", "snapshot_object_key", "read_mode", "browser_mode", "presentation", "surface_visible", "needs_structure_snapshot", "structure_snapshot_reasons"} {
				if _, ok := out[key]; !ok {
					t.Fatalf("browser.read output missing trace key %q: %#v", key, out)
				}
			}
		})
	}
}

func TestBrowserReadScenarioHiddenRenderedAndLazyPages(t *testing.T) {
	cases := []struct {
		name       string
		url        string
		html       string
		text       string
		marker     string
		scrollHigh int
	}{
		{
			name:       "js rendered article",
			url:        "https://example.com/js-rendered.html",
			html:       renderedScenarioHTML("JS Rendered Notice", "JS_RENDERED_NOTICE_2026 appears only after browser JavaScript execution."),
			text:       "JS Rendered Notice JS_RENDERED_NOTICE_2026 appears only after browser JavaScript execution.",
			marker:     "JS_RENDERED_NOTICE_2026",
			scrollHigh: 900,
		},
		{
			name:       "lazy loaded article after scroll",
			url:        "https://example.com/lazy-load.html",
			html:       renderedScenarioHTML("Lazy Loaded Notice", "LAZY_SECTION_AFTER_SCROLL confirms the hidden read path scrolled and captured delayed content."),
			text:       "Lazy Loaded Notice LAZY_SECTION_AFTER_SCROLL confirms the hidden read path scrolled and captured delayed content.",
			marker:     "LAZY_SECTION_AFTER_SCROLL",
			scrollHigh: 2400,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			readArgs := map[string]any{}
			hub := newHiddenBrowserScenarioHub(t, scenarioPageReadAdapter{
				readArgs: &readArgs,
				page: browserautomation.PageReadResult{
					URL:            tc.url,
					FinalURL:       tc.url + "#rendered",
					Title:          tc.name,
					HTML:           tc.html,
					Text:           tc.text,
					ContentType:    "text/html; source=browser",
					ReadyState:     "complete",
					Rendered:       true,
					HTMLLength:     len(tc.html),
					TextLength:     len(tc.text),
					ScrollHeight:   tc.scrollHigh,
					ReadMode:       "hidden_browser_session",
					BrowserMode:    "autonomous",
					Presentation:   "hidden",
					SurfaceVisible: false,
					Provider:       "scenario-hidden-browser",
					Actions:        []string{"agent_browser_tab_new", "agent_browser_read", "agent_browser_snapshot"},
					Untrusted:      true,
				},
			})

			out := executeBrowserScenarioRead(t, hub, tc.url, map[string]any{
				"browser_mode":    "autonomous",
				"presentation":    "hidden",
				"surface_visible": false,
			})
			if readArgs["browser_mode"] != "autonomous" || readArgs["presentation"] != "hidden" || readArgs["surface_visible"] != false {
				t.Fatalf("hidden read did not pass autonomous metadata: %#v", readArgs)
			}
			if out["read_mode"] != "hidden_browser_session" || out["rendered"] != true {
				t.Fatalf("expected hidden rendered read, got %#v", out)
			}
			if out["browser_provider"] != "scenario-hidden-browser" {
				t.Fatalf("hidden provider metadata missing: %#v", out)
			}
			if !strings.Contains(fmt.Sprint(out["text"]), tc.marker) {
				t.Fatalf("rendered evidence missing marker %q: %#v", tc.marker, out)
			}
			actions := fmt.Sprint(out["browser_actions"])
			if !strings.Contains(actions, "agent_browser_tab_new") || !strings.Contains(actions, "agent_browser_read") || !strings.Contains(actions, "agent_browser_snapshot") {
				t.Fatalf("hidden read actions should remain agent-browser native: %#v", out["browser_actions"])
			}
			if intArg(out, "browser_scroll_height", 0) < tc.scrollHigh {
				t.Fatalf("scroll diagnostics missing: %#v", out["browser_scroll_height"])
			}
		})
	}
}

func TestBrowserReadScenarioFallbackUsesDirectHTTPWithExplicitMetadata(t *testing.T) {
	server := newBrowserScenarioFixtureServer(t)
	defer server.Close()

	cfg := config.Default()
	cfg.Tools.BrowserAutomation.Enabled = true
	cfg.Security.BrowserReadAllowHosts = []string{"127.0.0.1"}
	cfg.Storage.ArtifactDir = t.TempDir()
	readArgs := map[string]any{}
	hub := New(cfg, store.NewMemoryStore()).WithBrowserAutomationAdapter(scenarioPageReadAdapter{
		readArgs: &readArgs,
		readErr:  fmt.Errorf("hidden browser unavailable"),
	})

	out := executeBrowserScenarioRead(t, hub, server.URL+"/fallback.html", map[string]any{
		"browser_mode":    "autonomous",
		"presentation":    "hidden",
		"surface_visible": false,
	})
	if readArgs["browser_mode"] != "autonomous" || readArgs["presentation"] != "hidden" {
		t.Fatalf("browser.read should attempt hidden provider before fallback: %#v", readArgs)
	}
	if out["read_mode"] != "direct_http_fallback" || out["rendered"] != false {
		t.Fatalf("expected direct fallback metadata, got %#v", out)
	}
	if !strings.Contains(fmt.Sprint(out["browser_session_error"]), "hidden browser unavailable") {
		t.Fatalf("fallback should explain hidden provider failure: %#v", out)
	}
	if !strings.Contains(fmt.Sprint(out["text"]), "DIRECT_HTTP_FALLBACK_EVIDENCE") {
		t.Fatalf("fallback did not preserve public page evidence: %#v", out)
	}
}

func TestBrowserReadScenarioRealHiddenProviderSmoke(t *testing.T) {
	if os.Getenv("SPARKCLAW_RUN_REAL_BROWSER_SCENARIOS") != "1" {
		t.Skip("set SPARKCLAW_RUN_REAL_BROWSER_SCENARIOS=1 to run the real hidden agent-browser Chromium smoke test")
	}
	server := newBrowserScenarioFixtureServer(t)
	defer server.Close()

	cfg := config.Default()
	cfg.Tools.BrowserAutomation.Enabled = true
	cfg.Security.BrowserReadAllowHosts = []string{"127.0.0.1"}
	cfg.Storage.ArtifactDir = t.TempDir()
	cfg.Adapters.BrowserAutomation.TimeoutMS = 30000
	hub := New(cfg, store.NewMemoryStore())
	t.Cleanup(func() {
		_ = hub.Close()
	})

	out := executeBrowserScenarioRead(t, hub, server.URL+"/js-rendered.html", map[string]any{
		"browser_mode":    "autonomous",
		"presentation":    "hidden",
		"surface_visible": false,
		"timeout_ms":      30000,
	})
	if out["read_mode"] != "hidden_browser_session" || out["rendered"] != true {
		t.Fatalf("expected real hidden rendered read, got %#v", out)
	}
	if !strings.Contains(fmt.Sprint(out["text"]), "JS_RENDERED_NOTICE_2026") {
		t.Fatalf("real hidden provider did not capture rendered JS article: %#v", out)
	}
	if strings.Contains(fmt.Sprint(out["text"]), "STATIC_SHELL_BEFORE_JS_RENDER") {
		t.Fatalf("real hidden provider returned static shell instead of rendered article: %#v", out)
	}
}

func newBrowserScenarioFixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	root := filepath.Join("..", "..", "..", "..", "eval", "fixtures", "browser")
	return httptest.NewServer(http.FileServer(http.Dir(root)))
}

func newBrowserScenarioHub(t *testing.T) *ToolHub {
	t.Helper()
	cfg := config.Default()
	cfg.Security.BrowserReadAllowHosts = []string{"127.0.0.1"}
	cfg.Storage.ArtifactDir = t.TempDir()
	return New(cfg, store.NewMemoryStore())
}

func newHiddenBrowserScenarioHub(t *testing.T, adapter browserautomation.Adapter) *ToolHub {
	t.Helper()
	cfg := config.Default()
	cfg.Tools.BrowserAutomation.Enabled = true
	cfg.Security.BrowserReadAllowHosts = []string{"example.com"}
	cfg.Storage.ArtifactDir = t.TempDir()
	return New(cfg, store.NewMemoryStore()).WithBrowserAutomationAdapter(adapter)
}

func executeBrowserScenarioRead(t *testing.T, hub *ToolHub, rawURL string, args map[string]any) map[string]any {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	args["url"] = rawURL
	result, err := hub.Execute(context.Background(), "browser.read", args, "scenario_session", "scenario_run")
	if err != nil {
		t.Fatal(err)
	}
	out, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %#v", result.Output)
	}
	return out
}

func renderedScenarioHTML(title, marker string) string {
	return `<!doctype html>
<html>
  <head><title>` + title + `</title></head>
  <body>
    <template>STATIC_SHELL_BEFORE_JS_RENDER</template>
    <nav>Navigation chrome should be ignored.</nav>
    <article>
      <h1>` + title + `</h1>
      <p>` + marker + `</p>
      <p>The article has enough official-looking body text for Readability to prefer it over navigation chrome.</p>
      <p>The browser provider returned this DOM after rendering and any normal lazy loading work.</p>
    </article>
  </body>
</html>`
}

type scenarioPageReadAdapter struct {
	page     browserautomation.PageReadResult
	readArgs *map[string]any
	readErr  error
}

func (scenarioPageReadAdapter) Health(ctx context.Context, _ map[string]any) (browserautomation.Result, error) {
	return browserautomation.Result{Tool: "browser.status", Output: map[string]any{"ok": true}, Provider: "scenario-browser", Untrusted: true}, nil
}

func (scenarioPageReadAdapter) Call(ctx context.Context, tool string, args map[string]any) (browserautomation.Result, error) {
	return browserautomation.Result{Tool: tool, Output: map[string]any{"ok": true}, Provider: "scenario-browser", Untrusted: true}, nil
}

func (s scenarioPageReadAdapter) ReadPage(ctx context.Context, rawURL string, args map[string]any) (browserautomation.PageReadResult, error) {
	if s.readArgs != nil {
		*s.readArgs = cloneTestArgs(args)
	}
	if s.readErr != nil {
		return browserautomation.PageReadResult{}, s.readErr
	}
	page := s.page
	if page.URL == "" {
		page.URL = rawURL
	}
	if page.FinalURL == "" {
		page.FinalURL = rawURL
	}
	return page, nil
}

func (scenarioPageReadAdapter) Close() error { return nil }
