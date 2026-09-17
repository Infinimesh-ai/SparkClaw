package app

import (
	"strings"
	"time"
)

// EmailAnalysisReferenceLimit bounds the reply history selected for analysis.
// Captured and normalized headers retain the complete original reference list.
const EmailAnalysisReferenceLimit = 32

const (
	EmailSyncScopeTimelineV2 = "timeline-v2"

	EmailProviderModeChangeCursor = "change_cursor"
	EmailProviderModeTimeRange    = "time_range"
	EmailProviderModeAnchoredHead = "anchored_head"
	EmailProviderModeUnqualified  = "unqualified"

	EmailSyncIdle                 = "idle"
	EmailSyncRunning              = "running"
	EmailSyncIncomplete           = "incomplete"
	EmailSyncOverflowConfirmation = "overflow_confirmation"
	EmailSyncCoverageGap          = "coverage_gap"
	EmailSyncUnqualified          = "unqualified"
	EmailSyncLoginRequired        = "login_required"

	EmailSyncFailureMailSpecific        = "mail_specific"
	EmailSyncFailureProviderOperational = "provider_operational"
	EmailSyncFailureLocalOperational    = "local_operational"

	EmailSyncFailureOpen                 = "open"
	EmailSyncFailureRetrying             = "retrying"
	EmailSyncFailureResolved             = "resolved"
	EmailSyncFailureSuppressed           = "suppressed"
	EmailSyncFailureOverflowConfirmation = "overflow_confirmation"
	EmailSyncFailureCoverageGap          = "coverage_gap"

	EmailMailSyncPending    = "pending"
	EmailMailSyncRetry      = "retry_pending"
	EmailMailSyncComplete   = "complete"
	EmailMailSyncSuppressed = "sync_suppressed"
)

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
	RefreshPending             bool              `json:"refresh_pending"`
	RefreshRequestID           string            `json:"refresh_request_id,omitempty"`
	PageAcks                   map[string]string `json:"page_acks,omitempty"`
	ID                         string            `json:"id"`
	OwnerID                    string            `json:"owner_id"`
	Provider                   string            `json:"provider"`
	Address                    string            `json:"address"`
	NormalizedAddress          string            `json:"normalized_address"`
	Version                    int64             `json:"version"`
	ActivatedAt                time.Time         `json:"activated_at"`
	BindingGeneration          int64             `json:"binding_generation"`
	Active                     bool              `json:"active"`
	IntakeEnabled              bool              `json:"intake_enabled"`
	ScopeVersion               string            `json:"scope_version,omitempty"`
	DeploymentAnchor           time.Time         `json:"deployment_anchor,omitempty"`
	DiscoveredThrough          time.Time         `json:"discovered_through,omitempty"`
	PollThrough                time.Time         `json:"poll_through,omitempty"`
	InflightUntil              time.Time         `json:"inflight_until,omitempty"`
	ProviderMode               string            `json:"provider_mode,omitempty"`
	ProviderCursor             string            `json:"provider_cursor,omitempty"`
	SyncState                  string            `json:"sync_state,omitempty"`
	CheckpointRevision         int64             `json:"checkpoint_revision,omitempty"`
	PendingFailureCount        int               `json:"pending_failure_count,omitempty"`
	SuppressedMailCount        int               `json:"suppressed_mail_count,omitempty"`
	CoverageGapCount           int               `json:"coverage_gap_count,omitempty"`
	UnacknowledgedWarningCount int               `json:"unacknowledged_warning_count,omitempty"`
	LastSyncErrorCode          string            `json:"last_sync_error_code,omitempty"`
	Boundary                   time.Time         `json:"boundary"`
	Cursor                     string            `json:"cursor,omitempty"`
	Coverage                   string            `json:"coverage,omitempty"`
	LastCheckedAt              time.Time         `json:"last_checked_at,omitempty"`
	ErrorCode                  string            `json:"error_code,omitempty"`
	UpdatedAt                  time.Time         `json:"updated_at"`
}

