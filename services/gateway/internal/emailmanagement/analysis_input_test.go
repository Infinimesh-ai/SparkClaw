package emailmanagement

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestLongReplyHistoryCanPublishContextAndCompleteAnalysis(t *testing.T) {
	repo := store.NewMemoryStore()
	s, _, _ := newFixtureService(t, repo)
	box, err := s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.discover(t.Context(), app.EmailJob{OwnerID: "email-owner", MailboxID: box.ID, BindingGeneration: box.BindingGeneration}); err != nil {
		t.Fatal(err)
	}
	if worked, err := s.workOne(t.Context(), []string{app.EmailJobCapture}); err != nil || !worked {
		t.Fatalf("capture: %v", err)
	}
	job, found, err := repo.ClaimEmailJob(t.Context(), store.EmailJobClaim{OwnerID: "email-owner", Kinds: []string{app.EmailJobParse}})
	if err != nil || !found {
		t.Fatalf("parse claim: %v", err)
	}
	mail, found, err := repo.GetEmailMail(t.Context(), "email-owner", job.TargetID)
	if err != nil || !found {
		t.Fatalf("mail: %v", err)
	}
	// A realistic long RFC References chain exceeds both repository limits for
	// context references and dependency registrations when used without a window.
	references := make([]string, 240)
	for i := range references {
		references[i] = fmt.Sprintf("<parent-%03d@example.test>", i)
	}
	representation := app.EmailRepresentation{ID: "long-reply-source", MailID: mail.ID, CaptureID: mail.CaptureID,
		Subject: "Re: Purchase approval", From: []string{"sender@example.test"}, To: []string{"owner@example.com"},
		MessageID: "<latest@example.test>", ReplyReferences: references, BodyText: "Please approve the revised purchase.", State: app.EmailParseReady}
	if _, err = repo.PublishEmailRepresentation(t.Context(), store.EmailRepresentationCommand{EmailCommand: command("email-owner", "long-reply-source"), Lease: lease(job, s.now()), Representation: representation}); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.FinishEmailJob(t.Context(), store.EmailJobFinish{EmailJobLease: lease(job, s.now())}); err != nil {
		t.Fatal(err)
	}
	input, _, _, err := s.buildAnalysis(t.Context(), app.EmailJob{OwnerID: "email-owner", TargetID: mail.ID, Kind: app.EmailJobMessageSummary})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(input.MissingContext, "reply_reference_window_limited") {
		t.Fatal("long reply history was silently truncated")
	}
	if len(input.Evidence) == 0 || !strings.Contains(input.Evidence[0].Text, references[239]) || strings.Contains(input.Evidence[0].Text, references[0]) {
		t.Fatal("model headers did not prioritize recent reply evidence")
	}
	mail, _, _ = repo.GetEmailMail(t.Context(), "email-owner", mail.ID)
	contextVersion, found, err := repo.GetEmailContext(t.Context(), "email-owner", mail.ContextID)
	if err != nil || !found || len(contextVersion.UnresolvedReferences) != app.EmailAnalysisReferenceLimit ||
		!slices.Contains(contextVersion.UnresolvedReferences, references[239]) || slices.Contains(contextVersion.UnresolvedReferences, references[0]) {
		t.Fatalf("bounded context unavailable: %+v %v", contextVersion, err)
	}
	source, _, err := repo.GetEmailRepresentation(t.Context(), "email-owner", mail.RepresentationID)
	if err != nil || !slices.Equal(source.ReplyReferences, references) || !slices.Equal(mail.ReplyReferences, references) {
		t.Fatal("analysis truncation changed source facts")
	}
	for range 16 {
		if err = s.plan(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err = s.workOne(t.Context(), []string{app.EmailJobClassification, app.EmailJobMessageSummary}); err != nil {
			t.Fatal(err)
		}
		mail, _, err = repo.GetEmailMail(t.Context(), "email-owner", mail.ID)
		if err != nil {
			t.Fatal(err)
		}
		if mail.Summary != nil && mail.Summary.Current {
			return
		}
	}
	t.Fatal("long references prevented summary publication")
}
