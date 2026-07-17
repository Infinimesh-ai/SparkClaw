package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func TestWorkflowRegistryProducesStableSemantics(t *testing.T) {
	registry := defaultWorkflowProfileRegistry()
	searchMatch, ok, err := registry.Recognize("turn_search", "帮我查一下今天的 AI 新闻，并读取官方原文")
	if err != nil || !ok {
		t.Fatalf("public Web intent was not recognized: ok=%v err=%v", ok, err)
	}
	search := searchMatch.Intent
	if len(search.Objectives) != 1 || search.Objectives[0].Operation != app.IntentOperationSearch || search.Constraints.EvidenceDepth != "source" {
		t.Fatalf("unexpected search intent: %#v", search)
	}
	if search.Objectives[0].Target.Kind != app.TargetKindNone || search.SourceTurnID != "turn_search" {
		t.Fatalf("search intent leaked realization details: %#v", search)
	}

	readMatch, ok, err := registry.Recognize("turn_read", "总结 https://example.com/article")
	if err != nil || !ok {
		t.Fatalf("explicit URL intent was not recognized: ok=%v err=%v", ok, err)
	}
	read := readMatch.Intent
	if read.Objectives[0].Operation != app.IntentOperationRead || read.Objectives[0].Target.Ref != "https://example.com/article" {
		t.Fatalf("unexpected explicit URL intent: %#v", read)
	}
	if _, ok, err := registry.Recognize("turn_calendar", "Read calendar for today"); err != nil || ok {
		t.Fatalf("unmigrated calendar intent must not be captured: ok=%v err=%v", ok, err)
	}
}

func TestWebWorkflowTransitionIsFrozenAndIdempotent(t *testing.T) {
	matched := mustRecognizeWorkflow(t, "turn", "查一下 SparkClaw，并读取官方原文")
	plan, err := matched.Profile.Resolve(matched.Intent)
	if err != nil {
		t.Fatal(err)
	}
	state := newWorkflowState(matched.Intent, plan)
	run := app.AgentRun{ID: "run", Workflow: state}
	outcome := app.ToolOutcome{
		ID:         "outcome_search",
		ToolCallID: "tc_search",
		NodeID:     "research",
		Status:     "completed",
		Signals:    []app.OutcomeSignal{app.OutcomeSignalResultsAvailable, app.OutcomeSignalSourcePageAvailable},
		Refs:       []app.ResourceRef{{Kind: "url", Ref: "https://example.com/source", Provenance: "tc_search"}},
	}
	assessment := matched.Profile.Assess(state, outcome)
	changed, err := applyWorkflowOutcome(&run, outcome, assessment)
	if err != nil || !changed {
		t.Fatalf("source-depth transition failed: changed=%v err=%v", changed, err)
	}
	node := run.Workflow.Nodes["research"]
	if node.ScopeRevision != 2 || len(node.CurrentScope.Requirements) != 1 || node.CurrentScope.Requirements[0].Name != "web.page.read" || node.TransitionActivations["source_page"] != 1 {
		t.Fatalf("transition did not replace the active scope: %#v", node)
	}
	changed, err = applyWorkflowOutcome(&run, outcome, assessment)
	if err != nil || changed || run.Workflow.Nodes["research"].TransitionActivations["source_page"] != 1 {
		t.Fatalf("duplicate outcome was not idempotent: changed=%v err=%v state=%#v", changed, err, run.Workflow.Nodes["research"])
	}
}

func TestWorkflowPageReadIsBoundToTypedURLReference(t *testing.T) {
	matched := mustRecognizeWorkflow(t, "turn", "查一下 SparkClaw，并读取官方原文")
	plan, err := matched.Profile.Resolve(matched.Intent)
	if err != nil {
		t.Fatal(err)
	}
	run := app.AgentRun{ID: "run_resource_boundary", SessionID: "session", Workflow: newWorkflowState(matched.Intent, plan)}
	outcome := app.ToolOutcome{
		ID:         "outcome_search",
		ToolCallID: "tc_search",
		NodeID:     "research",
		Status:     "completed",
		Signals:    []app.OutcomeSignal{app.OutcomeSignalResultsAvailable, app.OutcomeSignalSourcePageAvailable},
		Refs:       []app.ResourceRef{{Kind: "url", Ref: "https://example.com/allowed", Provenance: "tc_search"}},
	}
	assessment := matched.Profile.Assess(run.Workflow, outcome)
	if changed, err := applyWorkflowOutcome(&run, outcome, assessment); err != nil || !changed {
		t.Fatalf("transition failed: changed=%v err=%v", changed, err)
	}

	cfg := agentTestConfig()
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	defer tools.Close()
	definition, ok := tools.Definition("browser.read")
	if !ok {
		t.Fatal("browser.read definition missing")
	}
	node := run.Workflow.Nodes["research"]
	node.SelectedEntries = []app.ToolDirectoryEntryID{directoryEntryID(definition, app.CapabilityDescriptor{Name: "web.page.read"})}
	run.Workflow.Nodes["research"] = node
	st.SaveRun(run)
	runtime := NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), nil)
	basePlan := toolPlan{
		Name:           "browser.read",
		WorkflowID:     app.WorkflowWebPublicResearch,
		WorkflowNodeID: "research",
		ScopeRevision:  2,
		Capability:     "web.page.read",
	}
	basePlan.Args = map[string]any{"url": "https://example.com/allowed"}
	if err := runtime.validateWorkflowToolPlan(run.ID, basePlan, definition); err != nil {
		t.Fatalf("typed discovery URL was rejected: %v", err)
	}
	basePlan.Args = map[string]any{"url": "https://example.com/unrelated"}
	if err := runtime.validateWorkflowToolPlan(run.ID, basePlan, definition); err == nil {
		t.Fatal("unrelated URL escaped the workflow resource boundary")
	}
}

