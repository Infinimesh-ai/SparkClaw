package toolhub

import (
	"context"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type managedBrowserAdapter struct {
	calls       []string
	callArgs    []map[string]any
	releaseArgs map[string]any
}

func (a *managedBrowserAdapter) Health(context.Context, map[string]any) (browserautomation.Result, error) {
	return browserautomation.Result{Output: map[string]any{"ok": true}}, nil
}

func (a *managedBrowserAdapter) Call(_ context.Context, tool string, args map[string]any) (browserautomation.Result, error) {
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
func (*managedBrowserAdapter) Close() error { return nil }
func (a *managedBrowserAdapter) ReleaseSession(args map[string]any) error {
	a.releaseArgs = cloneStringAnyMap(args)
	return nil
}

func TestManagedBrowserWindowReusesAndReleasesDedicatedVisibleProfile(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.BrowserAutomation.Enabled = true
	adapter := &managedBrowserAdapter{}
	hub := New(cfg, store.NewMemoryStore()).WithBrowserAutomationAdapter(adapter)

	if err := hub.OpenManagedBrowserWindow(t.Context(), "owner-a", "bind-a", "https://liteapp.weixin.qq.com/q/example"); err != nil {
		t.Fatal(err)
	}
	if len(adapter.calls) != 1 || adapter.calls[0] != "browser.open" {
		t.Fatalf("managed window did not open its first target directly: %#v", adapter.calls)
	}
	if got := adapter.callArgs[0]["browser_profile_id"]; got != "managed-bind-a" {
		t.Fatalf("managed window did not use its dedicated profile: %#v", adapter.callArgs[0])
	}
	if err := hub.OpenManagedBrowserWindow(t.Context(), "owner-a", "bind-a", "https://liteapp.weixin.qq.com/q/replacement"); err != nil {
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
		if err := hub.OpenManagedBrowserWindow(t.Context(), "owner-a", "bind-a", "https://liteapp.weixin.qq.com/q/example"); err == nil {
			t.Errorf("%s: expected an error, window was opened against an unverified Chromium", name)
		}
		if len(adapter.calls) != 0 {
			t.Errorf("%s: browser calls were attempted despite failed health gate: %v", name, adapter.calls)
		}
	}
}
