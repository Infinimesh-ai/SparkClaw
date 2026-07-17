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
)

func TestWebSearchToolRegistersOnlyWhenEnabled(t *testing.T) {
	cfg := config.Default()
	disabled := New(cfg, store.NewMemoryStore())
	if _, ok := disabled.Definition("web.search"); ok {
		t.Fatal("web.search should not register when disabled")
	}

	cfg.Tools.Web.Search.Enabled = true
	enabled := New(cfg, store.NewMemoryStore())
	if _, ok := enabled.Definition("web.search"); !ok {
		t.Fatal("web.search should register when enabled")
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
			if r.Header.Get("Authorization") != "Bearer entitlement-proof" {
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
					"key_facts": []map[string]any{{"claim": "claim", "sources": []string{"src-1"}}},
				},
				"sources": []map[string]any{{
					"id": "src-1", "title": "Official source", "url": "https://example.test/official",
					"source_type": "official_documentation", "snippets": []string{"bounded evidence"},
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
	info.EntitlementProof = "entitlement-proof"
	info.DeviceAttestation = "device-attestation"
	info.LicenseProof = "license-proof"
	info.MaxAttempts = 1
	hub := New(cfg, store.NewMemoryStore())

	result, err := hub.Execute(context.Background(), "web.search", map[string]any{"query": "sparkclaw search"}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	out := result.Output.(map[string]any)
	if out["provider"] != "infinimesh-info" || out["answer"] != "Infinimesh summary" || out["count"] != 1 || out["untrusted"] != true {
		t.Fatalf("unexpected web search output: %#v", out)
	}
	citations, ok := out["citations"].([]string)
	if !ok || len(citations) != 1 || citations[0] != "https://example.test/official" {
		t.Fatalf("unexpected citations: %#v", out["citations"])
	}
}
