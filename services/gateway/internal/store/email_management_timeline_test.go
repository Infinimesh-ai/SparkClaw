package store

import (
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func beginTimelineSync(t *testing.T, f *emailContractFixture, upper time.Time) EmailSyncCheckpoint {
	t.Helper()
	checkpoint, err := f.repo.BeginEmailSync(t.Context(), EmailSyncBeginCommand{
		EmailCommand:      f.command(),
		MailboxID:         f.box.ID,
		BindingGeneration: f.box.BindingGeneration,
		ProviderMode:      app.EmailProviderModeTimeRange,
		UpperBound:        upper,
		Trigger:           "test",
		Actor:             "system",
	})
	f.must(err)
	f.box = checkpoint.Mailbox
	return checkpoint
}

func commitTimelineSync(t *testing.T, f *emailContractFixture, checkpoint EmailSyncCheckpoint, configure func(*EmailSyncCommitCommand)) EmailSyncCommitResult {
	t.Helper()
	command := EmailSyncCommitCommand{
		EmailCommand:      f.command(),
		MailboxID:         f.box.ID,
		BindingGeneration: f.box.BindingGeneration,
		IntervalStart:     checkpoint.IntervalStart,
		IntervalEnd:       checkpoint.IntervalEnd,
		ProviderMode:      app.EmailProviderModeTimeRange,
		Trigger:           "test",
		Actor:             "system",
		Complete:          true,
	}
	if configure != nil {
		configure(&command)
	}
	result, err := f.repo.CommitEmailSync(t.Context(), command)
	f.must(err)
	f.box = result.Mailbox
	return result
}

func TestEmailTimelineSuppressesSecondQualifiedFailureAndNeverSchedulesItAgain(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		mail := f.admit("two-failure-mail", time.Now())
		first := beginTimelineSync(t, f, f.box.Boundary.Add(time.Minute))
		commitTimelineSync(t, f, first, func(command *EmailSyncCommitCommand) {
			command.Outcomes = []EmailSyncFailureOutcome{{ProviderMessageID: mail.ProviderMessageID, MailID: mail.ID, Stage: "original", Scope: app.EmailSyncFailureMailSpecific, ErrorCode: "email_original_invalid", Qualified: true}}
		})
		if f.box.PendingFailureCount != 1 || f.box.SuppressedMailCount != 0 {
			t.Fatalf("first failure counters = pending %d suppressed %d", f.box.PendingFailureCount, f.box.SuppressedMailCount)
		}

		second := beginTimelineSync(t, f, first.IntervalEnd.Add(time.Minute))
		if len(second.RetryFailures) != 1 || second.RetryFailures[0].MailID != mail.ID {
			t.Fatalf("first qualified failure was not carried once: %+v", second.RetryFailures)
		}
		result := commitTimelineSync(t, f, second, func(command *EmailSyncCommitCommand) {
			command.Outcomes = []EmailSyncFailureOutcome{{ProviderMessageID: mail.ProviderMessageID, MailID: mail.ID, Stage: "original", Scope: app.EmailSyncFailureMailSpecific, ErrorCode: "email_original_invalid", Qualified: true}}
		})
		if result.Suppressed != 1 || f.box.PendingFailureCount != 0 || f.box.SuppressedMailCount != 1 || f.box.UnacknowledgedWarningCount != 1 {
			t.Fatalf("second failure did not become terminal: result=%+v mailbox=%+v", result, f.box)
		}
		stored, found, err := f.repo.GetEmailMail(t.Context(), f.owner, mail.ID)
		f.must(err)
		if !found || stored.SyncState != app.EmailMailSyncSuppressed || stored.QualifiedFailureCount != 2 {
			t.Fatalf("mail projection is not suppressed: %+v", stored)
		}

		third := beginTimelineSync(t, f, second.IntervalEnd.Add(time.Minute))
		if len(third.RetryFailures) != 0 {
			t.Fatalf("suppressed failure re-entered scheduler work: %+v", third.RetryFailures)
		}
		commitTimelineSync(t, f, third, nil)

		warnings, err := f.repo.ListEmailSyncWarnings(t.Context(), EmailQuery{OwnerID: f.owner, MailboxID: f.box.ID, Limit: 10})
		f.must(err)
		if len(warnings.Items) != 1 || warnings.Items[0].State != app.EmailSyncFailureSuppressed || warnings.Items[0].AttemptCount != 2 {
			t.Fatalf("persistent warning missing or unsafe projection changed: %+v", warnings)
		}
		warning, err := f.repo.AcknowledgeEmailSyncWarning(t.Context(), EmailSyncWarningAck{EmailCommand: f.command(), MailboxID: f.box.ID, FailureID: warnings.Items[0].ID, Actor: f.owner})
		f.must(err)
		if warning.AcknowledgedAt == nil {
			t.Fatal("warning acknowledgement was not recorded")
		}
		box, _, err := f.repo.GetEmailMailbox(t.Context(), f.owner, f.box.ID)
		f.must(err)
		if box.SuppressedMailCount != 1 || box.UnacknowledgedWarningCount != 0 {
			t.Fatalf("acknowledgement changed terminal state instead of presentation: %+v", box)
		}
		f.repo = restartEmailRepeatRepository(t, f.repo)
		afterRestart, err := f.repo.ListEmailSyncWarnings(t.Context(), EmailQuery{OwnerID: f.owner, MailboxID: f.box.ID, Limit: 10})
		f.must(err)
		if len(afterRestart.Items) != 1 || afterRestart.Items[0].AcknowledgedAt == nil {
			t.Fatalf("restart lost warning audit: %+v", afterRestart)
		}
	})
}

