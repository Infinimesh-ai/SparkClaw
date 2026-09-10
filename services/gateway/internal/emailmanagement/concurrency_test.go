package emailmanagement

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type browserCall struct {
	provider, operation, message string
}

type concurrentIntakeBrowser struct {
	*intakeFixture
	mu      sync.Mutex
	active  map[string]int
	overlap bool
	started chan browserCall
	gates   map[browserCall]chan struct{}
}

func (b *concurrentIntakeBrowser) AdmitIntake(_ context.Context, _, provider string) (app.EmailAdmissionBinding, error) {
	return app.EmailAdmissionBinding{Provider: provider, Account: app.EmailAccountDefault}, nil
}

func (b *concurrentIntakeBrowser) block(ctx context.Context, call browserCall) error {
	b.mu.Lock()
	b.active[call.provider]++
	b.overlap = b.overlap || b.active[call.provider] > 1
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		b.active[call.provider]--
		b.mu.Unlock()
	}()
	b.started <- call
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.gates[call]:
		return nil
	}
}

func (b *concurrentIntakeBrowser) DiscoverForOwner(ctx context.Context, _ string, request app.EmailReadRequest) (app.EmailDiscoveryResult, error) {
	result := app.EmailDiscoveryResult{Provider: request.Provider, AccountAddress: request.Provider + "@example.com", ObservedAt: time.Now().UTC()}
	if request.Discovery == nil {
		return result, nil
	}
	result.Coverage = app.EmailDiscoveryCoverage{Lane: request.Discovery.Lane, ScanComplete: true, BoundaryQualified: request.Discovery.Lane == "recent_inbound"}
	if request.Discovery.Lane != "recent_inbound" {
		return result, nil
	}
	if err := b.block(ctx, browserCall{provider: request.Provider, operation: app.EmailJobDiscover}); err != nil {
		return result, err
	}
	for _, id := range []string{"message-1", "message-2"} {
		result.Candidates = append(result.Candidates, app.EmailCaptureTarget{AccountAddress: result.AccountAddress, ProviderMessageID: id, ProviderSelectionID: id, Folder: "inbox"})
	}
	return result, nil
}

func (b *concurrentIntakeBrowser) CaptureForOwner(ctx context.Context, _ string, request app.EmailReadRequest) (app.EmailReadResult, error) {
	err := b.block(ctx, browserCall{provider: request.Provider, operation: app.EmailJobCapture, message: request.Target.ProviderMessageID})
	if err == nil {
		// Exercise queue progress after a failed download without manufacturing
		// source artifacts or counting this scheduler fixture as provider proof.
		err = errors.New("fixture download interrupted")
	}
	return app.EmailReadResult{}, err
}

func (b *concurrentIntakeBrowser) next(t *testing.T) browserCall {
	t.Helper()
	select {
	case call := <-b.started:
		return call
	case <-time.After(4 * time.Second):
		t.Fatal("an independent mailbox was blocked behind another browser job")
		return browserCall{}
	}
}

func TestThreeMailboxesReceiveConcurrentlyAndEachCapturesSerially(t *testing.T) {
	for _, backend := range []string{"memory", "file"} {
		t.Run(backend, func(t *testing.T) {
			var repo Repository = store.NewMemoryStore()
			if backend == "file" {
				file, err := store.NewFileStore(filepath.Join(t.TempDir(), "state.json"))
				if err != nil {
					t.Fatal(err)
				}
				repo = file
			}
			s, fixture, _ := newFixtureService(t, repo)
			s.opts.ScanInterval = time.Minute
			s.opts.JobTimeout = 20 * time.Second
			browser := &concurrentIntakeBrowser{intakeFixture: fixture, active: map[string]int{}, started: make(chan browserCall, 32), gates: map[browserCall]chan struct{}{}}
			s.browser = browser
			boxes := map[string]app.EmailMailbox{}
			for _, provider := range s.registry.List() {
				browser.gates[browserCall{provider: provider.ID, operation: app.EmailJobDiscover}] = make(chan struct{})
				for _, id := range []string{"message-1", "message-2"} {
					browser.gates[browserCall{provider: provider.ID, operation: app.EmailJobCapture, message: id}] = make(chan struct{})
				}
				box, err := s.Configure(t.Context(), "email-owner", provider.ID, true, 0)
				if err != nil {
					t.Fatal(err)
				}
				boxes[provider.ID] = box
			}
			s.Start(t.Context())
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if err := s.Close(ctx); err != nil {
					t.Error(err)
				}
			})
			seen := map[string]bool{}
			for range 3 {
				call := browser.next(t)
				if call.operation != app.EmailJobDiscover || seen[call.provider] {
					t.Fatalf("expected three independent discoveries: %+v", call)
				}
				seen[call.provider] = true
			}
			// Gmail stays blocked while QQ and Outlook advance into downloads.
			for _, provider := range []string{app.EmailProviderQQMail, app.EmailProviderOutlook} {
				close(browser.gates[browserCall{provider: provider, operation: app.EmailJobDiscover}])
			}
			captures := map[string]browserCall{}
			for range 2 {
				call := browser.next(t)
				if call.operation != app.EmailJobCapture || call.provider == app.EmailProviderGmail || captures[call.provider].provider != "" {
					t.Fatalf("mailbox captures did not overlap independently: %+v", call)
				}
				captures[call.provider] = call
			}
			// Pausing one mailbox must not cancel either other mailbox's work.
			box := boxes[app.EmailProviderGmail]
			if _, err := s.Configure(t.Context(), "email-owner", box.Provider, false, box.Version); err != nil {
				t.Fatal(err)
			}
			firstQQ := captures[app.EmailProviderQQMail]
			close(browser.gates[firstQQ])
			nextQQ := browser.next(t)
			if nextQQ.provider != firstQQ.provider || nextQQ.operation != app.EmailJobCapture || nextQQ.message == firstQQ.message {
				t.Fatalf("failed download did not yield to the next mail: %+v", nextQQ)
			}
			browser.mu.Lock()
			defer browser.mu.Unlock()
			if browser.overlap || browser.active[app.EmailProviderOutlook] != 1 || browser.active[app.EmailProviderQQMail] != 1 {
				t.Fatalf("mailbox isolation lost: active=%v overlap=%v", browser.active, browser.overlap)
			}
		})
	}
}
