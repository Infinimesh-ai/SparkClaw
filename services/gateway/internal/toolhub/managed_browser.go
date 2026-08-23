package toolhub

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserautomation"
)

const (
	managedBrowserWindowTTL           = 10 * time.Minute
	managedBrowserWindowSweepInterval = 30 * time.Second
)

var (
	errManagedBrowserWindowRegistryClosed = errors.New("managed browser window registry is closed")
	errManagedBrowserBindingExpired       = errors.New("managed browser binding has expired")
)

type managedBrowserWindowEntry struct {
	operationMu sync.Mutex
	key         string
	ownerID     string
	windowID    string
	args        map[string]any

	// The registry mutex guards the remaining fields.
	refs       int
	tracked    bool
	deadline   time.Time
	generation uint64
}

type managedBrowserWindowRegistry struct {
	mu            sync.Mutex
	entries       map[string]*managedBrowserWindowEntry
	closed        bool
	janitorCancel context.CancelFunc
	janitorDone   chan struct{}
	now           func() time.Time
	sweepInterval time.Duration
}

type managedBrowserWindowLease struct {
	entry      *managedBrowserWindowEntry
	generation uint64
}

func newManagedBrowserWindowRegistry() *managedBrowserWindowRegistry {
	return &managedBrowserWindowRegistry{
		entries:       map[string]*managedBrowserWindowEntry{},
		now:           func() time.Time { return time.Now().UTC() },
		sweepInterval: managedBrowserWindowSweepInterval,
	}
}

func (h *ToolHub) OpenManagedBrowserWindow(ctx context.Context, ownerID, windowID, targetURL string, bindingExpiresAt time.Time) error {
	if h == nil || h.browser == nil || !h.cfg.Tools.BrowserAutomation.Enabled {
		return browserautomation.ErrDisabled
	}
	// Fail closed at open time: an adapter that cannot release sessions
	// would otherwise open a visible window nothing can ever close, and the
	// gap would only surface at shutdown as an orphaned browser.
	if _, ok := h.browser.(browserautomation.SessionReleaser); !ok {
		return errors.New("browser adapter cannot release a managed session")
	}
	args, key, err := managedBrowserWindowArgs(ownerID, windowID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(targetURL) == "" {
		return errors.New("managed browser target URL is required")
	}
	registry := h.managedBrowserWindows
	if registry == nil {
		return errors.New("managed browser registry is unavailable")
	}
	if _, err := managedBrowserWindowDeadline(registry.now(), bindingExpiresAt); err != nil {
		return err
	}
	entry, err := registry.pinForOpen(key, ownerID, windowID, args)
	if err != nil {
		return err
	}
	defer registry.unpin(entry)

	args["url"] = targetURL
	args["require_visible_environment"] = true
	entry.operationMu.Lock()
	defer entry.operationMu.Unlock()

	health, err := h.browser.Health(ctx, args)
	if err != nil {
		return err
	}
	// Fail closed: an unexpected payload shape or a non-bool "ok" must not be
	// treated as healthy, or a broken Chromium gets browser.open attempts.
	output, outputOK := health.Output.(map[string]any)
	if !outputOK {
		return fmt.Errorf("visible Chromium health payload has unexpected shape %T", health.Output)
	}
	if healthy, isBool := output["ok"].(bool); !isBool || !healthy {
		return fmt.Errorf("visible Chromium is unavailable: %s", firstString(output, "error", "reason", "status"))
	}
	if !registry.isTracked(entry) {
		_, err = h.browser.Call(ctx, "browser.open", args)
	} else {
		var tabs browserautomation.Result
		tabs, err = h.browser.Call(ctx, "browser.list_tabs", args)
		if err == nil {
			if pageID := selectedManagedBrowserPage(tabs.Pages); pageID != "" {
				args["page_id"] = pageID
				_, err = h.browser.Call(ctx, "browser.navigate", args)
			} else {
				_, err = h.browser.Call(ctx, "browser.open", args)
			}
		}
	}
	if err != nil {
		return err
	}
	deadline, expiryErr := managedBrowserWindowDeadline(registry.now(), bindingExpiresAt)
	if expiryErr != nil {
		// The binding expired during the browser round trip. Publish an already
		// expired lease so the janitor owns cleanup and can retry a failed release.
		deadline = bindingExpiresAt.UTC()
	}
	if !registry.renew(entry, deadline) {
		releaseErr := h.forceReleaseManagedBrowserWindowEntryLocked(entry)
		return errors.Join(errManagedBrowserWindowRegistryClosed, releaseErr)
	}
	h.ensureManagedBrowserWindowJanitor()
	return expiryErr
}

func (h *ToolHub) CloseManagedBrowserWindow(ctx context.Context, ownerID, windowID string) error {
	_ = ctx
	if h == nil || h.browser == nil {
		return nil
	}
	_, key, err := managedBrowserWindowArgs(ownerID, windowID)
	if err != nil {
		return err
	}
	registry := h.managedBrowserWindows
	if registry == nil {
		return nil
	}
	entry := registry.pinExisting(key)
	if entry == nil {
		return nil
	}
	defer registry.unpin(entry)
	entry.operationMu.Lock()
	defer entry.operationMu.Unlock()
	if registry.isClosed() {
		return nil
	}
	return h.releaseManagedBrowserWindowEntryLocked(entry, 0, time.Time{})
}

func managedBrowserWindowDeadline(now, bindingExpiresAt time.Time) (time.Time, error) {
	now = now.UTC()
	if !bindingExpiresAt.IsZero() {
		bindingExpiresAt = bindingExpiresAt.UTC()
		if !bindingExpiresAt.After(now) {
			return time.Time{}, errManagedBrowserBindingExpired
		}
	}
	deadline := now.Add(managedBrowserWindowTTL)
	if !bindingExpiresAt.IsZero() && bindingExpiresAt.Before(deadline) {
		deadline = bindingExpiresAt
	}
	return deadline, nil
}

func (r *managedBrowserWindowRegistry) pinForOpen(key, ownerID, windowID string, args map[string]any) (*managedBrowserWindowEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, errManagedBrowserWindowRegistryClosed
	}
	entry := r.entries[key]
	if entry == nil {
		entry = &managedBrowserWindowEntry{
			key:      key,
			ownerID:  ownerID,
			windowID: windowID,
			args:     cloneManagedBrowserWindowArgs(args),
		}
		r.entries[key] = entry
	}
	entry.refs++
	return entry, nil
}

