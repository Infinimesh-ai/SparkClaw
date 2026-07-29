package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

const (
	browserLoginReplyCompleted    = "completed"
	browserLoginReplyWrongPage    = "wrong_page"
	browserLoginReplyCancel       = "cancel"
	browserLoginReplyAmbiguous    = "ambiguous"
	browserHandoffTransitionLease = 2 * time.Minute
)

func (r Runtime) recordBrowserLoginBlockFromToolCall(sessionID, runID, goal string, plan toolPlan, call app.ToolCall) (app.BrowserLoginBlock, bool) {
	if !browserToolMayCreateLoginBlock(call.Tool) || !toolCallCompleted(call) {
		return app.BrowserLoginBlock{}, false
	}
	output := browserLoginToolFields(call)
	r.store.AddAudit(app.AuditEvent{
		SessionID: sessionID,
		RunID:     runID,
		Actor:     "runtime",
		Type:      "browser_auth.assessed",
		Summary:   firstNonEmptyString(output["auth_evidence_state"], "unknown"),
		Fields: map[string]any{
			"tool_call_id": call.ID,
			"tool":         call.Tool,
			"state":        output["auth_evidence_state"],
			"confidence":   output["auth_evidence_confidence"],
			"signals":      output["auth_evidence_signals"],
		},
	})
	if !browserOutputNeedsLoginBlock(output) {
		return app.BrowserLoginBlock{}, false
	}
	resumeArgs := clonePlanArgs(plan.Args)
	targetURL := firstNonEmptyString(resumeArgs["url"], output["url"], output["final_url"], output["login_handoff_url"])
	if targetURL == "" {
		targetURL = r.recentBrowserURLForLoginBlock(sessionID, runID, call.ID)
	}
	if targetURL == "" {
		return app.BrowserLoginBlock{}, false
	}
	resumeArgs["url"] = targetURL
	if firstNonEmptyString(output["url"]) == "" {
		output["url"] = targetURL
	}
	if firstNonEmptyString(output["final_url"]) == "" {
		output["final_url"] = targetURL
	}
	if firstNonEmptyString(output["login_handoff_url"]) == "" {
		output["login_handoff_url"] = targetURL
	}
	if firstNonEmptyString(output["auth_site_origin"]) == "" {
		output["auth_site_origin"] = browserLoginURLOrigin(targetURL)
	}
	existing, hasExisting := r.store.FindActiveBrowserLoginBlock(sessionID)
	revalidatingHidden := hasExisting && existing.RunID == runID &&
		existing.Status == app.BrowserHandoffStatusValidatingHidden
	block := app.BrowserLoginBlock{}
	if hasExisting && existing.RunID == runID {
		block = existing
	}
	block.SessionID = sessionID
	block.RunID = runID
	block.SchemaVersion = app.BrowserHandoffSchemaVersion
	block.Status = app.BrowserLoginBlockStatusWaiting
	block.OriginalGoal = goal
	block.ResumeTool = browserLoginResumeTool(plan.Name)
	block.ResumeArgs = resumeArgs
	block.LastToolCallID = call.ID
	block.LoginHandoffURL = firstNonEmptyString(output["login_handoff_url"], output["final_url"], output["url"], resumeArgs["url"])
	block.OwnerID = firstNonEmptyString(output["owner_id"], resumeArgs["owner_id"])
	block.BrowserProfileID = firstNonEmptyString(output["browser_profile_id"], resumeArgs["browser_profile_id"])
	block.SiteOrigin = firstNonEmptyString(output["auth_site_origin"])
	block.SiteRealm = firstNonEmptyString(output["auth_site_realm"], resumeArgs["site_realm"])
	block.AccountHint = firstNonEmptyString(output["account_hint"], resumeArgs["account_hint"])
	block.BrowserAuthStatus = firstNonEmptyString(output["browser_auth_status"])
	block.LastError = firstNonEmptyString(output["login_handoff_error"], output["browser_session_error"])
	if revalidatingHidden && block.LastError == "" {
		block.LastError = "browser_login_profile_continuity_lost"
	}
	block.SessionGeneration = uint64(intLikeValue(output["session_generation"]))
	if run, ok := r.store.GetRun(runID); ok && run.Workflow != nil {
		block.WorkflowID = run.Workflow.Plan.ProfileID
		block.WorkflowRevision = run.Workflow.Plan.ProfileRevision
		if block.WorkflowRevision == app.BrowserWorkflowRevision2 {
			block.ResumeTool = "browser.snapshot"
		}
		if len(run.Workflow.ActiveNodeIDs) == 1 {
			block.WorkflowNodeID = run.Workflow.ActiveNodeIDs[0]
		}
		if run.Workflow.Browser != nil {
			block.Target = run.Workflow.Browser.Target
		}
	}
	block.ResolvedAt = nil
	if hasExisting && existing.RunID == runID {
		var err error
		block, err = r.store.UpdateBrowserLoginBlock(block, existing.Version)
		if err != nil {
			return app.BrowserLoginBlock{}, false
		}
	} else {
		block = r.store.SaveBrowserLoginBlock(block)
	}
	r.store.AddAudit(app.AuditEvent{
		SessionID: sessionID,
		RunID:     runID,
		Actor:     "runtime",
		Type:      "browser_login_block.created",
		Summary:   block.SiteOrigin,
		Fields:    browserLoginBlockRuntimeFields(block, map[string]any{"tool_call_id": call.ID}),
	})
	return block, true
}

func (r Runtime) recentBrowserURLForLoginBlock(sessionID, runID, currentCallID string) string {
	calls := toolCallsForRun(r.store.ListToolCalls(sessionID), runID)
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		if call.ID == currentCallID || !toolCallCompleted(call) || !strings.HasPrefix(call.Tool, "browser.") {
			continue
		}
		fields := browserLoginToolFields(call)
		if target := firstNonEmptyString(call.Arguments["url"], fields["login_handoff_url"], fields["final_url"], fields["url"], fields["current_url"]); target != "" {
			return target
		}
	}
	return ""
}

func browserToolMayCreateLoginBlock(tool string) bool {
	return tool == "browser.read" || strings.HasPrefix(tool, "browser.")
}

func browserLoginResumeTool(tool string) string {
	if tool == "browser.read" {
		return tool
	}
	return "browser.read"
}

func browserLoginToolFields(call app.ToolCall) map[string]any {
	fields := map[string]any{}
	mergeBrowserLoginFields(fields, call.Arguments, false)
	if output, ok := anyMap(call.Result); ok {
		mergeBrowserLoginFields(fields, output, true)
		if nested, ok := anyMap(output["output"]); ok {
			mergeBrowserLoginFields(fields, nested, true)
			if snapshot, ok := anyMap(nested["snapshot"]); ok {
				mergeBrowserLoginFields(fields, snapshot, true)
			}
		}
		if nestedArgs, ok := anyMap(output["arguments"]); ok {
			mergeBrowserLoginFields(fields, nestedArgs, false)
		}
	}
	if structured := toolResultStructuredFieldsFromSummary(call.ObservationSummary); len(structured) > 0 {
		mergeBrowserLoginFields(fields, structured, false)
	}
	normalizeBrowserLoginFieldsFromObservation(call, fields)
	return fields
}

func normalizeBrowserLoginFieldsFromObservation(call app.ToolCall, fields map[string]any) {
	if !browserToolCanInferLoginBlock(call.Tool) {
		return
	}
	assessment := assessBrowserAuthentication(call, fields)
	addBrowserAuthAssessmentFields(fields, assessment)
	if assessment.State != browserAuthChallenged {
		return
	}
	fields["auth_challenge_detected"] = true
	fields["login_handoff_required"] = true
	fields["browser_auth_status"] = "handoff_waiting"
	if firstNonEmptyString(fields["auth_challenge_kind"]) == "" {
		fields["auth_challenge_kind"] = "login_or_verification"
	}
	if firstNonEmptyString(fields["login_surface"]) == "" {
		fields["login_surface"] = browserLoginSurfaceFromFields(fields)
	}
	targetURL := firstNonEmptyString(fields["login_handoff_url"], fields["final_url"], fields["url"], fields["current_url"], call.Arguments["url"])
	if targetURL == "" {
		return
	}
	if firstNonEmptyString(fields["url"]) == "" {
		fields["url"] = targetURL
	}
	if firstNonEmptyString(fields["final_url"]) == "" {
		fields["final_url"] = targetURL
	}
	if firstNonEmptyString(fields["login_handoff_url"]) == "" {
		fields["login_handoff_url"] = targetURL
	}
	if firstNonEmptyString(fields["auth_site_origin"]) == "" {
		fields["auth_site_origin"] = browserLoginURLOrigin(targetURL)
	}
}

