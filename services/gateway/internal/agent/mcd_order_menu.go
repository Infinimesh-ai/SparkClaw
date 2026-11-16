package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

var errMCDNotesUnresolved = errors.New("mcd_customization_notes_unresolved")

func (s *mcdOrderStage) menu() error {
	if s.state.Answers != nil {
		for i := range s.state.Lines {
			id := mcdQuestionID(i)
			line := &s.state.Lines[i]
			for field, value := range s.state.Answers[id] {
				candidate := s.state.Candidates[id+":"+field+":"+mcdText(value)]
				if candidate == nil {
					return errors.New("mcd_menu_answer_outside_snapshot")
				}
				if field == "product" {
					line.Product = mcdClone(candidate)
				} else {
					if err := mcdApplyChoice(line, candidate); err != nil {
						return err
					}
					if s.state.PendingKind == "configuration" && strings.TrimSpace(line.Notes) != "" {
						// The configuration form explicitly explains that its official
						// selections replace any unparsed customization request.
						mcdMarkNoteResolution(line.Selection, "_notes_confirmed")
					}
				}
			}
		}
		s.consumeAnswers()
	}
	entries := []app.ApprovalInputEntry{}
	candidates := map[string]map[string]any{}
	for i := range s.state.Lines {
		line := &s.state.Lines[i]
		id := mcdQuestionID(i)
		if line.Product != nil {
			continue
		}
		out, err := s.read("menu_list", map[string]any{"keyword": line.Text, "limit": 20})
		if err != nil {
			return err
		}
		rows := mcdRows(out["products"])
		if len(rows) == 0 {
			affordable, err := s.mcdAffords(s.runtime.tools.MCDRequestCosts().Read("menu_list", false), 0)
			if err != nil {
				return err
			}
			if !affordable {
				s.state.StopReply = mcdBudgetExceeded
				return nil
			}
			out, err = s.read("menu_list", map[string]any{"limit": 50})
			if err != nil {
				return err
			}
			rows = mcdRows(out["products"])
		}
		if len(rows) > 50 {
			rows = rows[:50]
		}
		ranked, unique, err := s.rank(line.Text, rows)
		if err != nil {
			s.state.StopReply = "模型暂时不可用"
			return err
		}
		if unique {
			line.Product = mcdClone(ranked[0])
			continue
		}
		for _, row := range ranked {
			row["label"] = mcdText(row["name"]) + " · " + mcdText(row["display_price_cny"]) + " 元"
			candidates[id+":product:"+mcdText(row["offer_id"])] = row
		}
		entries = append(entries, mcdEntry(id, line.Text, mcdSelect("product", "餐品", ranked, "offer_id")))
	}
	if len(entries) > 0 {
		return s.ask("products", entries, candidates)
	}
	entries = nil
	candidates = map[string]map[string]any{}
	for i := range s.state.Lines {
		line := &s.state.Lines[i]
		id := mcdQuestionID(i)
		if line.Selection == nil {
			code, offer := mcdText(line.Product["product_code"]), mcdText(line.Product["offer_id"])
			if code == "" || offer == "" {
				return errors.New("mcd_product_identity_missing")
			}
			detail, err := s.read("product_detail", map[string]any{"product_code": code, "offer_id": offer})
			if err != nil {
				return err
			}
			if mcdText(detail["product_code"]) != code || mcdText(detail["offer_id"]) != offer || mcdInt(detail["max_quantity"]) < int64(line.Quantity) {
				return errors.New("mcd_product_detail_mismatch")
			}
			if len(mcdStrings(detail["configuration_issues"])) > 0 {
				s.state.StopReply = "未下单：所选餐品需要在麦当劳 App 配置"
				return nil
			}
			line.Detail = detail
			line.Selection = map[string]any{"product_code": code, "offer_id": offer, "quantity": line.Quantity}
			price, ok := mcdAmount(mcdText(line.Product["display_price_cny"]))
			if !ok {
				return errors.New("mcd_display_price_missing")
			}
			line.MenuCents = price
		}
		fields, choices, err := mcdConfiguration(line, id)
		if err != nil {
			s.state.StopReply = "未下单：所选餐品需要在麦当劳 App 配置"
			if errors.Is(err, errMCDNotesUnresolved) {
				s.state.StopReply = "未下单：无法按官方选项确认定制备注，请在麦当劳 App 配置"
			}
			return err
		}
		if len(fields) > 0 {
			entry := mcdEntry(id, mcdText(line.Detail["name"]), fields...)
			if strings.TrimSpace(line.Notes) != "" {
				entry.Summary = "原定制备注：" + line.Notes + "\n请确认下列官方选项；提交后仅按所选配置下单，不另行应用备注。若选项不能满足要求，请取消点餐。"
			}
			entries = append(entries, entry)
			for k, v := range choices {
				candidates[k] = v
			}
		}
	}
	if len(entries) > 0 {
		return s.ask("configuration", entries, candidates)
	}
	for i := range s.state.Lines {
		line := &s.state.Lines[i]
		base, ok := mcdAmount(mcdText(line.Product["display_price_cny"]))
		extra, err := mcdSelectionExtraCents(line)
		if !ok || err != nil {
			return errors.New("mcd_configured_price_unverified")
		}
		// Recompute from frozen official prices on every resume; never increment
		// the previous total or multiply the per-item price by order quantity.
		line.MenuCents = base + extra
		line.RegularSelection = mcdClone(line.Selection)
	}
	return nil
}

