package mcddining

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// These maxima follow the pinned Python provider's HTTP call graph, including
// its initial member wallet check, context reads and mutation readback. They
// reserve within the existing execution aggregate; unused requests are never
// refunded. Unknown inputs retain the caller's previous conservative budget.
func readRequestBudget(input Input, fallback int) int {
	switch input.Operation {
	case "membership_assets", "stores_favorites", "cities_list", "store_get":
		return 2
	case "stores_search":
		if len(input.CityCode) == 6 && strings.Trim(input.CityCode, "0123456789") == "" {
			return 2
		}
		return 3 // A city name requires a cities lookup before the store search.
	case "pickup_slots":
		return 3
	case "pickup_methods", "menu_list", "cart_get", "coupons_list":
		return 4
	case "coupon_products":
		return 5
	case "product_detail":
		if input.CouponRef != "" {
			return 8
		}
		return 7 // Includes the optional two card-variant evidence reads.
	default:
		return fallback
	}
}

func cartRequestBudget(input CartInput, fallback int) int {
	switch input.Operation {
	case "cart_snapshot", "cart_add_status":
		return 4
	case "cart_clear":
		return 8
	case "cart_add_simple":
		return 9
	case "checkout_quote_pickup":
		return 14
	case "checkout_quote_pickup_status":
		return 6 // Includes recovery from a receipt not yet linked to a quote.
	case "cart_add_configured":
		if len(input.Items) < 1 || len(input.Items) > 20 {
			return fallback
		}
		// 8 fixed calls, one detail per line, one shared regular menu, two
		// coupon lookups per coupon line and two card reads per variant line.
		budget, menu := 8+len(input.Items), 0
		for _, raw := range input.Items {
			var item struct {
				CouponRef string `json:"coupon_ref"`
				VariantOf string `json:"variant_of"`
			}
			if json.Unmarshal(raw, &item) != nil || item.CouponRef != "" && item.VariantOf != "" {
				return fallback // The Python item schema rejects these before dispatch.
			}
			if item.CouponRef != "" {
				budget += 2
			} else {
				menu = 1
			}
			if item.VariantOf != "" {
				budget += 2
			}
		}
		return budget + menu
	default:
		return fallback
	}
}

func quoteRequestBudget(sourceQuoteRef string) int {
	if sourceQuoteRef != "" {
		// Stored selected-quote export is local, but Execution requires >= 1.
		return 1
	}
	return 6 // Initial wallet + four checkout reads + quote wallet balance.
}

// RequestsRemaining reports this execution's unreserved provider requests.
func (s *Selection) RequestsRemaining(ctx context.Context) (int, error) {
	if s == nil || s.manager == nil {
		return 0, errors.New("mcd_selection_unavailable")
	}
	return s.manager.RequestsRemaining(ctx, s.principal, s.execution)
}

// OrderRequestCosts exposes the same per-call reservations used above so the
// ordering Workflow can plan within the aggregate before dispatching anything.
type OrderRequestCosts struct{}

func (OrderRequestCosts) Read(op string, couponRef bool) int {
	input := Input{Operation: op}
	if couponRef {
		input.CouponRef = "coupon"
	}
	return readRequestBudget(input, 8)
}

// StoreSearch uses the city-name maximum, never the cheaper six-digit case.
func (OrderRequestCosts) StoreSearch() int {
	return readRequestBudget(Input{Operation: "stores_search"}, 8)
}

func (OrderRequestCosts) Cart(op string, items, couponItems int) int {
	if op != "cart_add_configured" {
		return cartRequestBudget(CartInput{Operation: op}, 32)
	}
	raw := make([]json.RawMessage, 0, items)
	for i := 0; i < items; i++ {
		item := map[string]any{"product_code": "1"}
		if i < couponItems {
			item["coupon_ref"] = "coupon"
		}
		encoded, _ := json.Marshal(item)
		raw = append(raw, encoded)
	}
	return cartRequestBudget(CartInput{Operation: op, Items: raw}, 32)
}

func (OrderRequestCosts) Quote(sourceQuoteRef bool) int {
	if sourceQuoteRef {
		return quoteRequestBudget("source")
	}
	return quoteRequestBudget("")
}
