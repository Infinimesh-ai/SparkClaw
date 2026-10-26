package agent

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
)

func TestAgentReportsTraceWriteFailureWithoutChangingRun(t *testing.T) {
	for _, tc := range []struct{ name, boundary, id string }{
		{name: "local_write", boundary: "local_write", id: app.NewID("run")},
		{name: "artifact_write", boundary: "artifact_write", id: app.NewID("run")},
		{name: "private_identity", boundary: "local_write", id: "private@identity.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			prior := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			t.Cleanup(func() { slog.SetDefault(prior) })
			root := t.TempDir()
			blocked := filepath.Join(root, "private-credential-path")
			if err := os.WriteFile(blocked, []byte("unwritable as directory"), 0600); err != nil {
				t.Fatal(err)
			}
			cfg := config.Default()
			cfg.Storage.TraceDir = filepath.Join(root, "trace")
			cfg.Storage.ArtifactBackend = "filesystem"
			cfg.Storage.ArtifactBucket = "bucket"
			cfg.Storage.ArtifactDir = blocked
			if tc.boundary == "local_write" {
				cfg.Storage.TraceDir = filepath.Join(blocked, "trace")
			}
			run := app.AgentRun{ID: tc.id, SessionID: "session", State: "completed", Summary: "owner@example.test body subject"}
			repo := store.NewMemoryStore()
			runtime := Runtime{store: repo, traces: trace.NewWriterFromConfig(cfg)}
			runtime.writeTrace(t.Context(), run, modelrouter.ChatResult{}, nil, nil, nil, nil)
			var row map[string]any
			if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &row); err != nil {
				t.Fatalf("missing diagnostic: %q (%v)", output.String(), err)
			}
			wantID := run.ID
			if tc.name == "private_identity" {
				wantID = ""
			}
			if row["msg"] != "agent trace write unavailable" || row["run_id"] != wantID || row["stage"] != tc.boundary || row["evidence"] != "incomplete" {
				t.Fatalf("unexpected diagnostic: %#v", row)
			}
			for _, private := range []string{root, "credential", "@", "body subject"} {
				if strings.Contains(output.String(), private) {
					t.Fatalf("diagnostic leaked %q", private)
				}
			}
			if run.State != "completed" {
				t.Fatal("trace failure changed run result")
			}
			objects, err := repo.ListArtifactObjects(t.Context(), 10)
			if err != nil || len(objects) != 0 {
				t.Fatalf("unwritten artifact indexed as evidence: %+v %v", objects, err)
			}
		})
	}
}
