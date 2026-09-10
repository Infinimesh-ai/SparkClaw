package store

import (
	"fmt"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"path/filepath"
	"testing"
	"time"
)

func TestEmailComposeTimelineAllBackends(t *testing.T) {
	for _, backend := range []string{"memory", "file", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var repo EmailRepository = NewMemoryStore()
			if backend == "file" {
				s, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
				if err != nil {
					t.Fatal(err)
				}
				repo = s
			}
			if backend == "postgres" {
				s, err := NewPostgresStore(t.Context(), newPostgresMigrationTestSchema(t))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(s.Close)
				repo = s
			}
			f := emailFixture(t, repo)
			send := func(id, native string) EmailDraft {
				saved, err := repo.ChangeEmailDraft(t.Context(), EmailDraftCommand{OwnerID: f.owner, Action: "save", Draft: EmailDraft{ID: id, MailboxID: f.box.ID, Subject: "user subject", Body: "user body", To: []string{"recipient@example.test"}, CC: []string{"copy@example.test"}}})
				f.must(err)
				_, err = repo.ChangeEmailDraft(t.Context(), EmailDraftCommand{OwnerID: f.owner, Action: "begin", Draft: EmailDraft{ID: id}, ExpectedVersion: saved.Draft.Version, SendKey: "click"})
				f.must(err)
				out, err := repo.ChangeEmailDraft(t.Context(), EmailDraftCommand{OwnerID: f.owner, Action: "finish", Draft: EmailDraft{ID: id, State: "sent"}, SendKey: "click", Receipt: &app.EmailSendResult{Provider: f.box.Provider, Status: "sent", ProviderMessageID: native}})
				f.must(err)
				return out.Draft
			}
			outbound := func(native string) app.EmailMail {
				result, err := repo.AdmitEmailDiscovery(t.Context(), EmailDiscoveryCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, ObservedAt: time.Now(), Coverage: "partial", Members: []EmailDiscoveryMember{{ProviderMessageID: native, ProviderSelectionID: native, Direction: "sent", Folder: "sent"}}})
				f.must(err)
				return result.Mails[0]
			}
			d := send("known", "out-known")
			list, err := repo.ListEmailMails(t.Context(), EmailQuery{OwnerID: f.owner, ConversationID: d.ConversationID, CapturedOnly: true})
			f.must(err)
			if len(list.Items) != 1 || list.Items[0].CaptureID != "" || list.Items[0].MessageID != "" || list.Items[0].LocalSendID == "" {
				t.Fatal("local projection fabricated original or missing")
			}
			captured := f.capture(outbound("out-known"))
			if captured.ID != d.TimelineMailID {
				t.Fatal("capture created second timeline identity")
			}
			list, err = repo.ListEmailMails(t.Context(), EmailQuery{OwnerID: f.owner, ConversationID: d.ConversationID, CapturedOnly: true})
			f.must(err)
			if len(list.Items) != 1 {
				t.Fatal("duplicate known send card")
			}
			unknown := send("without-id", "")
			_, err = repo.MarkEmailMailsViewed(t.Context(), EmailViewedCommand{EmailCommand: f.command(), MailIDs: []string{unknown.TimelineMailID}})
			f.must(err)
			source := f.capture(outbound("owner-confirmed-source"))
			out, err := repo.ChangeEmailDraft(t.Context(), EmailDraftCommand{OwnerID: f.owner, Action: "confirm_source", Draft: EmailDraft{ID: unknown.ID, SentMailID: source.ID}})
			f.must(err)
			if out.Draft.ConfirmationSource != "owner_confirmed_capture" {
				t.Fatal("owner confirmation mislabeled")
			}
			list, err = repo.ListEmailMails(t.Context(), EmailQuery{OwnerID: f.owner, ConversationID: unknown.ConversationID, CapturedOnly: true})
			f.must(err)
			if len(list.Items) != 1 || list.Items[0].ID != source.ID || list.Items[0].ViewedAt == nil {
				t.Fatal("duplicate/seen receipt regression")
			}
			conv, ok, err := repo.GetEmailConversation(t.Context(), f.owner, unknown.ConversationID)
			f.must(err)
			if !ok || conv.MemberCount != 1 || conv.UnseenCount != 0 {
				t.Fatalf("member/unseen counts %+v", conv)
			}
			replay, err := repo.ChangeEmailDraft(t.Context(), EmailDraftCommand{OwnerID: f.owner, Action: "confirm_source", Draft: EmailDraft{ID: unknown.ID, SentMailID: source.ID}})
			f.must(err)
			if replay.Draft.Version != out.Draft.Version {
				t.Fatal("confirmation replay changed version")
			}
			if _, err = repo.ChangeEmailDraft(t.Context(), EmailDraftCommand{OwnerID: f.owner, Action: "confirm_source", Draft: EmailDraft{ID: unknown.ID, SentMailID: captured.ID}}); err == nil {
				t.Fatal("rebound confirmed send")
			}
			viewed := f.capture(outbound("already-viewed"))
			_, err = repo.MarkEmailMailsViewed(t.Context(), EmailViewedCommand{EmailCommand: f.command(), MailIDs: []string{viewed.ID}})
			f.must(err)
			viewedDraft := send("viewed-send", "already-viewed")
			viewedConv, _, err := repo.GetEmailConversation(t.Context(), f.owner, viewedDraft.ConversationID)
			f.must(err)
			if viewedConv.UnseenCount != 0 {
				t.Fatal("viewed existing source became unseen")
			}
			// Captured source fields are not replaced with editable snapshot facts.
			preserved, ok, err := repo.GetEmailMail(t.Context(), f.owner, source.ID)
			f.must(err)
			if !ok || preserved.Subject != source.Subject || preserved.ProviderThreadID != source.ProviderThreadID {
				t.Fatal("source facts overwritten")
			}
		})
	}
}

