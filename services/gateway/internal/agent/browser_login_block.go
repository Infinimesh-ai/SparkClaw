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
)

const (
	browserLoginReplyCompleted = "completed"
	browserLoginReplyWrongPage = "wrong_page"
	browserLoginReplyAmbiguous = "ambiguous"
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
	block := app.BrowserLoginBlock{}
	if hasExisting && existing.RunID == runID {
		block = existing
	}
	block.SessionID = sessionID
	block.RunID = runID
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
	block.ResolvedAt = nil
	block = r.store.SaveBrowserLoginBlock(block)
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
	return strings.Join([]string{
		"任务已暂停在浏览器登录步骤，原任务还没有完成。",
		"我已经打开了需要登录的页面：" + target,
		"请在可见浏览器里完成登录。完成后回复“登录完成”；如果页面不对，告诉我页面错了或直接发正确链接。",
	}, "\n")
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
		r.store.SaveBrowserLoginBlock(block)
		return Result{}, false, nil
	}
	goal := strings.TrimSpace(block.OriginalGoal)
	if goal == "" {
		goal = requestContentForRun(r.store.ListMessages(sessionID), run)
	}
	if goal == "" {
		goal = userReply
	}
	intent := browserLoginReplyIntent(userReply)
	block.Status = app.BrowserLoginBlockStatusResuming
	block.LastUserReply = userReply
	block.LastError = ""
	block = r.store.SaveBrowserLoginBlock(block)
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
		interruptedWorkflowCallID = block.LastToolCallID
	}

	resumeCalls := []app.ToolCall{}
	resumeApprovals := []app.Approval{}
	tabPlan := toolPlan{
		Name: "browser.list_tabs",
		Args: visibleBrowserResumeArgs(block, "browser_login_block_resume"),
	}
	// Tab discovery and the authenticated read are Runtime login preflight.
	// They must not consume or replace the persisted Workflow stage scope.
	tabCall, tabApproval, tabObservation := r.runToolPlan(ctx, sessionID, run.ID, tabPlan)
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

	if intent == browserLoginReplyWrongPage {
		return r.reopenBrowserLoginBlock(ctx, sessionID, run, block, userReply, resumeCalls, resumeApprovals, "user_reported_wrong_page")
	}
	if browserListTabsReturnedNoPages(tabCall) {
		block.LastError = "browser_login_block_missing_tab"
		return r.reopenBrowserLoginBlock(ctx, sessionID, run, block, userReply, resumeCalls, resumeApprovals, "browser_login_block_missing_tab")
	}
	if target, ok := browserSelectedTabTarget(tabCall, block.LastVisiblePageID, block.LoginHandoffPageID); ok {
		previousOrigin := block.SiteOrigin
		block.LoginHandoffURL = target.URL
		block.LastVisiblePageID = target.PageID
		if block.LoginHandoffPageID == "" {
			block.LoginHandoffPageID = target.PageID
		}
		block.SiteOrigin = browserLoginURLOrigin(target.URL)
		resumeArgs := clonePlanArgs(block.ResumeArgs)
		resumeArgs["url"] = target.URL
		block.ResumeArgs = resumeArgs
		block = r.store.SaveBrowserLoginBlock(block)
		r.store.AddAudit(app.AuditEvent{
			SessionID: sessionID,
			RunID:     run.ID,
			Actor:     "runtime",
			Type:      "browser_login_block.post_login_target_selected",
			Summary:   target.URL,
			Fields: browserLoginBlockRuntimeFields(block, map[string]any{
				"page_id":              target.PageID,
				"previous_site_origin": previousOrigin,
				"post_login_url":       target.URL,
			}),
		})
	}

	resumePlan := toolPlan{Name: block.ResumeTool, Args: browserLoginResumeArgs(block)}
	if resumePlan.Name == "" {
		resumePlan.Name = "browser.read"
	}
	if run.Workflow != nil {
		frozenTarget := normalizeBrowserURL(run.Workflow.Route.Slots.TargetRef)
		if frozenTarget == "" {
			return r.blockPersistedWorkflowResume(ctx, run, goal, errors.New("browser resume lost the frozen workflow target")), true, nil
		}
		resumePlan.Args["url"] = frozenTarget
	}
	readCall, readApproval, readObservation := r.runToolPlan(ctx, sessionID, run.ID, resumePlan)
	resumeCalls = append(resumeCalls, readCall)
	if readApproval != nil {
		resumeApprovals = append(resumeApprovals, *readApproval)
	}
	if readObservation == "" && tabObservation != "" {
		readObservation = tabObservation
	}
	readAssessment := assessBrowserAuthentication(readCall, browserLoginToolFields(readCall))
	r.store.AddAudit(app.AuditEvent{
		SessionID: sessionID,
		RunID:     run.ID,
		Actor:     "runtime",
		Type:      "browser_auth.confirmation",
		Summary:   string(readAssessment.State),
		Fields: browserLoginBlockRuntimeFields(block, map[string]any{
			"phase":        "profile_read",
			"tool_call_id": readCall.ID,
			"state":        string(readAssessment.State),
			"confidence":   readAssessment.Confidence,
			"signals":      readAssessment.Signals,
		}),
	})
	if readAssessment.State != browserAuthAuthenticated {
		block = updateBrowserLoginBlockFromResumeCall(block, readCall)
		block.Status = app.BrowserLoginBlockStatusWaiting
		if block.LastError == "" {
			if readAssessment.State == browserAuthUnknown {
				block.LastError = "browser_login_auth_evidence_inconclusive"
			} else {
				block.LastError = "browser_login_block_still_unauthenticated"
			}
		}
		block = r.store.SaveBrowserLoginBlock(block)
		r.store.AddAudit(app.AuditEvent{
			SessionID: sessionID,
			RunID:     run.ID,
			Actor:     "runtime",
			Type:      "browser_login_block.still_waiting",
			Summary:   block.LastError,
			Fields:    browserLoginBlockRuntimeFields(block, map[string]any{"tool_call_id": readCall.ID}),
		})
		summary := browserLoginStillWaitingMessage(block)
		return r.finishBrowserLoginBlockedRun(ctx, run, block, summary, resumeCalls, resumeApprovals), true, nil
	}

	now := time.Now().UTC()
	block.Status = app.BrowserLoginBlockStatusResolved
	block.ResolvedAt = &now
	block.LastToolCallID = readCall.ID
	block.LastError = ""
	block = r.store.SaveBrowserLoginBlock(block)
	r.store.AddAudit(app.AuditEvent{
		SessionID: sessionID,
		RunID:     run.ID,
		Actor:     "runtime",
		Type:      "browser_login_block.resolved",
		Summary:   block.SiteOrigin,
		Fields:    browserLoginBlockRuntimeFields(block, map[string]any{"tool_call_id": readCall.ID}),
	})
	if run.Workflow != nil {
		return r.finishMatchedBrowserLoginResume(ctx, run, goal, interruptedWorkflowCallID, emit), true, nil
	}

	seedCalls := completedToolCallsForResume(toolCallsForRun(r.store.ListToolCalls(sessionID), run.ID))
	seedObservations := observationsForResume(seedCalls)
	hint := r.generateTaskHint(ctx, sessionID, run.ID, goal)
	relevantSkills := r.relevantSkillsForHint(goal, hint)
	visibleTools := r.visibleToolDefinitions(hint, relevantSkills)
	execution := r.runReActLoopWithSeed(ctx, sessionID, run, goal, hint, relevantSkills, visibleTools, seedCalls, seedObservations)
	toolCalls := toolCallsForRun(r.store.ListToolCalls(sessionID), run.ID)
	approvals := approvalsForRun(r.store.ListApprovals(""), run.ID)
	if len(execution.Approvals) > 0 {
		approvals = approvalsForRun(r.store.ListApprovals(""), run.ID)
	}
	now = time.Now().UTC()
	if execution.BrowserLoginBlock != nil {
		run.State = "browser_login_blocked"
		run.CompletedAt = nil
	} else if len(execution.Approvals) > 0 {
		run.State = "approval_pending"
		run.CompletedAt = nil
	} else if isBlockedFinalAnswer(execution.FinalAnswer) {
		run.State = "blocked"
		run.CompletedAt = &now
	} else {
		run.State = "completed"
		run.CompletedAt = &now
	}
	run.ModelLane = execution.Chat.Lane
	run.Summary = summarizeRun(execution.Chat, execution.Observations, execution.Approvals)
	if strings.TrimSpace(execution.FinalAnswer) != "" {
		run.Summary = execution.FinalAnswer
		if len(execution.Observations) > 0 || len(execution.Approvals) > 0 {
			run.Summary = summarizeRun(modelrouter.ChatResult{Content: execution.FinalAnswer}, execution.Observations, execution.Approvals)
		}
	}
	run.Summary = r.applyGroundedSummary(sessionID, run.ID, goal, run.Summary, toolCalls)
	if emit != nil && len(execution.Approvals) == 0 && execution.BrowserLoginBlock == nil && !isBlockedFinalAnswer(execution.FinalAnswer) {
		_ = emitCompletedFinalAnswer(run, "legacy_react_answer", run.Summary, emit)
	}
	if call, approval, queued := r.queueExternalSendApproval(&run); queued {
		toolCalls = append(toolCalls, call)
		approvals = append(approvals, approval)
	}
	r.store.SaveRun(run)
	feedback := r.store.ListRunFeedback(run.ID)
	episode := summarizeEpisode(goal, run, toolCalls, approvals, run.Summary, now)
	r.store.SaveEpisodeSummary(episode)
	assistant := r.store.AddMessage(app.Message{
		SessionID: sessionID,
		RunID:     run.ID,
		Role:      "assistant",
		Content:   run.Summary,
		CreatedAt: now,
	})
	r.writeTrace(ctx, run, execution.Chat, toolCalls, approvals, feedback, &episode)
	return Result{Run: run, Message: assistant, ToolCalls: toolCalls, Approvals: approvals}, true, nil
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
		if run.Workflow != nil {
			var err error
			openPlan, err = r.bindPersistedWorkflowToolPlan(run, openPlan)
			if err != nil {
				return r.blockPersistedWorkflowResume(ctx, run, block.OriginalGoal, err), true, nil
			}
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
	block.Status = app.BrowserLoginBlockStatusWaiting
	block.LastUserReply = userReply
	block.LastError = reason
	block = r.store.SaveBrowserLoginBlock(block)
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
	profile, err := r.profiles.Get(run.Workflow.Plan.ProfileID)
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
	if firstURL(reply) != "" || containsAny(lower, "wrong page", "not this page", "incorrect page", "页面错", "页面不对", "不是这个页面", "不是这个", "打开错", "找错", "链接错") {
		return browserLoginReplyWrongPage
	}
	if containsAny(lower, "logged in", "login completed", "login successful", "signed in", "done", "finished", "登录完成", "登录成功", "已经登录成功", "已登录", "登好了", "登录好了", "好了", "完成了") {
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

func browserLoginMissingRunError(block app.BrowserLoginBlock) error {
	return fmt.Errorf("browser login block %s references missing run %s", block.ID, block.RunID)
}

var errBrowserLoginResumeBlocked = errors.New("browser login block is still waiting")
