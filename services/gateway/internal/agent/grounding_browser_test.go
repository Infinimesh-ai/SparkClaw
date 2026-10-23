package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
)

type embeddedGroundingBrowserAdapter struct{ fakeInteractionBrowserAdapter }

func (adapter *embeddedGroundingBrowserAdapter) Call(ctx context.Context, tool string, args map[string]any) (browserautomation.Result, error) {
	result, err := adapter.fakeInteractionBrowserAdapter.Call(ctx, tool, args)
	if err != nil {
		return result, err
	}
	output := browserOutcomePayload(result.Output)
	output["surface_visible"] = false
	if snapshot, ok := browserSnapshotPayload(output); ok {
		snapshot["profile_id"] = "client_embedded"
	}
	for _, value := range result.Pages {
		if page, ok := anyMap(value); ok {
			page["profile_id"] = "client_embedded"
		}
	}
	return result, nil
}

func TestBrowserOpenGroundingRunsThroughWorkflowFinalization(t *testing.T) {
	adapter := &embeddedGroundingBrowserAdapter{fakeInteractionBrowserAdapter{emptyTabs: true}}
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		cfg.config.Tools.BrowserAutomation.Enabled = true
		cfg.browserAdapter = adapter
	})
	defer closeRuntime()
	result, err := runtime.HandleMessage(context.Background(), session.ID, "Use browser automation to open https://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if result.Run.State != "completed" || result.Run.Workflow.Status != app.WorkflowStatusSucceeded ||
		!strings.Contains(result.Message.Content, "conversation's task page") || !strings.Contains(result.Message.Content, "https://example.com/") {
		t.Fatalf("workflow finalization did not ground its accepted result: state=%s workflow=%s summary=%q", result.Run.State, result.Run.Workflow.Status, result.Message.Content)
	}
	for _, call := range result.ToolCalls {
		if call.Tool == "browser.assess_goal" {
			t.Fatal("pure open regression unexpectedly obtained an interaction goal assessment")
		}
	}
	if !hasAgentAuditField(mustAgentListAudit(t, st, session.ID), "fallback.policy_applied", "strategy", strategyGroundedResult) {
		t.Fatal("finalization did not apply the grounded fallback after its persisted result")
	}
}

func browserOpenGroundingFixture() (app.AgentRun, []app.ToolCall) {
	resultURL := "https://www.apple.com.cn/store"
	identity := app.BrowserSessionRef{OwnerID: "owner", ProfileID: "client_embedded", Generation: 1,
		Presentation: app.BrowserPresentationVisible, ProviderSessionRef: "browser_host:lease"}
	run := app.AgentRun{ID: "run_grounding", SessionID: "session_grounding", State: "completed", Workflow: &app.WorkflowState{
		Status: app.WorkflowStatusSucceeded, Plan: app.WorkflowPlan{ProfileID: app.WorkflowBrowserAutomation},
		Browser: &app.BrowserWorkflowState{Result: &app.BrowserResultEvidence{
			Target: app.BrowserTargetDescriptor{CanonicalURL: resultURL}, VerifiedAt: time.Now().UTC(),
			VisiblePageID: "page", VisibleSnapshotID: "snapshot", VisibleSnapshotDigest: "digest", VisibleSession: identity,
			SourceToolCallIDs: []string{"snapshot_call"},
		}},
	}}
	calls := []app.ToolCall{
		{Tool: "browser.list_tabs", Status: app.ToolCallStatusCompleted, Result: map[string]any{"pages": []any{}}},
		{Tool: "browser.open", Status: app.ToolCallStatusCompleted},
		{ID: "snapshot_call", RunID: run.ID, WorkflowID: app.WorkflowBrowserAutomation, Tool: "browser.snapshot", Status: app.ToolCallStatusCompleted,
			Result: map[string]any{
				// ToolHub's requested visibility must not override the native host.
				"surface_visible": true, "output": map[string]any{"surface_visible": false, "snapshot": map[string]any{
					"snapshot_id": "snapshot", "digest": "digest", "page_id": "page", "url": resultURL,
					"session_generation": 1, "presentation": "visible", "owner_id": identity.OwnerID,
					"profile_id": identity.ProfileID, "provider_session_ref": identity.ProviderSessionRef,
				}},
			}},
	}
	return run, calls
}

func TestBrowserOpenGroundingUsesPersistedWorkflowResultWithoutGoalAssessment(t *testing.T) {
	run, calls := browserOpenGroundingFixture()
	st := store.NewMemoryStore()
	session := storetest.MustCreateSession(t, st, "browser grounding")
	run.SessionID = session.ID
	testSaveRun(st, run)
	runtime := Runtime{store: st}
	answer := runtime.applyGroundedSummary(context.Background(), session.ID, run.ID, "打开苹果官网", "", calls)
	if !strings.Contains(answer, "当前会话的任务页面") || !strings.Contains(answer, run.Workflow.Browser.Result.Target.CanonicalURL) ||
		strings.Contains(answer, "可见浏览器") || strings.Contains(answer, "没有打开") {
		t.Fatalf("completed open workflow lost its native result: %q", answer)
	}
}

func TestBrowserResultGroundingUsesActualProviderVisibility(t *testing.T) {
	run, calls := browserOpenGroundingFixture()
	browserOutcomePayload(calls[2].Result)["surface_visible"] = true
	answer, ok := browserWorkflowResultAnswer("打开苹果官网", run, calls)
	if !ok || !strings.Contains(answer, "可见浏览器") {
		t.Fatalf("actual visible result not reported: %q, %v", answer, ok)
	}
}

