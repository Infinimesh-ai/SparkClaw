package browserhost

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestReverseFailureDiagnosisPreservesFenceSemantics(t *testing.T) {
	for _, test := range []struct {
		name, operation, status, code, native string
		want                                  app.ToolErrorCode
	}{
		{"dns", "navigate", "failed", "navigation_failed", "ERR_NAME_NOT_RESOLVED", app.ToolErrorBrowserDNSResolutionFailed},
		{"tls", "navigate", "failed", "navigation_failed", "ERR_CERT_AUTHORITY_INVALID", app.ToolErrorBrowserNavigationFailed},
		{"deadline", "navigate", "failed", "command_timeout", "", app.ToolErrorBrowserCommandTimeout},
		{"read_deadline", "read", "failed", "command_timeout", "", app.ToolErrorBrowserCommandTimeout},
		{"stale_snapshot", "click", "failed", "browser_snapshot_stale", "", app.ToolErrorSnapshotStale},
		{"unsafe_native_code", "navigate", "failed", "navigation_failed", "ERR_FAILED https://private.test/?token=secret", app.ToolErrorBrowserNavigationFailed},
		{"unknown_error", "navigate", "failed", "other", "", ""},
		{"wrong_operation", "read", "failed", "navigation_failed", "ERR_NAME_NOT_RESOLVED", ""},
		{"unknown_write", "click", "unknown", "navigation_failed", "ERR_NAME_NOT_RESOLVED", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			desktop := newFakeDesktop("11111111-1111-4111-8111-111111111111")
			broker, _ := reverseBroker(t, desktop)
			binding, err := broker.Acquire(t.Context(), reverseScope)
			if err != nil {
				t.Fatal(err)
			}
			desktop.answer = func(command Command) (json.RawMessage, error) {
				output, _ := json.Marshal(map[string]any{"native_error_code": test.native})
				return json.Marshal(Message{SchemaVersion: 1, Type: "result", CommandID: command.CommandID, Binding: &command.Binding, Status: test.status, ErrorCode: test.code, Output: output})
			}
			args := map[string]any{}
			if test.operation == "navigate" {
				args["url"] = "https://example.test/"
			}
			if test.operation == "click" {
				args["ref"], args["snapshot_id"] = "snapshot:e1", "snapshot"
			}
			_, err = broker.Dispatch(t.Context(), binding, "cmd_failure", test.operation, args)
			if test.status == "unknown" {
				if !errors.Is(err, ErrUnknown) || broker.fences["cmd_failure"].State != "unknown" {
					t.Fatalf("uncertain write lost: %v", err)
				}
			} else if !errors.Is(err, ErrFence) || broker.fences["cmd_failure"].State != "failed" {
				t.Fatalf("failure bypassed fence: %v", err)
			}
			if app.ToolErrorCodeFrom(err) != test.want || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "private.test") {
				t.Fatalf("diagnosis=%v code=%s want=%s", err, app.ToolErrorCodeFrom(err), test.want)
			}
			if test.name == "dns" && !strings.Contains(err.Error(), "ERR_NAME_NOT_RESOLVED") {
				t.Fatal("native diagnosis lost", err)
			}
			if desktop.count() != 2 {
				t.Fatal("failure caused replay", desktop.count())
			}
		})
	}
}
