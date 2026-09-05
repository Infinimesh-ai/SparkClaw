package emailautomation

import (
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestRegistryMatchesProviderNamesWithoutASCIISubstringCollisions(t *testing.T) {
	registry := DefaultRegistry()
	for _, test := range []struct {
		request string
		want    []string
	}{
		{request: "请用 QQ邮箱 给同事发邮件", want: []string{app.EmailProviderQQMail}},
		{request: "Send it through Gmail", want: []string{app.EmailProviderGmail}},
		{request: "Use Outlook Mail", want: []string{app.EmailProviderOutlook}},
		{request: "This outlookish theme is unrelated", want: nil},
		{request: "Compare Gmail and Outlook", want: []string{app.EmailProviderGmail, app.EmailProviderOutlook}},
	} {
		t.Run(test.request, func(t *testing.T) {
			matches := registry.MatchRequest(test.request)
			if len(matches) != len(test.want) {
				t.Fatalf("matches = %#v, want %v", matches, test.want)
			}
			for index := range matches {
				if matches[index].ID != test.want[index] {
					t.Fatalf("matches = %#v, want %v", matches, test.want)
				}
			}
		})
	}
}

func TestRegistryRejectsAmbiguousAliasesAndClonesRegistrations(t *testing.T) {
	script := func(id string) Script {
		return Script{ID: id, Revision: 1, Timeout: time.Second}
	}
	providers := []Provider{
		{ID: app.EmailProviderGmail, DisplayName: "Gmail", LoginURL: "https://mail.google.com/", Aliases: []string{"shared"}, Probe: script("gmail-probe"), Send: script("gmail-send")},
		{ID: app.EmailProviderOutlook, DisplayName: "Outlook", LoginURL: "https://outlook.live.com/mail/", Aliases: []string{"shared"}, Probe: script("outlook-probe"), Send: script("outlook-send")},
	}
	if _, err := NewRegistry(providers); err == nil {
		t.Fatal("ambiguous provider alias was accepted")
	}

	providers[1].Aliases = []string{"outlook web"}
	registry, err := NewRegistry(providers)
	if err != nil {
		t.Fatal(err)
	}
	listed := registry.List()
	listed[0].Aliases[0] = "mutated"
	again := registry.List()
	if again[0].Aliases[0] == "mutated" {
		t.Fatal("registry list exposed mutable provider state")
	}
}
