package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestFileSessionDefiniteFailuresRestoreCompleteState(t *testing.T) {
	for _, stage := range []string{"encode", "mkdir", "create", "write", "file_sync", "file_close"} {
		t.Run(stage, func(t *testing.T) {
			store, err := NewFileStore(t.TempDir() + "/state.json")
			if err != nil {
				t.Fatal(err)
			}
			session, err := store.CreateSession(t.Context(), "before")
			if err != nil {
				t.Fatal(err)
			}
			before := store.captureFileRollback()
			store.commitOps = &controlledFileCommitOps{failStage: stage, failRemaining: 1}
			candidate, err := store.UpdateSessionTitle(t.Context(), session.ID, "after")
			if candidate.ID != "" || StoreErrorCodeOf(err) != StoreErrorDurability || !errorsIsFileCommitInjected(err) {
				t.Fatalf("stage=%s candidate=%#v err=%v code=%q", stage, candidate, err, StoreErrorCodeOf(err))
			}
			if after := store.captureFileRollback(); !reflect.DeepEqual(after, before) {
				t.Fatalf("stage %s did not restore the complete snapshot", stage)
			}
		})
	}

	t.Run("rename retained previous", func(t *testing.T) {
		store, err := NewFileStore(t.TempDir() + "/state.json")
		if err != nil {
			t.Fatal(err)
		}
		session, err := store.CreateSession(t.Context(), "before")
		if err != nil {
			t.Fatal(err)
		}
		before := store.captureFileRollback()
		store.commitOps = &controlledFileCommitOps{failStage: "rename", failRemaining: 1}
		candidate, err := store.UpdateSessionTitle(t.Context(), session.ID, "after")
		if candidate.ID != "" || StoreErrorCodeOf(err) != StoreErrorDurability || store.currentFileFence() != nil {
			t.Fatalf("candidate=%#v err=%v code=%q fence=%v", candidate, err, StoreErrorCodeOf(err), store.currentFileFence())
		}
		if after := store.captureFileRollback(); !reflect.DeepEqual(after, before) {
			t.Fatal("rename failure did not restore complete state")
		}
	})
}

func TestFileSessionSubmittedFailuresFenceAndReconcileCompleteSnapshot(t *testing.T) {
	for _, testCase := range []struct {
		name string
		ops  *controlledFileCommitOps
	}{
		{name: "rename applied", ops: &controlledFileCommitOps{failStage: "rename", failRemaining: 1, renameApplied: true}},
		{name: "directory open", ops: &controlledFileCommitOps{failStage: "dir_open", failRemaining: 1}},
		{name: "directory sync", ops: &controlledFileCommitOps{failStage: "dir_sync", failRemaining: 1}},
		{name: "directory close", ops: &controlledFileCommitOps{failStage: "dir_close", failRemaining: 1}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			store, err := NewFileStore(t.TempDir() + "/state.json")
			if err != nil {
				t.Fatal(err)
			}
			session, err := store.CreateSession(t.Context(), "before")
			if err != nil {
				t.Fatal(err)
			}
			store.commitOps = testCase.ops
			candidate, writeErr := store.UpdateSessionTitle(t.Context(), session.ID, "after")
			if candidate.ID != session.ID || StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome || store.currentFileFence() == nil {
				t.Fatalf("candidate=%#v err=%v code=%q fence=%v", candidate, writeErr, StoreErrorCodeOf(writeErr), store.currentFileFence())
			}
			reconciled, err := ReconcileSessionWrite(t.Context(), store, candidate, writeErr)
			if err != nil || !sessionsEqual(reconciled, candidate) || store.currentFileFence() != nil {
				t.Fatalf("reconciled=%#v err=%v fence=%v", reconciled, err, store.currentFileFence())
			}
			restarted, err := NewFileStore(store.path)
			if err != nil {
				t.Fatal(err)
			}
			stored, found, err := restarted.GetSession(t.Context(), session.ID)
			if err != nil || !found || !sessionsEqual(stored, candidate) {
				t.Fatalf("restart stored=%#v found=%v err=%v", stored, found, err)
			}
		})
	}

	t.Run("delete closure", func(t *testing.T) {
		store, err := NewFileStore(t.TempDir() + "/state.json")
		if err != nil {
			t.Fatal(err)
		}
		target, err := store.CreateSession(t.Context(), "target")
		if err != nil {
			t.Fatal(err)
		}
		other, err := store.CreateSession(t.Context(), "other")
		if err != nil {
			t.Fatal(err)
		}
		populateSessionDeleteFixture(store.inner, target.ID, other.ID)
		target, _, err = store.GetSession(t.Context(), target.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.persistSnapshot(); err != nil {
			t.Fatal(err)
		}
		store.commitOps = &controlledFileCommitOps{failStage: "dir_sync", failRemaining: 1}
		candidate, deleteErr := store.DeleteSession(t.Context(), target.ID)
		if candidate.ID != target.ID || StoreErrorCodeOf(deleteErr) != StoreErrorUnknownOutcome || store.currentFileFence() == nil {
			t.Fatalf("candidate=%#v err=%v fence=%v", candidate, deleteErr, store.currentFileFence())
		}
		reconciled, err := ReconcileSessionDelete(t.Context(), store, candidate, deleteErr)
		if err != nil || !sessionsEqual(reconciled, candidate) || store.currentFileFence() != nil {
			t.Fatalf("reconciled=%#v err=%v fence=%v", reconciled, err, store.currentFileFence())
		}
		assertSessionDeleteFixture(t, store.inner, target.ID, other.ID)
	})
}

