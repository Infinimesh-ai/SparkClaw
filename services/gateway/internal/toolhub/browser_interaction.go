package toolhub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type browserSnapshotRecord struct {
	CallID             string
	Index              int
	SnapshotID         string
	PreviousSnapshotID string
	PageID             string
	URL                string
	Digest             string
	ContentDigest      string
	Generation         uint64
	PageGeneration     uint64
	Refs               map[string]bool
	ClickableRefs      map[string]bool
	Labels             map[string]string
	Roles              map[string]string
	Containers         map[string]string
}

func (h *ToolHub) clickBrowserInteraction(ctx context.Context, args map[string]any, sessionID, runID string) (Result, error) {
	if run, ok := h.store.GetRun(runID); ok && run.Workflow != nil && run.Workflow.Plan.ProfileID == app.WorkflowBrowserInteraction {
		snapshotID := strings.TrimSpace(browserAutomationStringValue(args["snapshot_id"]))
		elementRef := strings.TrimSpace(browserAutomationStringValue(args["uid"]))
		calls := h.store.ListToolCalls(sessionID)
		if snapshot, found := findBrowserSnapshotRecord(calls, runID, snapshotID); found {
			if label := strings.TrimSpace(snapshot.Labels[elementRef]); unsafeBrowserInteractionLabel(label) {
				return Result{}, &app.CodedToolError{
					Code: app.ToolErrorUnsafeClickTarget,
					Err:  fmt.Errorf("unsafe click target %q is outside the bounded browser.interaction contract", label),
				}
			}
			if repeatedValidatedBrowserSemanticAction(calls, runID, snapshot, elementRef) {
				return Result{}, &app.CodedToolError{
					Code: app.ToolErrorBrowserInteractionLoop,
					Err:  errors.New("browser click repeats a semantic action whose state transition was already validated"),
				}
			}
		}
	}
	return h.browserAutomationTool(ctx, "browser.click", args, sessionID)
}

func (h *ToolHub) validateBrowserTransition(_ context.Context, args map[string]any, sessionID, runID string) (Result, error) {
	beforeID := strings.TrimSpace(browserAutomationStringValue(args["before_snapshot_id"]))
	afterID := strings.TrimSpace(browserAutomationStringValue(args["after_snapshot_id"]))
	elementRef := strings.TrimSpace(browserAutomationStringValue(args["element_ref"]))
	if beforeID == "" || afterID == "" || beforeID == afterID {
		return Result{}, errors.New("browser.validate_transition requires two different snapshot IDs")
	}
	calls := h.store.ListToolCalls(sessionID)
	before, beforeOK := findBrowserSnapshotRecord(calls, runID, beforeID)
	after, afterOK := findBrowserSnapshotRecord(calls, runID, afterID)
	if !beforeOK || !afterOK || before.Index >= after.Index {
		return Result{}, errors.New("browser.validate_transition snapshots are unavailable or out of order in the current run")
	}
	click, clickIndex, ok := findBrowserClickBetween(calls, runID, before.Index, after.Index, beforeID, elementRef)
	if !ok || !before.Refs[elementRef] {
		return Result{}, errors.New("browser.validate_transition could not bind one click to the before snapshot")
	}
	if after.PreviousSnapshotID != "" && after.PreviousSnapshotID != beforeID {
		return Result{}, errors.New("browser.validate_transition after snapshot does not follow the bound click snapshot")
	}
	if clicked := browserClickOutputRef(click); clicked != "" && clicked != elementRef {
		return Result{}, errors.New("browser.validate_transition click result does not match the selected snapshot element")
	}
	if before.Generation == 0 || after.Generation == 0 || before.Generation != after.Generation {
		return Result{}, errors.New("browser.validate_transition snapshots do not share the current session generation")
	}
	stateChanged := before.ContentDigest != "" && after.ContentDigest != "" && before.ContentDigest != after.ContentDigest
	if !stateChanged || priorValidatedBrowserState(calls, runID, clickIndex, after.ContentDigest) {
		return Result{}, errors.New("browser.validate_transition detected no new page state")
	}
	return Result{Output: map[string]any{
		"schema_version":     2,
		"status":             "validated",
		"code":               "ok",
		"before_snapshot_id": beforeID,
		"after_snapshot_id":  afterID,
		"page_id":            after.PageID,
		"element_ref":        elementRef,
		"session_generation": after.Generation,
		"state_changed":      true,
		"before_digest":      before.ContentDigest,
		"after_digest":       after.ContentDigest,
		"click_count":        completedBrowserClickCount(calls, runID, after.Index),
	}}, nil
}

