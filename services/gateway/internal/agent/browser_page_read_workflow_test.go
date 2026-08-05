package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestBrowserPageReadRunsFixedHiddenManagedBrowserChain(t *testing.T) {
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		cfg.browser = true
		cfg.config.Tools.BrowserAutomation.Enabled = true
		cfg.config.Security.BrowserReadAllowHosts = []string{"example.com"}
	})
	defer closeRuntime()

	result, err := runtime.HandleMessage(context.Background(), session.ID, "Read and summarize https://example.com/article")
	if err != nil {
		t.Fatal(err)
	}
	if result.RouteDecision == nil || result.RouteDecision.Status != app.RouteMatched ||
		result.RouteDecision.CapabilityPath[1] != app.CapabilityBrowserPageRead || result.Run.Workflow == nil ||
		result.Run.Workflow.Plan.ProfileID != app.WorkflowBrowserPageRead || result.Run.Workflow.Status != app.WorkflowStatusSucceeded {
		t.Fatalf("explicit page read did not complete browser.page_read: %#v", result)
	}
	calls := toolCallsForRun(st.ListToolCalls(session.ID), result.Run.ID)
	want := []string{"browser.status", "browser.open", "browser.read"}
	if len(calls) != len(want) {
		t.Fatalf("page-read chain length = %d, want %d: %#v", len(calls), len(want), calls)
	}
	for index, call := range calls {
		if call.Tool != want[index] {
			t.Fatalf("page-read call %d = %q, want %q", index, call.Tool, want[index])
		}
		if stringValue(call.Arguments["browser_mode"]) != "autonomous" || stringValue(call.Arguments["presentation"]) != "hidden" || boolValue(call.Arguments["surface_visible"]) {
			t.Fatalf("page-read call was not hidden/autonomous: %#v", call)
		}
	}
	readArgs := calls[2].Arguments
	if !boolValue(readArgs["require_browser_session"]) || !boolValue(readArgs["reuse_active_page"]) {
		t.Fatalf("page-read did not require and reuse the managed browser page: %#v", readArgs)
	}
}

func TestBrowserPageReadFallbackReturnsBoundedExtractedContent(t *testing.T) {
	content := strings.Repeat("页面读取内容。", 2500)
	answer := browserReadFallbackFailure([]app.ToolCall{{
		Tool: "browser.read", Status: "completed",
		Result: map[string]any{
			"title": "Example article", "final_url": "https://example.com/article",
			"text": content, "truncated": false,
		},
	}})
	if !strings.Contains(answer, "网页读取内容（外部不可信内容）") || !strings.Contains(answer, "Example article") ||
		!strings.Contains(answer, "https://example.com/article") || !strings.Contains(answer, "页面读取内容") {
		t.Fatalf("page-read fallback omitted extracted content or provenance: %q", answer)
	}
	if len([]rune(answer)) > 12500 || !strings.Contains(answer, "内容已按读取或返回上限截断") {
		t.Fatalf("page-read fallback was not bounded: runes=%d", len([]rune(answer)))
	}
}

func TestBrowserPageReadLoginResumeRestartsRevision1HealthOpenReadChain(t *testing.T) {
	adapter := &loginBlockBrowserAdapter{selectedTabURL: "https://example.com/protected"}
	runtime, st, session, closeRuntime := newWorkflowE2ERuntime(t, func(cfg *testRuntimeConfig) {
		cfg.browser = true
		cfg.browserAdapter = adapter
		cfg.config.Tools.BrowserAutomation.Enabled = true
		cfg.config.Security.BrowserReadAllowHosts = []string{"example.com"}
	})
	defer closeRuntime()

	first, err := runtime.HandleMessage(context.Background(), session.ID, "Read https://example.com/protected")
	if err != nil {
		t.Fatal(err)
	}
	if first.Run.State != "browser_login_blocked" || first.Run.Workflow == nil ||
		first.Run.Workflow.Plan.ProfileID != app.WorkflowBrowserPageRead || adapter.readCalls != 1 {
		t.Fatalf("page-read authentication did not pause revision 1 after its read stage: result=%#v adapter=%#v", first, adapter)
	}
	block, ok := st.FindActiveBrowserLoginBlock(session.ID)
	if !ok || block.WorkflowID != app.WorkflowBrowserPageRead || block.WorkflowRevision != browserPageReadRevision1 {
		t.Fatalf("page-read handoff lost its revision identity: %#v", block)
	}

	second, err := runtime.HandleMessage(context.Background(), session.ID, "登录完成")
	if err != nil {
		t.Fatal(err)
	}
	if second.Run.ID != first.Run.ID || second.Run.State != "completed" || second.Run.Workflow == nil ||
		second.Run.Workflow.Status != app.WorkflowStatusSucceeded {
		t.Fatalf("page-read handoff did not resume the original revision-1 run: %#v", second)
	}
	calls := toolCallsForRun(st.ListToolCalls(session.ID), first.Run.ID)
	counts := map[string]int{}
	for _, call := range calls {
		counts[call.Tool]++
	}
	if counts["browser.status"] != 2 || counts["browser.open"] != 2 || counts["browser.read"] != 2 || adapter.readCalls != 2 {
		t.Fatalf("login resume did not restart the fixed health/open/read chain: counts=%#v adapter=%#v", counts, adapter)
	}
	blocks := st.ListBrowserLoginBlocks(session.ID, "")
	if len(blocks) != 1 || blocks[0].Status != app.BrowserHandoffStatusResolved {
		t.Fatalf("page-read handoff did not resolve after the fresh hidden chain: %#v", blocks)
	}
}