// A configuration answer is a frozen official selection, never a model-built
// request body. Required groups are asked together; dependencies get a new form.
func mcdConfiguration(line *app.MCDOrderLine, id string) ([]app.ApprovalInputField, map[string]map[string]any, error) {
	fields := []app.ApprovalInputField{}
	all := map[string]map[string]any{}
	note := ""
	if mcdNoteMatchCount(line.Detail, strings.TrimSpace(line.Notes)) == 1 {
		note = line.Notes
	}
	for _, group := range mcdRows(line.Detail["groups"]) {
		round := mcdInt(group["round"])
		field := fmt.Sprintf("group_%d", round)
		if mcdHasRound(line.Selection, round) {
			continue
		}
		low, high := mcdInt(group["min_quantity"]), mcdInt(group["max_quantity"])
		if low < 0 || high < low || high > 20 {
			return nil, nil, errors.New("mcd_group_quantity_unsupported")
		}
		// The form expresses each required unit separately, including repeated
		// selections. Optional extras are omitted rather than silently added.
		if low == 0 {
			mcdAppendRound(line.Selection, round, nil)
			continue
		}
		choices := []map[string]any{}
		for _, row := range mcdRows(group["choices"]) {
			if row["available"] != true || mcdInt(row["max_quantity"]) < 1 {
				continue
			}
			code := mcdText(row["product_code"])
			if code == "" {
				return nil, nil, errors.New("mcd_component_identity_missing")
			}
			choices = append(choices, map[string]any{"value": code, "label": mcdText(row["name"]) + " · 加 " + mcdText(row["extra_cny"]) + " 元", "kind": "component", "round": round, "part": row})
		}
		for unit := int64(0); unit < low; unit++ {
			name := fmt.Sprintf("%s_%d", field, unit)
			if mcdHasChoice(line.Selection, name) {
				continue
			}
			if selected := mcdMentionChoice(note, choices); selected != nil && low == 1 {
				selected["field"] = name
				if err := mcdApplyChoice(line, selected); err != nil {
					return nil, nil, err
				}
				mcdMarkNoteResolution(line.Selection, "_notes_matched")
				continue
			}
			fields = append(fields, mcdSelect(name, mcdText(group["name"]), choices, "value"))
			for _, choice := range choices {
				c := mcdClone(choice)
				c["field"] = name
				all[id+":"+name+":"+mcdText(c["value"])] = c
			}
		}
	}
	// Component modifiers become knowable only after its product is selected.
	targets := []struct {
		selection map[string]any
		detail    map[string]any
		prefix    string
	}{{line.Selection, line.Detail, "modifier"}}
	for _, round := range mcdRows(line.Selection["combo"]) {
		for _, part := range mcdRows(round["items"]) {
			code := mcdText(part["product_code"])
			detail := mcdComponentDetail(line.Detail, mcdInt(round["round"]), code)
			targets = append(targets, struct {
				selection map[string]any
				detail    map[string]any
				prefix    string
			}{part, detail, fmt.Sprintf("part_%d_%s", mcdInt(round["round"]), code)})
		}
	}
	for _, target := range targets {
		mods := mcdMap(target.detail["modifiers"])
		if mods["supported"] != true && target.detail["standard_configuration_supported"] != true {
			return nil, nil, errors.New("mcd_modifiers_unsupported")
		}
		for _, g := range mcdRows(mods["groups"]) {
			if g["inactive"] == true {
				continue
			}
			field := fmt.Sprintf("%s_%d", target.prefix, mcdInt(g["index"]))
			if mcdHasChoice(line.Selection, field) {
				continue
			}
			kind := mcdText(g["kind"])
			rows := mcdRows(g["choices"])
			if kind == "toggle" || kind == "optional_choice" {
				// Optional defaults are official and free; explicit notes need a choice.
				for n, row := range rows {
					f := fmt.Sprintf("%s_%d", field, n)
					if mcdHasChoice(line.Selection, f) {
						continue
					}
					options := []map[string]any{}
					for q := 0; q <= 1; q++ {
						if q == 1 && row["available"] != true {
							continue
						}
						label := "不选 "
						if q == 1 {
							label = "选择 "
						}
						options = append(options, map[string]any{"value": fmt.Sprintf("%s:%d", mcdText(row["code"]), q), "label": label + mcdText(row["name"]), "kind": "modifier", "field": f, "target": target.prefix, "modifier": map[string]any{"code": row["code"], "quantity": q}})
					}
					if line.Notes == "" {
						q := mcdInt(row["default_quantity"])
						for _, choice := range options {
							if mcdInt(mcdMap(choice["modifier"])["quantity"]) == q {
								if err := mcdApplyChoice(line, choice); err != nil {
									return nil, nil, err
								}
							}
						}
						continue
					}
					fields = append(fields, mcdSelect(f, mcdText(g["name"]), options, "value"))
					for _, c := range options {
						all[id+":"+f+":"+mcdText(c["value"])] = c
					}
				}
				continue
			}
			if kind != "single_choice" {
				return nil, nil, errors.New("mcd_modifier_kind_unsupported")
			}
			options := []map[string]any{}
			for _, row := range rows {
				if row["available"] != true || row["selection_supported"] != true {
					continue
				}
				options = append(options, map[string]any{"value": mcdText(row["code"]) + ":" + mcdText(row["key"]), "label": mcdText(row["name"]) + " · 加 " + mcdText(row["extra_cny"]) + " 元", "name": row["name"], "kind": "modifier", "field": field, "target": target.prefix, "modifier": map[string]any{"code": row["code"], "key": row["key"], "quantity": 1}})
			}
			if choice := mcdMentionChoice(note, options); choice != nil {
				if err := mcdApplyChoice(line, choice); err != nil {
					return nil, nil, err
				}
				mcdMarkNoteResolution(line.Selection, "_notes_matched")
				continue
			}
			fields = append(fields, mcdSelect(field, mcdText(g["name"]), options, "value"))
			for _, c := range options {
				all[id+":"+field+":"+mcdText(c["value"])] = c
			}
		}
	}
	if len(fields) == 0 && strings.TrimSpace(line.Notes) != "" && !mcdHasChoice(line.Selection, "_notes_matched") && !mcdHasChoice(line.Selection, "_notes_confirmed") {
		return nil, nil, errMCDNotesUnresolved
	}
	return fields, all, nil
}

