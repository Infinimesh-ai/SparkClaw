package emailmanagement

import (
	"context"
	"errors"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"testing"
	"time"
)

type composeFixture struct {
	*intakeFixture
	sends       int
	resultError error
	address     string
}

func (f *composeFixture) Admit(context.Context, string, string) (emailautomation.AdmissionResult, error) {
	return emailautomation.AdmissionResult{Provider: "gmail", Account: "default", AccountHint: f.address}, nil
}
func (f *composeFixture) SendForOwner(_ context.Context, _ string, request app.EmailSendRequest) (app.EmailSendResult, error) {
	if request.AccountAddress != f.address {
		return app.EmailSendResult{}, &emailautomation.Error{Code: app.ToolErrorEmailAdmissionStale, Message: "account changed"}
	}
	f.sends++
	return app.EmailSendResult{Provider: "gmail", Status: "sent"}, f.resultError
}
func TestComposeUnknownOutcomeNeverResends(t *testing.T) {
	repo := store.NewMemoryStore()
	box, err := repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: store.EmailCommand{OwnerID: "owner", CommandKey: "bind"}, Provider: "gmail", Address: "owner@example.test", Boundary: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	browser := &composeFixture{intakeFixture: &intakeFixture{}, address: "owner@example.test", resultError: errors.New("transport disconnected")}
	s := &Service{repository: repo, browser: browser}
	d, err := s.SaveDraft(t.Context(), "owner", store.EmailDraft{ID: "draft", MailboxID: box.ID, To: []string{"recipient@example.test"}, Subject: "test", Body: "test"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	d, err = s.SendDraft(t.Context(), "owner", d.ID, d.Version, "click")
	if err != nil || d.State != "unknown" {
		t.Fatalf("state=%s err=%v", d.State, err)
	}
	if _, err = s.SendDraft(t.Context(), "owner", d.ID, 1, "click"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SendDraft(t.Context(), "owner", d.ID, d.Version, "other-click"); err == nil {
		t.Fatal("unknown outcome allowed new send")
	}
	if browser.sends != 1 {
		t.Fatalf("sent %d times", browser.sends)
	}
}
func TestComposeAccountSwitchBlocksSend(t *testing.T) {
	repo := store.NewMemoryStore()
	box, err := repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: store.EmailCommand{OwnerID: "owner", CommandKey: "bind"}, Provider: "gmail", Address: "owner@example.test", Boundary: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	browser := &composeFixture{intakeFixture: &intakeFixture{}, address: "different@example.test"}
	s := &Service{repository: repo, browser: browser}
	d, err := s.SaveDraft(t.Context(), "owner", store.EmailDraft{ID: "draft", MailboxID: box.ID, To: []string{"recipient@example.test"}, Subject: "test", Body: "test"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.SendDraft(t.Context(), "owner", d.ID, d.Version, "click")
	if err != nil || out.State != "failed" {
		t.Fatalf("account switch state=%s err=%v", out.State, err)
	}
	if browser.sends != 0 {
		t.Fatal("wrong account sent")
	}
}

func (f *composeFixture) ReconcileSendForOwner(context.Context, string, app.EmailSendRequest) (app.EmailSendResult, error) {
	return app.EmailSendResult{Provider: "gmail", Status: "unknown"}, nil
}

func TestComposeEmptySubjectSavesButCannotSend(t *testing.T) {
	repo := store.NewMemoryStore()
	box, err := repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: store.EmailCommand{OwnerID: "owner", CommandKey: "bind"}, Provider: "gmail", Address: "owner@example.test", Boundary: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	browser := &composeFixture{intakeFixture: &intakeFixture{}, address: "owner@example.test"}
	s := &Service{repository: repo, browser: browser}
	draft, err := s.SaveDraft(t.Context(), "owner", store.EmailDraft{ID: "blank-subject", MailboxID: box.ID, To: []string{"recipient@example.test"}, Subject: "  ", Body: "kept body"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SendDraft(t.Context(), "owner", draft.ID, draft.Version, "click"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("blank subject err=%v", err)
	}
	saved, err := s.Drafts(t.Context(), "owner", draft.ID)
	if err != nil || saved[0].State != "draft" || saved[0].Body != "kept body" || browser.sends != 0 {
		t.Fatal("blank subject changed draft or invoked send")
	}
}
