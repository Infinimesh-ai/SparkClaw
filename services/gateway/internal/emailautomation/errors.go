package emailautomation

import (
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const (
	CodeNotConfigured         = "email_not_configured"
	CodeLoginRequired         = "email_login_required"
	CodeAccountAmbiguous      = "email_account_ambiguous"
	CodeProviderUnavailable   = "email_provider_unavailable"
	CodePageContractChanged   = "email_page_contract_changed"
	CodeInvalidInput          = "email_invalid_input"
	CodeDraftConflict         = "email_draft_conflict"
	CodeDraftVerifyFailed     = "email_draft_verification_failed"
	CodeSendControlUnverified = "email_send_control_unverified"
	CodeSendOutcomeUnknown    = "email_send_outcome_unknown"
	CodeScriptTimeout         = "email_script_timeout"
	CodeScriptInvalidOutput   = "email_script_invalid_output"
	CodeAdmissionStale        = "email_admission_stale"
)

type Error struct {
	Code      string
	Message   string
	Retryable bool
}

func (e *Error) Error() string {
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	return e.Code
}

func (e *Error) ToolErrorCode() app.ToolErrorCode {
	return app.ToolErrorCode(e.Code)
}

func codedError(code, message string) error {
	return &Error{Code: code, Message: message}
}

func ErrorCode(err error) string {
	if typed, ok := err.(*Error); ok {
		return typed.Code
	}
	return ""
}
