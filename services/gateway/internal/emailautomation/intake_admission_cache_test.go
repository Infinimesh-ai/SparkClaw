package emailautomation

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type intakeCacheBrowser struct {
	*fakePlaywrightController
	onStatus func()
}

func (b *intakeCacheBrowser) Status(ctx context.Context) browsercontrol.Status {
	if b.onStatus != nil {
		b.onStatus()
	}
	return b.fakePlaywrightController.Status(ctx)
}

func intakeCacheFixture(t *testing.T) (*Controller, *intakeCacheBrowser, *store.MemoryStore, *time.Time) {
	t.Helper()
	now := time.Date(2026, 9, 8, 8, 0, 0, 0, time.UTC)
	checkedAt := now
	st := store.NewMemoryStore()
	_, err := st.UpdateEmailProviderSetting(t.Context(), app.EmailProviderSetting{OwnerID: "owner", Provider: "gmail", Account: "default", Enabled: true, State: app.EmailStateReady, LastCheckedAt: &checkedAt}, 0)
	if err != nil {
		t.Fatal(err)
	}
	browser := &intakeCacheBrowser{fakePlaywrightController: &fakePlaywrightController{status: browsercontrol.Status{Configured: true, CredentialGeneration: 7}}}
	runner := NewPlaywrightRunner(browser)
	runner.now = func() time.Time { return now }
	controller := NewController(st, DefaultRegistry(), nil, runner)
	controller.now = func() time.Time { return now }
	return controller, browser, st, &now
}

func TestIntakeProbeCacheReusesProductionProofWithoutChangingSendApproval(t *testing.T) {
	controller, browser, st, now := intakeCacheFixture(t)
	approved, err := controller.Admit(t.Context(), "owner", "gmail")
	if err != nil {
		t.Fatal(err)
	}
	before, _, _ := st.GetEmailProviderSetting(t.Context(), "owner", "gmail")
	for lane := 0; lane < 3; lane++ {
		binding, err := controller.AdmitIntake(t.Context(), "owner", "gmail")
		if err != nil || binding.SettingVersion != approved.SettingVersion || binding.BrowserCredentialGeneration != approved.BrowserCredentialGeneration {
			t.Fatalf("intake binding changed send approval: %#v %v", binding, err)
		}
		*now = now.Add(15 * time.Second)
	}
	if len(browser.requests) != 2 || browser.requests[0].Operation != "probe" || browser.requests[1].Operation != "probe" {
		t.Fatalf("three intake lanes did not share one intake probe: %#v", browser.requests)
	}
	after, _, _ := st.GetEmailProviderSetting(t.Context(), "owner", "gmail")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("intake rewrote sending configuration, version or last-checked time")
	}
	_, err = controller.SendForOwner(t.Context(), "owner", SendRequest{Provider: "gmail", Account: "default", InvocationID: "approved-send", Recipient: "recipient@example.test", Subject: "test", Body: "test", SettingVersion: approved.SettingVersion, BrowserCredentialGeneration: approved.BrowserCredentialGeneration, ProbeRevision: approved.ProbeRevision, ScriptRevision: approved.SendScriptRevision})
	if err != nil || browser.requests[len(browser.requests)-1].Operation != "send" {
		t.Fatalf("existing send approval was invalidated: %v", err)
	}
	if _, err := controller.Admit(t.Context(), "owner", "gmail"); err != nil || browser.requests[len(browser.requests)-1].Operation != "probe" || len(browser.requests) != 4 {
		t.Fatalf("human send admission reused the intake cache: %v", err)
	}
}

func TestIntakeProbeCacheExpiresWithoutSlidingOnHits(t *testing.T) {
	controller, browser, _, now := intakeCacheFixture(t)
	first, err := controller.AdmitIntake(t.Context(), "owner", "gmail")
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(intakeProbeTTL - time.Second)
	second, err := controller.AdmitIntake(t.Context(), "owner", "gmail")
	if err != nil || len(browser.requests) != 1 || !second.ValidatedAt.Equal(first.ValidatedAt) {
		t.Fatalf("fresh proof was not reused: %v", err)
	}
	*now = now.Add(time.Second)
	third, err := controller.AdmitIntake(t.Context(), "owner", "gmail")
	if err != nil || len(browser.requests) != 2 || !third.ValidatedAt.Equal(*now) {
		t.Fatalf("TTL extended on a cache hit: %v", err)
	}
	*now = now.Add(-time.Second)
	if _, err := controller.AdmitIntake(t.Context(), "owner", "gmail"); err != nil || len(browser.requests) != 3 {
		t.Fatalf("clock rollback reused a future proof: %v", err)
	}
}

