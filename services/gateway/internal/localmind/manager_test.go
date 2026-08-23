package localmind

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpclient"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpsafety"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func TestRefreshRegistersExactlyThreeLocalMindTaskTools(t *testing.T) {
	fake := newFakeLocalMind(t)
	manager, hub := newTestManager(t, fake.server.URL)

	snapshot, err := manager.Refresh(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ServerInfo.Name != config.LocalMindMCPServerName || snapshot.ProtocolVersion != config.LocalMindMCPProtocolVersion || snapshot.EndpointID == "" || snapshot.Revision == "" {
		t.Fatalf("unexpected snapshot identity: %#v", snapshot)
	}
	if !slices.Equal(snapshot.RemoteToolNames, []string{delegateRemoteName, getTaskRemoteName, controlRemoteName}) ||
		!slices.Equal(snapshot.RegisteredToolNames, []string{delegateLocalName, getTaskLocalName, cancelLocalName}) {
		t.Fatalf("unexpected LocalMind task snapshot: %#v", snapshot)
	}
	for _, name := range snapshot.RegisteredToolNames {
		if _, ok := hub.Definition(name); !ok {
			t.Fatalf("missing registered LocalMind tool %q", name)
		}
	}
	for _, obsolete := range []string{
		"localmind.discover_localmind_capabilities", "localmind.resources.list", "localmind.resources.templates.list", "localmind.resources.read",
	} {
		if _, ok := hub.Definition(obsolete); ok {
			t.Fatalf("obsolete LocalMind tool %q remained registered", obsolete)
		}
	}
	dynamic := []string{}
	for _, definition := range hub.Definitions() {
		if origin, ok := hub.DynamicToolOrigin(definition.Name); ok && origin.Source == DynamicSource {
			dynamic = append(dynamic, definition.Name)
		}
	}
	if !slices.Equal(dynamic, []string{cancelLocalName, delegateLocalName, getTaskLocalName}) {
		t.Fatalf("LocalMind registered %d tools instead of exactly three: %v", len(dynamic), dynamic)
	}
	for _, request := range fake.requestsSnapshot() {
		if request.accept != "application/json, text/event-stream" || request.protocol != config.LocalMindMCPProtocolVersion || request.authorization != "Bearer token-1" {
			t.Fatalf("request headers did not follow MCP contract: %#v", request)
		}
	}
}

func TestLocalMindTaskToolPolicyIsConservativeWithoutWorkflowPlanning(t *testing.T) {
	fake := newFakeLocalMind(t)
	manager, hub := newTestManager(t, fake.server.URL)
	if _, err := manager.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	engine := policy.New(config.Default())
	for _, name := range []string{delegateLocalName, cancelLocalName} {
		definition, ok := hub.Definition(name)
		if !ok || definition.Risk != app.RiskDangerous || !definition.RequiresApproval || definition.Sandbox != "remote" || !definition.Idempotent {
			t.Fatalf("dangerous LocalMind definition mismatch for %s: %#v", name, definition)
		}
		decision := engine.Decide(definition, map[string]any{"request": "test"}, app.PolicyExecutionContext{})
		if !decision.RequiresApproval || decision.RequiresSandbox || !decision.RequiresDeep {
			t.Fatalf("dangerous LocalMind policy mismatch for %s: %#v", name, decision)
		}
	}
	getDefinition, ok := hub.Definition(getTaskLocalName)
	if !ok || getDefinition.Risk != app.RiskRead || getDefinition.RequiresApproval || !getDefinition.Idempotent {
		t.Fatalf("LocalMind get definition mismatch: %#v", getDefinition)
	}
}

func TestLocalMindTaskWrappersTranslateArgumentsAndGenerateStableKeys(t *testing.T) {
	fake := newFakeLocalMind(t)
	fake.setToolResult(delegateRemoteName, taskToolResult("task-1", "queued", false, "v1"))
	fake.setToolResult(getTaskRemoteName, taskToolResult("task-1", "approval_required", false, "v2"))
	fake.setToolResult(controlRemoteName, taskToolResult("task-1", "cancelled", true, "v3"))
	manager, hub := newTestManager(t, fake.server.URL)
	if _, err := manager.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}

	delegateArgs := map[string]any{"request": "Prepare a class age table", "document_ids": []any{"doc-1"}}
	for range 2 {
		result, err := hub.Execute(t.Context(), delegateLocalName, delegateArgs, "session-1", "run-1")
		if err != nil || result.Output.(map[string]any)["taskId"] != "task-1" {
			t.Fatalf("delegate result=%#v err=%v", result.Output, err)
		}
	}
	delegateCalls := fake.toolRequests(delegateRemoteName)
	if len(delegateCalls) != 2 {
		t.Fatalf("delegate calls=%d", len(delegateCalls))
	}
	firstKey := stringValue(delegateCalls[0].arguments["idempotencyKey"])
	if firstKey == "" || firstKey != stringValue(delegateCalls[1].arguments["idempotencyKey"]) || strings.Contains(firstKey, "session-1") {
		t.Fatalf("delegate idempotency key is not stable and opaque: %q %q", firstKey, delegateCalls[1].arguments["idempotencyKey"])
	}
	if delegateCalls[0].arguments["request"] != delegateArgs["request"] || !slices.Equal(delegateCalls[0].arguments["documentIds"].([]any), []any{"doc-1"}) {
		t.Fatalf("delegate arguments were not translated exactly: %#v", delegateCalls[0].arguments)
	}

	getResult, err := hub.Execute(t.Context(), getTaskLocalName, map[string]any{
		"task_id": "task-1", "known_state_version": "v1", "wait_ms": 500,
	}, "session-1", "run-1")
	if err != nil || getResult.Output.(map[string]any)["status"] != "approval_required" {
		t.Fatalf("get result=%#v err=%v", getResult.Output, err)
	}
	getCall := fake.toolRequests(getTaskRemoteName)[0]
	if getCall.arguments["taskId"] != "task-1" || getCall.arguments["knownStateVersion"] != "v1" || getCall.arguments["waitMs"] != float64(500) {
		t.Fatalf("get arguments were not translated exactly: %#v", getCall.arguments)
	}

	cancelResult, err := hub.Execute(t.Context(), cancelLocalName, map[string]any{"task_id": "task-1", "reason": "Owner cancelled"}, "session-1", "run-1")
	if err != nil || cancelResult.Output.(map[string]any)["status"] != "cancelled" {
		t.Fatalf("cancel result=%#v err=%v", cancelResult.Output, err)
	}
	cancelCall := fake.toolRequests(controlRemoteName)[0]
	if cancelCall.arguments["taskId"] != "task-1" || cancelCall.arguments["action"] != "cancel" || cancelCall.arguments["reason"] != "Owner cancelled" || stringValue(cancelCall.arguments["idempotencyKey"]) == "" {
		t.Fatalf("cancel arguments were not translated exactly: %#v", cancelCall.arguments)
	}
}

