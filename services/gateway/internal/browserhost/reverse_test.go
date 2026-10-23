package browserhost

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeDesktop answers reverse commands the way the embedded host does: it
// echoes the binding, and tests choose the status, output or failure.
type fakeDesktop struct {
	mu       sync.Mutex
	id       string
	revision uint64
	done     chan struct{}
	commands []Command
	answer   func(Command) (json.RawMessage, error)
}

func newFakeDesktop(id string) *fakeDesktop {
	return &fakeDesktop{id: id, revision: 7, done: make(chan struct{})}
}
func (f *fakeDesktop) SessionID() string             { return f.id }
func (f *fakeDesktop) AuthorizationRevision() uint64 { return f.revision }
func (f *fakeDesktop) Done() <-chan struct{}         { return f.done }
func (f *fakeDesktop) end()                          { close(f.done) }
func (f *fakeDesktop) Command(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var command Command
	if err := json.Unmarshal(raw, &command); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.commands = append(f.commands, command)
	answer := f.answer
	f.mu.Unlock()
	if answer != nil {
		return answer(command)
	}
	return reply(command, "completed", `{"page_id":"`+command.Binding.PageID+`","url":"https://example.test/"}`), nil
}
func (f *fakeDesktop) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.commands) }
func reply(command Command, status, output string) json.RawMessage {
	raw, _ := json.Marshal(Message{SchemaVersion: 1, Type: "result", CommandID: command.CommandID, Binding: &command.Binding, Status: status, Output: json.RawMessage(output)})
	return raw
}

type fakeRouter struct {
	mu      sync.Mutex
	session ReverseSession
}

func (r *fakeRouter) set(session ReverseSession) { r.mu.Lock(); r.session = session; r.mu.Unlock() }
func (r *fakeRouter) ReverseSession(identity Identity) (ReverseSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if identity != testIdentity || r.session == nil {
		return nil, ErrUnavailable
	}
	return r.session, nil
}

var reverseScope = Scope{Identity: testIdentity, ConversationID: "conversation", TaskID: "task"}

func reverseBroker(t *testing.T, desktop *fakeDesktop) (*Broker, *fakeRouter) {
	t.Helper()
	b := newTestBroker(t)
	router := &fakeRouter{}
	if desktop != nil {
		router.set(desktop)
	}
	b.SetReverseRouter(router)
	return b, router
}

