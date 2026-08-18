package agent

import (
	"errors"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func workflowDirectorySelection(run app.AgentRun, state app.WorkflowNodeState, view app.DirectoryView) ([]app.ToolDirectoryEntryID, error) {
	primaryView, primaryState, supportEntryIDs, err := workflowDirectoryPartitions(state, view)
	if err != nil {
		return nil, err
	}
	if entryIDs, decided, err := workflowPersistedDecisionSelection(run, primaryState, primaryView); decided || err != nil {
		return append(entryIDs, supportEntryIDs...), err
	}
	if state.CurrentScope.MaterializeAll {
		entryIDs := make([]app.ToolDirectoryEntryID, 0, len(primaryView.Entries)+len(supportEntryIDs))
		for _, entry := range primaryView.Entries {
			entryIDs = append(entryIDs, entry.ID)
		}
		return append(entryIDs, supportEntryIDs...), nil
	}
	if len(primaryState.SelectedEntries) > 0 {
		if len(primaryState.SelectedEntries) != 1 {
			return nil, errors.New("persisted workflow directory selection is no longer eligible")
		}
		if _, eligible := directoryViewEntry(primaryView, primaryState.SelectedEntries[0]); !eligible {
			return nil, errors.New("persisted workflow directory selection is no longer eligible")
		}
		return append(append([]app.ToolDirectoryEntryID(nil), primaryState.SelectedEntries...), supportEntryIDs...), nil
	}
	if len(primaryView.Entries) == 1 {
		return append([]app.ToolDirectoryEntryID{primaryView.Entries[0].ID}, supportEntryIDs...), nil
	}
	return nil, errors.New("multiple tools satisfy the active workflow scope; an explicit decision node is required")
}

func workflowDirectoryPartitions(state app.WorkflowNodeState, view app.DirectoryView) (app.DirectoryView, app.WorkflowNodeState, []app.ToolDirectoryEntryID, error) {
	primaryView := view
	primaryView.Entries = nil
	supportEntryIDs := make([]app.ToolDirectoryEntryID, 0, len(state.CurrentScope.SupportRequirements))
	supportSet := map[app.ToolDirectoryEntryID]bool{}
	for _, requirement := range state.CurrentScope.SupportRequirements {
		matches := []app.ToolDirectoryEntryID{}
		for _, entry := range view.Entries {
			if matchesAnyRequirement(entry.Capability, []app.CapabilityRequirement{requirement}) {
				matches = append(matches, entry.ID)
			}
		}
		if len(matches) != 1 || supportSet[matches[0]] {
			return app.DirectoryView{}, app.WorkflowNodeState{}, nil, errors.New("support capability requirement must match exactly one directory entry")
		}
		supportSet[matches[0]] = true
		supportEntryIDs = append(supportEntryIDs, matches[0])
	}
	for _, entry := range view.Entries {
		if !supportSet[entry.ID] {
			primaryView.Entries = append(primaryView.Entries, entry)
		}
	}
	primaryState := state
	primaryState.SelectedEntries = nil
	for _, entryID := range state.SelectedEntries {
		if !supportSet[entryID] {
			primaryState.SelectedEntries = append(primaryState.SelectedEntries, entryID)
		}
	}
	return primaryView, primaryState, supportEntryIDs, nil
}

func workflowPersistedDecisionSelection(run app.AgentRun, state app.WorkflowNodeState, view app.DirectoryView) ([]app.ToolDirectoryEntryID, bool, error) {
	if run.Workflow == nil {
		return nil, false, errors.New("workflow state is unavailable during directory selection")
	}
	node, ok := workflowPlanNode(run.Workflow.Plan, view.NodeID)
	if !ok {
		return nil, false, errors.New("active workflow node is missing from the frozen plan")
	}
	decisionDependencies := []app.WorkflowNodeID{}
	for _, dependency := range node.DependsOn {
		dependencyNode, exists := workflowPlanNode(run.Workflow.Plan, dependency)
		if exists && dependencyNode.Goal.Completion == app.CompletionDecision {
			decisionDependencies = append(decisionDependencies, dependency)
		}
	}
	if len(decisionDependencies) == 0 {
		return nil, false, nil
	}
	if len(decisionDependencies) != 1 {
		return nil, true, errors.New("active workflow node has an ambiguous decision dependency")
	}
	decisionState, exists := run.Workflow.Nodes[decisionDependencies[0]]
	if !exists || decisionState.Status != app.WorkflowNodeSucceeded {
		return nil, true, errors.New("active workflow node is missing its completed directory decision")
	}
	refs := []app.ResourceRef{}
	for _, ref := range decisionState.OutcomeRefs {
		if ref.Kind == "tool_directory_entry" {
			refs = append(refs, ref)
		}
	}
	if len(refs) != 1 {
		return nil, true, errors.New("active workflow node has a missing or ambiguous directory decision reference")
	}
	entryID := app.ToolDirectoryEntryID(refs[0].Ref)
	entry, eligible := directoryViewEntry(view, entryID)
	if !eligible {
		return nil, true, errors.New("persisted workflow decision is no longer eligible")
	}
	if capability := strings.TrimSpace(refs[0].Attributes["capability"]); capability != "" && capability != entry.Capability.Name {
		return nil, true, errors.New("persisted workflow decision capability does not match the active directory entry")
	}
	for _, qualifier := range []string{app.CapabilityQualifierFormat, app.CapabilityQualifierOperation} {
		if value := strings.TrimSpace(refs[0].Attributes[qualifier]); value != "" && value != entry.Capability.Qualifiers[qualifier] {
			return nil, true, errors.New("persisted workflow decision qualifiers do not match the active directory entry")
		}
	}
	if len(state.SelectedEntries) > 0 && (len(state.SelectedEntries) != 1 || state.SelectedEntries[0] != entryID) {
		return nil, true, errors.New("materialized workflow selection conflicts with its persisted decision")
	}
	return []app.ToolDirectoryEntryID{entryID}, true, nil
}
