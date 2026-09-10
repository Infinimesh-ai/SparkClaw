package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestEmailManagementFileUnknownOutcomeAndRollback(t *testing.T) {
	for _, stage := range []string{"write", "dir_sync"} {
		t.Run(stage, func(t *testing.T) {
			s, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			f := emailFixture(t, s)
			c := EmailDiscoveryCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, ObservedAt: time.Now(), Cursor: "next", Coverage: "partial", Members: []EmailDiscoveryMember{{ProviderMessageID: "durable", ProviderSelectionID: "durable", Direction: "inbound"}}}
			s.commitOps = &controlledFileCommitOps{failStage: stage, failRemaining: 1}
			_, err = s.AdmitEmailDiscovery(t.Context(), c)
			want := StoreErrorDurability
			if stage == "dir_sync" {
				want = StoreErrorUnknownOutcome
			}
			if StoreErrorCodeOf(err) != want {
				t.Fatalf("outcome %v want %s", err, want)
			}
			receipt, found, err := s.ReconcileEmailCommand(t.Context(), f.owner, c.CommandKey)
			f.must(err)
			mails, err := s.ListEmailMails(t.Context(), EmailQuery{OwnerID: f.owner})
			f.must(err)
			jobs, err := s.ListEmailJobs(t.Context(), EmailQuery{OwnerID: f.owner})
			f.must(err)
			box, _, err := s.GetEmailMailbox(t.Context(), f.owner, f.box.ID)
			f.must(err)
			if stage == "write" {
				if found || len(mails.Items) != 0 || len(jobs) != 0 || box.Cursor != "" {
					t.Fatal("definite failure retained partial aggregate")
				}
			} else {
				if !found || len(mails.Items) != 1 || len(jobs) != 1 || box.Cursor != "next" || receipt.ContentHash == "" {
					t.Fatal("unknown commit did not reconcile whole aggregate")
				}
				replayed, err := s.AdmitEmailDiscovery(t.Context(), c)
				f.must(err)
				if replayed.Mails[0].ID != mails.Items[0].ID {
					t.Fatal("lost response duplicated mail")
				}
			}
		})
	}
}
func TestEmailManagementCorruptSnapshotRejected(t *testing.T) {
	s := NewMemoryStore()
	f := emailFixture(t, s)
	f.admit("corrupt", time.Now())
	snapshot := s.snapshot()
	for key, r := range snapshot.EmailRecords {
		if r.Kind == "mail" {
			r.Owner = "wrong-owner"
			snapshot.EmailRecords[key] = r
			break
		}
	}
	raw, err := json.Marshal(snapshot)
	f.must(err)
	path := filepath.Join(t.TempDir(), "state.json")
	f.must(os.WriteFile(path, raw, 0600))
	if _, err = NewFileStore(path); err == nil {
		t.Fatal("mismatched persisted owner was accepted")
	}
}
func TestEmailManagementRefreshIntentSurvivesFileRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	f := emailFixture(t, s)
	mails := []app.EmailMail{}
	for _, id := range []string{"a", "b", "c"} {
		m := f.parse(f.capture(f.admit(id, time.Now())), "<"+id+"@fixture>")
		mails = append(mails, m)
	}
	f.drain()
	before := map[string]int64{}
	for _, m := range mails {
		_, err = s.RequestEmailJob(t.Context(), EmailJobRequest{EmailCommand: f.command(), Kind: app.EmailJobMessageSummary, TargetID: m.ID, Dependencies: []string{"reply:<parent@fixture>"}})
		f.must(err)
		target, _, err := s.GetEmailAnalysisTarget(t.Context(), f.owner, app.EmailJobMessageSummary, m.ID)
		f.must(err)
		before[m.ID] = target.Generation
	}
	f.parse(f.capture(f.admit("parent", time.Now())), "<parent@fixture>")
	_, err = s.ExpandEmailRefresh(t.Context(), EmailRefreshCommand{EmailCommand: f.command(), Limit: 1})
	f.must(err)
	s, err = NewFileStore(path)
	f.must(err)
	f.repo = s
	f.drain()
	for _, m := range mails {
		target, _, err := s.GetEmailAnalysisTarget(t.Context(), f.owner, app.EmailJobMessageSummary, m.ID)
		f.must(err)
		if target.Generation <= before[m.ID] || target.State != app.EmailSummaryPending {
			t.Fatalf("unresolved reference refresh lost on restart: %+v", target)
		}
	}
}
func TestEmailManagementOverflowKeepsDiscoveryProgress(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		f.admit("existing", time.Now())
		c := EmailDiscoveryCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, MaxPendingJobs: 1, ObservedAt: time.Now(), Cursor: "must-not-advance", Coverage: "partial", Members: []EmailDiscoveryMember{{ProviderMessageID: "overflow", ProviderSelectionID: "overflow", Direction: "inbound"}}}
		if _, err := repo.AdmitEmailDiscovery(t.Context(), c); StoreErrorCodeOf(err) != StoreErrorConflict {
			t.Fatalf("overflow accepted: %v", err)
		}
		box, _, err := repo.GetEmailMailbox(t.Context(), f.owner, f.box.ID)
		f.must(err)
		if box.Cursor != "" {
			t.Fatal("overflow advanced cursor")
		}
		mails, err := repo.ListEmailMails(t.Context(), EmailQuery{OwnerID: f.owner})
		f.must(err)
		if len(mails.Items) != 1 {
			t.Fatal("overflow partially admitted identities")
		}
	})
}
func TestEmailManagementIdenticalThreadObservationCoalesces(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		c := EmailDiscoveryCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, ObservedAt: time.Now(), Trigger: "thread_sync", ThreadID: "stable-thread", Coverage: "complete_for_observation"}
		_, err := repo.AdmitEmailDiscovery(t.Context(), c)
		f.must(err)
		threads, err := repo.ListEmailThreads(t.Context(), EmailQuery{OwnerID: f.owner, MailboxID: f.box.ID})
		f.must(err)
		version := threads[0].ObservationVersion
		c.EmailCommand = f.command()
		c.ObservedAt = time.Now()
		_, err = repo.AdmitEmailDiscovery(t.Context(), c)
		f.must(err)
		c.EmailCommand = f.command()
		synced, _, err := repo.GetEmailThread(t.Context(), f.owner, threads[0].ID)
		f.must(err)
		checkedAt := synced.LastCheckedAt
		c.Trigger = "thread_discovery"
		c.Coverage = "pending"
		_, err = repo.AdmitEmailDiscovery(t.Context(), c)
		f.must(err)
		thread, _, err := repo.GetEmailThread(t.Context(), f.owner, threads[0].ID)
		f.must(err)
		if thread.ObservationVersion != version || thread.Coverage != "complete_for_observation" || !thread.LastCheckedAt.Equal(checkedAt) {
			t.Fatal("identical thread check caused reanalysis or coverage regression")
		}
	})
}

