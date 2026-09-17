package store

import (
	"reflect"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func restartEmailRepeatRepository(t *testing.T, repo EmailRepository) EmailRepository {
	t.Helper()
	switch s := repo.(type) {
	case *MemoryStore:
		next := NewMemoryStore()
		next.loadSnapshot(s.snapshot())
		return next
	case *FileStore:
		next, err := NewFileStore(s.path)
		if err != nil {
			t.Fatal(err)
		}
		return next
	case *PostgresStore:
		next, err := NewPostgresStore(t.Context(), s.db.Config().ConnString())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(next.Close)
		return next
	default:
		t.Fatalf("unexpected email backend %T", repo)
		return nil
	}
}

func TestEmailManagementPeriodicJobCompletionInterval(t *testing.T) {
	for _, kind := range []string{app.EmailJobDiscover, app.EmailJobThreadSync} {
		t.Run(kind, func(t *testing.T) {
			emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
				f := emailFixture(t, repo)
				startedAt := postgresTime(time.Now().Add(-2 * time.Minute))
				request := EmailJobRequest{Kind: kind, TargetID: f.box.ID, MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, Rearm: true, RepeatInterval: time.Minute, NextAttemptAt: startedAt}
				request.EmailCommand = f.command()
				_, err := f.repo.RequestEmailJob(t.Context(), request)
				f.must(err)
				// A lease that began two minutes ago models a scan spanning more
				// than one planner slot, without sleeping in the contract test.
				job, found, err := f.repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{kind}, Now: startedAt, LeaseDuration: 10 * time.Minute})
				f.must(err)
				if !found {
					t.Fatal("scheduled scan was not claimed")
				}
				completed, err := f.repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(job)})
				f.must(err)
				if completed.UpdatedAt.Sub(startedAt) <= time.Minute {
					t.Fatal("fixture did not span a complete polling interval")
				}
				request.NextAttemptAt = time.Time{}
				request.EmailCommand = f.command()
				queued, err := f.repo.RequestEmailJob(t.Context(), request)
				f.must(err)
				wantNext := completed.UpdatedAt.Add(time.Minute)
				if queued.State != app.EmailJobQueued || !queued.NextAttemptAt.Equal(wantNext) {
					t.Fatalf("completion signal restarted a long scan immediately: %+v", queued)
				}
				// Repeated planner slots and a later caller deadline must not
				// keep pushing already queued work into the future.
				for i := 0; i < 3; i++ {
					request.EmailCommand = f.command()
					request.NextAttemptAt = wantNext.Add(time.Hour)
					again, err := f.repo.RequestEmailJob(t.Context(), request)
					f.must(err)
					if !reflect.DeepEqual(again, queued) {
						t.Fatal("periodic rearm changed an already queued job")
					}
				}
				f.repo = restartEmailRepeatRepository(t, f.repo)
				request.EmailCommand = f.command()
				request.NextAttemptAt = time.Time{}
				afterRestart, err := f.repo.RequestEmailJob(t.Context(), request)
				f.must(err)
				if !reflect.DeepEqual(afterRestart, queued) {
					t.Fatal("restart lost the completion-based deadline")
				}
				_, found, err = f.repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{kind}, Now: wantNext.Add(-time.Microsecond)})
				f.must(err)
				if found {
					t.Fatal("next scan became eligible before the completion interval")
				}
				running, found, err := f.repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{kind}, Now: wantNext})
				f.must(err)
				if !found {
					t.Fatal("next scan did not become eligible at its deadline")
				}
				request.EmailCommand = f.command()
				again, err := f.repo.RequestEmailJob(t.Context(), request)
				f.must(err)
				if !reflect.DeepEqual(again, running) {
					t.Fatal("periodic planner changed a running scan or its lease")
				}
				f.finish(running)
				// An explicit manual repeat still follows the original immediate
				// success-rearm behavior when no RepeatInterval is supplied.
				request.EmailCommand = f.command()
				request.RepeatInterval = 0
				manual, err := f.repo.RequestEmailJob(t.Context(), request)
				f.must(err)
				if manual.State != app.EmailJobQueued || manual.NextAttemptAt.After(time.Now()) {
					t.Fatal("manual sync inherited the background polling delay")
				}
			})
		})
	}
}

