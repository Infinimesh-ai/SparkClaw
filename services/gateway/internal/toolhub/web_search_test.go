package toolhub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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

func TestWebSearchToolExecutesParallelFreeAdapter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		switch body["method"] {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "sess_toolhub")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      body["id"],
				"result":  map[string]any{"protocolVersion": "2025-06-18"},
			})
		case "notifications/initialized":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "result": map[string]any{}})
		case "tools/call":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      body["id"],
				"result": map[string]any{
					"structuredContent": map[string]any{
						"results": []map[string]any{{
							"title":    "SparkClaw Search",
							"url":      "https://example.test/sparkclaw",
							"excerpts": []string{"SparkClaw search evidence."},
						}},
					},
				},
			})
		default:
			t.Fatalf("unexpected MCP method: %#v", body)
		}
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.Tools.Web.Search.Enabled = true
	cfg.Tools.Web.Search.Provider = "parallel-free"
	cfg.Plugins.Entries.Parallel.Config.WebSearch.BaseURL = server.URL
	hub := New(cfg, store.NewMemoryStore())

	result, err := hub.Execute(context.Background(), "web.search", map[string]any{"query": "sparkclaw search"}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	out := result.Output.(map[string]any)
	if out["provider"] != "parallel-free" || out["answer"] == "" || out["count"] != 1 || out["untrusted"] != true {
		t.Fatalf("unexpected web search output: %#v", out)
	}
}

func TestWebSearchToolExecutesInfinimeshInfoAdapter(t *testing.T) {
	var parallelHits atomic.Int32
	parallelTrap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		parallelHits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer parallelTrap.Close()
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
	cfg.Plugins.Entries.Parallel.Config.WebSearch.BaseURL = parallelTrap.URL
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
	if parallelHits.Load() != 0 {
		t.Fatal("infinimesh info provider called the Parallel endpoint")
	}
}
