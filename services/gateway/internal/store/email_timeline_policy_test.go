package store

import (
	"fmt"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"reflect"
	"testing"
	"time"
)

func TestEmailTimelineRetiresOnlyLegacyJobsInBoundedDurableBatches(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		mail := f.admit("retirement-target", time.Now())
		upper := f.box.Boundary.Add(time.Minute)
		for range 2 {
			checkpoint := beginTimelineSync(t, f, upper)
			commitTimelineSync(t, f, checkpoint, func(c *EmailSyncCommitCommand) {
				c.Outcomes = []EmailSyncFailureOutcome{{MailID: mail.ID, ProviderMessageID: mail.ProviderMessageID, Stage: "original", Scope: app.EmailSyncFailureMailSpecific, ErrorCode: "email_original_invalid", Qualified: true}}
			})
			upper = upper.Add(time.Minute)
		}
		warningsBefore, err := repo.ListEmailSyncWarnings(t.Context(), EmailQuery{OwnerID: f.owner, MailboxID: f.box.ID})
		f.must(err)
		seed := func(e *emailEngine) (struct{}, error) {
			for _, kind := range []string{app.EmailJobCapture, app.EmailJobMarkRead, app.EmailJobThreadSync, app.EmailJobSourceRecovery} {
				for i := 0; i < 30; i++ {
					state := []string{app.EmailJobRunning, app.EmailJobFailed, app.EmailJobQueued, app.EmailJobPaused, app.EmailJobRetryWait}[i%5]
					j := app.EmailJob{ID: fmt.Sprintf("legacy-%s-%03d", kind, i), OwnerID: f.owner, MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, Kind: kind, TargetID: mail.ID, State: state, ErrorCode: "original_failure", Attempt: 2, NextAttemptAt: e.now, CreatedAt: e.now, UpdatedAt: e.now}
					if state == app.EmailJobRunning {
						j.LeaseToken = "old-live-lease"
						j.LeaseExpiresAt = e.now.Add(time.Minute)
					}
					emailSaveJob(e, j)
				}
			}
			for _, kind := range []string{app.EmailJobParse, app.EmailJobClassification, app.EmailJobAssignment, app.EmailJobDiscover} {
				emailSaveJob(e, app.EmailJob{ID: "keep-" + kind, OwnerID: f.owner, MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, Kind: kind, TargetID: f.box.ID, State: app.EmailJobQueued, MaxAttempts: 5, NextAttemptAt: e.now, CreatedAt: e.now, UpdatedAt: e.now})
			}
			return struct{}{}, e.err
		}
		switch s := repo.(type) {
		case *MemoryStore:
			_, err = emailMemoryRun(s, t.Context(), OperationRequestEmailJob, f.owner, "seed-retirement", nil, true, seed)
		case *FileStore:
			_, err = emailFileRun(s, t.Context(), OperationRequestEmailJob, f.owner, "seed-retirement", nil, true, seed)
		case *PostgresStore:
			_, err = emailPostgresRun(s, t.Context(), OperationRequestEmailJob, f.owner, "seed-retirement", nil, true, seed)
		}
		f.must(err)
		c := f.command()
		first, err := repo.ActivateEmailTimelinePolicy(t.Context(), c)
		f.must(err)
		if !first.Remaining || first.RetiredCount != 100 {
			t.Fatalf("unbounded retirement: %+v", first)
		}
		// This low-sorting live capture was not in the first 25-row page. The
		// activation fence must reject its late completion before migration ends.
		lease := EmailJobLease{OwnerID: f.owner, JobID: "legacy-" + app.EmailJobCapture + "-000", LeaseToken: "old-live-lease"}
		if _, err = repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: lease}); StoreErrorCodeOf(err) != StoreErrorConflict {
			t.Fatalf("old finish not fenced: %v", err)
		}
		if _, err = repo.PublishEmailCapture(t.Context(), EmailCaptureCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, Lease: lease, Capture: app.EmailCaptureVersion{MailID: mail.ID}}); StoreErrorCodeOf(err) != StoreErrorConflict {
			t.Fatalf("old publish not fenced: %v", err)
		}
		if _, err = repo.AdoptEmailCapture(t.Context(), EmailCaptureAdoption{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, Capture: app.EmailCaptureVersion{MailID: mail.ID}}); StoreErrorCodeOf(err) != StoreErrorConflict {
			t.Fatalf("legacy recovery adoption not fenced: %v", err)
		}
		if _, ok, err := repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobCapture, app.EmailJobSourceRecovery}, LeaseDuration: time.Minute}); err != nil || ok {
			t.Fatalf("old jobs still claimable: %v %v", ok, err)
		}
		if _, err = repo.RequestEmailJob(t.Context(), EmailJobRequest{EmailCommand: f.command(), Kind: app.EmailJobSourceRecovery, TargetID: f.owner, Rearm: true}); StoreErrorCodeOf(err) != StoreErrorConflict {
			t.Fatalf("old request not fenced: %v", err)
		}
		if _, ok, err := repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobDiscover}, LeaseDuration: time.Minute}); err != nil || !ok {
			t.Fatalf("old leases blocked new discovery: %v %v", ok, err)
		}
		if file, ok := repo.(*FileStore); ok {
			reopened, err := NewFileStore(file.path)
			f.must(err)
			repo, f.repo = reopened, reopened
		}
		second, err := repo.ActivateEmailTimelinePolicy(t.Context(), c)
		f.must(err)
		if second.Remaining || second.RetiredCount != 121 {
			t.Fatalf("retirement did not resume: %+v", second)
		}
		again, err := repo.ActivateEmailTimelinePolicy(t.Context(), c)
		f.must(err)
		if !reflect.DeepEqual(again, second) {
			t.Fatalf("completed migration changed: %+v", again)
		}
		warningsAfter, err := repo.ListEmailSyncWarnings(t.Context(), EmailQuery{OwnerID: f.owner, MailboxID: f.box.ID})
		f.must(err)
		if len(warningsBefore.Items) != 1 || !reflect.DeepEqual(warningsBefore, warningsAfter) {
			t.Fatal("retirement changed persistent failure warning")
		}
		status, err := repo.GetEmailOwnerStatus(t.Context(), f.owner)
		f.must(err)
		if status.BacklogCount != 4 {
			t.Fatalf("retired jobs still in backlog: %+v", status)
		}
		jobs, err := repo.ListEmailJobs(t.Context(), EmailQuery{OwnerID: f.owner, Limit: 200})
		f.must(err)
		for _, j := range jobs {
			if !emailTimelineLegacyKind(j.Kind) {
				continue
			}
			if j.State != app.EmailJobRetired || j.RetiredAt == nil || j.RetiredFromState == "" || j.RetirementReason != EmailTimelinePolicyVersion || j.LeaseToken != "" {
				t.Fatalf("bad retired audit: %+v", j)
			}
			if j.ID != "" && j.ID[:7] == "legacy-" && (j.ErrorCode != "original_failure" || j.Attempt != 2) {
				t.Fatalf("failure audit erased: %+v", j)
			}
		}
	})
}
