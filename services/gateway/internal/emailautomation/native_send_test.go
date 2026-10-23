package emailautomation

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// scriptedController answers probes as ready and sends with a fixed result.
type scriptedController struct {
	status   browsercontrol.Status
	send     json.RawMessage
	life     *browsercontrol.ScriptLifecycle
	requests []browsercontrol.RunScriptRequest
}

func (s *scriptedController) Status(context.Context) browsercontrol.Status { return s.status }
func (s *scriptedController) RunScript(_ context.Context, request browsercontrol.RunScriptRequest) (browsercontrol.ScriptExecutionResult, error) {
	s.requests = append(s.requests, request)
	result := browsercontrol.ScriptExecutionResult{State: "completed", CredentialGeneration: request.CredentialGeneration, Lifecycle: s.life}
	if request.Operation == "probe" {
		result.Result = json.RawMessage(`{"schema_version":1,"status":"ready","provider":"` + request.Provider + `","account_hint":"o***@qq.com"}`)
	} else {
		result.Result = s.send
	}
	return result, nil
}
func (s *scriptedController) OpenProviderLogin(context.Context, browsercontrol.OpenProviderLoginRequest) (browsercontrol.OpenProviderLoginResult, error) {
	return browsercontrol.OpenProviderLoginResult{}, nil
}
func (s *scriptedController) count(operation string) int {
	count := 0
	for _, request := range s.requests {
		if request.Operation == operation {
			count++
		}
	}
	return count
}

