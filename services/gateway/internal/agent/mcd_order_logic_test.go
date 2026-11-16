package agent

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

const mcdLogicIntent = `{"items":[{"text":"麦辣鸡腿堡","quantity":null,"notes":"不要酱"},{"text":"可乐","quantity":2,"notes":""}],"store_text":"公司楼下","pickup_method_text":"","pickup_time_text":"","max_amount_text":""}`

func TestMCDLogicIntentSchemaRejectsInventedIdentifiersAndMalformedFields(t *testing.T) {
	intent, err := decodeMCDIntent(mcdLogicIntent)
	if err != nil || len(intent.Items) != 2 || intent.Items[0].Quantity != nil || intent.Items[1].Quantity == nil || *intent.Items[1].Quantity != 2 {
		t.Fatalf("owner intent changed: %+v, %v", intent, err)
	}
	for name, raw := range map[string]string{
		"official store ID":     strings.Replace(mcdLogicIntent, `"store_text":`, `"store_code":"invented","store_text":`, 1),
		"official product ID":   strings.Replace(mcdLogicIntent, `"text":"麦辣鸡腿堡"`, `"text":"麦辣鸡腿堡","product_code":"invented"`, 1),
		"official coupon ID":    strings.Replace(mcdLogicIntent, `"text":"麦辣鸡腿堡"`, `"text":"麦辣鸡腿堡","coupon_ref":"invented"`, 1),
		"numeric amount":        strings.Replace(mcdLogicIntent, `"max_amount_text":""`, `"max_amount_text":30`, 1),
		"zero quantity":         strings.Replace(mcdLogicIntent, `"quantity":null`, `"quantity":0`, 1),
		"fractional quantity":   strings.Replace(mcdLogicIntent, `"quantity":null`, `"quantity":1.5`, 1),
		"quantity beyond limit": strings.Replace(mcdLogicIntent, `"quantity":null`, `"quantity":21`, 1),
		"missing quantity":      strings.Replace(mcdLogicIntent, `"quantity":null,`, ``, 1),
		"missing context field": strings.Replace(mcdLogicIntent, `"pickup_time_text":"",`, ``, 1),
		"null store text":       strings.Replace(mcdLogicIntent, `"store_text":"公司楼下"`, `"store_text":null`, 1),
		"null notes":            strings.Replace(mcdLogicIntent, `"notes":"不要酱"`, `"notes":null`, 1),
		"oversize store text":   strings.Replace(mcdLogicIntent, `"store_text":"公司楼下"`, `"store_text":"`+strings.Repeat("店", 501)+`"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeMCDIntent(raw); err == nil {
				t.Fatal("strict intent accepted invalid model output")
			}
		})
	}
}

func TestMCDLogicIntentAppliesQuantityAndAccountLimitAfterParsing(t *testing.T) {
	for _, test := range []struct {
		name, amount string
		want         int64
	}{
		{"account default", "", 2000},
		{"lower owner limit", "不超过 12.34 元", 1234},
		{"owner cannot raise account limit", "30 元以内", 2000},
		{"ambiguous amount needs form", "二十元左右", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := strings.Replace(mcdLogicIntent, `"max_amount_text":""`, `"max_amount_text":"`+test.amount+`"`, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if len(mcdRows(request["tools"])) != 0 {
					t.Error("intent parser received executable tools")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": response}}}})
			}))
			defer server.Close()
			cfg := agentTestConfig()
			cfg.Model.Mock, cfg.Model.Fast.BaseURL, cfg.Model.Fast.Model = false, server.URL, "intent-test"
			s := mcdOrderStage{
				ctx: t.Context(), runtime: Runtime{store: store.NewMemoryStore(), models: modelrouter.New(cfg)},
				run:   app.AgentRun{ID: "intent-run", SessionID: "intent-session", Workflow: &app.WorkflowState{Route: app.RouteDecision{Slots: app.RouteSlots{Query: "买麦辣鸡腿堡和两杯可乐"}}}},
				state: &app.MCDOrderState{MaxOrderCents: 2000},
			}
			if err := s.intent(); err != nil {
				t.Fatal(err)
			}
			if s.state.LimitCents != test.want || len(s.state.Lines) != 2 || s.state.Lines[0].Quantity != 1 || s.state.Lines[1].Quantity != 2 || s.state.Lines[0].Notes != "不要酱" {
				t.Fatalf("runtime defaults changed owner intent: %+v", s.state)
			}
			for _, line := range s.state.Lines {
				if line.Product != nil || line.Selection != nil {
					t.Fatal("intent parser invented official product binding")
				}
			}
		})
	}
}

func TestMCDLogicMoneyPreservesCentsAndRejectsAmbiguity(t *testing.T) {
	for text, want := range map[string]int64{"0.01": 1, "12.34元": 1234, "不超过 30 元": 3000, "20.5": 2050, "1000000.00": 100000000} {
		if got, ok := mcdAmount(text); !ok || got != want {
			t.Errorf("mcdAmount(%q) = %d/%t; want %d", text, got, ok, want)
		}
	}
	for _, text := range []string{"", "0", "-1", "12.345", "10-20", "10至20元", "10到20元", "1e3", "1000001", "999999999999999999999", ".50", "20."} {
		if got, ok := mcdAmount(text); ok {
			t.Errorf("ambiguous amount %q accepted as %d cents", text, got)
		}
	}
	for _, test := range []struct {
		value any
		want  int64
		valid bool
	}{{0.01, 1, true}, {12.34, 1234, true}, {19.99, 1999, true}, {20, 2000, true}, {1.001, 0, false}, {0.001, 0, false}, {-1.0, 0, false}, {math.NaN(), 0, false}, {math.Inf(1), 0, false}, {"12.34", 0, false}} {
		got, ok := mcdAmountNumberValue(test.value)
		if ok != test.valid || ok && got != test.want {
			t.Errorf("form amount %v = %d/%t; want %d/%t", test.value, got, ok, test.want, test.valid)
		}
	}
}

func TestMCDLogicRankingBindsOnlySuppliedUniqueCandidates(t *testing.T) {
	byID := map[string]map[string]any{"official-a": {"offer_id": "official-a", "product_code": "a"}, "official-b": {"offer_id": "official-b", "product_code": "b"}}
	ranked, unique, err := decodeMCDRanking(`{"ranked_ids":["official-b","official-a"],"unique_match":false}`, byID)
	if err != nil || unique || len(ranked) != 2 || ranked[0]["product_code"] != "b" {
		t.Fatalf("valid ranking = %v/%t/%v", ranked, unique, err)
	}
	for name, raw := range map[string]string{
		"invented candidate": `{"ranked_ids":["invented"],"unique_match":true}`,
		"repeated candidate": `{"ranked_ids":["official-a","official-a"],"unique_match":false}`,
		"empty unique":       `{"ranked_ids":[],"unique_match":true}`,
		"changed data":       `{"ranked_ids":["official-a"],"unique_match":true,"product_code":"invented"}`,
		"missing uniqueness": `{"ranked_ids":["official-a"]}`,
		"null uniqueness":    `{"ranked_ids":["official-a"],"unique_match":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := decodeMCDRanking(raw, byID); err == nil {
				t.Fatal("invalid ranking accepted")
			}
		})
	}
	if ranked, unique, err := decodeMCDRanking(`{"ranked_ids":[],"unique_match":false}`, byID); err != nil || unique || len(ranked) != 0 {
		t.Fatal("no-match result was not preserved", ranked, unique, err)
	}
}

