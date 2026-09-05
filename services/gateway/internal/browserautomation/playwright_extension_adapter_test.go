package browserautomation

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

func TestPlaywrightExtensionAdapterTabLifecycleAndRelease(t *testing.T) {
	controller := newFakePlaywrightController()
	adapter := NewPlaywrightExtensionAdapter(playwrightAdapterTestConfig(), controller).(*PlaywrightExtensionAdapter)

	listed, err := adapter.Call(context.Background(), "browser.list_tabs", map[string]any{"owner_id": "owner-1"})
	if err != nil {
		t.Fatalf("list tabs: %v", err)
	}
	if len(listed.Pages) != 1 || firstStringValue(mapValue(listed.Pages[0]), "page_id") != "page_1" {
		t.Fatalf("unexpected initial pages: %#v", listed.Pages)
	}
	if listed.ProviderSessionRef == "" || strings.Contains(listed.ProviderSessionRef, "controller-session") {
		t.Fatalf("provider session ref leaked or was omitted: %q", listed.ProviderSessionRef)
	}

	opened, err := adapter.Call(context.Background(), "browser.open", map[string]any{
		"owner_id": "owner-1", "url": "https://example.com/first",
	})
	if err != nil {
		t.Fatalf("reuse blank page: %v", err)
	}
	assertSelectedPlaywrightPage(t, opened.Pages, "page_1", "https://example.com/first")

	opened, err = adapter.Call(context.Background(), "browser.open", map[string]any{
		"owner_id": "owner-1", "url": "https://example.com/second",
	})
	if err != nil {
		t.Fatalf("open second page: %v", err)
	}
	assertSelectedPlaywrightPage(t, opened.Pages, "page_2", "https://example.com/second")

	focused, err := adapter.Call(context.Background(), "browser.focus", map[string]any{
		"owner_id": "owner-1", "page_id": "1",
	})
	if err != nil {
		t.Fatalf("focus page: %v", err)
	}
	focusedPage := mapValue(focused.Output)
	if firstStringValue(focusedPage, "page_id") != "page_1" || !boolValue(focusedPage["selected"]) {
		t.Fatalf("focus output did not identify the selected page: %#v", focused.Output)
	}
	if call := controller.lastSession().lastCall(); call.Operation != "tabs.handoff" {
		t.Fatalf("browser.focus must use explicit handoff, got %#v", call)
	}

	closed, err := adapter.Call(context.Background(), "browser.close", map[string]any{
		"owner_id": "owner-1", "page_id": "page_2",
	})
	if err != nil {
		t.Fatalf("close page: %v", err)
	}
	pages := extractPages(mapValue(closed.Output))
	if len(pages) != 1 || firstStringValue(mapValue(pages[0]), "page_id") != "page_1" {
		t.Fatalf("unexpected pages after close: %#v", pages)
	}

	if err := adapter.ReleaseSession(map[string]any{"owner_id": "owner-1"}); err != nil {
		t.Fatalf("release session: %v", err)
	}
	if controller.lastSession().releaseCount() != 1 {
		t.Fatalf("session release count = %d, want 1", controller.lastSession().releaseCount())
	}
}

func TestPlaywrightExtensionAdapterReadPageUsesRenderedTaskPage(t *testing.T) {
	controller := newFakePlaywrightController()
	adapter := NewPlaywrightExtensionAdapter(playwrightAdapterTestConfig(), controller).(*PlaywrightExtensionAdapter)
	t.Cleanup(func() { _ = adapter.Close() })

	result, err := adapter.ReadPage(context.Background(), "https://example.com/inbox", map[string]any{
		"owner_id": "owner-1", "max_chars": 4096,
	})
	if err != nil {
		t.Fatalf("read page: %v", err)
	}
	if result.FinalURL != "https://example.com/inbox" || result.Title != "Task page" || result.ReadyState != "complete" {
		t.Fatalf("unexpected page metadata: %#v", result)
	}
	if result.Text != fakePlaywrightPageText || result.HTML == "" || result.HTMLLength == 0 || !result.Rendered {
		t.Fatalf("rendered page content was not preserved: %#v", result)
	}
	if result.AuthState != "authenticated" || result.AuthConfidence == "" {
		t.Fatalf("snapshot auth inference was not applied: %#v", result)
	}
	wantActions := []string{
		"playwright_mcp.page.navigate",
		"playwright_mcp.page.info",
		"playwright_mcp.page.read",
		"playwright_mcp.page.snapshot",
	}
	if !reflect.DeepEqual(result.Actions, wantActions) {
		t.Fatalf("actions = %#v, want %#v", result.Actions, wantActions)
	}
	if result.Provider != "playwright-extension" || result.ProviderSessionRef == "" || result.SessionGeneration != 11 {
		t.Fatalf("runtime identity missing from read result: %#v", result)
	}
}

