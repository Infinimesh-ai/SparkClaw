package app

import (
	"slices"
	"time"
)

const (
	EmailProviderQQMail  = "qq_mail"
	EmailProviderOutlook = "outlook"
	EmailProviderGmail   = "gmail"

	EmailAccountDefault = "default"

	EmailStateNotConfigured          = IntegrationStateNotConfigured
	EmailStateLoginRequired          = "login_required"
	EmailStateReady                  = IntegrationStateReady
	EmailStateNeedsAttention         = IntegrationStateNeedsAttention
	EmailStateTemporarilyUnavailable = IntegrationStateTemporarilyUnavailable

	EmailRouteFactProvider                    = "email_provider"
	EmailRouteFactAccount                     = "email_account"
	EmailRouteFactAccountHint                 = "email_account_hint"
	EmailRouteFactSettingVersion              = "email_setting_version"
	EmailRouteFactBrowserCredentialGeneration = "email_browser_credential_generation"
	EmailRouteFactProbeRevision               = "email_probe_revision"
	EmailRouteFactSendScriptRevision          = "email_send_script_revision"
	EmailRouteFactValidatedAt                 = "email_validated_at"
	EmailRouteFactInvocationID                = "email_invocation_id"
)

// emailProviderIDs is the single registration point for browser-backed mail
// providers. The Controller registry (tools/browser-controller) owns each
// provider's scripts, login URL, and origins; the Gateway binds to that
// registry through the generated provider_scripts.json contract.
var emailProviderIDs = []string{EmailProviderQQMail, EmailProviderOutlook, EmailProviderGmail}

// EmailProviderIDs returns the supported provider identities in registration
// order.
func EmailProviderIDs() []string { return slices.Clone(emailProviderIDs) }

// KnownEmailProvider reports whether id names a supported provider.
func KnownEmailProvider(id string) bool { return slices.Contains(emailProviderIDs, id) }

// EmailProviderDisplayName returns the owner-facing provider name, or "" for
// an unknown provider.
func EmailProviderDisplayName(id string) string {
	switch id {
	case EmailProviderQQMail:
		return "QQ Mail"
	case EmailProviderOutlook:
		return "Outlook"
	case EmailProviderGmail:
		return "Gmail"
	default:
		return ""
	}
}

// EmailProviderSetting is the durable, non-secret owner configuration for one
// browser-backed mail provider. Authentication remains in Chromium.
type EmailProviderSetting struct {
	OwnerID       string     `json:"owner_id"`
	Provider      string     `json:"provider"`
	Enabled       bool       `json:"enabled"`
	Default       bool       `json:"default"`
	Account       string     `json:"account"`
	AccountHint   string     `json:"account_hint,omitempty"`
	State         string     `json:"state"`
	LastCheckedAt *time.Time `json:"last_checked_at,omitempty"`
	ErrorCode     string     `json:"error_code,omitempty"`
	Version       int64      `json:"version"`
	UpdatedBy     string     `json:"updated_by,omitempty"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// EmailAdmissionBinding is the fresh Runtime-owned proof that one configured
// provider was logged in immediately before a browser.email Workflow was
// created. These fields are persisted as route facts and never selected by the
// model.
type EmailAdmissionBinding struct {
	Provider                    string
	Account                     string
	AccountHint                 string
	SettingVersion              int64
	BrowserCredentialGeneration uint64
	ProbeRevision               int
	SendScriptRevision          int
	ValidatedAt                 time.Time
}

// EmailSendRequest contains the complete approved send contract. Provider,
// account, revisions, generation, and invocation identity are Runtime-owned;
// only recipient, subject, and body originate from the model.
type EmailSendRequest struct {
	Provider                    string
	Account                     string
	Recipient                   string
	Subject                     string
	Body                        string
	InvocationID                string
	BrowserCredentialGeneration uint64
	ProbeRevision               int
	ScriptRevision              int
	SettingVersion              int64
}

type EmailSendResult struct {
	Provider                    string `json:"provider"`
	Status                      string `json:"status"`
	RecipientDigest             string `json:"recipient_digest"`
	ProviderMessageID           string `json:"provider_message_id,omitempty"`
	BrowserCredentialGeneration uint64 `json:"browser_credential_generation"`
	ScriptRevision              int    `json:"script_revision"`
}
