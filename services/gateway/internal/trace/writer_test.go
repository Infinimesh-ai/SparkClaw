package trace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

func TestWriterStoresTraceArtifact(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Storage.TraceDir = filepath.Join(root, "traces")
	cfg.Storage.ArtifactBackend = "filesystem"
	cfg.Storage.ArtifactDir = filepath.Join(root, "artifacts")
	cfg.Storage.ArtifactBucket = "sparkclaw-test"

	writer := NewWriterFromConfig(cfg)
	run := app.AgentRun{
		ID:        "run_trace_artifact",
		SessionID: "s1",
		State:     "completed",
		ModelLane: "fast",
		Risk:      app.RiskRead,
		StartedAt: time.Now().UTC(),
	}
	if err := writer.WriteRun(context.Background(), RunTrace{Run: run}); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(cfg.Storage.TraceDir, run.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var decoded RunTrace
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Artifact == nil || decoded.Artifact.URI != "artifact://sparkclaw-test/traces/run_trace_artifact.json" {
		t.Fatalf("trace artifact metadata missing: %#v", decoded.Artifact)
	}
	if _, err := os.Stat(filepath.Join(cfg.Storage.ArtifactDir, "sparkclaw-test", "traces", run.ID+".json")); err != nil {
		t.Fatal(err)
	}
}

func TestWriterRedactsSecretsInTraceArtifacts(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Storage.TraceDir = filepath.Join(root, "traces")
	cfg.Storage.ArtifactBackend = "filesystem"
	cfg.Storage.ArtifactDir = filepath.Join(root, "artifacts")
	cfg.Storage.ArtifactBucket = "sparkclaw-test"
	cfg.Logging.RedactPatterns = []string{"api_key", "password", "token", "authorization"}

	writer := NewWriterFromConfig(cfg)
	run := app.AgentRun{
		ID:        "run_trace_redaction",
		SessionID: "s1",
		State:     "completed",
		ModelLane: "fast",
		Risk:      app.RiskDraft,
		StartedAt: time.Now().UTC(),
		Summary:   "used api_key sk-run-secret",
	}
	trace := RunTrace{
		Run: run,
		Messages: []app.Message{{
			ID:        "m1",
			SessionID: "s1",
			Role:      "user",
			Content:   "Remember api_key is sk-message-secret",
			CreatedAt: time.Now().UTC(),
		}},
		ToolCalls: []app.ToolCall{{
			ID:        "tc1",
			SessionID: "s1",
			RunID:     run.ID,
			Tool:      "file.delete",
			Risk:      app.RiskDangerous,
			Status:    "approval_pending",
			Arguments: map[string]any{
				"token":   "tool-secret",
				"subject": "Safe subject",
				"nested": map[string]any{
					"password": "nested-secret",
				},
			},
			Result: map[string]any{"authorization": "Bearer result-secret"},
		}},
		ModelCalls: []app.ModelCall{{
			ID:        "mcall1",
			SessionID: "s1",
			RunID:     run.ID,
			Lane:      "fast",
			Profile:   "sparkclaw-fast",
			Model:     "Qwen/Fast",
			Operation: "chat",
			Status:    "failed",
			Error:     "authorization Bearer model-secret",
			StartedAt: time.Now().UTC(),
		}},
		Approvals: []app.Approval{{
			ID:        "ap1",
			SessionID: "s1",
			RunID:     run.ID,
			Tool:      "file.delete",
			Status:    "pending",
			Summary:   "Approve token approval-secret",
			Reason:    "password is reason-secret",
			Arguments: map[string]any{
				"api_key": "approval-secret",
				"body":    "safe body",
			},
		}},
		Audit: []app.AuditEvent{{
			ID:        "audit1",
			SessionID: "s1",
			RunID:     run.ID,
			Actor:     "agent",
			Type:      "test",
			Summary:   "authorization Bearer audit-secret",
			Fields: map[string]any{
				"password": "audit-field-secret",
			},
		}},
	}
	if err := writer.WriteRun(context.Background(), trace); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(cfg.Storage.TraceDir, run.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, leaked := range []string{
		"sk-run-secret",
		"sk-message-secret",
		"tool-secret",
		"model-secret",
		"nested-secret",
		"result-secret",
		"approval-secret",
		"reason-secret",
		"audit-secret",
		"audit-field-secret",
	} {
		if strings.Contains(body, leaked) {
			t.Fatalf("trace leaked %q in:\n%s", leaked, body)
		}
	}
	if !strings.Contains(body, "[REDACTED]") || !strings.Contains(body, "Safe subject") || !strings.Contains(body, "safe body") {
		t.Fatalf("trace redaction lost expected fields:\n%s", body)
	}

	rawArtifact, err := os.ReadFile(filepath.Join(cfg.Storage.ArtifactDir, "sparkclaw-test", "traces", run.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rawArtifact), "tool-secret") {
		t.Fatalf("artifact trace was not redacted:\n%s", string(rawArtifact))
	}
}
