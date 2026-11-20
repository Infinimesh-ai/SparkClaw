package gateway

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func requireLinuxExecutionWorkspace(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("installed execution requires Linux /dev/shm tmpfs")
	}
}

func TestExecutionStartupRemovesOnlyOwnedDeploymentMemoryWorkspaces(t *testing.T) {
	requireLinuxExecutionWorkspace(t)
	root := filepath.Join(t.TempDir(), "execution")
	scratch, err := executionMemoryWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(scratch, "synthetic-private-input"), []byte("orphaned fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	other, err := executionMemoryWorkspace(root + "other")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(other)
	if err = executionMemorySweep(root); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(scratch); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("restart left temporary content", err)
	}
	if _, err = os.Stat(other); err != nil {
		t.Fatal("other deployment workspace removed", err)
	}
}