func TestLocalMindAuthorizationFailureNeverReplaysDelegate(t *testing.T) {
	fake := newFakeLocalMind(t)
	manager, hub := newTestManager(t, fake.server.URL)
	if _, err := manager.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	fake.setAuthorized(false)
	_, err := hub.Execute(t.Context(), delegateLocalName, map[string]any{"request": "read only task"}, "session", "run")
	if err == nil || app.ToolErrorCodeFrom(err) != app.ToolErrorMCPAuthorization {
		t.Fatalf("delegate authorization failure was not typed: %v", err)
	}
	if calls := len(fake.toolRequests(delegateRemoteName)); calls != 1 {
		t.Fatalf("delegate was replayed after authorization failure: %d calls", calls)
	}
}

func TestRefreshFailureRemovesAllStaleLocalMindTools(t *testing.T) {
	fake := newFakeLocalMind(t)
	manager, hub := newTestManager(t, fake.server.URL)
	if _, err := manager.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	fake.setAuthorized(false)
	if _, err := manager.Refresh(t.Context()); err == nil || app.ToolErrorCodeFrom(err) != app.ToolErrorMCPAuthorization {
		t.Fatalf("authorization failure was not typed: %v", err)
	}
	if _, ok := manager.Snapshot(); ok {
		t.Fatal("failed refresh retained a stale snapshot")
	}
	for _, name := range []string{delegateLocalName, getTaskLocalName, cancelLocalName} {
		if _, ok := hub.Definition(name); ok {
			t.Fatalf("failed refresh retained stale tool %q", name)
		}
	}
}

