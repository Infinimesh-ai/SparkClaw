package agent

import (
	"errors"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type workflowOutcomeAdapter func(app.ToolCall, app.WorkflowNodeID) app.ToolOutcome

var workflowOutcomeAdapters = map[app.ToolOutcomeAdapter]workflowOutcomeAdapter{
	app.OutcomeAdapterGeneric:         adaptGenericWorkflowOutcome,
	app.OutcomeAdapterWebSearch:       adaptWebSearchWorkflowOutcome,
	app.OutcomeAdapterWebPage:         adaptWebPageWorkflowOutcome,
	app.OutcomeAdapterWorkspaceSearch: adaptWorkspaceSearchOutcome,
	app.OutcomeAdapterWorkspaceRead:   adaptWorkspaceReadOutcome,
}

func adaptWorkflowOutcome(definition app.ToolDefinition, call app.ToolCall) (app.ToolOutcome, error) {
	adapter, ok := workflowOutcomeAdapters[definition.OutcomeAdapter]
	if !ok {
		return app.ToolOutcome{}, errors.New("tool definition has no registered workflow outcome adapter")
	}
	return adapter(call, call.WorkflowNodeID), nil
}

func adaptGenericWorkflowOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	return app.ToolOutcome{
		ID:         "outcome_" + call.ID,
		ToolCallID: call.ID,
		Tool:       call.Tool,
		NodeID:     nodeID,
		Status:     call.Status,
		Retryable:  call.Status == "failed",
	}
}

func adaptWebSearchWorkflowOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := adaptGenericWorkflowOutcome(call, nodeID)
	output, _ := anyMap(call.Result)
	refs := webSearchResourceRefs(output, call.ID)
	if webSearchResultCount(output) > 0 || len(refs) > 0 {
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalResultsAvailable}
		if len(refs) > 0 {
			outcome.Signals = append(outcome.Signals, app.OutcomeSignalSourcePageAvailable)
		}
		outcome.Refs = refs
	} else if toolCallCompleted(call) {
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalNoResults}
	}
	return outcome
}

func adaptWebPageWorkflowOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := adaptGenericWorkflowOutcome(call, nodeID)
	output, _ := anyMap(call.Result)
	if boolValue(output["auth_challenge_detected"]) || boolValue(output["login_handoff_required"]) {
		outcome.Signals = append(outcome.Signals, app.OutcomeSignalAuthenticationRequired)
	}
	if boolValue(output["needs_structure_snapshot"]) {
		outcome.Signals = append(outcome.Signals, app.OutcomeSignalStructureRequired)
	}
	if text := strings.TrimSpace(stringValue(output["text"])); text != "" && text != "<nil>" {
		outcome.Signals = append(outcome.Signals, app.OutcomeSignalContentAvailable)
	}
	if rawURL := firstNonEmptyString(output["final_url"], output["url"]); rawURL != "" {
		outcome.Refs = append(outcome.Refs, app.ResourceRef{Kind: "url", Ref: rawURL, Provenance: call.ID})
	}
	return outcome
}

func adaptWorkspaceSearchOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := adaptGenericWorkflowOutcome(call, nodeID)
	output, _ := anyMap(call.Result)
	if intLikeValue(output["count"]) > 0 {
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalResultsAvailable}
		for _, raw := range anySlice(output["results"]) {
			item, ok := anyMap(raw)
			if !ok {
				continue
			}
			if path := strings.TrimSpace(stringValue(item["path"])); path != "" && path != "<nil>" {
				outcome.Refs = appendUniqueResourceRefs(outcome.Refs, app.ResourceRef{Kind: "path", Ref: path, Provenance: call.ID})
			}
		}
	} else if toolCallCompleted(call) {
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalNoResults}
	}
	return outcome
}

func adaptWorkspaceReadOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := adaptGenericWorkflowOutcome(call, nodeID)
	output, ok := anyMap(call.Result)
	if ok && toolCallCompleted(call) {
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalContentAvailable}
		if path := firstNonEmptyString(output["rel_path"], output["path"]); path != "" {
			outcome.Refs = []app.ResourceRef{{Kind: "path", Ref: path, Provenance: call.ID}}
		}
	}
	return outcome
}

