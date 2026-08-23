package toolhub

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestBrowserValidateTransitionAcceptsBoundChangedSnapshots(t *testing.T) {
	st, hub := newBrowserVerificationHub()
	seedBrowserVerificationCycle(st, "session", "run", 1, "before", "after")

	result, err := hub.Execute(context.Background(), "browser.validate_transition", map[string]any{
		"before_snapshot_id": "snapshot_1", "after_snapshot_id": "snapshot_2",
		"element_ref": browserVerificationRef(1),
	}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	output := result.Output.(map[string]any)
	if output["status"] != "validated" || output["code"] != "ok" || output["state_changed"] != true ||
		output["session_generation"] != uint64(7) || output["click_count"] != 1 {
		t.Fatalf("unexpected transition validation: %#v", output)
	}
}

func TestBrowserAssessGoalAcceptsCurrentSnapshotEvidence(t *testing.T) {
	for _, verdict := range []string{"satisfied", "success", "succeeded"} {
		t.Run(verdict, func(t *testing.T) {
			st, hub := newBrowserVerificationHub()
			seedBrowserVerificationCycle(st, "session", "run", 1, "before", "after")

			result, err := hub.Execute(context.Background(), "browser.assess_goal", map[string]any{
				"snapshot_id": "snapshot_2", "verdict": verdict,
				"evidence_refs": []string{browserVerificationRef(2)}, "reason": "目标状态已出现",
			}, "session", "run")
			if err != nil {
				t.Fatal(err)
			}
			output := result.Output.(map[string]any)
			if output["status"] != "succeeded" || output["code"] != "ok" || output["goal_satisfied"] != true ||
				output["session_generation"] != uint64(7) {
				t.Fatalf("unexpected goal assessment: %#v", output)
			}
		})
	}
}

func TestBrowserAssessGoalTreatsInitialClickableEvidenceAsProgress(t *testing.T) {
	st, hub := newBrowserVerificationHub()
	ref := browserVerificationRef(1)
	call := browserVerificationSnapshotCall("session", "run", 1, "", "inbox", "inbox", ref)
	result := call.Result.(browserautomation.Result)
	snapshot := result.Output.(map[string]any)["snapshot"].(map[string]any)
	snapshot["controls"] = []any{map[string]any{
		"ref": ref, "role": "generic", "accessible_name": "Drafts",
	}}
	snapshot["action_refs"] = []string{ref}
	call.Result = result
	testSaveToolCall(st, call)

	assessment, err := hub.Execute(context.Background(), "browser.assess_goal", map[string]any{
		"snapshot_id": "snapshot_1", "verdict": "succeeded",
		"evidence_refs": []string{ref}, "reason": "Drafts is available in the sidebar",
	}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	output := assessment.Output.(map[string]any)
	if output["status"] != "progress" || output["code"] != "action_required" || output["goal_satisfied"] != false {
		t.Fatalf("initial actionable evidence was accepted as completion: %#v", output)
	}
}

func TestBrowserAssessGoalCountsApprovedDraftActions(t *testing.T) {
	st, hub := newBrowserVerificationHub()
	testSaveRun(st, app.AgentRun{
		ID: "run", SessionID: "session", StartedAt: browserVerificationCallTime(1),
		Workflow: &app.WorkflowState{Plan: app.WorkflowPlan{ProfileID: app.WorkflowBrowserFormDraft}},
	})

	testSaveToolCall(st, browserVerificationSnapshotCall("session", "run", 1, "", "before", "before", browserVerificationRef(1)))
	testSaveToolCall(st, app.ToolCall{
		ID: "draft_1", SessionID: "session", RunID: "run", Tool: "browser.type", Status: app.ToolCallStatusCompletedAfterApproval,
		Arguments: map[string]any{"snapshot_id": "snapshot_1", "uid": browserVerificationRef(1), "page_id": "page_1"},
		StartedAt: browserVerificationCallTime(2),
	})

	testSaveToolCall(st, browserVerificationSnapshotCall("session", "run", 2, "snapshot_1", "after", "after", browserVerificationRef(2)))

	assessment, err := hub.Execute(context.Background(), "browser.assess_goal", map[string]any{
		"snapshot_id": "snapshot_2", "verdict": "succeeded",
		"evidence_refs": []string{browserVerificationRef(2)}, "reason": "the approved draft action produced the requested state",
	}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	output := assessment.Output.(map[string]any)
	if output["status"] != "succeeded" || output["goal_satisfied"] != true || output["draft_action_count"] != 1 {
		t.Fatalf("approved draft action was not counted as completed evidence: %#v", output)
	}
}

func TestBrowserAssessGoalUsesLatestSnapshotWhenLegacyIDsCollide(t *testing.T) {
	st, hub := newBrowserVerificationHub()
	ref := browserVerificationRef(1)
	hidden := browserVerificationSnapshotCall("session", "run", 1, "", "inbox", "inbox", ref)
	testSaveToolCall(st, hidden)
	testSaveToolCall(st, app.ToolCall{
		ID: "click_hidden", SessionID: "session", RunID: "run", Tool: "browser.click", Status: app.ToolCallStatusCompleted,
		Arguments: map[string]any{"snapshot_id": "snapshot_1", "uid": ref, "page_id": "page_1"},
		StartedAt: browserVerificationCallTime(3),
	})

	visible := browserVerificationSnapshotCall("session", "run", 1, "", "drafts", "drafts", ref)
	visible.ID = "call_visible_snapshot"
	visible.StartedAt = browserVerificationCallTime(4)
	visibleResult := visible.Result.(browserautomation.Result)
	visibleSnapshot := visibleResult.Output.(map[string]any)["snapshot"].(map[string]any)
	visibleSnapshot["session_generation"] = uint64(8)
	visibleSnapshot["action_refs"] = []string{ref}
	visible.Result = visibleResult
	testSaveToolCall(st, visible)

	assessment, err := hub.Execute(context.Background(), "browser.assess_goal", map[string]any{
		"snapshot_id": "snapshot_1", "verdict": "succeeded",
		"evidence_refs": []string{ref}, "reason": "current route and content prove the drafts view",
	}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	output := assessment.Output.(map[string]any)
	if output["status"] != "succeeded" || output["session_generation"] != uint64(8) || output["click_count"] != 1 {
		t.Fatalf("legacy snapshot collision did not bind the latest visible state: %#v", output)
	}
}

func TestBrowserValidateTransitionRejectsRepeatedState(t *testing.T) {
	st, hub := newBrowserVerificationHub()
	seedBrowserVerificationCycle(st, "session", "run", 1, "same", "same")

	_, err := hub.Execute(context.Background(), "browser.validate_transition", map[string]any{
		"before_snapshot_id": "snapshot_1", "after_snapshot_id": "snapshot_2",
		"element_ref": browserVerificationRef(1),
	}, "session", "run")
	if err == nil || !strings.Contains(err.Error(), "no new page state") {
		t.Fatalf("repeated state did not fail closed: %v", err)
	}
}

func TestBrowserValidateTransitionRejectsRouteOnlyChange(t *testing.T) {
	st, hub := newBrowserVerificationHub()
	seedBrowserVerificationCycle(st, "session", "run", 1, "inbox-route", "drafts-route", "same-content", "same-content")

	_, err := hub.Execute(context.Background(), "browser.validate_transition", map[string]any{
		"before_snapshot_id": "snapshot_1", "after_snapshot_id": "snapshot_2",
		"element_ref": browserVerificationRef(1),
	}, "session", "run")
	if err == nil || !strings.Contains(err.Error(), "no new page state") {
		t.Fatalf("route-only transition did not fail closed: %v", err)
	}
}

func TestBrowserAssessGoalStopsProgressAfterThirdClick(t *testing.T) {
	st, hub := newBrowserVerificationHub()
	seedBrowserVerificationCycle(st, "session", "run", 1, "state_0", "state_1")
	seedBrowserVerificationCycle(st, "session", "run", 2, "state_1", "state_2")
	seedBrowserVerificationCycle(st, "session", "run", 3, "state_2", "state_3")

	result, err := hub.Execute(context.Background(), "browser.assess_goal", map[string]any{
		"snapshot_id": "snapshot_6", "verdict": "progress",
		"evidence_refs": []string{browserVerificationRef(6)}, "reason": "还需要继续点击",
	}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	output := result.Output.(map[string]any)
	if output["status"] != "failed" || output["code"] != "interaction_attempt_limit" || output["click_count"] != 3 {
		t.Fatalf("third progress verdict did not stop the interaction: %#v", output)
	}
}

func TestBrowserValidateTransitionRejectsSnapshotsFromAnotherRun(t *testing.T) {
	st, hub := newBrowserVerificationHub()
	seedBrowserVerificationCycle(st, "session", "other_run", 1, "before", "after")

	_, err := hub.Execute(context.Background(), "browser.validate_transition", map[string]any{
		"before_snapshot_id": "snapshot_1", "after_snapshot_id": "snapshot_2",
		"element_ref": browserVerificationRef(1),
	}, "session", "run")
	if err == nil {
		t.Fatal("browser.validate_transition accepted snapshots outside the current run")
	}
}

func TestBrowserInteractionClickRejectsUnsafeControlBeforeAdapter(t *testing.T) {
	st, hub := newBrowserVerificationHub()
	testSaveRun(st, app.AgentRun{
		ID: "run", SessionID: "session", StartedAt: browserVerificationCallTime(1),
		Workflow: &app.WorkflowState{Plan: app.WorkflowPlan{ProfileID: app.WorkflowBrowserInteraction}},
	})

	ref := browserVerificationRef(1)
	call := browserVerificationSnapshotCall("session", "run", 1, "", "state", "state", ref)
	result := call.Result.(browserautomation.Result)
	payload := result.Output.(map[string]any)["snapshot"].(map[string]any)
	payload["controls"] = []any{map[string]any{"ref": ref, "role": "button", "accessible_name": "Delete account"}}
	call.Result = result
	testSaveToolCall(st, call)

	_, err := hub.Execute(context.Background(), "browser.click", map[string]any{
		"page_id": "page_1", "snapshot_id": "snapshot_1", "uid": ref,
	}, "session", "run")
	if err == nil || !strings.Contains(err.Error(), "unsafe click target") {
		t.Fatalf("unsafe control reached the browser adapter: %v", err)
	}
	if code := app.ToolErrorCodeFrom(err); code != app.ToolErrorUnsafeClickTarget {
		t.Fatalf("unsafe click rejection lost its typed code: %q (%v)", code, err)
	}
}

func TestBrowserInteractionClickRejectsRepeatedValidatedSemanticControl(t *testing.T) {
	st, hub := newBrowserVerificationHub()
	testSaveRun(st, app.AgentRun{
		ID: "run", SessionID: "session", StartedAt: browserVerificationCallTime(1),
		Workflow: &app.WorkflowState{Plan: app.WorkflowPlan{ProfileID: app.WorkflowBrowserInteraction}},
	})

	seedBrowserVerificationCycle(st, "session", "run", 1, "before", "after")
	validation, err := hub.Execute(context.Background(), "browser.validate_transition", map[string]any{
		"before_snapshot_id": "snapshot_1", "after_snapshot_id": "snapshot_2",
		"element_ref": browserVerificationRef(1),
	}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	testSaveToolCall(st, app.ToolCall{
		ID: "validate_1", SessionID: "session", RunID: "run", Tool: "browser.validate_transition", Status: app.ToolCallStatusCompleted,
		Result: validation.Output, StartedAt: browserVerificationCallTime(10),
	})

	_, err = hub.Execute(context.Background(), "browser.click", map[string]any{
		"page_id": "page_1", "snapshot_id": "snapshot_2", "uid": browserVerificationRef(2),
	}, "session", "run")
	if code := app.ToolErrorCodeFrom(err); err == nil || code != app.ToolErrorBrowserInteractionLoop {
		t.Fatalf("repeated semantic click did not fail with its typed loop code: code=%q err=%v", code, err)
	}
}

func newBrowserVerificationHub() (*store.MemoryStore, *ToolHub) {
	cfg := config.Default()
	cfg.Tools.BrowserAutomation.Enabled = true
	st := store.NewMemoryStore()
	return st, New(cfg, st)
}

func seedBrowserVerificationCycle(st *store.MemoryStore, sessionID, runID string, cycle int, beforeDigest, afterDigest string, contentDigests ...string) {
	beforeContentDigest, afterContentDigest := beforeDigest, afterDigest
	if len(contentDigests) == 2 {
		beforeContentDigest, afterContentDigest = contentDigests[0], contentDigests[1]
	}
	beforeNumber := cycle*2 - 1
	afterNumber := cycle * 2
	beforeID := browserVerificationSnapshotID(beforeNumber)
	ref := browserVerificationRef(beforeNumber)
	testSaveToolCall(st, browserVerificationSnapshotCall(sessionID, runID, beforeNumber, "", beforeDigest, beforeContentDigest, ref))
	testSaveToolCall(st, app.ToolCall{
		ID: "click_" + beforeID, SessionID: sessionID, RunID: runID, Tool: "browser.click", Status: app.ToolCallStatusCompleted,
		Arguments: map[string]any{"snapshot_id": beforeID, "uid": ref, "page_id": "page_1"},
		Result:    browserautomation.Result{Output: map[string]any{"snapshot_id": beforeID, "clicked": ref, "page_id": "page_1"}},
		StartedAt: browserVerificationCallTime(beforeNumber*2 + 1),
	})

	testSaveToolCall(st, browserVerificationSnapshotCall(sessionID, runID, afterNumber, beforeID, afterDigest, afterContentDigest, browserVerificationRef(afterNumber)))
}

func browserVerificationSnapshotCall(sessionID, runID string, number int, previousID, digest, contentDigest, ref string) app.ToolCall {
	controls := []any{}
	if ref != "" {
		controls = append(controls, map[string]any{"ref": ref, "role": "button", "accessible_name": "Next"})
	}
	snapshotID := browserVerificationSnapshotID(number)
	return app.ToolCall{
		ID: "call_" + snapshotID, SessionID: sessionID, RunID: runID, Tool: "browser.snapshot", Status: app.ToolCallStatusCompleted,
		StartedAt: browserVerificationCallTime(number * 2),
		Result: browserautomation.Result{Output: map[string]any{"snapshot": map[string]any{
			"snapshot_id": snapshotID, "previous_snapshot_id": previousID, "page_id": "page_1",
			"digest": digest, "content_digest": contentDigest, "session_generation": uint64(7), "controls": controls,
		}}},
	}
}

func browserVerificationCallTime(order int) time.Time {
	return time.Unix(0, int64(order)*int64(time.Microsecond)).UTC()
}

func browserVerificationSnapshotID(number int) string {
	return "snapshot_" + browserAutomationStringValue(number)
}

func browserVerificationRef(snapshotNumber int) string {
	return browserVerificationSnapshotID(snapshotNumber) + ":e1:0123456789abcdef"
}
