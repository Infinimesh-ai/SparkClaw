package agent

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

type workflowOutcomeAdapter func(app.ToolCall, app.WorkflowNodeID) app.ToolOutcome

var workflowOutcomeAdapters = map[app.ToolOutcomeAdapter]workflowOutcomeAdapter{
	app.OutcomeAdapterGeneric:             adaptGenericWorkflowOutcome,
	app.OutcomeAdapterWebSearch:           adaptWebSearchWorkflowOutcome,
	app.OutcomeAdapterWeatherPayload:      adaptWeatherPayloadWorkflowOutcome,
	app.OutcomeAdapterWeatherCard:         adaptWeatherCardWorkflowOutcome,
	app.OutcomeAdapterWebPage:             adaptWebPageWorkflowOutcome,
	app.OutcomeAdapterWorkspaceSearch:     adaptWorkspaceSearchOutcome,
	app.OutcomeAdapterWorkspaceRead:       adaptWorkspaceReadOutcome,
	app.OutcomeAdapterBrowserHealth:       adaptBrowserHealthOutcome,
	app.OutcomeAdapterBrowserTabs:         adaptBrowserTabsOutcome,
	app.OutcomeAdapterBrowserFocus:        adaptBrowserFocusOutcome,
	app.OutcomeAdapterBrowserOpen:         adaptBrowserOpenOutcome,
	app.OutcomeAdapterBrowserClose:        adaptBrowserCloseOutcome,
	app.OutcomeAdapterBrowserNavigate:     adaptBrowserNavigateOutcome,
	app.OutcomeAdapterBrowserSnapshot:     adaptBrowserSnapshotOutcome,
	app.OutcomeAdapterBrowserPublicTarget: adaptBrowserPublicTargetOutcome,
	app.OutcomeAdapterBrowserVisual:       adaptBrowserVisualOutcome,
	app.OutcomeAdapterBrowserWait:         adaptBrowserWaitOutcome,
	app.OutcomeAdapterBrowserClick:        adaptBrowserClickOutcome,
	app.OutcomeAdapterBrowserForm:         adaptBrowserFormOutcome,
	app.OutcomeAdapterBrowserTransition:   adaptBrowserTransitionOutcome,
	app.OutcomeAdapterBrowserGoal:         adaptBrowserGoalOutcome,
	app.OutcomeAdapterDocumentEdit:        adaptDocumentEditOutcome,
	app.OutcomeAdapterScheduleList:        adaptScheduleListOutcome,
	app.OutcomeAdapterScheduleChange:      adaptScheduleChangeOutcome,
}

func adaptBrowserPublicTargetOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := adaptGenericWorkflowOutcome(call, nodeID)
	if !toolCallCompleted(call) {
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalPublicTargetUnavailable}
		return outcome
	}
	output, ok := anyMap(call.Result)
	if !ok || firstNonEmptyString(output["status"]) != "resolved" {
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalPublicTargetUnavailable}
		return outcome
	}
	finalURL := normalizeBrowserURL(firstNonEmptyString(output["normalized_final_url"]))
	evidenceID := firstNonEmptyString(output["evidence_id"])
	if finalURL == "" || evidenceID == "" {
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalPublicTargetUnavailable}
		return outcome
	}
	attributes := map[string]string{}
	for _, key := range []string{"resolution_source", "owner_target_phrase", "requested_surface_kind", "info_request_id", "source_result_ref", "canonical_entry_url", "normalized_final_url", "safety_gate_status", "created_at"} {
		if value := firstNonEmptyString(output[key]); value != "" {
			attributes[key] = value
		}
	}
	attributes["info_result_index"] = strconv.Itoa(intLikeValue(output["info_result_index"]))
	outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalPublicTargetResolved}
	outcome.Refs = []app.ResourceRef{
		{Kind: "public_target_evidence", Ref: evidenceID, Provenance: call.ID, Attributes: attributes},
		{Kind: "public_target_url", Ref: finalURL, Provenance: call.ID, Attributes: map[string]string{"evidence_id": evidenceID}},
	}
	return outcome
}

func adaptBrowserFormOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := adaptGenericWorkflowOutcome(call, nodeID)
	if !toolCallCompleted(call) {
		switch app.ToolErrorCode(call.ErrorCode) {
		case app.ToolErrorDraftActionStale:
			outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalSnapshotStale}
		case app.ToolErrorDraftForbiddenControl:
			outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalDraftActionForbidden}
		}
		return outcome
	}
	operation := strings.TrimPrefix(call.Tool, "browser.")
	elementRef := firstNonEmptyString(call.Arguments["uid"])
	payload := browserDraftOutcomePayload(call.Result)
	sessionGeneration := browserSessionGenerationString(payload["session_generation"])
	if sessionGeneration == "" {
		sessionGeneration = browserSessionGenerationString(call.Arguments["session_generation"])
	}
	pageGeneration := browserSessionGenerationString(payload["page_generation"])
	if pageGeneration == "" {
		pageGeneration = browserSessionGenerationString(call.Arguments["page_generation"])
	}
	outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalDraftActionCompleted}
	outcome.Refs = []app.ResourceRef{{Kind: "browser_draft", Ref: elementRef, Provenance: call.ID, Attributes: map[string]string{
		"action_id": firstNonEmptyString(payload["draft_action_id"], call.ID),
		"operation": operation, "page_id": firstNonEmptyString(payload["page_id"], call.Arguments["page_id"]), "snapshot_id": firstNonEmptyString(payload["snapshot_id"], call.Arguments["snapshot_id"]),
		"session_generation": sessionGeneration,
		"page_generation":    pageGeneration,
		"snapshot_digest":    firstNonEmptyString(payload["snapshot_digest"]), "role": firstNonEmptyString(payload["role"]),
		"name": firstNonEmptyString(payload["accessible_name"]), "container": firstNonEmptyString(payload["form_context"]),
		"value_source": firstNonEmptyString(payload["value_source"]), "value_digest": firstNonEmptyString(payload["value_digest"]),
	}}}
	return outcome
}

func browserDraftOutcomePayload(value any) map[string]any {
	outer, ok := anyMap(value)
	if !ok {
		return map[string]any{}
	}
	if firstNonEmptyString(outer["draft_action_id"]) != "" {
		return outer
	}
	return browserOutcomePayload(value)
}

func adaptBrowserVisualOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := adaptGenericWorkflowOutcome(call, nodeID)
	if !toolCallCompleted(call) {
		if app.ToolErrorCode(call.ErrorCode) == app.ToolErrorVisualEvidenceStale {
			outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalVisualEvidenceStale}
		}
		return outcome
	}
	output, ok := anyMap(call.Result)
	if !ok || firstNonEmptyString(output["status"]) != "completed" || firstNonEmptyString(output["evidence_id"]) == "" {
		return outcome
	}
	attributes := map[string]string{}
	for _, key := range []string{"reason", "page_id", "snapshot_id", "snapshot_digest", "normalized_url", "screenshot_ref", "screenshot_digest", "summary", "model", "profile", "lane", "created_at"} {
		if value := firstNonEmptyString(output[key]); value != "" {
			attributes[key] = value
		}
	}
	attributes["session_generation"] = browserSessionGenerationString(output["session_generation"])
	attributes["page_generation"] = browserSessionGenerationString(output["page_generation"])
	outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalVisualEvidenceAvailable}
	outcome.Refs = []app.ResourceRef{{Kind: "browser_visual_evidence", Ref: firstNonEmptyString(output["evidence_id"]), Provenance: call.ID, Attributes: attributes}}
	return outcome
}

func adaptWeatherPayloadWorkflowOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := adaptGenericWorkflowOutcome(call, nodeID)
	output, ok := anyMap(call.Result)
	if ok && toolCallCompleted(call) && intLikeValue(output["schema_version"]) == toolhub.WeatherPayloadSchemaVersion &&
		firstNonEmptyString(output["request_id"]) != "" &&
		firstNonEmptyString(output["location"]) != "" {
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

func adaptScheduleListOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := adaptGenericWorkflowOutcome(call, nodeID)
	if !toolCallCompleted(call) {
		return outcome
	}
	output, _ := anyMap(call.Result)
	outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalSchedulesListed}
	for _, raw := range anySlice(output["reminders"]) {
		item, ok := anyMap(raw)
		if !ok {
			continue
		}
		id := strings.TrimSpace(stringValue(item["reminder_id"]))
		if id == "" || id == "<nil>" {
			continue
		}
		attributes := map[string]string{}
		for _, key := range []string{"text", "text_summary", "due_time", "timezone", "recurrence", "channel", "status", "updated_at"} {
			value := strings.TrimSpace(stringValue(item[key]))
			if value != "" && value != "<nil>" {
				attributes[key] = value
			}
		}
		outcome.Refs = append(outcome.Refs, app.ResourceRef{Kind: "schedule", Ref: id, Provenance: call.ID, Attributes: attributes})
	}
	return outcome
}

func adaptScheduleChangeOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := adaptGenericWorkflowOutcome(call, nodeID)
	if output, ok := anyMap(call.Result); ok && toolCallCompleted(call) && strings.TrimSpace(stringValue(output["reminder_id"])) != "" {
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalScheduleChanged}
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
		coverage := projectDocumentReadCoverage(call, output)
		contentAvailable := !coverage.Applies || coverage.CoverageStatus == "complete" ||
			(coverage.CoverageStatus == "partial" && strings.TrimSpace(firstNonEmptyString(output["content"])) != "")
		if contentAvailable {
			outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalContentAvailable}
		}
		if path := firstNonEmptyString(output["rel_path"], output["path"]); path != "" {
			outcome.Refs = []app.ResourceRef{{Kind: "path", Ref: path, Provenance: call.ID, Attributes: coverage.attributes()}}
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
		pageID := firstNonEmptyString(page["page_id"])
		pageURL := normalizeBrowserURL(firstNonEmptyString(page["url"], page["final_url"]))
		if pageID == "" || pageURL == "" {
			continue
		}
		attributes := browserOutcomeIdentityAttributes(page, map[string]string{"url": pageURL})
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
	if browserHealthHealthy(payload) {
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalBrowserHealthy}
	} else {
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalBrowserUnavailable}
	}
	return outcome
}

func browserHealthHealthy(payload map[string]any) bool {
	if value, exists := payload["ok"]; exists {
		return boolValue(value)
	}
	return strings.EqualFold(strings.TrimSpace(stringValue(payload["status"])), "ok")
}

func adaptBrowserFocusOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := adaptGenericWorkflowOutcome(call, nodeID)
	if toolCallCompleted(call) {
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalFocusCompleted}
		outcome.Refs = browserPageRefs(call.Result, call.ID)
		if len(outcome.Refs) == 0 {
			payload := browserOutcomePayload(call.Result)
			pageID := firstNonEmptyString(payload["page_id"], call.Arguments["page_id"])
			if pageID != "" {
				outcome.Refs = []app.ResourceRef{{
					Kind: "browser_page", Ref: pageID, Provenance: call.ID,
					Attributes: browserOutcomeIdentityAttributes(payload, map[string]string{
						"url": normalizeBrowserURL(firstNonEmptyString(payload["url"], payload["current_url"])),
					}),
				}}
			}
		}
	}
	return outcome
}

func adaptBrowserOpenOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := adaptGenericWorkflowOutcome(call, nodeID)
	if toolCallCompleted(call) {
		if browserToolOutcomeRequiresAuthBlock(call) {
			outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalAuthenticationRequired}
			return outcome
		}
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalOpenCompleted}
		outcome.Refs = browserPageRefs(call.Result, call.ID)
	}
	return outcome
}

func adaptBrowserCloseOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := adaptGenericWorkflowOutcome(call, nodeID)
	if toolCallCompleted(call) {
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalCloseCompleted}
	}
	return outcome
}

func adaptBrowserNavigateOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := adaptGenericWorkflowOutcome(call, nodeID)
	if toolCallCompleted(call) {
		if browserToolOutcomeRequiresAuthBlock(call) {
			outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalAuthenticationRequired}
			return outcome
		}
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalNavigateCompleted}
		payload := browserOutcomePayload(call.Result)
		pageID := firstNonEmptyString(payload["page_id"], call.Arguments["page_id"])
		if pageID != "" {
			outcome.Refs = []app.ResourceRef{{
				Kind: "browser_page", Ref: pageID, Provenance: call.ID,
				Attributes: browserOutcomeIdentityAttributes(payload, map[string]string{
					"url": normalizeBrowserURL(firstNonEmptyString(payload["url"])),
				}),
			}}
		}
	}
	return outcome
}

func browserToolOutcomeRequiresAuthBlock(call app.ToolCall) bool {
	assessment := assessBrowserAuthentication(call, browserLoginToolFields(call))
	if assessment.State == browserAuthChallenged {
		return true
	}
	return assessment.State == browserAuthUnknown && containsString(assessment.Signals, "untrusted_auth_text")
}

func adaptBrowserWaitOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := adaptGenericWorkflowOutcome(call, nodeID)
	if toolCallCompleted(call) {
		payload := browserOutcomePayload(call.Result)
		if strings.EqualFold(firstNonEmptyString(call.Arguments["mode"]), "stable_state") &&
			strings.EqualFold(firstNonEmptyString(payload["status"]), "stable") {
			outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalTargetSettled}
			if pageID := firstNonEmptyString(payload["page_id"], call.Arguments["page_id"]); pageID != "" {
				outcome.Refs = []app.ResourceRef{{
					Kind:       "browser_page",
					Ref:        pageID,
					Provenance: call.ID,
					Attributes: browserOutcomeIdentityAttributes(payload, map[string]string{
						"url":          normalizeBrowserEvidenceURL(firstNonEmptyString(payload["url"])),
						"state_digest": firstNonEmptyString(payload["state_digest"]),
					}),
				}}
			}
		} else {
			outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalWaitCompleted}
		}
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
		"content_digest":       strings.TrimSpace(stringValue(snapshot["content_digest"])),
		"previous_snapshot_id": strings.TrimSpace(stringValue(snapshot["previous_snapshot_id"])),
		"repeated":             strings.TrimSpace(stringValue(snapshot["repeated"])),
	}
	common = browserOutcomeIdentityAttributes(snapshot, common)
	pageAttributes := browserOutcomeIdentityAttributes(snapshot, map[string]string{
		"url": normalizeBrowserEvidenceURL(firstNonEmptyString(snapshot["url"])),
	})
	outcome.Refs = append(outcome.Refs,
		app.ResourceRef{Kind: "browser_page", Ref: pageID, Provenance: call.ID, Attributes: pageAttributes},
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
			"short_ref":   firstNonEmptyString(control["short_ref"]),
			"role":        firstNonEmptyString(control["role"]),
			"name":        firstNonEmptyString(control["accessible_name"], control["name"]),
			"container":   firstNonEmptyString(control["container"]),
			"fingerprint": firstNonEmptyString(control["fingerprint"]),
		}
		attributes = browserOutcomeIdentityAttributes(snapshot, attributes)
		outcome.Refs = append(outcome.Refs, app.ResourceRef{Kind: "browser_element", Ref: ref, Provenance: call.ID, Attributes: attributes})
	}
	return outcome
}

func adaptBrowserClickOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := adaptGenericWorkflowOutcome(call, nodeID)
	if !toolCallCompleted(call) {
		switch app.ToolErrorCode(call.ErrorCode) {
		case app.ToolErrorUnsafeClickTarget:
			outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalUnsafeClickTarget}
		case app.ToolErrorSnapshotStale:
			outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalSnapshotStale}
		case app.ToolErrorBrowserInteractionLoop:
			outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalInteractionLoopDetected}
		case "":
			// Fallback prose matching for tool calls persisted before
			// ErrorCode existed and for unclassified adapter errors.
			lowerError := strings.ToLower(call.Error)
			if strings.Contains(lowerError, "unsafe click target") {
				outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalUnsafeClickTarget}
			} else if strings.Contains(lowerError, "stale") || strings.Contains(lowerError, "snapshot") {
				outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalSnapshotStale}
			}
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

func adaptBrowserTransitionOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := adaptGenericWorkflowOutcome(call, nodeID)
	if !toolCallCompleted(call) {
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalInteractionVerificationFailed}
		return outcome
	}
	payload, _ := anyMap(call.Result)
	if strings.TrimSpace(stringValue(payload["status"])) != "validated" || !boolValue(payload["state_changed"]) {
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalInteractionVerificationFailed}
		return outcome
	}
	outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalTargetValidated}
	outcome.Refs = []app.ResourceRef{{
		Kind: "browser_transition", Ref: call.ID, Provenance: call.ID,
		Attributes: map[string]string{
			"before_snapshot_id": strings.TrimSpace(stringValue(payload["before_snapshot_id"])),
			"after_snapshot_id":  strings.TrimSpace(stringValue(payload["after_snapshot_id"])),
			"after_digest":       strings.TrimSpace(stringValue(payload["after_digest"])),
			"session_generation": browserSessionGenerationString(payload["session_generation"]),
			"state_changed":      strconv.FormatBool(boolValue(payload["state_changed"])),
			"settled":            "true",
			"route_consistent":   "true",
			"same_session":       "true",
		},
	}}
	return outcome
}

func adaptBrowserGoalOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
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
	case code == "interaction_attempt_limit":
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalInteractionAttemptLimit}
	default:
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalInteractionVerificationFailed}
	}
	evidenceRefs := stringSliceValue(payload["evidence_refs"])
	outcome.Refs = []app.ResourceRef{{
		Kind: "browser_goal_assessment", Ref: call.ID, Provenance: call.ID,
		Attributes: map[string]string{
			"status":        status,
			"code":          code,
			"snapshot_id":   strings.TrimSpace(stringValue(payload["snapshot_id"])),
			"evidence_refs": strings.Join(evidenceRefs, ","),
			"reason_code":   strings.TrimSpace(stringValue(payload["reason_code"])),
		},
	}}
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
		pageID := firstNonEmptyString(page["page_id"])
		if pageID != "" {
			refs = append(refs, app.ResourceRef{
				Kind:       "browser_page",
				Ref:        pageID,
				Provenance: provenance,
				Attributes: browserOutcomeIdentityAttributes(page, map[string]string{
					"url": normalizeBrowserURL(firstNonEmptyString(page["url"])),
				}),
			})
		}
	}
	return refs
}

func browserOutcomeIdentityAttributes(payload map[string]any, attributes map[string]string) map[string]string {
	if attributes == nil {
		attributes = map[string]string{}
	}
	if generation := browserSessionGenerationString(payload["session_generation"]); generation != "" {
		attributes["session_generation"] = generation
	}
	for _, key := range []string{
		"provider_session_ref", "presentation", "page_generation",
		"owner_id", "profile_id", "browser_profile_id",
	} {
		if value := firstNonEmptyString(payload[key]); value != "" {
			attributes[key] = value
		}
	}
	if attributes["profile_id"] == "" {
		attributes["profile_id"] = attributes["browser_profile_id"]
	}
	delete(attributes, "browser_profile_id")
	return attributes
}