func TestEmailManagementFailureRetryAndCurrentCoalescing(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		m := f.parse(f.capture(f.admit("retry", time.Now())), "<retry@fixture>")
		f.drain()
		j := f.claim(app.EmailJobMessageSummary)
		_, err := repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(j), ErrorCode: "model_unavailable"})
		f.must(err)
		target, _, err := repo.GetEmailAnalysisTarget(t.Context(), f.owner, j.Kind, m.ID)
		f.must(err)
		if target.State != app.EmailSummaryFailed {
			t.Fatal("exhausted failure not visible")
		}
		_, err = repo.RequestEmailJob(t.Context(), EmailJobRequest{EmailCommand: f.command(), Kind: j.Kind, TargetID: m.ID})
		f.must(err)
		target, _, err = repo.GetEmailAnalysisTarget(t.Context(), f.owner, j.Kind, m.ID)
		f.must(err)
		if target.State != app.EmailSummaryFailed {
			t.Fatal("ordinary request hid exhausted failure")
		}
		_, err = repo.RequestEmailJob(t.Context(), EmailJobRequest{EmailCommand: f.command(), Kind: j.Kind, TargetID: m.ID, Rearm: true})
		f.must(err)
		j = f.claim(j.Kind)
		target, _, err = repo.GetEmailAnalysisTarget(t.Context(), f.owner, j.Kind, m.ID)
		f.must(err)
		if target.State != app.EmailSummaryPending {
			t.Fatal("explicit retry did not reset target")
		}
		summary, err := repo.PublishEmailSummary(t.Context(), EmailSummaryCommand{EmailCommand: f.command(), Lease: f.lease(j), Summary: app.EmailSummary{ID: "retry-success", TargetKind: j.Kind, TargetID: m.ID, Generation: j.Generation, InputFingerprint: j.InputFingerprint, Text: "current summary", ModelVersion: "fixture", PromptVersion: "v1"}})
		f.must(err)
		if !summary.Current {
			t.Fatal("retried summary not current")
		}
		f.finish(j)
		coalesced, err := repo.RequestEmailJob(t.Context(), EmailJobRequest{EmailCommand: f.command(), Kind: j.Kind, TargetID: m.ID, Rearm: true})
		f.must(err)
		if coalesced.State != app.EmailJobSucceeded || coalesced.Generation != j.Generation {
			t.Fatal("unchanged current summary reanalyzed")
		}
	})
}

