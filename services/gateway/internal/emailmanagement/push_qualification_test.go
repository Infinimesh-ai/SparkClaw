package emailmanagement

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

var pushTestNow = time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)

func pushTestBox() app.EmailMailbox {
	return app.EmailMailbox{ID: "box-qq", OwnerID: "owner", Provider: app.EmailProviderQQMail, Address: "owner@qq.example", BindingGeneration: 1, WatchState: "watching"}
}

// qqMatch is a natural QQ arrival: the notification's e field equals the
// listed emailid, which is also the row's selection and thread locator.
func qqMatch(id string) pushMatch {
	return pushMatch{kind: "message_id", candidate: pushCandidate{provider: app.EmailProviderQQMail, target: browsercontrol.MailObserverTarget{ProviderMessageID: id}},
		member: store.EmailDiscoveryMember{ProviderMessageID: id, ProviderSelectionID: id, ProviderThreadID: id, Folder: "inbox"}}
}

func qualifyPushGate(q *pushQualifications, box app.EmailMailbox, arrivals int) {
	for i := 0; i < arrivals; i++ {
		q.observeRound("owner", box, []pushMatch{qqMatch(fmt.Sprintf("ZC%04d", i))}, nil, pushRoundDirect{}, pushTestNow.Add(time.Duration(i)*time.Minute))
	}
}

func TestPushGateQualifiesOnlyAfterThresholdAndSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	box := pushTestBox()
	q := newPushQualifications(root, PushDirectFetchOptions{Enabled: true, Threshold: 3, QualifiedPollInterval: 5 * time.Minute})
	qualifyPushGate(q, box, 2)
	if status := q.status("owner", box); status.Qualified || status.Consecutive != 2 || !status.DirectCapable {
		t.Fatalf("two arrivals must not qualify: %+v", status)
	}
	if relaxed, _ := q.relaxedInterval("owner", box); relaxed != 0 {
		t.Fatal("an unqualified provider must keep its cadence")
	}
	q.observeRound("owner", box, []pushMatch{qqMatch("ZC0002")}, nil, pushRoundDirect{}, pushTestNow.Add(3*time.Minute))
	status := q.status("owner", box)
	if !status.Qualified || status.Consecutive != 3 || status.QualifiedAt == nil || status.Matched["message_id"] != 3 {
		t.Fatalf("third consecutive arrival must qualify: %+v", status)
	}
	_, epoch := q.relaxedInterval("owner", box)
	restarted := newPushQualifications(root, PushDirectFetchOptions{Enabled: true, Threshold: 3, QualifiedPollInterval: 5 * time.Minute})
	if relaxed, again := restarted.relaxedInterval("owner", box); relaxed != 5*time.Minute || again != epoch || epoch == 0 {
		t.Fatalf("qualification was not persisted: %s epoch %d/%d", relaxed, again, epoch)
	}
	rebound := box
	rebound.BindingGeneration++
	if restarted.status("owner", rebound).Qualified {
		t.Fatal("a new binding generation must start list-only")
	}
	stricter := newPushQualifications(root, PushDirectFetchOptions{Enabled: true, Threshold: 4})
	if stricter.status("owner", box).Qualified {
		t.Fatal("raising the threshold must require further arrivals")
	}
}

