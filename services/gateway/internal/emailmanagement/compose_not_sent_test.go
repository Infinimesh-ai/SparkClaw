package emailmanagement

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type negativeComposeBrowser struct {
	*composeFixture
	reconciles      int
	wrongInvocation bool
	original        string
}

func (b *negativeComposeBrowser) ReconcileSendForOwner(_ context.Context, _ string, r app.EmailSendRequest) (app.EmailSendResult, error) {
	b.reconciles++
	b.original = r.InvocationID
	invocation := r.InvocationID
	if b.wrongInvocation {
		invocation = "other-invocation"
	}
	return app.EmailSendResult{Provider: "gmail", Status: "not_sent", NotSent: &app.EmailNotSentProof{SchemaVersion: 1, Kind: "pre_dispatch_failure", InvocationID: invocation, TaskID: "original-task", IntentDigest: strings.Repeat("a", 64), ResourceDigest: strings.Repeat("b", 64), BindingDigest: strings.Repeat("c", 64), LedgerEpoch: 2, Reason: "EMAIL_ATTACHMENT_UPLOAD_UNVERIFIED"}}, nil
}
func TestComposeReconcileNegativeProofRetainsDraftAndDoesNotSend(t *testing.T) {
	repo := store.NewMemoryStore()
	box, err := repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: store.EmailCommand{OwnerID: "owner", CommandKey: "bind"}, Provider: "gmail", Address: "owner@example.test", Boundary: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	browser := &negativeComposeBrowser{composeFixture: &composeFixture{intakeFixture: &intakeFixture{}, address: "owner@example.test", resultError: errors.New("unknown upload outcome")}}
	service := &Service{repository: repo, browser: browser}
	draft, err := service.SaveDraft(t.Context(), "owner", store.EmailDraft{ID: "same-draft", MailboxID: box.ID, To: []string{"sink@example.test"}, Subject: "Reviewed", Body: "Body"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := service.SendDraft(t.Context(), "owner", draft.ID, draft.Version, "original-review")
	if err != nil || unknown.State != "unknown" {
		t.Fatalf("%+v %v", unknown, err)
	}
	browser.wrongInvocation = true
	if _, err = service.ReconcileDraft(t.Context(), "owner", draft.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong original accepted: %v", err)
	}
	browser.wrongInvocation = false
	failed, err := service.ReconcileDraft(t.Context(), "owner", draft.ID)
	if err != nil || failed.State != "failed" || failed.ID != draft.ID || failed.Snapshot.InvocationID != unknown.Snapshot.InvocationID || failed.Receipt.NotSent == nil || browser.original != unknown.Snapshot.InvocationID {
		t.Fatalf("failed=%+v err=%v", failed, err)
	}
	if _, err = service.ReconcileDraft(t.Context(), "owner", draft.ID); err != nil {
		t.Fatal(err)
	}
	if browser.sends != 1 || browser.reconciles != 2 {
		t.Fatalf("reconcile repeated a send/proof: %+v", browser)
	}
	replay, err := service.SendDraft(t.Context(), "owner", draft.ID, 1, "original-review")
	if err != nil || replay.State != "failed" || browser.sends != 1 {
		t.Fatal("original review was reexecuted")
	}
	edited, err := service.SaveDraft(t.Context(), "owner", failed, failed.Version)
	if err != nil {
		t.Fatal(err)
	}
	browser.resultError = nil
	sent, err := service.SendDraft(t.Context(), "owner", draft.ID, edited.Version, "new-review")
	if err != nil || sent.State != "sent" || browser.sends != 2 || sent.Snapshot.InvocationID == unknown.Snapshot.InvocationID {
		t.Fatalf("new reviewed attempt=%+v err=%v", sent, err)
	}
}