func mcdLogicLine(code string, quantity int, price int64) app.MCDOrderLine {
	selection := map[string]any{"product_code": code, "offer_id": "menu-" + code, "quantity": quantity}
	return app.MCDOrderLine{Text: code, Quantity: quantity, MenuCents: price, Product: map[string]any{"product_code": code}, Selection: mcdClone(selection), RegularSelection: selection}
}

func TestMCDLogicCouponAllocationUsesLargestSavingsAndRemainingUses(t *testing.T) {
	lines := []app.MCDOrderLine{mcdLogicLine("a", 2, 2000), mcdLogicLine("b", 2, 1500)}
	choices := []mcdCouponChoice{
		{line: 0, ref: "shared", name: "共享券", price: 1000, max: 2, product: map[string]any{"offer_id": "coupon-a"}},
		{line: 1, ref: "shared", name: "共享券", price: 100, max: 2, product: map[string]any{"offer_id": "coupon-b"}},
	}
	uses := map[string]int{"shared": 2}
	got := mcdAllocateCoupons(lines, choices, uses)
	if len(got) != 2 || got[0].CouponName != "" || got[0].Quantity != 2 || got[1].CouponName != "共享券" || got[1].Quantity != 2 || got[1].Selection["offer_id"] != "coupon-b" || uses["shared"] != 0 {
		t.Fatalf("remaining uses did not go to greatest savings: %+v, %v", got, uses)
	}
	if lines[1].RegularSelection["offer_id"] != "menu-b" || lines[1].RegularSelection["coupon_ref"] != nil {
		t.Fatal("allocation changed the no-coupon fallback")
	}
	line := mcdLogicLine("a", 4, 2000)
	uses = map[string]int{"best": 1, "next": 5}
	got = mcdAllocateCoupons([]app.MCDOrderLine{line}, []mcdCouponChoice{
		{line: 0, ref: "next", name: "次优券", price: 1800, max: 1, product: map[string]any{"offer_id": "next-offer"}},
		{line: 0, ref: "best", name: "最优券", price: 1000, max: 4, product: map[string]any{"offer_id": "best-offer"}},
	}, uses)
	if len(got) != 3 || got[0].CouponName != "最优券" || got[0].Quantity != 1 || got[1].CouponName != "次优券" || got[1].Quantity != 1 || got[2].CouponName != "" || got[2].Quantity != 2 || uses["best"] != 0 || uses["next"] != 4 {
		t.Fatalf("allocation exceeded coupon/product limits or lost quantity: %+v, %v", got, uses)
	}
}

