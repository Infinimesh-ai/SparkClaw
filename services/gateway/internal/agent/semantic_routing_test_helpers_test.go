package agent

import (
	"context"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/capability"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/skills"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func mustRouteIntent(t *testing.T, runtime Runtime, content string) app.RouteDecision {
	t.Helper()
	return mustRouteIntentOutput(t, runtime, "", content, nil, app.MessageSourceWeb).Route
}

func mustRouteIntentWithResources(t *testing.T, runtime Runtime, sessionID, content string, resources []app.MessagePart, sourceKind app.MessageSourceKind) app.RouteDecision {
	t.Helper()
	return mustRouteIntentOutput(t, runtime, sessionID, content, resources, sourceKind).Route
}

func mustRouteIntentOutput(t *testing.T, runtime Runtime, sessionID, content string, resources []app.MessagePart, sourceKind app.MessageSourceKind) IntentRoutingOutput {
	t.Helper()
	cfg := agentTestConfig()
	if runtime.store == nil {
		runtime.store = store.NewMemoryStore()
	}
	if runtime.tools == nil {
		runtime.tools = toolhub.New(cfg, runtime.store)
	}
	if runtime.capabilities.Revision() == "" {
		runtime.capabilities = capability.MustDefaultCatalog()
	}
	if len(runtime.profiles.byID) == 0 {
		runtime.profiles = defaultWorkflowProfileRegistry()
	}
	runtime.models = modelrouter.New(cfg)
	if runtime.semanticRouter == nil {
		graph, err := runtime.profiles.SemanticGraph(runtime.capabilities)
		if err != nil {
			t.Fatal(err)
		}
		router := newSemanticIntentRouter(runtime.capabilities.Revision(), graph)
		if _, err := router.initializeEmbeddingIndex(t.Context(), runtime.models); err != nil {
			t.Fatal(err)
		}
		runtime.semanticRouter = router
	}
	output, err := runtime.routeIntentWithRequest(t.Context(), sessionID, app.NewID("run_route_test"), content, resources, sourceKind)
	if err != nil {
		t.Fatal(err)
	}
	return output
}

func (runtime Runtime) routeIntentForTest(sessionID, runID, content string, _ agentContextSnapshot) (app.RouteDecision, error) {
	output, err := runtime.routeIntentOutputForTest(sessionID, runID, content)
	return output.Route, err
}

func (runtime Runtime) routeIntentOutputForTest(sessionID, runID, content string) (IntentRoutingOutput, error) {
	cfg := agentTestConfig()
	if runtime.store == nil {
		runtime.store = store.NewMemoryStore()
	}
	if runtime.tools == nil {
		runtime.tools = toolhub.New(cfg, runtime.store)
	}
	if runtime.capabilities.Revision() == "" {
		runtime.capabilities = capability.MustDefaultCatalog()
	}
	if len(runtime.profiles.byID) == 0 {
		runtime.profiles = defaultWorkflowProfileRegistry()
	}
	runtime.models = modelrouter.New(cfg)
	if runtime.semanticRouter == nil {
		graph, err := runtime.profiles.SemanticGraph(runtime.capabilities)
		if err != nil {
			return IntentRoutingOutput{}, err
		}
		router := newSemanticIntentRouter(runtime.capabilities.Revision(), graph)
		if _, err := router.initializeEmbeddingIndex(context.Background(), runtime.models); err != nil {
			return IntentRoutingOutput{}, err
		}
		runtime.semanticRouter = router
	}
	return runtime.routeIntentWithRequest(context.Background(), sessionID, runID, content, nil, app.MessageSourceWeb)
}

func TestRuntimeBuildsSemanticEmbeddingIndexBeforeRouting(t *testing.T) {
	cfg := agentTestConfig()
	st := store.NewMemoryStore()
	hub := toolhub.New(cfg, st)
	defer hub.Close()
	runtime := NewRuntime(st, hub, policy.New(cfg), modelrouter.New(cfg), nil)
	if runtime.semanticRouter == nil || runtime.semanticRouter.index == nil {
		t.Fatal("runtime returned before the semantic embedding index was ready")
	}
	startupCalls := 0
	for _, call := range st.ListModelCalls("", "") {
		if call.Operation == "intent_embedding_index" && call.Status == "completed" {
			startupCalls++
		}
	}
	if startupCalls != 1 {
		t.Fatalf("startup embedding index call count=%d want 1", startupCalls)
	}

	before := len(st.ListModelCalls("", ""))
	runtime.semanticRouter.index = nil
	result := runtime.scoreEmbeddingChannel(t.Context(), "", "", "hello", runtime.semanticRouter.graph.EligibleCandidates(app.MessageSourceWeb))
	if result.state.ReasonCode != "embedding_index_unavailable" {
		t.Fatalf("request path attempted to use an uninitialized index: %#v", result.state)
	}
	if after := len(st.ListModelCalls("", "")); after != before {
		t.Fatalf("request path rebuilt the embedding corpus: model calls %d -> %d", before, after)
	}
}

func TestRuntimeStartupFailsWhenSemanticEmbeddingIndexCannotBuild(t *testing.T) {
	cfg := agentTestConfig()
	cfg.Model.Mock = false
	cfg.Model.Embedding.BaseURL = "http://127.0.0.1:1"
	st := store.NewMemoryStore()
	hub := toolhub.New(cfg, st)
	defer hub.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewRuntimeWithSkillsContext(ctx, st, hub, policy.New(cfg), modelrouter.New(cfg), nil, skills.Registry{}); err == nil {
		t.Fatal("runtime started without a valid semantic embedding index")
	}
	failedStartupCall := false
	for _, call := range st.ListModelCalls("", "") {
		if call.Operation == "intent_embedding_index" && call.Status == "failed" {
			failedStartupCall = true
		}
	}
	if !failedStartupCall {
		t.Fatalf("failed startup embedding call was not recorded: %#v", st.ListModelCalls("", ""))
	}
}