func TestPlaywrightExtensionAdapterSnapshotActionsAndScreenshot(t *testing.T) {
	controller := newFakePlaywrightController()
	adapter := NewPlaywrightExtensionAdapter(playwrightAdapterTestConfig(), controller).(*PlaywrightExtensionAdapter)
	t.Cleanup(func() { _ = adapter.Close() })
	ctx := context.Background()
	baseArgs := map[string]any{"owner_id": "owner-1", "page_id": "page_1"}

	if _, err := adapter.Call(ctx, "browser.navigate", mergeArgs(baseArgs, map[string]any{"url": "https://example.com/form"})); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	snapshot := takePlaywrightTestSnapshot(t, adapter, baseArgs)
	buttonRef := playwrightTestControlRef(t, snapshot, "Submit")
	if _, err := adapter.Call(ctx, "browser.click", mergeArgs(baseArgs, map[string]any{
		"ref": buttonRef, "snapshot_id": firstStringValue(snapshot, "snapshot_id"),
	})); err != nil {
		t.Fatalf("click: %v", err)
	}
	assertLastPlaywrightCall(t, controller.lastSession(), "page.click", map[string]any{
		"page_id": "page_1", "ref": "e1",
	})
	if _, err := adapter.Call(ctx, "browser.click", mergeArgs(baseArgs, map[string]any{"ref": buttonRef})); app.ToolErrorCodeFrom(err) != app.ToolErrorSnapshotStale {
		t.Fatalf("reusing an action ref error = %v, code = %q", err, app.ToolErrorCodeFrom(err))
	}

	snapshot = takePlaywrightTestSnapshot(t, adapter, baseArgs)
	textboxRef := playwrightTestControlRef(t, snapshot, "Email")
	if _, err := adapter.Call(ctx, "browser.type", mergeArgs(baseArgs, map[string]any{
		"ref": textboxRef, "text": "owner@example.com",
	})); err != nil {
		t.Fatalf("fill: %v", err)
	}
	assertLastPlaywrightCall(t, controller.lastSession(), "page.fill", map[string]any{
		"page_id": "page_1", "ref": "e2", "text": "owner@example.com",
	})

	snapshot = takePlaywrightTestSnapshot(t, adapter, baseArgs)
	selectRef := playwrightTestControlRef(t, snapshot, "Country")
	if _, err := adapter.Call(ctx, "browser.select", mergeArgs(baseArgs, map[string]any{
		"ref": selectRef, "values": []string{"US", "CA"},
	})); err != nil {
		t.Fatalf("select: %v", err)
	}
	assertLastPlaywrightCall(t, controller.lastSession(), "page.select", map[string]any{
		"page_id": "page_1", "ref": "e3", "values": []string{"US", "CA"},
	})

	if _, err := adapter.Call(ctx, "browser.type", mergeArgs(baseArgs, map[string]any{
		"mode": "focused", "text": "continued text",
	})); err != nil {
		t.Fatalf("focused type: %v", err)
	}
	assertLastPlaywrightCall(t, controller.lastSession(), "page.type", map[string]any{
		"page_id": "page_1", "text": "continued text", "focused": true,
	})

	screenshot, err := adapter.Call(ctx, "browser.screenshot", mergeArgs(baseArgs, map[string]any{"full_page": true}))
	if err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	content := mapValue(screenshot.Output)["content"].([]any)
	image := mapValue(content[0])
	if firstStringValue(image, "type") != "image" || firstStringValue(image, "mimeType") != "image/png" || firstStringValue(image, "data") == "" {
		t.Fatalf("unexpected screenshot content: %#v", screenshot.Output)
	}
	assertLastPlaywrightCall(t, controller.lastSession(), "page.screenshot", map[string]any{
		"page_id": "page_1", "full_page": true,
	})
}

