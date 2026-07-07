package toolhub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
