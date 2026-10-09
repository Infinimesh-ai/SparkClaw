package execution

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Run in a subprocess so termination exercises the actual durable boundary,
// with no Close, deferred cleanup or workflow error handling before recovery.
func TestApprovalCrashProcess(t *testing.T) {
	root := os.Getenv("SPARKCLAW_APPROVAL_CRASH_ROOT")
	if root == "" {
		return
	}
	mode := os.Getenv("SPARKCLAW_APPROVAL_CRASH_MODE")
	e := fixture()
	row, _ := NewPendingApproval("approval_crash", "files.write", "private summary canary", map[string]any{"content": "private arguments canary"})
	second, _ := NewPendingApproval("approval_next", "files.write", "private next canary", nil)
	var s *Service
	var err error
	effect := make(chan struct{})
	s, err = New(root, func(ctx context.Context, e Envelope, _ map[string][]byte) (Output, error) {
		decision, err := s.AwaitApproval(ctx, e, row)
		if err != nil {
			return Output{}, err
		}
		if decision == "approve" {
			if err := atomicFile(filepath.Join(root, "effect"), []byte("external effect happened")); err != nil {
				return Output{}, err
			}
			close(effect)
		}
		if mode == "next_wait" {
			_, err = s.AwaitApproval(ctx, e, second)
			return Output{}, err
		}
		<-ctx.Done()
		return Output{}, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Bind(e.OwnerID, e.ClientID, e.InstallationID); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(e)
	if _, err = s.Submit(t.Context(), e, Digest(raw)); err != nil {
		t.Fatal(err)
	}
	waitPending(t, s, e)
	if mode != "pending" {
		if err = s.DecideApproval(e.OwnerID, e.ClientID, e.RequestID, row.ApprovalID, row.Digest, "approve"); err != nil {
			t.Fatal(err)
		}
		select {
		case <-effect:
		case <-time.After(3 * time.Second):
			t.Fatal("approved operation did not execute")
		}
		if mode == "next_wait" {
			if pending := waitPending(t, s, e); pending.ApprovalID != second.ApprovalID {
				t.Fatal("did not reach second approval", pending)
			}
		}
	}
	fmt.Println("READY " + string(raw))
	select {} // The parent kills this process at the specified durable boundary.
}

func TestApprovalAbruptRestartTerminatesAndKeepsDecisionReceipts(t *testing.T) {
	for _, mode := range []string{"pending", "decided", "next_wait"} {
		t.Run(mode, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "execution")
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestApprovalCrashProcess$", "-test.timeout=10s")
			cmd.Env = append(os.Environ(), "SPARKCLAW_APPROVAL_CRASH_ROOT="+root, "SPARKCLAW_APPROVAL_CRASH_MODE="+mode)
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = cmd.Process.Kill() })
			var e Envelope
			scanner := bufio.NewScanner(stdout)
			for scanner.Scan() {
				if strings.HasPrefix(scanner.Text(), "READY ") {
					if err := json.Unmarshal([]byte(strings.TrimPrefix(scanner.Text(), "READY ")), &e); err != nil {
						t.Fatal(err)
					}
					break
				}
			}
			if e.RequestID == "" {
				_ = cmd.Wait()
				t.Fatalf("child did not reach crash boundary: %v %s", scanner.Err(), stderr.String())
			}
			if err = cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = cmd.Wait()
			ledger, err := os.ReadFile(filepath.Join(root, "control.json"))
			if err != nil || bytes.Contains(ledger, []byte("canary")) || bytes.Contains(ledger, []byte("files.write")) {
				t.Fatalf("durable approval leaked content: %s %v", ledger, err)
			}
			s, err := New(root, func(context.Context, Envelope, map[string][]byte) (Output, error) {
				t.Error("interrupted approval automatically replayed")
				return Output{}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(s.Close)
			status, err := s.Lookup(e.OwnerID, e.ClientID, e.RequestID)
			if err != nil || len(status.PendingApprovals) != 0 || status.ExecutionExpiresAt != nil {
				t.Fatal(status, err)
			}
			wantState, wantReason, receipts := "failed", TerminationGatewayRestartedAwaitingApproval, 0
			if mode != "pending" {
				wantState, receipts = "unknown", 1
				if raw, err := os.ReadFile(filepath.Join(root, "effect")); err != nil || len(raw) == 0 {
					t.Fatal("lost external side effect evidence", err)
				}
			}
			if mode == "decided" {
				wantReason = TerminationGatewayRestartedAfterApproval
			}
			if status.State != wantState || status.TerminationReason != wantReason || len(status.ApprovalReceipts) != receipts {
				t.Fatalf("restart did not preserve termination/outcome distinction: %+v", status)
			}
			row, _ := NewPendingApproval("approval_crash", "files.write", "private summary canary", map[string]any{"content": "private arguments canary"})
			if err = s.DecideApproval(e.OwnerID, e.ClientID, e.RequestID, row.ApprovalID, row.Digest, "approve"); !errors.Is(err, ErrExpired) {
				t.Fatal("stale decision accepted", err)
			}
			raw, _ := json.Marshal(e)
			replay, err := s.Submit(t.Context(), e, Digest(raw))
			if err != nil || !reflect.DeepEqual(replay, status) {
				t.Fatal("original request did not recover terminal state", replay, err)
			}
			s.Close()
			reader, err := Read(root)
			if err != nil {
				t.Fatal(err)
			}
			durable, err := reader.Lookup(e.OwnerID, e.ClientID, e.RequestID)
			if err != nil || !reflect.DeepEqual(durable, status) {
				t.Fatal("startup termination was not durable", durable, err)
			}
		})
	}
}

