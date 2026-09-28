package store

import (
	"encoding/json"
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

func TestEmailAdaptivePollRecheckMissAndRecovery(t *testing.T) {
	repo := NewMemoryStore()
	f := emailFixture(t, repo)
	setMailbox := func(change func(*app.EmailMailbox)) {
		t.Helper()
		repo.mu.Lock()
		defer repo.mu.Unlock()
		key := emailRecordKey(f.owner, "mailbox", f.box.ID)
		record := repo.emailRecords[key]
		var box app.EmailMailbox
		if err := json.Unmarshal(record.Data, &box); err != nil {
			t.Fatal(err)
		}
		change(&box)
		record.Data = emailJSON(box)
		repo.emailRecords[key] = record
	}
	setMailbox(func(box *app.EmailMailbox) { box.WatchState, box.WakeAdapterQualified = "watching", true })
	request := pollRequest(f)
	request.RepeatInterval = 5 * time.Minute
	_, err := repo.RequestEmailJob(t.Context(), request)
	f.must(err)
	round := func(newTail bool) app.EmailJob {
		t.Helper()
		jobs, err := repo.ListEmailJobs(t.Context(), EmailQuery{OwnerID: f.owner, MailboxID: f.box.ID})
		f.must(err)
		job, found, err := repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobDiscover}, Now: jobs[0].NextAttemptAt, LeaseDuration: 10 * time.Minute})
		f.must(err)
		if !found {
			t.Fatal("adaptive round not claimable")
		}
		upper := f.box.Boundary.Add(time.Minute)
		if !f.box.PollThrough.IsZero() {
			upper = f.box.PollThrough.Add(time.Minute)
		}
		checkpoint := beginTimelineSync(t, f, upper)
		commitTimelineSync(t, f, checkpoint, nil)
		if newTail {
			setMailbox(func(box *app.EmailMailbox) { box.RoundNewTailFound = true })
		}
		finished, err := repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(job)})
		f.must(err)
		f.box, _, err = repo.GetEmailMailbox(t.Context(), f.owner, f.box.ID)
		f.must(err)
		return finished
	}
	first := round(false)
	if !first.PostActivityRecheck || !first.NextAttemptAt.Equal(first.UpdatedAt.Add(time.Minute)) {
		t.Fatalf("first finish did not arm 60-second recheck: %+v", first)
	}
	quiet := round(false)
	if quiet.PostActivityRecheck || !quiet.NextAttemptAt.Equal(quiet.UpdatedAt.Add(5*time.Minute)) {
		t.Fatalf("recheck did not arm five-minute quiet fallback: %+v", quiet)
	}
	miss := round(true)
	if miss.PollInterval != time.Minute || !miss.NextAttemptAt.Equal(miss.UpdatedAt.Add(time.Minute)) || f.box.LastPeriodicOnlyDiscovery.IsZero() {
		t.Fatalf("periodic-only discovery did not shorten immediately: %+v", miss)
	}
	recovery := round(false)
	if recovery.PollInterval != 2*time.Minute || !recovery.NextAttemptAt.Equal(recovery.UpdatedAt.Add(2*time.Minute)) || f.box.PeriodicEmptyStreak != 1 {
		t.Fatalf("empty periodic recheck did not double recovery interval: %+v", recovery)
	}
}