// Only a complete, unique official choice name is an automatic note binding.
// Negation, multiple requests and names shared by different targets need the
// existing configuration form, whose summary retains the owner's whole note.
func mcdNoteMatchCount(detail map[string]any, note string) int {
	if note == "" {
		return 0
	}
	count := 0
	for _, group := range mcdRows(detail["groups"]) {
		for _, part := range mcdRows(group["choices"]) {
			if part["available"] != true {
				continue
			}
			if mcdText(part["name"]) == note {
				count++
			}
			count += mcdNoteMatchCount(part, note)
		}
	}
	for _, group := range mcdRows(mcdMap(detail["modifiers"])["groups"]) {
		if group["inactive"] == true || group["kind"] != "single_choice" {
			continue
		}
		for _, choice := range mcdRows(group["choices"]) {
			if choice["available"] == true && choice["selection_supported"] == true && mcdText(choice["name"]) == note {
				count++
			}
		}
	}
	return count
}
func mcdMentionChoice(text string, rows []map[string]any) map[string]any {
	var result map[string]any
	for _, r := range rows {
		name := mcdText(r["name"])
		if name == "" {
			name = mcdText(mcdMap(r["part"])["name"])
		}
		if name != "" && strings.TrimSpace(text) == name {
			if result != nil {
				return nil
			}
			result = mcdClone(r)
		}
	}
	return result
}
func mcdHasChoice(sel map[string]any, name string) bool {
	return mcdMap(sel["_answered"])[name] == true
}
func mcdMarkNoteResolution(selection map[string]any, name string) {
	answered := mcdMap(selection["_answered"])
	if answered == nil {
		answered = map[string]any{}
	}
	answered[name] = true
	selection["_answered"] = answered
}
func mcdHasRound(sel map[string]any, round int64) bool {
	for _, r := range mcdRows(sel["combo"]) {
		if mcdInt(r["round"]) == round && r["_complete"] == true {
			return true
		}
	}
	return false
}
func mcdAppendRound(sel map[string]any, round int64, items []map[string]any) {
	if items == nil {
		items = []map[string]any{}
	}
	rows := mcdRows(sel["combo"])
	rows = append(rows, map[string]any{"round": round, "items": items, "_complete": true})
	sel["combo"] = rows
}
func mcdComponentDetail(detail map[string]any, round int64, code string) map[string]any {
	for _, g := range mcdRows(detail["groups"]) {
		if mcdInt(g["round"]) == round {
			for _, part := range mcdRows(g["choices"]) {
				if mcdText(part["product_code"]) == code {
					return part
				}
			}
		}
	}
	return nil
}
func mcdApplyChoice(line *app.MCDOrderLine, c map[string]any) error {
	field := mcdText(c["field"])
	answered := mcdMap(line.Selection["_answered"])
	if answered == nil {
		answered = map[string]any{}
	}
	if answered[field] == true {
		return errors.New("mcd_configuration_repeated")
	}
	answered[field] = true
	line.Selection["_answered"] = answered
	if c["kind"] == "component" {
		round := mcdInt(c["round"])
		part := mcdMap(c["part"])
		code := mcdText(part["product_code"])
		groups := mcdRows(line.Selection["combo"])
		index := -1
		for i, g := range groups {
			if mcdInt(g["round"]) == round {
				index = i
			}
		}
		if index < 0 {
			groups = append(groups, map[string]any{"round": round, "items": []map[string]any{}})
			index = len(groups) - 1
		}
		items := mcdRows(groups[index]["items"])
		found := false
		for _, item := range items {
			if item["product_code"] == code {
				q := mcdInt(item["quantity"]) + 1
				if q > mcdInt(part["max_quantity"]) {
					return errors.New("mcd_component_limit")
				}
				item["quantity"] = q
				found = true
			}
		}
		if !found {
			items = append(items, map[string]any{"product_code": code, "quantity": 1})
		}
		groups[index]["items"] = items
		line.Selection["combo"] = groups
		return nil
	}
	if c["kind"] != "modifier" {
		return errors.New("mcd_choice_kind_invalid")
	}
	modifier := mcdMap(c["modifier"])
	target := mcdText(c["target"])
	if target == "modifier" {
		line.Selection["modifiers"] = append(mcdRows(line.Selection["modifiers"]), modifier)
		return nil
	}
	groups := mcdRows(line.Selection["combo"])
	for _, g := range groups {
		items := mcdRows(g["items"])
		for _, part := range items {
			if target == fmt.Sprintf("part_%d_%s", mcdInt(g["round"]), mcdText(part["product_code"])) {
				part["modifiers"] = append(mcdRows(part["modifiers"]), modifier)
				g["items"] = items
				line.Selection["combo"] = groups
				return nil
			}
		}
	}
	return errors.New("mcd_modifier_target_missing")
}