func TestEmailManagementConcernClearKeepsHistoryAndMembership(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		m := f.assign(f.parse(f.capture(f.admit("concern", time.Now())), "<concern@fixture>"), "topic")
		f.drain()
		j := f.claim(app.EmailJobRelationshipCheck)
		proof := EmailConcernCommand{EmailCommand: f.command(), Lease: f.lease(j), TargetID: j.TargetID, Generation: j.Generation, Concern: app.EmailAssignmentConcern{ID: "first-check", Kind: app.EmailConcernPendingCorrection, MailIDs: []string{m.ID}, ConversationIDs: []string{m.ConversationID}, Reason: "ambiguous attachment", InputFingerprint: j.InputFingerprint}}
		_, err := repo.PublishEmailConcern(t.Context(), proof)
		f.must(err)
		f.finish(j)
		_, err = repo.PublishEmailContext(t.Context(), EmailContextCommand{EmailCommand: f.command(), Context: app.EmailContextVersion{ID: "resolved-attachment", MailID: m.ID, Coverage: "complete"}})
		f.must(err)
		f.drain()
		j = f.claim(app.EmailJobRelationshipCheck)
		_, err = repo.PublishEmailConcern(t.Context(), EmailConcernCommand{EmailCommand: f.command(), Lease: f.lease(j), TargetID: j.TargetID, Generation: j.Generation, Concern: app.EmailAssignmentConcern{ID: "clear-check", Kind: app.EmailConcernNone, InputFingerprint: j.InputFingerprint, Reason: "context resolved"}})
		f.must(err)
		f.finish(j)
		concerns, err := repo.ListEmailConcerns(t.Context(), EmailQuery{OwnerID: f.owner, ConversationID: m.ConversationID})
		f.must(err)
		if len(concerns) != 0 {
			t.Fatal("cleared concern remains visible")
		}
		target, _, err := repo.GetEmailAnalysisTarget(t.Context(), f.owner, j.Kind, m.ID)
		f.must(err)
		if target.ConcernID != "clear-check" || target.State != app.EmailSummaryCurrent {
			t.Fatal("clear proof not current")
		}
		stored, _, err := repo.GetEmailMail(t.Context(), f.owner, m.ID)
		f.must(err)
		if stored.ConversationID != m.ConversationID {
			t.Fatal("clear check moved membership")
		}
		receipt, found, err := repo.ReconcileEmailCommand(t.Context(), f.owner, proof.CommandKey)
		f.must(err)
		if !found || len(receipt.Result) == 0 {
			t.Fatal("prior concern proof history lost")
		}
	})
}
func TestEmailManagementExpiredDiscoveryCannotAdvanceCursor(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		_, err := repo.RequestEmailJob(t.Context(), EmailJobRequest{EmailCommand: f.command(), Kind: app.EmailJobDiscover, TargetID: f.box.ID, MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration})
		f.must(err)
		old := f.claim(app.EmailJobDiscover)
		_, ok, err := repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobDiscover}, Now: time.Now().Add(2 * time.Minute), LeaseDuration: time.Minute})
		f.must(err)
		if !ok {
			t.Fatal("discovery lease not reclaimed")
		}
		_, err = repo.AdmitEmailDiscovery(t.Context(), EmailDiscoveryCommand{EmailCommand: f.command(), Lease: f.lease(old), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, ObservedAt: time.Now(), Cursor: "stale-progress", Coverage: "partial"})
		if StoreErrorCodeOf(err) != StoreErrorConflict {
			t.Fatalf("stale discovery published: %v", err)
		}
		box, _, err := repo.GetEmailMailbox(t.Context(), f.owner, f.box.ID)
		f.must(err)
		if box.Cursor != "" {
			t.Fatal("stale discovery advanced cursor")
		}
	})
}

