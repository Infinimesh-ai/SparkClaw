package emailautomation

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// signalStore exposes controllable mailbox observer state, as the production
// stores do through ListEmailMailboxes.
type signalStore struct {
	*store.MemoryStore
	boxes []app.EmailMailbox
	err   error
}

func (s *signalStore) ListEmailMailboxes(context.Context, string) ([]app.EmailMailbox, error) {
	return append([]app.EmailMailbox(nil), s.boxes...), s.err
}

type sendAdmissionHarness struct {
	controller *Controller
	browser    *fakePlaywrightController
	store      *signalStore
	now        *time.Time
	sends      int
}

func sendAdmissionFixture(t *testing.T, window time.Duration) *sendAdmissionHarness {
	t.Helper()
	now := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	st := &signalStore{MemoryStore: store.NewMemoryStore(), boxes: []app.EmailMailbox{{ID: "mailbox-1", Provider: "gmail", Active: true,
		BindingGeneration: 3, ObserverEpoch: "epoch-1", ObserverCredentialGeneration: 7, WatchEpoch: "watch-1", WatchState: "watching",
		LastNotificationReason: "gmail_topic_invalidation", LastNotificationAt: now.Add(-time.Minute)},
		{ID: "mailbox-other", Provider: "outlook", Active: true, WatchState: "watching"}}}
	checkedAt := now.Add(-time.Hour)
	if _, err := st.UpdateEmailProviderSetting(t.Context(), app.EmailProviderSetting{OwnerID: "owner", Provider: "gmail", Account: "default", Enabled: true, State: app.EmailStateReady, LastCheckedAt: &checkedAt}, 0); err != nil {
		t.Fatal(err)
	}
	browser := &fakePlaywrightController{status: browsercontrol.Status{Configured: true, CredentialGeneration: 7}}
	runner := NewPlaywrightRunner(browser)
	runner.now = func() time.Time { return now }
	controller := NewController(st, DefaultRegistry(), runner, runner).WithSendAdmissionReuse(window)
	controller.now = func() time.Time { return now }
	return &sendAdmissionHarness{controller: controller, browser: browser, store: st, now: &now}
}

func (h *sendAdmissionHarness) probes() int {
	count := 0
	for _, request := range h.browser.requests {
		if request.Operation == "probe" {
			count++
		}
	}
	return count
}

