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

func TestBrowserVerifyAcceptsBoundChangedSnapshots(t *testing.T) {
	st, hub := newBrowserVerificationHub()
	seedBrowserVerificationCycle(st, "session", "run", 1, "before", "after")

	result, err := hub.Execute(context.Background(), "browser.verify", map[string]any{
		"before_snapshot_id": "snapshot_1", "after_snapshot_id": "snapshot_2",
		"element_ref": browserVerificationRef(1), "verdict": "success", "reason": "目标按钮已进入下一状态",
	}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	output := result.Output.(map[string]any)
	if output["status"] != "succeeded" || output["code"] != "ok" || output["goal_satisfied"] != true || output["click_count"] != 1 {
		t.Fatalf("unexpected successful verification: %#v", output)
	}
}

func TestBrowserVerifyRejectsRepeatedStateAsLoop(t *testing.T) {
	st, hub := newBrowserVerificationHub()
	seedBrowserVerificationCycle(st, "session", "run", 1, "same", "same")

	result, err := hub.Execute(context.Background(), "browser.verify", map[string]any{
		"before_snapshot_id": "snapshot_1", "after_snapshot_id": "snapshot_2",
		"element_ref": browserVerificationRef(1), "verdict": "progress", "reason": "页面没有变化",
	}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	output := result.Output.(map[string]any)
	if output["status"] != "failed" || output["code"] != "interaction_loop_detected" || output["goal_satisfied"] != false {
		t.Fatalf("repeated state did not fail closed: %#v", output)
	}
}

func TestBrowserVerifyStopsProgressAfterThirdClick(t *testing.T) {
	st, hub := newBrowserVerificationHub()
	seedBrowserVerificationCycle(st, "session", "run", 1, "state_0", "state_1")
	seedBrowserVerificationCycle(st, "session", "run", 2, "state_1", "state_2")
	seedBrowserVerificationCycle(st, "session", "run", 3, "state_2", "state_3")

	result, err := hub.Execute(context.Background(), "browser.verify", map[string]any{
		"before_snapshot_id": "snapshot_5", "after_snapshot_id": "snapshot_6",
		"element_ref": browserVerificationRef(5), "verdict": "progress", "reason": "还需要继续点击",
	}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	output := result.Output.(map[string]any)
	if output["status"] != "failed" || output["code"] != "interaction_attempt_limit" || output["click_count"] != 3 {
		t.Fatalf("third progress verdict did not stop the interaction: %#v", output)
	}
}

func TestBrowserVerifyRejectsSnapshotsFromAnotherRun(t *testing.T) {
	st, hub := newBrowserVerificationHub()
	seedBrowserVerificationCycle(st, "session", "other_run", 1, "before", "after")

	_, err := hub.Execute(context.Background(), "browser.verify", map[string]any{
		"before_snapshot_id": "snapshot_1", "after_snapshot_id": "snapshot_2",
		"element_ref": browserVerificationRef(1), "verdict": "success", "reason": "changed",
	}, "session", "run")
	if err == nil {
		t.Fatal("browser.verify accepted snapshots outside the current run")
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
	st.SaveToolCall(browserVerificationSnapshotCall(sessionID, runID, afterNumber, beforeID, afterDigest, ""))
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
			"digest": digest, "controls": controls,
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
