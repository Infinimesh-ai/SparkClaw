package toolhub

import (
	"context"
	"errors"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcddining"
	"reflect"
)

func mcdPurchaseDefinitions() []app.ToolDefinition {
	props := map[string]any{}
	keys := []string{"purchase_id", "purchase_digest", "currency", "payment_method", "store_code", "pickup_at", "eat_type", "store_name"}
	for _, k := range keys {
		props[k] = map[string]any{"type": "string", "maxLength": 128}
	}
	for _, k := range []string{"amount_cents", "credential_revision"} {
		keys = append(keys, k)
		props[k] = map[string]any{"type": "integer", "minimum": 1, "maximum": 1<<53 - 1}
	}
	props["items"] = map[string]any{"type": "array", "minItems": 1, "maxItems": 100, "items": objectValueSchema()}
	props["discount_names"] = map[string]any{"type": "array", "maxItems": 100, "items": map[string]any{"type": "string", "maxLength": 512}}
	keys = append(keys, "items", "discount_names")
	return []app.ToolDefinition{
		{Name: "mcd.purchase.commit", Description: "Submit exactly one imported McDonald's quote using the originating Mac's wallet (ARCHCARD), only after immutable user approval. Runtime binds the prepared quote; model prose cannot authorize payment. Unknown responses must be queried, never retried.", InputSchema: strictObjectSchema(keys, props), Risk: app.RiskDangerous, RequiresApproval: true, ArgumentsImmutable: true, Idempotent: false, TimeoutMS: 90000, OutputSchema: objectValueSchema(), Sandbox: "forbidden", Audit: "always"},
		{Name: "mcd.purchase.status", Description: "Reconcile the original McDonald's handoff or unknown purchase without any ordering or payment writes; otherwise list original local orders. Never infer success or authorize retries from missing or stale status.", InputSchema: strictObjectSchema(nil, map[string]any{"operation": map[string]any{"type": "string", "enum": []string{"policy"}}}), Risk: app.RiskRead, Idempotent: true, TimeoutMS: 90000, OutputSchema: objectValueSchema(), Sandbox: "forbidden", Audit: "always"},
	}
}
func (h *ToolHub) MCDPurchaseArguments() (map[string]any, error) {
	if h.mcdSelection() == nil {
		return nil, errors.New("mcd_purchase_unavailable")
	}
	return h.mcdSelection().PurchaseArguments()
}
func (h *ToolHub) MCDPreparedQuote() (map[string]any, error) {
	if h.mcdSelection() == nil {
		return nil, errors.New("mcd_purchase_unavailable")
	}
	return h.mcdSelection().PreparedQuote()
}
func (h *ToolHub) MCDOrderingAvailable() bool {
	return h.mcdSelection() != nil && h.requireResource("mcd.purchase.commit") == nil && h.mcdSelection().OrderingAvailable()
}
func (h *ToolHub) MCDStatusAvailable() bool {
	return h.mcdSelection() != nil && h.requireResource("mcd.purchase.status") == nil && h.mcdSelection().StatusAvailable()
}

// MCDRequestsRemaining lets the ordering Workflow keep optional reads within
// the execution's provider-request aggregate; it never reserves or refunds.
func (h *ToolHub) MCDRequestsRemaining(ctx context.Context) (int, error) {
	if h.mcdSelection() == nil {
		return 0, errors.New("mcd_selection_unavailable")
	}
	return h.mcdSelection().RequestsRemaining(ctx)
}
func (h *ToolHub) MCDRequestCosts() mcddining.OrderRequestCosts { return mcddining.OrderRequestCosts{} }
func mcdPurchaseRegistration(commit bool) toolRegistration {
	run := func(h *ToolHub, ctx context.Context, args map[string]any, session, run string) (Result, error) {
		nodes := []app.WorkflowNodeID{"guard", "settle"}
		if commit {
			nodes = []app.WorkflowNodeID{"pay"}
		}
		if _, err := h.requireMCDWorkflow(ctx, session, run, nodes...); err != nil {
			return Result{}, err
		}
		if h.mcdSelection() == nil {
			return Result{}, errors.New("mcd_purchase_unavailable")
		}
		if !commit {
			var out map[string]any
			var err error
			if args["operation"] == "policy" {
				out, err = h.mcdSelection().OrderPolicy(ctx)
			} else {
				out, err = h.mcdSelection().PurchaseStatus(ctx)
			}
			return Result{Output: out}, err
		}
		calls, err := h.store.ListToolCalls(ctx, session)
		if err != nil {
			return Result{}, err
		}
		approvalID := ""
		for _, call := range calls {
			if call.Tool != "mcd.purchase.commit" || call.RunID != run || call.SessionID != session || call.ApprovalID == "" || call.Status != app.ToolCallStatusRunningAfterApproval || !reflect.DeepEqual(call.Arguments, args) {
				continue
			}
			a, ok, err := h.store.GetApproval(ctx, call.ApprovalID)
			if err != nil {
				return Result{}, err
			}
			if ok && a.ToolCallID == call.ID && a.RunID == run && a.SessionID == session && a.Tool == call.Tool && a.Status == app.ApprovalStatusApproved && a.ArgumentsImmutable && reflect.DeepEqual(a.Arguments, args) {
				if approvalID != "" {
					return Result{}, errors.New("mcd_ambiguous_authority")
				}
				approvalID = a.ID
			}
		}
		if approvalID == "" {
			return Result{}, errors.New("mcd_purchase_approval_required")
		}
		// Verify the complete immutable approval above, including policy metadata.
		// The native purchase authority contains only the original quote review;
		// the runtime's verifier decision is not an official purchase field.
		review := make(map[string]any, len(args))
		for key, value := range args {
			if key != "_verifier" {
				review[key] = value
			}
		}
		out, err := h.mcdSelection().Commit(ctx, approvalID, review)
		return Result{Output: out}, err
	}
	name := "mcd.purchase.status"
	effect := app.ToolEffectExternalRead
	if commit {
		name = "mcd.purchase.commit"
		effect = app.ToolEffectExternalInteract
	}
	return workflowRegistration(toolRegistration{enabled: func(c config.Config) bool {
		return c.MCDSelection.Enabled && (!commit || c.MCDSelection.CartEnabled && c.MCDSelection.PurchaseEnabled)
	}, run: ctxArgsSessionRun(run)}, name, nil, app.OutcomeAdapterGeneric, "Use the original authenticated Mac and durable operation.", "Purchase requires an exact prepared quote and immutable user approval.", "No alternate payment channel, speculative retries or replacement attempts.", effect)
}
