package browserhost

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

type controllerDesktop struct {
	*fakeDesktop
	lock       sync.Mutex
	lease      browsercontrol.SessionLease
	operations []string
	bindings   []Binding
	arguments  []map[string]any
	fail       string
}

func controllerJSON(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }
func (f *controllerDesktop) ControllerCall(_ context.Context, operation string, raw json.RawMessage) (json.RawMessage, error) {
	f.lock.Lock()
	defer f.lock.Unlock()
	f.operations = append(f.operations, operation)
	if operation == "browser.controller.status" {
		return controllerJSON(browsercontrol.ClientControllerStatus{SchemaVersion: 1, Protocol: browsercontrol.ClientControllerProtocol, Available: true, ProcessState: "stopped",
			Versions: map[string]string{"@playwright/mcp": "0.0.80", "playwright": "1.63.0-alpha-2026-08-31", "playwright-core": "1.63.0-alpha-2026-08-31"},
			Limits:   browsercontrol.ControllerWireLimits{ResponseBytes: 10 << 20, TimeoutMS: 25000, WireBytes: 63488, CacheBytes: 20 << 20, ChunkBytes: 32768, ChunkConcurrency: 4}}), nil
	}
	var body struct {
		CommandID string         `json:"command_id"`
		Digest    string         `json:"digest"`
		Binding   Binding        `json:"binding"`
		Operation string         `json:"operation"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	f.bindings = append(f.bindings, body.Binding)
	reply := map[string]any{"schema_version": 1, "command_id": body.CommandID, "digest": body.Digest, "binding": body.Binding, "supervisor_incarnation": strings.Repeat("a", 32), "outcome": "completed"}
	switch operation {
	case "browser.controller.acquire":
		f.lease = browsercontrol.SessionLease{SchemaVersion: 1, State: "acquired", ProfileID: EmbeddedProfileID, Lane: "mcp", SessionID: "session_" + strings.Repeat("b", 32), CredentialGeneration: 7, ControllerGeneration: 9, SessionGeneration: 1, PageGeneration: 1, ExpiresAt: time.Now().Add(time.Minute)}
		reply["payload"] = f.lease
	case "browser.controller.release":
		reply["payload"] = browsercontrol.ReleaseResult{SchemaVersion: 1, State: "released", ProfileID: EmbeddedProfileID, ControllerGeneration: 9, SessionGeneration: 1}
	case "browser.controller.execute":
		f.operations = append(f.operations, body.Operation)
		f.arguments = append(f.arguments, body.Arguments)
		if f.fail == body.Operation {
			return nil, errors.New("transport lost after send")
		}
		mutation, err := browsercontrol.ClientControllerOperation(body.Operation, body.Arguments)
		if err != nil {
			return nil, err
		}
		if mutation {
			f.lease.PageGeneration++
		}
		page := map[string]any{"page_id": "owned_page", "url": "https://example.test/", "selected": true, "ready_state": "complete", "text": "Task content"}
		output := map[string]any{"page": page, "pages": []any{page}}
		if body.Operation == "tabs.close" {
			output["pages"] = []any{}
		}
		reply["payload"] = browsercontrol.ExecutionResult{SchemaVersion: 1, State: "completed", ProfileID: EmbeddedProfileID, Lane: "mcp", SessionID: f.lease.SessionID, CredentialGeneration: 7, ControllerGeneration: 9, SessionGeneration: 1, PageGeneration: f.lease.PageGeneration, Operation: body.Operation, Result: controllerJSON(output)}
	default:
		return nil, errors.New("unexpected operation")
	}
	return controllerJSON(reply), nil
}
func controllerHostFixture(t *testing.T) (*Broker, *controllerDesktop, *ScopedAdapter) {
	t.Helper()
	b := newTestBroker(t)
	desktop := &controllerDesktop{fakeDesktop: newFakeDesktop("session_controller")}
	router := &fakeRouter{session: desktop}
	b.SetReverseRouter(router)
	b.EnableClientController(config.Default())
	return b, desktop, b.ForScope(reverseScope).(*ScopedAdapter)
}
func TestClientControllerHostPreservesTypedToolsAndTrustedScope(t *testing.T) {
	b, desktop, adapter := controllerHostFixture(t)
	if _, err := adapter.Call(t.Context(), "browser.list_tabs", nil); err != nil {
		t.Fatal(err)
	}
	if len(desktop.operations) != 0 {
		t.Fatal("list acquired a page")
	}
	if _, err := adapter.Health(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if strings.Join(desktop.operations, ",") != "browser.controller.status" {
		t.Fatal(desktop.operations)
	}
	if _, err := adapter.Call(t.Context(), "browser.navigate", map[string]any{"url": "https://example.test/", "owner_id": "forged", "browser_profile_id": "backend", "local_task_id": "foreign"}); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Call(t.Context(), "browser.type", map[string]any{"mode": "focused", "page_id": "owned_page", "text": "retained text", "submit": true}); err != nil {
		t.Fatal(err)
	}
	last := desktop.arguments[len(desktop.arguments)-1]
	if last["focused"] != true || last["submit"] != true || last["text"] != "retained text" || last["page_id"] != "owned_page" {
		t.Fatal(last)
	}
	if _, err := adapter.Call(t.Context(), "browser.focus", map[string]any{"page_id": "owned_page"}); err != nil {
		t.Fatal(err)
	}
	if desktop.operations[len(desktop.operations)-1] != "page.info" {
		t.Fatal(desktop.operations)
	}
	if _, err := adapter.Call(t.Context(), "browser.close", map[string]any{"page_id": "owned_page"}); err != nil {
		t.Fatal(err)
	}
	for _, op := range desktop.operations {
		if op == "tabs.new" || op == "tabs.handoff" {
			t.Fatal("client used backend behavior", op)
		}
	}
	for _, binding := range desktop.bindings {
		if binding.Scope != reverseScope || binding.AuthorizationDigest != reverseAuthorizationDigest(desktop.id, 7) {
			t.Fatal(binding)
		}
	}
	if adapter.binding != nil || desktop.count() != 0 || len(b.controller.leases) != 0 {
		t.Fatal("legacy call or lease remained")
	}
	for _, fence := range b.fences {
		if fence.State != "completed" || !fence.Write {
			t.Fatal(fence)
		}
	}
}
func TestClientControllerHostUnknownFenceSurvivesReleaseAndRestart(t *testing.T) {
	b, desktop, adapter := controllerHostFixture(t)
	desktop.fail = "page.navigate"
	if _, err := adapter.Call(t.Context(), "browser.navigate", map[string]any{"url": "https://example.test/"}); !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
	if len(b.Fences(testIdentity)) != 1 {
		t.Fatal(b.Fences(testIdentity))
	}
	if err := adapter.ReleaseSession(nil); err != nil {
		t.Fatal(err)
	}
	before := len(desktop.operations)
	if _, err := adapter.Call(t.Context(), "browser.navigate", map[string]any{"url": "https://example.test/"}); !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
	for _, op := range desktop.operations[before:] {
		if op != "browser.controller.status" {
			t.Fatal("blocked mutation was submitted", op)
		}
	}
	reloaded, err := NewBroker(b.root)
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Close()
	fences := reloaded.Fences(testIdentity)
	if len(fences) != 1 || fences[0].State != "unknown" {
		t.Fatal(fences)
	}
	if err := b.Reconcile(testIdentity, fences[0].CommandID, fences[0].Digest, "observed_not_applied"); err != nil {
		t.Fatal(err)
	}
	desktop.fail = ""
	if _, err := adapter.Call(t.Context(), "browser.navigate", map[string]any{"url": "https://example.test/"}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Close(); err != nil {
		t.Fatal(err)
	}
}
func TestClientControllerHostRejectsLegacyOnlyPeer(t *testing.T) {
	b, _ := reverseBroker(t, newFakeDesktop("old_session"))
	b.EnableClientController(config.Default())
	if _, err := b.ForScope(reverseScope).Call(t.Context(), "browser.navigate", map[string]any{"url": "https://example.test/"}); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}