func TestEmailManagementPeriodicJobKeepsBackoffAndExhaustedFailure(t *testing.T) {
	for _, kind := range []string{app.EmailJobDiscover, app.EmailJobThreadSync} {
		t.Run(kind, func(t *testing.T) {
			emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
				f := emailFixture(t, repo)
				request := EmailJobRequest{EmailCommand: f.command(), Kind: kind, TargetID: f.box.ID, MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, Rearm: true, RepeatInterval: time.Minute}
				_, err := f.repo.RequestEmailJob(t.Context(), request)
				f.must(err)
				job := f.claim(kind)
				now := postgresTime(time.Now())
				var finished app.EmailJob
				for attempt := 1; attempt <= job.MaxAttempts; attempt++ {
					retryAt := now.Add(time.Hour)
					lease := f.lease(job)
					lease.Now = now
					finished, err = f.repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: lease, ErrorCode: "provider_unavailable", RetryAt: retryAt})
					f.must(err)
					request.EmailCommand = f.command()
					again, err := f.repo.RequestEmailJob(t.Context(), request)
					f.must(err)
					if !reflect.DeepEqual(again, finished) {
						t.Fatal("periodic planner changed retry backoff or revived a failed job")
					}
					if attempt == job.MaxAttempts {
						break
					}
					if finished.State != app.EmailJobRetryWait || !finished.NextAttemptAt.Equal(retryAt) {
						t.Fatal("retry schedule was not retained")
					}
					now = retryAt
					var found bool
					job, found, err = f.repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{kind}, Now: now})
					f.must(err)
					if !found {
						t.Fatal("retry was not available at its deadline")
					}
				}
				if finished.State != app.EmailJobFailed || finished.Attempt != finished.MaxAttempts {
					t.Fatal("fixture did not exhaust the attempt budget")
				}
				f.repo = restartEmailRepeatRepository(t, f.repo)
				request.EmailCommand = f.command()
				afterRestart, err := f.repo.RequestEmailJob(t.Context(), request)
				f.must(err)
				if !reflect.DeepEqual(afterRestart, finished) {
					t.Fatal("restart plus periodic planning revived terminal failure")
				}
				_, found, err := f.repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{kind}, Now: now.Add(24 * time.Hour)})
				f.must(err)
				if found {
					t.Fatal("failed scan became claimable through the timer")
				}
				request.EmailCommand = f.command()
				request.RepeatInterval = 0
				manual, err := f.repo.RequestEmailJob(t.Context(), request)
				f.must(err)
				if manual.ID != finished.ID || manual.State != app.EmailJobQueued || manual.Attempt != 0 || manual.ErrorCode != "" || manual.NextAttemptAt.After(time.Now()) {
					t.Fatalf("manual sync did not restore failed work: %+v", manual)
				}
			})
		})
	}
}

func TestEmailManagementSourceRecoveryRearmsAfterExhaustedFailure(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		request := EmailJobRequest{EmailCommand: f.command(), Kind: app.EmailJobSourceRecovery, TargetID: f.owner, Rearm: true, RearmFailed: true, RepeatInterval: time.Minute}
		_, err := f.repo.RequestEmailJob(t.Context(), request)
		f.must(err)
		job := f.claim(app.EmailJobSourceRecovery)
		now := postgresTime(time.Now())
		for attempt := 1; attempt <= job.MaxAttempts; attempt++ {
			job, err = f.repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(job), ErrorCode: "store_conflict", RetryAt: now})
			f.must(err)
			if attempt < job.MaxAttempts {
				job = f.claim(app.EmailJobSourceRecovery)
			}
		}
		if job.State != app.EmailJobFailed {
			t.Fatalf("recovery fixture did not exhaust: %+v", job)
		}
		request.EmailCommand = f.command()
		rearmed, err := f.repo.RequestEmailJob(t.Context(), request)
		f.must(err)
		if rearmed.State != app.EmailJobQueued || rearmed.Attempt != 0 || rearmed.ErrorCode != "" {
			t.Fatalf("self-healing recovery stayed terminal: %+v", rearmed)
		}
	})
}