func TestBrowserOpenGroundingRejectsIncompleteOrStaleEvidence(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*app.AgentRun, *[]app.ToolCall)
	}{
		{"running", func(run *app.AgentRun, _ *[]app.ToolCall) { run.Workflow.Status = app.WorkflowStatusRunning }},
		{"blocked", func(run *app.AgentRun, _ *[]app.ToolCall) { run.Workflow.Status = app.WorkflowStatusBlocked }},
		{"unverified", func(run *app.AgentRun, _ *[]app.ToolCall) { run.Workflow.Browser.Result.VerifiedAt = time.Time{} }},
		{"other run", func(_ *app.AgentRun, calls *[]app.ToolCall) { (*calls)[2].RunID = "other_run" }},
		{"missing provenance", func(run *app.AgentRun, _ *[]app.ToolCall) { run.Workflow.Browser.Result.SourceToolCallIDs = nil }},
		{"wrong snapshot", func(run *app.AgentRun, _ *[]app.ToolCall) { run.Workflow.Browser.Result.VisibleSnapshotID = "other" }},
		{"wrong digest", func(run *app.AgentRun, _ *[]app.ToolCall) {
			run.Workflow.Browser.Result.VisibleSnapshotDigest = "other"
		}},
		{"wrong page", func(run *app.AgentRun, _ *[]app.ToolCall) { run.Workflow.Browser.Result.VisiblePageID = "other" }},
		{"wrong generation", func(run *app.AgentRun, _ *[]app.ToolCall) { run.Workflow.Browser.Result.VisibleSession.Generation++ }},
		{"wrong owner", func(run *app.AgentRun, _ *[]app.ToolCall) {
			run.Workflow.Browser.Result.VisibleSession.OwnerID = "other"
		}},
		{"wrong route", func(_ *app.AgentRun, calls *[]app.ToolCall) {
			snapshot, _ := browserSnapshotPayload((*calls)[2].Result)
			snapshot["url"] = "https://example.com/"
		}},
		{"missing URL", func(_ *app.AgentRun, calls *[]app.ToolCall) {
			snapshot, _ := browserSnapshotPayload((*calls)[2].Result)
			delete(snapshot, "url")
		}},
		{"later navigation", func(_ *app.AgentRun, calls *[]app.ToolCall) {
			*calls = append(*calls, app.ToolCall{Tool: "browser.navigate", Status: app.ToolCallStatusCompleted})
		}},
		{"later close", func(_ *app.AgentRun, calls *[]app.ToolCall) {
			*calls = append(*calls, app.ToolCall{Tool: "browser.close", Status: app.ToolCallStatusCompleted})
		}},
		{"later failure", func(_ *app.AgentRun, calls *[]app.ToolCall) {
			*calls = append(*calls, app.ToolCall{Tool: "browser.wait", Status: app.ToolCallStatusFailed})
		}},
		{"rejected goal", func(_ *app.AgentRun, calls *[]app.ToolCall) {
			*calls = append(*calls, app.ToolCall{Tool: "browser.assess_goal", Status: app.ToolCallStatusCompleted,
				Result: map[string]any{"status": "failed", "goal_satisfied": false}})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			run, calls := browserOpenGroundingFixture()
			test.edit(&run, &calls)
			if answer, ok := browserWorkflowResultAnswer("打开苹果官网", run, calls); ok {
				t.Fatalf("unverified result was presented as success: %q", answer)
			}
			if answer, ok := browserTabsAnswerFromCalls(calls); ok {
				t.Fatalf("stale initial tab list was presented as current: %q", answer)
			}
		})
	}
}

func TestBrowserTabsGroundingRequiresLatestObservation(t *testing.T) {
	for _, pages := range [][]any{{}, {map[string]any{"page_id": "page", "url": "https://example.com/"}}} {
		list := app.ToolCall{Tool: "browser.list_tabs", Status: app.ToolCallStatusCompleted, Result: map[string]any{"pages": pages}}
		if _, ok := browserTabsAnswerFromCalls([]app.ToolCall{list}); !ok {
			t.Fatal("a current tab listing should remain available")
		}
		for _, tool := range []string{"browser.open", "browser.navigate", "browser.click", "browser.snapshot", "browser.close"} {
			later := app.ToolCall{Tool: tool, Status: app.ToolCallStatusCompleted}
			if answer, ok := browserTabsAnswerFromCalls([]app.ToolCall{list, later}); ok {
				t.Fatalf("%s left an obsolete tab list current: %q", tool, answer)
			}
			if _, ok := browserTabsAnswerFromCalls([]app.ToolCall{later, list}); !ok {
				t.Fatalf("a refreshed list after %s was rejected", tool)
			}
		}
	}
}

func TestBrowserGoalGroundingRejectsLaterFailureOrAction(t *testing.T) {
	_, calls := browserOpenGroundingFixture()
	calls = append(calls, app.ToolCall{Tool: "browser.assess_goal", Status: app.ToolCallStatusCompleted,
		Result: map[string]any{"status": "succeeded", "goal_satisfied": true, "snapshot_id": "snapshot"}})
	if _, ok := browserVerifiedResultAnswerFromCalls("打开苹果官网", calls); !ok {
		t.Fatal("verified latest assessment was rejected")
	}
	for _, call := range []app.ToolCall{
		{Tool: "browser.wait", Status: app.ToolCallStatusFailed},
		{Tool: "browser.navigate", Status: app.ToolCallStatusCompleted},
		{Tool: "browser.close", Status: app.ToolCallStatusCompleted},
	} {
		if answer, ok := browserVerifiedResultAnswerFromCalls("打开苹果官网", append(calls, call)); ok {
			t.Fatalf("%s did not invalidate the prior success: %q", call.Tool, answer)
		}
	}
}
