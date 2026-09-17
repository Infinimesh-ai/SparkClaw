package emailmanagement

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type pageFixture struct {
	*intakeFixture
	calls   []app.EmailReadRequest
	pending map[string]app.EmailPageResult
}

func (f *pageFixture) CollectPageForOwner(ctx context.Context, owner string, r app.EmailReadRequest) (app.EmailPageResult, error) {
	f.calls = append(f.calls, r)
	if p, ok := f.pending[r.InvocationID]; ok {
		return p, nil
	}
	p := app.EmailPageResult{SchemaVersion: 1, Provider: app.EmailProviderGmail, AccountAddress: "owner@example.com", PageID: "page_" + strings.Repeat("a", 64), DiscoveryOptions: *r.Discovery, ObservedAt: time.Now().UTC(), Status: "completed"}
	if r.Discovery.IntervalEnd.After(p.ObservedAt) {
		p.ObservedAt = r.Discovery.IntervalEnd
	}
	p.ObservedAt = p.ObservedAt.Add(2 * time.Second)
	p.Discovery = app.EmailDiscoveryResult{SchemaVersion: 1, Provider: p.Provider, AccountAddress: p.AccountAddress, ObservedAt: p.ObservedAt, Candidates: []app.EmailCaptureTarget{}, Coverage: app.EmailDiscoveryCoverage{Lane: r.Discovery.Lane, ScanComplete: true, BoundaryQualified: r.Discovery.Lane == "recent_inbound"}}
	if r.Discovery.Lane == "recent_inbound" {
		target := app.EmailCaptureTarget{AccountAddress: p.AccountAddress, ProviderMessageID: "message-1", ProviderSelectionID: "selection-1", Folder: "inbox"}
		capture, err := fixtureCapture(ctx, f.root, owner, emailautomation.PageCaptureInvocationID(r.InvocationID, p.Provider, target), target.ProviderMessageID)
		if err != nil {
			return p, err
		}
		capture.ReadState = "read"
		p.Discovery.Candidates = append(p.Discovery.Candidates, target)
		p.Captures = []app.EmailPageCapture{{Target: target, Result: app.EmailReadResult{Status: "collected", Capture: &capture}}}
	}
	f.pending[r.InvocationID] = p
	return p, nil
}

