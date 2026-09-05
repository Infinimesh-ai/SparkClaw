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
	if !status.Configured || status.State != StateReady || status.CredentialGeneration <= 0 ||
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
	first, err := service.SaveToken(t.Context(), []byte("first-extension-token"))
	if err != nil {
		t.Fatal(err)
	}
	controller.err = newError(CodeExtensionRejected, false, errors.New("private extension detail"))
	status, err := service.SaveToken(t.Context(), []byte("rejected-extension-token"))
	if ErrorCode(err) != CodeExtensionRejected || status.CredentialGeneration != first.CredentialGeneration || !status.Configured || status.State != StateNeedsAttention {
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
	saved, err := service.SaveToken(t.Context(), []byte("configured-extension-token"))
	if err != nil {
		t.Fatal(err)
	}
	controller.result = validationResult(11, 2, 1)
	checked, err := service.Check(t.Context())
	if err != nil || checked.CredentialGeneration != saved.CredentialGeneration || checked.ControllerGeneration != 11 || checked.SessionGeneration != 2 {
		t.Fatalf("check status=%#v err=%v", checked, err)
	}
	removed, err := service.Remove(t.Context())
	if err != nil || removed.Configured || removed.State != StateNotConfigured || removed.CredentialGeneration != saved.CredentialGeneration+1 || !removed.LastValidatedAt.IsZero() {
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

func TestServiceRunsScriptsOnlyForTheCurrentCredentialGeneration(t *testing.T) {
	repository := store.NewMemoryStore()
	key := strings.Repeat("s", 32)
	vault := credential.New(repository, credential.Options{Key: key})
	controller := &fakeControllerClient{result: validationResult(10, 1, 1)}
	service := New(vault, controller, "default")
	service.Initialize(t.Context())
	saved, err := service.SaveToken(t.Context(), []byte("configured-extension-token"))
	if err != nil {
		t.Fatal(err)
	}
	controller.scriptResult = ScriptExecutionResult{
		SchemaVersion: 1, State: "completed", ProfileID: "default", Lane: "cli",
		Provider: "gmail", Operation: "probe", ScriptID: "gmail.login_probe", Revision: 1,
		SourceChecksum: `sha256:` + strings.Repeat("a", 64), CredentialGeneration: saved.CredentialGeneration,
		ControllerGeneration: 20, SessionGeneration: 3,
		Result: json.RawMessage(`{"schema_version":1,"status":"ready","provider":"gmail"}`),
	}
	result, err := service.RunScript(t.Context(), RunScriptRequest{
		TaskID: "email-probe", CredentialGeneration: saved.CredentialGeneration, Provider: "gmail", Operation: "probe",
		ScriptID: "gmail.login_probe", Revision: 1, Input: map[string]any{"schema_version": 1},
	})
	if err != nil || result.State != "completed" || controller.scriptCalls != 1 ||
		controller.lastScript.ProfileID != "default" || string(controller.scriptToken) != "configured-extension-token" {
		t.Fatalf("script result=%#v err=%v controller=%#v", result, err, controller)
	}
	zero(controller.scriptToken)
	controller.scriptToken = nil

	_, err = service.RunScript(t.Context(), RunScriptRequest{
		TaskID: "email-probe", CredentialGeneration: saved.CredentialGeneration + 1, Provider: "gmail", Operation: "probe",
		ScriptID: "gmail.login_probe", Revision: 1, Input: map[string]any{"schema_version": 1},
	})
	if ErrorCode(err) != CodeCredentialStale || controller.scriptCalls != 1 {
		t.Fatalf("stale script err=%v code=%q calls=%d", err, ErrorCode(err), controller.scriptCalls)
	}

	restartedVault := credential.New(repository, credential.Options{Key: key})
	restartedController := &fakeControllerClient{result: validationResult(11, 2, 1)}
	restartedService := New(restartedVault, restartedController, "default")
	restartedService.Initialize(t.Context())
	if restartedService.Status(t.Context()).CredentialGeneration != saved.CredentialGeneration {
		t.Fatalf("credential generation changed across restart: old=%d new=%d", saved.CredentialGeneration, restartedService.Status(t.Context()).CredentialGeneration)
	}
	replaced, err := restartedService.SaveToken(t.Context(), []byte("replacement-extension-token"))
	if err != nil {
		t.Fatal(err)
	}
	if replaced.CredentialGeneration == saved.CredentialGeneration {
		t.Fatalf("credential generation did not change after replacement: %d", replaced.CredentialGeneration)
	}
	_, err = service.RunScript(t.Context(), RunScriptRequest{
		TaskID: "email-probe", CredentialGeneration: saved.CredentialGeneration, Provider: "gmail", Operation: "probe",
		ScriptID: "gmail.login_probe", Revision: 1, Input: map[string]any{"schema_version": 1},
	})
	if ErrorCode(err) != CodeCredentialStale || controller.scriptCalls != 1 {
		t.Fatalf("persisted stale script err=%v code=%q calls=%d", err, ErrorCode(err), controller.scriptCalls)
	}
}

func TestServiceOpensOnlyRegisteredProviderLoginRequests(t *testing.T) {
	controller := &fakeControllerClient{loginResult: OpenProviderLoginResult{
		SchemaVersion: 1, State: "opened", ProfileID: "default", Provider: "outlook",
		ControllerGeneration: 30, SessionGeneration: 4,
	}}
	service := New(nil, controller, "default")
	result, err := service.OpenProviderLogin(t.Context(), OpenProviderLoginRequest{
		TaskID: "email-login", Provider: "outlook",
	})
	if err != nil || result.State != "opened" || controller.loginCalls != 1 ||
		controller.lastLogin.ProfileID != "default" {
		t.Fatalf("login result=%#v err=%v controller=%#v", result, err, controller)
	}
}

func validationResult(controller, session, page int64) ValidationResult {
	return ValidationResult{
		SchemaVersion: 1, State: "ready", ProfileID: "default",
		ControllerGeneration: controller, SessionGeneration: session, PageGeneration: page,
		Versions: Versions{Client: "playwright-mcp", ClientVersion: "0.0.80", PlaywrightVersion: "1.63.0-alpha-2026-08-31", BrowserChannel: "chromium"},
	}
}

type fakeControllerClient struct {
	result       ValidationResult
	err          error
	calls        int
	acquireErr   error
	executeErr   error
	releaseErr   error
	acquireCalls int
	executeCalls int
	releaseCalls int
	lastAcquire  AcquireRequest
	lastExecute  ExecuteRequest
	lease        SessionLease
	scriptResult ScriptExecutionResult
	scriptErr    error
	scriptCalls  int
	lastScript   RunScriptRequest
	scriptToken  []byte
	loginResult  OpenProviderLoginResult
	loginErr     error
	loginCalls   int
	lastLogin    OpenProviderLoginRequest
}

func (f *fakeControllerClient) ValidateToken(_ context.Context, profileID string, token []byte) (ValidationResult, error) {
	f.calls++
	if profileID != "default" || len(token) == 0 {
		return ValidationResult{}, errors.New("invalid test request")
	}
	return f.result, f.err
}

func (f *fakeControllerClient) Acquire(_ context.Context, input AcquireRequest, token []byte) (SessionLease, error) {
	f.acquireCalls++
	f.lastAcquire = input
	if len(token) == 0 {
		return SessionLease{}, errors.New("missing test token")
	}
	if f.acquireErr != nil {
		return SessionLease{}, f.acquireErr
	}
	if f.lease.SchemaVersion == 0 {
		f.lease = SessionLease{
			SchemaVersion: 1, State: "acquired", ProfileID: input.ProfileID, Lane: "mcp",
			SessionID: "session-test", CredentialGeneration: input.CredentialGeneration,
			ControllerGeneration: 50, SessionGeneration: 2, PageGeneration: 1,
			ExpiresAt: time.Now().UTC().Add(time.Minute),
		}
	}
	return f.lease, nil
}

func (f *fakeControllerClient) Execute(_ context.Context, input ExecuteRequest) (ExecutionResult, error) {
	f.executeCalls++
	f.lastExecute = input
	if f.executeErr != nil {
		return ExecutionResult{}, f.executeErr
	}
	result, _ := json.Marshal(map[string]any{"operation": input.Operation})
	return ExecutionResult{
		SchemaVersion: 1, State: "completed", ProfileID: input.Lease.ProfileID, Lane: input.Lease.Lane,
		SessionID: input.Lease.SessionID, CredentialGeneration: input.Lease.CredentialGeneration,
		ControllerGeneration: input.Lease.ControllerGeneration, SessionGeneration: input.Lease.SessionGeneration,
		PageGeneration: input.Lease.PageGeneration + 1, Operation: input.Operation, Result: result,
	}, nil
}

func (f *fakeControllerClient) Release(_ context.Context, input ReleaseRequest) (ReleaseResult, error) {
	f.releaseCalls++
	if f.releaseErr != nil {
		return ReleaseResult{}, f.releaseErr
	}
	return ReleaseResult{
		SchemaVersion: 1, State: "released", ProfileID: input.ProfileID,
		ControllerGeneration: input.ControllerGeneration, SessionGeneration: input.SessionGeneration,
	}, nil
}

func (f *fakeControllerClient) RunScript(_ context.Context, input RunScriptRequest, token []byte) (ScriptExecutionResult, error) {
	f.scriptCalls++
	f.lastScript = input
	f.scriptToken = append([]byte(nil), token...)
	return f.scriptResult, f.scriptErr
}

func (f *fakeControllerClient) OpenProviderLogin(_ context.Context, input OpenProviderLoginRequest) (OpenProviderLoginResult, error) {
	f.loginCalls++
	f.lastLogin = input
	return f.loginResult, f.loginErr
}
