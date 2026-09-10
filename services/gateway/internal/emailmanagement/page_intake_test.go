package emailmanagement

import (
	"context"
	"encoding/json"
	"errors"
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
	if p, ok := f.pending[r.InvocationID]; ok && r.AckPageID != p.PageID {
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

type failPageAckRepo struct {
	Repository
	fail bool
}

func (r *failPageAckRepo) AdmitEmailDiscovery(ctx context.Context, c store.EmailDiscoveryCommand) (store.EmailDiscoveryAdmission, error) {
	if c.AcknowledgedPageID != "" && r.fail {
		r.fail = false
		return store.EmailDiscoveryAdmission{}, errors.New("simulated restart before page acknowledgement")
	}
	return r.Repository.AdmitEmailDiscovery(ctx, c)
}

func TestPageIntakePublishesSourcesBeforeAckAndResumesAfterLostAck(t *testing.T) {
	repo := store.NewMemoryStore()
	s, base, _ := newFixtureService(t, repo)
	wrapped := &failPageAckRepo{Repository: repo, fail: true}
	s.repository = wrapped
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
		t.Fatalf("source was not committed before ack failure: %+v", mails)
	}
	saved, _, _ := repo.GetEmailMailbox(t.Context(), "email-owner", box.ID)
	if saved.PageAcks["recent_inbound"] != "" {
		t.Fatal("failed ack advanced checkpoint")
	}
	jobs, _ := repo.ListEmailJobs(t.Context(), store.EmailQuery{OwnerID: "email-owner", MailboxID: box.ID, Limit: 50})
	var next time.Time
	for _, j := range jobs {
		if j.Kind == app.EmailJobDiscover {
			next = j.NextAttemptAt
		}
		if j.Kind == app.EmailJobCapture || j.Kind == app.EmailJobMarkRead {
			t.Fatal("page created a per-mail browser task")
		}
	}
	if next.Before(now.Add(20 * time.Minute)) {
		t.Fatal("failed page retry bypassed20minute idle")
	}
	version := mails.Items[0].InputVersion
	now = next.Add(time.Second)
	if worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover}); err != nil || !worked {
		t.Fatalf("resume: %v %v", worked, err)
	}
	saved, _, _ = repo.GetEmailMailbox(t.Context(), "email-owner", box.ID)
	if saved.PageAcks["unread"] != "" || saved.PageAcks["recent_inbound"] == "" {
		jobs, _ := repo.ListEmailJobs(t.Context(), store.EmailQuery{OwnerID: "email-owner", MailboxID: box.ID, Limit: 50})
		t.Fatalf("page not acknowledged: %+v jobs=%+v calls=%d", saved.PageAcks, jobs, len(browser.calls))
	}
	mails, _ = repo.ListEmailMails(t.Context(), store.EmailQuery{OwnerID: "email-owner", MailboxID: box.ID, Limit: 20})
	if mails.Items[0].InputVersion != version {
		t.Fatal("replayed page republished original")
	}
	if browser.calls[1].AckPageID != "" {
		t.Fatal("uncommitted acknowledgement sent to browser")
	}
	jobs, _ = repo.ListEmailJobs(t.Context(), store.EmailQuery{OwnerID: "email-owner", MailboxID: box.ID, Limit: 50})
	var completed time.Time
	for _, j := range jobs {
		if j.Kind == app.EmailJobDiscover {
			completed = j.UpdatedAt
		}
	}
	if err = s.plan(t.Context()); err != nil {
		t.Fatal(err)
	}
	jobs, _ = repo.ListEmailJobs(t.Context(), store.EmailQuery{OwnerID: "email-owner", MailboxID: box.ID, Limit: 50})
	for _, j := range jobs {
		if j.Kind == app.EmailJobDiscover && j.NextAttemptAt.Before(completed.Add(20*time.Minute)) {
			t.Fatal("next round did not wait after page completion")
		}
	}
}

type incompletePageFixture struct{ *pageFixture }

