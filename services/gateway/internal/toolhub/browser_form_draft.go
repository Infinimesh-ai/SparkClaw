package toolhub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (h *ToolHub) typeBrowserFormDraft(ctx context.Context, args map[string]any, sessionID, runID string) (Result, error) {
	return h.executeBrowserFormDraft(ctx, "browser.type", args, sessionID, runID)
}

func (h *ToolHub) selectBrowserFormDraft(ctx context.Context, args map[string]any, sessionID, runID string) (Result, error) {
	return h.executeBrowserFormDraft(ctx, "browser.select", args, sessionID, runID)
}

func (h *ToolHub) executeBrowserFormDraft(ctx context.Context, operation string, args map[string]any, sessionID, runID string) (Result, error) {
	run, ok := h.store.GetRun(runID)
	if !ok || run.SessionID != sessionID || run.Workflow == nil || run.Workflow.Plan.ProfileID != app.WorkflowBrowserFormDraft {
		return Result{}, draftForbiddenError("browser form mutation is available only inside browser.form_draft")
	}
	snapshotID := strings.TrimSpace(browserAutomationStringValue(args["snapshot_id"]))
	pageID := strings.TrimSpace(browserAutomationStringValue(args["page_id"]))
	elementRef := strings.TrimSpace(browserAutomationStringValue(args["uid"]))
	calls := h.store.ListToolCalls(sessionID)
	snapshot, found := findBrowserSnapshotRecord(calls, runID, snapshotID)
	latest, latestFound := latestBrowserSnapshotRecord(calls, runID)
	if !found || !latestFound || latest.SnapshotID != snapshotID || snapshot.PageID != pageID || !snapshot.Refs[elementRef] ||
		snapshot.Generation == 0 || snapshot.PageGeneration == 0 ||
		uint64(intLikeBrowserValue(args["session_generation"])) != snapshot.Generation ||
		uint64(intLikeBrowserValue(args["page_generation"])) != snapshot.PageGeneration {
		return Result{}, draftStaleError("browser form action is not bound to the latest page snapshot generation")
	}
	if run.Workflow.Browser != nil && !browserDraftTargetMatches(run.Workflow.Browser.Target, snapshot.URL) {
		return Result{}, draftStaleError("browser form action target no longer matches the frozen browser target")
	}
	role := strings.ToLower(strings.TrimSpace(snapshot.Roles[elementRef]))
	label := strings.TrimSpace(snapshot.Labels[elementRef])
	container := strings.TrimSpace(snapshot.Containers[elementRef])
	if !BrowserDraftControlAllowed(operation, role, label, container) {
		return Result{}, draftForbiddenError(fmt.Sprintf("browser form control %q is not an ordinary reversible draft field", label))
	}
	valueKey := "text"
	if operation == "browser.select" {
		valueKey = "value"
	}
	value := strings.TrimSpace(browserAutomationStringValue(args[valueKey]))
	ownerRequest := strings.TrimSpace(run.Workflow.Route.Slots.Query)
	if !BrowserDraftValueAllowed(ownerRequest, value) {
		return Result{}, draftForbiddenError("browser form value is not an exact owner-supplied value from the frozen request")
	}

	result, err := h.browserAutomationTool(ctx, operation, args, sessionID)
	if err != nil {
		if app.ToolErrorCodeFrom(err) == app.ToolErrorSnapshotStale {
			return Result{}, draftStaleError(err.Error())
		}
		return Result{}, err
	}
	output, _ := browserInteractionMap(result.Output)
	if output == nil {
		output = map[string]any{}
	}
	digest := sha256.Sum256([]byte(value))
	output["draft_action_id"] = app.NewID("browser_draft")
	output["operation"] = strings.TrimPrefix(operation, "browser.")
	output["page_id"] = pageID
	output["snapshot_id"] = snapshotID
	output["session_generation"] = snapshot.Generation
	output["page_generation"] = snapshot.PageGeneration
	output["snapshot_digest"] = snapshot.Digest
	output["element_ref"] = elementRef
	output["role"] = role
	output["accessible_name"] = label
	output["form_context"] = container
	output["value_source"] = "owner_request"
	output["value_digest"] = hex.EncodeToString(digest[:])
	result.Output = output
	return result, nil
}

func latestBrowserSnapshotRecord(calls []app.ToolCall, runID string) (browserSnapshotRecord, bool) {
	for index := len(calls) - 1; index >= 0; index-- {
		call := calls[index]
		if call.RunID != runID || call.Tool != "browser.snapshot" || call.Status != "completed" {
			continue
		}
		snapshot, ok := browserSnapshotMap(call.Result)
		if !ok {
			continue
		}
		return findBrowserSnapshotRecord(calls, runID, strings.TrimSpace(browserAutomationStringValue(snapshot["snapshot_id"])))
	}
	return browserSnapshotRecord{}, false
}

func BrowserDraftControlAllowed(operation, role, label, container string) bool {
	if strings.TrimSpace(label) == "" || forbiddenBrowserDraftControl(role, label, container) {
		return false
	}
	role = strings.ToLower(strings.TrimSpace(role))
	switch operation {
	case "browser.type":
		return role == "textbox" || role == "searchbox" || role == "combobox"
	case "browser.select":
		return role == "combobox" || role == "listbox"
	default:
		return false
	}
}

func BrowserDraftValueAllowed(ownerRequest, value string) bool {
	ownerRequest = strings.TrimSpace(ownerRequest)
	value = strings.TrimSpace(value)
	return ownerRequest != "" && value != "" && strings.Contains(ownerRequest, value)
}

func forbiddenBrowserDraftControl(role, label, container string) bool {
	lower := strings.ToLower(strings.TrimSpace(strings.Join([]string{role, label, container}, " ")))
	for _, marker := range []string{
		"password", "passkey", "passcode", "credential", "secret", "token", "one time", "otp", "captcha", "verification code",
		"payment", "card number", "bank", "purchase", "checkout", "delete", "remove", "submit", "send", "publish", "upload",
		"密码", "口令", "密钥", "令牌", "验证码", "校验码", "支付", "付款", "银行卡", "购买", "下单", "删除", "移除", "提交", "发送", "发布", "上传",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	normalized := " " + strings.Join(strings.FieldsFunc(lower, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}), " ") + " "
	for _, marker := range []string{"pin", "cvv", "cvc"} {
		if strings.Contains(normalized, " "+marker+" ") {
			return true
		}
	}
	return false
}

func browserDraftTargetMatches(target app.BrowserTargetDescriptor, candidate string) bool {
	parsed, err := url.Parse(strings.TrimSpace(candidate))
	if err != nil || parsed.Hostname() == "" || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	if target.TargetKind == app.BrowserTargetCurrentTab || strings.TrimSpace(target.CanonicalURL) == "" {
		return true
	}
	expected, err := url.Parse(strings.TrimSpace(target.CanonicalURL))
	return err == nil && strings.EqualFold(expected.Scheme, parsed.Scheme) &&
		strings.EqualFold(expected.Hostname(), parsed.Hostname()) && expected.Port() == parsed.Port()
}

func draftStaleError(message string) error {
	return &app.CodedToolError{Code: app.ToolErrorDraftActionStale, Err: errors.New(message)}
}

func draftForbiddenError(message string) error {
	return &app.CodedToolError{Code: app.ToolErrorDraftForbiddenControl, Err: errors.New(message)}
}