func TestApprovalDecisionShutdownRaceHasOneDurableWinner(t *testing.T) {
	for range 12 {
		var s *Service
		var effects atomic.Int32
		row, _ := NewPendingApproval("approval_race", "files.write", "bounded write", nil)
		s, e, digest := setup(t, func(ctx context.Context, e Envelope, _ map[string][]byte) (Output, error) {
			decision, err := s.AwaitApproval(ctx, e, row)
			if err == nil && decision == "approve" {
				effects.Add(1)
			}
			<-ctx.Done()
			return Output{}, ctx.Err()
		})
		if _, err := s.Submit(t.Context(), e, digest); err != nil {
			t.Fatal(err)
		}
		waitPending(t, s, e)
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		var decisionErr error
		go func() {
			defer wg.Done()
			<-start
			decisionErr = s.DecideApproval(e.OwnerID, e.ClientID, e.RequestID, row.ApprovalID, row.Digest, "approve")
		}()
		go func() { defer wg.Done(); <-start; s.Close() }()
		close(start)
		wg.Wait()
		restarted, err := New(s.Root(), nil)
		if err != nil {
			t.Fatal(err)
		}
		status, err := restarted.Lookup(e.OwnerID, e.ClientID, e.RequestID)
		restarted.Close()
		if err != nil || status.TerminationReason == "" || len(status.PendingApprovals) != 0 {
			t.Fatal(status, err)
		}
		if decisionErr == nil {
			if status.State != "unknown" || len(status.ApprovalReceipts) != 1 || status.ApprovalReceipts[0].Decision != "approve" || effects.Load() > 1 {
				t.Fatal("committed decision was lost or replayed", status, effects.Load())
			}
		} else if !errors.Is(decisionErr, ErrExpired) || status.State != "failed" || len(status.ApprovalReceipts) != 0 || effects.Load() != 0 {
			t.Fatal("shutdown lost decision race", status, decisionErr, effects.Load())
		}
	}
}

