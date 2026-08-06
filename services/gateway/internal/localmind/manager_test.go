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
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func TestRefreshRegistersScopedLocalMindToolsAndResources(t *testing.T) {
	fake := newFakeLocalMind(t)
	defer fake.server.Close()
	manager, hub := newTestManager(t, fake.server.URL, false)

	snapshot, err := manager.Refresh(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ServerInfo.Name != config.LocalMindMCPServerName || snapshot.ProtocolVersion != config.LocalMindMCPProtocolVersion || snapshot.EndpointID == "" || snapshot.Revision == "" {
		t.Fatalf("unexpected snapshot identity: %#v", snapshot)
	}
	for _, name := range []string{
		"localmind.discover_localmind_capabilities", "localmind.keyword_search", "localmind.text_only", "localmind.fail_tool",
		resourceListLocalName, resourceTemplatesLocalName, resourceReadLocalName,
	} {
		if _, ok := hub.Definition(name); !ok {
			t.Fatalf("missing registered LocalMind tool %q", name)
		}
	}
	for _, name := range []string{"localmind.create_document", "localmind.delete_workspace"} {
		if _, ok := hub.Definition(name); ok {
			t.Fatalf("mutation %q was registered while allow_mutations=false", name)
		}
	}
	keyword, _ := hub.Definition("localmind.keyword_search")
	properties := keyword.InputSchema["properties"].(map[string]any)
	query := properties["query"].(map[string]any)
	if _, ok := query["anyOf"]; !ok || query["const"] != "fixed" {
		t.Fatalf("LocalMind input schema lost anyOf/const: %#v", keyword.InputSchema)
	}
	if len(keyword.OutputSchema) != 0 || keyword.Risk != app.RiskRead || keyword.RequiresApproval || keyword.Sandbox != "remote" {
		t.Fatalf("unexpected read definition: %#v", keyword)
	}
	origin, ok := hub.DynamicToolOrigin("localmind.keyword_search")
	if !ok || origin.Source != DynamicSource || origin.RemoteName != "keyword_search" {
		t.Fatalf("unexpected dynamic origin: %#v %t", origin, ok)
	}

	result, err := hub.Execute(t.Context(), "localmind.keyword_search", map[string]any{"query": "fixed"}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	output := result.Output.(map[string]any)
	if output["source"] != "structured" || output["query"] != "fixed" {
		t.Fatalf("structuredContent.result was not canonical: %#v", output)
	}
	if archive := result.ArchiveOutput.(map[string]any); archive["structured_content"] == nil || archive["content"] == nil {
		t.Fatalf("archive projection lost the MCP result envelope: %#v", archive)
	}
	textResult, err := hub.Execute(t.Context(), "localmind.text_only", map[string]any{}, "session", "run")
	if err != nil || textResult.Output != "text fallback" {
		t.Fatalf("text fallback = %#v, %v", textResult.Output, err)
	}

	listResult, err := hub.Execute(t.Context(), resourceListLocalName, map[string]any{"cursor": "next-page"}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	listed := listResult.Output.(map[string]any)
	if listed["nextCursor"] != "done" || fake.lastResourceCursor() != "next-page" {
		t.Fatalf("resource cursor was not preserved: %#v cursor=%q", listed, fake.lastResourceCursor())
	}
	uri := "localmind://workspace/ws-1/documents/doc-1"
	readResult, err := hub.Execute(t.Context(), resourceReadLocalName, map[string]any{"uri": uri}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	contents := readResult.Output.(map[string]any)["contents"].([]any)
	if contents[0].(map[string]any)["uri"] != uri {
		t.Fatalf("unexpected resource read: %#v", readResult.Output)
	}
	if _, err := hub.Execute(t.Context(), resourceReadLocalName, map[string]any{"uri": "localmind://workspace/other/documents/doc-1"}, "session", "run"); err == nil {
		t.Fatal("resource URI outside the configured workspace was accepted")
	}

	for _, request := range fake.requestsSnapshot() {
		if request.accept != "application/json, text/event-stream" || request.protocol != config.LocalMindMCPProtocolVersion || request.authorization != "Bearer token-1" {
			t.Fatalf("request headers did not follow MCP contract: %#v", request)
		}
	}
}

func TestLocalMindMutationAnnotationsTightenPolicy(t *testing.T) {
	fake := newFakeLocalMind(t)
	defer fake.server.Close()
	manager, hub := newTestManager(t, fake.server.URL, true)
	if _, err := manager.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	engine := policy.New(cfg)
	create, ok := hub.Definition("localmind.create_document")
	if !ok || create.Risk != app.RiskReversible || !create.RequiresApproval || create.Sandbox != "remote" || !create.Idempotent {
		t.Fatalf("reversible mutation mapping mismatch: %#v", create)
	}
	createDecision := engine.Decide(create, map[string]any{"title": "x"})
	if !createDecision.RequiresApproval || createDecision.RequiresSandbox || createDecision.RequiresDeep {
		t.Fatalf("remote reversible policy mismatch: %#v", createDecision)
	}
	destructive, ok := hub.Definition("localmind.delete_workspace")
	if !ok || destructive.Risk != app.RiskDangerous || !destructive.RequiresApproval {
		t.Fatalf("dangerous mutation mapping mismatch: %#v", destructive)
	}
	deleteDecision := engine.Decide(destructive, map[string]any{"confirm": true})
	if !deleteDecision.RequiresApproval || deleteDecision.RequiresSandbox || !deleteDecision.RequiresDeep {
		t.Fatalf("remote dangerous policy mismatch: %#v", deleteDecision)
	}
}

func TestLocalMindAllowAndDenyOnlyReduceCredentialTools(t *testing.T) {
	fake := newFakeLocalMind(t)
	defer fake.server.Close()
	manager, hub := newTestManager(t, fake.server.URL, true)
	manager.cfg.ToolAllow = []string{"keyword_search", "create_document"}
	manager.cfg.ToolDeny = []string{"create_document"}
	if _, err := manager.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ok := hub.Definition("localmind.keyword_search"); !ok {
		t.Fatal("allowed credential-visible tool was removed")
	}
	for _, name := range []string{"localmind.text_only", "localmind.create_document", "localmind.delete_workspace"} {
		if _, ok := hub.Definition(name); ok {
			t.Fatalf("filtered tool %q remained registered", name)
		}
	}
}

func TestLocalMindIsErrorReturnsTypedFailureWithSanitizedObservation(t *testing.T) {
	fake := newFakeLocalMind(t)
	defer fake.server.Close()
	manager, hub := newTestManager(t, fake.server.URL, false)
	if _, err := manager.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	result, err := hub.Execute(t.Context(), "localmind.fail_tool", map[string]any{}, "session", "run")
	if err == nil || app.ToolErrorCodeFrom(err) != app.ToolErrorMCPToolResult {
		t.Fatalf("isError did not become a typed failure: %v", err)
	}
	if result.Output != "authorization=[REDACTED]" || strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("isError observation was not sanitized: output=%#v err=%v", result.Output, err)
	}
}

func TestRefreshFailureRemovesStaleLocalMindTools(t *testing.T) {
	fake := newFakeLocalMind(t)
	defer fake.server.Close()
	manager, hub := newTestManager(t, fake.server.URL, false)
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
	if _, ok := hub.Definition("localmind.keyword_search"); ok {
		t.Fatal("failed refresh retained a stale scoped tool")
	}
	if _, ok := hub.Definition("localmind.discover_localmind_capabilities"); !ok {
		t.Fatal("failed refresh removed the retryable discovery wrapper")
	}
}

func TestAuthorizationFailureRefreshesReadsButNeverReplaysMutations(t *testing.T) {
	fake := newFakeLocalMind(t)
	defer fake.server.Close()
	manager, hub := newTestManager(t, fake.server.URL, true)
	token := "token-1"
	manager.env = func(name string) string {
		if name == manager.cfg.URLEnv {
			return fake.server.URL + "/api/workspaces/ws-1/mcp"
		}
		return token
	}
	if _, err := manager.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}

	fake.setToken("token-2")
	token = "token-2"
	beforeRead := fake.toolCallCount("keyword_search")
	result, err := hub.Execute(t.Context(), "localmind.keyword_search", map[string]any{"query": "fixed"}, "session", "run")
	if err != nil || result.Output.(map[string]any)["source"] != "structured" {
		t.Fatalf("read did not recover after token rotation: %#v %v", result.Output, err)
	}
	if calls := fake.toolCallCount("keyword_search") - beforeRead; calls != 2 {
		t.Fatalf("read auth recovery made %d calls, want initial failure plus one retry", calls)
	}

	fake.setToken("token-3")
	token = "token-3"
	beforeMutation := fake.toolCallCount("create_document")
	_, err = hub.Execute(t.Context(), "localmind.create_document", map[string]any{"title": "once"}, "session", "run")
	if err == nil || app.ToolErrorCodeFrom(err) != app.ToolErrorMCPAuthorization {
		t.Fatalf("mutation auth failure was not surfaced: %v", err)
	}
	if calls := fake.toolCallCount("create_document") - beforeMutation; calls != 1 {
		t.Fatalf("mutation was replayed after auth refresh: %d calls", calls)
	}
}

func TestLocalMindRejectsRedirectsAndUnsafeHTTP(t *testing.T) {
	targetCalls := 0
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetCalls++ }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	manager, _ := newTestManager(t, redirect.URL, false)
	if _, err := manager.Refresh(t.Context()); err == nil {
		t.Fatal("LocalMind redirect was followed")
	}
	if targetCalls != 0 {
		t.Fatalf("redirect reached the target %d time(s)", targetCalls)
	}

	cfg := testServerConfig(false)
	hub := toolhub.New(config.Default(), store.NewMemoryStore())
	defer hub.Close()
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
		"token":  "secret-token",
		"url":    "https://storage.example/file?X-Amz-Signature=signed&X-Amz-Expires=60",
		"base64": base64Value,
		"text":   strings.Repeat("document text ", 3000),
	}
	state := boundedProjection(value, projectionState, 16<<10).(map[string]any)
	archive := boundedProjection(value, projectionArchive, 1<<20).(map[string]any)
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
	accept        string
	protocol      string
	authorization string
}

