package emailautomation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const (
	maxScriptOutputBytes = 64 << 10
	maxRecipientBytes    = 320
	maxSubjectRunes      = 998
	maxBodyBytes         = 200 << 10
)

var (
	invocationIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
	scriptCodePattern   = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
	recipientPattern    = regexp.MustCompile(`^[^\s@<>]+@[^\s@<>]+\.[^\s@<>]+$`)
)

type ProbeResult struct {
	Provider    string
	AccountHint string
	Generation  uint64
	Revision    int
	CheckedAt   time.Time
}

type SendRequest = app.EmailSendRequest
type SendResult = app.EmailSendResult
type ReadRequest = app.EmailReadRequest
type ReadResult = app.EmailReadResult

type ScriptRunner interface {
	Probe(context.Context, Provider, string, uint64) (ProbeResult, error)
	Send(context.Context, Provider, SendRequest) (SendResult, error)
	Read(context.Context, Provider, ReadRequest) (ReadResult, error)
	Discover(context.Context, Provider, ReadRequest) (app.EmailDiscoveryResult, error)
	CollectPage(context.Context, Provider, ReadRequest) (app.EmailPageResult, error)
	EnumerateThread(context.Context, Provider, app.EmailThreadRequest) (app.EmailThreadResult, error)
	MarkRead(context.Context, Provider, app.EmailMarkReadRequest) (app.EmailMarkReadResult, error)
}

func validateMessage(recipient, subject, body string) error {
	if len(recipient) == 0 || len(recipient) > maxRecipientBytes || strings.ContainsAny(recipient, "\r\n\x00") || !recipientPattern.MatchString(recipient) {
		return codedError(app.ToolErrorEmailInvalidInput, "Recipient must be one valid email address")
	}
	if !utf8.ValidString(subject) || utf8.RuneCountInString(subject) > maxSubjectRunes || strings.ContainsAny(subject, "\r\n\x00") {
		return codedError(app.ToolErrorEmailInvalidInput, "Email subject must be one bounded line")
	}
	if !utf8.ValidString(body) || strings.TrimSpace(body) == "" || len(body) > maxBodyBytes || strings.ContainsRune(body, '\x00') {
		return codedError(app.ToolErrorEmailInvalidInput, "Email body must be non-empty bounded UTF-8 text")
	}
	return nil
}

