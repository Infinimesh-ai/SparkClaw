package toolhub

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type managedBrowserAdapter struct {
	mu          sync.Mutex
	calls       []string
	callArgs    []map[string]any
	releaseArgs map[string]any
	releases    []map[string]any
	releaseErrs []error
	events      []string
	closeCalls  int
}

func (a *managedBrowserAdapter) Health(context.Context, map[string]any) (browserautomation.Result, error) {
	return browserautomation.Result{Output: map[string]any{"ok": true}}, nil
}

func (a *managedBrowserAdapter) Call(_ context.Context, tool string, args map[string]any) (browserautomation.Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, tool)
	a.callArgs = append(a.callArgs, cloneStringAnyMap(args))
	if tool == "browser.list_tabs" {
		return browserautomation.Result{Pages: []any{map[string]any{"page_id": "page_3", "selected": true}}}, nil
	}
	return browserautomation.Result{}, nil
}

func (*managedBrowserAdapter) ReadPage(context.Context, string, map[string]any) (browserautomation.PageReadResult, error) {
	return browserautomation.PageReadResult{}, nil
}
func (a *managedBrowserAdapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closeCalls++
	a.events = append(a.events, "close")
	return nil
}
func (a *managedBrowserAdapter) ReleaseSession(args map[string]any) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.releaseArgs = cloneStringAnyMap(args)
	a.releases = append(a.releases, cloneStringAnyMap(args))
	a.events = append(a.events, "release:"+stringValue(args["browser_profile_id"]))
	if len(a.releaseErrs) > 0 {
		err := a.releaseErrs[0]
		a.releaseErrs = a.releaseErrs[1:]
		return err
	}
	return nil
}

