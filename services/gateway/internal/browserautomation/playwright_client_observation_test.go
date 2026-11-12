package browserautomation

import (
	"strings"
	"testing"
)

func fakeClientObservation(pageID, url string, quiet int) map[string]any {
	return map[string]any{
		"schema_version": 1, "status": "stable", "reason_code": "browser_target_settled",
		"page_id": pageID, "page_ref": "page_" + strings.Repeat("a", 32), "document_version": strings.Repeat("d", 64),
		"url": url, "title": "Task page", "ready_state": "complete", "loading": false, "state_digest": strings.Repeat("b", 64),
		"frames_digest": strings.Repeat("c", 64), "frames_ready": true, "frame_count": 0,
		"signals":     []string{"url", "ready_state", "rendered_content", "frames"},
		"observed_ms": quiet + 10, "stable_for_ms": quiet, "quiet_period_ms": quiet,
		"stable_samples": 6, "observations": 6, "state_changed": true,
	}
}

func TestClientStableWaitUsesOneObservationAndOneDocumentBoundSnapshot(t *testing.T) {
	controller := newFakePlaywrightController()
	adapter := NewClientPlaywrightExtensionAdapter(playwrightAdapterTestConfig(), controller)
	t.Cleanup(func() { _ = adapter.Close() })
	if _, err := adapter.Call(t.Context(), "browser.open", map[string]any{"url": "https://example.com/"}); err != nil {
		t.Fatal(err)
	}
	session := controller.lastSession()
	start := len(session.calls)
	result, err := adapter.Call(t.Context(), "browser.wait", map[string]any{"mode": "stable_state", "page_id": "page_1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(session.calls) != start+1 || session.lastCall().Operation != "page.observe" {
		t.Fatalf("wait made extra remote reads: %#v", session.calls[start:])
	}
	if _, err := adapter.Call(t.Context(), "browser.snapshot", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if len(session.calls) != start+2 || session.lastCall().Operation != "page.snapshot" {
		t.Fatal("snapshot repeated remote content reads")
	}
	if session.lastCall().Arguments["document_version"] != mapValue(result.Output)["document_version"] {
		t.Fatal("snapshot lost document binding")
	}
	adapter.settledObservation["document_version"] = strings.Repeat("e", 64)
	if _, err := adapter.Call(t.Context(), "browser.snapshot", map[string]any{}); err == nil {
		t.Fatal("snapshot accepted a different document at the same URL")
	}
}

func TestClientStableEvidenceRejectsUnboundOrIncompleteResults(t *testing.T) {
	valid := fakeClientObservation("page_1", "https://example.com/", 500)
	if err := validateClientObservation(valid, "page_1", 500, 1000, "", false); err != nil {
		t.Fatal(err)
	}
	for _, patch := range []map[string]any{
		{"document_version": ""}, {"page_id": "page_2"}, {"page_ref": "page_unknown"}, {"frames_ready": false},
		{"signals": []string{"url"}}, {"observed_ms": 1001}, {"stable_for_ms": 499}, {"stable_samples": 1},
		{"state_changed": false}, {"ready_state": "loading"}, {"reason_code": ""}, {"frame_count": 64},
		{"loading": true}, {"url": ""}, {"observations": "6"}, {"frames_ready": "true"}, {"stable_samples": 6.5},
	} {
		o := cloneArgs(valid)
		for key, value := range patch {
			o[key] = value
		}
		if err := validateClientObservation(o, "page_1", 500, 1000, "", false); err == nil {
			t.Fatalf("accepted invalid evidence: %#v", patch)
		}
	}
	if err := validateClientObservation(map[string]any{"stable": true}, "page_1", 500, 1000, "", false); err == nil {
		t.Fatal("accepted an unsupported stable assertion")
	}
}