func TestRefreshRejectsAnythingOutsideExactTaskContract(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*fakeLocalMind)
		want   string
	}{
		{name: "old server", mutate: func(fake *fakeLocalMind) { fake.serverName = "localmind-workspace" }, want: "server name mismatch"},
		{name: "resources", mutate: func(fake *fakeLocalMind) { fake.capabilities["resources"] = map[string]any{} }, want: "must not advertise Resources"},
		{name: "extra tool", mutate: func(fake *fakeLocalMind) {
			fake.tools = append(fake.tools, mcpclient.Tool{Name: "discover_localmind_capabilities", InputSchema: objectSchema(nil, nil), OutputSchema: taskOutputSchema(), Annotations: contractAnnotations(true, false, true, false)})
		}, want: "exactly 3 tools"},
		{name: "schema drift", mutate: func(fake *fakeLocalMind) {
			fake.tools[0].InputSchema["additionalProperties"] = true
		}, want: "does not match"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := newFakeLocalMind(t)
			test.mutate(fake)
			manager, hub := newTestManager(t, fake.server.URL)
			if _, err := manager.Refresh(t.Context()); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("invalid LocalMind contract was accepted: %v", err)
			}
			for _, name := range []string{delegateLocalName, getTaskLocalName, cancelLocalName} {
				if _, ok := hub.Definition(name); ok {
					t.Fatalf("failed contract validation registered %q", name)
				}
			}
		})
	}
}

func TestLocalMindToolErrorPreservesSanitizedObservation(t *testing.T) {
	fake := newFakeLocalMind(t)
	fake.setToolResult(delegateRemoteName, mcpclient.ToolResult{
		IsError: true, Content: []mcpclient.ContentBlock{{"type": "text", "text": "authorization=super-secret"}},
	})
	manager, hub := newTestManager(t, fake.server.URL)
	if _, err := manager.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	result, err := hub.Execute(t.Context(), delegateLocalName, map[string]any{"request": "test"}, "session", "run")
	if err == nil || app.ToolErrorCodeFrom(err) != app.ToolErrorMCPToolResult {
		t.Fatalf("isError did not become a typed failure: %v", err)
	}
	if result.Output != "authorization=[REDACTED]" || strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("isError observation was not sanitized: output=%#v err=%v", result.Output, err)
	}
}

func TestLocalMindRejectsRedirectsAndUnsafeHTTP(t *testing.T) {
	targetCalls := 0
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetCalls++ }))
	t.Cleanup(target.Close)
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirect.Close)
	manager, _ := newTestManager(t, redirect.URL)
	if _, err := manager.Refresh(t.Context()); err == nil {
		t.Fatal("LocalMind redirect was followed")
	}
	if targetCalls != 0 {
		t.Fatalf("redirect reached the target %d time(s)", targetCalls)
	}

	cfg := testServerConfig(false)
	hub := toolhub.New(config.Default(), store.NewMemoryStore())
	t.Cleanup(func() { _ = hub.Close() })
	unsafe, err := New(cfg, hub, nil)
	if err != nil {
		t.Fatal(err)
	}
	unsafe.env = func(name string) string {
		if name == cfg.URLEnv {
			return "http://public.example/api/workspaces/ws-1/mcp"
		}
		return "token"
	}
	if _, err := unsafe.Refresh(t.Context()); err == nil || !strings.Contains(err.Error(), "allow_private_http") {
		t.Fatalf("unsafe public HTTP endpoint was accepted: %v", err)
	}
}

