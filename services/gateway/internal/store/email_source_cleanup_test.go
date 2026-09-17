package store

import (
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func emailDatedCapture(f *emailContractFixture, m app.EmailMail, datePath string) app.EmailMail {
	f.t.Helper()
	j := f.claim(app.EmailJobCapture)
	dir := "email/" + datePath + "/" + strings.Repeat("a", 64) + "/" + f.box.ID + "/" + m.ID + "/source/capture-" + m.ID
	v, err := f.repo.PublishEmailCapture(f.t.Context(), EmailCaptureCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, Lease: f.lease(j),
		Capture: app.EmailCaptureVersion{ID: "capture-" + m.ID, MailID: m.ID, ManifestPath: dir + "/capture.json", ManifestSHA256: strings.Repeat("a", 64),
			OriginalPath: dir + "/message.eml", OriginalSHA256: strings.Repeat("b", 64), State: app.EmailCaptureComplete}})
	f.must(err)
	f.finish(j)
	return v
}

func TestPurgeEmailCapturesTombstonesAndPreservesBody(t *testing.T) {
	f := emailFixture(t, NewMemoryStore())
	mail := f.admit("message-1", time.Now().Add(-time.Hour))
	mail = emailDatedCapture(f, mail, "2026/09/07")
	mail = f.parse(mail, "<message-1@example.com>")

	at := time.Now().UTC()
	out, err := f.repo.PurgeEmailCaptures(t.Context(), EmailCapturePurgeCommand{EmailCommand: f.command(), Scope: EmailPurgeScopeMail, MailID: mail.ID, Reason: app.EmailPurgeManual, At: at})
	f.must(err)
	if len(out.Captures) != 1 || out.Remaining {
		t.Fatalf("purged=%d remaining=%v", len(out.Captures), out.Remaining)
	}

	capture, ok, err := f.repo.GetEmailCapture(t.Context(), f.owner, mail.CaptureID)
	f.must(err)
	// The pointer and hashes must survive: they are the unlink target and the
	// only evidence a crashed cleanup leaves behind.
	if !ok || capture.PurgedAt == nil || capture.PurgeReason != app.EmailPurgeManual ||
		capture.ManifestPath == "" || capture.OriginalPath == "" || capture.OriginalSHA256 == "" {
		t.Fatalf("capture=%+v ok=%v", capture, ok)
	}
	// The mail stays captured so the capture job does not download it again.
	after, ok, err := f.repo.GetEmailMail(t.Context(), f.owner, mail.ID)
	f.must(err)
	if !ok || after.CaptureState != app.EmailCaptureComplete || after.CaptureID != capture.ID {
		t.Fatalf("mail=%+v", after)
	}
	representation, ok, err := f.repo.GetEmailRepresentation(t.Context(), f.owner, after.RepresentationID)
	f.must(err)
	if !ok || representation.BodyText != "Please approve this purchase." {
		t.Fatalf("body must survive a purge: %+v", representation)
	}
}

func TestPurgeEmailCapturesIsIdempotentAndRejectsForgedMarkers(t *testing.T) {
	f := emailFixture(t, NewMemoryStore())
	mail := emailDatedCapture(f, f.admit("message-1", time.Now().Add(-time.Hour)), "2026/09/07")
	first := time.Now().UTC()
	_, err := f.repo.PurgeEmailCaptures(t.Context(), EmailCapturePurgeCommand{EmailCommand: f.command(), Scope: EmailPurgeScopeMail, MailID: mail.ID, Reason: app.EmailPurgeManual, At: first})
	f.must(err)
	// A second purge must not re-stamp, otherwise a scoped sweep never converges.
	out, err := f.repo.PurgeEmailCaptures(t.Context(), EmailCapturePurgeCommand{EmailCommand: f.command(), Scope: EmailPurgeScopeMail, MailID: mail.ID, Reason: app.EmailPurgeManual, At: first.Add(time.Hour)})
	f.must(err)
	if len(out.Captures) != 0 {
		t.Fatalf("second purge restamped %d captures", len(out.Captures))
	}
	capture, _, err := f.repo.GetEmailCapture(t.Context(), f.owner, mail.CaptureID)
	f.must(err)
	if !capture.PurgedAt.Equal(first) {
		t.Fatalf("purged at %v want %v", capture.PurgedAt, first)
	}

	// Only the purge command may set these markers.
	next := f.admit("message-2", time.Now().Add(-time.Hour))
	j := f.claim(app.EmailJobCapture)
	forged := app.EmailCaptureVersion{ID: "capture-" + next.ID, MailID: next.ID, ManifestPath: "source/capture.json", ManifestSHA256: strings.Repeat("a", 64),
		OriginalPath: "source/message.eml", OriginalSHA256: strings.Repeat("b", 64), State: app.EmailCaptureComplete, PurgedAt: &first, PurgeReason: app.EmailPurgeManual}
	if _, err := f.repo.PublishEmailCapture(t.Context(), EmailCaptureCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, Lease: f.lease(j), Capture: forged}); StoreErrorCodeOf(err) != StoreErrorInvalid {
		t.Fatalf("forged purge marker accepted: %v", err)
	}
}

func TestPurgeScopeSelectsOnlyTheRequestedDate(t *testing.T) {
	f := emailFixture(t, NewMemoryStore())
	kept := emailDatedCapture(f, f.admit("message-1", time.Now().Add(-2*time.Hour)), "2026/09/07")
	purged := emailDatedCapture(f, f.admit("message-2", time.Now().Add(-time.Hour)), "2026/09/08")

	out, err := f.repo.PurgeEmailCaptures(t.Context(), EmailCapturePurgeCommand{EmailCommand: f.command(), Scope: EmailPurgeScopeDate, DatePath: "2026/09/08", Reason: app.EmailPurgeManual, At: time.Now().UTC()})
	f.must(err)
	if len(out.Captures) != 1 || out.Captures[0].ID != purged.CaptureID {
		t.Fatalf("date scope selected %+v", out.Captures)
	}
	untouched, _, err := f.repo.GetEmailCapture(t.Context(), f.owner, kept.CaptureID)
	f.must(err)
	if untouched.PurgedAt != nil {
		t.Fatal("a neighbouring day was purged")
	}
}

func TestPurgeRejectsUnknownScopeAndReason(t *testing.T) {
	f := emailFixture(t, NewMemoryStore())
	mail := emailDatedCapture(f, f.admit("message-1", time.Now().Add(-time.Hour)), "2026/09/07")
	for name, c := range map[string]EmailCapturePurgeCommand{
		"unknown scope":  {Scope: "everything", MailID: mail.ID, Reason: app.EmailPurgeManual, At: time.Now()},
		"unknown reason": {Scope: EmailPurgeScopeMail, MailID: mail.ID, Reason: "because", At: time.Now()},
		"missing time":   {Scope: EmailPurgeScopeMail, MailID: mail.ID, Reason: app.EmailPurgeManual},
		"bad date":       {Scope: EmailPurgeScopeDate, DatePath: "2026/13/01", Reason: app.EmailPurgeManual, At: time.Now()},
		"no mailbox":     {Scope: EmailPurgeScopeMailbox, Reason: app.EmailPurgeManual, At: time.Now()},
	} {
		t.Run(name, func(t *testing.T) {
			c.EmailCommand = f.command()
			if _, err := f.repo.PurgeEmailCaptures(t.Context(), c); StoreErrorCodeOf(err) != StoreErrorInvalid {
				t.Fatalf("err=%v", err)
			}
		})
	}
}
