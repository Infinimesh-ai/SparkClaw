package agent

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
)

func TestMCDMenuOptionalRoundKeepsEmptyArrayAtCartBoundary(t *testing.T) {
	line := app.MCDOrderLine{Selection: map[string]any{"product_code": "1001", "quantity": 1}, Detail: map[string]any{"standard_configuration_supported": true, "modifiers": map[string]any{"supported": true, "groups": []any{}}, "groups": []map[string]any{{"round": 1, "min_quantity": 0, "max_quantity": 1, "choices": []any{}}}}}
	fields, _, err := mcdConfiguration(&line, "item_1")
	if err != nil || len(fields) != 0 {
		t.Fatalf("optional group prompted: %v %v", fields, err)
	}
	raw, _ := json.Marshal(mcdCartSelection(line.Selection))
	var wire struct {
		Combo []struct {
			Items json.RawMessage `json:"items"`
		} `json:"combo"`
	}
	if json.Unmarshal(raw, &wire) != nil || len(wire.Combo) != 1 || string(wire.Combo[0].Items) != "[]" {
		t.Fatalf("empty optional selection crossed strict cart schema as null: %s", raw)
	}
}

func mcdMenuNoteFixture(note string) app.MCDOrderLine {
	part := func(code, name string) map[string]any {
		return map[string]any{"product_code": code, "name": name, "available": true, "max_quantity": 1,
			"extra_cny": "0.00", "standard_configuration_supported": true, "modifiers": map[string]any{"supported": true, "groups": []any{}}}
	}
	return app.MCDOrderLine{Text: "套餐", Notes: note, Quantity: 1,
		Product:   map[string]any{"product_code": "meal", "offer_id": "menu-1", "display_price_cny": "18.00"},
		Selection: map[string]any{"product_code": "meal", "offer_id": "menu-1", "quantity": 1},
		Detail: map[string]any{"name": "套餐", "standard_configuration_supported": true,
			"modifiers": map[string]any{"supported": true, "groups": []any{}},
			"groups": []map[string]any{{"round": 1, "name": "饮料", "min_quantity": 1, "max_quantity": 1,
				"choices": []map[string]any{part("cola", "可乐"), part("juice", "橙汁")}}}}}
}

func TestMCDMenuNotesRequireExplicitOfficialChoices(t *testing.T) {
	for _, note := range []string{"不要可乐", "可乐和橙汁", "可乐，不加冰", "no cola", "可乐; no cheese"} {
		t.Run(note, func(t *testing.T) {
			line := mcdMenuNoteFixture(note)
			fields, choices, err := mcdConfiguration(&line, "item_1")
			if err != nil || len(fields) != 1 || len(choices) != 2 || len(mcdRows(line.Selection["combo"])) != 0 {
				t.Fatalf("ambiguous/negated note was silently chosen or discarded: %v, %v, %#v", fields, err, line.Selection)
			}
			// Merely selecting a value internally is insufficient. Only the
			// displayed configuration form's approved answer resolves the note.
			if err := mcdApplyChoice(&line, choices["item_1:group_1_0:juice"]); err != nil {
				t.Fatal(err)
			}
			if _, _, err := mcdConfiguration(&line, "item_1"); !errors.Is(err, errMCDNotesUnresolved) {
				t.Fatalf("unconfirmed note disappeared: %v", err)
			}
		})
	}
}

func TestMCDMenuNotesAllowOnlyWholeUniqueChoiceNames(t *testing.T) {
	line := mcdMenuNoteFixture(" 可乐 ")
	fields, _, err := mcdConfiguration(&line, "item_1")
	if err != nil || len(fields) != 0 || !mcdHasChoice(line.Selection, "_notes_matched") {
		t.Fatalf("exact official note was not bound: %v, %v", fields, err)
	}
	public := mcdCartSelection(line.Selection)
	if public["_notes_matched"] != nil || public["_notes_confirmed"] != nil || public["_answered"] != nil {
		t.Fatal("note bookkeeping leaked to cart schema", public)
	}

	line = mcdMenuNoteFixture("可乐")
	groups := mcdRows(line.Detail["groups"])
	other := mcdClone(groups[0])
	other["round"] = 2
	line.Detail["groups"] = append(groups, other)
	fields, _, err = mcdConfiguration(&line, "item_1")
	if err != nil || len(fields) != 2 || len(mcdRows(line.Selection["combo"])) != 0 {
		t.Fatalf("same name in two official targets was treated as unique: %v, %v", fields, err)
	}
}