func (h *ToolHub) assessBrowserGoal(_ context.Context, args map[string]any, sessionID, runID string) (Result, error) {
	snapshotID := strings.TrimSpace(browserAutomationStringValue(args["snapshot_id"]))
	verdict := strings.ToLower(strings.TrimSpace(browserAutomationStringValue(args["verdict"])))
	evidenceRefs := browserInteractionStringSlice(args["evidence_refs"])
	if verdict != "satisfied" && verdict != "success" && verdict != "succeeded" && verdict != "progress" && verdict != "failure" {
		return Result{}, errors.New("browser.assess_goal verdict is unsupported")
	}
	if len(evidenceRefs) == 0 {
		return Result{}, errors.New("browser.assess_goal requires current after-snapshot evidence citations")
	}
	calls := h.store.ListToolCalls(sessionID)
	snapshot, ok := findBrowserSnapshotRecord(calls, runID, snapshotID)
	if !ok {
		return Result{}, errors.New("browser.assess_goal snapshot is unavailable in the current run")
	}
	for _, ref := range evidenceRefs {
		if !snapshot.Refs[ref] {
			return Result{}, fmt.Errorf("browser.assess_goal evidence ref %q is foreign to the current snapshot", ref)
		}
	}
	draftProfile := false
	if run, found := h.store.GetRun(runID); found && run.Workflow != nil {
		draftProfile = run.Workflow.Plan.ProfileID == app.WorkflowBrowserFormDraft
	}
	actionCount := completedBrowserClickCount(calls, runID, snapshot.Index)
	actionLimit := 3
	actionOnlyEvidence := browserGoalEvidenceOnlyOffersActions(snapshot, evidenceRefs)
	if draftProfile {
		actionCount = completedBrowserDraftCount(calls, runID, snapshot.Index)
		actionLimit = app.BrowserFormDraftMaxActions
		actionOnlyEvidence = browserGoalEvidenceOnlyOffersControls(snapshot, evidenceRefs)
	}
	if (verdict == "satisfied" || verdict == "success" || verdict == "succeeded") &&
		actionCount == 0 && actionOnlyEvidence {
		verdict = "progress"
	}
	status, code := verdict, "ok"
	if verdict == "satisfied" || verdict == "success" || verdict == "succeeded" {
		status = "succeeded"
	}
	if verdict == "failure" {
		status, code = "failed", "interaction_goal_failed"
	} else if verdict == "progress" && actionCount >= actionLimit {
		status, code = "failed", "interaction_attempt_limit"
	} else if verdict == "progress" && actionCount == 0 && actionOnlyEvidence {
		code = "action_required"
	}
	return Result{Output: map[string]any{
		"schema_version":     2,
		"status":             status,
		"code":               code,
		"snapshot_id":        snapshotID,
		"page_id":            snapshot.PageID,
		"session_generation": snapshot.Generation,
		"goal_satisfied":     status == "succeeded",
		"evidence_refs":      evidenceRefs,
		"reason_code":        code,
		"click_count":        completedBrowserClickCount(calls, runID, snapshot.Index),
		"draft_action_count": completedBrowserDraftCount(calls, runID, snapshot.Index),
	}}, nil
}

func repeatedValidatedBrowserSemanticAction(calls []app.ToolCall, runID string, current browserSnapshotRecord, elementRef string) bool {
	currentKey := browserInteractionSemanticControlKey(current, elementRef)
	if currentKey == "" {
		return false
	}
	for index := len(calls) - 1; index >= 0; index-- {
		call := calls[index]
		if call.RunID != runID || call.Tool != "browser.validate_transition" || call.Status != "completed" {
			continue
		}
		payload, ok := browserInteractionMap(call.Result)
		if !ok || strings.TrimSpace(browserAutomationStringValue(payload["status"])) != "validated" {
			continue
		}
		beforeID := strings.TrimSpace(browserAutomationStringValue(payload["before_snapshot_id"]))
		priorRef := strings.TrimSpace(browserAutomationStringValue(payload["element_ref"]))
		before, found := findBrowserSnapshotRecord(calls, runID, beforeID)
		if found && browserInteractionSemanticControlKey(before, priorRef) == currentKey {
			return true
		}
	}
	return false
}

