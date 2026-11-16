package agent

import (
	"encoding/json"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const mcdCartPlainSelection = `{"product_code":"1001","offer_id":"menu-1","quantity":2}`

// The official catalog.cart_items projection includes an explicitly empty
// customization even for ordinary products with no configurable ingredients.
const mcdCartPlainSnapshot = `{"product_code":"1001","name":"汉堡","quantity":2,"product_type":"1","components":[],"configuration":{"customization":{"options":[],"items":[]}},"coupon_fingerprint":null,"amount_cents":3600}`

func mcdCartMatchObject(t *testing.T, raw string) map[string]any {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal([]byte(raw), &object); err != nil {
		t.Fatal(err)
	}
	return object
}

func TestMCDCartMatchesOfficialPlainCart(t *testing.T) {
	selection := mcdCartMatchObject(t, mcdCartPlainSelection)
	item := mcdCartMatchObject(t, mcdCartPlainSnapshot)
	lines := []app.MCDOrderLine{{Quantity: 2, Selection: selection}}
	if !mcdCartMatches(lines, []map[string]any{item}) {
		t.Fatal("an identical ordinary cart would incorrectly ask the user to clear it")
	}
	item["name"], item["amount_cents"] = "新的展示名", 4000
	if !mcdCartMatches(lines, []map[string]any{item}) {
		t.Fatal("display name or price incorrectly changed the selected configuration")
	}
	item["quantity"] = 1
	other := mcdCartMatchObject(t, mcdCartPlainSnapshot)
	other["quantity"] = 1
	if !mcdCartMatches(lines, []map[string]any{item, other}) {
		t.Fatal("identical configurations were not aggregated across cart rows")
	}
	selection["quantity"] = 1
	lines = []app.MCDOrderLine{{Quantity: 1, Selection: selection}, {Quantity: 1, Selection: selection}}
	if !mcdCartMatches(lines, []map[string]any{mcdCartMatchObject(t, mcdCartPlainSnapshot)}) {
		t.Fatal("server-merged identical line quantities were not recognized")
	}
}

const mcdCartModifierSelection = `{"product_code":"1001","quantity":2,"modifiers":[{"code":"111","quantity":1},{"code":"112","quantity":0},{"code":"222","quantity":1,"key":"1-1"},{"code":"8001","quantity":1},{"code":"8002","quantity":1,"key":"0-1"}]}`

const mcdCartModernConfiguration = `{"modification":{"values":[{"code":"8002","quantity":1,"key":"0-1"},{"code":"8001","quantity":1,"key":"0-1"},{"code":"222","quantity":1,"key":"1-1"},{"code":"112","quantity":1,"key":"0-0"},{"code":"111","quantity":1,"key":"0-1"}]}}`

const mcdCartLegacyConfiguration = `{"customization":{"options":[{"code":"111","name":"配料","key":"0-1","checked":1,"price":0,"quantity":1,"checkedCode":"1","displayText":""},{"code":"112","quantity":1,"key":"0-0"}],"items":[{"code":"222","name":"冰量","key":"1-1","values":[{"quantity":1,"price":0}]},{"code":"8001","name":"换燕麦奶","key":"0-1","displayText":"换燕麦奶(换+¥2)","values":[{"code":"2","name":"换","price":200,"quantity":1}]},{"code":"8002","name":"加浓缩","key":"0-1","values":[{"code":"2","name":"加","price":500,"quantity":1}]}]}}`

func TestMCDCartMatchesOfficialModifierRepresentations(t *testing.T) {
	for _, tc := range []struct {
		name, configuration string
	}{
		{"modern_values", mcdCartModernConfiguration},
		{"legacy_options_and_items", mcdCartLegacyConfiguration},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selection := mcdCartMatchObject(t, mcdCartModifierSelection)
			item := mcdCartMatchObject(t, mcdCartPlainSnapshot)
			item["configuration"] = mcdCartMatchObject(t, tc.configuration)
			if !mcdCartMatches([]app.MCDOrderLine{{Quantity: 2, Selection: selection}}, []map[string]any{item}) {
				t.Fatal("identical official modifiers would incorrectly require cart clearing")
			}
		})
	}
	t.Run("consistent_both_formats", func(t *testing.T) {
		selection := mcdCartMatchObject(t, mcdCartModifierSelection)
		item := mcdCartMatchObject(t, mcdCartPlainSnapshot)
		configuration := mcdCartMatchObject(t, mcdCartModernConfiguration)
		configuration["customization"] = mcdCartMatchObject(t, mcdCartLegacyConfiguration)["customization"]
		item["configuration"] = configuration
		if !mcdCartMatches([]app.MCDOrderLine{{Quantity: 2, Selection: selection}}, []map[string]any{item}) {
			t.Fatal("consistent simultaneous modern/legacy echoes were not recognized")
		}
	})
	t.Run("legacy_display_and_price_need_fresh_quote", func(t *testing.T) {
		selection := mcdCartMatchObject(t, mcdCartModifierSelection)
		item := mcdCartMatchObject(t, mcdCartPlainSnapshot)
		configuration := mcdCartMatchObject(t, mcdCartLegacyConfiguration)
		modifier := configuration["customization"].(map[string]any)["items"].([]any)[1].(map[string]any)
		modifier["name"], modifier["displayText"] = "其他展示名称", "新的价格展示"
		modifier["values"].([]any)[0].(map[string]any)["price"] = 300
		item["configuration"] = configuration
		if !mcdCartMatches([]app.MCDOrderLine{{Quantity: 2, Selection: selection}}, []map[string]any{item}) {
			t.Fatal("display metadata was used instead of exact modifier code/key")
		}
	})
	t.Run("unselected_optional_replacement", func(t *testing.T) {
		selection := mcdCartMatchObject(t, `{"product_code":"1001","quantity":2,"modifiers":[{"code":"8001","quantity":0}]}`)
		item := mcdCartMatchObject(t, mcdCartPlainSnapshot)
		item["configuration"] = mcdCartMatchObject(t, `{"customization":{"options":[],"items":[{"code":"8001","key":"0-0","values":[{"code":"1","name":"不换","price":200,"quantity":0}]}]}}`)
		if !mcdCartMatches([]app.MCDOrderLine{{Quantity: 2, Selection: selection}}, []map[string]any{item}) {
			t.Fatal("unselected optional replacement was confused with a selected paid add-on")
		}
	})
}

