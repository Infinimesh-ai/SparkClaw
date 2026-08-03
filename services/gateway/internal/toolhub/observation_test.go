package toolhub

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/artifact"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestObservationReadIsSessionScopedAndWindowed(t *testing.T) {
	cfg := config.Default()
	cfg.Storage.ArtifactDir = filepath.Join(t.TempDir(), "artifacts")
	st := store.NewMemoryStore()
	ownerSession := st.CreateSession("owner")
	otherSession := st.CreateSession("other")
	artifacts := artifact.NewStore(cfg.Storage)
	hub := New(cfg, st).WithArtifactStore(artifacts)
	now := time.Now().UTC()
	call := app.ToolCall{
		ID: "tc_source", SessionID: ownerSession.ID, RunID: "run_source", Tool: "pdf.extract_text",
		Status: "completed", StartedAt: now, CompletedAt: &now,
	}
	uri := store.ArchiveToolObservation(context.Background(), st, artifacts, call, map[string]any{
		"content": "alpha beta 世界 gamma delta", "truncated": false,
	})
	if uri == "" {
		t.Fatal("fixture observation was not archived")
	}

	result, err := hub.Execute(context.Background(), "observation.read", map[string]any{
		"artifact_uri": uri, "offset": 0, "max_bytes": 48,
	}, ownerSession.ID, "run_reader")
	if err != nil {
		t.Fatal(err)
	}
	output := result.Output.(map[string]any)
	if output["artifact_uri"] != uri || output["bytes"].(int) > 48 || !strings.Contains(output["content"].(string), "alpha") {
		t.Fatalf("unexpected observation window: %#v", output)
	}
	full, err := hub.Execute(context.Background(), "observation.read", map[string]any{
		"artifact_uri": uri, "offset": 0, "max_bytes": maxObservationReadBytes,
	}, ownerSession.ID, "run_reader")
	if err != nil {
		t.Fatal(err)
	}
	fullContent := full.Output.(map[string]any)["content"].(string)
	firstRune := strings.Index(fullContent, "世")
	if firstRune < 0 {
		t.Fatalf("UTF-8 fixture is missing from archived output: %s", fullContent)
	}
	aligned, err := hub.Execute(context.Background(), "observation.read", map[string]any{
		"artifact_uri": uri, "offset": firstRune + 1, "max_bytes": 4,
	}, ownerSession.ID, "run_reader")
	if err != nil {
		t.Fatal(err)
	}
	alignedOutput := aligned.Output.(map[string]any)
	if alignedOutput["offset"].(int) != firstRune+len("世") || !strings.HasPrefix(alignedOutput["content"].(string), "界") || alignedOutput["next_offset"].(int) <= alignedOutput["offset"].(int) {
		t.Fatalf("UTF-8 window did not align and advance: %#v", alignedOutput)
	}
	if _, err := hub.Execute(context.Background(), "observation.read", map[string]any{
		"artifact_uri": uri, "offset": firstRune, "max_bytes": 1,
	}, ownerSession.ID, "run_reader"); err == nil || !strings.Contains(err.Error(), "too small") {
		t.Fatalf("non-advancing UTF-8 window was not rejected: %v", err)
	}
	if _, err := hub.Execute(context.Background(), "observation.read", map[string]any{
		"artifact_uri": uri,
	}, otherSession.ID, "run_other"); err == nil || !strings.Contains(err.Error(), "current session") {
		t.Fatalf("cross-session artifact read was not rejected: %v", err)
	}
}
