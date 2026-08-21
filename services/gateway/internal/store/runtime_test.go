package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMemoryRuntimePublishesTypedRepositoriesAndMetrics(t *testing.T) {
	runtime, err := NewRuntime(context.Background(), RuntimeOptions{Backend: BackendMemory})
	if err != nil {
		t.Fatal(err)
	}
	if status := runtime.Status(); !status.Ready || status.State != RuntimeStateReady || status.Durable || status.Backend != BackendMemory {
		t.Fatalf("unexpected startup status: %#v", status)
	}
	if runtime.ISCPOnboardingRepository() == nil || runtime.OwnerRepository() == nil ||
		runtime.ClientRepository() == nil || runtime.CredentialRepository() == nil ||
		runtime.ConnectorRepository() == nil || runtime.SessionRepository() == nil ||
		runtime.ConversationRepository() == nil || runtime.RunRepository() == nil ||
		runtime.DocumentRepository() == nil || runtime.ApprovalRepository() == nil ||
		runtime.AuditRepository() == nil || runtime.EvaluationRepository() == nil ||
		runtime.ArtifactMetadataRepository() == nil || runtime.BrowserStateRepository() == nil ||
		runtime.MemoryRepository() == nil || runtime.ScheduleRepository() == nil ||
		runtime.PassiveNotificationRepository() == nil || runtime.DeliveryRecordRepository() == nil ||
		runtime.ExternalChatRepository() == nil || runtime.MCPRepository() == nil {
		t.Fatal("runtime omitted a typed repository accessor")
	}
	if _, err := runtime.SessionRepository().CreateSession(context.Background(), "supervised"); err != nil {
		t.Fatal(err)
	}
	metrics := runtime.Metrics()
	if len(metrics) != 1 || metrics[0].Operation != OperationSessionCreate || metrics[0].Outcome != "success" || metrics[0].Count != 1 {
		t.Fatalf("unexpected metrics: %#v", metrics)
	}
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("idempotent close: %v", err)
	}
	_, err = runtime.SessionRepository().CreateSession(context.Background(), "after-close")
	if !errors.Is(err, ErrRuntimeClosing) || StoreErrorCodeOf(err) != StoreErrorUnavailable {
		t.Fatalf("post-close operation = %v code=%q", err, StoreErrorCodeOf(err))
	}
}

func TestSupervisorCountsNestedOperationOnceAndUsesTypedOutcome(t *testing.T) {
	supervisor := newSupervisor(BackendFile, nil)
	supervisor.recordProbe(nil)
	timeouts := normalizeOperationTimeouts(OperationTimeouts{})
	timeouts.supervisor = supervisor
	outer, finishOuter := operationContext(context.Background(), OperationSessionGet, timeouts)
	inner, finishInner := operationContext(outer, OperationSessionGet, timeouts)
	_ = storeError(inner, OperationSessionGet, StoreErrorNotFound, errors.New("missing"))
	finishInner()
	finishOuter()
	metrics := supervisor.Metrics()
	if len(metrics) != 1 || metrics[0].Count != 1 || metrics[0].Outcome != string(StoreErrorNotFound) {
		t.Fatalf("nested operation metrics = %#v", metrics)
	}
}

func TestSupervisorHealthTransitionsRequireProbeRecovery(t *testing.T) {
	now := time.Date(2026, 8, 22, 1, 0, 0, 0, time.UTC)
	supervisor := newSupervisor(BackendFile, func() time.Time { return now })
	supervisor.recordProbe(nil)
	finish := func(operation StoreOperation, code StoreErrorCode) {
		span, admitted := supervisor.begin(operation)
		if !admitted {
			t.Fatal("operation was not admitted")
		}
		span.mark(code)
		span.finish(context.Background())
		now = now.Add(time.Second)
	}
	finish(OperationSessionGet, StoreErrorUnavailable)
	finish(OperationSessionGet, StoreErrorUnavailable)
	if status := supervisor.Status(); !status.Ready {
		t.Fatalf("degraded before threshold: %#v", status)
	}
	finish(OperationSessionGet, StoreErrorUnavailable)
	if status := supervisor.Status(); status.Ready || status.ReasonCode != string(StoreErrorUnavailable) {
		t.Fatalf("threshold did not degrade runtime: %#v", status)
	}
	finish(OperationOwnerProfileGet, "")
	if supervisor.Status().Ready {
		t.Fatal("unrelated success cleared degradation")
	}
	supervisor.recordProbe(errors.New("still unavailable"))
	if supervisor.Status().Ready {
		t.Fatal("failed probe cleared degradation")
	}
	supervisor.recordProbe(nil)
	if status := supervisor.Status(); !status.Ready || status.ReasonCode != "" || status.LastRecovered == nil {
		t.Fatalf("successful probe did not recover runtime: %#v", status)
	}
	finish(OperationRunSave, StoreErrorUnknownOutcome)
	if status := supervisor.Status(); status.Ready || status.ReasonCode != string(StoreErrorUnknownOutcome) {
		t.Fatalf("critical durable outcome did not degrade runtime: %#v", status)
	}
}

