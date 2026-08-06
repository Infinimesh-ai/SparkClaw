package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"
)

type recordedRequest struct {
	Method          string
	ProtocolVersion string
	SessionID       string
}

func TestRefreshDiscoversNamespacedToolsAndResources(t *testing.T) {
	var mu sync.Mutex
	requests := []recordedRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var request struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		mu.Lock()
		requests = append(requests, recordedRequest{Method: request.Method, ProtocolVersion: r.Header.Get("MCP-Protocol-Version"), SessionID: r.Header.Get("Mcp-Session-Id")})
		mu.Unlock()
		if request.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
		switch request.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "session-1")
			response["result"] = map[string]any{
				"protocolVersion": ProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}, "resources": map[string]any{}},
				"serverInfo":      map[string]any{"name": "fixture-server", "version": "1.0.0"},
			}
		case "tools/list":
			if request.Params["cursor"] == "page-2" {
				response["result"] = map[string]any{"tools": []any{map[string]any{
					"name": "second", "description": "second tool", "inputSchema": map[string]any{"type": "object"},
				}}}
			} else {
				response["result"] = map[string]any{
					"nextCursor": "page-2",
					"tools": []any{map[string]any{
						"name": "first", "title": "First", "description": "first tool",
						"inputSchema":  map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}},
						"outputSchema": map[string]any{"type": "object", "required": []any{"answer"}},
						"annotations":  map[string]any{"readOnlyHint": true, "custom": "kept"},
					}},
				}
			}
		case "resources/list":
			response["result"] = map[string]any{"resources": []any{map[string]any{
				"uri": "localmind://memory/1", "name": "Memory", "mimeType": "application/json", "annotations": map[string]any{"audience": []any{"assistant"}},
			}}}
		case "resources/templates/list":
			response["result"] = map[string]any{"resourceTemplates": []any{map[string]any{
				"uriTemplate": "localmind://memory/{id}", "name": "Memory by id", "annotations": map[string]any{"priority": 0.8},
			}}}
		default:
			t.Fatalf("unexpected method %q", request.Method)
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client, err := New(Config{
		Endpoint: server.URL, BearerToken: "test-token", Namespace: "mcp.localmind", ExpectedServerName: "fixture-server",
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := client.Refresh(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(discovery.Tools) != 2 || discovery.Tools[0].LocalName != "mcp.localmind.first" || discovery.Tools[0].RemoteName != "first" {
		t.Fatalf("unexpected tools: %#v", discovery.Tools)
	}
	if discovery.Tools[0].Tool.Annotations["custom"] != "kept" || discovery.Tools[0].Tool.OutputSchema["type"] != "object" {
		t.Fatalf("tool annotations/output schema were lost: %#v", discovery.Tools[0].Tool)
	}
	if len(discovery.Resources) != 1 || len(discovery.ResourceTemplates) != 1 {
		t.Fatalf("resource discovery missing: %#v", discovery)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.ContainsFunc(requests, func(request recordedRequest) bool {
		return request.Method == "tools/list" && request.ProtocolVersion == ProtocolVersion && request.SessionID == "session-1"
	}) {
		t.Fatalf("negotiated headers were not sent: %#v", requests)
	}
	cached, ok := client.Discovery()
	if !ok || cached.Tools[0].LocalName != discovery.Tools[0].LocalName {
		t.Fatalf("discovery cache missing: %#v %t", cached, ok)
	}
}

func TestCallToolParsesSSEResultWithoutCollapsingBusinessError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"not awaiting approval\"}],\"structuredContent\":{\"result\":{\"status\":409}},\"isError\":true}}\n\n"))
	}))
	defer server.Close()
	client, err := New(Config{Endpoint: server.URL}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.CallTool(t.Context(), "approve_plan", map[string]any{"taskId": "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || result.StructuredContent["result"].(map[string]any)["status"] != float64(409) {
		t.Fatalf("business error or structuredContent was lost: %#v", result)
	}
}

func TestClientReturnsTypedHTTPAndServerIdentityErrors(t *testing.T) {
	unauthorized := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid token"}`))
	}))
	defer unauthorized.Close()
	client, err := New(Config{Endpoint: unauthorized.URL}, unauthorized.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ListTools(t.Context(), "")
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || !httpErr.Unauthorized() {
		t.Fatalf("expected typed unauthorized error, got %T %v", err, err)
	}

	mismatch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		if request["method"] == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": map[string]any{
			"protocolVersion": ProtocolVersion, "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "wrong", "version": "1"},
		}})
	}))
	defer mismatch.Close()
	client, err = New(Config{Endpoint: mismatch.URL, ExpectedServerName: "expected"}, mismatch.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Initialize(t.Context())
	var identityErr *UnexpectedServerError
	if !errors.As(err, &identityErr) {
		t.Fatalf("expected server identity error, got %T %v", err, err)
	}
}

func TestWaitForIdleTimeoutExceedsRemoteTimeout(t *testing.T) {
	client, err := New(Config{Endpoint: "http://127.0.0.1:8790", RequestTimeout: 5 * time.Second, LongCallGrace: 7 * time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := client.toolCallTimeout("wait_for_idle", map[string]any{"timeoutSeconds": 12}); got != 19*time.Second {
		t.Fatalf("wait_for_idle timeout = %s", got)
	}
	if got := client.toolCallTimeout("get_session", nil); got != 5*time.Second {
		t.Fatalf("ordinary timeout = %s", got)
	}
}

func TestNamespacedToolNameRejectsInvalidRemoteName(t *testing.T) {
	if _, err := NamespacedToolName("mcp.happy", "bad name"); err == nil {
		t.Fatal("invalid local tool name was accepted")
	}
	if got, err := NamespacedToolName("mcp.happy", "list_tasks"); err != nil || got != "mcp.happy.list_tasks" {
		t.Fatalf("name = %q, err = %v", got, err)
	}
}

func TestBoundedContextPreservesEarlierCallerDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	bounded, boundedCancel := boundedContext(ctx, time.Minute)
	defer boundedCancel()
	deadline, ok := bounded.Deadline()
	if !ok || time.Until(deadline) > time.Second {
		t.Fatalf("caller deadline was not preserved: %v %t", deadline, ok)
	}
}