func TestPushGateResetsOnUnmatchedNonCanonicalShapeAndDirectFailure(t *testing.T) {
	box := pushTestBox()
	for name, fail := range map[string]func(*pushQualifications){
		"unmatched candidate": func(q *pushQualifications) {
			q.observeRound("owner", box, nil, []pushCandidate{{provider: box.Provider}}, pushRoundDirect{}, pushTestNow.Add(time.Hour))
		},
		"item id match": func(q *pushQualifications) {
			m := qqMatch("item")
			m.kind = "item_id"
			q.observeRound("owner", box, []pushMatch{m}, nil, pushRoundDirect{}, pushTestNow.Add(time.Hour))
		},
		"changed row shape": func(q *pushQualifications) {
			m := qqMatch("ZC9999")
			m.member.ProviderSelectionID = "other"
			q.observeRound("owner", box, []pushMatch{m}, nil, pushRoundDirect{}, pushTestNow.Add(time.Hour))
		},
		"failed direct capture": func(q *pushQualifications) {
			q.observeRound("owner", box, nil, nil, pushRoundDirect{attempted: []string{"ZC9999"}, failed: "direct_capture_failed"}, pushTestNow.Add(time.Hour))
		},
		"failed direct round": func(q *pushQualifications) {
			q.degrade("owner", box, "direct_round_failed", pushTestNow.Add(time.Hour))
		},
	} {
		t.Run(name, func(t *testing.T) {
			q := newPushQualifications(t.TempDir(), PushDirectFetchOptions{Enabled: true, Threshold: 3, QualifiedPollInterval: 5 * time.Minute})
			qualifyPushGate(q, box, 3)
			q.observeNotification("owner", box, "qq_inbound_envelope", []browsercontrol.MailObserverTarget{{ProviderMessageID: "ZC9999"}}, pushTestNow.Add(5*time.Minute))
			_, before := q.relaxedInterval("owner", box)
			fail(q)
			status := q.status("owner", box)
			if status.Qualified || status.Consecutive != 0 || status.PendingTargets != 0 || status.Degraded != 1 {
				t.Fatalf("gate did not reset: %+v", status)
			}
			if relaxed, after := q.relaxedInterval("owner", box); relaxed != 0 || after == before {
				t.Fatalf("degradation must restore the cadence under a new epoch: %s %d/%d", relaxed, before, after)
			}
			if targets, _ := q.directTargets("owner", box, 9, pushTestNow, pushTestNow.Add(6*time.Minute)); len(targets) != 0 {
				t.Fatal("a reset gate must not issue direct targets")
			}
			qualifyPushGate(q, box, 3)
			if !q.status("owner", box).Qualified {
				t.Fatal("three new consecutive arrivals must requalify")
			}
		})
	}
}

func TestPushGateGmailWithoutIdentifiersAndOutlookItemIDsNeverQualify(t *testing.T) {
	q := newPushQualifications(t.TempDir(), PushDirectFetchOptions{Enabled: true, Threshold: 1, QualifiedPollInterval: 5 * time.Minute})
	gmail := app.EmailMailbox{ID: "box-gmail", OwnerID: "owner", Provider: app.EmailProviderGmail, Address: "owner@gmail.example", BindingGeneration: 1, WatchState: "watching"}
	for _, reason := range []string{"registration", "sequence_gap", "gmail_topic_invalidation", "gmail_topic_invalidation"} {
		q.observeNotification("owner", gmail, reason, nil, pushTestNow)
	}
	// Gmail's listed rows carry a native locator and no notification identifier.
	native := qqMatch("18f0a")
	native.member.ProviderNativeID = "msg-f:1"
	q.observeRound("owner", gmail, []pushMatch{native}, nil, pushRoundDirect{}, pushTestNow)
	status := q.status("owner", gmail)
	if status.NoIdentifier != 2 || status.Qualified || status.DirectCapable || status.LastOutcome != "not_direct_capable" {
		t.Fatalf("gmail gate: %+v", status)
	}
	if targets, _ := q.directTargets("owner", gmail, 1, pushTestNow, pushTestNow); targets != nil {
		t.Fatal("gmail must stay list-only")
	}
	outlook := app.EmailMailbox{ID: "box-outlook", OwnerID: "owner", Provider: app.EmailProviderOutlook, Address: "owner@outlook.example", BindingGeneration: 1, WatchState: "watching"}
	for i := 0; i < 3; i++ {
		m := qqMatch("AQMk")
		m.kind, m.member.ProviderMessageID, m.member.ProviderNativeID = "item_id", "immutable", "AQMk"
		q.observeRound("owner", outlook, []pushMatch{m}, nil, pushRoundDirect{}, pushTestNow)
	}
	if status := q.status("owner", outlook); status.Qualified || status.Matched["item_id"] != 3 || status.Consecutive != 0 {
		t.Fatalf("outlook ItemId is not a canonical identity: %+v", status)
	}
}

