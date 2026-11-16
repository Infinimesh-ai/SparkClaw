package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func mcdThroughConfiguration(t *testing.T, r Runtime, st *store.MemoryStore, session app.Session) Result {
	t.Helper()
	run, first := dispatchMCDTest(t, r, st, session, app.MessageSourceWeb)
	if len(first.Approvals) != 1 {
		t.Fatalf("store form missing: %s", first.FinalAnswer)
	}
	result := answerMCD(t, r, st, run, first.Approvals[0], app.ApprovalInputValues{"context": {"store": "3330613"}}, false)
	return answerMCD(t, r, st, result.Run, mcdPending(t, st, run.ID), app.ApprovalInputValues{"item_1": {"group_1_0": "2001"}}, false)
}

func TestMCDOrderExpiredQuoteReapprovesChangedPriceOnly(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "identical", true: "lower price still needs approval"}[changed], func(t *testing.T) {
			f := &mcdFlowFixture{quoteTTL: 250 * time.Millisecond, changedPrice: changed}
			r, st, session := newMCDFlowRuntime(t, f, true)
			ready := mcdThroughConfiguration(t, r, st, session)
			approval := mcdPending(t, st, ready.Run.ID)
			time.Sleep(time.Until(time.UnixMilli(f.quote.ExpiresAtMS).Add(time.Millisecond)))
			result := answerMCD(t, r, st, ready.Run, approval, nil, false)
			if changed {
				if f.commits != 0 {
					t.Fatal("changed price reused original approval")
				}
				next := mcdPending(t, st, ready.Run.ID)
				if mcdInt(next.Arguments["amount_cents"]) != 1400 || next.Arguments["purchase_digest"] == approval.Arguments["purchase_digest"] || !strings.Contains(next.Summary, "4.00") {
					t.Fatalf("fresh quote not bound to renewed approval: %+v", next)
				}
				result = answerMCD(t, r, st, result.Run, next, nil, false)
			}
			if f.commits != 1 || f.quotes != 2 || f.aborts != 1 || result.Message.Content != "已下单，取餐码：A123" {
				t.Fatalf("reply=%s commits=%d quotes=%d aborts=%d", result.Message.Content, f.commits, f.quotes, f.aborts)
			}
		})
	}
}

func TestMCDOrderCouponFallbackAndOwnerLimit(t *testing.T) {
	for _, tc := range []struct {
		name, limit string
		missing     bool
		quotes      int
		pay         bool
		amount      int64
	}{{"coupon absent requotes once", "", true, 2, true, 1800}, {"owner limit stops cheapest plan", "10元", false, 1, false, 1500}, {"fallback remains over limit", "16元", true, 2, false, 1800}} {
		t.Run(tc.name, func(t *testing.T) {
			f := &mcdFlowFixture{discountMissing: tc.missing, maxAmountText: tc.limit}
			r, st, session := newMCDFlowRuntime(t, f, true)
			result := mcdThroughConfiguration(t, r, st, session)
			if f.quotes != tc.quotes || f.commits != 0 || f.models != 2 {
				t.Fatalf("quotes=%d commits=%d models=%d reply=%s", f.quotes, f.commits, f.models, result.Message.Content)
			}
			if tc.pay {
				approval := mcdPending(t, st, result.Run.ID)
				if approval.Tool != "mcd.purchase.commit" || mcdInt(approval.Arguments["amount_cents"]) != tc.amount {
					t.Fatalf("wrong fallback approval: %+v", approval)
				}
			} else if !strings.Contains(result.Message.Content, mcdYuan(tc.amount)) || !strings.Contains(result.Message.Content, "超出上限") || f.aborts != tc.quotes {
				t.Fatalf("amount refusal/abort mismatch: %s aborts=%d", result.Message.Content, f.aborts)
			}
		})
	}
}

