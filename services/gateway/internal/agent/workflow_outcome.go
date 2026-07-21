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
	app.OutcomeAdapterInfoAnswer:      adaptInfoAnswerWorkflowOutcome,
	app.OutcomeAdapterWeatherPayload:  adaptWeatherPayloadWorkflowOutcome,
	app.OutcomeAdapterWeatherCard:     adaptWeatherCardWorkflowOutcome,
	app.OutcomeAdapterWebPage:         adaptWebPageWorkflowOutcome,
	app.OutcomeAdapterWorkspaceSearch: adaptWorkspaceSearchOutcome,
	app.OutcomeAdapterWorkspaceRead:   adaptWorkspaceReadOutcome,
	app.OutcomeAdapterBrowserHealth:   adaptBrowserHealthOutcome,
	app.OutcomeAdapterBrowserTabs:     adaptBrowserTabsOutcome,
	app.OutcomeAdapterBrowserFocus:    adaptBrowserFocusOutcome,
	app.OutcomeAdapterBrowserOpen:     adaptBrowserOpenOutcome,
	app.OutcomeAdapterBrowserNavigate: adaptBrowserNavigateOutcome,
	app.OutcomeAdapterBrowserSnapshot: adaptBrowserSnapshotOutcome,
	app.OutcomeAdapterBrowserWait:     adaptBrowserWaitOutcome,
	app.OutcomeAdapterBrowserClick:    adaptBrowserClickOutcome,
	app.OutcomeAdapterBrowserVerify:   adaptBrowserVerifyOutcome,
	app.OutcomeAdapterDocumentEdit:    adaptDocumentEditOutcome,
}

func adaptInfoAnswerWorkflowOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := adaptGenericWorkflowOutcome(call, nodeID)
	output, ok := anyMap(call.Result)
	if ok && toolCallCompleted(call) && infoAnswerHasEvidence(output) {
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalInfoAnswerAvailable}
		outcome.Refs = []app.ResourceRef{{Kind: "info_answer", Ref: call.ID, Provenance: call.ID}}
	}
	return outcome
}

func infoAnswerHasEvidence(output map[string]any) bool {
	if value := strings.TrimSpace(stringValue(output["summary"])); value != "" && value != "<nil>" {
		return true
	}
	for _, raw := range anySlice(output["key_facts"]) {
		if fact, ok := anyMap(raw); ok {
			if claim := strings.TrimSpace(stringValue(fact["claim"])); claim != "" && claim != "<nil>" {
				return true
			}
		}
	}
	for _, raw := range anySlice(output["sources"]) {
		source, ok := anyMap(raw)
		if !ok {
			continue
		}
		if snippet := strings.TrimSpace(stringValue(source["snippet"])); snippet != "" && snippet != "<nil>" {
			return true
		}
		for _, snippet := range stringSliceValue(source["snippets"]) {
			if strings.TrimSpace(snippet) != "" {
				return true
			}
		}
	}
	return false
}

func adaptWeatherPayloadWorkflowOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := adaptGenericWorkflowOutcome(call, nodeID)
	output, ok := anyMap(call.Result)
	if ok && toolCallCompleted(call) && intLikeValue(output["schema_version"]) > 0 && strings.TrimSpace(stringValue(output["location"])) != "" {
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalWeatherPayloadAvailable}
		outcome.Refs = []app.ResourceRef{{Kind: "weather_payload", Ref: call.ID, Provenance: call.ID}}
	}
	return outcome
}

func adaptWeatherCardWorkflowOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := adaptGenericWorkflowOutcome(call, nodeID)
	output, ok := anyMap(call.Result)
	if ok && toolCallCompleted(call) && strings.TrimSpace(stringValue(output["media_path"])) != "" {
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalWeatherCardAvailable}
	}
	return outcome
}

func adaptWorkflowOutcome(definition app.ToolDefinition, call app.ToolCall) (app.ToolOutcome, error) {
	adapter, ok := workflowOutcomeAdapters[definition.OutcomeAdapter]
	if !ok {
		return app.ToolOutcome{}, errors.New("tool definition has no registered workflow outcome adapter")
	}
	return adapter(call, call.WorkflowNodeID), nil
}

func adaptGenericWorkflowOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := app.ToolOutcome{
		ID:         "outcome_" + call.ID,
		ToolCallID: call.ID,
		Tool:       call.Tool,
		NodeID:     nodeID,
		Status:     call.Status,
		Retryable:  call.Status == "failed",
	}
	if output, ok := anyMap(call.Result); ok {
		if ref := firstNonEmptyString(output["output_path"], output["screenshot_path"], output["path"]); ref != "" {
			outcome.Refs = []app.ResourceRef{{Kind: "path", Ref: ref, Provenance: call.ID}}
		}
	}
	return outcome
}

func adaptWebSearchWorkflowOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := adaptGenericWorkflowOutcome(call, nodeID)
	output, _ := anyMap(call.Result)
	refs := webSearchResourceRefs(output, call.ID)
	if webSearchResultCount(output) > 0 || len(refs) > 0 || webSearchHasAnswer(output) {
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

func webSearchHasAnswer(output map[string]any) bool {
	for _, key := range []string{"summary", "answer"} {
		value := strings.TrimSpace(stringValue(output[key]))
		if value != "" && value != "<nil>" {
			return true
		}
	}
	return false
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

func adaptBrowserTabsOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := adaptGenericWorkflowOutcome(call, nodeID)
	if !toolCallCompleted(call) {
		return outcome
	}
	outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalTabsScanned}
	for _, raw := range browserOutcomePages(call.Result) {
		page, ok := anyMap(raw)
		if !ok {
			continue
		}
		pageID := firstNonEmptyString(page["page_id"], page["id"], page["pageId"])
		pageURL := normalizeBrowserURL(firstNonEmptyString(page["url"], page["final_url"]))
		if pageID == "" || pageURL == "" {
			continue
		}
		attributes := map[string]string{"url": pageURL}
		if boolValue(page["selected"]) {
			attributes["selected"] = "true"
		}
		outcome.Refs = append(outcome.Refs, app.ResourceRef{Kind: "browser_tab", Ref: pageID, Provenance: call.ID, Attributes: attributes})
	}
	return outcome
}

func adaptBrowserHealthOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := adaptGenericWorkflowOutcome(call, nodeID)
	if !toolCallCompleted(call) {
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalBrowserUnavailable}
		return outcome
	}
	payload := browserOutcomePayload(call.Result)
	if boolValue(payload["ok"]) {
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalBrowserHealthy}
	} else {
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalBrowserUnavailable}
	}
	return outcome
}

func adaptBrowserFocusOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := adaptGenericWorkflowOutcome(call, nodeID)
	if toolCallCompleted(call) {
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalFocusCompleted}
		outcome.Refs = browserPageRefs(call.Result, call.ID)
	}
	return outcome
}

func adaptBrowserOpenOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := adaptGenericWorkflowOutcome(call, nodeID)
	if toolCallCompleted(call) {
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalOpenCompleted}
		outcome.Refs = browserPageRefs(call.Result, call.ID)
	}
	return outcome
}

func adaptBrowserNavigateOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := adaptGenericWorkflowOutcome(call, nodeID)
	if toolCallCompleted(call) {
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalNavigateCompleted}
		payload := browserOutcomePayload(call.Result)
		pageID := firstNonEmptyString(payload["page_id"], call.Arguments["page_id"])
		if pageID != "" {
			outcome.Refs = []app.ResourceRef{{Kind: "browser_page", Ref: pageID, Provenance: call.ID, Attributes: map[string]string{"url": normalizeBrowserURL(firstNonEmptyString(payload["url"]))}}}
		}
	}
	return outcome
}

func adaptBrowserWaitOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := adaptGenericWorkflowOutcome(call, nodeID)
	if toolCallCompleted(call) {
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalWaitCompleted}
	}
	return outcome
}

func adaptBrowserSnapshotOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := adaptGenericWorkflowOutcome(call, nodeID)
	if !toolCallCompleted(call) {
		return outcome
	}
	snapshot, ok := browserSnapshotPayload(call.Result)
	if !ok {
		return outcome
	}
	snapshotID := strings.TrimSpace(stringValue(snapshot["snapshot_id"]))
	pageID := strings.TrimSpace(stringValue(snapshot["page_id"]))
	if snapshotID == "" || pageID == "" {
		return outcome
	}
	outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalSnapshotAvailable}
	if boolValue(snapshot["truncated"]) {
		outcome.Signals = append(outcome.Signals, app.OutcomeSignalSnapshotTruncated)
	}
	common := map[string]string{
		"page_id": pageID, "digest": strings.TrimSpace(stringValue(snapshot["digest"])),
		"previous_snapshot_id": strings.TrimSpace(stringValue(snapshot["previous_snapshot_id"])),
		"repeated":             strings.TrimSpace(stringValue(snapshot["repeated"])),
	}
	outcome.Refs = append(outcome.Refs,
		app.ResourceRef{Kind: "browser_page", Ref: pageID, Provenance: call.ID, Attributes: map[string]string{"url": normalizeBrowserURL(firstNonEmptyString(snapshot["url"]))}},
		app.ResourceRef{Kind: "browser_snapshot", Ref: snapshotID, Provenance: call.ID, Attributes: common},
	)
	for _, raw := range anySlice(firstPresent(snapshot, "controls", "refs")) {
		control, ok := anyMap(raw)
		if !ok {
			continue
		}
		ref := firstNonEmptyString(control["ref"], control["element_ref"])
		if ref == "" {
			continue
		}
		attributes := map[string]string{
			"snapshot_id": snapshotID,
			"page_id":     pageID,
			"role":        firstNonEmptyString(control["role"]),
			"name":        firstNonEmptyString(control["accessible_name"], control["name"]),
			"container":   firstNonEmptyString(control["container"]),
			"fingerprint": firstNonEmptyString(control["fingerprint"]),
		}
		outcome.Refs = append(outcome.Refs, app.ResourceRef{Kind: "browser_element", Ref: ref, Provenance: call.ID, Attributes: attributes})
	}
	return outcome
}

func adaptBrowserClickOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := adaptGenericWorkflowOutcome(call, nodeID)
	if !toolCallCompleted(call) {
		lowerError := strings.ToLower(call.Error)
		if strings.Contains(lowerError, "unsafe click target") {
			outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalUnsafeClickTarget}
		} else if strings.Contains(lowerError, "stale") || strings.Contains(lowerError, "snapshot") {
			outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalSnapshotStale}
		}
		return outcome
	}
	payload := browserOutcomePayload(call.Result)
	ref := firstNonEmptyString(payload["clicked"], call.Arguments["uid"])
	outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalClickCompleted}
	outcome.Refs = []app.ResourceRef{{Kind: "browser_click", Ref: ref, Provenance: call.ID, Attributes: map[string]string{
		"snapshot_id":     firstNonEmptyString(payload["snapshot_id"], call.Arguments["snapshot_id"]),
		"page_id":         firstNonEmptyString(payload["page_id"], call.Arguments["page_id"]),
		"expected_effect": firstNonEmptyString(call.Arguments["expected_effect"]),
	}}}
	return outcome
}

func adaptBrowserVerifyOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := adaptGenericWorkflowOutcome(call, nodeID)
	if !toolCallCompleted(call) {
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalInteractionVerificationFailed}
		return outcome
	}
	payload, _ := anyMap(call.Result)
	status := strings.TrimSpace(stringValue(payload["status"]))
	code := strings.TrimSpace(stringValue(payload["code"]))
	switch {
	case status == "succeeded" && boolValue(payload["goal_satisfied"]):
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalInteractionGoalSatisfied}
	case status == "progress":
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalInteractionProgress}
	case code == "interaction_loop_detected":
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalInteractionLoopDetected}
	case code == "interaction_attempt_limit":
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalInteractionAttemptLimit}
	case code == "unsafe_click_target":
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalUnsafeClickTarget}
	default:
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalInteractionVerificationFailed}
	}
	outcome.Refs = []app.ResourceRef{{Kind: "browser_verification", Ref: call.ID, Provenance: call.ID, Attributes: map[string]string{
		"status": status, "code": code, "reason": strings.TrimSpace(stringValue(payload["reason"])),
		"after_snapshot_id": strings.TrimSpace(stringValue(payload["after_snapshot_id"])),
	}}}
	return outcome
}

func browserOutcomePayload(value any) map[string]any {
	outer, ok := anyMap(value)
	if !ok {
		return map[string]any{}
	}
	if nested, ok := anyMap(outer["output"]); ok {
		return nested
	}
	return outer
}

func browserSnapshotPayload(value any) (map[string]any, bool) {
	payload := browserOutcomePayload(value)
	if snapshot, ok := anyMap(payload["snapshot"]); ok {
		return snapshot, true
	}
	if value := strings.TrimSpace(stringValue(payload["snapshot_id"])); value != "" && value != "<nil>" {
		return payload, true
	}
	return nil, false
}

func browserOutcomePages(value any) []any {
	outer, _ := anyMap(value)
	if pages := anySlice(outer["pages"]); len(pages) > 0 {
		return pages
	}
	return anySlice(browserOutcomePayload(value)["pages"])
}

func browserPageRefs(value any, provenance string) []app.ResourceRef {
	refs := []app.ResourceRef{}
	for _, raw := range browserOutcomePages(value) {
		page, ok := anyMap(raw)
		if !ok || !boolValue(page["selected"]) {
			continue
		}
		pageID := firstNonEmptyString(page["page_id"], page["id"], page["pageId"])
		if pageID != "" {
			refs = append(refs, app.ResourceRef{Kind: "browser_page", Ref: pageID, Provenance: provenance, Attributes: map[string]string{"url": normalizeBrowserURL(firstNonEmptyString(page["url"]))}})
		}
	}
	return refs
}

func adaptDocumentEditOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := adaptGenericWorkflowOutcome(call, nodeID)
	if !toolCallCompleted(call) {
		return outcome
	}
	output, _ := anyMap(call.Result)
	for _, raw := range anySlice(output["outputs"]) {
		if outputPath := strings.TrimSpace(stringValue(raw)); outputPath != "" && outputPath != "<nil>" {
			outcome.Refs = appendUniqueResourceRefs(outcome.Refs, app.ResourceRef{Kind: "path", Ref: outputPath, Provenance: call.ID})
		}
	}
	if outputPath := strings.TrimSpace(stringValue(output["output_path"])); outputPath != "" && outputPath != "<nil>" {
		outcome.Refs = appendUniqueResourceRefs(outcome.Refs, app.ResourceRef{Kind: "path", Ref: outputPath, Provenance: call.ID})
	}
	if len(outcome.Refs) > 0 {
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalEditCompleted}
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
	selectedRefs := outcome.Refs
	if assessment.SelectedRefs != nil {
		selectedRefs = assessment.SelectedRefs
	}
	state.OutcomeRefs = appendUniqueResourceRefs(state.OutcomeRefs, selectedRefs...)
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
		state.Stage = transition.NextStage
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