func TestManagedBrowserWindowReusesAndReleasesDedicatedVisibleProfile(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.BrowserAutomation.Enabled = true
	adapter := &managedBrowserAdapter{}
	hub := New(cfg, store.NewMemoryStore()).WithBrowserAutomationAdapter(adapter)

	if err := hub.OpenManagedBrowserWindow(t.Context(), "owner-a", "bind-a", "https://liteapp.weixin.qq.com/q/example", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if len(adapter.calls) != 1 || adapter.calls[0] != "browser.open" {
		t.Fatalf("managed window did not open its first target directly: %#v", adapter.calls)
	}
	if got := adapter.callArgs[0]["browser_profile_id"]; got != "managed-bind-a" {
		t.Fatalf("managed window did not use its dedicated profile: %#v", adapter.callArgs[0])
	}
	if err := hub.OpenManagedBrowserWindow(t.Context(), "owner-a", "bind-a", "https://liteapp.weixin.qq.com/q/replacement", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if len(adapter.calls) != 3 || adapter.calls[1] != "browser.list_tabs" || adapter.calls[2] != "browser.navigate" {
		t.Fatalf("managed window did not reuse its selected tab: %#v", adapter.calls)
	}
	if got := adapter.callArgs[2]["page_id"]; got != "page_3" {
		t.Fatalf("managed window navigated an unexpected page: %#v", adapter.callArgs[2])
	}
	if err := hub.CloseManagedBrowserWindow(t.Context(), "owner-a", "bind-a"); err != nil {
		t.Fatal(err)
	}
	if adapter.releaseArgs == nil || adapter.releaseArgs["presentation"] != "visible" {
		t.Fatalf("managed window did not release the visible session: %#v", adapter.releaseArgs)
	}
	adapter.releaseArgs = nil
	if err := hub.CloseManagedBrowserWindow(t.Context(), "owner-a", "bind-a"); err != nil {
		t.Fatal(err)
	}
	if adapter.releaseArgs != nil {
		t.Fatal("idempotent close tried to release the session twice")
	}
}

func cloneStringAnyMap(input map[string]any) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

type unhealthyManagedBrowserAdapter struct {
	managedBrowserAdapter
	output any
}

func (a *unhealthyManagedBrowserAdapter) Health(context.Context, map[string]any) (browserautomation.Result, error) {
	return browserautomation.Result{Output: a.output}, nil
}

func TestManagedBrowserWindowFailsClosedOnUnhealthyOrMalformedHealth(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.BrowserAutomation.Enabled = true
	for name, output := range map[string]any{
		"nil payload":     nil,
		"non-map payload": "ok",
		"string ok":       map[string]any{"ok": "false", "error": "broken"},
		"numeric ok":      map[string]any{"ok": 0},
		"missing ok":      map[string]any{"status": "starting"},
		"explicit false":  map[string]any{"ok": false, "error": "no display"},
	} {
		adapter := &unhealthyManagedBrowserAdapter{output: output}
		hub := New(cfg, store.NewMemoryStore()).WithBrowserAutomationAdapter(adapter)
		if err := hub.OpenManagedBrowserWindow(t.Context(), "owner-a", "bind-a", "https://liteapp.weixin.qq.com/q/example", time.Time{}); err == nil {
			t.Errorf("%s: expected an error, window was opened against an unverified Chromium", name)
		}
		if len(adapter.calls) != 0 {
			t.Errorf("%s: browser calls were attempted despite failed health gate: %v", name, adapter.calls)
		}
	}
}

func TestManagedBrowserWindowLeaseRenewsAndExpires(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.BrowserAutomation.Enabled = true
	adapter := &managedBrowserAdapter{}
	hub := New(cfg, store.NewMemoryStore()).WithBrowserAutomationAdapter(adapter)
	t.Cleanup(func() { _ = hub.Close() })
	now := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	hub.managedBrowserWindows.now = func() time.Time { return now }
	hub.managedBrowserWindows.sweepInterval = time.Hour

	if err := hub.OpenManagedBrowserWindow(t.Context(), "owner-a", "bind-a", "https://liteapp.weixin.qq.com/q/first", time.Time{}); err != nil {
		t.Fatal(err)
	}
	firstDeadline, firstGeneration, ok := managedBrowserLeaseState(hub, "owner-a\x00bind-a")
	if !ok || !firstDeadline.Equal(now.Add(managedBrowserWindowTTL)) || firstGeneration != 1 {
		t.Fatalf("first lease = %v generation=%d tracked=%v", firstDeadline, firstGeneration, ok)
	}

	now = now.Add(5 * time.Minute)
	if err := hub.OpenManagedBrowserWindow(t.Context(), "owner-a", "bind-a", "https://liteapp.weixin.qq.com/q/renewed", time.Time{}); err != nil {
		t.Fatal(err)
	}
	renewedDeadline, renewedGeneration, ok := managedBrowserLeaseState(hub, "owner-a\x00bind-a")
	if !ok || !renewedDeadline.Equal(now.Add(managedBrowserWindowTTL)) || renewedGeneration != 2 {
		t.Fatalf("renewed lease = %v generation=%d tracked=%v", renewedDeadline, renewedGeneration, ok)
	}
	if err := hub.sweepManagedBrowserWindows(firstDeadline); err != nil {
		t.Fatal(err)
	}
	if got := adapterReleaseCount(adapter); got != 0 {
		t.Fatalf("old deadline released renewed lease %d times", got)
	}
	if err := hub.sweepManagedBrowserWindows(renewedDeadline); err != nil {
		t.Fatal(err)
	}
	if got := adapterReleaseCount(adapter); got != 1 {
		t.Fatalf("renewed deadline release count = %d, want 1", got)
	}
	if _, _, tracked := managedBrowserLeaseState(hub, "owner-a\x00bind-a"); tracked {
		t.Fatal("expired lease remained tracked after successful release")
	}
}

func TestManagedBrowserWindowLeaseUsesBindingExpiryAndRejectsExpiredBinding(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.BrowserAutomation.Enabled = true
	adapter := &managedBrowserAdapter{}
	hub := New(cfg, store.NewMemoryStore()).WithBrowserAutomationAdapter(adapter)
	t.Cleanup(func() { _ = hub.Close() })
	now := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	hub.managedBrowserWindows.now = func() time.Time { return now }
	hub.managedBrowserWindows.sweepInterval = time.Hour
	expiresAt := now.Add(2 * time.Minute)

	if err := hub.OpenManagedBrowserWindow(t.Context(), "owner-a", "bind-a", "https://liteapp.weixin.qq.com/q/example", expiresAt); err != nil {
		t.Fatal(err)
	}
	deadline, _, ok := managedBrowserLeaseState(hub, "owner-a\x00bind-a")
	if !ok || !deadline.Equal(expiresAt) {
		t.Fatalf("binding-capped deadline = %v tracked=%v, want %v", deadline, ok, expiresAt)
	}
	if err := hub.CloseManagedBrowserWindow(t.Context(), "owner-a", "bind-a"); err != nil {
		t.Fatal(err)
	}

	now = expiresAt
	callsBefore := adapterCallCount(adapter)
	err := hub.OpenManagedBrowserWindow(t.Context(), "owner-a", "bind-expired", "https://liteapp.weixin.qq.com/q/expired", expiresAt)
	if !errors.Is(err, errManagedBrowserBindingExpired) {
		t.Fatalf("expired binding error = %v, want %v", err, errManagedBrowserBindingExpired)
	}
	if got := adapterCallCount(adapter); got != callsBefore {
		t.Fatalf("expired binding reached browser calls: before=%d after=%d", callsBefore, got)
	}

	t.Run("expiry during open remains tracked for cleanup", func(t *testing.T) {
		adapter := &managedBrowserAdapter{}
		hub := newManagedBrowserTestHub(adapter)
		t.Cleanup(func() { _ = hub.Close() })
		startedAt := time.Date(2026, 8, 17, 11, 0, 0, 0, time.UTC)
		expiresAt := startedAt.Add(time.Minute)
		clockCalls := 0
		hub.managedBrowserWindows.now = func() time.Time {
			clockCalls++
			if clockCalls == 1 {
				return startedAt
			}
			return expiresAt
		}
		err := hub.OpenManagedBrowserWindow(t.Context(), "owner-a", "bind-crossed", "https://liteapp.weixin.qq.com/q/crossed", expiresAt)
		if !errors.Is(err, errManagedBrowserBindingExpired) {
			t.Fatalf("expiry during open error = %v, want %v", err, errManagedBrowserBindingExpired)
		}
		deadline, _, tracked := managedBrowserLeaseState(hub, "owner-a\x00bind-crossed")
		if !tracked || !deadline.Equal(expiresAt) {
			t.Fatalf("expired in-flight lease = %v tracked=%v, want tracked deadline %v", deadline, tracked, expiresAt)
		}
		if err := hub.sweepManagedBrowserWindows(expiresAt); err != nil {
			t.Fatal(err)
		}
		if got := adapterReleaseCount(adapter); got != 1 {
			t.Fatalf("expired in-flight release count = %d, want 1", got)
		}
	})
}

func TestManagedBrowserWindowStaleSweepDoesNotReleaseRenewedGeneration(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.BrowserAutomation.Enabled = true
	adapter := &managedBrowserAdapter{}
	hub := New(cfg, store.NewMemoryStore()).WithBrowserAutomationAdapter(adapter)
	t.Cleanup(func() { _ = hub.Close() })
	now := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	hub.managedBrowserWindows.now = func() time.Time { return now }
	hub.managedBrowserWindows.sweepInterval = time.Hour

	if err := hub.OpenManagedBrowserWindow(t.Context(), "owner-a", "bind-a", "https://liteapp.weixin.qq.com/q/first", time.Time{}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(managedBrowserWindowTTL)
	leases := hub.managedBrowserWindows.pinExpired(now)
	if len(leases) != 1 {
		t.Fatalf("expired leases = %d, want 1", len(leases))
	}
	if err := hub.OpenManagedBrowserWindow(t.Context(), "owner-a", "bind-a", "https://liteapp.weixin.qq.com/q/renewed", time.Time{}); err != nil {
		t.Fatal(err)
	}
	lease := leases[0]
	lease.entry.operationMu.Lock()
	err := hub.releaseManagedBrowserWindowEntryLocked(lease.entry, lease.generation, now)
	lease.entry.operationMu.Unlock()
	hub.managedBrowserWindows.unpin(lease.entry)
	if err != nil {
		t.Fatal(err)
	}
	if got := adapterReleaseCount(adapter); got != 0 {
		t.Fatalf("stale sweep released renewed generation %d times", got)
	}
}

func TestManagedBrowserWindowReleaseFailureRemainsRetryable(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.BrowserAutomation.Enabled = true
	adapter := &managedBrowserAdapter{releaseErrs: []error{errors.New("close failed")}}
	hub := New(cfg, store.NewMemoryStore()).WithBrowserAutomationAdapter(adapter)
	t.Cleanup(func() { _ = hub.Close() })
	now := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	hub.managedBrowserWindows.now = func() time.Time { return now }
	hub.managedBrowserWindows.sweepInterval = time.Hour

	if err := hub.OpenManagedBrowserWindow(t.Context(), "owner-a", "bind-a", "https://liteapp.weixin.qq.com/q/example", time.Time{}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(managedBrowserWindowTTL)
	if err := hub.sweepManagedBrowserWindows(now); err == nil {
		t.Fatal("first failed release returned nil")
	}
	if _, _, tracked := managedBrowserLeaseState(hub, "owner-a\x00bind-a"); !tracked {
		t.Fatal("failed release removed the tracked lease")
	}
	if err := hub.sweepManagedBrowserWindows(now); err != nil {
		t.Fatal(err)
	}
	if got := adapterReleaseCount(adapter); got != 2 {
		t.Fatalf("release attempts = %d, want 2", got)
	}
	if _, _, tracked := managedBrowserLeaseState(hub, "owner-a\x00bind-a"); tracked {
		t.Fatal("successful retry left the lease tracked")
	}
}

type blockingManagedBrowserAdapter struct {
	mu       sync.Mutex
	entered  chan string
	unblock  chan struct{}
	releases []string
	events   []string
}

func newBlockingManagedBrowserAdapter() *blockingManagedBrowserAdapter {
	return &blockingManagedBrowserAdapter{entered: make(chan string, 8), unblock: make(chan struct{})}
}

func (a *blockingManagedBrowserAdapter) Health(ctx context.Context, args map[string]any) (browserautomation.Result, error) {
	select {
	case a.entered <- stringValue(args["owner_id"]):
	case <-ctx.Done():
		return browserautomation.Result{}, ctx.Err()
	}
	select {
	case <-a.unblock:
		return browserautomation.Result{Output: map[string]any{"ok": true}}, nil
	case <-ctx.Done():
		return browserautomation.Result{}, ctx.Err()
	}
}

func (*blockingManagedBrowserAdapter) Call(context.Context, string, map[string]any) (browserautomation.Result, error) {
	return browserautomation.Result{}, nil
}
func (*blockingManagedBrowserAdapter) ReadPage(context.Context, string, map[string]any) (browserautomation.PageReadResult, error) {
	return browserautomation.PageReadResult{}, nil
}
func (a *blockingManagedBrowserAdapter) ReleaseSession(args map[string]any) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	profile := stringValue(args["browser_profile_id"])
	a.releases = append(a.releases, profile)
	a.events = append(a.events, "release:"+profile)
	return nil
}
func (a *blockingManagedBrowserAdapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, "close")
	return nil
}

func TestManagedBrowserWindowLockingIsPerKey(t *testing.T) {
	t.Run("different keys proceed concurrently", func(t *testing.T) {
		adapter := newBlockingManagedBrowserAdapter()
		hub := newManagedBrowserTestHub(adapter)
		t.Cleanup(func() { _ = hub.Close() })
		errs := make(chan error, 2)
		go func() {
			errs <- hub.OpenManagedBrowserWindow(context.Background(), "owner-a", "bind-a", "https://liteapp.weixin.qq.com/q/a", time.Time{})
		}()
		go func() {
			errs <- hub.OpenManagedBrowserWindow(context.Background(), "owner-b", "bind-b", "https://liteapp.weixin.qq.com/q/b", time.Time{})
		}()
		waitManagedBrowserEntry(t, adapter.entered)
		waitManagedBrowserEntry(t, adapter.entered)
		close(adapter.unblock)
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	})

	t.Run("same key serializes", func(t *testing.T) {
		adapter := newBlockingManagedBrowserAdapter()
		hub := newManagedBrowserTestHub(adapter)
		t.Cleanup(func() { _ = hub.Close() })
		errs := make(chan error, 2)
		go func() {
			errs <- hub.OpenManagedBrowserWindow(context.Background(), "owner-a", "bind-a", "https://liteapp.weixin.qq.com/q/first", time.Time{})
		}()
		waitManagedBrowserEntry(t, adapter.entered)
		go func() {
			errs <- hub.OpenManagedBrowserWindow(context.Background(), "owner-a", "bind-a", "https://liteapp.weixin.qq.com/q/second", time.Time{})
		}()
		select {
		case owner := <-adapter.entered:
			t.Fatalf("same-key second call entered early for %s", owner)
		case <-time.After(100 * time.Millisecond):
		}
		close(adapter.unblock)
		waitManagedBrowserEntry(t, adapter.entered)
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	})
}

func TestManagedBrowserWindowShutdownDrainsBeforeAdapterClose(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.BrowserAutomation.Enabled = true
	adapter := &managedBrowserAdapter{}
	hub := New(cfg, store.NewMemoryStore()).WithBrowserAutomationAdapter(adapter)
	hub.managedBrowserWindows.sweepInterval = time.Hour
	for _, bindingID := range []string{"bind-a", "bind-b"} {
		if err := hub.OpenManagedBrowserWindow(t.Context(), "owner-a", bindingID, "https://liteapp.weixin.qq.com/q/"+bindingID, time.Time{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := hub.Close(); err != nil {
		t.Fatal(err)
	}
	adapter.mu.Lock()
	events := append([]string(nil), adapter.events...)
	adapter.mu.Unlock()
	if len(events) != 3 || events[2] != "close" {
		t.Fatalf("shutdown events = %#v, want two releases then close", events)
	}
	if err := hub.Close(); err != nil {
		t.Fatal(err)
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if len(adapter.events) != len(events) || adapter.closeCalls != 1 {
		t.Fatalf("repeated shutdown was not idempotent: %#v close_calls=%d", adapter.events, adapter.closeCalls)
	}
	if err := hub.OpenManagedBrowserWindow(t.Context(), "owner-a", "bind-c", "https://liteapp.weixin.qq.com/q/c", time.Time{}); !errors.Is(err, errManagedBrowserWindowRegistryClosed) {
		t.Fatalf("open after shutdown error = %v, want registry closed", err)
	}
}

func TestManagedBrowserWindowShutdownWaitsForInflightFirstOpen(t *testing.T) {
	adapter := newBlockingManagedBrowserAdapter()
	hub := newManagedBrowserTestHub(adapter)
	openErr := make(chan error, 1)
	go func() {
		openErr <- hub.OpenManagedBrowserWindow(context.Background(), "owner-a", "bind-a", "https://liteapp.weixin.qq.com/q/a", time.Time{})
	}()
	waitManagedBrowserEntry(t, adapter.entered)
	closeErr := make(chan error, 1)
	go func() { closeErr <- hub.Close() }()
	select {
	case err := <-closeErr:
		t.Fatalf("shutdown returned before in-flight open completed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(adapter.unblock)
	if err := <-openErr; !errors.Is(err, errManagedBrowserWindowRegistryClosed) {
		t.Fatalf("in-flight open error = %v, want registry closed", err)
	}
	if err := <-closeErr; err != nil {
		t.Fatal(err)
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if len(adapter.events) != 2 || adapter.events[0] != "release:managed-bind-a" || adapter.events[1] != "close" {
		t.Fatalf("in-flight shutdown events = %#v", adapter.events)
	}
}

func newManagedBrowserTestHub(adapter browserautomation.Adapter) *ToolHub {
	cfg := config.Default()
	cfg.Tools.BrowserAutomation.Enabled = true
	hub := New(cfg, store.NewMemoryStore()).WithBrowserAutomationAdapter(adapter)
	hub.managedBrowserWindows.sweepInterval = time.Hour
	return hub
}

func waitManagedBrowserEntry(t *testing.T, entered <-chan string) string {
	t.Helper()
	select {
	case owner := <-entered:
		return owner
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for managed browser operation")
		return ""
	}
}

func managedBrowserLeaseState(hub *ToolHub, key string) (time.Time, uint64, bool) {
	registry := hub.managedBrowserWindows
	registry.mu.Lock()
	defer registry.mu.Unlock()
	entry := registry.entries[key]
	if entry == nil || !entry.tracked {
		return time.Time{}, 0, false
	}
	return entry.deadline, entry.generation, true
}

func adapterCallCount(adapter *managedBrowserAdapter) int {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	return len(adapter.calls)
}

func adapterReleaseCount(adapter *managedBrowserAdapter) int {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	return len(adapter.releases)
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
