package agent

import (
	"encoding/json"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const (
	browserEffectVerificationProjectionSchema = "browser_effect_verification_v1"
	browserPresentationProjectionSchema       = "browser_presentation_verification_v1"
)

func browserWorkflowEvidenceProjection(run app.AgentRun, output any, maxBytes int) (string, bool) {
	if run.Workflow == nil || maxBytes <= 0 || len(run.Workflow.ActiveNodeIDs) != 1 {
		return "", false
	}
	nodeID := run.Workflow.ActiveNodeIDs[0]
	state, ok := run.Workflow.Nodes[nodeID]
	if !ok || state.Stage != browserStageAssessGoalAfterAction && state.Stage != browserStageAssessGoalVisible {
		return "", false
	}
	outputMap, ok := outputAsMap(output)
	if !ok {
		return "", false
	}
	snapshot, ok := browserSnapshotPayload(outputMap)
	if !ok {
		return "", false
	}
	base := browserInteractionSnapshotProjection(snapshot)
	controls := anySlice(base["controls"])
	projection := map[string]any{
		"schema_version": browserEffectVerificationProjectionSchema,
		"goal":           strings.TrimSpace(run.Workflow.Route.Slots.Query),
		"after_state": map[string]any{
			"title": snapshot["title"], "controls": []any{},
			"controls_total": snapshot["controls_total"], "controls_returned": snapshot["controls_returned"],
			"truncated": snapshot["truncated"],
		},
		"coverage": map[string]any{
			"transition": "complete", "after_target_region": "bounded", "complete_for_consumer": true,
		},
		"untrusted": true,
	}
	if state.Stage == browserStageAssessGoalAfterAction {
		projection["action"] = browserProjectedAction(state.OutcomeRefs)
		projection["transition"] = browserProjectedTransition(state.OutcomeRefs)
	} else {
		projection["schema_version"] = browserPresentationProjectionSchema
		projection["presentation"] = browserProjectedPresentation(run.Workflow, state.OutcomeRefs)
		projection["coverage"].(map[string]any)["presentation"] = "bounded"
	}
	return packBrowserWorkflowProjection(projection, controls, maxBytes), true
}

func browserProjectedAction(refs []app.ResourceRef) map[string]any {
	click := app.ResourceRef{}
	for index := len(refs) - 1; index >= 0; index-- {
		if refs[index].Kind == "browser_click" {
			click = refs[index]
			break
		}
	}
	action := map[string]any{"kind": "click"}
	if click.Ref == "" {
		return action
	}
	action["candidate_id"] = browserProjectionLocalControlID(click.Ref)
	for _, ref := range refs {
		if ref.Kind != "browser_element" || ref.Ref != click.Ref {
			continue
		}
		if label := strings.TrimSpace(ref.Attributes["name"]); label != "" {
			action["semantic_label"] = label
		}
		if role := strings.TrimSpace(ref.Attributes["role"]); role != "" {
			action["role"] = role
		}
		break
	}
	return action
}

func browserProjectedTransition(refs []app.ResourceRef) map[string]any {
	transition := map[string]any{
		"settled": false, "rendered_content_changed": false, "route_consistent": false,
		"same_session": false, "repeated_action": browserRepeatedSemanticAction(refs),
	}
	for _, ref := range refs {
		if ref.Kind != "browser_transition" {
			continue
		}
		transition["settled"] = ref.Attributes["settled"] == "true"
		transition["rendered_content_changed"] = ref.Attributes["state_changed"] == "true"
		transition["route_consistent"] = ref.Attributes["route_consistent"] == "true"
		transition["same_session"] = ref.Attributes["same_session"] == "true" && ref.Attributes["session_generation"] != ""
		break
	}
	return transition
}

func browserProjectedPresentation(state *app.WorkflowState, refs []app.ResourceRef) map[string]any {
	presentation := map[string]any{
		"hidden_goal_verified":     state != nil && state.Browser != nil && state.Browser.Result != nil && state.Browser.Result.GoalAssessmentCallID != "",
		"visible_route_consistent": true,
		"content_equivalent":       false,
	}
	if state == nil || state.Browser == nil || state.Browser.Result == nil {
		return presentation
	}
	_, snapshot, ok := browserSnapshotRefs(currentBrowserSnapshotRefs(refs))
	if !ok {
		return presentation
	}
	presentation["content_equivalent"] = state.Browser.Result.HiddenContentDigest != "" &&
		state.Browser.Result.HiddenContentDigest == snapshot.Attributes["content_digest"]
	return presentation
}

func browserRepeatedSemanticAction(refs []app.ResourceRef) bool {
	keys := map[string]int{}
	for _, click := range refs {
		if click.Kind != "browser_click" {
			continue
		}
		key := click.Ref
		for _, element := range refs {
			if element.Kind == "browser_element" && element.Ref == click.Ref {
				key = browserSemanticControlKey(element.Attributes["role"], element.Attributes["name"], element.Attributes["container"])
				break
			}
		}
		if key != "" {
			keys[key]++
			if keys[key] > 1 {
				return true
			}
		}
	}
	return false
}

func packBrowserWorkflowProjection(projection map[string]any, controls []any, maxBytes int) string {
	afterState, _ := anyMap(projection["after_state"])
	for _, control := range controls {
		current := anySlice(afterState["controls"])
		afterState["controls"] = append(current, control)
		raw, err := json.Marshal(projection)
		if err != nil || len(raw) > maxBytes {
			afterState["controls"] = current
			break
		}
	}
	raw, err := json.Marshal(projection)
	if err != nil || len(raw) > maxBytes {
		return ""
	}
	return string(raw)
}

func browserProjectionLocalControlID(ref string) string {
	parts := strings.Split(strings.TrimSpace(ref), ":")
	if len(parts) >= 2 {
		return parts[len(parts)-2]
	}
	return ref
}

func browserSemanticControlKey(role, name, container string) string {
	return strings.ToLower(strings.Join([]string{
		strings.TrimSpace(role), strings.TrimSpace(name), strings.TrimSpace(container),
	}, "\x00"))
}

func workflowProjectionCoverageForStage(run app.AgentRun, stage workflowStageContext, coverage workflowEvidenceProjectionCoverage) workflowEvidenceProjectionCoverage {
	if run.Workflow == nil {
		return coverage
	}
	activeStage := browserOrWorkflowStage(run.Workflow, stage.WorkflowNodeID)
	switch activeStage {
	case browserStageAssessGoalAfterAction:
		coverage.Transition = workflowCoverageComplete
	case browserStageAssessGoalVisible:
		coverage.Transition = workflowCoverageComplete
		coverage.Presentation = workflowCoverageBounded
	}
	return coverage
}
