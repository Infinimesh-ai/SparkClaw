package browserautomation

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestBrowserStableStateRejectsStaleSessionGeneration(t *testing.T) {
	adapter := &AgentBrowserAdapter{sessionGeneration: 9}
	_, err := adapter.waitForStableStateLocked(context.Background(), nil, map[string]any{
		"session_generation": 8,
	})
	if err == nil || !strings.Contains(err.Error(), "browser_session_stale") {
		t.Fatalf("stale session generation error = %v", err)
	}
}

func TestBrowserStableStateReportsRendererFailure(t *testing.T) {
	adapter := &AgentBrowserAdapter{
		sessionGeneration: 1,
		observeStableState: func(context.Context, *agentBrowserSession) (browserStableObservation, error) {
			return browserStableObservation{}, errors.New("renderer process exited")
		},
	}
	_, err := adapter.waitForStableStateLocked(context.Background(), nil, map[string]any{
		"session_generation": 1,
	})
	if err == nil || !strings.Contains(err.Error(), "browser_renderer_unavailable") {
		t.Fatalf("renderer failure error = %v", err)
	}
}

func TestBrowserStableStateTimesOutWithoutRequiredChange(t *testing.T) {
	adapter := &AgentBrowserAdapter{
		sessionGeneration: 1,
		observeStableState: func(context.Context, *agentBrowserSession) (browserStableObservation, error) {
			return browserStableObservation{
				URL: "https://example.com/app", Title: "App", Digest: "unchanged",
			}, nil
		},
	}
	_, err := adapter.waitForStableStateLocked(context.Background(), nil, map[string]any{
		"session_generation": 1,
		"before_digest":      "unchanged",
		"allow_no_change":    false,
		"poll_interval_ms":   25,
		"quiet_period_ms":    100,
		"timeout_ms":         500,
	})
	if err == nil || !strings.Contains(err.Error(), "browser_settle_timeout") {
		t.Fatalf("settle timeout error = %v", err)
	}
}

func TestBrowserStableStatePreservesCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	adapter := &AgentBrowserAdapter{sessionGeneration: 1}
	_, err := adapter.waitForStableStateLocked(ctx, nil, map[string]any{
		"session_generation": 1,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled settle error = %v, want context.Canceled", err)
	}
}

func TestSettleBrowserRouteRejectsForeignOriginAndRebindsSafeFragment(t *testing.T) {
	if _, err := settleBrowserRoute(
		"https://mail.qq.com/cgi-bin/frame_html#/mail_list",
		"https://evil.example/cgi-bin/frame_html#/mail_list",
	); err == nil || !strings.Contains(err.Error(), "browser_route_diverged") {
		t.Fatalf("foreign route error = %v", err)
	}
	rebound, err := settleBrowserRoute(
		"https://wx.mail.qq.com/home/index#/list/1/1",
		"https://wx.mail.qq.com/home/index?sid=provider#/login",
	)
	if err != nil {
		t.Fatal(err)
	}
	if rebound != "https://wx.mail.qq.com/home/index?sid=provider#/list/1/1" {
		t.Fatalf("same-origin fragment rebind = %q", rebound)
	}
}
