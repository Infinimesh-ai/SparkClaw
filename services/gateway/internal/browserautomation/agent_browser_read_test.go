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

func TestAgentBrowserToolAllowlistUsesNativeReadWithoutEvaluation(t *testing.T) {
	for _, name := range []string{"agent_browser_read", "agent_browser_get_text", "agent_browser_snapshot", "agent_browser_reload"} {
		if _, ok := requiredAgentBrowserTools[name]; !ok {
			t.Fatalf("native browser tool %q is missing from the adapter allowlist", name)
		}
	}
	if agentBrowserMCPToolsProfile != "core,tabs" {
		t.Fatalf("agent-browser MCP profile must include native reload support: %q", agentBrowserMCPToolsProfile)
	}
	if _, ok := requiredAgentBrowserTools["agent_browser_eval"]; ok {
		t.Fatal("browser evaluation must not be available through the SparkClaw adapter")
	}
}

func TestRealChromiumReadUsesAgentBrowserRenderedDOM(t *testing.T) {
	if os.Getenv("SPARKCLAW_RUN_REAL_BROWSER_SCENARIOS") != "1" {
		t.Skip("set SPARKCLAW_RUN_REAL_BROWSER_SCENARIOS=1 to validate native agent-browser rendered reads")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>Rendered read</title></head><body>
<main id="app">Loading</main>
<script>document.querySelector('#app').innerHTML='<h1>Native rendered content</h1><p>AGENT_BROWSER_RENDERED_DOM_OK</p>';</script>
</body></html>`))
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.Adapters.BrowserAutomation.ProfileDir = t.TempDir()
	cfg.Adapters.BrowserAutomation.TimeoutMS = 30000
	adapter := NewAdapter(cfg)
	t.Cleanup(func() { _ = adapter.Close() })

	page, err := adapter.ReadPage(context.Background(), server.URL, map[string]any{
		"browser_mode":       "autonomous",
		"presentation":       "hidden",
		"owner_id":           "owner-test",
		"browser_profile_id": "native-rendered-read",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page.Text, "AGENT_BROWSER_RENDERED_DOM_OK") || strings.Contains(page.Text, "Loading") {
		t.Fatalf("native read did not return the rendered active-tab DOM: %q", page.Text)
	}
	if page.ReadSource != "active-tab-html" || page.ContentType != "text/plain; source=agent-browser-read" {
		t.Fatalf("native read provenance is incomplete: source=%q content_type=%q", page.ReadSource, page.ContentType)
	}
	actions := strings.Join(page.Actions, " ")
	for _, required := range []string{"agent_browser_read", "agent_browser_get_text", "agent_browser_snapshot"} {
		if !strings.Contains(actions, required) {
			t.Fatalf("native read action %q is missing: %v", required, page.Actions)
		}
	}
	if strings.Contains(actions, "eval") {
		t.Fatalf("native read unexpectedly used script evaluation: %v", page.Actions)
	}
}
