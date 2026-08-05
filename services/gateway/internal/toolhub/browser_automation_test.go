package toolhub

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestBrowserAutomationToolsRegisterOnlyWhenEnabled(t *testing.T) {
	cfg := config.Default()
	disabled := New(cfg, store.NewMemoryStore())
	if _, ok := disabled.Definition("browser.open"); ok {
		t.Fatal("browser automation tools should not register when disabled")
	}

	cfg.Tools.BrowserAutomation.Enabled = true
	enabled := New(cfg, store.NewMemoryStore())
	for _, name := range []string{
		"browser.status",
		"browser.list_tabs",
		"browser.open",
		"browser.focus",
		"browser.close",
		"browser.navigate",
		"browser.snapshot",
		"browser.screenshot",
		"browser.visual_inspect",
		"browser.wait",
		"browser.click",
		"browser.validate_transition",
		"browser.assess_goal",
		"browser.type",
		"browser.select",
	} {
		if _, ok := enabled.Definition(name); !ok {
			t.Fatalf("%s should register when browser automation is enabled", name)
		}
	}
	if _, ok := enabled.Definition("browser.verify"); ok {
		t.Fatal("retired browser.verify must not register")
	}
}

func TestBrowserAutomationToolSchemas(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.BrowserAutomation.Enabled = true
	hub := New(cfg, store.NewMemoryStore())

	valid := map[string]map[string]any{
		"browser.open":     {"url": "https://example.com"},
		"browser.focus":    {"page_id": "page_1"},
		"browser.close":    {"page_id": "page_1"},
		"browser.navigate": {"url": "https://example.com/settings"},
		"browser.wait":     {"text": "Loaded"},
		"browser.click":    {"uid": "button_1"},
		"browser.type": {
			"uid": "field_1", "page_id": "page_1", "snapshot_id": "snapshot_1",
			"session_generation": 1, "page_generation": 1, "text": "hello",
		},
		"browser.select": {
			"uid": "select_1", "page_id": "page_1", "snapshot_id": "snapshot_1",
			"session_generation": 1, "page_generation": 1, "value": "A",
		},
		"browser.visual_inspect": {
			"page_id": "page_1", "snapshot_id": "snapshot_1", "snapshot_digest": "digest_1",
			"session_generation": 1, "page_generation": 1, "reason": "owner_requested",
		},
		"browser.validate_transition": {
			"before_snapshot_id": "snapshot_1", "after_snapshot_id": "snapshot_2", "element_ref": "element_1",
		},
		"browser.assess_goal": {"snapshot_id": "snapshot_2", "verdict": "satisfied", "evidence_refs": []string{"element_1"}, "reason": "target state changed"},
	}
	for name, args := range valid {
		if err := hub.Validate(name, args); err != nil {
			t.Fatalf("%s should accept valid args: %v", name, err)
		}
	}
	if err := hub.Validate("browser.click", map[string]any{}); err == nil {
		t.Fatal("browser.click should require uid")
	}
	if err := hub.Validate("browser.focus", map[string]any{"page_id": 1}); err != nil {
		t.Fatalf("browser.focus should accept numeric page ids from agent-browser output: %v", err)
	}
	click, _ := hub.Definition("browser.click")
	if click.RequiresApproval {
		t.Fatal("browser.interaction clicks must not require approval")
	}
	closeTab, _ := hub.Definition("browser.close")
	if closeTab.RequiresApproval || closeTab.Risk != app.RiskDraft {
		t.Fatalf("workflow-owned tab cleanup should remain bounded and approval-free: %#v", closeTab)
	}
	for _, name := range []string{"browser.type", "browser.select"} {
		definition, ok := hub.Definition(name)
		if !ok || !definition.RequiresApproval || definition.Risk != app.RiskDraft {
			t.Fatalf("%s must require an independent draft approval: %#v", name, definition)
		}
	}
}

func TestBrowserListTabsPreservesEmptyPagesArray(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.BrowserAutomation.Enabled = true
	hub := New(cfg, store.NewMemoryStore()).WithBrowserAutomationAdapter(fakePageReadAdapter{})

	result, err := hub.Execute(context.Background(), "browser.list_tabs", map[string]any{}, "", "run")
	if err != nil {
		t.Fatal(err)
	}
	out, ok := result.Output.(browserautomation.Result)
	if !ok || out.Pages == nil || len(out.Pages) != 0 {
		t.Fatalf("zero-tab result must preserve pages as an empty array: %#v", result.Output)
	}
}

