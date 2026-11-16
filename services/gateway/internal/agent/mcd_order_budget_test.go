package agent

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func mcdCallCount(f *mcdFlowFixture, op string) int {
	count := 0
	for _, call := range f.calls {
		if call == op {
			count++
		}
	}
	return count
}

func mcdStoreOptions(approval app.Approval) []string {
	values := []string{}
	for _, option := range approval.InputForm.Entries[0].Fields[0].Options {
		values = append(values, option.Value)
	}
	return values
}

// Eight favorites must not spend the 128-request aggregate before the store
// question. Only the pre-checks that still leave room for a coupon probe run;
// every favorite stays listed, and the coupon still applies.
func TestMCDOrderBudgetPrechecksFavoritesWithinRoomForCoupons(t *testing.T) {
	f := &mcdFlowFixture{favorites: 8}
	r, st, session := newMCDFlowRuntime(t, f, true)
	run, first := dispatchMCDTest(t, r, st, session, app.MessageSourceWeb)
	if len(first.Approvals) != 1 {
		t.Fatalf("store form missing: %s calls=%v", first.FinalAnswer, f.calls)
	}
	options := mcdStoreOptions(first.Approvals[0])
	if len(options) != 10 || !slices.Contains(options, "fav-8") {
		t.Fatalf("favorites were dropped instead of listed: %v", options)
	}
	if checks := mcdCallCount(f, "pickup_methods"); checks != 1 {
		t.Fatalf("pre-checked %d favorites; want only what fits", checks)
	}
	result := answerMCD(t, r, st, run, first.Approvals[0], app.ApprovalInputValues{"context": {"store": "3330613"}}, false)
	answerMCD(t, r, st, result.Run, mcdPending(t, st, run.ID), app.ApprovalInputValues{"item_1": {"group_1_0": "2001"}}, false)
	payment := mcdPending(t, st, run.ID)
	if payment.Tool != "mcd.purchase.commit" || mcdInt(payment.Arguments["amount_cents"]) != 1500 {
		t.Fatalf("coupon lost to favorite pre-checks: %+v calls=%v", payment.Arguments, f.calls)
	}
	if final := answerMCD(t, r, st, run, payment, nil, false); final.Message.Content != "已下单，取餐码：A123" {
		t.Fatalf("reply=%s", final.Message.Content)
	}
}

// An unchecked favorite is checked once selected; an unsupported one returns
// to the favorite list without it, exactly like a search result.
func TestMCDOrderUncheckedFavoriteIsCheckedAfterSelection(t *testing.T) {
	f := &mcdFlowFixture{favorites: 3, unsupportedStores: map[string]bool{"fav-3": true}}
	r, st, session := newMCDFlowRuntime(t, f, true)
	run, first := dispatchMCDTest(t, r, st, session, app.MessageSourceWeb)
	if options := mcdStoreOptions(first.Approvals[0]); !slices.Contains(options, "fav-3") || !slices.Contains(options, mcdOtherStoreOption) {
		t.Fatalf("unchecked favorite not offered: %v", options)
	}
	answerMCD(t, r, st, run, first.Approvals[0], app.ApprovalInputValues{"context": {"store": "fav-3"}}, false)
	again := mcdPending(t, st, run.ID)
	options := mcdStoreOptions(again)
	if again.InputForm.Entries[0].Summary == "" || slices.Contains(options, "fav-3") || !slices.Contains(options, "3330613") {
		t.Fatalf("unsupported favorite not removed with a note: %v %+v", options, again.InputForm.Entries[0])
	}
	result := answerMCD(t, r, st, run, again, app.ApprovalInputValues{"context": {"store": "3330613"}}, false)
	answerMCD(t, r, st, result.Run, mcdPending(t, st, run.ID), app.ApprovalInputValues{"item_1": {"group_1_0": "2001"}}, false)
	if payment := mcdPending(t, st, run.ID); payment.Tool != "mcd.purchase.commit" || payment.Arguments["store_code"] != "3330613" {
		t.Fatalf("order did not continue at the supported store: %+v", payment.Arguments)
	}
}

// An order whose required reads cannot fit stops before any store question,
// provider read or cart write.
func TestMCDOrderBudgetStopsOversizedOrderBeforeQuestions(t *testing.T) {
	f := &mcdFlowFixture{items: 12}
	r, st, session := newMCDFlowRuntime(t, f, true)
	_, result := dispatchMCDTest(t, r, st, session, app.MessageSourceWeb)
	if result.FinalAnswer != mcdBudgetExceeded || len(result.Approvals) != 0 || len(f.calls) != 0 || f.models != 1 {
		t.Fatalf("reply=%s calls=%v models=%d", result.FinalAnswer, f.calls, f.models)
	}
}