func browserInteractionSemanticControlKey(snapshot browserSnapshotRecord, ref string) string {
	role := strings.ToLower(strings.TrimSpace(snapshot.Roles[ref]))
	label := strings.ToLower(strings.TrimSpace(snapshot.Labels[ref]))
	container := strings.ToLower(strings.TrimSpace(snapshot.Containers[ref]))
	if role == "" && label == "" {
		return ""
	}
	return strings.Join([]string{role, label, container}, "\x00")
}

func findBrowserSnapshotRecord(calls []app.ToolCall, runID, snapshotID string) (browserSnapshotRecord, bool) {
	for index := len(calls) - 1; index >= 0; index-- {
		call := calls[index]
		if call.RunID != runID || call.Tool != "browser.snapshot" || call.Status != "completed" {
			continue
		}
		snapshot, ok := browserSnapshotMap(call.Result)
		if !ok || strings.TrimSpace(browserAutomationStringValue(snapshot["snapshot_id"])) != snapshotID {
			continue
		}
		record := browserSnapshotRecord{
			CallID: call.ID, Index: index, SnapshotID: snapshotID,
			PreviousSnapshotID: strings.TrimSpace(browserAutomationStringValue(snapshot["previous_snapshot_id"])),
			PageID:             strings.TrimSpace(browserAutomationStringValue(snapshot["page_id"])),
			URL:                strings.TrimSpace(browserAutomationStringValue(snapshot["url"])),
			Digest:             strings.TrimSpace(browserAutomationStringValue(snapshot["digest"])),
			ContentDigest:      strings.TrimSpace(browserAutomationStringValue(snapshot["content_digest"])),
			Generation:         uint64(intLikeBrowserValue(snapshot["session_generation"])),
			PageGeneration:     uint64(intLikeBrowserValue(snapshot["page_generation"])),
			Refs:               map[string]bool{},
			ClickableRefs:      map[string]bool{},
			Labels:             map[string]string{},
			Roles:              map[string]string{},
			Containers:         map[string]string{},
		}
		for _, raw := range browserInteractionSlice(firstBrowserValue(snapshot["controls"], snapshot["refs"])) {
			control, ok := browserInteractionMap(raw)
			if !ok {
				continue
			}
			if ref := strings.TrimSpace(firstBrowserString(control["ref"], control["element_ref"])); ref != "" {
				record.Refs[ref] = true
				record.Labels[ref] = strings.TrimSpace(firstBrowserString(control["accessible_name"], control["name"]))
				record.Roles[ref] = strings.TrimSpace(firstBrowserString(control["role"]))
				record.Containers[ref] = strings.TrimSpace(firstBrowserString(control["container"]))
			}
		}
		for _, ref := range browserInteractionStringSlice(snapshot["action_refs"]) {
			if record.Refs[ref] {
				record.ClickableRefs[ref] = true
			}
		}
		return record, true
	}
	return browserSnapshotRecord{}, false
}

func browserGoalEvidenceOnlyOffersActions(snapshot browserSnapshotRecord, evidenceRefs []string) bool {
	if len(evidenceRefs) == 0 {
		return false
	}
	for _, ref := range evidenceRefs {
		if !snapshot.ClickableRefs[ref] {
			return false
		}
	}
	return true
}

func browserGoalEvidenceOnlyOffersControls(snapshot browserSnapshotRecord, evidenceRefs []string) bool {
	if len(evidenceRefs) == 0 {
		return false
	}
	for _, ref := range evidenceRefs {
		if !snapshot.Refs[ref] {
			return false
		}
	}
	return true
}

func intLikeBrowserValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	default:
		var parsed int
		_, _ = fmt.Sscan(strings.TrimSpace(browserAutomationStringValue(value)), &parsed)
		return parsed
	}
}

func unsafeBrowserInteractionLabel(label string) bool {
	lower := strings.ToLower(strings.TrimSpace(label))
	if lower == "" {
		return false
	}
	for _, term := range []string{"删除", "移除", "发布", "发送", "购买", "付款", "支付", "下单", "确认订单", "退出登录", "注销", "授权"} {
		if strings.Contains(lower, term) {
			return true
		}
	}
	normalized := " " + strings.Join(strings.FieldsFunc(lower, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}), " ") + " "
	for _, term := range []string{"delete", "remove", "publish", "send", "buy", "purchase", "pay", "checkout", "place order", "confirm order", "log out", "logout", "sign out", "authorize", "grant access"} {
		if strings.Contains(normalized, " "+term+" ") {
			return true
		}
	}
	return false
}

