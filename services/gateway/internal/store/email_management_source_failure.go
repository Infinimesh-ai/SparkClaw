package store

import (
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func emailSameSourceHash(a, b string) bool {
	return strings.TrimPrefix(a, "sha256:") == strings.TrimPrefix(b, "sha256:")
}

func emailReportSourceFailure(e *emailEngine, c EmailSourceFailureCommand) (app.EmailMail, error) {
	m, err := emailMail(e, c.MailID)
	if err != nil {
		return m, err
	}
	box, err := emailMailbox(e, c.MailboxID)
	if err != nil {
		return m, err
	}
	// Integrity reporting is local-only and must work while intake is paused.
	// The binding CAS still prevents a stale observer from changing a rebound
	// mailbox; this command never enables intake or schedules browser work.
	if box.BindingGeneration != c.BindingGeneration {
		return m, errEmailConflict
	}
	if m.MailboxID != box.ID || c.ExpectedCaptureID == "" || !emailHashValid(c.ExpectedOriginalSHA256) || !containsEmail([]string{"email_source_missing", "email_source_corrupt"}, c.ErrorCode) {
		return m, errEmailInvalid
	}
	if m.CaptureID != c.ExpectedCaptureID || m.SyncState == app.EmailMailSyncSuppressed {
		return m, errEmailConflict
	}
	prior, ok := emailGet[app.EmailCaptureVersion](e, "capture", m.CaptureID)
	if !ok || prior.MailID != m.ID {
		return m, errEmailCorrupt
	}
	if prior.PurgedAt != nil || !emailSameSourceHash(prior.OriginalSHA256, c.ExpectedOriginalSHA256) {
		return m, errEmailConflict
	}
	id := emailFailureID(box, EmailSyncFailureOutcome{ProviderMessageID: m.ProviderMessageID})
	failure, exists := emailGet[app.EmailSyncFailure](e, "sync_failure", id)
	if exists && failure.State == app.EmailSyncFailureSuppressed {
		return m, errEmailConflict
	}
	if !exists {
		failure = app.EmailSyncFailure{ID: id, OwnerID: e.owner, MailboxID: box.ID, BindingGeneration: box.BindingGeneration, MailID: m.ID, ProviderMessageID: m.ProviderMessageID, WarningRef: "email-sync-" + id[:12], FirstFailedAt: e.now}
	}
	if !exists || failure.State != app.EmailSyncFailureOpen {
		box.PendingFailureCount++
		// A resolved episode must not contribute stale consecutive failures to
		// a later operational incident; preserve its attempts as audit only.
		failure.ConsecutiveFailures = m.QualifiedFailureCount
	}
	failure.BindingGeneration = box.BindingGeneration
	failure.ProviderMode = box.ProviderMode
	failure.Stage, failure.Scope, failure.ErrorCode = "source_recovery", app.EmailSyncFailureLocalOperational, c.ErrorCode
	failure.State = app.EmailSyncFailureOpen
	failure.LastFailedAt, failure.LastAttemptAt = e.now, e.now
	failure.ResolvedAt, failure.ResolutionMethod, failure.ResolutionActor = nil, "", ""
	emailAppendSyncAttempt(&failure, app.EmailSyncAttempt{Trigger: "source_recovery", Actor: "system", InvocationID: c.CommandKey, CompletedAt: e.now, Outcome: "failed", ErrorCode: c.ErrorCode})
	m.CaptureState, m.SyncState, m.LastSyncErrorCode = app.EmailCaptureSourceMissing, app.EmailMailSyncRetry, c.ErrorCode
	// Keep CaptureID and the immutable version unchanged: subsequent capture
	// admission must prove it restored these exact bytes, not a new message.
	emailSaveMail(e, m)
	emailSaveSyncFailure(e, failure)
	box.UpdatedAt = e.now
	emailSaveMailbox(e, box)
	return m, e.err
}

// Explicit cleanup wins over an already queued operational repair.
func emailCancelSourceRepair(e *emailEngine, m *app.EmailMail, reason string) error {
	box, err := emailMailbox(e, m.MailboxID)
	if err != nil {
		return err
	}
	id := emailFailureID(box, EmailSyncFailureOutcome{ProviderMessageID: m.ProviderMessageID})
	failure, exists := emailGet[app.EmailSyncFailure](e, "sync_failure", id)
	if exists && failure.State == app.EmailSyncFailureOpen {
		failure.State = app.EmailSyncFailureResolved
		at := e.now
		failure.ResolvedAt, failure.ResolutionMethod, failure.ResolutionActor = &at, reason, e.owner
		emailSaveSyncFailure(e, failure)
		if box.PendingFailureCount > 0 {
			box.PendingFailureCount--
		}
		emailSaveMailbox(e, box)
	}
	m.CaptureState = app.EmailCaptureComplete
	if m.SyncState != app.EmailMailSyncSuppressed {
		m.SyncState = app.EmailMailSyncComplete
	}
	m.LastSyncErrorCode = ""
	return e.err
}
