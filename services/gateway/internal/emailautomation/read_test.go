package emailautomation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func validReadRequest() ReadRequest {
	return ReadRequest{Provider: app.EmailProviderGmail, Account: app.EmailAccountDefault, OwnerScope: strings.Repeat("a", 64), InvocationID: "read:test", BrowserCredentialGeneration: 7, ProbeRevision: 1, ScriptRevision: 1, SettingVersion: 1}
}

func validReadOutput() string {
	mailbox := "mb_" + strings.Repeat("b", 32)
	mail := "mail_" + strings.Repeat("c", 32)
	capture := "cap_" + strings.Repeat("d", 32)
	raw, _ := json.Marshal(map[string]any{"schema_version": 1, "status": "collected", "provider": "gmail", "capture": app.EmailCaptureReceipt{
		ManifestPath: "email/" + strings.Repeat("a", 64) + "/" + mailbox + "/" + mail + "/source/" + capture + "/capture.json", ManifestSHA256: "sha256:" + strings.Repeat("e", 64), MailID: mail, MailboxID: mailbox, CaptureID: capture, AttachmentsCount: 1, ReadState: "read",
	}})
	return string(raw)
}

func TestReadRunnerBindsCaptureScriptAndRejectsInvalidReceipts(t *testing.T) {
	provider, _ := DefaultRegistry().Get(app.EmailProviderGmail)
	valid := validReadOutput()
	for name, raw := range map[string]string{
		"valid": valid, "partial": strings.Replace(valid, `"status":"collected"`, `"status":"partial"`, 1),
		"empty":                `{"schema_version":1,"status":"empty","provider":"gmail","capture":null}`,
		"missing":              `{"schema_version":1,"status":"empty","provider":"gmail"}`,
		"null":                 `{"schema_version":1,"status":"collected","provider":"gmail","capture":null}`,
		"cross owner":          strings.Replace(valid, "email/"+strings.Repeat("a", 64), "email/"+strings.Repeat("f", 64), 1),
		"traversal":            strings.Replace(valid, "email/", "../email/", 1),
		"wrong provider":       strings.Replace(valid, `"provider":"gmail"`, `"provider":"outlook"`, 1),
		"wrong state":          strings.Replace(valid, `"read_state":"read"`, `"read_state":"sent"`, 1),
		"missing count":        strings.Replace(valid, `"attachments_count":1,`, ``, 1),
		"too many attachments": strings.Replace(valid, `"attachments_count":1`, `"attachments_count":21`, 1),
		"content leakage":      strings.Replace(valid, `"status":"collected"`, `"body":"private","status":"collected"`, 1),
		"trailing":             valid + ` {}`, "oversized": strings.Repeat(" ", 64<<10) + valid,
	} {
		t.Run(name, func(t *testing.T) {
			controller := &fakePlaywrightController{status: browsercontrol.Status{Configured: true, CredentialGeneration: 7}, result: browsercontrol.ScriptExecutionResult{State: "completed", CredentialGeneration: 7, Result: json.RawMessage(raw)}}
			result, err := NewPlaywrightRunner(controller).Read(t.Context(), provider, validReadRequest())
			if name != "valid" && name != "partial" && name != "empty" {
				if ErrorCode(err) != app.ToolErrorEmailScriptInvalidOutput {
					t.Fatalf("error=%v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != "empty" && result.Capture == nil {
				t.Fatal("missing capture")
			}
			request := controller.requests[0]
			if request.Operation != "read" || request.ScriptID != "gmail.read" || request.CredentialGeneration != 7 {
				t.Fatalf("request=%#v", request)
			}
			encoded, _ := json.Marshal(request.Input)
			if !strings.Contains(string(encoded), `"owner_scope":"`+strings.Repeat("a", 64)+`"`) || strings.Contains(string(encoded), `"query"`) {
				t.Fatalf("input=%s", encoded)
			}
		})
	}
}

func TestReadRunnerRejectsBindingsBeforeBrowser(t *testing.T) {
	provider, _ := DefaultRegistry().Get(app.EmailProviderGmail)
	for _, mutate := range []func(*ReadRequest){func(r *ReadRequest) { r.BrowserCredentialGeneration = 8 }, func(r *ReadRequest) { r.ScriptRevision = 2 }, func(r *ReadRequest) { r.OwnerScope = "../other" }, func(r *ReadRequest) { r.Account = "other" }} {
		controller := &fakePlaywrightController{status: browsercontrol.Status{Configured: true, CredentialGeneration: 7}}
		request := validReadRequest()
		mutate(&request)
		if _, err := NewPlaywrightRunner(controller).Read(t.Context(), provider, request); err == nil || len(controller.requests) != 0 {
			t.Fatalf("err=%v requests=%v", err, controller.requests)
		}
	}
}

func TestReadControllerDerivesOwnerScopeAndRejectsChangedSettings(t *testing.T) {
	st := store.NewMemoryStore()
	checkedAt := time.Now().UTC()
	setting, err := st.UpdateEmailProviderSetting(t.Context(), app.EmailProviderSetting{OwnerID: "owner", Provider: app.EmailProviderGmail, Account: app.EmailAccountDefault, Enabled: true, State: app.EmailStateReady, LastCheckedAt: &checkedAt}, 0)
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeScriptRunner{readResult: ReadResult{Status: "empty"}}
	controller := NewController(st, DefaultRegistry(), nil, runner)
	request := validReadRequest()
	request.SettingVersion = setting.Version
	if _, err := controller.ReadForOwner(t.Context(), "owner", request); err != nil || len(runner.readCalls) != 1 {
		t.Fatalf("err=%v", err)
	}
	digest := sha256.Sum256([]byte("owner"))
	if runner.readCalls[0].OwnerScope != hex.EncodeToString(digest[:]) {
		t.Fatal("caller-controlled scope reached runner")
	}
	if _, err := controller.ReadForOwner(t.Context(), "other", request); ErrorCode(err) != app.ToolErrorEmailAdmissionStale {
		t.Fatalf("owner err=%v", err)
	}
	request.SettingVersion++
	if _, err := controller.ReadForOwner(t.Context(), "owner", request); ErrorCode(err) != app.ToolErrorEmailAdmissionStale || len(runner.readCalls) != 1 {
		t.Fatalf("stale err=%v", err)
	}
}
