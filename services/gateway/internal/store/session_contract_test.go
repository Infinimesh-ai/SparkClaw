package store

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type sessionRepositoryHarness struct {
	repository SessionRepository
	memory     *MemoryStore
	restart    func(testing.TB) SessionRepository
}

func newSessionRepositoryHarness(t testing.TB, backend string) sessionRepositoryHarness {
	t.Helper()
	switch backend {
	case "memory":
		memory := NewMemoryStore()
		return sessionRepositoryHarness{repository: memory, memory: memory}
	case "file":
		path := t.TempDir() + "/state.json"
		file, err := NewFileStore(path)
		if err != nil {
			t.Fatal(err)
		}
		return sessionRepositoryHarness{
			repository: file,
			memory:     file.inner,
			restart: func(t testing.TB) SessionRepository {
				t.Helper()
				restarted, err := NewFileStore(path)
				if err != nil {
					t.Fatal(err)
				}
				return restarted
			},
		}
	default:
		t.Fatalf("unknown backend %q", backend)
		return sessionRepositoryHarness{}
	}
}

func TestSessionRepositoryMemoryAndFileContract(t *testing.T) {
	for _, backend := range []string{"memory", "file"} {
		t.Run(backend, func(t *testing.T) {
			harness := newSessionRepositoryHarness(t, backend)
			initialNow := time.Date(2026, 8, 21, 8, 30, 0, 999, time.FixedZone("test", 8*60*60))
			harness.memory.sessionNow = func() time.Time { return initialNow }

			first, err := harness.repository.CreateSessionWithScope(t.Context(), "  Preserved title  ", " owner-a ", " /workspace/a ", " telegram ", false)
			if err != nil {
				t.Fatal(err)
			}
			if first.Title != "  Preserved title  " || first.OwnerID != "owner-a" || first.WorkspaceRoot != "/workspace/a" || first.Source != "telegram" ||
				!first.CreatedAt.Equal(first.UpdatedAt) || !validSessionTime(first.CreatedAt) {
				t.Fatalf("normalized session = %#v", first)
			}
			second, err := harness.repository.CreateSession(t.Context(), "   ")
			if err != nil {
				t.Fatal(err)
			}
			if second.Title != "New SparkClaw Session" || second.OwnerID != app.DefaultOwnerID || second.Source != "webchat" {
				t.Fatalf("default session = %#v", second)
			}
			hidden, err := harness.repository.CreateSessionWithScope(t.Context(), "hidden", "owner-a", "", "weixin", true)
			if err != nil {
				t.Fatal(err)
			}

			listed, err := harness.repository.ListSessions(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			wantIDs := []string{first.ID, second.ID}
			slices.Sort(wantIDs)
			if len(listed) != 2 {
				t.Fatalf("visible deterministic list = %#v, want IDs %v", listed, wantIDs)
			}
			gotIDs := []string{listed[0].ID, listed[1].ID}
			if !reflect.DeepEqual(gotIDs, wantIDs) {
				t.Fatalf("visible deterministic list = %#v, want IDs %v", listed, wantIDs)
			}
			if got, found, err := harness.repository.GetSession(t.Context(), hidden.ID); err != nil || !found || got.ID != hidden.ID {
				t.Fatalf("exact hidden get = %#v found=%v err=%v", got, found, err)
			}
			if got, found, err := harness.repository.GetSession(t.Context(), "missing"); err != nil || found || !reflect.ValueOf(got).IsZero() {
				t.Fatalf("normal absence = %#v found=%v err=%v", got, found, err)
			}

			harness.memory.sessionNow = func() time.Time { return initialNow.Add(-time.Hour) }
			updated, err := harness.repository.UpdateSessionTitle(t.Context(), first.ID, "  renamed  ")
			if err != nil {
				t.Fatal(err)
			}
			if updated.Title != "renamed" || !updated.UpdatedAt.After(first.UpdatedAt) {
				t.Fatalf("high-water update = %#v, previous %#v", updated, first)
			}

			mcp, err := harness.repository.CreateSessionWithScope(t.Context(), "AI conversation", "owner-a", "", " mcp ", false)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := harness.repository.UpdateSessionTitle(t.Context(), mcp.ID, "changed"); StoreErrorCodeOf(err) != StoreErrorConflict {
				t.Fatalf("MCP rename error = %v", err)
			}
			if _, err := harness.repository.DeleteSession(t.Context(), mcp.ID); StoreErrorCodeOf(err) != StoreErrorConflict {
				t.Fatalf("MCP delete error = %v", err)
			}

			canceled, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := harness.repository.CreateSession(canceled, "canceled"); StoreErrorCodeOf(err) != StoreErrorCanceled {
				t.Fatalf("canceled create error = %v", err)
			}
			if _, err := harness.repository.ListSessions(canceled); StoreErrorCodeOf(err) != StoreErrorCanceled {
				t.Fatalf("canceled list error = %v", err)
			}

			if harness.restart != nil {
				restarted := harness.restart(t)
				stored, found, err := restarted.GetSession(t.Context(), updated.ID)
				if err != nil || !found || !sessionsEqual(stored, updated) {
					t.Fatalf("restart session = %#v found=%v err=%v", stored, found, err)
				}
			}
		})
	}
}

func TestSessionRepositoryLifecycleRowsAreAtomic(t *testing.T) {
	store := NewMemoryStore()
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	store.sessionNow = func() time.Time { return now }
	session, err := store.CreateSession(t.Context(), "lifecycle")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := store.UpdateSessionTitle(t.Context(), session.ID, "updated")
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := store.DeleteSession(t.Context(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !sessionsEqual(deleted, updated) {
		t.Fatalf("deleted session = %#v, want %#v", deleted, updated)
	}
	if scoped := mustListAudit(t, store, session.ID); len(scoped) != 0 {
		t.Fatalf("deleted session retained scoped audit: %#v", scoped)
	}
	if scoped := mustEventsAfter(t, store, session.ID, ""); len(scoped) != 0 {
		t.Fatalf("deleted session retained scoped events: %#v", scoped)
	}
	audits := mustListAudit(t, store, "")
	events := mustEventsAfter(t, store, "", "")
	if len(audits) != 1 || audits[0].Type != "session.deleted" || audits[0].SessionID != "" ||
		len(events) != 1 || events[0].Type != "session.deleted" || events[0].SessionID != "" {
		t.Fatalf("replacement lifecycle audit=%#v events=%#v", audits, events)
	}
	payload, ok := events[0].Payload.(app.Session)
	if !ok || !sessionsEqual(payload, deleted) {
		t.Fatalf("deleted lifecycle payload = %#v", events[0].Payload)
	}
}

func TestSessionRepositoryRejectsCorruptPersistedState(t *testing.T) {
	store := NewMemoryStore()
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	store.sessions["corrupt"] = app.Session{
		ID: "corrupt", OwnerID: " owner ", Title: "title", Source: "webchat", CreatedAt: now, UpdatedAt: now,
	}
	if _, _, err := store.GetSession(t.Context(), "corrupt"); StoreErrorCodeOf(err) != StoreErrorCorrupt {
		t.Fatalf("corrupt get error = %v", err)
	}
	if _, err := store.ListSessions(t.Context()); StoreErrorCodeOf(err) != StoreErrorCorrupt {
		t.Fatalf("corrupt list error = %v", err)
	}
}
