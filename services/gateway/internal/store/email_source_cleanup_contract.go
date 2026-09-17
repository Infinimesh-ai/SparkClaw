package store

import (
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// Manual cleanup scopes. The caller names what to clean up; the engine resolves
// it to capture versions through owner-scoped queries, never through the
// filesystem, so authorization always derives from the database.
const (
	EmailPurgeScopeMail       = "mail"
	EmailPurgeScopeDate       = "date"
	EmailPurgeScopeMailbox    = "mailbox"
	EmailPurgeScopeAll        = "all"
	EmailPurgeScopeCaptureIDs = "capture_ids"
)

// EmailCapturePurgeCommand tombstones capture versions so their local bytes can
// then be unlinked. It never deletes mail metadata, representations, or bodies.
type EmailCapturePurgeCommand struct {
	At time.Time
	EmailCommand
	Scope      string
	MailID     string
	MailboxID  string
	DatePath   string // "YYYY/MM/DD"
	Reason     string
	After      string
	CaptureIDs []string
	Limit      int
	// Finalize marks already-tombstoned captures as reaped once their bytes are
	// gone, so the recovery scan stops returning them. It never tombstones.
	Finalize bool
}

// EmailCaptureScan reads capture pointers for reconciliation. State "purged"
// selects tombstones still awaiting an unlink; "" selects live captures, which
// is how the cutover sweep finds pointers predating the date layout.
type EmailCaptureScan struct {
	OwnerID string
	State   string
	After   string
	Limit   int
}

// EmailCaptureAdoption registers a capture whose bytes are already on disk but
// whose transaction never committed. Unlike PublishEmailCapture it carries no
// job lease, and is correspondingly narrower: it can only fill a hole in a mail
// that has no capture yet, never replace or supersede a committed one.
type EmailCaptureAdoption struct {
	EmailCommand
	MailboxID         string
	BindingGeneration int64
	Capture           app.EmailCaptureVersion
}

// EmailCapturePurgeResult returns the tombstoned versions with their paths and
// hashes intact, because those are exactly what the caller needs to unlink.
type EmailCapturePurgeResult struct {
	NextCursor string                    `json:"next_cursor,omitempty"`
	Captures   []app.EmailCaptureVersion `json:"captures"`
	Remaining  bool                      `json:"remaining"`
}
