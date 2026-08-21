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
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
)

func TestObservationReadIsSessionScopedAndWindowed(t *testing.T) {
	cfg := config.Default()
	cfg.Storage.ArtifactDir = filepath.Join(t.TempDir(), "artifacts")
	st := store.NewMemoryStore()
	ownerSession := storetest.MustCreateSession(t, st, "owner")
	otherSession := storetest.MustCreateSession(t, st, "other")
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

func TestObservationReadTrimsTrailingPartialRune(t *testing.T) {
	cfg := config.Default()
	cfg.Storage.ArtifactDir = filepath.Join(t.TempDir(), "artifacts")
	st := store.NewMemoryStore()
	session := storetest.MustCreateSession(t, st, "owner")
	artifacts := artifact.NewStore(cfg.Storage)
	hub := New(cfg, st).WithArtifactStore(artifacts)
	now := time.Now().UTC()
	call := app.ToolCall{
		ID: "tc_trim", SessionID: session.ID, RunID: "run_trim", Tool: "pdf.extract_text",
		Status: "completed", StartedAt: now, CompletedAt: &now,
	}
	uri := store.ArchiveToolObservation(context.Background(), st, artifacts, call, map[string]any{
		"content": "alpha 世界 omega",
	})
	if uri == "" {
		t.Fatal("fixture observation was not archived")
	}
	full, err := hub.Execute(context.Background(), "observation.read", map[string]any{
		"artifact_uri": uri, "max_bytes": maxObservationReadBytes,
	}, session.ID, "run_reader")
	if err != nil {
		t.Fatal(err)
	}
	content := full.Output.(map[string]any)["content"].(string)
	runeStart := strings.Index(content, "世")
	if runeStart < 0 {
		t.Fatalf("multi-byte fixture is missing from archived output: %s", content)
	}

	// A window boundary one byte into the rune must trim exactly the partial
	// rune and hand back its start as next_offset.
	cut, err := hub.Execute(context.Background(), "observation.read", map[string]any{
		"artifact_uri": uri, "offset": 0, "max_bytes": runeStart + 1,
	}, session.ID, "run_reader")
	if err != nil {
		t.Fatal(err)
	}
	output := cut.Output.(map[string]any)
	if output["bytes"].(int) != runeStart || output["next_offset"].(int) != runeStart || !output["truncated"].(bool) {
		t.Fatalf("trailing partial rune was not trimmed to the rune boundary: %#v", output)
	}
	resumed, err := hub.Execute(context.Background(), "observation.read", map[string]any{
		"artifact_uri": uri, "offset": output["next_offset"], "max_bytes": maxObservationReadBytes,
	}, session.ID, "run_reader")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(resumed.Output.(map[string]any)["content"].(string), "世") {
		t.Fatalf("resume from next_offset lost the trimmed rune: %#v", resumed.Output)
	}
}

func TestObservationReadReportsBinaryContentWithNextOffset(t *testing.T) {
	cfg := config.Default()
	cfg.Storage.ArtifactDir = filepath.Join(t.TempDir(), "artifacts")
	st := store.NewMemoryStore()
	session := storetest.MustCreateSession(t, st, "owner")
	artifacts := artifact.NewStore(cfg.Storage)
	hub := New(cfg, st).WithArtifactStore(artifacts)

	// Archived output whose JSON string carries raw invalid UTF-8 bytes, as a
	// binary tool observation would. record.Output = "AB\xff\xfeCD" (8 bytes
	// with quotes); the invalid run spans offsets 3-4.
	raw := append([]byte(`{"output":"AB`), 0xFF, 0xFE)
	raw = append(raw, []byte(`CD"}`)...)
	object, err := artifacts.Put(context.Background(), "observations/run_bin/tc_bin.json", "application/json", raw)
	if err != nil {
		t.Fatal(err)
	}
	st.SaveArtifactObject(app.ArtifactObject{
		ID: "obj_bin", Kind: "tool_observation", RunID: "run_bin", SessionID: session.ID,
		Backend: object.Backend, Bucket: object.Bucket, Key: object.Key, URI: object.URI,
		Path: object.Path, ContentType: object.ContentType, Bytes: object.Bytes,
		CreatedAt: time.Now().UTC(),
	})

	_, err = hub.Execute(context.Background(), "observation.read", map[string]any{
		"artifact_uri": object.URI,
	}, session.ID, "run_reader")
	if err == nil {
		t.Fatal("mid-window invalid UTF-8 did not fail")
	}
	if app.ToolErrorCodeFrom(err) != app.ToolErrorObservationBinaryContent {
		t.Fatalf("binary content error carries the wrong code: %v (%s)", err, app.ToolErrorCodeFrom(err))
	}
	if !strings.Contains(err.Error(), "offset 3") || !strings.Contains(err.Error(), "offset=5") {
		t.Fatalf("binary content error lacks a usable next offset: %v", err)
	}
	resumed, err := hub.Execute(context.Background(), "observation.read", map[string]any{
		"artifact_uri": object.URI, "offset": 5,
	}, session.ID, "run_reader")
	if err != nil {
		t.Fatalf("skipping past the binary region failed: %v", err)
	}
	output := resumed.Output.(map[string]any)
	if output["content"].(string) != `CD"` || output["truncated"].(bool) {
		t.Fatalf("resume past the binary region returned the wrong window: %#v", output)
	}
}