func TestPushGateDirectTargetsAreOptInBoundedAndFrozenPerCheckpoint(t *testing.T) {
	box := pushTestBox()
	disabled := newPushQualifications(t.TempDir(), PushDirectFetchOptions{Threshold: 3})
	qualifyPushGate(disabled, box, 3)
	disabled.observeNotification("owner", box, "qq_inbound_envelope", []browsercontrol.MailObserverTarget{{ProviderMessageID: "ZCnew"}}, pushTestNow.Add(5*time.Minute))
	if targets, _ := disabled.directTargets("owner", box, 1, pushTestNow, pushTestNow.Add(5*time.Minute)); targets != nil || !disabled.status("owner", box).Qualified {
		t.Fatal("measurement may qualify, but direct fetch is off by default")
	}

	q := newPushQualifications(t.TempDir(), PushDirectFetchOptions{Enabled: true, Threshold: 3})
	qualifyPushGate(q, box, 3)
	at := pushTestNow.Add(5 * time.Minute)
	q.observeNotification("owner", box, "qq_inbound_envelope", []browsercontrol.MailObserverTarget{{ProviderMessageID: "ZCa"}, {ProviderMessageID: "ZCa"}, {ProviderItemID: "item-only"}}, at)
	targets, ids := q.directTargets("owner", box, 7, pushTestNow, at)
	want := app.EmailCaptureTarget{AccountAddress: box.Address, ProviderMessageID: "ZCa", ProviderSelectionID: "ZCa", ProviderThreadID: "ZCa", Folder: "inbox"}
	if len(targets) != 1 || targets[0] != want || len(ids) != 1 {
		t.Fatalf("direct targets: %+v", targets)
	}
	q.observeNotification("owner", box, "qq_inbound_envelope", []browsercontrol.MailObserverTarget{{ProviderMessageID: "ZCb"}}, at)
	if again, _ := q.directTargets("owner", box, 7, pushTestNow, at); len(again) != 1 || again[0].ProviderMessageID != "ZCa" {
		t.Fatalf("a replayed checkpoint must keep its frozen targets: %+v", again)
	}
	if next, _ := q.directTargets("owner", box, 8, pushTestNow.Add(time.Second), at); len(next) != 2 {
		t.Fatalf("a new checkpoint selects every pending target: %+v", next)
	}
	for i := 0; i < 40; i++ {
		q.observeNotification("owner", box, "qq_inbound_envelope", []browsercontrol.MailObserverTarget{{ProviderMessageID: fmt.Sprintf("ZCbulk%02d", i)}}, at)
	}
	if status := q.status("owner", box); status.PendingTargets != maxPushDirectPending {
		t.Fatalf("pending targets must be bounded: %d", status.PendingTargets)
	}
	if next, _ := q.directTargets("owner", box, 9, pushTestNow.Add(2*time.Second), at); len(next) != maxPushDirectPerRound {
		t.Fatalf("one round must be bounded: %d", len(next))
	}
	if late, _ := q.directTargets("owner", box, 10, pushTestNow.Add(3*time.Second), at.Add(pushCandidateTTL)); len(late) != 0 {
		t.Fatal("expired notification targets must not be fetched")
	}
}

