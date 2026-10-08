package execution

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func waitPending(t *testing.T, s *Service, e Envelope) PendingApproval {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status, err := s.Lookup(e.OwnerID, e.ClientID, e.RequestID)
		if err == nil && len(status.PendingApprovals) == 1 {
			if status.State != "running" || status.ExecutionExpiresAt == nil {
				t.Fatal(status)
			}
			return status.PendingApprovals[0]
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("approval was not published")
	return PendingApproval{}
}

func TestApprovalBoundDecisionIdempotencyAndNoDurableContent(t *testing.T) {
	var s *Service
	var effects atomic.Int32
	finish := make(chan struct{})
	row, err := NewPendingApproval("approval_test", "files.write", "private approval canary", map[string]any{"content": "private argument canary"})
	if err != nil {
		t.Fatal(err)
	}
	s, e, digest := setup(t, func(ctx context.Context, e Envelope, _ map[string][]byte) (Output, error) {
		decision, err := s.AwaitApproval(ctx, e, row)
		if err != nil {
			return Output{}, err
		}
		if decision == "approve" {
			effects.Add(1)
		}
		<-finish
		return Output{Content: "approved"}, nil
	})
	if _, err = s.Submit(t.Context(), e, digest); err != nil {
		t.Fatal(err)
	}
	pending := waitPending(t, s, e)
	pending.Arguments["content"] = "mutated lookup copy"
	if err = s.DecideApproval("other", e.ClientID, e.RequestID, row.ApprovalID, row.Digest, "approve"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err = s.DecideApproval(e.OwnerID, e.ClientID, e.RequestID, row.ApprovalID, strings.Repeat("0", 64), "approve"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	for range 5 {
		if err = s.DecideApproval(e.OwnerID, e.ClientID, e.RequestID, row.ApprovalID, row.Digest, "approve"); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.DecideApproval(e.OwnerID, e.ClientID, e.RequestID, row.ApprovalID, row.Digest, "reject"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	status, _ := s.Lookup(e.OwnerID, e.ClientID, e.RequestID)
	if len(status.PendingApprovals) != 0 {
		t.Fatal("resolved decision still actionable")
	}
	ledger, _ := os.ReadFile(filepath.Join(s.Root(), "control.json"))
	if strings.Contains(string(ledger), "canary") || strings.Contains(string(ledger), row.ApprovalID) {
		t.Fatal("approval content entered durable control")
	}
	close(finish)
	completed(t, s, e)
	if effects.Load() != 1 {
		t.Fatal("duplicate effect", effects.Load())
	}
	if err = s.DecideApproval(e.OwnerID, e.ClientID, e.RequestID, row.ApprovalID, row.Digest, "approve"); !errors.Is(err, ErrExpired) {
		t.Fatal(err)
	}
}

func TestPendingApprovalCancelRestartAndDeadlineNeverReplay(t *testing.T) {
	for _, end := range []string{"cancel", "deadline", "restart"} {
		t.Run(end, func(t *testing.T) {
			var s *Service
			var effects atomic.Int32
			row, _ := NewPendingApproval("approval_test", "files.write", "private canary", nil)
			s, e, digest := setup(t, func(ctx context.Context, e Envelope, _ map[string][]byte) (Output, error) {
				decision, err := s.AwaitApproval(ctx, e, row)
				if err == nil && decision == "approve" {
					effects.Add(1)
				}
				return Output{}, err
			})
			if _, err := s.Submit(t.Context(), e, digest); err != nil {
				t.Fatal(err)
			}
			waitPending(t, s, e)
			if end == "deadline" {
				s.mu.Lock()
				f := s.control.Fences[keyFor(e.OwnerID, e.ClientID, e.RequestID)]
				f.Deadline = s.now().Add(-time.Second)
				s.control.Fences[keyFor(e.OwnerID, e.ClientID, e.RequestID)] = f
				s.mu.Unlock()
				if err := s.DecideApproval(e.OwnerID, e.ClientID, e.RequestID, row.ApprovalID, row.Digest, "approve"); !errors.Is(err, ErrExpired) {
					t.Fatal(err)
				}
				status, _ := s.Lookup(e.OwnerID, e.ClientID, e.RequestID)
				if status.State != "unknown" || len(status.PendingApprovals) != 0 {
					t.Fatal(status)
				}
			}
			if end == "restart" {
				s.Close()
			} else if err := s.Cancel(e.OwnerID, e.ClientID, e.RequestID); err != nil {
				t.Fatal(err)
			}
			s.Wait()
			s.Close()
			restarted, err := New(s.Root(), func(context.Context, Envelope, map[string][]byte) (Output, error) {
				t.Fatal("approval replayed on restart")
				return Output{}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer restarted.Close()
			status, err := restarted.Submit(t.Context(), e, digest)
			if err != nil || status.State != "unknown" || len(status.PendingApprovals) != 0 || effects.Load() != 0 {
				t.Fatal(status, err, effects.Load())
			}
		})
	}
}
