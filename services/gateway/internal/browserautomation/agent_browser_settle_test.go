package browserautomation

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func settleSessionEntryFixture(generation uint64, observe func(context.Context, *agentBrowserSession) (browserStableObservation, error)) *agentBrowserSessionEntry {
	return &agentBrowserSessionEntry{
		adapter:    &AgentBrowserAdapter{observeStableState: observe},
		session:    &agentBrowserSession{sessionName: "fixture"},
		generation: generation,
	}
}

func TestBrowserStableStateRejectsStaleSessionGeneration(t *testing.T) {
	entry := settleSessionEntryFixture(9, nil)
	_, err := entry.waitForStableStateLocked(context.Background(), map[string]any{
		"session_generation": 8,
	})
	if err == nil || !strings.Contains(err.Error(), "browser_session_stale") {
		t.Fatalf("stale session generation error = %v", err)
	}
}

func TestBrowserStableStateReportsRendererFailure(t *testing.T) {
	entry := settleSessionEntryFixture(1, func(context.Context, *agentBrowserSession) (browserStableObservation, error) {
		return browserStableObservation{}, errors.New("renderer process exited")
	})
	_, err := entry.waitForStableStateLocked(context.Background(), map[string]any{
		"session_generation": 1,
	})
	if err == nil || !strings.Contains(err.Error(), "browser_renderer_unavailable") {
		t.Fatalf("renderer failure error = %v", err)
	}
}

