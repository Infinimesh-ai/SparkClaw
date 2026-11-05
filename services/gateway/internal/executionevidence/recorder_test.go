package executionevidence

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func fixture(t *testing.T, opts Options) (*Recorder, Scope, context.Context) {
	t.Helper()
	r := New(filepath.Join(t.TempDir(), "evidence"), opts)
	s := Scope{OwnerID: "owner_test", SessionID: "session_test", RunID: "run_test"}
	ctx := r.Bind(t.Context(), s)
	if err := r.Begin(ctx, s, map[string]any{"request": "Read the second paragraph", "attachment": "report.docx"}); err != nil {
		t.Fatal(err)
	}
	return r, s, ctx
}
func zipEntries(t *testing.T, path string) map[string][]byte {
	t.Helper()
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	out := map[string][]byte{}
	for _, f := range archive.File {
		rd, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(rd)
		rd.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[f.Name] = raw
	}
	return out
}
func TestFrozenSourceModelCallsAndOfflineExport(t *testing.T) {
	r, s, ctx := fixture(t, Options{})
	source := filepath.Join(t.TempDir(), "source.txt")
	if err := os.WriteFile(source, []byte("original text"), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Capture(ctx, "source", "source.txt", "text/plain", file)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(source, []byte("changed later"), 0600); err != nil {
		t.Fatal(err)
	}
	input, err := CaptureJSON(ctx, "model_request", "call-input", map[string]any{"messages": []string{"second paragraph"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = Record(ctx, Event{ID: "event_start", Kind: "model.dispatched", SpanID: "call_1", Status: "dispatched", PayloadIDs: []string{input.ID}}); err != nil {
		t.Fatal(err)
	}
	output, err := CaptureJSON(ctx, "model_response", "call-output", map[string]any{"content": "a factual answer"})
	if err != nil {
		t.Fatal(err)
	}
	if err = Record(WithParent(ctx, "event_start"), Event{Kind: "model.completed", SpanID: "call_1", Status: "completed", PayloadIDs: []string{output.ID}, Data: map[string]any{"source_payload": p.ID}}); err != nil {
		t.Fatal(err)
	}
	if err = r.Seal(ctx, s, "completed"); err != nil {
		t.Fatal(err)
	}
	var deliverables []string
	_ = filepath.WalkDir(r.root, func(path string, d os.DirEntry, err error) error {
		if strings.HasSuffix(path, ".zip") || d != nil && d.Name() == "report.md" {
			deliverables = append(deliverables, path)
		}
		return err
	})
	if len(deliverables) != 0 {
		t.Fatalf("automatic deliverables: %v", deliverables)
	}
	// A fresh recorder exports only frozen bytes; the source is now gone.
	if err = os.Remove(source); err != nil {
		t.Fatal(err)
	}
	fresh := New(r.root, Options{})
	dest := filepath.Join(t.TempDir(), "evaluation.zip")
	m, err := fresh.Export(ctx, s, dest, false)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Capture.Complete || !m.Export.Complete || m.Review.Mode != "owner_external" || m.Review.Status != "not_performed" {
		t.Fatalf("bad flags: %+v", m)
	}
	entries := zipEntries(t, dest)
	if string(entries[p.Path]) != "original text" {
		t.Fatalf("source snapshot replaced: %q", entries[p.Path])
	}
	if !bytes.Contains(entries["events.jsonl"], []byte(`"parent_id":"event_start"`)) {
		t.Fatal("causal binding missing")
	}
	if !bytes.Contains(entries["report.md"], []byte("Read the second paragraph")) {
		t.Fatal("frozen request missing from factual report")
	}
	if _, ok := entries[input.Path]; !ok {
		t.Fatal("model request not portable")
	}
	if err = Record(ctx, Event{Kind: "late.event"}); !errors.Is(err, ErrSealed) {
		t.Fatalf("sealed record=%v", err)
	}
	original, _ := os.ReadFile(dest)
	if _, err = fresh.Export(ctx, s, dest, false); !errors.Is(err, os.ErrExist) {
		t.Fatalf("overwrite=%v", err)
	}
	after, _ := os.ReadFile(dest)
	if !bytes.Equal(original, after) {
		t.Fatal("existing export changed")
	}
}
func TestRequestFreezeAndRedactionKeepSourceBytes(t *testing.T) {
	redact := func(raw []byte) []byte { return bytes.ReplaceAll(raw, []byte("secret-value"), []byte("[REDACTED]")) }
	r, s, ctx := fixture(t, Options{Redact: redact})
	if err := r.Begin(ctx, s, map[string]any{"request": "Read the second paragraph", "attachment": "report.docx"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Begin(ctx, s, map[string]any{"request": "later context"}); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("request changed: %v", err)
	}
	p, err := CaptureJSON(ctx, "model_request", "secret", map[string]string{"content": "secret-value"})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Redacted || p.OriginalSHA256 == "" || p.OriginalSHA256 == p.SHA256 {
		t.Fatalf("missing redaction lineage: %+v", p)
	}
	source, err := Capture(ctx, "source", "secret.txt", "text/plain", strings.NewReader("secret-value"))
	if err != nil {
		t.Fatal(err)
	}
	if source.Redacted || source.SHA256 != digest([]byte("secret-value")) {
		t.Fatal("original was redacted")
	}
	if err = r.Seal(ctx, s, "failed"); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "report.zip")
	m, err := r.Export(ctx, s, dest, false)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Capture.Complete || m.Status != "failed" {
		t.Fatal("business failure confused with capture failure")
	}
	entries := zipEntries(t, dest)
	if bytes.Contains(entries[p.Path], []byte("secret-value")) || !json.Valid(entries[p.Path]) {
		t.Fatal("redacted request incorrect")
	}
	if string(entries[source.Path]) != "secret-value" {
		t.Fatal("source changed")
	}
}
func TestInterruptedAndMissingPayloadDiagnosticOnly(t *testing.T) {
	r, s, ctx := fixture(t, Options{})
	p, err := CaptureJSON(ctx, "model_request", "input", map[string]string{"content": "text"})
	if err != nil {
		t.Fatal(err)
	}
	if err = Record(ctx, Event{Kind: "model.dispatched", CallID: "call_interrupted", Status: "dispatched", PayloadIDs: []string{p.ID}}); err != nil {
		t.Fatal(err)
	}
	if err = r.Seal(ctx, s, "failed"); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "strict.zip")
	if _, err = r.Export(ctx, s, dest, false); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("strict=%v", err)
	}
	if _, err = os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("incomplete strict export published")
	}
	path, _ := runPath(s)
	if err = os.Remove(filepath.Join(r.root, path, "blobs", p.SHA256)); err != nil {
		t.Fatal(err)
	}
	dest = filepath.Join(t.TempDir(), "diagnostic.zip")
	m, err := r.Export(ctx, s, dest, true)
	if err != nil {
		t.Fatal(err)
	}
	if m.Capture.Complete || m.Export.Complete || len(m.Export.MissingEntries) != 1 || len(m.Capture.OpenSpans) != 1 {
		t.Fatalf("false completeness: %+v", m)
	}
	if _, ok := zipEntries(t, dest)[p.Path]; ok {
		t.Fatal("missing payload fabricated")
	}
}
func TestLimitsAndCancelledRecordAreHonest(t *testing.T) {
	r, s, ctx := fixture(t, Options{MaxPayloadBytes: 128, MaxEvents: 3})
	if _, err := Capture(ctx, "source", "large", "text/plain", strings.NewReader(strings.Repeat("x", 129))); !errors.Is(err, ErrLimit) {
		t.Fatalf("payload limit=%v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := Record(cancelled, Event{Kind: "tool.failed", Status: "failed"}); err != nil {
		t.Fatalf("terminal event lost on cancellation: %v", err)
	}
	if err := r.Seal(ctx, s, "failed"); err != nil {
		t.Fatal(err)
	}
	m, err := r.Read(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if m.Capture.Complete || len(m.Capture.Gaps) == 0 {
		t.Fatal("quota gap missing")
	}
	out := filepath.Join(t.TempDir(), "small.zip")
	tiny := New(r.root, Options{MaxExportBytes: 30})
	if _, err = tiny.Export(ctx, s, out, true); !errors.Is(err, ErrLimit) {
		t.Fatalf("export limit=%v", err)
	}
	if _, err = os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed export published")
	}
}
func TestConcurrentRecordersScopeAndReadOnlyInspection(t *testing.T) {
	r, s, ctx := fixture(t, Options{})
	second := New(r.root, Options{})
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			current := r
			if i%2 == 0 {
				current = second
			}
			errs <- current.Record(ctx, s, Event{Kind: "step", Data: map[string]any{"position": i}})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Seal(ctx, s, "completed"); err != nil {
		t.Fatal(err)
	}
	m, err := r.Read(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if m.EventCount != 42 || !m.Capture.Complete {
		t.Fatalf("events lost: %+v", m)
	}
	wrong := s
	wrong.OwnerID = "other_owner"
	if _, err = r.Export(ctx, wrong, filepath.Join(t.TempDir(), "other.zip"), true); err == nil {
		t.Fatal("cross-owner export")
	}
	wrong = s
	wrong.RunID = "../escape"
	if _, err = r.Read(ctx, wrong); !errors.Is(err, ErrScope) {
		t.Fatalf("invalid scope=%v", err)
	}
	absent := filepath.Join(t.TempDir(), "absent")
	if _, err = New(absent, Options{}).Read(ctx, s); err == nil {
		t.Fatal("missing capture read succeeded")
	}
	if _, err = os.Stat(absent); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read created storage")
	}
}
func TestPayloadSymlinkAndDigestTamperingRejected(t *testing.T) {
	for _, mode := range []string{"symlink", "digest"} {
		t.Run(mode, func(t *testing.T) {
			r, s, ctx := fixture(t, Options{})
			p, err := Capture(ctx, "source", "source", "text/plain", strings.NewReader("safe"))
			if err != nil {
				t.Fatal(err)
			}
			if err = r.Seal(ctx, s, "completed"); err != nil {
				t.Fatal(err)
			}
			path, _ := runPath(s)
			blob := filepath.Join(r.root, path, "blobs", p.SHA256)
			if mode == "symlink" {
				if err = os.Remove(blob); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(t.TempDir(), "secret")
				os.WriteFile(target, []byte("safe"), 0600)
				err = os.Symlink(target, blob)
			} else {
				err = os.WriteFile(blob, []byte("evil"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(t.TempDir(), "report.zip")
			if _, err = r.Export(ctx, s, out, false); !errors.Is(err, ErrIncomplete) {
				t.Fatalf("tampered payload=%v", err)
			}
			m, err := r.Export(ctx, s, out, true)
			if err != nil {
				t.Fatal(err)
			}
			if m.Export.Complete {
				t.Fatal("tamper hidden")
			}
			if _, ok := zipEntries(t, out)[p.Path]; ok {
				t.Fatal("unsafe entry included")
			}
		})
	}
}
func TestDurableAppendRecoversLaggingManifest(t *testing.T) {
	r, s, ctx := fixture(t, Options{})
	path, _ := runPath(s)
	manifest := filepath.Join(r.root, path, "manifest.json")
	before, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = Record(ctx, Event{ID: "persisted", Kind: "step"}); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash after event fsync but before manifest replacement.
	if err = os.WriteFile(manifest, before, 0600); err != nil {
		t.Fatal(err)
	}
	if err = New(r.root, Options{}).Record(ctx, s, Event{Kind: "recovered"}); err != nil {
		t.Fatal(err)
	}
	m, err := r.Read(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if m.EventCount != 3 {
		t.Fatalf("durable event lost: %d", m.EventCount)
	}
	if err = Record(ctx, Event{ID: "persisted", Kind: "duplicate"}); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("duplicate event=%v", err)
	}
}

func TestTotalQuotaStopsNewRunsWithoutDeletingHistory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "evidence")
	r := New(root, Options{MaxPayloadBytes: 1024, MaxTotalBytes: 100 << 10})
	s := Scope{OwnerID: "o", SessionID: "s", RunID: "r1"}
	if err := r.Begin(t.Context(), s, map[string]string{"request": "first"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Seal(t.Context(), s, "completed"); err != nil {
		t.Fatal(err)
	}
	other := s
	other.RunID = "r2"
	if err := r.Begin(t.Context(), other, map[string]string{"request": "second"}); !errors.Is(err, ErrLimit) {
		t.Fatalf("total quota=%v", err)
	}
	if _, err := r.Read(t.Context(), s); err != nil {
		t.Fatalf("old history lost: %v", err)
	}
	otherPath, _ := runPath(other)
	if _, err := os.Stat(filepath.Join(root, otherPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("over-quota run admitted")
	}
}

func TestInvalidRedactionAndTruncatedEventLogNeverLookComplete(t *testing.T) {
	r, s, ctx := fixture(t, Options{})
	bad := New(r.root, Options{Redact: func([]byte) []byte { return []byte("broken json") }})
	if _, err := bad.CaptureJSON(ctx, s, "model_request", "input", map[string]string{"text": "hello"}); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("bad redaction=%v", err)
	}
	if err := r.Seal(ctx, s, "failed"); err != nil {
		t.Fatal(err)
	}
	path, _ := runPath(s)
	eventsPath := filepath.Join(r.root, path, "events.jsonl")
	f, err := os.OpenFile(eventsPath, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(`{"incomplete":`)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(ctx, s); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("corrupt chain=%v", err)
	}
	dest := filepath.Join(t.TempDir(), "diagnostic.zip")
	m, err := r.Export(ctx, s, dest, true)
	if err != nil {
		t.Fatal(err)
	}
	if m.Capture.Complete {
		t.Fatal("corrupt log declared complete")
	}
	if !bytes.Contains(zipEntries(t, dest)["events.jsonl"], []byte(`{"incomplete":`)) {
		t.Fatal("corrupt historical tail silently discarded")
	}
}

func TestMissingEventLogIsAnExplicitPackageGap(t *testing.T) {
	r, s, ctx := fixture(t, Options{})
	if err := r.Seal(ctx, s, "completed"); err != nil {
		t.Fatal(err)
	}
	path, _ := runPath(s)
	if err := os.Remove(filepath.Join(r.root, path, "events.jsonl")); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "diagnostic.zip")
	m, err := r.Export(ctx, s, out, true)
	if err != nil {
		t.Fatal(err)
	}
	if m.Capture.Complete || m.Export.Complete || !containsEntry(m.Export.MissingEntries, "events.jsonl") {
		t.Fatalf("missing log hidden: %+v", m)
	}
	if _, ok := zipEntries(t, out)["events.jsonl"]; ok {
		t.Fatal("missing log replaced with fabricated empty file")
	}
}
