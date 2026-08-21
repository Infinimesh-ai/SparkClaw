package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMigratedOperationSpecsAreFiniteAndComplete(t *testing.T) {
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
		OperationOwnerProfileGet: {
			ID: OperationOwnerProfileGet, Repository: "OwnerRepository",
			Method: "GetOwnerProfile", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationOwnerProfileUpdate: {
			ID: OperationOwnerProfileUpdate, Repository: "OwnerRepository",
			Method: "UpdateOwnerProfile", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationOwnerProfileGetByID: {
			ID: OperationOwnerProfileGetByID, Repository: "OwnerRepository",
			Method: "GetOwnerProfileByID", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationOwnerProfileSave: {
			ID: OperationOwnerProfileSave, Repository: "OwnerRepository",
			Method: "SaveOwnerProfile", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationOwnerProfileList: {
			ID: OperationOwnerProfileList, Repository: "OwnerRepository",
			Method: "ListOwnerProfiles", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationOwnerProfileFindExternalRef: {
			ID: OperationOwnerProfileFindExternalRef, Repository: "OwnerRepository",
			Method: "FindOwnerProfileByExternalRef", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationClientGet: {
			ID: OperationClientGet, Repository: "ClientRepository",
			Method: "GetClient", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationClientList: {
			ID: OperationClientList, Repository: "ClientRepository",
			Method: "ListClients", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationClientRevoke: {
			ID: OperationClientRevoke, Repository: "ClientRepository",
			Method: "RevokeClient", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationClientFindTokenHash: {
			ID: OperationClientFindTokenHash, Repository: "ClientRepository",
			Method: "FindClientByTokenHash", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationClientTouch: {
			ID: OperationClientTouch, Repository: "ClientRepository",
			Method: "TouchClient", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationPairingCodeSave: {
			ID: OperationPairingCodeSave, Repository: "ClientRepository",
			Method: "SavePairingCode", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationPairingCodeGet: {
			ID: OperationPairingCodeGet, Repository: "ClientRepository",
			Method: "GetPairingCode", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationPairingCodeClaim: {
			ID: OperationPairingCodeClaim, Repository: "ClientRepository",
			Method: "ClaimPairingCode", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationCredentialSecretSave: {
			ID: OperationCredentialSecretSave, Repository: "CredentialRepository",
			Method: "SaveCredentialSecret", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationCredentialSecretGet: {
			ID: OperationCredentialSecretGet, Repository: "CredentialRepository",
			Method: "GetCredentialSecret", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationCredentialSecretDelete: {
			ID: OperationCredentialSecretDelete, Repository: "CredentialRepository",
			Method: "DeleteCredentialSecret", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationConnectorSettingGet: {
			ID: OperationConnectorSettingGet, Repository: "ConnectorRepository",
			Method: "GetConnectorSetting", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationConnectorSettingList: {
			ID: OperationConnectorSettingList, Repository: "ConnectorRepository",
			Method: "ListConnectorSettings", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationConnectorSettingListAll: {
			ID: OperationConnectorSettingListAll, Repository: "ConnectorRepository",
			Method: "ListAllConnectorSettings", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationConnectorSettingUpdate: {
			ID: OperationConnectorSettingUpdate, Repository: "ConnectorRepository",
			Method: "UpdateConnectorSetting", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationNotificationBindingCreate: {
			ID: OperationNotificationBindingCreate, Repository: "ConnectorRepository",
			Method: "CreateNotificationBinding", Mode: operationWrite, Timeout: timeoutTransaction,
		},
		OperationNotificationBindingGet: {
			ID: OperationNotificationBindingGet, Repository: "ConnectorRepository",
			Method: "GetNotificationBinding", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationNotificationBindingList: {
			ID: OperationNotificationBindingList, Repository: "ConnectorRepository",
			Method: "ListNotificationBindings", Mode: operationRead, Timeout: timeoutRead,
		},
		OperationNotificationBindingUpdate: {
			ID: OperationNotificationBindingUpdate, Repository: "ConnectorRepository",
			Method: "UpdateNotificationBinding", Mode: operationWrite, Timeout: timeoutTransaction,
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
		if got.Timeout != timeoutRead && got.Timeout != timeoutWrite && got.Timeout != timeoutTransaction {
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
