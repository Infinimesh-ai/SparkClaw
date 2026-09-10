package app

import (
	"strings"
	"time"
)

// EmailAnalysisReferenceLimit bounds the reply history selected for analysis.
// Captured and normalized headers retain the complete original reference list.
const EmailAnalysisReferenceLimit = 32

// EmailAnalysisReferences returns distinct nonempty references, newest first.
// Parsers append In-Reply-To after References so the direct parent is retained.
func EmailAnalysisReferences(references []string) []string {
	selected := make([]string, 0, min(len(references), EmailAnalysisReferenceLimit))
	seen := make(map[string]bool)
	for i := len(references) - 1; i >= 0 && len(selected) < EmailAnalysisReferenceLimit; i-- {
		ref := strings.TrimSpace(references[i])
		if ref != "" && !seen[ref] {
			seen[ref] = true
			selected = append(selected, ref)
		}
	}
	return selected
}

// EmailMailbox identifies a verified provider account independently of settings
// and browser credentials. Only one mailbox per owner/provider can be active.
type EmailMailbox struct {
	PageAcks          map[string]string `json:"page_acks,omitempty"`
	ID                string            `json:"id"`
	OwnerID           string            `json:"owner_id"`
	Provider          string            `json:"provider"`
	Address           string            `json:"address"`
	NormalizedAddress string            `json:"normalized_address"`
	Version           int64             `json:"version"`
	ActivatedAt       time.Time         `json:"activated_at"`
	BindingGeneration int64             `json:"binding_generation"`
	Active            bool              `json:"active"`
	IntakeEnabled     bool              `json:"intake_enabled"`
	Boundary          time.Time         `json:"boundary"`
	Cursor            string            `json:"cursor,omitempty"`
	Coverage          string            `json:"coverage,omitempty"`
	LastCheckedAt     time.Time         `json:"last_checked_at,omitempty"`
	ErrorCode         string            `json:"error_code,omitempty"`
	UpdatedAt         time.Time         `json:"updated_at"`
}

type EmailMail struct {
	AssignmentSource       string               `json:"assignment_source,omitempty"`
	AssignmentRevision     int64                `json:"assignment_revision,omitempty"`
	SendConfirmationSource string               `json:"send_confirmation_source,omitempty"`
	SupersededByMailID     string               `json:"superseded_by_mail_id,omitempty"`
	LocalSendID            string               `json:"local_send_id,omitempty"`
	ReplyMailID            string               `json:"reply_mail_id,omitempty"`
	Verification           *EmailVerification   `json:"verification,omitempty"`
	Classification         *EmailClassification `json:"classification,omitempty"`
	RemoteReadObservedAt   time.Time            `json:"remote_read_observed_at"`
	ID                     string               `json:"id"`
	OwnerID                string               `json:"owner_id"`
	MailboxID              string               `json:"mailbox_id"`
	ProviderMessageID      string               `json:"provider_message_id"`
	ProviderSelectionID    string               `json:"provider_selection_id"`
	ProviderThreadID       string               `json:"provider_thread_id,omitempty"`
	Direction              string               `json:"direction"`
	Folder                 string               `json:"folder"`
	DiscoveryReason        string               `json:"discovery_reason"`
	RemoteReadState        string               `json:"remote_read_state"`
	DiscoveredAt           time.Time            `json:"discovered_at"`
	SourceTime             time.Time            `json:"source_time"`
	ArrivalSequence        int64                `json:"arrival_sequence"`
	CaptureID              string               `json:"capture_id,omitempty"`
	RepresentationID       string               `json:"representation_id,omitempty"`
	ContextID              string               `json:"context_id,omitempty"`
	ConversationID         string               `json:"conversation_id,omitempty"`
	Subject                string               `json:"subject,omitempty"`
	Participants           []string             `json:"participants,omitempty"`
	MessageID              string               `json:"message_id,omitempty"`
	ReplyReferences        []string             `json:"reply_references,omitempty"`
	InputVersion           int64                `json:"input_version"`
	CaptureState           string               `json:"capture_state"`
	ParseState             string               `json:"parse_state"`
	AssignmentState        string               `json:"assignment_state"`
	ViewedAt               *time.Time           `json:"viewed_at,omitempty"`
	Summary                *EmailSummary        `json:"summary,omitempty"`
}

type EmailCaptureVersion struct {
	ID             string    `json:"id"`
	MailID         string    `json:"mail_id"`
	ManifestPath   string    `json:"manifest_path"`
	ManifestSHA256 string    `json:"manifest_sha256"`
	OriginalPath   string    `json:"original_path"`
	OriginalSHA256 string    `json:"original_sha256"`
	State          string    `json:"state"`
	CreatedAt      time.Time `json:"created_at"`
}

type EmailAttachment struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MIMEType  string `json:"mime_type"`
	SizeBytes int64  `json:"size_bytes"`
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	Text      string `json:"text,omitempty"`
	TextPath  string `json:"text_path,omitempty"`
	State     string `json:"state"`
}

