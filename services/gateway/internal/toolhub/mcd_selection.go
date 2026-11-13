package toolhub

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

func mcdReadDefinition() app.ToolDefinition {
	properties := map[string]any{"operation": map[string]any{"type": "string", "enum": []string{"membership_assets", "stores_favorites", "cities_list", "stores_search", "store_get", "pickup_slots", "pickup_methods", "menu_list", "product_detail", "coupons_list", "coupon_products", "cart_get"}}}
	for _, field := range []string{"store_code", "pickup_at", "eat_type", "max_amount_cny", "keyword", "city_code", "latitude", "longitude", "product_code", "offer_id", "coupon_ref", "variant_of"} {
		properties[field] = map[string]any{"type": "string", "maxLength": 256}
	}
	for _, field := range []string{"offset", "limit", "page"} {
		properties[field] = map[string]any{"type": "integer", "minimum": 0, "maximum": 10000}
	}
	return app.ToolDefinition{Name: "mcd.selection.read", Description: "Read the connected McDonald's account's restaurants, pickup options, menus, benefits or cart. Use original product/coupon identifiers from returned evidence. Checkout context requires the user's exact store, future pickup time, supported pickup method and amount ceiling. Coordinates are decimal strings. This tool cannot change a cart, create an order or pay.", InputSchema: strictObjectSchema([]string{"operation"}, properties), Risk: app.RiskRead, Idempotent: true, OutputSchema: objectValueSchema(), TimeoutMS: 90000, Sandbox: "forbidden", Audit: "always"}
}
func mcdReadRegistration() toolRegistration {
	return workflowRegistration(toolRegistration{enabled: func(c config.Config) bool { return c.MCDSelection.Enabled }, run: ctxArgs((*ToolHub).mcdRead)}, app.ToolCapabilityMCDSelection, nil, app.OutcomeAdapterGeneric,
		"Read the authorized McDonald's selection session.", "Use only for the dining.mcd_read Workflow and exact owner-provided context.", "Never infer an account, store, pickup time, price, product identity, consent or payment authority.", app.ToolEffectExternalRead)
}
func (h *ToolHub) mcdRead(ctx context.Context, args map[string]any) (Result, error) {
	if h.resources == nil || h.resources.MCDSelection == nil {
		return Result{}, errors.New("mcd_selection_unavailable")
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return Result{}, err
	}
	result, err := h.resources.MCDSelection.Read(ctx, raw)
	// Result content belongs only to the execution's bounded transient store.
	return Result{Output: result}, err
}
