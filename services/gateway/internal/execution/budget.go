package execution

import (
	"context"
	"encoding/json"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/artifact"

	"sync"
)

// Budget accounts content conservatively, including retained event copies and
// repeated state versions. Exhaustion fails writes; it never drops a fence.
type Budget struct {
	mu    sync.Mutex
	used  int
	limit int
}

func NewBudget(limit int) *Budget { return &Budget{limit: limit} }
func (b *Budget) Reserve(bytes int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if bytes < 0 || b.used+bytes > b.limit {
		return ErrCapacity
	}
	b.used += bytes
	return nil
}

// Release returns a reservation whose temporary output was removed. Content
// retained by stores/artifacts is never released through this operation.
func (b *Budget) Release(bytes int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if bytes >= 0 && bytes <= b.used {
		b.used -= bytes
	}
}
func (b *Budget) Admit(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return ErrCapacity
	}
	return b.Reserve(2 * len(raw))
}

type TemporaryArtifacts struct {
	artifact.Store
	Budget *Budget
}

func (s TemporaryArtifacts) Put(ctx context.Context, key, contentType string, raw []byte) (artifact.Object, error) {
	if err := s.Budget.Reserve(len(raw)); err != nil {
		return artifact.Object{}, err
	}
	return s.Store.Put(ctx, key, contentType, raw)
}
