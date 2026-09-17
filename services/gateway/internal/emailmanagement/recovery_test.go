package emailmanagement

import (
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestOutageKeepsSourcesAndTerminatesRetries(t *testing.T) {
	repo := store.NewMemoryStore()
	s, _, model := newFixtureService(t, repo)
	_, err := s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := seedFixtureCollection(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{app.EmailJobParse} {
		if worked, err := s.workOne(t.Context(), []string{kind}); err != nil || !worked {
			t.Fatalf("%s: %v", kind, err)
		}
	}
	model.fail = true
	// Advance the test clock through a 30-minute outage. This verifies durable
	// retry timing and terminal projection; it is not a real-time soak result.
	clock := time.Now()
	s.now = func() time.Time { return clock }
	for i := 0; i < 30; i++ {
		clock = clock.Add(time.Minute)
		if err := s.plan(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err := s.workOne(t.Context(), []string{app.EmailJobClassification, app.EmailJobAssignment}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := repo.ListEmailMails(t.Context(), store.EmailQuery{OwnerID: "email-owner"})
	if err != nil || len(page.Items) != 1 {
		t.Fatal("outage lost mail")
	}
	mail := page.Items[0]
	if mail.CaptureID == "" || mail.RepresentationID == "" {
		t.Fatal("outage discarded source")
	}
	target, found, err := repo.GetEmailAnalysisTarget(t.Context(), "email-owner", app.EmailJobClassification, mail.ID)
	if err != nil || !found || target.State != app.EmailSummaryFailed {
		t.Fatalf("outage not explicit: %+v %v", target, err)
	}
	model.fail = false
	if _, err := s.Reanalyze(t.Context(), "email-owner", mail.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 16; i++ {
		if err := s.plan(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err := s.workOne(t.Context(), []string{app.EmailJobClassification, app.EmailJobAssignment}); err != nil {
			t.Fatal(err)
		}
	}
	mail, _, err = repo.GetEmailMail(t.Context(), "email-owner", mail.ID)
	if err != nil || mail.Classification == nil || mail.ConversationID == "" || mail.Summary != nil {
		t.Fatalf("retry after recovery: %+v %v", mail.Classification, err)
	}
}
