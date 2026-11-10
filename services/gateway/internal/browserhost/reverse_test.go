package browserhost

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStableWaitValidationIsClosed(t *testing.T) {
	good := map[string]any{"mode": "stable_state", "expected_url": "https://example.test/", "before_digest": strings.Repeat("a", 64), "allow_no_change": true, "require_ready_state": true, "timeout_ms": 15000}
	if err := validateOperation("wait", good); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []map[string]any{
		{"mode": "sleep"},
		{"mode": "stable_state", "milliseconds": 10},
		{"mode": "stable_state", "timeout_ms": 16000},
		{"mode": "stable_state", "before_digest": "short"},
		{"mode": "stable_state", "expected_url": "file:///etc/passwd"},
		{"mode": "stable_state", "allow_no_change": "yes"},
		{"milliseconds": 10, "before_digest": strings.Repeat("a", 64)},
	} {
		if validateOperation("wait", bad) == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	if validateOperation("read", map[string]any{"max_chars": MaxReadChars + 1}) == nil {
		t.Fatal("read window exceeds the host window")
	}
}

func TestReverseBindingValuesMatchTheDesktopVector(t *testing.T) {
	session := "11111111-1111-4111-8111-111111111111"
	if got := reverseHostID(testIdentity, session); got != "iscp_7b0b83b55eedc9a5146da1bce2231528" {
		t.Fatal(got)
	}
	if got := reverseAuthorizationDigest(session, 7); got != "ceb65fe5cc84a5429d48508b563c2793e22fe712b6e1a5501c6221041d81e61d" {
		t.Fatal(got)
	}
	raw, _ := json.Marshal(Binding{})
	if !strings.Contains(string(raw), `"lease_expires_at":"0001-01-01T00:00:00Z"`) {
		t.Fatal(string(raw))
	}
}
