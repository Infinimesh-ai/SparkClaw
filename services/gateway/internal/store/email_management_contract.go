package store

import (
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// Email commands require an owner and stable command key. Reuse a key only for
// identical content. A receipt permits reconciliation after a lost response.
type EmailCommand struct {
	OwnerID    string
	CommandKey string
}
type EmailBindCommand struct {
	EmailCommand
	Provider string
	Address  string
	Enabled  bool
	// ExpectedVersion is the owner/provider selected binding version, including
	// its paused state; it is not the replacement mailbox historical version.
	ExpectedVersion int64
	Boundary        time.Time
}
type EmailPauseCommand struct {
	EmailCommand
	MailboxID         string
	BindingGeneration int64
	ErrorCode         string
}
type EmailDiscoveryMember struct {
	ProviderMessageID   string
	ProviderSelectionID string
	ProviderThreadID    string
	Direction           string
	Folder              string
	Reason              string
	RemoteReadState     string
	SourceTime          time.Time
	Draft               bool
}
type EmailDiscoveryCommand struct {
	// PageBatch admits records whose source receipts are published under the
	// same discover lease, without separate per-message browser jobs.
	PageBatch bool `json:",omitempty"`
	// AcknowledgedPageID is an ack-only command: receipts must already be
	// durable, and this does not change discovery cursors or coverage.
	AcknowledgedPageID  string `json:",omitempty"`
	Lease               EmailJobLease
	MaxPendingJobs      int
	ProviderSelectionID string
	EmailCommand
	MailboxID         string
	BindingGeneration int64
	Members           []EmailDiscoveryMember
	Cursor            string
	Coverage          string
	Trigger           string
	Gaps              []string
	ObservedAt        time.Time
	CompletedBoundary time.Time
	ThreadID          string
	Folder            string
}
type EmailDiscoveryAdmission struct {
	Mails []app.EmailMail  `json:"mails"`
	Run   app.EmailSyncRun `json:"run"`
}
type EmailJobRequest struct {
	// ForceAnalysis explicitly regenerates a succeeded semantic target on user request.
	ForceAnalysis bool `json:",omitempty"`
	EmailCommand
	Kind              string
	TargetID          string
	MailboxID         string
	BindingGeneration int64
	Dependencies      []string
	Rearm             bool
	NextAttemptAt     time.Time
	// RepeatInterval is an optional background polling interval, at most one
	// day. A succeeded job rearms no earlier than its completion plus this
	// interval; failed jobs require an explicit rearm with a zero interval.
	// Active/queued/retry-wait work retains its existing schedule and lease.
	RepeatInterval time.Duration `json:",omitempty"`
}
type EmailJobClaim struct {
	OwnerID       string
	Kinds         []string
	Now           time.Time
	LeaseDuration time.Duration
}
type EmailJobLease struct {
	OwnerID    string
	JobID      string
	LeaseToken string
	Now        time.Time
}
type EmailJobRenew struct {
	EmailJobLease
	LeaseDuration time.Duration
}
type EmailJobFinish struct {
	EmailJobLease
	ErrorCode string
	RetryAt   time.Time
}
type EmailCaptureCommand struct {
	PageBatch bool   `json:",omitempty"`
	ReadState string `json:",omitempty"`
	EmailCommand
	MailboxID         string
	BindingGeneration int64
	Lease             EmailJobLease
	Capture           app.EmailCaptureVersion
}
type EmailRepresentationCommand struct {
	EmailCommand
	Lease          EmailJobLease
	Representation app.EmailRepresentation
}
type EmailContextCommand struct {
	EmailCommand
	Context app.EmailContextVersion
}
type EmailAssignmentCommand struct {
	EmailCommand
	Lease                    EmailJobLease
	Decision                 app.EmailAssignmentDecision
	CandidateConversationIDs []string
	Generation               int64
}
type EmailConcernCommand struct {
	EmailCommand
	Lease      EmailJobLease
	Concern    app.EmailAssignmentConcern
	TargetID   string
	Generation int64
}
type EmailSummaryCommand struct {
	EmailCommand
	Lease   EmailJobLease
	Summary app.EmailSummary
}
type EmailViewedCommand struct {
	EmailCommand
	MailIDs  []string
	ViewedAt time.Time
}
type EmailRefreshCommand struct {
	EmailCommand
	Limit int
}
type EmailRefreshResult struct {
	Processed int  `json:"processed"`
	Remaining bool `json:"remaining"`
}
type EmailQuery struct {
	CursorKind           string
	Direction            string
	RequireNativeCapture bool
	Validity             string
	AsOf                 time.Time
	Entry                string
	NotificationSubtype  string
	UnassignedOnly       bool
	OwnerID              string
	MailboxID            string
	ConversationID       string
	PendingOnly          bool
	CapturedOnly         bool
	Search               string
	After                string
	Limit                int
}
type EmailMailPage struct {
	Counts     *EmailScopeCounts `json:"counts,omitempty"`
	ServerNow  time.Time         `json:"server_now"`
	Items      []app.EmailMail   `json:"items"`
	NextCursor string            `json:"next_cursor,omitempty"`
}
type EmailConversationPage struct {
	// Counts covers all matching captured interaction mails, including unassigned.
	Counts     *EmailScopeCounts       `json:"counts,omitempty"`
	Items      []app.EmailConversation `json:"items"`
	NextCursor string                  `json:"next_cursor,omitempty"`
}
type EmailCandidateQuery struct {
	OwnerID string
	MailID  string
	Limit   int
}
type EmailCandidateSet struct {
	OwnerEpoch    int64                   `json:"owner_epoch"`
	Conversations []app.EmailConversation `json:"conversations"`
	RelatedMails  []app.EmailMail         `json:"related_mails"`
}
type EmailCommandReceipt struct {
	Key         string    `json:"key"`
	Operation   string    `json:"operation"`
	ContentHash string    `json:"content_hash"`
	Result      []byte    `json:"result"`
	CommittedAt time.Time `json:"committed_at"`
}

var (
	_ EmailRepository = (*MemoryStore)(nil)
	_ EmailRepository = (*FileStore)(nil)
	_ EmailRepository = (*PostgresStore)(nil)
)

type EmailScopeCounts struct {
	Total  int `json:"total"`
	Unseen int `json:"unseen"`
}
