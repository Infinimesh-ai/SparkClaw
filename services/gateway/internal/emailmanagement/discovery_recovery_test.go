package emailmanagement

import (
	"context"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"path/filepath"
	"testing"
	"time"
)

type restartIntervalBrowser struct {
	*intakeFixture
	now, received time.Time
}

func (b *restartIntervalBrowser) CollectPageForOwner(ctx context.Context, owner string, request app.EmailReadRequest) (app.EmailPageResult, error) {
	d := app.EmailDiscoveryResult{Provider: app.EmailProviderGmail, AccountAddress: "owner@example.com", ObservedAt: b.now, Coverage: app.EmailDiscoveryCoverage{Lane: "recent_inbound"}}
	if request.Discovery.IntervalEnd.Before(b.received) {
		d.Coverage.Limited = true
		d.Coverage.Reason = "network_page_continues"
	} else {
		d.Coverage.ScanComplete = true
		d.Coverage.BoundaryQualified = true
		d.Candidates = []app.EmailCaptureTarget{{AccountAddress: d.AccountAddress, ProviderMessageID: "already-read-new-mail", ProviderSelectionID: "already-read-new-mail", Folder: "inbox"}}
	}
	return fixtureCollectPage(ctx, owner, request, func(context.Context, string, app.EmailReadRequest) (app.EmailDiscoveryResult, error) { return d, nil }, b.CaptureForOwner)
}
func TestOverflowConfirmationSurvivesRestartThenReadsNewTail(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	repo, err := store.NewFileStore(state)
	if err != nil {
		t.Fatal(err)
	}
	s, base, _ := newFixtureService(t, repo)
	clock := time.Now().UTC().Truncate(time.Microsecond)
	browser := &restartIntervalBrowser{intakeFixture: base, now: clock, received: clock.Add(30 * time.Minute)}
	s.browser = browser
	s.now = func() time.Time { return browser.now }
	box, err := s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	run := func() {
		t.Helper()
		if _, err := s.Sync(t.Context(), "email-owner", box.ID); err != nil {
			t.Fatal(err)
		}
		if worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover}); err != nil || !worked {
			t.Fatalf("round: %v %v", worked, err)
		}
	}
	browser.now = clock.Add(time.Minute)
	run()
	before, _, _ := repo.GetEmailMailbox(t.Context(), "email-owner", box.ID)
	if before.SyncState != app.EmailSyncOverflowConfirmation {
		t.Fatalf("first overflow: %+v", before)
	}
	reopened, err := store.NewFileStore(state)
	if err != nil {
		t.Fatal(err)
	}
	s.repository = reopened
	browser.now = clock.Add(time.Hour)
	run()
	confirmed, _, _ := reopened.GetEmailMailbox(t.Context(), "email-owner", box.ID)
	if confirmed.CoverageGapCount != 1 || !confirmed.PollThrough.Equal(clock.Add(time.Minute)) {
		t.Fatalf("confirmation: %+v", confirmed)
	}
	run()
	page, err := reopened.ListEmailMails(t.Context(), store.EmailQuery{OwnerID: "email-owner"})
	if err != nil || len(page.Items) != 1 || page.Items[0].ProviderMessageID != "already-read-new-mail" {
		t.Fatalf("new tail hidden: %+v %v", page, err)
	}
	after, _, _ := reopened.GetEmailMailbox(t.Context(), "email-owner", box.ID)
	if !after.PollThrough.Equal(browser.now) || after.CoverageGapCount != 1 || !after.DiscoveredThrough.Equal(before.DiscoveredThrough) {
		t.Fatalf("gap history lost: %+v", after)
	}
}
