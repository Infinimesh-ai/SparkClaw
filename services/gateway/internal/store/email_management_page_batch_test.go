package store

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func emailPageBatchLease(t *testing.T, f *emailContractFixture) app.EmailJob {
	t.Helper()
	_, err := f.repo.RequestEmailJob(t.Context(), EmailJobRequest{EmailCommand: f.command(), Kind: app.EmailJobDiscover, TargetID: f.box.ID, MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, Rearm: true})
	f.must(err)
	return f.claim(app.EmailJobDiscover)
}
func emailPageBatchCapture(f *emailContractFixture, job app.EmailJob, m app.EmailMail, id, state, read string) EmailCaptureCommand {
	return EmailCaptureCommand{EmailCommand: f.command(), PageBatch: true, ReadState: read, MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, Lease: f.lease(job), Capture: app.EmailCaptureVersion{ID: id, MailID: m.ID, ManifestPath: "source/manifest.json", ManifestSHA256: strings.Repeat("a", 64), OriginalPath: "source/original.eml", OriginalSHA256: strings.Repeat("b", 64), State: state}}
}
func TestEmailManagementPageBatchDurabilityAndReadConfirmation(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		job := emailPageBatchLease(t, f)
		discovery := EmailDiscoveryCommand{EmailCommand: f.command(), PageBatch: true, Lease: f.lease(job), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, Trigger: "recent_inbound", Cursor: "next-page", Coverage: "partial", ObservedAt: time.Now(), Members: []EmailDiscoveryMember{
			{ProviderMessageID: "one", ProviderSelectionID: "one", Direction: "inbound", RemoteReadState: "unread"},
			{ProviderMessageID: "two", ProviderSelectionID: "two", Direction: "inbound", RemoteReadState: "unread"},
			{ProviderMessageID: "failed-download", ProviderSelectionID: "failed-download", Direction: "inbound", RemoteReadState: "unread"},
		}}
		admitted, err := repo.AdmitEmailDiscovery(t.Context(), discovery)
		f.must(err)
		complete, err := repo.PublishEmailCapture(t.Context(), emailPageBatchCapture(f, job, admitted.Mails[0], "complete", app.EmailCaptureComplete, "unknown"))
		f.must(err)
		partial, err := repo.PublishEmailCapture(t.Context(), emailPageBatchCapture(f, job, admitted.Mails[1], "partial", app.EmailCapturePartial, "unknown"))
		f.must(err)
		jobs, err := repo.ListEmailJobs(t.Context(), EmailQuery{OwnerID: f.owner})
		f.must(err)
		parses := 0
		for _, j := range jobs {
			if j.Kind == app.EmailJobCapture || j.Kind == app.EmailJobMarkRead {
				t.Fatalf("batch scheduled per-mail browser job: %s", j.Kind)
			}
			if j.Kind == app.EmailJobParse {
				parses++
			}
		}
		if parses != 2 {
			t.Fatalf("batch sources scheduled %d parse jobs", parses)
		}
		// Reopen after durable receipt publication but before page ack. The next
		// page replay can safely promote the read state without republishing source.
		f.repo = restartEmailRepeatRepository(t, repo)
		partialReplay := emailPageBatchCapture(f, job, partial, "partial", app.EmailCapturePartial, "unknown")
		replayedPartial, err := f.repo.PublishEmailCapture(t.Context(), partialReplay)
		f.must(err)
		if replayedPartial.InputVersion != partial.InputVersion {
			t.Fatal("partial page replay republished the source")
		}
		partialReplay.EmailCommand = f.command()
		partialReplay.Capture.OriginalSHA256 = strings.Repeat("e", 64)
		if _, err := f.repo.PublishEmailCapture(t.Context(), partialReplay); StoreErrorCodeOf(err) != StoreErrorConflict {
			t.Fatalf("partial source ID reused with different content: %v", err)
		}
		replay := emailPageBatchCapture(f, job, complete, "unused-replay", app.EmailCaptureComplete, "read")
		repeated, err := f.repo.PublishEmailCapture(t.Context(), replay)
		f.must(err)
		if repeated.CaptureID != complete.CaptureID || repeated.InputVersion != complete.InputVersion || repeated.RemoteReadState != "read" {
			t.Fatal("page replay replaced source or lost read confirmation")
		}
		replay.EmailCommand = f.command()
		replay.ReadState = "unknown"
		repeated, err = f.repo.PublishEmailCapture(t.Context(), replay)
		f.must(err)
		if repeated.RemoteReadState != "read" {
			t.Fatal("uncertain replay downgraded confirmed read")
		}
		ack := EmailDiscoveryCommand{EmailCommand: f.command(), PageBatch: true, Lease: f.lease(job), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, Trigger: "recent_inbound", AcknowledgedPageID: "page_" + strings.Repeat("d", 64)}
		_, err = f.repo.AdmitEmailDiscovery(t.Context(), ack)
		f.must(err)
		f.repo = restartEmailRepeatRepository(t, f.repo)
		box, found, err := f.repo.GetEmailMailbox(t.Context(), f.owner, f.box.ID)
		f.must(err)
		if !found || box.PageAcks["recent_inbound"] != ack.AcknowledgedPageID || box.Cursor != discovery.Cursor || box.Coverage != discovery.Coverage || !box.LastCheckedAt.Equal(postgresTime(discovery.ObservedAt)) {
			t.Fatalf("ack lost durable page or changed discovery: %+v", box)
		}
		f.finish(job)
		next := emailPageBatchLease(t, f)
		upgraded, err := f.repo.PublishEmailCapture(t.Context(), emailPageBatchCapture(f, next, partial, "recovered", app.EmailCaptureComplete, "read"))
		f.must(err)
		if upgraded.CaptureID != "recovered" || upgraded.InputVersion != partial.InputVersion+1 || upgraded.RemoteReadState != "read" {
			t.Fatal("next page could not retry partial source")
		}
		jobs, err = f.repo.ListEmailJobs(t.Context(), EmailQuery{OwnerID: f.owner})
		f.must(err)
		for _, j := range jobs {
			if j.Kind == app.EmailJobCapture || j.Kind == app.EmailJobMarkRead {
				t.Fatal("page retry created single-mail browser fallback")
			}
		}
	})
}