func TestMemorySupervisorTreatsIntentionalNonDurabilityAsHealthy(t *testing.T) {
	supervisor := newSupervisor(BackendMemory, nil)
	supervisor.recordProbe(nil)
	span, _ := supervisor.begin(OperationRunSave)
	span.mark(StoreErrorDurability)
	span.finish(context.Background())
	if status := supervisor.Status(); !status.Ready || status.Durable {
		t.Fatalf("memory durability classification changed readiness: %#v", status)
	}
	span, _ = supervisor.begin(OperationRunGet)
	span.mark(StoreErrorCorrupt)
	span.finish(context.Background())
	if status := supervisor.Status(); status.Ready || status.ReasonCode != string(StoreErrorCorrupt) {
		t.Fatalf("memory corruption did not fail its invariant: %#v", status)
	}
}

func TestFileRuntimeProbeDoesNotChangeSnapshot(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state", "sparkclaw.json")
	runtime, err := NewRuntime(context.Background(), RuntimeOptions{
		Backend: BackendFile,
		File:    FileStoreOptions{Path: statePath},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("startup probe created state snapshot: %v", err)
	}
	if _, err := runtime.SessionRepository().CreateSession(context.Background(), "probe-isolation"); err != nil {
		t.Fatal(err)
	}
	metrics := runtime.Metrics()
	if len(metrics) != 1 || metrics[0].Operation != OperationSessionCreate || metrics[0].Count != 1 {
		t.Fatalf("file delegation did not retain one outer operation span: %#v", metrics)
	}
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("file probe changed the state snapshot")
	}
	entries, err := os.ReadDir(filepath.Dir(statePath))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".sparkclaw-probe-") {
			t.Fatalf("file probe left temporary path %q", entry.Name())
		}
	}
}

func TestRuntimeCloseIsBoundedWithInflightOperation(t *testing.T) {
	runtime, err := NewRuntime(context.Background(), RuntimeOptions{Backend: BackendMemory})
	if err != nil {
		t.Fatal(err)
	}
	timeouts := normalizeOperationTimeouts(OperationTimeouts{})
	timeouts.supervisor = runtime.supervisor
	_, finish := operationContext(context.Background(), OperationRunGet, timeouts)
	closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := runtime.Close(closeCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("bounded close error = %v", err)
	}
	finish()
	if status := runtime.Status(); status.State != RuntimeStateClosed {
		t.Fatalf("runtime did not close: %#v", status)
	}
}

func TestRuntimeCloseBoundsInflightProbeAndRejectsLaterProbe(t *testing.T) {
	runtime, err := NewRuntime(context.Background(), RuntimeOptions{Backend: BackendMemory})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	runtime.probe = func(context.Context) error {
		close(started)
		<-release
		return nil
	}
	probeDone := make(chan error, 1)
	go func() { probeDone <- runtime.Probe(context.Background()) }()
	<-started
	closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := runtime.Close(closeCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("bounded close error = %v", err)
	}
	close(release)
	if err := <-probeDone; err != nil {
		t.Fatalf("in-flight probe completion = %v", err)
	}
	if err := runtime.Probe(context.Background()); !errors.Is(err, ErrRuntimeClosing) {
		t.Fatalf("post-close probe = %v", err)
	}
}

func TestPostgresRuntimeProbeUsesExistingOptInDSN(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("SPARKCLAW_TEST_POSTGRES_DSN is not configured")
	}
	runtime, err := NewRuntime(context.Background(), RuntimeOptions{Backend: BackendPostgres, PostgresDSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	if err := runtime.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
}