func TestBrowserOpenPassesCollaborativeMetadata(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.BrowserAutomation.Enabled = true
	callArgs := map[string]any{}
	st := store.NewMemoryStore()
	session := st.CreateSessionWithScope("browser owner", "owner-a", "", "webchat", false)
	hub := New(cfg, st).WithBrowserAutomationAdapter(fakePageReadAdapter{callArgs: &callArgs})

	result, err := hub.Execute(context.Background(), "browser.open", map[string]any{
		"url":             "https://example.com",
		"browser_mode":    "collaborative",
		"presentation":    "visible",
		"surface_visible": true,
	}, session.ID, "run")
	if err != nil {
		t.Fatal(err)
	}
	if callArgs["browser_mode"] != "collaborative" || callArgs["presentation"] != "visible" || callArgs["surface_visible"] != true {
		t.Fatalf("browser.open should pass collaborative visible metadata to adapter: %#v", callArgs)
	}
	if callArgs["owner_id"] != "owner-a" || callArgs["browser_profile_id"] != "default" {
		t.Fatalf("browser.open should bind the shared profile to session owner and logical profile: %#v", callArgs)
	}
	out, ok := result.Output.(browserautomation.Result)
	if !ok {
		t.Fatalf("expected browser automation result, got %#v", result.Output)
	}
	if out.BrowserMode != "collaborative" || out.Presentation != "visible" || !out.SurfaceVisible {
		t.Fatalf("browser.open output should include collaborative metadata: %#v", out)
	}
}

func TestBrowserStatusPassesPresentationAndProfileIdentity(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.BrowserAutomation.Enabled = true
	healthArgs := map[string]any{}
	st := store.NewMemoryStore()
	session := st.CreateSessionWithScope("browser owner", "owner-health", "", "webchat", false)
	hub := New(cfg, st).WithBrowserAutomationAdapter(fakePageReadAdapter{healthArgs: &healthArgs})

	result, err := hub.Execute(context.Background(), "browser.status", map[string]any{
		"browser_mode":    "collaborative",
		"presentation":    "visible",
		"surface_visible": true,
	}, session.ID, "run")
	if err != nil {
		t.Fatal(err)
	}
	if healthArgs["browser_mode"] != "collaborative" || healthArgs["presentation"] != "visible" || healthArgs["surface_visible"] != true {
		t.Fatalf("browser.status should pass collaborative visible metadata to adapter: %#v", healthArgs)
	}
	if healthArgs["owner_id"] != "owner-health" || healthArgs["browser_profile_id"] != "default" {
		t.Fatalf("browser.status should bind the health session to owner and logical profile: %#v", healthArgs)
	}
	out, ok := result.Output.(browserautomation.Result)
	if !ok || out.BrowserMode != "collaborative" || out.Presentation != "visible" || !out.SurfaceVisible {
		t.Fatalf("browser.status output should preserve requested presentation: %#v", result.Output)
	}
}

func TestBrowserOpenDefaultsToAutonomousHidden(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.BrowserAutomation.Enabled = true
	callArgs := map[string]any{}
	hub := New(cfg, store.NewMemoryStore()).WithBrowserAutomationAdapter(fakePageReadAdapter{callArgs: &callArgs})

	if _, err := hub.Execute(context.Background(), "browser.open", map[string]any{"url": "https://example.com"}, "", "run"); err != nil {
		t.Fatal(err)
	}
	if callArgs["browser_mode"] != "autonomous" || callArgs["presentation"] != "hidden" || callArgs["surface_visible"] != false {
		t.Fatalf("browser.open should stay hidden unless visibility is explicit: %#v", callArgs)
	}
}

func TestAttachBrowserScreenshotSavesWorkspaceFile(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	hub := New(cfg, store.NewMemoryStore())
	result := browserautomation.Result{
		Tool: "browser.screenshot",
		Output: map[string]any{
			"content": []any{
				map[string]any{
					"type":     "image",
					"mimeType": "image/png",
					"data":     "iVBORw0KGgo=",
				},
			},
		},
	}

	hub.attachBrowserScreenshot(context.Background(), &result)

	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected output map, got %#v", result.Output)
	}
	path := strings.TrimSpace(browserAutomationStringValue(output["screenshot_path"]))
	if path == "" {
		t.Fatalf("expected screenshot_path in output: %#v", output)
	}
	if !strings.HasPrefix(path, filepath.Join(root, ".sparkclaw", "screenshots")) {
		t.Fatalf("screenshot should be saved under workspace, got %q", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected screenshot file to exist: %v", err)
	}
	if markdown := browserAutomationStringValue(output["screenshot_markdown"]); !strings.Contains(markdown, path) {
		t.Fatalf("markdown should include saved path, got %q", markdown)
	}
}
