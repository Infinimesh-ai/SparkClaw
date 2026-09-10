package emailmanagement

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type unknownCaptureRepository struct {
	*store.MemoryStore
	injected bool
}

func (r *unknownCaptureRepository) PublishEmailCapture(ctx context.Context, c store.EmailCaptureCommand) (app.EmailMail, error) {
	mail, err := r.MemoryStore.PublishEmailCapture(ctx, c)
	if err == nil && !r.injected {
		r.injected = true
		return app.EmailMail{}, &store.StoreError{Code: store.StoreErrorUnknownOutcome, Err: errors.New("injected response loss")}
	}
	return mail, err
}

func TestCaptureCommitUnknownReconcilesWithoutReopening(t *testing.T) {
	repo := &unknownCaptureRepository{MemoryStore: store.NewMemoryStore()}
	s, _, _ := newFixtureService(t, repo)
	box, err := s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	admission, err := repo.AdmitEmailDiscovery(t.Context(), store.EmailDiscoveryCommand{EmailCommand: command("email-owner", "discover"), MailboxID: box.ID, BindingGeneration: box.BindingGeneration, ObservedAt: time.Now(), Members: []store.EmailDiscoveryMember{{ProviderMessageID: "message-1", ProviderSelectionID: "selection-1", Direction: "inbound"}}})
	if err != nil {
		t.Fatal(err)
	}
	job, found, err := repo.ClaimEmailJob(t.Context(), store.EmailJobClaim{OwnerID: "email-owner", Kinds: []string{app.EmailJobCapture}, LeaseDuration: time.Minute})
	if err != nil || !found {
		t.Fatalf("claim: %v", err)
	}
	if err := s.capture(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	if !repo.injected {
		t.Fatal("response loss not exercised")
	}
	mail, _, err := repo.GetEmailMail(t.Context(), "email-owner", admission.Mails[0].ID)
	if err != nil || mail.CaptureID == "" {
		t.Fatal("capture missing after reconciliation")
	}
	// A replay after the source directory disappeared must use the command
	// receipt, rather than revisit the provider or re-create a capture.
	s.opts.WorkspaceRoot = "/missing-fixture-workspace"
	if err := s.capture(t.Context(), job); err != nil {
		t.Fatal(err)
	}
}

func TestOutageKeepsSourcesAndTerminatesRetries(t *testing.T) {
	repo := store.NewMemoryStore()
	s, _, model := newFixtureService(t, repo)
	box, err := s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.discover(t.Context(), app.EmailJob{OwnerID: "email-owner", MailboxID: box.ID, BindingGeneration: box.BindingGeneration}); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{app.EmailJobCapture, app.EmailJobParse} {
		if worked, err := s.workOne(t.Context(), []string{kind}); err != nil || !worked {
			t.Fatalf("%s: %v", kind, err)
		}
	}
	model.fail = true
	// Advance the test clock through a 30-minute outage. This verifies durable
	// retry timing and terminal projection; it is not a real-time soak result.
	clock := time.Now()
	s.now = func() time.Time { return clock }
	for i := 0; i < 30; i++ {
		clock = clock.Add(time.Minute)
		if err := s.plan(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err := s.workOne(t.Context(), []string{app.EmailJobClassification, app.EmailJobMessageSummary}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := repo.ListEmailMails(t.Context(), store.EmailQuery{OwnerID: "email-owner"})
	if err != nil || len(page.Items) != 1 {
		t.Fatal("outage lost mail")
	}
	mail := page.Items[0]
	if mail.CaptureID == "" || mail.RepresentationID == "" {
		t.Fatal("outage discarded source")
	}
	target, found, err := repo.GetEmailAnalysisTarget(t.Context(), "email-owner", app.EmailJobMessageSummary, mail.ID)
	if err != nil || !found || target.State != app.EmailSummaryFailed {
		t.Fatalf("outage not explicit: %+v %v", target, err)
	}
	model.fail = false
	if _, err := s.Reanalyze(t.Context(), "email-owner", mail.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 16; i++ {
		if err := s.plan(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err := s.workOne(t.Context(), []string{app.EmailJobClassification, app.EmailJobMessageSummary}); err != nil {
			t.Fatal(err)
		}
	}
	mail, _, err = repo.GetEmailMail(t.Context(), "email-owner", mail.ID)
	if err != nil || mail.Summary == nil || !mail.Summary.Current {
		t.Fatalf("retry after recovery: %+v %v", mail.Summary, err)
	}
}

type threadAdmissionFailure struct {
	*store.MemoryStore
	fail    bool
	unknown bool
}

func (r *threadAdmissionFailure) AdmitEmailDiscovery(ctx context.Context, c store.EmailDiscoveryCommand) (store.EmailDiscoveryAdmission, error) {
	if c.ThreadID != "" && r.fail {
		return store.EmailDiscoveryAdmission{}, errors.New("injected thread admission failure")
	}
	result, err := r.MemoryStore.AdmitEmailDiscovery(ctx, c)
	if err == nil && c.ThreadID != "" && r.unknown {
		return result, &store.StoreError{Code: store.StoreErrorUnknownOutcome, Err: errors.New("injected thread response loss")}
	}
	return result, err
}

func TestGroupedThreadsPersistBeforeDiscoveryBoundary(t *testing.T) {
	repo := &threadAdmissionFailure{MemoryStore: store.NewMemoryStore(), fail: true}
	s, _, _ := newFixtureService(t, repo)
	box, err := repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: command("email-owner", "bind"), Provider: app.EmailProviderGmail, Address: "owner@example.com", Enabled: true, Boundary: time.Now().Add(-30 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	end := time.Now().Add(-time.Second)
	job := app.EmailJob{OwnerID: "email-owner", MailboxID: box.ID, BindingGeneration: box.BindingGeneration}
	result := app.EmailDiscoveryResult{AccountAddress: box.Address, ObservedAt: time.Now(), Coverage: app.EmailDiscoveryCoverage{Lane: "recent_inbound", ScanComplete: true, BoundaryQualified: true},
		Threads: []app.EmailThreadTarget{{AccountAddress: box.Address, ProviderThreadID: "old-grouped-thread", ProviderSelectionID: "row", Folder: "inbox"}}}
	position := discoveryCursor{Start: box.Boundary, End: end}
	if err := s.admitDiscovery(t.Context(), job, box, result, position, "batch"); err == nil {
		t.Fatal("thread failure ignored")
	}
	after, _, err := repo.GetEmailMailbox(t.Context(), job.OwnerID, box.ID)
	if err != nil || !after.Boundary.Equal(box.Boundary) {
		t.Fatal("boundary advanced over an unregistered old thread")
	}
	repo.fail = false
	repo.unknown = true
	if err := s.admitDiscovery(t.Context(), job, box, result, position, "batch"); err != nil {
		t.Fatal(err)
	}
	threads, err := repo.ListEmailThreads(t.Context(), store.EmailQuery{OwnerID: job.OwnerID, MailboxID: box.ID})
	if err != nil || len(threads) != 1 {
		t.Fatal("lost committed thread receipt")
	}
	after, _, err = repo.GetEmailMailbox(t.Context(), job.OwnerID, box.ID)
	if err != nil || after.Boundary.Sub(end) > time.Microsecond || after.Boundary.Before(end.Add(-time.Microsecond)) {
		t.Fatal("reconciled thread did not allow boundary commit")
	}
}