func TestEmailMinutePollRefreshCoalescesThroughFollowupAndRestart(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		_, err := repo.RequestEmailJob(t.Context(), pollRequest(f))
		f.must(err)
		auto := f.claim(app.EmailJobDiscover)
		initial := beginTimelineSync(t, f, f.box.Boundary.Add(time.Minute))
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
		requestIDs := map[string]bool{}
		for j := range results {
			if j.RefreshRequestID == "" || requestIDs[j.RefreshRequestID] || !j.RefreshPending || j.RefreshActiveID != "" || j.LeaseToken != auto.LeaseToken {
				t.Fatal("parallel requests did not retain distinct acceptances behind active automatic lease")
			}
			requestIDs[j.RefreshRequestID] = true
		}
		if len(requestIDs) != callers {
			t.Fatal("one or more refresh requests were not accepted")
		}
		f.repo = restartEmailRepeatRepository(t, repo)
		commitTimelineSync(t, f, initial, nil)
		queued, err := f.repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(auto)})
		f.must(err)
		if !queued.RefreshPending || queued.State != app.EmailJobQueued || !queued.NextAttemptAt.Equal(queued.UpdatedAt) {
			t.Fatal("missing immediate follow-up")
		}
		followup := f.claim(app.EmailJobDiscover)
		if !requestIDs[followup.RefreshActiveID] || followup.SyncTrigger != "manual_refresh" {
			t.Fatal("followup lost request identity")
		}
		if !followup.RoundStartedAt.Equal(auto.RoundStartedAt) {
			t.Fatal("followup restarted the logical round")
		}
		checkpoint := beginTimelineSync(t, f, f.box.PollThrough.Add(time.Minute))
		newer, err := f.repo.RequestEmailJob(t.Context(), manualPollRequest(f))
		f.must(err)
		commitTimelineSync(t, f, checkpoint, nil)
		finished, err := f.repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(followup)})
		f.must(err)
		if !finished.RefreshPending || finished.State != app.EmailJobQueued || !finished.NextAttemptAt.Equal(finished.UpdatedAt) {
			t.Fatal("Refresh accepted during running manual query did not schedule exactly one follow-up")
		}
		last := f.claim(app.EmailJobDiscover)
		if last.RefreshActiveID != newer.RefreshRequestID {
			t.Fatal("follow-up query did not capture the newest Refresh")
		}
		if !last.RoundStartedAt.Equal(auto.RoundStartedAt) {
			t.Fatal("second followup restarted the logical round")
		}
		checkpoint = beginTimelineSync(t, f, f.box.PollThrough.Add(time.Minute))
		commitTimelineSync(t, f, checkpoint, nil)
		finished, err = f.repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(last)})
		f.must(err)
		if finished.RefreshPending || finished.RefreshActiveID != "" || !finished.NextAttemptAt.Equal(finished.UpdatedAt.Add(time.Minute)) {
			t.Fatal("settled refresh did not restart automatic idle")
		}
		box, _, err := f.repo.GetEmailMailbox(t.Context(), f.owner, f.box.ID)
		f.must(err)
		if box.RefreshPending || box.RefreshRequestID != newer.RefreshRequestID {
			t.Fatal("mailbox projection did not retain completion identity")
		}
		requestIDs[newer.RefreshRequestID] = true
		for id := range requestIDs {
			stored := readSyncRefreshRequestRecord(t, f.repo, f.owner, id)
			if stored.State != "settled" || stored.SettledAt == nil || stored.RefreshRevision < 1 || stored.MailboxID != f.box.ID {
				t.Fatal("accepted Refresh did not retain its own durable settlement")
			}
		}
		if _, found, err := f.repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobDiscover}}); found || err != nil {
			t.Fatal("duplicate clicks created another immediate round")
		}
	})
}

func readSyncRefreshRequestRecord(t *testing.T, repo EmailRepository, ownerID, id string) emailSyncRefreshRequest {
	t.Helper()
	var raw []byte
	switch s := repo.(type) {
	case *MemoryStore:
		raw = s.snapshot().EmailRecords[emailRecordKey(ownerID, "sync_refresh_request", id)].Data
	case *FileStore:
		raw = s.inner.snapshot().EmailRecords[emailRecordKey(ownerID, "sync_refresh_request", id)].Data
	case *PostgresStore:
		if err := s.db.QueryRow(t.Context(), `SELECT payload FROM email_management_records WHERE owner_id=$1 AND kind='sync_refresh_request' AND id=$2`, ownerID, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unexpected email backend %T", repo)
	}
	var request emailSyncRefreshRequest
	if len(raw) == 0 || json.Unmarshal(raw, &request) != nil || request.ID != id || request.OwnerID != ownerID {
		t.Fatal("durable Refresh record is missing or invalid")
	}
	return request
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

func TestEmailMinutePollManualRetryExhaustionAndPause(t *testing.T) {
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
			if !finished.RefreshPending {
				t.Fatalf("attempt %d lost the unsettled Refresh", attempt)
			}
			if attempt == 5 && (finished.State != app.EmailJobQueued || !finished.NextAttemptAt.Equal(finished.UpdatedAt.Add(time.Minute))) {
				t.Fatalf("exhausted retry did not preserve periodic recovery: %+v", finished)
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

func TestEmailRefreshRevisionFrozenBeforeProviderQuery(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		_, err := repo.RequestEmailJob(t.Context(), pollRequest(f))
		f.must(err)
		first := f.claim(app.EmailJobDiscover)
		checkpoint := beginTimelineSync(t, f, f.box.Boundary.Add(time.Minute))
		if checkpoint.QueryRevision != 0 {
			t.Fatalf("unexpected first query revision: %d", checkpoint.QueryRevision)
		}
		accepted, err := repo.RequestEmailJob(t.Context(), manualPollRequest(f))
		f.must(err)
		if accepted.RefreshRevision != 1 || accepted.RefreshRequestID == "" {
			t.Fatalf("running query swallowed Refresh: %+v", accepted)
		}
		commitTimelineSync(t, f, checkpoint, nil)
		if f.box.ReconciledRevision != 0 || f.box.SignalRevision != 1 {
			t.Fatalf("query settled an intent accepted after Begin: %+v", f.box)
		}
		queued, err := repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(first)})
		f.must(err)
		if !queued.RefreshPending || queued.State != app.EmailJobQueued || !queued.NextAttemptAt.Equal(queued.UpdatedAt) {
			t.Fatalf("missing immediate follow-up: %+v", queued)
		}
		second := f.claim(app.EmailJobDiscover)
		checkpoint = beginTimelineSync(t, f, f.box.PollThrough.Add(time.Minute))
		if checkpoint.QueryRevision != 1 {
			t.Fatalf("follow-up did not capture Refresh revision: %d", checkpoint.QueryRevision)
		}
		commitTimelineSync(t, f, checkpoint, nil)
		finished, err := repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(second)})
		f.must(err)
		if finished.RefreshPending || finished.State != app.EmailJobQueued || !finished.NextAttemptAt.Equal(finished.UpdatedAt.Add(time.Minute)) {
			t.Fatalf("covered Refresh did not settle: %+v", finished)
		}
	})
}

