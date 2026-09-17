package emailmanagement

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestCommittedSourceRecoveryAfterJournalRemoval(t *testing.T) {
	for _, damage := range []string{"missing", "corrupt"} {
		t.Run(damage, func(t *testing.T) {
			s, repo, browser, mail := cleanupFixture(t)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			if err := s.Close(ctx); err != nil {
				t.Fatal(err)
			}
			capture, found, err := repo.GetEmailCapture(t.Context(), "email-owner", mail.CaptureID)
			if err != nil || !found {
				t.Fatal("capture missing", err)
			}
			original := filepath.Join(browser.root, filepath.FromSlash(capture.OriginalPath))
			if damage == "missing" {
				err = os.Remove(original)
			} else {
				var bytes []byte
				bytes, err = os.ReadFile(original)
				if err == nil {
					bytes[len(bytes)-1] ^= 1
					err = os.WriteFile(original, bytes, 0600)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			calls := browser.captureCalls()
			if err = s.reconcileCommittedSources(t.Context()); err != nil {
				t.Fatal(err)
			}
			after, _, err := repo.GetEmailMail(t.Context(), "email-owner", mail.ID)
			if err != nil || after.CaptureState != app.EmailCaptureSourceMissing || after.QualifiedFailureCount != 0 {
				t.Fatalf("source failure not retained: %+v %v", after, err)
			}
			box, _, err := repo.GetEmailMailbox(t.Context(), "email-owner", mail.MailboxID)
			if err != nil {
				t.Fatal(err)
			}
			checkpoint, err := repo.BeginEmailSync(t.Context(), store.EmailSyncBeginCommand{EmailCommand: command("email-owner", "restart-test"), MailboxID: box.ID, BindingGeneration: box.BindingGeneration, ProviderMode: app.EmailProviderModeTimeRange, UpperBound: time.Now().UTC().Add(time.Second), Trigger: "test", Actor: "test"})
			if err != nil || len(checkpoint.RetryFailures) != 1 || checkpoint.RetryFailures[0].MailID != mail.ID {
				t.Fatalf("missing exact retry: %+v %v", checkpoint, err)
			}
			if browser.captureCalls() != calls {
				t.Fatal("startup recovery accessed provider")
			}
		})
	}
}
