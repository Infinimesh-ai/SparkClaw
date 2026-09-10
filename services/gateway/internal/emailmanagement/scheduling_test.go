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
	if err != nil || len(jobs) != 1 || jobs[0].State != app.EmailJobQueued || time.Until(jobs[0].NextAttemptAt) < 55*time.Second {
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
	paused, err := s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, false, box.Version)
	if err != nil || paused.IntakeEnabled {
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