func TestBrowserStableStateRetriesTransientObservationFailure(t *testing.T) {
	attempts := 0
	entry := settleSessionEntryFixture(1, func(context.Context, *agentBrowserSession) (browserStableObservation, error) {
		attempts++
		if attempts <= 2 {
			return browserStableObservation{}, &agentBrowserActionError{
				Tool:    "agent_browser_get_text",
				Message: "Element not found",
			}
		}
		return browserStableObservation{
			URL: "https://example.com/app", Title: "App", Digest: "stable",
		}, nil
	})
	result, err := entry.waitForStableStateLocked(context.Background(), map[string]any{
		"session_generation": 1,
		"page_id":            "page_1",
		"allow_no_change":    true,
		"poll_interval_ms":   25,
		"quiet_period_ms":    100,
		"timeout_ms":         500,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "stable" || result["page_id"] != "page_1" || attempts < 3 {
		t.Fatalf("settle result = %#v after %d observations", result, attempts)
	}
}

func TestBrowserStableStateTimesOutWhileObservationRemainsUnavailable(t *testing.T) {
	entry := settleSessionEntryFixture(1, func(context.Context, *agentBrowserSession) (browserStableObservation, error) {
		return browserStableObservation{}, &agentBrowserActionError{
			Tool:    "agent_browser_get_text",
			Message: "Element not found",
		}
	})
	_, err := entry.waitForStableStateLocked(context.Background(), map[string]any{
		"session_generation": 1,
		"allow_no_change":    true,
		"poll_interval_ms":   25,
		"quiet_period_ms":    100,
		"timeout_ms":         500,
	})
	if err == nil || !strings.Contains(err.Error(), "browser_settle_timeout") {
		t.Fatalf("settle timeout error = %v", err)
	}
}

func TestBrowserStableStateTimesOutWithoutRequiredChange(t *testing.T) {
	entry := settleSessionEntryFixture(1, func(context.Context, *agentBrowserSession) (browserStableObservation, error) {
		return browserStableObservation{
			URL: "https://example.com/app", Title: "App", Digest: "unchanged",
		}, nil
	})
	_, err := entry.waitForStableStateLocked(context.Background(), map[string]any{
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

func TestBrowserStableStateDoesNotTreatRouteOnlyChangeAsContentChange(t *testing.T) {
	entry := settleSessionEntryFixture(1, func(context.Context, *agentBrowserSession) (browserStableObservation, error) {
		return browserStableObservation{
			URL: "https://example.com/app#/drafts", Title: "App", Digest: "inbox-content",
		}, nil
	})
	_, err := entry.waitForStableStateLocked(context.Background(), map[string]any{
		"session_generation": 1,
		"expected_url":       "https://example.com/app#/drafts",
		"before_digest":      "inbox-content",
		"allow_no_change":    false,
		"poll_interval_ms":   25,
		"quiet_period_ms":    100,
		"timeout_ms":         500,
	})
	if err == nil || !strings.Contains(err.Error(), "browser_settle_timeout") {
		t.Fatalf("route-only settle error = %v", err)
	}
}

func TestBrowserStableStateWaitsOutTransientSPADefaultRouteBeforeRebinding(t *testing.T) {
	const (
		inboxURL  = "https://example.com/app#/inbox"
		draftsURL = "https://example.com/app#/drafts"
	)
	observations := 0
	rebinds := 0
	postRebindDefaultRoutes := 0
	entry := settleSessionEntryFixture(1, func(context.Context, *agentBrowserSession) (browserStableObservation, error) {
		observations++
		if rebinds == 0 || postRebindDefaultRoutes > 0 {
			if rebinds > 0 {
				postRebindDefaultRoutes--
			}
			return browserStableObservation{URL: inboxURL, Title: "Mail", Digest: "inbox"}, nil
		}
		return browserStableObservation{URL: draftsURL, Title: "Mail", Digest: "drafts"}, nil
	})
	entry.adapter.cfg.Adapters.BrowserAutomation.RouteRebindLimit = 2
	entry.adapter.callAgentTool = func(_ context.Context, _ *agentBrowserSession, name string, args map[string]any) (agentBrowserToolResult, error) {
		if name != "agent_browser_open" || stringArg(args, "url") != draftsURL {
			t.Fatalf("unexpected route rebind: tool=%q args=%#v", name, args)
		}
		rebinds++
		if rebinds == 1 {
			postRebindDefaultRoutes = 2
		}
		return agentBrowserToolResult{}, nil
	}

	result, err := entry.waitForStableStateLocked(context.Background(), map[string]any{
		"session_generation": 1,
		"page_id":            "page_1",
		"expected_url":       draftsURL,
		"before_digest":      "inbox",
		"allow_no_change":    false,
		"poll_interval_ms":   25,
		"quiet_period_ms":    100,
		"timeout_ms":         1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rebinds != 1 || observations < 8 {
		t.Fatalf("settle spent rebinds before the SPA route stabilized: rebinds=%d observations=%d", rebinds, observations)
	}
	if result["url"] != draftsURL || result["state_digest"] != "drafts" || result["route_rebinds"] != 1 {
		t.Fatalf("unexpected settled drafts state: %#v", result)
	}
}

func TestBrowserStableStatePreservesCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	entry := settleSessionEntryFixture(1, nil)
	_, err := entry.waitForStableStateLocked(ctx, map[string]any{
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
		app.BrowserTargetExplicitURL,
	); err == nil || !strings.Contains(err.Error(), "browser_route_diverged") {
		t.Fatalf("foreign route error = %v", err)
	}
	rebound, err := settleBrowserRoute(
		"https://wx.mail.qq.com/home/index#/list/1/1",
		"https://wx.mail.qq.com/home/index?sid=provider#/login",
		app.BrowserTargetExplicitURL,
	)
	if err != nil {
		t.Fatal(err)
	}
	if rebound != "https://wx.mail.qq.com/home/index?sid=provider#/list/1/1" {
		t.Fatalf("same-origin fragment rebind = %q", rebound)
	}
}

func TestSettleBrowserRouteAllowsRegisteredDestinationSubdomainRedirect(t *testing.T) {
	rebound, err := settleBrowserRoute(
		"https://mail.qq.com/",
		"https://wx.mail.qq.com/list/readtemplate",
		app.BrowserTargetRegisteredDestination,
	)
	if err != nil {
		t.Fatal(err)
	}
	if rebound != "" {
		t.Fatalf("registered destination rebound = %q, want none", rebound)
	}
}

func TestSettleBrowserRouteKeepsExplicitTargetsExactAndRegisteredDestinationsBounded(t *testing.T) {
	for _, test := range []struct {
		name       string
		currentURL string
		targetKind app.BrowserTargetKind
	}{
		{
			name:       "explicit target rejects subdomain",
			currentURL: "https://wx.mail.qq.com/list/readtemplate",
			targetKind: app.BrowserTargetExplicitURL,
		},
		{
			name:       "registered destination rejects unrelated host",
			currentURL: "https://mail.qq.com.example.org/list/readtemplate",
			targetKind: app.BrowserTargetRegisteredDestination,
		},
		{
			name:       "registered destination rejects scheme change",
			currentURL: "http://wx.mail.qq.com/list/readtemplate",
			targetKind: app.BrowserTargetRegisteredDestination,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := settleBrowserRoute(
				"https://mail.qq.com/",
				test.currentURL,
				test.targetKind,
			); err == nil || !strings.Contains(err.Error(), "browser_route_diverged") {
				t.Fatalf("route error = %v", err)
			}
		})
	}
}
