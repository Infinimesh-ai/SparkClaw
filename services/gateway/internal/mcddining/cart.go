package mcddining

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/appstream"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcdintegration"
)

type Host interface {
	Prepare(context.Context, json.RawMessage) (map[string]any, error)
	Abort(context.Context, mcdintegration.QuoteScope, string, int64, string) (map[string]any, error)
}
type cartResources struct {
	root            string
	host            Host
	mu              sync.Mutex
	purchase        string
	revision        int64
	handoff         *mcdintegration.Handoff
	purchaseEnabled bool
	statusEnabled   bool
	refreshes       int
	lastPurchase    *mcdintegration.Handoff
	commitStarted   bool
	review          map[string]any
	preparedQuote   map[string]any
	quoteInput      CartInput
}

func (s *Selection) WithCart(root string, host Host) *Selection {
	if filepath.IsAbs(root) {
		sum := sha256.Sum256([]byte(s.execution.ExecutionID))
		s.cart = &cartResources{root: root, host: host, purchase: "purchase-" + hex.EncodeToString(sum[:])}
	}
	return s
}

type CartInput struct {
	Operation      string            `json:"operation"`
	StoreCode      string            `json:"store_code,omitempty"`
	PickupAt       string            `json:"pickup_at,omitempty"`
	EatType        string            `json:"eat_type,omitempty"`
	MaxAmountCNY   string            `json:"max_amount_cny,omitempty"`
	Items          []json.RawMessage `json:"items,omitempty"`
	CartRef        string            `json:"cart_ref,omitempty"`
	OperationID    string            `json:"operation_id,omitempty"`
	SourceQuoteRef string            `json:"source_quote_ref,omitempty"`
	OperationKey   string            `json:"operation_key,omitempty"`
	InputDigest    string            `json:"input_digest,omitempty"`
}

func (s *Selection) request(i CartInput) map[string]any {
	return map[string]any{"account": s.execution.Identity.AccountRef, "store_code": i.StoreCode, "pickup_at": i.PickupAt, "eat_type": i.EatType, "max_amount_cny": i.MaxAmountCNY}
}
func (s *Selection) Cart(ctx context.Context, raw []byte, write bool) (map[string]any, error) {
	if s.cart == nil {
		return nil, errors.New("mcd_cart_unavailable")
	}
	s.cart.mu.Lock()
	defer s.cart.mu.Unlock()
	var i CartInput
	if len(raw) > 16<<10 || appstream.DecodeStrict(raw, &i) != nil {
		return nil, errors.New("mcd_cart_input_invalid")
	}
	if i.Operation == "cart_operations_list" && !write || i.Operation == "cart_operation_retire" && write {
		key := ""
		if write {
			key = i.OperationKey
			if key == "" || i.InputDigest == "" {
				return nil, errors.New("mcd_original_operation_required")
			}
		}
		rows, err := s.manager.CartRecovery(ctx, s.principal, s.execution, key, i.InputDigest)
		return map[string]any{"operations": rows, "local_only": true, "retirement_confirms_upstream_outcome": false, "automatic_write_retry": false, "checkout_enabled": false}, err
	}
	writes := map[string]bool{"cart_add_simple": true, "cart_add_configured": true, "cart_clear": true, "checkout_quote_pickup": true}
	reads := map[string]bool{"cart_snapshot": true, "cart_add_status": true, "checkout_quote_pickup_status": true}
	if write && !writes[i.Operation] || !write && !reads[i.Operation] {
		return nil, errors.New("mcd_cart_operation_not_permitted")
	}
	args := map[string]any{"request": s.request(i)}
	switch i.Operation {
	case "cart_add_simple", "cart_add_configured":
		if len(i.Items) < 1 || len(i.Items) > 20 {
			return nil, errors.New("mcd_cart_items_required")
		}
		args["items"] = i.Items
	case "cart_clear", "checkout_quote_pickup":
		args["cart_ref"] = i.CartRef
	case "cart_add_status", "checkout_quote_pickup_status":
		args = map[string]any{"operation_id": i.OperationID}
	}
	arguments, _ := json.Marshal(args)
	x := s.execution
	if write {
		x.MaxRequests = 32
	}
	x.MaxRequests = cartRequestBudget(i, x.MaxRequests)
	result, receipt, err := s.manager.Cart(ctx, s.principal, x, i.Operation, arguments, s.cart.root)
	if err != nil {
		return nil, errors.New("mcd_cart_requires_recovery")
	}
	if receipt != nil && receipt.State != "confirmed" {
		return map[string]any{"state": receipt.State, "operation_key": receipt.OperationKey, "automatic_write_retry": false, "checkout_enabled": false}, errors.New("mcd_cart_requires_recovery")
	}
	var value map[string]any
	if result.SchemaVersion == 0 {
		return map[string]any{"state": receipt.State, "operation_key": receipt.OperationKey, "repeated": true, "automatic_write_retry": false, "checkout_enabled": false}, nil
	}
	if result.ErrorCode != "" || json.Unmarshal([]byte(result.ResultJSON), &value) != nil {
		return nil, errors.New("mcd_cart_requires_recovery")
	}
	return map[string]any{"provider": "mcd-cn", "operation": i.Operation, "result": value, "checkout_enabled": false}, nil
}
func (s *Selection) Quote(ctx context.Context, raw []byte) (map[string]any, error) {
	if s.cart == nil || s.cart.host == nil {
		return nil, errors.New("mcd_quote_host_unavailable")
	}
	s.cart.mu.Lock()
	defer s.cart.mu.Unlock()
	var i CartInput
	if len(raw) > 8192 || appstream.DecodeStrict(raw, &i) != nil || i.Operation != "checkout_quote" {
		return nil, errors.New("mcd_quote_input_invalid")
	}
	return s.quoteLocked(ctx, i)
}
func (s *Selection) quoteLocked(ctx context.Context, i CartInput) (map[string]any, error) {
	if s.cart.handoff != nil {
		return nil, errors.New("mcd_original_handoff_requires_recovery")
	}
	s.cart.quoteInput = i
	request, _ := json.Marshal(s.request(i))
	s.cart.revision++
	x := s.execution
	x.MaxRequests = quoteRequestBudget(i.SourceQuoteRef)
	rawQuote, h, err := s.manager.Quote(ctx, s.principal, x, mcdintegration.QuoteRequest{Request: request, PurchaseID: s.cart.purchase, QuoteRevision: s.cart.revision, SourceQuoteRef: i.SourceQuoteRef}, s.cart.root)
	defer clear(rawQuote)
	if h.Epoch > 0 {
		s.cart.handoff = &h
	}
	if err != nil {
		return nil, errors.New("mcd_quote_requires_recovery")
	}
	prepared, err := s.cart.host.Prepare(ctx, rawQuote)
	if err != nil {
		return nil, errors.New("mcd_prepare_requires_original_status")
	}
	if prepared["state"] != "prepared" || prepared["purchase_digest"] != h.PurchaseDigest {
		return nil, errors.New("mcd_prepare_receipt_mismatch")
	}
	var envelope mcdintegration.QuoteEnvelope
	if json.Unmarshal(rawQuote, &envelope) != nil {
		return nil, errors.New("mcd_invalid_quote")
	}
	var quote map[string]any
	if json.Unmarshal(envelope.Quote, &quote) != nil {
		return nil, errors.New("mcd_invalid_quote")
	}
	summary := map[string]any{}
	for _, key := range []string{"store_code", "store_name", "pickup_at", "eat_type", "items", "amount_cents", "regular_cents", "discount_cents", "discount_names", "wallet_before_cents", "pickup_confirmation"} {
		if value, ok := quote[key]; ok {
			summary[key] = value
		}
	}
	s.cart.review = map[string]any{"purchase_id": h.PurchaseID, "purchase_digest": h.PurchaseDigest, "credential_revision": s.execution.CredentialRevision, "currency": "CNY", "payment_method": "ARCHCARD", "amount_cents": quote["amount_cents"], "store_code": quote["store_code"], "pickup_at": quote["pickup_at"], "eat_type": quote["eat_type"], "store_name": quote["store_name"], "items": quote["items"], "discount_names": quote["discount_names"]}
	s.cart.preparedQuote = summary
	return map[string]any{"provider": "mcd-cn", "state": "prepared", "quote": summary, "purchase_id": h.PurchaseID, "purchase_digest": h.PurchaseDigest, "expires_at_ms": envelope.ExpiresAtMS, "payment_method": "ARCHCARD", "currency": "CNY", "checkout_enabled": s.cart.purchaseEnabled, "approval_required": true}, nil
}

