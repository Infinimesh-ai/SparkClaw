package gateway

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestR3StartupRemovesOnlyOwnedDeploymentMemoryWorkspaces(t *testing.T) {
	root := filepath.Join(t.TempDir(), "r3")
	scratch, err := r3MemoryWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(scratch, "synthetic-private-input"), []byte("orphaned fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	other, err := r3MemoryWorkspace(root + "other")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(other)
	if err = r3MemorySweep(root); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(scratch); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("restart left temporary content", err)
	}
	if _, err = os.Stat(other); err != nil {
		t.Fatal("other deployment workspace removed", err)
	}
}
