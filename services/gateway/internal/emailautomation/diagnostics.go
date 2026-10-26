package emailautomation

import (
	"context"
	"errors"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
)

// FailureDiagnostic is internal, content-free attribution, independent of the
// public error and effect certainty. It never carries an error message.
type FailureDiagnostic struct{ Code, Stage, Source string }

func DiagnosticCode(err error) string {
	switch code := ErrorCode(err); code {
	case app.ToolErrorEmailNotConfigured, app.ToolErrorEmailLoginRequired, app.ToolErrorEmailAccountAmbiguous, app.ToolErrorEmailProviderUnavailable, app.ToolErrorEmailBrowserBusy, app.ToolErrorEmailBrowserCleanupPending, app.ToolErrorEmailPageContractChanged, app.ToolErrorEmailInvalidInput, app.ToolErrorEmailDraftConflict, app.ToolErrorEmailDraftVerificationFailed, app.ToolErrorEmailSendControlUnverified, app.ToolErrorEmailSendOutcomeUnknown, app.ToolErrorEmailSendNotDispatched, app.ToolErrorEmailScriptTimeout, app.ToolErrorEmailScriptInvalidOutput, app.ToolErrorEmailAdmissionStale:
		return string(code)
	default:
		return ""
	}
}

func (e *Error) DiagnosticCause() (code, stage, source string) {
	if e.diagnostic.Code != "" {
		return e.diagnostic.Code, e.diagnostic.Stage, e.diagnostic.Source
	}
	code = DiagnosticCode(e)
	if code == "" {
		code = "unclassified"
	}
	return code, "operation", "automation"
}

func FailureDetails(err error) FailureDiagnostic {
	if err == nil {
		return FailureDiagnostic{}
	}
	var typed *Error
	if errors.As(err, &typed) {
		code, stage, source := typed.DiagnosticCause()
		return FailureDiagnostic{code, stage, source}
	}
	var controller *browsercontrol.Error
	if errors.As(err, &controller) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return controllerFailureDetails(err)
	}
	return FailureDiagnostic{Code: "unclassified", Stage: "unknown", Source: "unknown"}
}

func controllerFailureDetails(err error) FailureDiagnostic {
	d := FailureDiagnostic{Code: "unclassified", Stage: "controller_request", Source: "browser_control"}
	switch {
	case errors.Is(err, context.Canceled):
		d.Code, d.Source = "canceled", "context"
		return d
	case errors.Is(err, context.DeadlineExceeded):
		d.Code, d.Source = "timeout", "context"
		return d
	}
	switch code := browsercontrol.ErrorCode(err); code {
	case browsercontrol.CodeInvalidRequest, browsercontrol.CodeNotConfigured, browsercontrol.CodeControllerUnavailable, browsercontrol.CodeExtensionUnavailable, browsercontrol.CodeExtensionRejected, browsercontrol.CodeBusy, browsercontrol.CodeCleanupPending, browsercontrol.CodeCredentialStale, browsercontrol.CodeControllerStale, browsercontrol.CodeSessionNotFound, browsercontrol.CodeSessionStale, browsercontrol.CodePageStale, browsercontrol.CodeOperationUnavailable, browsercontrol.CodeScriptUnavailable, browsercontrol.CodeScriptTimeout, browsercontrol.CodeVaultUnavailable:
		d.Code = code
	}
	return d
}

func scriptFailureDetails(code string) FailureDiagnostic {
	d := FailureDiagnostic{Code: "unclassified", Stage: "script_result", Source: "application"}
	_, script := scriptErrorCodes[code]
	_, host := hostRejectionCodes[code]
	if script || host || DiagnosticCode(&Error{Code: app.ToolErrorCode(code)}) != "" {
		d.Code = code
	}
	return d
}
