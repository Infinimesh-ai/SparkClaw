package agent

import (
	"errors"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// Only values from persisted tool outcomes may select a version or resume a
// range. Model-supplied replacements are discarded before ToolHub validation.
func bindScopedDocumentReadArguments(state *app.WorkflowState, plan toolPlan, args map[string]any) (map[string]any, error) {
	if !isScopedDocumentRead(state) {
		return args, nil
	}
	args["path"] = state.Route.Slots.TargetRef
	for _, key := range []string{"source_sha256", "structure_generation", "cursor", "contract_version"} {
		delete(args, key)
	}
	facts := state.Route.Facts
	for _, key := range []string{"source_sha256", "structure_generation"} {
		if value := facts[documentReadFactPrefix+key]; value != "" {
			args[key] = value
		}
	}
	if plan.Capability == app.ToolCapabilityDocumentOutline {
		if cursor := facts[documentReadFactPrefix+"outline_next_cursor"]; cursor != "" {
			args["cursor"] = cursor
		}
		return args, nil
	}
	mode := strings.TrimSpace(stringValue(args["task_mode"]))
	if plan.Capability == app.ToolCapabilityDocumentSummarize {
		mode = "whole_summary"
		args["contract_version"] = 2
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
	if plan.Capability == app.ToolCapabilityDocumentReadRange {
		if cursor := facts[documentReadFactPrefix+"next_cursor"]; cursor != "" {
			for key := range documentReadSelector(args) {
				delete(args, key)
			}
			args["cursor"] = cursor
		} else if mode != "targeted" && len(stringSliceValue(args["chunk_ids"])) > 0 {
			return nil, errors.New("search chunks cannot establish exhaustive, summary, comparison or calculation scope; declare nodes, a typed range or all")
		}
	}
	return args, nil
}

func scopedDocumentReadModelArguments(run app.AgentRun, definition app.ToolDefinition) (map[string]bool, []string) {
	bound := map[string]bool{}
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
	for _, key := range []string{"source_sha256", "structure_generation", "cursor", "contract_version"} {
		bound[key] = true
	}
	if capability == app.ToolCapabilityDocumentOutline {
		return bound, nil
	}
	if run.Workflow.Route.Facts[documentReadFactPrefix+"task_mode"] != "" || capability == app.ToolCapabilityDocumentSummarize {
		bound["task_mode"] = true
		return bound, nil
	}
	return bound, []string{"task_mode", "scope_reason"}
}