func TestMCDOrderPaymentSettlementIsBoundedAndNeverRetries(t *testing.T) {
	for _, tc := range []struct {
		name    string
		f       mcdFlowFixture
		want    string
		queries int
	}{{"unknown", mcdFlowFixture{unknown: true}, mcdUnknown, 3}, {"paid awaiting code", mcdFlowFixture{noPickupCode: true}, "已下单，订单号：order-1，取餐码生成后可在麦当劳 App 查看", 3}, {"cancelled", mcdFlowFixture{cancelled: true}, "订单已取消，未扣款", 1}} {
		t.Run(tc.name, func(t *testing.T) {
			f := &tc.f
			r, st, session := newMCDFlowRuntime(t, f, true)
			ready := mcdThroughConfiguration(t, r, st, session)
			final := answerMCD(t, r, st, ready.Run, mcdPending(t, st, ready.Run.ID), nil, false)
			if final.Message.Content != tc.want || f.commits != 1 || f.statuses != tc.queries || f.quotes != 1 || f.aborts != 0 {
				t.Fatalf("reply=%s commits=%d status=%d quote=%d abort=%d", final.Message.Content, f.commits, f.statuses, f.quotes, f.aborts)
			}
			if f.unknown {
				_, next := dispatchMCDTest(t, r, st, session, app.MessageSourceWeb)
				if !strings.Contains(next.FinalAnswer, "上一单付款结果尚未确认") || f.commits != 1 || f.models != 2 || f.statuses != 4 {
					t.Fatalf("unresolved order allowed a replacement: %s", next.FinalAnswer)
				}
			}
		})
	}
}

func TestMCDOrderRejectFormOrPayment(t *testing.T) {
	for _, payment := range []bool{false, true} {
		t.Run(map[bool]string{false: "question", true: "payment"}[payment], func(t *testing.T) {
			f := &mcdFlowFixture{}
			r, st, session := newMCDFlowRuntime(t, f, true)
			var run app.AgentRun
			if payment {
				run = mcdThroughConfiguration(t, r, st, session).Run
			} else {
				run, _ = dispatchMCDTest(t, r, st, session, app.MessageSourceWeb)
			}
			final := answerMCD(t, r, st, run, mcdPending(t, st, run.ID), nil, true)
			if final.Message.Content != mcdCancelled || f.commits != 0 || payment && f.aborts != 1 || !payment && len(f.cart) != 0 {
				t.Fatalf("reject=%v reply=%s commits=%d aborts=%d", payment, final.Message.Content, f.commits, f.aborts)
			}
		})
	}
}

func TestMCDOrderExistingCartClearRequiresBoundConfirmation(t *testing.T) {
	f := &mcdFlowFixture{cart: []map[string]any{{"product_code": "other", "quantity": 1}}}
	r, st, session := newMCDFlowRuntime(t, f, true)
	ready := mcdThroughConfiguration(t, r, st, session)
	clear := mcdPending(t, st, ready.Run.ID)
	if clear.InputForm == nil || clear.InputForm.Entries[0].Fields[0].Name != "clear_cart" {
		t.Fatal("clear confirmation missing")
	}
	// A concurrent cart edit invalidates the approval's original snapshot.
	f.cart = []map[string]any{{"product_code": "changed", "quantity": 1}}
	ready = answerMCD(t, r, st, ready.Run, clear, app.ApprovalInputValues{"cart": {"clear_cart": "clear"}}, false)
	for _, op := range f.calls {
		if op == "cart_clear" {
			t.Fatal("stale confirmation cleared another cart")
		}
	}
	clear = mcdPending(t, st, ready.Run.ID)
	ready = answerMCD(t, r, st, ready.Run, clear, app.ApprovalInputValues{"cart": {"clear_cart": "clear"}}, false)
	if mcdPending(t, st, ready.Run.ID).Tool != "mcd.purchase.commit" {
		t.Fatal("confirmed current cart did not continue")
	}
	count := 0
	for _, op := range f.calls {
		if op == "cart_clear" {
			count++
		}
	}
	if count != 1 || f.commits != 0 {
		t.Fatalf("clears=%d commits=%d", count, f.commits)
	}
}

func TestMCDOrderOfflineWithoutOriginalPurchaseStopsBeforeModel(t *testing.T) {
	f := &mcdFlowFixture{offline: true}
	r, st, session := newMCDFlowRuntime(t, f, true)
	_, result := dispatchMCDTest(t, r, st, session, app.MessageSourceWeb)
	if result.FinalAnswer != mcdUnavailable || f.models != 0 || len(f.calls) != 0 || f.commits != 0 {
		t.Fatalf("offline entry=%s models=%d calls=%v", result.FinalAnswer, f.models, f.calls)
	}
}

