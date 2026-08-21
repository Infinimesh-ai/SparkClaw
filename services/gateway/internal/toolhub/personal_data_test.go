package toolhub

import (
	"context"
	"errors"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
)

type approvalSaveFaultStore struct {
	store.Store
	err        error
	contextKey any
	seen       any
}

func (s *approvalSaveFaultStore) SaveApproval(ctx context.Context, approval app.Approval) (app.Approval, error) {
	s.seen = ctx.Value(s.contextKey)
	return app.Approval{}, s.err
}

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
	approvals := storetest.MustListApprovals(t, st, "pending")
	if len(approvals) != 1 || approvals[0].Summary != "Confirm sending the deployment note" || approvals[0].Tool != "notify.ask_approval" {
		t.Fatalf("notify approval not saved: %#v", approvals)
	}
	calls := testListToolCalls(st, "s")
	if len(calls) != 1 || calls[0].Status != "approval_pending" || calls[0].ApprovalID != approvals[0].ID {
		t.Fatalf("notify tool call not saved: %#v", calls)
	}
}

func TestNotifyAskApprovalPropagatesApprovalSaveFailure(t *testing.T) {
	privateCause := errors.New("private approval write failure")
	key := struct{ name string }{"toolhub-approval-context"}
	fault := &approvalSaveFaultStore{
		Store: store.NewMemoryStore(), contextKey: key,
		err: &store.StoreError{Code: store.StoreErrorUnavailable, Operation: store.OperationApprovalSave, Err: privateCause},
	}
	hub := New(config.Default(), fault)
	ctx := context.WithValue(t.Context(), key, "caller-value")
	_, err := hub.Execute(ctx, "notify.ask_approval", map[string]any{"summary": "Confirm"}, "session", "run")
	if !errors.Is(err, privateCause) || fault.seen != "caller-value" {
		t.Fatalf("Execute err=%v context=%#v", err, fault.seen)
	}
}