func TestPageIntakePublishesSourceAndAdvancesOneFixedInterval(t *testing.T) {
	repo := store.NewMemoryStore()
	s, base, _ := newFixtureService(t, repo)
	browser := &pageFixture{intakeFixture: base, pending: map[string]app.EmailPageResult{}}
	s.browser = browser
	now := time.Now().UTC().Truncate(time.Microsecond)
	s.now = func() time.Time { return now }
	s.opts.ScanInterval = 20 * time.Minute
	box, err := s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.plan(t.Context()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover}); err != nil || !worked {
		t.Fatalf("first attempt: %v %v", worked, err)
	}
	mails, err := repo.ListEmailMails(t.Context(), store.EmailQuery{OwnerID: "email-owner", MailboxID: box.ID, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(mails.Items) != 1 || mails.Items[0].CaptureState != app.EmailCaptureComplete || mails.Items[0].RemoteReadState != "read" {
		t.Fatalf("source was not committed by the incremental round: %+v", mails)
	}
	saved, _, _ := repo.GetEmailMailbox(t.Context(), "email-owner", box.ID)
	if saved.ScopeVersion != app.EmailSyncScopeTimelineV2 || !saved.PollThrough.Equal(browser.calls[0].Discovery.IntervalEnd) || !saved.InflightUntil.IsZero() {
		t.Fatalf("fixed interval was not committed exactly once: %+v", saved)
	}
	jobs, _ := repo.ListEmailJobs(t.Context(), store.EmailQuery{OwnerID: "email-owner", MailboxID: box.ID, Limit: 50})
	for _, j := range jobs {
		if j.Kind == app.EmailJobCapture || j.Kind == app.EmailJobMarkRead || j.Kind == app.EmailJobThreadSync {
			t.Fatal("incremental round created legacy per-mail or history work")
		}
	}
	if len(browser.calls) != 1 || browser.calls[0].Discovery.Continuation != "" {
		t.Fatalf("incremental round performed hidden pagination: %+v", browser.calls)
	}
}

type incompletePageFixture struct{ *pageFixture }

func (f *incompletePageFixture) CollectPageForOwner(ctx context.Context, owner string, r app.EmailReadRequest) (app.EmailPageResult, error) {
	p, err := f.pageFixture.CollectPageForOwner(ctx, owner, r)
	if r.Discovery.Lane == "recent_inbound" && len(p.Captures) > 0 {
		p.Failures = []app.EmailPageFailure{{Target: p.Captures[0].Target, ErrorCode: "email_pinned_message_unavailable", Scope: app.EmailSyncFailureMailSpecific, Qualified: true}}
		p.Captures = nil
	}
	return p, err
}
func TestMailFailureRetriesOnceThenBecomesTerminalSuppression(t *testing.T) {
	repo := store.NewMemoryStore()
	s, base, _ := newFixtureService(t, repo)
	f := &pageFixture{intakeFixture: base, pending: map[string]app.EmailPageResult{}}
	s.browser = &incompletePageFixture{f}
	box, err := s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return time.Now().UTC().Add(time.Second) }
	if err = s.plan(t.Context()); err != nil {
		t.Fatal(err)
	}
	if worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover}); !worked || err != nil {
		t.Fatalf("page run %v %v", worked, err)
	}
	saved, _, _ := repo.GetEmailMailbox(t.Context(), "email-owner", box.ID)
	if saved.PendingFailureCount != 1 || saved.SuppressedMailCount != 0 || saved.UnacknowledgedWarningCount != 0 {
		t.Fatalf("first qualified failure was not retained exactly once: %+v", saved)
	}
	if _, err = s.Sync(t.Context(), "email-owner", box.ID); err != nil {
		t.Fatal(err)
	}
	if worked, workErr := s.workOne(t.Context(), []string{app.EmailJobDiscover}); !worked || workErr != nil {
		t.Fatalf("second round %v %v", worked, workErr)
	}
	saved, _, _ = repo.GetEmailMailbox(t.Context(), "email-owner", box.ID)
	if saved.PendingFailureCount != 0 || saved.SuppressedMailCount != 1 || saved.UnacknowledgedWarningCount != 1 {
		t.Fatalf("second qualified failure was not terminally suppressed: %+v", saved)
	}
	if len(f.calls) < 2 || len(f.calls[1].Discovery.RetryTargets) != 1 || f.calls[1].Discovery.RetryTargets[0].ProviderMessageID != "message-1" {
		t.Fatalf("next normal poll did not carry the exact retained failure: %+v", f.calls)
	}
	if _, err = s.Sync(t.Context(), "email-owner", box.ID); err != nil {
		t.Fatal(err)
	}
	if worked, workErr := s.workOne(t.Context(), []string{app.EmailJobDiscover}); !worked || workErr != nil {
		t.Fatalf("third round %v %v", worked, workErr)
	}
	if len(f.calls[2].Discovery.RetryTargets) != 0 {
		t.Fatal("terminally suppressed mail was scheduled again")
	}
}

type continuedPageFixture struct {
	*intakeFixture
	calls []app.EmailReadRequest
}

func (f *continuedPageFixture) CollectPageForOwner(_ context.Context, _ string, r app.EmailReadRequest) (app.EmailPageResult, error) {
	f.calls = append(f.calls, r)
	complete := r.Discovery.Continuation != ""
	pageID := "page_" + strings.Repeat("a", 64)
	coverage := app.EmailDiscoveryCoverage{Lane: r.Discovery.Lane, ScanComplete: complete, BoundaryQualified: complete}
	if !complete {
		coverage.Continuation = "n1:next"
		coverage.Reason = "network_page_continues"
	} else {
		pageID = "page_" + strings.Repeat("b", 64)
	}
	discovery := app.EmailDiscoveryResult{SchemaVersion: 1, Provider: app.EmailProviderGmail, AccountAddress: "owner@example.com", ObservedAt: r.Discovery.IntervalEnd, Coverage: coverage}
	return app.EmailPageResult{SchemaVersion: 1, Provider: discovery.Provider, AccountAddress: discovery.AccountAddress, PageID: pageID, DiscoveryOptions: *r.Discovery, Discovery: discovery, Status: "empty", ObservedAt: discovery.ObservedAt}, nil
}

