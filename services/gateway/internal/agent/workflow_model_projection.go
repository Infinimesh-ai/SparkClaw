package agent

import (
	"sort"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// workflowModelToolProjection removes arguments that Runtime can restore from
// the frozen Workflow state. ToolHub's registered definition remains the
// execution authority and validates the fully rebound arguments later.
func workflowModelToolProjection(run app.AgentRun, selectedEntries []app.ToolDirectoryEntryID, definitions []app.ToolDefinition) []app.ToolDefinition {
	if run.Workflow == nil || len(run.Workflow.ActiveNodeIDs) != 1 {
		return definitions
	}
	nodeID := run.Workflow.ActiveNodeIDs[0]
	node, ok := workflowPlanNode(run.Workflow.Plan, nodeID)
	if !ok {
		return definitions
	}
	state, ok := run.Workflow.Nodes[nodeID]
	if !ok {
		return definitions
	}

	projected := make([]app.ToolDefinition, len(definitions))
	copy(projected, definitions)
	for index := range projected {
		bound := runtimeBoundModelArguments(run, node, state, selectedEntries, projected[index])
		if len(bound) > 0 {
			projected[index].InputSchema = projectToolSchemaWithoutArguments(projected[index].InputSchema, bound)
		}
		if required := modelRequiredWorkflowArguments(selectedEntries, projected[index]); len(required) > 0 {
			projected[index].InputSchema = requireToolSchemaArguments(projected[index].InputSchema, required)
		}
	}
	return projected
}

func workflowModelSemanticVariables(definitions []app.ToolDefinition) []string {
	variables := []string{}
	if len(definitions) > 1 {
		variables = append(variables, "eligible_tool")
	}
	for _, definition := range definitions {
		properties, _ := anyMap(definition.InputSchema["properties"])
		names := make([]string, 0, len(properties))
		for name := range properties {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			variables = append(variables, definition.Name+"."+name)
		}
	}
	if len(variables) == 0 && len(definitions) > 0 {
		variables = append(variables, "tool_invocation")
	}
	return uniqueWorkflowArgumentValues(variables)
}

func modelRequiredWorkflowArguments(selectedEntries []app.ToolDirectoryEntryID, definition app.ToolDefinition) []string {
	required := []string{}
	for _, capability := range selectedWorkflowCapabilities(selectedEntries, definition) {
		if capability.Name != app.ToolCapabilityDocumentEdit {
			continue
		}
		policy, ok := registeredAgentDocumentFormatPolicies().operation(
			capability.Qualifiers[app.CapabilityQualifierFormat],
			capability.Qualifiers[app.CapabilityQualifierOperation],
		)
		if ok {
			required = append(required, policy.ModelRequiredArguments...)
		}
	}
	return uniqueWorkflowArgumentValues(required)
}

func runtimeBoundModelArguments(run app.AgentRun, node app.WorkflowNode, state app.WorkflowNodeState, selectedEntries []app.ToolDirectoryEntryID, definition app.ToolDefinition) map[string]bool {
	bound := map[string]bool{}
	capabilities := selectedWorkflowCapabilities(selectedEntries, definition)
	for _, capability := range capabilities {
		for qualifier, value := range capability.Qualifiers {
			if strings.TrimSpace(value) != "" && toolDefinitionDeclaresArgument(definition, qualifier) {
				bound[qualifier] = true
			}
		}
		for _, binding := range node.ArgumentBindings {
			if binding.Capability != capability.Name || !runtimeOwnedWorkflowBinding(binding) || !toolDefinitionDeclaresArgument(definition, binding.Argument) {
				continue
			}
			if _, resolved := runtimeBoundWorkflowArgument(run, node, state, binding); resolved {
				bound[binding.Argument] = true
			}
		}
		if capability.Name == app.ToolCapabilityDocumentEdit {
			policy, ok := registeredAgentDocumentFormatPolicies().operation(
				capability.Qualifiers[app.CapabilityQualifierFormat],
				capability.Qualifiers[app.CapabilityQualifierOperation],
			)
			if ok {
				for _, argument := range policy.RuntimeBoundArguments {
					if toolDefinitionDeclaresArgument(definition, argument) {
						bound[argument] = true
					}
				}
			}
		}
	}
	return bound
}

func selectedWorkflowCapabilities(selectedEntries []app.ToolDirectoryEntryID, definition app.ToolDefinition) []app.CapabilityDescriptor {
	selected := make([]app.CapabilityDescriptor, 0, len(definition.Capabilities))
	for _, capability := range definition.Capabilities {
		if containsDirectoryEntryID(selectedEntries, directoryEntryID(definition, capability)) {
			selected = append(selected, capability)
		}
	}
	return selected
}

func runtimeOwnedWorkflowBinding(binding app.ArgumentBinding) bool {
	if binding.ResourceKind == "browser_element" {
		return false
	}
	return materializedWorkflowResourceKind(binding.ResourceKind)
}

func runtimeBoundWorkflowArgument(run app.AgentRun, node app.WorkflowNode, state app.WorkflowNodeState, binding app.ArgumentBinding) (string, bool) {
	if run.Workflow == nil {
		return "", false
	}
	if run.Workflow.Plan.ProfileID == app.WorkflowBrowserFormDraft &&
		(binding.Capability == app.ToolCapabilityBrowserFormType || binding.Capability == app.ToolCapabilityBrowserFormSelect) {
		if snapshot, ok := currentBrowserFormDraftSnapshot(state.OutcomeRefs); ok {
			var value string
			switch binding.Argument {
			case "page_id":
				value = snapshot.Attributes["page_id"]
			case "snapshot_id":
				value = snapshot.Ref
			case "session_generation", "page_generation":
				value = snapshot.Attributes[binding.Argument]
			}
			if strings.TrimSpace(value) != "" {
				return value, true
			}
		}
	}
	values := workflowBoundArgumentValues(binding, node, run.Workflow.Intent, run.Workflow.Route, state)
	if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		return "", false
	}
	return values[0], true
}