func TestPlaywrightExtensionAdapterRejectsConcurrentScope(t *testing.T) {
	controller := newFakePlaywrightController()
	adapter := NewPlaywrightExtensionAdapter(playwrightAdapterTestConfig(), controller).(*PlaywrightExtensionAdapter)
	t.Cleanup(func() { _ = adapter.Close() })

	if _, err := adapter.Call(context.Background(), "browser.list_tabs", map[string]any{"owner_id": "owner-1"}); err != nil {
		t.Fatalf("first scope: %v", err)
	}
	if _, err := adapter.Call(context.Background(), "browser.list_tabs", map[string]any{"owner_id": "owner-2"}); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("second scope error = %v, want busy", err)
	}
	if len(controller.sessionsSnapshot()) != 1 {
		t.Fatalf("controller acquired %d sessions, want 1", len(controller.sessionsSnapshot()))
	}
}

func TestPlaywrightExtensionAdapterKeepsSessionAfterToolContextEnds(t *testing.T) {
	controller := newFakePlaywrightController()
	adapter := NewPlaywrightExtensionAdapter(playwrightAdapterTestConfig(), controller).(*PlaywrightExtensionAdapter)
	t.Cleanup(func() { _ = adapter.Close() })

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	if _, err := adapter.Call(firstCtx, "browser.list_tabs", map[string]any{"owner_id": "owner-1"}); err != nil {
		t.Fatalf("first call: %v", err)
	}
	cancelFirst()
	time.Sleep(10 * time.Millisecond)

	if _, err := adapter.Call(context.Background(), "browser.list_tabs", map[string]any{"owner_id": "owner-1"}); err != nil {
		t.Fatalf("second call after first tool context ended: %v", err)
	}
	if controller.lastSession().releaseCount() != 0 {
		t.Fatal("tool-call context cancellation released the adapter-owned session")
	}
}

const fakePlaywrightPageText = "Account owner@example.com has a usable application page with navigation, profile, settings, and recent messages."

type fakePlaywrightController struct {
	mu       sync.Mutex
	sessions []*fakePlaywrightSession
}

func newFakePlaywrightController() *fakePlaywrightController {
	return &fakePlaywrightController{}
}

func (c *fakePlaywrightController) Status(context.Context) browsercontrol.Status {
	return browsercontrol.Status{
		Configured: true, State: browsercontrol.StateReady, ProfileID: "default", CredentialGeneration: 7,
		Versions: browsercontrol.Versions{Client: "playwright-mcp", ClientVersion: "0.0.80"},
	}
}

func (c *fakePlaywrightController) AcquireSession(ctx context.Context, _ string, _, _ time.Duration) (browsercontrol.Session, error) {
	c.mu.Lock()
	session := newFakePlaywrightSession(len(c.sessions) + 1)
	c.sessions = append(c.sessions, session)
	c.mu.Unlock()
	go func() {
		<-ctx.Done()
		_ = session.Release(context.Background())
	}()
	return session, nil
}

func (c *fakePlaywrightController) lastSession() *fakePlaywrightSession {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessions[len(c.sessions)-1]
}

func (c *fakePlaywrightController) sessionsSnapshot() []*fakePlaywrightSession {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*fakePlaywrightSession{}, c.sessions...)
}

type fakePlaywrightPage struct {
	ID       string
	URL      string
	Title    string
	Selected bool
}

type fakePlaywrightCall struct {
	Operation string
	Arguments map[string]any
}

type fakePlaywrightSession struct {
	mu       sync.Mutex
	lease    browsercontrol.SessionLease
	pages    []fakePlaywrightPage
	calls    []fakePlaywrightCall
	releases int
}

