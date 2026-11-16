package mcddining

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcdintegration"
	"reflect"
	"time"
)

type PurchaseHost interface {
	Commit(context.Context, mcdintegration.QuoteScope, mcdintegration.Handoff, string, map[string]any) (map[string]any, error)
	Status(context.Context, mcdintegration.QuoteScope, string) (map[string]any, error)
	Orders(context.Context, mcdintegration.QuoteScope) (map[string]any, error)
}

// maxApprovedQuoteRefreshes keeps the original two expiry refreshes per
// execution; each changed refresh still needs a newly displayed approval.
const maxApprovedQuoteRefreshes = 2

func (s *Selection) WithPurchase(enabled bool) *Selection {
	if s.cart != nil {
		s.cart.purchaseEnabled = enabled
		if enabled {
			s.cart.statusEnabled = true
		}
	}
	return s
}
func (s *Selection) WithStatus(enabled bool) *Selection {
	if s.cart != nil {
		s.cart.statusEnabled = enabled
	}
	return s
}
func (s *Selection) OrderingAvailable() bool {
	return s != nil && s.cart != nil && s.cart.purchaseEnabled && s.cart.host != nil
}
func (s *Selection) StatusAvailable() bool {
	return s != nil && s.cart != nil && s.cart.statusEnabled && s.cart.host != nil
}
func (s *Selection) PurchaseArguments() (map[string]any, error) {
	if s.cart == nil {
		return nil, errors.New("mcd_purchase_unavailable")
	}
	s.cart.mu.Lock()
	defer s.cart.mu.Unlock()
	if !s.cart.purchaseEnabled || s.cart.review == nil {
		return nil, errors.New("mcd_purchase_requires_prepared_quote")
	}
	raw, _ := json.Marshal(s.cart.review)
	var value map[string]any
	json.Unmarshal(raw, &value)
	return value, nil
}
func (s *Selection) Commit(ctx context.Context, approval string, args map[string]any) (map[string]any, error) {
	if s.cart == nil {
		return nil, errors.New("mcd_purchase_unavailable")
	}
	s.cart.mu.Lock()
	defer s.cart.mu.Unlock()
	host, ok := s.cart.host.(PurchaseHost)
	raw, _ := json.Marshal(s.cart.review)
	var expected map[string]any
	json.Unmarshal(raw, &expected)
	if !ok || !s.cart.purchaseEnabled || s.cart.handoff == nil || s.cart.review == nil || !reflect.DeepEqual(args, expected) || approval == "" {
		return nil, errors.New("mcd_purchase_authority_mismatch")
	}
	if time.Now().UnixMilli() >= s.cart.handoff.DeadlineMS {
		if err := s.refreshApprovedQuote(ctx); err != nil {
			return nil, err
		}
		raw, _ = json.Marshal(s.cart.review)
		json.Unmarshal(raw, &expected)
		if !reflect.DeepEqual(args, expected) {
			return nil, errors.New("mcd_changed_quote_requires_new_approval")
		}
	}
	h := *s.cart.handoff
	if err := s.manager.StartCommit(ctx, s.principal, s.execution, h); err != nil {
		return nil, err
	}
	s.cart.commitStarted = true
	receipt, err := host.Commit(ctx, mcdintegration.QuoteScope{Identity: s.execution.Identity, CredentialRevision: s.execution.CredentialRevision, ExecutionID: s.execution.ExecutionID}, h, approval, expected)
	if err != nil {
		return map[string]any{"state": "UNKNOWN", "operation_key": mcdintegration.CommitKey(h), "automatic_write_retry": false}, err
	}
	return s.purchaseReceipt(ctx, h, receipt)
}