func TestEmailManagementPeriodicJobIntervalValidation(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		request := EmailJobRequest{Kind: app.EmailJobDiscover, TargetID: f.box.ID, MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, Rearm: true}
		for _, interval := range []time.Duration{-time.Nanosecond, 24*time.Hour + time.Nanosecond} {
			request.EmailCommand = f.command()
			request.RepeatInterval = interval
			if _, err := repo.RequestEmailJob(t.Context(), request); StoreErrorCodeOf(err) != StoreErrorInvalid {
				t.Fatalf("invalid interval %s was accepted: %v", interval, err)
			}
		}
		request.EmailCommand = f.command()
		request.RepeatInterval = 24 * time.Hour
		_, err := repo.RequestEmailJob(t.Context(), request)
		f.must(err)
		completed, err := repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(f.claim(app.EmailJobDiscover))})
		f.must(err)
		request.EmailCommand = f.command()
		request.NextAttemptAt = completed.UpdatedAt.Add(48 * time.Hour)
		again, err := repo.RequestEmailJob(t.Context(), request)
		f.must(err)
		if !again.NextAttemptAt.Equal(request.NextAttemptAt) {
			t.Fatal("repeat interval overrode an explicitly later next-attempt deadline")
		}
	})
}

func TestEmailManagementManualSyncAdvancesQueuedPeriodicDiscovery(t *testing.T) {
	for _, kind := range []string{app.EmailJobDiscover, app.EmailJobThreadSync} {
		t.Run(kind, func(t *testing.T) {
			emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
				f := emailFixture(t, repo)
				request := EmailJobRequest{EmailCommand: f.command(), Kind: kind, TargetID: f.box.ID, MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, Rearm: true, RepeatInterval: 20 * time.Minute}
				_, err := repo.RequestEmailJob(t.Context(), request)
				f.must(err)
				first := f.claim(kind)
				f.finish(first)
				request.EmailCommand = f.command()
				queued, err := repo.RequestEmailJob(t.Context(), request)
				f.must(err)
				if !queued.NextAttemptAt.After(time.Now()) {
					t.Fatal("test needs future periodic scan")
				}
				// Repeated background requests and explicit scheduled requests must
				// preserve the original completion-relative deadline.
				for _, scheduled := range []EmailJobRequest{request, {Kind: kind, TargetID: f.box.ID, MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, Rearm: true, NextAttemptAt: time.Now().Add(time.Hour)}} {
					scheduled.EmailCommand = f.command()
					unchanged, err := repo.RequestEmailJob(t.Context(), scheduled)
					f.must(err)
					if !reflect.DeepEqual(unchanged, queued) {
						t.Fatal("background/scheduled request advanced queued deadline")
					}
				}
				f.repo = restartEmailRepeatRepository(t, repo)
				manual := request
				manual.EmailCommand = f.command()
				manual.RepeatInterval = 0
				manual.NextAttemptAt = time.Time{}
				advanced, err := f.repo.RequestEmailJob(t.Context(), manual)
				f.must(err)
				if advanced.ID != queued.ID || advanced.State != app.EmailJobQueued || advanced.NextAttemptAt.After(time.Now()) {
					t.Fatal("manual sync failed to advance queued periodic scan")
				}
				running := f.claim(kind)
				manual.EmailCommand = f.command()
				unchanged, err := f.repo.RequestEmailJob(t.Context(), manual)
				f.must(err)
				if !reflect.DeepEqual(unchanged, running) {
					t.Fatal("manual sync changed active browser lease")
				}
				retryAt := time.Now().Add(time.Hour)
				retry, err := f.repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(running), ErrorCode: "provider_unavailable", RetryAt: retryAt})
				f.must(err)
				manual.EmailCommand = f.command()
				unchanged, err = f.repo.RequestEmailJob(t.Context(), manual)
				f.must(err)
				if !reflect.DeepEqual(unchanged, retry) {
					t.Fatal("manual sync discarded retry backoff")
				}
			})
		})
	}
}
