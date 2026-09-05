package emailautomation

import (
	"context"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type fakeScriptRunner struct {
	probeResult ProbeResult
	probeErr    error
	probeCalls  []string
	sendResult  SendResult
	sendErr     error
	sendCalls   []SendRequest
}

type fakeLoginBrowser struct {
	loginErr  error
	loginURLs []string
}

func (f *fakeLoginBrowser) OpenLogin(_ context.Context, provider Provider) error {
	f.loginURLs = append(f.loginURLs, provider.LoginURL)
	return f.loginErr
}

func (f *fakeScriptRunner) Probe(_ context.Context, provider Provider, invocationID string, expectedGeneration uint64) (ProbeResult, error) {
	f.probeCalls = append(f.probeCalls, provider.ID+":"+invocationID)
	if expectedGeneration != 0 {
		panic("controller probe unexpectedly supplied a generation")
	}
	return f.probeResult, f.probeErr
}

func (f *fakeScriptRunner) Send(_ context.Context, _ Provider, request SendRequest) (SendResult, error) {
	f.sendCalls = append(f.sendCalls, request)
	return f.sendResult, f.sendErr
}

func TestControllerLoginCheckAndAdmissionPersistOnlyBoundedStatus(t *testing.T) {
	st := store.NewMemoryStore()
	browser := &fakeLoginBrowser{}
	checkedAt := time.Date(2026, 9, 3, 8, 0, 0, 0, time.UTC)
	runner := &fakeScriptRunner{probeResult: ProbeResult{
		Provider: app.EmailProviderGmail, AccountHint: "a***@gmail.com", Generation: 11, Revision: 1, CheckedAt: checkedAt,
	}}
	controller := NewController(st, DefaultRegistry(), browser, runner)

	opened, err := controller.OpenLoginBrowser(t.Context(), "owner-email", "owner-email", app.EmailProviderGmail)
	if err != nil {
		t.Fatal(err)
	}
	if !opened.Enabled || opened.State != app.EmailStateLoginRequired || len(browser.loginURLs) != 1 || browser.loginURLs[0] != "https://mail.google.com/" {
		t.Fatalf("opened status = %#v browser=%#v", opened, browser)
	}
	checked, err := controller.Check(t.Context(), "owner-email", "owner-email", app.EmailProviderGmail)
	if err != nil {
		t.Fatal(err)
	}
	if checked.State != app.EmailStateReady || checked.AccountHint != "a***@gmail.com" || checked.Version != 2 || checked.LastCheckedAt == nil {
		t.Fatalf("checked status = %#v", checked)
	}

	updated, err := controller.Update(t.Context(), "owner-email", "owner-email", app.EmailProviderGmail, UpdateProviderInput{
		Default: boolPointer(true), ExpectedVersion: checked.Version,
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := controller.Admit(t.Context(), "owner-email", "send a message")
	if err != nil {
		t.Fatal(err)
	}
	if binding.Provider != app.EmailProviderGmail || binding.Account != app.EmailAccountDefault || binding.AccountHint != "a***@gmail.com" ||
		binding.SettingVersion != updated.Version+1 || binding.BrowserCredentialGeneration != 11 || binding.ProbeRevision != 1 || binding.SendScriptRevision != 1 ||
		!binding.ValidatedAt.Equal(checkedAt) {
		t.Fatalf("admission binding = %#v", binding)
	}
	stored, ok, err := st.GetEmailProviderSetting(t.Context(), "owner-email", app.EmailProviderGmail)
	if err != nil || !ok || stored.AccountHint != "a***@gmail.com" || stored.ErrorCode != "" || stored.State != app.EmailStateReady {
		t.Fatalf("stored setting = %#v ok=%t err=%v", stored, ok, err)
	}
}

func TestControllerRejectsAmbiguousInvalidAndStaleAdmission(t *testing.T) {
	st := store.NewMemoryStore()
	checkedAt := time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC)
	runner := &fakeScriptRunner{probeResult: ProbeResult{
		Provider: app.EmailProviderGmail, AccountHint: "a***@gmail.com", Generation: 12, Revision: 1, CheckedAt: checkedAt,
	}, sendResult: SendResult{Provider: app.EmailProviderGmail, Status: "sent"}}
	controller := NewController(st, DefaultRegistry(), &fakeLoginBrowser{}, runner)
	gmail, err := controller.Update(t.Context(), "owner-email", "owner-email", app.EmailProviderGmail, UpdateProviderInput{
		Enabled: boolPointer(true), Default: boolPointer(true), ExpectedVersion: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Update(t.Context(), "owner-email", "owner-email", app.EmailProviderOutlook, UpdateProviderInput{
		Enabled: boolPointer(true), ExpectedVersion: 0,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Admit(t.Context(), "owner-email", "use Gmail and Outlook"); ErrorCode(err) != CodeAccountAmbiguous || len(runner.probeCalls) != 0 {
		t.Fatalf("ambiguous admission error=%v code=%q probes=%v", err, ErrorCode(err), runner.probeCalls)
	}

	runner.probeResult.Provider = app.EmailProviderOutlook
	if _, err := controller.Admit(t.Context(), "owner-email", "use Gmail"); ErrorCode(err) != CodeScriptInvalidOutput {
		t.Fatalf("invalid probe error=%v code=%q", err, ErrorCode(err))
	}
	runner.probeResult.Provider = app.EmailProviderGmail
	binding, err := controller.Admit(t.Context(), "owner-email", "use Gmail")
	if err != nil {
		t.Fatal(err)
	}
	request := SendRequest{
		Provider: binding.Provider, Account: binding.Account, Recipient: "alice@example.com", Body: "body", InvocationID: "send:controller",
		BrowserCredentialGeneration: binding.BrowserCredentialGeneration, ProbeRevision: binding.ProbeRevision,
		ScriptRevision: binding.SendScriptRevision, SettingVersion: binding.SettingVersion,
	}
	if _, err := controller.SendForOwner(t.Context(), "owner-email", request); err != nil || len(runner.sendCalls) != 1 {
		t.Fatalf("fresh send err=%v calls=%#v", err, runner.sendCalls)
	}
	current, ok, err := st.GetEmailProviderSetting(t.Context(), "owner-email", app.EmailProviderGmail)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if _, err := controller.Update(t.Context(), "owner-email", "owner-email", app.EmailProviderGmail, UpdateProviderInput{
		Default: boolPointer(false), ExpectedVersion: current.Version,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.SendForOwner(t.Context(), "owner-email", request); ErrorCode(err) != CodeAdmissionStale || len(runner.sendCalls) != 1 {
		t.Fatalf("stale send error=%v code=%q calls=%#v", err, ErrorCode(err), runner.sendCalls)
	}
	if gmail.Version != 1 {
		t.Fatalf("initial Gmail version = %d", gmail.Version)
	}
}

func boolPointer(value bool) *bool { return &value }