// A runtime-chosen earliest slot can expire while the owner answers menu
// questions. It is re-read before the first cart write and the payment
// approval shows the new slot.
func TestMCDOrderStaleDefaultPickupIsReReadBeforeCart(t *testing.T) {
	f := &mcdFlowFixture{}
	r, st, session := newMCDFlowRuntime(t, f, true)
	f.pickup = time.Now().Add(mcdPickupLead + 1500*time.Millisecond).Format(time.RFC3339)
	run, first := dispatchMCDTest(t, r, st, session, app.MessageSourceWeb)
	answerMCD(t, r, st, run, first.Approvals[0], app.ApprovalInputValues{"context": {"store": "3330613"}}, false)
	config := mcdPending(t, st, run.ID)
	time.Sleep(2 * time.Second)
	f.pickup = time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	answerMCD(t, r, st, run, config, app.ApprovalInputValues{"item_1": {"group_1_0": "2001"}}, false)
	payment := mcdPending(t, st, run.ID)
	if payment.Tool != "mcd.purchase.commit" || payment.Arguments["pickup_at"] != f.pickup || mcdCallCount(f, "pickup_slots") != 2 {
		t.Fatalf("stale slot not replaced: %+v calls=%v", payment.Arguments, f.calls)
	}
}

func TestMCDOrderOwnerChosenPickupIsNeverReplaced(t *testing.T) {
	s := mcdOrderStage{state: &app.MCDOrderState{PickupAt: time.Now().Add(time.Minute).Format(time.RFC3339)}}
	if err := s.mcdEnsurePickupFresh(); err != nil || !strings.Contains(s.state.StopReply, "所选取餐时间已过") {
		t.Fatalf("owner time silently replaced: %v %+v", err, s.state)
	}
}

// When a coupon requote already used the remaining requests, an expired
// approved quote cannot refresh. Nothing was dispatched, so the reply says the
// order was not placed instead of reporting an unconfirmed payment.
func TestMCDOrderExpiredQuoteWithoutRefreshIsReportedNotOrdered(t *testing.T) {
	f := &mcdFlowFixture{discountMissing: true, quoteTTL: 250 * time.Millisecond, ttlQuote: 2}
	r, st, session := newMCDFlowRuntime(t, f, true)
	ready := mcdThroughConfiguration(t, r, st, session)
	approval := mcdPending(t, st, ready.Run.ID)
	if f.quotes != 2 || approval.Tool != "mcd.purchase.commit" {
		t.Fatalf("coupon fallback approval missing: quotes=%d", f.quotes)
	}
	time.Sleep(time.Until(time.UnixMilli(f.quote.ExpiresAtMS).Add(time.Millisecond)))
	final := answerMCD(t, r, st, ready.Run, approval, nil, false)
	if final.Message.Content != "未下单：报价已过期，请重新点餐" || f.commits != 0 || f.statuses != 0 {
		t.Fatalf("reply=%s commits=%d statuses=%d", final.Message.Content, f.commits, f.statuses)
	}
}

// A verified PAID receipt is never reported as an unconfirmed payment, even
// before the official order number or pickup code exists.
func TestMCDTerminalReplyKeepsVerifiedPaidWithoutIdentifiers(t *testing.T) {
	for _, tc := range []struct {
		status map[string]any
		want   string
		ok     bool
	}{
		{map[string]any{"terminal_verified": true, "state": "PAID", "pickup_code": "A1"}, "已下单，取餐码：A1", true},
		{map[string]any{"terminal_verified": true, "state": "PAID", "order_no": "o-1"}, "已下单，订单号：o-1，取餐码生成后可在麦当劳 App 查看", true},
		{map[string]any{"terminal_verified": true, "state": "PAID"}, "已下单，取餐码生成后可在麦当劳 App 查看", true},
		{map[string]any{"terminal_verified": true, "state": "CANCELLED"}, "订单已取消，未扣款", true},
		{map[string]any{"state": "PAID", "pickup_code": "A1"}, "", false},
		{map[string]any{"terminal_verified": true, "state": "UNKNOWN"}, "", false},
	} {
		if got, ok := mcdTerminalReply(tc.status); got != tc.want || ok != tc.ok {
			t.Errorf("mcdTerminalReply(%v) = %q/%t; want %q/%t", tc.status, got, ok, tc.want, tc.ok)
		}
	}
}
