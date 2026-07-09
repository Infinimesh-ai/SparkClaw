package websearch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

func TestParallelFreeAdapterHandlesMCPToolCall(t *testing.T) {
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		method, _ := body["method"].(string)
		requests = append(requests, method)
		w.Header().Set("Content-Type", "application/json")
		if method == "initialize" {
			w.Header().Set("Mcp-Session-Id", "sess_test")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      body["id"],
				"result": map[string]any{
					"protocolVersion": mcpProtocolVersion,
				},
			})
			return
		}
		if method == "notifications/initialized" {
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "result": map[string]any{}})
			return
		}
		if method != "tools/call" {
			t.Fatalf("unexpected MCP method %q", method)
		}
		if got := r.Header.Get("Mcp-Session-Id"); got != "sess_test" {
			t.Fatalf("session header = %q", got)
		}
		params := body["params"].(map[string]any)
		if params["name"] != "web_search" {
			t.Fatalf("tool name missing: %#v", params)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      body["id"],
			"result": map[string]any{
				"structuredContent": map[string]any{
					"search_id": "search_1",
					"results": []map[string]any{{
						"title":        "SparkClaw Search",
						"url":          "https://example.test/sparkclaw",
						"excerpts":     []string{"SparkClaw supports web search."},
						"publish_date": "2026-06-24",
					}},
				},
			},
		})
	}))
	defer server.Close()

	adapter := NewParallelFreeAdapter(config.ParallelWebSearchConfig{BaseURL: server.URL}, server.Client())
	result, err := adapter.Search(context.Background(), Request{Query: "sparkclaw search"})
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 3 || requests[0] != "initialize" || requests[1] != "notifications/initialized" || requests[2] != "tools/call" {
		t.Fatalf("unexpected MCP request sequence: %#v", requests)
	}
	if result.Provider != "parallel-free" || result.Count != 1 || result.Results[0].URL != "https://example.test/sparkclaw" || result.Results[0].Snippet == "" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestParallelFreeAdapterHandlesSSEToolPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "text/event-stream")
		switch body["method"] {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "sess_sse")
			_, _ = w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"id\":\"" + body["id"].(string) + "\",\"result\":{\"protocolVersion\":\"" + mcpProtocolVersion + "\"}}\n\n"))
		case "notifications/initialized":
			_, _ = w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"result\":{}}\n\n"))
		case "tools/call":
			_, _ = w.Write([]byte("event: message\n"))
			_, _ = w.Write([]byte("data: {\"jsonrpc\":\"2.0\",\"id\":\"" + body["id"].(string) + "\",\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"{\\\"results\\\":[{\\\"title\\\":\\\"SSE Result\\\",\\\"url\\\":\\\"https://example.test/sse\\\",\\\"excerpts\\\":[\\\"SSE excerpt\\\"]}]}\"}]}}\n\n"))
		}
	}))
	defer server.Close()

	adapter := NewParallelFreeAdapter(config.ParallelWebSearchConfig{BaseURL: server.URL}, server.Client())
	result, err := adapter.Search(context.Background(), Request{Query: "sse search"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Count != 1 || result.Results[0].Title != "SSE Result" {
		t.Fatalf("unexpected SSE result: %#v", result)
	}
}

func TestParallelFreeAdapterAppliesFreshnessToSearchQuery(t *testing.T) {
	var capturedQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch body["method"] {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "sess_fresh")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      body["id"],
				"result":  map[string]any{"protocolVersion": mcpProtocolVersion},
			})
		case "notifications/initialized":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "result": map[string]any{}})
		case "tools/call":
			params := body["params"].(map[string]any)
			arguments := params["arguments"].(map[string]any)
			capturedQuery = arguments["search_queries"].([]any)[0].(string)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      body["id"],
				"result": map[string]any{
					"structuredContent": map[string]any{
						"results": []map[string]any{},
					},
				},
			})
		default:
			t.Fatalf("unexpected MCP method %q", body["method"])
		}
	}))
	defer server.Close()

	adapter := NewParallelFreeAdapter(config.ParallelWebSearchConfig{BaseURL: server.URL}, server.Client())
	result, err := adapter.Search(context.Background(), Request{Query: "台风巴威 登陆信息", Freshness: "latest"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(capturedQuery, "最新") || !strings.Contains(capturedQuery, "当前") {
		t.Fatalf("fresh search query should preserve recency intent, got %q", capturedQuery)
	}
	if !strings.Contains(result.Query, "最新") {
		t.Fatalf("result query should report the actual freshened query, got %q", result.Query)
	}
}
