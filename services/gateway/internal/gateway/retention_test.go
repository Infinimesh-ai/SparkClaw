package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
