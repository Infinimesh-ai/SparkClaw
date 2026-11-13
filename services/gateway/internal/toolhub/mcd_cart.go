package toolhub

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

func mcdCartDefinitions() []app.ToolDefinition {
	properties := map[string]any{}
	for _, k := range []string{"store_code", "pickup_at", "eat_type", "max_amount_cny", "cart_ref", "operation_id", "source_quote_ref", "operation_key", "input_digest"} {
		properties[k] = map[string]any{"type": "string", "maxLength": 256}
	}
	modifier := strictObjectSchema([]string{"code", "quantity"}, map[string]any{"code": map[string]any{"type": "string", "maxLength": 20}, "quantity": map[string]any{"type": "integer", "minimum": 0, "maximum": 1}, "key": map[string]any{"type": "string", "maxLength": 9}})
	component := strictObjectSchema([]string{"product_code"}, map[string]any{"product_code": map[string]any{"type": "string", "maxLength": 20}, "quantity": map[string]any{"type": "integer", "minimum": 1, "maximum": 20}, "modifiers": map[string]any{"type": "array", "maxItems": 20, "items": modifier}})
	item := strictObjectSchema([]string{"product_code"}, map[string]any{"product_code": map[string]any{"type": "string", "maxLength": 20}, "quantity": map[string]any{"type": "integer", "minimum": 1, "maximum": 20}, "offer_id": map[string]any{"type": "string", "maxLength": 128}, "coupon_ref": map[string]any{"type": "string", "maxLength": 32}, "variant_of": map[string]any{"type": "string", "maxLength": 20}, "modifiers": map[string]any{"type": "array", "maxItems": 20, "items": modifier}, "combo": map[string]any{"type": "array", "maxItems": 20, "items": strictObjectSchema([]string{"round", "items"}, map[string]any{"round": map[string]any{"type": "integer", "minimum": 0, "maximum": 100}, "items": map[string]any{"type": "array", "maxItems": 20, "items": component}})}})
	properties["items"] = map[string]any{"type": "array", "maxItems": 20, "minItems": 1, "items": item}
	var definitions []app.ToolDefinition
	for _, r := range []struct {
		name        string
		ops         []string
		write       bool
		description string
	}{
		{"mcd.cart.read", []string{"cart_snapshot", "cart_add_status", "checkout_quote_pickup_status", "cart_operations_list"}, false, "Read the current cart and fresh cart_ref, or reconcile an original cart/pickup operation. Never infer success from a timeout."},
		{"mcd.cart.write", []string{"cart_add_simple", "cart_add_configured", "cart_clear", "checkout_quote_pickup", "cart_operation_retire"}, true, "Apply one explicitly selected McDonald's cart draft or pickup method. Exact item/coupon/combo/modifier identifiers must come from official evidence. Clearing requires explicit removal intent and a fresh cart_ref. This operation cannot create an order or pay."},
		{"mcd.quote.prepare", []string{"checkout_quote"}, false, "Quote the exact current McDonald's cart and import its full quote into the originating Mac. Use the original source_quote_ref for an explicitly selected pickup quote. The quote expires after its original 120 seconds. This operation does not authorize or execute a purchase."},
	} {
		props := map[string]any{}
		for k, v := range properties {
			props[k] = v
		}
		props["operation"] = map[string]any{"type": "string", "enum": r.ops}
		risk := app.RiskRead
		if r.write {
			risk = app.RiskReversible
		}
		definitions = append(definitions, app.ToolDefinition{Name: r.name, Description: r.description, InputSchema: strictObjectSchema([]string{"operation"}, props), Risk: risk, RequiresApproval: r.write, Idempotent: !r.write, TimeoutMS: 90000, OutputSchema: objectValueSchema(), Sandbox: "forbidden", Audit: "always"})
	}
	return definitions
}
func mcdCartRegistration(kind string) toolRegistration {
	run := func(h *ToolHub, ctx context.Context, args map[string]any) (Result, error) {
		if h.resources == nil || h.resources.MCDSelection == nil {
			return Result{}, errors.New("mcd_selection_unavailable")
		}
		raw, err := json.Marshal(args)
		if err != nil {
			return Result{}, err
		}
		var output map[string]any
		if kind == "quote" {
			output, err = h.resources.MCDSelection.Quote(ctx, raw)
		} else {
			output, err = h.resources.MCDSelection.Cart(ctx, raw, kind == "write")
		}
		return Result{Output: output}, err
	}
	effect := app.ToolEffectExternalRead
	if kind == "write" {
		effect = app.ToolEffectExternalInteract
	}
	return workflowRegistration(toolRegistration{enabled: func(c config.Config) bool { return c.MCDSelection.Enabled && c.MCDSelection.CartEnabled }, run: ctxArgs(run)}, "mcd.cart."+kind, nil, app.OutcomeAdapterGeneric, "Use the original connected member and exact official evidence.", "Use only in the owned dining Workflow.", "No guessed products, silent clearing, retry of unknown writes, order creation or payment.", effect)
}