func (r *managedBrowserWindowRegistry) pinExisting(key string) *managedBrowserWindowEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.entries[key]
	if entry != nil {
		entry.refs++
	}
	return entry
}

func (r *managedBrowserWindowRegistry) unpin(entry *managedBrowserWindowEntry) {
	if entry == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry.refs--
	if entry.refs == 0 && !entry.tracked && r.entries[entry.key] == entry {
		delete(r.entries, entry.key)
	}
}

func (r *managedBrowserWindowRegistry) isTracked(entry *managedBrowserWindowEntry) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.entries[entry.key] == entry && entry.tracked
}

func (r *managedBrowserWindowRegistry) isClosed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

func (r *managedBrowserWindowRegistry) renew(entry *managedBrowserWindowEntry, deadline time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.entries[entry.key] != entry {
		return false
	}
	entry.tracked = true
	entry.deadline = deadline.UTC()
	entry.generation++
	return true
}

func (r *managedBrowserWindowRegistry) releaseEligible(entry *managedBrowserWindowEntry, generation uint64, expiredAt time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries[entry.key] != entry || !entry.tracked {
		return false
	}
	if generation != 0 && entry.generation != generation {
		return false
	}
	return expiredAt.IsZero() || !entry.deadline.After(expiredAt)
}

func (r *managedBrowserWindowRegistry) markReleased(entry *managedBrowserWindowEntry, generation uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries[entry.key] != entry || !entry.tracked {
		return
	}
	if generation != 0 && entry.generation != generation {
		return
	}
	entry.tracked = false
	entry.deadline = time.Time{}
}

func (r *managedBrowserWindowRegistry) pinExpired(now time.Time) []managedBrowserWindowLease {
	r.mu.Lock()
	defer r.mu.Unlock()
	leases := make([]managedBrowserWindowLease, 0)
	for _, entry := range r.entries {
		if !entry.tracked || entry.deadline.After(now) {
			continue
		}
		entry.refs++
		leases = append(leases, managedBrowserWindowLease{entry: entry, generation: entry.generation})
	}
	return leases
}

func (r *managedBrowserWindowRegistry) pinAllEntries() []managedBrowserWindowLease {
	r.mu.Lock()
	defer r.mu.Unlock()
	leases := make([]managedBrowserWindowLease, 0, len(r.entries))
	for _, entry := range r.entries {
		entry.refs++
		leases = append(leases, managedBrowserWindowLease{entry: entry, generation: entry.generation})
	}
	return leases
}