func applyWorkflowOutcome(run *app.AgentRun, outcome app.ToolOutcome, assessment app.NodeAssessment) (bool, error) {
	if run.Workflow == nil || workflowPlanDigest(run.Workflow.Plan) != run.Workflow.PlanDigest {
		return false, errors.New("persisted workflow plan digest mismatch")
	}
	state, ok := run.Workflow.Nodes[outcome.NodeID]
	if !ok || state.Status != app.WorkflowNodeActive {
		return false, errors.New("tool outcome does not belong to an active workflow node")
	}
	if containsString(state.AppliedOutcomeIDs, outcome.ID) {
		return false, nil
	}
	node, ok := workflowPlanNode(run.Workflow.Plan, outcome.NodeID)
	if !ok {
		return false, errors.New("active workflow node is missing from frozen plan")
	}
	if state.Attempts >= node.MaxAttempts {
		state.Status = app.WorkflowNodeBlocked
		run.Workflow.Nodes[outcome.NodeID] = state
		run.Workflow.Status = app.WorkflowStatusBlocked
		return false, errors.New("workflow node attempt bound is exhausted")
	}
	state.Attempts++
	state.AppliedOutcomeIDs = append(state.AppliedOutcomeIDs, outcome.ID)
	state.ToolCallIDs = appendUniqueString(state.ToolCallIDs, outcome.ToolCallID)
	state.OutcomeRefs = appendUniqueResourceRefs(state.OutcomeRefs, outcome.Refs...)
	state.LastAssessment = &assessment
	if assessment.Status == app.AssessmentComplete {
		state.Status = app.WorkflowNodeSucceeded
		run.Workflow.Nodes[outcome.NodeID] = state
		run.Workflow.ActiveNodeIDs = removeWorkflowNodeID(run.Workflow.ActiveNodeIDs, outcome.NodeID)
		activated := activateReadyWorkflowNodes(run.Workflow)
		if allWorkflowNodesSucceeded(run.Workflow) {
			run.Workflow.Status = app.WorkflowStatusSucceeded
		} else if len(run.Workflow.ActiveNodeIDs) == 0 {
			run.Workflow.Status = app.WorkflowStatusBlocked
			return false, errors.New("workflow has pending nodes whose dependencies cannot be satisfied")
		}
		return activated, nil
	}
	if assessment.Status == app.AssessmentBlocked {
		state.Status = app.WorkflowNodeBlocked
		run.Workflow.Nodes[outcome.NodeID] = state
		run.Workflow.Status = app.WorkflowStatusBlocked
		return false, nil
	}
	for _, transition := range node.Transitions {
		if !transitionPredicateMatches(transition.On, outcome, assessment) {
			continue
		}
		if transition.MaxActivations <= 0 || state.TransitionActivations[transition.ID] >= transition.MaxActivations {
			continue
		}
		if transition.Replace != nil {
			state.CurrentScope = *transition.Replace
		} else {
			state.CurrentScope.Requirements = appendUniqueRequirements(state.CurrentScope.Requirements, transition.Add...)
		}
		state.TransitionActivations[transition.ID]++
		state.ScopeRevision++
		state.LastDirectory = nil
		state.SelectedEntries = nil
		run.Workflow.Nodes[outcome.NodeID] = state
		return true, nil
	}
	state.Status = app.WorkflowNodeBlocked
	run.Workflow.Nodes[outcome.NodeID] = state
	run.Workflow.Status = app.WorkflowStatusBlocked
	return false, errors.New("no frozen workflow transition matched the node assessment")
}

func activateReadyWorkflowNodes(state *app.WorkflowState) bool {
	activated := false
	for _, node := range state.Plan.Nodes {
		nodeState := state.Nodes[node.ID]
		if nodeState.Status != app.WorkflowNodePending || !workflowDependenciesSucceeded(state, node.DependsOn) {
			continue
		}
		nodeState.Status = app.WorkflowNodeActive
		state.Nodes[node.ID] = nodeState
		if !containsWorkflowNodeID(state.ActiveNodeIDs, node.ID) {
			state.ActiveNodeIDs = append(state.ActiveNodeIDs, node.ID)
			activated = true
		}
	}
	return activated
}

