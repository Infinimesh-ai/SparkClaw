package agent

import (
	"errors"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// Only values from persisted tool outcomes may select a version or resume a
// range. Model-supplied replacements are discarded before ToolHub validation.
func bindScopedDocumentReadArguments(state *app.WorkflowState, plan toolPlan, args map[string]any) (map[string]any, error) {
	delete(args, "model_view")
	if !isScopedDocumentRead(state) {
		delete(args, "structure_version")
		delete(args, "node_id")
		delete(args, "depth")
		return args, nil
	}
	args["path"] = state.Route.Slots.TargetRef
	delete(args, "structure_version")
	if state.Plan.ProfileRevision >= 9 {
		args["structure_version"] = 2
	}
	if documentUsesModelView(state) && (plan.Capability == app.ToolCapabilityDocumentOutline || plan.Capability == app.ToolCapabilityDocumentReadRange || plan.Capability == app.ToolCapabilityDocumentSummarize) {
		args["model_view"] = true
	}
	delete(args, "include_visuals")
	delete(args, "include_source_locations")
	if state.Plan.ProfileRevision >= 8 && (plan.Capability == app.ToolCapabilityDocumentReadRange || plan.Capability == app.ToolCapabilityDocumentSummarize) {
		args["include_visuals"] = true
		args["include_source_locations"] = true
	}
	for _, key := range []string{"source_sha256", "structure_generation", "cursor", "contract_version"} {
		delete(args, key)
	}
	facts := state.Route.Facts
	if state.Plan.ProfileRevision >= 7 {
		// Legacy section identifiers belong to the old outline, while current
		// outline/search returns canonical node IDs. Expose only canonical
		// node/page selectors to new plans, avoiding conflicting selectors.
		delete(args, "section")
	}
	for _, key := range []string{"source_sha256", "structure_generation"} {
		if value := facts[documentReadFactPrefix+key]; value != "" {
			args[key] = value
		}
	}
	if plan.Capability == app.ToolCapabilityDocumentOutline {
		if state.Plan.ProfileRevision < 9 {
			delete(args, "node_id")
			delete(args, "depth")
		}
		requested := strings.TrimSpace(stringValue(args["node_id"]))
		if requested == "<nil>" {
			requested = ""
		}
		if cursor := facts[documentReadFactPrefix+"outline_next_cursor"]; cursor != "" && (state.Plan.ProfileRevision < 9 || requested == facts[documentReadFactPrefix+"outline_node_id"]) {
			args["cursor"] = cursor
		}
		return args, nil
	}
	mode := strings.TrimSpace(stringValue(args["task_mode"]))
	if plan.Capability == app.ToolCapabilityDocumentSummarize {
		mode = "whole_summary"
		args["contract_version"] = 2
		if state.Plan.ProfileRevision >= 7 {
			args["contract_version"] = 3
		}
		if state.Plan.ProfileRevision >= 8 {
			args["contract_version"] = 4
		}
		if documentUsesModelView(state) {
			args["contract_version"] = 5
		}
	}
	if frozen := facts[documentReadFactPrefix+"task_mode"]; frozen != "" {
		if mode != "" && mode != "<nil>" && mode != frozen {
			return nil, errors.New("document task mode cannot change after scope selection")
		}
		mode = frozen
	}
	args["task_mode"] = mode
	if mode == "" || mode == "<nil>" {
		return nil, errors.New("document task_mode must be selected before reading")
	}
	if state.Plan.ProfileRevision >= 7 && plan.Capability != app.ToolCapabilityDocumentSearch {
		extent := strings.TrimSpace(stringValue(args["scope_extent"]))
		if plan.Capability == app.ToolCapabilityDocumentSummarize {
			extent = "whole_document"
		}
		if frozen := facts[documentReadFactPrefix+"scope_extent"]; frozen != "" {
			if extent != "" && extent != "<nil>" && extent != frozen {
				return nil, errors.New("document scope extent cannot change after declaration")
			}
			extent = frozen
		}
		if extent != "whole_document" && extent != "selected_range" {
			return nil, errors.New("select scope_extent whole_document or selected_range from the user's requested coverage")
		}
		args["scope_extent"] = extent
		if plan.Capability == app.ToolCapabilityDocumentReadRange && extent == "whole_document" && facts[documentReadFactPrefix+"next_cursor"] == "" {
			// Semantic whole-source intent is authoritative. Runtime enumerates
			// the source; the model does not copy a paginated outline's IDs.
			for key := range documentReadSelector(args) {
				delete(args, key)
			}
			args["all"] = true
		}
	}
	if plan.Capability == app.ToolCapabilityDocumentReadRange {
		if state.Plan.ProfileRevision >= 7 {
			args["include_descendants"] = true
			args["acquisition_max_bytes"] = 64 << 10
		}
		if cursor := facts[documentReadFactPrefix+"next_cursor"]; cursor != "" {
			for key := range documentReadSelector(args) {
				delete(args, key)
			}
			args["cursor"] = cursor
		} else if mode != "targeted" && len(stringSliceValue(args["chunk_ids"])) > 0 {
			return nil, errors.New("search chunks cannot establish exhaustive, summary, comparison or calculation scope; declare nodes, a typed range or all")
		}
	}
	if state.Plan.ProfileRevision >= 7 && plan.Capability == app.ToolCapabilityDocumentSearch {
		delete(args, "scope_extent")
	}
	return args, nil
}

func scopedDocumentReadModelArguments(run app.AgentRun, definition app.ToolDefinition) (map[string]bool, []string) {
	bound := map[string]bool{}
	if run.Workflow != nil && run.Workflow.Plan.ProfileID == app.WorkflowDocumentRead {
		bound["structure_version"] = true
		bound["model_view"] = true
		if run.Workflow.Plan.ProfileRevision < 9 {
			bound["node_id"] = true
			bound["depth"] = true
		}
	}
	if !isScopedDocumentRead(run.Workflow) {
		return bound, nil
	}
	capability := ""
	for _, descriptor := range definition.Capabilities {
		if matchesAnyRequirement(descriptor, documentRetrievalScope().Requirements) {
			capability = descriptor.Name
			break
		}
	}
	if capability == "" {
		return bound, nil
	}
	if run.Workflow.Plan.ProfileRevision >= 7 {
		bound["section"] = true
		bound["task_goal"] = true
		bound["include_descendants"] = true
		bound["acquisition_max_bytes"] = true
	}
	bound["include_visuals"] = true
	bound["structure_version"] = true
	if run.Workflow.Plan.ProfileRevision < 9 {
		bound["node_id"] = true
		bound["depth"] = true
	}
	bound["include_source_locations"] = true
	for _, key := range []string{"source_sha256", "structure_generation", "cursor", "contract_version"} {
		bound[key] = true
	}
	if capability == app.ToolCapabilityDocumentOutline {
		return bound, nil
	}
	required := []string{}
	if run.Workflow.Route.Facts[documentReadFactPrefix+"task_mode"] != "" || capability == app.ToolCapabilityDocumentSummarize {
		bound["task_mode"] = true
	} else {
		required = append(required, "task_mode", "scope_reason")
	}
	if run.Workflow.Plan.ProfileRevision >= 7 {
		if capability == app.ToolCapabilityDocumentSearch || capability == app.ToolCapabilityDocumentSummarize || run.Workflow.Route.Facts[documentReadFactPrefix+"scope_extent"] != "" {
			bound["scope_extent"] = true
		} else {
			required = append(required, "scope_extent")
		}
	}
	return bound, required
}

// Bind the persisted, governed user text, never a model-generated replacement.
func documentReadSummaryGoal(run app.AgentRun) string {
	if run.MessageContext == nil {
		return ""
	}
	parts := []string{}
	for _, part := range run.MessageContext.RequestContent.Parts {
		if part.Kind == app.MessagePartText && strings.TrimSpace(part.Text) != "" {
			parts = append(parts, part.Text)
		}
	}
	return strings.Join(parts, "\n")
}
