package toolhub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/websearch"
)

// configureTestInfoCredentials satisfies InfinimeshInfoConfig.Configured so
// tests can register the credential-gated weather.lookup tool.
func configureTestInfoCredentials(cfg *config.Config) {
	cfg.Plugins.Entries.InfinimeshInfo.Config.LicenseID = "lic_test"
	cfg.Plugins.Entries.InfinimeshInfo.Config.LicenseKey = "ilk_v1.lic_test.test-key"
}

func TestWebSearchToolRegistersOnlyWhenEnabled(t *testing.T) {
	cfg := config.Default()
	disabled := New(cfg, store.NewMemoryStore())
	if _, ok := disabled.Definition("web.search"); ok {
		t.Fatal("web.search should not register when disabled")
	}
	if _, ok := disabled.Definition("weather.lookup"); ok {
		t.Fatal("weather.lookup should not register without Info credentials")
	}

	cfg.Tools.Web.Search.Enabled = true
	enabled := New(cfg, store.NewMemoryStore())
	if _, ok := enabled.Definition("web.search"); !ok {
		t.Fatal("web.search should register when enabled")
	}
	if _, ok := enabled.Definition("weather.lookup"); ok {
		t.Fatal("weather.lookup must not register from the web-search toggle alone")
	}

	cfg.Tools.Web.Search.Enabled = false
	configureTestInfoCredentials(&cfg)
	configured := New(cfg, store.NewMemoryStore())
	if _, ok := configured.Definition("weather.lookup"); !ok {
		t.Fatal("weather.lookup should register when Info credentials are configured")
	}
	if _, ok := enabled.Definition("info.query"); ok {
		t.Fatal("legacy info.query must not remain registered")
	}
}

func TestWeatherLookupDegradesWithoutUsableInfoClient(t *testing.T) {
	cfg := config.Default()
	configureTestInfoCredentials(&cfg)
	cfg.Plugins.Entries.InfinimeshInfo.Config.BaseURL = "not-an-absolute-url"
	hub := New(cfg, store.NewMemoryStore())

	// The client constructor fails on the base URL; the tool must surface a
	// clean unavailability error instead of dereferencing a typed-nil client.
	_, err := hub.Execute(context.Background(), "weather.lookup", map[string]any{"location": "Shanghai"}, "s", "run")
	if err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("expected adapter-unavailable error, got %v", err)
	}
}

func TestWebSearchToolRejectsLegacyProvider(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.Web.Search.Enabled = true
	cfg.Tools.Web.Search.Provider = "parallel-free"
	hub := New(cfg, store.NewMemoryStore())

	_, err := hub.Execute(context.Background(), "web.search", map[string]any{"query": "sparkclaw search"}, "s", "run")
	if err == nil || !strings.Contains(err.Error(), "unsupported web search provider") {
		t.Fatalf("legacy provider should fail explicitly, got %v", err)
	}
}

func TestWebSearchToolExecutesInfinimeshInfoAdapter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/info/tokens/issue":
			if r.Header.Get("Authorization") != "Bearer ilk_v1.lic_test.test-key" {
				t.Error("unexpected token authorization")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"epoch": time.Now().UTC().Format("2006-01-02"),
				"issued_tokens": []map[string]any{{
					"type": "info.basic", "token_mode": "internal_opaque", "token": "anonymous-token",
					"expires_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
				}},
				"quota_remaining": map[string]int{"info.basic": 9},
			})
		case "/v1/info/query":
			if r.Header.Get("Authorization") != "PrivateToken anonymous-token" {
				t.Error("unexpected query authorization")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"request_id": r.Header.Get("X-Request-Id"),
				"status":     "ok",
				"answer_context": map[string]any{
					"summary":   "Infinimesh summary",
					"key_facts": []map[string]any{{"claim": "claim", "confidence": "high", "sources": []string{"src-1"}}},
					"freshness": map[string]any{"status": "current", "staleness_risk": "low"},
				},
				"sources": []map[string]any{{
					"id": "src-1", "title": "Official source", "url": "https://example.test/official",
					"source_type": "official_documentation", "retrieved_at": "2026-08-14T00:00:00Z", "authority_score": 0.9, "snippets": []string{"bounded evidence"},
				}},
				"usage": map[string]any{"cost_credits": 1, "token_type": "info.basic"},
			})
		default:
			t.Error("unexpected Infinimesh Info path")
		}
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.Tools.Web.Search.Enabled = true
	info := &cfg.Plugins.Entries.InfinimeshInfo.Config
	info.BaseURL = server.URL
	info.LicenseID = "lic_test"
	info.LicenseKey = "ilk_v1.lic_test.test-key"
	info.MaxAttempts = 1
	hub := New(cfg, store.NewMemoryStore())

	result, err := hub.Execute(context.Background(), "web.search", map[string]any{"query": "sparkclaw search"}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	out := result.Output.(map[string]any)
	if out["schema_version"] != websearch.InfoResultSchemaVersion || out["status"] != "ok" || out["provider"] != "infinimesh-info" || out["request_id"] == "" || out["retrieved_at"] == "" || out["untrusted"] != true {
		t.Fatalf("unexpected web search output: %#v", out)
	}
	if aggregate := out["aggregate"].(websearch.Aggregate); aggregate.Summary != "Infinimesh summary" || len(aggregate.Facts) != 1 || aggregate.Facts[0].Claim != "claim" {
		t.Fatalf("web search did not preserve the Info aggregate: %#v", out)
	}
	if sources := out["sources"].([]websearch.Source); len(sources) != 1 || sources[0].Snippets[0] != "bounded evidence" {
		t.Fatalf("web search did not preserve Info source snippets: %#v", out)
	}
	for _, removed := range []string{"summary", "answer", "count", "results", "key_facts", "citations"} {
		if _, exists := out[removed]; exists {
			t.Fatalf("new producer wrote legacy field %q: %#v", removed, out)
		}
	}

}
