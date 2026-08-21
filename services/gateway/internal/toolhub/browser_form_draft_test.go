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

func TestBrowserFormDraftAcceptsOnlyLatestExactOwnerBoundAction(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		role      string
		valueKey  string
		value     string
	}{
		{name: "type", operation: "browser.type", role: "textbox", valueKey: "text", value: "Alice Example"},
		{name: "select", operation: "browser.select", role: "combobox", valueKey: "value", value: "Technical Support"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			st, hub, adapter, ref := newBrowserFormDraftHub(t, test.role, "Contact field", test.value)
			args := browserFormDraftArgs(ref, test.valueKey, test.value)
			result, err := hub.Execute(context.Background(), test.operation, args, "session", "run")
			if err != nil {
				t.Fatal(err)
			}
			if adapter.calls != 1 || adapter.lastTool != test.operation || adapter.lastArgs[test.valueKey] != test.value {
				t.Fatalf("exact owner action did not reach the adapter once: %#v", adapter)
			}
			output, ok := result.Output.(map[string]any)
			if !ok || strings.TrimSpace(browserAutomationStringValue(output["draft_action_id"])) == "" ||
				strings.TrimSpace(browserAutomationStringValue(output["value_digest"])) == "" || output["value_source"] != "owner_request" {
				t.Fatalf("draft action output lost its frozen identity: %#v", result.Output)
			}
			if calls := testListToolCalls(st, "session"); len(calls) != 1 || calls[0].Tool != "browser.snapshot" {
				t.Fatalf("ToolHub draft execution unexpectedly persisted a parallel action record: %#v", calls)
			}
		})
	}
}

func TestBrowserFormDraftRejectsForeignOrStaleSnapshotBindings(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any, *store.MemoryStore, string)
	}{
		{name: "foreign ref", mutate: func(args map[string]any, _ *store.MemoryStore, _ string) { args["uid"] = "snapshot_1:e9:foreign" }},
		{name: "foreign page", mutate: func(args map[string]any, _ *store.MemoryStore, _ string) { args["page_id"] = "page_2" }},
		{name: "stale session generation", mutate: func(args map[string]any, _ *store.MemoryStore, _ string) { args["session_generation"] = 6 }},
		{name: "stale page generation", mutate: func(args map[string]any, _ *store.MemoryStore, _ string) { args["page_generation"] = 8 }},
		{name: "older snapshot", mutate: func(args map[string]any, st *store.MemoryStore, ref string) {
			seedBrowserFormDraftSnapshot(st, "run", "snapshot_2", "page_1", 7, 10, ref+"-new", "textbox", "Other field")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			st, hub, adapter, ref := newBrowserFormDraftHub(t, "textbox", "Display name", "Alice Example")
			args := browserFormDraftArgs(ref, "text", "Alice Example")
			test.mutate(args, st, ref)
			_, err := hub.Execute(context.Background(), "browser.type", args, "session", "run")
			if app.ToolErrorCodeFrom(err) != app.ToolErrorDraftActionStale || adapter.calls != 0 {
				t.Fatalf("stale/foreign action was not rejected before mutation: err=%v adapter=%#v", err, adapter)
			}
		})
	}
}

func TestBrowserFormDraftRejectsForbiddenControlsAndPageSuppliedValues(t *testing.T) {
	for _, forbidden := range []struct {
		role      string
		label     string
		container string
	}{
		{role: "textbox", label: "Password"},
		{role: "textbox", label: "Verification code"},
		{role: "textbox", label: "银行卡号"},
		{role: "combobox", label: "Payment method"},
		{role: "textbox", label: "Message", container: "Send message"},
		{role: "button", label: "Display name"},
	} {
		if BrowserDraftControlAllowed("browser.type", forbidden.role, forbidden.label, forbidden.container) {
			t.Fatalf("forbidden control was accepted: %#v", forbidden)
		}
	}

	_, hub, adapter, ref := newBrowserFormDraftHub(t, "textbox", "Display name", "Alice Example")
	_, err := hub.Execute(context.Background(), "browser.type", browserFormDraftArgs(ref, "text", "page suggested value"), "session", "run")
	if app.ToolErrorCodeFrom(err) != app.ToolErrorDraftForbiddenControl || adapter.calls != 0 {
		t.Fatalf("page-supplied value was not rejected before mutation: err=%v adapter=%#v", err, adapter)
	}
}

