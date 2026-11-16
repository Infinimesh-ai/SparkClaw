package toolhub

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// McDonald's definitions are callable only by the deterministic ordering
// workflow. Manual tools, external MCP clients and other model profiles cannot
// turn the reversible cart's approval exemption into a generic write surface.
func (h *ToolHub) requireMCDWorkflow(ctx context.Context, session, id string, nodes ...app.WorkflowNodeID) (app.AgentRun, error) {
	run, ok, err := h.store.GetRun(ctx, id)
	if err != nil {
		return run, err
	}
	if !ok || run.SessionID != session || run.Workflow == nil || run.Workflow.Plan.ProfileID != app.WorkflowMCDOrder || run.Workflow.MCDOrder == nil || len(run.Workflow.ActiveNodeIDs) != 1 || !slices.Contains(nodes, run.Workflow.ActiveNodeIDs[0]) || run.Workflow.Nodes[run.Workflow.ActiveNodeIDs[0]].Status != app.WorkflowNodeActive {
		return run, errors.New("mcd_order_workflow_required")
	}
	return run, nil
}
func validateMCDCartPlan(run app.AgentRun, args map[string]any) error {
	state := run.Workflow.MCDOrder
	if args["store_code"] != state.Store["store_code"] || args["pickup_at"] != state.PickupAt || args["eat_type"] != state.EatType {
		return errors.New("mcd_cart_outside_frozen_plan")
	}
	if args["operation"] == "cart_add_configured" || args["operation"] == "cart_add_simple" {
		expected := []map[string]any{}
		for _, line := range state.Lines {
			raw, _ := json.Marshal(line.Selection)
			var selected map[string]any
			_ = json.Unmarshal(raw, &selected)
			delete(selected, "_answered")
			if groups, ok := selected["combo"].([]any); ok {
				for _, g := range groups {
					if group, ok := g.(map[string]any); ok {
						delete(group, "_complete")
					}
				}
			}
			expected = append(expected, selected)
		}
		want, _ := json.Marshal(expected)
		got, _ := json.Marshal(args["items"])
		if string(want) != string(got) {
			return errors.New("mcd_cart_outside_frozen_plan")
		}
	}
	return nil
}
