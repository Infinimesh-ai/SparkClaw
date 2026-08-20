package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestISCPOnboardingOperationSpecsAreFiniteAndComplete(t *testing.T) {
	want := map[StoreOperation]operationSpec{
		OperationISCPOnboardingSave: {
			ID: OperationISCPOnboardingSave, Repository: "ISCPOnboardingRepository",
			Method: "SaveISCPOnboarding", Mode: operationWrite, Timeout: timeoutWrite,
		},
		OperationISCPOnboardingGet: {
			ID: OperationISCPOnboardingGet, Repository: "ISCPOnboardingRepository",
			Method: "GetISCPOnboarding", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationISCPOnboardingList: {
			ID: OperationISCPOnboardingList, Repository: "ISCPOnboardingRepository",
			Method: "ListISCPOnboardings", Mode: operationRead, Timeout: timeoutRead,
		},
	}
	if len(operationSpecs) != len(want) {
		t.Fatalf("operation spec count = %d, want %d", len(operationSpecs), len(want))
	}
	methods := map[string]struct{}{}
	for id, expected := range want {
		got, exists := operationSpecs[id]
		if !exists || got != expected {
			t.Errorf("operation %s = %#v, want %#v", id, got, expected)
		}
		if _, duplicate := methods[got.Method]; duplicate {
			t.Errorf("pilot method %s has duplicate operation specs", got.Method)
		}
		methods[got.Method] = struct{}{}
		if got.Timeout != timeoutRead && got.Timeout != timeoutWrite {
			t.Errorf("operation %s has unknown timeout class %q", id, got.Timeout)
		}
	}
}

func TestOperationContextUsesEarlierDeadlineAndTypedErrors(t *testing.T) {
	caller, cancelCaller := context.WithTimeout(context.Background(), time.Second)
	defer cancelCaller()
	effective, cancelEffective := operationContext(caller, OperationISCPOnboardingGet, OperationTimeouts{Read: time.Minute, Write: time.Minute})
	defer cancelEffective()
	callerDeadline, _ := caller.Deadline()
	effectiveDeadline, _ := effective.Deadline()
	if !effectiveDeadline.Equal(callerDeadline) {
		t.Fatalf("effective deadline = %s, want caller deadline %s", effectiveDeadline, callerDeadline)
	}
	fallbackStart := time.Now()
	fallback, cancelFallback := operationContext(context.Background(), OperationISCPOnboardingSave, OperationTimeouts{Read: time.Second, Write: 2 * time.Second})
	defer cancelFallback()
	fallbackDeadline, exists := fallback.Deadline()
	if !exists || fallbackDeadline.Before(fallbackStart.Add(1900*time.Millisecond)) || fallbackDeadline.After(fallbackStart.Add(2100*time.Millisecond)) {
		t.Fatalf("write fallback deadline = %s", fallbackDeadline)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	err := operationContextError(OperationISCPOnboardingGet, canceled)
	if StoreErrorCodeOf(err) != StoreErrorCanceled || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled classification = %v code=%q", err, StoreErrorCodeOf(err))
	}
	typed := &StoreError{Code: StoreErrorConflict, Operation: OperationISCPOnboardingSave, Err: ErrISCPOnboardingConflict}
	if !errors.Is(typed, ErrISCPOnboardingConflict) || StoreErrorCodeOf(typed) != StoreErrorConflict {
		t.Fatalf("StoreError did not preserve cause/code: %v", typed)
	}
}
