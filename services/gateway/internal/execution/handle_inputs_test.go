package execution

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRequestedHandleUploadIsBoundTemporaryAndIdempotent(t *testing.T) {
	var s *Service
	content := []byte("private historical file")
	file := File{ID: newUUID(), Name: "report.md", Size: len(content), SHA256: Digest(content)}
	result := make(chan error, 1)
	s, e, _ := setup(t, func(ctx context.Context, e Envelope, _ map[string][]byte) (Output, error) {
		got, err := s.AwaitHandle(ctx, e, file.ID)
		if err == nil && string(got) != string(content) {
			err = errors.New("wrong bytes")
		}
		result <- err
		// Keep execution active while testing duplicate uploads.
		<-ctx.Done()
		return Output{}, ctx.Err()
	})
	e.Messages[0].Files = []File{file}
	raw, _ := json.Marshal(e)
	if _, err := s.Submit(t.Context(), e, Digest(raw)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		status, err := s.Lookup(e.OwnerID, e.ClientID, e.RequestID)
		if err != nil {
			t.Fatal(err)
		}
		if len(status.PendingInputs) > 0 {
			if status.PendingInputs[0] != (PendingInput{FileID: file.ID, Size: file.Size, SHA256: file.SHA256}) {
				t.Fatal(status.PendingInputs)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("handle did not become pending")
		}
		time.Sleep(time.Millisecond)
	}
	if err := s.Upload(e.OwnerID, e.ClientID, e.InstallationID, e.RequestID, newUUID(), Digest(content), content); !errors.Is(err, ErrConflict) {
		t.Fatalf("undeclared upload: %v", err)
	}
	wrong := []byte("wrong")
	if err := s.Upload(e.OwnerID, e.ClientID, e.InstallationID, e.RequestID, file.ID, Digest(wrong), wrong); !errors.Is(err, ErrConflict) {
		t.Fatalf("digest mismatch: %v", err)
	}
	for range 2 {
		if err := s.Upload(e.OwnerID, e.ClientID, e.InstallationID, e.RequestID, file.ID, file.SHA256, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	control, err := os.ReadFile(filepath.Join(s.root, "control.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(control), string(content)) || strings.Contains(string(control), file.Name) {
		t.Fatal("historical file leaked into control ledger")
	}
	if err := s.Cancel(e.OwnerID, e.ClientID, e.RequestID); err != nil {
		t.Fatal(err)
	}
	s.Wait()
	if len(s.handles) != 0 {
		t.Fatal("stage retained after execution")
	}
}

func TestHandleWaitCancellationRejectsLateUpload(t *testing.T) {
	var s *Service
	file := File{ID: newUUID(), Name: "report.md", Size: 1, SHA256: Digest([]byte("x"))}
	waiting := make(chan error, 1)
	s, e, _ := setup(t, func(ctx context.Context, e Envelope, _ map[string][]byte) (Output, error) {
		_, err := s.AwaitHandle(ctx, e, file.ID)
		waiting <- err
		return Output{}, err
	})
	e.Messages[0].Files = []File{file}
	raw, _ := json.Marshal(e)
	if _, err := s.Submit(t.Context(), e, Digest(raw)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		status, _ := s.Lookup(e.OwnerID, e.ClientID, e.RequestID)
		if len(status.PendingInputs) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not pending")
		}
		time.Sleep(time.Millisecond)
	}
	if err := s.Cancel(e.OwnerID, e.ClientID, e.RequestID); err != nil {
		t.Fatal(err)
	}
	if err := <-waiting; !errors.Is(err, context.Canceled) {
		t.Fatalf("wait error: %v", err)
	}
	s.Wait()
	if err := s.Upload(e.OwnerID, e.ClientID, e.InstallationID, e.RequestID, file.ID, file.SHA256, []byte("x")); !errors.Is(err, ErrConflict) {
		t.Fatalf("late upload: %v", err)
	}
}

func TestDecodeHandleValidation(t *testing.T) {
	e := fixture()
	data := []byte("x")
	file := File{ID: newUUID(), Name: "x.md", Size: 1, SHA256: Digest(data)}
	e.Messages[0].Files = []File{file}
	check := func(want bool) {
		t.Helper()
		raw, _ := json.Marshal(e)
		_, err := Decode(raw, Digest(raw))
		if (err == nil) != want {
			t.Fatalf("decode error=%v", err)
		}
	}
	check(true)
	e.Messages[1].Files = []File{file}
	check(false)
	e.Messages[1].Files = nil
	e.Messages[0].Files[0].Name = "../x"
	check(false)
}

func TestCurrentInputTakesPrecedenceOverMatchingHistoryHandle(t *testing.T) {
	data := []byte("explicit")
	file := File{ID: newUUID(), Name: "same.txt", Size: len(data), SHA256: Digest(data)}
	e := fixture()
	e.InputFiles = []File{file}
	e.Messages[0].Files = []File{file}
	raw, _ := json.Marshal(e)
	if _, err := Decode(raw, Digest(raw)); err != nil {
		t.Fatal(err)
	}
	stage := newHandleStage(e)
	if _, ok := stage.declared[file.ID]; ok {
		t.Fatal("current input was also declared for on-demand upload")
	}
}

func TestExecutionEnvelopeDirectCutoverRejectsSchemaOne(t *testing.T) {
	e := fixture()
	e.SchemaVersion = 1
	raw, _ := json.Marshal(e)
	if _, err := Decode(raw, Digest(raw)); err == nil {
		t.Fatal("retired schema 1 accepted")
	}
	e.SchemaVersion = 2
	raw, _ = json.Marshal(e)
	if _, err := Decode(raw, Digest(raw)); err != nil {
		t.Fatal(err)
	}
}

func awaitPendingHandles(t *testing.T, s *Service, e Envelope, count int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status, err := s.Lookup(e.OwnerID, e.ClientID, e.RequestID)
		if err != nil {
			t.Fatal(err)
		}
		if len(status.PendingInputs) == count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("did not reach %d pending handles", count)
}

func TestConcurrentHandleReadersShareUploadAndCancelIndependently(t *testing.T) {
	var s *Service
	start := make(chan struct{})
	keep := make(chan struct{})
	s, e, _ := setup(t, func(ctx context.Context, e Envelope, _ map[string][]byte) (Output, error) {
		close(start)
		select {
		case <-keep:
		case <-ctx.Done():
		}
		return Output{Content: "done"}, ctx.Err()
	})
	content := []byte("same immutable original")
	file := File{ID: newUUID(), Name: "source.md", Size: len(content), SHA256: Digest(content)}
	e.Messages[0].Files = []File{file}
	raw, _ := json.Marshal(e)
	if _, err := s.Submit(t.Context(), e, Digest(raw)); err != nil {
		t.Fatal(err)
	}
	<-start
	if err := s.Upload(e.OwnerID, e.ClientID, e.InstallationID, e.RequestID, file.ID, file.SHA256, content); !errors.Is(err, ErrConflict) {
		t.Fatalf("unsolicited upload=%v", err)
	}
	short, cancel := context.WithCancel(t.Context())
	defer cancel()
	type result struct {
		data []byte
		err  error
	}
	a, b := make(chan result, 1), make(chan result, 1)
	go func() { data, err := s.AwaitHandle(short, e, file.ID); a <- result{data, err} }()
	go func() { data, err := s.AwaitHandle(t.Context(), e, file.ID); b <- result{data, err} }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.mu.Lock()
		stage := s.handles[keyFor(e.OwnerID, e.ClientID, e.RequestID)]
		waiters := 0
		if w := stage.pending[file.ID]; w != nil {
			waiters = w.waiters
		}
		s.mu.Unlock()
		if waiters == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("readers did not coalesce")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if got := <-a; !errors.Is(got.err, context.Canceled) {
		t.Fatalf("cancelled reader=%v", got.err)
	}
	awaitPendingHandles(t, s, e, 1)
	if err := s.Upload(e.OwnerID, e.ClientID, e.InstallationID, e.RequestID, file.ID, file.SHA256, content); err != nil {
		t.Fatal(err)
	}
	got := <-b
	if got.err != nil || string(got.data) != string(content) {
		t.Fatalf("other reader=%+v", got)
	}
	got.data[0] = 'X'
	read, err := s.AwaitHandle(t.Context(), e, file.ID)
	if err != nil || string(read) != string(content) {
		t.Fatalf("returned bytes aliased upload: %q %v", read, err)
	}
	close(keep)
	s.Wait()
}

func TestHandleCancellationWakesReaderOutsideExecutionContext(t *testing.T) {
	s, e, _ := setup(t, func(ctx context.Context, _ Envelope, _ map[string][]byte) (Output, error) {
		<-ctx.Done()
		return Output{}, ctx.Err()
	})
	file := File{ID: newUUID(), Name: "source.md", Size: 1, SHA256: Digest([]byte("x"))}
	e.Messages[0].Files = []File{file}
	raw, _ := json.Marshal(e)
	if _, err := s.Submit(t.Context(), e, Digest(raw)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.AwaitHandle(t.Context(), e, file.ID); done <- err }()
	awaitPendingHandles(t, s, e, 1)
	if err := s.Cancel(e.OwnerID, e.ClientID, e.RequestID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrExpired) {
			t.Fatalf("cancel wake=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal cleanup retained waiter")
	}
	if err := s.Upload(e.OwnerID, e.ClientID, e.InstallationID, e.RequestID, file.ID, file.SHA256, []byte("x")); !errors.Is(err, ErrConflict) {
		t.Fatalf("late upload=%v", err)
	}
}

func TestHandleReservationsBoundConcurrentReads(t *testing.T) {
	s, e, _ := setup(t, func(ctx context.Context, _ Envelope, _ map[string][]byte) (Output, error) {
		<-ctx.Done()
		return Output{}, ctx.Err()
	})
	for range 4 {
		e.Messages[0].Files = append(e.Messages[0].Files, File{ID: newUUID(), Name: "large.bin", Size: ResultBytes, SHA256: Digest([]byte("manifest-only"))})
	}
	raw, _ := json.Marshal(e)
	if _, err := s.Submit(t.Context(), e, Digest(raw)); err != nil {
		t.Fatal(err)
	}
	readers, cancel := context.WithCancel(t.Context())
	defer cancel()
	results := make(chan error, 4)
	for _, file := range e.Messages[0].Files {
		go func(id string) { _, err := s.AwaitHandle(readers, e, id); results <- err }(file.ID)
	}
	select {
	case err := <-results:
		if !errors.Is(err, ErrCapacity) {
			t.Fatalf("reservation=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("oversized concurrent reservations admitted")
	}
	awaitPendingHandles(t, s, e, 3)
	cancel()
	for range 3 {
		<-results
	}
	awaitPendingHandles(t, s, e, 0)
	if err := s.Cancel(e.OwnerID, e.ClientID, e.RequestID); err != nil {
		t.Fatal(err)
	}
}

func TestDecodeRequiresStableMessageIDsAndConsistentCurrentMetadata(t *testing.T) {
	for name, change := range map[string]func(*Envelope){
		"missing":   func(e *Envelope) { e.Messages[0].ID = "" },
		"duplicate": func(e *Envelope) { e.Messages[0].ID = e.Messages[1].ID },
		"conflicting current file": func(e *Envelope) {
			f := File{ID: newUUID(), Name: "a.md", Size: 1, SHA256: Digest([]byte("x"))}
			e.InputFiles = []File{f}
			f.Name = "renamed.md"
			e.Messages[0].Files = []File{f}
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := fixture()
			change(&e)
			raw, _ := json.Marshal(e)
			if _, err := Decode(raw, Digest(raw)); err == nil {
				t.Fatal("invalid schema2 accepted")
			}
		})
	}
}

func TestHandleWaitUsesExecutionDeadlineAndReleasesPendingReservation(t *testing.T) {
	s, e, _ := setup(t, func(ctx context.Context, _ Envelope, _ map[string][]byte) (Output, error) {
		<-ctx.Done()
		return Output{}, ctx.Err()
	})
	file := File{ID: newUUID(), Name: "source.md", Size: 1, SHA256: Digest([]byte("x"))}
	e.Messages[0].Files = []File{file}
	raw, _ := json.Marshal(e)
	if _, err := s.Submit(t.Context(), e, Digest(raw)); err != nil {
		t.Fatal(err)
	}
	key := keyFor(e.OwnerID, e.ClientID, e.RequestID)
	s.mu.Lock()
	f := s.control.Fences[key]
	f.Deadline = time.Now().Add(30 * time.Millisecond)
	s.control.Fences[key] = f
	s.mu.Unlock()
	start := time.Now()
	if _, err := s.AwaitHandle(t.Context(), e, file.ID); !errors.Is(err, ErrExpired) {
		t.Fatalf("deadline wait=%v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("file read exceeded execution deadline")
	}
	s.mu.Lock()
	pending := len(s.handles[key].pending)
	reserved := s.handles[key].reserved
	s.mu.Unlock()
	if pending != 0 || reserved != ContextBytes {
		t.Fatalf("deadline retained reservation: pending=%d bytes=%d", pending, reserved)
	}
	if err := s.Cancel(e.OwnerID, e.ClientID, e.RequestID); err != nil {
		t.Fatal(err)
	}
}

func TestRevocationAndCloseDropUnsubmittedUploadBytes(t *testing.T) {
	s, e, _ := setup(t, nil)
	file := newUUID()
	raw := []byte("private staged input")
	if err := s.Upload(e.OwnerID, e.ClientID, e.InstallationID, e.RequestID, file, Digest(raw), raw); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeWorkbenchClient(e.ClientID); err != nil {
		t.Fatal(err)
	}
	if len(s.inputs) != 0 {
		t.Fatal("revocation retained unsubmitted content")
	}
	if err := s.Upload(e.OwnerID, e.ClientID, e.InstallationID, newUUID(), file, Digest(raw), raw); !errors.Is(err, ErrExpired) {
		t.Fatalf("revoked upload accepted: %v", err)
	}
	other := fixture()
	other.ClientID = "other-client"
	if err := s.Bind(other.OwnerID, other.ClientID, other.InstallationID); err != nil {
		t.Fatal(err)
	}
	if err := s.Upload(other.OwnerID, other.ClientID, other.InstallationID, other.RequestID, file, Digest(raw), raw); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if len(s.inputs) != 0 {
		t.Fatal("closed service retained staged content")
	}
}
