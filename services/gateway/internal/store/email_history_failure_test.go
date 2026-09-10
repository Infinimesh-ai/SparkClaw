package store

import (
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"testing"
	"time"
)

func TestEmailHistoryFailurePersistsPerThreadAndClearsOnRecovery(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		activateEvents(t, f)
		observation := EmailDiscoveryCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, ThreadID: "history-thread", ProviderSelectionID: "selection", Folder: "inbox", Coverage: "partial", Cursor: "next-page", Gaps: []string{"batch_limit"}, Trigger: app.EmailJobThreadSync, ObservedAt: time.Now()}
		_, err := repo.AdmitEmailDiscovery(t.Context(), observation)
		f.must(err)
		threadID := EmailProviderThreadID(f.box.ID, "history-thread")
		before, _, err := repo.GetEmailThread(t.Context(), f.owner, threadID)
		f.must(err)
		request := EmailJobRequest{EmailCommand: f.command(), Kind: app.EmailJobThreadSync, TargetID: threadID, MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, Rearm: true}
		_, err = repo.RequestEmailJob(t.Context(), request)
		f.must(err)
		job := f.claim(app.EmailJobThreadSync)
		_, err = repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(job), ErrorCode: "email_pinned_message_unavailable"})
		f.must(err)
		if file, ok := repo.(*FileStore); ok {
			reopened, err := NewFileStore(file.path)
			f.must(err)
			repo, f.repo = reopened, reopened
		}
		// Ordinary discovery succeeds later and clears the mailbox error only.
		_, err = repo.AdmitEmailDiscovery(t.Context(), EmailDiscoveryCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, Coverage: "partial", ObservedAt: time.Now()})
		f.must(err)
		failed, _, err := repo.GetEmailThread(t.Context(), f.owner, threadID)
		f.must(err)
		if failed.ErrorCode != "email_pinned_message_unavailable" || failed.Cursor != before.Cursor || failed.ObservationVersion != before.ObservationVersion || !failed.LastCheckedAt.Equal(before.LastCheckedAt) {
			t.Fatalf("failure changed source observation or disappeared: %+v", failed)
		}
		request.EmailCommand = f.command()
		_, err = repo.RequestEmailJob(t.Context(), request)
		f.must(err)
		retry := f.claim(app.EmailJobThreadSync)
		retried, _, err := repo.GetEmailThread(t.Context(), f.owner, threadID)
		f.must(err)
		if retried.ErrorCode != "" || retried.ObservationVersion != before.ObservationVersion {
			t.Fatalf("retry not cleared independently: %+v", retried)
		}
		_, err = repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(retry), ErrorCode: "email_pinned_message_unavailable"})
		f.must(err)
		observation.EmailCommand = f.command()
		_, err = repo.AdmitEmailDiscovery(t.Context(), observation)
		f.must(err)
		recovered, _, err := repo.GetEmailThread(t.Context(), f.owner, threadID)
		f.must(err)
		if recovered.ErrorCode != "" || recovered.ObservationVersion != before.ObservationVersion {
			t.Fatalf("successful identical observation failed to clear error independently: %+v", recovered)
		}
	})
}

func TestEmailEventsSupersededSendCannotKeepAnEmptyEventVisible(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		activateEvents(t, f)
		saved, err := repo.ChangeEmailDraft(t.Context(), EmailDraftCommand{OwnerID: f.owner, Action: "save", Draft: EmailDraft{ID: "unknown-send", MailboxID: f.box.ID, Subject: "Confirm order", Body: "Please confirm", To: []string{"recipient@example.test"}}})
		f.must(err)
		_, err = repo.ChangeEmailDraft(t.Context(), EmailDraftCommand{OwnerID: f.owner, Action: "begin", Draft: EmailDraft{ID: saved.Draft.ID}, ExpectedVersion: saved.Draft.Version, SendKey: "click"})
		f.must(err)
		sent, err := repo.ChangeEmailDraft(t.Context(), EmailDraftCommand{OwnerID: f.owner, Action: "finish", Draft: EmailDraft{ID: saved.Draft.ID, State: "sent"}, SendKey: "click", Receipt: &app.EmailSendResult{Provider: f.box.Provider, Status: "sent"}})
		f.must(err)
		admitted, err := repo.AdmitEmailDiscovery(t.Context(), EmailDiscoveryCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, ObservedAt: time.Now(), Coverage: "partial", Members: []EmailDiscoveryMember{{ProviderMessageID: "sent-source", ProviderSelectionID: "sent-source", Direction: "sent", Folder: "sent"}}})
		f.must(err)
		source := f.capture(admitted.Mails[0])
		_, err = repo.ChangeEmailDraft(t.Context(), EmailDraftCommand{OwnerID: f.owner, Action: "confirm_source", Draft: EmailDraft{ID: sent.Draft.ID, SentMailID: source.ID}})
		f.must(err)
		source, _, err = repo.GetEmailMail(t.Context(), f.owner, source.ID)
		f.must(err)
		moved := manualEvent(t, f, source, "", "Coordinate delivery")
		rows, err := repo.ListEmailConversations(t.Context(), EmailQuery{OwnerID: f.owner, Entry: "interaction"})
		f.must(err)
		if len(rows.Items) != 1 || rows.Items[0].ID != moved.ConversationID {
			t.Fatalf("superseded send retained old event: %+v", rows.Items)
		}
		notices, err := repo.ListEmailConversations(t.Context(), EmailQuery{OwnerID: f.owner, Entry: "notification"})
		f.must(err)
		if len(notices.Items) != 0 {
			t.Fatal("empty old event leaked into notification category")
		}
	})
}
