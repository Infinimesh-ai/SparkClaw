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
	Digest             string
	Generation         uint64
	Refs               map[string]bool
	Labels             map[string]string
}

func (h *ToolHub) clickBrowserInteraction(ctx context.Context, args map[string]any, sessionID, runID string) (Result, error) {
	if run, ok := h.store.GetRun(runID); ok && run.Workflow != nil && run.Workflow.Plan.ProfileID == app.WorkflowBrowserInteraction {
		snapshotID := strings.TrimSpace(browserAutomationStringValue(args["snapshot_id"]))
		elementRef := strings.TrimSpace(browserAutomationStringValue(args["uid"]))
		if snapshot, found := findBrowserSnapshotRecord(h.store.ListToolCalls(sessionID), runID, snapshotID); found {
			if label := strings.TrimSpace(snapshot.Labels[elementRef]); unsafeBrowserInteractionLabel(label) {
				return Result{}, &app.CodedToolError{
					Code: app.ToolErrorUnsafeClickTarget,
					Err:  fmt.Errorf("unsafe click target %q is outside the bounded browser.interaction contract", label),
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
	stateChanged := before.Digest != "" && after.Digest != "" && before.Digest != after.Digest
	if !stateChanged || priorValidatedBrowserState(calls, runID, clickIndex, after.Digest) {
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
		"before_digest":      before.Digest,
		"after_digest":       after.Digest,
		"click_count":        completedBrowserClickCount(calls, runID, after.Index),
	}}, nil
}

func (h *ToolHub) assessBrowserGoal(_ context.Context, args map[string]any, sessionID, runID string) (Result, error) {
	snapshotID := strings.TrimSpace(browserAutomationStringValue(args["snapshot_id"]))
	verdict := strings.ToLower(strings.TrimSpace(browserAutomationStringValue(args["verdict"])))
	reason := strings.TrimSpace(browserAutomationStringValue(args["reason"]))
	evidenceRefs := browserInteractionStringSlice(args["evidence_refs"])
	if verdict != "satisfied" && verdict != "success" && verdict != "progress" && verdict != "failure" {
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
	status, code := verdict, "ok"
	if verdict == "satisfied" || verdict == "success" {
		status = "succeeded"
	}
	clickCount := completedBrowserClickCount(calls, runID, snapshot.Index)
	if verdict == "failure" {
		status, code = "failed", "interaction_goal_failed"
	} else if verdict == "progress" && clickCount >= 3 {
		status, code = "failed", "interaction_attempt_limit"
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
		"reason":             trimBrowserVerificationReason(reason),
		"click_count":        clickCount,
	}}, nil
}

func findBrowserSnapshotRecord(calls []app.ToolCall, runID, snapshotID string) (browserSnapshotRecord, bool) {
	for index, call := range calls {
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
			Digest:             strings.TrimSpace(browserAutomationStringValue(snapshot["digest"])),
			Generation:         uint64(intLikeBrowserValue(snapshot["session_generation"])),
			Refs:               map[string]bool{},
			Labels:             map[string]string{},
		}
		for _, raw := range browserInteractionSlice(firstBrowserValue(snapshot["controls"], snapshot["refs"])) {
			control, ok := browserInteractionMap(raw)
			if !ok {
				continue
			}
			if ref := strings.TrimSpace(firstBrowserString(control["ref"], control["element_ref"])); ref != "" {
				record.Refs[ref] = true
				record.Labels[ref] = strings.TrimSpace(firstBrowserString(control["accessible_name"], control["name"]))
			}
		}
		return record, true
	}
	return browserSnapshotRecord{}, false
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
		if call.RunID != runID || call.Tool != "browser.click" || call.Status != "completed" {
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
		if call.RunID == runID && call.Tool == "browser.click" && call.Status == "completed" {
			count++
		}
	}
	return count
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

func trimBrowserVerificationReason(reason string) string {
	reason = strings.Join(strings.Fields(reason), " ")
	if len(reason) > 500 {
		return reason[:500]
	}
	return reason
}