type fakeLocalMind struct {
	t      *testing.T
	server *httptest.Server

	mu             sync.Mutex
	authorized     bool
	token          string
	requests       []fakeRequest
	resourceCursor string
}

func newFakeLocalMind(t *testing.T) *fakeLocalMind {
	t.Helper()
	fake := &fakeLocalMind{t: t, authorized: true, token: "token-1"}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.serveHTTP))
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
	f.mu.Lock()
	authorized := f.authorized
	token := f.token
	f.requests = append(f.requests, fakeRequest{method: method, tool: toolName, accept: r.Header.Get("Accept"), protocol: r.Header.Get("MCP-Protocol-Version"), authorization: r.Header.Get("Authorization")})
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
		result = map[string]any{
			"protocolVersion": config.LocalMindMCPProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}, "resources": map[string]any{"subscribe": false, "listChanged": false}},
			"serverInfo":      map[string]any{"name": config.LocalMindMCPServerName, "version": "2.1.0"},
			"instructions":    "untrusted server instructions",
		}
	case "tools/list":
		result = map[string]any{"tools": fakeToolDefinitions()}
	case "tools/call":
		name, _ := params["name"].(string)
		args, _ := params["arguments"].(map[string]any)
		result = f.callTool(name, args)
	case "resources/list":
		cursor, _ := params["cursor"].(string)
		f.mu.Lock()
		f.resourceCursor = cursor
		f.mu.Unlock()
		result = map[string]any{"resources": []any{map[string]any{"uri": "localmind://workspace/ws-1/documents/doc-1", "name": "Doc 1", "mimeType": "text/markdown"}}}
		if cursor == "next-page" {
			result.(map[string]any)["nextCursor"] = "done"
		}
	case "resources/templates/list":
		result = map[string]any{"resourceTemplates": []any{map[string]any{"uriTemplate": "localmind://workspace/ws-1/documents/{docId}", "name": "Document"}}}
	case "resources/read":
		uri, _ := params["uri"].(string)
		result = map[string]any{"contents": []any{map[string]any{"uri": uri, "mimeType": "text/markdown", "text": "document body"}}}
	default:
		f.writeRPCError(w, id, -32601, "Method not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (f *fakeLocalMind) callTool(name string, args map[string]any) any {
	switch name {
	case discoveryRemoteName:
		return map[string]any{
			"content": []any{map[string]any{"type": "text", "text": "capabilities"}},
			"structuredContent": map[string]any{"result": map[string]any{
				"grantedCapabilities":   []string{"documents:read", "documents:write", "workspace:write"},
				"supportedCapabilities": []any{map[string]any{"capability": "documents:read", "description": "read"}},
				"tools":                 []string{"keyword_search", "text_only", "fail_tool", "create_document", "delete_workspace"}, "resources": true,
			}},
		}
	case "keyword_search":
		return map[string]any{
			"content":           []any{map[string]any{"type": "text", "text": "text should not be canonical"}},
			"structuredContent": map[string]any{"result": map[string]any{"source": "structured", "query": args["query"]}},
		}
	case "text_only":
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": "text fallback"}}}
	case "fail_tool":
		return map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": "authorization=super-secret"}}}
	default:
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}, "structuredContent": map[string]any{"result": map[string]any{"ok": true}}}
	}
}