func browserToolCanInferLoginBlock(tool string) bool {
	switch tool {
	case "browser.read", "browser.open", "browser.navigate", "browser.snapshot", "browser.wait", "browser.click", "browser.type", "browser.select", "browser.press":
		return true
	default:
		return false
	}
}

func browserLoginObservationText(call app.ToolCall, fields map[string]any) string {
	parts := []string{}
	appendText := func(value any) {
		text := strings.TrimSpace(stringValue(value))
		if text == "" || text == "<nil>" {
			return
		}
		parts = append(parts, text)
	}
	for _, key := range []string{"title", "text", "excerpt", "summary", "browser_snapshot_text", "snapshot_text", "current_title", "current_url"} {
		if value, ok := fields[key]; ok {
			appendText(value)
		}
	}
	if result, ok := anyMap(call.Result); ok {
		appendBrowserLoginObservationText(&parts, result, 0)
	}
	if message := toolResultMessageFromSummary(call.ObservationSummary); message != nil {
		appendText(message.Summary)
		for _, evidence := range message.Evidence {
			appendText(evidence.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func appendBrowserLoginObservationText(parts *[]string, values map[string]any, depth int) {
	if depth > 3 || values == nil {
		return
	}
	for _, key := range []string{"title", "text", "excerpt", "summary", "message", "description", "current_title"} {
		text := strings.TrimSpace(stringValue(values[key]))
		if text != "" && text != "<nil>" {
			*parts = append(*parts, text)
		}
	}
	if content := browserAutomationContentText(values); content != "" {
		*parts = append(*parts, content)
	}
	for _, key := range []string{"output", "result", "value", "data", "snapshot", "hidden_page_state"} {
		if nested, ok := anyMap(values[key]); ok {
			appendBrowserLoginObservationText(parts, nested, depth+1)
		}
	}
	if content, ok := values["content"].([]any); ok {
		for _, item := range content {
			if nested, ok := anyMap(item); ok {
				appendBrowserLoginObservationText(parts, nested, depth+1)
			}
		}
	}
}

func toolResultMessageFromSummary(summary string) *toolResultMessage {
	summary = strings.TrimSpace(summary)
	if summary == "" || !strings.HasPrefix(summary, "{") {
		return nil
	}
	var message toolResultMessage
	if err := json.Unmarshal([]byte(summary), &message); err != nil {
		return nil
	}
	return &message
}

func browserLoginObservationLooksLikeAuthGate(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return false
	}
	for _, marker := range []string{
		"please sign in", "sign in to continue", "login required", "log in to view", "authentication required",
		"captcha", "verification code", "sms code", "two-factor", "2fa", "sso login", "cas login",
		"access denied", "members only", "paywall", "subscribe to continue",
		"请登录", "登录后查看", "未登录", "验证码", "短信验证码", "扫码登录", "账号密码",
		"访问受限", "vpn登录", "cas登录", "cas认证",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	limitedResource := containsAny(text, "本资源仅限内网访问", "仅限内网", "内网访问", "restricted resource")
	authInstruction := containsAny(text,
		"请您使用校园网", "请使用校园网", "登录 sslvpn", "登录sslvpn", "login with vpn", "connect to vpn",
	)
	if limitedResource && authInstruction {
		return true
	}
	credentialField := containsAny(text, "password", "密码") &&
		containsAny(text, "enter", "input", "textbox", "username", "account", "请输入", "输入", "用户名", "账号")
	return credentialField
}

func browserLoginSurfaceFromFields(fields map[string]any) string {
	mode := strings.ToLower(strings.TrimSpace(firstNonEmptyString(fields["browser_mode"])))
	presentation := strings.ToLower(strings.TrimSpace(firstNonEmptyString(fields["presentation"])))
	if mode == "collaborative" || presentation == "visible" || boolValue(fields["surface_visible"]) {
		return "collaborative_visible"
	}
	return "visible_handoff"
}

func browserLoginURLOrigin(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return strings.ToLower(parsed.Scheme + "://" + parsed.Host)
}

func toolResultStructuredFieldsFromSummary(summary string) map[string]any {
	message := toolResultMessageFromSummary(summary)
	if message == nil {
		return nil
	}
	return message.Structured
}

func mergeBrowserLoginFields(dst, src map[string]any, overwrite bool) {
	if src == nil {
		return
	}
	for key, value := range src {
		if _, ok := dst[key]; ok && !overwrite {
			continue
		}
		if strings.TrimSpace(stringValue(value)) == "" || stringValue(value) == "<nil>" {
			continue
		}
		dst[key] = value
	}
}

func browserOutputNeedsLoginBlock(output map[string]any) bool {
	status := strings.ToLower(strings.TrimSpace(stringValue(output["browser_auth_status"])))
	if status == "handoff_waiting" || status == "handoff_required" {
		return true
	}
	return boolValue(output["login_handoff_opened"]) ||
		(boolValue(output["auth_challenge_detected"]) && boolValue(output["login_handoff_required"]))
}

func browserLoginBlockedMessage(block app.BrowserLoginBlock) string {
	target := firstNonEmptyString(block.LoginHandoffURL, block.ResumeArgs["url"])
	if target == "" {
		target = block.SiteOrigin
	}
	lines := []string{
		"任务已暂停在浏览器登录步骤，原任务还没有完成。",
		"我已经打开了需要登录的页面：" + target,
	}
	if block.LastError == "browser_login_profile_continuity_lost" {
		lines = append(lines, "登录状态没有从可见浏览器连续传递到新的隐藏会话，因此我没有继续原任务。")
	}
	lines = append(lines, "请在可见浏览器里完成登录。完成后回复“登录完成”；如果页面不对，告诉我页面错了或直接发正确链接。")
	return strings.Join(lines, "\n")
}

func (r Runtime) resumeBrowserLoginBlock(ctx context.Context, sessionID, userReply string, emit StreamHandler) (Result, bool, error) {
	block, ok := r.store.FindActiveBrowserLoginBlock(sessionID)
	if !ok {
		return Result{}, false, nil
	}
	run, ok := r.store.GetRun(block.RunID)
	if !ok || run.SessionID != sessionID {
		block.Status = app.BrowserLoginBlockStatusFailed
		block.LastUserReply = userReply
		block.LastError = "original run for browser login block was not found"
		now := time.Now().UTC()
		block.ResolvedAt = &now
		_, _ = r.store.UpdateBrowserLoginBlock(block, block.Version)
		return Result{}, false, nil
	}
	if run.Workflow != nil && run.Workflow.Browser != nil &&
		block.WorkflowID == run.Workflow.Plan.ProfileID &&
		block.WorkflowRevision == run.Workflow.Plan.ProfileRevision {
		block.Target = run.Workflow.Browser.Target
	}
	goal := strings.TrimSpace(block.OriginalGoal)
	if goal == "" {
		goal = requestContentForRun(r.store.ListMessages(sessionID), run)
	}
	if goal == "" {
		goal = userReply
	}
	recoveringVisibleValidation := false
	switch block.Status {
	case app.BrowserHandoffStatusReopeningVisible:
		var claimed bool
		var claimErr error
		block, claimed, claimErr = r.claimBrowserHandoffTransition(block)
		if claimErr != nil {
			return r.browserHandoffConflictResult(run, claimErr)
		}
		if !claimed {
			return r.resultForExistingRun(run), true, nil
		}
		return r.reopenBrowserLoginBlock(
			ctx, sessionID, run, block, block.LastUserReply, nil, nil,
			firstNonEmptyString(block.LastError, "browser_login_recover_reopening_visible"),
		)
	case app.BrowserHandoffStatusValidatingVisible:
		var claimed bool
		var claimErr error
		block, claimed, claimErr = r.claimBrowserHandoffTransition(block)
		if claimErr != nil {
			return r.browserHandoffConflictResult(run, claimErr)
		}
		if !claimed {
			return r.resultForExistingRun(run), true, nil
		}
		recoveringVisibleValidation = true
		userReply = firstNonEmptyString(block.LastUserReply, userReply)
	case app.BrowserHandoffStatusTransferring,
		app.BrowserHandoffStatusValidatingHidden,
		app.BrowserHandoffStatusResumingWorkflow:
		var claimed bool
		var claimErr error
		block, claimed, claimErr = r.claimBrowserHandoffTransition(block)
		if claimErr != nil {
			return r.browserHandoffConflictResult(run, claimErr)
		}
		if !claimed {
			return r.resultForExistingRun(run), true, nil
		}
		result, recoverErr := r.finishMatchedBrowserHandoffResume(
			ctx, run, goal, block, r.browserHandoffInterruptedCallID(run, block), emit,
		)
		return result, true, recoverErr
	case app.BrowserHandoffStatusWaitingOwner:
	default:
		return r.resultForExistingRun(run), true, nil
	}
	intent := browserLoginReplyIntent(userReply)
	if !recoveringVisibleValidation && intent == browserLoginReplyAmbiguous {
		block.LastUserReply = userReply
		block.LastError = "browser_login_explicit_confirmation_required"
		var err error
		block, err = r.store.UpdateBrowserLoginBlock(block, block.Version)
		if err != nil {
			return r.browserHandoffConflictResult(run, err)
		}
		summary := browserLoginExplicitConfirmationMessage(block)
		return r.finishBrowserLoginBlockedRun(ctx, run, block, summary, nil, nil), true, nil
	}
	if !recoveringVisibleValidation && intent == browserLoginReplyCancel {
		now := time.Now().UTC()
		block.Status = app.BrowserHandoffStatusCanceled
		block.LastUserReply = userReply
		block.LastError = "browser_login_canceled_by_owner"
		block.ResolvedAt = &now
		var err error
		block, err = r.store.UpdateBrowserLoginBlock(block, block.Version)
		if err != nil {
			return r.browserHandoffConflictResult(run, err)
		}
		return r.finishBrowserLoginCanceledRun(ctx, run, block), true, nil
	}
	if !recoveringVisibleValidation && intent == browserLoginReplyWrongPage {
		block.Status = app.BrowserHandoffStatusReopeningVisible
		block.LastUserReply = userReply
		block.LastError = "user_reported_wrong_page"
		beginBrowserHandoffTransition(&block, r.instanceID)
		var err error
		block, err = r.store.UpdateBrowserLoginBlock(block, block.Version)
		if err != nil {
			return r.browserHandoffConflictResult(run, err)
		}
		return r.reopenBrowserLoginBlock(ctx, sessionID, run, block, userReply, nil, nil, "user_reported_wrong_page")
	}

	var err error
	if !recoveringVisibleValidation {
		block.Status = app.BrowserHandoffStatusValidatingVisible
		block.LastUserReply = userReply
		block.LastError = ""
		beginBrowserHandoffTransition(&block, r.instanceID)
		block, err = r.store.UpdateBrowserLoginBlock(block, block.Version)
		if err != nil {
			return r.browserHandoffConflictResult(run, err)
		}
	}
	r.store.AddAudit(app.AuditEvent{
		SessionID: sessionID,
		RunID:     run.ID,
		Actor:     "runtime",
		Type:      "browser_login_block.resume_requested",
		Summary:   intent,
		Fields:    browserLoginBlockRuntimeFields(block, map[string]any{"reply_intent": intent}),
	})

	run.State = "executing"
	run.CompletedAt = nil
	r.store.SaveRun(run)
	interruptedWorkflowCallID := ""
	if run.Workflow != nil {
		interruptedWorkflowCallID = r.browserHandoffInterruptedCallID(run, block)
	}

	resumeCalls := []app.ToolCall{}
	resumeApprovals := []app.Approval{}
	tabPlan := toolPlan{
		Name: "browser.list_tabs",
		Args: visibleBrowserResumeArgs(block, "browser_login_block_resume"),
	}
	// Tab discovery and the authenticated read are Runtime login preflight.
	// They must not consume or replace the persisted Workflow stage scope.
	tabCall, tabApproval, _ := r.runToolPlan(ctx, sessionID, run.ID, tabPlan)
	resumeCalls = append(resumeCalls, tabCall)
	if tabApproval != nil {
		resumeApprovals = append(resumeApprovals, *tabApproval)
	}
	r.store.AddAudit(app.AuditEvent{
		SessionID: sessionID,
		RunID:     run.ID,
		Actor:     "runtime",
		Type:      "browser_login_block.current_tabs_checked",
		Summary:   tabCall.Status,
		Fields: browserLoginBlockRuntimeFields(block, map[string]any{
			"tool_call_id": tabCall.ID,
			"status":       tabCall.Status,
			"no_tabs":      browserListTabsReturnedNoPages(tabCall),
		}),
	})

	if browserListTabsReturnedNoPages(tabCall) {
		block.LastError = "browser_login_block_missing_tab"
		return r.reopenBrowserLoginBlock(ctx, sessionID, run, block, userReply, resumeCalls, resumeApprovals, "browser_login_block_missing_tab")
	}
	target, targetSelected := browserSelectedTabTarget(tabCall, block.LastVisiblePageID, block.LoginHandoffPageID)
	if !targetSelected {
		block.Status = app.BrowserHandoffStatusWaitingOwner
		block.LastError = "browser_login_post_login_target_unavailable"
		block, err = r.store.UpdateBrowserLoginBlock(block, block.Version)
		if err != nil {
			return Result{}, true, err
		}
		r.store.AddAudit(app.AuditEvent{
			SessionID: sessionID,
			RunID:     run.ID,
			Actor:     "runtime",
			Type:      "browser_login_block.post_login_target_rejected",
			Summary:   block.LastError,
			Fields:    browserLoginBlockRuntimeFields(block, map[string]any{"reason": block.LastError}),
		})
		summary := browserLoginTargetMismatchMessage("", "")
		return r.finishBrowserLoginBlockedRun(ctx, run, block, summary, resumeCalls, resumeApprovals), true, nil
	}

	expectedTarget := ""
	if run.Workflow != nil {
		matchesTarget := false
		switch run.Workflow.Route.Slots.TargetKind {
		case string(app.TargetKindBrowserCurrentTab):
			expectedTarget = "当前任务标签页"
			expectedPageID := firstNonEmptyString(block.LastVisiblePageID, block.LoginHandoffPageID)
			matchesTarget = expectedPageID == "" || target.PageID == expectedPageID
		case "url":
			expectedTarget = normalizeBrowserURL(run.Workflow.Route.Slots.TargetRef)
			if !browserLoginResumeURLUsable(expectedTarget) {
				return r.blockPersistedWorkflowResume(ctx, run, goal, errors.New("browser resume lost the frozen workflow target")), true, nil
			}
			matchesTarget = browserTargetMatchesURL(expectedTarget, run.Workflow.Route.Facts["browser_destination"], target.URL)
		default:
			return r.blockPersistedWorkflowResume(ctx, run, goal, errors.New("browser resume has an unsupported frozen workflow target")), true, nil
		}
		if !matchesTarget {
			block.Status = app.BrowserHandoffStatusWaitingOwner
			block.LastError = "browser_login_post_login_target_mismatch"
			block, err = r.store.UpdateBrowserLoginBlock(block, block.Version)
			if err != nil {
				return Result{}, true, err
			}
			r.store.AddAudit(app.AuditEvent{
				SessionID: sessionID,
				RunID:     run.ID,
				Actor:     "runtime",
				Type:      "browser_login_block.post_login_target_rejected",
				Summary:   target.URL,
				Fields: browserLoginBlockRuntimeFields(block, map[string]any{
					"reason":          block.LastError,
					"expected_target": expectedTarget,
					"post_login_url":  target.URL,
					"page_id":         target.PageID,
				}),
			})
			summary := browserLoginTargetMismatchMessage(expectedTarget, target.URL)
			return r.finishBrowserLoginBlockedRun(ctx, run, block, summary, resumeCalls, resumeApprovals), true, nil
		}
		r.store.AddAudit(app.AuditEvent{
			SessionID: sessionID,
			RunID:     run.ID,
			Actor:     "runtime",
			Type:      "browser_login_block.post_login_target_validated",
			Summary:   browserSafeHandoffURL(block.Target, target.URL),
			Fields: browserLoginBlockRuntimeFields(block, map[string]any{
				"expected_target": expectedTarget,
				"post_login_url":  browserSafeHandoffURL(block.Target, target.URL),
				"page_id":         target.PageID,
			}),
		})
	}

	previousOrigin := block.SiteOrigin
	block.LastVisiblePageID = target.PageID
	if block.LoginHandoffPageID == "" {
		block.LoginHandoffPageID = target.PageID
	}
	block.SiteOrigin = browserLoginURLOrigin(target.URL)
	safeTargetURL := browserSafeHandoffURL(block.Target, target.URL)
	block.LoginHandoffURL = safeTargetURL
	resumeArgs := clonePlanArgs(block.ResumeArgs)
	resumeArgs["url"] = safeTargetURL
	block.ResumeArgs = resumeArgs
	block, err = r.store.UpdateBrowserLoginBlock(block, block.Version)
	if err != nil {
		return Result{}, true, err
	}
	r.store.AddAudit(app.AuditEvent{
		SessionID: sessionID,
		RunID:     run.ID,
		Actor:     "runtime",
		Type:      "browser_login_block.post_login_target_selected",
		Summary:   target.URL,
		Fields: browserLoginBlockRuntimeFields(block, map[string]any{
			"page_id":              target.PageID,
			"previous_site_origin": previousOrigin,
			"post_login_url":       safeTargetURL,
		}),
	})

	snapshotArgs := visibleBrowserResumeArgs(block, "browser_login_block_validate_visible")
	snapshotArgs["page_id"] = target.PageID
	if strings.TrimSpace(goal) != "" {
		snapshotArgs["interaction_goal"] = goal
	}
	snapshotCall, snapshotApproval, _ := r.runToolPlan(ctx, sessionID, run.ID, toolPlan{
		Name: "browser.snapshot",
		Args: snapshotArgs,
	})
	resumeCalls = append(resumeCalls, snapshotCall)
	if snapshotApproval != nil {
		resumeApprovals = append(resumeApprovals, *snapshotApproval)
	}
	visibleAssessment := assessBrowserAuthentication(snapshotCall, browserLoginToolFields(snapshotCall))
	r.store.AddAudit(app.AuditEvent{
		SessionID: sessionID,
		RunID:     run.ID,
		Actor:     "runtime",
		Type:      "browser_auth.visible_confirmation",
		Summary:   string(visibleAssessment.State),
		Fields: browserLoginBlockRuntimeFields(block, map[string]any{
			"phase":        "visible_snapshot",
			"tool_call_id": snapshotCall.ID,
			"state":        string(visibleAssessment.State),
			"confidence":   visibleAssessment.Confidence,
			"signals":      visibleAssessment.Signals,
		}),
	})
	visibleEvidence, evidenceReason := browserHandoffVisibleEvidence(run.Workflow, block, snapshotCall)
	if visibleAssessment.State != browserAuthAuthenticated || evidenceReason != "" {
		block = updateBrowserLoginBlockFromResumeCall(block, snapshotCall)
		block.Status = app.BrowserHandoffStatusWaitingOwner
		if block.LastError == "" {
			switch {
			case evidenceReason != "":
				block.LastError = evidenceReason
			case visibleAssessment.State == browserAuthUnknown:
				block.LastError = "browser_login_auth_evidence_inconclusive"
			default:
				block.LastError = "browser_login_block_still_unauthenticated"
			}
		}
		block, err = r.store.UpdateBrowserLoginBlock(block, block.Version)
		if err != nil {
			return Result{}, true, err
		}
		r.store.AddAudit(app.AuditEvent{
			SessionID: sessionID,
			RunID:     run.ID,
			Actor:     "runtime",
			Type:      "browser_login_block.still_waiting",
			Summary:   block.LastError,
			Fields:    browserLoginBlockRuntimeFields(block, map[string]any{"tool_call_id": snapshotCall.ID}),
		})
		summary := browserLoginStillWaitingMessage(block)
		return r.finishBrowserLoginBlockedRun(ctx, run, block, summary, resumeCalls, resumeApprovals), true, nil
	}

	block.Status = app.BrowserHandoffStatusTransferring
	block.VisibleEvidence = visibleEvidence
	block.SessionGeneration = visibleEvidence.VisibleSession.Generation
	block.LastToolCallID = snapshotCall.ID
	block.LastError = ""
	block, err = r.store.UpdateBrowserLoginBlock(block, block.Version)
	if err != nil {
		return Result{}, true, err
	}
	r.store.AddAudit(app.AuditEvent{
		SessionID: sessionID,
		RunID:     run.ID,
		Actor:     "runtime",
		Type:      "browser_login_block.visible_validated",
		Summary:   block.SiteOrigin,
		Fields:    browserLoginBlockRuntimeFields(block, map[string]any{"tool_call_id": snapshotCall.ID}),
	})
	if run.Workflow != nil {
		if run.Workflow.Plan.ProfileRevision == app.BrowserWorkflowRevision2 {
			result, transitionErr := r.finishMatchedBrowserHandoffResume(ctx, run, goal, block, interruptedWorkflowCallID, emit)
			return result, true, transitionErr
		}
		return r.finishMatchedBrowserLoginResume(ctx, run, goal, interruptedWorkflowCallID, emit), true, nil
	}

	now := time.Now().UTC()
	block.Status = app.BrowserHandoffStatusResolved
	block.ResolvedAt = &now
	_, _ = r.store.UpdateBrowserLoginBlock(block, block.Version)
	return r.completeRetiredLegacyRun(ctx, run, goal, "workflow.legacy_login_resume_retired",
		"Resolved a browser login block for a run without a persisted workflow plan"), true, nil
}

func (r Runtime) reopenBrowserLoginBlock(ctx context.Context, sessionID string, run app.AgentRun, block app.BrowserLoginBlock, userReply string, calls []app.ToolCall, approvals []app.Approval, reason string) (Result, bool, error) {
	target := firstNonEmptyString(firstURL(userReply), block.LoginHandoffURL, block.ResumeArgs["url"])
	if target != "" && firstURL(userReply) != "" {
		args := clonePlanArgs(block.ResumeArgs)
		args["url"] = target
		block.ResumeArgs = args
		block.LoginHandoffURL = target
	}
	if target != "" {
		openPlan := toolPlan{
			Name: "browser.open",
			Args: map[string]any{
				"url":                target,
				"browser_mode":       "collaborative",
				"presentation":       "visible",
				"surface_visible":    true,
				"reason":             reason,
				"owner_id":           block.OwnerID,
				"browser_profile_id": block.BrowserProfileID,
			},
		}
		openCall, approval, _ := r.runToolPlan(ctx, sessionID, run.ID, openPlan)
		calls = append(calls, openCall)
		if approval != nil {
			approvals = append(approvals, *approval)
		}
		if opened, ok := browserSelectedTabTarget(openCall); ok {
			block.LoginHandoffURL = opened.URL
			block.LoginHandoffPageID = opened.PageID
			block.LastVisiblePageID = opened.PageID
			block.SiteOrigin = browserLoginURLOrigin(opened.URL)
		}
	}
	block.Status = app.BrowserHandoffStatusWaitingOwner
	block.LastUserReply = userReply
	block.LastError = reason
	var err error
	block, err = r.store.UpdateBrowserLoginBlock(block, block.Version)
	if err != nil {
		return Result{}, true, err
	}
	r.store.AddAudit(app.AuditEvent{
		SessionID: sessionID,
		RunID:     run.ID,
		Actor:     "runtime",
		Type:      "browser_login_block.reopened",
		Summary:   target,
		Fields:    browserLoginBlockRuntimeFields(block, map[string]any{"reason": reason}),
	})
	summary := browserLoginReopenedMessage(block, target)
	return r.finishBrowserLoginBlockedRun(ctx, run, block, summary, calls, approvals), true, nil
}

func (r Runtime) finishBrowserLoginBlockedRun(ctx context.Context, run app.AgentRun, block app.BrowserLoginBlock, summary string, calls []app.ToolCall, approvals []app.Approval) Result {
	now := time.Now().UTC()
	run.State = "browser_login_blocked"
	run.CompletedAt = nil
	run.Summary = summary
	r.store.SaveRun(run)
	allToolCalls := toolCallsForRun(r.store.ListToolCalls(run.SessionID), run.ID)
	allApprovals := approvalsForRun(r.store.ListApprovals(""), run.ID)
	feedback := r.store.ListRunFeedback(run.ID)
	episode := summarizeEpisode(block.OriginalGoal, run, allToolCalls, allApprovals, run.Summary, now)
	r.store.SaveEpisodeSummary(episode)
	var workflowResult *app.WorkflowResult
	if run.Workflow != nil {
		workflowResult = r.workflowResultForRun(run, run.Workflow.Route, run.Workflow.ReturnRoute, summary)
	}
	assistant := r.store.AddMessage(workflowResultMessage(run, workflowResult, run.Summary, now))
	r.writeTrace(ctx, run, modelrouter.ChatResult{}, allToolCalls, allApprovals, feedback, &episode)
	if len(calls) == 0 {
		calls = allToolCalls
	}
	if len(approvals) == 0 {
		approvals = allApprovals
	}
	result := Result{Run: run, Message: assistant, ToolCalls: calls, Approvals: approvals}
	if run.Workflow != nil {
		route := run.Workflow.Route
		result.RouteDecision = &route
		result.WorkflowResult = workflowResult
	}
	return result
}

func (r Runtime) finishBrowserLoginCanceledRun(ctx context.Context, run app.AgentRun, block app.BrowserLoginBlock) Result {
	now := time.Now().UTC()
	if run.Workflow != nil {
		run.Workflow.Status = app.WorkflowStatusBlocked
	}
	run.State = "cancelled"
	run.CompletedAt = &now
	run.Summary = "已取消浏览器登录交接，原任务没有继续执行。可见浏览器页面保持打开。"
	r.store.SaveRun(run)
	r.store.AddAudit(app.AuditEvent{
		SessionID: run.SessionID, RunID: run.ID, Actor: "owner",
		Type: "browser_login_block.canceled", Summary: block.LastError,
		Fields: browserLoginBlockRuntimeFields(block, nil),
	})
	toolCalls := toolCallsForRun(r.store.ListToolCalls(run.SessionID), run.ID)
	approvals := approvalsForRun(r.store.ListApprovals(""), run.ID)
	episode := summarizeEpisode(block.OriginalGoal, run, toolCalls, approvals, run.Summary, now)
	r.store.SaveEpisodeSummary(episode)
	var workflowResult *app.WorkflowResult
	if run.Workflow != nil {
		workflowResult = r.workflowResultForRun(run, run.Workflow.Route, run.Workflow.ReturnRoute, run.Summary)
	}
	assistant := r.store.AddMessage(workflowResultMessage(run, workflowResult, run.Summary, now))
	r.writeTrace(ctx, run, modelrouter.ChatResult{}, toolCalls, approvals, r.store.ListRunFeedback(run.ID), &episode)
	return Result{Run: run, Message: assistant, ToolCalls: toolCalls, Approvals: approvals, WorkflowResult: workflowResult}
}

func (r Runtime) bindPersistedWorkflowToolPlan(run app.AgentRun, plan toolPlan) (toolPlan, error) {
	if run.Workflow == nil || len(run.Workflow.ActiveNodeIDs) != 1 {
		return toolPlan{}, errors.New("browser resume requires one active persisted workflow node")
	}
	nodeID := run.Workflow.ActiveNodeIDs[0]
	state := run.Workflow.Nodes[nodeID]
	capability, err := r.materializedWorkflowCapability(run.ID, nodeID, state.ScopeRevision, plan.Name)
	if err != nil {
		return toolPlan{}, err
	}
	plan.WorkflowID = run.Workflow.Plan.ProfileID
	plan.WorkflowNodeID = nodeID
	plan.ScopeRevision = state.ScopeRevision
	plan.Capability = capability
	return plan, nil
}

func browserHandoffVisibleEvidence(state *app.WorkflowState, block app.BrowserLoginBlock, call app.ToolCall) (*app.BrowserResultEvidence, string) {
	if !toolCallCompleted(call) {
		return nil, "browser_login_visible_snapshot_failed"
	}
	outcome := adaptBrowserSnapshotOutcome(call, block.WorkflowNodeID)
	page, snapshot, ok := browserSnapshotRefs(outcome.Refs)
	if !ok {
		return nil, "browser_login_visible_snapshot_missing"
	}
	target := block.Target
	if state != nil && state.Browser != nil {
		target = state.Browser.Target
		if _, reason := browserRevision2ValidatedSnapshot(state, outcome, app.BrowserPresentationVisible); reason != "" {
			return nil, "browser_login_visible_" + strings.TrimPrefix(reason, "browser_")
		}
	}
	generation := browserRefGeneration(snapshot)
	if generation == 0 {
		return nil, "browser_login_visible_generation_missing"
	}
	if snapshot.Attributes["presentation"] != "" && snapshot.Attributes["presentation"] != string(app.BrowserPresentationVisible) {
		return nil, "browser_login_visible_presentation_mismatch"
	}
	liveURL := page.Attributes["url"]
	safeURL := browserSafeHandoffURL(target, liveURL)
	target.CanonicalURL = safeURL
	target.RedactedURL = safeURL
	return &app.BrowserResultEvidence{
		ID:            "browser_handoff_visible_" + call.ID,
		SchemaVersion: app.BrowserHandoffSchemaVersion,
		Target:        target,
		VisibleSession: app.BrowserSessionRef{
			OwnerID: page.Attributes["owner_id"], ProfileID: page.Attributes["profile_id"],
			Presentation: app.BrowserPresentationVisible, Generation: generation,
			ProviderSessionRef: snapshot.Attributes["provider_session_ref"],
		},
		VisiblePageID: page.Ref, VisibleSnapshotID: snapshot.Ref,
		VisibleSnapshotDigest: snapshot.Attributes["digest"],
		SourceToolCallIDs:     []string{call.ID},
		VerifiedAt:            time.Now().UTC(),
	}, ""
}

func browserSafeHandoffURL(target app.BrowserTargetDescriptor, liveRaw string) string {
	if target.TargetKind == app.BrowserTargetExplicitURL && target.QueryProvenance == app.BrowserQueryOwnerSupplied {
		return target.CanonicalURL
	}
	parsed, err := url.Parse(strings.TrimSpace(liveRaw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return target.CanonicalURL
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	return parsed.String()
}

func (r Runtime) finishMatchedBrowserHandoffResume(ctx context.Context, run app.AgentRun, goal string, block app.BrowserLoginBlock, interruptedCallID string, emit StreamHandler) (Result, error) {
	if run.Workflow == nil || run.Workflow.Plan.ProfileRevision != app.BrowserWorkflowRevision2 {
		return Result{}, errors.New("browser handoff revision does not match the persisted workflow")
	}
	if block.WorkflowID != run.Workflow.Plan.ProfileID ||
		block.WorkflowRevision != run.Workflow.Plan.ProfileRevision ||
		block.WorkflowNodeID == "" {
		return Result{}, errors.New("browser handoff ownership does not match the persisted workflow")
	}
	if interruptedCallID != "" {
		call, ok := r.store.GetToolCall(interruptedCallID)
		if !ok || call.RunID != run.ID || call.WorkflowID != run.Workflow.Plan.ProfileID ||
			call.WorkflowNodeID != block.WorkflowNodeID {
			return Result{}, errors.New("browser handoff could not recover its interrupted workflow call")
		}
	}
	switch block.Status {
	case app.BrowserHandoffStatusTransferring:
		if block.VisibleEvidence == nil {
			return Result{}, errors.New("browser handoff lost its validated visible evidence")
		}
		if !browserRevision2HandoffResetPersisted(run, block.WorkflowNodeID) {
			if err := resetBrowserRevision2AfterHandoff(&run, block.WorkflowNodeID); err != nil {
				return Result{}, err
			}
			run.State = "executing"
			run.CompletedAt = nil
			r.store.SaveRun(run)
			r.store.AddAudit(app.AuditEvent{
				SessionID: run.SessionID, RunID: run.ID, Actor: "runtime",
				Type:    "browser_login_block.pre_login_refs_discarded",
				Summary: "Reset browser revision 2 to hidden target reacquisition using a new session generation",
				Fields: browserLoginBlockRuntimeFields(block, map[string]any{
					"interrupted_tool_call_id": interruptedCallID,
					"visible_generation":       block.SessionGeneration,
				}),
			})
		}
		block.Status = app.BrowserHandoffStatusValidatingHidden
		var err error
		block, err = r.store.UpdateBrowserLoginBlock(block, block.Version)
		if err != nil {
			return Result{}, err
		}
	case app.BrowserHandoffStatusValidatingHidden:
		// The transition to validating_hidden is persisted only after the reset
		// run state, so a restart can resume whatever hidden stage was last saved.
	case app.BrowserHandoffStatusResumingWorkflow:
		if browserWorkflowRunSucceeded(run) {
			return r.resolveRecoveredBrowserHandoff(run, block)
		}
	default:
		return Result{}, fmt.Errorf("browser handoff status %q cannot transfer profile", block.Status)
	}

	run.State = "executing"
	run.CompletedAt = nil
	r.store.SaveRun(run)
	result, _, resumeErr := r.resumeMatchedWorkflow(ctx, run, goal, nil, "workflow.resumed_after_browser_handoff")
	if resumeErr != nil {
		return Result{}, resumeErr
	}
	current, ok := r.store.GetBrowserLoginBlock(block.ID)
	if !ok {
		return Result{}, errors.New("browser handoff disappeared during hidden validation")
	}
	if current.Status == app.BrowserHandoffStatusWaitingOwner {
		result.Run.State = "browser_login_blocked"
		result.Run.CompletedAt = nil
		if result.Run.Workflow != nil {
			result.Run.Workflow.Status = app.WorkflowStatusRunning
		}
		r.store.SaveRun(result.Run)
		return result, nil
	}
	if result.Run.State != "completed" || result.Run.Workflow == nil || result.Run.Workflow.Status != app.WorkflowStatusSucceeded {
		current.Status = app.BrowserHandoffStatusFailed
		current.LastError = "browser_login_hidden_validation_failed"
		now := time.Now().UTC()
		current.ResolvedAt = &now
		_, _ = r.store.UpdateBrowserLoginBlock(current, current.Version)
		return result, nil
	}
	current.Status = app.BrowserHandoffStatusResumingWorkflow
	current.LastError = ""
	current, err := r.store.UpdateBrowserLoginBlock(current, current.Version)
	if err != nil {
		return Result{}, err
	}
	return r.resolveBrowserHandoffResult(result, current)
}

func browserRevision2HandoffResetPersisted(run app.AgentRun, nodeID app.WorkflowNodeID) bool {
	if run.Workflow == nil || len(run.Workflow.ActiveNodeIDs) != 1 || run.Workflow.ActiveNodeIDs[0] != nodeID {
		return false
	}
	node, ok := run.Workflow.Nodes[nodeID]
	return ok && node.Stage == "scan_tabs" && len(node.OutcomeRefs) == 0 &&
		run.Workflow.Browser != nil && run.Workflow.Browser.Result == nil
}

func browserWorkflowRunSucceeded(run app.AgentRun) bool {
	return run.State == "completed" && run.Workflow != nil && run.Workflow.Status == app.WorkflowStatusSucceeded
}

func (r Runtime) resolveRecoveredBrowserHandoff(run app.AgentRun, block app.BrowserLoginBlock) (Result, error) {
	result := r.resultForExistingRun(run)
	return r.resolveBrowserHandoffResult(result, block)
}

func (r Runtime) resolveBrowserHandoffResult(result Result, block app.BrowserLoginBlock) (Result, error) {
	now := time.Now().UTC()
	block.Status = app.BrowserHandoffStatusResolved
	block.LastError = ""
	block.ResolvedAt = &now
	block, err := r.store.UpdateBrowserLoginBlock(block, block.Version)
	if err != nil {
		return Result{}, err
	}
	r.store.AddAudit(app.AuditEvent{
		SessionID: block.SessionID, RunID: block.RunID, Actor: "runtime",
		Type: "browser_login_block.resolved", Summary: block.SiteOrigin,
		Fields: browserLoginBlockRuntimeFields(block, nil),
	})
	return result, nil
}

func beginBrowserHandoffTransition(block *app.BrowserLoginBlock, runtimeID string) {
	if block == nil {
		return
	}
	now := time.Now().UTC()
	block.TransitionOwnerID = firstNonEmptyString(runtimeID, "runtime")
	leaseUntil := now.Add(browserHandoffTransitionLease)
	block.TransitionLeaseUntil = &leaseUntil
}

func (r Runtime) claimBrowserHandoffTransition(block app.BrowserLoginBlock) (app.BrowserLoginBlock, bool, error) {
	now := time.Now().UTC()
	if block.TransitionOwnerID == firstNonEmptyString(r.instanceID, "runtime") &&
		block.TransitionLeaseUntil != nil && block.TransitionLeaseUntil.After(now) {
		return block, false, nil
	}
	beginBrowserHandoffTransition(&block, r.instanceID)
	updated, err := r.store.UpdateBrowserLoginBlock(block, block.Version)
	if err != nil {
		return app.BrowserLoginBlock{}, false, err
	}
	return updated, true, nil
}

func (r Runtime) browserHandoffConflictResult(run app.AgentRun, err error) (Result, bool, error) {
	if !errors.Is(err, store.ErrBrowserHandoffConflict) {
		return Result{}, true, err
	}
	if current, ok := r.store.GetRun(run.ID); ok {
		run = current
	}
	return r.resultForExistingRun(run), true, nil
}

func (r Runtime) browserHandoffInterruptedCallID(run app.AgentRun, block app.BrowserLoginBlock) string {
	if call, ok := r.store.GetToolCall(block.LastToolCallID); ok &&
		call.RunID == run.ID && call.WorkflowNodeID == block.WorkflowNodeID {
		return call.ID
	}
	calls := toolCallsForRun(r.store.ListToolCalls(run.SessionID), run.ID)
	for index := len(calls) - 1; index >= 0; index-- {
		call := calls[index]
		if call.WorkflowID == block.WorkflowID && call.WorkflowNodeID == block.WorkflowNodeID &&
			browserOutputNeedsLoginBlock(browserLoginToolFields(call)) {
			return call.ID
		}
	}
	return ""
}

func resetBrowserRevision2AfterHandoff(run *app.AgentRun, nodeID app.WorkflowNodeID) error {
	if run == nil || run.Workflow == nil || workflowPlanDigest(run.Workflow.Plan) != run.Workflow.PlanDigest {
		return errors.New("persisted workflow plan digest mismatch during browser handoff")
	}
	if len(run.Workflow.ActiveNodeIDs) != 1 || run.Workflow.ActiveNodeIDs[0] != nodeID {
		return errors.New("browser handoff does not own the active workflow node")
	}
	nodePlan, ok := workflowPlanNode(run.Workflow.Plan, nodeID)
	if !ok {
		return errors.New("browser handoff workflow node is missing from the frozen plan")
	}
	node, ok := run.Workflow.Nodes[nodeID]
	if !ok || node.Status != app.WorkflowNodeActive || node.Attempts >= nodePlan.MaxAttempts {
		return errors.New("browser handoff workflow node is not resumable")
	}
	node.Stage = "scan_tabs"
	node.CurrentScope = nodePlan.InitialScope
	node.ScopeRevision++
	node.LastDirectory = nil
	node.SelectedEntries = nil
	node.OutcomeRefs = nil
	node.LastAssessment = nil
	for _, transitionID := range []app.TransitionID{
		"reuse_existing", "reuse_blank", "open_missing",
		"focus_acquired", "open_acquired", "navigate_acquired",
		"hidden_settled", "hidden_validated", "assess_initial",
	} {
		delete(node.TransitionActivations, transitionID)
	}
	run.Workflow.Nodes[nodeID] = node
	run.Workflow.Status = app.WorkflowStatusRunning
	if run.Workflow.Browser != nil {
		run.Workflow.Browser.Result = nil
	}
	return nil
}

func (r Runtime) blockPersistedWorkflowResume(ctx context.Context, run app.AgentRun, goal string, err error) Result {
	result := r.blockWorkflowSetup(ctx, run, goal, err)
	if run.Workflow != nil {
		route := run.Workflow.Route
		result.RouteDecision = &route
		result.WorkflowResult = r.workflowResultForRun(result.Run, route, run.Workflow.ReturnRoute, result.Message.Content)
	}
	return result
}

func (r Runtime) finishMatchedBrowserLoginResume(ctx context.Context, run app.AgentRun, goal, interruptedCallID string, emit StreamHandler) Result {
	profile, err := r.profiles.Get(run.Workflow.Plan.ProfileID, run.Workflow.Plan.ProfileRevision)
	if err != nil {
		return r.blockPersistedWorkflowResume(ctx, run, goal, err)
	}
	interruptedCall, ok := r.store.GetToolCall(interruptedCallID)
	if !ok || interruptedCall.RunID != run.ID || interruptedCall.WorkflowID != run.Workflow.Plan.ProfileID {
		return r.blockPersistedWorkflowResume(ctx, run, goal, errors.New("browser resume could not recover the interrupted workflow tool call"))
	}
	definition, ok := r.tools.Definition(interruptedCall.Tool)
	if !ok {
		return r.blockPersistedWorkflowResume(ctx, run, goal, errors.New("interrupted browser workflow tool is no longer registered"))
	}
	outcome, err := adaptWorkflowOutcomeAfterConfirmedBrowserLogin(definition, interruptedCall)
	if err != nil {
		return r.blockPersistedWorkflowResume(ctx, run, goal, err)
	}
	if run.Workflow.Plan.ProfileID == app.WorkflowBrowserInteraction && interruptedCall.Tool == "browser.snapshot" {
		if err := discardBrowserLoginInterruptedOutcome(&run, outcome); err != nil {
			return r.blockPersistedWorkflowResume(ctx, run, goal, err)
		}
		r.store.SaveRun(run)
		r.store.AddAudit(app.AuditEvent{
			SessionID: run.SessionID, RunID: run.ID, Actor: "runtime", Type: "browser_login_block.interrupted_snapshot_discarded",
			Summary: "Discarded the pre-login snapshot before resuming browser interaction", Fields: map[string]any{"tool_call_id": interruptedCall.ID},
		})
		result, _, resumeErr := r.resumeMatchedWorkflow(ctx, run, goal, nil, "workflow.resumed_after_browser_login")
		if resumeErr != nil {
			return r.blockPersistedWorkflowResume(ctx, run, goal, resumeErr)
		}
		return result
	}
	assessment := profile.Assess(run.Workflow, outcome)
	changed, applyErr := applyWorkflowOutcome(&run, outcome, assessment)
	if applyErr != nil && assessment.Status != app.AssessmentBlocked {
		return r.blockPersistedWorkflowResume(ctx, run, goal, applyErr)
	}
	r.store.SaveRun(run)
	r.auditWorkflowOutcome(run, outcome, assessment, changed, applyErr)
	if run.Workflow.Status == app.WorkflowStatusRunning {
		result, _, resumeErr := r.resumeMatchedWorkflow(ctx, run, goal, nil, "workflow.resumed_after_browser_login")
		if resumeErr != nil {
			return r.blockPersistedWorkflowResume(ctx, run, goal, resumeErr)
		}
		return result
	}

	now := time.Now().UTC()
	if run.Workflow.Status == app.WorkflowStatusSucceeded {
		run.State = "completed"
		run.CompletedAt = &now
	} else {
		run.State = "blocked"
		run.CompletedAt = &now
	}
	toolCalls := toolCallsForRun(r.store.ListToolCalls(run.SessionID), run.ID)
	run.Summary = r.applyGroundedSummary(run.SessionID, run.ID, goal, "", toolCalls)
	if strings.TrimSpace(run.Summary) == "" {
		run.Summary = "The browser workflow resumed after login and completed its bounded read."
	}
	if emit != nil && run.State == "completed" {
		_ = emitCompletedFinalAnswer(run, "workflow_grounded_answer", run.Summary, emit)
	}
	if call, _, queued := r.queueExternalSendApproval(&run); queued {
		toolCalls = append(toolCalls, call)
	}
	r.store.SaveRun(run)
	approvals := approvalsForRun(r.store.ListApprovals(""), run.ID)
	feedback := r.store.ListRunFeedback(run.ID)
	episode := summarizeEpisode(goal, run, toolCalls, approvals, run.Summary, now)
	r.store.SaveEpisodeSummary(episode)
	route := run.Workflow.Route
	workflowResult := r.workflowResultForRun(run, route, run.Workflow.ReturnRoute, run.Summary)
	assistantMessage := r.messageWithWorkflowResult(app.Message{SessionID: run.SessionID, RunID: run.ID, Role: "assistant", Content: run.Summary, CreatedAt: now}, workflowResult)
	assistant := r.store.AddMessage(assistantMessage)
	r.writeTrace(ctx, run, modelrouter.ChatResult{}, toolCalls, approvals, feedback, &episode)
	return Result{
		Run: run, Message: assistant, ToolCalls: toolCalls, Approvals: approvals, RouteDecision: &route,
		WorkflowResult: workflowResult,
	}
}

func adaptWorkflowOutcomeAfterConfirmedBrowserLogin(definition app.ToolDefinition, call app.ToolCall) (app.ToolOutcome, error) {
	outcome, err := adaptWorkflowOutcome(definition, call)
	if err != nil || !containsOutcomeSignal(outcome.Signals, app.OutcomeSignalAuthenticationRequired) {
		return outcome, err
	}
	switch call.Tool {
	case "browser.open":
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalOpenCompleted}
		outcome.Refs = browserPageRefs(call.Result, call.ID)
	case "browser.navigate":
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalNavigateCompleted}
		payload := browserOutcomePayload(call.Result)
		pageID := firstNonEmptyString(payload["page_id"], call.Arguments["page_id"])
		if pageID != "" {
			outcome.Refs = []app.ResourceRef{{Kind: "browser_page", Ref: pageID, Provenance: call.ID, Attributes: map[string]string{"url": normalizeBrowserURL(firstNonEmptyString(payload["url"]))}}}
		}
	}
	return outcome, nil
}

func discardBrowserLoginInterruptedOutcome(run *app.AgentRun, outcome app.ToolOutcome) error {
	if run.Workflow == nil || workflowPlanDigest(run.Workflow.Plan) != run.Workflow.PlanDigest {
		return errors.New("persisted workflow plan digest mismatch during browser login resume")
	}
	state, ok := run.Workflow.Nodes[outcome.NodeID]
	if !ok || state.Status != app.WorkflowNodeActive {
		return errors.New("interrupted browser snapshot does not belong to an active workflow node")
	}
	node, ok := workflowPlanNode(run.Workflow.Plan, outcome.NodeID)
	if !ok || state.Attempts >= node.MaxAttempts {
		return errors.New("browser login resume exhausted the workflow attempt bound")
	}
	state.Attempts++
	state.AppliedOutcomeIDs = appendUniqueString(state.AppliedOutcomeIDs, outcome.ID)
	state.ToolCallIDs = appendUniqueString(state.ToolCallIDs, outcome.ToolCallID)
	run.Workflow.Nodes[outcome.NodeID] = state
	return nil
}

func browserLoginReplyIntent(reply string) string {
	lower := strings.ToLower(strings.TrimSpace(reply))
	if containsAny(lower, "cancel", "stop", "abort", "取消", "停止", "不继续", "不用了", "终止") {
		return browserLoginReplyCancel
	}
	if firstURL(reply) != "" || containsAny(lower, "wrong page", "not this page", "incorrect page", "页面错", "页面不对", "不是这个页面", "不是这个", "打开错", "找错", "链接错") {
		return browserLoginReplyWrongPage
	}
	if containsAny(lower, "logged in", "login completed", "login successful", "signed in", "done", "finished", "登录完成", "登录成功", "已经登录成功", "已登录", "登陆完成", "登陆成功", "已经登陆成功", "已登陆", "登好了", "登录好了", "登陆好了", "好了", "完成了") {
		return browserLoginReplyCompleted
	}
	return browserLoginReplyAmbiguous
}

func visibleBrowserResumeArgs(block app.BrowserLoginBlock, reason string) map[string]any {
	args := map[string]any{
		"browser_mode":    "collaborative",
		"presentation":    "visible",
		"surface_visible": true,
		"reason":          reason,
	}
	if block.OwnerID != "" {
		args["owner_id"] = block.OwnerID
	}
	if block.BrowserProfileID != "" {
		args["browser_profile_id"] = block.BrowserProfileID
	}
	return args
}

func browserLoginResumeArgs(block app.BrowserLoginBlock) map[string]any {
	args := clonePlanArgs(block.ResumeArgs)
	if firstNonEmptyString(args["url"]) == "" {
		args["url"] = firstNonEmptyString(block.LoginHandoffURL, block.SiteOrigin)
	}
	args["login_handoff_completed"] = true
	args["browser_mode"] = "autonomous"
	args["presentation"] = "hidden"
	args["surface_visible"] = false
	if block.OwnerID != "" {
		args["owner_id"] = block.OwnerID
	}
	if block.BrowserProfileID != "" {
		args["browser_profile_id"] = block.BrowserProfileID
	}
	if block.SiteRealm != "" {
		args["site_realm"] = block.SiteRealm
	}
	if block.AccountHint != "" {
		args["account_hint"] = block.AccountHint
	}
	return args
}

func updateBrowserLoginBlockFromResumeCall(block app.BrowserLoginBlock, call app.ToolCall) app.BrowserLoginBlock {
	block.LastToolCallID = call.ID
	if call.Error != "" {
		block.LastError = call.Error
		return block
	}
	output, ok := anyMap(call.Result)
	if !ok {
		return block
	}
	if value := firstNonEmptyString(output["login_handoff_url"], output["final_url"], output["url"]); value != "" {
		block.LoginHandoffURL = value
	}
	if value := firstNonEmptyString(output["browser_auth_status"]); value != "" {
		block.BrowserAuthStatus = value
	}
	block.LastError = firstNonEmptyString(output["login_handoff_error"], output["browser_session_error"])
	return block
}

func browserListTabsReturnedNoPages(call app.ToolCall) bool {
	if call.Status != "completed" {
		return false
	}
	output, ok := anyMap(call.Result)
	if !ok {
		return false
	}
	pages, ok := output["pages"]
	if !ok {
		return false
	}
	return len(anySlice(pages)) == 0
}

func browserLoginStillWaitingMessage(block app.BrowserLoginBlock) string {
	target := firstNonEmptyString(block.LoginHandoffURL, block.ResumeArgs["url"], block.SiteOrigin)
	lines := []string{"我还没有验证到登录后的页面，原任务仍然暂停在浏览器登录步骤。"}
	if target != "" {
		lines = append(lines, "当前等待的页面："+target)
	}
	if block.LastError != "" {
		lines = append(lines, "原因："+block.LastError)
	}
	lines = append(lines, "请在可见浏览器里确认已经登录完成；如果页面不对，直接把正确链接发给我。")
	return strings.Join(lines, "\n")
}

func browserLoginExplicitConfirmationMessage(block app.BrowserLoginBlock) string {
	target := firstNonEmptyString(block.LoginHandoffURL, block.ResumeArgs["url"], block.SiteOrigin)
	lines := []string{
		"原浏览器任务仍在等待登录确认，本次回复没有触发任何浏览器检查或后续操作。",
	}
	if target != "" {
		lines = append(lines, "等待确认的页面："+target)
	}
	lines = append(lines, "完成登录并确认当前页面符合原任务后，请明确回复“登录完成”；要终止原任务，请回复“取消”。")
	return strings.Join(lines, "\n")
}

func browserLoginTargetMismatchMessage(expectedTarget, currentURL string) string {
	lines := []string{"我已检查登录后的浏览器页面，但当前页面不符合原任务要求，因此没有切回隐藏模式，也没有继续执行后续操作。"}
	if expectedTarget != "" {
		lines = append(lines, "原任务目标："+expectedTarget)
	}
	if currentURL != "" {
		lines = append(lines, "当前页面："+currentURL)
	} else {
		lines = append(lines, "当前没有唯一可确认的登录后任务页面。")
	}
	lines = append(lines, "请在可见浏览器中切换到原任务需要的页面，然后回复“登录完成”；如果原目标本身不对，请直接发送正确链接。")
	return strings.Join(lines, "\n")
}

func browserLoginReopenedMessage(block app.BrowserLoginBlock, target string) string {
	if target == "" {
		return strings.Join([]string{
			"原任务仍然暂停在浏览器登录步骤，但我没有可用的登录交接页面可以重新打开。",
			"请把正确的登录页面链接发给我，或在可见浏览器中打开正确页面后回复“登录完成”。",
		}, "\n")
	}
	return strings.Join([]string{
		"原任务仍然暂停在浏览器登录步骤，我已经重新打开登录交接页面：",
		target,
		"请在可见浏览器里完成登录。完成后回复“登录完成”；如果这仍然不是正确页面，请发正确链接。",
	}, "\n")
}

func clonePlanArgs(args map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range args {
		out[key] = value
	}
	return out
}

func firstURL(content string) string {
	urls := extractURLs(content)
	if len(urls) == 0 {
		return ""
	}
	return urls[0]
}

func browserLoginBlockRuntimeFields(block app.BrowserLoginBlock, extra map[string]any) map[string]any {
	fields := map[string]any{
		"block_id":              block.ID,
		"run_id":                block.RunID,
		"status":                block.Status,
		"resume_tool":           block.ResumeTool,
		"owner_id":              block.OwnerID,
		"browser_profile_id":    block.BrowserProfileID,
		"site_origin":           block.SiteOrigin,
		"site_realm":            block.SiteRealm,
		"account_hint":          block.AccountHint,
		"login_handoff_url":     block.LoginHandoffURL,
		"login_handoff_page_id": block.LoginHandoffPageID,
		"last_visible_page_id":  block.LastVisiblePageID,
	}
	for key, value := range extra {
		fields[key] = value
	}
	return fields
}

type browserTabTarget struct {
	URL    string
	PageID string
}

func browserSelectedTabTarget(call app.ToolCall, preferredPageIDs ...string) (browserTabTarget, bool) {
	if (call.Tool != "browser.list_tabs" && call.Tool != "browser.open") || !toolCallCompleted(call) {
		return browserTabTarget{}, false
	}
	result, ok := anyMap(call.Result)
	if !ok {
		return browserTabTarget{}, false
	}
	pages := anySlice(result["pages"])
	if len(pages) == 0 {
		if output, ok := anyMap(result["output"]); ok {
			pages = anySlice(output["pages"])
		}
	}
	candidates := []browserTabTarget{}
	selectedTarget := browserTabTarget{}
	preferred := map[string]bool{}
	for _, pageID := range preferredPageIDs {
		if pageID = strings.TrimSpace(pageID); pageID != "" {
			preferred[pageID] = true
		}
	}
	for _, raw := range pages {
		item, ok := anyMap(raw)
		if !ok {
			continue
		}
		url := strings.TrimSpace(stringValue(item["url"]))
		if !browserLoginResumeURLUsable(url) {
			continue
		}
		pageID := strings.TrimSpace(stringValue(item["page_id"]))
		if pageID == "" || pageID == "<nil>" {
			continue
		}
		target := browserTabTarget{URL: url, PageID: pageID}
		if preferred[pageID] {
			return target, true
		}
		selected := boolValue(item["selected"])
		if selected {
			selectedTarget = target
		}
		candidates = append(candidates, target)
	}
	if selectedTarget.URL != "" {
		return selectedTarget, true
	}
	if len(candidates) == 1 {
		return candidates[0], true
	}
	return browserTabTarget{}, false
}

func browserLoginResumeURLUsable(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

var errBrowserLoginResumeBlocked = errors.New("browser login block is still waiting")
