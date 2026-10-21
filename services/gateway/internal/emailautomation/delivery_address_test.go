package emailautomation

import (
	"encoding/json"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestDiscoverySeparatesAccountIdentityFromDeliveryAddress(t *testing.T) {
	provider, _ := DefaultRegistry().Get(app.EmailProviderOutlook)
	base := map[string]any{"schema_version": 1, "provider": provider.ID, "status": "empty", "account_address": "outlook_0123456789abcdef@outlook.com", "candidates": []any{}, "coverage": map[string]any{"scope": "account", "scan_complete": true, "scanned_rows": 0, "unsupported_rows": 0, "limited": false}, "observed_at": "2026-10-21T08:00:00Z"}
	for _, tc := range []struct {
		name   string
		fields map[string]any
		valid  bool
	}{
		{"legacy identity only", nil, true},
		{"native delivery alias", map[string]any{"delivery_address": "owner@outlook.com", "account_aliases": []string{"owner@outlook.com", "second@outlook.com"}}, true},
		{"delivery without aliases", map[string]any{"delivery_address": "owner@outlook.com"}, false},
		{"aliases without delivery", map[string]any{"account_aliases": []string{"owner@outlook.com"}}, false},
		{"delivery outside aliases", map[string]any{"delivery_address": "foreign@outlook.com", "account_aliases": []string{"owner@outlook.com"}}, false},
		{"header injection", map[string]any{"delivery_address": "owner@outlook.com\r\nBcc:other@outlook.com", "account_aliases": []string{"owner@outlook.com"}}, false},
		{"duplicate aliases", map[string]any{"delivery_address": "owner@outlook.com", "account_aliases": []string{"owner@outlook.com", "OWNER@outlook.com"}}, false},
		{"empty metadata", map[string]any{"delivery_address": "", "account_aliases": []string{}}, false},
		{"null metadata", map[string]any{"delivery_address": nil, "account_aliases": nil}, false},
		{"undeclared metadata", map[string]any{"unverified_from": "owner@outlook.com"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire := map[string]any{}
			for key, value := range base {
				wire[key] = value
			}
			for key, value := range tc.fields {
				wire[key] = value
			}
			raw, _ := json.Marshal(wire)
			got, err := decodeDiscoveryResult(raw, provider, ReadRequest{}, maxScriptOutputBytes)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t: %v", tc.valid, err)
			}
			if tc.valid && got.AccountAddress != base["account_address"] {
				t.Fatal("delivery alias changed stable account identity")
			}
		})
	}
}
