package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
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
	if entry.Capability.Name != app.ToolCapabilityWebDiscovery || entry.Capability.Qualifiers[app.CapabilityQualifierProvider] != app.CapabilityProviderInfo || entry.Summary == "" {
		t.Fatalf("unexpected directory entry: %#v", entry)
	}
	if run, ok := st.GetRun(request.RunID); !ok || run.Workflow.Nodes[request.NodeID].LastDirectory.ViewID != view.ViewID {
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
	cfg := config.Default()
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
			cfg := config.Default()
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
			st.SaveRun(app.AgentRun{ID: runID, SessionID: "session", StartedAt: time.Now().UTC(), Workflow: state})
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
			call := app.ToolCall{ID: "tc_tabs", Tool: definition.Name, Status: "completed", Result: map[string]any{"pages": test.pages}, WorkflowNodeID: nodeID}
			outcome, err := adaptWorkflowOutcome(definition, call)
			if err != nil {
				t.Fatal(err)
			}
			stored, _ := st.GetRun(runID)
			assessment := profile.Assess(stored.Workflow, outcome)
			if _, err := applyWorkflowOutcome(&stored, outcome, assessment); err != nil {
				t.Fatal(err)
			}
			st.SaveRun(stored)
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
	cfg := config.Default()
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
	st.SaveRun(app.AgentRun{
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