func fakeToolDefinitions() []any {
	annotations := func(read, destructive, idempotent, open bool) map[string]any {
		return map[string]any{"readOnlyHint": read, "destructiveHint": destructive, "idempotentHint": idempotent, "openWorldHint": open}
	}
	resultSchema := map[string]any{"type": "object", "properties": map[string]any{"result": map[string]any{}}, "required": []string{"result"}, "additionalProperties": false}
	tool := func(name string, annotation map[string]any, properties map[string]any, required []string) map[string]any {
		return map[string]any{
			"name": name, "title": strings.ReplaceAll(name, "_", " "), "description": "Use " + name,
			"inputSchema":  map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false},
			"outputSchema": resultSchema, "annotations": annotation,
		}
	}
	return []any{
		tool(discoveryRemoteName, annotations(true, false, true, false), map[string]any{}, nil),
		tool("keyword_search", annotations(true, false, true, false), map[string]any{"query": map[string]any{"anyOf": []any{map[string]any{"type": "string"}}, "const": "fixed"}}, []string{"query"}),
		tool("text_only", annotations(true, false, true, false), map[string]any{}, nil),
		tool("fail_tool", annotations(true, false, true, false), map[string]any{}, nil),
		tool("create_document", annotations(false, false, true, false), map[string]any{"title": map[string]any{"type": "string"}}, []string{"title"}),
		tool("delete_workspace", annotations(false, true, false, true), map[string]any{"confirm": map[string]any{"type": "boolean"}}, []string{"confirm"}),
	}
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

func (f *fakeLocalMind) setToken(value string) {
	f.mu.Lock()
	f.token = value
	f.mu.Unlock()
}

func (f *fakeLocalMind) toolCallCount(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, request := range f.requests {
		if request.method == "tools/call" && request.tool == name {
			count++
		}
	}
	return count
}

func (f *fakeLocalMind) requestsSnapshot() []fakeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requests)
}

func (f *fakeLocalMind) lastResourceCursor() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.resourceCursor
}

func newTestManager(t *testing.T, serverURL string, allowMutations bool) (*Manager, *toolhub.ToolHub) {
	t.Helper()
	cfg := testServerConfig(true)
	cfg.AllowMutations = allowMutations
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