func TestVerificationIntervalRelaxesOnlyForQualifiedEnabledWatchingMailbox(t *testing.T) {
	box := pushTestBox()
	for name, test := range map[string]struct {
		opts      PushDirectFetchOptions
		arrivals  int
		watch     string
		want      time.Duration
		epochKeys bool
	}{
		"default":                  {opts: PushDirectFetchOptions{Threshold: 3}, arrivals: 3, watch: "watching", want: time.Minute},
		"enabled without interval": {opts: PushDirectFetchOptions{Enabled: true, Threshold: 3}, arrivals: 3, watch: "watching", want: time.Minute},
		"interval without switch":  {opts: PushDirectFetchOptions{Threshold: 3, QualifiedPollInterval: 5 * time.Minute}, arrivals: 3, watch: "watching", want: time.Minute},
		"below threshold":          {opts: PushDirectFetchOptions{Enabled: true, Threshold: 3, QualifiedPollInterval: 5 * time.Minute}, arrivals: 2, watch: "watching", want: time.Minute, epochKeys: true},
		"observer degraded":        {opts: PushDirectFetchOptions{Enabled: true, Threshold: 3, QualifiedPollInterval: 5 * time.Minute}, arrivals: 3, watch: "degraded", want: time.Minute, epochKeys: true},
		"qualified":                {opts: PushDirectFetchOptions{Enabled: true, Threshold: 3, QualifiedPollInterval: 5 * time.Minute}, arrivals: 3, watch: "watching", want: 5 * time.Minute, epochKeys: true},
	} {
		t.Run(name, func(t *testing.T) {
			s := &Service{opts: Options{ScanInterval: time.Minute, PushDirectFetch: test.opts}, pushq: newPushQualifications(t.TempDir(), test.opts)}
			qualifyPushGate(s.pushq, box, test.arrivals)
			candidate := box
			candidate.WatchState = test.watch
			if got := s.verificationInterval(candidate); got != test.want {
				t.Fatalf("interval = %s, want %s", got, test.want)
			}
			key := s.pollCommandKey("owner", candidate, test.want)
			if strings.Contains(key, ":q") != test.epochKeys || !test.epochKeys && key != fmt.Sprintf("poll-v1:%s:%d:%d", box.ID, box.BindingGeneration, test.want) {
				t.Fatalf("schedule key %q", key)
			}
		})
	}
	// Switching back after a degradation must not replay the earlier
	// one-minute schedule receipt.
	opts := PushDirectFetchOptions{Enabled: true, Threshold: 3, QualifiedPollInterval: 5 * time.Minute}
	s := &Service{opts: Options{ScanInterval: time.Minute, PushDirectFetch: opts}, pushq: newPushQualifications(t.TempDir(), opts)}
	first := s.pollCommandKey("owner", box, s.verificationInterval(box))
	qualifyPushGate(s.pushq, box, 3)
	relaxed := s.pollCommandKey("owner", box, s.verificationInterval(box))
	s.pushq.degrade("owner", box, "direct_round_failed", pushTestNow.Add(time.Hour))
	restored := s.pollCommandKey("owner", box, s.verificationInterval(box))
	if s.verificationInterval(box) != time.Minute || first == relaxed || restored == first || restored == relaxed {
		t.Fatalf("schedule keys must change on each transition: %s %s %s", first, relaxed, restored)
	}
}

// qqDirectPageFixture lists the configured rows and, like the Reader, fetches
// every unlisted retry target through the retained original endpoint.
type qqDirectPageFixture struct {
	*intakeFixture
	listed     []string
	failDirect bool
	calls      []app.EmailReadRequest
}

func (f *qqDirectPageFixture) AdmitIntake(context.Context, string, string) (app.EmailAdmissionBinding, error) {
	return app.EmailAdmissionBinding{Provider: app.EmailProviderQQMail, Account: app.EmailAccountDefault}, nil
}

func (f *qqDirectPageFixture) DiscoverForOwner(_ context.Context, _ string, _ app.EmailReadRequest) (app.EmailDiscoveryResult, error) {
	return app.EmailDiscoveryResult{Provider: app.EmailProviderQQMail, AccountAddress: "owner@qq.example", ObservedAt: time.Now().UTC()}, nil
}

