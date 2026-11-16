package agent

import (
	"encoding/json"
	"errors"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *mcdOrderStage) cart(requote bool) error {
	if !requote {
		// The pickup re-read, when needed, is already inside the reserve. Then
		// check the whole required remainder before the first write, so an order
		// that cannot finish within the aggregate never touches the cart.
		if err := s.mcdEnsurePickupFresh(); err != nil || s.state.StopReply != "" {
			return err
		}
		s.pickupChecked = true
		affordable, err := s.mcdAffords(0, 0)
		if err != nil {
			return err
		}
		if !affordable {
			s.state.StopReply = mcdBudgetExceeded
			return nil
		}
	}
	args := s.contextArgs()
	args["operation"] = "cart_snapshot"
	snapshot, err := s.tool("mcd.cart.read", args)
	if err != nil {
		return err
	}
	cart := mcdMap(snapshot["result"])
	items, exists := cart["items"]
	if !exists {
		return errors.New("mcd_cart_snapshot_missing")
	}
	same := mcdCartMatches(s.state.Lines, mcdRows(items))
	clearConfirmed := requote && mcdSameCartItems(s.state.CartItems, mcdRows(items))
	if requote && !clearConfirmed {
		return errors.New("mcd_cart_changed_before_coupon_fallback")
	}
	if s.state.PendingKind == "clear_cart" && s.state.Answers != nil {
		clearConfirmed = s.answer("cart", "clear_cart") == "clear" && mcdSameCartItems(mcdRows(s.state.Candidates["snapshot"]["items"]), mcdRows(items))
		s.consumeAnswers()

	}
	if !same && len(mcdRows(items)) > 0 && !clearConfirmed {
		field := app.ApprovalInputField{Name: "clear_cart", Label: "购物车已有餐品，请确认是否清空", Type: app.ApprovalInputSelect, Required: true, Options: []app.ApprovalInputOption{{Value: "clear", Label: "清空并继续"}, {Value: mcdCancelOption, Label: "取消点餐"}}}
		return s.ask("clear_cart", []app.ApprovalInputEntry{mcdEntry("cart", "确认清空已有购物车", field)}, map[string]map[string]any{"snapshot": cart})
	}
	if !same && len(mcdRows(items)) > 0 {
		args = s.contextArgs()
		args["operation"] = "cart_clear"
		args["cart_ref"] = cart["cart_ref"]
		if mcdText(cart["cart_ref"]) == "" {
			return errors.New("mcd_snapshot_ref_missing")
		}
		if _, err = s.tool("mcd.cart.write", args); err != nil {
			return err
		}
	}
	if !same {
		planned := make([]map[string]any, 0, len(s.state.Lines))
		for _, l := range s.state.Lines {
			planned = append(planned, mcdCartSelection(l.Selection))
		}
		args = s.contextArgs()
		args["operation"] = "cart_add_configured"
		args["items"] = planned
		added, addErr := s.tool("mcd.cart.write", args)
		if addErr != nil {
			return addErr
		}
		verified := mcdMap(added["result"])
		if verified["state"] != "MATCHED" || verified["cart_matches_requested"] != true || verified["write_receipt_verified"] != true {
			return errors.New("mcd_cart_plan_not_confirmed")
		}
		s.state.CartItems = mcdRows(verified["items"])
	} else {
		s.state.CartItems = mcdRows(items)
	}
	args = s.contextArgs()
	args["operation"] = "cart_snapshot"
	snapshot, err = s.tool("mcd.cart.read", args)
	if err != nil {
		return err
	}
	cart = mcdMap(snapshot["result"])
	if !mcdSameCartItems(s.state.CartItems, mcdRows(cart["items"])) {
		return errors.New("mcd_cart_changed_after_plan_verification")
	}
	// The pinned cart already supplies verified line subtotals. The upstream
	// quote rejects an account-ceiling overflow before returning a quote, so
	// report that known policy blocker here without increasing its authority.
	total := int64(0)
	for _, item := range s.state.CartItems {
		amount, ok := mcdCartInteger(item["amount_cents"])
		if !ok || amount < 0 {
			return errors.New("mcd_cart_amount_unverified")
		}
		if amount > s.state.MaxOrderCents-total {
			s.state.StopReply = mcdUserActionReason("policy_order_amount_exceeded")
			return nil
		}
		total += amount
	}
	if mcdText(cart["cart_ref"]) == "" {
		return errors.New("mcd_snapshot_ref_missing")
	}
	args = s.contextArgs()
	args["operation"] = "checkout_quote_pickup"
	args["cart_ref"] = cart["cart_ref"]
	picked, err := s.tool("mcd.cart.write", args)
	if err != nil {
		return err
	}
	picked = mcdMap(picked["result"])
	if picked["state"] != "READY" || mcdText(picked["quote_id"]) == "" {
		return errors.New("mcd_pickup_not_confirmed")
	}
	s.state.SourceQuoteRef = mcdText(picked["quote_id"])
	s.state.CartReady = true
	return nil
}
func mcdSameCartItems(a, b []map[string]any) bool {
	if a == nil || b == nil {
		return false
	}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