func (h *sendAdmissionHarness) admit(t *testing.T) AdmissionResult {
	t.Helper()
	binding, err := h.controller.Admit(t.Context(), "owner", "gmail")
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func (h *sendAdmissionHarness) send(binding AdmissionResult) (SendResult, error) {
	h.sends++
	return h.controller.SendForOwner(context.Background(), "owner", SendRequest{Provider: "gmail", Account: "default",
		InvocationID: "send-" + string(rune('a'+h.sends)), Recipient: "recipient@example.test", Subject: "test", Body: "test",
		SettingVersion: binding.SettingVersion, BrowserCredentialGeneration: binding.BrowserCredentialGeneration,
		ProbeRevision: binding.ProbeRevision, ScriptRevision: binding.SendScriptRevision})
}

func TestSendAdmissionReusesOneLoginCheckAcrossConsecutiveSends(t *testing.T) {
	h := sendAdmissionFixture(t, DefaultSendAdmissionReuse)
	var first AdmissionResult
	for index := 0; index < 5; index++ {
		binding := h.admit(t)
		if index == 0 {
			first = binding
		} else if binding != first {
			t.Fatalf("reused admission differs from the probed one: %#v != %#v", binding, first)
		}
		if result, err := h.send(binding); err != nil || result.Status != "sent" {
			t.Fatalf("send %d with reused admission failed: %#v %v", index, result, err)
		}
		*h.now = h.now.Add(20 * time.Second)
	}
	if got := h.probes(); got != 1 {
		t.Fatalf("five sends within the window ran %d login checks, want 1", got)
	}
	if !first.ValidatedAt.Equal(h.now.Add(-100 * time.Second)) {
		t.Fatalf("reuse must report the original check time, got %s", first.ValidatedAt)
	}
}

func TestSendAdmissionWindowIsMeasuredFromTheProbeAndNeverSlides(t *testing.T) {
	h := sendAdmissionFixture(t, 90*time.Second)
	first := h.admit(t)
	*h.now = h.now.Add(89 * time.Second)
	if h.admit(t) != first || h.probes() != 1 {
		t.Fatal("proof inside the window was not reused")
	}
	*h.now = h.now.Add(time.Second)
	if second := h.admit(t); h.probes() != 2 || !second.ValidatedAt.Equal(*h.now) {
		t.Fatalf("reuse extended the window: probes=%d", h.probes())
	}
	*h.now = h.now.Add(-time.Second)
	h.admit(t)
	if h.probes() != 3 {
		t.Fatal("clock rollback reused a proof from the future")
	}
}

func TestSendAdmissionDisabledWindowAlwaysChecks(t *testing.T) {
	for _, window := range []time.Duration{0, -time.Second} {
		h := sendAdmissionFixture(t, window)
		h.admit(t)
		h.admit(t)
		if h.probes() != 2 {
			t.Fatalf("window %s reused a login check", window)
		}
	}
	// NewController without the option keeps the original behavior.
	h := sendAdmissionFixture(t, 0)
	h.controller = NewController(h.store, DefaultRegistry(), nil, NewPlaywrightRunner(h.browser))
	h.admit(t)
	h.admit(t)
	if h.probes() != 2 {
		t.Fatal("default controller reused a login check")
	}
	if got := sendAdmissionFixture(t, time.Hour).controller.sendAdmissionWindow; got != MaxSendAdmissionReuse {
		t.Fatalf("window not clamped: %s", got)
	}
}

func TestSendAdmissionInvalidationSignalsForceAFullCheck(t *testing.T) {
	setting := func(h *sendAdmissionHarness, mutate func(*app.EmailProviderSetting)) {
		current, _, _ := h.store.GetEmailProviderSetting(context.Background(), "owner", "gmail")
		mutate(&current)
		if _, err := h.store.UpdateEmailProviderSetting(context.Background(), current, current.Version); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name   string
		signal func(*testing.T, *sendAdmissionHarness)
	}{
		{"credential generation changed", func(_ *testing.T, h *sendAdmissionHarness) { h.browser.status.CredentialGeneration = 8 }},
		{"setting version changed", func(_ *testing.T, h *sendAdmissionHarness) {
			setting(h, func(s *app.EmailProviderSetting) { s.Default = true })
		}},
		{"setting error recorded", func(_ *testing.T, h *sendAdmissionHarness) {
			setting(h, func(s *app.EmailProviderSetting) { s.ErrorCode = string(app.ToolErrorEmailLoginRequired) })
		}},
		{"owner reopened login", func(t *testing.T, h *sendAdmissionHarness) {
			if _, err := h.controller.OpenLoginBrowser(t.Context(), "owner", "owner", "gmail"); err != nil {
				t.Fatal(err)
			}
		}},
		{"observer re-registered (page reload)", func(_ *testing.T, h *sendAdmissionHarness) { h.store.boxes[0].ObserverEpoch = "epoch-2" }},
		{"observer resync for a replaced document", func(_ *testing.T, h *sendAdmissionHarness) {
			h.store.boxes[0].LastNotificationReason, h.store.boxes[0].LastNotificationAt = "document_replaced", *h.now
		}},
		{"watch restarted", func(_ *testing.T, h *sendAdmissionHarness) { h.store.boxes[0].WatchEpoch = "watch-2" }},
		{"watch reports degraded", func(_ *testing.T, h *sendAdmissionHarness) { h.store.boxes[0].WatchState = "degraded" }},
		{"mailbox needs attention", func(_ *testing.T, h *sendAdmissionHarness) { h.store.boxes[0].ErrorCode = "email_sync_failed" }},
		{"mailbox state unreadable", func(_ *testing.T, h *sendAdmissionHarness) { h.store.err = errors.New("store unavailable") }},
		{"send outcome unknown", func(t *testing.T, h *sendAdmissionHarness) {
			binding := h.admit(t) // reused
			h.browser.err = &browsercontrol.Error{Code: browsercontrol.CodeControllerUnavailable, Retryable: true}
			if _, err := h.send(binding); ErrorCode(err) != app.ToolErrorEmailSendOutcomeUnknown {
				t.Fatalf("unexpected send result: %v", err)
			}
			h.browser.err = nil
		}},
		{"send not dispatched", func(t *testing.T, h *sendAdmissionHarness) {
			binding := h.admit(t)
			h.browser.result = browsercontrol.ScriptExecutionResult{State: "failed", CredentialGeneration: 7,
				Result: json.RawMessage(`{"schema_version":1,"status":"error","provider":"gmail","code":"send_not_dispatched"}`)}
			if _, err := h.send(binding); err == nil {
				t.Fatal("not-dispatched send reported success")
			}
			h.browser.result = browsercontrol.ScriptExecutionResult{}
		}},
		{"intake login check failed", func(t *testing.T, h *sendAdmissionHarness) {
			h.browser.err = &browsercontrol.Error{Code: browsercontrol.CodeScriptTimeout}
			if _, err := h.controller.AdmitIntake(t.Context(), "owner", "gmail"); err == nil {
				t.Fatal("intake probe failure was not reported")
			}
			h.browser.err = nil
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := sendAdmissionFixture(t, DefaultSendAdmissionReuse)
			h.admit(t)
			*h.now = h.now.Add(10 * time.Second)
			h.admit(t)
			if h.probes() != 1 {
				t.Fatal("baseline did not reuse the login check")
			}
			tc.signal(t, h)
			probes := h.probes()
			// Signals that are still present (a mailbox error, an unreadable
			// state) also make the new probe's result non-reusable.
			h.admit(t)
			if h.probes() != probes+1 {
				t.Fatalf("%s: admission reused the earlier login check", tc.name)
			}
		})
	}
}

func TestSendAdmissionNeverReusesWhileTheObserverReportsLoginRequired(t *testing.T) {
	h := sendAdmissionFixture(t, DefaultSendAdmissionReuse)
	h.store.boxes[0].WatchState = app.EmailStateLoginRequired
	h.admit(t)
	h.admit(t)
	if h.probes() != 2 {
		t.Fatal("a login_required observer state allowed reuse")
	}
	// Another provider's state does not affect this provider.
	h.store.boxes[0].WatchState = "watching"
	h.store.boxes[1].WatchState = app.EmailStateLoginRequired
	h.admit(t)
	h.admit(t)
	if h.probes() != 3 {
		t.Fatalf("unrelated provider state blocked reuse: %d", h.probes())
	}
}

func TestSendAdmissionFailedCheckIsNeverCachedAndCheckSeedsReuse(t *testing.T) {
	h := sendAdmissionFixture(t, DefaultSendAdmissionReuse)
	h.browser.result = browsercontrol.ScriptExecutionResult{State: "failed", CredentialGeneration: 7,
		Result: json.RawMessage(`{"schema_version":1,"status":"error","provider":"gmail","code":"email_login_required"}`)}
	if _, err := h.controller.Admit(t.Context(), "owner", "gmail"); ErrorCode(err) != app.ToolErrorEmailLoginRequired {
		t.Fatalf("login failure not reported: %v", err)
	}
	h.browser.result = browsercontrol.ScriptExecutionResult{}
	if _, err := h.controller.Check(t.Context(), "owner", "owner", "gmail"); err != nil {
		t.Fatal(err)
	}
	probes := h.probes()
	h.admit(t)
	if h.probes() != probes {
		t.Fatal("a successful Check did not seed send admission")
	}
}
