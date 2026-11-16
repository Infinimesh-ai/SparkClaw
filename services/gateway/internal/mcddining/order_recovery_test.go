package mcddining

import (
	"context"
	"errors"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcdintegration"
)

type orderRecoveryHost struct {
	*diningHost
	handoff     mcdintegration.Handoff
	pickupCode  string
	statuses    int
	statusScope mcdintegration.QuoteScope
}

type nonDispatchRecoveryHost struct {
	*orderRecoveryHost
	statusErr error
	unretired bool
}

func (h *nonDispatchRecoveryHost) Status(ctx context.Context, scope mcdintegration.QuoteScope, key string) (map[string]any, error) {
	receipt, err := h.orderRecoveryHost.Status(ctx, scope, key)
	if h.statusErr != nil {
		return nil, h.statusErr
	}
	receipt["state"], receipt["terminal_verified"], receipt["non_dispatch_proven"] = "aborted_before_write", false, true
	receipt["operation_key"], receipt["scope_digest"] = key, hashDining(scope)
	receipt["reason"], receipt["purchase_closed"] = "execution_termination", true
	receipt["automatic_write_retry"], receipt["official_order_canceled"], receipt["refund_attempted"] = false, false, false
	receipt["original_state"], receipt["stop_reason"] = "NOT_DISPATCHED", "insufficient_wallet_balance"
	if h.unretired {
		receipt["state"], receipt["purchase_closed"] = "NOT_DISPATCHED", false
	}
	return receipt, err
}

func (h *nonDispatchRecoveryHost) Abort(ctx context.Context, scope mcdintegration.QuoteScope, purchase string, epoch int64, reason string) (map[string]any, error) {
	h.aborts++
	return nil, errors.New("checkout.abort is not negotiated for this status-only host")
}

func TestNotDispatchedRecoveryUsesStatusProofWithoutCheckoutAbort(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		t.Run(map[bool]string{false: "confirmed_abort", true: "lost_abort"}[unavailable], func(t *testing.T) {
			s, _, original := diningFixture(t)
			original.unknown = true
			host := &nonDispatchRecoveryHost{orderRecoveryHost: &orderRecoveryHost{diningHost: original}}
			if unavailable {
				host.statusErr = errors.New("lost status retirement response")
			}
			s.cart.host = host
			args, _ := s.PurchaseArguments()
			if _, err := s.Commit(t.Context(), "approval", args); err == nil {
				t.Fatal("fixture must lose the commit reply")
			}
			recovery, err := NewRecoverySelection(t.Context(), s.manager, s.principal, "recovery-execution")
			if err != nil {
				t.Fatal(err)
			}
			recovery.WithCart(t.TempDir(), host).WithPurchase(false).WithStatus(true)
			status, err := recovery.PurchaseStatus(t.Context())
			pending, pendingErr := s.manager.PendingHandoff(t.Context(), s.principal, recovery.execution)
			if pendingErr != nil || (err != nil) != unavailable || (pending != nil) != unavailable {
				t.Fatalf("release before abort proof: status=%v err=%v pending=%v pendingErr=%v", status, err, pending, pendingErr)
			}
			if !unavailable && (status["state"] != "NOT_DISPATCHED" || status["non_dispatch_proven"] != true) {
				t.Fatalf("non-dispatch result = %v", status)
			}
			if host.statusScope.ExecutionID != s.execution.ExecutionID || host.statusScope.CredentialRevision != s.execution.CredentialRevision || host.commits != 1 || host.statuses != 1 || host.aborts != 0 {
				t.Fatalf("non-dispatch rebound/replayed original: scope=%+v commits/statuses/aborts=%d/%d/%d", host.statusScope, host.commits, host.statuses, host.aborts)
			}
		})
	}
}

func TestFrozenRecoveryAlsoUsesOnlyStatusAndRejectsUnretiredEvidence(t *testing.T) {
	for _, unretired := range []bool{false, true} {
		t.Run(map[bool]string{false: "retired", true: "unretired"}[unretired], func(t *testing.T) {
			s, _, original := diningFixture(t)
			host := &nonDispatchRecoveryHost{orderRecoveryHost: &orderRecoveryHost{diningHost: original, handoff: *s.cart.handoff}, unretired: unretired}
			recovery, err := NewRecoverySelection(t.Context(), s.manager, s.principal, "recovery-execution")
			if err != nil {
				t.Fatal(err)
			}
			recovery.WithCart(t.TempDir(), host).WithPurchase(false).WithStatus(true)
			result, err := recovery.PurchaseStatus(t.Context())
			pending, pendingErr := s.manager.PendingHandoff(t.Context(), s.principal, recovery.execution)
			if err != nil || pendingErr != nil || (pending != nil) != unretired || host.aborts != 0 || host.commits != 0 || host.statuses != 1 {
				t.Fatalf("status-only frozen recovery: result=%v pending=%v err=%v/%v host=%+v", result, pending, err, pendingErr, host)
			}
			if unretired && result["non_dispatch_proven"] != false {
				t.Fatal("unretired preparation released", result)
			}
		})
	}
}

