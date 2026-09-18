package emailmanagement

import (
	"fmt"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

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

func TestIncrementalCollectorDoesNotScheduleKnownHistory(t *testing.T) {
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
	if scheduled {
		t.Fatal("incremental collection scheduled legacy history")
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
	state, reason, err := s.mailHistory(t.Context(), seed.OwnerID, app.EmailMail{MailboxID: box.ID, ProviderThreadID: "thread-0", ReplyMailID: "proved-reply"})
	if err != nil || state != "failed" || reason != "email_pinned_message_unavailable" {
		t.Fatalf("failed history hidden: %s %s %v", state, reason, err)
	}
}

func TestHistoryProjectionDoesNotInferMissingMessagesFromStandaloneProviderThread(t *testing.T) {
	repo := store.NewMemoryStore()
	s, _, _ := newFixtureService(t, repo)
	box, _ := historyThread(t, s, 1)
	state, reason, err := s.mailHistory(t.Context(), "email-owner", app.EmailMail{ID: "standalone", MailboxID: box.ID, ProviderThreadID: "thread-0"})
	if err != nil || state != "complete" || reason != "" {
		t.Fatalf("standalone provider thread fabricated history gap: %s %s %v", state, reason, err)
	}
}
