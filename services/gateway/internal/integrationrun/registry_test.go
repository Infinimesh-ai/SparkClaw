package integrationrun

import (
	"context"
	"errors"
	"testing"
)

func TestRegistryCancelsOnlyRunsUsingSelectedGeneration(t *testing.T) {
	registry := New()
	oldCtx, endOld := registry.Begin(t.Context(), "old")
	defer endOld(false)
	newCtx, endNew := registry.Begin(t.Context(), "new")
	defer endNew(false)
	if err := registry.Use("old", "info", 2); err != nil {
		t.Fatal(err)
	}
	if err := registry.Use("new", "info", 3); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("changed")
	if got := registry.CancelGeneration("info", 2, cause); got != 1 {
		t.Fatalf("cancelled=%d", got)
	}
	if !errors.Is(context.Cause(oldCtx), cause) {
		t.Fatalf("old cause=%v", context.Cause(oldCtx))
	}
	if context.Cause(newCtx) != nil {
		t.Fatalf("new run was cancelled: %v", context.Cause(newCtx))
	}
}

func TestRegistryRejectsGenerationMixing(t *testing.T) {
	registry := New()
	ctx, end := registry.Begin(t.Context(), "run")
	defer end(false)
	if err := registry.Use("run", "info", 1); err != nil {
		t.Fatal(err)
	}
	if err := registry.Use("run", "info", 2); !errors.Is(err, ErrGenerationChanged) {
		t.Fatalf("generation change error=%v", err)
	}
	if !errors.Is(context.Cause(ctx), ErrGenerationChanged) {
		t.Fatalf("run cause=%v", context.Cause(ctx))
	}
}

func TestRegistrySuspendedRunCannotCrossCredentialGenerations(t *testing.T) {
	registry := New()
	_, suspend := registry.Begin(t.Context(), "run")
	if err := registry.Use("run", "info", 1); err != nil {
		t.Fatal(err)
	}
	suspend(true)

	changed := errors.New("info credentials changed")
	if got := registry.CancelGeneration("info", 1, changed); got != 1 {
		t.Fatalf("cancelled=%d", got)
	}
	resumedCtx, finish := registry.Begin(t.Context(), "run")
	defer finish(false)
	if !errors.Is(context.Cause(resumedCtx), changed) {
		t.Fatalf("resumed cause=%v", context.Cause(resumedCtx))
	}
	if err := registry.Use("run", "info", 2); !errors.Is(err, changed) {
		t.Fatalf("cross-generation use error=%v", err)
	}
}
