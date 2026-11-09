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
