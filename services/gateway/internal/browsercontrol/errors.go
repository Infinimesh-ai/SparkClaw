package browsercontrol

import "errors"

const (
	CodeInvalidRequest        = "browser_control_invalid_request"
	CodeNotConfigured         = "browser_control_not_configured"
	CodeControllerUnavailable = "browser_controller_unavailable"
	CodeExtensionUnavailable  = "browser_extension_unavailable"
	CodeExtensionRejected     = "browser_extension_rejected"
	CodeBusy                  = "browser_busy"
	CodeCredentialStale       = "browser_credential_stale"
	CodeControllerStale       = "browser_controller_stale"
	CodeSessionNotFound       = "browser_session_not_found"
	CodeSessionStale          = "browser_session_stale"
	CodePageStale             = "browser_page_stale"
	CodeOperationUnavailable  = "browser_operation_unavailable"
	CodeScriptUnavailable     = "browser_script_unavailable"
	CodeScriptTimeout         = "browser_script_timeout"
	CodeVaultUnavailable      = "browser_control_vault_unavailable"
)

type Error struct {
	Code      string
	Retryable bool
	cause     error
}

func (e *Error) Error() string {
	switch e.Code {
	case CodeInvalidRequest:
		return "browser control input is invalid"
	case CodeNotConfigured:
		return "browser control is not configured"
	case CodeExtensionUnavailable:
		return "browser extension is temporarily unavailable"
	case CodeExtensionRejected:
		return "browser extension rejected the credential"
	case CodeBusy:
		return "browser profile is busy"
	case CodeCredentialStale:
		return "browser credential generation is stale"
	case CodeControllerStale, CodeSessionNotFound, CodeSessionStale, CodePageStale:
		return "browser session is stale"
	case CodeOperationUnavailable:
		return "browser operation is unavailable"
	case CodeScriptUnavailable:
		return "browser provider script is unavailable"
	case CodeScriptTimeout:
		return "browser provider script timed out"
	case CodeVaultUnavailable:
		return "encrypted browser credential storage is unavailable"
	case CodeControllerUnavailable:
		return "browser controller is temporarily unavailable"
	default:
		return "browser control operation failed"
	}
}

func (e *Error) Unwrap() error { return e.cause }

func ErrorCode(err error) string {
	var target *Error
	if errors.As(err, &target) {
		return target.Code
	}
	return ""
}

func ErrorRetryable(err error) bool {
	var target *Error
	return errors.As(err, &target) && target.Retryable
}

func newError(code string, retryable bool, cause error) *Error {
	return &Error{Code: code, Retryable: retryable, cause: cause}
}
