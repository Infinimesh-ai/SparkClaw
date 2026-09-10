package store

import (
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestEmailManagementMailboxBrowserLaneMixedKinds(t *testing.T) {
	for _, activeKind := range []string{app.EmailJobDiscover, app.EmailJobCapture, app.EmailJobMarkRead, app.EmailJobThreadSync} {
		t.Run(activeKind, func(t *testing.T) {
			emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
				f := emailFixture(t, repo)
				mail := f.admit("first", time.Now())
				f.admit("second", time.Now())
				for _, kind := range []string{app.EmailJobDiscover, app.EmailJobMarkRead, app.EmailJobThreadSync, app.EmailJobParse} {
					target := f.box.ID
					if kind == app.EmailJobMarkRead || kind == app.EmailJobParse {
						target = mail.ID
					}
					_, err := repo.RequestEmailJob(t.Context(), EmailJobRequest{EmailCommand: f.command(), Kind: kind, TargetID: target, MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration})
					f.must(err)
				}
				active := f.claim(activeKind)
				other, err := repo.BindEmailMailbox(t.Context(), EmailBindCommand{EmailCommand: f.command(), Provider: app.EmailProviderOutlook, Address: "other@example.com", Enabled: true})
				f.must(err)
				_, err = repo.RequestEmailJob(t.Context(), EmailJobRequest{EmailCommand: f.command(), Kind: app.EmailJobDiscover, TargetID: other.ID, MailboxID: other.ID, BindingGeneration: other.BindingGeneration})
				f.must(err)
				otherJob := f.claim(app.EmailJobDiscover)
				if otherJob.MailboxID != other.ID {
					t.Fatal("another browser job overlapped the occupied mailbox")
				}
				// Each kind-specific request must see leases of all other browser kinds,
				// even though those leases have future due times and survive a reopen.
				f.repo = restartEmailRepeatRepository(t, repo)
				for _, kind := range []string{app.EmailJobDiscover, app.EmailJobCapture, app.EmailJobMarkRead, app.EmailJobThreadSync} {
					job, found, err := f.repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{kind}})
					f.must(err)
					if found {
						t.Fatalf("occupied mailbox yielded %s job %s", kind, job.ID)
					}
				}
				local := f.claim(app.EmailJobParse)
				if local.MailboxID != f.box.ID {
					t.Fatal("local job lost mailbox identity")
				}
				f.finish(active)
				resumed := f.claim(app.EmailJobCapture)
				if resumed.MailboxID != f.box.ID {
					t.Fatal("released mailbox did not resume")
				}
			})
		})
	}
}

func TestEmailManagementMailboxBrowserLaneExpiredLease(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		_, err := repo.RequestEmailJob(t.Context(), EmailJobRequest{EmailCommand: f.command(), Kind: app.EmailJobDiscover, TargetID: f.box.ID, MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration})
		f.must(err)
		old := f.claim(app.EmailJobDiscover)
		f.repo = restartEmailRepeatRepository(t, repo)
		recovered, found, err := f.repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobDiscover}, Now: old.LeaseExpiresAt})
		f.must(err)
		if !found || recovered.ID != old.ID || recovered.LeaseToken == old.LeaseToken || recovered.Attempt != 2 {
			t.Fatalf("expired lease not recovered: %+v", recovered)
		}
		_, err = f.repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(old)})
		if StoreErrorCodeOf(err) != StoreErrorConflict {
			t.Fatalf("old lease committed: %v", err)
		}
		// A different kind is blocked by the recovered lease, not the old expiry.
		_, err = f.repo.RequestEmailJob(t.Context(), EmailJobRequest{EmailCommand: f.command(), Kind: app.EmailJobThreadSync, TargetID: f.box.ID, MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration})
		f.must(err)
		_, found, err = f.repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobThreadSync}, Now: old.LeaseExpiresAt.Add(time.Second)})
		f.must(err)
		if found {
			t.Fatal("recovered lease did not exclude another browser kind")
		}
	})
}

func TestEmailManagementMailboxBrowserLaneConcurrentClaims(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		boxes := []app.EmailMailbox{f.box}
		for _, provider := range []string{app.EmailProviderOutlook, app.EmailProviderQQMail} {
			box, err := repo.BindEmailMailbox(t.Context(), EmailBindCommand{EmailCommand: f.command(), Provider: provider, Address: "other@example.com", Enabled: true})
			f.must(err)
			boxes = append(boxes, box)
		}
		for _, box := range boxes {
			for _, kind := range []string{app.EmailJobDiscover, app.EmailJobThreadSync} {
				_, err := repo.RequestEmailJob(t.Context(), EmailJobRequest{EmailCommand: f.command(), Kind: kind, TargetID: box.ID, MailboxID: box.ID, BindingGeneration: box.BindingGeneration})
				f.must(err)
			}
		}
		// Independent PostgreSQL clients exercise the durable transaction lock;
		// Memory/File clients share the same synchronized Store instance.
		second := repo
		if _, ok := repo.(*PostgresStore); ok {
			second = restartEmailRepeatRepository(t, repo)
		}
		type result struct {
			job   app.EmailJob
			found bool
			err   error
		}
		results := make(chan result, 12)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < 12; i++ {
			client := repo
			if i%2 == 1 {
				client = second
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				job, found, err := client.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobDiscover, app.EmailJobThreadSync}})
				results <- result{job, found, err}
			}()
		}
		close(start)
		wg.Wait()
		close(results)
		seen := map[string]bool{}
		for result := range results {
			f.must(result.err)
			if result.found {
				if seen[result.job.MailboxID] {
					t.Fatal("concurrent claims overlapped one mailbox")
				}
				seen[result.job.MailboxID] = true
			}
		}
		if len(seen) != 3 {
			t.Fatalf("claimed %d mailbox lanes, want 3", len(seen))
		}
	})
}

func TestEmailManagementMailboxBrowserLaneBindingFence(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		_, err := repo.RequestEmailJob(t.Context(), EmailJobRequest{EmailCommand: f.command(), Kind: app.EmailJobDiscover, TargetID: f.box.ID, MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration})
		f.must(err)
		old := f.claim(app.EmailJobDiscover)
		rebound, err := repo.BindEmailMailbox(t.Context(), EmailBindCommand{EmailCommand: f.command(), Provider: f.box.Provider, Address: f.box.Address, Enabled: true, ExpectedVersion: f.box.Version})
		f.must(err)
		_, err = repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(old)})
		if StoreErrorCodeOf(err) != StoreErrorConflict {
			t.Fatalf("old binding lease committed: %v", err)
		}
		_, err = repo.RequestEmailJob(t.Context(), EmailJobRequest{EmailCommand: f.command(), Kind: app.EmailJobThreadSync, TargetID: f.box.ID, MailboxID: f.box.ID, BindingGeneration: rebound.BindingGeneration})
		f.must(err)
		_, found, err := repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobThreadSync}})
		f.must(err)
		if found {
			t.Fatal("binding replacement overlapped a still-live browser lease")
		}
		next, found, err := repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobThreadSync}, Now: old.LeaseExpiresAt})
		f.must(err)
		if !found || next.BindingGeneration != rebound.BindingGeneration {
			t.Fatal("new binding did not claim after prior lease expired")
		}
	})
}
