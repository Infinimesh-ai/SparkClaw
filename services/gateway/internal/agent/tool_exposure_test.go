package agent

import (
	"context"
	"errors"
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
	if entry.Capability.Name != string(app.CapabilityBrowserSearch) || entry.Capability.Qualifiers[app.CapabilityQualifierOperation] != app.CapabilityOperationDiscover || entry.Summary == "" {
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
	nodeID := app.WorkflowNodeID("browser_search")
	plan := app.WorkflowPlan{
		SchemaVersion:   1,
		ProfileID:       app.WorkflowBrowserSearch,
		ProfileRevision: 1,
		InitialNodeIDs:  []app.WorkflowNodeID{nodeID},
		Completion:      app.CompletionEvidence,
		Nodes: []app.WorkflowNode{{
			ID:           nodeID,
			Goal:         app.NodeGoal{ObjectiveIDs: []string{"objective_1"}, Summary: "discover public web evidence", Completion: app.CompletionEvidence},
			InitialScope: app.CapabilityScope{Requirements: []app.CapabilityRequirement{{Name: string(app.CapabilityBrowserSearch), Qualifiers: map[string]string{app.CapabilityQualifierOperation: app.CapabilityOperationDiscover}}}},
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
					CurrentScope:          plan.Nodes[0].InitialScope,
					ScopeRevision:         1,
					TransitionActivations: map[app.TransitionID]int{},
				},
			},
		},
	})
	return st, engine, app.ExposureRequest{
		RunID:         "run_web_exposure",
		WorkflowID:    app.WorkflowBrowserSearch,
		NodeID:        nodeID,
		ScopeRevision: 1,
		ActorRef:      "owner",
		Limit:         4,
	}
}