func recipientDigest(recipient string) string {
	digest := sha256.Sum256([]byte(recipient))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func validAccountHint(value string) bool {
	if value == "" {
		return true
	}
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > 64 || strings.ContainsAny(value, "\r\n\x00") {
		return false
	}
	separator := strings.LastIndex(value, "***@")
	if separator <= 0 || strings.Count(value, "***@") != 1 {
		return false
	}
	prefix, domain := value[:separator], value[separator+4:]
	return utf8.RuneCountInString(prefix) <= 2 && strings.TrimSpace(prefix) == prefix && domain != "" &&
		domain == strings.ToLower(domain) && !strings.ContainsAny(domain, " @/\\") && strings.Contains(domain, ".")
}

func validOpaqueProviderID(value string) bool {
	return value == "" || utf8.ValidString(value) && len(value) <= 256 && !strings.ContainsAny(value, "\r\n\x00")
}

func decodeStrictJSON(raw []byte, output any) error {
	return decodeStrictJSONLimit(raw, output, maxScriptOutputBytes)
}

func decodeStrictJSONLimit(raw []byte, output any, maxBytes int) error {
	if len(raw) == 0 || len(raw) > maxBytes {
		return errors.New("JSON output is empty or oversized")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return errors.New("JSON output has a trailing value")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// scriptErrorCodes maps every provider-specific failure code the fixed email
// scripts emit onto the bounded email vocabulary. Canonical app.ToolErrorEmail*
// codes pass through unchanged; anything unmapped is reported as a provider
// outage. TestScriptErrorCodesCoverEveryEmittedCode keeps this table and the
// scripts under scripts/email in step.
var scriptErrorCodes = map[string]app.ToolErrorCode{
	"email_send_journal_unavailable":      app.ToolErrorEmailNotConfigured,
	"email_send_configuration_error":      app.ToolErrorEmailNotConfigured,
	"email_send_journal_conflict":         app.ToolErrorEmailDraftConflict,
	"email_existing_draft":                app.ToolErrorEmailDraftConflict,
	"email_reply_target_unverified":       app.ToolErrorEmailDraftVerificationFailed,
	"email_reply_control_unavailable":     app.ToolErrorEmailDraftVerificationFailed,
	"email_reply_editor_unverified":       app.ToolErrorEmailDraftVerificationFailed,
	"email_draft_fields_unverified":       app.ToolErrorEmailDraftVerificationFailed,
	"email_recipient_editor_unverified":   app.ToolErrorEmailDraftVerificationFailed,
	"email_cc_unavailable":                app.ToolErrorEmailDraftVerificationFailed,
	"email_send_control_unverified":       app.ToolErrorEmailSendControlUnverified,
	"email_recipient_verification_failed": app.ToolErrorEmailDraftVerificationFailed,

	"email_cursor_invalid":      app.ToolErrorEmailInvalidInput,
	"invalid_request":           app.ToolErrorEmailInvalidInput,
	"invalid_input":             app.ToolErrorEmailInvalidInput,
	"invalid_message":           app.ToolErrorEmailInvalidInput,
	"email_probe_invalid_input": app.ToolErrorEmailInvalidInput,
	"email_send_invalid_input":  app.ToolErrorEmailInvalidInput,
	"invalid_recipient":         app.ToolErrorEmailInvalidInput,
	"invalid_subject":           app.ToolErrorEmailInvalidInput,
	"invalid_body":              app.ToolErrorEmailInvalidInput,
	"body_too_large":            app.ToolErrorEmailInvalidInput,

	"email_network_list_unqualified":       app.ToolErrorEmailPageContractChanged,
	"email_incremental_unqualified":        app.ToolErrorEmailPageContractChanged,
	"email_network_capability_unavailable": app.ToolErrorEmailPageContractChanged,
	"email_network_original_unqualified":   app.ToolErrorEmailPageContractChanged,
	"email_network_thread_unqualified":     app.ToolErrorEmailPageContractChanged,
	"email_network_mark_read_unqualified":  app.ToolErrorEmailPageContractChanged,
	"email_network_target_unobserved":      app.ToolErrorEmailPageContractChanged,
	"email_network_interval_required":      app.ToolErrorEmailPageContractChanged,
	"email_network_read_failed":            app.ToolErrorEmailProviderUnavailable,
	"page_contract_changed":                app.ToolErrorEmailPageContractChanged,
	"email_login_evidence_conflict":        app.ToolErrorEmailPageContractChanged,
	"provider_origin_mismatch":             app.ToolErrorEmailPageContractChanged,
	"email_account_identity_mismatch":      app.ToolErrorEmailPageContractChanged,
	"email_account_identity_unavailable":   app.ToolErrorEmailPageContractChanged,
	"email_message_identity_invalid":       app.ToolErrorEmailPageContractChanged,
	"email_message_identity_ambiguous":     app.ToolErrorEmailPageContractChanged,
	"email_pinned_message_unavailable":     app.ToolErrorEmailPageContractChanged,
	"email_provider_origin_invalid":        app.ToolErrorEmailPageContractChanged,
	"outlook_origin_not_allowed":           app.ToolErrorEmailPageContractChanged,
	"outlook_page_contract_changed":        app.ToolErrorEmailPageContractChanged,

	"draft_verification_failed":      app.ToolErrorEmailDraftVerificationFailed,
	"field_verification_failed":      app.ToolErrorEmailDraftVerificationFailed,
	"email_send_precondition_failed": app.ToolErrorEmailDraftVerificationFailed,
	"send_precondition_failed":       app.ToolErrorEmailDraftVerificationFailed,
	"send_preparation_failed":        app.ToolErrorEmailDraftVerificationFailed,

	"send_control_not_ready": app.ToolErrorEmailSendControlUnverified,
	"send_unavailable":       app.ToolErrorEmailSendControlUnverified,

	"send_outcome_unknown": app.ToolErrorEmailSendOutcomeUnknown,

	"login_probe_timeout": app.ToolErrorEmailScriptTimeout,

	"login_probe_invalid_output":   app.ToolErrorEmailScriptInvalidOutput,
	"send_browser_output_invalid":  app.ToolErrorEmailScriptInvalidOutput,
	"browser_output_invalid":       app.ToolErrorEmailScriptInvalidOutput,
	"email_browser_output_invalid": app.ToolErrorEmailScriptInvalidOutput,
	"email_download_limit":         app.ToolErrorEmailScriptInvalidOutput,
	"email_capture_invalid":        app.ToolErrorEmailScriptInvalidOutput,
	"email_capture_limit":          app.ToolErrorEmailScriptInvalidOutput,
	// Batch failures retain their raw code and local_operational scope. These
	// mappings apply only if an exceptional error escapes the whole script.
	"email_local_io":                app.ToolErrorEmailScriptInvalidOutput,
	"email_batch_limit":             app.ToolErrorEmailScriptInvalidOutput,
	"email_source_conflict":         app.ToolErrorEmailScriptInvalidOutput,
	"email_source_recovery_pending": app.ToolErrorEmailScriptInvalidOutput,
}

func normalizeScriptErrorCode(code string) app.ToolErrorCode {
	switch app.ToolErrorCode(code) {
	case app.ToolErrorEmailNotConfigured, app.ToolErrorEmailLoginRequired, app.ToolErrorEmailAccountAmbiguous,
		app.ToolErrorEmailProviderUnavailable, app.ToolErrorEmailPageContractChanged, app.ToolErrorEmailInvalidInput,
		app.ToolErrorEmailDraftConflict, app.ToolErrorEmailDraftVerificationFailed, app.ToolErrorEmailSendControlUnverified,
		app.ToolErrorEmailSendOutcomeUnknown, app.ToolErrorEmailScriptTimeout, app.ToolErrorEmailScriptInvalidOutput:
		return app.ToolErrorCode(code)
	}
	if mapped, ok := scriptErrorCodes[code]; ok {
		return mapped
	}
	return app.ToolErrorEmailProviderUnavailable
}

func publicScriptErrorMessage(code app.ToolErrorCode) string {
	switch code {
	case app.ToolErrorEmailLoginRequired:
		return "Email login is required"
	case app.ToolErrorEmailInvalidInput:
		return "Email request is invalid"
	case app.ToolErrorEmailPageContractChanged:
		return "Email provider page contract changed"
	case app.ToolErrorEmailDraftConflict:
		return "An existing email draft prevents this send"
	case app.ToolErrorEmailDraftVerificationFailed:
		return "Email draft verification failed; Send was not clicked"
	case app.ToolErrorEmailSendControlUnverified:
		return "Email Send control could not be verified; Send was not clicked"
	case app.ToolErrorEmailSendOutcomeUnknown:
		return "Email send outcome is unknown and must not be retried"
	case app.ToolErrorEmailScriptTimeout:
		return "Email provider script timed out"
	case app.ToolErrorEmailScriptInvalidOutput:
		return "Email provider script returned invalid output"
	default:
		return "Email provider is unavailable"
	}
}
