package emailautomation

import (
	"errors"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// Error is a bounded email automation failure. Code is drawn from the
// app.ToolErrorEmail* vocabulary so tool calls, /api/email responses, and
// Workflow outcome adapters all classify the same failure the same way.
type Error struct {
	Code    app.ToolErrorCode
	Message string
}

func (e *Error) Error() string {
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	return string(e.Code)
}

func (e *Error) ToolErrorCode() app.ToolErrorCode { return e.Code }

// ErrorCode and Retryable satisfy the interfaces the gateway's shared error
// writer probes, so /api/email responses carry the same {code, retryable}
// shape as the browser and integration endpoints.
func (e *Error) ErrorCode() string { return string(e.Code) }

func (e *Error) Retryable() bool { return retryableEmailCode(e.Code) }

func codedError(code app.ToolErrorCode, message string) error {
	return &Error{Code: code, Message: message}
}

func retryableEmailCode(code app.ToolErrorCode) bool {
	switch code {
	case app.ToolErrorEmailProviderUnavailable, app.ToolErrorEmailScriptTimeout:
		return true
	default:
		return false
	}
}

// ErrorCode returns the bounded code of an email automation error, or "" for
// any other error.
func ErrorCode(err error) app.ToolErrorCode {
	var typed *Error
	if errors.As(err, &typed) {
		return typed.Code
	}
	return ""
}
