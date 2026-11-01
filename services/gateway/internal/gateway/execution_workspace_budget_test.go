package gateway

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
)

func TestWorkspaceCopyBudgetCoversInputsCollectionAndFailureRelease(t *testing.T) {
	budget := execution.NewBudget(32)
	if err := budget.Reserve(25); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	workspace := newExecutionWorkspaceBudget(root, budget)
	copy := filepath.Join(root, "copy.md")
	if err := workspace.reserve(copy, 8); !errors.Is(err, execution.ErrCapacity) {
		t.Fatalf("copy exceeded remaining task budget: %v", err)
	}
	if err := workspace.reserve(copy, 6); err != nil {
		t.Fatal(err)
	}
	if err := workspace.reserve(copy, 6); err != nil {
		t.Fatalf("collection double charged copy: %v", err)
	}
	if err := workspace.reserve(filepath.Join(root, "other.md"), 2); !errors.Is(err, execution.ErrCapacity) {
		t.Fatalf("concurrent output escaped budget: %v", err)
	}
	workspace.release(copy)
	workspace.release(copy)
	if err := workspace.reserve(filepath.Join(root, "other.md"), 7); err != nil {
		t.Fatalf("failed copy did not release reservation: %v", err)
	}
	if err := workspace.reserve(filepath.Join(root, "..", "outside.md"), 0); !errors.Is(err, execution.ErrConflict) {
		t.Fatalf("outside output admitted: %v", err)
	}
}