func (h *orderRecoveryHost) receipt() map[string]any {
	return map[string]any{
		"state": "PAID", "purchase_id": h.handoff.PurchaseID, "handoff_epoch": h.handoff.Epoch,
		"terminal_verified": true, "result_json": "{}", "order_no": "fixture-order", "pickup_code": h.pickupCode,
	}
}

func (h *orderRecoveryHost) Commit(ctx context.Context, scope mcdintegration.QuoteScope, q mcdintegration.Handoff, approval string, review map[string]any) (map[string]any, error) {
	h.handoff = q
	if _, err := h.diningHost.Commit(ctx, scope, q, approval, review); err != nil {
		return nil, err
	}
	return h.receipt(), nil
}

func (h *orderRecoveryHost) Status(_ context.Context, scope mcdintegration.QuoteScope, key string) (map[string]any, error) {
	h.statuses++
	h.statusScope = scope
	return h.receipt(), nil
}

func TestPaidCommitSettlesOriginalPurchaseWithoutAbort(t *testing.T) {
	s, _, original := diningFixture(t)
	host := &orderRecoveryHost{diningHost: original}
	s.cart.host = host
	args, err := s.PurchaseArguments()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(t.Context(), "approval", args); err != nil {
		t.Fatal(err)
	}
	host.pickupCode = "A123"
	status, err := s.PurchaseStatus(t.Context())
	if err != nil || status["state"] != "PAID" || status["pickup_code"] != "A123" || host.statuses != 1 || host.aborts != 0 || host.commits != 1 {
		t.Fatalf("paid original settlement = %v, err = %v; statuses/aborts/commits = %d/%d/%d", status, err, host.statuses, host.aborts, host.commits)
	}
}

func TestRecoveredPaidPurchaseAcceptsLaterPickupCode(t *testing.T) {
	s, _, original := diningFixture(t)
	original.unknown = true
	host := &orderRecoveryHost{diningHost: original}
	s.cart.host = host
	args, _ := s.PurchaseArguments()
	if _, err := s.Commit(t.Context(), "approval", args); err == nil {
		t.Fatal("fixture must lose the commit reply")
	}
	if status, err := s.PurchaseStatus(t.Context()); err != nil || status["state"] != "PAID" || status["pickup_code"] != "" {
		t.Fatalf("first paid receipt = %v, %v", status, err)
	}
	host.pickupCode = "A123"
	status, err := s.PurchaseStatus(t.Context())
	if err != nil || status["state"] != "PAID" || status["pickup_code"] != "A123" || host.statuses != 2 || host.aborts != 0 || host.commits != 1 {
		t.Fatalf("later pickup receipt = %v, err = %v; statuses/aborts/commits = %d/%d/%d", status, err, host.statuses, host.aborts, host.commits)
	}
}

func TestEarlierExecutionRecoveryReleasesOnlyOriginalPurchase(t *testing.T) {
	s, _, original := diningFixture(t)
	original.unknown = true
	host := &orderRecoveryHost{diningHost: original, pickupCode: "A123"}
	s.cart.host = host
	args, _ := s.PurchaseArguments()
	if _, err := s.Commit(t.Context(), "approval", args); err == nil {
		t.Fatal("fixture must lose the commit reply")
	}
	recovery, err := NewRecoverySelection(t.Context(), s.manager, s.principal, "recovery-execution")
	if err != nil {
		t.Fatal(err)
	}
	recovery.WithCart(t.TempDir(), host).WithPurchase(false).WithStatus(true)
	status, err := recovery.PurchaseStatus(t.Context())
	if err != nil || status["state"] != "PAID" || status["pickup_code"] != "A123" {
		t.Fatalf("earlier purchase status = %v, %v", status, err)
	}
	if host.statusScope.ExecutionID != s.execution.ExecutionID || host.statusScope.CredentialRevision != s.execution.CredentialRevision || host.statusScope.Identity != s.execution.Identity {
		t.Fatalf("status rebound the earlier purchase: %+v", host.statusScope)
	}
	if pending, err := s.manager.PendingHandoff(t.Context(), s.principal, recovery.execution); err != nil || pending != nil {
		t.Fatalf("original verified purchase remains frozen: %v, %v", pending, err)
	}
	if host.commits != 1 || host.statuses != 1 || host.aborts != 0 || recovery.OrderingAvailable() {
		t.Fatalf("recovery gained write authority: commits/statuses/aborts = %d/%d/%d", host.commits, host.statuses, host.aborts)
	}
}