type EmailMail struct {
	ProviderNativeID       string               `json:"provider_native_id,omitempty"`
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
	SyncState              string               `json:"sync_state,omitempty"`
	QualifiedFailureCount  int                  `json:"qualified_failure_count,omitempty"`
	LastSyncErrorCode      string               `json:"last_sync_error_code,omitempty"`
	ViewedAt               *time.Time           `json:"viewed_at,omitempty"`
	Summary                *EmailSummary        `json:"summary,omitempty"`
}

// EmailSyncAttempt is bounded audit evidence for a discovery or original
// synchronization attempt. It deliberately contains no provider identifier,
// subject, address, cursor, credential, or download URL.
type EmailSyncAttempt struct {
	Trigger      string    `json:"trigger"`
	Actor        string    `json:"actor"`
	InvocationID string    `json:"invocation_id,omitempty"`
	StartedAt    time.Time `json:"started_at,omitempty"`
	CompletedAt  time.Time `json:"completed_at"`
	Outcome      string    `json:"outcome"`
	ErrorCode    string    `json:"error_code,omitempty"`
}

// EmailSyncFailure is the durable retry/warning ledger. ProviderMessageID is
// internal-only and must never be included in an owner-facing projection.
type EmailSyncFailure struct {
	ID                  string             `json:"id"`
	OwnerID             string             `json:"owner_id"`
	MailboxID           string             `json:"mailbox_id"`
	BindingGeneration   int64              `json:"binding_generation"`
	MailID              string             `json:"mail_id,omitempty"`
	ProviderMessageID   string             `json:"provider_message_id,omitempty"`
	WarningRef          string             `json:"warning_ref"`
	ProviderMode        string             `json:"provider_mode,omitempty"`
	ReaderRevision      int                `json:"reader_revision,omitempty"`
	CheckpointRevision  int64              `json:"checkpoint_revision"`
	Stage               string             `json:"stage"`
	Scope               string             `json:"scope"`
	ErrorCode           string             `json:"error_code"`
	State               string             `json:"state"`
	IntervalStart       time.Time          `json:"interval_start,omitempty"`
	IntervalEnd         time.Time          `json:"interval_end,omitempty"`
	ObservedAt          time.Time          `json:"observed_at,omitempty"`
	ConsecutiveFailures int                `json:"consecutive_failures,omitempty"`
	ConfirmationCount   int                `json:"confirmation_count,omitempty"`
	FirstFailedAt       time.Time          `json:"first_failed_at"`
	LastFailedAt        time.Time          `json:"last_failed_at"`
	LastAttemptAt       time.Time          `json:"last_attempt_at"`
	ResolvedAt          *time.Time         `json:"resolved_at,omitempty"`
	SuppressedAt        *time.Time         `json:"suppressed_at,omitempty"`
	ResolutionMethod    string             `json:"resolution_method,omitempty"`
	ResolutionActor     string             `json:"resolution_actor,omitempty"`
	AcknowledgedAt      *time.Time         `json:"acknowledged_at,omitempty"`
	AcknowledgedBy      string             `json:"acknowledged_by,omitempty"`
	Attempts            []EmailSyncAttempt `json:"attempts,omitempty"`
}

