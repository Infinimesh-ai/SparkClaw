package emailautomation

import (
	"encoding/json"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"strings"
	"testing"
	"time"
)

func TestDiscoveryAdmissionAndSpecifiedCaptureRequireOwnerBinding(t *testing.T) {
	st := store.NewMemoryStore()
	at := time.Now().UTC()
	setting, err := st.UpdateEmailProviderSetting(t.Context(), app.EmailProviderSetting{OwnerID: "owner", Provider: "gmail", Account: "default", Enabled: true, State: app.EmailStateReady, LastCheckedAt: &at}, 0)
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeScriptRunner{}
	controller := NewController(st, DefaultRegistry(), nil, runner)
	request := validReadRequest()
	request.SettingVersion = setting.Version
	if _, err := controller.DiscoverForOwner(t.Context(), "owner", request); err != nil {
		t.Fatal(err)
	}
	if len(runner.discoveryCalls) != 1 || runner.discoveryCalls[0].OwnerScope == request.OwnerScope {
		t.Fatal("owner scope was not derived")
	}
	if _, err := controller.DiscoverForOwner(t.Context(), "other", request); ErrorCode(err) != app.ToolErrorEmailAdmissionStale {
		t.Fatal(err)
	}
	if len(runner.discoveryCalls) != 1 {
		t.Fatal("stale admission reached browser")
	}
	if _, err := controller.CaptureForOwner(t.Context(), "owner", request); ErrorCode(err) != app.ToolErrorEmailInvalidInput || len(runner.readCalls) != 0 {
		t.Fatal("background capture chose first unread")
	}
	if !validMailTarget(app.EmailCaptureTarget{AccountAddress: "owner@example.test", ProviderMessageID: "qq~fixture", ProviderSelectionID: "qq~fixture"}) {
		t.Fatal("valid QQ locator rejected")
	}
}

