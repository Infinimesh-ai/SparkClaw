package browserautomation

import (
	"strings"
	"testing"
)

func TestPlaywrightSnapshotPreservesVisibleTextAndSelectedOptions(t *testing.T) {
	controller := newFakePlaywrightController()
	adapter := NewClientPlaywrightExtensionAdapter(playwrightAdapterTestConfig(), controller)
	t.Cleanup(func() { _ = adapter.Close() })
	if _, err := adapter.Call(t.Context(), "browser.open", map[string]any{"url": "https://example.com/form"}); err != nil {
		t.Fatal(err)
	}
	session := controller.lastSession()
	session.snapshot = []any{
		map[string]any{"role": "combobox", "name": "Choice", "ref": "e1", "children": []any{
			map[string]any{"role": "option", "name": "One"}, map[string]any{"role": "option", "name": "Two", "selected": true},
		}},
		map[string]any{"role": "paragraph", "ref": "e2", "text": "Dynamic ready"},
		map[string]any{"role": "button", "name": "Add dynamic field", "ref": "e3", "disabled": true},
	}
	result, err := adapter.Call(t.Context(), "browser.snapshot", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := mapValue(mapValue(result.Output)["snapshot"])
	controls := map[string]map[string]any{}
	for _, raw := range snapshot["controls"].([]any) {
		control := mapValue(raw)
		controls[firstStringValue(control, "short_ref")] = control
	}
	if controls["e2"]["text"] != "Dynamic ready" {
		t.Fatal("rendered paragraph evidence was dropped")
	}
	options, ok := controls["e1"]["options"].([]any)
	if !ok || len(options) != 2 || mapValue(options[1])["name"] != "Two" || mapValue(options[1])["selected"] != true {
		t.Fatalf("selected option evidence was dropped: %#v", controls["e1"])
	}
	if controls["e3"]["enabled"] != false {
		t.Fatal("disabled state was dropped")
	}
	for _, ref := range snapshot["action_refs"].([]string) {
		if ref == controls["e3"]["ref"] {
			t.Fatal("disabled control remains actionable")
		}
	}
}

func TestPlaywrightStableWaitObservesFrameEffectsWithoutRefChurn(t *testing.T) {
	for _, changed := range []bool{true, false} {
		t.Run(map[bool]string{true: "frame_effect", false: "refs_only"}[changed], func(t *testing.T) {
			controller := newFakePlaywrightController()
			adapter := NewClientPlaywrightExtensionAdapter(playwrightAdapterTestConfig(), controller)
			t.Cleanup(func() { _ = adapter.Close() })
			if _, err := adapter.Call(t.Context(), "browser.open", map[string]any{"url": "https://example.com/frame"}); err != nil {
				t.Fatal(err)
			}
			session := controller.lastSession()
			frame := func(ref, name string) []any {
				return []any{map[string]any{"role": "iframe", "ref": "e1", "children": []any{map[string]any{"role": "button", "ref": ref, "name": name}}}}
			}
			session.snapshot = frame("f1e1", "Apply frame action")
			before, err := adapter.Call(t.Context(), "browser.snapshot", map[string]any{})
			if err != nil {
				t.Fatal(err)
			}
			label := "Apply frame action"
			if changed {
				label = "Frame applied"
			}
			session.snapshot = frame("f1e99", label)
			result, err := adapter.Call(t.Context(), "browser.wait", map[string]any{"mode": "stable_state", "page_id": "page_1", "before_digest": mapValue(before.Output)["content_digest"], "allow_no_change": false, "timeout_ms": 500, "quiet_period_ms": 100, "poll_interval_ms": 25})
			if changed {
				if err != nil {
					t.Fatalf("actual frame effect did not settle: %v", err)
				}
				if mapValue(result.Output)["state_changed"] != true {
					t.Fatal("frame effect was not identified")
				}
			} else if err == nil || !strings.Contains(err.Error(), "browser_settle_timeout") {
				t.Fatalf("opaque ref changes are not page effects: %v", err)
			}
		})
	}
}
