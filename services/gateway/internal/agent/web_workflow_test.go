package agent

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func TestFastRouterMapsCurrentInternetFactsToOneSearchLeaf(t *testing.T) {
	runtime, _, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	for index, goal := range []string{
		"现在的金价是多少",
		"人民币兑美元实时汇率",
		"上证指数现在多少点",
		"刚结束的比赛比分是多少",
		"今天有什么重大新闻",
	} {
		candidate := app.RouteDecision{
			SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched, CatalogRevision: runtime.capabilities.Revision(),
			CapabilityPath: []app.CapabilityID{"browser", app.CapabilityBrowserInternetSearch},
			Slots:          app.RouteSlots{Operation: app.RouteOperationSearch, FactScope: app.RouteFactScopeCurrentInternet, Query: "model paraphrase"},
			Confidence:     0.95,
		}
		routing, err := runtime.routeIntent(context.Background(), session.ID, fmt.Sprintf("run_live_fact_%d", index), mockIntentRoute(t, goal, candidate))
		if err != nil {
			t.Fatalf("route %q: %v", goal, err)
		}
		if routing.Route.CapabilityPath[1] != app.CapabilityBrowserInternetSearch || routing.Route.Slots.Query != goal || routing.Route.Slots.FactScope != app.RouteFactScopeCurrentInternet {
			t.Fatalf("current fact did not normalize to browser.internet_search: goal=%q route=%#v", goal, routing.Route)
		}
		resolved, err := runtime.profiles.Resolve(runtime.capabilities, routing.Route, "turn")
		if err != nil || resolved.Profile.ID() != app.WorkflowBrowserInternetSearch {
			t.Fatalf("current fact did not resolve the exact search Workflow: goal=%q resolved=%#v err=%v", goal, resolved, err)
		}
	}
}

func TestFastRouterLeavesStaticCommonKnowledgeUnmatched(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	goal := "法国的首都是什么"
	candidate := app.RouteDecision{
		SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteUnmatched, CatalogRevision: runtime.capabilities.Revision(),
		Confidence: 0.96, Reason: "The answer is stable common knowledge and does not require current Internet state.",
	}
	content := mockIntentRoute(t, goal, candidate) + `
MOCK_REACT_RESPONSE:{"type":"final","answer":"巴黎。"}`
	result, err := runtime.HandleMessage(context.Background(), session.ID, content)
	if err != nil {
		t.Fatal(err)
	}
	if result.RouteDecision == nil || result.RouteDecision.Status != app.RouteUnmatched || result.Run.Workflow != nil {
		t.Fatalf("static common knowledge was forced into a Workflow: %#v", result)
	}
	for _, call := range toolCallsForRun(st.ListToolCalls(session.ID), result.Run.ID) {
		if call.Tool == "web.search" {
			t.Fatalf("static common knowledge forced an Internet search: %#v", call)
		}
	}
}

func TestFastRouterKeepsWeatherCardBoundaryNarrow(t *testing.T) {
	runtime, _, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	tests := []struct {
		goal     string
		location string
		leaf     app.CapabilityID
		workflow app.WorkflowID
	}{
		{goal: "杭州今天天气怎么样", location: "杭州", leaf: app.CapabilityBrowserWeather, workflow: app.WorkflowBrowserWeather},
		{goal: "杭州暴雨预警有什么最新消息", leaf: app.CapabilityBrowserInternetSearch, workflow: app.WorkflowBrowserInternetSearch},
		{goal: "比较杭州和上海今天的天气", leaf: app.CapabilityBrowserInternetSearch, workflow: app.WorkflowBrowserInternetSearch},
	}
	for index, test := range tests {
		slots := app.RouteSlots{Operation: app.RouteOperationSearch, FactScope: app.RouteFactScopeCurrentInternet, Query: "model paraphrase"}
		if test.leaf == app.CapabilityBrowserWeather {
			slots = app.RouteSlots{Operation: app.RouteOperationRender, FactScope: app.RouteFactScopeWeatherSnapshot, Location: test.location}
		}
		candidate := app.RouteDecision{
			SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched, CatalogRevision: runtime.capabilities.Revision(),
			CapabilityPath: []app.CapabilityID{"browser", test.leaf}, Slots: slots, Confidence: 0.94,
		}
		routing, err := runtime.routeIntent(context.Background(), session.ID, fmt.Sprintf("run_weather_boundary_%d", index), mockIntentRoute(t, test.goal, candidate))
		if err != nil {
			t.Fatalf("route %q: %v", test.goal, err)
		}
		resolved, err := runtime.profiles.Resolve(runtime.capabilities, routing.Route, "turn")
		if err != nil || resolved.Profile.ID() != test.workflow {
			t.Fatalf("weather boundary selected the wrong Workflow: goal=%q route=%#v resolved=%#v err=%v", test.goal, routing.Route, resolved, err)
		}
	}
}