type EmailRepresentation struct {
	HeaderSignals   map[string]string `json:"header_signals,omitempty"`
	ID              string            `json:"id"`
	MailID          string            `json:"mail_id"`
	CaptureID       string            `json:"capture_id"`
	ManifestPath    string            `json:"manifest_path"`
	ManifestSHA256  string            `json:"manifest_sha256"`
	Subject         string            `json:"subject"`
	From            []string          `json:"from"`
	To              []string          `json:"to"`
	CC              []string          `json:"cc,omitempty"`
	ReplyTo         []string          `json:"reply_to,omitempty"`
	MessageID       string            `json:"message_id,omitempty"`
	ReplyReferences []string          `json:"reply_references,omitempty"`
	SourceTime      time.Time         `json:"source_time"`
	BodyText        string            `json:"body_text"`
	Attachments     []EmailAttachment `json:"attachments,omitempty"`
	State           string            `json:"state"`
	Coverage        string            `json:"coverage"`
	ParserVersion   string            `json:"parser_version"`
	CreatedAt       time.Time         `json:"created_at"`
}

type EmailContextVersion struct {
	ID                   string    `json:"id"`
	MailID               string    `json:"mail_id"`
	RelatedMailIDs       []string  `json:"related_mail_ids"`
	UnresolvedReferences []string  `json:"unresolved_references"`
	Coverage             string    `json:"coverage"`
	CreatedAt            time.Time `json:"created_at"`
}

type EmailConversation struct {
	TitleState        string        `json:"title_state,omitempty"`
	EffectiveEntry    string        `json:"effective_entry,omitempty"`
	HistoricalMixed   bool          `json:"historical_mixed"`
	ID                string        `json:"id"`
	OwnerID           string        `json:"owner_id"`
	Title             string        `json:"title"`
	Participants      []string      `json:"participants"`
	MailboxIDs        []string      `json:"mailbox_ids"`
	MembershipVersion int64         `json:"membership_version"`
	InputVersion      int64         `json:"input_version"`
	MemberCount       int           `json:"member_count"`
	UnseenCount       int           `json:"unseen_count"`
	CreatedAt         time.Time     `json:"created_at"`
	UpdatedAt         time.Time     `json:"updated_at"`
	Summary           *EmailSummary `json:"summary,omitempty"`
}

type EmailAssignmentDecision struct {
	OutputPath       string    `json:"output_path,omitempty"`
	OutputSHA256     string    `json:"output_sha256,omitempty"`
	ID               string    `json:"id"`
	MailID           string    `json:"mail_id"`
	Action           string    `json:"action"`
	ConversationID   string    `json:"conversation_id,omitempty"`
	Title            string    `json:"title,omitempty"`
	OwnerEpoch       int64     `json:"owner_epoch"`
	InputFingerprint string    `json:"input_fingerprint"`
	InputPath        string    `json:"input_path,omitempty"`
	InputSHA256      string    `json:"input_sha256,omitempty"`
	ModelVersion     string    `json:"model_version"`
	PromptVersion    string    `json:"prompt_version"`
	Reason           string    `json:"reason"`
	EvidenceRefs     []string  `json:"evidence_refs"`
	CreatedAt        time.Time `json:"created_at"`
}

type EmailAssignmentConcern struct {
	InputPath        string    `json:"input_path,omitempty"`
	InputSHA256      string    `json:"input_sha256,omitempty"`
	OutputPath       string    `json:"output_path,omitempty"`
	OutputSHA256     string    `json:"output_sha256,omitempty"`
	ModelVersion     string    `json:"model_version"`
	PromptVersion    string    `json:"prompt_version"`
	ID               string    `json:"id"`
	OwnerID          string    `json:"owner_id"`
	Kind             string    `json:"kind"`
	MailIDs          []string  `json:"mail_ids"`
	ConversationIDs  []string  `json:"conversation_ids"`
	Reason           string    `json:"reason"`
	EvidenceRefs     []string  `json:"evidence_refs"`
	InputFingerprint string    `json:"input_fingerprint"`
	Version          int64     `json:"version"`
	CreatedAt        time.Time `json:"created_at"`
}

type EmailSummary struct {
	ID               string    `json:"id"`
	TargetKind       string    `json:"target_kind"`
	TargetID         string    `json:"target_id"`
	Text             string    `json:"text"`
	OutputPath       string    `json:"output_path,omitempty"`
	OutputSHA256     string    `json:"output_sha256,omitempty"`
	EvidenceRefs     []string  `json:"evidence_refs"`
	Coverage         string    `json:"coverage"`
	InputPath        string    `json:"input_path,omitempty"`
	InputSHA256      string    `json:"input_sha256,omitempty"`
	ModelVersion     string    `json:"model_version"`
	PromptVersion    string    `json:"prompt_version"`
	Generation       int64     `json:"generation"`
	InputFingerprint string    `json:"input_fingerprint"`
	Current          bool      `json:"current"`
	CreatedAt        time.Time `json:"created_at"`
}