func browserSessionGenerationString(value any) string {
	const maxExactJSONInteger = uint64(1<<53 - 1)
	var candidate string
	switch typed := value.(type) {
	case string:
		candidate = strings.TrimSpace(typed)
	case json.Number:
		candidate = typed.String()
	case uint64:
		if typed <= maxExactJSONInteger {
			return strconv.FormatUint(typed, 10)
		}
		return ""
	case uint:
		if uint64(typed) <= maxExactJSONInteger {
			return strconv.FormatUint(uint64(typed), 10)
		}
		return ""
	case int:
		if typed > 0 && uint64(typed) <= maxExactJSONInteger {
			return strconv.FormatUint(uint64(typed), 10)
		}
		return ""
	case int64:
		if typed > 0 && uint64(typed) <= maxExactJSONInteger {
			return strconv.FormatUint(uint64(typed), 10)
		}
		return ""
	case float64:
		if typed > 0 && typed <= float64(maxExactJSONInteger) && typed == float64(uint64(typed)) {
			return strconv.FormatUint(uint64(typed), 10)
		}
		return ""
	case float32:
		const maxExactFloat32Integer = uint64(1<<24 - 1)
		if typed > 0 && typed <= float32(maxExactFloat32Integer) && typed == float32(uint64(typed)) {
			return strconv.FormatUint(uint64(typed), 10)
		}
		return ""
	default:
		return ""
	}
	if parsed, err := strconv.ParseUint(candidate, 10, 64); err == nil && parsed <= maxExactJSONInteger {
		return strconv.FormatUint(parsed, 10)
	}
	parsed, err := strconv.ParseFloat(candidate, 64)
	if err != nil || parsed <= 0 || parsed > float64(maxExactJSONInteger) || parsed != float64(uint64(parsed)) {
		return ""
	}
	return strconv.FormatUint(uint64(parsed), 10)
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
		if state.TransitionActivations == nil {
			state.TransitionActivations = make(map[app.TransitionID]int)
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

func activeWorkflowNodeUsesModelAnswer(state *app.WorkflowState) bool {
	if state == nil || len(state.ActiveNodeIDs) != 1 {
		return false
	}
	node, ok := workflowPlanNode(state.Plan, state.ActiveNodeIDs[0])
	return ok && node.Goal.Completion == app.CompletionModelAnswer
}

func activeWorkflowNodeUsesMessageContent(state *app.WorkflowState) bool {
	if state == nil || len(state.ActiveNodeIDs) != 1 {
		return false
	}
	node, ok := workflowPlanNode(state.Plan, state.ActiveNodeIDs[0])
	return ok && node.Goal.Completion == app.CompletionMessage
}

func completeActiveNoToolNode(run *app.AgentRun, completion app.CompletionRule) error {
	if run.Workflow == nil || workflowPlanDigest(run.Workflow.Plan) != run.Workflow.PlanDigest {
		return errors.New("persisted workflow plan digest mismatch")
	}
	if len(run.Workflow.ActiveNodeIDs) != 1 {
		return errors.New("no-tool workflow requires one active node")
	}
	nodeID := run.Workflow.ActiveNodeIDs[0]
	node, ok := workflowPlanNode(run.Workflow.Plan, nodeID)
	if !ok || node.Goal.Completion != completion || (completion != app.CompletionModelAnswer && completion != app.CompletionMessage) {
		return errors.New("active workflow node has the wrong no-tool completion rule")
	}
	state := run.Workflow.Nodes[nodeID]
	if state.Status != app.WorkflowNodeActive || len(state.CurrentScope.Requirements) != 0 {
		return errors.New("no-tool workflow node has an invalid active scope")
	}
	state.Attempts++
	state.Status = app.WorkflowNodeSucceeded
	run.Workflow.Nodes[nodeID] = state
	run.Workflow.ActiveNodeIDs = removeWorkflowNodeID(run.Workflow.ActiveNodeIDs, nodeID)
	activateReadyWorkflowNodes(run.Workflow)
	if allWorkflowNodesSucceeded(run.Workflow) {
		run.Workflow.Status = app.WorkflowStatusSucceeded
		return nil
	}
	return errors.New("model-answer workflow has unsatisfied dependent nodes")
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