func workflowDependenciesSucceeded(state *app.WorkflowState, dependencies []app.WorkflowNodeID) bool {
	for _, dependency := range dependencies {
		if state.Nodes[dependency].Status != app.WorkflowNodeSucceeded {
			return false
		}
	}
	return true
}

func allWorkflowNodesSucceeded(state *app.WorkflowState) bool {
	for _, nodeState := range state.Nodes {
		if nodeState.Status != app.WorkflowNodeSucceeded {
			return false
		}
	}
	return len(state.Nodes) > 0
}

func transitionPredicateMatches(predicate app.TransitionPredicate, outcome app.ToolOutcome, assessment app.NodeAssessment) bool {
	if len(predicate.OutcomeSignals) > 0 && !anyOutcomeSignal(predicate.OutcomeSignals, outcome.Signals) && !anyOutcomeSignal(predicate.OutcomeSignals, assessment.Signals) {
		return false
	}
	if len(predicate.Assessments) > 0 {
		matched := false
		for _, status := range predicate.Assessments {
			if assessment.Status == status {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func webSearchResultCount(output map[string]any) int {
	if results, ok := output["results"].([]any); ok {
		return len(results)
	}
	if results, ok := output["results"].([]map[string]any); ok {
		return len(results)
	}
	switch count := output["count"].(type) {
	case int:
		return count
	case float64:
		return int(count)
	}
	return 0
}

func webSearchResourceRefs(output map[string]any, provenance string) []app.ResourceRef {
	refs := []app.ResourceRef{}
	appendURL := func(value any) {
		raw := strings.TrimSpace(stringValue(value))
		if raw != "" && raw != "<nil>" {
			refs = appendUniqueResourceRefs(refs, app.ResourceRef{Kind: "url", Ref: raw, Provenance: provenance})
		}
	}
	switch citations := output["citations"].(type) {
	case []string:
		for _, citation := range citations {
			appendURL(citation)
		}
	case []any:
		for _, citation := range citations {
			appendURL(citation)
		}
	}
	switch results := output["results"].(type) {
	case []any:
		for _, result := range results {
			if item, ok := anyMap(result); ok {
				appendURL(item["url"])
			}
		}
	case []map[string]any:
		for _, item := range results {
			appendURL(item["url"])
		}
	}
	return refs
}

func containsOutcomeSignal(values []app.OutcomeSignal, want app.OutcomeSignal) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func anyOutcomeSignal(wants, values []app.OutcomeSignal) bool {
	for _, want := range wants {
		if containsOutcomeSignal(values, want) {
			return true
		}
	}
	return false
}

func workflowPlanNode(plan app.WorkflowPlan, nodeID app.WorkflowNodeID) (app.WorkflowNode, bool) {
	for _, node := range plan.Nodes {
		if node.ID == nodeID {
			return node, true
		}
	}
	return app.WorkflowNode{}, false
}

func appendUniqueString(values []string, additions ...string) []string {
	for _, addition := range additions {
		if addition != "" && !containsString(values, addition) {
			values = append(values, addition)
		}
	}
	return values
}

func appendUniqueResourceRefs(values []app.ResourceRef, additions ...app.ResourceRef) []app.ResourceRef {
	seen := map[string]bool{}
	for _, value := range values {
		seen[value.Kind+"\x00"+value.Ref+"\x00"+value.Provenance] = true
	}
	for _, addition := range additions {
		key := addition.Kind + "\x00" + addition.Ref + "\x00" + addition.Provenance
		if addition.Ref != "" && !seen[key] {
			values = append(values, addition)
			seen[key] = true
		}
	}
	return values
}

func appendUniqueRequirements(values []app.CapabilityRequirement, additions ...app.CapabilityRequirement) []app.CapabilityRequirement {
	for _, addition := range additions {
		found := false
		for _, value := range values {
			if value.Name == addition.Name && mapsEqual(value.Qualifiers, addition.Qualifiers) {
				found = true
				break
			}
		}
		if !found {
			values = append(values, addition)
		}
	}
	return values
}

func mapsEqual(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func removeWorkflowNodeID(values []app.WorkflowNodeID, remove app.WorkflowNodeID) []app.WorkflowNodeID {
	out := make([]app.WorkflowNodeID, 0, len(values))
	for _, value := range values {
		if value != remove {
			out = append(out, value)
		}
	}
	return out
}