func TestBrowserWeatherDispatchesOnlyWeatherCardCapability(t *testing.T) {
	runtime, _, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	route := app.RouteDecision{
		SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched, CatalogRevision: runtime.capabilities.Revision(),
		CapabilityPath: []app.CapabilityID{"browser", app.CapabilityBrowserWeather},
		Slots:          app.RouteSlots{Operation: app.RouteOperationRender, FactScope: app.RouteFactScopeWeatherSnapshot, Location: "杭州"},
	}
	dispatch, err := runtime.dispatchMatchedWorkflow(context.Background(), app.AgentRun{ID: "run_weather", SessionID: session.ID, StartedAt: time.Now().UTC()}, route, app.ReturnRoute{Mode: app.ReturnToSource}, "turn")
	if err != nil {
		t.Fatal(err)
	}
	if dispatch.Profile.ID() != app.WorkflowBrowserWeather || len(dispatch.Tools) != 1 || dispatch.Tools[0].Name != "media.render_weather_card" || dispatch.Hint.Capability != app.ToolCapabilityWeatherCard {
		t.Fatalf("browser.weather exposed the wrong Workflow capability: %#v", dispatch)
	}
}

func TestInvalidFastRouteBlocksWithoutWrongWorkflowOrReActFallback(t *testing.T) {
	for _, raw := range []string{
		`{"route":{"schema_version":1,"status":"matched","catalog_revision":"REVISION","capability_path":["browser","browser.gold_price"],"slots":{"operation":"search","fact_scope":"current_internet_state","query":"gold"}},"delivery":{"explicit_external":false}}`,
		`{"route":{"schema_version":1,"status":"matched","catalog_revision":"REVISION","capability_path":["browser","browser.internet_search"],"slots":{"operation":"search","fact_scope":"current_internet_state","query":"gold"},"tool":"web.search"},"delivery":{"explicit_external":false}}`,
	} {
		runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
		content := "现在的金价是多少\nMOCK_INTENT_RESPONSE:" + strings.ReplaceAll(raw, "REVISION", runtime.capabilities.Revision())
		result, err := runtime.HandleMessage(context.Background(), session.ID, content)
		closeRuntime()
		if err != nil {
			t.Fatal(err)
		}
		if result.RouteDecision == nil || result.RouteDecision.Status != app.RouteBlocked || result.Run.Workflow != nil || len(result.ToolCalls) != 0 || hasReActModelCall(st.ListModelCalls(session.ID, result.Run.ID)) {
			t.Fatalf("invalid Fast route degraded into another Workflow: %#v", result)
		}
	}
}

func TestIntentRoutingPromptDefinesLiveFactAndWeatherSemantics(t *testing.T) {
	prompt := intentRoutingSystemPrompt("[]")
	for _, expected := range []string{
		"current gold prices", "exchange rates", "stock or index quotes", "current match results",
		"Stable common knowledge", "browser.weather", "Weather alerts", "multi-location comparisons", "tool", "workflow",
	} {
		if !strings.Contains(strings.ToLower(prompt), strings.ToLower(expected)) {
			t.Fatalf("intent routing prompt is missing %q:\n%s", expected, prompt)
		}
	}
}

func mockIntentRoute(t *testing.T, goal string, route app.RouteDecision) string {
	t.Helper()
	raw, err := json.Marshal(IntentRoutingOutput{Route: route})
	if err != nil {
		t.Fatal(err)
	}
	return goal + "\nMOCK_INTENT_RESPONSE:" + string(raw)
}

