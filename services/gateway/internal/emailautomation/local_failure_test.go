package emailautomation

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestScriptLocalOperationalFailureDomain(t *testing.T) {
	provider, _ := DefaultRegistry().Get("gmail")
	for _, code := range []string{"email_batch_limit", "email_local_io", "email_source_conflict", "email_source_recovery_pending", "email_network_read_failed", "email_capture_limit"} {
		raw, _ := json.Marshal(map[string]any{"schema_version": 1, "status": "error", "provider": "gmail", "code": code})
		err := playwrightScriptFailure(provider, raw)
		want := code != "email_network_read_failed" && code != "email_capture_limit"
		if LocalOperationalFailure(fmt.Errorf("wrapped: %w", err)) != want {
			t.Errorf("%s: local failure domain lost", code)
		}
	}
}
