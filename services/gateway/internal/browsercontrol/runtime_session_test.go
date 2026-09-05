package browsercontrol

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestRuntimeSessionOpensVaultCredentialForBoundedAcquireAndTracksGenerations(t *testing.T) {
	vault := credential.New(store.NewMemoryStore(), credential.Options{Key: strings.Repeat("r", 32)})
	controller := &fakeControllerClient{result: validationResult(41, 1, 1)}
	service := New(vault, controller, "default")
	service.Initialize(t.Context())
	saved, err := service.SaveToken(t.Context(), []byte("runtime-extension-token"))
	if err != nil {
		t.Fatal(err)
	}

	runtime, err := service.AcquireSession(t.Context(), "task-runtime", 250*time.Millisecond, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if controller.lastAcquire.ProfileID != "default" || controller.lastAcquire.TaskID != "task-runtime" ||
		controller.lastAcquire.CredentialGeneration != saved.CredentialGeneration || controller.lastAcquire.WaitTimeoutMS != 250 ||
		controller.lastAcquire.SessionTTLMS != 60_000 {
		t.Fatalf("acquire request = %#v", controller.lastAcquire)
	}
	output, err := runtime.Execute(t.Context(), "tabs.list", map[string]any{})
	if err != nil || output["operation"] != "tabs.list" {
		t.Fatalf("execute output=%#v err=%v", output, err)
	}
	if runtime.Lease().PageGeneration != 2 || service.Status(t.Context()).PageGeneration != 2 {
		t.Fatalf("runtime generation was not advanced: lease=%#v status=%#v", runtime.Lease(), service.Status(t.Context()))
	}
	if err := runtime.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
	if controller.releaseCalls != 1 || service.Status(t.Context()).SessionGeneration != 0 {
		t.Fatalf("release calls=%d status=%#v", controller.releaseCalls, service.Status(t.Context()))
	}
}

func TestRuntimeSessionReleasesOnCancellationAndCredentialReplacement(t *testing.T) {
	vault := credential.New(store.NewMemoryStore(), credential.Options{Key: strings.Repeat("s", 32)})
	controller := &fakeControllerClient{result: validationResult(41, 1, 1)}
	service := New(vault, controller, "default")
	service.Initialize(t.Context())
	first, err := service.SaveToken(t.Context(), []byte("runtime-extension-token"))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	if _, err := service.AcquireSession(ctx, "task-cancel", 0, time.Minute); err != nil {
		t.Fatal(err)
	}
	cancel()
	waitForControllerCalls(t, func() bool { return service.Status(t.Context()).SessionGeneration == 0 })

	if _, err := service.AcquireSession(t.Context(), "task-rotate", 0, time.Minute); err != nil {
		t.Fatal(err)
	}
	controller.result = validationResult(42, 3, 1)
	status, err := service.SaveToken(t.Context(), []byte("replacement-extension-token"))
	if err != nil {
		t.Fatal(err)
	}
	if controller.releaseCalls != 2 || status.CredentialGeneration == first.CredentialGeneration || status.ControllerGeneration != 42 {
		t.Fatalf("replacement status=%#v release calls=%d", status, controller.releaseCalls)
	}
}

func TestRuntimeSessionRejectsCredentialReplacedByAnotherService(t *testing.T) {
	repository := store.NewMemoryStore()
	key := strings.Repeat("v", 32)
	controller := &fakeControllerClient{result: validationResult(41, 1, 1)}
	service := New(credential.New(repository, credential.Options{Key: key}), controller, "default")
	service.Initialize(t.Context())
	first, err := service.SaveToken(t.Context(), []byte("runtime-extension-token"))
	if err != nil {
		t.Fatal(err)
	}

	replacementController := &fakeControllerClient{result: validationResult(42, 2, 1)}
	replacementService := New(credential.New(repository, credential.Options{Key: key}), replacementController, "default")
	replacementService.Initialize(t.Context())
	if replacementService.Status(t.Context()).CredentialGeneration != first.CredentialGeneration {
		t.Fatalf("credential generation changed across restart: old=%d new=%d", first.CredentialGeneration, replacementService.Status(t.Context()).CredentialGeneration)
	}
	second, err := replacementService.SaveToken(t.Context(), []byte("replacement-extension-token"))
	if err != nil {
		t.Fatal(err)
	}
	if second.CredentialGeneration == first.CredentialGeneration {
		t.Fatalf("credential generation did not change after replacement: %d", second.CredentialGeneration)
	}

	if _, err := service.AcquireSession(t.Context(), "task-stale", 0, time.Minute); ErrorCode(err) != CodeCredentialStale {
		t.Fatalf("stale runtime acquire error=%v code=%q", err, ErrorCode(err))
	}
	if controller.acquireCalls != 0 {
		t.Fatalf("stale runtime acquire reached controller %d times", controller.acquireCalls)
	}
}

func TestRuntimeSessionRejectsASecondTaskWithoutInterruptingTheActiveTask(t *testing.T) {
	vault := credential.New(store.NewMemoryStore(), credential.Options{Key: strings.Repeat("t", 32)})
	controller := &fakeControllerClient{result: validationResult(41, 1, 1)}
	service := New(vault, controller, "default")
	service.Initialize(t.Context())
	if _, err := service.SaveToken(t.Context(), []byte("runtime-extension-token")); err != nil {
		t.Fatal(err)
	}
	runtime, err := service.AcquireSession(t.Context(), "task-active", 0, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AcquireSession(t.Context(), "task-conflict", 0, time.Minute); ErrorCode(err) != CodeBusy || !ErrorRetryable(err) {
		t.Fatalf("conflicting acquire error = %v", err)
	}
	if controller.releaseCalls != 0 {
		t.Fatalf("active task was interrupted: release calls=%d", controller.releaseCalls)
	}
	if _, err := runtime.Execute(t.Context(), "tabs.list", map[string]any{}); err != nil {
		t.Fatalf("active task stopped after conflict: %v", err)
	}
	if err := runtime.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func waitForControllerCalls(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for controller call")
		}
		time.Sleep(time.Millisecond)
	}
}
