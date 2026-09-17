package emailmanagement

import (
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestVerificationPatternBypassesEveryModelJob(t *testing.T) {
	repo := store.NewMemoryStore()
	service, browser, analyzer := newFixtureService(t, repo)
	browser.subject = "Your sign-in verification code"
	browser.body = "Your verification code is 482193. It expires in ten minutes."
	if _, err := service.Configure(t.Context(), "email-owner", app.EmailProviderGmail, true, 0); err != nil {
		t.Fatal(err)
	}
	if err := seedFixtureCollection(t.Context(), service); err != nil {
		t.Fatal(err)
	}
	if worked, err := service.workOne(t.Context(), []string{app.EmailJobParse}); err != nil || !worked {
		t.Fatalf("parse worked=%v err=%v", worked, err)
	}
	if worked, err := service.workOne(t.Context(), []string{app.EmailJobClassification}); err != nil || !worked {
		t.Fatalf("classification worked=%v err=%v", worked, err)
	}
	analyzer.mu.Lock()
	calls := analyzer.calls
	analyzer.mu.Unlock()
	if calls != 0 {
		t.Fatalf("verification pattern invoked the analyzer %d times", calls)
	}
	page, err := repo.ListEmailMails(t.Context(), store.EmailQuery{OwnerID: "email-owner", Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("mail page=%+v err=%v", page, err)
	}
	classification := page.Items[0].Classification
	if classification == nil || classification.Source != app.EmailClassificationSourcePattern || classification.EffectiveEntry != "notification" || classification.NotificationSubtype != "verification" {
		t.Fatalf("classification=%+v", classification)
	}
	for _, kind := range []string{app.EmailJobMessageSummary, app.EmailJobAssignment} {
		if _, found, claimErr := repo.ClaimEmailJob(t.Context(), store.EmailJobClaim{OwnerID: "email-owner", Kinds: []string{kind}, Now: time.Now().Add(time.Hour), LeaseDuration: time.Minute}); claimErr != nil || found {
			t.Fatalf("%s found=%v err=%v", kind, found, claimErr)
		}
	}
}
