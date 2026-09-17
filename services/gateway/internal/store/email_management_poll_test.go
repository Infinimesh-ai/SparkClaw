package store

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func pollRequest(f *emailContractFixture) EmailJobRequest {
	return EmailJobRequest{EmailCommand: f.command(), Kind: app.EmailJobDiscover, TargetID: f.box.ID, MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, Rearm: true, RearmFailed: true, AutomaticPoll: true, RepeatInterval: time.Minute}
}

func manualPollRequest(f *emailContractFixture) EmailJobRequest {
	c := pollRequest(f)
	c.AutomaticPoll, c.RepeatInterval = false, 0
	c.SyncTrigger, c.SyncActor = "manual_refresh", f.owner
	return c
}

func TestEmailMinutePollCompletesWithoutPlannerAndSurvivesRestart(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		request := pollRequest(f)
		_, err := repo.RequestEmailJob(t.Context(), request)
		f.must(err)
		for round := 0; round < 3; round++ {
			jobs, err := f.repo.ListEmailJobs(t.Context(), EmailQuery{OwnerID: f.owner, MailboxID: f.box.ID})
			f.must(err)
			at := jobs[0].NextAttemptAt
			j, found, err := f.repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobDiscover}, Now: at, LeaseDuration: 10 * time.Minute})
			f.must(err)
			if !found {
				t.Fatal("round not eligible at persisted deadline")
			}
			finished, err := f.repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(j)})
			f.must(err)
			if finished.State != app.EmailJobQueued || finished.Attempt != 0 || !finished.NextAttemptAt.Equal(finished.UpdatedAt.Add(time.Minute)) {
				t.Fatalf("not completion-relative: %+v", finished)
			}
			_, found, err = f.repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobDiscover}, Now: finished.NextAttemptAt.Add(-time.Microsecond)})
			f.must(err)
			if found {
				t.Fatal("started before completion + 60 seconds")
			}
			// Identical original command is intentionally cached; no new planner
			// generation or extra job is necessary for the next round.
			_, err = f.repo.RequestEmailJob(t.Context(), request)
			f.must(err)
			f.repo = restartEmailRepeatRepository(t, f.repo)
		}
		status, err := f.repo.GetEmailOwnerStatus(t.Context(), f.owner)
		f.must(err)
		if status.BacklogCount != 0 {
			t.Fatalf("idle heartbeat counted as backlog: %d", status.BacklogCount)
		}
	})
}

func TestEmailMinutePollRefreshCoalescesThroughFollowupAndRestart(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		_, err := repo.RequestEmailJob(t.Context(), pollRequest(f))
		f.must(err)
		auto := f.claim(app.EmailJobDiscover)
		const callers = 12
		results := make(chan app.EmailJob, callers)
		errors := make(chan error, callers)
		var wg sync.WaitGroup
		for n := 0; n < callers; n++ {
			c := manualPollRequest(f)
			wg.Add(1)
			go func() { defer wg.Done(); j, err := repo.RequestEmailJob(t.Context(), c); results <- j; errors <- err }()
		}
		wg.Wait()
		close(results)
		close(errors)
		for err := range errors {
			f.must(err)
		}
		requestID := ""
		for j := range results {
			if requestID == "" {
				requestID = j.RefreshRequestID
			}
			if requestID == "" || j.RefreshRequestID != requestID || !j.RefreshPending || j.RefreshActiveID != "" || j.LeaseToken != auto.LeaseToken {
				t.Fatal("parallel requests did not merge behind active automatic lease")
			}
		}
		f.repo = restartEmailRepeatRepository(t, repo)
		queued, err := f.repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(auto)})
		f.must(err)
		if !queued.RefreshPending || queued.State != app.EmailJobQueued || !queued.NextAttemptAt.Equal(queued.UpdatedAt) {
			t.Fatal("missing immediate follow-up")
		}
		followup := f.claim(app.EmailJobDiscover)
		if followup.RefreshActiveID != requestID || followup.SyncTrigger != "manual_refresh" {
			t.Fatal("followup lost request identity")
		}
		_, err = f.repo.RequestEmailJob(t.Context(), manualPollRequest(f))
		f.must(err)
		finished, err := f.repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(followup)})
		f.must(err)
		if finished.RefreshPending || finished.RefreshActiveID != "" || !finished.NextAttemptAt.Equal(finished.UpdatedAt.Add(time.Minute)) {
			t.Fatal("refresh did not release and restart automatic idle")
		}
		box, _, err := f.repo.GetEmailMailbox(t.Context(), f.owner, f.box.ID)
		f.must(err)
		if box.RefreshPending || box.RefreshRequestID != requestID {
			t.Fatal("mailbox projection did not retain completion identity")
		}
		if _, found, err := f.repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobDiscover}}); found || err != nil {
			t.Fatal("duplicate clicks created another immediate round")
		}
	})
}

