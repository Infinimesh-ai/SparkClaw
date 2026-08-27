package integrationrun

import (
	"context"
	"errors"
	"strings"
	"sync"
)

var ErrGenerationChanged = errors.New("integration credentials changed")

type runEntry struct {
	ctx          context.Context
	cancel       context.CancelCauseFunc
	dependencies map[string]uint64
	cause        error
}

// Registry binds integration generations to active agent runs so a committed
// credential switch can stop every run that already used the old generation.
type Registry struct {
	mu   sync.Mutex
	runs map[string]*runEntry
}

func New() *Registry {
	return &Registry{runs: map[string]*runEntry{}}
}

func (r *Registry) Begin(parent context.Context, runID string) (context.Context, func(bool)) {
	if r == nil || strings.TrimSpace(runID) == "" {
		return parent, func(bool) {}
	}
	ctx, cancel := context.WithCancelCause(parent)
	entry := &runEntry{ctx: ctx, cancel: cancel, dependencies: map[string]uint64{}}
	r.mu.Lock()
	if previous := r.runs[runID]; previous != nil {
		previous.cancel(context.Canceled)
		entry.dependencies = cloneDependencies(previous.dependencies)
		entry.cause = previous.cause
	}
	r.runs[runID] = entry
	if entry.cause != nil {
		cancel(entry.cause)
	}
	r.mu.Unlock()
	return ctx, func(suspend bool) {
		r.mu.Lock()
		if r.runs[runID] == entry {
			if suspend {
				entry.cancel(context.Canceled)
			} else {
				delete(r.runs, runID)
			}
		}
		r.mu.Unlock()
		cancel(nil)
	}
}

// Use records the generation used by a run. A run can never cross from one
// generation to another, including after an approval pause or resumed stage.
func (r *Registry) Use(runID, integrationID string, generation uint64) error {
	if r == nil || strings.TrimSpace(runID) == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.runs[runID]
	if entry == nil {
		return nil
	}
	if entry.cause != nil {
		return entry.cause
	}
	if cause := context.Cause(entry.ctx); cause != nil {
		return cause
	}
	if previous, ok := entry.dependencies[integrationID]; ok && previous != generation {
		entry.cause = ErrGenerationChanged
		entry.cancel(ErrGenerationChanged)
		return ErrGenerationChanged
	}
	entry.dependencies[integrationID] = generation
	return nil
}

func (r *Registry) CancelGeneration(integrationID string, generation uint64, cause error) int {
	if r == nil {
		return 0
	}
	if cause == nil {
		cause = ErrGenerationChanged
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cancelled := 0
	for _, entry := range r.runs {
		if used, ok := entry.dependencies[integrationID]; ok && used == generation && entry.cause == nil {
			entry.cause = cause
			entry.cancel(cause)
			cancelled++
		}
	}
	return cancelled
}

// Forget removes the dependency history for a terminal run.
func (r *Registry) Forget(runID string) {
	if r == nil || strings.TrimSpace(runID) == "" {
		return
	}
	r.mu.Lock()
	entry := r.runs[runID]
	delete(r.runs, runID)
	r.mu.Unlock()
	if entry != nil {
		entry.cancel(context.Canceled)
	}
}

func cloneDependencies(source map[string]uint64) map[string]uint64 {
	cloned := make(map[string]uint64, len(source))
	for integrationID, generation := range source {
		cloned[integrationID] = generation
	}
	return cloned
}