func TestMCDOrderAccountCeilingStopsBeforeUnquotablePickup(t *testing.T) {
	f := &mcdFlowFixture{policyMaxCents: 1000}
	r, st, session := newMCDFlowRuntime(t, f, true)
	result := mcdThroughConfiguration(t, r, st, session)
	if !strings.Contains(result.Message.Content, "下单限额") || f.quotes != 0 || f.commits != 0 {
		t.Fatalf("policy refusal=%s quotes=%d commits=%d", result.Message.Content, f.quotes, f.commits)
	}
	for _, op := range f.calls {
		if op == "checkout_quote_pickup" {
			t.Fatal("account ceiling overflow entered pickup quote")
		}
	}
}

func TestMCDOrderNoFavoritesSearchesWithinOriginalExecution(t *testing.T) {
	f := &mcdFlowFixture{noFavorites: true}
	r, st, session := newMCDFlowRuntime(t, f, true)
	run, first := dispatchMCDTest(t, r, st, session, app.MessageSourceWeb)
	if len(first.Approvals) != 1 {
		t.Fatalf("no city form: %s", first.FinalAnswer)
	}
	city := first.Approvals[0]
	var cityField string
	for _, field := range city.InputForm.Entries[0].Fields {
		if field.Type == app.ApprovalInputSelect {
			cityField = field.Name
			break
		}
	}
	ready := answerMCD(t, r, st, run, city, app.ApprovalInputValues{"context": {cityField: "330100", "store_keyword": "测试路"}}, false)
	store := mcdPending(t, st, run.ID)
	if store.InputForm.Entries[0].Fields[0].Name != "store" || ready.Run.ID != run.ID {
		t.Fatal("search did not produce same-run store confirmation")
	}
}

func TestMCDOrderStoreSearchHasThreeSearchLimit(t *testing.T) {
	f := &mcdFlowFixture{noFavorites: true}
	r, st, session := newMCDFlowRuntime(t, f, true)
	run, _ := dispatchMCDTest(t, r, st, session, app.MessageSourceWeb)
	var result Result
	for i := 0; i < 3; i++ {
		result = answerMCD(t, r, st, run, mcdPending(t, st, run.ID), app.ApprovalInputValues{"context": {"city": "330100", "store_keyword": "测试路"}}, false)
		result = answerMCD(t, r, st, result.Run, mcdPending(t, st, run.ID), app.ApprovalInputValues{"context": {"store": mcdOtherStoreOption}}, false)
		run = result.Run
	}
	searches := 0
	for _, op := range f.calls {
		if op == "stores_search" {
			searches++
		}
	}
	if searches != 3 || result.Message.Content != "未选定门店，未下单" || f.models != 1 || f.commits != 0 || f.quotes != 0 {
		t.Fatalf("reply=%s searches=%d models=%d", result.Message.Content, searches, f.models)
	}
}

func TestMCDOrderUnsupportedStoreReturnsToStoreSelection(t *testing.T) {
	f := &mcdFlowFixture{noFavorites: true, unsupported: true}
	r, st, session := newMCDFlowRuntime(t, f, true)
	run, _ := dispatchMCDTest(t, r, st, session, app.MessageSourceWeb)
	result := answerMCD(t, r, st, run, mcdPending(t, st, run.ID), app.ApprovalInputValues{"context": {"city": "330100"}}, false)
	result = answerMCD(t, r, st, result.Run, mcdPending(t, st, run.ID), app.ApprovalInputValues{"context": {"store": "3330613"}}, false)
	form := mcdPending(t, st, run.ID)
	if form.InputForm == nil || !strings.Contains(form.InputForm.Entries[0].Summary, "不支持") || f.models != 1 || f.quotes != 0 || f.commits != 0 {
		t.Fatalf("unqualified store escaped context: %+v", form)
	}
	for _, option := range form.InputForm.Entries[0].Fields[0].Options {
		if option.Value == "3330613" {
			t.Fatal("unsupported store remains selectable")
		}
	}
}