func TestMCDCartMatchesRejectsChangedOrUnprovenModifiers(t *testing.T) {
	tests := []struct {
		name   string
		change func(map[string]any, map[string]any)
	}{
		{"different_ice", func(_ map[string]any, c map[string]any) {
			c["modification"].(map[string]any)["values"].([]any)[2].(map[string]any)["key"] = "0-1"
		}},
		{"different_add_on", func(_ map[string]any, c map[string]any) {
			c["modification"].(map[string]any)["values"].([]any)[0].(map[string]any)["key"] = "0-0"
		}},
		{"missing_removed_ingredient", func(_ map[string]any, c map[string]any) {
			rows := c["modification"].(map[string]any)["values"].([]any)
			c["modification"].(map[string]any)["values"] = append(rows[:3], rows[4:]...)
		}},
		{"duplicate_modifier", func(_ map[string]any, c map[string]any) {
			m := c["modification"].(map[string]any)
			rows := m["values"].([]any)
			m["values"] = append(rows, rows[0])
		}},
		{"wire_quantity_is_not_toggle_quantity", func(_ map[string]any, c map[string]any) {
			c["modification"].(map[string]any)["values"].([]any)[3].(map[string]any)["quantity"] = 0
		}},
		{"modern_legacy_conflict", func(_ map[string]any, c map[string]any) {
			legacy := mcdCartMatchObject(t, mcdCartLegacyConfiguration)["customization"].(map[string]any)
			legacy["items"].([]any)[0].(map[string]any)["key"] = "0-0"
			c["customization"] = legacy
		}},
		{"empty_modern_conflicts_with_legacy", func(_ map[string]any, c map[string]any) {
			c["modification"].(map[string]any)["values"] = []any{}
			c["customization"] = mcdCartMatchObject(t, mcdCartLegacyConfiguration)["customization"]
		}},
		{"unsupported_grill", func(_ map[string]any, c map[string]any) { c["grills"] = []any{"extra"} }},
		{"unsupported_bias_choice", func(_ map[string]any, c map[string]any) { c["biasChoices"] = map[string]any{"x": 1} }},
		{"unknown_configuration", func(_ map[string]any, c map[string]any) { c["new_configuration"] = []any{} }},
		{"unknown_modifier_configuration", func(_ map[string]any, c map[string]any) {
			c["modification"].(map[string]any)["values"].([]any)[0].(map[string]any)["options"] = []any{"extra"}
		}},
		{"malformed_modifiers", func(s map[string]any, _ map[string]any) { s["modifiers"] = map[string]any{} }},
		{"key_without_selected_quantity", func(s map[string]any, _ map[string]any) {
			s["modifiers"].([]any)[2].(map[string]any)["quantity"] = 0
		}},
		{"unexpected_key_on_toggle", func(s map[string]any, _ map[string]any) {
			s["modifiers"].([]any)[0].(map[string]any)["key"] = ""
		}},
		{"modifier_code_is_not_string", func(_ map[string]any, c map[string]any) {
			c["modification"].(map[string]any)["values"].([]any)[0].(map[string]any)["code"] = 8002
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			selection := mcdCartMatchObject(t, mcdCartModifierSelection)
			configuration := mcdCartMatchObject(t, mcdCartModernConfiguration)
			tc.change(selection, configuration)
			item := mcdCartMatchObject(t, mcdCartPlainSnapshot)
			item["configuration"] = configuration
			if mcdCartMatches([]app.MCDOrderLine{{Quantity: 2, Selection: selection}}, []map[string]any{item}) {
				t.Fatal("a changed or unsupported configuration was treated as the requested cart")
			}
		})
	}
}

