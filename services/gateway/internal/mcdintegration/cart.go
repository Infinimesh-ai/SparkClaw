package mcdintegration

import (
	"context"
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/appstream"
)

var readOperations = map[string]bool{"membership_assets": true, "stores_favorites": true, "cities_list": true, "stores_search": true, "store_get": true, "pickup_slots": true, "pickup_methods": true, "menu_list": true, "product_detail": true, "coupons_list": true, "coupon_products": true, "cart_get": true}
var cartKinds = map[string]string{"cart_add_simple": "cart_add", "cart_add_configured": "cart_add_configured", "cart_clear": "cart_clear", "checkout_quote_pickup": "pickup_select"}
var cartReads = map[string]bool{"cart_snapshot": true, "cart_add_status": true, "checkout_quote_pickup_status": true}

// CartAuthority is made after Policy admission and the durable member claim.
// It is never accepted from renderer/model input.
type CartAuthority struct {
	OperationKey string `json:"operation_key"`
	InputDigest  string `json:"input_digest"`
	DeadlineMS   int64  `json:"deadline_ms"`
}
type CartRunner interface {
	Cart(context.Context, Envelope, Execution, string, json.RawMessage, string, *CartAuthority) (json.RawMessage, error)
}
type CartResult struct {
	SchemaVersion    int    `json:"schema_version"`
	Encoding         string `json:"result_encoding"`
	ResultJSON       string `json:"result_json"`
	WriteClaimed     bool   `json:"write_claimed"`
	OrderCreated     bool   `json:"order_created"`
	PaymentAttempted bool   `json:"payment_attempted"`
	DispatchState    string `json:"dispatch_state"`
	ErrorCode        string `json:"error_code,omitempty"`
}

// Cart is only called by the owned Workflow after its tool-specific Policy
// decision. root is reserved execution tmpfs; no path comes from a tool input.
// Duplicate mutations return their durable metadata and do not enter a helper.
func (m *Manager) Cart(ctx context.Context, p Principal, x Execution, operation string, args json.RawMessage, root string) (CartResult, *CartReceipt, error) {
	if !filepath.IsAbs(root) || (cartKinds[operation] == "" && !cartReads[operation]) {
		return CartResult{}, nil, ErrScope
	}
	runner, ok := m.runner.(CartRunner)
	if !ok {
		return CartResult{}, nil, ErrUnavailable
	}
	d, _, err := m.directory(ctx)
	if err != nil {
		return CartResult{}, nil, err
	}
	registered := false
	for _, ref := range d.Connections {
		registered = registered || ref.ID == x.ConnectionID && ref.Identity == x.Identity
	}
	if !registered {
		return CartResult{}, nil, ErrUnavailable
	}
	c, err := m.connection(p, x.Identity, x.ConnectionID)
	if err != nil {
		return CartResult{}, nil, err
	}
	var receipt *CartReceipt
	var control *CartControl
	var intent CartIntent
	var decoded CartResult
	output, err := c.invoke(ctx, x, operation, args, func(ctx context.Context, e Envelope) (json.RawMessage, error) {
		var authority *CartAuthority
		if kind := cartKinds[operation]; kind != "" {
			control, err = NewCartControl(m.vault, m.deployment, x.Identity.MemberDigest)
			if err != nil {
				return nil, err
			}
			control.now = m.now
			input := digest(map[string]any{"operation": operation, "arguments": args})
			// Identical content within this execution retains one key even when the
			// model repeats a tool call. Quantity belongs in the frozen item draft.
			intent = CartIntent{Identity: x.Identity, ExecutionID: x.ExecutionID, OperationKey: "cart-" + digest([]any{x.Identity, x.ExecutionID, input}), InputDigest: input, Kind: kind, DeadlineMS: x.DeadlineMS}
			var context struct {
				Request struct {
					PickupAt string `json:"pickup_at"`
				} `json:"request"`
			}
			if json.Unmarshal(args, &context) == nil {
				if at, err := time.Parse(time.RFC3339, context.Request.PickupAt); err == nil {
					intent.PickupAtMS = at.UnixMilli()
				}
			}
			r, dispatch, claimErr := control.Begin(ctx, intent)
			receipt = &r
			if claimErr != nil {
				return nil, claimErr
			}
			if !dispatch {
				return json.RawMessage(`{}`), nil
			}
			authority = &CartAuthority{intent.OperationKey, input, x.DeadlineMS}
		}
		raw, callErr := runner.Cart(ctx, e, x, operation, args, root, authority)
		if callErr != nil {
			return nil, callErr
		}
		if len(raw) > 160<<10 || appstream.DecodeStrict(raw, &decoded) != nil || decoded.SchemaVersion != 1 || decoded.Encoding != "bounded-json-v1" || len(decoded.ResultJSON) > 64<<10 || decoded.OrderCreated || decoded.PaymentAttempted {
			clear(raw)
			return nil, ErrInvalid
		}
		if decoded.DispatchState != "dispatched" && decoded.DispatchState != "unknown" && decoded.DispatchState != "not_dispatched" || decoded.DispatchState == "not_dispatched" && decoded.WriteClaimed {
			clear(raw)
			return nil, ErrInvalid
		}
		if control != nil && decoded.DispatchState == "not_dispatched" && decoded.ErrorCode != "" {
			r, resolveErr := control.NotDispatched(ctx, intent, digest(decoded))
			if resolveErr != nil {
				clear(raw)
				return nil, resolveErr
			}
			receipt = &r
		}
		var body struct {
			OperationID           string `json:"operation_id"`
			State                 string `json:"state"`
			CartMatchesRequested  bool   `json:"cart_matches_requested"`
			OriginalClearVerified bool   `json:"original_clear_verified"`
		}
		if json.Unmarshal([]byte(decoded.ResultJSON), &body) != nil {
			clear(raw)
			return nil, ErrInvalid
		}
		matched := body.State == "MATCHED" && body.CartMatchesRequested || operation == "cart_clear" && body.OriginalClearVerified || operation == "checkout_quote_pickup" && body.State == "READY"
		if control != nil && matched {
			r, confirmErr := control.Confirm(ctx, intent, digest(decoded.ResultJSON))
			if confirmErr != nil {
				clear(raw)
				return nil, confirmErr
			}
			receipt = &r
		}
		if operation == "cart_add_status" && body.State == "MATCHED" && body.CartMatchesRequested || operation == "checkout_quote_pickup_status" && body.State == "READY" {
			control, err := NewCartControl(m.vault, m.deployment, x.Identity.MemberDigest)
			if err != nil {
				clear(raw)
				return nil, err
			}
			control.now = m.now
			if err = control.ConfirmStatus(ctx, x.Identity, x.ExecutionID, body.OperationID, digest(decoded.ResultJSON)); err != nil {
				clear(raw)
				return nil, err
			}
		}
		return raw, nil
	})
	clear(output)
	return decoded, receipt, err
}
