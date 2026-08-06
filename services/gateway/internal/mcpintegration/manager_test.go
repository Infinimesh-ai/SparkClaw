package mcpintegration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpclient"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func TestIndependentServersDiscoverAndRegisterWithoutSharedFailure(t *testing.T) {
	t.Setenv("HAPPY_TEAM_MCP_TOKEN", "team-token")
	teamServer := newMCPFixture(t, "happy-team-tasks", []map[string]any{
		{"name": "list_tasks", "description": "List tasks", "inputSchema": map[string]any{"type": "object"}, "annotations": map[string]any{"readOnlyHint": true}},
		{"name": "create_task", "description": "Create task", "inputSchema": map[string]any{"type": "object", "required": []any{"title"}, "properties": map[string]any{"title": map[string]any{"type": "string"}}}},
	}, func(name string) map[string]any {
		if name == "list_tasks" {
			return map[string]any{"content": []any{map[string]any{"type": "text", "text": `{"tasks":[{"id":"task-1"}]}`}}, "isError": false}
		}
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": `{"task":{"id":"task-2"}}`}}, "isError": false}
	})
	defer teamServer.Close()

	cfg := config.Default()
	cfg.MCPServers = map[string]config.MCPServerConfig{
		"happy-tasks": {
			URL: teamServer.URL, TokenEnv: "HAPPY_TEAM_MCP_TOKEN", Namespace: "mcp.happy-tasks", ExpectedServerName: "happy-team-tasks",
			RequestTimeoutSeconds: 2, DiscoveryRefreshSeconds: 60, ResponseBodyMaxBytes: 1 << 20,
		},
		"happy-bridge": {
			URL: "http://127.0.0.1:1/", Namespace: "mcp.happy-bridge", ExpectedServerName: "happy-bridge",
			RequestTimeoutSeconds: 1, DiscoveryRefreshSeconds: 60, ResponseBodyMaxBytes: 1 << 20,
		},
	}
	hub := toolhub.New(cfg, store.NewMemoryStore())
	defer hub.Close()
	manager := New(cfg.MCPServers, hub, teamServer.Client())

	teamStatus, err := manager.Refresh(t.Context(), "happy-tasks")
	if err != nil || teamStatus.State != "connected" || teamStatus.ToolCount != 2 {
		t.Fatalf("team refresh: status=%#v err=%v", teamStatus, err)
	}
	bridgeStatus, err := manager.Refresh(t.Context(), "happy-bridge")
	if err == nil || bridgeStatus.State != "temporarily_unavailable" {
		t.Fatalf("bridge failure was not isolated: status=%#v err=%v", bridgeStatus, err)
	}
	if _, ok := hub.Definition("mcp.happy-tasks.list_tasks"); !ok {
		t.Fatal("team tool disappeared when bridge was unavailable")
	}
	create, ok := hub.Definition("mcp.happy-tasks.create_task")
	if !ok || !create.RequiresApproval || create.Risk != app.RiskReversible {
		t.Fatalf("create_task risk mapping = %#v", create)
	}
	read, _ := hub.Definition("mcp.happy-tasks.list_tasks")
	if read.RequiresApproval || read.Risk != app.RiskRead || read.Annotations["readOnlyHint"] != true {
		t.Fatalf("list_tasks mapping = %#v", read)
	}
	result, err := hub.Execute(t.Context(), "mcp.happy-tasks.list_tasks", map[string]any{}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output.(map[string]any)["tasks"] == nil {
		t.Fatalf("Happy JSON text result was not projected: %#v", result.Output)
	}
}