// EmailSyncWarning is the redacted owner-facing form of EmailSyncFailure.
type EmailSyncWarning struct {
	ID             string     `json:"id"`
	MailboxID      string     `json:"mailbox_id"`
	WarningRef     string     `json:"warning_ref"`
	Stage          string     `json:"stage"`
	Scope          string     `json:"scope"`
	ErrorCode      string     `json:"error_code"`
	State          string     `json:"state"`
	ObservedAt     time.Time  `json:"observed_at,omitempty"`
	AttemptCount   int        `json:"attempt_count"`
	FirstFailedAt  time.Time  `json:"first_failed_at"`
	LastAttemptAt  time.Time  `json:"last_attempt_at"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
}

// EmailCaptureVersion points at an immutable capture on local disk. PurgedAt
// records that the user manually cleaned up those bytes: the pointer and its
// hashes are deliberately retained as the evidence a reaper needs, while
// PurgedAt is the single authority for "these bytes must no longer exist". It
// is a nullable field rather than a fourth State so that a purged mail stays
// "captured" and the capture job does not immediately download it again.
type EmailCaptureVersion struct {
	ManifestJSON   string     `json:"manifest_json,omitempty"`
	PurgedAt       *time.Time `json:"purged_at,omitempty"`
	ID             string     `json:"id"`
	MailID         string     `json:"mail_id"`
	ManifestPath   string     `json:"manifest_path"`
	ManifestSHA256 string     `json:"manifest_sha256"`
	OriginalPath   string     `json:"original_path"`
	OriginalSHA256 string     `json:"original_sha256"`
	State          string     `json:"state"`
	PurgeReason    string     `json:"purge_reason,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
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
	Language         string    `json:"language,omitempty"`
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
	RoundStartedAt    time.Time     `json:"round_started_at,omitempty"`
	RoundFinishedAt   time.Time     `json:"round_finished_at,omitempty"`
	PollInterval      time.Duration `json:"poll_interval,omitempty"`
	RefreshRequestID  string        `json:"refresh_request_id,omitempty"`
	RefreshPending    bool          `json:"refresh_pending,omitempty"`
	RefreshActiveID   string        `json:"refresh_active_id,omitempty"`
	RetiredAt         *time.Time    `json:"retired_at,omitempty"`
	RetiredFromState  string        `json:"retired_from_state,omitempty"`
	RetirementReason  string        `json:"retirement_reason,omitempty"`
	SyncTrigger       string        `json:"sync_trigger,omitempty"`
	SyncActor         string        `json:"sync_actor,omitempty"`
	Priority          string        `json:"priority,omitempty"`
	ID                string        `json:"id"`
	OwnerID           string        `json:"owner_id"`
	Kind              string        `json:"kind"`
	TargetID          string        `json:"target_id"`
	MailboxID         string        `json:"mailbox_id,omitempty"`
	BindingGeneration int64         `json:"binding_generation,omitempty"`
	InputFingerprint  string        `json:"input_fingerprint,omitempty"`
	Generation        int64         `json:"generation"`
	State             string        `json:"state"`
	Attempt           int           `json:"attempt"`
	MaxAttempts       int           `json:"max_attempts"`
	LeaseToken        string        `json:"lease_token,omitempty"`
	LeaseExpiresAt    time.Time     `json:"lease_expires_at,omitempty"`
	NextAttemptAt     time.Time     `json:"next_attempt_at"`
	ErrorCode         string        `json:"error_code,omitempty"`
	CreatedAt         time.Time     `json:"created_at"`
	UpdatedAt         time.Time     `json:"updated_at"`
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
	EmailJobCapture             = "capture"
	EmailJobMarkRead            = "mark_read"
	EmailJobParse               = "parse"
	EmailJobMessageSummary      = "message_summary"
	EmailJobAssignment          = "assignment"
	EmailJobRelationshipCheck   = "relationship_check"
	EmailJobConversationSummary = "conversation_summary"
	EmailJobDiscover            = "discover"
	EmailJobThreadSync          = "thread_sync"
	// EmailJobSourceRecovery reconciles local capture files against the database:
	// finishing interrupted cleanups, adopting bytes that landed before their
	// transaction committed, and sweeping abandoned staging. It never downloads.
	EmailJobSourceRecovery    = "source_recovery"
	EmailJobQueued            = "queued"
	EmailJobRunning           = "running"
	EmailJobRetryWait         = "retry_wait"
	EmailJobSucceeded         = "succeeded"
	EmailJobFailed            = "failed"
	EmailJobPaused            = "paused"
	EmailJobRetired           = "retired"
	EmailCaptureComplete      = "complete"
	EmailCaptureSourceMissing = "source_missing"
	EmailCapturePartial       = "partial"
	EmailCaptureFailed        = "failed"
	// EmailAttachmentPurged joins the manifest-derived attachment states
	// ("available", "skipped_limit", "failed") once the local bytes are gone.
	EmailAttachmentPurged          = "purged"
	EmailPurgeManual               = "manual_cleanup"
	EmailPurgeLegacy               = "legacy_cutover"
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