func TestEmailMinutePollUpgradeAndRefreshPreserveBackoff(t *testing.T) {
	for _, state := range []string{"idle", "retry"} {
		t.Run(state, func(t *testing.T) {
			emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
				f := emailFixture(t, repo)
				old := pollRequest(f)
				old.AutomaticPoll = false
				old.RepeatInterval = 20 * time.Minute
				_, err := repo.RequestEmailJob(t.Context(), old)
				f.must(err)
				job := f.claim(app.EmailJobDiscover)
				finish := EmailJobFinish{EmailJobLease: f.lease(job)}
				if state == "retry" {
					finish.ErrorCode = "provider_unavailable"
					finish.RetryAt = time.Now().Add(5 * time.Minute)
				}
				completed, err := repo.FinishEmailJob(t.Context(), finish)
				f.must(err)
				old.EmailCommand = f.command()
				_, err = repo.RequestEmailJob(t.Context(), old)
				f.must(err)
				upgraded, err := repo.RequestEmailJob(t.Context(), pollRequest(f))
				f.must(err)
				if state == "idle" {
					if upgraded.NextAttemptAt.After(completed.UpdatedAt.Add(61 * time.Second)) {
						t.Fatal("legacy 20 minute idle deadline retained")
					}
				} else if !upgraded.NextAttemptAt.Equal(completed.NextAttemptAt) {
					t.Fatal("upgrade shortened backoff")
				}
				manual, err := repo.RequestEmailJob(t.Context(), manualPollRequest(f))
				f.must(err)
				if !manual.RefreshPending {
					t.Fatal("manual request lost")
				}
				if state == "retry" && !manual.NextAttemptAt.Equal(completed.NextAttemptAt) {
					t.Fatal("refresh shortened backoff")
				}
				if state == "idle" && manual.NextAttemptAt.After(time.Now()) {
					t.Fatal("idle refresh did not skip idle deadline")
				}
			})
		})
	}
}

func TestEmailMinutePollManualRetryTerminalAndPause(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		_, err := repo.RequestEmailJob(t.Context(), pollRequest(f))
		f.must(err)
		_, err = repo.RequestEmailJob(t.Context(), manualPollRequest(f))
		f.must(err)
		for attempt := 1; attempt <= 5; attempt++ {
			job := f.claim(app.EmailJobDiscover)
			finished, err := repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(job), ErrorCode: "provider_unavailable", RetryAt: time.Now().Add(-time.Second)})
			f.must(err)
			if finished.RefreshPending != (attempt < 5) {
				t.Fatalf("attempt %d pending=%v", attempt, finished.RefreshPending)
			}
		}
		_, err = repo.RequestEmailJob(t.Context(), manualPollRequest(f))
		f.must(err)
		paused, err := repo.PauseEmailMailbox(t.Context(), EmailPauseCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration})
		f.must(err)
		if paused.RefreshPending {
			t.Fatal("pause retained refresh lock")
		}
		if _, found, err := repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobDiscover}, Now: time.Now().Add(time.Hour)}); found || err != nil {
			t.Fatal(fmt.Sprintf("paused mailbox admitted poll: %v", err))
		}
	})
}