func TestEmailManagementTransitiveSummaryStaleBeforeFanout(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		m := f.assign(f.parse(f.capture(f.admit("member", time.Now())), "<member@fixture>"), "topic")
		related := f.capture(f.admit("related", time.Now()))
		_, err := repo.RequestEmailJob(t.Context(), EmailJobRequest{EmailCommand: f.command(), Kind: app.EmailJobMessageSummary, TargetID: m.ID, Dependencies: []string{"mail:" + related.ID}})
		f.must(err)
		f.drain()
		j := f.claim(app.EmailJobMessageSummary)
		_, err = repo.PublishEmailSummary(t.Context(), EmailSummaryCommand{EmailCommand: f.command(), Lease: f.lease(j), Summary: app.EmailSummary{ID: "individual-current", TargetKind: j.Kind, TargetID: j.TargetID, Generation: j.Generation, InputFingerprint: j.InputFingerprint, Text: "summary using related context", ModelVersion: "fixture", PromptVersion: "v1"}})
		f.must(err)
		f.finish(j)
		_, err = repo.RequestEmailJob(t.Context(), EmailJobRequest{EmailCommand: f.command(), Kind: app.EmailJobConversationSummary, TargetID: m.ConversationID, Dependencies: []string{"summary:" + app.EmailJobMessageSummary + ":" + m.ID}})
		f.must(err)
		f.drain()
		j = f.claim(app.EmailJobConversationSummary)
		_, err = repo.PublishEmailSummary(t.Context(), EmailSummaryCommand{EmailCommand: f.command(), Lease: f.lease(j), Summary: app.EmailSummary{ID: "conversation-current", TargetKind: j.Kind, TargetID: j.TargetID, Generation: j.Generation, InputFingerprint: j.InputFingerprint, Text: "conversation uses individual summary", ModelVersion: "fixture", PromptVersion: "v1"}})
		f.must(err)
		f.finish(j)
		oldGeneration := j.Generation
		_, err = repo.PublishEmailContext(t.Context(), EmailContextCommand{EmailCommand: f.command(), Context: app.EmailContextVersion{ID: "related-improved", MailID: related.ID, Coverage: "complete"}})
		f.must(err)
		conversation, _, err := repo.GetEmailConversation(t.Context(), f.owner, m.ConversationID)
		f.must(err)
		if conversation.Summary == nil || conversation.Summary.Current {
			t.Fatal("transitively stale summary appeared current before fanout")
		}
		f.drain()
		target, _, err := repo.GetEmailAnalysisTarget(t.Context(), f.owner, app.EmailJobConversationSummary, m.ConversationID)
		f.must(err)
		if target.Generation <= oldGeneration || target.State != app.EmailSummaryPending {
			t.Fatal("transitive refresh has no durable continuation")
		}
	})
}
func TestEmailManagementExplicitDependencyWindowsReplacePriorSelection(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		m := f.assign(f.parse(f.capture(f.admit("window-root", time.Now())), "<window@fixture>"), "topic")
		ids := []string{}
		for batch := 0; batch < 3; batch++ {
			members := []EmailDiscoveryMember{}
			for n := 0; n < 80; n++ {
				id := fmt.Sprintf("context-%d-%d", batch, n)
				members = append(members, EmailDiscoveryMember{ProviderMessageID: id, ProviderSelectionID: id, Direction: "inbound"})
			}
			result, err := repo.AdmitEmailDiscovery(t.Context(), EmailDiscoveryCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, Members: members, ObservedAt: time.Now(), Coverage: "partial"})
			f.must(err)
			for _, mail := range result.Mails {
				ids = append(ids, mail.ID)
			}
		}
		for window := 0; window < 12; window++ {
			refs := []string{}
			for _, id := range ids[window*20 : (window+1)*20] {
				refs = append(refs, "mail:"+id, "summary:"+app.EmailJobMessageSummary+":"+id)
			}
			_, err := repo.RequestEmailJob(t.Context(), EmailJobRequest{EmailCommand: f.command(), Kind: app.EmailJobConversationSummary, TargetID: m.ConversationID, Dependencies: refs})
			f.must(err)
			target, _, err := repo.GetEmailAnalysisTarget(t.Context(), f.owner, app.EmailJobConversationSummary, m.ConversationID)
			f.must(err)
			if len(target.Inputs) != 41 {
				t.Fatalf("bounded window accumulated %d inputs", len(target.Inputs))
			}
		}
		_, err := repo.RequestEmailJob(t.Context(), EmailJobRequest{EmailCommand: f.command(), Kind: app.EmailJobConversationSummary, TargetID: m.ConversationID})
		f.must(err)
		target, _, err := repo.GetEmailAnalysisTarget(t.Context(), f.owner, app.EmailJobConversationSummary, m.ConversationID)
		f.must(err)
		if len(target.Inputs) != 41 {
			t.Fatal("bare refresh lost current selected dependencies")
		}
		// Returning to a previously selected window reuses the idempotency key,
		// but must adopt the latest generation and fence any old lease.
		refs := []string{}
		for _, id := range ids[:20] {
			refs = append(refs, "mail:"+id, "summary:"+app.EmailJobMessageSummary+":"+id)
		}
		job, err := repo.RequestEmailJob(t.Context(), EmailJobRequest{EmailCommand: f.command(), Kind: app.EmailJobConversationSummary, TargetID: m.ConversationID, Dependencies: refs})
		f.must(err)
		current, _, err := repo.GetEmailAnalysisTarget(t.Context(), f.owner, job.Kind, job.TargetID)
		f.must(err)
		if job.Generation != current.Generation || job.State != app.EmailJobQueued {
			t.Fatal("revisited fingerprint retained historical job generation")
		}

	})
}

