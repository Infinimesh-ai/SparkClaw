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

func (s *Selection) WithPurchase(enabled bool) *Selection {
	if s.cart != nil {
		s.cart.purchaseEnabled = enabled
	}
	return s
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
	if s.cart.revision >= 3 || time.Now().UnixMilli() >= s.execution.DeadlineMS {
		return errors.New("mcd_quote_refresh_limit_or_deadline")
	}
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
		snapshot, err := invoke("cart_snapshot", map[string]any{"request": s.request(i)}, 8)
		if err != nil {
			return err
		}
		ref, ok := snapshot["cart_ref"].(string)
		if !ok || ref == "" {
			return errors.New("mcd_refresh_snapshot_missing")
		}
		selected, err := invoke("checkout_quote_pickup", map[string]any{"request": s.request(i), "cart_ref": ref}, 32)
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
		if err := s.manager.FinishPurchase(ctx, s.principal, s.execution, h, receipt); err != nil {
			return nil, err
		}
		s.cart.handoff = nil
	}
	result := map[string]any{"state": receipt["state"], "purchase_id": h.PurchaseID, "operation_key": mcdintegration.CommitKey(h), "automatic_write_retry": false}
	if encoded, ok := receipt["result_json"].(string); ok && len(encoded) <= 32<<10 {
		var value map[string]any
		if json.Unmarshal([]byte(encoded), &value) == nil {
			result["result"] = value
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
	if !ok || !s.cart.purchaseEnabled {
		return nil, errors.New("mcd_purchase_unavailable")
	}
	h, err := s.manager.PendingHandoff(ctx, s.principal, s.execution)
	if err != nil {
		return nil, err
	}
	if h == nil {
		return host.Orders(ctx, mcdintegration.QuoteScope{Identity: s.execution.Identity, CredentialRevision: s.execution.CredentialRevision, ExecutionID: s.execution.ExecutionID})
	}
	scope := mcdintegration.QuoteScope{Identity: h.Identity, CredentialRevision: h.CredentialRevision, ExecutionID: h.ExecutionID}
	if h.State == "frozen" || h.State == "abort_requested" {
		x := s.execution
		x.ExecutionID = h.ExecutionID
		x.CredentialRevision = h.CredentialRevision
		reason := "execution_termination"
		if h.AbortReason != "" {
			reason = h.AbortReason
		}
		err = s.manager.AbortHandoff(ctx, s.principal, x, *h, reason, func(ctx context.Context) (map[string]any, error) {
			return s.cart.host.Abort(ctx, scope, h.PurchaseID, h.Epoch, reason)
		})
		return map[string]any{"state": "preparation_recovery", "automatic_write_retry": false}, err
	}
	receipt, err := host.Status(ctx, scope, mcdintegration.CommitKey(*h))
	if err != nil {
		return nil, err
	}
	return s.purchaseReceipt(ctx, *h, receipt)
}