func findBrowserClickBetween(calls []app.ToolCall, runID string, beforeIndex, afterIndex int, snapshotID, elementRef string) (app.ToolCall, int, bool) {
	for index := beforeIndex + 1; index < afterIndex && index < len(calls); index++ {
		call := calls[index]
		if call.RunID != runID || call.Tool != "browser.click" || !browserInteractionToolCallCompleted(call) {
			continue
		}
		if strings.TrimSpace(browserAutomationStringValue(call.Arguments["snapshot_id"])) == snapshotID &&
			strings.TrimSpace(browserAutomationStringValue(call.Arguments["uid"])) == elementRef {
			return call, index, true
		}
	}
	return app.ToolCall{}, 0, false
}

func completedBrowserClickCount(calls []app.ToolCall, runID string, throughIndex int) int {
	count := 0
	for index, call := range calls {
		if index > throughIndex {
			break
		}
		if call.RunID == runID && call.Tool == "browser.click" && browserInteractionToolCallCompleted(call) {
			count++
		}
	}
	return count
}

func completedBrowserDraftCount(calls []app.ToolCall, runID string, throughIndex int) int {
	count := 0
	for index, call := range calls {
		if index > throughIndex {
			break
		}
		if call.RunID == runID && (call.Tool == "browser.type" || call.Tool == "browser.select") && browserInteractionToolCallCompleted(call) {
			count++
		}
	}
	return count
}

func browserInteractionToolCallCompleted(call app.ToolCall) bool {
	return call.Status == "completed" || call.Status == "completed_after_approval"
}

func priorValidatedBrowserState(calls []app.ToolCall, runID string, beforeIndex int, digest string) bool {
	if digest == "" {
		return false
	}
	for index, call := range calls {
		if index >= beforeIndex {
			break
		}
		if call.RunID != runID || call.Tool != "browser.validate_transition" || call.Status != "completed" {
			continue
		}
		output, ok := browserInteractionMap(call.Result)
		if ok && strings.TrimSpace(browserAutomationStringValue(output["after_digest"])) == digest {
			return true
		}
	}
	return false
}

func browserSnapshotMap(value any) (map[string]any, bool) {
	outer, ok := browserInteractionMap(value)
	if !ok {
		return nil, false
	}
	payload := outer
	if nested, ok := browserInteractionMap(outer["output"]); ok {
		payload = nested
	}
	if snapshot, ok := browserInteractionMap(payload["snapshot"]); ok {
		return snapshot, true
	}
	if strings.TrimSpace(browserAutomationStringValue(payload["snapshot_id"])) != "" {
		return payload, true
	}
	return nil, false
}

func browserClickOutputRef(call app.ToolCall) string {
	outer, ok := browserInteractionMap(call.Result)
	if !ok {
		return ""
	}
	payload := outer
	if nested, ok := browserInteractionMap(outer["output"]); ok {
		payload = nested
	}
	return strings.TrimSpace(firstBrowserString(payload["clicked"], payload["element_ref"], payload["ref"]))
}

func browserInteractionMap(value any) (map[string]any, bool) {
	if value == nil {
		return nil, false
	}
	if mapped, ok := value.(map[string]any); ok {
		return mapped, true
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var mapped map[string]any
	if err := json.Unmarshal(raw, &mapped); err != nil {
		return nil, false
	}
	return mapped, true
}

func browserInteractionSlice(value any) []any {
	if values, ok := value.([]any); ok {
		return values
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var values []any
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil
	}
	return values
}

func browserInteractionStringSlice(value any) []string {
	out := []string{}
	switch typed := value.(type) {
	case []string:
		for _, text := range typed {
			if text = strings.TrimSpace(text); text != "" {
				out = append(out, text)
			}
		}
	default:
		for _, raw := range browserInteractionSlice(value) {
			text := strings.TrimSpace(browserAutomationStringValue(raw))
			if text != "" && text != "<nil>" {
				out = append(out, text)
			}
		}
	}
	return out
}

func firstBrowserValue(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstBrowserString(values ...any) string {
	for _, value := range values {
		if text := strings.TrimSpace(browserAutomationStringValue(value)); text != "" && text != "<nil>" {
			return text
		}
	}
	return ""
}

