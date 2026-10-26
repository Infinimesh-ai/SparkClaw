package emailmanagement

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"regexp"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// jobRenewalFailure carries the typed storage failure through cancellation.
// It is used only for diagnostics: dispatch's error still controls completion.
type jobRenewalFailure struct{ cause error }

func (e *jobRenewalFailure) Error() string { return "email_lease_renewal_failed" }
func (e *jobRenewalFailure) Unwrap() error { return e.cause }

var jobExecutionComplete = errors.New("email_job_execution_complete")

type jobFailureDiagnostic struct{ code, source, stage string }

var diagnosticJobID = regexp.MustCompile(`^[0-9a-f]{64}$`)

func jobStage(kind string) string {
	switch kind {
	case app.EmailJobDiscover:
		return "collection"
	case app.EmailJobParse:
		return "parse"
	case app.EmailJobPresentation:
		return "presentation"
	case app.EmailJobMessageSummary, app.EmailJobClassification, app.EmailJobAssignment, app.EmailJobRelationshipCheck, app.EmailJobConversationSummary:
		return "analysis"
	default:
		return "dispatch"
	}
}

func jobOperation(kind string) string {
	switch kind {
	case app.EmailJobDiscover, app.EmailJobParse, app.EmailJobPresentation, app.EmailJobMessageSummary, app.EmailJobClassification, app.EmailJobAssignment, app.EmailJobRelationshipCheck, app.EmailJobConversationSummary:
		return kind
	default:
		return "unknown"
	}
}

func diagnosticStoreCode(err error) string {
	switch code := store.StoreErrorCodeOf(err); code {
	case store.StoreErrorNotFound, store.StoreErrorConflict, store.StoreErrorInvalid, store.StoreErrorCanceled, store.StoreErrorTimeout, store.StoreErrorUnavailable, store.StoreErrorDurability, store.StoreErrorUnknownOutcome, store.StoreErrorCorrupt, store.StoreErrorInternal:
		return "store_" + string(code)
	default:
		return ""
	}
}

func diagnosticEmailCode(err error) string {
	switch code := emailautomation.ErrorCode(err); code {
	case app.ToolErrorEmailNotConfigured, app.ToolErrorEmailLoginRequired, app.ToolErrorEmailAccountAmbiguous, app.ToolErrorEmailProviderUnavailable, app.ToolErrorEmailBrowserBusy, app.ToolErrorEmailBrowserCleanupPending, app.ToolErrorEmailPageContractChanged, app.ToolErrorEmailInvalidInput, app.ToolErrorEmailDraftConflict, app.ToolErrorEmailDraftVerificationFailed, app.ToolErrorEmailSendControlUnverified, app.ToolErrorEmailSendOutcomeUnknown, app.ToolErrorEmailSendNotDispatched, app.ToolErrorEmailScriptTimeout, app.ToolErrorEmailScriptInvalidOutput, app.ToolErrorEmailAdmissionStale:
		return string(code)
	default:
		return ""
	}
}

// Public semantics stay in safeCode. This filters only their log projection,
// because typed error fields can still be constructed with arbitrary values.
func diagnosticPublicCode(err error) string {
	code := safeCode(err)
	if code == diagnosticStoreCode(err) || code == diagnosticEmailCode(err) {
		return code
	}
	switch code {
	case "canceled", "timeout", "email_retry_target_too_large", "email_backlog_full", "email_login_required", "email_model_unavailable", "email_model_mock_unqualified", "email_model_output_invalid", "email_model_evidence_invalid", "email_model_input_limit", "email_representation_missing", "email_mail_not_found", "email_processing_failed":
		return code
	default:
		return "unclassified"
	}
}

func jobFailureCause(kind string, err error) jobFailureDiagnostic {
	d := jobFailureDiagnostic{code: "unclassified", source: "internal", stage: jobStage(kind)}
	var renewal *jobRenewalFailure
	if errors.As(err, &renewal) {
		d.stage = "lease_renewal"
		err = renewal.cause
	}
	if code := diagnosticStoreCode(err); code != "" {
		d.code, d.source = code, "store"
		return d
	}
	if errors.Is(err, context.DeadlineExceeded) {
		d.code, d.source = "timeout", "context"
		if renewal == nil {
			d.stage = "deadline"
		}
		return d
	}
	if errors.Is(err, context.Canceled) {
		d.code, d.source = "canceled", "context"
		if renewal == nil {
			d.stage = "cancellation"
		}
		return d
	}
	if errors.Is(err, fs.ErrNotExist) {
		d.code, d.source = "file_not_found", "filesystem"
		return d
	}
	if errors.Is(err, fs.ErrPermission) {
		d.code, d.source = "file_permission_denied", "filesystem"
		return d
	}
	if code := diagnosticEmailCode(err); code != "" {
		d.code, d.source = code, "automation"
		return d
	}
	if err == nil {
		return d
	}
	// These are exact internal sentinel messages, never prefixes or raw text.
	// Public safeCode deliberately retains its smaller vocabulary.
	switch err.Error() {
	case "email_batch_journal_invalid", "email_capture_identity", "email_command_receipt_invalid", "email_output_path_invalid", "email_source_integrity", "email_source_invalid", "email_source_missing", "email_source_identity", "email_source_scope", "email_source_headers_invalid", "email_source_body_invalid", "email_source_mime_depth", "email_source_mime_invalid", "email_source_json_invalid", "email_original_missing", "email_capture_missing", "email_representation_missing":
		d.code, d.source = err.Error(), "source"
	case "email_model_unavailable", "email_model_mock_unqualified", "email_model_output_invalid", "email_model_output_limit", "email_model_evidence_invalid", "email_model_input_limit":
		d.code, d.source = err.Error(), "model"
	case "email_presentation_language_invalid", "email_presentation_output_invalid":
		d.code, d.source = err.Error(), "presentation"
	}
	return d
}

func recordJobFailure(job app.EmailJob, firstCause, dispatchError error) {
	d := jobFailureCause(job.Kind, firstCause)
	// Job IDs are generated SHA-256 keys. Other identifiers, lease tokens and
	// error messages must never enter ordinary diagnostics.
	id := ""
	if diagnosticJobID.MatchString(job.ID) {
		id = job.ID
	}
	slog.Warn("email job failed", "schema_version", 1, "job_id", id,
		"attempt", job.Attempt, "generation", job.Generation, "operation", jobOperation(job.Kind),
		"stage", d.stage, "cause_code", d.code, "cause_source", d.source,
		"public_code", diagnosticPublicCode(dispatchError))
}