func TestIntakeProbeCacheRejectsChangedOrUnavailableCredentialGeneration(t *testing.T) {
	controller, browser, _, _ := intakeCacheFixture(t)
	if _, err := controller.AdmitIntake(t.Context(), "owner", "gmail"); err != nil {
		t.Fatal(err)
	}
	browser.status.CredentialGeneration = 8
	binding, err := controller.AdmitIntake(t.Context(), "owner", "gmail")
	if err != nil || binding.BrowserCredentialGeneration != 8 || len(browser.requests) != 2 {
		t.Fatalf("changed credential did not require a new probe: %#v %v", binding, err)
	}
	browser.status.Configured = false
	if _, err := controller.AdmitIntake(t.Context(), "owner", "gmail"); ErrorCode(err) != app.ToolErrorEmailNotConfigured || len(browser.requests) != 2 {
		t.Fatalf("cached proof accepted removed credentials: %v", err)
	}
	browser.status.Configured = true
	if _, err := controller.AdmitIntake(t.Context(), "owner", "gmail"); err != nil || len(browser.requests) != 3 {
		t.Fatalf("credential failure did not evict its proof: %v", err)
	}
}

func TestIntakeProbeCacheInvalidatesSettingChangesAndDisabledProviders(t *testing.T) {
	controller, browser, st, _ := intakeCacheFixture(t)
	first, err := controller.AdmitIntake(t.Context(), "owner", "gmail")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := controller.Update(t.Context(), "owner", "owner", "gmail", UpdateProviderInput{Default: boolPointer(true), ExpectedVersion: first.SettingVersion})
	if err != nil {
		t.Fatal(err)
	}
	second, err := controller.AdmitIntake(t.Context(), "owner", "gmail")
	if err != nil || second.SettingVersion != updated.Version || len(browser.requests) != 2 {
		t.Fatalf("setting change reused the old proof: %v", err)
	}
	setting, _, _ := st.GetEmailProviderSetting(t.Context(), "owner", "gmail")
	setting.State = app.EmailStateLoginRequired
	if _, err := st.UpdateEmailProviderSetting(t.Context(), setting, setting.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.AdmitIntake(t.Context(), "owner", "gmail"); err != nil || len(browser.requests) != 3 {
		t.Fatalf("expired login was not revalidated with a fresh probe: %v", err)
	}
	setting, _, _ = st.GetEmailProviderSetting(t.Context(), "owner", "gmail")
	setting.Enabled = false
	setting.Default = false
	if _, err := st.UpdateEmailProviderSetting(t.Context(), setting, setting.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.AdmitIntake(t.Context(), "owner", "gmail"); ErrorCode(err) != app.ToolErrorEmailNotConfigured || len(browser.requests) != 3 {
		t.Fatalf("cached proof accepted disabled setting: %v", err)
	}
}

func TestIntakeProbeCacheSeparatesOwnersAndProviders(t *testing.T) {
	controller, browser, st, now := intakeCacheFixture(t)
	for _, key := range []intakeProbeKey{{ownerID: "other", providerID: "gmail"}, {ownerID: "owner", providerID: "outlook"}} {
		if _, err := st.UpdateEmailProviderSetting(t.Context(), app.EmailProviderSetting{OwnerID: key.ownerID, Provider: key.providerID, Account: "default", Enabled: true, State: app.EmailStateReady, LastCheckedAt: now}, 0); err != nil {
			t.Fatal(err)
		}
	}
	for index, key := range []intakeProbeKey{{ownerID: "owner", providerID: "gmail"}, {ownerID: "other", providerID: "gmail"}, {ownerID: "owner", providerID: "outlook"}, {ownerID: "owner", providerID: "gmail"}} {
		raw, _ := json.Marshal(map[string]any{"schema_version": 1, "status": "ready", "provider": key.providerID})
		browser.result = browsercontrol.ScriptExecutionResult{State: "completed", CredentialGeneration: 7, Result: raw}
		if _, err := controller.AdmitIntake(t.Context(), key.ownerID, key.providerID); err != nil {
			t.Fatal(err)
		}
		expected := min(index+1, 3)
		if len(browser.requests) != expected {
			t.Fatalf("scope %v reused another scope's proof", key)
		}
	}
}

func TestIntakeProbeCacheRechecksConfigurationAfterCredentialStatus(t *testing.T) {
	controller, browser, st, _ := intakeCacheFixture(t)
	first, err := controller.AdmitIntake(t.Context(), "owner", "gmail")
	if err != nil {
		t.Fatal(err)
	}
	browser.onStatus = func() {
		browser.onStatus = nil
		setting, _, _ := st.GetEmailProviderSetting(t.Context(), "owner", "gmail")
		setting.Default = true
		if _, err := st.UpdateEmailProviderSetting(t.Context(), setting, first.SettingVersion); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := controller.AdmitIntake(t.Context(), "owner", "gmail"); ErrorCode(err) != app.ToolErrorEmailAdmissionStale || len(browser.requests) != 1 {
		t.Fatalf("configuration changed during cached admission was accepted: %v", err)
	}
	if _, err := controller.AdmitIntake(t.Context(), "owner", "gmail"); err != nil || len(browser.requests) != 2 {
		t.Fatalf("stale cache was not evicted: %v", err)
	}
}

type intakeAccountRepository struct {
	Repository
	account string
}

func (r *intakeAccountRepository) GetEmailProviderSetting(ctx context.Context, ownerID, providerID string) (app.EmailProviderSetting, bool, error) {
	setting, exists, err := r.Repository.GetEmailProviderSetting(ctx, ownerID, providerID)
	setting.Account = r.account
	return setting, exists, err
}

func TestIntakeProbeCacheAlsoFencesAccountValueAndFailedProbes(t *testing.T) {
	controller, browser, st, now := intakeCacheFixture(t)
	repository := &intakeAccountRepository{Repository: st, account: "default"}
	controller.store = repository
	if _, err := controller.AdmitIntake(t.Context(), "owner", "gmail"); err != nil {
		t.Fatal(err)
	}
	repository.account = "changed-binding"
	binding, err := controller.AdmitIntake(t.Context(), "owner", "gmail")
	if err != nil || binding.Account != repository.account || len(browser.requests) != 2 {
		t.Fatalf("account change reused a proof even with the same version: %v", err)
	}
	*now = now.Add(intakeProbeTTL)
	browser.result = browsercontrol.ScriptExecutionResult{State: "failed", CredentialGeneration: 7, Result: json.RawMessage(`{"schema_version":1,"status":"error","provider":"gmail","code":"email_login_required"}`)}
	if _, err := controller.AdmitIntake(t.Context(), "owner", "gmail"); err == nil {
		t.Fatal("failed refresh fell back to expired proof")
	}
	browser.result = browsercontrol.ScriptExecutionResult{}
	if _, err := controller.AdmitIntake(t.Context(), "owner", "gmail"); err != nil || len(browser.requests) != 4 {
		t.Fatalf("failed probe was cached: %v", err)
	}
}

func TestIntakeProbeCacheStillRequiresCurrentDiscoveryAccount(t *testing.T) {
	controller, browser, _, _ := intakeCacheFixture(t)
	for range 2 {
		if _, err := controller.AdmitIntake(t.Context(), "owner", "gmail"); err != nil {
			t.Fatal(err)
		}
	}
	if len(browser.requests) != 1 {
		t.Fatal("fixture did not reuse its probe")
	}
	browser.result = browsercontrol.ScriptExecutionResult{State: "completed", CredentialGeneration: 7, Result: json.RawMessage(`{"schema_version":1,"provider":"gmail","status":"partial","account_address":"other@example.test","candidates":[],"coverage":{"scope":"inbound_received","lane":"recent_inbound","scan_complete":false,"scanned_rows":0,"unsupported_rows":0,"limited":true,"reason":"network_page_continues"},"observed_at":"2026-09-08T08:00:00Z"}`)}
	request := validReadRequest()
	start := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	request.Discovery = &app.EmailDiscoveryOptions{Lane: "recent_inbound", AccountAddress: "owner@example.test", IntervalStart: start, IntervalEnd: start.Add(time.Hour), Limit: 50}
	if _, err := controller.DiscoverForOwner(t.Context(), "owner", request); ErrorCode(err) != app.ToolErrorEmailScriptInvalidOutput {
		t.Fatalf("cached proof accepted a different browser account: %v", err)
	}
	if len(browser.requests) != 2 || browser.requests[1].Operation != "discover" {
		t.Fatal("cached admission bypassed current-account discovery")
	}
}

func TestIntakeProbeCacheConcurrentCallsShareOneProbe(t *testing.T) {
	controller, browser, _, _ := intakeCacheFixture(t)
	var workers sync.WaitGroup
	errors := make(chan error, 12)
	for range 12 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, err := controller.AdmitIntake(t.Context(), "owner", "gmail")
			errors <- err
		}()
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(browser.requests) != 1 {
		t.Fatalf("concurrent intake launched %d redundant probes", len(browser.requests))
	}
}
