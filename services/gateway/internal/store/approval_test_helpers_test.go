package store

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func mustSaveApproval(t testing.TB, repository ApprovalRepository, approval app.Approval) app.Approval {
	t.Helper()
	candidate, err := repository.SaveApproval(t.Context(), approval)
	stored, err := ReconcileApprovalWrite(t.Context(), repository, candidate, err)
	if err != nil {
		t.Fatalf("save approval: %v", err)
	}
	return stored
}

func mustGetApproval(t testing.TB, repository ApprovalRepository, id string) (app.Approval, bool) {
	t.Helper()
	approval, found, err := repository.GetApproval(t.Context(), id)
	if err != nil {
		t.Fatalf("get approval: %v", err)
	}
	return approval, found
}

func mustFindApprovalByExternalRef(t testing.TB, repository ApprovalRepository, source app.ApprovalSource, externalID string) (app.Approval, bool) {
	t.Helper()
	approval, found, err := repository.FindApprovalByExternalRef(t.Context(), source, externalID)
	if err != nil {
		t.Fatalf("find approval: %v", err)
	}
	return approval, found
}

func mustUpdatePendingApproval(t testing.TB, repository ApprovalRepository, expected, candidate app.Approval) app.Approval {
	t.Helper()
	updated, err := repository.UpdatePendingApproval(t.Context(), NewApprovalUpdate(expected, candidate))
	updated, err = ReconcileApprovalWrite(t.Context(), repository, updated, err)
	if err != nil {
		t.Fatalf("update pending approval: %v", err)
	}
	return updated
}

func mustResolveApproval(t testing.TB, repository ApprovalRepository, id, status, note string) app.Approval {
	t.Helper()
	resolved, err := repository.ResolveApproval(t.Context(), id, status, note)
	resolved, err = ReconcileApprovalWrite(t.Context(), repository, resolved, err)
	if err != nil {
		t.Fatalf("resolve approval: %v", err)
	}
	return resolved
}

func mustListApprovals(t testing.TB, repository ApprovalRepository, status string) []app.Approval {
	t.Helper()
	approvals, err := repository.ListApprovals(t.Context(), status)
	if err != nil {
		t.Fatalf("list approvals: %v", err)
	}
	return approvals
}
