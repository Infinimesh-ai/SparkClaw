package agent

import (
	"encoding/json"
	"errors"
	"maps"
	"sort"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type mcdCouponChoice struct {
	line            int
	ref, name       string
	price           int64
	max             int
	product, detail map[string]any
}

func (s *mcdOrderStage) offer() error {
	// Coupons are optional savings. Every coupon read must still leave room for
	// the coupon cart, its no-coupon requote and the required payment path;
	// otherwise the order continues at menu prices instead of failing later.
	costs := s.runtime.tools.MCDRequestCosts()
	potential := len(s.state.Lines)
	affordable, err := s.mcdAffords(costs.Read("coupons_list", false), potential)
	if err != nil || !affordable {
		return err
	}
	out, err := s.read("coupons_list", nil)
	if err != nil {
		return err
	}
	choices := []mcdCouponChoice{}
	uses := map[string]int{}
	for _, coupon := range mcdRows(out["coupons"]) {
		if coupon["available_for_request"] != true || coupon["configuration_path"] != "coupon_products" {
			continue
		}
		ref, name := mcdText(coupon["coupon_ref"]), mcdText(coupon["name"])
		if ref == "" || name == "" {
			return errors.New("mcd_coupon_identity_missing")
		}
		remaining := int(min(mcdInt(coupon["remaining_today"]), mcdInt(coupon["remaining_total"])))
		if remaining <= 0 {
			continue
		}
		affordable, err := s.mcdAffords(costs.Read("coupon_products", false), potential)
		if err != nil {
			return err
		}
		if !affordable {
			break
		}
		uses[ref] = remaining
		products, err := s.read("coupon_products", map[string]any{"coupon_ref": ref})
		if err != nil {
			return err
		}
		for _, product := range mcdRows(products["products"]) {
			if product["cart_configuration_supported"] != true {
				continue
			}
			cents, priceErr := mcdExtraPrice(product["display_price_cny"])
			if priceErr != nil {
				continue
			}
			for i, line := range s.state.Lines {
				if product["product_code"] != line.Product["product_code"] {
					continue
				}
				extra, err := mcdSelectionExtraCents(&line)
				if err != nil {
					return err
				}
				price := cents + extra
				if price >= line.MenuCents {
					continue
				}
				affordable, err := s.mcdAffords(costs.Read("product_detail", true), potential)
				if err != nil {
					return err
				}
				if !affordable {
					continue
				}
				detail, err := s.read("product_detail", map[string]any{"product_code": product["product_code"], "offer_id": product["offer_id"], "coupon_ref": ref})
				if err != nil {
					return err
				}
				if detail["product_code"] != line.Product["product_code"] || detail["offer_id"] != product["offer_id"] || !mcdSameConfiguration(line.Detail, detail) {
					continue
				}
				maxQuantity := int(min(mcdInt(product["max_quantity_before_detail_check"]), mcdInt(detail["max_quantity"])))
				if maxQuantity > 0 {
					choices = append(choices, mcdCouponChoice{i, ref, name, price, maxQuantity, product, detail})
				}
			}
		}
	}
	regular := s.state.Lines
	s.state.Lines = mcdAllocateCoupons(s.state.Lines, choices, uses)
	affordable, err = s.mcdAffords(0, 0)
	if err != nil {
		return err
	}
	if len(s.state.Lines) > 20 || !affordable {
		// The allocated split does not fit the cart or request aggregate.
		s.state.Lines = regular
	}
	return nil
}
func mcdSameConfiguration(a, b map[string]any) bool {
	if len(mcdStrings(b["configuration_issues"])) > 0 {
		return false
	}
	for _, key := range []string{"product_type", "groups", "modifiers", "standard_configuration_supported"} {
		x, _ := json.Marshal(a[key])
		y, _ := json.Marshal(b[key])
		if string(x) != string(y) {
			return false
		}
	}
	return true
}
func mcdAllocateCoupons(lines []app.MCDOrderLine, choices []mcdCouponChoice, uses map[string]int) []app.MCDOrderLine {
	sort.SliceStable(choices, func(i, j int) bool {
		return lines[choices[i].line].MenuCents-choices[i].price > lines[choices[j].line].MenuCents-choices[j].price
	})
	remaining := make([]int, len(lines))
	for i, l := range lines {
		remaining[i] = l.Quantity
	}
	selected := map[int][]app.MCDOrderLine{}
	for _, choice := range choices {
		q := min(remaining[choice.line], uses[choice.ref], choice.max)
		if q <= 0 {
			continue
		}
		line := lines[choice.line]
		line.Quantity = q
		line.Selection = mcdClone(line.RegularSelection)
		line.Selection["quantity"] = q
		line.Selection["offer_id"] = choice.product["offer_id"]
		line.Selection["coupon_ref"] = choice.ref
		line.CouponName = choice.name
		line.CouponCents = choice.price
		line.RegularSelection = mcdClone(line.RegularSelection)
		line.RegularSelection["quantity"] = q
		selected[choice.line] = append(selected[choice.line], line)
		remaining[choice.line] -= q
		uses[choice.ref] -= q
	}
	result := []app.MCDOrderLine{}
	for i, line := range lines {
		result = append(result, selected[i]...)
		if remaining[i] > 0 {
			line.Quantity = remaining[i]
			line.Selection = mcdClone(line.RegularSelection)
			line.Selection["quantity"] = line.Quantity
			line.RegularSelection = mcdClone(line.Selection)
			result = append(result, line)
		}
	}
	return result
}

// mcdCouponQuoteValid checks the official quote against the frozen plan.
// Coupon list titles and quote coupon names are different upstream fields, so
// names are display-only. The pinned quote binds each coupon to its cart line
// and exposes that as coupon_fingerprint; the planned coupon lines must match
// those quote lines by product and quantity, and the coupons must save money.
func mcdCouponQuoteValid(lines []app.MCDOrderLine, quote map[string]any, limit int64) bool {
	amount := mcdInt(quote["amount_cents"])
	regular := int64(0)
	expected, actual := map[string]int64{}, map[string]int64{}
	for _, line := range lines {
		regular += line.MenuCents * int64(line.Quantity)
		if line.CouponName != "" {
			expected[mcdText(line.Selection["product_code"])] += int64(line.Quantity)
		}
	}
	for _, item := range mcdRows(quote["items"]) {
		switch fingerprint := item["coupon_fingerprint"].(type) {
		case nil:
		case string:
			if fingerprint == "" {
				return false
			}
			actual[mcdText(item["product_code"])] += mcdInt(item["quantity"])
		default:
			return false
		}
	}
	if !maps.Equal(expected, actual) {
		return false
	}
	return amount > 0 && amount <= limit && (len(expected) == 0 || mcdInt(quote["discount_cents"]) > 0 && amount < regular)
}
func (s *mcdOrderStage) quote() error {
	for attempt := 0; attempt < 2; attempt++ {
		args := s.contextArgs()
		args["operation"] = "checkout_quote"
		args["source_quote_ref"] = s.state.SourceQuoteRef
		s.state.QuoteCount++
		out, err := s.tool("mcd.quote.prepare", args)
		if err != nil {
			return err
		}
		if out["state"] != "prepared" {
			return errors.New("mcd_quote_not_prepared")
		}
		quote := mcdMap(out["quote"])
		s.state.Quote = quote
		if !mcdQuoteMatchesPlan(s.state, quote) {
			return errors.New("mcd_quote_plan_mismatch")
		}
		if mcdCouponQuoteValid(s.state.Lines, quote, s.state.MaxOrderCents) {
			if amount := mcdInt(quote["amount_cents"]); amount > s.state.LimitCents {
				s.state.StopReply = "按当前餐单最低需 " + mcdYuan(amount) + " 元，超出上限 " + mcdYuan(s.state.LimitCents) + " 元，未下单"
			}
			return nil
		}
		hasCoupons := false
		for _, l := range s.state.Lines {
			hasCoupons = hasCoupons || l.CouponName != ""
		}
		if hasCoupons && s.state.QuoteCount < 2 {
			aborted, err := s.tool("mcd.quote.prepare", map[string]any{"operation": "checkout_abort"})
			if err != nil || aborted["non_dispatch_proven"] != true {
				return errors.New("mcd_abort_unconfirmed")
			}
			s.state.Quote = nil
			for i := range s.state.Lines {
				l := &s.state.Lines[i]
				l.Selection = mcdClone(l.RegularSelection)
				l.CouponName = ""
				l.CouponCents = 0
			}
			if err = s.cart(true); err != nil {
				return err
			}
			continue
		}
		if amount := mcdInt(quote["amount_cents"]); amount > s.state.LimitCents {
			s.state.StopReply = "按当前餐单最低需 " + mcdYuan(amount) + " 元，超出上限 " + mcdYuan(s.state.LimitCents) + " 元，未下单"
			return nil
		}
		return errors.New("mcd_quote_discount_unverified")
	}
	return errors.New("mcd_quote_limit")
}
func mcdQuoteMatchesPlan(state *app.MCDOrderState, quote map[string]any) bool {
	if state.CartItems != nil && !mcdSameCartConfiguration(state.CartItems, mcdRows(quote["items"])) {
		return false
	}
	if quote["store_code"] != state.Store["store_code"] || quote["pickup_at"] != state.PickupAt || quote["eat_type"] != state.EatType {
		return false
	}
	// Upstream already compares exact configured cart and quote. Also keep an
	// independent product/quantity fence at the Workflow-to-approval boundary.
	expected := map[string]int64{}
	actual := map[string]int64{}
	for _, l := range state.Lines {
		expected[mcdText(l.Selection["product_code"])] += int64(l.Quantity)
	}
	for _, item := range mcdRows(quote["items"]) {
		code := mcdText(item["product_code"])
		if code == "" || mcdInt(item["quantity"]) <= 0 {
			return false
		}
		actual[code] += mcdInt(item["quantity"])
	}
	x, _ := json.Marshal(expected)
	y, _ := json.Marshal(actual)
	return string(x) == string(y)
}

func mcdSameCartConfiguration(a, b []map[string]any) bool {
	withoutAmounts := func(items []map[string]any) []map[string]any {
		if items == nil {
			return nil
		}
		out := make([]map[string]any, 0, len(items))
		for _, item := range items {
			copy := mcdClone(item)
			delete(copy, "amount_cents")
			out = append(out, copy)
		}
		return out
	}
	return mcdSameCartItems(withoutAmounts(a), withoutAmounts(b))
}
