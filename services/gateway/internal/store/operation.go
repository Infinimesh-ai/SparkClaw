package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type StoreErrorCode string

const (
	StoreErrorNotFound       StoreErrorCode = "not_found"
	StoreErrorConflict       StoreErrorCode = "conflict"
	StoreErrorInvalid        StoreErrorCode = "invalid"
	StoreErrorCanceled       StoreErrorCode = "canceled"
	StoreErrorTimeout        StoreErrorCode = "timeout"
	StoreErrorUnavailable    StoreErrorCode = "unavailable"
	StoreErrorDurability     StoreErrorCode = "durability_failed"
	StoreErrorUnknownOutcome StoreErrorCode = "unknown_outcome"
	StoreErrorCorrupt        StoreErrorCode = "corrupt"
	StoreErrorInternal       StoreErrorCode = "internal"
)

type StoreOperation string

const (
	OperationISCPOnboardingSave StoreOperation = "iscp_onboarding.save"
	OperationISCPOnboardingGet  StoreOperation = "iscp_onboarding.get"
	OperationISCPOnboardingList StoreOperation = "iscp_onboarding.list"
)

type StoreError struct {
	Code      StoreErrorCode
	Operation StoreOperation
	Err       error
}

func (e *StoreError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return fmt.Sprintf("store %s failed: %s", e.Operation, e.Code)
	}
	return fmt.Sprintf("store %s failed: %s: %v", e.Operation, e.Code, e.Err)
}

func (e *StoreError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func StoreErrorCodeOf(err error) StoreErrorCode {
	var storeError *StoreError
	if errors.As(err, &storeError) {
		return storeError.Code
	}
	return ""
}

type OperationTimeouts struct {
	Read  time.Duration
	Write time.Duration
}

var defaultOperationTimeouts = OperationTimeouts{
	Read:  10 * time.Second,
	Write: 30 * time.Second,
}

type operationMode string
type operationTimeoutClass string

const (
	operationRead  operationMode = "read"
	operationWrite operationMode = "write"

	timeoutRead  operationTimeoutClass = "read"
	timeoutWrite operationTimeoutClass = "write"
)

type operationSpec struct {
	ID         StoreOperation
	Repository string
	Method     string
	Mode       operationMode
	Timeout    operationTimeoutClass
}

var operationSpecs = map[StoreOperation]operationSpec{
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

func normalizeOperationTimeouts(timeouts OperationTimeouts) OperationTimeouts {
	if timeouts.Read <= 0 {
		timeouts.Read = defaultOperationTimeouts.Read
	}
	if timeouts.Write <= 0 {
		timeouts.Write = defaultOperationTimeouts.Write
	}
	return timeouts
}

func operationContext(parent context.Context, operation StoreOperation, timeouts OperationTimeouts) (context.Context, context.CancelFunc) {
	spec := operationSpecs[operation]
	timeout := timeouts.Read
	if spec.Timeout == timeoutWrite {
		timeout = timeouts.Write
	}
	if deadline, exists := parent.Deadline(); exists && time.Until(deadline) <= timeout {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, timeout)
}

func contextStoreError(operation StoreOperation, ctx context.Context, cause error) error {
	code := StoreErrorInternal
	if errors.Is(cause, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		code = StoreErrorCanceled
	} else if errors.Is(cause, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		code = StoreErrorTimeout
	}
	return &StoreError{Code: code, Operation: operation, Err: cause}
}

func operationContextError(operation StoreOperation, ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return contextStoreError(operation, ctx, err)
	}
	return nil
}

func storeError(operation StoreOperation, code StoreErrorCode, cause error) error {
	return &StoreError{Code: code, Operation: operation, Err: cause}
}
