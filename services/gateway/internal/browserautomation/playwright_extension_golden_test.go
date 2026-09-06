package browserautomation

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// playwrightGoldenPath points at the fixture recorded from the pinned
// @playwright/mcp package by tools/browser-controller/test/fixtures/record-playwright-golden.mjs.
// The Node controller forwards the `snapshot` array from that payload verbatim,
// so this file is the contract both parsers are held to.
const playwrightGoldenPath = "../../../../tools/browser-controller/test/fixtures/playwright-golden.json"

type playwrightGolden struct {
	Recorded struct {
		PlaywrightMCP string `json:"playwright_mcp"`
	} `json:"recorded"`
	MCP map[string]json.RawMessage `json:"mcp"`
}

type playwrightGoldenResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func loadPlaywrightGolden(t *testing.T) playwrightGolden {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(playwrightGoldenPath))
	if err != nil {
		t.Fatalf("read playwright golden: %v", err)
	}
	var golden playwrightGolden
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatalf("decode playwright golden: %v", err)
	}
	return golden
}

func (g playwrightGolden) payload(t *testing.T, key string) map[string]any {
	t.Helper()
	raw, ok := g.MCP[key]
	if !ok {
		t.Fatalf("golden mcp payload %q missing", key)
	}
	var entry playwrightGoldenResult
	if err := json.Unmarshal(raw, &entry); err != nil || len(entry.Content) == 0 || entry.Content[0].Type != "text" {
		t.Fatalf("golden mcp payload %q is not a text tool result: %v", key, err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(entry.Content[0].Text), &payload); err != nil {
		t.Fatalf("decode golden payload %q: %v", key, err)
	}
	return payload
}

func TestPlaywrightSnapshotRefsFromRecordedSnapshot(t *testing.T) {
	golden := loadPlaywrightGolden(t)
	if golden.Recorded.PlaywrightMCP != "0.0.80" {
		t.Fatalf("golden was recorded from @playwright/mcp %q; update the pinned expectations", golden.Recorded.PlaywrightMCP)
	}

	full := golden.payload(t, "snapshot")
	if _, hasPage := full["page"]; hasPage {
		t.Fatalf("json snapshot payload unexpectedly carries a page block: %#v", full)
	}
	refs := playwrightSnapshotRefs(full["snapshot"])
	want := map[string][3]string{
		"e2": {"main", "", "false"},
		"e3": {"heading", "playwright-extension-adapter-live-fixture", "false"},
		"e4": {"generic", "", "false"},
		"e5": {"textbox", "Name", "true"},
		"e6": {"generic", "", "false"},
		"e7": {"combobox", "Country", "true"},
		"e8": {"button", "Advance", "true"},
		"e9": {"status", "", "false"},
	}
	if len(refs) != len(want) {
		t.Fatalf("refs = %#v, want %d entries", refs, len(want))
	}
	for ref, expected := range want {
		values := mapValue(refs[ref])
		if values == nil {
			t.Fatalf("ref %s missing from %#v", ref, refs)
		}
		got := [3]string{firstStringValue(values, "role"), firstStringValue(values, "name"), map[bool]string{true: "true", false: "false"}[boolValue(values["clickable"])]}
		if got != expected {
			t.Fatalf("ref %s = %v, want %v", ref, got, expected)
		}
	}

	depth1 := playwrightSnapshotRefs(golden.payload(t, "snapshot_depth1")["snapshot"])
	for _, pruned := range []string{"e5", "e7"} {
		if _, ok := depth1[pruned]; ok {
			t.Fatalf("depth-limited snapshot still exposes %s: %#v", pruned, depth1)
		}
	}
	if _, ok := depth1["e8"]; !ok {
		t.Fatalf("depth-limited snapshot lost the top-level button: %#v", depth1)
	}
	if blank := playwrightSnapshotRefs(golden.payload(t, "snapshot_about_blank")["snapshot"]); len(blank) != 0 {
		t.Fatalf("about:blank snapshot produced refs: %#v", blank)
	}
}

func TestPlaywrightExtensionAdapterSnapshotFromRecordedPayload(t *testing.T) {
	golden := loadPlaywrightGolden(t)
	controller := newFakePlaywrightController()
	adapter := NewPlaywrightExtensionAdapter(playwrightAdapterTestConfig(), controller).(*PlaywrightExtensionAdapter)
	ctx := context.Background()
	baseArgs := map[string]any{"owner_id": "owner-golden"}

	opened, err := adapter.Call(ctx, "browser.open", mergeArgs(baseArgs, map[string]any{"url": "https://example.com/golden"}))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	pageID := selectedPageID(mapValue(opened.Output))
	session := controller.lastSession()
	session.mu.Lock()
	session.snapshot, _ = golden.payload(t, "snapshot")["snapshot"].([]any)
	session.mu.Unlock()

	snapshot := takePlaywrightTestSnapshot(t, adapter, mergeArgs(baseArgs, map[string]any{"page_id": pageID}))
	for _, name := range []string{"Name", "Country", "Advance"} {
		if playwrightTestControlRef(t, snapshot, name) == "" {
			t.Fatalf("control %q has no ref in %#v", name, snapshot)
		}
	}
	controls, _ := snapshot["controls"].([]any)
	for _, raw := range controls {
		control := mapValue(raw)
		if name := firstStringValue(control, "accessible_name"); name == "China" || name == "United States" {
			t.Fatalf("option without a Playwright ref surfaced as a control: %#v", control)
		}
	}
	if firstStringValue(snapshot, "url") != "https://example.com/golden" {
		t.Fatalf("snapshot url must come from page.read when the snapshot payload carries none: %#v", snapshot)
	}
	buttonRef := playwrightTestControlRef(t, snapshot, "Advance")
	if _, err := adapter.Call(ctx, "browser.click", mergeArgs(baseArgs, map[string]any{"page_id": pageID, "ref": buttonRef})); err != nil {
		t.Fatalf("click recorded button ref: %v", err)
	}
	assertLastPlaywrightCall(t, session, "page.click", map[string]any{"page_id": pageID, "ref": "e8"})
}
