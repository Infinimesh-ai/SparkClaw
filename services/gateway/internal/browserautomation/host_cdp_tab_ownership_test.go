package browserautomation

import (
	"context"
	"strings"
	"testing"
)

func agentBrowserTabsResult(tabs ...map[string]any) agentBrowserToolResult {
	values := make([]any, 0, len(tabs))
	for _, tab := range tabs {
		values = append(values, tab)
	}
	return agentBrowserToolResult{Data: map[string]any{"tabs": values}}
}

func TestHostCDPOpenRegistersOnlyUniqueNewTab(t *testing.T) {
	adapter := &AgentBrowserAdapter{}
	entry := &agentBrowserSessionEntry{adapter: adapter, ownedTabs: map[string]string{}}
	listCall := 0
	adapter.callAgentTool = func(_ context.Context, _ *agentBrowserSession, name string, args map[string]any) (agentBrowserToolResult, error) {
		switch name {
		case "agent_browser_tab_list":
			listCall++
			if listCall == 1 {
				return agentBrowserTabsResult(map[string]any{"tabId": "t1", "active": true, "url": "https://owner.example/"}), nil
			}
			return agentBrowserTabsResult(
				map[string]any{"tabId": "t1", "active": false, "url": "https://owner.example/"},
				map[string]any{"tabId": "t2", "active": true, "url": "https://task.example/"},
			), nil
		case "agent_browser_tab_new":
			if stringArg(args, "url") != "https://task.example/" {
				t.Fatalf("unexpected new-tab args: %#v", args)
			}
			return agentBrowserToolResult{}, nil
		case "agent_browser_tab_switch":
			if stringArg(args, "tab") != "t2" {
				t.Fatalf("adapter switched an unverified tab: %#v", args)
			}
			return agentBrowserToolResult{}, nil
		default:
			t.Fatalf("unexpected tool %s", name)
			return agentBrowserToolResult{}, nil
		}
	}

	_, _, pages, err := entry.openURLLocked(context.Background(), "https://task.example/", "owner-a\x00default")
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 || firstStringValue(mapValue(pages[0]), "tab_id") != "t2" {
		t.Fatalf("owner tab crossed the ownership filter: %#v", pages)
	}
	if entry.ownedTabs["t2"] != "owner-a\x00default" {
		t.Fatalf("new tab ownership was not recorded: %#v", entry.ownedTabs)
	}
}

func TestHostCDPOpenFailsClosedOnAmbiguousTabDiff(t *testing.T) {
	adapter := &AgentBrowserAdapter{}
	entry := &agentBrowserSessionEntry{adapter: adapter, ownedTabs: map[string]string{}}
	listCall := 0
	adapter.callAgentTool = func(_ context.Context, _ *agentBrowserSession, name string, _ map[string]any) (agentBrowserToolResult, error) {
		if name == "agent_browser_tab_new" {
			return agentBrowserToolResult{}, nil
		}
		listCall++
		if listCall == 1 {
			return agentBrowserTabsResult(map[string]any{"tabId": "t1", "active": true}), nil
		}
		return agentBrowserTabsResult(
			map[string]any{"tabId": "t1"},
			map[string]any{"tabId": "t2"},
			map[string]any{"tabId": "t3", "active": true},
		), nil
	}

	_, _, _, err := entry.openURLLocked(context.Background(), "https://task.example/", "owner-a\x00default")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous tab diff error = %v", err)
	}
	if len(entry.ownedTabs) != 0 {
		t.Fatalf("ambiguous tabs received ownership: %#v", entry.ownedTabs)
	}
}

func TestHostCDPImplicitCurrentTabRejectsOwnerTab(t *testing.T) {
	adapter := &AgentBrowserAdapter{}
	entry := &agentBrowserSessionEntry{
		adapter:   adapter,
		ownedTabs: map[string]string{"t2": "owner-a\x00default"},
	}
	adapter.callAgentTool = func(_ context.Context, _ *agentBrowserSession, name string, _ map[string]any) (agentBrowserToolResult, error) {
		if name != "agent_browser_tab_list" {
			t.Fatalf("owner tab must be rejected before %s", name)
		}
		return agentBrowserTabsResult(
			map[string]any{"tabId": "t1", "active": true, "url": "https://owner.example/"},
			map[string]any{"tabId": "t2", "active": false, "url": "https://task.example/"},
		), nil
	}

	err := entry.selectRequestedTabLocked(context.Background(), map[string]any{}, false, "owner-a\x00default")
	if err == nil || !strings.Contains(err.Error(), "not owned") {
		t.Fatalf("implicit owner-tab selection error = %v", err)
	}
}

func TestHostCDPTabOwnershipIsScopeBound(t *testing.T) {
	entry := &agentBrowserSessionEntry{ownedTabs: map[string]string{"t2": "owner-a\x00default"}}
	if err := entry.requireOwnedTabLocked("t2", "owner-b\x00default"); err == nil {
		t.Fatal("another logical scope operated a SparkClaw-owned tab")
	}
	pages := entry.ownedPagesLocked([]any{
		map[string]any{"tab_id": "t1", "page_id": "page_1"},
		map[string]any{"tab_id": "t2", "page_id": "page_2"},
	}, "owner-a\x00default")
	if len(pages) != 1 || firstStringValue(mapValue(pages[0]), "page_id") != "page_2" {
		t.Fatalf("owner pages were exposed: %#v", pages)
	}
}
