package store

import (
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestEmailBrowserBusyYieldsWithoutFailingOrSpendingAttempts(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		_, err := repo.RequestEmailJob(t.Context(), manualPollRequest(f))
		f.must(err)
		job := f.claim(app.EmailJobDiscover)
		checkpoint := beginTimelineSync(t, f, f.box.Boundary.Add(time.Minute))
		for i := 0; i < 7; i++ {
			l := f.lease(job)
			l.Now = time.Now().UTC()
			retryAt := postgresTime(l.Now.Add(2 * time.Second))
			yielded, err := f.repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: l, ErrorCode: string(app.ToolErrorEmailBrowserBusy), RetryAt: retryAt})
			f.must(err)
			if yielded.State != app.EmailJobRetryWait || yielded.ErrorCode != "" || yielded.Attempt != 0 || yielded.LeaseToken != "" || !yielded.LeaseExpiresAt.IsZero() || !yielded.RoundFinishedAt.IsZero() || !yielded.RefreshPending || !yielded.NextAttemptAt.Equal(retryAt) {
				t.Fatalf("contention counted as failure/round completion: %+v", yielded)
			}
			// Neither repeated planner work nor a notification cancels the yield.
			_, err = f.repo.RequestEmailJob(t.Context(), pollRequest(f))
			f.must(err)
			f.repo = restartEmailRepeatRepository(t, f.repo)
			_, found, err := f.repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobDiscover}, Now: retryAt.Add(-time.Microsecond)})
			f.must(err)
			if found {
				t.Fatal("yield deadline bypassed")
			}
			job, found, err = f.repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobDiscover}, Now: retryAt, LeaseDuration: time.Minute})
			f.must(err)
			if !found || job.Attempt != 1 {
				t.Fatalf("busy exhausted attempts: %+v", job)
			}
			box, _, err := f.repo.GetEmailMailbox(t.Context(), f.owner, f.box.ID)
			f.must(err)
			if box.ReconciledRevision != 0 || box.SignalRevision != 1 || box.PendingFailureCount != 0 || box.LastSyncErrorCode != "" || !box.InflightUntil.Equal(checkpoint.IntervalEnd) {
				t.Fatalf("yield changed mailbox checkpoint/health: %+v", box)
			}
		}
		commitTimelineSync(t, f, checkpoint, nil)
		finished, err := f.repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(job)})
		f.must(err)
		if finished.RefreshPending || finished.State != app.EmailJobQueued || finished.ErrorCode != "" {
			t.Fatalf("could not resume normally: %+v", finished)
		}
	})
}
