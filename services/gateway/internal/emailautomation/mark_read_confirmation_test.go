package emailautomation

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
)

func TestMarkReadConfirmationPreservesReceiptTimeAndTarget(t *testing.T) {
	provider, _ := DefaultRegistry().Get(app.EmailProviderQQMail)
	received := time.Date(2026, 10, 18, 8, 7, 50, 123456789, time.UTC)
	target := app.EmailCaptureTarget{AccountAddress: "owner@example.test", ProviderMessageID: "mail",
		ProviderSelectionID: "selection", ProviderThreadID: "thread", ProviderNativeID: "native",
		Folder: "inbox", ReceivedAt: &received}
	for _, tc := range []struct {
		name   string
		change func(*app.EmailCaptureTarget)
		valid  bool
	}{
		{"same timestamp after JSON decoding", func(*app.EmailCaptureTarget) {}, true},
		{"same instant with another zone", func(v *app.EmailCaptureTarget) { at := received.In(time.FixedZone("offset", 3600)); v.ReceivedAt = &at }, true},
		{"missing time", func(v *app.EmailCaptureTarget) { v.ReceivedAt = nil }, false},
		{"nanosecond drift", func(v *app.EmailCaptureTarget) { at := received.Add(time.Nanosecond); v.ReceivedAt = &at }, false},
		{"account drift", func(v *app.EmailCaptureTarget) { v.AccountAddress = "other@example.test" }, false},
		{"message drift", func(v *app.EmailCaptureTarget) { v.ProviderMessageID = "other" }, false},
		{"selection drift", func(v *app.EmailCaptureTarget) { v.ProviderSelectionID = "other" }, false},
		{"thread drift", func(v *app.EmailCaptureTarget) { v.ProviderThreadID = "other" }, false},
		{"native identity drift", func(v *app.EmailCaptureTarget) { v.ProviderNativeID = "other" }, false},
		{"folder drift", func(v *app.EmailCaptureTarget) { v.Folder = "sent" }, false},
		{"unexpected recovery", func(v *app.EmailCaptureTarget) { v.RecoveryCapture = &app.EmailCaptureVersion{} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binding := validReadRequest()
			binding.Provider, binding.ScriptRevision, binding.Target = provider.ID, provider.MarkRead.Revision, &target
			output := app.EmailMarkReadResult{SchemaVersion: 1, Provider: provider.ID, Target: target, ReadState: "read", ObservedAt: received.Add(time.Minute)}
			tc.change(&output.Target)
			raw, err := json.Marshal(output)
			if err != nil {
				t.Fatal(err)
			}
			browser := &fakePlaywrightController{status: browsercontrol.Status{Configured: true, CredentialGeneration: 7},
				result: browsercontrol.ScriptExecutionResult{State: "completed", CredentialGeneration: 7, Result: raw}}
			_, err = NewPlaywrightRunner(browser).MarkRead(t.Context(), provider, app.EmailMarkReadRequest{Binding: binding})
			if tc.valid && err != nil || !tc.valid && ErrorCode(err) != app.ToolErrorEmailScriptInvalidOutput {
				t.Fatalf("valid=%t: %v", tc.valid, err)
			}
		})
	}
}
