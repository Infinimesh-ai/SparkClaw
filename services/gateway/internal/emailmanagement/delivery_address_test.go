package emailmanagement

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type deliveryPageFixture struct {
	*pageFixture
	verified bool
}

func (f *deliveryPageFixture) CollectPageForOwner(ctx context.Context, owner string, r app.EmailReadRequest) (app.EmailPageResult, error) {
	p, err := f.pageFixture.CollectPageForOwner(ctx, owner, r)
	if f.verified {
		p.Discovery.DeliveryAddress = "delivery@example.com"
		p.Discovery.AccountAliases = []string{"delivery@example.com", "second@example.com"}
	}
	return p, err
}

func TestFreshPageRefreshesDeliveryProjectionWithoutRebinding(t *testing.T) {
	repo := store.NewMemoryStore()
	s, base, _ := newFixtureService(t, repo)
	browser := &deliveryPageFixture{pageFixture: &pageFixture{intakeFixture: base, pending: map[string]app.EmailPageResult{}}, verified: true}
	s.browser = browser
	now := time.Now().UTC().Truncate(time.Microsecond)
	s.now = func() time.Time { return now }
	box, err := s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.plan(t.Context()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if worked, e := s.workOne(t.Context(), []string{app.EmailJobDiscover}); e != nil || !worked {
		t.Fatalf("page %t %v", worked, e)
	}
	updated, ok, err := repo.GetEmailMailbox(t.Context(), "email-owner", box.ID)
	if err != nil || !ok || updated.Address != box.Address || updated.BindingGeneration != box.BindingGeneration || updated.Version != box.Version || updated.DeliveryAddress != "delivery@example.com" {
		t.Fatalf("refresh %+v %v", updated, err)
	}
	projected := ProjectMailbox(updated)
	if projected.Address != box.Address || projected.DeliveryAddress != updated.DeliveryAddress || !reflect.DeepEqual(projected.AccountAliases, updated.AccountAliases) {
		t.Fatal("query lost stable identity or metadata")
	}
	// An overlap may retain a cached identity while the fresh tail loses it.
	// That combination must withdraw the projection rather than re-certify it.
	a := timelinePagePart{page: app.EmailPageResult{Discovery: app.EmailDiscoveryResult{DeliveryAddress: updated.DeliveryAddress, AccountAliases: updated.AccountAliases}}}
	b := timelinePagePart{page: app.EmailPageResult{Discovery: app.EmailDiscoveryResult{}}}
	if address, aliases, e := pageDeliveryIdentity([]timelinePagePart{a, b}); e != nil || address != "" || len(aliases) != 0 {
		t.Fatal("missing tail evidence reused earlier metadata")
	}
	b.page.Discovery = app.EmailDiscoveryResult{DeliveryAddress: "foreign@example.com", AccountAliases: []string{"foreign@example.com"}}
	if address, aliases, e := pageDeliveryIdentity([]timelinePagePart{a, b}); e != nil || address != "" || len(aliases) != 0 {
		t.Fatal("changed native default blocked collection or retained disputed metadata")
	}
}

func TestReplyAllExcludesVerifiedAliasesAndKeepsStableSendIdentity(t *testing.T) {
	repo := store.NewMemoryStore()
	s, base, _ := newFixtureService(t, repo)
	browser := &deliveryPageFixture{pageFixture: &pageFixture{intakeFixture: base, pending: map[string]app.EmailPageResult{}}, verified: true}
	s.browser = browser
	box, err := s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.plan(t.Context()); err != nil {
		t.Fatal(err)
	}
	if worked, e := s.workOne(t.Context(), []string{app.EmailJobDiscover}); e != nil || !worked {
		t.Fatalf("page %t %v", worked, e)
	}
	mails, e := repo.ListEmailMails(t.Context(), store.EmailQuery{OwnerID: "email-owner", MailboxID: box.ID, Limit: 10})
	if e != nil || len(mails.Items) != 1 {
		t.Fatalf("mail %v %v", mails, e)
	}
	draft, e := s.SaveDraft(t.Context(), "email-owner", store.EmailDraft{ID: "reply-alias", MailboxID: box.ID, Mode: "reply_all", ReplyMailID: mails.Items[0].ID, To: []string{box.Address, "DELIVERY@EXAMPLE.COM", "SECOND@example.com", "outside@example.com"}, CC: []string{"delivery@example.com", "cc@example.com"}, Subject: "reply", Body: "reply"}, 0)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(draft.To, []string{"outside@example.com"}) || !reflect.DeepEqual(draft.CC, []string{"cc@example.com"}) || draft.ReplyTarget.AccountAddress != box.Address || draft.MailboxID != box.ID {
		t.Fatalf("reply alias filter/identity %+v", draft)
	}
	compose := &composeFixture{intakeFixture: base, address: box.Address}
	s.browser = compose
	direct, e := s.SaveDraft(t.Context(), "email-owner", store.EmailDraft{ID: "alias-send", MailboxID: box.ID, To: []string{"outside@example.com"}, Subject: "send", Body: "send"}, 0)
	if e != nil {
		t.Fatal(e)
	}
	sent, e := s.SendDraft(t.Context(), "email-owner", direct.ID, direct.Version, "fresh-click")
	if e != nil || sent.State != "sent" || compose.sends != 1 {
		t.Fatalf("delivery metadata changed send admission identity: %+v %v", sent, e)
	}
}