func TestLocalMindResultProjectionRedactsSecretsAndBoundsState(t *testing.T) {
	base64Value := strings.Repeat("QUJD", 2048)
	value := map[string]any{
		"token": "secret-token", "url": "https://storage.example/file?X-Amz-Signature=signed&X-Amz-Expires=60",
		"base64": base64Value, "text": strings.Repeat("document text ", 3000),
	}
	state := mcpsafety.BoundedProjection(value, projectionState, 16<<10).(map[string]any)
	archive := mcpsafety.BoundedProjection(value, projectionArchive, 1<<20).(map[string]any)
	for label, projected := range map[string]map[string]any{"state": state, "archive": archive} {
		raw, _ := json.Marshal(projected)
		text := string(raw)
		if strings.Contains(text, "secret-token") || strings.Contains(text, "X-Amz-Signature") || strings.Contains(text, "signed") {
			t.Fatalf("%s projection leaked a secret or signed URL: %s", label, text)
		}
	}
	if _, ok := state["base64"].(map[string]any); !ok {
		t.Fatalf("large base64 entered state: %#v", state["base64"])
	}
	if archive["base64"] != base64Value {
		t.Fatal("bounded archive did not retain allowed base64 data")
	}
}

type fakeRequest struct {
	method        string
	tool          string
	arguments     map[string]any
	accept        string
	protocol      string
	authorization string
}

type fakeLocalMind struct {
	t      *testing.T
	server *httptest.Server

	mu           sync.Mutex
	authorized   bool
	token        string
	serverName   string
	capabilities map[string]any
	tools        []mcpclient.Tool
	toolResults  map[string]mcpclient.ToolResult
	requests     []fakeRequest
}

