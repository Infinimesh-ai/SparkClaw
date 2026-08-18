package toolhub

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"testing/fstest"
)

func TestRunPythonPackageAdapterExecutesAndCleansUp(t *testing.T) {
	tempRoot := t.TempDir()
	t.Setenv("TMPDIR", tempRoot)
	packageFS := fstest.MapFS{
		"adapter/__init__.py": {Data: []byte("")},
		"adapter/__main__.py": {Data: []byte("import json, sys\nrequest = json.load(sys.stdin)\nprint(json.dumps({'status': 'ok', 'value': request.get('value')}))\n")},
	}

	result, err := runPythonPackageAdapter(context.Background(), packageFS, "adapter", "adapter", map[string]any{"value": "expected"})
	if err != nil {
		t.Fatalf("run embedded python package: %v", err)
	}
	if result["status"] != "ok" || result["value"] != "expected" {
		t.Fatalf("unexpected adapter result: %#v", result)
	}
	entries, err := os.ReadDir(tempRoot)
	if err != nil {
		t.Fatalf("read temporary root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("python package directory was not cleaned up: %#v", entries)
	}
}

func TestPptxSlidePythonUnitTests(t *testing.T) {
	cmd := exec.Command(documentPythonBinary(), "-m", "unittest", "discover", "-s", "scripts/pptx_slide/tests", "-t", "scripts")
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run PPTX slide Python unit tests: %v\n%s", err, output)
	}
}
