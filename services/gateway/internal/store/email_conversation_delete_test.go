package store

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestDeleteEmailConversationCascadesAcrossBackends(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		_, err := repo.ActivateEmailEventPolicy(t.Context(), f.command())
		f.must(err)
		mail := f.parse(f.capture(f.admit("delete-source", time.Now())), "<delete-source@example.com>")
		capture, found, err := repo.GetEmailCapture(t.Context(), f.owner, mail.CaptureID)
		f.must(err)
		if !found {
			t.Fatal("capture was not created")
		}
		preview, err := repo.PublishEmailRenderPreview(t.Context(), EmailRenderPreviewCommand{
			EmailCommand: f.command(),
			Preview: app.EmailRenderPreview{
				MailID: mail.ID, CaptureID: capture.ID, RepresentationID: mail.RepresentationID,
				SourceSHA256: capture.OriginalSHA256, SanitizerVersion: "safe-mail-v1", State: app.EmailRenderReady,
				HTMLPath: "email/render/preview.html", HTMLSHA256: "sha256:" + strings.Repeat("a", 64), HTMLBytes: 128,
			},
		})
		f.must(err)
		mail = manualEvent(t, f, mail, "", "Conversation to delete")
		conversation, ok, err := repo.GetEmailConversation(t.Context(), f.owner, mail.ConversationID)
		f.must(err)
		if !ok {
			t.Fatal("conversation was not created")
		}
		hidden := app.EmailMail{ID: "superseded-member", OwnerID: f.owner, MailboxID: f.box.ID, ConversationID: conversation.ID, SupersededByMailID: mail.ID, SourceTime: mail.SourceTime.Add(-time.Second), DiscoveredAt: time.Now(), InputVersion: 1}
		seedSupersededMember(t, repo, f.owner, hidden)
		draft := EmailDraft{ID: "delete-draft", MailboxID: f.box.ID, Mode: "reply", ReplyMailID: mail.ID, ConversationID: conversation.ID, To: []string{"supplier@example.com"}, Subject: "Re: purchase", Body: "Reply body"}
		_, err = repo.ChangeEmailDraft(t.Context(), EmailDraftCommand{OwnerID: f.owner, Action: "save", Draft: draft})
		f.must(err)

		stale := EmailConversationDelete{EmailCommand: f.command(), ConversationID: conversation.ID, ExpectedVersion: conversation.InputVersion - 1}
		if _, err = repo.DeleteEmailConversation(t.Context(), stale); StoreErrorCodeOf(err) != StoreErrorConflict {
			t.Fatalf("stale delete accepted: %v", err)
		}
		command := EmailConversationDelete{EmailCommand: f.command(), ConversationID: conversation.ID, ExpectedVersion: conversation.InputVersion}
		deleted, err := repo.DeleteEmailConversation(t.Context(), command)
		f.must(err)
		replay, err := repo.DeleteEmailConversation(t.Context(), command)
		f.must(err)
		if deleted.DeletedMails != 2 || !reflect.DeepEqual(replay, deleted) {
			t.Fatalf("delete result is not stable: %+v %+v", deleted, replay)
		}
		if _, found, err := repo.GetEmailConversation(t.Context(), f.owner, conversation.ID); err != nil || found {
			t.Fatalf("conversation survived deletion: found=%v err=%v", found, err)
		}
		if _, found, err := repo.GetEmailMail(t.Context(), f.owner, mail.ID); err != nil || found {
			t.Fatalf("mail survived deletion: found=%v err=%v", found, err)
		}
		if _, found, err := repo.GetEmailMail(t.Context(), f.owner, hidden.ID); err != nil || found {
			t.Fatalf("superseded mail survived deletion: found=%v err=%v", found, err)
		}
		if _, found, err := repo.GetEmailCapture(t.Context(), f.owner, mail.CaptureID); err != nil || found {
			t.Fatalf("capture survived deletion: found=%v err=%v", found, err)
		}
		if _, found, err := repo.GetEmailRepresentation(t.Context(), f.owner, mail.RepresentationID); err != nil || found {
			t.Fatalf("representation survived deletion: found=%v err=%v", found, err)
		}
		if _, found, err := repo.GetEmailRenderPreview(t.Context(), f.owner, mail.RepresentationID, preview.SanitizerVersion); err != nil || found {
			t.Fatalf("render preview survived deletion: found=%v err=%v", found, err)
		}
		if _, err := repo.ListEmailDrafts(t.Context(), f.owner, draft.ID); StoreErrorCodeOf(err) != StoreErrorNotFound {
			t.Fatalf("draft survived deletion: %v", err)
		}
		jobs, err := repo.ListEmailJobs(t.Context(), EmailQuery{OwnerID: f.owner, Limit: 100})
		f.must(err)
		for _, job := range jobs {
			if job.TargetID == mail.ID || job.TargetID == conversation.ID {
				t.Fatalf("conversation job survived deletion: %+v", job)
			}
		}
	})
}

func seedSupersededMember(t *testing.T, repo EmailRepository, owner string, mail app.EmailMail) {
	t.Helper()
	seed := func(e *emailEngine) (struct{}, error) {
		emailSaveMail(e, mail)
		return struct{}{}, e.err
	}
	var err error
	switch backend := repo.(type) {
	case *MemoryStore:
		_, err = emailMemoryRun(backend, t.Context(), OperationChangeEmailAssignment, owner, "", nil, true, seed)
	case *FileStore:
		_, err = emailFileRun(backend, t.Context(), OperationChangeEmailAssignment, owner, "", nil, true, seed)
	case *PostgresStore:
		_, err = emailPostgresRun(backend, t.Context(), OperationChangeEmailAssignment, owner, "", nil, true, seed)
	default:
		t.Fatalf("unsupported backend %T", repo)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestDeleteEmailConversationRejectsUncertainSend(t *testing.T) {
	repo := NewMemoryStore()
	f := emailFixture(t, repo)
	_, err := repo.ActivateEmailEventPolicy(t.Context(), f.command())
	f.must(err)
	mail := f.parse(f.capture(f.admit("active-send", time.Now())), "<active-send@example.com>")
	mail = manualEvent(t, f, mail, "", "Active send")
	conversation, _, err := repo.GetEmailConversation(t.Context(), f.owner, mail.ConversationID)
	f.must(err)
	draft := EmailDraft{ID: "active-draft", MailboxID: f.box.ID, Mode: "reply", ReplyMailID: mail.ID, ConversationID: conversation.ID, To: []string{"supplier@example.com"}, Subject: "Re: purchase", Body: "Reply body"}
	saved, err := repo.ChangeEmailDraft(t.Context(), EmailDraftCommand{OwnerID: f.owner, Action: "save", Draft: draft})
	f.must(err)
	_, err = repo.ChangeEmailDraft(t.Context(), EmailDraftCommand{OwnerID: f.owner, Action: "begin", Draft: saved.Draft, ExpectedVersion: saved.Draft.Version, SendKey: "send-once"})
	f.must(err)
	_, err = repo.DeleteEmailConversation(t.Context(), EmailConversationDelete{EmailCommand: f.command(), ConversationID: conversation.ID, ExpectedVersion: conversation.InputVersion})
	if StoreErrorCodeOf(err) != StoreErrorConflict {
		t.Fatalf("active send delete was not rejected: %v", err)
	}
	if _, found, err := repo.GetEmailMail(t.Context(), f.owner, mail.ID); err != nil || !found {
		t.Fatalf("failed delete changed conversation: found=%v err=%v", found, err)
	}
}
