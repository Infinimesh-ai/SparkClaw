package agent

import (
	"errors"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func workflowDirectorySelection(run app.AgentRun, state app.WorkflowNodeState, view app.DirectoryView) ([]app.ToolDirectoryEntryID, error) {
	if entryIDs, decided, err := workflowPersistedDecisionSelection(run, state, view); decided || err != nil {
		return entryIDs, err
	}
	if state.CurrentScope.MaterializeAll {
		entryIDs := make([]app.ToolDirectoryEntryID, 0, len(view.Entries))
		for _, entry := range view.Entries {
			entryIDs = append(entryIDs, entry.ID)
		}
		return entryIDs, nil
	}
	if len(state.SelectedEntries) > 0 {
		if len(state.SelectedEntries) != 1 {
			return nil, errors.New("persisted workflow directory selection is no longer eligible")
		}
		if _, eligible := directoryViewEntry(view, state.SelectedEntries[0]); !eligible {
			return nil, errors.New("persisted workflow directory selection is no longer eligible")
		}
		return append([]app.ToolDirectoryEntryID(nil), state.SelectedEntries...), nil
	}
	if len(view.Entries) == 1 {
		return []app.ToolDirectoryEntryID{view.Entries[0].ID}, nil
	}
	return nil, errors.New("multiple tools satisfy the active workflow scope; an explicit decision node is required")
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