func TestBusinessErrorIsCheckedAndRetainedAsUntrustedOutput(t *testing.T) {
	server := newMCPFixture(t, "happy-team-tasks", []map[string]any{
		{"name": "approve_plan", "inputSchema": map[string]any{"type": "object"}},
	}, func(string) map[string]any {
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": "Task is not awaiting approval"}}, "isError": true}
	})
	defer server.Close()
	cfg := config.Default()
	cfg.MCPServers = map[string]config.MCPServerConfig{"happy-tasks": {
		URL: server.URL, Namespace: "mcp.happy-tasks", ExpectedServerName: "happy-team-tasks",
		RequestTimeoutSeconds: 2, DiscoveryRefreshSeconds: 60, ResponseBodyMaxBytes: 1 << 20,
	}}
	hub := toolhub.New(cfg, store.NewMemoryStore())
	defer hub.Close()
	manager := New(cfg.MCPServers, hub, server.Client())
	if _, err := manager.Refresh(t.Context(), "happy-tasks"); err != nil {
		t.Fatal(err)
	}
	definition, ok := hub.Definition("mcp.happy-tasks.approve_plan")
	if !ok || len(definition.Capabilities) != 1 || definition.Capabilities[0].Name != app.ToolCapabilityMCPApprovalResolve {
		t.Fatalf("approve_plan was exposed to the ordinary MCP workflow: %#v", definition)
	}
	result, err := hub.Execute(t.Context(), "mcp.happy-tasks.approve_plan", map[string]any{}, "", "")
	if err == nil || app.ToolErrorCodeFrom(err) != app.ToolErrorMCPTool {
		t.Fatalf("business error was not classified: result=%#v err=%v", result, err)
	}
	if result.Output.(map[string]any)["content"] != "Task is not awaiting approval" {
		t.Fatalf("business result was not retained: %#v", result.Output)
	}
}

func TestStructuredContentResultIsCanonicalToolHubOutput(t *testing.T) {
	output := projectToolResult(mcpclient.ToolResult{StructuredContent: map[string]any{
		"result": map[string]any{"documents": []any{"one"}}, "diagnostic": "not canonical",
	}})
	projected, ok := output.(map[string]any)
	if !ok || projected["documents"] == nil || projected["diagnostic"] != nil {
		t.Fatalf("structuredContent.result was not canonical: %#v", output)
	}
}

func TestUnauthorizedStatusDistinguishesTokenSources(t *testing.T) {
	t.Setenv("TEAM_TOKEN", "stale")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	tokenFile := filepath.Join(t.TempDir(), "mcp.token")
	if err := os.WriteFile(tokenFile, []byte("wrong"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.MCPServers = map[string]config.MCPServerConfig{
		"team":   {URL: server.URL, TokenEnv: "TEAM_TOKEN", Namespace: "mcp.team", RequestTimeoutSeconds: 2, DiscoveryRefreshSeconds: 60},
		"bridge": {URL: server.URL, TokenFile: tokenFile, Namespace: "mcp.bridge", RequestTimeoutSeconds: 2, DiscoveryRefreshSeconds: 60},
	}
	hub := toolhub.New(cfg, store.NewMemoryStore())
	defer hub.Close()
	manager := New(cfg.MCPServers, hub, server.Client())
	team, _ := manager.Refresh(t.Context(), "team")
	bridge, _ := manager.Refresh(t.Context(), "bridge")
	if team.ErrorCode != string(app.ToolErrorMCPTokenReissueRequired) || team.ActionRequired != "reissue_mcp_token" {
		t.Fatalf("team 401 status = %#v", team)
	}
	if bridge.ErrorCode != string(app.ToolErrorMCPTokenFileMismatch) || bridge.ActionRequired != "verify_bridge_token_file" {
		t.Fatalf("bridge 401 status = %#v", bridge)
	}
}

func newMCPFixture(t *testing.T, serverName string, tools []map[string]any, call func(string) map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode MCP request: %v", err)
		}
		if request.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
		switch request.Method {
		case "initialize":
			response["result"] = map[string]any{
				"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}},
				"serverInfo": map[string]any{"name": serverName, "version": "1"},
			}
		case "tools/list":
			response["result"] = map[string]any{"tools": tools}
		case "tools/call":
			response["result"] = call(request.Params["name"].(string))
		default:
			t.Fatalf("unexpected MCP method %q", request.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
}

func TestRunRefreshesWithoutBlockingOnUnavailablePeer(t *testing.T) {
	// The refresh worker is asynchronous; this only guards the service lifecycle
	// contract and catches accidental synchronous network work in Run.
	cfg := config.Default()
	cfg.MCPServers = map[string]config.MCPServerConfig{"offline": {
		URL: "http://127.0.0.1:1", Namespace: "mcp.offline", RequestTimeoutSeconds: 1, DiscoveryRefreshSeconds: 1,
	}}
	hub := toolhub.New(cfg, store.NewMemoryStore())
	defer hub.Close()
	manager := New(cfg.MCPServers, hub, nil)
	ctx, cancel := context.WithCancel(t.Context())
	started := time.Now()
	manager.Run(ctx)
	if time.Since(started) > 100*time.Millisecond {
		t.Fatal("Run blocked on discovery")
	}
	cancel()
}