const mcdCartComboSelection = `{"product_code":"7001","offer_id":"menu-combo","quantity":2,"combo":[{"round":1,"_complete":true,"items":[{"product_code":"2001","quantity":1,"modifiers":[{"code":"111","quantity":1},{"code":"112","quantity":0}]}]},{"round":2,"_complete":true,"items":[{"product_code":"3001","quantity":1,"modifiers":[{"code":"222","quantity":1,"key":"1-1"},{"code":"8001","quantity":1}]}]},{"round":3,"_complete":true,"items":[]}]}`

const mcdCartComboSnapshot = `{"product_code":"7001","name":"套餐","quantity":2,"product_type":"7","configuration":{},"coupon_fingerprint":null,"amount_cents":5200,"components":[{"round":2,"isChoices":true,"choicesCode":"drinks","comboProducts":[{"code":"3001","name":"咖啡","quantity":1,"customization":{"options":[],"items":[{"code":"222","key":"1-1","values":[{"quantity":1,"price":0}]},{"code":"8001","key":"0-1","values":[{"code":"2","name":"换","quantity":1,"price":200}]}]}}]},{"round":1,"isChoices":true,"choicesCode":"food","comboProducts":[{"code":"2001","name":"汉堡","quantity":1,"modification":{"values":[{"code":"112","quantity":1,"key":"0-0"},{"code":"111","quantity":1,"key":"0-1"}]}}]}]}`

func TestMCDCartMatchesOfficialComboRoundsAndConfiguration(t *testing.T) {
	selection := mcdCartMatchObject(t, mcdCartComboSelection)
	item := mcdCartMatchObject(t, mcdCartComboSnapshot)
	if !mcdCartMatches([]app.MCDOrderLine{{Quantity: 2, Selection: selection}}, []map[string]any{item}) {
		t.Fatal("equivalent combo with reordered and omitted empty optional rounds was rejected")
	}
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"different_round", func(c map[string]any) { c["components"].([]any)[0].(map[string]any)["round"] = 4 }},
		{"duplicate_round", func(c map[string]any) { c["components"].([]any)[0].(map[string]any)["round"] = 1 }},
		{"different_component", func(c map[string]any) {
			c["components"].([]any)[0].(map[string]any)["comboProducts"].([]any)[0].(map[string]any)["code"] = "3002"
		}},
		{"component_quantity", func(c map[string]any) {
			c["components"].([]any)[0].(map[string]any)["comboProducts"].([]any)[0].(map[string]any)["quantity"] = 2
		}},
		{"component_ice", func(c map[string]any) {
			c["components"].([]any)[0].(map[string]any)["comboProducts"].([]any)[0].(map[string]any)["customization"].(map[string]any)["items"].([]any)[0].(map[string]any)["key"] = "0-1"
		}},
		{"component_milk", func(c map[string]any) {
			c["components"].([]any)[0].(map[string]any)["comboProducts"].([]any)[0].(map[string]any)["customization"].(map[string]any)["items"].([]any)[1].(map[string]any)["key"] = "0-0"
		}},
		{"component_unsupported_configuration", func(c map[string]any) {
			c["components"].([]any)[0].(map[string]any)["comboProducts"].([]any)[0].(map[string]any)["biasChoices"] = []any{"extra"}
		}},
		{"missing_component_configuration", func(c map[string]any) {
			delete(c["components"].([]any)[0].(map[string]any)["comboProducts"].([]any)[0].(map[string]any), "customization")
		}},
		{"component_shape_not_official", func(c map[string]any) {
			c["components"].([]any)[0].(map[string]any)["items"] = []any{}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := mcdCartMatchObject(t, mcdCartComboSnapshot)
			tc.change(item)
			if mcdCartMatches([]app.MCDOrderLine{{Quantity: 2, Selection: selection}}, []map[string]any{item}) {
				t.Fatal("a different or unverified combo was accepted")
			}
		})
	}
}