func TestMCDLogicCouponQuoteRequiresBoundCouponLinesSavingsAndAmountLimit(t *testing.T) {
	lines := []app.MCDOrderLine{mcdLogicLine("a", 2, 1000), mcdLogicLine("b", 1, 500)}
	lines[0].CouponName, lines[1].CouponName = "券甲", "券乙"
	coupon := func(code string, quantity int) map[string]any {
		return map[string]any{"product_code": code, "quantity": quantity, "coupon_fingerprint": "fp-" + code}
	}
	plainItem := func(code string, quantity int) map[string]any {
		return map[string]any{"product_code": code, "quantity": quantity, "coupon_fingerprint": nil}
	}
	bound := []map[string]any{coupon("a", 2), coupon("b", 1)}
	for _, test := range []struct {
		name             string
		amount, discount any
		items            []map[string]any
		names            []string
		limit            int64
		valid            bool
	}{
		// Quote coupon names come from a different upstream field than the
		// coupon list title, so they are display-only.
		{"verified with official names", 2000, 500, bound, []string{"官方甲", "官方乙"}, 2000, true},
		{"verified without names", 2000, 500, bound, nil, 2000, true},
		{"missing one coupon line", 2000, 500, []map[string]any{coupon("a", 2), plainItem("b", 1)}, nil, 2000, false},
		{"coupon on wrong quantity", 2000, 500, []map[string]any{coupon("a", 1), plainItem("a", 1), coupon("b", 1)}, nil, 2000, false},
		{"unplanned coupon", 2000, 500, []map[string]any{coupon("a", 2), coupon("b", 1), coupon("c", 1)}, nil, 2000, false},
		{"empty fingerprint", 2000, 500, []map[string]any{coupon("a", 2), {"product_code": "b", "quantity": 1, "coupon_fingerprint": ""}}, nil, 2000, false},
		{"missing discount", 2000, 0, bound, nil, 2000, false},
		{"no savings", 2500, 500, bound, nil, 3000, false},
		{"over owner limit", 2001, 499, bound, nil, 2000, false},
		{"fractional cents", 1999.5, 500, bound, nil, 2000, false},
		{"missing amount", nil, 500, bound, nil, 2000, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			quote := map[string]any{"amount_cents": test.amount, "discount_cents": test.discount, "discount_names": test.names, "items": test.items}
			if got := mcdCouponQuoteValid(lines, quote, test.limit); got != test.valid {
				t.Fatalf("quote verification = %t; want %t", got, test.valid)
			}
		})
	}
	plain := []app.MCDOrderLine{mcdLogicLine("a", 1, 1000)}
	if !mcdCouponQuoteValid(plain, map[string]any{"amount_cents": 1000}, 1000) || mcdCouponQuoteValid(plain, map[string]any{"amount_cents": 1001}, 1000) {
		t.Fatal("no-coupon quote ignored the final amount limit")
	}
}

