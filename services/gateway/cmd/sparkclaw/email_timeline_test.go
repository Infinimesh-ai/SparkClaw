package main

import (
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"testing"
)

func TestQualifiedEmailTimelineModes(t *testing.T) {
	modes := qualifiedEmailTimelineModes()
	if len(modes) != 3 {
		t.Fatal("unexpected qualified provider")
	}
	for _, provider := range []string{app.EmailProviderQQMail, app.EmailProviderGmail, app.EmailProviderOutlook} {
		if modes[provider] != app.EmailProviderModeTimeRange {
			t.Fatal("native range adapter is not wired")
		}
	}
	delete(modes, app.EmailProviderGmail)
	if qualifiedEmailTimelineModes()[app.EmailProviderGmail] != app.EmailProviderModeTimeRange {
		t.Fatal("shared mutable qualification map")
	}
}