func (r *managedBrowserWindowRegistry) beginClose() (context.CancelFunc, <-chan struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	return r.janitorCancel, r.janitorDone
}

func (h *ToolHub) ensureManagedBrowserWindowJanitor() {
	registry := h.managedBrowserWindows
	registry.mu.Lock()
	if registry.closed || registry.janitorCancel != nil {
		registry.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	interval := registry.sweepInterval
	if interval <= 0 {
		interval = managedBrowserWindowSweepInterval
	}
	registry.janitorCancel = cancel
	registry.janitorDone = done
	registry.mu.Unlock()

	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				_ = h.sweepManagedBrowserWindows(now.UTC())
			}
		}
	}()
}

func (h *ToolHub) sweepManagedBrowserWindows(now time.Time) error {
	registry := h.managedBrowserWindows
	if registry == nil {
		return nil
	}
	var errs []error
	for _, lease := range registry.pinExpired(now.UTC()) {
		entry := lease.entry
		entry.operationMu.Lock()
		err := h.releaseManagedBrowserWindowEntryLocked(entry, lease.generation, now.UTC())
		entry.operationMu.Unlock()
		registry.unpin(entry)
		if err != nil {
			slog.Warn("failed to release expired managed browser window", "window_id", entry.windowID, "error", err)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (h *ToolHub) closeManagedBrowserWindows() error {
	registry := h.managedBrowserWindows
	if registry == nil {
		return nil
	}
	cancel, done := registry.beginClose()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	var errs []error
	// Pin every entry, including a first open that has not published its lease
	// yet. Taking each operation lock waits for those in-flight calls before the
	// browser adapter is closed.
	for _, lease := range registry.pinAllEntries() {
		entry := lease.entry
		entry.operationMu.Lock()
		err := h.releaseManagedBrowserWindowEntryLocked(entry, 0, time.Time{})
		entry.operationMu.Unlock()
		registry.unpin(entry)
		if err != nil {
			errs = append(errs, fmt.Errorf("release managed browser window %s: %w", entry.windowID, err))
		}
	}
	return errors.Join(errs...)
}

// releaseManagedBrowserWindowEntryLocked expects entry.operationMu to be held.
func (h *ToolHub) releaseManagedBrowserWindowEntryLocked(entry *managedBrowserWindowEntry, generation uint64, expiredAt time.Time) error {
	registry := h.managedBrowserWindows
	if registry == nil || !registry.releaseEligible(entry, generation, expiredAt) {
		return nil
	}
	releaser, ok := h.browser.(browserautomation.SessionReleaser)
	if !ok {
		return errors.New("browser adapter cannot release a managed session")
	}
	if err := releaser.ReleaseSession(entry.args); err != nil {
		return err
	}
	registry.markReleased(entry, generation)
	return nil
}

// forceReleaseManagedBrowserWindowEntryLocked is used only when shutdown won
// the race after a browser call started but before its first lease was
// published. The new session must still be closed even though it was never
// marked tracked.
func (h *ToolHub) forceReleaseManagedBrowserWindowEntryLocked(entry *managedBrowserWindowEntry) error {
	releaser, ok := h.browser.(browserautomation.SessionReleaser)
	if !ok {
		return errors.New("browser adapter cannot release a managed session")
	}
	if err := releaser.ReleaseSession(entry.args); err != nil {
		return err
	}
	h.managedBrowserWindows.markReleased(entry, 0)
	return nil
}

func managedBrowserWindowArgs(ownerID, windowID string) (map[string]any, string, error) {
	ownerID = strings.TrimSpace(ownerID)
	windowID = strings.TrimSpace(windowID)
	if ownerID == "" || windowID == "" {
		return nil, "", errors.New("managed browser owner and window id are required")
	}
	key := ownerID + "\x00" + windowID
	return map[string]any{
		"browser_mode":       "collaborative",
		"presentation":       "visible",
		"surface_visible":    true,
		"visible_browser":    true,
		"owner_id":           ownerID,
		"browser_profile_id": "managed-" + windowID,
	}, key, nil
}

func cloneManagedBrowserWindowArgs(input map[string]any) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func selectedManagedBrowserPage(pages []any) string {
	fallback := ""
	for _, raw := range pages {
		page, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		pageID := strings.TrimSpace(fmt.Sprint(page["page_id"]))
		if pageID == "" || pageID == "<nil>" {
			continue
		}
		if fallback == "" {
			fallback = pageID
		}
		if selected, _ := page["selected"].(bool); selected {
			return pageID
		}
	}
	return fallback
}
