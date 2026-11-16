package agent

import (
	"bytes"
	"encoding/json"
	"maps"
	"math"
	"regexp"
	"strconv"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

var (
	mcdCartModifierCode = regexp.MustCompile(`^[0-9]{1,20}$`)
	mcdCartModifierKey  = regexp.MustCompile(`^[0-9]{1,4}-[0-9]{1,4}$`)
)

func mcdCartMatches(lines []app.MCDOrderLine, items []map[string]any) bool {
	if len(lines) == 0 || items == nil {
		return false
	}
	expected, actual := map[string]int64{}, map[string]int64{}
	for _, line := range lines {
		// The public coupon_ref cannot be proven equal to a cart fingerprint.
		if line.CouponName != "" || line.Quantity < 1 {
			return false
		}
		quantity := int64(1) // ItemSelection's public default.
		if q, exists := line.Selection["quantity"]; exists {
			var ok bool
			quantity, ok = mcdCartInteger(q)
			if !ok {
				return false
			}
		}
		if quantity != int64(line.Quantity) {
			return false
		}
		key, ok := mcdSimpleCartKey(line.Selection, true)
		if !ok || !mcdCartAddQuantity(expected, key, int64(line.Quantity)) {
			return false
		}
	}
	for _, item := range items {
		key, ok := mcdSimpleCartKey(item, false)
		quantity, quantityOK := mcdCartInteger(item["quantity"])
		if !ok || !quantityOK || !mcdCartAddQuantity(actual, key, quantity) {
			return false
		}
	}
	return maps.Equal(expected, actual)
}

// mcdSimpleCartKey compares only the supported public selection and official
// cart shapes. Display text and prices require a fresh quote, not cart equality.
func mcdSimpleCartKey(item map[string]any, planned bool) (string, bool) {
	raw, err := json.Marshal(item)
	if err != nil {
		return "", false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var normalized map[string]any
	if decoder.Decode(&normalized) != nil || normalized == nil || !mcdCartNoCoupon(normalized) {
		return "", false
	}
	item = normalized
	code, ok := mcdCandidateID(item["product_code"])
	if !ok {
		return "", false
	}
	groupsField := "components"
	if planned {
		groupsField = "combo"
		if !mcdCartKnownFields(item, "product_code", "quantity", "offer_id", "variant_of", "coupon_ref", "coupon_fingerprint", "modifiers", "combo", "_answered") {
			return "", false
		}
	} else if !mcdCartKnownFields(item, "product_code", "quantity", "product_type", "name", "components", "configuration", "coupon_fingerprint", "amount_cents") {
		return "", false
	}
	if !planned {
		// All three fields are emitted by the pinned catalog.cart_items.
		// Missing evidence must not be interpreted as an ordinary empty cart.
		configuration, configurationOK := item["configuration"].(map[string]any)
		_, componentsOK := item["components"].([]any)
		if !configurationOK || configuration == nil || !componentsOK {
			return "", false
		}
	}
	groups, ok := mcdCartList(item, groupsField)
	if !ok {
		return "", false
	}
	kind := "1"
	if len(groups) > 0 {
		kind = "7"
	}
	if !planned {
		actualType := item["product_type"]
		if actualType != "1" && actualType != "7" || actualType == "1" && len(groups) > 0 {
			return "", false
		}
		kind = actualType.(string)
	}
	modifiers, ok := mcdCartModifiers(item, planned)
	if !ok || kind == "7" && len(modifiers) != 0 {
		return "", false
	}
	rounds, ok := mcdCartComponents(groups, planned)
	if !ok {
		return "", false
	}
	return mcdCartCanonical(map[string]any{"code": code, "type": kind, "modifiers": modifiers, "components": rounds}), true
}

func mcdCartComponents(groups []map[string]any, planned bool) (map[string]map[string]int64, bool) {
	rounds := map[string]map[string]int64{}
	seen := map[int64]bool{}
	for _, group := range groups {
		round, ok := mcdCartInteger(group["round"])
		if !ok || round < 0 || seen[round] {
			return nil, false
		}
		seen[round] = true
		field, codeField := "comboProducts", "code"
		if planned {
			field, codeField = "items", "product_code"
			if !mcdCartKnownFields(group, "round", "items", "_complete") {
				return nil, false
			}
		} else if !mcdCartKnownFields(group, "round", "comboProducts", "isChoices", "choicesCode") {
			return nil, false
		}
		if !planned {
			if _, exists := group[field]; !exists {
				return nil, false
			}
		}
		parts, ok := mcdCartList(group, field)
		if !ok {
			return nil, false
		}
		quantities := map[string]int64{}
		for _, part := range parts {
			code, codeOK := mcdCandidateID(part[codeField])
			quantity, quantityOK := mcdCartInteger(part["quantity"])
			if !codeOK || !quantityOK || !mcdCartNoCoupon(part) {
				return nil, false
			}
			if planned && !mcdCartKnownFields(part, "product_code", "quantity", "modifiers") {
				return nil, false
			}
			if !planned {
				if !mcdCartKnownFields(part, "code", "quantity", "name", "customization", "modification", "grills", "biasChoices") {
					return nil, false
				}
				part = map[string]any{"configuration": mcdCartComponentConfiguration(part)}
			}
			modifiers, modifierOK := mcdCartModifiers(part, planned)
			key := mcdCartCanonical(map[string]any{"code": code, "modifiers": modifiers})
			if !modifierOK || !mcdCartAddQuantity(quantities, key, quantity) {
				return nil, false
			}
		}
		// The upstream cart may omit an explicitly empty optional round.
		if len(quantities) > 0 {
			rounds[strconv.FormatInt(round, 10)] = quantities
		}
	}
	return rounds, true
}

func mcdCartComponentConfiguration(part map[string]any) map[string]any {
	result := map[string]any{}
	for _, key := range []string{"customization", "modification", "grills", "biasChoices"} {
		if value, exists := part[key]; exists {
			result[key] = value
		}
	}
	return result
}

func mcdCartModifiers(item map[string]any, planned bool) (map[string]string, bool) {
	if planned {
		modifiers, ok := mcdCartList(item, "modifiers")
		if !ok {
			return nil, false
		}
		keys := map[string]string{}
		for _, modifier := range modifiers {
			if !mcdCartKnownFields(modifier, "code", "quantity", "key") {
				return nil, false
			}
			quantity, ok := mcdCartInteger(modifier["quantity"])
			if !ok || quantity < 0 || quantity > 1 {
				return nil, false
			}
			key := modifier["key"]
			if key == nil {
				// The pinned upstream maps toggles/optional choices to these keys.
				key = "0-" + strconv.FormatInt(quantity, 10)
			} else if quantity != 1 {
				return nil, false
			}
			if !mcdCartAddModifier(keys, modifier["code"], key) {
				return nil, false
			}
		}
		return keys, true
	}
	configuration, ok := mcdCartObject(item, "configuration")
	if !ok || !mcdCartKnownFields(configuration, "customization", "modification", "grills", "biasChoices") {
		return nil, false
	}
	for _, key := range []string{"grills", "biasChoices"} {
		raw := mcdCartCanonical(configuration[key])
		if raw != "null" && raw != "{}" && raw != "[]" {
			return nil, false
		}
	}
	modern, ok := mcdCartObject(configuration, "modification")
	if !ok || !mcdCartKnownFields(modern, "values") {
		return nil, false
	}
	modernRows, ok := mcdCartList(modern, "values")
	if !ok {
		return nil, false
	}
	modernKeys := map[string]string{}
	for _, row := range modernRows {
		q, ok := mcdCartInteger(row["quantity"])
		if !ok || q != 1 || !mcdCartKnownFields(row, "code", "quantity", "key", "name", "price", "displayText") || !mcdCartAddModifier(modernKeys, row["code"], row["key"]) {
			return nil, false
		}
	}
	legacy, ok := mcdCartObject(configuration, "customization")
	if !ok || !mcdCartKnownFields(legacy, "options", "items") {
		return nil, false
	}
	options, optionsOK := mcdCartList(legacy, "options")
	items, itemsOK := mcdCartList(legacy, "items")
	if !optionsOK || !itemsOK {
		return nil, false
	}
	legacyKeys := map[string]string{}
	for _, row := range options {
		if !mcdCartKnownFields(row, "code", "name", "key", "checked", "price", "quantity", "checkedCode", "displayText") {
			return nil, false
		}
		if q, exists := row["quantity"]; exists {
			quantity, ok := mcdCartInteger(q)
			if !ok || quantity != 1 {
				return nil, false
			}
		}
		if !mcdCartAddModifier(legacyKeys, row["code"], row["key"]) {
			return nil, false
		}
	}
	for _, row := range items {
		values, ok := mcdCartList(row, "values")
		if !ok || len(values) != 1 || !mcdCartKnownFields(row, "code", "name", "key", "displayText", "values") || !mcdCartKnownFields(values[0], "code", "name", "price", "quantity") {
			return nil, false
		}
		quantity, ok := mcdCartInteger(values[0]["quantity"])
		if !ok || quantity < 0 || quantity > 1 || quantity == 0 && row["key"] != "0-0" || !mcdCartAddModifier(legacyKeys, row["code"], row["key"]) {
			return nil, false
		}
	}
	if _, exists := modern["values"]; exists && len(legacyKeys) > 0 && !maps.Equal(modernKeys, legacyKeys) {
		return nil, false
	}
	if len(legacyKeys) > 0 {
		return legacyKeys, true
	}
	return modernKeys, true
}

func mcdCartNoCoupon(item map[string]any) bool {
	if item["coupon_fingerprint"] != nil {
		return false
	}
	ref, exists := item["coupon_ref"]
	return !exists || ref == nil || ref == ""
}

func mcdCartAddModifier(keys map[string]string, rawCode, rawKey any) bool {
	code, codeOK := rawCode.(string)
	key, keyOK := rawKey.(string)
	if !codeOK || !keyOK || !mcdCartModifierCode.MatchString(code) || !mcdCartModifierKey.MatchString(key) || keys[code] != "" {
		return false
	}
	keys[code] = key
	return true
}

func mcdCartInteger(value any) (int64, bool) {
	raw, err := json.Marshal(value)
	var number *int64
	if err != nil || json.Unmarshal(raw, &number) != nil || number == nil {
		return 0, false
	}
	return *number, true
}

func mcdCartAddQuantity(quantities map[string]int64, key string, quantity int64) bool {
	if quantity < 1 || quantity > math.MaxInt64-quantities[key] {
		return false
	}
	quantities[key] += quantity
	return true
}

func mcdCartList(object map[string]any, key string) ([]map[string]any, bool) {
	value, exists := object[key]
	if !exists {
		return nil, true
	}
	rows, ok := value.([]any)
	if !ok {
		return nil, false
	}
	result := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		object, ok := row.(map[string]any)
		if !ok || object == nil {
			return nil, false
		}
		result = append(result, object)
	}
	return result, true
}

func mcdCartObject(object map[string]any, key string) (map[string]any, bool) {
	value := object[key]
	if value == nil {
		return map[string]any{}, true
	}
	result, ok := value.(map[string]any)
	return result, ok
}

func mcdCartKnownFields(object map[string]any, allowed ...string) bool {
	for key := range object {
		found := false
		for _, candidate := range allowed {
			if key == candidate {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func mcdCartCanonical(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