func (f *qqDirectPageFixture) CollectPageForOwner(ctx context.Context, owner string, r app.EmailReadRequest) (app.EmailPageResult, error) {
	f.calls = append(f.calls, r)
	observed := r.Discovery.IntervalEnd.Add(time.Second)
	page := app.EmailPageResult{SchemaVersion: 1, Provider: app.EmailProviderQQMail, Status: "collected", AccountAddress: "owner@qq.example", PageID: "page_" + strings.Repeat("d", 64), DiscoveryOptions: *r.Discovery, ObservedAt: observed,
		Discovery: app.EmailDiscoveryResult{SchemaVersion: 1, Provider: app.EmailProviderQQMail, AccountAddress: "owner@qq.example", Candidates: []app.EmailCaptureTarget{}, Coverage: app.EmailDiscoveryCoverage{Lane: r.Discovery.Lane, ScanComplete: true, BoundaryQualified: true}, ObservedAt: observed},
		Captures:  []app.EmailPageCapture{}, Failures: []app.EmailPageFailure{}}
	capture := func(target app.EmailCaptureTarget) error {
		receipt, err := fixtureCaptureForProviderContent(ctx, f.root, owner, emailautomation.PageCaptureInvocationID(r.InvocationID, app.EmailProviderQQMail, target), "fixture-qq-box", "mail-"+target.ProviderMessageID, target.ProviderMessageID, "2026/09/07", app.EmailProviderQQMail, target.AccountAddress, "", "")
		if err == nil {
			page.Captures = append(page.Captures, app.EmailPageCapture{Target: target, Result: app.EmailReadResult{Status: "collected", Capture: &receipt}})
		}
		return err
	}
	listed := map[string]bool{}
	for _, id := range f.listed {
		received := r.Discovery.IntervalStart
		target := app.EmailCaptureTarget{AccountAddress: "owner@qq.example", ProviderMessageID: id, ProviderSelectionID: id, ProviderThreadID: id, Folder: "inbox", ReceivedAt: &received}
		page.Discovery.Candidates = append(page.Discovery.Candidates, target)
		listed[id] = true
		if err := capture(target); err != nil {
			return page, err
		}
	}
	for _, target := range r.Discovery.RetryTargets {
		if listed[target.ProviderMessageID] {
			continue
		}
		if f.failDirect {
			page.Status = "partial"
			page.Failures = append(page.Failures, app.EmailPageFailure{Target: target, ErrorCode: "email_network_target_unobserved", Scope: app.EmailSyncFailureMailSpecific, Qualified: true})
			continue
		}
		if err := capture(target); err != nil {
			return page, err
		}
	}
	return page, nil
}

func newDirectFetchService(t *testing.T, browser *qqDirectPageFixture) (*Service, *store.MemoryStore, app.EmailMailbox) {
	t.Helper()
	repo := store.NewMemoryStore()
	s, base, _ := newFixtureService(t, repo)
	browser.intakeFixture = base
	s.browser = browser
	s.opts.QualifiedProviderModes[app.EmailProviderQQMail] = app.EmailProviderModeTimeRange
	s.opts.PushDirectFetch = PushDirectFetchOptions{Enabled: true, Threshold: 3, QualifiedPollInterval: 5 * time.Minute}
	s.pushq = newPushQualifications(base.root, s.opts.PushDirectFetch)
	box, err := s.Configure(t.Context(), "email-owner", app.EmailProviderQQMail, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		s.pushq.observeRound("email-owner", box, []pushMatch{qqMatch(fmt.Sprintf("ZCseed%d", i))}, nil, pushRoundDirect{}, time.Now().UTC())
	}
	return s, repo, box
}

func notifyQQ(s *Service, box app.EmailMailbox, id string) {
	now := s.now()
	targets := []browsercontrol.MailObserverTarget{{ProviderMessageID: id}}
	s.pushq.observeRound("email-owner", box, nil, s.push.record("email-owner", box.ID, box.Provider, targets, now), pushRoundDirect{}, now)
	s.pushq.observeNotification("email-owner", box, "qq_inbound_envelope", targets, now)
}

func runDiscoverRound(t *testing.T, s *Service) {
	t.Helper()
	if err := s.plan(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sync(t.Context(), "email-owner", ""); err != nil {
		t.Fatal(err)
	}
	if worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover}); err != nil || !worked {
		t.Fatalf("round: worked=%t err=%v", worked, err)
	}
}