func TestEmailComposeExistingNotificationMemberUnseenTransition(t *testing.T) {
	for _, provisional := range []bool{false, true} {
		for _, manual := range []bool{false, true} {
			t.Run(fmt.Sprintf("provisional=%t/manual=%t", provisional, manual), func(t *testing.T) {
				repo := NewMemoryStore()
				f := emailFixture(t, repo)
				source := f.capture(f.admit("native-notice", time.Now()))
				saved, err := repo.ChangeEmailDraft(t.Context(), EmailDraftCommand{OwnerID: f.owner, Action: "save", Draft: EmailDraft{ID: "draft", MailboxID: f.box.ID, Body: "confirmation", Subject: "reply"}})
				f.must(err)
				_, err = repo.ChangeEmailDraft(t.Context(), EmailDraftCommand{OwnerID: f.owner, Action: "begin", Draft: EmailDraft{ID: "draft"}, ExpectedVersion: saved.Draft.Version, SendKey: "click"})
				f.must(err)
				conversationID := "existing"
				if provisional {
					local, err := repo.ChangeEmailDraft(t.Context(), EmailDraftCommand{OwnerID: f.owner, Action: "finish", Draft: EmailDraft{ID: "draft", State: "sent"}, SendKey: "click", Receipt: &app.EmailSendResult{Provider: f.box.Provider, Status: "sent"}})
					f.must(err)
					conversationID = local.Draft.ConversationID
				}
				_, err = emailMemoryRun(repo, t.Context(), OperationChangeEmailDraft, f.owner, "", nil, true, func(e *emailEngine) (bool, error) {
					conv, exists := emailGet[app.EmailConversation](e, "conversation", conversationID)
					if !exists {
						conv = app.EmailConversation{ID: conversationID, OwnerID: f.owner, CreatedAt: e.now, UpdatedAt: e.now}
					}
					conv.MemberCount++
					conv.MembershipVersion++
					emailSaveConversation(e, conv)
					source.Direction = "sent"
					source.ConversationID = conversationID
					source.AssignmentState = app.EmailAssignmentAssigned
					origin := "model"
					if manual {
						origin = "manual"
					}
					source.Classification = &app.EmailClassification{Category: "notification", EffectiveEntry: "notification", Source: origin, Revision: 1, State: "ready"}
					emailSaveMail(e, source)
					return true, e.err
				})
				f.must(err)
				action := "finish"
				if provisional {
					action = "resolve"
				}
				_, err = repo.ChangeEmailDraft(t.Context(), EmailDraftCommand{OwnerID: f.owner, Action: action, Draft: EmailDraft{ID: "draft", State: "sent"}, SendKey: "click", Receipt: &app.EmailSendResult{Provider: f.box.Provider, Status: "sent", ProviderMessageID: source.ProviderMessageID}})
				f.must(err)
				conv, _, err := repo.GetEmailConversation(t.Context(), f.owner, conversationID)
				f.must(err)
				wantUnseen := 1
				if manual {
					wantUnseen = 0
				}
				if conv.MemberCount != 1 || conv.UnseenCount != wantUnseen {
					t.Fatalf("members=%d unseen=%d want=1/%d", conv.MemberCount, conv.UnseenCount, wantUnseen)
				}
			})
		}
	}
}