func projectToolSchemaWithoutArguments(schema map[string]any, removed map[string]bool) map[string]any {
	projected := cloneAnyMap(schema)
	properties, ok := anyMap(projected["properties"])
	if !ok {
		return projected
	}
	properties = cloneAnyMap(properties)
	for argument := range removed {
		delete(properties, argument)
	}
	projected["properties"] = properties

	required := toolDefinitionRequiredArgs(projected)
	visibleRequired := make([]string, 0, len(required))
	for _, argument := range required {
		if !removed[argument] {
			visibleRequired = append(visibleRequired, argument)
		}
	}
	sort.Strings(visibleRequired)
	projected["required"] = visibleRequired
	return projected
}

func requireToolSchemaArguments(schema map[string]any, required []string) map[string]any {
	projected := cloneAnyMap(schema)
	properties, ok := anyMap(projected["properties"])
	if !ok {
		return projected
	}
	visibleRequired := toolDefinitionRequiredArgs(projected)
	for _, argument := range required {
		if _, visible := properties[argument]; visible && !containsString(visibleRequired, argument) {
			visibleRequired = append(visibleRequired, argument)
		}
	}
	sort.Strings(visibleRequired)
	projected["required"] = visibleRequired
	return projected
}

func bindSelectedCapabilityQualifiers(state app.WorkflowNodeState, definition app.ToolDefinition, capabilityName string, args map[string]any) bool {
	changed := false
	for _, capability := range selectedWorkflowCapabilities(state.SelectedEntries, definition) {
		if capability.Name != capabilityName {
			continue
		}
		for qualifier, value := range capability.Qualifiers {
			if strings.TrimSpace(value) == "" || !toolDefinitionDeclaresArgument(definition, qualifier) {
				continue
			}
			args[qualifier] = value
			changed = true
		}
	}
	return changed
}
