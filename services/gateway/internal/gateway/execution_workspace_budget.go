package gateway

import (
	"path/filepath"
	"strings"
	"sync"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
)

// Account each temporary output once across pre-write admission and collection.
// A failed copy explicitly releases its reservation after deleting the file.
type executionWorkspaceBudget struct {
	mu     sync.Mutex
	root   string
	budget *execution.Budget
	files  map[string]int
}

func newExecutionWorkspaceBudget(root string, budget *execution.Budget) *executionWorkspaceBudget {
	return &executionWorkspaceBudget{root: filepath.Clean(root), budget: budget, files: map[string]int{}}
}
func (b *executionWorkspaceBudget) reserve(path string, size int) error {
	path = filepath.Clean(path)
	rel, err := filepath.Rel(b.root, path)
	if err != nil || !filepath.IsAbs(path) || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || size < 0 {
		return execution.ErrConflict
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	previous := b.files[path]
	if size > previous {
		if err := b.budget.Reserve(size - previous); err != nil {
			return err
		}
	}
	b.files[path] = max(size, previous)
	return nil
}
func (b *executionWorkspaceBudget) release(path string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	path = filepath.Clean(path)
	if size, exists := b.files[path]; exists {
		delete(b.files, path)
		b.budget.Release(size)
	}
}