// Strip runtime bookkeeping before crossing the closed cart schema.
func mcdCartSelection(sel map[string]any) map[string]any {
	out := mcdClone(sel)
	delete(out, "_answered")
	groups := mcdRows(out["combo"])
	for _, g := range groups {
		delete(g, "_complete")
	}
	if groups != nil {
		out["combo"] = groups
	}
	return out
}

// mcdSelectionExtraCents returns the surcharge for one configured menu item,
// using only its frozen public_detail/public_modifiers evidence. Component
// quantities multiply both diffPrice and the component's modifier prices;
// line.Quantity belongs to the later order-total calculation.
func mcdSelectionExtraCents(line *app.MCDOrderLine) (int64, error) {
	total, err := mcdModifierExtraCents(line.Detail, line.Selection)
	if err != nil {
		return 0, err
	}
	groups := map[int64]map[string]any{}
	for _, group := range mcdRows(line.Detail["groups"]) {
		groups[mcdInt(group["round"])] = group
	}
	seen := map[int64]bool{}
	for _, selected := range mcdRows(line.Selection["combo"]) {
		round := mcdInt(selected["round"])
		group := groups[round]
		if group == nil || seen[round] {
			return 0, errors.New("mcd_component_price_unverified")
		}
		seen[round] = true
		quantity := int64(0)
		codes := map[string]bool{}
		for _, part := range mcdRows(selected["items"]) {
			code := mcdText(part["product_code"])
			q, quantityErr := mcdSelectionQuantity(part["quantity"])
			detail := mcdComponentDetail(line.Detail, round, code)
			if quantityErr != nil || detail == nil || detail["available"] != true || codes[code] || q < 1 || q > mcdInt(detail["max_quantity"]) {
				return 0, errors.New("mcd_component_price_unverified")
			}
			codes[code] = true
			price, err := mcdExtraPrice(detail["extra_cny"])
			if err != nil {
				return 0, err
			}
			modifier, err := mcdModifierExtraCents(detail, part)
			if err != nil {
				return 0, err
			}
			total += q * (price + modifier)
			quantity += q
		}
		if quantity < mcdInt(group["min_quantity"]) || quantity > mcdInt(group["max_quantity"]) {
			return 0, errors.New("mcd_component_quantity_unverified")
		}
	}
	for round, group := range groups {
		if !seen[round] && mcdInt(group["min_quantity"]) > 0 {
			return 0, errors.New("mcd_component_missing")
		}
	}
	return total, nil
}

