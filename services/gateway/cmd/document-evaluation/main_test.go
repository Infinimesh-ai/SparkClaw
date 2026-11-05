package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/executionevidence"
)

func TestOfflineCLIExportsOnlyOnRequestAndRefusesForeignScope(t *testing.T) {
	root := filepath.Join(t.TempDir(), "capture")
	r := executionevidence.New(root, executionevidence.Options{})
	s := executionevidence.Scope{OwnerID: "owner_1", SessionID: "session_1", RunID: "run_1"}
	ctx := context.Background()
	if err := r.Begin(ctx, s, map[string]string{"question": "What is in the document?"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Seal(ctx, s, "completed"); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "evaluation.zip")
	var stdout, stderr bytes.Buffer
	args := []string{"--evidence-dir", root, "--owner", s.OwnerID, "--session", s.SessionID, "--run", s.RunID, "--output", output}
	if code := run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("code %d: %s", code, stderr.String())
	}
	var result map[string]any
	if json.Unmarshal(stdout.Bytes(), &result) != nil || result["capture_complete"] != true || result["package_complete"] != true {
		t.Fatalf("result %s", stdout.String())
	}
	archive, err := zip.OpenReader(output)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	found := false
	for _, file := range archive.File {
		if file.Name == "report.md" {
			found = true
		}
	}
	if !found {
		t.Fatal("report missing")
	}
	before, _ := os.ReadFile(output)
	stdout.Reset()
	stderr.Reset()
	if code := run(args, &stdout, &stderr); code == 0 {
		t.Fatal("existing output overwritten")
	}
	after, _ := os.ReadFile(output)
	if !bytes.Equal(before, after) {
		t.Fatal("existing file changed")
	}
	args[3] = "owner_other"
	args[len(args)-1] = filepath.Join(t.TempDir(), "foreign.zip")
	if code := run(args, &stdout, &stderr); code == 0 {
		t.Fatal("foreign export allowed")
	}
}
func TestCLIRequiresExplicitScopeAndDiagnosticForLiveCapture(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--evidence-dir", "unused"}, &stdout, &stderr); code != 2 {
		t.Fatalf("missing arguments=%d", code)
	}
	root := filepath.Join(t.TempDir(), "capture")
	r := executionevidence.New(root, executionevidence.Options{})
	s := executionevidence.Scope{OwnerID: "o", SessionID: "s", RunID: "r"}
	if err := r.Begin(t.Context(), s, map[string]string{"question": "pending"}); err != nil {
		t.Fatal(err)
	}
	args := []string{"--evidence-dir", root, "--owner", "o", "--session", "s", "--run", "r", "--output", filepath.Join(t.TempDir(), "pending.zip")}
	if code := run(args, &stdout, &stderr); code != 1 {
		t.Fatalf("live strict=%d", code)
	}
	args = append(args, "--diagnostic")
	if code := run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("diagnostic=%d: %s", code, stderr.String())
	}
}