func newFakePlaywrightSession(sequence int) *fakePlaywrightSession {
	return &fakePlaywrightSession{
		lease: browsercontrol.SessionLease{
			SchemaVersion: 1, State: "acquired", ProfileID: "default", Lane: "mcp",
			SessionID: "controller-session-secret-" + string(rune('0'+sequence)), CredentialGeneration: 7,
			ControllerGeneration: 3, SessionGeneration: 11, PageGeneration: 13,
		},
		pages: []fakePlaywrightPage{{ID: "page_1", URL: "about:blank", Selected: true}},
	}
}

func (s *fakePlaywrightSession) Lease() browsercontrol.SessionLease {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lease
}

func (s *fakePlaywrightSession) Execute(_ context.Context, operation string, arguments map[string]any) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, fakePlaywrightCall{Operation: operation, Arguments: cloneArgs(arguments)})
	if s.releases > 0 {
		return nil, errors.New("session released")
	}
	s.lease.PageGeneration++

	switch operation {
	case "tabs.list":
		return map[string]any{"pages": s.pageMapsLocked()}, nil
	case "tabs.new":
		for index := range s.pages {
			s.pages[index].Selected = false
		}
		page := fakePlaywrightPage{
			ID: "page_" + string(rune('1'+len(s.pages))), URL: firstNonEmptyBrowserString(stringArg(arguments, "url"), "about:blank"), Selected: true,
		}
		s.pages = append(s.pages, page)
		return map[string]any{"pages": s.pageMapsLocked()}, nil
	case "tabs.select", "tabs.handoff":
		page, err := s.selectPageLocked(stringArg(arguments, "page_id"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"page": s.pageMapLocked(*page)}, nil
	case "tabs.close":
		pageID := firstNonEmptyBrowserString(stringArg(arguments, "page_id"), s.selectedPageIDLocked())
		if !s.removePageLocked(pageID) {
			return nil, errors.New("page not found")
		}
		return map[string]any{"pages": s.pageMapsLocked()}, nil
	case "page.navigate":
		page, err := s.selectPageLocked(stringArg(arguments, "page_id"))
		if err != nil {
			return nil, err
		}
		page.URL = stringArg(arguments, "url")
		page.Title = "Task page"
		return map[string]any{"page": s.pageMapLocked(*page)}, nil
	case "page.info", "page.wait":
		page, err := s.selectPageLocked(stringArg(arguments, "page_id"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"page": s.pageMapLocked(*page)}, nil
	case "page.read":
		page, err := s.selectPageLocked(stringArg(arguments, "page_id"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"page": s.readPageMapLocked(*page)}, nil
	case "page.snapshot":
		page, err := s.selectPageLocked(stringArg(arguments, "page_id"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"page": s.pageMapLocked(*page), "snapshot": fakePlaywrightSnapshot()}, nil
	case "page.click", "page.fill", "page.type", "page.select":
		page, err := s.selectPageLocked(stringArg(arguments, "page_id"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"page": s.pageMapLocked(*page)}, nil
	case "page.screenshot":
		page, err := s.selectPageLocked(stringArg(arguments, "page_id"))
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"page": s.pageMapLocked(*page), "screenshot": map[string]any{"mime_type": "image/png", "data_base64": "c2NyZWVuc2hvdA=="},
		}, nil
	default:
		return nil, errors.New("unsupported fake operation: " + operation)
	}
}

func (s *fakePlaywrightSession) Release(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.releases == 0 {
		s.releases = 1
	}
	return nil
}

func (s *fakePlaywrightSession) releaseCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.releases
}

func (s *fakePlaywrightSession) lastCall() fakePlaywrightCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[len(s.calls)-1]
}

func (s *fakePlaywrightSession) pageMapsLocked() []any {
	pages := make([]any, 0, len(s.pages))
	for _, page := range s.pages {
		pages = append(pages, s.pageMapLocked(page))
	}
	return pages
}

func (s *fakePlaywrightSession) pageMapLocked(page fakePlaywrightPage) map[string]any {
	return map[string]any{
		"page_id": page.ID, "url": page.URL, "title": page.Title, "selected": page.Selected, "ready_state": "complete",
	}
}

func (s *fakePlaywrightSession) readPageMapLocked(page fakePlaywrightPage) map[string]any {
	result := s.pageMapLocked(page)
	result["lang"] = "en"
	result["text"] = fakePlaywrightPageText
	result["html"] = "<html lang=\"en\"><body>" + fakePlaywrightPageText + "</body></html>"
	result["text_length"] = len([]rune(fakePlaywrightPageText))
	result["text_truncated"] = false
	result["scroll_height"] = 900
	return result
}

func (s *fakePlaywrightSession) selectPageLocked(pageID string) (*fakePlaywrightPage, error) {
	if pageID == "" {
		pageID = s.selectedPageIDLocked()
	}
	for index := range s.pages {
		if s.pages[index].ID != pageID {
			continue
		}
		for other := range s.pages {
			s.pages[other].Selected = other == index
		}
		return &s.pages[index], nil
	}
	return nil, errors.New("page not found")
}

func (s *fakePlaywrightSession) selectedPageIDLocked() string {
	for _, page := range s.pages {
		if page.Selected {
			return page.ID
		}
	}
	return ""
}

func (s *fakePlaywrightSession) removePageLocked(pageID string) bool {
	for index, page := range s.pages {
		if page.ID != pageID {
			continue
		}
		selected := page.Selected
		s.pages = append(s.pages[:index], s.pages[index+1:]...)
		if selected && len(s.pages) > 0 {
			s.pages[minInt(index, len(s.pages)-1)].Selected = true
		}
		return true
	}
	return false
}

func fakePlaywrightSnapshot() []any {
	return []any{
		map[string]any{"role": "button", "name": "Submit", "ref": "e1"},
		map[string]any{"role": "textbox", "name": "Email", "ref": "e2"},
		map[string]any{"role": "combobox", "name": "Country", "ref": "e3"},
		map[string]any{"role": "button", "name": "Sign out", "ref": "e4"},
	}
}

func playwrightAdapterTestConfig() config.Config {
	cfg := config.Default()
	cfg.Tools.BrowserAutomation.Profile = "default"
	cfg.Adapters.BrowserAutomation.TimeoutMS = 1000
	return cfg
}

func mergeArgs(base, extra map[string]any) map[string]any {
	result := cloneArgs(base)
	for key, value := range extra {
		result[key] = value
	}
	return result
}

func takePlaywrightTestSnapshot(t *testing.T, adapter *PlaywrightExtensionAdapter, args map[string]any) map[string]any {
	t.Helper()
	result, err := adapter.Call(context.Background(), "browser.snapshot", args)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	snapshot := mapValue(mapValue(result.Output)["snapshot"])
	if snapshot == nil || firstStringValue(snapshot, "snapshot_id") == "" {
		t.Fatalf("snapshot output is incomplete: %#v", result.Output)
	}
	return snapshot
}

func playwrightTestControlRef(t *testing.T, snapshot map[string]any, name string) string {
	t.Helper()
	controls, ok := snapshot["controls"].([]any)
	if !ok {
		t.Fatalf("snapshot controls missing: %#v", snapshot)
	}
	for _, raw := range controls {
		control := mapValue(raw)
		if firstStringValue(control, "accessible_name") == name {
			return firstStringValue(control, "ref")
		}
	}
	t.Fatalf("control %q not found in %#v", name, controls)
	return ""
}

func assertSelectedPlaywrightPage(t *testing.T, pages []any, pageID, pageURL string) {
	t.Helper()
	for _, raw := range pages {
		page := mapValue(raw)
		if firstStringValue(page, "page_id") == pageID && firstStringValue(page, "url") == pageURL && boolValue(page["selected"]) {
			return
		}
	}
	t.Fatalf("selected page %s %s not found in %#v", pageID, pageURL, pages)
}

func assertLastPlaywrightCall(t *testing.T, session *fakePlaywrightSession, operation string, arguments map[string]any) {
	t.Helper()
	call := session.lastCall()
	if call.Operation != operation || !reflect.DeepEqual(call.Arguments, arguments) {
		t.Fatalf("last call = %#v, want operation=%q arguments=%#v", call, operation, arguments)
	}
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
