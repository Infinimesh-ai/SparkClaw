package app

import "regexp"

// EmailNotSentProof is emitted only by the authenticated runtime's reconciliation
// of the original task. It does not deny attachment upload/draft side effects.
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
}

var emailProofID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var emailProofDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

const EmailLegacy15QQBindingDigest = "e4375abe5b06a033a818ed6a3659d88067c26c7727bbf45848f231274ab5ae97"

func (p *EmailNotSentProof) ValidFor(invocation, provider string) bool {
	if p == nil || p.SchemaVersion != 1 || p.InvocationID != invocation || !emailProofID.MatchString(invocation) || !emailProofID.MatchString(p.TaskID) || p.LedgerEpoch == 0 || p.LedgerEpoch > 9007199254740991 ||
		!emailProofDigest.MatchString(p.IntentDigest) || !emailProofDigest.MatchString(p.ResourceDigest) || !emailProofDigest.MatchString(p.BindingDigest) {
		return false
	}
	if p.Kind != "pre_dispatch_failure" && p.Kind != "legacy_15_pre_dispatch_failure" {
		return false
	}
	if p.Kind == "legacy_15_pre_dispatch_failure" {
		return provider == EmailProviderQQMail && p.BindingDigest == EmailLegacy15QQBindingDigest && p.Reason == "EMAIL_ATTACHMENT_UPLOAD_UNVERIFIED"
	}
	switch p.Reason {
	case "EMAIL_ATTACHMENT_UPLOAD_UNVERIFIED", "EMAIL_ATTACHMENT_UPLOAD_FAILED", "EMAIL_ATTACHMENT_UNVERIFIED":
		return true
	default:
		return false
	}
}