func TestMCDLogicCouponKeepsConfigurationAndQuoteKeepsOrderBinding(t *testing.T) {
	detail := map[string]any{"product_type": "7", "standard_configuration_supported": true, "groups": []any{map[string]any{"round": 1, "min_quantity": 1}}, "modifiers": map[string]any{"supported": true}}
	coupon := mcdClone(detail)
	if !mcdSameConfiguration(detail, coupon) {
		t.Fatal("identical official configuration was rejected")
	}
	coupon["groups"] = []any{map[string]any{"round": 1, "min_quantity": 2}}
	if mcdSameConfiguration(detail, coupon) {
		t.Fatal("coupon silently added a required component")
	}
	coupon = mcdClone(detail)
	coupon["configuration_issues"] = []string{"official_app_required"}
	if mcdSameConfiguration(detail, coupon) {
		t.Fatal("coupon requiring official App configuration was admitted")
	}
	state := &app.MCDOrderState{Store: map[string]any{"store_code": "confirmed-store"}, PickupAt: "2090-01-01T12:00:00+08:00", EatType: "locker-in", Lines: []app.MCDOrderLine{mcdLogicLine("a", 2, 1000)}}
	quote := map[string]any{"store_code": "confirmed-store", "pickup_at": state.PickupAt, "eat_type": "locker-in", "items": []any{map[string]any{"product_code": "a", "quantity": 2}}}
	if !mcdQuoteMatchesPlan(state, quote) {
		t.Fatal("matching official order was rejected")
	}
	for key, value := range map[string]any{"store_code": "another-store", "pickup_at": "2090-01-01T12:30:00+08:00", "eat_type": "take-in-store", "items": []any{map[string]any{"product_code": "a", "quantity": 1}}} {
		changed := mcdClone(quote)
		changed[key] = value
		if mcdQuoteMatchesPlan(state, changed) {
			t.Errorf("changed quote %s crossed order-to-payment boundary", key)
		}
	}
}

