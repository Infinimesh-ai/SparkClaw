package emailmanagement

import (
	"context"
	"errors"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// Narrowing to Browser deliberately omits CollectPageForOwner. Unsupported
// providers must fail closed instead of reintroducing old discovery/capture.
type bindingOnlyBrowser struct{ Browser }

func TestRetiredIntakeExecutorsAndFallbackCannotRun(t *testing.T) {
	s, browser, _ := newFixtureService(t, store.NewMemoryStore())
	for _, kind := range []string{app.EmailJobCapture, app.EmailJobMarkRead, app.EmailJobThreadSync, app.EmailJobSourceRecovery} {
		if err := s.dispatch(t.Context(), app.EmailJob{Kind: kind}); err == nil || err.Error() != "email_job_kind_invalid" {
			t.Fatalf("retired executor %s reachable: %v", kind, err)
		}
	}
	s.browser = bindingOnlyBrowser{Browser: browser}
	if err := s.discover(t.Context(), app.EmailJob{Kind: app.EmailJobDiscover}); !errors.Is(err, ErrIncrementalUnqualified) {
		t.Fatalf("missing incremental reader silently fell back: %v", err)
	}
	if browser.captureCalls() != 0 {
		t.Fatal("rejected executor touched originals")
	}
}

type unknownTimelineRepository struct {
	*store.MemoryStore
	injected bool
}

func (r *unknownTimelineRepository) CommitEmailSync(ctx context.Context, c store.EmailSyncCommitCommand) (store.EmailSyncCommitResult, error) {
	result, err := r.MemoryStore.CommitEmailSync(ctx, c)
	if err == nil && !r.injected {
		r.injected = true
		return store.EmailSyncCommitResult{}, &store.StoreError{Code: store.StoreErrorUnknownOutcome, Err: errors.New("injected timeline commit response loss")}
	}
	return result, err
}

func TestTimelineCommitUnknownReconcilesWithoutRepeatingCollection(t *testing.T) {
	repo := &unknownTimelineRepository{MemoryStore: store.NewMemoryStore()}
	s, browser, _ := newFixtureService(t, repo)
	box, err := s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.plan(t.Context()); err != nil {
		t.Fatal(err)
	}
	if worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover}); !worked || err != nil {
		t.Fatalf("collection: %v", err)
	}
	if !repo.injected || browser.captureCalls() != 1 {
		t.Fatal("lost-ack fixture did not make exactly one original request")
	}
	mails, err := repo.ListEmailMails(t.Context(), store.EmailQuery{OwnerID: "email-owner", MailboxID: box.ID})
	if err != nil || len(mails.Items) != 1 || mails.Items[0].CaptureID == "" {
		t.Fatalf("committed original lost: %+v %v", mails, err)
	}
	jobs, err := repo.ListEmailJobs(t.Context(), store.EmailQuery{OwnerID: "email-owner", MailboxID: box.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		if job.Kind == app.EmailJobDiscover && (job.ErrorCode != "" || job.Attempt != 0 || job.State != app.EmailJobQueued) {
			t.Fatalf("known commit was incorrectly retried: %+v", job)
		}
	}
	if worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover}); worked || err != nil {
		t.Fatalf("response loss immediately redownloaded: %v", err)
	}
}
