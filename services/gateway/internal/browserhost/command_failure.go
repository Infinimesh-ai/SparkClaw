package browserhost

import (
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

var nativeNavigationCode = regexp.MustCompile(`^ERR_[A-Z0-9_]{1,64}$`)

// A failed result never authorizes retry. Preserve a bounded diagnosis without
// changing the fence state or trusting arbitrary host error prose/URLs.
func failedHostCommand(operation string, response Message) error {
	var code app.ToolErrorCode
	detail := ""
	switch response.ErrorCode {
	case "navigation_failed":
		if operation != "navigate" {
			return ErrFence
		}
		code, detail = app.ToolErrorBrowserNavigationFailed, "browser navigation failed"
		var output struct {
			NativeErrorCode string `json:"native_error_code"`
		}
		if json.Unmarshal(response.Output, &output) == nil && nativeNavigationCode.MatchString(output.NativeErrorCode) {
			detail += " (" + output.NativeErrorCode + ")"
			if output.NativeErrorCode == "ERR_NAME_NOT_RESOLVED" {
				code = app.ToolErrorBrowserDNSResolutionFailed
			}
		}
	case "command_timeout":
		if writeOperation(operation) {
			return ErrFence
		}
		code, detail = app.ToolErrorBrowserCommandTimeout, "browser command timed out"
	case "browser_snapshot_stale":
		code, detail = app.ToolErrorSnapshotStale, "browser snapshot is stale"
	default:
		return ErrFence
	}
	return &app.CodedToolError{Code: code, Err: fmt.Errorf("%s: %w", detail, ErrFence)}
}
