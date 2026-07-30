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
	for _, verdict := range []string{"satisfied", "success"} {
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
	st.SaveRun(app.AgentRun{
		ID: "run", SessionID: "session", StartedAt: browserVerificationCallTime(1),
		Workflow: &app.WorkflowState{Plan: app.WorkflowPlan{ProfileID: app.WorkflowBrowserInteraction}},
	})
	ref := browserVerificationRef(1)
	call := browserVerificationSnapshotCall("session", "run", 1, "", "state", ref)
	result := call.Result.(browserautomation.Result)
	payload := result.Output.(map[string]any)["snapshot"].(map[string]any)
	payload["controls"] = []any{map[string]any{"ref": ref, "role": "button", "accessible_name": "Delete account"}}
	call.Result = result
	st.SaveToolCall(call)

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

func newBrowserVerificationHub() (*store.MemoryStore, *ToolHub) {
	cfg := config.Default()
	cfg.Tools.BrowserAutomation.Enabled = true
	st := store.NewMemoryStore()
	return st, New(cfg, st)
}

func seedBrowserVerificationCycle(st *store.MemoryStore, sessionID, runID string, cycle int, beforeDigest, afterDigest string) {
	beforeNumber := cycle*2 - 1
	afterNumber := cycle * 2
	beforeID := browserVerificationSnapshotID(beforeNumber)
	ref := browserVerificationRef(beforeNumber)
	st.SaveToolCall(browserVerificationSnapshotCall(sessionID, runID, beforeNumber, "", beforeDigest, ref))
	st.SaveToolCall(app.ToolCall{
		ID: "click_" + beforeID, SessionID: sessionID, RunID: runID, Tool: "browser.click", Status: "completed",
		Arguments: map[string]any{"snapshot_id": beforeID, "uid": ref, "page_id": "page_1"},
		Result:    browserautomation.Result{Output: map[string]any{"snapshot_id": beforeID, "clicked": ref, "page_id": "page_1"}},
		StartedAt: browserVerificationCallTime(beforeNumber*2 + 1),
	})
	st.SaveToolCall(browserVerificationSnapshotCall(sessionID, runID, afterNumber, beforeID, afterDigest, browserVerificationRef(afterNumber)))
}

func browserVerificationSnapshotCall(sessionID, runID string, number int, previousID, digest, ref string) app.ToolCall {
	controls := []any{}
	if ref != "" {
		controls = append(controls, map[string]any{"ref": ref, "role": "button", "accessible_name": "Next"})
	}
	snapshotID := browserVerificationSnapshotID(number)
	return app.ToolCall{
		ID: "call_" + snapshotID, SessionID: sessionID, RunID: runID, Tool: "browser.snapshot", Status: "completed",
		StartedAt: browserVerificationCallTime(number * 2),
		Result: browserautomation.Result{Output: map[string]any{"snapshot": map[string]any{
			"snapshot_id": snapshotID, "previous_snapshot_id": previousID, "page_id": "page_1",
			"digest": digest, "session_generation": uint64(7), "controls": controls,
		}}},
	}
}

func browserVerificationCallTime(order int) time.Time {
	return time.Unix(0, int64(order)).UTC()
}

func browserVerificationSnapshotID(number int) string {
	return "snapshot_" + browserAutomationStringValue(number)
}

func browserVerificationRef(snapshotNumber int) string {
	return browserVerificationSnapshotID(snapshotNumber) + ":e1:0123456789abcdef"
}