// A quote refresh has its own epoch and original expiry. Reusing an approval
// requires every review field (including the complete purchase digest) to match.
func (s *Selection) refreshApprovedQuote(ctx context.Context) error {
	h := *s.cart.handoff
	// Expiry refreshes have their own allowance. A Workflow's planned no-coupon
	// requote is a new plan quote, not an approval refresh, and must not use it.
	if s.cart.refreshes >= maxApprovedQuoteRefreshes || time.Now().UnixMilli() >= s.execution.DeadlineMS {
		return errors.New("mcd_quote_refresh_limit_or_deadline")
	}
	s.cart.refreshes++
	scope := mcdintegration.QuoteScope{Identity: s.execution.Identity, CredentialRevision: s.execution.CredentialRevision, ExecutionID: s.execution.ExecutionID}
	if err := s.manager.AbortHandoff(ctx, s.principal, s.execution, h, "quote_refresh", func(ctx context.Context) (map[string]any, error) {
		return s.cart.host.Abort(ctx, scope, h.PurchaseID, h.Epoch, "quote_refresh")
	}); err != nil {
		return err
	}
	s.cart.handoff = nil
	i := s.cart.quoteInput
	if i.SourceQuoteRef != "" {
		// Selected pickup receipts themselves expire: obtain a new explicit switch
		// receipt using a fresh cart snapshot. Replaying the old PUT is forbidden.
		// The still-live immutable purchase approval supplies this identical-method
		// intent; no item, amount or pickup context is changed here.
		invoke := func(op string, args map[string]any, budget int) (map[string]any, error) {
			raw, _ := json.Marshal(args)
			x := s.execution
			x.MaxRequests = budget
			result, receipt, err := s.manager.Cart(ctx, s.principal, x, op, raw, s.cart.root)
			if err != nil || result.ErrorCode != "" || receipt != nil && receipt.State != "confirmed" {
				return nil, errors.New("mcd_pickup_refresh_requires_recovery")
			}
			var value map[string]any
			if json.Unmarshal([]byte(result.ResultJSON), &value) != nil {
				return nil, errors.New("mcd_pickup_refresh_invalid")
			}
			return value, nil
		}
		snapshot, err := invoke("cart_snapshot", map[string]any{"request": s.request(i)}, cartRequestBudget(CartInput{Operation: "cart_snapshot"}, 8))
		if err != nil {
			return err
		}
		ref, ok := snapshot["cart_ref"].(string)
		if !ok || ref == "" {
			return errors.New("mcd_refresh_snapshot_missing")
		}
		selected, err := invoke("checkout_quote_pickup", map[string]any{"request": s.request(i), "cart_ref": ref}, cartRequestBudget(CartInput{Operation: "checkout_quote_pickup"}, 32))
		if err != nil {
			return err
		}
		if selected["state"] != "READY" {
			return errors.New("mcd_refresh_receipt_missing")
		}
		i.SourceQuoteRef, _ = selected["quote_id"].(string)
		if i.SourceQuoteRef == "" {
			return errors.New("mcd_refresh_receipt_missing")
		}
	}
	_, err := s.quoteLocked(ctx, i)
	return err
}
func (s *Selection) purchaseReceipt(ctx context.Context, h mcdintegration.Handoff, receipt map[string]any) (map[string]any, error) {
	if receipt["purchase_id"] != h.PurchaseID || receipt["handoff_epoch"] != h.Epoch {
		return nil, errors.New("mcd_purchase_receipt_mismatch")
	}
	if receipt["terminal_verified"] == true {
		if s.cart.lastPurchase == nil || s.cart.lastPurchase.PurchaseID != h.PurchaseID || s.cart.lastPurchase.Epoch != h.Epoch {
			if err := s.manager.FinishPurchase(ctx, s.principal, s.execution, h, receipt); err != nil {
				return nil, err
			}
		} else if h.State != receipt["state"] {
			return nil, errors.New("mcd_terminal_receipt_changed")
		}
		s.cart.handoff = nil
		h.State, _ = receipt["state"].(string)
		s.cart.lastPurchase = &h
	}
	result := map[string]any{"state": receipt["state"], "purchase_id": h.PurchaseID, "operation_key": mcdintegration.CommitKey(h), "automatic_write_retry": false}
	for _, key := range []string{"terminal_verified", "non_dispatch_proven", "stop_reason", "pickup_code", "order_no"} {
		if value, ok := receipt[key]; ok {
			result[key] = value
		}
	}
	return result, nil
}
func (s *Selection) PurchaseStatus(ctx context.Context) (map[string]any, error) {
	if s.cart == nil {
		return nil, errors.New("mcd_purchase_unavailable")
	}
	s.cart.mu.Lock()
	defer s.cart.mu.Unlock()
	host, ok := s.cart.host.(PurchaseHost)
	if !ok || !s.cart.statusEnabled {
		return nil, errors.New("mcd_purchase_unavailable")
	}
	h, err := s.manager.PendingHandoff(ctx, s.principal, s.execution)
	if err != nil {
		return nil, err
	}
	if h == nil {
		if s.cart.lastPurchase != nil {
			h = s.cart.lastPurchase
		} else {
			orders, err := host.Orders(ctx, mcdintegration.QuoteScope{Identity: s.execution.Identity, CredentialRevision: s.execution.CredentialRevision, ExecutionID: s.execution.ExecutionID})
			if err != nil {
				return nil, errors.New("mcd_status_unavailable_without_pending_purchase")
			}
			view, err := orderPolicyView(orders)
			if err != nil {
				return nil, errors.New("mcd_status_unavailable_without_pending_purchase")
			}
			return view, nil
		}
	}
	scope := mcdintegration.QuoteScope{Identity: h.Identity, CredentialRevision: h.CredentialRevision, ExecutionID: h.ExecutionID}
	receipt, err := host.Status(ctx, scope, mcdintegration.CommitKey(*h))
	if err != nil {
		return nil, err
	}
	if receipt["state"] == "aborted_before_write" {
		if err := s.manager.FinishStatusAbort(ctx, s.principal, s.execution, *h, receipt); err != nil {
			return nil, err
		}
		s.cart.handoff, s.cart.review, s.cart.lastPurchase = nil, nil, nil
		s.cart.commitStarted = false
		state := "aborted_before_write"
		if receipt["original_state"] == "NOT_DISPATCHED" {
			state = "NOT_DISPATCHED"
		}
		return map[string]any{"state": state, "operation_key": mcdintegration.CommitKey(*h),
			"non_dispatch_proven": true, "purchase_closed": true, "automatic_write_retry": false, "stop_reason": receipt["stop_reason"]}, nil
	}
	if receipt["state"] == "NOT_DISPATCHED" || receipt["state"] == "not_found" {
		return map[string]any{"state": "reconciliation_required", "non_dispatch_proven": false, "automatic_write_retry": false}, nil
	}
	return s.purchaseReceipt(ctx, *h, receipt)
}

func (s *Selection) OrderPolicy(ctx context.Context) (map[string]any, error) {
	if !s.StatusAvailable() {
		return nil, errors.New("mcd_purchase_unavailable")
	}
	host, ok := s.cart.host.(PurchaseHost)
	if !ok {
		return nil, errors.New("mcd_purchase_unavailable")
	}
	orders, err := host.Orders(ctx, mcdintegration.QuoteScope{Identity: s.execution.Identity, CredentialRevision: s.execution.CredentialRevision, ExecutionID: s.execution.ExecutionID})
	if err != nil {
		return nil, err
	}
	return orderPolicyView(orders)
}

// orderPolicyView keeps only the account Policy ceiling. Historical journal
// rows can carry earlier pickup codes and opaque official JSON; the ordering
// Workflow needs neither, so they never enter persisted tool results.
func orderPolicyView(orders map[string]any) (map[string]any, error) {
	policy, _ := orders["policy"].(map[string]any)
	limit, ok := policy["max_order_cents"]
	if !ok {
		return nil, errors.New("mcd_order_policy_missing")
	}
	return map[string]any{"pending_purchase": false, "policy": map[string]any{"max_order_cents": limit}, "automatic_write_retry": false}, nil
}