// AbortPreparation clears the quote lock only after both ledgers prove that
// the originating Mac can no longer pay this preparation.
func (s *Selection) AbortPreparation(ctx context.Context) (map[string]any, error) {
	if s == nil || s.cart == nil || s.cart.host == nil {
		return nil, errors.New("mcd_quote_host_unavailable")
	}
	s.cart.mu.Lock()
	defer s.cart.mu.Unlock()
	if s.cart.commitStarted {
		return nil, errors.New("mcd_original_operation_requires_recovery")
	}
	if s.cart.handoff != nil {
		h := *s.cart.handoff
		scope := mcdintegration.QuoteScope{Identity: s.execution.Identity, CredentialRevision: s.execution.CredentialRevision, ExecutionID: s.execution.ExecutionID}
		if err := s.manager.AbortHandoff(ctx, s.principal, s.execution, h, "quote_refresh", func(ctx context.Context) (map[string]any, error) {
			return s.cart.host.Abort(ctx, scope, h.PurchaseID, h.Epoch, "quote_refresh")
		}); err != nil {
			return nil, err
		}
	}
	s.cart.handoff, s.cart.review = nil, nil
	return map[string]any{"state": "aborted_before_write", "non_dispatch_proven": true}, nil
}

// Close terminates undispatched preparation when a quote-only Workflow ends.
// Offline/unknown Mac responses keep the member frozen for later recovery.
func (s *Selection) Close(ctx context.Context) error {
	if s == nil || s.cart == nil {
		return nil
	}
	s.cart.mu.Lock()
	defer s.cart.mu.Unlock()
	if s.cart.handoff == nil || s.cart.commitStarted {
		return nil
	}
	h := s.cart.handoff
	return s.manager.AbortHandoff(ctx, s.principal, s.execution, *h, "execution_termination", func(ctx context.Context) (map[string]any, error) {
		return s.cart.host.Abort(ctx, mcdintegration.QuoteScope{Identity: s.execution.Identity, CredentialRevision: s.execution.CredentialRevision, ExecutionID: s.execution.ExecutionID}, h.PurchaseID, h.Epoch, "execution_termination")
	})
}
