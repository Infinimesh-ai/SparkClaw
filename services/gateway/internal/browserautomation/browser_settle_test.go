package browserautomation

import (
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

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