func TestPageIntakeConfirmsOverflowOnceThenRecordsCoverageGap(t *testing.T) {
	repo := store.NewMemoryStore()
	s, base, _ := newFixtureService(t, repo)
	browser := &continuedPageFixture{intakeFixture: base}
	s.browser = browser
	box, err := s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return box.ActivatedAt.Add(time.Minute) }
	if err = s.plan(t.Context()); err != nil {
		t.Fatal(err)
	}
	if worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover}); !worked || err != nil {
		t.Fatalf("page run %v %v", worked, err)
	}
	if len(browser.calls) != 1 {
		t.Fatalf("one poll made %d provider calls", len(browser.calls))
	}
	firstStart, firstEnd := browser.calls[0].Discovery.IntervalStart, browser.calls[0].Discovery.IntervalEnd
	saved, _, err := repo.GetEmailMailbox(t.Context(), "email-owner", box.ID)
	if err != nil || saved.SyncState != app.EmailSyncOverflowConfirmation || !saved.PollThrough.Equal(firstStart) || !saved.InflightUntil.IsZero() {
		t.Fatalf("first overflow did not freeze for one confirmation: %+v err=%v", saved, err)
	}
	if _, err = s.Sync(t.Context(), "email-owner", box.ID); err != nil {
		t.Fatal(err)
	}
	if worked, workErr := s.workOne(t.Context(), []string{app.EmailJobDiscover}); !worked || workErr != nil {
		t.Fatalf("confirmation round %v %v", worked, workErr)
	}
	if len(browser.calls) != 2 || !browser.calls[1].Discovery.IntervalStart.Equal(firstStart) || !browser.calls[1].Discovery.IntervalEnd.Equal(firstEnd) || browser.calls[1].Discovery.Continuation != "" {
		t.Fatalf("overflow confirmation did not repeat the exact fixed interval: %+v", browser.calls)
	}
	saved, _, err = repo.GetEmailMailbox(t.Context(), "email-owner", box.ID)
	if err != nil || saved.SyncState != app.EmailSyncCoverageGap || saved.CoverageGapCount != 1 || saved.UnacknowledgedWarningCount != 1 || !saved.PollThrough.Equal(firstEnd) {
		t.Fatalf("second overflow did not become a terminal coverage gap: %+v err=%v", saved, err)
	}
}

func TestPageDirectionDoesNotLabelSentOrUnknownMembersInbound(t *testing.T) {
	for folder, want := range map[string]string{"sent": "sent", "all": "unknown", "inbox": "inbound", "qq:1001": "inbound"} {
		if got := pageDirection(folder); got != want {
			t.Fatalf("%s: %s", folder, got)
		}
	}
}

type emptyPageFixture struct {
	*intakeFixture
	calls          []app.EmailReadRequest
	recentMail     bool
	partialHistory bool
}

func (f *emptyPageFixture) CollectPageForOwner(ctx context.Context, owner string, r app.EmailReadRequest) (app.EmailPageResult, error) {
	f.calls = append(f.calls, r)
	observed := r.Discovery.IntervalEnd
	if observed.IsZero() {
		observed = time.Now().UTC()
	}
	observed = observed.Add(time.Second)
	p := app.EmailPageResult{SchemaVersion: 1, Provider: app.EmailProviderGmail, Status: "completed", AccountAddress: "owner@example.com", PageID: "page_" + strings.Repeat("e", 64), DiscoveryOptions: *r.Discovery, ObservedAt: observed}
	p.Discovery = app.EmailDiscoveryResult{SchemaVersion: 1, Provider: p.Provider, AccountAddress: p.AccountAddress, ObservedAt: observed, Coverage: app.EmailDiscoveryCoverage{Lane: r.Discovery.Lane, ScanComplete: true, BoundaryQualified: r.Discovery.Lane == "recent_inbound"}}
	if f.partialHistory && r.Discovery.Continuation != "" {
		p.Discovery.Coverage.ScanComplete = false
		p.Discovery.Coverage.BoundaryQualified = false
		p.Discovery.Coverage.Continuation = r.Discovery.Continuation
		p.Discovery.Coverage.Reason = "scope_partial"
	}
	if f.recentMail && r.Discovery.Lane == "recent_inbound" {
		target := app.EmailCaptureTarget{AccountAddress: p.AccountAddress, ProviderMessageID: "already-read-new", ProviderSelectionID: "already-read-new", Folder: "inbox"}
		capture, err := fixtureCapture(ctx, f.root, owner, emailautomation.PageCaptureInvocationID(r.InvocationID, p.Provider, target), target.ProviderMessageID)
		if err != nil {
			return p, err
		}
		capture.ReadState = "read"
		p.Discovery.Candidates = []app.EmailCaptureTarget{target}
		p.Captures = []app.EmailPageCapture{{Target: target, Result: app.EmailReadResult{Status: "collected", Capture: &capture}}}
	}
	return p, nil
}