func TestEmailManagementPageBatchLeaseAndInputFences(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		job := emailPageBatchLease(t, f)
		admission := EmailDiscoveryCommand{EmailCommand: f.command(), PageBatch: true, Lease: f.lease(job), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, Trigger: "recent_inbound", ObservedAt: time.Now(), Members: []EmailDiscoveryMember{{ProviderMessageID: "one", ProviderSelectionID: "one", Direction: "inbound"}}}
		admitted, err := repo.AdmitEmailDiscovery(t.Context(), admission)
		f.must(err)
		original := emailPageBatchCapture(f, job, admitted.Mails[0], "source", app.EmailCaptureComplete, "read")
		for _, scenario := range []string{"expired", "token", "generation", "owner", "mailbox", "missing_mail", "non_batch", "bad_read", "partial_read"} {
			t.Run(scenario, func(t *testing.T) {
				command := original
				command.EmailCommand = f.command()
				switch scenario {
				case "expired":
					command.Lease.Now = job.LeaseExpiresAt
				case "token":
					command.Lease.LeaseToken = "wrong"
				case "generation":
					command.BindingGeneration++
				case "owner":
					command.OwnerID = "someone-else"
				case "mailbox":
					command.MailboxID = "wrong"
				case "missing_mail":
					command.Capture.MailID = "missing"
				case "non_batch":
					command.PageBatch = false
					command.ReadState = ""
				case "bad_read":
					command.ReadState = "unread"
				case "partial_read":
					command.Capture.State = app.EmailCapturePartial
				}
				if _, err := repo.PublishEmailCapture(t.Context(), command); err == nil {
					t.Fatalf("%s receipt accepted", scenario)
				}
			})
		}
		ack := EmailDiscoveryCommand{EmailCommand: f.command(), PageBatch: true, Lease: f.lease(job), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, Trigger: "recent_inbound", AcknowledgedPageID: "page_" + strings.Repeat("d", 64)}
		for _, scenario := range []string{"expired", "token", "trigger", "page_id", "members", "not_batch", "missing_lease"} {
			t.Run("ack_"+scenario, func(t *testing.T) {
				command := ack
				command.EmailCommand = f.command()
				switch scenario {
				case "expired":
					command.Lease.Now = job.LeaseExpiresAt
				case "token":
					command.Lease.LeaseToken = "wrong"
				case "trigger":
					command.Trigger = "unbounded-lane"
				case "page_id":
					command.AcknowledgedPageID = "page_bad"
				case "members":
					command.Members = admission.Members
				case "not_batch":
					command.PageBatch = false
				case "missing_lease":
					command.Lease = EmailJobLease{}
				}
				if _, err := repo.AdmitEmailDiscovery(t.Context(), command); err == nil {
					t.Fatalf("%s ack accepted", scenario)
				}
			})
		}
		_, err = repo.PublishEmailCapture(t.Context(), original)
		f.must(err)
	})
}

