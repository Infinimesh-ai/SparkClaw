package mcddining

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcdintegration"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestRequestBudgetsCoverPinnedProviderBranches(t *testing.T) {
	for _, tc := range []struct {
		input Input
		want  int
	}{
		{Input{Operation: "stores_favorites"}, 2}, {Input{Operation: "membership_assets"}, 2},
		{Input{Operation: "cities_list"}, 2}, {Input{Operation: "store_get"}, 2},
		{Input{Operation: "stores_search", CityCode: "330100"}, 2},
		{Input{Operation: "stores_search", CityCode: "杭州"}, 3},
		{Input{Operation: "stores_search", CityCode: "+30100"}, 3},
		{Input{Operation: "stores_search", CityCode: "３３０１００"}, 3},
		{Input{Operation: "pickup_slots"}, 3}, {Input{Operation: "pickup_methods"}, 4},
		{Input{Operation: "menu_list"}, 4}, {Input{Operation: "cart_get"}, 4},
		{Input{Operation: "coupons_list"}, 4}, {Input{Operation: "coupon_products"}, 5},
		{Input{Operation: "product_detail"}, 7}, {Input{Operation: "product_detail", CouponRef: "coupon"}, 8},
		{Input{Operation: "future_operation"}, 8},
	} {
		if got := readRequestBudget(tc.input, 8); got != tc.want {
			t.Errorf("%+v: budget %d, want %d", tc.input, got, tc.want)
		}
	}
	for op, want := range map[string]int{"cart_snapshot": 4, "cart_add_status": 4, "cart_clear": 8,
		"cart_add_simple": 9, "checkout_quote_pickup": 14, "checkout_quote_pickup_status": 6, "future_write": 32} {
		if got := cartRequestBudget(CartInput{Operation: op}, 32); got != want {
			t.Errorf("%s: budget %d, want %d", op, got, want)
		}
	}
	if quoteRequestBudget("") != 6 || quoteRequestBudget("original-selected-quote") != 1 {
		t.Fatal("ordinary quote and local export budgets changed")
	}
}

func budgetItems(count int, coupon, variant bool) []json.RawMessage {
	items := make([]json.RawMessage, count)
	for index := range items {
		item := map[string]any{"product_code": "100001", "quantity": 20, "offer_id": "offer"}
		if coupon {
			item["coupon_ref"] = "coupon-" + strings.Repeat("a", 24)
		}
		if variant {
			item["variant_of"] = "100002"
		}
		items[index], _ = json.Marshal(item)
	}
	return items
}

func TestConfiguredCartBudgetIncludesEveryLineAndReadback(t *testing.T) {
	for _, tc := range []struct {
		name  string
		items []json.RawMessage
		want  int
	}{
		{"one ordinary", budgetItems(1, false, false), 10},
		{"one coupon", budgetItems(1, true, false), 11},
		{"one variant", budgetItems(1, false, true), 12},
		{"twenty ordinary", budgetItems(20, false, false), 29},
		{"twenty coupons", budgetItems(20, true, false), 68},
		{"twenty card variants", budgetItems(20, false, true), 69},
		{"zero lines", nil, 32}, {"too many lines", budgetItems(21, false, true), 32},
		{"invalid coupon variant", budgetItems(1, true, true), 32},
		{"invalid field type", []json.RawMessage{json.RawMessage(`{"coupon_ref":42}`)}, 32},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := cartRequestBudget(CartInput{Operation: "cart_add_configured", Items: tc.items}, 32); got != tc.want {
				t.Fatalf("budget %d, want %d", got, tc.want)
			}
		})
	}
	// The ordering Workflow's real call graph: one pre-checked favorite, one
	// configured line, coupon probe, the cart path (snapshot, add, snapshot,
	// selected pickup) and the selected-quote export.
	c := OrderRequestCosts{}
	plain := c.Read("stores_favorites", false) + c.Read("pickup_slots", false) + c.Read("pickup_methods", false) +
		c.Read("menu_list", false) + c.Read("product_detail", false) + c.Read("coupons_list", false) +
		2*c.Cart("cart_snapshot", 0, 0) + c.Cart("cart_add_configured", 1, 0) + c.Cart("checkout_quote_pickup", 0, 0) + c.Quote(true)
	if plain != 57 {
		t.Fatalf("plain ordering path reserved %d requests, want 57", plain)
	}
	// A coupon adds its probe and line lookups; a failed quote check adds the
	// no-coupon requote (snapshot, clear, add, snapshot, pickup, quote).
	coupon := plain + c.Read("coupon_products", false) + c.Read("product_detail", true) + c.Cart("cart_add_configured", 1, 1) - c.Cart("cart_add_configured", 1, 0)
	requote := 2*c.Cart("cart_snapshot", 0, 0) + c.Cart("cart_clear", 0, 0) + c.Cart("cart_add_configured", 1, 0) + c.Cart("checkout_quote_pickup", 0, 0) + c.Quote(true)
	if coupon != 71 || requote != 41 || coupon+requote > 128 {
		t.Fatalf("coupon path %d + requote %d must fit the original 128 cap", coupon, requote)
	}
	// An approved-quote expiry refresh needs a snapshot, pickup and export; it
	// fits after the plain path but not after a coupon requote, which is why
	// the Workflow treats refreshes as best effort.
	refresh := c.Cart("cart_snapshot", 0, 0) + c.Cart("checkout_quote_pickup", 0, 0) + c.Quote(true)
	if refresh != 19 || plain+refresh > 128 || coupon+requote+refresh <= 128 {
		t.Fatalf("refresh %d changed its fit against the 128 cap", refresh)
	}
}