func TestEmptyPagesFinishNormallyWithoutHidingReadMailOrPendingScope(t *testing.T) {
	for _, scenario := range []string{"all_empty", "already_read_new", "pending_history"} {
		t.Run(scenario, func(t *testing.T) {
			repo := store.NewMemoryStore()
			s, base, _ := newFixtureService(t, repo)
			browser := &emptyPageFixture{intakeFixture: base, recentMail: scenario == "already_read_new", partialHistory: scenario == "pending_history"}
			s.browser = browser
			box, err := s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, true, 0)
			if err != nil {
				t.Fatal(err)
			}
			now := box.ActivatedAt.Add(10 * time.Second)
			s.now = func() time.Time { return now }
			if scenario == "pending_history" {
				position := map[string]any{"start": box.ActivatedAt, "end": box.ActivatedAt.Add(time.Second), "continuation": "pending-page"}
				raw, _ := json.Marshal(position)
				_, err = repo.AdmitEmailDiscovery(t.Context(), store.EmailDiscoveryCommand{EmailCommand: command("email-owner", "pending-history"), MailboxID: box.ID, BindingGeneration: box.BindingGeneration, ObservedAt: now, Trigger: "recent_inbound", Cursor: string(raw), Coverage: "partial"})
				if err != nil {
					t.Fatal(err)
				}
			}
			if err = s.plan(t.Context()); err != nil {
				t.Fatal(err)
			}
			worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover})
			if !worked || err != nil {
				t.Fatalf("empty collection did not finish: %v %v", worked, err)
			}
			if len(browser.calls) != 1 {
				t.Fatalf("one poll made %d provider calls", len(browser.calls))
			}
			saved, _, _ := repo.GetEmailMailbox(t.Context(), "email-owner", box.ID)
			if saved.ScopeVersion != app.EmailSyncScopeTimelineV2 || saved.Cursor != "" || len(saved.PageAcks) != 0 || !saved.PollThrough.Equal(browser.calls[0].Discovery.IntervalEnd) {
				t.Fatalf("empty interval did not advance the v2 checkpoint: %+v", saved)
			}
			jobs, _ := repo.ListEmailJobs(t.Context(), store.EmailQuery{OwnerID: "email-owner", MailboxID: box.ID, Limit: 50})
			for _, job := range jobs {
				if job.Kind == app.EmailJobDiscover && (job.State != app.EmailJobQueued || job.ErrorCode != "" || job.Attempt != 0 || !job.NextAttemptAt.Equal(job.UpdatedAt.Add(s.opts.ScanInterval))) {
					t.Fatalf("empty page retried/failed: %+v", job)
				}
				if job.Kind == app.EmailJobCapture || job.Kind == app.EmailJobMarkRead {
					t.Fatal("empty-page path created per-mail browser work")
				}
			}
			mails, _ := repo.ListEmailMails(t.Context(), store.EmailQuery{OwnerID: "email-owner", MailboxID: box.ID, Limit: 20})
			if scenario == "already_read_new" {
				if len(mails.Items) != 1 || mails.Items[0].CaptureState != app.EmailCaptureComplete {
					t.Fatal("recent scan missed newly arrived already-read mail")
				}
			} else {
				if len(mails.Items) != 0 {
					t.Fatal("empty page fabricated mail")
				}
			}
		})
	}
}