func TestEmailNotificationHintDeduplicatesAndSurvivesQuery(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		_, err := repo.RequestEmailJob(t.Context(), pollRequest(f))
		f.must(err)
		job := f.claim(app.EmailJobDiscover)
		checkpoint := beginTimelineSync(t, f, f.box.Boundary.Add(time.Minute))
		hint := pollRequest(f)
		hint.AutomaticPoll, hint.RepeatInterval = false, 0
		hint.SyncTrigger, hint.SyncActor = "notification_hint", f.owner
		hint.EventEpoch, hint.EventSequence = "owned-document-1", 1
		_, err = repo.RequestEmailJob(t.Context(), hint)
		f.must(err)
		hint.EmailCommand = f.command()
		_, err = repo.RequestEmailJob(t.Context(), hint)
		f.must(err)
		box, _, err := repo.GetEmailMailbox(t.Context(), f.owner, f.box.ID)
		f.must(err)
		if box.SignalRevision != 1 || box.LastEventSequence != 1 {
			t.Fatalf("redelivery was not deduplicated: %+v", box)
		}
		commitTimelineSync(t, f, checkpoint, nil)
		queued, err := repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(job)})
		f.must(err)
		if queued.State != app.EmailJobQueued || !queued.NextAttemptAt.Equal(queued.UpdatedAt) {
			t.Fatalf("hint accepted during list did not wake a new query: %+v", queued)
		}
		job = f.claim(app.EmailJobDiscover)
		checkpoint = beginTimelineSync(t, f, f.box.PollThrough.Add(time.Minute))
		if checkpoint.QueryRevision != 1 {
			t.Fatalf("notification revision not captured: %d", checkpoint.QueryRevision)
		}
		commitTimelineSync(t, f, checkpoint, nil)
		if f.box.RefreshPending {
			t.Fatal("qualified query left accepted Refresh pending until round finish")
		}
		finished, err := repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(job)})
		f.must(err)
		if finished.State != app.EmailJobQueued || !finished.NextAttemptAt.Equal(finished.UpdatedAt.Add(time.Minute)) {
			t.Fatalf("hint failed to settle: %+v", finished)
		}
	})
}

func TestEmailRefreshAcceptedBeforeBeginSettlesInThatQuery(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		_, err := repo.RequestEmailJob(t.Context(), pollRequest(f))
		f.must(err)
		job := f.claim(app.EmailJobDiscover)
		_, err = repo.RequestEmailJob(t.Context(), manualPollRequest(f))
		f.must(err)
		checkpoint := beginTimelineSync(t, f, f.box.Boundary.Add(time.Minute))
		if checkpoint.QueryRevision != 1 {
			t.Fatalf("Begin did not capture the accepted request: %d", checkpoint.QueryRevision)
		}
		commitTimelineSync(t, f, checkpoint, nil)
		finished, err := repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(job)})
		f.must(err)
		if finished.RefreshPending || !finished.NextAttemptAt.Equal(finished.UpdatedAt.Add(time.Minute)) {
			t.Fatalf("covered request caused a duplicate query: %+v", finished)
		}
	})
}

