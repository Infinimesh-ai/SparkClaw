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
	EmailRouteFactReadScriptRevision          = "email_read_script_revision"
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
	ReadScriptRevision          int
	ValidatedAt                 time.Time
}

// EmailSendRequest contains the complete approved send contract. Provider,
// account, revisions, generation, and invocation identity are Runtime-owned;
// only recipient, subject, and body originate from the model.
type EmailReplyTarget struct {
	EmailCaptureTarget
	Subject string `json:"subject"`
}

type EmailSendRequest struct {
	To                          []string
	CC                          []string
	Mode                        string
	AccountAddress              string
	ReplyTarget                 *EmailReplyTarget
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
	ProviderThreadID            string `json:"provider_thread_id,omitempty"`
	Provider                    string `json:"provider"`
	Status                      string `json:"status"`
	RecipientDigest             string `json:"recipient_digest"`
	ProviderMessageID           string `json:"provider_message_id,omitempty"`
	BrowserCredentialGeneration uint64 `json:"browser_credential_generation"`
	ScriptRevision              int    `json:"script_revision"`
}

// EmailReadRequest binds discovery or capture to Runtime-owned admission.
// A non-nil Target selects capture by identity instead of first-unread reading.
type EmailReadRequest struct {
	Provider                    string
	Account                     string
	OwnerScope                  string
	InvocationID                string
	BrowserCredentialGeneration uint64
	ProbeRevision               int
	ScriptRevision              int
	SettingVersion              int64
	Target                      *EmailCaptureTarget
	Discovery                   *EmailDiscoveryOptions
	AckPageID                   string
}

// EmailDiscoveryOptions pins a bounded observation to the admitted account.
// IntervalStart/End are required only by recent_inbound. Unread scans are not
// time-filtered. An incomplete observation never advances the interval boundary.
type EmailDiscoveryOptions struct {
	Lane           string    `json:"lane"`
	AccountAddress string    `json:"account_address"`
	IntervalStart  time.Time `json:"interval_start"`
	IntervalEnd    time.Time `json:"interval_end"`
	Continuation   string    `json:"continuation"`
	Limit          int       `json:"limit"`
}

// EmailCaptureTarget is Runtime-owned discovery evidence, never model-selected.
type EmailCaptureTarget struct {
	AccountAddress      string `json:"account_address"`
	ProviderMessageID   string `json:"provider_message_id"`
	ProviderSelectionID string `json:"provider_selection_id"`
	ProviderThreadID    string `json:"provider_thread_id,omitempty"`
	Folder              string `json:"folder,omitempty"`
}

type EmailDiscoveryCoverage struct {
	Scope             string     `json:"scope"`
	ScanComplete      bool       `json:"scan_complete"`
	ScannedRows       int        `json:"scanned_rows"`
	UnsupportedRows   int        `json:"unsupported_rows"`
	Limited           bool       `json:"limited"`
	Lane              string     `json:"lane,omitempty"`
	Continuation      string     `json:"continuation,omitempty"`
	Reason            string     `json:"reason,omitempty"`
	BoundaryQualified bool       `json:"boundary_qualified,omitempty"`
	OldestObservedAt  *time.Time `json:"oldest_observed_at,omitempty"`
	Ordering          string     `json:"ordering,omitempty"`
}

type EmailThreadTarget struct {
	AccountAddress      string `json:"account_address"`
	ProviderThreadID    string `json:"provider_thread_id"`
	ProviderSelectionID string `json:"provider_selection_id"`
	Folder              string `json:"folder"`
}

type EmailThreadRequest struct {
	Binding      EmailReadRequest
	Thread       EmailThreadTarget
	Continuation string
	Limit        int
}

type EmailThreadMember struct {
	Target    EmailCaptureTarget `json:"target"`
	Direction string             `json:"direction"`
	Draft     bool               `json:"draft"`
	ReadState string             `json:"read_state"`
}

type EmailThreadResult struct {
	SchemaVersion int                    `json:"schema_version"`
	Provider      string                 `json:"provider"`
	Status        string                 `json:"status"`
	Thread        EmailThreadTarget      `json:"thread"`
	Members       []EmailThreadMember    `json:"members"`
	Coverage      EmailDiscoveryCoverage `json:"coverage"`
	ObservedAt    time.Time              `json:"observed_at"`
}

// The runtime supplies this receipt only after canonical source publication.
// Controller re-verifies its bytes and identity before opening the provider.
type EmailMarkReadRequest struct {
	Binding             EmailReadRequest
	CommittedCapture    EmailCaptureReceipt
	CaptureInvocationID string
}

type EmailMarkReadResult struct {
	SchemaVersion int                `json:"schema_version"`
	Provider      string             `json:"provider"`
	Target        EmailCaptureTarget `json:"target"`
	ReadState     string             `json:"read_state"`
	ObservedAt    time.Time          `json:"observed_at"`
}

type EmailDiscoveryResult struct {
	SchemaVersion  int                    `json:"schema_version"`
	Provider       string                 `json:"provider"`
	Status         string                 `json:"status"`
	AccountAddress string                 `json:"account_address"`
	Candidates     []EmailCaptureTarget   `json:"candidates"`
	Threads        []EmailThreadTarget    `json:"threads,omitempty"`
	Coverage       EmailDiscoveryCoverage `json:"coverage"`
	ObservedAt     time.Time              `json:"observed_at"`
}

// EmailCaptureReceipt references script-captured source material; it is not
// canonical Store admission or model analysis.
type EmailCaptureReceipt struct {
	ManifestPath     string `json:"manifest_path"`
	ManifestSHA256   string `json:"manifest_sha256"`
	MailID           string `json:"mail_id"`
	MailboxID        string `json:"mailbox_id"`
	CaptureID        string `json:"capture_id"`
	AttachmentsCount int    `json:"attachments_count"`
	ReadState        string `json:"read_state"`
}

type EmailReadResult struct {
	Provider                    string               `json:"provider"`
	Status                      string               `json:"status"`
	Capture                     *EmailCaptureReceipt `json:"capture"`
	BrowserCredentialGeneration uint64               `json:"browser_credential_generation"`
	ScriptRevision              int                  `json:"script_revision"`
}

// EmailPageResult is one durable browser-page checkpoint. The caller acknowledges
// PageID only after every verified capture has been published to the mail Store.
type EmailPageResult struct {
	SchemaVersion    int                   `json:"schema_version"`
	Provider         string                `json:"provider"`
	Status           string                `json:"status"`
	AccountAddress   string                `json:"account_address"`
	PageID           string                `json:"page_id"`
	Discovery        EmailDiscoveryResult  `json:"discovery"`
	DiscoveryOptions EmailDiscoveryOptions `json:"discovery_options"`
	Captures         []EmailPageCapture    `json:"captures"`
	Failures         []EmailPageFailure    `json:"failures"`
	ObservedAt       time.Time             `json:"observed_at"`
}

type EmailPageCapture struct {
	Target EmailCaptureTarget `json:"target"`
	Result EmailReadResult    `json:"result"`
}

type EmailPageFailure struct {
	Target    EmailCaptureTarget `json:"target"`
	ErrorCode string             `json:"error_code"`
}