func newFakeLocalMind(t *testing.T) *fakeLocalMind {
	t.Helper()
	fake := &fakeLocalMind{
		t: t, authorized: true, token: "token-1", serverName: config.LocalMindMCPServerName,
		capabilities: map[string]any{"tools": map[string]any{"listChanged": false}},
		tools:        fakeTaskToolDefinitions(),
		toolResults: map[string]mcpclient.ToolResult{
			delegateRemoteName: taskToolResult("task-1", "queued", false, "v1"),
			getTaskRemoteName:  taskToolResult("task-1", "running", false, "v2"),
			controlRemoteName:  taskToolResult("task-1", "cancelled", true, "v3"),
		},
	}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.serveHTTP))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakeLocalMind) serveHTTP(w http.ResponseWriter, r *http.Request) {
	var request map[string]any
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		f.t.Errorf("decode request: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	method, _ := request["method"].(string)
	params, _ := request["params"].(map[string]any)
	toolName, _ := params["name"].(string)
	arguments, _ := params["arguments"].(map[string]any)
	f.mu.Lock()
	authorized := f.authorized
	token := f.token
	f.requests = append(f.requests, fakeRequest{
		method: method, tool: toolName, arguments: arguments, accept: r.Header.Get("Accept"),
		protocol: r.Header.Get("MCP-Protocol-Version"), authorization: r.Header.Get("Authorization"),
	})
	f.mu.Unlock()
	if !authorized || r.Header.Get("Authorization") != "Bearer "+token {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "Authentication failed"})
		return
	}
	id, hasID := request["id"]
	if !hasID {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	var result any
	switch method {
	case "initialize":
		f.mu.Lock()
		result = map[string]any{
			"protocolVersion": config.LocalMindMCPProtocolVersion,
			"capabilities":    cloneMap(f.capabilities),
			"serverInfo":      map[string]any{"name": f.serverName, "version": "3.2.1"},
		}
		f.mu.Unlock()
	case "tools/list":
		f.mu.Lock()
		result = map[string]any{"tools": slices.Clone(f.tools)}
		f.mu.Unlock()
	case "tools/call":
		f.mu.Lock()
		result = f.toolResults[toolName]
		f.mu.Unlock()
	default:
		f.writeRPCError(w, id, -32601, "Method not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func fakeTaskToolDefinitions() []mcpclient.Tool {
	contracts := expectedTaskTools()
	names := []string{delegateRemoteName, getTaskRemoteName, controlRemoteName}
	tools := make([]mcpclient.Tool, 0, len(names))
	for _, name := range names {
		contract := contracts[name]
		tools = append(tools, mcpclient.Tool{
			Name: name, InputSchema: contract.InputSchema, OutputSchema: taskOutputSchema(), Annotations: contract.Annotations,
		})
	}
	return tools
}

func taskToolResult(taskID, status string, terminal bool, stateVersion string) mcpclient.ToolResult {
	result := map[string]any{
		"protocolVersion": taskProtocolVersion, "taskId": taskID, "stateVersion": stateVersion,
		"status": status, "terminal": terminal, "phase": "execute", "pollAfterMs": nil,
		"result": map[string]any{"kind": "answer", "answer": "ok"}, "error": nil,
	}
	return mcpclient.ToolResult{
		Content:           []mcpclient.ContentBlock{{"type": "text", "text": status}},
		StructuredContent: map[string]any{"result": result},
	}
}

func cloneMap(value map[string]any) map[string]any {
	raw, _ := json.Marshal(value)
	var cloned map[string]any
	_ = json.Unmarshal(raw, &cloned)
	return cloned
}

func (f *fakeLocalMind) writeRPCError(w http.ResponseWriter, id any, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
}

func (f *fakeLocalMind) setAuthorized(value bool) {
	f.mu.Lock()
	f.authorized = value
	f.mu.Unlock()
}

func (f *fakeLocalMind) setToolResult(name string, result mcpclient.ToolResult) {
	f.mu.Lock()
	f.toolResults[name] = result
	f.mu.Unlock()
}

func (f *fakeLocalMind) requestsSnapshot() []fakeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requests)
}

func (f *fakeLocalMind) toolRequests(name string) []fakeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	requests := []fakeRequest{}
	for _, request := range f.requests {
		if request.method == "tools/call" && request.tool == name {
			requests = append(requests, request)
		}
	}
	return requests
}

func newTestManager(t *testing.T, serverURL string) (*Manager, *toolhub.ToolHub) {
	t.Helper()
	cfg := testServerConfig(true)
	hub := toolhub.New(config.Default(), store.NewMemoryStore())
	t.Cleanup(func() { _ = hub.Close() })
	manager, err := New(cfg, hub, nil)
	if err != nil {
		t.Fatal(err)
	}
	manager.env = func(name string) string {
		switch name {
		case cfg.URLEnv:
			return serverURL + "/api/workspaces/ws-1/mcp"
		case cfg.BearerTokenEnv:
			return "token-1"
		default:
			return ""
		}
	}
	return manager, hub
}

func testServerConfig(allowPrivateHTTP bool) config.MCPServerConfig {
	return config.MCPServerConfig{
		Transport: "streamable-http", URLEnv: "LOCALMIND_MCP_URL", BearerTokenEnv: "LOCALMIND_MCP_TOKEN",
		Namespace: "localmind", ExpectedServerName: config.LocalMindMCPServerName, ProtocolVersion: config.LocalMindMCPProtocolVersion,
		AllowPrivateHTTP: allowPrivateHTTP, RequestTimeoutSeconds: 2, LongCallGraceSeconds: 1,
		MaxResponseBytes: 2 << 20, StateOutputMaxBytes: 16 << 10, ArchiveOutputMaxBytes: 1 << 20, RefreshIntervalSeconds: 30,
	}
}