// inPageRegistry is the default contract with the App-CLI release that
// declares QQ's in-page send admission.
func inPageRegistry(t *testing.T) Registry {
	t.Helper()
	raw := bytes.Replace(providerScriptContract, []byte(`"script_id": "qqmail.send",`), []byte(`"script_id": "qqmail.send", "send_admission": "in_page",`), 1)
	registry, err := registryFromContract(raw)
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func rejection(code int, reason string) *app.EmailNotSentProof {
	return &app.EmailNotSentProof{SchemaVersion: 1, Kind: "provider_rejected", InvocationID: "native-invocation", TaskID: "task-1",
		IntentDigest: strings.Repeat("a", 64), ResourceDigest: strings.Repeat("b", 64), BindingDigest: strings.Repeat("c", 64), LedgerEpoch: 3,
		Reason: reason, ProviderCode: &code}
}

func qqComposeRequest(provider Provider) SendRequest {
	return SendRequest{Provider: provider.ID, Account: app.EmailAccountDefault, Mode: "compose", AccountAddress: "owner@qq.com", To: []string{"sink@example.test"},
		Subject: "Approved", Body: "Body", InvocationID: "native-invocation", BrowserCredentialGeneration: 7, ProbeRevision: provider.Probe.Revision, ScriptRevision: provider.Send.Revision}
}

func TestProviderRejectionProofAcceptsOnlyListedCodesInTheirCategory(t *testing.T) {
	for code, want := range map[int]string{-20002: app.EmailSendRejectedLoginRequired, -5012: app.EmailSendRejectedLoginRequired,
		-21101: app.EmailSendRejectedParameter, -20011: app.EmailSendTemplateDrift, -21519: app.EmailSendRejectedTemporarily, -20003: app.EmailSendRejectedTemporarily} {
		category := map[string]string{app.EmailSendRejectedLoginRequired: "PROVIDER_LOGIN_REJECTED", app.EmailSendRejectedParameter: "PROVIDER_PARAMETER_REJECTED",
			app.EmailSendTemplateDrift: "PROVIDER_PARAMETER_REJECTED", app.EmailSendRejectedTemporarily: "PROVIDER_TEMPORARILY_REJECTED"}[want]
		proof := rejection(code, category)
		if !proof.ValidFor("native-invocation", app.EmailProviderQQMail) || proof.RejectionErrorCode(app.EmailProviderQQMail) != want {
			t.Fatalf("code %d: valid=%v code=%q", code, proof.ValidFor("native-invocation", app.EmailProviderQQMail), proof.RejectionErrorCode(app.EmailProviderQQMail))
		}
	}
	for name, proof := range map[string]*app.EmailNotSentProof{
		"unlisted code":  rejection(-1, "PROVIDER_LOGIN_REJECTED"),
		"wrong category": rejection(-21519, "PROVIDER_LOGIN_REJECTED"),
		"legacy reason":  rejection(-20002, "EMAIL_DRAFT_FIELDS_UNVERIFIED"),
		"other provider": nil,
		"code on legacy kind": func() *app.EmailNotSentProof {
			p := rejection(-20002, "EMAIL_DRAFT_FIELDS_UNVERIFIED")
			p.Kind = "pre_dispatch_failure"
			return p
		}(),
		"missing code": func() *app.EmailNotSentProof {
			p := rejection(-20002, "PROVIDER_LOGIN_REJECTED")
			p.ProviderCode = nil
			return p
		}(),
	} {
		provider := app.EmailProviderQQMail
		if name == "other provider" {
			proof, provider = rejection(-20002, "PROVIDER_LOGIN_REJECTED"), app.EmailProviderGmail
		}
		if proof.ValidFor("native-invocation", provider) || proof.RejectionErrorCode(provider) != "" {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestPlaywrightRunnerAcceptsAndAcknowledgesAnExplicitProviderRejection(t *testing.T) {
	provider, _ := DefaultRegistry().Get(app.EmailProviderQQMail)
	request := qqComposeRequest(provider)
	digest, err := validateComposeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	life := &browsercontrol.ScriptLifecycle{OperationID: request.InvocationID, TaskID: "task-1", Status: "completed", TerminalRevision: 2, ResultDigest: strings.Repeat("d", 64)}
	for _, change := range []string{"valid", "legacy-mode", "pre-dispatch-kind", "unlisted-code", "message-id"} {
		t.Run(change, func(t *testing.T) {
			r, proof := request, rejection(-20002, "PROVIDER_LOGIN_REJECTED")
			result := map[string]any{"schema_version": 1, "provider": provider.ID, "status": "not_sent", "recipient_digest": digest, "not_sent": proof}
			switch change {
			case "legacy-mode":
				r.Mode = ""
			case "pre-dispatch-kind":
				proof.Kind, proof.Reason, proof.ProviderCode = "pre_dispatch_failure", "EMAIL_DRAFT_FIELDS_UNVERIFIED", nil
			case "unlisted-code":
				code := -1
				proof.ProviderCode = &code
			case "message-id":
				result["provider_message_id"] = "sent-id"
			}
			raw, _ := json.Marshal(result)
			controller := &scriptedController{status: browsercontrol.Status{Configured: true, CredentialGeneration: 7}, send: raw, life: life}
			receipt, err := NewPlaywrightRunner(controller).Send(t.Context(), provider, r)
			if change == "valid" {
				if err != nil || receipt.Status != "not_sent" || receipt.NotSent.Kind != "provider_rejected" || receipt.Lifecycle == nil || receipt.Lifecycle.TaskID != "task-1" {
					t.Fatalf("receipt=%+v err=%v", receipt, err)
				}
			} else if change == "legacy-mode" {
				// The legacy single-recipient contract never carries a rejection.
				if err == nil || receipt.Status != "" {
					t.Fatalf("legacy mode accepted: %+v", receipt)
				}
				return
			} else if ErrorCode(err) != app.ToolErrorEmailScriptInvalidOutput {
				t.Fatalf("%s accepted: %+v %v", change, receipt, err)
			}
			if controller.count("send") != 1 {
				t.Fatalf("send retried: %d", controller.count("send"))
			}
		})
	}
}

func TestProviderScriptContractDeclaresInPageAdmissionOnlyForSends(t *testing.T) {
	provider, _ := inPageRegistry(t).Get(app.EmailProviderQQMail)
	gmail, _ := inPageRegistry(t).Get(app.EmailProviderGmail)
	if provider.SendAdmission != SendAdmissionInPage || gmail.SendAdmission != "" {
		t.Fatalf("admission qq=%q gmail=%q", provider.SendAdmission, gmail.SendAdmission)
	}
	for _, raw := range [][]byte{
		bytes.Replace(providerScriptContract, []byte(`"script_id": "qqmail.send",`), []byte(`"script_id": "qqmail.send", "send_admission": "none",`), 1),
		bytes.Replace(providerScriptContract, []byte(`"script_id": "qqmail.login_probe",`), []byte(`"script_id": "qqmail.login_probe", "send_admission": "in_page",`), 1),
	} {
		if _, err := registryFromContract(raw); err == nil {
			t.Fatal("unsupported admission declaration accepted")
		}
	}
}

type inPageHarness struct {
	controller *Controller
	browser    *scriptedController
	store      *signalStore
}

func inPageFixture(t *testing.T) *inPageHarness {
	t.Helper()
	now := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	st := &signalStore{MemoryStore: store.NewMemoryStore(), boxes: []app.EmailMailbox{{ID: "mailbox-qq", Provider: app.EmailProviderQQMail, Active: true, WatchState: "watching"}}}
	if _, err := st.UpdateEmailProviderSetting(t.Context(), app.EmailProviderSetting{OwnerID: "owner", Provider: app.EmailProviderQQMail, Account: "default", Enabled: true, State: app.EmailStateReady, LastCheckedAt: &now}, 0); err != nil {
		t.Fatal(err)
	}
	browser := &scriptedController{status: browsercontrol.Status{Configured: true, CredentialGeneration: 7}}
	runner := NewPlaywrightRunner(browser)
	controller := NewController(st, inPageRegistry(t), runner, runner)
	controller.now = func() time.Time { return now }
	return &inPageHarness{controller: controller, browser: browser, store: st}
}

func (h *inPageHarness) admit(t *testing.T) AdmissionResult {
	t.Helper()
	binding, err := h.controller.Admit(t.Context(), "owner", app.EmailProviderQQMail)
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func TestInPageSendAdmissionSkipsTheProbeOnlyWhilePersistedStateHolds(t *testing.T) {
	h := inPageFixture(t)
	first := h.admit(t)
	for range 3 {
		if binding := h.admit(t); binding != first {
			t.Fatalf("in-page admission changed the binding: %#v != %#v", binding, first)
		}
	}
	if h.browser.count("probe") != 1 {
		t.Fatalf("in-page admission probed %d times, want only the first", h.browser.count("probe"))
	}
	for name, change := range map[string]func(){
		"credential generation": func() { h.browser.status.CredentialGeneration = 8 },
		"observer login":        func() { h.store.boxes[0].WatchState = app.EmailStateLoginRequired },
		"mailbox error":         func() { h.store.boxes[0].ErrorCode = "email_network_read_failed" },
		"setting changed": func() {
			setting, _, _ := h.store.GetEmailProviderSetting(t.Context(), "owner", app.EmailProviderQQMail)
			setting.UpdatedBy = "user"
			if _, err := h.store.UpdateEmailProviderSetting(t.Context(), setting, setting.Version); err != nil {
				t.Fatal(err)
			}
		},
	} {
		h.admit(t)
		before := h.browser.count("probe")
		change()
		if name == "observer login" || name == "mailbox error" {
			// The probe cannot fix a mailbox signal; the admission still probes.
			_, _ = h.controller.Admit(t.Context(), "owner", app.EmailProviderQQMail)
			h.store.boxes[0].WatchState, h.store.boxes[0].ErrorCode = "watching", ""
		} else {
			h.admit(t)
		}
		if h.browser.count("probe") != before+1 {
			t.Fatalf("%s did not force a probe", name)
		}
	}
	// Providers without the declaration keep probing every time.
	gmail, _ := h.controller.registry.Get(app.EmailProviderGmail)
	if _, ok := h.controller.inPageSendAdmission(t.Context(), intakeProbeKey{ownerID: "owner", providerID: gmail.ID}, gmail); ok {
		t.Fatal("gmail admitted in page")
	}
}

func TestLoginRejectionMovesTheSettingToLoginRequired(t *testing.T) {
	for _, tc := range []struct {
		code   int
		reason string
		login  bool
	}{{-20002, "PROVIDER_LOGIN_REJECTED", true}, {-21519, "PROVIDER_TEMPORARILY_REJECTED", false}} {
		h := inPageFixture(t)
		binding := h.admit(t)
		provider, _ := h.controller.registry.Get(app.EmailProviderQQMail)
		request := qqComposeRequest(provider)
		request.SettingVersion, request.BrowserCredentialGeneration = binding.SettingVersion, binding.BrowserCredentialGeneration
		digest, _ := validateComposeRequest(request)
		raw, _ := json.Marshal(map[string]any{"schema_version": 1, "provider": provider.ID, "status": "not_sent", "recipient_digest": digest, "not_sent": rejection(tc.code, tc.reason)})
		h.browser.send = raw
		result, err := h.controller.SendForOwner(t.Context(), "owner", request)
		if err != nil || result.Status != "not_sent" {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		setting, _, _ := h.store.GetEmailProviderSetting(t.Context(), "owner", app.EmailProviderQQMail)
		if (setting.State == app.EmailStateLoginRequired) != tc.login {
			t.Fatalf("code %d: setting state %q", tc.code, setting.State)
		}
		// A send that did not end sent always checks the page again.
		h.admit(t)
		if h.browser.count("probe") != 2 && !tc.login {
			t.Fatalf("code %d: next admission probed %d times", tc.code, h.browser.count("probe"))
		}
	}
}
