package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestApprovalRepositoryContract(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		runApprovalRepositoryContract(t, NewMemoryStore())
	})
	t.Run("file", func(t *testing.T) {
		repository, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
		if err != nil {
			t.Fatal(err)
		}
		runApprovalRepositoryContract(t, repository)
	})
}

func TestPostgresApprovalRepositoryContract(t *testing.T) {
	dsn := os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set SPARKCLAW_TEST_POSTGRES_DSN to run postgres store integration tests")
	}
	repository, err := NewPostgresStore(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	truncatePostgresStore(t, repository)
	runApprovalRepositoryContract(t, repository)
}

func runApprovalRepositoryContract(t *testing.T, repository Store) {
	t.Helper()
	ctx := t.Context()
	created := time.Date(2026, 8, 21, 16, 0, 0, 123456789, time.FixedZone("contract", 8*60*60))
	wantCreated := created.UTC().Truncate(time.Microsecond)
	approval := approvalContractFixture("approval-contract", "external-contract", created)

	saved, err := repository.SaveApproval(ctx, approval)
	if err != nil || !saved.CreatedAt.Equal(wantCreated) || saved.CreatedAt.Location() != time.UTC {
		t.Fatalf("SaveApproval = %#v err=%v", saved, err)
	}
	approval.Resources[0] = "mutated-input"
	approval.Arguments["nested"].(map[string]any)["value"] = "mutated-input"
	approval.PolicyContext.MCP.RequesterDeviceID = "mutated-input"
	approval.Presentation.Locators[0].Path = "mutated-input"
	stored, found, err := repository.GetApproval(ctx, saved.ID)
	if err != nil || !found || stored.Resources[0] != "workspace:report.txt" ||
		stored.Arguments["nested"].(map[string]any)["value"] != "original" ||
		stored.PolicyContext.MCP.RequesterDeviceID != "device-original" || stored.Presentation.Locators[0].Path != "report.txt" {
		t.Fatalf("saved approval retained caller aliases: %#v found=%t err=%v", stored, found, err)
	}
	stored.Arguments["nested"].(map[string]any)["value"] = "mutated-read"
	stored.Presentation.Locators[0].Path = "mutated-read"
	again, found, err := repository.GetApproval(ctx, saved.ID)
	if err != nil || !found || again.Arguments["nested"].(map[string]any)["value"] != "original" || again.Presentation.Locators[0].Path != "report.txt" {
		t.Fatalf("GetApproval returned mutable aliases: %#v found=%t err=%v", again, found, err)
	}

	replayed, err := repository.SaveApproval(ctx, saved)
	if err != nil || !reflect.DeepEqual(replayed, saved) {
		t.Fatalf("exact SaveApproval replay = %#v err=%v", replayed, err)
	}
	changed := saved
	changed.Summary = "different payload"
	if _, err := repository.SaveApproval(ctx, changed); StoreErrorCodeOf(err) != StoreErrorConflict {
		t.Fatalf("same-ID conflict = %v code=%q", err, StoreErrorCodeOf(err))
	}
	externalConflict := saved
	externalConflict.ID = "approval-external-conflict"
	if _, err := repository.SaveApproval(ctx, externalConflict); StoreErrorCodeOf(err) != StoreErrorConflict {
		t.Fatalf("external-ref conflict = %v code=%q", err, StoreErrorCodeOf(err))
	}
	byExternalRef, found, err := repository.FindApprovalByExternalRef(ctx, saved.Source, saved.ExternalID)
	if err != nil || !found || byExternalRef.ID != saved.ID {
		t.Fatalf("FindApprovalByExternalRef = %#v found=%t err=%v", byExternalRef, found, err)
	}

	expected := again
	candidate, err := cloneApproval(expected)
	if err != nil {
		t.Fatal(err)
	}
	candidate.Summary = "updated summary"
	candidate.Resources = []string{"workspace:updated.txt"}
	candidate.Arguments = map[string]any{"nested": map[string]any{"value": "updated"}}
	candidate.ExternalContext.Plan = "updated plan"
	updated, err := repository.UpdatePendingApproval(ctx, NewApprovalUpdateWithNote(expected, candidate, "owner edit"))
	if err != nil || updated.Summary != candidate.Summary || !updated.CreatedAt.Equal(saved.CreatedAt) || updated.Status != "pending" {
		t.Fatalf("UpdatePendingApproval = %#v err=%v", updated, err)
	}
	stale := candidate
	stale.Summary = "stale update"
	if _, err := repository.UpdatePendingApproval(ctx, NewApprovalUpdate(expected, stale)); StoreErrorCodeOf(err) != StoreErrorConflict {
		t.Fatalf("stale pending CAS = %v code=%q", err, StoreErrorCodeOf(err))
	}

	resolved, err := repository.ResolveApproval(ctx, saved.ID, "approved", "owner approved")
	if err != nil || resolved.Status != "approved" || resolved.ResolvedAt == nil || resolved.ResolvedAt.Location() != time.UTC || resolved.ResolvedAt.Nanosecond()%1000 != 0 {
		t.Fatalf("ResolveApproval = %#v err=%v", resolved, err)
	}
	resolutionReplay, err := repository.ResolveApproval(ctx, saved.ID, "approved", "owner approved")
	if err != nil || !reflect.DeepEqual(resolutionReplay, resolved) {
		t.Fatalf("resolution replay = %#v err=%v", resolutionReplay, err)
	}
	if _, err := repository.ResolveApproval(ctx, saved.ID, "rejected", "different decision"); StoreErrorCodeOf(err) != StoreErrorConflict {
		t.Fatalf("different terminal decision = %v code=%q", err, StoreErrorCodeOf(err))
	}
	if _, err := repository.UpdatePendingApproval(ctx, NewApprovalUpdate(updated, updated)); StoreErrorCodeOf(err) != StoreErrorConflict {
		t.Fatalf("resolved pending update = %v code=%q", err, StoreErrorCodeOf(err))
	}

	assertApprovalLifecycleContract(t, repository)
	missing, found, err := repository.GetApproval(ctx, "missing-approval")
	if err != nil || found || missing.ID != "" {
		t.Fatalf("missing approval = %#v found=%t err=%v", missing, found, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := repository.GetApproval(cancelled, saved.ID); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("cancelled GetApproval = %v code=%q", err, StoreErrorCodeOf(err))
	}
	if _, err := repository.ListApprovals(cancelled, ""); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("cancelled ListApprovals = %v code=%q", err, StoreErrorCodeOf(err))
	}
}