func (f *incompletePageFixture) CollectPageForOwner(ctx context.Context, owner string, r app.EmailReadRequest) (app.EmailPageResult, error) {
	p, err := f.pageFixture.CollectPageForOwner(ctx, owner, r)
	if r.Discovery.Lane == "recent_inbound" && len(p.Captures) > 0 {
		p.Failures = []app.EmailPageFailure{{Target: p.Captures[0].Target, ErrorCode: "email_pinned_message_unavailable"}}
		p.Captures = nil
	}
	return p, err
}
func TestIncompletePagePreservesCheckpointAndStillCollectsRecentLane(t *testing.T) {
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
	if saved.PageAcks["recent_inbound"] != "" || !saved.Boundary.Equal(box.Boundary) {
		t.Fatalf("incomplete interval was acknowledged: %+v", saved.PageAcks)
	}
	jobs, _ := repo.ListEmailJobs(t.Context(), store.EmailQuery{OwnerID: "email-owner", MailboxID: box.ID, Limit: 50})
	for _, j := range jobs {
		if j.Kind == app.EmailJobDiscover && j.State != app.EmailJobSucceeded {
			t.Fatalf("partial export would exhaust whole-mailbox retries: %+v", j)
		}
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

type observePageRepo struct {
	Repository
	admissions []store.EmailDiscoveryCommand
}

func (r *observePageRepo) AdmitEmailDiscovery(ctx context.Context, c store.EmailDiscoveryCommand) (store.EmailDiscoveryAdmission, error) {
	r.admissions = append(r.admissions, c)
	return r.Repository.AdmitEmailDiscovery(ctx, c)
}

func TestEmptyPagesFinishNormallyWithoutHidingReadMailOrPendingScope(t *testing.T) {
	for _, scenario := range []string{"all_empty", "already_read_new", "pending_history"} {
		t.Run(scenario, func(t *testing.T) {
			repo := store.NewMemoryStore()
			s, base, _ := newFixtureService(t, repo)
			observed := &observePageRepo{Repository: repo}
			s.repository = observed
			browser := &emptyPageFixture{intakeFixture: base, recentMail: scenario == "already_read_new", partialHistory: scenario == "pending_history"}
			s.browser = browser
			box, err := s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, true, 0)
			if err != nil {
				t.Fatal(err)
			}
			now := box.ActivatedAt.Add(10 * time.Second)
			s.now = func() time.Time { return now }
			originalCursor := ""
			if scenario == "pending_history" {
				position := discoveryCursor{Start: box.ActivatedAt, End: box.ActivatedAt.Add(time.Second), Continuation: "pending-page"}
				raw, _ := json.Marshal(position)
				originalCursor = string(raw)
				_, err = repo.AdmitEmailDiscovery(t.Context(), store.EmailDiscoveryCommand{EmailCommand: command("email-owner", "pending-history"), MailboxID: box.ID, BindingGeneration: box.BindingGeneration, ObservedAt: now, Trigger: "recent_inbound", Cursor: originalCursor, Coverage: "partial"})
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
			wantCalls := 1
			if scenario == "pending_history" {
				wantCalls = 2
			}
			if len(browser.calls) != wantCalls {
				t.Fatalf("scope checks=%d want %d", len(browser.calls), wantCalls)
			}
			saved, _, _ := repo.GetEmailMailbox(t.Context(), "email-owner", box.ID)
			if saved.PageAcks["unread"] != "" || saved.PageAcks["recent_inbound"] == "" {
				t.Fatal("valid empty page was not acknowledged")
			}
			jobs, _ := repo.ListEmailJobs(t.Context(), store.EmailQuery{OwnerID: "email-owner", MailboxID: box.ID, Limit: 50})
			for _, job := range jobs {
				if job.Kind == app.EmailJobDiscover && (job.State != app.EmailJobSucceeded || job.ErrorCode != "") {
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
				if len(observed.admissions) != 2*wantCalls {
					t.Fatalf("empty page did more than progress+ack: %d writes", len(observed.admissions))
				}
			}
			if scenario == "pending_history" && (saved.Cursor != originalCursor || !saved.Boundary.Equal(box.Boundary) || saved.Coverage != "partial") {
				t.Fatal("empty fresh scope discarded pending history or claimed unscanned coverage")
			}
		})
	}
}
