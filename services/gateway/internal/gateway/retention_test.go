package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/artifact"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
)

func testPassiveNotificationForRetention(id string, createdAt time.Time) app.PassiveNotification {
	return app.PassiveNotification{
		ID:             id,
		OwnerID:        app.DefaultOwnerID,
		EndpointID:     "endpoint-retention",
		IdempotencyKey: "delivery-" + id,
		Fingerprint:    "fingerprint-" + id,
		NotificationID: "external-" + id,
		Source:         "webchat",
		Kind:           "message",
		OccurredAt:     createdAt,
		CreatedAt:      createdAt,
		UpdatedAt:      createdAt,
	}
}

func mustListRetentionNotifications(t testing.TB, repository store.PassiveNotificationRepository) []app.PassiveNotification {
	t.Helper()
	notifications, err := repository.ListPassiveNotifications(t.Context(), app.DefaultOwnerID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	return notifications
}

func TestRetentionSweepPrunesExpiredMemoriesAndNotifications(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.Memory.RetentionDays = 7
	cfg.PassiveNotifications.RetentionDays = 7

	now := time.Now().UTC().Truncate(time.Microsecond)
	session := app.Session{ID: "s_retention_sweep", OwnerID: app.DefaultOwnerID, Title: "Retention sweep", Source: "webchat", CreatedAt: now, UpdatedAt: now}
	run := app.AgentRun{ID: "run_retention_sweep", SessionID: session.ID, State: "completed", ModelLane: "fast", Risk: app.RiskRead, StartedAt: now}
	oldMemory := app.Memory{
		ID: "mem_old_sweep", Kind: "profile", Content: "SparkClaw old sweep memory",
		SourceID: run.ID, CreatedAt: now.AddDate(0, 0, -30),
	}
	freshMemory := app.Memory{
		ID: "mem_fresh_sweep", Kind: "profile", Content: "SparkClaw fresh sweep memory",
		SourceID: run.ID, CreatedAt: now,
	}
	oldNotification := testPassiveNotificationForRetention("sweep-old", now.AddDate(0, 0, -30))
	freshNotification := testPassiveNotificationForRetention("sweep-fresh", now)
	snapshot := store.Snapshot{
		Sessions: map[string]app.Session{session.ID: session},
		Runs:     map[string]app.AgentRun{run.ID: run},
		Memories: map[string]app.Memory{oldMemory.ID: oldMemory, freshMemory.ID: freshMemory},
		PassiveNotifications: map[string]app.PassiveNotification{
			oldNotification.ID:   oldNotification,
			freshNotification.ID: freshNotification,
		},
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(root, "state.json")
	if err := os.WriteFile(statePath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.NewFileStore(statePath)
	if err != nil {
		t.Fatal(err)
	}

	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)

	server.runRetentionSweep(t.Context())

	memories := mustSearchMemories(t, st, "sweep")
	if len(memories) != 1 || memories[0].ID != freshMemory.ID {
		t.Fatalf("sweep did not prune old memory: %#v", memories)
	}
	notifications := mustListRetentionNotifications(t, st)
	if len(notifications) != 1 || notifications[0].ID != freshNotification.ID {
		t.Fatalf("sweep did not prune old notification: %#v", notifications)
	}
}

func TestStartRetentionSweepsRunsImmediatelyAndStopsWithContext(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.PassiveNotifications.RetentionDays = 7

	st := store.NewMemoryStore()
	expired := testPassiveNotificationForRetention("start-old", time.Now().UTC().AddDate(0, 0, -30))
	if _, inserted, err := st.CreatePassiveNotification(t.Context(), expired); err != nil || !inserted {
		t.Fatalf("seed notification = %v, %v", inserted, err)
	}

	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)

	ctx, cancel := context.WithCancel(context.Background())
	server.StartRetentionSweeps(ctx)

	deadline := time.Now().Add(5 * time.Second)
	for len(mustListRetentionNotifications(t, st)) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("initial sweep never pruned expired notification: %#v", mustListRetentionNotifications(t, st))
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	waitCtx, cancelWait := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelWait()
	if err := server.WaitForBackgroundWork(waitCtx); err != nil {
		t.Fatalf("retention coordinator did not stop with lifecycle context: %v", err)
	}
}

func TestRetentionSweepRemovesExpiredSealedPPTXCandidatesWithinBound(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	files, ok := server.artifacts.(artifact.FileStore)
	if !ok {
		t.Fatalf("expected file-backed artifacts, got %T", server.artifacts)
	}

	// One sweep-bound plus a partial page of expired objects, and one fresh
	// candidate pair that must survive every sweep.
	expired := time.Now().Add(-25 * time.Hour)
	expiredKeys := make([]string, 0, pptxSealedSweepLimit+50)
	for index := 0; index < pptxSealedSweepLimit+50; index++ {
		key := fmt.Sprintf("pptx/sealed/rejected%03d/%s.json", index/2, []string{"candidate", "manifest"}[index%2])
		if _, err := server.artifacts.Put(t.Context(), key, "application/json", []byte("{}")); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(filepath.Join(files.Root, files.Bucket, filepath.FromSlash(key)), expired, expired); err != nil {
			t.Fatal(err)
		}
		expiredKeys = append(expiredKeys, key)
	}
	for _, key := range []string{"pptx/sealed/zfresh/candidate.pptx", "pptx/sealed/zfresh/manifest.json"} {
		if _, err := server.artifacts.Put(t.Context(), key, "application/octet-stream", []byte("fresh")); err != nil {
			t.Fatal(err)
		}
	}
	countSealed := func() int {
		objects, err := server.artifacts.List(t.Context(), "pptx/sealed/", "", 1000)
		if err != nil {
			t.Fatal(err)
		}
		return len(objects)
	}

	server.runRetentionSweep(t.Context())
	if remaining := countSealed(); remaining != len(expiredKeys)+2-pptxSealedSweepLimit {
		t.Fatalf("first sweep left %d sealed objects, want exactly the bound removed", remaining)
	}
	if server.pptxSealedSweepCursor == "" {
		t.Fatal("full sweep page did not record a resume cursor")
	}
	server.runRetentionSweep(t.Context())
	if remaining := countSealed(); remaining != 2 {
		t.Fatalf("second sweep left %d sealed objects, want only the fresh pair", remaining)
	}
	if server.pptxSealedSweepCursor != "" {
		t.Fatalf("exhausted sweep kept a cursor: %q", server.pptxSealedSweepCursor)
	}
	server.runRetentionSweep(t.Context())
	if remaining := countSealed(); remaining != 2 {
		t.Fatalf("idle sweep touched the fresh candidate pair: %d objects remain", remaining)
	}
	events, err := st.ListAudit(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	audited := 0
	for _, event := range events {
		if event.Type == "document.pptx.candidate_expired" {
			audited++
		}
	}
	if audited != 2 {
		t.Fatalf("expected one expiry audit per sweep that deleted, got %d", audited)
	}
}

func TestRetentionSweepSurvivesArtifactBackendWithoutListing(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	st := store.NewMemoryStore()
	tools := toolhub.New(cfg, st).WithArtifactStore(artifact.NotImplementedStore{Backend: "gcs"})
	runtime := agent.NewRuntime(st, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, st, tools, runtime)
	server.pptxSealedSweepCursor = "pptx/sealed/stale"
	server.runRetentionSweep(t.Context())
	if server.pptxSealedSweepCursor != "" {
		t.Fatalf("failed listing kept a stale cursor: %q", server.pptxSealedSweepCursor)
	}
}