func approvalContractFixture(id, externalID string, created time.Time) app.Approval {
	return app.Approval{
		ID: id, Source: app.ApprovalSourceHappyTeamPlan, ExternalID: externalID,
		ExternalContext: &app.ExternalApprovalContext{Provider: "happy-team", Plan: "original plan", PlanAvailability: app.ExternalPlanAvailable},
		Tool:            "workspace.data.access", Risk: app.RiskRead, Status: "pending", Summary: "approve workspace read", Reason: "contract",
		Resources: []string{"workspace:report.txt"}, Arguments: map[string]any{"nested": map[string]any{"value": "original"}}, CreatedAt: created,
		PolicyContext: &app.PolicyExecutionContext{SchemaVersion: 1, MCP: &app.MCPInvocationRef{RequesterDeviceID: "device-original"}},
		Presentation:  &app.ApprovalPresentation{Kind: "workspace", Locators: []app.MessageMediaLocator{{Path: "report.txt"}}},
	}
}

func assertApprovalLifecycleContract(t *testing.T, repository Store) {
	t.Helper()
	auditCounts := map[string]int{}
	for _, event := range repository.ListAudit("") {
		auditCounts[event.Type]++
	}
	eventCounts := map[string]int{}
	for _, event := range repository.EventsAfter("", "") {
		eventCounts[event.Type]++
	}
	if auditCounts["approval.pending"] != 1 || auditCounts["approval.modified"] != 1 || auditCounts["approval.approved"] != 1 {
		t.Fatalf("approval audit lifecycle = %#v", auditCounts)
	}
	if eventCounts["approval.pending"] != 2 || eventCounts["approval.approved"] != 1 {
		t.Fatalf("approval event lifecycle = %#v", eventCounts)
	}
}