func TestEmailTimelineCohortFailureDoesNotSuppressIndividualMail(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		firstMail := f.admit("cohort-a", time.Now())
		secondMail := f.admit("cohort-b", time.Now())
		checkpoint := beginTimelineSync(t, f, f.box.Boundary.Add(time.Minute))
		commitTimelineSync(t, f, checkpoint, func(command *EmailSyncCommitCommand) {
			for _, mail := range []app.EmailMail{firstMail, secondMail} {
				command.Outcomes = append(command.Outcomes, EmailSyncFailureOutcome{ProviderMessageID: mail.ProviderMessageID, MailID: mail.ID, Stage: "original", Scope: app.EmailSyncFailureMailSpecific, ErrorCode: "email_template_invalid", Qualified: true})
			}
		})
		for _, mail := range []app.EmailMail{firstMail, secondMail} {
			stored, _, err := f.repo.GetEmailMail(t.Context(), f.owner, mail.ID)
			f.must(err)
			if stored.QualifiedFailureCount != 0 || stored.SyncState != app.EmailMailSyncRetry {
				t.Fatalf("shared failure counted against one mail: %+v", stored)
			}
		}
		if f.box.SuppressedMailCount != 0 || f.box.PendingFailureCount != 2 {
			t.Fatalf("cohort counters are incorrect: %+v", f.box)
		}
	})
}

func TestEmailTimelineOverflowFreezesOnceThenLeavesTerminalGap(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		first := beginTimelineSync(t, f, f.box.Boundary.Add(time.Minute))
		commitTimelineSync(t, f, first, func(command *EmailSyncCommitCommand) {
			command.Complete = false
			command.Overflow = true
		})
		if f.box.SyncState != app.EmailSyncOverflowConfirmation || !f.box.PollThrough.Equal(first.IntervalStart) {
			t.Fatalf("first overflow did not freeze its range: %+v", f.box)
		}
		confirmation := beginTimelineSync(t, f, first.IntervalEnd.Add(10*time.Minute))
		if !confirmation.ConfirmingOverflow || !confirmation.IntervalStart.Equal(first.IntervalStart) || !confirmation.IntervalEnd.Equal(first.IntervalEnd) {
			t.Fatalf("overflow confirmation widened the range: %+v", confirmation)
		}
		commitTimelineSync(t, f, confirmation, func(command *EmailSyncCommitCommand) {
			command.Complete = false
			command.Overflow = true
		})
		if f.box.SyncState != app.EmailSyncCoverageGap || f.box.CoverageGapCount != 1 || !f.box.PollThrough.Equal(first.IntervalEnd) || !f.box.DiscoveredThrough.Equal(first.IntervalStart) {
			t.Fatalf("second overflow did not become a terminal gap: %+v", f.box)
		}
		next := beginTimelineSync(t, f, first.IntervalEnd.Add(time.Minute))
		if next.ConfirmingOverflow || !next.IntervalStart.Equal(first.IntervalEnd) {
			t.Fatalf("terminal gap re-entered polling: %+v", next)
		}
	})
}

func TestEmailTimelineSuccessfulOverflowConfirmationClosesFrozenRange(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		first := beginTimelineSync(t, f, f.box.Boundary.Add(time.Minute))
		commitTimelineSync(t, f, first, func(c *EmailSyncCommitCommand) { c.Complete = false; c.Overflow = true })
		confirmation := beginTimelineSync(t, f, first.IntervalEnd.Add(time.Minute))
		commitTimelineSync(t, f, confirmation, nil)
		next := beginTimelineSync(t, f, first.IntervalEnd.Add(2*time.Minute))
		if next.ConfirmingOverflow || !next.IntervalStart.Equal(first.IntervalEnd) || next.Mailbox.CoverageGapCount != 0 {
			t.Fatalf("successful confirmation retained stale overflow: %+v", next)
		}
	})
}

func TestEmailTimelineBatchRollsBackAdmissionOnInvalidSource(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		_, err := repo.RequestEmailJob(t.Context(), EmailJobRequest{EmailCommand: f.command(), Kind: app.EmailJobDiscover, TargetID: f.box.ID, MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration})
		f.must(err)
		job := f.claim(app.EmailJobDiscover)
		checkpoint := beginTimelineSync(t, f, f.box.Boundary.Add(time.Minute))
		c := EmailSyncCommitCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration,
			Lease: EmailJobLease{OwnerID: f.owner, JobID: job.ID, LeaseToken: job.LeaseToken, Now: time.Now()}, ObservedAt: time.Now(),
			ProviderMode: app.EmailProviderModeTimeRange, IntervalStart: checkpoint.IntervalStart, IntervalEnd: checkpoint.IntervalEnd, Trigger: "test", Actor: "system", Complete: true,
			Members:  []EmailDiscoveryMember{{ProviderMessageID: "batch-mail", ProviderSelectionID: "batch-mail", Direction: "inbound"}},
			Captures: []EmailSyncCapture{{ProviderMessageID: "batch-mail", ReadState: "unknown", Capture: app.EmailCaptureVersion{ID: "invalid-source"}}},
		}
		if _, err := repo.CommitEmailSync(t.Context(), c); err == nil {
			t.Fatal("invalid source committed")
		}
		mails, err := repo.ListEmailMails(t.Context(), EmailQuery{OwnerID: f.owner, MailboxID: f.box.ID})
		f.must(err)
		if len(mails.Items) != 0 {
			t.Fatalf("failed composite transaction leaked admission: %+v", mails)
		}
		box, _, err := repo.GetEmailMailbox(t.Context(), f.owner, f.box.ID)
		f.must(err)
		if !box.PollThrough.Equal(checkpoint.IntervalStart) || !box.InflightUntil.Equal(checkpoint.IntervalEnd) {
			t.Fatalf("failed batch advanced checkpoint: %+v", box)
		}
	})
}
