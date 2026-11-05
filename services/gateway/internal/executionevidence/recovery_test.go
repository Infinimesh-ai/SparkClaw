package executionevidence

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func replaceManifest(t *testing.T, r *Recorder, s Scope, edit func(*Manifest)) {
	t.Helper()
	path, err := runPath(s)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(r.root, path, "manifest.json")
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	edit(&m)
	raw, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSealOnlyAcceptsTerminalBusinessStates(t *testing.T) {
	for _, state := range []string{"", "received", "running", "approval_pending", "browser_login_blocked", "unknown"} {
		t.Run(state, func(t *testing.T) {
			r, s, ctx := fixture(t, Options{})
			before, err := r.Read(ctx, s)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.Seal(ctx, s, state); !errors.Is(err, ErrIncomplete) {
				t.Fatalf("nonterminal seal: %v", err)
			}
			after, err := r.Read(ctx, s)
			if err != nil {
				t.Fatal(err)
			}
			if after.Capture.Sealed || after.EventCount != before.EventCount {
				t.Fatal("rejected seal mutated capture")
			}
		})
	}
	for _, state := range []string{"completed", "failed", "blocked", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			r, s, ctx := fixture(t, Options{})
			if err := r.Seal(ctx, s, state); err != nil {
				t.Fatal(err)
			}
			m, err := r.Read(ctx, s)
			if err != nil || !m.Capture.Sealed || !m.Capture.Complete || m.Status != state {
				t.Fatalf("terminal seal: %+v %v", m, err)
			}
		})
	}
}

func TestInterruptedBeginCannotBecomeComplete(t *testing.T) {
	for _, missing := range []string{"request", "begin_event"} {
		t.Run(missing, func(t *testing.T) {
			r, s, ctx := fixture(t, Options{})
			// Simulate either initialization crash window: before the frozen
			// request identity, or after it but before the first event fsync.
			replaceManifest(t, r, s, func(m *Manifest) {
				m.EventCount, m.EventBytes = 0, 0
				if missing == "request" {
					m.RequestPayloadID = ""
				}
			})
			path, _ := runPath(s)
			if err := os.Remove(filepath.Join(r.root, path, "events.jsonl")); err != nil {
				t.Fatal(err)
			}
			fresh := New(r.root, Options{})
			if _, err := fresh.Read(ctx, s); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("incomplete begin accepted: %v", err)
			}
			if err := fresh.Seal(ctx, s, "completed"); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("incomplete begin sealed: %v", err)
			}
			if _, err := fresh.Export(ctx, s, filepath.Join(t.TempDir(), "strict.zip"), false); err == nil {
				t.Fatal("incomplete begin exported as complete")
			}
			m, err := fresh.Export(ctx, s, filepath.Join(t.TempDir(), "diagnostic.zip"), true)
			if err != nil || m.Capture.Complete || len(m.Capture.Gaps) == 0 {
				t.Fatalf("diagnostic lost gap: %+v %v", m, err)
			}
		})
	}
}

func TestMissingReferencedPayloadMetadataIsAnExplicitGap(t *testing.T) {
	r, s, ctx := fixture(t, Options{})
	p, err := CaptureJSON(ctx, "model_response", "actual-response", map[string]string{"response": "answer"})
	if err != nil {
		t.Fatal(err)
	}
	if err := Record(ctx, Event{Kind: "model.response", PayloadIDs: []string{p.ID}}); err != nil {
		t.Fatal(err)
	}
	if err := r.Seal(ctx, s, "completed"); err != nil {
		t.Fatal(err)
	}
	replaceManifest(t, r, s, func(m *Manifest) {
		for i, payload := range m.Payloads {
			if payload.ID == p.ID {
				m.Payloads = append(m.Payloads[:i], m.Payloads[i+1:]...)
				break
			}
		}
	})
	if _, err := r.Export(ctx, s, filepath.Join(t.TempDir(), "strict.zip"), false); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("dangling event reference accepted: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "diagnostic.zip")
	m, err := r.Export(ctx, s, dest, true)
	if err != nil || m.Capture.Complete || m.Export.Complete || !containsEntry(m.Export.MissingEntries, "payload:"+p.ID) {
		t.Fatalf("missing payload omitted silently: %+v %v", m, err)
	}
	if len(zipEntries(t, dest)["events.jsonl"]) == 0 {
		t.Fatal("original chain missing from diagnostic")
	}
}

func TestSealedManifestRequiresMatchingTerminalEvent(t *testing.T) {
	r, s, ctx := fixture(t, Options{})
	if err := r.Seal(ctx, s, "completed"); err != nil {
		t.Fatal(err)
	}
	replaceManifest(t, r, s, func(m *Manifest) { m.Status = "failed" })
	if _, err := r.Read(ctx, s); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("terminal mismatch accepted: %v", err)
	}
	if _, err := r.Export(ctx, s, filepath.Join(t.TempDir(), "strict.zip"), false); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("terminal mismatch exported: %v", err)
	}
}

func TestExportArchiveBudgetFailureLeavesNoPartialOutput(t *testing.T) {
	r, s, ctx := fixture(t, Options{})
	if err := r.Seal(ctx, s, "completed"); err != nil {
		t.Fatal(err)
	}
	m, err := r.Read(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	// Admit raw evidence, then exhaust the budget on ZIP/manifest/report
	// overhead. This exercises cleanup after output streaming has begun.
	limit := m.EventBytes + 1
	for _, payload := range m.Payloads {
		limit += payload.Bytes
	}
	bounded := New(r.root, Options{MaxExportBytes: limit})
	dir := t.TempDir()
	if _, err := bounded.Export(ctx, s, filepath.Join(dir, "evaluation.zip"), false); !errors.Is(err, ErrLimit) {
		t.Fatalf("archive budget ignored: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("partial export was published or retained: %v %v", entries, err)
	}
	if _, err := r.Read(ctx, s); err != nil {
		t.Fatalf("failed export changed capture: %v", err)
	}
}
