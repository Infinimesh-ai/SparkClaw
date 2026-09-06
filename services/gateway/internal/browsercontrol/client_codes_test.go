package browsercontrol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The Controller's error-code table is the shared contract; mapControllerFailure
// must agree with it entry for entry.
const controllerErrorCodesPath = "../../../../tools/browser-controller/src/controller-error-codes.json"

func TestControllerErrorCodesMatchTheSharedTable(t *testing.T) {
	raw, err := os.ReadFile(filepath.FromSlash(controllerErrorCodesPath))
	if err != nil {
		t.Fatalf("read shared controller error codes: %v", err)
	}
	var table struct {
		SchemaVersion int `json:"schemaVersion"`
		Codes         map[string]struct {
			GatewayCode      string `json:"gateway_code"`
			GatewayRetryable bool   `json:"gateway_retryable"`
		} `json:"codes"`
	}
	if err := json.Unmarshal(raw, &table); err != nil || table.SchemaVersion != 1 || len(table.Codes) == 0 {
		t.Fatalf("shared controller error codes are invalid: err=%v table=%#v", err, table)
	}
	for code, expected := range table.Codes {
		body := []byte(`{"code":"` + code + `","retryable":false}`)
		mapped := mapControllerFailure(400, body)
		if ErrorCode(mapped) != expected.GatewayCode || ErrorRetryable(mapped) != expected.GatewayRetryable {
			t.Fatalf("controller code %s mapped to %s (retryable=%t), table says %s (retryable=%t)",
				code, ErrorCode(mapped), ErrorRetryable(mapped), expected.GatewayCode, expected.GatewayRetryable)
		}
	}
	for code := range controllerCodeProjections {
		if _, ok := table.Codes[code]; !ok {
			t.Fatalf("Go projects controller code %s that the shared table does not declare", code)
		}
	}
	unknown := mapControllerFailure(503, []byte(`{"code":"browser_future_code","retryable":true}`))
	if ErrorCode(unknown) != CodeControllerUnavailable || !ErrorRetryable(unknown) {
		t.Fatalf("unknown controller code must degrade to a retryable controller_unavailable: %v", unknown)
	}
}
