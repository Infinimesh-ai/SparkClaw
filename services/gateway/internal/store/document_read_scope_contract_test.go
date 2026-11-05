package store

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestDocumentReadScopeSurvivesRepositoryRoundTrip(t *testing.T) {
	for _, backend := range []string{"memory", "file", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var repository testBackend
			var reopen func() testBackend
			switch backend {
			case "memory":
				repository = NewMemoryStore()
			case "file":
				path := filepath.Join(t.TempDir(), "state.json")
				reopen = func() testBackend {
					st, err := NewFileStore(path)
					if err != nil {
						t.Fatal(err)
					}
					return st
				}
				repository = reopen()
			case "postgres":
				dsn := os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("set SPARKCLAW_TEST_POSTGRES_DSN for isolated postgres qualification")
				}
				reopen = func() testBackend {
					st, err := NewPostgresStore(t.Context(), dsn)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(st.Close)
					return st
				}
				repository = reopen()
				truncatePostgresStore(t, repository.(*PostgresStore))
			}
			session := mustCreateSession(t, repository, "document scope persistence")
			facts := map[string]string{
				"document_read_task_mode":            "exhaustive",
				"document_read_scope_reason":         "全部记录，包括附录",
				"document_read_scope_selector":       `{"all":true}`,
				"document_read_source_sha256":        "frozen-source-hash",
				"document_read_structure_generation": "frozen-generation",
				"document_read_next_cursor":          "opaque.cursor.with-signature",
				"document_read_coverage":             `{"required":12,"covered":5,"missing":7,"status":"partial"}`,
				"document_read_scope_status":         "partial",
			}
			input := app.AgentRun{ID: "run-document-scope", SessionID: session.ID, State: "executing", StartedAt: time.Now().UTC(),
				Workflow: &app.WorkflowState{Plan: app.WorkflowPlan{SchemaVersion: 1, ProfileID: app.WorkflowDocumentRead, ProfileRevision: 6}, Route: app.RouteDecision{Facts: facts}}}
			if _, err := repository.SaveRun(t.Context(), input); err != nil {
				t.Fatal(err)
			}
			if reopen != nil {
				repository = reopen()
			}
			got, found, err := repository.GetRun(t.Context(), input.ID)
			if err != nil || !found || got.Workflow == nil || got.Workflow.Plan.ProfileRevision != 6 || !reflect.DeepEqual(got.Workflow.Route.Facts, facts) {
				t.Fatalf("frozen scope changed across %s restart: %+v found=%v err=%v", backend, got.Workflow, found, err)
			}
			got.Workflow.Route.Facts["document_read_next_cursor"] = "mutated-read"
			again, _, err := repository.GetRun(t.Context(), input.ID)
			if err != nil || again.Workflow.Route.Facts["document_read_next_cursor"] != facts["document_read_next_cursor"] {
				t.Fatal("read exposed a mutable scope alias")
			}
		})
	}
}