func TestDirectTargetUnlistedByTheListIsCapturedThroughExistingPublication(t *testing.T) {
	browser := &qqDirectPageFixture{}
	s, repo, box := newDirectFetchService(t, browser)
	notifyQQ(s, box, "ZCdirect")
	runDiscoverRound(t, s)
	if len(browser.calls) == 0 {
		t.Fatal("no page request")
	}
	d := browser.calls[0].Discovery
	if len(d.RetryTargets) != 1 || d.RetryTargets[0].ProviderMessageID != "ZCdirect" || d.RetryTargets[0].ProviderSelectionID != "ZCdirect" || len(d.DirectTargetIDs) != 1 {
		t.Fatalf("direct target not requested: %+v", d)
	}
	mail, found, err := repo.GetEmailMail(t.Context(), "email-owner", store.EmailMailID("email-owner", box.ID, "ZCdirect"))
	if err != nil || !found || mail.CaptureState != app.EmailCaptureComplete || !mail.SourceTime.Equal(time.Date(2026, 9, 7, 2, 0, 0, 0, time.UTC)) {
		t.Fatalf("direct capture was not published with its original receipt time: %+v found=%t err=%v", mail, found, err)
	}
	status := s.pushq.status("email-owner", box)
	if !status.Qualified || status.DirectCaptured != 1 || status.PendingTargets != 0 || status.Consecutive != 3 {
		t.Fatalf("a direct-only capture is recorded but does not qualify itself: %+v", status)
	}
	// A late duplicate notification for a mail Store already holds is left to
	// the list instead of fetching the original again.
	notifyQQ(s, box, "ZCdirect")
	runDiscoverRound(t, s)
	for _, call := range browser.calls[1:] {
		if len(call.Discovery.RetryTargets) != 0 {
			t.Fatalf("captured mail was targeted again: %+v", call.Discovery.RetryTargets)
		}
	}
}

func TestDirectTargetListedInTheSameRoundAdvancesTheGate(t *testing.T) {
	browser := &qqDirectPageFixture{listed: []string{"ZClisted"}}
	s, repo, box := newDirectFetchService(t, browser)
	notifyQQ(s, box, "ZClisted")
	runDiscoverRound(t, s)
	mails, err := repo.ListEmailMails(t.Context(), store.EmailQuery{OwnerID: "email-owner", MailboxID: box.ID, Limit: 10})
	if err != nil || len(mails.Items) != 1 || mails.Items[0].CaptureState != app.EmailCaptureComplete {
		t.Fatalf("listed direct target must publish once: %+v err=%v", mails.Items, err)
	}
	if status := s.pushq.status("email-owner", box); !status.Qualified || status.Consecutive != 4 || status.DirectCaptured != 0 || status.PendingTargets != 0 {
		t.Fatalf("listed arrival must advance the gate: %+v", status)
	}
}

func TestFailedDirectCaptureDegradesToListOnlyWithoutAMailboxFailure(t *testing.T) {
	browser := &qqDirectPageFixture{failDirect: true}
	s, repo, box := newDirectFetchService(t, browser)
	notifyQQ(s, box, "ZCmissing")
	before := s.verificationInterval(box)
	runDiscoverRound(t, s)
	if _, found, err := repo.GetEmailMail(t.Context(), "email-owner", store.EmailMailID("email-owner", box.ID, "ZCmissing")); err != nil || found {
		t.Fatalf("an unlisted failed target must not create a mail: found=%t err=%v", found, err)
	}
	current, _, err := repo.GetEmailMailbox(t.Context(), "email-owner", box.ID)
	if err != nil || current.SyncState != app.EmailSyncIdle || current.LastSyncErrorCode != "" {
		t.Fatalf("the list round must still commit cleanly: %+v err=%v", current, err)
	}
	status := s.pushq.status("email-owner", box)
	if status.Qualified || status.DirectFailed != 1 || status.LastOutcome != "direct_capture_failed" || status.PendingTargets != 0 {
		t.Fatalf("direct failure must degrade the gate: %+v", status)
	}
	if got := s.verificationInterval(current); got != s.opts.ScanInterval || got > before {
		t.Fatalf("degraded provider must return to the normal cadence: %s", got)
	}
	notifyQQ(s, box, "ZCnext")
	runDiscoverRound(t, s)
	if last := browser.calls[len(browser.calls)-1]; len(last.Discovery.RetryTargets) != 0 {
		t.Fatal("a degraded provider must use list-only discovery")
	}
}