func TestMCDLogicPickupDefaultsAndExactTimeBinding(t *testing.T) {
	first, second := "2090-01-01T12:00:00+08:00", "2090-01-01T12:30:00+08:00"
	times := mcdPickupSlots(map[string]any{"dates": []any{map[string]any{"periods": []any{map[string]any{"pickup_times": []string{second, "bad", first, first, "2000-01-01T12:00:00+08:00"}}}}}})
	if !slices.Equal(times, []string{first, second}) {
		t.Fatalf("pickup slots not ordered, future-only and unique: %v", times)
	}
	s := mcdOrderStage{state: &app.MCDOrderState{Phase: "pickup", Intent: &app.MCDOrderIntent{}, Methods: []map[string]any{{"code": "locker-in", "name": "取餐柜"}}, Slots: times, LimitCents: 2000, MaxOrderCents: 2000}}
	if err := s.context(); err != nil || s.state.EatType != "locker-in" || s.state.PickupAt != first || s.state.Answers != nil {
		t.Fatalf("unstated pickup did not choose qualified method/earliest slot: %+v, %v", s.state, err)
	}
	for text, want := range map[string]string{"12:00": first, "12点": first, "2090-01-01 12:30": second, second: second, "中午": "", "12:15": ""} {
		if got := mcdMatchTime(text, times); got != want {
			t.Errorf("time %q bound to %q; want %q", text, got, want)
		}
	}
	if got := mcdMatchTime("12:00", []string{first, "2090-01-02T12:00:00+08:00"}); got != "" {
		t.Fatal("ambiguous multi-day time silently selected", got)
	}
	methods := []map[string]any{{"code": "locker-in", "name": "取餐柜"}, {"code": "take-in-store", "name": "到店取餐"}}
	if mcdUniqueText("取餐柜", methods, "code") != "locker-in" || mcdUniqueText("take-in-store", methods, "code") != "take-in-store" || mcdUniqueText("取餐", methods, "code") != "" {
		t.Fatal("pickup method guessed beyond an exact candidate")
	}
}

func TestMCDLogicPickupSlotsUseChronologicalOrderAcrossOffsets(t *testing.T) {
	// RFC3339 offsets are accepted by the provider boundary; textual ordering
	// must not turn the later UTC instant into the default pickup.
	earlier, later := "2090-01-01T10:00:00+08:00", "2090-01-01T03:00:00Z"
	got := mcdPickupSlots(map[string]any{"dates": []any{map[string]any{"periods": []any{map[string]any{"pickup_times": []string{later, earlier}}}}}})
	if !slices.Equal(got, []string{earlier, later}) {
		t.Fatalf("earliest available pickup ordered by text: %v", got)
	}
}

func TestMCDLogicExplicitPickupMethodIsNotSilentlyReplaced(t *testing.T) {
	// Form persistence is covered by the Workflow tests. This rule must leave
	// the choice unresolved before asking when the sole method differs from
	// what the owner explicitly requested.
	s := mcdOrderStage{state: &app.MCDOrderState{
		Phase: "pickup", Intent: &app.MCDOrderIntent{PickupMethodText: "到店取餐"},
		Methods: []map[string]any{{"code": "locker-in", "name": "取餐柜"}},
		Slots:   []string{"2090-01-01T12:00:00+08:00"}, LimitCents: 2000, MaxOrderCents: 2000,
	}}
	_ = s.context()
	if s.state.EatType != "" {
		t.Fatalf("explicit pickup method silently replaced by %q", s.state.EatType)
	}
}

