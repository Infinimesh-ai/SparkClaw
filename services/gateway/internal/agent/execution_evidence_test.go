package agent

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/artifact"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/executionevidence"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
)

func TestExecutionEvidenceFileBackendExportsFrozenDocumentOnDemand(t *testing.T) {
	root := t.TempDir()
	cfg := agentTestConfig()
	cfg.Workspaces.DefaultRoot, cfg.Workspaces.Allowlist = root, []string{root}
	cfg.Storage.TraceDir, cfg.Storage.ArtifactDir = filepath.Join(root, "traces"), filepath.Join(root, "artifacts")
	cfg.Storage.ExecutionEvidence.Enabled = true
	st, err := store.NewFileStore(filepath.Join(root, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	session := storetest.MustCreateSessionWithScope(t, st, "evaluation", app.DefaultOwnerID, root, "web", false)
	hub := toolhub.New(cfg, st)
	defer hub.Close()
	writer := trace.NewWriterFromConfig(cfg)
	runtime := NewRuntime(st, hub, policy.New(cfg), modelrouter.New(cfg), writer)
	const original = "Original evaluation source, with a final exception: retain 14 days.\n"
	path := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := runtime.HandleMessage(context.Background(), session.ID, "Summarize notes.txt")
	if err != nil {
		t.Fatal(err)
	}
	scope := executionevidence.Scope{OwnerID: session.OwnerID, SessionID: session.ID, RunID: result.Run.ID}
	manifest, err := writer.ExecutionEvidence().Read(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if !manifest.Capture.Sealed || !manifest.Capture.Complete {
		t.Fatalf("incomplete capture: %+v", manifest.Capture)
	}
	if manifest.Review.Mode != "owner_external" || manifest.Review.Status != "not_performed" {
		t.Fatalf("unexpected review: %+v", manifest.Review)
	}
	for _, p := range manifest.Payloads {
		if strings.HasSuffix(p.Path, ".zip") || p.Kind == "report" {
			t.Fatalf("automatic deliverable: %+v", p)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "evaluation.zip")
	if _, err := writer.ExecutionEvidence().Export(t.Context(), scope, output, false); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(output)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	foundSource, foundProjection, foundTool, foundResult := false, false, false, false
	for _, file := range archive.File {
		r, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil {
			t.Fatal(err)
		}
		foundSource = foundSource || string(raw) == original
		if strings.HasSuffix(file.Name, "events.jsonl") {
			foundProjection = strings.Contains(string(raw), "model.projection")
			foundTool = strings.Contains(string(raw), "tool.observation")
			foundResult = strings.Contains(string(raw), "run.result")
		}
	}
	if !foundSource || !foundProjection || !foundTool || !foundResult {
		t.Fatalf("missing chain: source=%v projection=%v tool=%v result=%v", foundSource, foundProjection, foundTool, foundResult)
	}
}

func TestExecutionEvidenceFailureDoesNotRetryBusinessOperation(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	path := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(path, []byte("occupied"), 0600); err != nil {
		t.Fatal(err)
	}
	runtime.evidence = executionevidence.New(path, executionevidence.DefaultOptions())
	result, err := runtime.HandleMessageWithAttachmentsIdempotent(t.Context(), session.ID, "source-eval", "run-eval", "hello", nil)
	if err != nil || result.Run.CompletedAt == nil {
		t.Fatalf("business failed: %+v %v", result.Run, err)
	}
	if runs := testListRuns(st, session.ID); len(runs) != 1 {
		t.Fatalf("capture retried operation: %d runs", len(runs))
	}
}

func TestExecutionEvidenceTransientScopeNeverInheritsHostCapture(t *testing.T) {
	parent, _, _, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	root := t.TempDir()
	capture := filepath.Join(root, "host-evidence")
	parent.evidence = executionevidence.New(capture, executionevidence.DefaultOptions())
	cfg := parent.tools.Config()
	cfg.Storage.ArtifactDir = filepath.Join(root, "artifacts")
	local := store.NewMemoryStore()
	scoped, release, err := parent.WithExecutionScope(local, toolhub.ExecutionResources{OwnerID: app.DefaultOwnerID, WorkspaceRoot: root, Artifacts: artifact.NewStore(cfg.Storage)})
	if err != nil {
		t.Fatal(err)
	}
	defer release(t.Context())
	if scoped.evidence != nil {
		t.Fatal("host recorder was inherited")
	}
	session := storetest.MustCreateSessionWithScope(t, local, "transient", app.DefaultOwnerID, root, "web", false)
	if _, err := scoped.HandleMessage(t.Context(), session.ID, "hello"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(capture); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("transient content retained: %v", err)
	}
}

func TestExecutionEvidenceManualReadCapturesSuccessAndFailure(t *testing.T) {
	runtime, _, session, closeRuntime := newWorkflowE2ERuntime(t, nil)
	defer closeRuntime()
	runtime.evidence = executionevidence.New(filepath.Join(t.TempDir(), "evidence"), executionevidence.DefaultOptions())
	if err := os.WriteFile(filepath.Join(session.WorkspaceRoot, "manual.txt"), []byte("manual source\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"manual.txt", "missing.txt"} {
		result, err := runtime.InvokeToolManually(t.Context(), "files.read", map[string]any{"path": path}, session.ID)
		if (err != nil) != (path == "missing.txt") {
			t.Fatalf("manual result %s: %v", path, err)
		}
		scope := executionevidence.Scope{OwnerID: session.OwnerID, SessionID: session.ID, RunID: result.Call.RunID}
		manifest, err := runtime.evidence.Read(t.Context(), scope)
		if err != nil {
			t.Fatal(err)
		}
		if !manifest.Capture.Complete || !manifest.Capture.Sealed {
			t.Fatalf("manual capture incomplete: %+v", manifest.Capture)
		}
		if path == "manual.txt" {
			found := false
			for _, p := range manifest.Payloads {
				found = found || p.Kind == "source"
			}
			if !found {
				t.Fatal("manual source snapshot missing")
			}
		} else if manifest.Status != "failed" {
			t.Fatalf("failed operation was rewritten: %s", manifest.Status)
		}
	}
}