func TestEmailUnqualifiedQueryRetainsHintWithoutImmediateRetry(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		_, err := repo.RequestEmailJob(t.Context(), pollRequest(f))
		f.must(err)
		hint := pollRequest(f)
		hint.AutomaticPoll, hint.RepeatInterval = false, 0
		hint.SyncTrigger, hint.SyncActor = "notification_hint", f.owner
		hint.EventEpoch, hint.EventSequence = "owned-document-1", 1
		_, err = repo.RequestEmailJob(t.Context(), hint)
		f.must(err)
		job := f.claim(app.EmailJobDiscover)
		checkpoint := beginTimelineSync(t, f, f.box.Boundary.Add(time.Minute))
		commitTimelineSync(t, f, checkpoint, func(command *EmailSyncCommitCommand) {
			command.Complete = false
			command.ErrorCode = "email_provider_unavailable"
			command.FailureScope = app.EmailSyncFailureProviderOperational
		})
		finished, err := repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(job)})
		f.must(err)
		if f.box.SignalRevision != 1 || f.box.ReconciledRevision != 0 ||
			finished.State != app.EmailJobQueued || !finished.NextAttemptAt.Equal(finished.UpdatedAt.Add(time.Minute)) {
			t.Fatalf("unqualified result retried immediately or lost the hint: mailbox=%+v job=%+v", f.box, finished)
		}
		hint.EventSequence = 2
		hint.EmailCommand = f.command()
		afterHint, err := repo.RequestEmailJob(t.Context(), hint)
		f.must(err)
		if !afterHint.NextAttemptAt.Equal(finished.NextAttemptAt) {
			t.Fatalf("new hint bypassed the incomplete query's recovery wait: %+v", afterHint)
		}
	})
}

func TestEmailHintBeforeFinalCheckCannotBeSettledByOldInterval(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		_, err := repo.RequestEmailJob(t.Context(), pollRequest(f))
		f.must(err)
		_, err = repo.RequestEmailJob(t.Context(), manualPollRequest(f))
		f.must(err)
		job := f.claim(app.EmailJobDiscover)
		increment := beginTimelineSync(t, f, f.box.Boundary.Add(time.Minute))
		commitTimelineSync(t, f, increment, nil)
		hint := pollRequest(f)
		hint.AutomaticPoll, hint.RepeatInterval = false, 0
		hint.SyncTrigger, hint.SyncActor = "notification_hint", f.owner
		hint.EventEpoch, hint.EventSequence = "owned-document-2", 1
		_, err = repo.RequestEmailJob(t.Context(), hint)
		f.must(err)
		check, err := repo.BeginEmailSync(t.Context(), EmailSyncBeginCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, ProviderMode: app.EmailProviderModeTimeRange, UpperBound: increment.IntervalEnd, FinalCheck: true, Trigger: "test", Actor: "system"})
		f.must(err)
		if check.Kind != "final_check" || check.QueryRevision != increment.QueryRevision || !check.IntervalEnd.Equal(increment.IntervalEnd) {
			t.Fatalf("final check copied a newer intent or widened tail: %+v", check)
		}
		commitTimelineSync(t, f, check, nil)
		if f.box.SignalRevision != 2 || f.box.ReconciledRevision != 1 {
			t.Fatalf("final check settled hint received after increment: %+v", f.box)
		}
		queued, err := repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(job)})
		f.must(err)
		if queued.State != app.EmailJobQueued || !queued.NextAttemptAt.Equal(queued.UpdatedAt) {
			t.Fatalf("unsettled hint did not queue another increment: %+v", queued)
		}
	})
}

func TestEmailBindingReplacementReanchorsQueryButKeepsSignalCounter(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		_, err := repo.RequestEmailJob(t.Context(), pollRequest(f))
		f.must(err)
		_, err = repo.RequestEmailJob(t.Context(), manualPollRequest(f))
		f.must(err)
		hint := pollRequest(f)
		hint.AutomaticPoll, hint.RepeatInterval = false, 0
		hint.SyncTrigger, hint.SyncActor = "notification_hint", f.owner
		hint.EventEpoch, hint.EventSequence = "previous-document", 7
		_, err = repo.RequestEmailJob(t.Context(), hint)
		f.must(err)
		checkpoint := beginTimelineSync(t, f, f.box.Boundary.Add(time.Minute))
		rebound, err := repo.BindEmailMailbox(t.Context(), EmailBindCommand{EmailCommand: f.command(), Provider: f.box.Provider, Address: f.box.Address, Enabled: true, ExpectedVersion: f.box.Version})
		f.must(err)
		if !rebound.InflightUntil.IsZero() || !rebound.InflightStart.IsZero() || rebound.InflightKind != "" || !rebound.LastIntervalStart.IsZero() || rebound.SignalRevision != checkpoint.Mailbox.SignalRevision || !rebound.PollThrough.Equal(checkpoint.Mailbox.PollThrough) || rebound.LastEventEpoch != "" || rebound.LastEventSequence != 0 {
			t.Fatalf("rebind leaked stale query or reset lifetime revision: %+v", rebound)
		}
	})
}
