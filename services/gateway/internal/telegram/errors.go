package telegram

import (
	"errors"
	"net"
	"net/http"
)

const (
	CodeActivationInvalid     = "activation_invalid"
	CodeAttachmentTooLarge    = "attachment_too_large"
	CodeAttachmentUnsupported = "attachment_unsupported"
	CodeBindingUnavailable    = "binding_unavailable"
	CodeQueueFull             = "queue_full"
	CodeRetryExhausted        = "retry_exhausted"
	CodeVoiceUnavailable      = "voice_unavailable"
)

type ConnectorError struct {
	Code      string
	Retryable bool
	cause     error
}

func NewConnectorError(code string, retryable bool, cause error) *ConnectorError {
	return &ConnectorError{Code: code, Retryable: retryable, cause: cause}
}

func (e *ConnectorError) Error() string {
	if e == nil {
		return "Telegram connector failed"
	}
	switch e.Code {
	case CodeActivationInvalid:
		return "Telegram activation could not be verified"
	case CodeAttachmentTooLarge:
		return "Telegram attachment exceeds the configured limit"
	case CodeAttachmentUnsupported:
		return "Telegram attachment type is not supported"
	case CodeBindingUnavailable:
		return "Telegram binding is unavailable"
	case CodeQueueFull:
		return "Telegram connector is busy"
	case CodeRetryExhausted:
		return "Telegram delivery retry budget was exhausted"
	case CodeVoiceUnavailable:
		return "Telegram voice transcription is unavailable"
	default:
		return "Telegram connector failed"
	}
}

func (e *ConnectorError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func connectorErrorCode(err error) string {
	var connectorErr *ConnectorError
	if errors.As(err, &connectorErr) {
		return connectorErr.Code
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return "telegram_api_failed"
	}
	return "telegram_processing_failed"
}

func isRetryable(err error) bool {
	var connectorErr *ConnectorError
	if errors.As(err, &connectorErr) {
		return connectorErr.Retryable
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.RetryAfter > 0 || apiErr.Code == http.StatusTooManyRequests || apiErr.Code >= 500
	}
	var netErr net.Error
	return errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary())
}
