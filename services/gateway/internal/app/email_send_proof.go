package app

import "regexp"

// EmailNotSentProof is emitted only by the authenticated runtime for the
// original task. Pre-dispatch kinds come from reconciliation and do not deny
// attachment upload/draft side effects; provider_rejected is the provider's own
// explicit rejection of a released dispatch, reported with the send result.
type EmailNotSentProof struct {
	SchemaVersion  int    `json:"schema_version"`
	Kind           string `json:"kind"`
	InvocationID   string `json:"invocation_id"`
	TaskID         string `json:"task_id"`
	IntentDigest   string `json:"intent_digest"`
	ResourceDigest string `json:"resource_digest"`
	BindingDigest  string `json:"binding_digest"`
	LedgerEpoch    uint64 `json:"ledger_epoch"`
	Reason         string `json:"reason"`
	ProviderCode   *int   `json:"provider_code,omitempty"`
}

var emailProofID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var emailProofDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

const EmailLegacy15QQBindingDigest = "e4375abe5b06a033a818ed6a3659d88067c26c7727bbf45848f231274ab5ae97"

// Provider rejection categories. Each maps to one runtime reason.
const (
	EmailRejectionLogin     = "login"
	EmailRejectionParameter = "parameter"
	EmailRejectionTemporary = "temporary"
)

var emailRejectionReasons = map[string]string{
	EmailRejectionLogin:     "PROVIDER_LOGIN_REJECTED",
	EmailRejectionParameter: "PROVIDER_PARAMETER_REJECTED",
	EmailRejectionTemporary: "PROVIDER_TEMPORARILY_REJECTED",
}

// emailProviderRejections are the explicit rejection codes of each provider's
// maintained native send template, mirrored from its App-CLI provider module.
// Only these codes prove "not sent"; any other code stays pending confirmation.
var emailProviderRejections = map[string]map[int]string{
	EmailProviderQQMail: {
		-5012: EmailRejectionLogin, -20002: EmailRejectionLogin, -20062: EmailRejectionLogin, -20071: EmailRejectionLogin, -21021: EmailRejectionLogin,
		-20011: EmailRejectionParameter, -21101: EmailRejectionParameter, -5151: EmailRejectionParameter, -21501: EmailRejectionParameter, -21132: EmailRejectionParameter,
		-21519: EmailRejectionTemporary, -20003: EmailRejectionTemporary,
	},
}

// emailTemplateDriftCodes suggest that a maintained template no longer matches
// the provider's request format.
var emailTemplateDriftCodes = map[string]map[int]bool{EmailProviderQQMail: {-20011: true}}

// Draft error codes for an explicit provider rejection.
const (
	EmailSendRejectedLoginRequired = "email_send_rejected_login_required"
	EmailSendRejectedParameter     = "email_send_rejected_invalid_parameter"
	EmailSendRejectedTemporarily   = "email_send_rejected_temporarily"
	EmailSendTemplateDrift         = "email_send_template_drift"
)

// RejectionCategory returns the category of a valid provider_rejected proof.
func (p *EmailNotSentProof) RejectionCategory(provider string) (string, bool) {
	if p == nil || p.Kind != "provider_rejected" || p.ProviderCode == nil {
		return "", false
	}
	category, ok := emailProviderRejections[provider][*p.ProviderCode]
	return category, ok && emailRejectionReasons[category] == p.Reason
}

// RejectionErrorCode is the draft error code of a provider_rejected proof.
func (p *EmailNotSentProof) RejectionErrorCode(provider string) string {
	category, ok := p.RejectionCategory(provider)
	switch {
	case !ok:
		return ""
	case category == EmailRejectionLogin:
		return EmailSendRejectedLoginRequired
	case category == EmailRejectionParameter && emailTemplateDriftCodes[provider][*p.ProviderCode]:
		return EmailSendTemplateDrift
	case category == EmailRejectionParameter:
		return EmailSendRejectedParameter
	default:
		return EmailSendRejectedTemporarily
	}
}

func (p *EmailNotSentProof) ValidFor(invocation, provider string) bool {
	if p == nil || p.SchemaVersion != 1 || p.InvocationID != invocation || !emailProofID.MatchString(invocation) || !emailProofID.MatchString(p.TaskID) || p.LedgerEpoch == 0 || p.LedgerEpoch > 9007199254740991 ||
		!emailProofDigest.MatchString(p.IntentDigest) || !emailProofDigest.MatchString(p.ResourceDigest) || !emailProofDigest.MatchString(p.BindingDigest) {
		return false
	}
	if p.Kind == "provider_rejected" {
		_, ok := p.RejectionCategory(provider)
		return ok
	}
	if p.ProviderCode != nil {
		return false
	}
	if p.Kind != "pre_dispatch_failure" && p.Kind != "legacy_15_pre_dispatch_failure" {
		return false
	}
	if p.Kind == "legacy_15_pre_dispatch_failure" {
		return provider == EmailProviderQQMail && p.BindingDigest == EmailLegacy15QQBindingDigest && p.Reason == "EMAIL_ATTACHMENT_UPLOAD_UNVERIFIED"
	}
	switch p.Reason {
	case "EMAIL_ATTACHMENT_UPLOAD_UNVERIFIED", "EMAIL_ATTACHMENT_UPLOAD_FAILED", "EMAIL_ATTACHMENT_UNVERIFIED", "BROWSER_EXTENSION_UNAVAILABLE",
		"EMAIL_ATTACHMENT_CONTROL_UNAVAILABLE", "EMAIL_DRAFT_FIELDS_UNVERIFIED":
		return true
	default:
		return false
	}
}
