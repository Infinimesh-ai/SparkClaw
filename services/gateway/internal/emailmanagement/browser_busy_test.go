package emailmanagement

import (
	"context"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type busyPageFixture struct {
	*emptyPageFixture
	busyOn, busyRemaining, attempts int
}

func (f *busyPageFixture) CollectPageForOwner(ctx context.Context, owner string, request app.EmailReadRequest) (app.EmailPageResult, error) {
	f.attempts++
	if f.attempts >= f.busyOn && f.busyRemaining > 0 {
		f.busyRemaining--
		return app.EmailPageResult{}, &emailautomation.Error{Code: app.ToolErrorEmailBrowserBusy}
	}
	return f.emptyPageFixture.CollectPageForOwner(ctx, owner, request)
}

func TestReaderBusyPreservesCheckpointAndFinishesNotificationRound(t *testing.T) {
	for _, finalCheckBusy := range []bool{false, true} {
		t.Run(map[bool]string{false: "hint_during_yield", true: "final_check_yield"}[finalCheckBusy], func(t *testing.T) {
			repo := store.NewMemoryStore()
			s, base, _ := newFixtureService(t, repo)
			browser := &busyPageFixture{emptyPageFixture: &emptyPageFixture{intakeFixture: base}, busyOn: 1, busyRemaining: 1}
			if finalCheckBusy {
				browser.busyOn = 2
			}
			s.browser = browser
			box, err := s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, true, 0)
			if err != nil {
				t.Fatal(err)
			}
			now := box.ActivatedAt.Add(10 * time.Second)
			s.now = func() time.Time { return now }
			if err := s.plan(t.Context()); err != nil {
				t.Fatal(err)
			}
			hint := func() {
				_, err := repo.RequestEmailJob(t.Context(), store.EmailJobRequest{EmailCommand: command("email-owner", "busy-hint"), Kind: app.EmailJobDiscover, TargetID: box.ID, MailboxID: box.ID, BindingGeneration: box.BindingGeneration, Rearm: true, SyncTrigger: "notification_hint", EventEpoch: "test", EventSequence: 1})
				if err != nil {
					t.Fatal(err)
				}
			}
			if finalCheckBusy {
				hint()
			}
			if worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover}); !worked || err != nil {
				t.Fatalf("first work: %v %v", worked, err)
			}
			yielded, _, _ := repo.GetEmailMailbox(t.Context(), "email-owner", box.ID)
			if yielded.LastSyncErrorCode != "" || yielded.PendingFailureCount != 0 || yielded.CoverageGapCount != 0 || yielded.InflightUntil.IsZero() {
				t.Fatalf("contention recorded provider failure or discarded checkpoint: %+v", yielded)
			}
			if finalCheckBusy && yielded.InflightKind != "final_check" {
				t.Fatal("final check intent lost")
			}
			if !finalCheckBusy {
				hint()
			}
			jobs, _ := repo.ListEmailJobs(t.Context(), store.EmailQuery{OwnerID: "email-owner", MailboxID: box.ID})
			var job app.EmailJob
			for _, candidate := range jobs {
				if candidate.Kind == app.EmailJobDiscover {
					job = candidate
				}
			}
			if job.State != app.EmailJobRetryWait || job.ErrorCode != "" || job.Attempt != 0 || !job.NextAttemptAt.Equal(now.Add(2*time.Second)) || !job.RoundFinishedAt.IsZero() {
				t.Fatalf("busy did not yield for two seconds: %+v", job)
			}
			now = job.NextAttemptAt.Add(-time.Microsecond)
			if worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover}); worked || err != nil {
				t.Fatal("new hint bypassed yield")
			}
			now = job.NextAttemptAt
			if worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover}); !worked || err != nil {
				t.Fatalf("resume: %v %v", worked, err)
			}
			after, _, _ := repo.GetEmailMailbox(t.Context(), "email-owner", box.ID)
			if after.SignalRevision != 1 || after.ReconciledRevision != 1 || after.LastFinishedReconciledRevision != 1 || after.LastSyncErrorCode != "" || after.PendingFailureCount != 0 || after.InflightKind != "" {
				t.Fatalf("resumed round lost notification or failed: %+v", after)
			}
			wantCalls := 3 // successful old interval, new hint interval, final check
			if finalCheckBusy {
				wantCalls = 2
			}
			if len(browser.calls) != wantCalls {
				t.Fatalf("missing final check after yield: calls=%d", len(browser.calls))
			}
			last := browser.calls[len(browser.calls)-1].Discovery
			previous := browser.calls[len(browser.calls)-2].Discovery
			if !last.IntervalEnd.Equal(previous.IntervalEnd) {
				t.Fatal("final check did not reread the completed interval")
			}
		})
	}
}

func TestRepeatedReaderBusyClaimsReuseFrozenIntervalWithoutCommandConflict(t *testing.T) {
	repo := store.NewMemoryStore()
	s, base, _ := newFixtureService(t, repo)
	browser := &busyPageFixture{emptyPageFixture: &emptyPageFixture{intakeFixture: base}, busyOn: 1, busyRemaining: 4}
	s.browser = browser
	box, err := s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	now := box.ActivatedAt.Add(10 * time.Second)
	s.now = func() time.Time { return now }
	if err := s.plan(t.Context()); err != nil {
		t.Fatal(err)
	}
	var frozen time.Time
	for attempt := 0; attempt < 5; attempt++ {
		if worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover}); !worked || err != nil {
			t.Fatalf("attempt %d: %v %v", attempt, worked, err)
		}
		jobs, _ := repo.ListEmailJobs(t.Context(), store.EmailQuery{OwnerID: "email-owner", MailboxID: box.ID})
		var job app.EmailJob
		for _, candidate := range jobs {
			if candidate.Kind == app.EmailJobDiscover {
				job = candidate
			}
		}
		saved, _, _ := repo.GetEmailMailbox(t.Context(), "email-owner", box.ID)
		if attempt < 4 {
			if frozen.IsZero() {
				frozen = saved.InflightUntil
			}
			if job.ErrorCode != "" || job.State != app.EmailJobRetryWait || job.Attempt != 0 || !saved.InflightUntil.Equal(frozen) || saved.LastSyncErrorCode != "" {
				t.Fatalf("repeat yield caused failure or changed interval: job=%+v mailbox=%+v", job, saved)
			}
		} else if job.ErrorCode != "" || job.State != app.EmailJobQueued || !saved.PollThrough.Equal(frozen) || !saved.InflightUntil.IsZero() {
			t.Fatalf("frozen interval could not resume: job=%+v mailbox=%+v", job, saved)
		}
		now = job.NextAttemptAt
	}
	if browser.attempts != 5 {
		t.Fatalf("Store blocked provider retry: attempts=%d", browser.attempts)
	}
}