func TestBrowserSearchRouteDispatchesRealWebSearchWorkflow(t *testing.T) {
	info := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/info/tokens/issue":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"epoch":           time.Now().UTC().Format("2006-01-02"),
				"issued_tokens":   []map[string]any{{"type": "info.basic", "token_mode": "internal_opaque", "token": "workflow-token", "expires_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339)}},
				"quota_remaining": map[string]int{"info.basic": 9},
			})
		case "/v1/info/query":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"request_id": r.Header.Get("X-Request-Id"), "status": "ok",
				"answer_context": map[string]any{"summary": "Bounded search evidence", "key_facts": []map[string]any{{"claim": "SparkClaw result", "sources": []string{"src-1"}}}},
				"sources":        []map[string]any{{"id": "src-1", "title": "Official source", "url": "https://example.com/source", "source_type": "official_documentation", "snippets": []string{"bounded evidence"}}},
				"usage":          map[string]any{"cost_credits": 1, "token_type": "info.basic"},
			})
		default:
			t.Fatalf("unexpected Infinimesh path: %s", r.URL.Path)
		}
	}))
	defer info.Close()

	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		cfg.config.Tools.Web.Search.Enabled = true
		infoCfg := &cfg.config.Plugins.Entries.InfinimeshInfo.Config
		infoCfg.BaseURL = info.URL
		infoCfg.EntitlementProof = "entitlement-proof"
		infoCfg.DeviceAttestation = "device-attestation"
		infoCfg.LicenseProof = "license-proof"
		infoCfg.MaxAttempts = 1
	})
	defer closeRuntime()

	result, err := runtime.HandleMessage(context.Background(), session.ID, `Search online for SparkClaw architecture
MOCK_REACT_RESPONSE:{"type":"action","tool":"web.search","arguments":{"query":"Search online for SparkClaw architecture"}}`)
	if err != nil {
		t.Fatal(err)
	}
	assertWorkflowClosure(t, result, st, session.ID, app.CapabilityBrowserInternetSearch, app.WorkflowBrowserInternetSearch,
		[]string{"web.search"}, []string{app.ToolCapabilityWebDiscovery})
}

func TestBrowserAutomationRouteDispatchesRealAutomationAdapter(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		cfg.config.Tools.BrowserAutomation.Enabled = true
		cfg.browser = true
	})
	defer closeRuntime()

	result, err := runtime.HandleMessage(context.Background(), session.ID, "Use browser automation to open https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	assertWorkflowClosure(t, result, st, session.ID, app.CapabilityBrowserAutomation, app.WorkflowBrowserAutomation,
		[]string{"browser.list_tabs", "browser.open"}, []string{app.ToolCapabilityBrowserListTabs, app.ToolCapabilityBrowserOpen})
}