func TestMCDLogicFormsAcceptOnlyFrozenCandidatesAndCancel(t *testing.T) {
	rows := []map[string]any{{"offer_id": "official", "label": "官方餐品"}}
	form := &app.ApprovalInputForm{SchemaVersion: app.ApprovalInputSchemaVersion, Entries: []app.ApprovalInputEntry{mcdEntry("item_1", "餐品", mcdSelect("product", "确认餐品", rows, "offer_id"))}}
	if err := app.ValidateApprovalInputForm(form); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"official", mcdCancelOption} {
		if err := app.ValidateApprovalInputs(form, app.ApprovalInputValues{"item_1": {"product": value}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, values := range []app.ApprovalInputValues{{"item_1": {"product": "invented"}}, {"item_2": {"product": "official"}}, {"item_1": {"product": "official", "coupon_ref": "invented"}}} {
		if err := app.ValidateApprovalInputs(form, values); err == nil {
			t.Fatal("form accepted value outside frozen candidates", values)
		}
	}
	s := mcdOrderStage{state: &app.MCDOrderState{Lines: []app.MCDOrderLine{{Text: "meal", Quantity: 1}}, Answers: app.ApprovalInputValues{"item_1": {"product": "invented"}}, Candidates: map[string]map[string]any{"item_1:product:official": {"product_code": "official"}}}}
	if err := s.menu(); err == nil || s.state.Lines[0].Product != nil {
		t.Fatal("menu accepted answer that bypassed candidate snapshot")
	}
}

func TestMCDLogicConfigurationRequiresOfficialChoicesAndKeepsFallback(t *testing.T) {
	line := mcdLogicLine("meal", 1, 2000)
	line.Detail = map[string]any{"standard_configuration_supported": true, "groups": []any{map[string]any{"round": 1, "name": "饮料", "min_quantity": 1, "max_quantity": 1, "choices": []any{map[string]any{"product_code": "cola", "name": "可乐", "available": true, "max_quantity": 1, "extra_cny": "0.00"}, map[string]any{"product_code": "sold-out", "name": "售罄饮料", "available": false, "max_quantity": 1}}}}}
	fields, choices, err := mcdConfiguration(&line, "item_1")
	if err != nil || len(fields) != 1 || len(choices) != 1 || choices["item_1:group_1_0:cola"] == nil {
		t.Fatalf("required official group = %v %v %v", fields, choices, err)
	}
	if err := mcdApplyChoice(&line, choices["item_1:group_1_0:cola"]); err != nil {
		t.Fatal(err)
	}
	if err := mcdApplyChoice(&line, choices["item_1:group_1_0:cola"]); err == nil {
		t.Fatal("repeated choice inflated quantity")
	}
	public := mcdCartSelection(line.Selection)
	if public["_answered"] != nil || len(mcdRows(public["combo"])) != 1 || line.RegularSelection["combo"] != nil {
		t.Fatal("configuration leaked bookkeeping or mutated fallback", public)
	}
	if got := mcdMentionChoice("可乐或橙汁都行", []map[string]any{{"name": "可乐"}, {"name": "橙汁"}}); got != nil {
		t.Fatal("ambiguous customization was chosen silently", got)
	}
}

func TestMCDLogicResultSummariesNeverContainPickupEvidence(t *testing.T) {
	for _, name := range []string{"mcd.purchase.commit", "mcd.purchase.status", "mcd.selection.read"} {
		for _, status := range []app.ToolCallStatus{app.ToolCallStatusCompleted, app.ToolCallStatusFailed} {
			output := map[string]any{"pickup_code": "PRIVATE-PICKUP-9123", "order_no": "PRIVATE-ORDER-4567", "nested": map[string]any{"pickup_code": "PRIVATE-PICKUP-9123"}}
			call := app.ToolCall{Tool: name, Status: status, Result: output, ObservationSummary: "PRIVATE-PICKUP-9123"}
			text := adaptToolResult(toolResultAdapterInput{Call: call, Output: output})
			if strings.Contains(text, "PRIVATE-") || !strings.Contains(text, string(status)) {
				t.Fatalf("private receipt entered generic summary: %q", text)
			}
			if !reflect.DeepEqual(call.Result, output) {
				t.Fatal("summary redaction destroyed scoped receipt")
			}
		}
	}
}

func TestMCDLogicOfficialCandidateSchemaRequiresOriginalStringKeys(t *testing.T) {
	for _, key := range []string{"code", "city_code"} {
		valid := map[string]any{key: "official", "name": "官方名称", "provider_metadata": map[string]any{"kept": true}}
		if rows, err := mcdNamedCandidates([]any{valid}, key); err != nil || len(rows) != 1 || rows[0][key] != "official" {
			t.Fatalf("valid %s candidate rejected: %v, %v", key, rows, err)
		}
		invalid := []any{nil, map[string]any{"unexpected": "object"}, []any{nil}, []any{valid, valid}}
		for _, id := range []any{nil, 123, "", " official ", "official\n", mcdCancelOption, mcdOtherStoreOption} {
			invalid = append(invalid, []any{map[string]any{key: id, "name": "官方名称"}})
		}
		invalid = append(invalid, []any{map[string]any{key: "official", "name": 123}})
		for _, value := range invalid {
			if rows, err := mcdNamedCandidates(value, key); err == nil {
				t.Errorf("malformed %s candidates accepted: %#v => %#v", key, value, rows)
			}
		}
	}
	for _, candidate := range []map[string]any{
		{"product_code": "official", "name": "餐品"},
		{"offer_id": "official", "product_code": 1001, "name": "餐品"},
		{"offer_id": " official ", "product_code": "1001", "name": "餐品"},
		{"offer_id": "official", "product_code": "1001", "name": nil},
	} {
		// Invalid official data must fail before any model call or selection.
		stage := mcdOrderStage{}
		if _, _, err := stage.rank("餐品", []map[string]any{candidate}); err == nil {
			t.Fatal("malformed official menu reached ranking", candidate)
		}
	}
}

func TestMCDLogicStoreAliasesAndPickupSchemasFailClosed(t *testing.T) {
	for _, input := range []map[string]any{
		{"list": []any{map[string]any{"storeCode": "3330613", "storeName": "官方门店", "storeAddress": "测试路"}}},
		{"stores": []any{map[string]any{"store_code": "3330613", "name": "官方门店", "address": "测试路"}}},
	} {
		rows, err := mcdStores(input)
		if err != nil || len(rows) != 1 || rows[0]["store_code"] != "3330613" || rows[0]["name"] != "官方门店" || rows[0]["address"] != "测试路" {
			t.Fatalf("documented store shape rejected: %v, %v", rows, err)
		}
	}
	for _, input := range []map[string]any{
		{"stores": []any{map[string]any{"store_code": 3330613, "name": "门店"}}},
		{"stores": []any{map[string]any{"store_code": nil, "storeCode": "3330613", "name": "门店"}}},
		{"stores": nil, "list": []any{map[string]any{"storeCode": "3330613", "storeName": "门店"}}},
		{"stores": []any{nil}},
	} {
		if rows, err := mcdStores(input); err == nil {
			t.Fatalf("invalid official store shape silently defaulted: %v", rows)
		}
	}
	for _, input := range []map[string]any{
		{}, {"dates": nil}, {"dates": []any{nil}},
		{"dates": []any{map[string]any{"periods": "invalid"}}},
		{"dates": []any{map[string]any{"periods": []any{map[string]any{"pickup_times": []any{nil}}}}}},
		{"dates": []any{map[string]any{"periods": []any{map[string]any{"pickup_times": []any{123}}}}}},
	} {
		if got := mcdPickupSlots(input); got != nil {
			t.Fatalf("invalid pickup schema became ordinary availability: %v", got)
		}
	}
	if got := mcdPickupSlots(map[string]any{"dates": []any{}}); got == nil || len(got) != 0 {
		t.Fatal("valid empty availability must remain distinct from invalid data", got)
	}
}

func TestMCDLogicPickupAnswersRemainInConfirmedSnapshot(t *testing.T) {
	for field, value := range map[string]string{"pickup_method": "unqualified-method", "pickup_time": "2090-01-01T12:30:00+08:00"} {
		s := mcdOrderStage{state: &app.MCDOrderState{
			Phase: "pickup", Intent: &app.MCDOrderIntent{},
			Methods: []map[string]any{{"code": "locker-in", "name": "取餐柜"}},
			Slots:   []string{"2090-01-01T12:00:00+08:00"}, LimitCents: 2000, MaxOrderCents: 2000,
			Answers: app.ApprovalInputValues{"context": {field: value}},
		}}
		if err := s.context(); err == nil || s.state.EatType != "" || s.state.PickupAt != "" {
			t.Fatalf("out-of-snapshot %s was accepted: %+v, %v", field, s.state, err)
		}
	}
}
