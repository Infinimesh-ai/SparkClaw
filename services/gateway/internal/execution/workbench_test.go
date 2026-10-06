package execution

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func workbenchFixture(t *testing.T) (*Service, WorkbenchBinding) {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "execution"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, WorkbenchBinding{OwnerID: "owner", ActorID: "actor", ClientID: "client", WorkspaceID: Digest([]byte("workspace")), SessionID: "session", RequestID: newUUID(), InputDigest: Digest([]byte("canonical immutable request")), ContextDigest: Digest([]byte("bounded authoritative context")), ContextBefore: time.Now().UTC()}
}

func TestWorkbenchAdmissionClaimsOnceAndBindsWholeAuthority(t *testing.T) {
	s, b := workbenchFixture(t)
	var claims atomic.Int32
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lease, status, err := s.BeginWorkbench(t.Context(), b)
			if err != nil {
				t.Error(err)
				return
			}
			if status.RunID == "" {
				t.Error("missing stable run")
			}
			if lease != nil {
				claims.Add(1)
				if err := lease.Finish("completed", "message"); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	if claims.Load() != 1 {
		t.Fatalf("claimed %d times", claims.Load())
	}
	for _, change := range []func(*WorkbenchBinding){func(b *WorkbenchBinding) { b.ActorID = "other" }, func(b *WorkbenchBinding) { b.ClientID = "other" }, func(b *WorkbenchBinding) { b.SessionID = "other" }, func(b *WorkbenchBinding) { b.InputDigest = Digest([]byte("changed")) }} {
		altered := b
		change(&altered)
		if lease, _, err := s.BeginWorkbench(t.Context(), altered); !errors.Is(err, ErrConflict) || lease != nil {
			t.Fatalf("accepted changed binding: %v", err)
		}
	}
	rows, _, err := s.ListWorkbench(b, "", false)
	if err != nil || len(rows) != 1 || rows[0].State != "completed" {
		t.Fatalf("list=%+v err=%v", rows, err)
	}
	s.Close()
	restored, err := New(s.root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	lease, status, err := restored.BeginWorkbench(t.Context(), b)
	if err != nil || lease != nil || status.State != "completed" {
		t.Fatalf("replayed after restart: %+v %v", status, err)
	}
}

func TestWorkbenchInterruptedSnapshotNeverWritesOrReplays(t *testing.T) {
	s, b := workbenchFixture(t)
	revision := int64(7)
	b.SubmittedDraftRevision = &revision
	lease, _, err := s.BeginWorkbench(t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Finish("unknown", ""); err != nil {
		t.Fatal(err)
	}
	// Reproduce a process dying after durable admission and before any result.
	s.mu.Lock()
	f := s.control.WorkbenchFences[workbenchKey(b)]
	f.State = "running"
	s.control.WorkbenchFences[workbenchKey(b)] = f
	err = s.saveLocked()
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	path := filepath.Join(s.root, "control.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := ReadWorkbench(s.root)
	if err != nil {
		t.Fatal(err)
	}
	status, err := reader.LookupWorkbench(b)
	if err != nil || status.State != "unknown" || status.SubmittedDraftRevision == nil || *status.SubmittedDraftRevision != 7 || status.DraftRevision != nil {
		t.Fatalf("interruption not projected unknown: %+v %v", status, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatal("GET-style snapshot changed durable state")
	}
	restored, err := New(s.root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	lease, status, err = restored.BeginWorkbench(t.Context(), b)
	if err != nil || lease != nil || status.State != "unknown" {
		t.Fatalf("replayed interrupted request: %+v %v", status, err)
	}
}

func TestWorkbenchFailedAdmissionCannotExecuteOrReplayAndCancelKeepsFence(t *testing.T) {
	s, b := workbenchFixture(t)
	lease, _, err := s.BeginWorkbench(t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	status, err := s.CancelWorkbench(b)
	if err != nil || status.State != "unknown" || !errors.Is(lease.Context.Err(), context.Canceled) {
		t.Fatalf("cancel=%+v %v ctx=%v", status, err, lease.Context.Err())
	}
	if err := lease.Finish("completed", "message"); err != nil {
		t.Fatal(err)
	}
	if retry, status, err := s.BeginWorkbench(t.Context(), b); err != nil || retry != nil || status.State != "unknown" {
		t.Fatalf("cancel replayed: %+v %v", status, err)
	}
	b.RequestID = newUUID()
	path := filepath.Join(s.root, "control.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if lease, _, err := s.BeginWorkbench(t.Context(), b); !errors.Is(err, ErrUnavailable) || lease != nil {
		t.Fatalf("executed without durable admission: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if lease, status, err := s.BeginWorkbench(t.Context(), b); err != nil || lease != nil || status.State != "unknown" {
		t.Fatalf("persistence uncertainty replayed: %+v %v", status, err)
	}
}

func TestWorkbenchAttentionListKeepsOlderUnknownBeyondCompletedPage(t *testing.T) {
	s, b := workbenchFixture(t)
	original := b.RequestID
	for i := 0; i < 103; i++ {
		lease, _, err := s.BeginWorkbench(t.Context(), b)
		if err != nil {
			t.Fatal(err)
		}
		state := "completed"
		if i == 0 {
			state = "unknown"
		}
		if err := lease.Finish(state, ""); err != nil {
			t.Fatal(err)
		}
		b.RequestID = newUUID()
	}
	rows, next, err := s.ListWorkbench(b, "", false)
	if err != nil || len(rows) != 100 || next == "" {
		t.Fatalf("normal pagination %d %q %v", len(rows), next, err)
	}
	rows, next, err = s.ListWorkbench(b, "", true)
	if err != nil || len(rows) != 1 || rows[0].RequestID != original || next != "" {
		t.Fatalf("older uncertain request lost %+v %q %v", rows, next, err)
	}
}

func TestWorkbenchContinuationSharesOriginalBudgetAndTerminalFence(t *testing.T) {
	for _, stop := range []string{"cancel", "revoke", "expired", "continue"} {
		t.Run(stop, func(t *testing.T) {
			s, b := workbenchFixture(t)
			lease, _, err := s.BeginWorkbench(t.Context(), b)
			if err != nil {
				t.Fatal(err)
			}
			deadline, _ := lease.Context.Deadline()
			if err := lease.Finish("approval_pending", "message"); err != nil {
				t.Fatal(err)
			}
			switch stop {
			case "cancel":
				if _, err := s.CancelWorkbench(b); err != nil {
					t.Fatal(err)
				}
			case "revoke":
				if err := s.RevokeWorkbenchClient(b.ClientID); err != nil {
					t.Fatal(err)
				}
			case "expired":
				s.now = func() time.Time { return deadline.Add(time.Second) }
			}
			continued, err := s.ContinueWorkbench(t.Context(), b)
			if stop != "continue" {
				if err == nil || continued != nil {
					t.Fatal("continued fenced request")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			actual, _ := continued.Context.Deadline()
			if !actual.Equal(deadline) {
				t.Fatal("continuation extended original budget")
			}
			if duplicate, err := s.ContinueWorkbench(t.Context(), b); err == nil || duplicate != nil {
				t.Fatal("concurrent continuation admitted")
			}
			if err := s.RevokeWorkbenchClient(b.ClientID); err != nil {
				t.Fatal(err)
			}
			if continued.Context.Err() == nil {
				t.Fatal("revocation did not cancel live continuation")
			}
			if err := continued.Finish("completed", "message"); err != nil {
				t.Fatal(err)
			}
			status, err := s.LookupWorkbench(b)
			if err != nil || status.State != "unknown" {
				t.Fatalf("revoked completion escaped fence %+v %v", status, err)
			}
		})
	}
}