func TestDocumentInformationRouteDispatchesRealFileRead(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		if err := os.WriteFile(filepath.Join(cfg.root, "note.txt"), []byte("SparkClaw document information evidence."), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	defer closeRuntime()

	result, err := runtime.HandleMessage(context.Background(), session.ID, `Summarize the document note.txt
MOCK_REACT_RESPONSE:{"type":"action","tool":"files.read","arguments":{"path":"note.txt"}}`)
	if err != nil {
		t.Fatal(err)
	}
	assertWorkflowClosure(t, result, st, session.ID, app.CapabilityDocumentRead, app.WorkflowDocumentRead,
		[]string{"files.read"}, []string{app.ToolCapabilityDocumentRead})
}

func TestDocumentEditPreflightExposesCompatibleEditorAndReturnsOutputCopy(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		writeTestOfficePackage(t, filepath.Join(cfg.root, "note.docx"), "word/document.xml")
	})
	defer closeRuntime()

	route, err := runtime.recognizeCapabilityRoute(session.ID, "turn_document_edit", "Replace a paragraph in note.docx", agentContextSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if route.Status != app.RouteMatched || route.CapabilityPath[1] != app.CapabilityDocumentEdit ||
		route.Facts["document_format"] != app.DocumentFormatDOCX || route.Facts["document_operation"] != "replace_paragraph" ||
		route.Facts["output_path"] != "note-sparkclaw-edit.docx" {
		t.Fatalf("document edit preflight did not freeze format, operation, and output copy: %#v", route)
	}
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC()}
	dispatch, err := runtime.dispatchMatchedWorkflow(context.Background(), run, route, app.ReturnRoute{Mode: app.ReturnToSource}, "turn_document_edit")
	if err != nil {
		t.Fatal(err)
	}
	if len(dispatch.Tools) != 1 || dispatch.Tools[0].Name != "docx.replace_paragraph" {
		t.Fatalf("document edit stage exposed the wrong editor set: %#v", visibleToolNames(dispatch.Tools))
	}
	definition, ok := runtime.tools.Definition("docx.replace_paragraph")
	if !ok {
		t.Fatal("docx editor definition is unavailable")
	}
	call := app.ToolCall{
		ID: "tc_document_edit", SessionID: session.ID, RunID: dispatch.Run.ID, Tool: definition.Name, Status: "completed", Result: map[string]any{"output_path": "note-sparkclaw-edit.docx"},
		WorkflowID: app.WorkflowDocumentEdit, WorkflowNodeID: "document_edit", ScopeRevision: 2, Capability: app.ToolCapabilityDocumentEdit,
	}
	st.SaveToolCall(call)
	outcome, err := adaptWorkflowOutcome(definition, call)
	if err != nil {
		t.Fatal(err)
	}
	assessment := dispatch.Profile.Assess(dispatch.Run.Workflow, outcome)
	if _, err := applyWorkflowOutcome(&dispatch.Run, outcome, assessment); err != nil {
		t.Fatal(err)
	}
	dispatch.Run.State = "completed"
	result := runtime.workflowResultForRun(dispatch.Run, route, dispatch.Run.Workflow.ReturnRoute, "Document copy created.")
	if result == nil || result.Status != app.WorkflowResultSucceeded || len(result.Content.Parts) != 2 {
		t.Fatalf("document edit did not return its output copy: %#v", result)
	}
	filePart := result.Content.Parts[1]
	if filePart.Kind != app.MessagePartFile || filePart.Resource == nil || filePart.Resource.Kind != "workspace_file" || filePart.Resource.Ref != "note-sparkclaw-edit.docx" {
		t.Fatalf("unexpected document output part: %#v", filePart)
	}

	endpoint := app.MessageEndpoint{ID: "endpoint_fake", OwnerID: result.OwnerID, ActorID: result.Authorization.PrincipalID, Kind: app.EndpointKindThirdPartyDevice, ProviderKey: "fake", Status: app.EndpointActive}
	routes := fixedWorkflowResultEndpoint{endpoint: endpoint}
	provider := &capturingWorkflowResultProvider{}
	providers := delivery.NewProviderRegistry()
	if err := providers.Register(provider); err != nil {
		t.Fatal(err)
	}
	workflowResult := *result
	workflowResult.ReturnRoute = app.ReturnRoute{Mode: app.ReturnToEndpoint, EndpointID: endpoint.ID}
	request, deliverResult, err := delivery.RequestFromWorkflowResult(t.Context(), workflowResult, routes)
	if err != nil || !deliverResult {
		t.Fatalf("build document delivery request: deliver=%t err=%v", deliverResult, err)
	}
	if _, err := delivery.NewGateway(routes, providers, nil).Deliver(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if provider.calls != 1 || len(provider.content.Parts) != 2 || provider.content.Parts[1].Kind != app.MessagePartFile {
		t.Fatalf("document result did not reach the provider exactly once: calls=%d content=%#v", provider.calls, provider.content)
	}
}

type fixedWorkflowResultEndpoint struct{ endpoint app.MessageEndpoint }

func (r fixedWorkflowResultEndpoint) Get(context.Context, app.EndpointID) (app.MessageEndpoint, error) {
	return r.endpoint, nil
}

func (r fixedWorkflowResultEndpoint) Resolve(context.Context, app.ReturnRoute) (app.MessageEndpoint, bool, error) {
	return r.endpoint, true, nil
}

type capturingWorkflowResultProvider struct {
	calls   int
	content app.MessageContent
}

func (*capturingWorkflowResultProvider) Key() string { return "fake" }
func (*capturingWorkflowResultProvider) Capabilities() app.DeliveryCapabilities {
	return app.DeliveryCapabilities{Kinds: []app.MessagePartKind{app.MessagePartText, app.MessagePartFile}}
}
func (p *capturingWorkflowResultProvider) Deliver(_ context.Context, endpoint app.MessageEndpoint, request app.DeliveryRequest) (app.DeliveryReceipt, error) {
	p.calls++
	p.content = request.Content
	now := time.Now().UTC()
	return app.DeliveryReceipt{DeliveryID: request.ID, EndpointID: endpoint.ID, Status: app.DeliverySucceeded, AttemptedAt: now, DeliveredAt: &now}, nil
}

