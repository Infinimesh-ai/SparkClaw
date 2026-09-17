package store

import (
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func sourceFailureCommand(f *emailContractFixture, m app.EmailMail) EmailSourceFailureCommand {
	return EmailSourceFailureCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, MailID: m.ID, ExpectedCaptureID: m.CaptureID, ExpectedOriginalSHA256: strings.Repeat("b", 64), ErrorCode: "email_source_missing"}
}

func TestEmailSourceFailureRecoveryCASAndImmutableBytes(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		m := f.capture(f.admit("lost-original", time.Now()))
		for _, mutate := range []func(*EmailSourceFailureCommand){
			func(c *EmailSourceFailureCommand) { c.ExpectedCaptureID = "stale" },
			func(c *EmailSourceFailureCommand) { c.ExpectedOriginalSHA256 = strings.Repeat("c", 64) },
			func(c *EmailSourceFailureCommand) { c.BindingGeneration++ },
			func(c *EmailSourceFailureCommand) { c.OwnerID = "other" },
		} {
			c := sourceFailureCommand(f, m)
			mutate(&c)
			if _, err := repo.ReportEmailSourceFailure(t.Context(), c); err == nil {
				t.Fatal("accepted stale or foreign source report")
			}
		}
		c := sourceFailureCommand(f, m)
		lost, err := repo.ReportEmailSourceFailure(t.Context(), c)
		f.must(err)
		if file, ok := repo.(*FileStore); ok {
			reopened, err := NewFileStore(file.path)
			f.must(err)
			repo, f.repo = reopened, reopened
		}
		_, err = repo.ReportEmailSourceFailure(t.Context(), c)
		f.must(err)
		if lost.CaptureState != app.EmailCaptureSourceMissing || lost.CaptureID != m.CaptureID || lost.QualifiedFailureCount != 0 {
			t.Fatalf("wrong repair projection: %+v", lost)
		}
		checkpoint := beginTimelineSync(t, f, f.box.Boundary.Add(time.Minute))
		if len(checkpoint.RetryFailures) != 1 || checkpoint.Mailbox.PendingFailureCount != 1 || checkpoint.RetryFailures[0].Scope != app.EmailSyncFailureLocalOperational || checkpoint.RetryFailures[0].ConsecutiveFailures != 0 {
			t.Fatalf("wrong exact retry: %+v", checkpoint)
		}
		job := emailPageBatchLease(t, f)
		capture := emailPageBatchCapture(f, job, m, m.CaptureID, app.EmailCaptureComplete, "unknown")
		capture.Capture.OriginalSHA256 = strings.Repeat("c", 64)
		if _, err = repo.PublishEmailCapture(t.Context(), capture); StoreErrorCodeOf(err) != StoreErrorConflict {
			t.Fatalf("changed source accepted: %v", err)
		}
		capture.EmailCommand = f.command()
		capture.Capture.OriginalSHA256 = strings.Repeat("b", 64)
		restored, err := repo.PublishEmailCapture(t.Context(), capture)
		f.must(err)
		if restored.CaptureState != app.EmailCaptureComplete || restored.CaptureID != m.CaptureID {
			t.Fatalf("source not restored: %+v", restored)
		}
		commitTimelineSync(t, f, checkpoint, func(c *EmailSyncCommitCommand) {
			c.Outcomes = []EmailSyncFailureOutcome{{MailID: m.ID, ProviderMessageID: m.ProviderMessageID, Stage: "original", Success: true}}
		})
		if f.box.PendingFailureCount != 0 {
			t.Fatal("successful repair left pending retry")
		}
	})
}

func TestEmailSourceFailureCannotRevivePurgedOrSuppressed(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		purged := f.capture(f.admit("purged", time.Now()))
		_, err := repo.ReportEmailSourceFailure(t.Context(), sourceFailureCommand(f, purged))
		f.must(err)
		_, err = repo.PurgeEmailCaptures(t.Context(), EmailCapturePurgeCommand{EmailCommand: f.command(), Scope: EmailPurgeScopeMail, MailID: purged.ID, Reason: app.EmailPurgeManual, At: time.Now()})
		f.must(err)
		if _, err = repo.ReportEmailSourceFailure(t.Context(), sourceFailureCommand(f, purged)); StoreErrorCodeOf(err) != StoreErrorConflict {
			t.Fatalf("purged source revived: %v", err)
		}
		check := beginTimelineSync(t, f, f.box.Boundary.Add(time.Minute))
		if len(check.RetryFailures) != 0 || check.Mailbox.PendingFailureCount != 0 {
			t.Fatal("cleanup retained an operational repair")
		}
		commitTimelineSync(t, f, check, nil)
		m := f.capture(f.admit("suppressed", time.Now()))
		upper := check.IntervalEnd.Add(time.Minute)
		for range 2 {
			checkpoint := beginTimelineSync(t, f, upper)
			commitTimelineSync(t, f, checkpoint, func(c *EmailSyncCommitCommand) {
				c.Outcomes = []EmailSyncFailureOutcome{{MailID: m.ID, ProviderMessageID: m.ProviderMessageID, Stage: "original", Scope: app.EmailSyncFailureMailSpecific, ErrorCode: "email_original_invalid", Qualified: true}}
			})
			upper = upper.Add(time.Minute)
		}
		if _, err = repo.ReportEmailSourceFailure(t.Context(), sourceFailureCommand(f, m)); StoreErrorCodeOf(err) != StoreErrorConflict {
			t.Fatalf("suppressed source revived: %v", err)
		}
	})
}

func TestEmailSourceFailurePausedMailboxRetainsRepairUntilEnabled(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		m := f.capture(f.admit("paused-missing-original", time.Now()))
		stale := sourceFailureCommand(f, m)
		paused, err := repo.PauseEmailMailbox(t.Context(), EmailPauseCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration})
		f.must(err)
		f.box = paused
		if _, err := repo.ReportEmailSourceFailure(t.Context(), stale); StoreErrorCodeOf(err) != StoreErrorConflict {
			t.Fatalf("old generation accepted: %v", err)
		}
		_, err = repo.ReportEmailSourceFailure(t.Context(), sourceFailureCommand(f, m))
		f.must(err)
		box, found, err := repo.GetEmailMailbox(t.Context(), f.owner, f.box.ID)
		f.must(err)
		if !found || box.Active || box.IntakeEnabled || box.PendingFailureCount != 1 {
			t.Fatalf("report changed intake: %+v", box)
		}
		_, claimed, err := repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobDiscover, app.EmailJobCapture}, LeaseDuration: time.Minute})
		f.must(err)
		if claimed {
			t.Fatal("paused repair scheduled provider work")
		}
		f.box, err = repo.BindEmailMailbox(t.Context(), EmailBindCommand{EmailCommand: f.command(), Provider: box.Provider, Address: box.Address, Enabled: true, ExpectedVersion: box.Version})
		f.must(err)
		checkpoint := beginTimelineSync(t, f, f.box.Boundary.Add(time.Minute))
		if len(checkpoint.RetryFailures) != 1 || checkpoint.RetryFailures[0].MailID != m.ID {
			t.Fatalf("reenable lost exact retry: %+v", checkpoint)
		}
	})
}
