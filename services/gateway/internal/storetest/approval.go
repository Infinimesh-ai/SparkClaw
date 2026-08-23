package storetest

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func MustSaveApproval(t testing.TB, repository store.ApprovalRepository, approval app.Approval) app.Approval {
	t.Helper()
	candidate, err := repository.SaveApproval(t.Context(), approval)
	stored, err := store.ReconcileApprovalWrite(t.Context(), repository, candidate, err)
	if err != nil {
		t.Fatalf("save approval fixture: %v", err)
	}
	return stored
}

func MustGetApproval(t testing.TB, repository store.ApprovalRepository, id string) (app.Approval, bool) {
	t.Helper()
	approval, found, err := repository.GetApproval(t.Context(), id)
	if err != nil {
		t.Fatalf("get approval fixture: %v", err)
	}
	return approval, found
}

func MustFindApprovalByExternalRef(t testing.TB, repository store.ApprovalRepository, source app.ApprovalSource, externalID string) (app.Approval, bool) {
	t.Helper()
	approval, found, err := repository.FindApprovalByExternalRef(t.Context(), source, externalID)
	if err != nil {
		t.Fatalf("find approval fixture: %v", err)
	}
	return approval, found
}

func MustUpdatePendingApproval(t testing.TB, repository store.ApprovalRepository, expected, candidate app.Approval) app.Approval {
	t.Helper()
	updated, err := repository.UpdatePendingApproval(t.Context(), store.NewApprovalUpdate(expected, candidate))
	updated, err = store.ReconcileApprovalWrite(t.Context(), repository, updated, err)
	if err != nil {
		t.Fatalf("update pending approval fixture: %v", err)
	}
	return updated
}

func MustResolveApproval(t testing.TB, repository store.ApprovalRepository, id string, status app.ApprovalStatus, note string) app.Approval {
	t.Helper()
	resolved, err := repository.ResolveApproval(t.Context(), id, status, note)
	resolved, err = store.ReconcileApprovalWrite(t.Context(), repository, resolved, err)
	if err != nil {
		t.Fatalf("resolve approval fixture: %v", err)
	}
	return resolved
}

func MustListApprovals(t testing.TB, repository store.ApprovalRepository, status app.ApprovalStatus) []app.Approval {
	t.Helper()
	approvals, err := repository.ListApprovals(t.Context(), status)
	if err != nil {
		t.Fatalf("list approval fixtures: %v", err)
	}
	return approvals
}
