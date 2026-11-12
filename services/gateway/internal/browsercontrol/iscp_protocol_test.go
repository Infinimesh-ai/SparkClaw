package browsercontrol

import (
	"encoding/json"
	"os"
	"testing"
)

func TestControllerDigestMatchesDesktopUnicodeVectors(t *testing.T) {
	raw, err := os.ReadFile("../../../../tools/browser-controller/test/fixtures/client-wire-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Operation string         `json:"operation"`
		Body      map[string]any `json:"body"`
		Digest    string         `json:"digest"`
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, vector := range vectors {
		got, err := ControllerRequestDigest(vector.Operation, vector.Body)
		if err != nil || got != vector.Digest {
			t.Fatalf("digest %s: got %s want %s: %v", vector.Operation, got, vector.Digest, err)
		}
	}
}

func TestClientControllerOperationProjectionIsClosedAndCurrent(t *testing.T) {
	raw, err := os.ReadFile("../../../../tools/browser-controller/src/generic-operations.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(controllerOperationsJSON) {
		t.Fatal("run sync-controller-operations.mjs")
	}
	if mutation, err := ClientControllerOperation("page.fill", map[string]any{"ref": "f1e2", "text": "value", "submit": true}); err != nil || !mutation {
		t.Fatal(mutation, err)
	}
	for _, operation := range []string{"tabs.handoff", "ai_chat.export", "ai_platform.check", "script", "cdp"} {
		if _, err := ClientControllerOperation(operation, map[string]any{}); err == nil {
			t.Fatal("accepted", operation)
		}
	}
	if _, err := ClientControllerOperation("page.read", map[string]any{"script": "bad"}); err == nil {
		t.Fatal("accepted extra argument")
	}
	if _, err := ClientControllerOperation("page.wait", map[string]any{"duration_ms": 15001}); err == nil {
		t.Fatal("accepted excessive wait")
	}
}

func TestClientObservationRequiresBoundedCompleteSignals(t *testing.T) {
	valid := map[string]any{"timeout_ms": 15000, "quiet_period_ms": 500, "poll_interval_ms": 100,
		"signals": []string{"url", "ready_state", "rendered_content", "frames"}}
	if mutation, err := ClientControllerOperation("page.observe", valid); err != nil || mutation {
		t.Fatalf("valid read-only observation rejected: %v", err)
	}
	for _, patch := range []map[string]any{{"timeout_ms": 20001}, {"quiet_period_ms": 99}, {"poll_interval_ms": 501},
		{"minimum_samples": 1}, {"signals": []string{"url"}}, {"timeout_ms": "1000"}, {"script": "untrusted"},
		{"before_digest": "unbound"}, {"allow_no_change": "true"}} {
		args := map[string]any{}
		for key, value := range valid {
			args[key] = value
		}
		for key, value := range patch {
			args[key] = value
		}
		if _, err := ClientControllerOperation("page.observe", args); err == nil {
			t.Fatalf("invalid observation admitted: %#v", patch)
		}
	}
}