func TestEmailManagementPageBatchSupersedesLegacyBacklog(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		var mails []app.EmailMail
		for page := 0; page < 3; page++ {
			members := []EmailDiscoveryMember{}
			for i := 0; i < 50; i++ {
				id := fmt.Sprintf("legacy-%d-%d", page, i)
				members = append(members, EmailDiscoveryMember{ProviderMessageID: id, ProviderSelectionID: id, Direction: "inbound", RemoteReadState: "unread"})
			}
			admission, err := repo.AdmitEmailDiscovery(t.Context(), EmailDiscoveryCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, ObservedAt: time.Now(), Members: members})
			f.must(err)
			mails = append(mails, admission.Mails...)
		}
		retry := f.claim(app.EmailJobCapture)
		_, err := repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(retry), ErrorCode: "temporary", RetryAt: time.Now().Add(time.Hour)})
		f.must(err)
		for _, kind := range []string{app.EmailJobMarkRead, app.EmailJobThreadSync, app.EmailJobParse} {
			target := mails[0].ID
			if kind == app.EmailJobThreadSync {
				target = f.box.ID
			}
			_, err = repo.RequestEmailJob(t.Context(), EmailJobRequest{EmailCommand: f.command(), Kind: kind, TargetID: target, MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration})
			f.must(err)
		}
		// Old generation tasks and queues longer than one Store window must all
		// leave the actionable backlog when this mailbox changes to page mode.
		f.box, err = repo.BindEmailMailbox(t.Context(), EmailBindCommand{EmailCommand: f.command(), Provider: f.box.Provider, Address: f.box.Address, ExpectedVersion: f.box.Version, Enabled: true})
		f.must(err)
		other, err := repo.BindEmailMailbox(t.Context(), EmailBindCommand{EmailCommand: f.command(), Provider: app.EmailProviderOutlook, Address: "other@example.com", Enabled: true})
		f.must(err)
		_, err = repo.RequestEmailJob(t.Context(), EmailJobRequest{EmailCommand: f.command(), Kind: app.EmailJobThreadSync, TargetID: other.ID, MailboxID: other.ID, BindingGeneration: other.BindingGeneration})
		f.must(err)
		job := emailPageBatchLease(t, f)
		_, err = repo.AdmitEmailDiscovery(t.Context(), EmailDiscoveryCommand{EmailCommand: f.command(), PageBatch: true, Lease: f.lease(job), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, ObservedAt: time.Now(), Trigger: "recent_inbound", MaxPendingJobs: 4})
		f.must(err)
		f.repo = restartEmailRepeatRepository(t, repo)
		status, err := f.repo.GetEmailOwnerStatus(t.Context(), f.owner)
		f.must(err)
		if status.BacklogCount != 3 {
			t.Fatalf("superseded legacy jobs remain actionable: %+v", status)
		}
		// Superseding jobs never pretends the mail was captured or marked read.
		mail, found, err := f.repo.GetEmailMail(t.Context(), f.owner, mails[0].ID)
		f.must(err)
		if !found || mail.CaptureID != "" || mail.RemoteReadState != "unread" {
			t.Fatal("legacy supersession fabricated source or remote read")
		}
		f.finish(job)
		otherJob := f.claim(app.EmailJobThreadSync)
		if otherJob.MailboxID != other.ID {
			t.Fatal("legacy mailbox task was automatically revived")
		}
		f.claim(app.EmailJobParse)
		_, found, err = f.repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobCapture, app.EmailJobMarkRead}})
		f.must(err)
		if found {
			t.Fatal("superseded per-mail job was automatically revived")
		}
		// Manual rearm still works beyond >100 superseded rows; their old due
		// times must not hide actionable work in the bounded claim window.
		requested, err := f.repo.RequestEmailJob(t.Context(), EmailJobRequest{EmailCommand: f.command(), Kind: app.EmailJobCapture, TargetID: mails[149].ID, MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, Rearm: true})
		f.must(err)
		manual := f.claim(app.EmailJobCapture)
		if manual.ID != requested.ID {
			t.Fatal("superseded queue starved explicit capture")
		}
	})
}