func TestReverseHostBindsToTheSessionWithoutGrantOrHeartbeat(t *testing.T) {
	desktop := newFakeDesktop("11111111-1111-4111-8111-111111111111")
	b, _ := reverseBroker(t, desktop)
	binding, err := b.Acquire(context.Background(), reverseScope)
	if err != nil {
		t.Fatal(err)
	}
	if binding.ConnectionEpoch != desktop.id || binding.HostID != reverseHostID(testIdentity, desktop.id) || binding.RuntimeGeneration != "iscp_"+desktop.id ||
		binding.AuthorizationDigest != reverseAuthorizationDigest(desktop.id, 7) || !binding.LeaseExpiresAt.IsZero() {
		t.Fatalf("binding %+v", binding)
	}
	if desktop.count() != 1 || desktop.commands[0].Operation != "acquire" {
		t.Fatalf("commands %+v", desktop.commands)
	}
	if len(b.grants) != 0 {
		t.Fatal("an ISCP host issued a grant")
	}
	// No heartbeat ever arrives; the lease follows the task, not a timer.
	b.now = func() time.Time { return time.Now().Add(time.Hour) }
	if _, err := b.Dispatch(context.Background(), binding, "cmd_read_after_hour", "read", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if renewed, err := b.Renew(context.Background(), binding); err != nil || renewed != binding {
		t.Fatal(renewed, err)
	}
}

func TestReverseSessionEndReleasesLeasesAndNextTaskCallUsesNewSession(t *testing.T) {
	first := newFakeDesktop("11111111-1111-4111-8111-111111111111")
	b, router := reverseBroker(t, first)
	adapter := NewScopedAdapter(b, reverseScope)
	if _, err := adapter.Call(context.Background(), "browser.open", map[string]any{"url": "https://example.test/"}); err != nil {
		t.Fatal(err)
	}
	old := *adapter.binding
	first.end()
	deadline := time.Now().Add(time.Second)
	for b.LeaseActive(old) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if b.LeaseActive(old) {
		t.Fatal("ended session kept its lease")
	}
	if _, err := b.Dispatch(context.Background(), old, "cmd_stale", "read", map[string]any{}); err == nil {
		t.Fatal("ended session accepted a command")
	}
	second := newFakeDesktop("22222222-2222-4222-8222-222222222222")
	router.set(second)
	if _, err := adapter.Call(context.Background(), "browser.read", map[string]any{}); err == nil {
		t.Fatal("stale binding was used")
	}
	result, err := adapter.Call(context.Background(), "browser.open", map[string]any{"url": "https://example.test/"})
	if err != nil {
		t.Fatal(err)
	}
	if adapter.binding.ConnectionEpoch != second.id || result.SessionGeneration == 0 || second.count() != 2 {
		t.Fatalf("reacquired %+v %+v %d", adapter.binding, result, second.count())
	}
}

func TestReverseHealthIsLocalAndBounded(t *testing.T) {
	desktop := newFakeDesktop("11111111-1111-4111-8111-111111111111")
	b, router := reverseBroker(t, desktop)
	adapter := NewScopedAdapter(b, reverseScope)
	result, err := adapter.Health(context.Background(), nil)
	if err != nil || result.Output.(map[string]any)["ok"] != true || desktop.count() != 0 {
		t.Fatalf("health %+v %v %d", result, err, desktop.count())
	}
	router.set(nil)
	previous := readyWait
	readyWait = 50 * time.Millisecond
	t.Cleanup(func() { readyWait = previous })
	started := time.Now()
	if _, err := adapter.Health(context.Background(), nil); !errors.Is(err, ErrUnavailable) || time.Since(started) > time.Second {
		t.Fatalf("missing session health %v", err)
	}
	// A session that recovers within the bound is waited for.
	readyWait = 2 * time.Second
	go func() { time.Sleep(100 * time.Millisecond); router.set(desktop) }()
	if _, err := adapter.Health(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if desktop.count() != 0 {
		t.Fatal("health dispatched a command")
	}
}

func TestReverseUnavailableInstallationFailsAtOnce(t *testing.T) {
	b, _ := reverseBroker(t, nil)
	started := time.Now()
	if _, err := b.Acquire(context.Background(), reverseScope); !errors.Is(err, ErrUnavailable) || time.Since(started) > 100*time.Millisecond {
		t.Fatal(err)
	}
}

func TestReverseUnknownWriteIsFencedAndNeverResent(t *testing.T) {
	desktop := newFakeDesktop("11111111-1111-4111-8111-111111111111")
	b, _ := reverseBroker(t, desktop)
	binding, err := b.Acquire(context.Background(), reverseScope)
	if err != nil {
		t.Fatal(err)
	}
	desktop.answer = func(Command) (json.RawMessage, error) { return nil, errors.New("outcome_unknown") }
	args := map[string]any{"ref": "snapshot_1:e1", "snapshot_id": "snapshot_1"}
	if _, err := b.Dispatch(context.Background(), binding, "cmd_click", "click", args); !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
	if _, err := b.Dispatch(context.Background(), binding, "cmd_click", "click", args); !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
	desktop.answer = nil
	if _, err := b.Dispatch(context.Background(), binding, "cmd_read", "read", map[string]any{}); !errors.Is(err, ErrUnknown) {
		t.Fatal("conversation with an unknown write accepted another command", err)
	}
	if desktop.count() != 2 {
		t.Fatalf("unknown write was resent: %d commands", desktop.count())
	}
	fences := b.Fences(testIdentity)
	if len(fences) != 1 || fences[0].CommandID != "cmd_click" || fences[0].State != "unknown" || fences[0].ConnectionEpoch != desktop.id {
		t.Fatalf("fences %+v", fences)
	}
	if err := b.Reconcile(testIdentity, "cmd_click", fences[0].Digest, "observed_not_applied"); err != nil {
		t.Fatal(err)
	}
}

func TestReverseRefusalBeforeExecutionIsNotUncertain(t *testing.T) {
	desktop := newFakeDesktop("11111111-1111-4111-8111-111111111111")
	b, _ := reverseBroker(t, desktop)
	binding, err := b.Acquire(context.Background(), reverseScope)
	if err != nil {
		t.Fatal(err)
	}
	desktop.answer = func(Command) (json.RawMessage, error) { return nil, ErrNotDispatched }
	if _, err := b.Dispatch(context.Background(), binding, "cmd_fill", "fill", map[string]any{"ref": "s:e1", "snapshot_id": "s", "value": "draft"}); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if fences := b.Fences(testIdentity); len(fences) != 0 {
		t.Fatalf("undispatched write left an unknown fence: %+v", fences)
	}
	if b.fences["cmd_fill"].State != "failed" {
		t.Fatal(b.fences["cmd_fill"])
	}
}

func TestReverseResultBudgetAndMismatchedBindingAreRejected(t *testing.T) {
	desktop := newFakeDesktop("11111111-1111-4111-8111-111111111111")
	b, _ := reverseBroker(t, desktop)
	binding, err := b.Acquire(context.Background(), reverseScope)
	if err != nil {
		t.Fatal(err)
	}
	desktop.answer = func(command Command) (json.RawMessage, error) {
		return reply(command, "completed", `{"text":"`+strings.Repeat("x", MaxResultBytes)+`"}`), nil
	}
	if _, err := b.Dispatch(context.Background(), binding, "cmd_big", "read", map[string]any{}); err == nil {
		t.Fatal("oversized result accepted")
	}
	if b.fences["cmd_big"].State != "fenced" {
		t.Fatal(b.fences["cmd_big"])
	}
	// The malformed answer retires the host; the next acquire is fresh.
	deadline := time.Now().Add(time.Second)
	for b.LeaseActive(binding) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	desktop.answer = func(command Command) (json.RawMessage, error) {
		command.Binding.PageID = "page_other"
		return reply(command, "completed", `{}`), nil
	}
	if _, err := b.Acquire(context.Background(), Scope{Identity: testIdentity, ConversationID: "other", TaskID: "task"}); err == nil {
		t.Fatal("result for another page accepted")
	}
}

func TestScopedStableWaitPassesHostDecisionAndRebindsRoute(t *testing.T) {
	desktop := newFakeDesktop("11111111-1111-4111-8111-111111111111")
	b, _ := reverseBroker(t, desktop)
	adapter := NewScopedAdapter(b, reverseScope)
	if _, err := adapter.Call(context.Background(), "browser.open", map[string]any{"url": "https://example.test/start"}); err != nil {
		t.Fatal(err)
	}
	waits := 0
	desktop.answer = func(command Command) (json.RawMessage, error) {
		switch command.Operation {
		case "wait":
			waits++
			if command.Arguments["mode"] != "stable_state" || command.Arguments["before_digest"] != strings.Repeat("a", 64) || command.Arguments["allow_no_change"] != false || command.Arguments["milliseconds"] != nil {
				t.Errorf("wait arguments %+v", command.Arguments)
			}
			url := "https://example.test/other"
			if waits > 1 {
				url = "https://example.test/start"
			}
			return reply(command, "completed", `{"status":"stable","page_id":"`+command.Binding.PageID+`","url":"`+url+`","state_digest":"`+strings.Repeat("b", 64)+`","state_changed":true,"surface_visible":false}`), nil
		default:
			return reply(command, "completed", `{"url":"`+command.Arguments["url"].(string)+`"}`), nil
		}
	}
	result, err := adapter.Call(context.Background(), "browser.wait", map[string]any{"mode": "stable_state", "expected_url": "https://example.test/start", "before_digest": strings.Repeat("a", 64), "allow_no_change": false, "require_url_stable": true, "presentation": "hidden"})
	if err != nil {
		t.Fatal(err)
	}
	output := result.Output.(map[string]any)
	if waits != 2 || output["status"] != "stable" || output["route_rebinds"] != 1 || output["presentation"] != "hidden" || output["profile_id"] != EmbeddedProfileID || result.Presentation != "hidden" || result.SurfaceVisible {
		t.Fatalf("stable wait %d %+v %+v", waits, output, result)
	}
	desktop.answer = func(command Command) (json.RawMessage, error) {
		return reply(command, "completed", `{"status":"timeout","page_id":"`+command.Binding.PageID+`"}`), nil
	}
	if _, err := adapter.Call(context.Background(), "browser.wait", map[string]any{"mode": "stable_state"}); err == nil || !strings.Contains(err.Error(), "browser_settle_timeout") {
		t.Fatal(err)
	}
	if _, err := adapter.Call(context.Background(), "browser.wait", map[string]any{"mode": "stable_state", "session_generation": 99}); err == nil || !strings.Contains(err.Error(), "browser_session_stale") {
		t.Fatal(err)
	}
}

func TestScopedTabsFocusAndSnapshotIdentity(t *testing.T) {
	desktop := newFakeDesktop("11111111-1111-4111-8111-111111111111")
	b, _ := reverseBroker(t, desktop)
	adapter := NewScopedAdapter(b, reverseScope)
	tabs, err := adapter.Call(context.Background(), "browser.list_tabs", map[string]any{})
	if err != nil || len(tabs.Pages) != 0 || desktop.count() != 0 {
		t.Fatalf("tabs before navigation %+v %v", tabs, err)
	}
	if _, err := adapter.Call(context.Background(), "browser.focus", map[string]any{}); err == nil {
		t.Fatal("focused a page that was never opened")
	}
	if _, err := adapter.Call(context.Background(), "browser.open", map[string]any{"url": "https://example.test/"}); err != nil {
		t.Fatal(err)
	}
	tabs, _ = adapter.Call(context.Background(), "browser.list_tabs", map[string]any{"presentation": "hidden"})
	if len(tabs.Pages) != 1 || tabs.Pages[0].(map[string]any)["url"] != "https://example.test/" {
		t.Fatalf("tabs %+v", tabs.Pages)
	}
	focus, err := adapter.Call(context.Background(), "browser.focus", map[string]any{"page_id": adapter.binding.PageID})
	if err != nil || focus.Output.(map[string]any)["focused"] != true {
		t.Fatal(focus, err)
	}
	desktop.answer = func(command Command) (json.RawMessage, error) {
		return reply(command, "completed", `{"snapshot_id":"s1","page_id":"`+command.Binding.PageID+`","snapshot":{"snapshot_id":"s1","page_id":"`+command.Binding.PageID+`","digest":"d","content_digest":"c","previous_snapshot_id":"s0","repeated":false},"surface_visible":true}`), nil
	}
	snapshot, err := adapter.Call(context.Background(), "browser.snapshot", map[string]any{"presentation": "visible"})
	if err != nil {
		t.Fatal(err)
	}
	inner := snapshot.Output.(map[string]any)["snapshot"].(map[string]any)
	for key, want := range map[string]any{"owner_id": "owner", "profile_id": EmbeddedProfileID, "presentation": "visible", "provider_session_ref": "browser_host:" + adapter.binding.LeaseID, "previous_snapshot_id": "s0", "repeated": false} {
		if inner[key] != want {
			t.Fatalf("snapshot %s=%v", key, inner[key])
		}
	}
	if !snapshot.SurfaceVisible {
		t.Fatal("host visibility was not reported")
	}
}

func TestStableWaitValidationIsClosed(t *testing.T) {
	good := map[string]any{"mode": "stable_state", "expected_url": "https://example.test/", "before_digest": strings.Repeat("a", 64), "allow_no_change": true, "require_ready_state": true, "timeout_ms": 15000}
	if err := validateOperation("wait", good); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []map[string]any{
		{"mode": "sleep"},
		{"mode": "stable_state", "milliseconds": 10},
		{"mode": "stable_state", "timeout_ms": 16000},
		{"mode": "stable_state", "before_digest": "short"},
		{"mode": "stable_state", "expected_url": "file:///etc/passwd"},
		{"mode": "stable_state", "allow_no_change": "yes"},
		{"milliseconds": 10, "before_digest": strings.Repeat("a", 64)},
	} {
		if validateOperation("wait", bad) == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	if validateOperation("read", map[string]any{"max_chars": MaxReadChars + 1}) == nil {
		t.Fatal("read window exceeds the host window")
	}
}

func TestReverseBindingValuesMatchTheDesktopVector(t *testing.T) {
	// apps/desktop/test/host-agent-iscp.test.mjs asserts the same values.
	session := "11111111-1111-4111-8111-111111111111"
	if got := reverseHostID(testIdentity, session); got != "iscp_7b0b83b55eedc9a5146da1bce2231528" {
		t.Fatal(got)
	}
	if got := reverseAuthorizationDigest(session, 7); got != "ceb65fe5cc84a5429d48508b563c2793e22fe712b6e1a5501c6221041d81e61d" {
		t.Fatal(got)
	}
	raw, _ := json.Marshal(Binding{})
	if !strings.Contains(string(raw), `"lease_expires_at":"0001-01-01T00:00:00Z"`) {
		t.Fatal(string(raw))
	}
}
