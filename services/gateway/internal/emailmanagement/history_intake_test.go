package emailmanagement

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type historyBrowser struct {
	*intakeFixture
	calls      []string
	failSecond bool
	pause      func()
}

func (b *historyBrowser) EnumerateThreadForOwner(_ context.Context, _ string, r app.EmailThreadRequest) (app.EmailThreadResult, error) {
	b.calls = append(b.calls, r.Continuation)
	if r.Continuation == "second" && b.failSecond {
		return app.EmailThreadResult{}, errors.New("history unavailable")
	}
	member := app.EmailThreadMember{Target: app.EmailCaptureTarget{AccountAddress: r.Thread.AccountAddress, ProviderThreadID: r.Thread.ProviderThreadID, ProviderSelectionID: r.Thread.ProviderSelectionID, ProviderMessageID: "read-parent", Folder: "inbox"}, Direction: "inbound", ReadState: "read"}
	coverage := app.EmailDiscoveryCoverage{Scope: "thread", Continuation: "second", Reason: "batch_limit"}
	if r.Continuation == "second" {
		member.Target.ProviderMessageID = "sent-parent"
		member.Target.Folder = "sent"
		member.Direction = "outbound"
		coverage = app.EmailDiscoveryCoverage{Scope: "thread", ScanComplete: true}
	}
	if b.pause != nil {
		b.pause()
		b.pause = nil
	}
	return app.EmailThreadResult{Thread: r.Thread, Members: []app.EmailThreadMember{member}, Coverage: coverage, ObservedAt: time.Now().UTC()}, nil
}
func historyThread(t *testing.T, s *Service, count int) (app.EmailMailbox, app.EmailJob) {
	t.Helper()
	box, err := s.repository.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: command("email-owner", "history-bind"), Provider: app.EmailProviderGmail, Address: "owner@example.com", Enabled: true, Boundary: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < count; i++ {
		_, err = s.repository.AdmitEmailDiscovery(t.Context(), store.EmailDiscoveryCommand{EmailCommand: command("email-owner", fmt.Sprintf("history-thread-%d", i)), MailboxID: box.ID, BindingGeneration: box.BindingGeneration, ThreadID: fmt.Sprintf("thread-%d", i), ProviderSelectionID: fmt.Sprintf("selection-%d", i), Folder: "inbox", Coverage: "pending", Trigger: "thread_discovery", ObservedAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
	}
	threads, err := s.repository.ListEmailThreads(t.Context(), store.EmailQuery{OwnerID: "email-owner", MailboxID: box.ID})
	if err != nil {
		t.Fatal(err)
	}
	return box, app.EmailJob{OwnerID: "email-owner", MailboxID: box.ID, BindingGeneration: box.BindingGeneration, TargetID: threads[0].ID}
}
func TestHistoryPaginationPersistsReadAndSentMembersAcrossRestart(t *testing.T) {
	file := filepath.Join(t.TempDir(), "state.json")
	repo, err := store.NewFileStore(file)
	if err != nil {
		t.Fatal(err)
	}
	s, base, _ := newFixtureService(t, repo)
	b := &historyBrowser{intakeFixture: base, failSecond: true}
	s.browser = b
	_, job := historyThread(t, s, 1)
	if err = s.syncThread(t.Context(), job); err == nil {
		t.Fatal("expected second-page failure")
	}
	thread, _, _ := repo.GetEmailThread(t.Context(), job.OwnerID, job.TargetID)
	if thread.Cursor != "second" || thread.Coverage != "partial" {
		t.Fatalf("checkpoint lost: %+v", thread)
	}
	reopened, err := store.NewFileStore(file)
	if err != nil {
		t.Fatal(err)
	}
	s.repository = reopened
	b.failSecond = false
	b.calls = nil
	if err = s.syncThread(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	if len(b.calls) != 1 || b.calls[0] != "second" {
		t.Fatalf("did not resume: %v", b.calls)
	}
	if err = s.syncThread(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	page, err := reopened.ListEmailMails(t.Context(), store.EmailQuery{OwnerID: job.OwnerID})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("dedup failed: %+v %v", page, err)
	}
	directions := map[string]bool{}
	for _, mail := range page.Items {
		directions[mail.Direction] = true
		if mail.RemoteReadState != "read" {
			t.Fatal("read history excluded")
		}
	}
	if !directions["inbound"] || !directions["sent"] {
		t.Fatalf("missing Sent: %v", directions)
	}
}
func TestHistorySchedulerIncludesThreadsBeyondFirstPage(t *testing.T) {
	repo := store.NewMemoryStore()
	s, _, _ := newFixtureService(t, repo)
	box, job := historyThread(t, s, 105)
	if err := s.scheduleThreads(t.Context(), job, box); err != nil {
		t.Fatal(err)
	}
	count := 0
	for {
		j, ok, err := repo.ClaimEmailJob(t.Context(), store.EmailJobClaim{OwnerID: job.OwnerID, Kinds: []string{app.EmailJobThreadSync}, Now: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		count++
		if _, err = repo.FinishEmailJob(t.Context(), store.EmailJobFinish{EmailJobLease: lease(j, time.Now())}); err != nil {
			t.Fatal(err)
		}
	}
	if count != 105 {
		t.Fatalf("scheduled %d of 105", count)
	}
}
func TestHistoryPausedBindingStopsPageAdmission(t *testing.T) {
	repo := store.NewMemoryStore()
	s, base, _ := newFixtureService(t, repo)
	box, job := historyThread(t, s, 1)
	b := &historyBrowser{intakeFixture: base}
	s.browser = b
	b.pause = func() {
		_, err := repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: command(job.OwnerID, "history-pause"), Provider: box.Provider, Address: box.Address, Enabled: false, ExpectedVersion: box.Version, Boundary: box.Boundary})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := s.syncThread(t.Context(), job); err == nil {
		t.Fatal("paused admission accepted")
	}
	page, _ := repo.ListEmailMails(t.Context(), store.EmailQuery{OwnerID: job.OwnerID})
	if len(page.Items) != 0 {
		t.Fatal("paused browser result ingested")
	}
}

func TestHistoryProjectionKeepsMissingReferencesVisibleWithoutBrowserWork(t *testing.T) {
	repo := store.NewMemoryStore()
	s, _, _ := newFixtureService(t, repo)
	box, _ := historyThread(t, s, 1)
	mail := app.EmailMail{MailboxID: box.ID, MessageID: "<current@test>", ReplyReferences: []string{"<missing@test>"}}
	state, reason, err := s.mailHistory(t.Context(), "email-owner", mail)
	if err != nil || state != "partial" || reason != "reply_source_unavailable" {
		t.Fatalf("missing reference hidden: %s %s %v", state, reason, err)
	}
	_, err = repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: command("email-owner", "history-projection-pause"), Provider: box.Provider, Address: box.Address, Enabled: false, ExpectedVersion: box.Version, Boundary: box.Boundary})
	if err != nil {
		t.Fatal(err)
	}
	state, reason, err = s.mailHistory(t.Context(), "email-owner", mail)
	if err != nil || state != "paused" || reason != "receiving_disabled" {
		t.Fatalf("pause hidden: %s %s %v", state, reason, err)
	}
}

func TestHistoryPageCollectorSchedulesKnownThreadsWithoutPerMailDuplicateJobs(t *testing.T) {
	repo := store.NewMemoryStore()
	s, base, _ := newFixtureService(t, repo)
	box, _ := historyThread(t, s, 1)
	s.browser = &emptyPageFixture{intakeFixture: base}
	if err := s.plan(t.Context()); err != nil {
		t.Fatal(err)
	}
	if worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover}); err != nil || !worked {
		t.Fatalf("discovery: %v %v", worked, err)
	}
	jobs, err := repo.ListEmailJobs(t.Context(), store.EmailQuery{OwnerID: "email-owner", MailboxID: box.ID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	scheduled := false
	for _, job := range jobs {
		if job.Kind == app.EmailJobThreadSync && job.State != app.EmailJobSucceeded {
			scheduled = true
		}
		if job.Kind == app.EmailJobCapture {
			t.Fatal("empty page scheduled per-mail capture")
		}
	}
	if !scheduled {
		t.Fatal("page collection did not schedule encountered history")
	}
}

func TestHistoryProjectionReportsTerminalThreadFailure(t *testing.T) {
	repo := store.NewMemoryStore()
	s, _, _ := newFixtureService(t, repo)
	box, seed := historyThread(t, s, 1)
	_, err := repo.RequestEmailJob(t.Context(), store.EmailJobRequest{EmailCommand: command(seed.OwnerID, "failed-history-request"), Kind: app.EmailJobThreadSync, TargetID: seed.TargetID, MailboxID: box.ID, BindingGeneration: box.BindingGeneration})
	if err != nil {
		t.Fatal(err)
	}
	job, ok, err := repo.ClaimEmailJob(t.Context(), store.EmailJobClaim{OwnerID: seed.OwnerID, Kinds: []string{app.EmailJobThreadSync}})
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	_, err = repo.FinishEmailJob(t.Context(), store.EmailJobFinish{EmailJobLease: lease(job, time.Now()), ErrorCode: "email_pinned_message_unavailable"})
	if err != nil {
		t.Fatal(err)
	}
	state, reason, err := s.mailHistory(t.Context(), seed.OwnerID, app.EmailMail{MailboxID: box.ID, ProviderThreadID: "thread-0"})
	if err != nil || state != "failed" || reason != "email_pinned_message_unavailable" {
		t.Fatalf("failed history hidden: %s %s %v", state, reason, err)
	}
}

func TestHistoryExplicitSyncRetriesExhaustedThreadAndCaptureOnlyOnRequest(t *testing.T) {
	repo := store.NewMemoryStore()
	s, _, _ := newFixtureService(t, repo)
	box, seed := historyThread(t, s, 1)
	admitted, err := repo.AdmitEmailDiscovery(t.Context(), store.EmailDiscoveryCommand{EmailCommand: command(seed.OwnerID, "retry-history-member"), MailboxID: box.ID, BindingGeneration: box.BindingGeneration, ObservedAt: time.Now(), Coverage: "partial", Members: []store.EmailDiscoveryMember{{ProviderMessageID: "missing-source", ProviderSelectionID: "selection-0", ProviderThreadID: "thread-0", Folder: "inbox", Direction: "inbound", Reason: app.EmailJobThreadSync}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.scheduleThreads(t.Context(), seed, box); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{app.EmailJobThreadSync, app.EmailJobCapture} {
		job, ok, err := repo.ClaimEmailJob(t.Context(), store.EmailJobClaim{OwnerID: seed.OwnerID, Kinds: []string{kind}})
		if err != nil || !ok {
			t.Fatalf("claim %s: %v %v", kind, ok, err)
		}
		_, err = repo.FinishEmailJob(t.Context(), store.EmailJobFinish{EmailJobLease: lease(job, time.Now()), ErrorCode: "email_pinned_message_unavailable"})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = s.scheduleThreads(t.Context(), seed, box); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := repo.ClaimEmailJob(t.Context(), store.EmailJobClaim{OwnerID: seed.OwnerID, Kinds: []string{app.EmailJobThreadSync, app.EmailJobCapture}}); err != nil || ok {
		t.Fatalf("periodic scan revived exhausted work: %v %v", ok, err)
	}
	if _, err = s.Sync(t.Context(), seed.OwnerID, box.ID); err != nil {
		t.Fatal(err)
	}
	retry, ok, err := repo.ClaimEmailJob(t.Context(), store.EmailJobClaim{OwnerID: seed.OwnerID, Kinds: []string{app.EmailJobThreadSync}})
	if err != nil || !ok {
		t.Fatalf("thread retry unavailable: %v %v", ok, err)
	}
	_, err = repo.FinishEmailJob(t.Context(), store.EmailJobFinish{EmailJobLease: lease(retry, time.Now())})
	if err != nil {
		t.Fatal(err)
	}
	state, _, err := s.mailHistory(t.Context(), seed.OwnerID, app.EmailMail{MailboxID: box.ID, ProviderThreadID: "thread-0"})
	if err != nil || state != "failed" {
		t.Fatalf("source failure falsely recovered on thread retry: %s %v", state, err)
	}
	capture, ok, err := repo.ClaimEmailJob(t.Context(), store.EmailJobClaim{OwnerID: seed.OwnerID, Kinds: []string{app.EmailJobCapture}})
	if err != nil || !ok || capture.TargetID != admitted.Mails[0].ID || capture.Attempt != 1 {
		t.Fatalf("capture not explicitly rearmed: %+v %v %v", capture, ok, err)
	}
}
