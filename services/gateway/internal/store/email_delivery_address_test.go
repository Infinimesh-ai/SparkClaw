package store

import (
	"reflect"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestDeliveryAddressRefreshPreservesMailboxAndBusinessIdentity(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := &emailContractFixture{t: t, repo: repo, owner: "email-owner"}
		var err error
		f.box, err = repo.BindEmailMailbox(t.Context(), EmailBindCommand{EmailCommand: f.command(), Provider: app.EmailProviderOutlook, Address: "outlook_0123456789abcdef@outlook.com", Enabled: true})
		f.must(err)
		before := f.box
		mail := f.parse(f.capture(f.admit("retained-mail", time.Now())), "<retained@example.test>")
		original, _, err := repo.GetEmailCapture(t.Context(), f.owner, mail.CaptureID)
		f.must(err)
		saved, err := repo.ChangeEmailDraft(t.Context(), EmailDraftCommand{OwnerID: f.owner, Action: "save", Draft: EmailDraft{ID: "retained-draft", OwnerID: f.owner, MailboxID: f.box.ID, To: []string{"other@example.test"}, Subject: "retained", Body: "unchanged"}})
		f.must(err)
		draft := saved.Draft
		first := beginTimelineSync(t, f, f.box.Boundary.Add(time.Minute))
		commitTimelineSync(t, f, first, func(c *EmailSyncCommitCommand) {
			c.DeliveryObserved = true
			c.DeliveryAddress = "owner@outlook.com"
			c.AccountAliases = []string{"owner@outlook.com", "second@outlook.com"}
		})
		if f.box.ID != before.ID || f.box.Address != before.Address || f.box.NormalizedAddress != before.NormalizedAddress || f.box.BindingGeneration != before.BindingGeneration || f.box.Version != before.Version || !f.box.Boundary.Equal(before.Boundary) {
			t.Fatal("delivery metadata rebound or reset the mailbox")
		}
		f.repo = restartEmailRepeatRepository(t, f.repo)
		stored, ok, err := f.repo.GetEmailMailbox(t.Context(), f.owner, f.box.ID)
		f.must(err)
		if !ok || stored.DeliveryAddress != "owner@outlook.com" || len(stored.AccountAliases) != 2 || !stored.PollThrough.Equal(first.IntervalEnd) {
			t.Fatalf("refresh not durable: %+v", stored)
		}
		afterMail, _, err := f.repo.GetEmailMail(t.Context(), f.owner, mail.ID)
		f.must(err)
		afterCapture, _, err := f.repo.GetEmailCapture(t.Context(), f.owner, mail.CaptureID)
		f.must(err)
		drafts, err := f.repo.ListEmailDrafts(t.Context(), f.owner, draft.ID)
		f.must(err)
		if !reflect.DeepEqual(mail, afterMail) || !reflect.DeepEqual(original, afterCapture) || len(drafts) != 1 || !reflect.DeepEqual(draft, drafts[0]) {
			t.Fatal("address refresh mutated retained mail, capture, or draft")
		}
		f.box = stored
		second := beginTimelineSync(t, f, first.IntervalEnd.Add(time.Minute))
		commitTimelineSync(t, f, second, func(c *EmailSyncCommitCommand) { c.DeliveryObserved = true })
		if f.box.DeliveryAddress != "" || len(f.box.AccountAliases) != 0 || f.box.ContactAddress() != "" || f.box.Address != before.Address {
			t.Fatal("missing native evidence retained unverified Outlook SMTP or changed identity")
		}
	})
}

func TestInvalidDeliveryMetadataDoesNotAdvanceBindingOrSync(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		before := f.box
		_, err := repo.BindEmailMailbox(t.Context(), EmailBindCommand{EmailCommand: f.command(), Provider: f.box.Provider, Address: f.box.Address, Enabled: true, ExpectedVersion: f.box.Version, DeliveryAddress: "foreign@example.test", AccountAliases: []string{"owner@example.test"}})
		if StoreErrorCodeOf(err) != StoreErrorInvalid {
			t.Fatalf("invalid binding err=%v", err)
		}
		stored, _, err := repo.GetEmailMailbox(t.Context(), f.owner, f.box.ID)
		f.must(err)
		if !reflect.DeepEqual(before, stored) {
			t.Fatal("rejected metadata advanced binding")
		}
		checkpoint := beginTimelineSync(t, f, f.box.Boundary.Add(time.Minute))
		before = f.box
		_, err = repo.CommitEmailSync(t.Context(), EmailSyncCommitCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, IntervalStart: checkpoint.IntervalStart, IntervalEnd: checkpoint.IntervalEnd, ProviderMode: app.EmailProviderModeTimeRange, Complete: true, DeliveryObserved: true, DeliveryAddress: "foreign@example.test", AccountAliases: []string{"owner@example.test"}})
		if StoreErrorCodeOf(err) != StoreErrorInvalid {
			t.Fatalf("invalid sync err=%v", err)
		}
		stored, _, err = repo.GetEmailMailbox(t.Context(), f.owner, f.box.ID)
		f.must(err)
		if !reflect.DeepEqual(before, stored) {
			t.Fatal("rejected metadata advanced sync")
		}
	})
}
