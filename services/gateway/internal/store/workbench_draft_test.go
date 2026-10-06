package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestWorkbenchDraftRepositoryMemoryFile(t *testing.T) {
	for _, backend := range []string{"memory", "file"} {
		t.Run(backend, func(t *testing.T) {
			var repository testBackend = NewMemoryStore()
			var restart func() testBackend
			if backend == "file" {
				path := filepath.Join(t.TempDir(), "state.json")
				file, err := NewFileStore(path)
				if err != nil {
					t.Fatal(err)
				}
				repository = file
				restart = func() testBackend {
					file, err := NewFileStore(path)
					if err != nil {
						t.Fatal(err)
					}
					return file
				}
			}
			exerciseWorkbenchDraftContract(t, repository, restart)
		})
	}
}

func TestPostgresWorkbenchDraftConfiguredContract(t *testing.T) {
	dsn := os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set SPARKCLAW_TEST_POSTGRES_DSN for isolated integration")
	}
	st, err := NewPostgresStore(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	truncatePostgresStore(t, st)
	exerciseWorkbenchDraftContract(t, st, nil)
}

func exerciseWorkbenchDraftContract(t *testing.T, repository testBackend, restart func() testBackend) {
	t.Helper()
	const owner = "draft-owner"
	session, err := repository.CreateSessionWithScope(t.Context(), "Draft", owner, "", "webchat", false)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := repository.GetWorkbenchDraft(t.Context(), owner, session.ID)
	if err != nil || empty.Revision != 0 || empty.Content != "" || empty.AttachmentIDs == nil {
		t.Fatalf("empty=%#v err=%v", empty, err)
	}
	input := app.WorkbenchDraft{Content: "  retain\n draft whitespace \n", AttachmentIDs: []string{"workspace:file-one"}}
	saved, err := repository.SaveWorkbenchDraft(t.Context(), owner, session.ID, input)
	if err != nil || saved.Revision != 1 || saved.Content != input.Content || saved.UpdatedAt.IsZero() {
		t.Fatalf("saved=%#v err=%v", saved, err)
	}
	input.AttachmentIDs[0], saved.AttachmentIDs[0] = "mutated-input", "mutated-output"
	if restart != nil {
		repository = restart()
	}
	loaded, err := repository.GetWorkbenchDraft(t.Context(), owner, session.ID)
	if err != nil || loaded.Content != input.Content || loaded.AttachmentIDs[0] != "workspace:file-one" || loaded.Revision != 1 {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	if _, err := repository.GetWorkbenchDraft(t.Context(), "other-owner", session.ID); StoreErrorCodeOf(err) != StoreErrorNotFound {
		t.Fatalf("cross-owner read=%v", err)
	}
	if _, err := repository.SaveWorkbenchDraft(t.Context(), "other-owner", session.ID, app.WorkbenchDraft{}); StoreErrorCodeOf(err) != StoreErrorNotFound {
		t.Fatalf("cross-owner write=%v", err)
	}
	welcome, err := repository.SaveWorkbenchDraft(t.Context(), owner, "", app.WorkbenchDraft{Content: "welcome"})
	if err != nil || welcome.Revision != 1 {
		t.Fatal(err)
	}
	otherWelcome, err := repository.GetWorkbenchDraft(t.Context(), "other-owner", "")
	if err != nil || otherWelcome.Content != "" || otherWelcome.Revision != 0 {
		t.Fatalf("welcome isolation=%#v err=%v", otherWelcome, err)
	}
	// Exactly one writer can consume a loaded revision; an empty clear keeps a
	// durable revision tombstone instead of resetting to zero.
	var wins atomic.Int32
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, err := repository.SaveWorkbenchDraft(t.Context(), owner, session.ID, app.WorkbenchDraft{Revision: loaded.Revision})
			if err == nil {
				wins.Add(1)
			} else if !errors.Is(err, ErrWorkbenchDraftConflict) {
				t.Error(err)
			}
		}()
	}
	workers.Wait()
	if wins.Load() != 1 {
		t.Fatalf("CAS winners=%d", wins.Load())
	}
	if _, err := repository.SaveWorkbenchDraft(t.Context(), owner, session.ID, loaded); !errors.Is(err, ErrWorkbenchDraftConflict) {
		t.Fatalf("stale draft resurrected: %v", err)
	}
	if restart != nil {
		repository = restart()
	}
	cleared, err := repository.GetWorkbenchDraft(t.Context(), owner, session.ID)
	if err != nil || cleared.Content != "" || len(cleared.AttachmentIDs) != 0 || cleared.Revision != 2 {
		t.Fatalf("clear=%#v err=%v", cleared, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repository.SaveWorkbenchDraft(ctx, owner, session.ID, cleared); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("canceled=%v", err)
	}
	if _, err := repository.DeleteSession(t.Context(), session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetWorkbenchDraft(t.Context(), owner, session.ID); StoreErrorCodeOf(err) != StoreErrorNotFound {
		t.Fatalf("deleted session draft exposed: %v", err)
	}
	stillWelcome, err := repository.GetWorkbenchDraft(t.Context(), owner, "")
	if err != nil || stillWelcome.Content != "welcome" {
		t.Fatalf("delete consumed welcome: %#v err=%v", stillWelcome, err)
	}
}

func TestFileWorkbenchDraftFailedCommitCannotPretendSaved(t *testing.T) {
	st, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	before := st.captureFileRollback()
	st.commitOps = &controlledFileCommitOps{failStage: "encode", failRemaining: 1}
	if _, err := st.SaveWorkbenchDraft(t.Context(), app.DefaultOwnerID, "", app.WorkbenchDraft{Content: "draft"}); StoreErrorCodeOf(err) != StoreErrorDurability {
		t.Fatalf("save=%v", err)
	}
	if !reflect.DeepEqual(before, st.captureFileRollback()) {
		t.Fatal("failed save changed draft or revision")
	}
}

func TestFileWorkbenchDraftLostCommitResponseReconcilesRevision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	st.commitOps = &controlledFileCommitOps{failStage: "rename", failRemaining: 1, renameApplied: true}
	if _, err := st.SaveWorkbenchDraft(t.Context(), app.DefaultOwnerID, "", app.WorkbenchDraft{Content: "saved before response lost"}); StoreErrorCodeOf(err) != StoreErrorUnknownOutcome {
		t.Fatalf("save=%v", err)
	}
	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := reloaded.GetWorkbenchDraft(t.Context(), app.DefaultOwnerID, "")
	if err != nil || draft.Revision != 1 || draft.Content == "" {
		t.Fatalf("reconcile=%#v err=%v", draft, err)
	}
	if _, err := reloaded.SaveWorkbenchDraft(t.Context(), app.DefaultOwnerID, "", app.WorkbenchDraft{Content: "old revision"}); !errors.Is(err, ErrWorkbenchDraftConflict) {
		t.Fatalf("replay=%v", err)
	}
}

