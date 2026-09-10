package emailmanagement

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type readBeforeDiscoveryBrowser struct {
	*intakeFixture
	now, received time.Time
}

func (b *readBeforeDiscoveryBrowser) DiscoverForOwner(ctx context.Context, owner string, request app.EmailReadRequest) (app.EmailDiscoveryResult, error) {
	if request.Discovery == nil {
		return b.intakeFixture.DiscoverForOwner(ctx, owner, request)
	}
	options := request.Discovery
	result := app.EmailDiscoveryResult{Provider: app.EmailProviderGmail, AccountAddress: "owner@example.com", ObservedAt: b.now,
		Candidates: []app.EmailCaptureTarget{}, Coverage: app.EmailDiscoveryCoverage{Lane: options.Lane, ScanComplete: options.Lane == "unread"}}
	if options.Lane == "recent_inbound" {
		result.Coverage.Reason, result.Coverage.Continuation = "unqualified_historical_page", "historical-page-2"
		if !b.received.Before(options.IntervalStart) && b.received.Before(options.IntervalEnd) {
			result.Candidates = []app.EmailCaptureTarget{{AccountAddress: result.AccountAddress, ProviderMessageID: "already-read-new-mail", ProviderSelectionID: "already-read-new-mail", Folder: "inbox"}}
		}
	}
	return result, nil
}

func TestPartialCatchupDoesNotHideNewAlreadyReadMailAfterRestart(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	repo, err := store.NewFileStore(state)
	if err != nil {
		t.Fatal(err)
	}
	s, browser, _ := newFixtureService(t, repo)
	clock := time.Now().UTC()
	newMail := &readBeforeDiscoveryBrowser{intakeFixture: browser, now: clock, received: clock.Add(30 * time.Minute)}
	s.browser, s.now = newMail, func() time.Time { return newMail.now }
	box, err := repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: command("email-owner", "bind-catchup"), Provider: app.EmailProviderGmail, Address: "owner@example.com", Enabled: true, Boundary: clock.Add(-7 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	job := app.EmailJob{OwnerID: "email-owner", MailboxID: box.ID, BindingGeneration: box.BindingGeneration}
	if err = s.discover(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	before, _, err := repo.GetEmailMailbox(t.Context(), "email-owner", box.ID)
	if err != nil || before.Cursor == "" {
		t.Fatal("historical progress was not retained")
	}
	// The source becomes read before the next tick and never appears in unread
	// discovery. Its receiving time falls after the retained catch-up upper bound.
	reopened, err := store.NewFileStore(state)
	if err != nil {
		t.Fatal(err)
	}
	s.repository, newMail.repo, newMail.now = reopened, reopened, clock.Add(time.Hour)
	if err = s.discover(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	page, err := reopened.ListEmailMails(t.Context(), store.EmailQuery{OwnerID: "email-owner"})
	if err != nil || len(page.Items) != 1 || page.Items[0].ProviderMessageID != "already-read-new-mail" {
		t.Fatalf("new read mail hidden behind historical catch-up: %+v %v", page, err)
	}
	after, _, err := reopened.GetEmailMailbox(t.Context(), "email-owner", box.ID)
	if err != nil || !after.Boundary.Equal(before.Boundary) || after.Cursor != before.Cursor || after.Coverage != "partial" {
		t.Fatalf("recent observation skipped unqualified history: %+v %v", after, err)
	}
	var pending discoveryCursor
	if json.Unmarshal([]byte(after.Cursor), &pending) != nil || !pending.End.Equal(clock) {
		t.Fatal("catch-up upper bound drifted")
	}
}
