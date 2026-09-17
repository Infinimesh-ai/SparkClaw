package emailmanagement

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type idleDiscoveryBrowser struct {
	*intakeFixture
	admissions atomic.Int32
	scans      atomic.Int32
	started    chan struct{}
}

func TestEmailPollingDefaultIsOneMinute(t *testing.T) {
	s, _, _ := newFixtureService(t, store.NewMemoryStore())
	opts := s.opts
	opts.ScanInterval = 0
	configured, err := New(s.repository, s.browser, s.registry, s.analyzer, s.extractor, opts)
	if err != nil || configured.opts.ScanInterval != time.Minute {
		t.Fatalf("default interval: %v", err)
	}
}

func (b *idleDiscoveryBrowser) CollectPageForOwner(ctx context.Context, owner string, r app.EmailReadRequest) (app.EmailPageResult, error) {
	return fixtureCollectPage(ctx, owner, r, b.DiscoverForOwner, b.CaptureForOwner)
}

func (b *idleDiscoveryBrowser) AdmitIntake(ctx context.Context, owner, provider string) (app.EmailAdmissionBinding, error) {
	b.admissions.Add(1)
	return b.intakeFixture.AdmitIntake(ctx, owner, provider)
}

func (b *idleDiscoveryBrowser) DiscoverForOwner(ctx context.Context, owner string, request app.EmailReadRequest) (app.EmailDiscoveryResult, error) {
	if request.Discovery == nil {
		return b.intakeFixture.DiscoverForOwner(ctx, owner, request)
	}
	if b.scans.Add(1) == 1 && b.started != nil {
		close(b.started)
		<-ctx.Done()
		return app.EmailDiscoveryResult{}, ctx.Err()
	}
	return app.EmailDiscoveryResult{Provider: app.EmailProviderGmail, AccountAddress: "owner@example.com", ObservedAt: time.Now().UTC(),
		Candidates: []app.EmailCaptureTarget{}, Coverage: app.EmailDiscoveryCoverage{Lane: request.Discovery.Lane, Reason: "partial_folder_scope"}}, nil
}

func TestLongDiscoveryCompletionWaitsBeforeNextScanAndAcrossRestart(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	repo, err := store.NewFileStore(state)
	if err != nil {
		t.Fatal(err)
	}
	s, fixture, _ := newFixtureService(t, repo)
	s.opts.ScanInterval = time.Minute
	browser := &idleDiscoveryBrowser{intakeFixture: fixture}
	s.browser = browser
	if _, err = s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, true, 0); err != nil {
		t.Fatal(err)
	}
	// Admission occurred in an earlier timer slot, as with a slow real browser
	// scan. Completion now must schedule a future run instead of an immediate one.
	s.now = func() time.Time { return time.Now().UTC().Add(-2 * time.Minute) }
	if err = s.plan(t.Context()); err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return time.Now().UTC() }
	if worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover}); err != nil || !worked {
		t.Fatalf("initial scan: %v", err)
	}
	if browser.admissions.Load() != 2 || browser.scans.Load() != 1 {
		t.Fatal("scan lanes repeated login admission")
	}
	if err = s.plan(t.Context()); err != nil {
		t.Fatal(err)
	}
	if worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover}); err != nil || worked {
		t.Fatalf("completion immediately restarted discovery: worked=%v err=%v", worked, err)
	}
	jobs, err := repo.ListEmailJobs(t.Context(), store.EmailQuery{OwnerID: "email-owner"})
	discover := emailJobsOfKind(jobs, app.EmailJobDiscover)
	if err != nil || len(discover) != 1 || discover[0].State != app.EmailJobQueued || time.Until(discover[0].NextAttemptAt) < 55*time.Second {
		t.Fatalf("missing durable idle interval: %+v %v", jobs, err)
	}
	reopened, err := store.NewFileStore(state)
	if err != nil {
		t.Fatal(err)
	}
	s.repository = reopened
	if err = s.plan(t.Context()); err != nil {
		t.Fatal(err)
	}
	if worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover}); err != nil || worked {
		t.Fatalf("restart discarded idle interval: worked=%v err=%v", worked, err)
	}
}

