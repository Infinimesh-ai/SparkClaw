package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestLatestModelCallsByLaneMemoryAndFileContract(t *testing.T) {
	for _, backend := range []string{"memory", "file"} {
		t.Run(backend, func(t *testing.T) {
			var repository testBackend
			var restart func() testBackend
			switch backend {
			case "memory":
				repository = NewMemoryStore()
			case "file":
				path := filepath.Join(t.TempDir(), "state.json")
				file, err := NewFileStore(path)
				if err != nil {
					t.Fatal(err)
				}
				repository = file
				restart = func() testBackend {
					reloaded, err := NewFileStore(path)
					if err != nil {
						t.Fatal(err)
					}
					return reloaded
				}
			}
			runLatestModelCallsByLaneContract(t, repository)
			if restart != nil {
				runLatestModelCallsByLaneReadAssertions(t, restart())
			}
		})
	}
}

func TestLatestModelCallsByLanePostgresContract(t *testing.T) {
	dsn := os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set SPARKCLAW_TEST_POSTGRES_DSN to run postgres store integration tests")
	}
	repository, err := NewPostgresStore(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	truncatePostgresStore(t, repository)
	runLatestModelCallsByLaneContract(t, repository)
}

var latestModelCallBase = time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC)

func runLatestModelCallsByLaneContract(t *testing.T, repository testBackend) {
	t.Helper()
	ctx := t.Context()
	empty, err := repository.LatestModelCallsByLane(ctx)
	if err != nil || len(empty) != 0 {
		t.Fatalf("latest calls on an empty store = %#v err=%v", empty, err)
	}
	session := mustCreateSession(t, repository, "latest model calls")
	for _, call := range []app.ModelCall{
		{ID: "fast-old", SessionID: session.ID, Lane: "fast", Profile: "fast", Model: "m", Operation: "chat", Status: "failed", Error: "old failure", StartedAt: latestModelCallBase},
		{ID: "fast-new", SessionID: session.ID, Lane: "fast", Profile: "fast", Model: "m", Operation: "chat", Status: "completed", StartedAt: latestModelCallBase.Add(time.Minute)},
		{ID: "fast-stale-id", Lane: "fast", Profile: "fast", Model: "m", Operation: "chat", Status: "failed", StartedAt: latestModelCallBase.Add(30 * time.Second)},
		{ID: "embedding-a", Lane: "embedding", Profile: "embedding", Model: "e", Operation: "embed", Status: "failed", StartedAt: latestModelCallBase.Add(2 * time.Minute)},
		{ID: "embedding-b", Lane: "embedding", Profile: "embedding", Model: "e", Operation: "embed", Status: "completed", StartedAt: latestModelCallBase.Add(2 * time.Minute)},
		{ID: "guard-only", Lane: "guard", Profile: "guard", Model: "g", Operation: "moderate", Status: "started", StartedAt: latestModelCallBase.Add(3 * time.Minute)},
	} {
		if _, err := repository.SaveModelCall(ctx, call); err != nil {
			t.Fatal(err)
		}
	}
	runLatestModelCallsByLaneReadAssertions(t, repository)

	// Re-saving an existing call moves it in the recency order; the latest
	// read must follow the persisted state rather than insertion order.
	if _, err := repository.SaveModelCall(ctx, app.ModelCall{
		ID: "fast-old", SessionID: session.ID, Lane: "fast", Profile: "fast", Model: "m", Operation: "chat", Status: "completed",
		StartedAt: latestModelCallBase.Add(10 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	latest, err := repository.LatestModelCallsByLane(ctx)
	if err != nil || latest["fast"].ID != "fast-old" || latest["fast"].Status != "completed" {
		t.Fatalf("latest fast call after re-save = %#v err=%v", latest["fast"], err)
	}
	if _, err := repository.SaveModelCall(ctx, app.ModelCall{
		ID: "fast-old", SessionID: session.ID, Lane: "fast", Profile: "fast", Model: "m", Operation: "chat", Status: "failed",
		Error: "old failure", StartedAt: latestModelCallBase,
	}); err != nil {
		t.Fatal(err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := repository.LatestModelCallsByLane(cancelled); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("canceled latest-model-call error = %v", err)
	}
}

func runLatestModelCallsByLaneReadAssertions(t *testing.T, repository testBackend) {
	t.Helper()
	latest, err := repository.LatestModelCallsByLane(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(latest) != 3 {
		t.Fatalf("latest calls by lane = %#v", latest)
	}
	if got := latest["fast"]; got.ID != "fast-new" || got.Status != "completed" || got.SessionID == "" {
		t.Fatalf("latest fast call = %#v", got)
	}
	if got := latest["embedding"]; got.ID != "embedding-b" || got.Status != "completed" {
		t.Fatalf("latest embedding call (same started_at, larger ID wins) = %#v", got)
	}
	if got := latest["guard"]; got.ID != "guard-only" || got.Status != "started" {
		t.Fatalf("latest guard call = %#v", got)
	}
	if !latest["fast"].StartedAt.Equal(latestModelCallBase.Add(time.Minute)) {
		t.Fatalf("latest fast started_at = %s", latest["fast"].StartedAt)
	}
}