func mcdModifierExtraCents(detail, selection map[string]any) (int64, error) {
	selected := map[string]map[string]any{}
	for _, modifier := range mcdRows(selection["modifiers"]) {
		code := mcdText(modifier["code"])
		if code == "" || selected[code] != nil {
			return 0, errors.New("mcd_modifier_price_unverified")
		}
		selected[code] = modifier
	}
	total := int64(0)
	for _, group := range mcdRows(mcdMap(detail["modifiers"])["groups"]) {
		if group["inactive"] == true {
			continue
		}
		kind := mcdText(group["kind"])
		if kind != "single_choice" && kind != "toggle" && kind != "optional_choice" {
			return 0, errors.New("mcd_modifier_price_unverified")
		}
		quantity := int64(0)
		for _, choice := range mcdRows(group["choices"]) {
			code := mcdText(choice["code"])
			modifier := selected[code]
			if kind == "single_choice" && mcdText(modifier["key"]) != mcdText(choice["key"]) {
				continue
			}
			if modifier == nil {
				return 0, errors.New("mcd_modifier_selection_missing")
			}
			q, quantityErr := mcdSelectionQuantity(modifier["quantity"])
			if quantityErr != nil || q > 1 || kind == "single_choice" && q != 1 || kind != "single_choice" && mcdText(modifier["key"]) != "" {
				return 0, errors.New("mcd_modifier_quantity_unverified")
			}
			if q == 1 {
				if choice["available"] != true || choice["selection_supported"] != true {
					return 0, errors.New("mcd_modifier_price_unverified")
				}
				price, err := mcdExtraPrice(choice["extra_cny"])
				if err != nil {
					return 0, err
				}
				total += price
			}
			quantity += q
			delete(selected, code)
		}
		if quantity < mcdInt(group["min_quantity"]) || quantity > mcdInt(group["max_quantity"]) {
			return 0, errors.New("mcd_modifier_quantity_unverified")
		}
	}
	if len(selected) != 0 {
		return 0, errors.New("mcd_modifier_price_unverified")
	}
	return total, nil
}

func mcdSelectionQuantity(value any) (int64, error) {
	raw, err := json.Marshal(value)
	var quantity *int64
	if err != nil || json.Unmarshal(raw, &quantity) != nil || quantity == nil || *quantity < 0 || *quantity > 20 {
		return 0, errors.New("mcd_selection_quantity_invalid")
	}
	return *quantity, nil
}

func mcdExtraPrice(value any) (int64, error) {
	text, ok := value.(string)
	if !ok || mcdAmountNumber.FindString(text) != text || text == "" {
		return 0, errors.New("mcd_extra_price_missing")
	}
	parts := strings.Split(text, ".")
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole > 1000000 {
		return 0, errors.New("mcd_extra_price_invalid")
	}
	fraction := int64(0)
	if len(parts) == 2 {
		fraction, _ = strconv.ParseInt(parts[1]+strings.Repeat("0", 2-len(parts[1])), 10, 64)
	}
	return whole*100 + fraction, nil
}
