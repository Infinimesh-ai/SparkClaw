package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	if !browserOutputNeedsLoginBlock(output) {
		return app.BrowserLoginBlock{}, false
	}
	resumeArgs := clonePlanArgs(plan.Args)
	if firstNonEmptyString(resumeArgs["url"], output["url"], output["final_url"], output["login_handoff_url"]) == "" {
		return app.BrowserLoginBlock{}, false
	}
	resumeArgs["url"] = firstNonEmptyString(resumeArgs["url"], output["url"], output["final_url"], output["login_handoff_url"])
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
	block.LastError = firstNonEmptyString(output["login_handoff_error"], output["browser_auth_restore_error"])
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
		}
		if nestedArgs, ok := anyMap(output["arguments"]); ok {
			mergeBrowserLoginFields(fields, nestedArgs, false)
		}
	}
	if structured := toolResultStructuredFieldsFromSummary(call.ObservationSummary); len(structured) > 0 {
		mergeBrowserLoginFields(fields, structured, false)
	}
	return fields
}

func toolResultStructuredFieldsFromSummary(summary string) map[string]any {
	summary = strings.TrimSpace(summary)
	if summary == "" || !strings.HasPrefix(summary, "{") {
		return nil
	}
	var message toolResultMessage
	if err := json.Unmarshal([]byte(summary), &message); err != nil {
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
		goal = originalUserMessageForRun(r.store.ListMessages(sessionID), run)
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

	run.State = "reacting"
	run.CompletedAt = nil
	r.store.SaveRun(run)

	resumeCalls := []app.ToolCall{}
	resumeApprovals := []app.Approval{}
	tabCall, tabApproval, tabObservation := r.runToolPlan(ctx, sessionID, run.ID, toolPlan{
		Name: "browser.list_tabs",
		Args: visibleBrowserResumeArgs("browser_login_block_resume"),
	})
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

	resumePlan := toolPlan{Name: block.ResumeTool, Args: browserLoginResumeArgs(block)}
	if resumePlan.Name == "" {
		resumePlan.Name = "browser.read"
	}
	readCall, readApproval, readObservation := r.runToolPlan(ctx, sessionID, run.ID, resumePlan)
	resumeCalls = append(resumeCalls, readCall)
	if readApproval != nil {
		resumeApprovals = append(resumeApprovals, *readApproval)
	}
	if readObservation == "" && tabObservation != "" {
		readObservation = tabObservation
	}
	if browserLoginResumeStillWaiting(readCall) {
		block = updateBrowserLoginBlockFromResumeCall(block, readCall)
		block.Status = app.BrowserLoginBlockStatusWaiting
		if block.LastError == "" {
			block.LastError = "browser_login_block_still_unauthenticated"
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

	seedCalls := completedToolCallsForResume(toolCallsForRun(r.store.ListToolCalls(sessionID), run.ID))
	seedObservations := observationsForResume(seedCalls)
	hint := r.generateTaskHint(ctx, sessionID, run.ID, goal)
	relevantSkills := r.relevantSkillsForHint(goal, hint)
	visibleTools := r.visibleToolDefinitions(hint, relevantSkills)
	reactResult := r.runReActLoopWithSeed(ctx, sessionID, run, goal, hint, relevantSkills, visibleTools, seedCalls, seedObservations)
	toolCalls := toolCallsForRun(r.store.ListToolCalls(sessionID), run.ID)
	approvals := approvalsForRun(r.store.ListApprovals(""), run.ID)
	if len(reactResult.Approvals) > 0 {
		approvals = approvalsForRun(r.store.ListApprovals(""), run.ID)
	}
	now = time.Now().UTC()
	if reactResult.BrowserLoginBlock != nil {
		run.State = "browser_login_blocked"
		run.CompletedAt = nil
	} else if len(reactResult.Approvals) > 0 {
		run.State = "approval_pending"
		run.CompletedAt = nil
	} else if isBlockedFinalAnswer(reactResult.FinalAnswer) {
		run.State = "blocked"
		run.CompletedAt = &now
	} else {
		run.State = "completed"
		run.CompletedAt = &now
	}
	run.ModelLane = reactResult.Chat.Lane
	run.Summary = summarizeRun(reactResult.Chat, reactResult.Observations, reactResult.Approvals)
	if strings.TrimSpace(reactResult.FinalAnswer) != "" {
		run.Summary = reactResult.FinalAnswer
		if len(reactResult.Observations) > 0 || len(reactResult.Approvals) > 0 {
			run.Summary = summarizeRun(modelrouter.ChatResult{Content: reactResult.FinalAnswer}, reactResult.Observations, reactResult.Approvals)
		}
	}
	run.Summary = r.applyGroundedSummary(sessionID, run.ID, goal, run.Summary, toolCalls)
	if emit != nil && len(reactResult.Approvals) == 0 && reactResult.BrowserLoginBlock == nil && !isBlockedFinalAnswer(reactResult.FinalAnswer) {
		if streamed, streamedChat, err := r.streamFinalAnswer(ctx, goal, run, run.Summary, toolCalls, emit); err == nil && strings.TrimSpace(streamed) != "" {
			run.Summary = r.applyGroundedSummary(sessionID, run.ID, goal, streamed, toolCalls)
			reactResult.Chat = streamedChat
			run.ModelLane = streamedChat.Lane
		}
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
	r.writeTrace(ctx, run, reactResult.Chat, toolCalls, approvals, feedback, &episode)
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
		openCall, approval, _ := r.runToolPlan(ctx, sessionID, run.ID, toolPlan{
			Name: "browser.open",
			Args: map[string]any{
				"url":             target,
				"browser_mode":    "collaborative",
				"presentation":    "visible",
				"surface_visible": true,
				"reason":          reason,
			},
		})
		calls = append(calls, openCall)
		if approval != nil {
			approvals = append(approvals, *approval)
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
	assistant := r.store.AddMessage(app.Message{
		SessionID: run.SessionID,
		RunID:     run.ID,
		Role:      "assistant",
		Content:   run.Summary,
		CreatedAt: now,
	})
	r.writeTrace(ctx, run, modelrouter.ChatResult{}, allToolCalls, allApprovals, feedback, &episode)
	if len(calls) == 0 {
		calls = allToolCalls
	}
	if len(approvals) == 0 {
		approvals = allApprovals
	}
	return Result{Run: run, Message: assistant, ToolCalls: calls, Approvals: approvals}
}

func browserLoginReplyIntent(reply string) string {
	lower := strings.ToLower(strings.TrimSpace(reply))
	if firstURL(reply) != "" || containsAny(lower, "wrong page", "not this page", "incorrect page", "页面错", "页面不对", "不是这个页面", "不是这个", "打开错", "找错", "链接错") {
		return browserLoginReplyWrongPage
	}
	if containsAny(lower, "logged in", "login completed", "signed in", "done", "finished", "登录完成", "已登录", "登好了", "登录好了", "好了", "完成了") {
		return browserLoginReplyCompleted
	}
	return browserLoginReplyAmbiguous
}

func visibleBrowserResumeArgs(reason string) map[string]any {
	return map[string]any{
		"browser_mode":    "collaborative",
		"presentation":    "visible",
		"surface_visible": true,
		"reason":          reason,
	}
}

func browserLoginResumeArgs(block app.BrowserLoginBlock) map[string]any {
	args := clonePlanArgs(block.ResumeArgs)
	if firstNonEmptyString(args["url"]) == "" {
		args["url"] = firstNonEmptyString(block.LoginHandoffURL, block.SiteOrigin)
	}
	args["login_handoff_completed"] = true
	args["persist_browser_auth"] = true
	args["save_browser_auth"] = true
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

func browserLoginResumeStillWaiting(call app.ToolCall) bool {
	if call.Status != "completed" {
		return true
	}
	output, ok := anyMap(call.Result)
	if !ok {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(stringValue(output["browser_auth_status"])))
	if status == "handoff_waiting" || status == "handoff_required" || boolValue(output["auth_challenge_detected"]) || boolValue(output["login_handoff_required"]) {
		return true
	}
	if err := strings.TrimSpace(stringValue(output["browser_auth_export_error"])); err != "" && err != "<nil>" {
		return true
	}
	return false
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
	block.LastError = firstNonEmptyString(output["browser_auth_export_error"], output["login_handoff_error"], output["browser_auth_restore_error"])
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
		"block_id":           block.ID,
		"run_id":             block.RunID,
		"status":             block.Status,
		"resume_tool":        block.ResumeTool,
		"owner_id":           block.OwnerID,
		"browser_profile_id": block.BrowserProfileID,
		"site_origin":        block.SiteOrigin,
		"site_realm":         block.SiteRealm,
		"account_hint":       block.AccountHint,
		"login_handoff_url":  block.LoginHandoffURL,
	}
	for key, value := range extra {
		fields[key] = value
	}
	return fields
}

func browserLoginMissingRunError(block app.BrowserLoginBlock) error {
	return fmt.Errorf("browser login block %s references missing run %s", block.ID, block.RunID)
}

var errBrowserLoginResumeBlocked = errors.New("browser login block is still waiting")
