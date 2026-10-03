package r3execution

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fixture() Envelope {
	return Envelope{SchemaVersion: 1, DeploymentID: "deployment", OwnerID: "owner", ClientID: "client", InstallationID: "11111111-1111-4111-8111-111111111111", ConversationID: newUUID(), TaskID: newUUID(), RequestID: newUUID(), Messages: []Message{{Role: "assistant", Content: "ordinary untrusted history"}, {Role: "user", Content: "synthetic private question"}}}
}
func setup(t *testing.T, execute Executor) (*Service, Envelope, string) {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "r3"), execute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	e := fixture()
	if err = s.Bind(e.OwnerID, e.ClientID, e.InstallationID); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(e)
	return s, e, Digest(raw)
}
func completed(t *testing.T, s *Service, e Envelope) Status {
	t.Helper()
	s.Wait()
	status, err := s.Lookup(e.OwnerID, e.ClientID, e.RequestID)
	if err != nil || status.State != "completed" || status.Result == nil {
		t.Fatalf("result=%+v err=%v", status, err)
	}
	return status
}
func TestDurableAdmissionRedeliveryAckRestartAndResidualAudit(t *testing.T) {
	var calls atomic.Int32
	s, e, digest := setup(t, func(ctx context.Context, e Envelope, files map[string][]byte) (Output, error) {
		calls.Add(1)
		return Output{Content: "synthetic private output", Files: map[string][]byte{"report.txt": []byte("private report bytes")}}, nil
	})
	for range 5 {
		if _, err := s.Submit(t.Context(), e, digest); err != nil {
			t.Fatal(err)
		}
	}
	status := completed(t, s, e)
	if calls.Load() != 1 {
		t.Fatalf("executions=%d", calls.Load())
	}
	raw, err := os.ReadFile(filepath.Join(s.root, "control.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{e.Messages[1].Content, e.Messages[0].Content, "synthetic private output", "report.txt", "private report bytes", e.ConversationID} {
		if strings.Contains(string(raw), canary) {
			t.Fatalf("content/control leakage: %s", canary)
		}
	}
	sealed, err := os.ReadFile(s.contentPath(keyFor(e.OwnerID, e.ClientID, e.RequestID)))
	if err != nil || strings.Contains(string(sealed), "private") {
		t.Fatal("unencrypted spool", err)
	}
	s.Close()
	restarted, err := New(s.root, func(context.Context, Envelope, map[string][]byte) (Output, error) {
		t.Fatal("restart reran task")
		return Output{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	replay := completed(t, restarted, e)
	if replay.Result.Digest != status.Result.Digest || !replay.ExpiresAt.Equal(*status.ExpiresAt) {
		t.Fatal("restart changed result/expiry")
	}
	if err = restarted.Ack(e.OwnerID, e.ClientID, e.RequestID, 1, status.Result.Digest, false); !errors.Is(err, ErrConflict) {
		t.Fatal("transport ACK accepted", err)
	}
	var payload Payload
	if err = json.Unmarshal([]byte(status.Result.Payload), &payload); err != nil {
		t.Fatal(err)
	}
	bytes, err := restarted.File(e.OwnerID, e.ClientID, e.RequestID, payload.Files[0].ID)
	if err != nil || Digest(bytes) != payload.Files[0].SHA256 {
		t.Fatal("file", err)
	}
	for range 2 {
		if err = restarted.Ack(e.OwnerID, e.ClientID, e.RequestID, 1, status.Result.Digest, true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = os.Stat(s.contentPath(keyFor(e.OwnerID, e.ClientID, e.RequestID))); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("ACK did not erase content", err)
	}
	status, err = restarted.Submit(t.Context(), e, digest)
	if err != nil || status.State != "delivered" || calls.Load() != 1 {
		t.Fatalf("fence lost: %+v %v", status, err)
	}
}
func TestExpiryCapacityIsolationDigestAndInstallation(t *testing.T) {
	s, e, digest := setup(t, func(context.Context, Envelope, map[string][]byte) (Output, error) {
		return Output{Content: "answer"}, nil
	})
	if err := s.Bind(e.OwnerID, e.ClientID, newUUID()); !errors.Is(err, ErrConflict) {
		t.Fatal("installation takeover", err)
	}
	if _, err := Decode([]byte(`{"prompt":"injected"}`), digest); err == nil {
		t.Fatal("unbounded envelope")
	}
	raw, _ := json.Marshal(e)
	decoded, err := Decode(raw, digest)
	if err != nil || decoded.RequestID != e.RequestID {
		t.Fatal(err)
	}
	var ids []string
	for range OwnerBytes / TaskBytes {
		next := e
		next.RequestID = newUUID()
		ids = append(ids, next.RequestID)
		if _, err = s.Submit(t.Context(), next, digest); err != nil {
			t.Fatal(err)
		}
		s.Wait()
	}
	if _, err = s.Submit(t.Context(), e, digest); !errors.Is(err, ErrCapacity) {
		t.Fatal("capacity", err)
	}
	if _, err = s.Lookup(e.OwnerID, "other-device", ids[0]); !errors.Is(err, ErrNotFound) {
		t.Fatal("device leak", err)
	}
	s.now = func() time.Time { return time.Now().UTC().Add(25 * time.Hour) }
	status, err := s.Lookup(e.OwnerID, e.ClientID, ids[0])
	if err != nil || status.State != "delivery_expired" || status.Result != nil {
		t.Fatal("expired deliverable", status, err)
	}
	if err = s.Sweep(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(s.root, "content"))
	if len(entries) != 0 {
		t.Fatal("expired content remains")
	}
	s.Close()
	restarted, err := New(s.root, nil)
	if err != nil {
		t.Fatal(err)
	}
	status, err = restarted.Lookup(e.OwnerID, e.ClientID, ids[0])
	if err != nil || status.State != "delivery_expired" {
		t.Fatal("restart extended expiry", status, err)
	}
}
func TestInterruptedExecutionNeverReplaysAndFailedPersistenceNeverRuns(t *testing.T) {
	var calls atomic.Int32
	s, e, digest := setup(t, func(ctx context.Context, e Envelope, _ map[string][]byte) (Output, error) {
		calls.Add(1)
		<-ctx.Done()
		return Output{}, ctx.Err()
	})
	if _, err := s.Submit(t.Context(), e, digest); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	s.Close()
	restarted, err := New(s.root, nil)
	if err != nil {
		t.Fatal(err)
	}
	status, err := restarted.Submit(t.Context(), e, digest)
	if err != nil || status.State != "unknown" {
		t.Fatal("interruption replay", status, err)
	}
	if err = s.Cancel(e.OwnerID, e.ClientID, e.RequestID); err != nil {
		t.Fatal(err)
	}
	s.Wait()
	s = restarted
	e.RequestID = newUUID()
	if err = os.Remove(filepath.Join(s.root, "control.json")); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(s.root, "control.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Submit(t.Context(), e, digest); !errors.Is(err, ErrUnavailable) || calls.Load() != 1 {
		t.Fatal("executed without durable fence", err)
	}
}
func TestBoundedInputUploadIsExplicitTemporaryAndVerified(t *testing.T) {
	s, e, _ := setup(t, func(_ context.Context, _ Envelope, files map[string][]byte) (Output, error) {
		for _, data := range files {
			if string(data) != "input body" {
				t.Error("wrong input")
			}
		}
		return Output{Content: "summary"}, nil
	})
	file := newUUID()
	data := []byte("input body")
	manifest := File{ID: file, Name: "input.txt", Size: len(data), SHA256: Digest(data)}
	e.InputFiles = []File{manifest}
	raw, _ := json.Marshal(e)
	digest := Digest(raw)
	if err := s.Upload(e.OwnerID, e.ClientID, e.InstallationID, e.RequestID, file, manifest.SHA256, data); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lookup(e.OwnerID, e.ClientID, e.RequestID); !errors.Is(err, ErrNotFound) {
		t.Fatal("upload submitted work", err)
	}
	if _, err := s.Submit(t.Context(), e, digest); err != nil {
		t.Fatal(err)
	}
	completed(t, s, e)
	if len(s.inputs) != 0 {
		t.Fatal("input retained after admission")
	}
}

func TestTemporaryContentBudgetRejectsBeforeRetainingPayload(t *testing.T) {
	budget := NewBudget(1024)
	if err := budget.Admit(strings.Repeat("x", 600)); !errors.Is(err, ErrCapacity) {
		t.Fatal("oversized payload retained", err)
	}
	if err := budget.Reserve(900); err != nil {
		t.Fatal(err)
	}
	if err := budget.Reserve(200); !errors.Is(err, ErrCapacity) {
		t.Fatal("cumulative capacity", err)
	}
}

func TestDuplicateInputNamesRejectBeforeAdmissionAndCannotOverwrite(t *testing.T) {
	s, e, _ := setup(t, func(context.Context, Envelope, map[string][]byte) (Output, error) {
		t.Fatal("ambiguous request executed")
		return Output{}, nil
	})
	for _, body := range []string{"first source", "second source"} {
		file := File{ID: newUUID(), Name: "notes.md", Size: len(body), SHA256: Digest([]byte(body))}
		if err := s.Upload(e.OwnerID, e.ClientID, e.InstallationID, e.RequestID, file.ID, file.SHA256, []byte(body)); err != nil {
			t.Fatal(err)
		}
		e.InputFiles = append(e.InputFiles, file)
	}
	raw, _ := json.Marshal(e)
	if _, err := Decode(raw, Digest(raw)); err == nil {
		t.Fatal("duplicate filenames decoded")
	}
	if _, err := s.Submit(t.Context(), e, Digest(raw)); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err := s.Lookup(e.OwnerID, e.ClientID, e.RequestID); !errors.Is(err, ErrNotFound) {
		t.Fatal("request admitted", err)
	}
}
