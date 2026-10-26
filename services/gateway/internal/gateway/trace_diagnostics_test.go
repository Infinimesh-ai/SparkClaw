package gateway

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
)

func TestFeedbackReportsTraceFailureAndKeepsHTTPAndBusinessResult(t *testing.T) {
	for _, stage := range []string{"local_write", "artifact_write"} {
		t.Run(stage, func(t *testing.T) {
			root := t.TempDir()
			cfg := testConfig(root)
			blocked := filepath.Join(root, "private-backend-path")
			if err := os.WriteFile(blocked, []byte("private"), 0600); err != nil {
				t.Fatal(err)
			}
			if stage == "local_write" {
				cfg.Storage.TraceDir = filepath.Join(blocked, "trace")
			} else {
				cfg.Storage.ArtifactDir = blocked
			}
			repo := store.NewMemoryStore()
			tools := toolhub.New(cfg, repo)
			t.Cleanup(func() { _ = tools.Close() })
			writer := trace.NewWriterFromConfig(cfg)
			runtime := agent.NewRuntime(repo, tools, policy.New(cfg), modelrouter.New(cfg), writer)
			server := NewWithTrace(cfg, repo, tools, runtime, writer)
			ts := httptest.NewServer(server.Handler())
			defer ts.Close()
			sessionID := createTestSession(t, ts.URL)
			now := time.Now().UTC()
			run := app.AgentRun{ID: app.NewID("run"), SessionID: sessionID, State: "completed", StartedAt: now, CompletedAt: &now}
			testSaveRun(repo, run)
			before, found, err := repo.GetRun(t.Context(), run.ID)
			if err != nil || !found {
				t.Fatal(err)
			}
			var output bytes.Buffer
			prior := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			defer slog.SetDefault(prior)
			resp, err := http.Post(ts.URL+"/api/runs/"+run.ID+"/feedback", "application/json", strings.NewReader(`{"rating":"up","note":"private@feedback.example body"}`))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("trace failure changed feedback API status: %d", resp.StatusCode)
			}
			var feedback app.RunFeedback
			if err := json.NewDecoder(resp.Body).Decode(&feedback); err != nil {
				t.Fatal(err)
			}
			saved, err := repo.ListRunFeedback(t.Context(), run.ID)
			if err != nil || len(saved) != 1 || saved[0].ID != feedback.ID || saved[0].Note != "private@feedback.example body" {
				t.Fatalf("feedback was not preserved: %+v %v", saved, err)
			}
			after, found, err := repo.GetRun(t.Context(), run.ID)
			if err != nil || !found || !reflect.DeepEqual(before, after) {
				t.Fatalf("run changed: %+v %v", after, err)
			}
			var row map[string]any
			if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &row); err != nil {
				t.Fatalf("trace failure remained silent: %q (%v)", output.String(), err)
			}
			if row["msg"] != "trace refresh write unavailable" || row["run_id"] != run.ID || row["stage"] != stage || row["evidence"] != "incomplete" {
				t.Fatalf("wrong trace diagnostic: %#v", row)
			}
			for _, private := range []string{root, "private", "@", "body"} {
				if strings.Contains(output.String(), private) {
					t.Fatalf("private evidence leaked: %s", output.String())
				}
			}
		})
	}
}
