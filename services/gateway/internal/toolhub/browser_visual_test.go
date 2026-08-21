package toolhub

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestBrowserVisualInspectReturnsFreshGenerationBoundEvidence(t *testing.T) {
	st, hub, adapter := newBrowserVisualHub(t, 9)
	result, err := hub.Execute(context.Background(), "browser.visual_inspect", browserVisualTestArgs(), "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	output, ok := result.Output.(map[string]any)
	if !ok || output["status"] != "completed" || output["reason"] != "owner_requested" ||
		output["session_generation"] != uint64(7) || output["page_generation"] != uint64(9) ||
		output["snapshot_id"] != "snapshot_1" || output["post_snapshot_id"] != "snapshot_post" ||
		output["screenshot_digest"] == "" || output["summary"] == "" || output["untrusted"] != true {
		t.Fatalf("fresh visual evidence lost its page identity: %#v", result.Output)
	}
	if _, exists := output["coordinates"]; exists {
		t.Fatalf("visual evidence exposed coordinates: %#v", output)
	}
	if _, exists := output["element_ref"]; exists {
		t.Fatalf("visual evidence exposed an executable element ref: %#v", output)
	}
	if adapter.screenshotCalls != 1 || adapter.snapshotCalls != 1 {
		t.Fatalf("visual inspection did not capture and then revalidate exactly once: %#v", adapter)
	}
	if calls := testListToolCalls(st, "session"); len(calls) != 1 || calls[0].Tool != "browser.snapshot" {
		t.Fatalf("Workflow-only visual helper created parallel persisted tool calls: %#v", calls)
	}
}

func TestBrowserVisualInspectRejectsPageChangeDuringInference(t *testing.T) {
	_, hub, adapter := newBrowserVisualHub(t, 10)
	_, err := hub.Execute(context.Background(), "browser.visual_inspect", browserVisualTestArgs(), "session", "run")
	if app.ToolErrorCodeFrom(err) != app.ToolErrorVisualEvidenceStale {
		t.Fatalf("page generation change did not produce typed stale evidence: %v", err)
	}
	if adapter.screenshotCalls != 1 || adapter.snapshotCalls != 1 {
		t.Fatalf("stale check did not occur after capture and inference: %#v", adapter)
	}
}

func TestBrowserVisualInspectRequiresFrozenOwnerReasonAndLatestSnapshot(t *testing.T) {
	st, hub, adapter := newBrowserVisualHub(t, 9)
	run, _ := testGetRun(st, "run")
	run.Workflow.Route.Facts = nil
	testSaveRun(st, run)
	_, err := hub.Execute(context.Background(), "browser.visual_inspect", browserVisualTestArgs(), "session", "run")
	if app.ToolErrorCodeFrom(err) != app.ToolErrorVisualEvidenceStale || adapter.screenshotCalls != 0 {
		t.Fatalf("unfrozen visual request reached capture: err=%v adapter=%#v", err, adapter)
	}
}

func newBrowserVisualHub(t *testing.T, postPageGeneration uint64) (*store.MemoryStore, *ToolHub, *browserVisualTestAdapter) {
	t.Helper()
	root := t.TempDir()
	screenshotPath := filepath.Join(root, "browser-state.jpg")
	if err := writeTestJPEG(screenshotPath, 32, 24); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Model.Mock = true
	cfg.Tools.BrowserAutomation.Enabled = true
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	st := store.NewMemoryStore()
	testSaveRun(st, app.AgentRun{
		ID: "run", SessionID: "session", StartedAt: time.Now().UTC(),
		Workflow: &app.WorkflowState{
			Plan:  app.WorkflowPlan{ProfileID: app.WorkflowBrowserAutomation},
			Route: app.RouteDecision{Facts: map[string]string{"browser_visual_reason": "owner_requested"}},
		},
	})

	seedBrowserVisualSnapshot(st)
	adapter := &browserVisualTestAdapter{screenshotPath: screenshotPath, postPageGeneration: postPageGeneration}
	hub := New(cfg, st).WithBrowserAutomationAdapter(adapter)
	t.Cleanup(func() { _ = hub.Close() })
	return st, hub, adapter
}

func seedBrowserVisualSnapshot(st *store.MemoryStore) {
	testSaveToolCall(st, app.ToolCall{
		ID: "snapshot_call", SessionID: "session", RunID: "run", Tool: "browser.snapshot", Status: "completed",
		StartedAt: time.Now().UTC(),
		Result: browserautomation.Result{Output: map[string]any{"snapshot": map[string]any{
			"snapshot_id": "snapshot_1", "page_id": "page_1", "url": "https://example.com/dashboard",
			"digest": "digest_1", "content_digest": "content_1",
			"session_generation": uint64(7), "page_generation": uint64(9), "controls": []any{},
		}}},
	})

}

func browserVisualTestArgs() map[string]any {
	return map[string]any{
		"page_id": "page_1", "snapshot_id": "snapshot_1", "snapshot_digest": "digest_1",
		"session_generation": 7, "page_generation": 9, "reason": "owner_requested",
		"question": "Describe the visible dashboard state without coordinates.",
	}
}

type browserVisualTestAdapter struct {
	screenshotPath     string
	postPageGeneration uint64
	screenshotCalls    int
	snapshotCalls      int
}

func (*browserVisualTestAdapter) Health(context.Context, map[string]any) (browserautomation.Result, error) {
	return browserautomation.Result{Tool: "browser.status", Output: map[string]any{"ok": true}, Provider: "visual-test", Untrusted: true}, nil
}

func (a *browserVisualTestAdapter) Call(_ context.Context, tool string, _ map[string]any) (browserautomation.Result, error) {
	switch tool {
	case "browser.screenshot":
		a.screenshotCalls++
		return browserautomation.Result{
			Tool: tool, RawTool: "agent_browser_screenshot", Output: map[string]any{"screenshot_path": a.screenshotPath},
			Provider: "visual-test", Untrusted: true,
		}, nil
	case "browser.snapshot":
		a.snapshotCalls++
		return browserautomation.Result{
			Tool: tool, RawTool: "agent_browser_snapshot", Output: map[string]any{"snapshot": map[string]any{
				"snapshot_id": "snapshot_post", "page_id": "page_1", "url": "https://example.com/dashboard",
				"digest": "digest_1", "content_digest": "content_1",
				"session_generation": uint64(7), "page_generation": a.postPageGeneration,
			}}, Provider: "visual-test", Untrusted: true,
		}, nil
	default:
		return browserautomation.Result{}, nil
	}
}

func (*browserVisualTestAdapter) ReadPage(context.Context, string, map[string]any) (browserautomation.PageReadResult, error) {
	return browserautomation.PageReadResult{}, nil
}

func (*browserVisualTestAdapter) Close() error { return nil }