func draftPostgresRow(draft app.WorkbenchDraft) onboardingPostgresRow {
	return fakeConnectorPostgresRow{scan: func(out ...any) error {
		*(out[0].(*string)) = draft.Content
		*(out[1].(*[]byte)) = mustJSON(draft.AttachmentIDs)
		*(out[2].(*int64)) = draft.Revision
		*(out[3].(*time.Time)) = draft.UpdatedAt
		return nil
	}}
}

func TestPostgresWorkbenchDraftUnknownCommitDoesNotReturnSuccess(t *testing.T) {
	draft := app.WorkbenchDraft{Content: "draft", AttachmentIDs: []string{}, Revision: 1, UpdatedAt: time.Now().UTC()}
	tx := &fakeConnectorPostgresTx{rowQueue: []onboardingPostgresRow{draftPostgresRow(draft)}, commitErr: errors.New("response lost")}
	repository, operations, session := newFakeSchedulePostgresStore(tx)
	repository.sessionPostgres = &fakeSchedulePostgresOps{fakeConnectorPostgresOps: operations}
	_, err := repository.SaveWorkbenchDraft(t.Context(), app.DefaultOwnerID, "", app.WorkbenchDraft{Content: "draft"})
	if StoreErrorCodeOf(err) != StoreErrorUnknownOutcome || session.terminates != 1 {
		t.Fatalf("err=%v terminates=%d", err, session.terminates)
	}
}