func TestAuthoritativeWebWorkflowExpandsFromDiscoveryToSourcePage(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><title>Official SparkClaw source</title><article><p>Source-level evidence for the workflow.</p></article></html>`))
	}))
	defer page.Close()

	info := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/info/tokens/issue":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"epoch": time.Now().UTC().Format("2006-01-02"),
				"issued_tokens": []map[string]any{{
					"type": "info.basic", "token_mode": "internal_opaque", "token": "workflow-token",
					"expires_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
				}},
				"quota_remaining": map[string]int{"info.basic": 9},
			})
		case "/v1/info/query":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"request_id": r.Header.Get("X-Request-Id"),
				"status":     "ok",
				"answer_context": map[string]any{
					"summary": "Search summary", "key_facts": []map[string]any{{"claim": "claim", "sources": []string{"src-1"}}},
				},
				"sources": []map[string]any{{
					"id": "src-1", "title": "Official source", "url": page.URL,
					"source_type": "official_documentation", "snippets": []string{"bounded evidence"},
				}},
				"usage": map[string]any{"cost_credits": 1, "token_type": "info.basic"},
			})
		default:
			t.Fatalf("unexpected Infinimesh path: %s", r.URL.Path)
		}
	}))
	defer info.Close()

	root := t.TempDir()
	cfg := agentTestConfig()
	cfg.Tools.Web.Search.Enabled = true
	cfg.Security.BrowserReadAllowHosts = []string{"127.0.0.1"}
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	cfg.Storage.TraceDir = filepath.Join(root, ".sparkclaw", "traces")
	cfg.Storage.ArtifactDir = filepath.Join(root, ".sparkclaw", "artifacts")
	infoCfg := &cfg.Plugins.Entries.InfinimeshInfo.Config
	infoCfg.BaseURL = info.URL
	infoCfg.EntitlementProof = "entitlement-proof"
	infoCfg.DeviceAttestation = "device-attestation"
	infoCfg.LicenseProof = "license-proof"
	infoCfg.MaxAttempts = 1

	st := store.NewMemoryStore()
	session := st.CreateSession("authoritative Web workflow")
	tools := toolhub.New(cfg, st)
	defer tools.Close()
	runtime := NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), nil)

	result, err := runtime.HandleMessage(context.Background(), session.ID, "帮我查一下 SparkClaw，并读取官方原文")
	if err != nil {
		t.Fatal(err)
	}
	if result.Run.Workflow == nil || result.Run.Workflow.Status != app.WorkflowStatusSucceeded || result.Run.Workflow.Plan.ProfileID != app.WorkflowWebPublicResearch {
		t.Fatalf("Web workflow did not complete: %#v", result.Run.Workflow)
	}
	calls := st.ListToolCalls(session.ID)
	if len(calls) != 2 || calls[0].Tool != "web.search" || calls[1].Tool != "browser.read" {
		t.Fatalf("unexpected adaptive Web chain: %#v", calls)
	}
	if calls[0].Capability != "web.discovery" || calls[0].ScopeRevision != 1 || calls[1].Capability != "web.page.read" || calls[1].ScopeRevision != 2 {
		t.Fatalf("tool calls were not bound to scope revisions: %#v", calls)
	}
	node := result.Run.Workflow.Nodes["research"]
	if node.TransitionActivations["source_page"] != 1 || len(node.AppliedOutcomeIDs) != 2 || node.Attempts != 2 {
		t.Fatalf("workflow transition state is incomplete: %#v", node)
	}
	for _, event := range st.ListAudit(session.ID) {
		if event.Type == "task_hint.generated" || event.Type == "task_hint.fallback" || event.Type == "react.visible_tools_expanded" {
			t.Fatalf("authoritative Web workflow used a legacy routing path: %#v", event)
		}
	}
}