func TestMCDMenuUnsupportedNotesStopWithoutCartMutation(t *testing.T) {
	line := mcdMenuNoteFixture("不要芝士，也不要酱")
	line.Detail["groups"] = []any{}
	s := mcdOrderStage{state: &app.MCDOrderState{Lines: []app.MCDOrderLine{line}}}
	if err := s.menu(); !errors.Is(err, errMCDNotesUnresolved) || !strings.Contains(s.state.StopReply, "无法按官方选项确认定制备注") {
		t.Fatalf("unsupported notes silently continued: %v, %q", err, s.state.StopReply)
	}
	if len(s.result.ToolCalls) != 0 || s.state.Lines[0].RegularSelection != nil {
		t.Fatal("unsupported customization reached a cart-ready selection")
	}
}

func TestMCDMenuNotesAreShownAndResolvedByOriginalConfigurationForm(t *testing.T) {
	f := &mcdFlowFixture{}
	runtime, st, session := newMCDFlowRuntime(t, f, true)
	note := "不要可乐，也不要冰"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		name := mcdMap(mcdMap(request["response_format"])["json_schema"])["name"]
		response := `{"ranked_ids":["menu-1"],"unique_match":true}`
		if name == "mcd_order_intent_v1" {
			response = `{"items":[{"text":"麦辣鸡腿堡套餐","quantity":null,"notes":"` + note + `"}],"store_text":"","pickup_method_text":"","pickup_time_text":"","max_amount_text":""}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": response}}}})
	}))
	defer server.Close()
	cfg := agentTestConfig()
	cfg.Model.Mock, cfg.Model.Fast.BaseURL, cfg.Model.Fast.Model = false, server.URL, "menu-test"
	runtime.models = modelrouter.New(cfg)
	run, started := dispatchMCDTest(t, runtime, st, session, app.MessageSourceWeb)
	if len(started.Approvals) != 1 {
		t.Fatal("store confirmation missing")
	}
	configured := answerMCD(t, runtime, st, run, started.Approvals[0], app.ApprovalInputValues{"context": {"store": "3330613"}}, false)
	if len(configured.Approvals) != 1 || configured.Approvals[0].InputForm == nil {
		t.Fatal("configuration confirmation missing", configured.Run.Summary)
	}
	form := configured.Approvals[0].InputForm
	if len(form.Entries) != 1 || !strings.Contains(form.Entries[0].Summary, note) || !strings.Contains(form.Entries[0].Summary, "不另行应用备注") {
		t.Fatalf("original note and explicit selection semantics missing: %#v", form)
	}
	// The user can explicitly accept an official option despite the earlier
	// note, or choose the unchanged cancellation option shown beside it.
	confirmed := answerMCD(t, runtime, st, configured.Run, configured.Approvals[0], app.ApprovalInputValues{"item_1": {"group_1_0": "2001"}}, false)
	if len(confirmed.Approvals) != 1 || confirmed.Approvals[0].Tool != "mcd.purchase.commit" || !mcdHasChoice(confirmed.Run.Workflow.MCDOrder.Lines[0].Selection, "_notes_confirmed") {
		t.Fatal("explicit configuration did not resume the original order", confirmed.Run.Summary)
	}
	for _, call := range confirmed.ToolCalls {
		if call.Tool == "mcd.cart.write" && call.Arguments["operation"] == "cart_add_configured" {
			for _, item := range mcdRows(call.Arguments["items"]) {
				if item["_notes_confirmed"] != nil || item["_notes_matched"] != nil {
					t.Fatal("private note binding leaked into cart write", item)
				}
			}
		}
	}
}

// Matches public_detail/public_modifiers: optional 0/1 selection without key,
// single_choice with quantity 1 and key, and per-component diffPrice.
func mcdMenuPriceFixture() app.MCDOrderLine {
	line := mcdMenuNoteFixture("")
	line.Quantity = 3
	line.Detail["modifiers"] = map[string]any{"supported": true, "groups": []map[string]any{{
		"index": 0, "name": "燕麦奶", "kind": "optional_choice", "min_quantity": 0, "max_quantity": 1,
		"choices": []map[string]any{{"code": "10", "name": "燕麦奶", "available": true, "selection_supported": true, "default_quantity": 0, "extra_cny": "2.00"}},
	}}}
	line.Detail["groups"] = []map[string]any{{"round": 1, "name": "饮料", "min_quantity": 2, "max_quantity": 2,
		"choices": []map[string]any{{"product_code": "drink", "name": "饮料", "available": true, "max_quantity": 2, "extra_cny": "1.50", "standard_configuration_supported": true,
			"modifiers": map[string]any{"supported": true, "groups": []map[string]any{{"index": 0, "name": "加料", "kind": "single_choice", "min_quantity": 1, "max_quantity": 1,
				"choices": []map[string]any{
					{"code": "20", "key": "0-0", "name": "不加料", "available": true, "selection_supported": true, "extra_cny": "0.00"},
					{"code": "20", "key": "0-1", "name": "加料", "available": true, "selection_supported": true, "extra_cny": "0.75"},
				}}}}}},
	}}
	line.Selection["quantity"] = 3
	line.Selection["modifiers"] = []map[string]any{{"code": "10", "quantity": 1}}
	line.Selection["combo"] = []map[string]any{{"round": 1, "items": []map[string]any{{"product_code": "drink", "quantity": 2, "modifiers": []map[string]any{{"code": "20", "key": "0-1", "quantity": 1}}}}}}
	line.Selection["_answered"] = map[string]any{"group_1_0": true, "group_1_1": true, "modifier_0_0": true, "part_1_drink_0": true}
	return line
}

func TestMCDMenuPriceIncludesSelectedExtrasOnceAcrossResumes(t *testing.T) {
	line := mcdMenuPriceFixture()
	extra, err := mcdSelectionExtraCents(&line)
	if err != nil || extra != 650 { // 200 + 2 * (150 + 75), per meal.
		t.Fatalf("configured surcharge = %d, %v", extra, err)
	}
	s := mcdOrderStage{state: &app.MCDOrderState{Lines: []app.MCDOrderLine{line}}}
	for i := 0; i < 2; i++ {
		if err := s.menu(); err != nil || s.state.Lines[0].MenuCents != 2450 {
			t.Fatalf("resume %d price = %d, %v", i, s.state.Lines[0].MenuCents, err)
		}
	}
	line = mcdMenuPriceFixture()
	line.Selection["modifiers"] = []map[string]any{{"code": "10", "quantity": 0}}
	if extra, err := mcdSelectionExtraCents(&line); err != nil || extra != 450 {
		t.Fatalf("unselected optional modifier was charged: %d, %v", extra, err)
	}
}

func TestMCDMenuPriceRejectsUnknownOrMalformedSelectionEvidence(t *testing.T) {
	for name, mutate := range map[string]func(*app.MCDOrderLine){
		"unknown modifier": func(line *app.MCDOrderLine) {
			line.Selection["modifiers"] = []map[string]any{{"code": "forged", "quantity": 1}}
		},
		"fractional quantity": func(line *app.MCDOrderLine) {
			line.Selection["modifiers"] = []map[string]any{{"code": "10", "quantity": 0.5}}
		},
		"null quantity": func(line *app.MCDOrderLine) {
			line.Selection["modifiers"] = []map[string]any{{"code": "10", "quantity": nil}}
		},
		"missing component": func(line *app.MCDOrderLine) { line.Selection["combo"] = []any{} },
		"unknown choice key": func(line *app.MCDOrderLine) {
			groups := mcdRows(line.Selection["combo"])
			parts := mcdRows(groups[0]["items"])
			parts[0]["modifiers"] = []map[string]any{{"code": "20", "key": "invented", "quantity": 1}}
			groups[0]["items"] = parts
			line.Selection["combo"] = groups
		},
		"missing component price": func(line *app.MCDOrderLine) {
			groups := mcdRows(line.Detail["groups"])
			choices := mcdRows(groups[0]["choices"])
			choices[0]["extra_cny"] = nil
			groups[0]["choices"] = choices
			line.Detail["groups"] = groups
		},
	} {
		t.Run(name, func(t *testing.T) {
			line := mcdMenuPriceFixture()
			mutate(&line)
			if _, err := mcdSelectionExtraCents(&line); err == nil {
				t.Fatal("unverified selection price accepted")
			}
		})
	}
	for _, price := range []any{nil, 0.75, "-1.00", "0.001", "1 元", "", "NaN"} {
		if _, err := mcdExtraPrice(price); err == nil {
			t.Fatalf("invalid extra price accepted: %#v", price)
		}
	}
}
