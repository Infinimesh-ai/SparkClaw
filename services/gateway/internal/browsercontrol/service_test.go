package browsercontrol

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestServiceValidatesBeforePersistAndProjectsOnlyRedactedState(t *testing.T) {
	vault := credential.New(store.NewMemoryStore(), credential.Options{Key: strings.Repeat("b", 32)})
	controller := &fakeControllerClient{result: validationResult(41, 7, 1)}
	service := New(vault, controller, "default")
	service.now = func() time.Time { return time.Date(2026, 9, 4, 9, 30, 0, 0, time.UTC) }
	service.Initialize(t.Context())

	secret := []byte("extension-token-secret-value")
	status, err := service.SaveToken(t.Context(), secret)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Configured || status.State != StateReady || status.CredentialGeneration != 1 ||
		status.ControllerGeneration != 41 || status.SessionGeneration != 7 || status.PageGeneration != 1 {
		t.Fatalf("unexpected ready status: %#v", status)
	}
	if !status.LastValidatedAt.Equal(service.now()) || status.Versions.Client != "playwright-mcp" {
		t.Fatalf("validation evidence missing: %#v", status)
	}
	opened, found, err := vault.OpenBinding(t.Context(), credentialBinding, credentialKind)
	if err != nil || !found || string(opened) != string(secret) {
		t.Fatalf("saved binding = %q found=%v err=%v", opened, found, err)
	}
	zero(opened)
	projected, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(projected), string(secret)) || strings.Contains(string(projected), credentialBinding) {
		t.Fatalf("public status disclosed credential material: %s", projected)
	}
}

func TestServiceFailedReplacementRetainsExistingCredential(t *testing.T) {
	vault := credential.New(store.NewMemoryStore(), credential.Options{Key: strings.Repeat("c", 32)})
	controller := &fakeControllerClient{result: validationResult(1, 1, 1)}
	service := New(vault, controller, "default")
	service.Initialize(t.Context())
	if _, err := service.SaveToken(t.Context(), []byte("first-extension-token")); err != nil {
		t.Fatal(err)
	}
	controller.err = newError(CodeExtensionRejected, false, errors.New("private extension detail"))
	status, err := service.SaveToken(t.Context(), []byte("rejected-extension-token"))
	if ErrorCode(err) != CodeExtensionRejected || status.CredentialGeneration != 1 || !status.Configured || status.State != StateNeedsAttention {
		t.Fatalf("failed replacement status=%#v err=%v", status, err)
	}
	opened, found, openErr := vault.OpenBinding(t.Context(), credentialBinding, credentialKind)
	if openErr != nil || !found || string(opened) != "first-extension-token" {
		t.Fatalf("existing credential was replaced: %q found=%v err=%v", opened, found, openErr)
	}
	zero(opened)
}

func TestServiceProjectsExtensionUnavailableAsRetryableState(t *testing.T) {
	vault := credential.New(store.NewMemoryStore(), credential.Options{Key: strings.Repeat("u", 32)})
	controller := &fakeControllerClient{
		err: newError(CodeExtensionUnavailable, true, errors.New("private extension detail")),
	}
	service := New(vault, controller, "default")
	service.Initialize(t.Context())

	status, err := service.SaveToken(t.Context(), []byte("unavailable-extension-token"))
	if ErrorCode(err) != CodeExtensionUnavailable || !ErrorRetryable(err) || status.Configured ||
		status.State != StateTemporarilyUnavailable || status.ErrorCode != CodeExtensionUnavailable {
		t.Fatalf("status=%#v err=%v", status, err)
	}
}

func TestServiceCheckAndRemoveAdvanceCredentialLifecycle(t *testing.T) {
	vault := credential.New(store.NewMemoryStore(), credential.Options{Key: strings.Repeat("d", 32)})
	controller := &fakeControllerClient{result: validationResult(10, 1, 1)}
	service := New(vault, controller, "default")
	service.Initialize(t.Context())
	if _, err := service.Check(t.Context()); ErrorCode(err) != CodeNotConfigured {
		t.Fatalf("unconfigured check error = %v", err)
	}
	if _, err := service.SaveToken(t.Context(), []byte("configured-extension-token")); err != nil {
		t.Fatal(err)
	}
	controller.result = validationResult(11, 2, 1)
	checked, err := service.Check(t.Context())
	if err != nil || checked.CredentialGeneration != 1 || checked.ControllerGeneration != 11 || checked.SessionGeneration != 2 {
		t.Fatalf("check status=%#v err=%v", checked, err)
	}
	removed, err := service.Remove(t.Context())
	if err != nil || removed.Configured || removed.State != StateNotConfigured || removed.CredentialGeneration != 2 || !removed.LastValidatedAt.IsZero() {
		t.Fatalf("remove status=%#v err=%v", removed, err)
	}
	if _, found, err := vault.OpenBinding(t.Context(), credentialBinding, credentialKind); err != nil || found {
		t.Fatalf("credential survived removal: found=%v err=%v", found, err)
	}
}

func TestServiceRejectsMalformedTokenBeforeControllerCall(t *testing.T) {
	vault := credential.New(store.NewMemoryStore(), credential.Options{Key: strings.Repeat("e", 32)})
	controller := &fakeControllerClient{}
	service := New(vault, controller, "default")
	service.Initialize(t.Context())

	for _, token := range [][]byte{
		[]byte("short"),
		[]byte(" token-with-padding"),
		[]byte("token-with-newline\n"),
		append([]byte("valid-prefix-value"), 0xff),
	} {
		if _, err := service.SaveToken(t.Context(), token); ErrorCode(err) != CodeInvalidRequest {
			t.Fatalf("token %q error=%v", token, err)
		}
	}
	if controller.calls != 0 {
		t.Fatalf("invalid input reached controller %d times", controller.calls)
	}
}

func validationResult(controller, session, page int64) ValidationResult {
	return ValidationResult{
		SchemaVersion: 1, State: "ready", ProfileID: "default",
		ControllerGeneration: controller, SessionGeneration: session, PageGeneration: page,
		Versions: Versions{Client: "playwright-mcp", ClientVersion: "0.0.80", PlaywrightVersion: "1.63.0-alpha-2026-08-31", BrowserChannel: "chrome"},
	}
}

type fakeControllerClient struct {
	result ValidationResult
	err    error
	calls  int
}

func (f *fakeControllerClient) ValidateToken(_ context.Context, profileID string, token []byte) (ValidationResult, error) {
	f.calls++
	if profileID != "default" || len(token) == 0 {
		return ValidationResult{}, errors.New("invalid test request")
	}
	return f.result, f.err
}