func TestDiscoveryRejectsMisleadingCoverageAndCrossAccountTargets(t *testing.T) {
	provider, _ := DefaultRegistry().Get(app.EmailProviderGmail)
	valid := `{"schema_version":1,"provider":"gmail","status":"listed","account_address":"owner@example.test","candidates":[{"account_address":"owner@example.test","provider_message_id":"a","provider_selection_id":"a"}],"coverage":{"scope":"inbox_unread","scan_complete":false,"scanned_rows":1,"unsupported_rows":0,"limited":true},"observed_at":"2026-09-08T00:00:00Z"}`
	for name, raw := range map[string]string{"valid": valid, "false empty": strings.Replace(valid, `"listed"`, `"empty"`, 1), "false complete": strings.Replace(valid, `"scan_complete":false`, `"scan_complete":true`, 1), "cross account": strings.Replace(valid, `"account_address":"owner@example.test"`, `"account_address":"other@example.test"`, 1), "missing candidates": strings.Replace(valid, `"candidates":[{"account_address":"owner@example.test","provider_message_id":"a","provider_selection_id":"a"}],`, ``, 1)} {
		t.Run(name, func(t *testing.T) {
			controller := &fakePlaywrightController{status: browsercontrol.Status{Configured: true, CredentialGeneration: 7}, result: browsercontrol.ScriptExecutionResult{State: "completed", CredentialGeneration: 7, Result: json.RawMessage(raw)}}
			_, err := NewPlaywrightRunner(controller).Discover(t.Context(), provider, validReadRequest())
			if name == "valid" {
				if err != nil {
					t.Fatal(err)
				}
				if controller.requests[0].Operation != "discover" {
					t.Fatal("wrong script")
				}
			} else if ErrorCode(err) != app.ToolErrorEmailScriptInvalidOutput {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestSpecifiedCaptureUsesCaptureContractAndCannotReturnEmpty(t *testing.T) {
	provider, _ := DefaultRegistry().Get(app.EmailProviderGmail)
	controller := &fakePlaywrightController{status: browsercontrol.Status{Configured: true, CredentialGeneration: 7}, result: browsercontrol.ScriptExecutionResult{State: "completed", CredentialGeneration: 7, Result: json.RawMessage(`{"schema_version":1,"provider":"gmail","status":"empty","capture":null}`)}}
	request := validReadRequest()
	request.Target = &app.EmailCaptureTarget{AccountAddress: "owner@example.test", ProviderMessageID: "a", ProviderSelectionID: "a"}
	_, err := NewPlaywrightRunner(controller).Read(t.Context(), provider, request)
	if ErrorCode(err) != app.ToolErrorEmailScriptInvalidOutput || len(controller.requests) != 1 || controller.requests[0].Operation != "capture" {
		t.Fatalf("err=%v", err)
	}
	request.Target.ProviderMessageID = `a"]`
	controller.requests = nil
	_, err = NewPlaywrightRunner(controller).Read(t.Context(), provider, request)
	if ErrorCode(err) != app.ToolErrorEmailInvalidInput || len(controller.requests) != 0 {
		t.Fatalf("invalid target reached browser: %v", err)
	}
}

func TestDiscoveryCoverageAcceptsBoundedQQFolderCursorAndRejectsInvalidMetadata(t *testing.T) {
	target := app.EmailCaptureTarget{AccountAddress: "owner@example.test", ProviderMessageID: "qq~message", ProviderSelectionID: "qq~message", Folder: "qq:2000"}
	if !validMailTarget(target) || !validContinuation("q1:eyJiIjoiYWJjIn0") {
		t.Fatal("ordinary QQ folder or bounded cursor rejected")
	}
	for _, cursor := range []string{"q1:", "q1:" + strings.Repeat("a", 1001), "q1:../../secret", "bad:123"} {
		if validContinuation(cursor) {
			t.Fatal("unbounded or unsafe cursor accepted")
		}
	}
	at := time.Now().UTC()
	coverage := app.EmailDiscoveryCoverage{Scope: "inbox_loaded", ScannedRows: 1, Limited: true, OldestObservedAt: &at, Ordering: "qq_totime", Continuation: "q1:eyJiIjoiYWJjIn0"}
	if !validCoverage(coverage, 1) {
		t.Fatal("valid receipt metadata rejected")
	}
	coverage.Ordering = "raw private value"
	if validCoverage(coverage, 1) {
		t.Fatal("arbitrary ordering metadata accepted")
	}
	coverage.Ordering = "qq_totime"
	coverage.OldestObservedAt = &time.Time{}
	if validCoverage(coverage, 1) {
		t.Fatal("zero receipt timestamp accepted")
	}
}

func TestProductionDiscoveryRunnerAcceptsUnreadWithoutRecentInterval(t *testing.T) {
	for _, providerID := range []string{"qq_mail", "outlook", "gmail"} {
		t.Run(providerID, func(t *testing.T) {
			provider, _ := DefaultRegistry().Get(providerID)
			request := validReadRequest()
			request.Provider = providerID
			request.ScriptRevision = provider.Discover.Revision
			request.Discovery = &app.EmailDiscoveryOptions{Lane: "unread", AccountAddress: "owner@example.test", Limit: 50}
			value := app.EmailDiscoveryResult{SchemaVersion: 1, Provider: providerID, Status: "partial", AccountAddress: "owner@example.test", Candidates: []app.EmailCaptureTarget{{AccountAddress: "owner@example.test", ProviderMessageID: "mail", ProviderSelectionID: "mail", Folder: "inbox"}}, Coverage: app.EmailDiscoveryCoverage{Scope: "inbox_unread", Lane: "unread", ScannedRows: 1, Limited: true, Reason: "loaded_rows_only"}, ObservedAt: time.Now().UTC()}
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			browser := &fakePlaywrightController{status: browsercontrol.Status{Configured: true, CredentialGeneration: 7}, result: browsercontrol.ScriptExecutionResult{State: "completed", CredentialGeneration: 7, Result: raw}}
			runner := NewPlaywrightRunner(browser)
			result, err := runner.Discover(t.Context(), provider, request)
			if err != nil || len(result.Candidates) != 1 || len(browser.requests) != 1 {
				t.Fatalf("production unread request rejected before script: %v", err)
			}
			input, ok := browser.requests[0].Input.(map[string]any)["discovery"].(*app.EmailDiscoveryOptions)
			if !ok || input.Lane != "unread" || !input.IntervalStart.IsZero() || !input.IntervalEnd.IsZero() {
				t.Fatal("unread script request invented a time interval")
			}
			browser.requests = nil
			request.Discovery.Lane = "recent_inbound"
			if _, err := runner.Discover(t.Context(), provider, request); ErrorCode(err) != app.ToolErrorEmailInvalidInput || len(browser.requests) != 0 {
				t.Fatalf("recent request without interval reached script: %v", err)
			}
			request.Discovery.Lane = "unread"
			request.Discovery.AccountAddress = ""
			if _, err := runner.Discover(t.Context(), provider, request); ErrorCode(err) != app.ToolErrorEmailInvalidInput || len(browser.requests) != 0 {
				t.Fatalf("unbound unread request reached script: %v", err)
			}
		})
	}
}
