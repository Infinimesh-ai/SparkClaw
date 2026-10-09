package execution

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func TestClientRevocationStopsInstalledExecutionAndPreservesOtherFences(t *testing.T) {
	started := make(chan string, 2)
	canceled := make(chan string, 2)
	s, e, digest := setup(t, func(ctx context.Context, e Envelope, _ map[string][]byte) (Output, error) {
		if e.Messages[0].Content == "complete" {
			return Output{Content: "durable terminal result"}, nil
		}
		started <- e.RequestID
		<-ctx.Done()
		canceled <- e.RequestID
		return Output{}, ctx.Err()
	})
	terminal := e
	terminal.RequestID = newUUID()
	terminal.Messages = []Message{{Role: "user", Content: "complete"}}
	if _, err := s.Submit(t.Context(), terminal, digest); err != nil {
		t.Fatal(err)
	}
	terminalBefore := completed(t, s, terminal)
	other := e
	other.ClientID, other.RequestID = "unrelated-client", newUUID()
	if err := s.Bind(other.OwnerID, other.ClientID, other.InstallationID); err != nil {
		t.Fatal(err)
	}
	for _, request := range []Envelope{e, other} {
		if _, err := s.Submit(t.Context(), request, digest); err != nil {
			t.Fatal(err)
		}
		if got := <-started; got != request.RequestID {
			t.Fatal("wrong execution started", got)
		}
	}
	before, _ := s.Lookup(e.OwnerID, e.ClientID, e.RequestID)
	otherBefore, _ := s.Lookup(other.OwnerID, other.ClientID, other.RequestID)
	if err := s.RevokeWorkbenchClient(e.ClientID); err != nil {
		t.Fatal(err)
	}
	if got := <-canceled; got != e.RequestID {
		t.Fatal("revocation canceled another client", got)
	}
	status, err := s.Lookup(e.OwnerID, e.ClientID, e.RequestID)
	if err != nil || status.State != "unknown" || status.Revision != before.Revision+1 {
		t.Fatal("ordinary execution escaped revocation", status, err)
	}
	otherAfter, _ := s.Lookup(other.OwnerID, other.ClientID, other.RequestID)
	terminalAfter, _ := s.Lookup(terminal.OwnerID, terminal.ClientID, terminal.RequestID)
	if !reflect.DeepEqual(otherAfter, otherBefore) || !reflect.DeepEqual(terminalAfter, terminalBefore) {
		t.Fatal("revocation changed another client or terminal result", otherAfter, terminalAfter)
	}
	if err := s.Cancel(other.OwnerID, other.ClientID, other.RequestID); err != nil {
		t.Fatal(err)
	}
	s.Wait()
	s.Close()
	restarted, err := New(s.Root(), func(context.Context, Envelope, map[string][]byte) (Output, error) {
		t.Error("revoked original request replayed")
		return Output{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	status, err = restarted.Submit(t.Context(), e, digest)
	if err != nil || status.State != "unknown" {
		t.Fatal("revocation lost its durable fence", status, err)
	}
}

func TestClientRevocationFencesAcceptedExecutionBeforeWorkerStarts(t *testing.T) {
	s, e, digest := setup(t, nil)
	key := keyFor(e.OwnerID, e.ClientID, e.RequestID)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s.mu.Lock()
	s.control.Fences[key] = Fence{OwnerID: e.OwnerID, ClientID: e.ClientID, InstallationID: e.InstallationID, RequestID: e.RequestID, InputDigest: digest, State: "accepted", Revision: 1, CreatedAt: s.now(), Deadline: s.now().Add(ExecutionBudget)}
	s.active[key] = cancel
	err := s.saveLocked()
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RevokeWorkbenchClient(e.ClientID); err != nil {
		t.Fatal(err)
	}
	status, err := s.Submit(t.Context(), e, digest)
	if err != nil || status.State != "unknown" || status.Revision != 2 || !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("accepted execution was not fenced", status, err, ctx.Err())
	}
}

func TestClientRevocationInvalidatesApprovalAndRetainsReceiptAcrossPersistenceFailure(t *testing.T) {
	for _, failSave := range []bool{false, true} {
		t.Run(map[bool]string{false: "durable", true: "failed_save"}[failSave], func(t *testing.T) {
			var s *Service
			var effects atomic.Int32
			first, _ := NewPendingApproval("approval_first", "files.write", "first operation", nil)
			second, _ := NewPendingApproval("approval_second", "files.write", "second operation", nil)
			s, e, digest := setup(t, func(ctx context.Context, e Envelope, _ map[string][]byte) (Output, error) {
				decision, err := s.AwaitApproval(ctx, e, first)
				if err != nil {
					return Output{}, err
				}
				if decision == "approve" {
					effects.Add(1)
				}
				decision, err = s.AwaitApproval(ctx, e, second)
				if err == nil && decision == "approve" {
					effects.Add(1)
				}
				return Output{}, err
			})
			if _, err := s.Submit(t.Context(), e, digest); err != nil {
				t.Fatal(err)
			}
			waitPending(t, s, e)
			if err := s.DecideApproval(e.OwnerID, e.ClientID, e.RequestID, first.ApprovalID, first.Digest, "approve"); err != nil {
				t.Fatal(err)
			}
			if pending := waitPending(t, s, e); pending.ApprovalID != second.ApprovalID {
				t.Fatal("expected second pending approval", pending)
			}
			before, _ := s.Lookup(e.OwnerID, e.ClientID, e.RequestID)
			path := filepath.Join(s.Root(), "control.json")
			if failSave {
				if err := os.Rename(path, path+".prior"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			err := s.RevokeWorkbenchClient(e.ClientID)
			if (!failSave && err != nil) || (failSave && !errors.Is(err, ErrUnavailable)) {
				t.Fatal("wrong persistence result", err)
			}
			s.Wait()
			status, err := s.Lookup(e.OwnerID, e.ClientID, e.RequestID)
			if err != nil || status.State != "unknown" || status.Revision != before.Revision+1 || len(status.PendingApprovals) != 0 || !reflect.DeepEqual(status.ApprovalReceipts, before.ApprovalReceipts) || effects.Load() != 1 {
				t.Fatal("revocation lost receipts or released another operation", status, err, effects.Load())
			}
			s.mu.Lock()
			_, retainedWait := s.approvals[keyFor(e.OwnerID, e.ClientID, e.RequestID)]
			s.mu.Unlock()
			if retainedWait {
				t.Fatal("revocation retained plaintext approval content")
			}
			if err := s.DecideApproval(e.OwnerID, e.ClientID, e.RequestID, second.ApprovalID, second.Digest, "approve"); !errors.Is(err, ErrExpired) {
				t.Fatal("revocation left a pending approval actionable", err)
			}
			next := e
			next.RequestID = newUUID()
			if _, err := s.Submit(t.Context(), next, digest); !errors.Is(err, ErrExpired) {
				t.Fatal("revoked client admitted a new request", err)
			}
			if failSave {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(path+".prior", path); err != nil {
					t.Fatal(err)
				}
				if err := s.RevokeWorkbenchClient(e.ClientID); err != nil {
					t.Fatal("revocation retry did not repair persistence", err)
				}
			}
			s.Close()
			restarted, err := New(s.Root(), func(context.Context, Envelope, map[string][]byte) (Output, error) {
				t.Error("revoked approval replayed")
				return Output{}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer restarted.Close()
			got, err := restarted.Submit(t.Context(), e, digest)
			if err != nil || !reflect.DeepEqual(got, status) {
				t.Fatal("revocation did not persist its exact terminal fence", got, err)
			}
		})
	}
}

func TestClientRevocationSerializesWithNewInstalledAdmission(t *testing.T) {
	for range 12 {
		var calls atomic.Int32
		s, e, digest := setup(t, func(ctx context.Context, _ Envelope, _ map[string][]byte) (Output, error) {
			calls.Add(1)
			<-ctx.Done()
			return Output{}, ctx.Err()
		})
		start := make(chan struct{})
		var submitErr, revokeErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); <-start; _, submitErr = s.Submit(t.Context(), e, digest) }()
		go func() { defer wg.Done(); <-start; revokeErr = s.RevokeWorkbenchClient(e.ClientID) }()
		close(start)
		wg.Wait()
		s.Wait()
		if revokeErr != nil || (submitErr != nil && !errors.Is(submitErr, ErrExpired)) || calls.Load() > 1 {
			t.Fatal("invalid race outcome", submitErr, revokeErr, calls.Load())
		}
		status, err := s.Lookup(e.OwnerID, e.ClientID, e.RequestID)
		if submitErr == nil {
			if err != nil || status.State != "unknown" {
				t.Fatal("admission winner escaped revocation", status, err)
			}
			if replay, err := s.Submit(t.Context(), e, digest); err != nil || replay.State != "unknown" {
				t.Fatal("revoked original request lost query access", replay, err)
			}
		} else if !errors.Is(err, ErrNotFound) || calls.Load() != 0 {
			t.Fatal("revocation winner still admitted execution", status, err, calls.Load())
		}
		e.RequestID = newUUID()
		if _, err := s.Submit(t.Context(), e, digest); !errors.Is(err, ErrExpired) {
			t.Fatal("post-revocation request admitted", err)
		}
		s.Close()
	}
}

func TestClientRevocationBlocksNewHostAdmissionButRetainsOriginalQuery(t *testing.T) {
	s, b := workbenchFixture(t)
	lease, _, err := s.BeginWorkbench(t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Finish("approval_pending", "message"); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeWorkbenchClient(b.ClientID); err != nil {
		t.Fatal(err)
	}
	if lease, status, err := s.BeginWorkbench(t.Context(), b); err != nil || lease != nil || status.State != "unknown" {
		t.Fatal("revocation lost original host query", status, err)
	}
	if lease, err := s.ContinueWorkbench(t.Context(), b); !errors.Is(err, ErrContinuationClosed) || lease != nil {
		t.Fatal("revoked host continuation admitted", err)
	}
	b.RequestID = newUUID()
	if lease, _, err := s.BeginWorkbench(t.Context(), b); !errors.Is(err, ErrExpired) || lease != nil {
		t.Fatal("revoked host admitted new request", err)
	}
	b.ClientID = "unrelated-client"
	lease, _, err = s.BeginWorkbench(t.Context(), b)
	if err != nil || lease == nil {
		t.Fatal("revocation affected another host client", err)
	}
	if err := lease.Finish("completed", "message"); err != nil {
		t.Fatal(err)
	}
}