func newBrowserFormDraftHub(t *testing.T, role, label, ownerValue string) (*store.MemoryStore, *ToolHub, *recordingBrowserDraftAdapter, string) {
	t.Helper()
	cfg := config.Default()
	cfg.Tools.BrowserAutomation.Enabled = true
	st := store.NewMemoryStore()
	testSaveRun(st, app.AgentRun{
		ID: "run", SessionID: "session", StartedAt: time.Now().UTC(),
		Workflow: &app.WorkflowState{
			Plan:  app.WorkflowPlan{ProfileID: app.WorkflowBrowserFormDraft},
			Route: app.RouteDecision{Slots: app.RouteSlots{Query: "Fill the contact field with " + ownerValue}},
			Browser: &app.BrowserWorkflowState{Target: app.BrowserTargetDescriptor{
				TargetKind: app.BrowserTargetCurrentTab,
			}},
		},
	})

	ref := "snapshot_1:e1:0123456789abcdef"
	seedBrowserFormDraftSnapshot(st, "run", "snapshot_1", "page_1", 7, 9, ref, role, label)
	adapter := &recordingBrowserDraftAdapter{}
	hub := New(cfg, st).WithBrowserAutomationAdapter(adapter)
	t.Cleanup(func() { _ = hub.Close() })
	return st, hub, adapter, ref
}

func browserFormDraftArgs(ref, valueKey, value string) map[string]any {
	return map[string]any{
		"uid": ref, "page_id": "page_1", "snapshot_id": "snapshot_1",
		"session_generation": 7, "page_generation": 9, valueKey: value,
	}
}

func seedBrowserFormDraftSnapshot(st *store.MemoryStore, runID, snapshotID, pageID string, sessionGeneration, pageGeneration uint64, ref, role, label string) {
	testSaveToolCall(st, app.ToolCall{
		ID: app.NewID("snapshot_call"), SessionID: "session", RunID: runID,
		Tool: "browser.snapshot", Status: "completed", StartedAt: time.Now().UTC(),
		Result: browserautomation.Result{Output: map[string]any{"snapshot": map[string]any{
			"snapshot_id": snapshotID, "page_id": pageID, "url": "https://example.com/contact",
			"digest": "digest-" + snapshotID, "content_digest": "content-" + snapshotID,
			"session_generation": sessionGeneration, "page_generation": pageGeneration,
			"controls": []any{map[string]any{
				"ref": ref, "role": role, "accessible_name": label, "container": "Contact form",
			}},
		}}},
	})

}

type recordingBrowserDraftAdapter struct {
	calls    int
	lastTool string
	lastArgs map[string]any
}

func (*recordingBrowserDraftAdapter) Health(context.Context, map[string]any) (browserautomation.Result, error) {
	return browserautomation.Result{Tool: "browser.status", Output: map[string]any{"ok": true}, Provider: "draft-test", Untrusted: true}, nil
}

func (a *recordingBrowserDraftAdapter) Call(_ context.Context, tool string, args map[string]any) (browserautomation.Result, error) {
	a.calls++
	a.lastTool = tool
	a.lastArgs = cloneTestArgs(args)
	return browserautomation.Result{
		Tool: tool, RawTool: strings.TrimPrefix(tool, "browser."), Output: map[string]any{"ok": true},
		Provider: "draft-test", Untrusted: true,
	}, nil
}

func (*recordingBrowserDraftAdapter) ReadPage(context.Context, string, map[string]any) (browserautomation.PageReadResult, error) {
	return browserautomation.PageReadResult{}, nil
}

func (*recordingBrowserDraftAdapter) Close() error { return nil }
