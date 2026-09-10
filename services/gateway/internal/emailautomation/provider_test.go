package emailautomation

import (
	"slices"
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
		{ID: app.EmailProviderGmail, DisplayName: "Gmail", Aliases: []string{"shared"}, Probe: script("gmail-probe"), Send: script("gmail-send"), Read: script("gmail-read")},
		{ID: app.EmailProviderOutlook, DisplayName: "Outlook", Aliases: []string{"shared"}, Probe: script("outlook-probe"), Send: script("outlook-send"), Read: script("outlook-read")},
	}
	for i := range providers {
		providers[i].Discover = script(providers[i].ID + "-discover")
		providers[i].Capture = script(providers[i].ID + "-capture")
		providers[i].EnumerateThread = script(providers[i].ID + "-enumerate")
		providers[i].CollectPage = script(providers[i].ID + "-collect-page")
		providers[i].MarkRead = script(providers[i].ID + "-mark-read")
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

func TestDefaultRegistryIsGeneratedFromTheControllerContract(t *testing.T) {
	var contract providerScriptContractFile
	if err := decodeStrictJSON(providerScriptContract, &contract); err != nil {
		t.Fatalf("embedded provider_scripts.json is invalid: %v", err)
	}
	registry := DefaultRegistry()
	listed := registry.List()
	wantIDs := app.EmailProviderIDs()
	slices.Sort(wantIDs)
	if len(listed) != len(wantIDs) {
		t.Fatalf("registry lists %d providers, want %v", len(listed), wantIDs)
	}
	for index, provider := range listed {
		if provider.ID != wantIDs[index] || provider.DisplayName != app.EmailProviderDisplayName(provider.ID) {
			t.Fatalf("provider %d = %#v, want %s", index, provider, wantIDs[index])
		}
		for operation, script := range map[string]Script{"probe": provider.Probe, "send": provider.Send, "read": provider.Read, "discover": provider.Discover, "capture": provider.Capture, "enumerate_thread": provider.EnumerateThread, "mark_read": provider.MarkRead, "collect_page": provider.CollectPage} {
			index := slices.IndexFunc(contract.Scripts, func(entry providerScriptContractEntry) bool {
				return entry.Provider == provider.ID && entry.Operation == operation
			})
			if index < 0 {
				t.Fatalf("contract has no %s script for %s", operation, provider.ID)
			}
			entry := contract.Scripts[index]
			if script.ID != entry.ScriptID || script.Revision != entry.Revision || script.Timeout != time.Duration(entry.TimeoutMS)*time.Millisecond {
				t.Fatalf("%s %s script %#v does not match contract entry %#v", provider.ID, operation, script, entry)
			}
		}
	}
	if len(contract.Scripts) != 8*len(wantIDs) {
		t.Fatalf("contract lists %d scripts for %d providers", len(contract.Scripts), len(wantIDs))
	}
}

func TestRegistryFromContractRejectsIncompleteOrForeignScripts(t *testing.T) {
	for name, raw := range map[string]string{
		"missing send":     `{"schema_version":1,"scripts":[{"provider":"gmail","operation":"probe","script_id":"gmail.login_probe","revision":1,"timeout_ms":1000}]}`,
		"unknown provider": `{"schema_version":1,"scripts":[{"provider":"yahoo","operation":"probe","script_id":"yahoo.login_probe","revision":1,"timeout_ms":1000}]}`,
		"schema":           `{"schema_version":2,"scripts":[]}`,
		"unknown field":    `{"schema_version":1,"scripts":[],"login_url":"https://example.com"}`,
	} {
		if _, err := registryFromContract([]byte(raw)); err == nil {
			t.Errorf("%s contract was accepted", name)
		}
	}
}
