package emailautomation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type providerBlockingRunner struct {
	ScriptRunner
	entered chan string
	release chan struct{}
}

func (r *providerBlockingRunner) wait(ctx context.Context, provider Provider) error {
	r.entered <- provider.ID
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.release:
		return nil
	}
}
func (r *providerBlockingRunner) Probe(ctx context.Context, provider Provider, _ string, _ uint64) (ProbeResult, error) {
	err := r.wait(ctx, provider)
	return ProbeResult{Provider: provider.ID, AccountHint: "a***@example.test", Generation: 1, Revision: provider.Probe.Revision, CheckedAt: time.Now().UTC()}, err
}
func (r *providerBlockingRunner) credentialGeneration(context.Context, uint64) (int64, error) {
	return 1, nil
}
func (r *providerBlockingRunner) Read(ctx context.Context, provider Provider, _ ReadRequest) (ReadResult, error) {
	return ReadResult{Status: "empty"}, r.wait(ctx, provider)
}

func providerConcurrencyFixture(t *testing.T) (*Controller, *providerBlockingRunner) {
	t.Helper()
	st := store.NewMemoryStore()
	checkedAt := time.Now().UTC()
	for _, owner := range []string{"owner", "other"} {
		for _, provider := range DefaultRegistry().List() {
			if _, err := st.UpdateEmailProviderSetting(t.Context(), app.EmailProviderSetting{OwnerID: owner, Provider: provider.ID, Account: app.EmailAccountDefault, Enabled: true, State: app.EmailStateReady, LastCheckedAt: &checkedAt}, 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	runner := &providerBlockingRunner{entered: make(chan string, 16), release: make(chan struct{})}
	return NewController(st, DefaultRegistry(), nil, runner), runner
}
func awaitProvider(t *testing.T, entered <-chan string) string {
	t.Helper()
	select {
	case provider := <-entered:
		return provider
	case <-time.After(time.Second):
		t.Fatal("provider blocked behind another provider")
		return ""
	}
}
func awaitOperation(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		t.Fatal("operation did not finish")
		return nil
	}
}

func TestProviderAdmissionsOverlapAndKeepSeparateProofs(t *testing.T) {
	controller, runner := providerConcurrencyFixture(t)
	done := make(chan error, 3)
	for _, provider := range DefaultRegistry().List() {
		go func() { _, err := controller.AdmitIntake(t.Context(), "owner", provider.ID); done <- err }()
	}
	seen := map[string]bool{}
	for range 3 {
		seen[awaitProvider(t, runner.entered)] = true
	}
	if len(seen) != 3 {
		t.Fatalf("expected three concurrent providers, got %v", seen)
	}
	close(runner.release)
	for range 3 {
		if err := awaitOperation(t, done); err != nil {
			t.Fatal(err)
		}
	}
	// All providers now hit their independent proofs without launching a browser.
	for _, provider := range DefaultRegistry().List() {
		if _, err := controller.AdmitIntake(t.Context(), "owner", provider.ID); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case provider := <-runner.entered:
		t.Fatalf("extra probe for %s", provider)
	default:
	}
}

func TestSameProviderSerializesAcrossOwners(t *testing.T) {
	controller, runner := providerConcurrencyFixture(t)
	request := ReadRequest{Provider: "gmail", Account: app.EmailAccountDefault, SettingVersion: 1}
	done := make(chan error, 2)
	go func() { _, err := controller.ReadForOwner(t.Context(), "owner", request); done <- err }()
	awaitProvider(t, runner.entered)
	go func() { _, err := controller.ReadForOwner(t.Context(), "other", request); done <- err }()
	select {
	case <-runner.entered:
		t.Fatal("shared provider browser account overlapped across owners")
	case <-time.After(30 * time.Millisecond):
	}
	close(runner.release)
	awaitProvider(t, runner.entered)
	for range 2 {
		if err := awaitOperation(t, done); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCanceledProviderWaitersNeverReachBrowser(t *testing.T) {
	controller, runner := providerConcurrencyFixture(t)
	request := ReadRequest{Provider: "gmail", Account: app.EmailAccountDefault, SettingVersion: 1}
	active := make(chan error, 1)
	go func() { _, err := controller.ReadForOwner(t.Context(), "owner", request); active <- err }()
	awaitProvider(t, runner.entered)
	target := app.EmailCaptureTarget{AccountAddress: "a@example.test", ProviderMessageID: "message", ProviderSelectionID: "selection"}
	operations := map[string]func(context.Context) error{
		"login": func(ctx context.Context) error {
			_, err := controller.OpenLoginBrowser(ctx, "other", "other", "gmail")
			return err
		},
		"check": func(ctx context.Context) error {
			_, err := controller.Check(ctx, "other", "other", "gmail")
			return err
		},
		"admit-send":   func(ctx context.Context) error { _, err := controller.Admit(ctx, "other", "gmail"); return err },
		"admit-intake": func(ctx context.Context) error { _, err := controller.AdmitIntake(ctx, "other", "gmail"); return err },
		"send": func(ctx context.Context) error {
			_, err := controller.SendForOwner(ctx, "other", SendRequest{Provider: "gmail", Account: app.EmailAccountDefault, SettingVersion: 1})
			return err
		},
		"read": func(ctx context.Context) error { _, err := controller.ReadForOwner(ctx, "other", request); return err },
		"capture": func(ctx context.Context) error {
			capture := request
			capture.Target = &target
			_, err := controller.CaptureForOwner(ctx, "other", capture)
			return err
		},
		"discover": func(ctx context.Context) error {
			_, err := controller.DiscoverForOwner(ctx, "other", request)
			return err
		},
		"thread": func(ctx context.Context) error {
			_, err := controller.EnumerateThreadForOwner(ctx, "other", app.EmailThreadRequest{Binding: request})
			return err
		},
		"collect-page": func(ctx context.Context) error {
			binding := request
			start := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
			binding.Discovery = &app.EmailDiscoveryOptions{Lane: "recent_inbound", AccountAddress: "owner@example.test", IntervalStart: start, IntervalEnd: start.Add(time.Hour), Limit: 50, ProviderMode: app.EmailProviderModeTimeRange}
			_, err := controller.CollectPageForOwner(ctx, "other", binding)
			return err
		},
		"mark-read": func(ctx context.Context) error {
			_, err := controller.MarkReadForOwner(ctx, "other", app.EmailMarkReadRequest{Binding: request})
			return err
		},
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- operation(ctx) }()
			if err := awaitOperation(t, done); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expected canceled wait, got %v", err)
			}
		})
	}
	close(runner.release)
	if err := awaitOperation(t, active); err != nil {
		t.Fatal(err)
	}
	select {
	case provider := <-runner.entered:
		t.Fatalf("canceled waiter entered %s", provider)
	default:
	}
}
