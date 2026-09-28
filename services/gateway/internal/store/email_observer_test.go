package store

import (
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"testing"
	"time"
)

func TestEmailObserverDeliveryFenceAndDurableReplay(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		apply := func(c EmailObserverCommand) EmailObserverResult {
			t.Helper()
			c.EmailCommand = f.command()
			out, err := f.repo.ApplyEmailObserver(t.Context(), c)
			f.must(err)
			return out
		}
		c := EmailObserverCommand{MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, CredentialGeneration: 7, Action: "register", Epoch: "controller-a", State: "starting"}
		if !apply(c).Accepted {
			t.Fatal("registration rejected")
		}
		_, err := repo.RequestEmailJob(t.Context(), pollRequest(f))
		f.must(err)
		j, found, err := repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobDiscover}, Now: time.Now().Add(time.Second), LeaseDuration: time.Minute})
		f.must(err)
		if !found {
			t.Fatal("no job")
		}
		_, err = repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(j)})
		f.must(err)
		c.Action = "mailbox_changed"
		c.Sequence = 10
		c.State = "watching"
		c.Reason = "gmail_topic_invalidation"
		if !apply(c).Accepted {
			t.Fatal("hint not admitted")
		}
		box, _, err := repo.GetEmailMailbox(t.Context(), f.owner, f.box.ID)
		f.must(err)
		revision := box.SignalRevision
		if !apply(c).Duplicate {
			t.Fatal("replay not deduplicated")
		}
		jobs, err := repo.ListEmailJobs(t.Context(), EmailQuery{OwnerID: f.owner, MailboxID: f.box.ID})
		f.must(err)
		if jobs[0].NextAttemptAt.After(time.Now()) || jobs[0].SyncTrigger != "notification_hint" {
			t.Fatal("notification did not advance idle job")
		}
		c.Action = "register"
		c.PreviousEpoch = "controller-a"
		c.Epoch = "controller-b"
		c.Sequence = 0
		if !apply(c).Accepted {
			t.Fatal("restart not registered")
		}
		c.Action = "mailbox_changed"
		c.Epoch = "controller-a"
		c.Sequence = 999
		if apply(c).Accepted {
			t.Fatal("retired controller revived")
		}
		c.Epoch = "controller-b"
		c.Sequence = 1
		c.BindingGeneration++
		if apply(c).Accepted {
			t.Fatal("wrong binding accepted")
		}
		c.BindingGeneration--
		c.CredentialGeneration--
		if apply(c).Accepted {
			t.Fatal("old credential accepted")
		}
		box, _, err = repo.GetEmailMailbox(t.Context(), f.owner, f.box.ID)
		f.must(err)
		if box.SignalRevision != revision {
			t.Fatal("replays/stale notifications rearmed query")
		}
		c.CredentialGeneration++
		_, err = repo.PauseEmailMailbox(t.Context(), EmailPauseCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration})
		f.must(err)
		if apply(c).Accepted {
			t.Fatal("disabled mailbox accepted event")
		}
	})
}

func TestEmailObserverDoesNotStealRetryBackoffOrSettleNewSignal(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		c := EmailObserverCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, CredentialGeneration: 1, Action: "register", Epoch: "epoch", State: "watching"}
		_, err := repo.ApplyEmailObserver(t.Context(), c)
		f.must(err)
		_, err = repo.RequestEmailJob(t.Context(), pollRequest(f))
		f.must(err)
		j, _, err := repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobDiscover}, Now: time.Now().Add(time.Second), LeaseDuration: time.Minute})
		f.must(err)
		before, _, err := repo.GetEmailMailbox(t.Context(), f.owner, f.box.ID)
		f.must(err)
		c.EmailCommand = f.command()
		c.Action = "mailbox_changed"
		c.Sequence = 1
		c.Reason = "gmail_topic_invalidation"
		_, err = repo.ApplyEmailObserver(t.Context(), c)
		f.must(err)
		after, _, err := repo.GetEmailMailbox(t.Context(), f.owner, f.box.ID)
		f.must(err)
		if after.SignalRevision != before.SignalRevision+1 || after.ReconciledRevision != before.ReconciledRevision {
			t.Fatal("running query swallowed event")
		}
		finished, err := repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(j), ErrorCode: "browser_busy", RetryAt: time.Now().Add(time.Minute)})
		f.must(err)
		c.EmailCommand = f.command()
		c.Sequence = 2
		_, err = repo.ApplyEmailObserver(t.Context(), c)
		f.must(err)
		jobs, err := repo.ListEmailJobs(t.Context(), EmailQuery{OwnerID: f.owner, MailboxID: f.box.ID})
		f.must(err)
		if jobs[0].State != app.EmailJobRetryWait || !jobs[0].NextAttemptAt.Equal(finished.NextAttemptAt) {
			t.Fatal("notification stole retry backoff")
		}
	})
}
