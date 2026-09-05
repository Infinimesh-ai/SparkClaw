package app

import "time"

const (
	EmailProviderQQMail  = "qq_mail"
	EmailProviderOutlook = "outlook"
	EmailProviderGmail   = "gmail"

	EmailAccountDefault = "default"

	EmailStateNotConfigured          = "not_configured"
	EmailStateLoginRequired          = "login_required"
	EmailStateReady                  = "ready"
	EmailStateNeedsAttention         = "needs_attention"
	EmailStateTemporarilyUnavailable = "temporarily_unavailable"

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