type budgetRunner struct {
	diningRunner
	observed []mcdintegration.Execution
	failRead bool
}

func (r *budgetRunner) Read(ctx context.Context, e mcdintegration.Envelope, x mcdintegration.Execution, op string, args json.RawMessage) (json.RawMessage, error) {
	r.observed = append(r.observed, x)
	if r.failRead {
		return nil, errors.New("lost helper response")
	}
	return r.selectionRunner.Read(ctx, e, x, op, args)
}
func (r *budgetRunner) Quote(ctx context.Context, e mcdintegration.Envelope, x mcdintegration.Execution, q mcdintegration.QuoteRequest, root string) (json.RawMessage, error) {
	r.observed = append(r.observed, x)
	return r.diningRunner.Quote(ctx, e, x, q, root)
}
func (r *budgetRunner) Cart(_ context.Context, _ mcdintegration.Envelope, x mcdintegration.Execution, _ string, _ json.RawMessage, _ string, _ *mcdintegration.CartAuthority) (json.RawMessage, error) {
	r.observed = append(r.observed, x)
	return nil, errors.New("lost helper response")
}

func budgetSelection(t *testing.T) (*Selection, *budgetRunner) {
	t.Helper()
	raw, err := os.ReadFile("../../../../tools/ezmcd-adapter/tests/session-envelope-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	json.Unmarshal(raw, &fields)
	fields["issued_at_ms"] = time.Now().UnixMilli() - 1000
	fields["session_not_after_ms"] = time.Now().UnixMilli() + 900000
	fields["operations"] = []string{"cart_add_configured", "checkout_quote", "stores_favorites"}
	fields["max_requests"] = 128
	delete(fields, "envelope_digest")
	fields["envelope_digest"] = hashDining(fields)
	raw, _ = json.Marshal(fields)
	e, err := mcdintegration.DecodeEnvelope(raw)
	if err != nil {
		t.Fatal(err)
	}
	runner := &budgetRunner{}
	manager, err := mcdintegration.NewManager(credential.New(store.NewMemoryStore(), credential.Options{Key: strings.Repeat("43", 32)}), runner, e.Identity.DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	i := e.Identity
	principal := mcdintegration.Principal{DeploymentID: i.DeploymentID, OwnerID: i.OwnerID, ClientID: i.ClientID, InstallationID: i.InstallationID}
	if _, err = manager.Provision(t.Context(), principal, raw); err != nil {
		t.Fatal(err)
	}
	selection, err := NewSelection(t.Context(), manager, principal, "execution")
	if err != nil {
		t.Fatal(err)
	}
	selection.WithCart(t.TempDir(), &diningHost{})
	return selection, runner
}

func TestRequestBudgetsReachHelpersWithoutChangingExecutionScope(t *testing.T) {
	for _, operation := range []string{"stores_favorites", "cart_add_configured", "checkout_quote", "selected_quote"} {
		t.Run(operation, func(t *testing.T) {
			selection, runner := budgetSelection(t)
			before, want := selection.execution, 0
			switch operation {
			case "stores_favorites":
				want = 2
				_, _ = selection.Read(t.Context(), []byte(`{"operation":"stores_favorites"}`))
			case "cart_add_configured":
				want = 69
				raw, _ := json.Marshal(CartInput{Operation: operation, Items: budgetItems(20, false, true)})
				_, _ = selection.Cart(t.Context(), raw, true)
			default:
				input := CartInput{Operation: "checkout_quote"}
				want = 6
				if operation == "selected_quote" {
					input.SourceQuoteRef, want = "original-quote", 1
				}
				raw, _ := json.Marshal(input)
				_, _ = selection.Quote(t.Context(), raw)
			}
			if len(runner.observed) != 1 || runner.observed[0].MaxRequests != want {
				t.Fatalf("helper executions %+v, want budget %d", runner.observed, want)
			}
			observed := runner.observed[0]
			observed.MaxRequests = before.MaxRequests
			if observed != before || selection.execution != before {
				t.Fatal("budget update changed original execution identity, deadline or defaults")
			}
		})
	}
}

func TestRequestBudgetStillCapsAggregateAndNeverRefundsFailedReads(t *testing.T) {
	selection, runner := budgetSelection(t)
	runner.failRead = true
	for index := 0; index < 65; index++ {
		if _, err := selection.Read(t.Context(), []byte(`{"operation":"stores_favorites"}`)); err == nil {
			t.Fatal("lost response succeeded")
		}
	}
	if len(runner.observed) != 64 {
		t.Fatalf("128-request aggregate dispatched %d two-request calls; want 64", len(runner.observed))
	}
}
