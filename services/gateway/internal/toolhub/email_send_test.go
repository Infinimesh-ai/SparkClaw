package toolhub

import (
	"context"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
)

type fakeEmailSender struct {
	ownerIDs []string
	requests []app.EmailSendRequest
	result   app.EmailSendResult
	err      error
}

func (f *fakeEmailSender) SendForOwner(_ context.Context, ownerID string, request app.EmailSendRequest) (app.EmailSendResult, error) {
	f.ownerIDs = append(f.ownerIDs, ownerID)
	f.requests = append(f.requests, request)
	return f.result, f.err
}

func TestEmailSendUsesAuthenticatedSessionOwnerAndRuntimeBindings(t *testing.T) {
	st := store.NewMemoryStore()
	session := storetest.MustCreateSessionWithScope(t, st, "email", "owner-email", t.TempDir(), "web", false)
	sender := &fakeEmailSender{result: app.EmailSendResult{
		Provider: app.EmailProviderGmail, Status: "sent", RecipientDigest: "sha256:digest",
		BrowserCredentialGeneration: 7, ScriptRevision: 3,
	}}
	hub := New(config.Default(), st).WithEmailSender(sender)
	t.Cleanup(func() { _ = hub.Close() })
	args := validEmailSendArgs()
	result, err := hub.Execute(t.Context(), "email.send", args, session.ID, "run-email")
	if err != nil {
		t.Fatal(err)
	}
	if len(sender.ownerIDs) != 1 || sender.ownerIDs[0] != "owner-email" || len(sender.requests) != 1 {
		t.Fatalf("sender calls owner=%v requests=%#v", sender.ownerIDs, sender.requests)
	}
	request := sender.requests[0]
	if request.Provider != app.EmailProviderGmail || request.Account != app.EmailAccountDefault || request.Recipient != "alice@example.com" ||
		request.Subject != "Exact subject" || request.Body != "Exact body" || request.SettingVersion != 4 ||
		request.BrowserCredentialGeneration != 7 || request.ProbeRevision != 2 || request.ScriptRevision != 3 || request.InvocationID != "email:send:1" {
		t.Fatalf("bound request = %#v", request)
	}
	if result.Output != sender.result {
		t.Fatalf("tool output = %#v", result.Output)
	}
}

func TestEmailSendRejectsMissingSessionAndInvalidRuntimeNumbers(t *testing.T) {
	sender := &fakeEmailSender{}
	hub := New(config.Default(), store.NewMemoryStore()).WithEmailSender(sender)
	t.Cleanup(func() { _ = hub.Close() })
	args := validEmailSendArgs()
	if _, err := hub.Execute(t.Context(), "email.send", args, "missing", "run-email"); app.ToolErrorCodeFrom(err) != app.ToolErrorEmailInvalidInput {
		t.Fatalf("missing session error=%v code=%q", err, app.ToolErrorCodeFrom(err))
	}
	args["browser_credential_generation"] = "0"
	if _, err := hub.Execute(t.Context(), "email.send", args, "", "run-email"); app.ToolErrorCodeFrom(err) != app.ToolErrorEmailInvalidInput {
		t.Fatalf("invalid binding error=%v code=%q", err, app.ToolErrorCodeFrom(err))
	}
	if len(sender.requests) != 0 {
		t.Fatalf("invalid calls reached sender: %#v", sender.requests)
	}
}

func TestEmailSendDefinitionIsDangerousNonIdempotentAndApprovalGated(t *testing.T) {
	definition := emailSendDefinition()
	if definition.Risk != app.RiskDangerous || !definition.RequiresApproval || definition.Idempotent ||
		definition.OutcomeAdapter != "" {
		t.Fatalf("email definition effect boundary = %#v", definition)
	}
	hub := New(config.Default(), store.NewMemoryStore())
	t.Cleanup(func() { _ = hub.Close() })
	registered, ok := hub.Definition("email.send")
	if !ok || registered.OutcomeAdapter != app.OutcomeAdapterBrowserEmailSend ||
		len(registered.Capabilities) != 1 || registered.Capabilities[0].Name != app.ToolCapabilityBrowserEmailSend {
		t.Fatalf("registered email definition = %#v", registered)
	}
}

func validEmailSendArgs() map[string]any {
	return map[string]any{
		"provider": app.EmailProviderGmail, "account": app.EmailAccountDefault, "account_hint": "a***@gmail.com",
		"recipient": "alice@example.com", "subject": "Exact subject", "body": "Exact body",
		"setting_version": "4", "browser_credential_generation": "7", "probe_revision": "2", "send_script_revision": "3",
		"validated_at": "2026-09-03T08:00:00Z", "invocation_id": "email:send:1",
	}
}
