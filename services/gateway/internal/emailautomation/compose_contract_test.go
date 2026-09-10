package emailautomation

import (
	"encoding/json"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"testing"
)

func TestManagedComposeContractRejectsWrongAccountAndRecipients(t *testing.T) {
	base := SendRequest{Mode: "reply_all", AccountAddress: "owner@example.test", To: []string{"sender@example.test"}, CC: []string{"copy@example.test"}, Subject: "Re: hello", Body: "confirmed", ReplyTarget: &app.EmailReplyTarget{EmailCaptureTarget: app.EmailCaptureTarget{AccountAddress: "owner@example.test", ProviderMessageID: "native", ProviderSelectionID: "selected", Folder: "inbox"}, Subject: "hello"}}
	digest, err := validateComposeRequest(base)
	if err != nil {
		t.Fatal(err)
	}
	if digest != recipientDigest(`{"to":["sender@example.test"],"cc":["copy@example.test"]}`) {
		t.Fatal("recipient digest differs from JS JSON contract")
	}
	for _, mutate := range []func(*SendRequest){func(r *SendRequest) { r.AccountAddress = "other@example.test" }, func(r *SendRequest) { r.CC = []string{"sender@example.test"} }, func(r *SendRequest) { r.ReplyTarget = nil }, func(r *SendRequest) { r.Recipient = "legacy@example.test" }, func(r *SendRequest) { r.Mode = "compose" }} {
		copy := base
		mutate(&copy)
		if _, err := validateComposeRequest(copy); err == nil {
			t.Fatal("invalid native binding accepted")
		}
	}
	replay := base
	replay.Mode = "reconcile"
	if got, err := validateComposeRequest(replay); err != nil || got != digest {
		t.Fatal("read-only reconciliation changed frozen digest")
	}
}

func TestManagedComposeRunnerPreservesNativeReplyWire(t *testing.T) {
	for _, provider := range DefaultRegistry().List() {
		t.Run(provider.ID, func(t *testing.T) {
			request := SendRequest{Provider: provider.ID, Account: "default", Mode: "reply", AccountAddress: "owner@example.test", To: []string{"sender@example.test"}, CC: []string{"copy@example.test"}, Subject: "Re: hello", Body: "confirmed", InvocationID: "managed-reply", ProbeRevision: provider.Probe.Revision, ScriptRevision: provider.Send.Revision, BrowserCredentialGeneration: 7, ReplyTarget: &app.EmailReplyTarget{EmailCaptureTarget: app.EmailCaptureTarget{AccountAddress: "owner@example.test", ProviderMessageID: "native", ProviderSelectionID: "selected", Folder: "inbox"}, Subject: "hello"}}
			digest, err := validateComposeRequest(request)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(map[string]any{"schema_version": 1, "status": "sent", "provider": provider.ID, "recipient_digest": digest, "provider_message_id": "sent-native"})
			controller := &fakePlaywrightController{status: browsercontrol.Status{Configured: true, State: app.IntegrationStateReady, CredentialGeneration: 7}, result: browsercontrol.ScriptExecutionResult{State: "completed", CredentialGeneration: 7, Result: raw}}
			result, err := NewPlaywrightRunner(controller).Send(t.Context(), provider, request)
			if err != nil {
				t.Fatal(err)
			}
			if result.ProviderMessageID != "sent-native" {
				t.Fatal("lost native identity")
			}
			payload, _ := json.Marshal(controller.requests[0].Input)
			var input map[string]any
			if json.Unmarshal(payload, &input) != nil {
				t.Fatal("invalid payload")
			}
			if input["mode"] != "reply" || input["account_address"] != "owner@example.test" || input["reply_target"].(map[string]any)["provider_message_id"] != "native" {
				t.Fatal("native reply binding not on wire")
			}
			message := input["message"].(map[string]any)
			if _, legacy := message["recipient"]; legacy || len(message["to"].([]any)) != 1 || len(message["cc"].([]any)) != 1 {
				t.Fatal("recipient topology lost")
			}
		})
	}
}