func TestEmailManagementIdleFileCommandsDoNotRewriteSnapshot(t *testing.T) {
	s, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	f := emailFixture(t, s)
	before, err := s.GetEmailOwnerStatus(t.Context(), f.owner)
	f.must(err)
	ops := &controlledFileCommitOps{failStage: "encode", failRemaining: 1}
	s.commitOps = ops
	for n := 0; n < 10; n++ {
		_, found, err := s.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobCapture}})
		f.must(err)
		if found {
			t.Fatal("idle fixture has a job")
		}
		result, err := s.ExpandEmailRefresh(t.Context(), EmailRefreshCommand{EmailCommand: f.command(), Limit: 50})
		f.must(err)
		if result.Remaining || result.Processed != 0 {
			t.Fatal("idle fixture has a refresh")
		}
	}
	after, err := s.GetEmailOwnerStatus(t.Context(), f.owner)
	f.must(err)
	if after.Revision != before.Revision || ops.failRemaining != 1 {
		t.Fatal("idle polling mutated or encoded the File snapshot")
	}
	_, err = s.AdmitEmailDiscovery(t.Context(), EmailDiscoveryCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, ObservedAt: time.Now(), Members: []EmailDiscoveryMember{{ProviderMessageID: "real-write", ProviderSelectionID: "real-write", Direction: "inbound"}}})
	if StoreErrorCodeOf(err) != StoreErrorDurability {
		t.Fatalf("actual mutation bypassed durable replacement: %v", err)
	}
}
func TestEmailManagementFreshCapturePrecedesHistoryBacklog(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		for batch := 0; batch < 2; batch++ {
			members := []EmailDiscoveryMember{}
			for n := 0; n < 60; n++ {
				id := fmt.Sprintf("history-%d-%d", batch, n)
				members = append(members, EmailDiscoveryMember{ProviderMessageID: id, ProviderSelectionID: id, Direction: "inbound", Reason: app.EmailJobThreadSync})
			}
			_, err := repo.AdmitEmailDiscovery(t.Context(), EmailDiscoveryCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, ObservedAt: time.Now(), Members: members, Coverage: "partial"})
			f.must(err)
		}
		fresh := f.admit("fresh-arrival", time.Now())
		job := f.claim(app.EmailJobCapture)
		if job.TargetID != fresh.ID {
			t.Fatal("new mail starved behind history capture window")
		}
		f.finish(job)
		history := f.claim(app.EmailJobCapture)
		if history.Priority != app.EmailJobPriorityHistory {
			t.Fatal("history work not preserved as lower priority")
		}
	})
}

func TestEmailManagementMailboxFailureClearsOnlyAfterObservation(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		request := EmailJobRequest{EmailCommand: f.command(), Kind: app.EmailJobDiscover, TargetID: f.box.ID, MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration}
		_, err := repo.RequestEmailJob(t.Context(), request)
		f.must(err)
		j := f.claim(app.EmailJobDiscover)
		_, err = repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(j), ErrorCode: "provider_unavailable"})
		f.must(err)
		box, _, err := repo.GetEmailMailbox(t.Context(), f.owner, f.box.ID)
		f.must(err)
		if box.ErrorCode != "provider_unavailable" {
			t.Fatal("terminal intake failure not visible")
		}
		request.EmailCommand = f.command()
		request.Rearm = true
		_, err = repo.RequestEmailJob(t.Context(), request)
		f.must(err)
		box, _, err = repo.GetEmailMailbox(t.Context(), f.owner, f.box.ID)
		f.must(err)
		if box.ErrorCode == "" {
			t.Fatal("rearm cleared failure without an observation")
		}
		j = f.claim(app.EmailJobDiscover)
		_, err = repo.AdmitEmailDiscovery(t.Context(), EmailDiscoveryCommand{EmailCommand: f.command(), Lease: f.lease(j), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, ObservedAt: time.Now(), Trigger: "recent_inbound", Coverage: "partial"})
		f.must(err)
		box, _, err = repo.GetEmailMailbox(t.Context(), f.owner, f.box.ID)
		f.must(err)
		if box.ErrorCode != "" {
			t.Fatal("successful observation retained old failure")
		}
	})
}