// References use mail:<id>, conversation:<id>, reply:<RFC Message-ID>,
// thread:<mailbox-id>:<provider-thread-id>, or summary:<kind>:<target-id>.
type EmailAnalysisTarget struct {
	CurrentJob           *EmailJob        `json:"-"`
	SelectedDependencies []string         `json:"selected_dependencies"`
	ConcernID            string           `json:"concern_id,omitempty"`
	Kind                 string           `json:"kind"`
	TargetID             string           `json:"target_id"`
	Generation           int64            `json:"generation"`
	InputFingerprint     string           `json:"input_fingerprint"`
	Inputs               map[string]int64 `json:"inputs"`
	SummaryID            string           `json:"summary_id,omitempty"`
	State                string           `json:"state"`
}

type EmailJob struct {
	Priority          string    `json:"priority,omitempty"`
	ID                string    `json:"id"`
	OwnerID           string    `json:"owner_id"`
	Kind              string    `json:"kind"`
	TargetID          string    `json:"target_id"`
	MailboxID         string    `json:"mailbox_id,omitempty"`
	BindingGeneration int64     `json:"binding_generation,omitempty"`
	InputFingerprint  string    `json:"input_fingerprint,omitempty"`
	Generation        int64     `json:"generation"`
	State             string    `json:"state"`
	Attempt           int       `json:"attempt"`
	MaxAttempts       int       `json:"max_attempts"`
	LeaseToken        string    `json:"lease_token,omitempty"`
	LeaseExpiresAt    time.Time `json:"lease_expires_at,omitempty"`
	NextAttemptAt     time.Time `json:"next_attempt_at"`
	ErrorCode         string    `json:"error_code,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type EmailProviderThread struct {
	ErrorCode           string    `json:"error_code,omitempty"`
	Gaps                []string  `json:"gaps,omitempty"`
	ProviderSelectionID string    `json:"provider_selection_id"`
	ID                  string    `json:"id"`
	MailboxID           string    `json:"mailbox_id"`
	ProviderThreadID    string    `json:"provider_thread_id"`
	Folder              string    `json:"folder"`
	ObservationVersion  int64     `json:"observation_version"`
	Cursor              string    `json:"cursor,omitempty"`
	Coverage            string    `json:"coverage"`
	LastCheckedAt       time.Time `json:"last_checked_at"`
}

type EmailSyncRun struct {
	ID         string    `json:"id"`
	MailboxID  string    `json:"mailbox_id"`
	Trigger    string    `json:"trigger"`
	Cursor     string    `json:"cursor,omitempty"`
	Coverage   string    `json:"coverage"`
	Discovered int       `json:"discovered"`
	Gaps       []string  `json:"gaps,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type EmailViewReceipt struct {
	MailID        string    `json:"mail_id"`
	FirstViewedAt time.Time `json:"first_viewed_at"`
}

const (
	EmailJobCapture                = "capture"
	EmailJobMarkRead               = "mark_read"
	EmailJobParse                  = "parse"
	EmailJobMessageSummary         = "message_summary"
	EmailJobAssignment             = "assignment"
	EmailJobRelationshipCheck      = "relationship_check"
	EmailJobConversationSummary    = "conversation_summary"
	EmailJobDiscover               = "discover"
	EmailJobThreadSync             = "thread_sync"
	EmailJobQueued                 = "queued"
	EmailJobRunning                = "running"
	EmailJobRetryWait              = "retry_wait"
	EmailJobSucceeded              = "succeeded"
	EmailJobFailed                 = "failed"
	EmailJobPaused                 = "paused"
	EmailCaptureComplete           = "complete"
	EmailCapturePartial            = "partial"
	EmailCaptureFailed             = "failed"
	EmailParseReady                = "ready"
	EmailParsePartial              = "partial"
	EmailParseUnsupported          = "unsupported"
	EmailParseFailed               = "failed"
	EmailAssignmentPending         = "pending"
	EmailAssignmentAssigned        = "assigned"
	EmailConcernSuspectedDuplicate = "suspected_duplicate"
	EmailConcernPendingCorrection  = "pending_correction"
	EmailSummaryPending            = "pending"
	EmailSummaryCurrent            = "current"
	EmailSummaryStale              = "stale"
	EmailSummaryFailed             = "failed"
)

type EmailOwnerStatus struct {
	Revision          int64 `json:"revision"`
	PendingCount      int   `json:"pending_count"`
	BacklogCount      int   `json:"backlog_count"`
	CapturedCount     int   `json:"captured_count"`
	ConversationCount int   `json:"conversation_count"`
}

const EmailConcernNone = "none"

const EmailJobPriorityHistory = "history"
