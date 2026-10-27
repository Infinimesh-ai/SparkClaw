package emailmanagement

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type qqRetryPageFixture struct {
	*qqMarkReadPageFixture
	calls int
}

func (f *qqRetryPageFixture) CollectPageForOwner(ctx context.Context, owner string, request app.EmailReadRequest) (app.EmailPageResult, error) {
	page, err := f.qqMarkReadPageFixture.CollectPageForOwner(ctx, owner, request)
	if err != nil {
		return page, err
	}
	f.calls++
	// QQ lists second-precision receipt times, including when a failed
	// original appears again beside new mail in the following poll.
	received := page.Discovery.Candidates[0].ReceivedAt.Truncate(time.Second)
	page.Discovery.Candidates[0].ReceivedAt = &received
	page.Captures[0].Target.ReceivedAt = &received
	if f.calls == 1 {
		page.Status = "partial"
		page.Failures = []app.EmailPageFailure{{Target: page.Captures[0].Target,
			ErrorCode: "email_provider_unavailable", Scope: app.EmailSyncFailureProviderOperational}}
		page.Captures = []app.EmailPageCapture{}
	} else if f.calls == 2 {
		if len(request.Discovery.RetryTargets) != 1 || request.Discovery.RetryTargets[0].ReceivedAt == nil ||
			!request.Discovery.RetryTargets[0].ReceivedAt.Equal(received) {
			return app.EmailPageResult{}, errors.New("retry lost the listed receipt time")
		}
	}
	return page, nil
}

func TestQQFailedOriginalRetryPreservesReceiptTimeAfterRestart(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	repo, err := store.NewFileStore(state)
	if err != nil {
		t.Fatal(err)
	}
	s, base, _ := newFixtureService(t, repo)
	browser := &qqRetryPageFixture{qqMarkReadPageFixture: &qqMarkReadPageFixture{intakeFixture: base}}
	s.browser = browser
	s.opts.QualifiedProviderModes[app.EmailProviderQQMail] = app.EmailProviderModeTimeRange
	now := time.Now().UTC().Truncate(time.Microsecond)
	s.now = func() time.Time { return now }
	box, err := s.Configure(t.Context(), "email-owner", app.EmailProviderQQMail, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.plan(t.Context()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover}); err != nil || !worked {
		t.Fatalf("initial collection: %t %v", worked, err)
	}
	failed, _, err := repo.GetEmailMailbox(t.Context(), "email-owner", box.ID)
	if err != nil || failed.PendingFailureCount != 1 || browser.markCalls != 0 {
		t.Fatalf("failed original must remain pending and unread: %+v %v", failed, err)
	}
	reopened, err := store.NewFileStore(state)
	if err != nil {
		t.Fatal(err)
	}
	s.repository = reopened
	now = now.Add(time.Minute)
	if _, err = s.Sync(t.Context(), "email-owner", box.ID); err != nil {
		t.Fatal(err)
	}
	if worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover}); err != nil || !worked {
		t.Fatalf("retry collection: %t %v", worked, err)
	}
	recovered, _, err := reopened.GetEmailMailbox(t.Context(), "email-owner", box.ID)
	if err != nil || recovered.PendingFailureCount != 0 || recovered.LastSyncErrorCode != "" || recovered.RefreshPending || !recovered.PollThrough.Equal(now) {
		t.Fatalf("retry still blocks the mailbox: %+v %v", recovered, err)
	}
	mails, err := reopened.ListEmailMails(t.Context(), store.EmailQuery{OwnerID: "email-owner", MailboxID: box.ID, Limit: 10})
	if err != nil || len(mails.Items) != 1 || mails.Items[0].CaptureState != app.EmailCaptureComplete {
		t.Fatalf("original not recovered: %+v %v", mails, err)
	}
}
