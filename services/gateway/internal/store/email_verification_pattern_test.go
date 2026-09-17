package store

import (
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestVerificationPatternClassificationSkipsGeneratedAnalysis(t *testing.T) {
	repo := NewMemoryStore()
	f := emailFixture(t, repo)
	_, err := repo.ActivateEmailEventPolicy(t.Context(), f.command())
	f.must(err)
	mail := f.capture(f.admit("verification-pattern", time.Now()))
	parse := f.claim(app.EmailJobParse)
	mail, err = repo.PublishEmailRepresentation(t.Context(), EmailRepresentationCommand{EmailCommand: f.command(), Lease: f.lease(parse), Representation: app.EmailRepresentation{
		ID: "representation-" + mail.ID, MailID: mail.ID, CaptureID: mail.CaptureID, Subject: "Your sign-in code", From: []string{"security@example.com"}, To: []string{f.box.Address}, BodyText: "Your verification code is 482193.", State: app.EmailParseReady, Coverage: "complete", ParserVersion: "v1", ManifestPath: "representations/message.json", ManifestSHA256: strings.Repeat("c", 64),
	}})
	f.must(err)
	f.finish(parse)

	classification := f.claim(app.EmailJobClassification)
	mail, err = repo.PublishEmailClassification(t.Context(), EmailClassificationCommand{EmailCommand: f.command(), Lease: f.lease(classification), MailID: mail.ID, Generation: classification.Generation, Classification: app.EmailClassification{
		Category: "notification", NotificationSubtype: "verification", Source: app.EmailClassificationSourcePattern, ReasonCode: app.EmailClassificationReasonVerificationPattern, PromptVersion: app.EmailClassificationVerificationPatternVersion, Stage: "body", EvidenceRefs: []string{"representation:" + mail.RepresentationID + ":body"}, InputFingerprint: classification.InputFingerprint,
	}})
	f.must(err)
	f.finish(classification)
	if !emailPatternVerification(mail) {
		t.Fatalf("pattern classification was not preserved: %+v", mail.Classification)
	}

	for _, kind := range []string{app.EmailJobMessageSummary, app.EmailJobAssignment} {
		if _, claimed, claimErr := repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{kind}, LeaseDuration: time.Minute}); claimErr != nil || claimed {
			t.Fatalf("%s claimed=%v err=%v", kind, claimed, claimErr)
		}
	}
	target, found, err := repo.GetEmailAnalysisTarget(t.Context(), f.owner, app.EmailJobMessageSummary, mail.ID)
	f.must(err)
	if !found || target.State != app.EmailSummaryCurrent || target.SummaryID != "" {
		t.Fatalf("summary target should be a completed no-op: %+v", target)
	}
}

func TestStoreRejectsForgedPatternClassification(t *testing.T) {
	repo := NewMemoryStore()
	f := emailFixture(t, repo)
	_, err := repo.ActivateEmailEventPolicy(t.Context(), f.command())
	f.must(err)
	mail := f.capture(f.admit("forged-pattern", time.Now()))
	parse := f.claim(app.EmailJobParse)
	mail, err = repo.PublishEmailRepresentation(t.Context(), EmailRepresentationCommand{EmailCommand: f.command(), Lease: f.lease(parse), Representation: app.EmailRepresentation{ID: "representation-" + mail.ID, MailID: mail.ID, CaptureID: mail.CaptureID, Subject: "Invoice", BodyText: "Invoice 482193", State: app.EmailParseReady, Coverage: "complete", ParserVersion: "v1", ManifestPath: "representations/message.json", ManifestSHA256: strings.Repeat("c", 64)}})
	f.must(err)
	f.finish(parse)
	classification := f.claim(app.EmailJobClassification)
	_, err = repo.PublishEmailClassification(t.Context(), EmailClassificationCommand{EmailCommand: f.command(), Lease: f.lease(classification), MailID: mail.ID, Generation: classification.Generation, Classification: app.EmailClassification{Category: "interaction", Source: app.EmailClassificationSourcePattern, ReasonCode: app.EmailClassificationReasonVerificationPattern, PromptVersion: app.EmailClassificationVerificationPatternVersion, Stage: "body", EvidenceRefs: []string{"representation:" + mail.RepresentationID + ":body"}, InputFingerprint: classification.InputFingerprint}})
	if StoreErrorCodeOf(err) != StoreErrorInvalid {
		t.Fatalf("forged pattern error = %v", err)
	}
}
