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

type ScriptRunner interface {
	Probe(context.Context, Provider, string, uint64) (ProbeResult, error)
	Send(context.Context, Provider, SendRequest) (SendResult, error)
}

func validateMessage(recipient, subject, body string) error {
	if len(recipient) == 0 || len(recipient) > maxRecipientBytes || strings.ContainsAny(recipient, "\r\n\x00") || !recipientPattern.MatchString(recipient) {
		return codedError(CodeInvalidInput, "Recipient must be one valid email address")
	}
	if !utf8.ValidString(subject) || utf8.RuneCountInString(subject) > maxSubjectRunes || strings.ContainsAny(subject, "\r\n\x00") {
		return codedError(CodeInvalidInput, "Email subject must be one bounded line")
	}
	if !utf8.ValidString(body) || strings.TrimSpace(body) == "" || len(body) > maxBodyBytes || strings.ContainsRune(body, '\x00') {
		return codedError(CodeInvalidInput, "Email body must be non-empty bounded UTF-8 text")
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
	if len(raw) == 0 || len(raw) > maxScriptOutputBytes {
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

func normalizeScriptErrorCode(code string) string {
	switch code {
	case CodeNotConfigured, CodeLoginRequired, CodeAccountAmbiguous, CodeProviderUnavailable,
		CodePageContractChanged, CodeInvalidInput, CodeDraftConflict, CodeDraftVerifyFailed,
		CodeSendControlUnverified, CodeSendOutcomeUnknown, CodeScriptTimeout, CodeScriptInvalidOutput:
		return code
	case "invalid_request", "invalid_input", "invalid_json", "invalid_message", "email_probe_invalid_input", "email_send_invalid_input", "invalid_recipient", "invalid_subject", "invalid_body":
		return CodeInvalidInput
	case "page_contract_changed", "email_login_evidence_conflict", "login_evidence_conflict", "provider_origin_mismatch",
		"email_provider_origin_invalid", "outlook_origin_not_allowed", "outlook_evidence_conflict", "outlook_page_contract_changed":
		return CodePageContractChanged
	case "draft_verification_failed", "field_verification_failed", "email_send_precondition_failed", "send_precondition_failed", "send_preparation_failed":
		return CodeDraftVerifyFailed
	case "send_control_not_ready", "send_unavailable":
		return CodeSendControlUnverified
	case "send_outcome_unknown":
		return CodeSendOutcomeUnknown
	case "login_probe_timeout", "email_probe_timeout", "email_send_timeout", "browser_script_timeout":
		return CodeScriptTimeout
	case "login_probe_invalid_output", "send_browser_output_invalid", "browser_output_invalid", "email_browser_output_invalid":
		return CodeScriptInvalidOutput
	default:
		return CodeProviderUnavailable
	}
}

func publicScriptErrorMessage(code string) string {
	switch code {
	case CodeLoginRequired:
		return "Email login is required"
	case CodeInvalidInput:
		return "Email request is invalid"
	case CodePageContractChanged:
		return "Email provider page contract changed"
	case CodeDraftConflict:
		return "An existing email draft prevents this send"
	case CodeDraftVerifyFailed:
		return "Email draft verification failed; Send was not clicked"
	case CodeSendControlUnverified:
		return "Email Send control could not be verified; Send was not clicked"
	case CodeSendOutcomeUnknown:
		return "Email send outcome is unknown and must not be retried"
	case CodeScriptTimeout:
		return "Email provider script timed out"
	case CodeScriptInvalidOutput:
		return "Email provider script returned invalid output"
	default:
		return "Email provider is unavailable"
	}
}
