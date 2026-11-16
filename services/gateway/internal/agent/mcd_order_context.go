package agent

import (
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *mcdOrderStage) context() error {
	for round := 0; round < 8; round++ {
		switch s.state.Phase {
		case "":
			// Stop before any store question when the required path cannot fit.
			affordable, err := s.mcdAffords(s.runtime.tools.MCDRequestCosts().Read("stores_favorites", false), 0)
			if err != nil {
				return err
			}
			if !affordable {
				s.state.StopReply = mcdBudgetExceeded
				return nil
			}
			favorites, err := s.read("stores_favorites", nil)
			if err != nil {
				return err
			}
			rows, err := mcdStores(favorites)
			if err != nil {
				return err
			}
			// Mentioned favorites come first. Favorites are pre-checked only while
			// one coupon probe and the complete required path still fit; the rest
			// stay listed and are checked after selection, like search results.
			if text := s.state.Intent.StoreText; text != "" {
				sort.SliceStable(rows, func(i, j int) bool {
					return strings.Contains(mcdText(rows[i]["name"]), text) && !strings.Contains(mcdText(rows[j]["name"]), text)
				})
			}
			listed := []map[string]any{}
			for i, store := range rows {
				if len(listed) == 20 {
					break
				}
				if i < mcdMaxFavoriteChecks {
					affordable, err := s.mcdAffordsStorePrefetch()
					if err != nil {
						return err
					}
					if affordable {
						checked, err := s.storeContext(store)
						if err != nil {
							return err
						}
						if len(mcdRows(checked["methods"])) > 0 {
							listed = append(listed, checked)
						}
						continue
					}
				}
				listed = append(listed, store)
			}
			if len(listed) == 0 {
				s.state.Phase = "city"
				continue
			}
			s.state.Stores = listed
			s.state.Phase = "favorite"
			return s.askStores(true, "")
		case "favorite", "store":
			if s.state.Answers == nil {
				return s.askStores(s.state.Phase == "favorite", "")
			}
			chosen := s.answer("context", "store")
			if chosen == mcdOtherStoreOption {
				s.consumeAnswers()
				s.state.Phase = "city"
				continue
			}
			store := s.state.Candidates[chosen]
			if store == nil {
				return errors.New("mcd_store_not_in_snapshot")
			}
			fromFavorite := s.state.Phase == "favorite"
			s.consumeAnswers()
			if _, checked := store["methods"]; !checked {
				costs := s.runtime.tools.MCDRequestCosts()
				affordable, err := s.mcdAffords(costs.Read("pickup_slots", false)+costs.Read("pickup_methods", false), 0)
				if err != nil {
					return err
				}
				if !affordable {
					s.state.StopReply = mcdBudgetExceeded
					return nil
				}
				store, err = s.storeContext(store)
				if err != nil {
					return err
				}
				if len(mcdRows(store["methods"])) == 0 {
					remaining := []map[string]any{}
					for _, row := range s.state.Stores {
						if row["store_code"] != chosen {
							remaining = append(remaining, row)
						}
					}
					s.state.Stores = remaining
					return s.askStores(fromFavorite, "该门店不支持已启用的取餐方式，请选择其他门店")
				}
			}
			s.state.Store = map[string]any{"store_code": store["store_code"], "name": store["name"], "address": store["address"]}
			s.state.Methods = mcdRows(store["methods"])
			s.state.Slots = mcdStrings(store["slots"])
			s.state.Phase = "pickup"
		case "city":
			if s.state.Searches >= 3 {
				s.state.StopReply = "未选定门店，未下单"
				return nil
			}
			if s.state.PendingKind != "city" || s.state.Answers == nil {
				if affordable, err := s.mcdAffords(s.runtime.tools.MCDRequestCosts().Read("cities_list", false), 0); err != nil || !affordable {
					if err == nil {
						s.state.StopReply = mcdBudgetExceeded
					}
					return err
				}
				cities, err := s.read("cities_list", nil)
				if err != nil {
					return err
				}
				rows, err := mcdNamedCandidates(cities["cities"], "city_code")
				if err != nil {
					return err
				}
				if len(rows) == 0 {
					return errors.New("mcd_no_cities")
				}
				for _, c := range rows {
					c["label"] = mcdText(c["name"])
				}
				return s.ask("city", []app.ApprovalInputEntry{mcdEntry("context", "选择门店城市", mcdSelect("city", "城市", rows, "city_code"), app.ApprovalInputField{Name: "store_keyword", Label: "区、街道或门店关键词（可选）", Type: app.ApprovalInputText, MaxLength: 40})}, mcdCandidates(rows, "city_code"))
			}
			city := s.answer("context", "city")
			keyword := s.answer("context", "store_keyword")
			if s.state.Candidates[city] == nil {
				return errors.New("mcd_city_not_in_snapshot")
			}
			s.consumeAnswers()
			s.state.Searches++
			stores := []map[string]any{}
			for page := 1; page <= 3; page++ {
				affordable, err := s.mcdAffords(s.runtime.tools.MCDRequestCosts().StoreSearch(), 0)
				if err != nil {
					return err
				}
				if !affordable {
					if page == 1 {
						s.state.StopReply = mcdBudgetExceeded
						return nil
					}
					break
				}
				out, err := s.read("stores_search", map[string]any{"city_code": city, "keyword": keyword, "page": page, "limit": 30})
				if err != nil {
					return err
				}
				rows, err := mcdStores(out)
				if err != nil {
					return err
				}
				stores = append(stores, rows...)
				if out["has_more"] != true {
					break
				}
			}
			if len(stores) > 90 {
				stores = stores[:90]
			}
			s.state.Stores = stores
			s.state.Phase = "store"
			return s.askStores(false, "")
		case "pickup":
			if s.state.Answers != nil {
				if v := s.answer("context", "pickup_method"); v != "" {
					if !slices.ContainsFunc(s.state.Methods, func(method map[string]any) bool { return method["code"] == v }) {
						return errors.New("mcd_pickup_method_outside_snapshot")
					}
					s.state.EatType = v
				}
				if v := s.answer("context", "pickup_time"); v != "" {
					if !slices.Contains(s.state.Slots, v) {
						return errors.New("mcd_pickup_time_outside_snapshot")
					}
					s.state.PickupAt = v
				}
				if v, exists := s.state.Answers["context"]["max_amount"]; exists {
					amount, ok := mcdAmountNumberValue(v)
					if !ok {
						return errors.New("mcd_amount_invalid")
					}
					s.state.LimitCents = min(amount, s.state.MaxOrderCents)
				}
				s.consumeAnswers()
			}
			fields := []app.ApprovalInputField{}
			if s.state.EatType == "" {
				methods := s.state.Methods
				if len(methods) == 1 && s.state.Intent.PickupMethodText == "" {
					s.state.EatType = mcdText(methods[0]["code"])
				} else if selected := mcdUniqueText(s.state.Intent.PickupMethodText, methods, "code"); selected != "" {
					s.state.EatType = selected
				} else {
					fields = append(fields, mcdSelect("pickup_method", "取餐方式", methods, "code"))
				}
			}
			if s.state.PickupAt == "" {
				if len(s.state.Slots) == 0 {
					return errors.New("mcd_no_pickup_slots")
				}
				if s.state.Intent.PickupTimeText == "" {
					s.state.PickupAt = mcdEarliestSlot(s.state.Slots, time.Now().Add(mcdPickupLead))
					s.state.PickupDefaulted = true
					if s.state.PickupAt == "" {
						s.state.StopReply = "未下单：门店暂无可选取餐时间"
						return nil
					}
				} else if exact := mcdMatchTime(s.state.Intent.PickupTimeText, s.state.Slots); exact != "" {
					s.state.PickupAt = exact
				} else {
					rows := []map[string]any{}
					for _, slot := range s.state.Slots {
						rows = append(rows, map[string]any{"value": slot, "label": slot})
					}
					fields = append(fields, mcdSelect("pickup_time", "取餐时间", rows, "value"))
				}
			}
			if s.state.LimitCents == 0 {
				low, high := 0.01, float64(s.state.MaxOrderCents)/100
				fields = append(fields, app.ApprovalInputField{Name: "max_amount", Label: "本单金额上限（元）", Type: app.ApprovalInputNumber, Required: true, Min: &low, Max: &high})
			}
			if len(fields) > 0 {
				return s.ask("pickup", []app.ApprovalInputEntry{mcdEntry("context", "确认取餐信息", fields...)}, nil)
			}
			s.state.Phase = ""
			return nil
		default:
			return errors.New("mcd_context_phase_invalid")
		}
	}
	return errors.New("mcd_context_round_limit")
}
func (s *mcdOrderStage) askStores(favorites bool, note string) error {
	rows := []map[string]any{}
	for _, store := range s.state.Stores {
		row := mcdClone(store)
		row["label"] = mcdText(row["name"]) + " · " + mcdText(row["address"])
		if opening := mcdText(row["opening_time"]); opening != "" {
			row["label"] = mcdText(row["label"]) + " · " + opening + "–" + mcdText(row["closing_time"])
		}
		rows = append(rows, row)
	}
	field := mcdSelect("store", "确认门店", rows, "store_code")
	label := "重新搜索"
	if favorites {
		label = "不是这些，选择其他门店"
	}
	field.Options = append(field.Options, app.ApprovalInputOption{Value: mcdOtherStoreOption, Label: label})
	entry := mcdEntry("context", "请选择本次订单的门店", field)
	entry.Summary = note
	return s.ask("store", []app.ApprovalInputEntry{entry}, mcdCandidates(rows, "store_code"))
}
func (s *mcdOrderStage) storeContext(store map[string]any) (map[string]any, error) {
	code := mcdText(store["store_code"])
	slots, err := s.read("pickup_slots", map[string]any{"store_code": code})
	if err != nil {
		return nil, err
	}
	times := mcdPickupSlots(slots)
	if times == nil {
		return nil, errors.New("mcd_pickup_slots_schema_changed")
	}
	out := mcdClone(store)
	out["slots"] = times
	out["methods"] = []map[string]any{}
	if len(times) == 0 {
		return out, nil
	}
	qualified := s.runtime.tools.Config().MCDSelection.QualifiedEatTypes
	if len(qualified) == 0 {
		return nil, errors.New("mcd_no_qualified_pickup")
	}
	methods, err := s.read("pickup_methods", map[string]any{"store_code": code, "pickup_at": times[0], "eat_type": qualified[0], "max_amount_cny": mcdYuan(s.state.MaxOrderCents)})
	if err != nil {
		return nil, err
	}
	filtered := []map[string]any{}
	rows, err := mcdNamedCandidates(methods["methods"], "code")
	if err != nil {
		return nil, err
	}
	for _, method := range rows {
		supported, ok := method["checkout_supported"].(bool)
		if !ok {
			return nil, errors.New("mcd_pickup_methods_schema_changed")
		}
		if supported && slices.Contains(qualified, mcdText(method["code"])) {
			method["label"] = mcdText(method["name"])
			filtered = append(filtered, method)
		}
	}
	out["methods"] = filtered
	return out, nil
}
func mcdStores(out map[string]any) ([]map[string]any, error) {
	value, present := out["stores"]
	if !present {
		value = out["list"]
	}
	rows := mcdRows(value)
	if rows == nil {
		return nil, errors.New("mcd_store_schema_changed")
	}
	normalized := []map[string]any{}
	seen := map[string]bool{}
	for _, row := range rows {
		code, validCode := mcdCandidateID(mcdStoreAlias(row, "store_code", "code", "storeCode"))
		name := mcdText(mcdStoreAlias(row, "name", "storeName"))
		if !validCode || name == "" || seen[code] {
			return nil, errors.New("mcd_store_schema_changed")
		}
		seen[code] = true
		address := mcdText(row["address"])
		if address == "" {
			address = mcdText(row["storeAddress"])
		}
		opening := mcdText(row["opening_time"])
		if opening == "" {
			opening = mcdText(row["startTime"])
		}
		closing := mcdText(row["closing_time"])
		if closing == "" {
			closing = mcdText(row["endTime"])
		}
		normalized = append(normalized, map[string]any{"store_code": code, "name": name, "address": address, "opening_time": opening, "closing_time": closing})
	}
	return normalized, nil
}

