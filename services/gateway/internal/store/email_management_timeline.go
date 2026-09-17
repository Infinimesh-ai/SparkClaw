package store

import (
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const emailSyncRetryLimit = 50

func emailQualifiedProviderMode(mode string) bool {
	return mode == app.EmailProviderModeChangeCursor || mode == app.EmailProviderModeTimeRange
}

func emailSyncScope(scope string) bool {
	return scope == app.EmailSyncFailureMailSpecific || scope == app.EmailSyncFailureProviderOperational || scope == app.EmailSyncFailureLocalOperational
}

func emailSyncMailbox(e *emailEngine, id string, generation int64, mode string) (app.EmailMailbox, error) {
	box, err := emailBound(e, id, generation)
	if err != nil {
		return box, err
	}
	if !emailQualifiedProviderMode(mode) {
		box.ProviderMode = app.EmailProviderModeUnqualified
		box.SyncState = app.EmailSyncUnqualified
		box.LastSyncErrorCode = "email_incremental_unqualified"
		box.UpdatedAt = e.now
		emailSaveMailbox(e, box)
		return box, errEmailInvalid
	}
	if box.ScopeVersion == "" {
		anchor := box.Boundary
		if anchor.IsZero() {
			anchor = box.ActivatedAt
		}
		if anchor.IsZero() {
			anchor = e.now
		}
		box.ScopeVersion = app.EmailSyncScopeTimelineV2
		box.DeploymentAnchor = postgresTime(anchor)
		box.DiscoveredThrough = postgresTime(anchor)
		box.PollThrough = postgresTime(anchor)
		box.ProviderCursor = ""
		box.Cursor = ""
		box.PageAcks = nil
		box.CheckpointRevision++
	}
	box.ProviderMode = mode
	return box, nil
}

func emailBeginSync(e *emailEngine, c EmailSyncBeginCommand) (EmailSyncCheckpoint, error) {
	out := EmailSyncCheckpoint{RetryFailures: []app.EmailSyncFailure{}}
	if c.UpperBound.IsZero() || !containsEmail([]string{"scheduled", "manual_refresh", "test"}, c.Trigger) || strings.TrimSpace(c.Actor) == "" {
		return out, errEmailInvalid
	}
	box, err := emailSyncMailbox(e, c.MailboxID, c.BindingGeneration, c.ProviderMode)
	if err != nil {
		return out, err
	}
	if box.PollThrough.IsZero() || !c.UpperBound.After(box.PollThrough) {
		return out, errEmailInvalid
	}
	confirmations := emailList[app.EmailSyncFailure](e, emailRowsQuery{Kind: "sync_failure", Parent: box.ID, State: app.EmailSyncFailureOverflowConfirmation, Limit: 2})
	if e.err != nil || len(confirmations) > 1 {
		if e.err == nil {
			e.err = errEmailCorrupt
		}
		return out, e.err
	}
	start, end := box.PollThrough, postgresTime(c.UpperBound)
	if len(confirmations) == 1 {
		start, end = confirmations[0].IntervalStart, confirmations[0].IntervalEnd
		out.ConfirmingOverflow = true
	} else if !box.InflightUntil.IsZero() {
		// A process may have lost the Begin response. Reuse the already durable
		// upper bound instead of widening or conflicting with the unfinished round.
		end = box.InflightUntil
	}
	if box.InflightUntil.IsZero() {
		box.InflightUntil = end
		box.CheckpointRevision++
	}
	box.ProviderCursor = c.ProviderCursor
	box.SyncState = app.EmailSyncRunning
	box.LastSyncErrorCode = ""
	box.UpdatedAt = e.now
	emailSaveMailbox(e, box)

	// Only open exact-ID failures are scheduler work. Terminal warning rows use
	// different states and are therefore never loaded or scanned here.
	open := emailList[app.EmailSyncFailure](e, emailRowsQuery{Kind: "sync_failure", Parent: box.ID, State: app.EmailSyncFailureOpen, Limit: 100})
	for _, failure := range open {
		if failure.ProviderMessageID == "" || failure.MailID == "" {
			continue
		}
		out.RetryFailures = append(out.RetryFailures, failure)
		if len(out.RetryFailures) == emailSyncRetryLimit {
			break
		}
	}
	out.Mailbox, out.IntervalStart, out.IntervalEnd = box, start, end
	return out, e.err
}

func emailFailureID(box app.EmailMailbox, outcome EmailSyncFailureOutcome) string {
	return emailID(box.ID, "mail", outcome.ProviderMessageID)
}

func emailFailureOrder(f app.EmailSyncFailure) string {
	return emailOrder(f.LastAttemptAt, f.ID)
}

func emailSaveSyncFailure(e *emailEngine, failure app.EmailSyncFailure) {
	emailPut(e, "sync_failure", failure.ID, failure.MailboxID, failure.MailID, failure.State, failure.WarningRef, emailFailureOrder(failure), failure)
}

func emailSyncAttempt(c EmailSyncCommitCommand, at time.Time, outcome, code string) app.EmailSyncAttempt {
	actor := strings.TrimSpace(c.Actor)
	if actor == "" {
		actor = "system"
	}
	return app.EmailSyncAttempt{Trigger: c.Trigger, Actor: actor, InvocationID: c.InvocationID, CompletedAt: at, Outcome: outcome, ErrorCode: code}
}

func emailAppendSyncAttempt(failure *app.EmailSyncFailure, attempt app.EmailSyncAttempt) {
	// A mail has at most two qualified attempts. Operational incidents can be
	// longer-lived, so retain a bounded recent audit without growing hot rows.
	if len(failure.Attempts) == 16 {
		copy(failure.Attempts, failure.Attempts[1:])
		failure.Attempts = failure.Attempts[:15]
	}
	failure.Attempts = append(failure.Attempts, attempt)
}

func emailCommitSync(e *emailEngine, c EmailSyncCommitCommand) (EmailSyncCommitResult, error) {
	out := EmailSyncCommitResult{Failures: []app.EmailSyncFailure{}}
	if c.IntervalStart.IsZero() || c.IntervalEnd.IsZero() || !c.IntervalEnd.After(c.IntervalStart) || len(c.Outcomes) > 100 || c.UnsupportedItems < 0 || !containsEmail([]string{"scheduled", "manual_refresh", "test"}, c.Trigger) {
		return out, errEmailInvalid
	}
	box, err := emailSyncMailbox(e, c.MailboxID, c.BindingGeneration, c.ProviderMode)
	if err != nil {
		return out, err
	}
	if !box.PollThrough.Equal(postgresTime(c.IntervalStart)) || !box.InflightUntil.Equal(postgresTime(c.IntervalEnd)) {
		return out, errEmailConflict
	}
	if len(c.Members) > 50 || len(c.Captures) > 100 {
		return out, errEmailInvalid
	}
	if len(c.Members) > 0 {
		_, err = emailAdmit(e, EmailDiscoveryCommand{MailboxID: box.ID, BindingGeneration: box.BindingGeneration, Lease: c.Lease, PageBatch: true, MaxPendingJobs: 1000, ObservedAt: c.ObservedAt, Coverage: "partial", Trigger: "recent_inbound", Members: c.Members})
		if err != nil {
			return out, err
		}
	}
	for _, captured := range c.Captures {
		version := captured.Capture
		version.MailID = emailID(e.owner, box.ID, captured.ProviderMessageID)
		mail, exists := emailGet[app.EmailMail](e, "mail", version.MailID)
		if !exists || mail.MailboxID != box.ID {
			return out, errEmailInvalid
		}
		if mail.SyncState == app.EmailMailSyncSuppressed {
			continue
		}
		_, err = emailCapture(e, EmailCaptureCommand{MailboxID: box.ID, BindingGeneration: box.BindingGeneration, Lease: c.Lease, PageBatch: true, Capture: version, ReadState: captured.ReadState})
		if err != nil {
			return out, err
		}
	}

	// Two IDs failing at the same purportedly mail-specific stage/error in one
	// batch indicate a shared adapter/provider problem, not two bad messages.
	cohorts := map[string]int{}
	seenOutcomes := map[string]bool{}
	for _, outcome := range c.Outcomes {
		if seenOutcomes[outcome.ProviderMessageID] {
			return out, errEmailInvalid
		}
		seenOutcomes[outcome.ProviderMessageID] = true
		if !outcome.Success && outcome.Qualified && outcome.Scope == app.EmailSyncFailureMailSpecific {
			cohorts[outcome.Stage+"\x00"+outcome.ErrorCode]++
		}
	}
	for index := range c.Outcomes {
		outcome := c.Outcomes[index]
		if strings.TrimSpace(outcome.ProviderMessageID) == "" || strings.TrimSpace(outcome.Stage) == "" || (!outcome.Success && (strings.TrimSpace(outcome.ErrorCode) == "" || !emailSyncScope(outcome.Scope))) {
			return out, errEmailInvalid
		}
		if cohorts[outcome.Stage+"\x00"+outcome.ErrorCode] >= 2 {
			outcome.Scope = app.EmailSyncFailureProviderOperational
			outcome.Qualified = false
		}
		failureID := emailFailureID(box, outcome)
		failure, exists := emailGet[app.EmailSyncFailure](e, "sync_failure", failureID)
		if e.err != nil {
			return out, e.err
		}
		mailID := outcome.MailID
		if mailID == "" {
			mailID = emailID(e.owner, box.ID, outcome.ProviderMessageID)
		}
		mail, mailExists := emailGet[app.EmailMail](e, "mail", mailID)
		if !mailExists || mail.MailboxID != box.ID || mail.ProviderMessageID != outcome.ProviderMessageID {
			return out, errEmailInvalid
		}
		if exists && (failure.MailboxID != box.ID || failure.ProviderMessageID != outcome.ProviderMessageID) {
			return out, errEmailCorrupt
		}
		if mail.SyncState == app.EmailMailSyncSuppressed {
			continue
		}
		if outcome.Success {
			if exists && failure.State == app.EmailSyncFailureOpen {
				failure.State = app.EmailSyncFailureResolved
				resolvedAt := e.now
				failure.ResolvedAt = &resolvedAt
				failure.ResolutionMethod = c.Trigger
				failure.ResolutionActor = c.Actor
				failure.LastAttemptAt = e.now
				emailAppendSyncAttempt(&failure, emailSyncAttempt(c, e.now, "resolved", ""))
				emailSaveSyncFailure(e, failure)
				if box.PendingFailureCount > 0 {
					box.PendingFailureCount--
				}
				out.Resolved++
			}
			mail.QualifiedFailureCount = 0
			mail.LastSyncErrorCode = ""
			if mail.SyncState != app.EmailMailSyncSuppressed {
				mail.SyncState = app.EmailMailSyncComplete
			}
			emailSaveMail(e, mail)
			continue
		}
		if !exists || failure.State == app.EmailSyncFailureResolved {
			failure = app.EmailSyncFailure{ID: failureID, OwnerID: e.owner, MailboxID: box.ID, BindingGeneration: box.BindingGeneration, MailID: mail.ID, ProviderMessageID: outcome.ProviderMessageID, WarningRef: "email-sync-" + failureID[:12], FirstFailedAt: e.now}
			box.PendingFailureCount++
		}
		if failure.State == app.EmailSyncFailureSuppressed {
			continue
		}
		failure.ProviderMode = c.ProviderMode
		failure.ReaderRevision = c.ReaderRevision
		failure.CheckpointRevision = box.CheckpointRevision
		failure.Stage = outcome.Stage
		failure.Scope = outcome.Scope
		failure.ErrorCode = outcome.ErrorCode
		failure.State = app.EmailSyncFailureOpen
		failure.ObservedAt = postgresTime(outcome.ObservedAt)
		failure.LastFailedAt = e.now
		failure.LastAttemptAt = e.now
		failure.ResolvedAt = nil
		failure.ResolutionMethod = ""
		failure.ResolutionActor = ""
		if outcome.Scope == app.EmailSyncFailureMailSpecific && outcome.Qualified {
			failure.ConsecutiveFailures++
			mail.QualifiedFailureCount = failure.ConsecutiveFailures
		}
		emailAppendSyncAttempt(&failure, emailSyncAttempt(c, e.now, "failed", outcome.ErrorCode))
		if failure.ConsecutiveFailures >= 2 {
			failure.State = app.EmailSyncFailureSuppressed
			suppressedAt := e.now
			failure.SuppressedAt = &suppressedAt
			failure.ResolutionMethod = "two_failure_suppression"
			failure.ResolutionActor = "system"
			mail.SyncState = app.EmailMailSyncSuppressed
			box.PendingFailureCount--
			box.SuppressedMailCount++
			box.UnacknowledgedWarningCount++
			out.Suppressed++
		} else {
			mail.SyncState = app.EmailMailSyncRetry
		}
		mail.LastSyncErrorCode = outcome.ErrorCode
		emailSaveMail(e, mail)
		emailSaveSyncFailure(e, failure)
		out.Failures = append(out.Failures, failure)
	}

	if c.Overflow {
		id := emailID(box.ID, "overflow", c.IntervalStart.UTC().Format(time.RFC3339Nano), c.IntervalEnd.UTC().Format(time.RFC3339Nano))
		failure, exists := emailGet[app.EmailSyncFailure](e, "sync_failure", id)
		if !exists {
			failure = app.EmailSyncFailure{ID: id, OwnerID: e.owner, MailboxID: box.ID, BindingGeneration: box.BindingGeneration, WarningRef: "email-gap-" + id[:12], ProviderMode: c.ProviderMode, CheckpointRevision: box.CheckpointRevision, Stage: "overflow", Scope: app.EmailSyncFailureProviderOperational, ErrorCode: "email_interval_overflow", State: app.EmailSyncFailureOverflowConfirmation, IntervalStart: postgresTime(c.IntervalStart), IntervalEnd: postgresTime(c.IntervalEnd), ConfirmationCount: 1, FirstFailedAt: e.now, LastFailedAt: e.now, LastAttemptAt: e.now}
			box.SyncState = app.EmailSyncOverflowConfirmation
		} else if failure.State == app.EmailSyncFailureOverflowConfirmation {
			failure.State = app.EmailSyncFailureCoverageGap
			failure.ConfirmationCount++
			failure.LastFailedAt = e.now
			failure.LastAttemptAt = e.now
			box.CoverageGapCount++
			box.UnacknowledgedWarningCount++
			box.PollThrough = postgresTime(c.IntervalEnd)
			box.SyncState = app.EmailSyncCoverageGap
		}
		emailAppendSyncAttempt(&failure, emailSyncAttempt(c, e.now, "overflow", failure.ErrorCode))
		emailSaveSyncFailure(e, failure)
		out.Failures = append(out.Failures, failure)
		box.InflightUntil = time.Time{}
		box.LastSyncErrorCode = failure.ErrorCode
	} else if c.Complete && c.UnsupportedItems == 0 {
		// A successful confirmation closes the frozen overflow record as well
		// as advancing the watermark. Otherwise Begin would select it forever.
		overflowID := emailID(box.ID, "overflow", c.IntervalStart.UTC().Format(time.RFC3339Nano), c.IntervalEnd.UTC().Format(time.RFC3339Nano))
		if failure, ok := emailGet[app.EmailSyncFailure](e, "sync_failure", overflowID); ok && failure.State == app.EmailSyncFailureOverflowConfirmation {
			failure.State = app.EmailSyncFailureResolved
			resolvedAt := e.now
			failure.ResolvedAt = &resolvedAt
			failure.LastAttemptAt = e.now
			failure.ResolutionMethod = c.Trigger
			failure.ResolutionActor = c.Actor
			emailAppendSyncAttempt(&failure, emailSyncAttempt(c, e.now, "resolved", ""))
			emailSaveSyncFailure(e, failure)
		}
		box.PollThrough = postgresTime(c.IntervalEnd)
		if box.CoverageGapCount == 0 && box.DiscoveredThrough.Equal(postgresTime(c.IntervalStart)) {
			box.DiscoveredThrough = postgresTime(c.IntervalEnd)
		}
		box.ProviderCursor = c.ProviderCursor
		box.InflightUntil = time.Time{}
		box.LastSyncErrorCode = ""
		if box.CoverageGapCount > 0 {
			box.SyncState = app.EmailSyncCoverageGap
		} else {
			box.SyncState = app.EmailSyncIdle
		}
		// A previous range-level list failure shares this unchanged lower bound.
		// Resolve it when a later combined range is proved complete.
		rangeID := emailID(box.ID, "range", "list", c.IntervalStart.UTC().Format(time.RFC3339Nano))
		if failure, ok := emailGet[app.EmailSyncFailure](e, "sync_failure", rangeID); ok && failure.State == app.EmailSyncFailureOpen {
			failure.State = app.EmailSyncFailureResolved
			resolvedAt := e.now
			failure.ResolvedAt = &resolvedAt
			failure.LastAttemptAt = e.now
			failure.ResolutionMethod = c.Trigger
			failure.ResolutionActor = c.Actor
			emailAppendSyncAttempt(&failure, emailSyncAttempt(c, e.now, "resolved", ""))
			emailSaveSyncFailure(e, failure)
		}
	} else {
		box.InflightUntil = time.Time{}
		box.SyncState = app.EmailSyncIncomplete
		box.LastSyncErrorCode = c.ErrorCode
		id := emailID(box.ID, "range", "list", c.IntervalStart.UTC().Format(time.RFC3339Nano))
		failure, exists := emailGet[app.EmailSyncFailure](e, "sync_failure", id)
		if !exists {
			failure = app.EmailSyncFailure{ID: id, OwnerID: e.owner, MailboxID: box.ID, BindingGeneration: box.BindingGeneration, WarningRef: "email-list-" + id[:12], FirstFailedAt: e.now}
		}
		failure.ProviderMode = c.ProviderMode
		failure.ReaderRevision = c.ReaderRevision
		failure.CheckpointRevision = box.CheckpointRevision
		failure.Stage = "list"
		failure.Scope = c.FailureScope
		if !emailSyncScope(failure.Scope) || failure.Scope == app.EmailSyncFailureMailSpecific {
			failure.Scope = app.EmailSyncFailureProviderOperational
		}
		failure.ErrorCode = c.ErrorCode
		if failure.ErrorCode == "" {
			failure.ErrorCode = "email_list_incomplete"
		}
		failure.State = app.EmailSyncFailureOpen
		failure.IntervalStart = postgresTime(c.IntervalStart)
		failure.IntervalEnd = postgresTime(c.IntervalEnd)
		failure.LastFailedAt = e.now
		failure.LastAttemptAt = e.now
		emailAppendSyncAttempt(&failure, emailSyncAttempt(c, e.now, "failed", failure.ErrorCode))
		emailSaveSyncFailure(e, failure)
		out.Failures = append(out.Failures, failure)
	}
	box.LastCheckedAt = e.now
	box.CheckpointRevision++
	box.UpdatedAt = e.now
	emailSaveMailbox(e, box)
	out.Mailbox = box
	return out, e.err
}

func emailSyncWarnings(e *emailEngine, q EmailQuery) (EmailSyncWarningPage, error) {
	limit := emailLimit(q.Limit)
	rows := emailList[app.EmailSyncFailure](e, emailRowsQuery{Kind: "sync_failure", Parent: q.MailboxID, States: []string{app.EmailSyncFailureSuppressed, app.EmailSyncFailureCoverageGap}, After: q.After, Limit: limit + 1})
	out := EmailSyncWarningPage{Items: []app.EmailSyncWarning{}}
	for index, failure := range rows {
		if index == limit {
			out.NextCursor = emailFailureOrder(rows[index-1])
			break
		}
		out.Items = append(out.Items, app.EmailSyncWarning{ID: failure.ID, MailboxID: failure.MailboxID, WarningRef: failure.WarningRef, Stage: failure.Stage, Scope: failure.Scope, ErrorCode: failure.ErrorCode, State: failure.State, ObservedAt: failure.ObservedAt, AttemptCount: len(failure.Attempts), FirstFailedAt: failure.FirstFailedAt, LastAttemptAt: failure.LastAttemptAt, AcknowledgedAt: failure.AcknowledgedAt})
	}
	return out, e.err
}

func emailAcknowledgeSyncWarning(e *emailEngine, c EmailSyncWarningAck) (app.EmailSyncWarning, error) {
	failure, ok := emailGet[app.EmailSyncFailure](e, "sync_failure", c.FailureID)
	if !ok {
		return app.EmailSyncWarning{}, errEmailNotFound
	}
	if failure.MailboxID != c.MailboxID || !containsEmail([]string{app.EmailSyncFailureSuppressed, app.EmailSyncFailureCoverageGap}, failure.State) || strings.TrimSpace(c.Actor) == "" {
		return app.EmailSyncWarning{}, errEmailInvalid
	}
	if failure.AcknowledgedAt == nil {
		acknowledgedAt := e.now
		failure.AcknowledgedAt = &acknowledgedAt
		failure.AcknowledgedBy = c.Actor
		emailSaveSyncFailure(e, failure)
		box, err := emailMailbox(e, c.MailboxID)
		if err != nil {
			return app.EmailSyncWarning{}, err
		}
		if box.UnacknowledgedWarningCount > 0 {
			box.UnacknowledgedWarningCount--
		}
		box.UpdatedAt = e.now
		emailSaveMailbox(e, box)
	}
	return app.EmailSyncWarning{ID: failure.ID, MailboxID: failure.MailboxID, WarningRef: failure.WarningRef, Stage: failure.Stage, Scope: failure.Scope, ErrorCode: failure.ErrorCode, State: failure.State, ObservedAt: failure.ObservedAt, AttemptCount: len(failure.Attempts), FirstFailedAt: failure.FirstFailedAt, LastAttemptAt: failure.LastAttemptAt, AcknowledgedAt: failure.AcknowledgedAt}, e.err
}
