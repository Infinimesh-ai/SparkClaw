package emailmanagement

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// rejectingComposeBrowser is a QQ runtime whose provider explicitly rejected
// the dispatch with one listed code.
type rejectingComposeBrowser struct {
	*lifecycleComposeBrowser
	code   int
	reason string
	forged bool
}

func (b *rejectingComposeBrowser) Admit(context.Context, string, string) (emailautomation.AdmissionResult, error) {
	return emailautomation.AdmissionResult{Provider: app.EmailProviderQQMail, Account: "default"}, nil
}
func (b *rejectingComposeBrowser) SendForOwner(_ context.Context, _ string, request app.EmailSendRequest) (app.EmailSendResult, error) {
	b.sends++
	b.invocation = request.InvocationID
	code := b.code
	invocation := request.InvocationID
	if b.forged {
		invocation = "other-invocation"
	}
	return app.EmailSendResult{Provider: app.EmailProviderQQMail, Status: "not_sent",
		NotSent: &app.EmailNotSentProof{SchemaVersion: 1, Kind: "provider_rejected", InvocationID: invocation, TaskID: "task-1",
			IntentDigest: strings.Repeat("a", 64), ResourceDigest: strings.Repeat("b", 64), BindingDigest: strings.Repeat("c", 64), LedgerEpoch: 4,
			Reason: b.reason, ProviderCode: &code},
		Lifecycle: &app.EmailRuntimeLifecycle{OperationID: request.InvocationID, TaskID: "task-1", TerminalRevision: 1, ResultDigest: strings.Repeat("d", 64)}}, nil
}

func TestComposeProviderRejectionFailsTheDraftWithItsCodeAndAcknowledges(t *testing.T) {
	for _, tc := range []struct {
		code   int
		reason string
		want   string
	}{
		{-20002, "PROVIDER_LOGIN_REJECTED", app.EmailSendRejectedLoginRequired},
		{-21101, "PROVIDER_PARAMETER_REJECTED", app.EmailSendRejectedParameter},
		{-20011, "PROVIDER_PARAMETER_REJECTED", app.EmailSendTemplateDrift},
		{-20003, "PROVIDER_TEMPORARILY_REJECTED", app.EmailSendRejectedTemporarily},
	} {
		repo := store.NewMemoryStore()
		box, err := repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: store.EmailCommand{OwnerID: "owner", CommandKey: "bind"}, Provider: app.EmailProviderQQMail, Address: "owner@qq.com", Boundary: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		browser := &rejectingComposeBrowser{lifecycleComposeBrowser: &lifecycleComposeBrowser{composeFixture: &composeFixture{intakeFixture: &intakeFixture{}, address: "owner@qq.com"},
			state: browsercontrol.EpochState{Generation: 1, OpenEpoch: 4, SealedThrough: 2, AuthorizationGeneration: 1}, lookups: map[string]string{}}, code: tc.code, reason: tc.reason}
		service := &Service{repository: repo, browser: browser}
		draft, err := service.SaveDraft(t.Context(), "owner", store.EmailDraft{ID: "draft", MailboxID: box.ID, To: []string{"sink@example.test"}, Subject: "s", Body: "b"}, 0)
		if err != nil {
			t.Fatal(err)
		}
		failed, err := service.SendDraft(t.Context(), "owner", draft.ID, draft.Version, "review")
		if err != nil || failed.State != "failed" || failed.ErrorCode != tc.want || failed.Receipt == nil || failed.Receipt.NotSent == nil ||
			failed.Receipt.Lifecycle == nil || !failed.Receipt.Lifecycle.Acknowledged || len(browser.acks) != 1 {
			t.Fatalf("code %d: draft=%+v err=%v acks=%v", tc.code, failed, err, browser.acks)
		}
		if failed.SentMailID != "" || failed.TimelineMailID != "" {
			t.Fatalf("a rejected send was published as sent: %+v", failed)
		}
		// The same review never sends again; a new review is a new identity.
		if replay, err := service.SendDraft(t.Context(), "owner", draft.ID, failed.Version, "review"); err != nil || replay.State != "failed" || browser.sends != 1 {
			t.Fatalf("replay=%+v err=%v sends=%d", replay, err, browser.sends)
		}
	}
}

func TestComposeNeverTrustsARejectionOfAnotherInvocation(t *testing.T) {
	repo := store.NewMemoryStore()
	box, err := repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: store.EmailCommand{OwnerID: "owner", CommandKey: "bind"}, Provider: app.EmailProviderQQMail, Address: "owner@qq.com", Boundary: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	browser := &rejectingComposeBrowser{lifecycleComposeBrowser: &lifecycleComposeBrowser{composeFixture: &composeFixture{intakeFixture: &intakeFixture{}, address: "owner@qq.com"},
		state: browsercontrol.EpochState{Generation: 1, OpenEpoch: 4, SealedThrough: 2, AuthorizationGeneration: 1}, lookups: map[string]string{}},
		code: -20002, reason: "PROVIDER_LOGIN_REJECTED", forged: true}
	service := &Service{repository: repo, browser: browser}
	draft, err := service.SaveDraft(t.Context(), "owner", store.EmailDraft{ID: "draft", MailboxID: box.ID, To: []string{"sink@example.test"}, Subject: "s", Body: "b"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := service.SendDraft(t.Context(), "owner", draft.ID, draft.Version, "review")
	if err != nil || unknown.State != "unknown" || unknown.Receipt != nil {
		t.Fatalf("draft=%+v err=%v", unknown, err)
	}
}
