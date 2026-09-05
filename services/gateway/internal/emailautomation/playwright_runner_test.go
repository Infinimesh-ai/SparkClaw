package emailautomation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
)

func TestPlaywrightRunnerBindsFixedScriptsAndCredentialGeneration(t *testing.T) {
	controller := &fakePlaywrightController{status: browsercontrol.Status{
		Configured: true, State: browsercontrol.StateReady, CredentialGeneration: 7,
	}}
	runner := NewPlaywrightRunner(controller)
	runner.now = func() time.Time { return time.Date(2026, 9, 4, 14, 0, 0, 0, time.UTC) }
	provider, ok := DefaultRegistry().Get(app.EmailProviderGmail)
	if !ok {
		t.Fatal("Gmail provider is missing")
	}

	probe, err := runner.Probe(t.Context(), provider, "probe:playwright", 0)
	if err != nil {
		t.Fatal(err)
	}
	if probe.Provider != app.EmailProviderGmail || probe.AccountHint != "a***@gmail.com" ||
		probe.Generation != 7 || probe.Revision != 1 || !probe.CheckedAt.Equal(runner.now()) {
		t.Fatalf("probe = %#v", probe)
	}
	if len(controller.requests) != 1 || controller.requests[0].ScriptID != "gmail.login_probe" ||
		controller.requests[0].Revision != 1 || controller.requests[0].CredentialGeneration != 7 {
		t.Fatalf("probe request = %#v", controller.requests)
	}

	result, err := runner.Send(t.Context(), provider, SendRequest{
		Provider: app.EmailProviderGmail, Account: app.EmailAccountDefault,
		Recipient: "alice@example.com", Subject: "subject", Body: "Exact body\nsecond line",
		InvocationID: "send:playwright", BrowserCredentialGeneration: 7,
		ProbeRevision: 1, ScriptRevision: 1, SettingVersion: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Provider != app.EmailProviderGmail || result.Status != "sent" ||
		result.RecipientDigest != recipientDigest("alice@example.com") ||
		result.BrowserCredentialGeneration != 7 || result.ScriptRevision != 1 {
		t.Fatalf("send result = %#v", result)
	}
	if len(controller.requests) != 2 || controller.requests[1].ScriptID != "gmail.send" ||
		controller.requests[1].Operation != "send" {
		t.Fatalf("send request = %#v", controller.requests)
	}
	encoded, err := json.Marshal(controller.requests[1].Input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"recipient":"alice@example.com"`) ||
		strings.Contains(string(encoded), "script_dir") || strings.Contains(string(encoded), "selector") {
		t.Fatalf("unexpected deterministic input: %s", encoded)
	}

	if err := runner.OpenLogin(t.Context(), provider); err != nil {
		t.Fatal(err)
	}
	if len(controller.logins) != 1 || controller.logins[0].Provider != app.EmailProviderGmail ||
		controller.logins[0].TaskID == "" {
		t.Fatalf("login request = %#v", controller.logins)
	}
}

func TestPlaywrightRunnerRejectsStaleCredentialBeforeControllerInvocation(t *testing.T) {
	controller := &fakePlaywrightController{status: browsercontrol.Status{
		Configured: true, State: browsercontrol.StateReady, CredentialGeneration: 8,
	}}
	runner := NewPlaywrightRunner(controller)
	provider, _ := DefaultRegistry().Get(app.EmailProviderGmail)
	_, err := runner.Send(t.Context(), provider, SendRequest{
		Provider: app.EmailProviderGmail, Account: app.EmailAccountDefault,
		Recipient: "alice@example.com", Body: "body", InvocationID: "send:stale",
		BrowserCredentialGeneration: 7, ProbeRevision: 1, ScriptRevision: 1, SettingVersion: 1,
	})
	if ErrorCode(err) != CodeAdmissionStale || len(controller.requests) != 0 {
		t.Fatalf("stale error=%v code=%q requests=%#v", err, ErrorCode(err), controller.requests)
	}
}

func TestPlaywrightRunnerPreservesProviderFailureAndUnknownTransportOutcome(t *testing.T) {
	provider, _ := DefaultRegistry().Get(app.EmailProviderGmail)
	controller := &fakePlaywrightController{
		status: browsercontrol.Status{Configured: true, CredentialGeneration: 7},
		result: browsercontrol.ScriptExecutionResult{
			State: "failed", CredentialGeneration: 7,
			Result: json.RawMessage(`{"schema_version":1,"status":"error","provider":"gmail","code":"send_outcome_unknown"}`),
		},
	}
	runner := NewPlaywrightRunner(controller)
	request := SendRequest{
		Provider: app.EmailProviderGmail, Account: app.EmailAccountDefault,
		Recipient: "alice@example.com", Body: "body", InvocationID: "send:unknown",
		BrowserCredentialGeneration: 7, ProbeRevision: 1, ScriptRevision: 1, SettingVersion: 1,
	}
	if _, err := runner.Send(t.Context(), provider, request); ErrorCode(err) != CodeSendOutcomeUnknown {
		t.Fatalf("provider failure=%v code=%q", err, ErrorCode(err))
	}

	controller.result = browsercontrol.ScriptExecutionResult{}
	controller.err = &browsercontrol.Error{Code: browsercontrol.CodeControllerUnavailable, Retryable: true}
	if _, err := runner.Send(t.Context(), provider, request); ErrorCode(err) != CodeSendOutcomeUnknown {
		t.Fatalf("transport failure=%v code=%q", err, ErrorCode(err))
	}
	controller.err = errors.New("untyped")
	if _, err := runner.Probe(t.Context(), provider, "probe:failed", 0); ErrorCode(err) != CodeProviderUnavailable {
		t.Fatalf("probe failure=%v code=%q", err, ErrorCode(err))
	}
}

type fakePlaywrightController struct {
	status   browsercontrol.Status
	result   browsercontrol.ScriptExecutionResult
	err      error
	requests []browsercontrol.RunScriptRequest
	logins   []browsercontrol.OpenProviderLoginRequest
}

func (f *fakePlaywrightController) Status(context.Context) browsercontrol.Status {
	return f.status
}

func (f *fakePlaywrightController) RunScript(
	_ context.Context,
	request browsercontrol.RunScriptRequest,
) (browsercontrol.ScriptExecutionResult, error) {
	f.requests = append(f.requests, request)
	if f.err != nil {
		return browsercontrol.ScriptExecutionResult{}, f.err
	}
	if f.result.State != "" {
		return f.result, nil
	}
	result := browsercontrol.ScriptExecutionResult{
		State: "completed", CredentialGeneration: request.CredentialGeneration,
	}
	if request.Operation == "probe" {
		result.Result = json.RawMessage(`{"schema_version":1,"status":"ready","provider":"gmail","account_hint":"a***@gmail.com"}`)
	} else {
		var input struct {
			Message struct {
				Recipient string `json:"recipient"`
			} `json:"message"`
		}
		encoded, _ := json.Marshal(request.Input)
		_ = json.Unmarshal(encoded, &input)
		result.Result = json.RawMessage(`{"schema_version":1,"status":"sent","provider":"gmail","recipient_digest":"` + recipientDigest(input.Message.Recipient) + `"}`)
	}
	return result, nil
}

func (f *fakePlaywrightController) OpenProviderLogin(
	_ context.Context,
	request browsercontrol.OpenProviderLoginRequest,
) (browsercontrol.OpenProviderLoginResult, error) {
	f.logins = append(f.logins, request)
	return browsercontrol.OpenProviderLoginResult{
		SchemaVersion: 1, State: "opened", ProfileID: "default", Provider: request.Provider,
		ControllerGeneration: 1, SessionGeneration: 1,
	}, f.err
}