func mcdStoreAlias(row map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, exists := row[key]; exists {
			return value
		}
	}
	return nil
}

func mcdNamedCandidates(value any, key string) ([]map[string]any, error) {
	rows := mcdRows(value)
	if rows == nil {
		return nil, errors.New("mcd_candidates_schema_changed")
	}
	seen := map[string]bool{}
	for _, row := range rows {
		id, valid := mcdCandidateID(row[key])
		if !valid || mcdText(row["name"]) == "" || seen[id] {
			return nil, errors.New("mcd_candidates_schema_changed")
		}
		seen[id] = true
	}
	return rows, nil
}
func mcdStrings(v any) []string {
	switch values := v.(type) {
	case []string:
		return values
	case []any:
		out := []string{}
		for _, v := range values {
			if s := mcdText(v); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
func mcdPickupSlots(out map[string]any) []string {
	times := []string{}
	instants := map[string]time.Time{}
	now := time.Now()
	dates := mcdRows(out["dates"])
	if dates == nil {
		return nil
	}
	for _, date := range dates {
		periods := mcdRows(date["periods"])
		if periods == nil {
			return nil
		}
		for _, period := range periods {
			raw, err := json.Marshal(period["pickup_times"])
			var slots []*string
			if err != nil || json.Unmarshal(raw, &slots) != nil || slots == nil {
				return nil
			}
			for _, value := range slots {
				if value == nil {
					return nil
				}
				slot := *value
				t, err := time.Parse(time.RFC3339, slot)
				if _, seen := instants[slot]; err == nil && !seen && t.After(now) {
					instants[slot] = t
					times = append(times, slot)
				}
			}
		}
	}
	sort.SliceStable(times, func(i, j int) bool { return instants[times[i]].Before(instants[times[j]]) })
	if len(times) > 1000 {
		times = times[:1000]
	}
	return times
}
func mcdMatchTime(text string, slots []string) string {
	matches := []string{}
	for _, slot := range slots {
		t, e := time.Parse(time.RFC3339, slot)
		if e != nil {
			continue
		}
		if text == slot || text == t.Format("15:04") || text == t.Format("2006-01-02 15:04") || t.Minute() == 0 && text == t.Format("15")+"点" {
			matches = append(matches, slot)
		}
	}
	if len(matches) == 1 {
		return matches[0]
	}
	return ""
}
func mcdUniqueText(text string, rows []map[string]any, key string) string {
	if text == "" {
		return ""
	}
	found := ""
	for _, row := range rows {
		if text == mcdText(row["name"]) || text == mcdText(row[key]) {
			if found != "" {
				return ""
			}
			found = mcdText(row[key])
		}
	}
	return found
}
func mcdAmountNumberValue(v any) (int64, bool) {
	raw, ok := v.(float64)
	if !ok {
		if n, yes := v.(int); yes {
			raw = float64(n)
		} else {
			return 0, false
		}
	}
	if raw <= 0 || raw > 1000000 {
		return 0, false
	}
	c := int64(raw*100 + 0.0000001)
	return c, raw-float64(c)/100 < 0.00000001
}

// mcdEarliestSlot returns the first chronologically ordered slot after after.
func mcdEarliestSlot(slots []string, after time.Time) string {
	for _, slot := range slots {
		if t, err := time.Parse(time.RFC3339, slot); err == nil && t.After(after) {
			return slot
		}
	}
	return ""
}

// mcdEnsurePickupFresh runs before any cart write. Store and menu questions can
// take minutes, so a runtime-chosen earliest slot is re-read and re-chosen once
// it is too close; an owner-chosen time is never silently replaced.
func (s *mcdOrderStage) mcdEnsurePickupFresh() error {
	at, err := time.Parse(time.RFC3339, s.state.PickupAt)
	if err != nil {
		return errors.New("mcd_pickup_time_invalid")
	}
	deadline := time.Now().Add(mcdPickupLead)
	if at.After(deadline) {
		return nil
	}
	if !s.state.PickupDefaulted {
		s.state.StopReply = "未下单：所选取餐时间已过，请重新点餐"
		return nil
	}
	slots, err := s.read("pickup_slots", map[string]any{"store_code": mcdText(s.state.Store["store_code"])})
	if err != nil {
		return err
	}
	times := mcdPickupSlots(slots)
	if times == nil {
		return errors.New("mcd_pickup_slots_schema_changed")
	}
	s.state.Slots = times
	if s.state.PickupAt = mcdEarliestSlot(times, deadline); s.state.PickupAt == "" {
		s.state.StopReply = "未下单：门店暂无可选取餐时间"
	}
	return nil
}
