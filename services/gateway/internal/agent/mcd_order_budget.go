package agent

import "time"

const mcdBudgetExceeded = "未下单：本单所需查询超出单次点餐额度，请减少餐品后重试"

// Favorites may be checked before the store question; mcdAffordsStorePrefetch
// decides how many fit, and this is only an outer bound.
const mcdMaxFavoriteChecks = 5

// A defaulted pickup slot must still be comfortably in the future when the cart
// is written, so the quote and a slow payment approval do not race its start.
const mcdPickupLead = 3 * time.Minute

// mcdRequiredRequests returns the provider-request reservations the remaining
// required path still needs. extraCouponLines models coupon lines not yet in
// the plan; they add the no-coupon requote reserve, and a coupon can split only
// a line whose quantity exceeds one.
func (s *mcdOrderStage) mcdRequiredRequests(extraCouponLines int, storeChecked bool) int {
	c := s.runtime.tools.MCDRequestCosts()
	st := s.state
	total := 0
	if st.Store == nil && !storeChecked {
		total += c.Read("pickup_slots", false) + c.Read("pickup_methods", false)
	}
	coupons := extraCouponLines
	for _, line := range st.Lines {
		if line.Product == nil {
			total += c.Read("menu_list", false)
		}
		if line.Detail == nil {
			total += c.Read("product_detail", false)
		}
		if line.CouponName != "" {
			coupons++
		}
	}
	splits := 0
	for _, line := range st.Lines {
		if line.Quantity > 1 && splits < extraCouponLines {
			splits++
		}
	}
	items := len(st.Lines) + splits
	if !st.CartReady {
		// A runtime-defaulted pickup slot may be re-read before any cart write.
		if !s.pickupChecked && (st.PickupDefaulted || st.PickupAt == "" && (st.Intent == nil || st.Intent.PickupTimeText == "")) {
			total += c.Read("pickup_slots", false)
		}
		total += mcdCartPathRequests(s, items, coupons)
		if coupons > 0 {
			// Planned coupons commit the order to a possible no-coupon requote.
			total += mcdCartPathRequests(s, items, 0)
		}
	}
	// An approved-quote expiry refresh is best effort: it uses whatever is left,
	// and settle reports an unrefreshable expired quote as not ordered.
	return total
}

func mcdCartPathRequests(s *mcdOrderStage, items, coupons int) int {
	c := s.runtime.tools.MCDRequestCosts()
	return 2*c.Cart("cart_snapshot", 0, 0) + c.Cart("cart_clear", 0, 0) + c.Cart("cart_add_configured", max(items, 1), coupons) + c.Cart("checkout_quote_pickup", 0, 0) + c.Quote(true)
}

// mcdAffords reports whether an optional read costing cost requests still
// leaves room for the required remainder. It reads the durable aggregate and
// never reserves, so a false answer has no side effect.
func (s *mcdOrderStage) mcdAffords(cost, extraCouponLines int) (bool, error) {
	remaining, err := s.runtime.tools.MCDRequestsRemaining(s.ctx)
	if err != nil {
		return false, err
	}
	return remaining >= cost+s.mcdRequiredRequests(extraCouponLines, false), nil
}

// mcdAffordsStorePrefetch admits one favorite pre-check only while a later
// single-coupon probe, its possible requote and the required path still fit.
// The pre-check itself stands in for the selected store's check.
func (s *mcdOrderStage) mcdAffordsStorePrefetch() (bool, error) {
	remaining, err := s.runtime.tools.MCDRequestsRemaining(s.ctx)
	if err != nil {
		return false, err
	}
	c := s.runtime.tools.MCDRequestCosts()
	check := c.Read("pickup_slots", false) + c.Read("pickup_methods", false)
	probe := c.Read("coupons_list", false) + c.Read("coupon_products", false) + c.Read("product_detail", true)
	return remaining >= check+probe+s.mcdRequiredRequests(1, true), nil
}
