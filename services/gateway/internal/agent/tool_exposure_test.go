package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/configtest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func TestToolExposureSearchAndMaterializeWebDiscovery(t *testing.T) {
	st, engine, request := newWebExposureFixture(t, nil)

	view, err := engine.Search(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if view.ViewID == "" || view.DirectoryRevision == "" || len(view.Entries) != 1 {
		t.Fatalf("unexpected directory view: %#v", view)
	}
	entry := view.Entries[0]
	if entry.Name != "web.search" || entry.Description == "" || entry.Capability.Name != app.ToolCapabilityWebDiscovery || entry.Capability.Qualifiers[app.CapabilityQualifierProvider] != app.CapabilityProviderInfo || entry.Summary == "" {
		t.Fatalf("unexpected directory entry: %#v", entry)
	}
	if run, ok := testGetRun(st, request.RunID); !ok || run.Workflow.Nodes[request.NodeID].LastDirectory.ViewID != view.ViewID {
		t.Fatalf("directory binding was not persisted: %#v", run)
	}

	exposure, err := engine.Materialize(context.Background(), app.MaterializeRequest{
		ViewID:        view.ViewID,
		RunID:         request.RunID,
		WorkflowID:    request.WorkflowID,
		NodeID:        request.NodeID,
		ScopeRevision: request.ScopeRevision,
		EntryIDs:      []app.ToolDirectoryEntryID{entry.ID},
		ActorRef:      request.ActorRef,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(exposure.Definitions) != 1 || exposure.Definitions[0].Name != "web.search" {
		t.Fatalf("unexpected materialized definitions: %#v", exposure.Definitions)
	}
}

func TestToolExposureMaterializesFrozenSupportEntryAlongsideBusinessSelection(t *testing.T) {
	st, engine, request := newWebExposureFixture(t, nil)
	run, _ := testGetRun(st, request.RunID)
	node := run.Workflow.Plan.Nodes[0]
	node.InitialScope.SupportRequirements = []app.CapabilityRequirement{{Name: app.ToolCapabilityObservationRead}}
	run.Workflow.Plan.Nodes[0] = node
	state := run.Workflow.Nodes[request.NodeID]
	state.CurrentScope = node.InitialScope
	run.Workflow.Nodes[request.NodeID] = state
	run.Workflow.PlanDigest = workflowPlanDigest(run.Workflow.Plan)
	testSaveRun(st, run)

	view, err := engine.Search(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Entries) != 2 || view.Entries[1].Capability.Name != app.ToolCapabilityObservationRead {
		t.Fatalf("support entry was not appended after business directory entries: %#v", view.Entries)
	}
	entryIDs, err := workflowDirectorySelection(run, state, view)
	if err != nil {
		t.Fatal(err)
	}
	exposure, err := engine.Materialize(context.Background(), app.MaterializeRequest{
		ViewID: view.ViewID, RunID: request.RunID, WorkflowID: request.WorkflowID, NodeID: request.NodeID,
		ScopeRevision: request.ScopeRevision, EntryIDs: entryIDs, ActorRef: request.ActorRef,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !exactVisibleToolNames(exposure.Definitions, "web.search", "observation.read") {
		t.Fatalf("business and support definitions were not materialized together: %#v", visibleToolNames(exposure.Definitions))
	}
	stored, _ := testGetRun(st, request.RunID)
	if len(stored.Workflow.Nodes[request.NodeID].SelectedEntries) != 2 {
		t.Fatalf("support entry was not frozen in selected entries: %#v", stored.Workflow.Nodes[request.NodeID].SelectedEntries)
	}
}

func TestWorkflowDirectorySupportRequirementMustMatchExactlyOneEntry(t *testing.T) {
	state := app.WorkflowNodeState{CurrentScope: app.CapabilityScope{
		Requirements:        []app.CapabilityRequirement{{Name: app.ToolCapabilityWebDiscovery}},
		SupportRequirements: []app.CapabilityRequirement{{Name: app.ToolCapabilityObservationRead}},
	}}
	primary := app.ToolDirectoryEntry{ID: "primary", Capability: app.CapabilityDescriptor{Name: app.ToolCapabilityWebDiscovery}}
	if _, _, _, err := workflowDirectoryPartitions(state, app.DirectoryView{Entries: []app.ToolDirectoryEntry{primary}}); err == nil {
		t.Fatal("missing support definition did not fail closed")
	}
	supportA := app.ToolDirectoryEntry{ID: "support_a", Capability: app.CapabilityDescriptor{Name: app.ToolCapabilityObservationRead}}
	supportB := app.ToolDirectoryEntry{ID: "support_b", Capability: app.CapabilityDescriptor{Name: app.ToolCapabilityObservationRead}}
	if _, _, _, err := workflowDirectoryPartitions(state, app.DirectoryView{Entries: []app.ToolDirectoryEntry{primary, supportA, supportB}}); err == nil {
		t.Fatal("ambiguous support definition did not fail closed")
	}
}

func TestDirectoryRelevanceUsesOwnerQueryAndDefinitionMetadata(t *testing.T) {
	definition := app.ToolDefinition{
		Name: "localmind.keyword_search", Title: "Keyword search",
		Description: "Search authorized LocalMind documents",
		Directory:   app.ToolDirectoryMetadata{Summary: "Workspace evidence"},
	}
	if score := directoryRelevance("find localmind keyword matches", definition); score != 2 {
		t.Fatalf("definition metadata was not indexed, score=%d", score)
	}
	if score := directoryRelevance("unrelated weather forecast", definition); score != 0 {
		t.Fatalf("unrelated query received score=%d", score)
	}
}

func TestDynamicToolDirectoryBoundsLargeCatalogAndMaterializesOneSchema(t *testing.T) {
	const directoryLimit = 16
	cfg := configtest.MustLoadDefault()
	st := store.NewMemoryStore()
	hub := toolhub.New(cfg, st)
	defer hub.Close()
	registrations := make([]toolhub.DynamicToolRegistration, 0, 117)
	for index := 0; index < 117; index++ {
		name := fmt.Sprintf("localmind.read_%03d", index)
		registrations = append(registrations, toolhub.DynamicToolRegistration{
			Definition: app.ToolDefinition{
				Name: name, Title: fmt.Sprintf("LocalMind reader %03d", index), Description: "Read one credential-visible LocalMind record.",
				InputSchema: map[string]any{
					"type": "object", "properties": map[string]any{
						"query": map[string]any{"type": "string", "description": fmt.Sprintf("full_schema_sentinel_%03d", index)},
					}, "required": []string{"query"}, "additionalProperties": false,
				},
				Risk: app.RiskRead, Idempotent: true, TimeoutMS: 5000, Sandbox: "remote", Audit: "always",
				Capabilities: []app.CapabilityDescriptor{{Name: app.ToolCapabilityExternalMCPWorkspace, Qualifiers: map[string]string{
					app.CapabilityQualifierProvider: app.CapabilityProviderLocalMind,
					app.CapabilityQualifierMode:     "read", app.CapabilityQualifierOperation: string(app.RouteOperationRead),
					app.CapabilityQualifierEndpointID: "endpoint", app.CapabilityQualifierSnapshotRevision: "snapshot_1",
				}}},
				Directory: app.ToolDirectoryMetadata{Summary: "LocalMind reader", Effects: []app.ToolEffect{app.ToolEffectExternalRead}},
			},
			RemoteName: strings.TrimPrefix(name, "localmind."),
			Execute: func(context.Context, map[string]any, string, string) (toolhub.Result, error) {
				return toolhub.Result{}, nil
			},
		})
	}
	if err := hub.ReplaceDynamicTools("test.large.localmind", registrations); err != nil {
		t.Fatal(err)
	}
	nodeID := app.WorkflowNodeID("select_external_operation")
	plan := app.WorkflowPlan{
		SchemaVersion: 1, ProfileID: app.WorkflowCodingAgentManage, ProfileRevision: 1,
		InitialNodeIDs: []app.WorkflowNodeID{nodeID}, Completion: app.CompletionDecision,
		Nodes: []app.WorkflowNode{{
			ID: nodeID, InitialStage: "select", Goal: app.NodeGoal{Summary: "select LocalMind reader", Completion: app.CompletionDecision},
			InitialScope: app.CapabilityScope{Requirements: []app.CapabilityRequirement{{Name: app.ToolCapabilityExternalMCPWorkspace, Qualifiers: map[string]string{
				app.CapabilityQualifierProvider: app.CapabilityProviderLocalMind,
				app.CapabilityQualifierMode:     "read", app.CapabilityQualifierOperation: string(app.RouteOperationRead),
			}}}}, AllowedRisks: []app.RiskLevel{app.RiskRead}, MaxAttempts: 1,
		}},
	}
	state := &app.WorkflowState{
		Plan: plan, PlanDigest: "large_catalog", Status: app.WorkflowStatusRunning, ActiveNodeIDs: []app.WorkflowNodeID{nodeID},
		Route: app.RouteDecision{Slots: app.RouteSlots{Query: "reader 042"}},
		Nodes: map[app.WorkflowNodeID]app.WorkflowNodeState{nodeID: {
			Status: app.WorkflowNodeActive, Stage: "select", CurrentScope: plan.Nodes[0].InitialScope, ScopeRevision: 1,
		}},
	}
	testSaveRun(st, app.AgentRun{ID: "run_large_localmind", SessionID: "session", StartedAt: time.Now().UTC(), Workflow: state})
	engine := newToolExposureEngine(st, hub, policy.New(cfg))
	request := app.ExposureRequest{
		RunID: "run_large_localmind", WorkflowID: app.WorkflowCodingAgentManage, NodeID: nodeID,
		ScopeRevision: 1, ActorRef: "owner", Limit: directoryLimit,
	}
	view, err := engine.Search(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Entries) != directoryLimit {
		t.Fatalf("large LocalMind catalog returned %d directory entries", len(view.Entries))
	}
	rawView, _ := json.Marshal(view)
	if strings.Contains(string(rawView), "full_schema_sentinel") {
		t.Fatalf("directory view included full tool schemas: %s", rawView)
	}
	selected := view.Entries[0]
	exposure, err := engine.Materialize(context.Background(), app.MaterializeRequest{
		ViewID: view.ViewID, RunID: request.RunID, WorkflowID: request.WorkflowID, NodeID: request.NodeID,
		ScopeRevision: request.ScopeRevision, EntryIDs: []app.ToolDirectoryEntryID{selected.ID}, ActorRef: request.ActorRef,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(exposure.Definitions) != 1 {
		t.Fatalf("materialized %d schemas instead of one", len(exposure.Definitions))
	}
	rawDefinition, _ := json.Marshal(exposure.Definitions[0])
	if !strings.Contains(string(rawDefinition), "full_schema_sentinel") {
		t.Fatalf("selected schema was not materialized: %s", rawDefinition)
	}
}

func TestToolExposureReleaseRunEvictsCachedViews(t *testing.T) {
	_, engine, request := newWebExposureFixture(t, nil)

	view, err := engine.Search(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}

	engine.releaseRun("run_unrelated")
	engine.mu.Lock()
	kept := len(engine.latest)
	engine.mu.Unlock()
	if kept != 1 {
		t.Fatalf("releasing an unrelated run must keep cached views, have %d", kept)
	}

	engine.releaseRun(request.RunID)
	engine.mu.Lock()
	remaining := len(engine.latest)
	engine.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("run release left %d cached views", remaining)
	}

	if _, err := engine.Materialize(context.Background(), app.MaterializeRequest{
		ViewID:        view.ViewID,
		RunID:         request.RunID,
		WorkflowID:    request.WorkflowID,
		NodeID:        request.NodeID,
		ScopeRevision: request.ScopeRevision,
		EntryIDs:      []app.ToolDirectoryEntryID{view.Entries[0].ID},
		ActorRef:      request.ActorRef,
	}); err == nil {
		t.Fatal("materializing a released view must fail as stale")
	}
}

func TestToolExposureRejectsSelectionOutsideLatestView(t *testing.T) {
	_, engine, request := newWebExposureFixture(t, nil)
	view, err := engine.Search(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Materialize(context.Background(), app.MaterializeRequest{
		ViewID:        view.ViewID,
		RunID:         request.RunID,
		WorkflowID:    request.WorkflowID,
		NodeID:        request.NodeID,
		ScopeRevision: request.ScopeRevision,
		EntryIDs:      []app.ToolDirectoryEntryID{"entry_not_returned"},
		ActorRef:      request.ActorRef,
	})
	if !errors.Is(err, errExposureEntryInvalid) {
		t.Fatalf("expected invalid-entry error, got %v", err)
	}
}

func TestToolExposureRejectsViewAfterRuntimeRestart(t *testing.T) {
	st, engine, request := newWebExposureFixture(t, nil)
	view, err := engine.Search(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	restarted := newToolExposureEngine(st, engine.tools, engine.policy)
	_, err = restarted.Materialize(context.Background(), app.MaterializeRequest{
		ViewID:        view.ViewID,
		RunID:         request.RunID,
		WorkflowID:    request.WorkflowID,
		NodeID:        request.NodeID,
		ScopeRevision: request.ScopeRevision,
		EntryIDs:      []app.ToolDirectoryEntryID{view.Entries[0].ID},
		ActorRef:      request.ActorRef,
	})
	if !errors.Is(err, errExposureViewStale) {
		t.Fatalf("expected stale-view error after restart, got %v", err)
	}
	if refreshed, err := restarted.Search(context.Background(), request); err != nil || len(refreshed.Entries) != 1 {
		t.Fatalf("restart should recover by searching the persisted scope again: %#v %v", refreshed, err)
	}
}

func TestToolExposureAppliesStaticPolicyBeforeRanking(t *testing.T) {
	cfg := configtest.MustLoadDefault()
	cfg.Security.DeniedTools = []string{"web.search"}
	_, engine, request := newWebExposureFixture(t, &cfg)
	view, err := engine.Search(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Entries) != 0 {
		t.Fatalf("denied tool entered the directory: %#v", view.Entries)
	}
}

func TestCapabilityQualifierMatchingUsesRequirementSubset(t *testing.T) {
	capability := app.CapabilityDescriptor{Name: "document.modify", Qualifiers: map[string]string{"format": "docx", "operation": "replace"}}
	if !matchesAnyRequirement(capability, []app.CapabilityRequirement{{Name: "document.modify", Qualifiers: map[string]string{"format": "docx"}}}) {
		t.Fatal("descriptor should satisfy a requirement whose qualifiers are a subset")
	}
	if matchesAnyRequirement(capability, []app.CapabilityRequirement{{Name: "document.modify", Qualifiers: map[string]string{"format": "xlsx"}}}) {
		t.Fatal("descriptor must not satisfy a conflicting qualifier")
	}
}

func TestBrowserAutomationStageExposureReplacesViewAndRejectsOldRevision(t *testing.T) {
	for _, test := range []struct {
		name      string
		pages     []any
		wantTool  string
		wantStage string
	}{
		{name: "exact target exists", pages: []any{map[string]any{"page_id": "page_7", "url": "https://example.com/"}}, wantTool: "browser.focus", wantStage: "focus_existing"},
		{name: "exact target absent", pages: []any{map[string]any{"page_id": "page_8", "url": "https://other.example/"}}, wantTool: "browser.open", wantStage: "open_new"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := configtest.MustLoadDefault()
			cfg.Tools.BrowserAutomation.Enabled = true
			st := store.NewMemoryStore()
			hub := toolhub.New(cfg, st)
			defer hub.Close()
			engine := newToolExposureEngine(st, hub, policy.New(cfg))
			profile := browserAutomationProfile{}
			route := app.RouteDecision{
				SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched, CatalogRevision: "test",
				CapabilityPath: []app.CapabilityID{"browser", app.CapabilityBrowserAutomation},
				Slots:          app.RouteSlots{Operation: app.RouteOperationOpen, TargetKind: "url", TargetRef: "https://example.com/"},
				Facts:          map[string]string{"url": "https://example.com/"},
			}
			intent, plan, err := profile.Resolve(route, "turn")
			if err != nil {
				t.Fatal(err)
			}
			state := newWorkflowState(route, app.ReturnRoute{}, intent, plan)
			if _, err := profile.Prepare(state); err != nil {
				t.Fatal(err)
			}
			nodeID := plan.InitialNodeIDs[0]
			node := state.Nodes[nodeID]
			node.Stage = "scan_tabs"
			state.Nodes[nodeID] = node
			runID := "run_browser_stage_" + strings.ReplaceAll(test.name, " ", "_")
			testSaveRun(st, app.AgentRun{ID: runID, SessionID: "session", StartedAt: time.Now().UTC(), Workflow: state})
			request := app.ExposureRequest{RunID: runID, WorkflowID: profile.ID(), NodeID: nodeID, ScopeRevision: 1, ActorRef: "owner"}
			initial, err := engine.Search(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			var listTabsEntry app.ToolDirectoryEntry
			for _, entry := range initial.Entries {
				if entry.Capability.Name == app.ToolCapabilityBrowserListTabs {
					listTabsEntry = entry
					break
				}
			}
			if listTabsEntry.ID == "" {
				t.Fatalf("revision-2 boundary did not include tab discovery: %#v", initial.Entries)
			}
			if _, err := engine.Materialize(context.Background(), app.MaterializeRequest{
				ViewID: initial.ViewID, RunID: runID, WorkflowID: profile.ID(), NodeID: nodeID, ScopeRevision: 1,
				EntryIDs: []app.ToolDirectoryEntryID{listTabsEntry.ID}, ActorRef: "owner",
			}); err != nil {
				t.Fatal(err)
			}
			definition, _ := hub.Definition("browser.list_tabs")
			call := app.ToolCall{ID: "tc_tabs", Tool: definition.Name, Status: app.ToolCallStatusCompleted, Result: map[string]any{"pages": test.pages}, WorkflowNodeID: nodeID}
			outcome, err := adaptWorkflowOutcome(definition, call)
			if err != nil {
				t.Fatal(err)
			}
			stored, _ := testGetRun(st, runID)
			assessment := profile.Assess(stored.Workflow, outcome)
			if _, err := applyWorkflowOutcome(&stored, outcome, assessment); err != nil {
				t.Fatal(err)
			}
			testSaveRun(st, stored)
			if node := stored.Workflow.Nodes[nodeID]; node.ScopeRevision != 2 || node.Stage != test.wantStage || node.LastDirectory != nil || len(node.SelectedEntries) != 0 {
				t.Fatalf("stage transition did not clear the old exposure view: %#v", node)
			}
			_, err = engine.Materialize(context.Background(), app.MaterializeRequest{
				ViewID: initial.ViewID, RunID: runID, WorkflowID: profile.ID(), NodeID: nodeID, ScopeRevision: 1,
				EntryIDs: []app.ToolDirectoryEntryID{listTabsEntry.ID}, ActorRef: "owner",
			})
			if !errors.Is(err, errExposureWorkflowMismatch) {
				t.Fatalf("old scope revision remained callable: %v", err)
			}
			request.ScopeRevision = 2
			next, err := engine.Search(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			wantDefinition, ok := hub.Definition(test.wantTool)
			if !ok || len(wantDefinition.Capabilities) != 1 {
				t.Fatalf("missing exact definition for %q", test.wantTool)
			}
			var nextEntry app.ToolDirectoryEntry
			for _, entry := range next.Entries {
				if entry.Capability.Name == wantDefinition.Capabilities[0].Name {
					nextEntry = entry
					break
				}
			}
			if nextEntry.ID == "" {
				t.Fatalf("next revision-2 boundary omitted %q: %#v", test.wantTool, next.Entries)
			}
			exposure, err := engine.Materialize(context.Background(), app.MaterializeRequest{
				ViewID: next.ViewID, RunID: runID, WorkflowID: profile.ID(), NodeID: nodeID, ScopeRevision: 2,
				EntryIDs: []app.ToolDirectoryEntryID{nextEntry.ID}, ActorRef: "owner",
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(exposure.Definitions) != 1 || exposure.Definitions[0].Name != test.wantTool {
				t.Fatalf("next stage exposed the wrong exact tool: %#v", visibleToolNames(exposure.Definitions))
			}
		})
	}
}

func newWebExposureFixture(t *testing.T, cfgOverride *config.Config) (*store.MemoryStore, *toolExposureEngine, app.ExposureRequest) {
	t.Helper()
	cfg := configtest.MustLoadDefault()
	if cfgOverride != nil {
		cfg = *cfgOverride
	}
	cfg.Tools.Web.Search.Enabled = true
	st := store.NewMemoryStore()
	hub := toolhub.New(cfg, st)
	engine := newToolExposureEngine(st, hub, policy.New(cfg))
	nodeID := app.WorkflowNodeID("search_info")
	plan := app.WorkflowPlan{
		SchemaVersion:   1,
		ProfileID:       app.WorkflowBrowserInternetSearch,
		ProfileRevision: 1,
		InitialNodeIDs:  []app.WorkflowNodeID{nodeID},
		Completion:      app.CompletionEvidence,
		Nodes: []app.WorkflowNode{{
			ID:           nodeID,
			InitialStage: "search_info",
			Goal:         app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "discover public web evidence", Completion: app.CompletionEvidence},
			InitialScope: app.CapabilityScope{Requirements: []app.CapabilityRequirement{{Name: app.ToolCapabilityWebDiscovery, Qualifiers: map[string]string{app.CapabilityQualifierProvider: app.CapabilityProviderInfo}}}},
			AllowedRisks: []app.RiskLevel{app.RiskRead},
			MaxAttempts:  2,
		}},
	}
	testSaveRun(st, app.AgentRun{
		ID:        "run_web_exposure",
		SessionID: "session_web_exposure",
		State:     "routing",
		StartedAt: time.Now().UTC(),
		Workflow: &app.WorkflowState{
			SchemaVersion: 1,
			Plan:          plan,
			PlanDigest:    "plan_test",
			Status:        app.WorkflowStatusRunning,
			ActiveNodeIDs: []app.WorkflowNodeID{nodeID},
			Nodes: map[app.WorkflowNodeID]app.WorkflowNodeState{
				nodeID: {
					Status:                app.WorkflowNodeActive,
					Stage:                 "search_info",
					CurrentScope:          plan.Nodes[0].InitialScope,
					ScopeRevision:         1,
					TransitionActivations: map[app.TransitionID]int{},
				},
			},
		},
	})

	return st, engine, app.ExposureRequest{
		RunID:         "run_web_exposure",
		WorkflowID:    app.WorkflowBrowserInternetSearch,
		NodeID:        nodeID,
		ScopeRevision: 1,
		ActorRef:      "owner",
		Limit:         4,
	}
}