func TestFileApprovalDefiniteFailureRestoresRecordAndLifecycle(t *testing.T) {
	repository, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	before := repository.captureFileRollback()
	repository.commitOps = &controlledFileCommitOps{failStage: "encode", failRemaining: 1}
	candidate, err := repository.SaveApproval(t.Context(), approvalContractFixture("approval-file-definite", "file-definite", time.Now()))
	if candidate.ID != "" || StoreErrorCodeOf(err) != StoreErrorDurability || !reflect.DeepEqual(repository.captureFileRollback(), before) {
		t.Fatalf("definite failure candidate=%#v err=%v rollback=%t", candidate, err, reflect.DeepEqual(repository.captureFileRollback(), before))
	}
}

func TestFileApprovalUnknownOutcomeReconcilesAndSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	repository, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	repository.commitOps = &controlledFileCommitOps{failStage: "dir_sync", failRemaining: 1}
	want := approvalContractFixture("approval-file-unknown", "file-unknown", time.Now())
	candidate, writeErr := repository.SaveApproval(t.Context(), want)
	if candidate.ID != want.ID || StoreErrorCodeOf(writeErr) != StoreErrorUnknownOutcome || repository.currentFileFence() == nil {
		t.Fatalf("unknown candidate=%#v err=%v fence=%v", candidate, writeErr, repository.currentFileFence())
	}
	reconciled, err := ReconcileApprovalWrite(t.Context(), repository, candidate, writeErr)
	if err != nil || reconciled.ID != want.ID || repository.currentFileFence() != nil {
		t.Fatalf("reconciled=%#v err=%v fence=%v", reconciled, err, repository.currentFileFence())
	}
	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, found, err := reloaded.GetApproval(t.Context(), want.ID); err != nil || !found || !approvalsEqual(got, reconciled) {
		t.Fatalf("restart approval=%#v found=%t err=%v", got, found, err)
	}
}

func TestPostgresApprovalConcurrentExternalRefAndPendingCAS(t *testing.T) {
	dsn := os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set SPARKCLAW_TEST_POSTGRES_DSN to run postgres store integration tests")
	}
	first, err := NewPostgresStore(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	truncatePostgresStore(t, first)
	second, err := NewPostgresStore(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	start := make(chan struct{})
	errorsFound := make(chan error, 2)
	var wait sync.WaitGroup
	for index, repository := range []*PostgresStore{first, second} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			approval := approvalContractFixture([]string{"approval-concurrent-a", "approval-concurrent-b"}[index], "external-concurrent", time.Now())
			_, err := repository.SaveApproval(t.Context(), approval)
			errorsFound <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errorsFound)
	successes, conflicts := 0, 0
	for err := range errorsFound {
		switch StoreErrorCodeOf(err) {
		case "":
			successes++
		case StoreErrorConflict:
			conflicts++
		default:
			t.Fatalf("concurrent external ref error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("external ref outcomes success=%d conflict=%d", successes, conflicts)
	}
	items, err := first.ListApprovals(t.Context(), "pending")
	if err != nil || len(items) != 1 {
		t.Fatalf("concurrent external ref items=%#v err=%v", items, err)
	}

	expected := items[0]
	start = make(chan struct{})
	errorsFound = make(chan error, 2)
	for index, repository := range []*PostgresStore{first, second} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			candidate := expected
			candidate.Summary = []string{"candidate-a", "candidate-b"}[index]
			_, err := repository.UpdatePendingApproval(t.Context(), NewApprovalUpdate(expected, candidate))
			errorsFound <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errorsFound)
	successes, conflicts = 0, 0
	for err := range errorsFound {
		switch StoreErrorCodeOf(err) {
		case "":
			successes++
		case StoreErrorConflict:
			conflicts++
		default:
			t.Fatalf("concurrent pending CAS error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("pending CAS outcomes success=%d conflict=%d", successes, conflicts)
	}
}

func TestReconcileApprovalWritePreservesNonUnknownErrors(t *testing.T) {
	want := errors.New("definite failure")
	if _, err := ReconcileApprovalWrite(t.Context(), NewMemoryStore(), app.Approval{ID: "approval"}, want); !errors.Is(err, want) {
		t.Fatalf("reconciliation changed definite error: %v", err)
	}
}
