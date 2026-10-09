package emailautomation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
)

func TestPlaywrightRunnerNotSentRequiresReconcileAndBoundTypedProof(t *testing.T) {
	provider, _ := DefaultRegistry().Get(app.EmailProviderQQMail)
	request := SendRequest{Provider: provider.ID, Account: app.EmailAccountDefault, Mode: "reconcile", AccountAddress: "owner@example.test", To: []string{"sink@example.test"}, Subject: "Approved", Body: "Body", InvocationID: "original-invocation", BrowserCredentialGeneration: 7, ProbeRevision: provider.Probe.Revision, ScriptRevision: provider.Send.Revision}
	digest, err := validateComposeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	proof := &app.EmailNotSentProof{SchemaVersion: 1, Kind: "legacy_15_pre_dispatch_failure", InvocationID: request.InvocationID, TaskID: "original-task", IntentDigest: strings.Repeat("a", 64), ResourceDigest: strings.Repeat("b", 64), BindingDigest: app.EmailLegacy15QQBindingDigest, LedgerEpoch: 13, Reason: "EMAIL_ATTACHMENT_UPLOAD_UNVERIFIED"}
	for _, change := range []string{"valid", "no-proof", "other-invocation", "bad-digest", "sent-id", "wrong-recipient", "unknown-field", "send-mode", "unknown-kind", "sent-with-proof", "legacy-wrong-binding", "legacy-wrong-reason"} {
		t.Run(change, func(t *testing.T) {
			r := request
			p := *proof
			result := map[string]any{"schema_version": 1, "provider": provider.ID, "status": "not_sent", "recipient_digest": digest, "not_sent": &p}
			switch change {
			case "legacy-wrong-binding":
				p.BindingDigest = strings.Repeat("c", 64)
			case "legacy-wrong-reason":
				p.Reason = "EMAIL_ATTACHMENT_UPLOAD_FAILED"
			case "no-proof":
				delete(result, "not_sent")
			case "other-invocation":
				p.InvocationID = "other"
			case "bad-digest":
				p.ResourceDigest = "short"
			case "sent-id":
				result["provider_message_id"] = "sent-id"
			case "wrong-recipient":
				result["recipient_digest"] = "sha256:" + strings.Repeat("a", 64)
			case "unknown-field":
				result["unsafe_bypass"] = true
			case "send-mode":
				r.Mode = "compose"
			case "unknown-kind":
				p.Kind = "no_journal"
			case "sent-with-proof":
				result["status"] = "sent"
			}
			raw, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			controller := &fakePlaywrightController{status: browsercontrol.Status{Configured: true, CredentialGeneration: 7}, result: browsercontrol.ScriptExecutionResult{State: "completed", CredentialGeneration: 7, Result: raw}}
			receipt, err := NewPlaywrightRunner(controller).Send(t.Context(), provider, r)
			if change == "valid" {
				if err != nil || receipt.Status != "not_sent" || receipt.NotSent == nil || receipt.NotSent.InvocationID != request.InvocationID {
					t.Fatalf("receipt=%+v err=%v", receipt, err)
				}
			} else if ErrorCode(err) != app.ToolErrorEmailScriptInvalidOutput {
				t.Fatalf("%s accepted: %+v %v", change, receipt, err)
			}
			if len(controller.requests) != 1 {
				t.Fatalf("unexpected retry count %d", len(controller.requests))
			}
		})
	}
}