func TestClarifyAndBlockedRoutesReturnWithoutFallback(t *testing.T) {
	for _, test := range []struct {
		status app.RouteStatus
		want   app.WorkflowResultStatus
	}{
		{app.RouteClarify, app.WorkflowResultWaiting},
		{app.RouteBlocked, app.WorkflowResultBlocked},
	} {
		runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
		run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, StartedAt: time.Now().UTC()}
		st.SaveRun(run)
		route := app.RouteDecision{
			SchemaVersion: app.RouteDecisionSchemaVersion, Status: test.status, CatalogRevision: runtime.capabilities.Revision(),
			CapabilityPath: []app.CapabilityID{"browser"}, Reason: "more routing information is required",
		}
		result := runtime.completeTerminalRoute(context.Background(), run, "ambiguous request", app.ReturnRoute{Mode: app.ReturnToSource}, route)
		closeRuntime()
		if result.WorkflowResult == nil || result.WorkflowResult.Status != test.want || len(result.ToolCalls) != 0 {
			t.Fatalf("terminal route %q returned the wrong result: %#v", test.status, result)
		}
		if hasReActModelCall(st.ListModelCalls(session.ID, run.ID)) {
			t.Fatalf("terminal route %q entered ReAct fallback", test.status)
		}
	}
}

func TestUnmatchedRouteAloneUsesReActFallback(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	result, err := runtime.HandleMessage(context.Background(), session.ID, "hello, explain this conversation briefly")
	if err != nil {
		t.Fatal(err)
	}
	if result.RouteDecision == nil || result.RouteDecision.Status != app.RouteUnmatched {
		t.Fatalf("ordinary unmatched request did not remain unmatched: %#v", result.RouteDecision)
	}
	if result.WorkflowResult == nil || result.WorkflowResult.Workflow.ID != "react.unmatched" {
		t.Fatalf("unmatched fallback did not produce WorkflowResult: %#v", result.WorkflowResult)
	}
	if !hasReActModelCall(st.ListModelCalls(session.ID, result.Run.ID)) {
		t.Fatalf("unmatched request did not invoke the ReAct fallback: %#v", st.ListModelCalls(session.ID, result.Run.ID))
	}
}

func TestMatchedDispatchFailureReturnsFailedWorkflowResultWithoutFallback(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	result, err := runtime.HandleMessage(context.Background(), session.ID, "Search online for current SparkClaw news")
	if err != nil {
		t.Fatal(err)
	}
	if result.RouteDecision == nil || result.RouteDecision.Status != app.RouteMatched || result.RouteDecision.CapabilityPath[1] != app.CapabilityBrowserInternetSearch {
		t.Fatalf("known request lost its matched route: %#v", result.RouteDecision)
	}
	if result.WorkflowResult == nil || result.WorkflowResult.Status != app.WorkflowResultFailed || result.WorkflowResult.Workflow.ID != app.WorkflowBrowserSearch {
		t.Fatalf("matched setup failure did not return the leaf WorkflowResult: %#v", result.WorkflowResult)
	}
	if hasReActModelCall(st.ListModelCalls(session.ID, result.Run.ID)) {
		t.Fatalf("matched workflow failure entered ReAct fallback: %#v", st.ListModelCalls(session.ID, result.Run.ID))
	}
}

func TestExistingTerminalRouteKeepsItsWorkflowIdentity(t *testing.T) {
	runtime, _, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	for _, test := range []struct {
		status app.RouteStatus
		path   []app.CapabilityID
		want   app.WorkflowID
	}{
		{status: app.RouteClarify, path: []app.CapabilityID{"document"}, want: "router.clarify"},
		{status: app.RouteBlocked, path: []app.CapabilityID{"browser"}, want: "router.blocked"},
		{status: app.RouteMatched, path: []app.CapabilityID{"browser", app.CapabilityBrowserInternetSearch}, want: app.WorkflowBrowserInternetSearch},
	} {
		route := app.RouteDecision{SchemaVersion: app.RouteDecisionSchemaVersion, Status: test.status, CatalogRevision: runtime.capabilities.Revision(), CapabilityPath: test.path}
		run := app.AgentRun{
			ID: app.NewID("run"), SessionID: session.ID, State: "blocked", Summary: "persisted result", StartedAt: time.Now().UTC(),
			MessageContext: &app.MessageRunContext{
				OwnerID: app.DefaultOwnerID, Authorization: app.MessageAuthorization{PrincipalID: app.DefaultOwnerID},
				ReturnRoute: app.ReturnRoute{Mode: app.ReturnToSource, SourceEndpointID: app.EndpointID("session:" + session.ID)}, Route: route,
			},
		}
		result := runtime.resultForExistingRun(run)
		if result.WorkflowResult == nil || result.WorkflowResult.Workflow.ID != test.want {
			t.Fatalf("existing %q route changed identity: %#v", test.status, result.WorkflowResult)
		}
	}
}

