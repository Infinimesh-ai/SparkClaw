package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

type manualPersistenceRepository interface {
	Repository
	toolhub.Repository
}

func TestManualCompletedToolCallPersistence(t *testing.T) {
	for _, backend := range []string{"memory", "file", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var repository manualPersistenceRepository
			var reopen func() manualPersistenceRepository
			switch backend {
			case "memory":
				repository = store.NewMemoryStore()
			case "file":
				path := filepath.Join(t.TempDir(), "state.json")
				open := func() manualPersistenceRepository {
					repo, err := store.NewFileStore(path)
					if err != nil {
						t.Fatal(err)
					}
					return repo
				}
				repository, reopen = open(), open
			case "postgres":
				dsn := os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("set SPARKCLAW_TEST_POSTGRES_DSN for isolated PostgreSQL acceptance")
				}
				open := func() manualPersistenceRepository {
					repo, err := store.NewPostgresStore(t.Context(), dsn)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(repo.Close)
					return repo
				}
				repository, reopen = open(), open
			}
			cfg := agentTestConfig()
			cfg.Memory.RetentionDays = 0
			cfg.Workspaces.DefaultRoot = t.TempDir()
			cfg.Workspaces.Allowlist = []string{cfg.Workspaces.DefaultRoot}
			cfg.Storage.ArtifactDir = filepath.Join(t.TempDir(), "artifacts")
			tools := toolhub.New(cfg, repository)
			t.Cleanup(func() { _ = tools.Close() })
			runtime := Runtime{store: repository, tools: tools, policy: policy.New(cfg)}
			session := storetest.MustCreateSession(t, repository, "synthetic manual persistence")
			invocation, err := runtime.InvokeToolManually(t.Context(), "memory.search", map[string]any{"query": "synthetic manual persistence"}, session.ID)
			if err != nil {
				t.Fatalf("completed manual tool must persist its parent run before the call: %v", err)
			}
			if invocation.Call.Status != app.ToolCallStatusCompleted || invocation.Result == nil || invocation.Approval != nil {
				t.Fatalf("unexpected manual result: %#v", invocation)
			}
			if reopen != nil {
				repository = reopen()
			}
			run, found, err := repository.GetRun(t.Context(), invocation.Call.RunID)
			if err != nil || !found || run.SessionID != session.ID || run.State != "completed" || run.CompletedAt == nil {
				t.Fatalf("completed parent run missing: %#v found=%v err=%v", run, found, err)
			}
			call, found, err := repository.GetToolCall(t.Context(), invocation.Call.ID)
			if err != nil || !found || call.RunID != run.ID || call.SessionID != session.ID || call.Status != app.ToolCallStatusCompleted {
				t.Fatalf("completed call missing: %#v found=%v err=%v", call, found, err)
			}
			got, err := json.Marshal(call.Result)
			if err != nil {
				t.Fatal(err)
			}
			want, err := json.Marshal(invocation.Result)
			if err != nil || string(got) != string(want) {
				t.Fatalf("persisted result differs: %s want=%s err=%v", got, want, err)
			}
			calls, err := repository.ListToolCalls(t.Context(), session.ID)
			if err != nil || len(calls) != 1 {
				t.Fatalf("manual tool duplicated: count=%d err=%v", len(calls), err)
			}
		})
	}
}