func TestMCDCartMatchesFailsClosedForMalformedOrCouponCart(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*app.MCDOrderLine, map[string]any)
	}{
		{"coupon_name", func(l *app.MCDOrderLine, _ map[string]any) { l.CouponName = "套餐券" }},
		{"coupon_reference", func(l *app.MCDOrderLine, _ map[string]any) { l.Selection["coupon_ref"] = "coupon-1" }},
		{"coupon_fingerprint", func(_ *app.MCDOrderLine, c map[string]any) { c["coupon_fingerprint"] = "opaque" }},
		{"empty_fingerprint_not_known_absent", func(_ *app.MCDOrderLine, c map[string]any) { c["coupon_fingerprint"] = "" }},
		{"zero_line_quantity", func(l *app.MCDOrderLine, _ map[string]any) { l.Quantity = 0 }},
		{"selection_quantity_differs", func(l *app.MCDOrderLine, _ map[string]any) { l.Selection["quantity"] = 1 }},
		{"default_quantity_differs", func(l *app.MCDOrderLine, _ map[string]any) { delete(l.Selection, "quantity") }},
		{"fractional_quantity", func(_ *app.MCDOrderLine, c map[string]any) { c["quantity"] = 2.5 }},
		{"string_quantity", func(_ *app.MCDOrderLine, c map[string]any) { c["quantity"] = "2" }},
		{"missing_quantity", func(_ *app.MCDOrderLine, c map[string]any) { delete(c, "quantity") }},
		{"changed_product", func(_ *app.MCDOrderLine, c map[string]any) { c["product_code"] = "1002" }},
		{"wrong_product_type", func(_ *app.MCDOrderLine, c map[string]any) { c["product_type"] = "7" }},
		{"missing_product_type", func(_ *app.MCDOrderLine, c map[string]any) { delete(c, "product_type") }},
		{"missing_configuration", func(_ *app.MCDOrderLine, c map[string]any) { delete(c, "configuration") }},
		{"null_configuration", func(_ *app.MCDOrderLine, c map[string]any) { c["configuration"] = nil }},
		{"missing_components", func(_ *app.MCDOrderLine, c map[string]any) { delete(c, "components") }},
		{"null_components", func(_ *app.MCDOrderLine, c map[string]any) { c["components"] = nil }},
		{"malformed_configuration", func(_ *app.MCDOrderLine, c map[string]any) { c["configuration"] = []any{} }},
		{"unknown_empty_configuration", func(_ *app.MCDOrderLine, c map[string]any) {
			c["configuration"] = map[string]any{"future": map[string]any{}}
		}},
		{"malformed_customization", func(_ *app.MCDOrderLine, c map[string]any) {
			c["configuration"] = map[string]any{"customization": []any{}}
		}},
		{"malformed_options", func(_ *app.MCDOrderLine, c map[string]any) {
			c["configuration"] = map[string]any{"customization": map[string]any{"options": map[string]any{}}}
		}},
		{"malformed_components", func(_ *app.MCDOrderLine, c map[string]any) { c["components"] = map[string]any{} }},
		{"null_component", func(_ *app.MCDOrderLine, c map[string]any) { c["components"] = []any{nil} }},
		{"hidden_planned_configuration", func(l *app.MCDOrderLine, _ map[string]any) {
			l.Selection["configuration"] = map[string]any{"extra": true}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			line := app.MCDOrderLine{Quantity: 2, Selection: mcdCartMatchObject(t, mcdCartPlainSelection)}
			item := mcdCartMatchObject(t, mcdCartPlainSnapshot)
			tc.change(&line, item)
			if mcdCartMatches([]app.MCDOrderLine{line}, []map[string]any{item}) {
				t.Fatal("unsupported cart equivalence was guessed")
			}
		})
	}
	if mcdCartMatches(nil, nil) {
		t.Fatal("absent evidence was treated as cart equivalence")
	}
}
