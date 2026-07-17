package toolhub

import (
	"context"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestNotifyAskApprovalCreatesPendingApproval(t *testing.T) {
	cfg := config.Default()
	st := store.NewMemoryStore()
	hub := New(cfg, st)

	result, err := hub.Execute(context.Background(), "notify.ask_approval", map[string]any{
		"summary": "Confirm sending the deployment note",
		"reason":  "The user should approve external communication.",
	}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	out := result.Output.(map[string]any)
	if out["status"] != "approval_requested" || out["approval_id"] == "" {
		t.Fatalf("unexpected notify output: %#v", out)
	}
	approvals := st.ListApprovals("pending")
	if len(approvals) != 1 || approvals[0].Summary != "Confirm sending the deployment note" || approvals[0].Tool != "notify.ask_approval" {
		t.Fatalf("notify approval not saved: %#v", approvals)
	}
	calls := st.ListToolCalls("s")
	if len(calls) != 1 || calls[0].Status != "approval_pending" || calls[0].ApprovalID != approvals[0].ID {
		t.Fatalf("notify tool call not saved: %#v", calls)
	}
}
