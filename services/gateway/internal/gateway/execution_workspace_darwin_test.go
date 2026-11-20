package gateway

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExecutionMemoryWorkspaceRefusesDiskFallback(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	path, err := executionMemoryWorkspace(filepath.Join(tmp, "control"))
	if err == nil || path != "" {
		t.Fatalf("allocated an installed execution workspace without Linux tmpfs: %q, %v", path, err)
	}
	entries, err := os.ReadDir(tmp)
	if err != nil || len(entries) != 0 {
		t.Fatalf("disk workspace contents after refusal: %v, %v", entries, err)
	}
}
