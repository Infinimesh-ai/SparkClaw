package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func TestPersistedLegacyDocumentReadResumesOriginalPlan(t *testing.T) {
	for _, revision := range []int{4, 5, 6} {
		t.Run(fmt.Sprint(revision), func(t *testing.T) {
			root := t.TempDir()
			statePath := filepath.Join(root, "state.json")
			st, err := store.NewFileStore(statePath)
			if err != nil {
				t.Fatal(err)
			}
			cfg := agentTestConfig()
			cfg.Workspaces.DefaultRoot, cfg.Workspaces.Allowlist = root, []string{root}
			cfg.Storage.TraceDir, cfg.Storage.ArtifactDir = filepath.Join(root, "traces"), filepath.Join(root, "artifacts")
			hub := toolhub.New(cfg, st)
			runtime := NewRuntime(st, hub, policy.New(cfg), modelrouter.New(cfg), nil)
			session := storetest.MustCreateSessionWithScope(t, st, "legacy resume", app.DefaultOwnerID, root, "web", false)
			if err := os.WriteFile(filepath.Join(root, "legacy.txt"), []byte("Original direct-read evidence."), 0600); err != nil {
				t.Fatal(err)
			}
			goal := "Read legacy.txt"
			message, err := st.AddMessage(t.Context(), app.Message{ID: app.NewID("message"), SessionID: session.ID, Role: "user", Content: goal, CreatedAt: time.Now().UTC()})
			if err != nil {
				t.Fatal(err)
			}
			route, err := runtime.routeIntentForTest(session.ID, message.ID, goal, agentContextSnapshot{})
			if err != nil {
				t.Fatal(err)
			}
			profile, err := runtime.profiles.Get(app.WorkflowDocumentRead, revision)
			if err != nil {
				t.Fatal(err)
			}
			intent, plan, err := profile.Resolve(route, message.ID)
			if err != nil {
				t.Fatal(err)
			}
			workflow := newWorkflowState(route, app.ReturnRoute{Mode: app.ReturnToSource}, intent, plan)
			if err := prepareWorkflowState(profile, workflow); err != nil {
				t.Fatal(err)
			}
			run, err := st.SaveRun(t.Context(), app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, State: "running", StartedAt: time.Now().UTC(), Workflow: workflow})
			if err != nil {
				t.Fatal(err)
			}
			if err := hub.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := store.NewFileStore(statePath)
			if err != nil {
				t.Fatal(err)
			}
			loaded, found, err := reopened.GetRun(t.Context(), run.ID)
			if err != nil || !found {
				t.Fatalf("persisted plan unavailable: %v", err)
			}
			restoredHub := toolhub.New(cfg, reopened)
			defer restoredHub.Close()
			restored := NewRuntime(reopened, restoredHub, policy.New(cfg), modelrouter.New(cfg), nil)
			result, handled, err := restored.resumeMatchedWorkflow(t.Context(), loaded, goal, nil, "test.legacy_document_resumed")
			if err != nil || !handled || result.Run.Workflow.Status != app.WorkflowStatusSucceeded || result.Run.Workflow.Plan.ProfileRevision != revision || result.Run.Workflow.PlanDigest != run.Workflow.PlanDigest {
				t.Fatalf("legacy workflow drifted after reopen: revision=%d handled=%v err=%v status=%s", revision, handled, err, result.Run.Workflow.Status)
			}
			calls, err := reopened.ListToolCalls(t.Context(), session.ID)
			if revision == 6 {
				if err != nil || len(calls) != 2 || calls[0].Tool != "document.outline" || calls[1].Tool != "document.read_range" || calls[1].Arguments["include_descendants"] != nil {
					t.Fatalf("legacy scoped read changed: %#v %v", calls, err)
				}
			} else if err != nil || len(calls) != 1 || calls[0].Tool != "files.read" || calls[0].Status != app.ToolCallStatusCompleted {
				t.Fatalf("legacy direct read changed: %#v %v", calls, err)
			}
		})
	}
}
