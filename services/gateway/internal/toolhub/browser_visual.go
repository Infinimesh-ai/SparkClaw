package toolhub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const browserVisualSummaryMaxRunes = 4000

func (h *ToolHub) inspectBrowserVisual(ctx context.Context, args map[string]any, sessionID, runID string) (Result, error) {
	run, ok := h.store.GetRun(runID)
	if !ok || run.SessionID != sessionID || run.Workflow == nil || !browserVisualWorkflowAllowed(run.Workflow.Plan.ProfileID) ||
		strings.TrimSpace(run.Workflow.Route.Facts["browser_visual_reason"]) != "owner_requested" {
		return Result{}, visualStaleError("browser visual inspection is not enabled by the active Workflow route")
	}
	reason := strings.TrimSpace(browserAutomationStringValue(args["reason"]))
	if reason != "owner_requested" {
		return Result{}, visualStaleError("browser visual inspection requires a frozen typed reason")
	}

	calls := h.store.ListToolCalls(sessionID)
	snapshotID := strings.TrimSpace(browserAutomationStringValue(args["snapshot_id"]))
	pageID := strings.TrimSpace(browserAutomationStringValue(args["page_id"]))
	snapshot, found := findBrowserSnapshotRecord(calls, runID, snapshotID)
	latest, latestFound := latestBrowserSnapshotRecord(calls, runID)
	if !found || !latestFound || latest.SnapshotID != snapshotID || snapshot.PageID != pageID ||
		snapshot.Generation == 0 || snapshot.PageGeneration == 0 || snapshot.Digest == "" || snapshot.URL == "" ||
		uint64(intLikeBrowserValue(args["session_generation"])) != snapshot.Generation ||
		uint64(intLikeBrowserValue(args["page_generation"])) != snapshot.PageGeneration ||
		strings.TrimSpace(browserAutomationStringValue(args["snapshot_digest"])) != snapshot.Digest {
		return Result{}, visualStaleError("browser visual inspection is not bound to the latest page snapshot generation")
	}

	callArgs := browserVisualAutomationArgs(args, snapshot)
	screenshot, err := h.browserAutomationTool(ctx, "browser.screenshot", callArgs, sessionID)
	if err != nil {
		return Result{}, visualStaleError(err.Error())
	}
	screenshotPath := browserVisualScreenshotPath(screenshot.Output)
	if screenshotPath == "" {
		return Result{}, errors.New("browser visual inspection did not persist a screenshot")
	}
	raw, err := os.ReadFile(screenshotPath)
	if err != nil {
		return Result{}, err
	}
	screenshotSum := sha256.Sum256(raw)
	screenshotDigest := hex.EncodeToString(screenshotSum[:])

	question := strings.TrimSpace(browserAutomationStringValue(args["question"]))
	if question == "" {
		question = "请描述当前网页的视觉状态，并指出与用户请求直接相关的可见证据。不要输出点击坐标或可执行元素引用。"
	}
	inspection, err := h.imageInspect(ctx, map[string]any{"path": screenshotPath, "question": question}, sessionID, runID)
	if err != nil {
		return Result{}, err
	}
	inspectionOutput, _ := browserInteractionMap(inspection.Output)

	fresh, err := h.browserAutomationTool(ctx, "browser.snapshot", map[string]any{
		"page_id": pageID, "interaction_goal": question,
		"browser_mode": callArgs["browser_mode"], "presentation": callArgs["presentation"],
		"surface_visible": callArgs["surface_visible"], "owner_id": callArgs["owner_id"],
		"browser_profile_id": callArgs["browser_profile_id"],
	}, sessionID)
	if err != nil {
		return Result{}, visualStaleError(err.Error())
	}
	freshSnapshot, ok := browserSnapshotMap(fresh.Output)
	if !ok || strings.TrimSpace(browserAutomationStringValue(freshSnapshot["page_id"])) != snapshot.PageID ||
		uint64(intLikeBrowserValue(freshSnapshot["session_generation"])) != snapshot.Generation ||
		uint64(intLikeBrowserValue(freshSnapshot["page_generation"])) != snapshot.PageGeneration ||
		strings.TrimSpace(browserAutomationStringValue(freshSnapshot["digest"])) != snapshot.Digest ||
		normalizePublicTargetURL(browserAutomationStringValue(freshSnapshot["url"])) != normalizePublicTargetURL(snapshot.URL) {
		return Result{}, visualStaleError("browser page identity changed during visual inference")
	}

	createdAt := time.Now().UTC()
	return Result{Output: map[string]any{
		"status": "completed", "evidence_id": app.NewID("browser_visual"), "reason": reason,
		"session_generation": snapshot.Generation, "page_generation": snapshot.PageGeneration,
		"page_id": snapshot.PageID, "snapshot_id": snapshot.SnapshotID, "snapshot_digest": snapshot.Digest,
		"post_snapshot_id": strings.TrimSpace(browserAutomationStringValue(freshSnapshot["snapshot_id"])),
		"normalized_url":   normalizePublicTargetURL(snapshot.URL), "screenshot_ref": screenshotPath,
		"screenshot_digest": screenshotDigest, "summary": boundedBrowserVisualSummary(inspectionOutput["summary"]),
		"model":      strings.TrimSpace(browserAutomationStringValue(inspectionOutput["model"])),
		"profile":    strings.TrimSpace(browserAutomationStringValue(inspectionOutput["profile"])),
		"lane":       strings.TrimSpace(browserAutomationStringValue(inspectionOutput["lane"])),
		"created_at": createdAt.Format(time.RFC3339Nano), "untrusted": true,
	}}, nil
}

func browserVisualWorkflowAllowed(id app.WorkflowID) bool {
	switch id {
	case app.WorkflowBrowserAutomation, app.WorkflowBrowserInteraction, app.WorkflowBrowserFormDraft:
		return true
	default:
		return false
	}
}

func browserVisualAutomationArgs(args map[string]any, snapshot browserSnapshotRecord) map[string]any {
	out := map[string]any{
		"page_id": snapshot.PageID, "snapshot_id": snapshot.SnapshotID,
		"session_generation": snapshot.Generation, "page_generation": snapshot.PageGeneration,
		"snapshot_digest": snapshot.Digest,
	}
	for _, key := range []string{"browser_mode", "presentation", "surface_visible", "owner_id", "browser_profile_id"} {
		if value, ok := args[key]; ok {
			out[key] = value
		}
	}
	return out
}

func browserVisualScreenshotPath(value any) string {
	outer, _ := browserInteractionMap(value)
	if path := strings.TrimSpace(browserAutomationStringValue(outer["screenshot_path"])); path != "" {
		return path
	}
	if nested, ok := browserInteractionMap(outer["output"]); ok {
		return strings.TrimSpace(browserAutomationStringValue(nested["screenshot_path"]))
	}
	return ""
}

func boundedBrowserVisualSummary(value any) string {
	runes := []rune(strings.TrimSpace(browserAutomationStringValue(value)))
	if len(runes) <= browserVisualSummaryMaxRunes {
		return string(runes)
	}
	return string(runes[:browserVisualSummaryMaxRunes])
}

func visualStaleError(message string) error {
	return &app.CodedToolError{Code: app.ToolErrorVisualEvidenceStale, Err: errors.New(message)}
}