type testRuntimeConfig struct {
	config  config.Config
	root    string
	browser bool
}

func newWorkflowE2ERuntime(t *testing.T, customize func(*testRuntimeConfig)) (Runtime, *store.MemoryStore, app.Session, func()) {
	t.Helper()
	root := t.TempDir()
	testCfg := testRuntimeConfig{config: agentTestConfig(), root: root}
	testCfg.config.Workspaces.DefaultRoot = root
	testCfg.config.Workspaces.Allowlist = []string{root}
	testCfg.config.Storage.TraceDir = filepath.Join(root, ".sparkclaw", "traces")
	testCfg.config.Storage.ArtifactDir = filepath.Join(root, ".sparkclaw", "artifacts")
	if customize != nil {
		customize(&testCfg)
	}
	st := store.NewMemoryStore()
	session := st.CreateSessionWithScope("workflow e2e", app.DefaultOwnerID, root, "web", false)
	tools := toolhub.New(testCfg.config, st)
	if testCfg.browser {
		tools = tools.WithBrowserAutomationAdapter(fakeBrowserAutomationAdapter{})
	}
	runtime := NewRuntime(st, tools, policy.New(testCfg.config), modelrouter.New(testCfg.config), nil)
	return runtime, st, session, func() { _ = tools.Close() }
}

func assertWorkflowClosure(t *testing.T, result Result, st *store.MemoryStore, sessionID string, capabilityID app.CapabilityID, workflowID app.WorkflowID, toolNames, semanticCapabilities []string) {
	t.Helper()
	if result.RouteDecision == nil || result.RouteDecision.Status != app.RouteMatched || len(result.RouteDecision.CapabilityPath) != 2 || result.RouteDecision.CapabilityPath[1] != capabilityID {
		t.Fatalf("message did not reach expected capability leaf: %#v", result.RouteDecision)
	}
	if result.Run.Workflow == nil || result.Run.Workflow.Plan.ProfileID != workflowID || result.Run.Workflow.Status != app.WorkflowStatusSucceeded {
		var assessment *app.NodeAssessment
		if result.Run.Workflow != nil && len(result.Run.Workflow.ActiveNodeIDs) == 1 {
			node := result.Run.Workflow.Nodes[result.Run.Workflow.ActiveNodeIDs[0]]
			assessment = node.LastAssessment
		}
		t.Fatalf("workflow did not complete under expected contract: workflow=%#v assessment=%#v calls=%#v", result.Run.Workflow, assessment, toolCallsForRun(st.ListToolCalls(sessionID), result.Run.ID))
	}
	if result.WorkflowResult == nil || result.WorkflowResult.Status != app.WorkflowResultSucceeded || result.WorkflowResult.Workflow.ID != workflowID || result.WorkflowResult.CapabilityPath[1] != capabilityID {
		t.Fatalf("missing successful WorkflowResult: %#v", result.WorkflowResult)
	}
	calls := toolCallsForRun(st.ListToolCalls(sessionID), result.Run.ID)
	if len(calls) != len(toolNames) || len(toolNames) != len(semanticCapabilities) {
		t.Fatalf("workflow did not execute the expected registered tool: %#v", calls)
	}
	for index := range calls {
		if calls[index].Tool != toolNames[index] || calls[index].WorkflowID != workflowID || calls[index].Capability != semanticCapabilities[index] {
			t.Fatalf("workflow call %d escaped its stage capability: %#v", index, calls[index])
		}
	}
	assertNoLegacyRoutingAudit(t, st.ListAudit(sessionID))
	if !hasAgentAuditType(st.ListAudit(sessionID), "workflow.dispatched") || !hasAgentAuditType(st.ListAudit(sessionID), "tools.exposure.fixed") {
		t.Fatalf("workflow dispatcher/exposure audit missing: %#v", st.ListAudit(sessionID))
	}
}

func writeTestOfficePackage(t *testing.T, path, sentinel string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	entry, err := archive.Create(sentinel)
	if err == nil {
		_, err = entry.Write([]byte("test package"))
	}
	if closeErr := archive.Close(); err == nil {
		err = closeErr
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
}