func TestApprovalControlMigratesV2WithoutLosingBindingsOrResults(t *testing.T) {
	s, e, digest := setup(t, func(context.Context, Envelope, map[string][]byte) (Output, error) {
		return Output{Content: "retained result", Files: map[string][]byte{"retained.txt": []byte("retained file")}}, nil
	})
	if _, err := s.Submit(t.Context(), e, digest); err != nil {
		t.Fatal(err)
	}
	original := completed(t, s, e)
	b := WorkbenchBinding{OwnerID: "owner", ActorID: "actor", ClientID: "client", WorkspaceID: Digest([]byte("workspace")), SessionID: "session", RequestID: newUUID(), InputDigest: Digest([]byte("input")), ContextDigest: Digest([]byte("context")), ContextBefore: time.Now().UTC()}
	lease, _, err := s.BeginWorkbench(t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	if err = lease.Finish("completed", "message"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	legacy := s.control
	legacy.Version = 2
	for key, f := range legacy.Fences {
		f.Revision = 0
		legacy.Fences[key] = f
	}
	interrupted := e
	interrupted.RequestID = newUUID()
	f := legacy.Fences[keyFor(e.OwnerID, e.ClientID, e.RequestID)]
	f.RequestID, f.State, f.ResultDigest = interrupted.RequestID, "running", ""
	f.GeneratedAt, f.ExpiresAt = nil, nil
	legacy.Fences[keyFor(interrupted.OwnerID, interrupted.ClientID, interrupted.RequestID)] = f
	raw, _ := json.Marshal(legacy)
	path := filepath.Join(s.Root(), "control.json")
	if err = atomicFile(path, raw); err != nil {
		t.Fatal(err)
	}
	// GET can read legacy ledgers without rewriting them or touching content.
	before := readerDiskSnapshot(t, s.Root())
	reader, err := Read(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	got, err := reader.Lookup(e.OwnerID, e.ClientID, e.RequestID)
	if err != nil || !reflect.DeepEqual(got.Result, original.Result) || !reflect.DeepEqual(before, readerDiskSnapshot(t, s.Root())) {
		t.Fatal("legacy read lost output or mutated storage", got, err)
	}
	restored, err := New(s.Root(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err := restored.Installation(e.OwnerID, e.ClientID, e.InstallationID); err != nil {
		t.Fatal("migration lost installation", err)
	}
	got, err = restored.Lookup(e.OwnerID, e.ClientID, e.RequestID)
	if err != nil || !reflect.DeepEqual(got.Result, original.Result) || got.Revision == 0 {
		t.Fatal("migration lost completed result", got, err)
	}
	if lease, status, err := restored.BeginWorkbench(t.Context(), b); err != nil || lease != nil || status.State != "completed" || status.MessageID != "message" {
		t.Fatal("migration lost host fence", status, err)
	}
	got, err = restored.Submit(t.Context(), interrupted, digest)
	if err != nil || got.State != "unknown" || got.TerminationReason != "" {
		t.Fatal("migration invented legacy approval state or replayed execution", got, err)
	}
	var saved control
	raw, err = os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &saved) != nil || saved.Version != controlVersion || len(saved.Fences) != 2 || len(saved.WorkbenchFences) != 1 {
		t.Fatal("migration was not persisted", saved, err)
	}
}

func TestApprovalPersistenceFailureNeverPublishesOrExecutes(t *testing.T) {
	for _, phase := range []string{"wait", "decide"} {
		t.Run(phase, func(t *testing.T) {
			var s *Service
			var effects atomic.Int32
			startWait, executorStarted := make(chan struct{}), make(chan struct{})
			row, _ := NewPendingApproval("approval_persistence", "files.write", "private summary", nil)
			s, e, digest := setup(t, func(ctx context.Context, e Envelope, _ map[string][]byte) (Output, error) {
				close(executorStarted)
				<-startWait
				decision, err := s.AwaitApproval(ctx, e, row)
				if err == nil && decision == "approve" {
					effects.Add(1)
				}
				return Output{}, err
			})
			if _, err := s.Submit(t.Context(), e, digest); err != nil {
				t.Fatal(err)
			}
			<-executorStarted
			if phase == "decide" {
				close(startWait)
				waitPending(t, s, e)
			}
			path := filepath.Join(s.Root(), "control.json")
			if err := os.Rename(path, path+".prior"); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
			if phase == "wait" {
				close(startWait)
			} else if err := s.DecideApproval(e.OwnerID, e.ClientID, e.RequestID, row.ApprovalID, row.Digest, "approve"); !errors.Is(err, ErrUnavailable) {
				t.Fatal("decision succeeded without durable receipt", err)
			}
			s.Wait()
			status, err := s.Lookup(e.OwnerID, e.ClientID, e.RequestID)
			if err != nil || status.State != "unknown" || len(status.PendingApprovals) != 0 || effects.Load() != 0 {
				t.Fatal("persistence failure released approval", status, err, effects.Load())
			}
			if phase == "decide" && (len(status.ApprovalReceipts) != 1 || status.ApprovalReceipts[0].State != ApprovalDecisionUnknown) {
				t.Fatal("uncertain persistence presented a committed decision", status)
			}
			if err = s.DecideApproval(e.OwnerID, e.ClientID, e.RequestID, row.ApprovalID, row.Digest, "approve"); !errors.Is(err, ErrExpired) {
				t.Fatal("uncertain decision became retryable", err)
			}
			if err = os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err = s.Sweep(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestApprovalClientReconnectKeepsLiveWaitAndDurableDecision(t *testing.T) {
	var s *Service
	row, _ := NewPendingApproval("approval_reconnect", "files.write", "summary", nil)
	finish := make(chan struct{})
	s, e, digest := setup(t, func(ctx context.Context, e Envelope, _ map[string][]byte) (Output, error) {
		decision, err := s.AwaitApproval(ctx, e, row)
		if err != nil {
			return Output{}, err
		}
		select {
		case <-finish:
		case <-ctx.Done():
			return Output{}, ctx.Err()
		}
		return Output{Content: decision}, nil
	})
	if _, err := s.Submit(t.Context(), e, digest); err != nil {
		t.Fatal(err)
	}
	waitPending(t, s, e)
	ledger, err := os.ReadFile(filepath.Join(s.Root(), "control.json"))
	var saved control
	if err != nil || json.Unmarshal(ledger, &saved) != nil {
		t.Fatal(err)
	}
	f := saved.Fences[keyFor(e.OwnerID, e.ClientID, e.RequestID)]
	if len(f.Approvals) != 1 || f.Approvals[0].State != ApprovalPending || f.Approvals[0].Digest != row.Digest {
		t.Fatal("approval published before durable waiting phase", f)
	}
	// A restarted desktop rebinds and queries the same service; it neither
	// terminates nor duplicates a still-valid Gateway continuation.
	for range 3 {
		if err := s.Bind(e.OwnerID, e.ClientID, e.InstallationID); err != nil {
			t.Fatal(err)
		}
		status, err := s.Submit(t.Context(), e, digest)
		if err != nil || status.State != "running" || status.TerminationReason != "" || len(status.PendingApprovals) != 1 || status.Revision != f.Revision {
			t.Fatal("desktop reconnect invalidated pending approval", status, err)
		}
	}
	if err := s.DecideApproval(e.OwnerID, e.ClientID, e.RequestID, row.ApprovalID, row.Digest, "approve"); err != nil {
		t.Fatal(err)
	}
	status, err := s.Lookup(e.OwnerID, e.ClientID, e.RequestID)
	if err != nil || len(status.ApprovalReceipts) != 1 || status.ApprovalReceipts[0].Revision <= f.Revision || status.ApprovalReceipts[0].Decision != "approve" {
		t.Fatal("missing durable decision receipt", status, err)
	}
	status.ApprovalReceipts[0].Decision = "reject"
	*status.ApprovalReceipts[0].DecidedAt = time.Time{}
	status, _ = s.Lookup(e.OwnerID, e.ClientID, e.RequestID)
	if status.ApprovalReceipts[0].Decision != "approve" || status.ApprovalReceipts[0].DecidedAt.IsZero() {
		t.Fatal("lookup mutated durable decision", status)
	}
	close(finish)
	completed(t, s, e)
}

func TestApprovalGatewayLifecycleCancellationKeepsRestartReason(t *testing.T) {
	for range 8 {
		var s *Service
		row, _ := NewPendingApproval("approval_shutdown", "files.write", "summary", nil)
		s, e, digest := setup(t, func(ctx context.Context, e Envelope, _ map[string][]byte) (Output, error) {
			_, err := s.AwaitApproval(ctx, e, row)
			return Output{}, err
		})
		lifecycle, cancel := context.WithCancel(t.Context())
		s.Start(lifecycle)
		if _, err := s.Submit(lifecycle, e, digest); err != nil {
			t.Fatal(err)
		}
		waitPending(t, s, e)
		cancel()
		s.Close()
		restarted, err := New(s.Root(), nil)
		if err != nil {
			t.Fatal(err)
		}
		status, err := restarted.Lookup(e.OwnerID, e.ClientID, e.RequestID)
		restarted.Close()
		if err != nil || status.State != "failed" || status.TerminationReason != TerminationGatewayRestartedAwaitingApproval {
			t.Fatal("context cancellation erased Gateway restart reason", status, err)
		}
	}
}