func TestMinutePollManualResetsSameMinuteDeadlineAndStatus(t *testing.T) {
	repo := store.NewMemoryStore()
	s, fixture, _ := newFixtureService(t, repo)
	s.opts.ScanInterval = time.Minute
	s.browser = &idleDiscoveryBrowser{intakeFixture: fixture}
	box, err := s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.plan(t.Context()); err != nil {
		t.Fatal(err)
	}
	if worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover}); !worked || err != nil {
		t.Fatalf("initial round: %v", err)
	}
	for round := 0; round < 2; round++ {
		requested, err := s.Sync(t.Context(), "email-owner", box.ID)
		if err != nil || len(requested.RefreshRequests) != 1 {
			t.Fatalf("manual request: %+v %v", requested, err)
		}
		status, err := s.Status(t.Context(), "email-owner")
		if err != nil || !status.Mailboxes[0].RefreshPending {
			t.Fatalf("missing pending: %+v %v", status, err)
		}
		if worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover}); !worked || err != nil {
			t.Fatalf("manual round: %v", err)
		}
		status, err = s.Status(t.Context(), "email-owner")
		if err != nil || status.Mailboxes[0].RefreshPending || status.Mailboxes[0].RefreshRequestID != requested.RefreshRequests[0].RefreshRequestID || status.Backlog != 0 {
			t.Fatalf("missing completion: %+v %v", status, err)
		}
		jobs, err := repo.ListEmailJobs(t.Context(), store.EmailQuery{OwnerID: "email-owner", MailboxID: box.ID})
		if err != nil || len(jobs) != 1 || !jobs[0].NextAttemptAt.Equal(jobs[0].RoundFinishedAt.Add(time.Minute)) {
			t.Fatalf("manual did not reset idle: %+v %v", jobs, err)
		}
		// No planner runs between completion and this assertion. A cached
		// same-minute activation command cannot strand the heartbeat.
		if worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover}); worked || err != nil {
			t.Fatal("immediate automatic rerun")
		}
	}
}

func TestMinutePollPauseReenableUsesNewActivationGeneration(t *testing.T) {
	repo := store.NewMemoryStore()
	s, fixture, _ := newFixtureService(t, repo)
	s.opts.ScanInterval = time.Minute
	browser := &idleDiscoveryBrowser{intakeFixture: fixture}
	s.browser = browser
	box, err := s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 2; round++ {
		if err := s.plan(t.Context()); err != nil {
			t.Fatal(err)
		}
		if worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover}); !worked || err != nil {
			t.Fatalf("activation %d did not start: %v", round, err)
		}
		if worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover}); worked || err != nil {
			t.Fatal("completed automatic round did not enter idle")
		}
		if round == 1 {
			break
		}
		before, _, err := repo.GetEmailMailbox(t.Context(), "email-owner", box.ID)
		if err != nil {
			t.Fatal(err)
		}
		paused, err := s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, false, box.Version)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.plan(t.Context()); err != nil {
			t.Fatal(err)
		}
		if worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover}); worked || err != nil {
			t.Fatal("paused mailbox started a round")
		}
		box, err = s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, true, paused.Version)
		if err != nil {
			t.Fatal(err)
		}
		after, _, err := repo.GetEmailMailbox(t.Context(), "email-owner", box.ID)
		if err != nil || after.BindingGeneration <= before.BindingGeneration {
			t.Fatalf("re-enable reused old activation generation: %v", err)
		}
	}
	if browser.scans.Load() != 2 {
		t.Fatalf("expected two isolated activation rounds, got %d", browser.scans.Load())
	}
}

func TestPauseCancelsActiveBrowserScanBeforeStartingAnotherLane(t *testing.T) {
	repo := store.NewMemoryStore()
	s, fixture, _ := newFixtureService(t, repo)
	browser := &idleDiscoveryBrowser{intakeFixture: fixture, started: make(chan struct{})}
	s.browser = browser
	box, err := s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.plan(t.Context()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.workOne(t.Context(), []string{app.EmailJobDiscover}); done <- err }()
	select {
	case <-browser.started:
	case <-time.After(3 * time.Second):
		t.Fatal("scan did not start")
	}
	if result, err := s.Sync(t.Context(), "email-owner", box.ID); err != nil || len(result.RefreshRequests) != 1 {
		t.Fatalf("pending refresh during automatic scan: %+v %v", result, err)
	}
	paused, err := s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, false, box.Version)
	if err != nil || paused.IntakeEnabled || paused.RefreshPending {
		t.Fatalf("pause: %v", err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("paused browser operation was not canceled")
	}
	if browser.scans.Load() != 1 {
		t.Fatal("pause started another browser lane")
	}
	if worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover}); err != nil || worked {
		t.Fatal("paused mailbox admitted another task")
	}
}

func TestTimelinePollingAndRefreshDoNotScheduleLegacySourceRecovery(t *testing.T) {
	repo := store.NewMemoryStore()
	s, _, _ := newFixtureService(t, repo)
	box, err := s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC()
	for i := 0; i < 3; i++ {
		at := started.Add(time.Duration(i) * s.opts.ScanInterval)
		s.now = func() time.Time { return at }
		if err := s.plan(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Sync(t.Context(), "email-owner", box.ID); err != nil {
			t.Fatal(err)
		}
		jobs, err := repo.ListEmailJobs(t.Context(), store.EmailQuery{OwnerID: "email-owner"})
		if err != nil {
			t.Fatal(err)
		}
		if len(emailJobsOfKind(jobs, app.EmailJobSourceRecovery)) != 0 {
			t.Fatal("ordinary polling or refresh created historical source recovery work")
		}
	}
}