func TestFileSessionCancellationPerformsNoWork(t *testing.T) {
	store, err := NewFileStore(t.TempDir() + "/state.json")
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateSession(t.Context(), "before")
	if err != nil {
		t.Fatal(err)
	}
	before := store.captureFileRollback()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.UpdateSessionTitle(ctx, session.ID, "after"); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("canceled update error = %v", err)
	}
	if after := store.captureFileRollback(); !reflect.DeepEqual(after, before) {
		t.Fatal("canceled update changed state")
	}
}

func TestFileSessionDeleteFailureRestoresCompleteClosure(t *testing.T) {
	store, err := NewFileStore(t.TempDir() + "/state.json")
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateSession(t.Context(), "target")
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateSession(t.Context(), "other")
	if err != nil {
		t.Fatal(err)
	}
	populateSessionDeleteFixture(store.inner, target.ID, other.ID)
	target, _, err = store.GetSession(t.Context(), target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.persistSnapshot(); err != nil {
		t.Fatal(err)
	}
	before := store.captureFileRollback()
	store.commitOps = &controlledFileCommitOps{failStage: "encode", failRemaining: 1}
	candidate, err := store.DeleteSession(t.Context(), target.ID)
	if candidate.ID != "" || StoreErrorCodeOf(err) != StoreErrorDurability {
		t.Fatalf("candidate=%#v err=%v", candidate, err)
	}
	assertSessionFileRollbackEqual(t, store.captureFileRollback(), before)
}

func TestFileSessionStartupRepairsLegacyDefaultsAndTimestampPrecision(t *testing.T) {
	path := t.TempDir() + "/state.json"
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateSession(t.Context(), "legacy")
	if err != nil {
		t.Fatal(err)
	}
	rewriteFileSessionSnapshot(t, path, session.ID, func(value app.Session) app.Session {
		value.OwnerID = "  "
		value.Source = ""
		value.CreatedAt = time.Date(2026, 8, 20, 12, 0, 0, 123456789, time.UTC)
		value.UpdatedAt = value.CreatedAt.Add(987654321 * time.Nanosecond)
		return value
	})
	restarted, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := restarted.GetSession(t.Context(), session.ID)
	if err != nil || !found || got.OwnerID != app.DefaultOwnerID || got.Source != "webchat" ||
		got.CreatedAt.Nanosecond() != 123456000 || got.UpdatedAt.Nanosecond() != 111111000 {
		t.Fatalf("legacy repair got=%#v found=%v err=%v", got, found, err)
	}

	for _, testCase := range []struct {
		name   string
		mutate func(app.Session) app.Session
	}{
		{name: "nonblank owner whitespace", mutate: func(value app.Session) app.Session { value.OwnerID = " owner "; return value }},
		{name: "nonblank source whitespace", mutate: func(value app.Session) app.Session { value.Source = " webchat "; return value }},
		{name: "workspace whitespace", mutate: func(value app.Session) app.Session { value.WorkspaceRoot = " /workspace "; return value }},
		{name: "key mismatch", mutate: func(value app.Session) app.Session { value.ID = "different"; return value }},
		{name: "submicrosecond reversed timestamps", mutate: func(value app.Session) app.Session {
			value.CreatedAt = time.Date(2026, 8, 20, 12, 0, 0, 123456789, time.UTC)
			value.UpdatedAt = time.Date(2026, 8, 20, 12, 0, 0, 123456700, time.UTC)
			return value
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			casePath := t.TempDir() + "/state.json"
			caseStore, err := NewFileStore(casePath)
			if err != nil {
				t.Fatal(err)
			}
			created, err := caseStore.CreateSession(t.Context(), "corrupt")
			if err != nil {
				t.Fatal(err)
			}
			rewriteFileSessionSnapshot(t, casePath, created.ID, testCase.mutate)
			if _, err := NewFileStore(casePath); err == nil {
				t.Fatal("corrupt persisted session was accepted")
			}
		})
	}
}

func rewriteFileSessionSnapshot(t testing.TB, path, id string, mutate func(app.Session) app.Session) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.Sessions[id] = mutate(snapshot.Sessions[id])
	raw, err = json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func errorsIsFileCommitInjected(err error) bool {
	return errors.Is(err, errFileCommitInjected)
}

func assertSessionFileRollbackEqual(t testing.TB, got, want fileRollbackState) {
	t.Helper()
	if reflect.DeepEqual(got, want) {
		return
	}
	gotSnapshot := reflect.ValueOf(got.snapshot)
	wantSnapshot := reflect.ValueOf(want.snapshot)
	typeOfSnapshot := gotSnapshot.Type()
	for index := 0; index < gotSnapshot.NumField(); index++ {
		if !reflect.DeepEqual(gotSnapshot.Field(index).Interface(), wantSnapshot.Field(index).Interface()) {
			t.Errorf("rollback field %s differs: got=%#v want=%#v", typeOfSnapshot.Field(index).Name, gotSnapshot.Field(index).Interface(), wantSnapshot.Field(index).Interface())
		}
	}
	if !reflect.DeepEqual(got.passiveNotificationRevs, want.passiveNotificationRevs) {
		t.Errorf("rollback passive notification revisions differ: got=%#v want=%#v", got.passiveNotificationRevs, want.passiveNotificationRevs)
	}
}

func TestSessionHighWaterSurvivesFileRollback(t *testing.T) {
	store, err := NewFileStore(t.TempDir() + "/state.json")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	store.inner.sessionNow = func() time.Time { return now }
	session, err := store.CreateSession(t.Context(), "before")
	if err != nil {
		t.Fatal(err)
	}
	store.commitOps = &controlledFileCommitOps{failStage: "encode", failRemaining: 1}
	if _, err := store.UpdateSessionTitle(t.Context(), session.ID, "failed"); StoreErrorCodeOf(err) != StoreErrorDurability {
		t.Fatal(err)
	}
	store.commitOps = osFileCommitOps{}
	updated, err := store.UpdateSessionTitle(t.Context(), session.ID, "after")
	if err != nil {
		t.Fatal(err)
	}
	if !updated.UpdatedAt.Equal(session.UpdatedAt.Add(2 * time.Microsecond)) {
		t.Fatalf("updated time=%s want=%s", updated.UpdatedAt, session.UpdatedAt.Add(2*time.Microsecond))
	}
}
