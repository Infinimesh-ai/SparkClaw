package emailmanagement

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// cleanupFixture drives the real pipeline to a captured, parsed mail so the
// cleanup assertions run against exactly what production writes.
func cleanupFixture(t *testing.T) (*Service, Repository, *intakeFixture, app.EmailMail) {
	t.Helper()
	repo := store.NewMemoryStore()
	s, browser, _ := newFixtureService(t, repo)
	if _, err := s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, true, 0); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	s.Start(ctx)
	t.Cleanup(func() {
		closeCtx, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		if err := s.Close(closeCtx); err != nil {
			t.Error(err)
		}
	})
	deadline := time.Now().Add(15 * time.Second)
	for {
		page, err := repo.ListEmailMails(t.Context(), store.EmailQuery{OwnerID: "email-owner", Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range page.Items {
			if m.CaptureID != "" && m.RepresentationID != "" {
				// Cleanup tests inspect a completed source, not concurrent model
				// projections. Drain workers so revision fences are deterministic.
				closeCtx, done := context.WithTimeout(context.Background(), 3*time.Second)
				err := s.Close(closeCtx)
				done()
				if err != nil {
					t.Fatal(err)
				}
				return s, repo, browser, m
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("pipeline did not produce a captured, parsed mail")
		}
		time.Sleep(30 * time.Millisecond)
	}
}

func TestManualCleanupTombstonesBeforeUnlinkAndKeepsTheBodyReadable(t *testing.T) {
	s, repo, browser, mail := cleanupFixture(t)
	capture, _, err := repo.GetEmailCapture(t.Context(), "email-owner", mail.CaptureID)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(browser.root, filepath.FromSlash(path.Dir(capture.ManifestPath)))
	if _, err := os.Stat(directory); err != nil {
		t.Fatalf("fixture capture missing before cleanup: %v", err)
	}

	out, err := s.CleanupSource(t.Context(), "email-owner", CleanupRequest{Scope: store.EmailPurgeScopeMail, MailID: mail.ID, CommandKey: "cleanup-1"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Purged != 1 || out.Partial || out.Freed <= 0 {
		t.Fatalf("result=%+v", out)
	}

	// The tombstone is durable.
	after, _, err := repo.GetEmailCapture(t.Context(), "email-owner", mail.CaptureID)
	if err != nil {
		t.Fatal(err)
	}
	if after.PurgedAt == nil || after.PurgeReason != app.EmailPurgeManual || after.ManifestPath != capture.ManifestPath {
		t.Fatalf("capture=%+v", after)
	}
	// The bytes are gone, and so are the now-empty date ancestors.
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("capture directory survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(browser.root, "email", "2026")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty date ancestors survived: %v", err)
	}
	// Downloading says "cleaned up", not "missing" or "corrupt".
	if _, _, err := s.OpenFile(t.Context(), "email-owner", mail.ID, "original"); !errors.Is(err, ErrPurged) {
		t.Fatalf("download after cleanup err=%v", err)
	}
	// The parsed body survives, which is what makes the action safe to offer
	// without a confirmation step.
	view, err := s.Message(t.Context(), "email-owner", mail.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.BodyText == "" {
		t.Fatal("cleanup destroyed the parsed body")
	}
}

func TestDeleteConversationRemovesSourcesAndAllMailRecords(t *testing.T) {
	s, repo, browser, mail := cleanupFixture(t)
	if _, err := repo.ActivateEmailEventPolicy(t.Context(), command("email-owner", "delete-activate-events")); err != nil {
		t.Fatal(err)
	}
	current, found, err := repo.GetEmailMail(t.Context(), "email-owner", mail.ID)
	if err != nil || !found {
		t.Fatalf("mail lookup: found=%v err=%v", found, err)
	}
	if current.ConversationID == "" {
		current, err = repo.ChangeEmailAssignment(t.Context(), store.EmailManualAssignment{EmailCommand: command("email-owner", "delete-assign"), MailID: current.ID, Title: "Conversation to remove", ExpectedVersion: current.InputVersion})
		if err != nil {
			t.Fatal(err)
		}
	}
	conversation, found, err := repo.GetEmailConversation(t.Context(), "email-owner", current.ConversationID)
	if err != nil || !found {
		t.Fatalf("conversation lookup: found=%v err=%v", found, err)
	}
	capture, found, err := repo.GetEmailCapture(t.Context(), "email-owner", current.CaptureID)
	if err != nil || !found {
		t.Fatalf("capture lookup: found=%v err=%v", found, err)
	}
	directory := filepath.Join(browser.root, filepath.FromSlash(path.Dir(capture.ManifestPath)))
	if _, err := os.Stat(directory); err != nil {
		t.Fatalf("fixture capture missing before delete: %v", err)
	}
	deleted, err := s.DeleteConversation(t.Context(), store.EmailConversationDelete{EmailCommand: command("email-owner", "delete-conversation"), ConversationID: conversation.ID, ExpectedVersion: conversation.InputVersion})
	if err != nil {
		t.Fatal(err)
	}
	if deleted.DeletedMails != 1 || deleted.FreedBytes <= 0 {
		t.Fatalf("delete result=%+v", deleted)
	}
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("conversation source survived delete: %v", err)
	}
	if _, found, err := repo.GetEmailConversation(t.Context(), "email-owner", conversation.ID); err != nil || found {
		t.Fatalf("conversation survived delete: found=%v err=%v", found, err)
	}
	if _, found, err := repo.GetEmailMail(t.Context(), "email-owner", current.ID); err != nil || found {
		t.Fatalf("mail survived delete: found=%v err=%v", found, err)
	}
	if _, found, err := repo.GetEmailCapture(t.Context(), "email-owner", current.CaptureID); err != nil || found {
		t.Fatalf("capture survived delete: found=%v err=%v", found, err)
	}
}

func TestCleanupScopeLeavesNeighbouringCapturesByteIdentical(t *testing.T) {
	s, repo, browser, mail := cleanupFixture(t)
	// A second capture on a different day, in the same workspace.
	kept, err := fixtureCaptureOn(t.Context(), browser.root, "email-owner", "invocation-2", "message-2", "2026/09/08")
	if err != nil {
		t.Fatal(err)
	}
	keptDir := filepath.Join(browser.root, filepath.FromSlash(path.Dir(kept.ManifestPath)))
	before, err := os.ReadFile(filepath.Join(keptDir, "capture.json"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.CleanupSource(t.Context(), "email-owner", CleanupRequest{Scope: store.EmailPurgeScopeMail, MailID: mail.ID, CommandKey: "cleanup-1"}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(keptDir, "capture.json"))
	if err != nil {
		t.Fatalf("neighbouring capture was removed: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("neighbouring capture was modified")
	}
	if _, _, err := repo.GetEmailCapture(t.Context(), "email-owner", mail.CaptureID); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupNeverEscapesTheWorkspaceRoot(t *testing.T) {
	s, repo, browser, mail := cleanupFixture(t)
	outside := filepath.Join(t.TempDir(), "evidence.txt")
	if err := os.WriteFile(outside, []byte("must survive"), 0600); err != nil {
		t.Fatal(err)
	}
	capture, _, err := repo.GetEmailCapture(t.Context(), "email-owner", mail.CaptureID)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(browser.root, filepath.FromSlash(path.Dir(capture.ManifestPath)))
	if err := os.Symlink(outside, filepath.Join(directory, "escape.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CleanupSource(t.Context(), "email-owner", CleanupRequest{Scope: store.EmailPurgeScopeMail, MailID: mail.ID, CommandKey: "cleanup-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("cleanup followed a symlink out of the workspace: %v", err)
	}
}

func TestCleanupRejectsMalformedScopes(t *testing.T) {
	s, _, _, mail := cleanupFixture(t)
	for name, req := range map[string]CleanupRequest{
		"unknown scope": {Scope: "everything", CommandKey: "k"},
		"mail no id":    {Scope: store.EmailPurgeScopeMail, CommandKey: "k"},
		"mailbox no id": {Scope: store.EmailPurgeScopeMailbox, CommandKey: "k"},
		"bad date":      {Scope: store.EmailPurgeScopeDate, Date: "2026/09/07", CommandKey: "k"},
		"no command":    {Scope: store.EmailPurgeScopeMail, MailID: mail.ID},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := s.CleanupSource(t.Context(), "email-owner", req); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestOriginalAvailableReflectsTheFilesystemNotJustThePointer(t *testing.T) {
	s, repo, browser, mail := cleanupFixture(t)
	view, err := s.Message(t.Context(), "email-owner", mail.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !view.OriginalAvailable || view.OriginalPurged {
		t.Fatalf("baseline view=%+v", view)
	}
	// Remove the bytes behind the pointer without going through cleanup, the way
	// an external deletion or a lost volume would. The projection must not keep
	// offering a download that cannot succeed.
	capture, _, err := repo.GetEmailCapture(t.Context(), "email-owner", mail.CaptureID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(browser.root, filepath.FromSlash(capture.OriginalPath))); err != nil {
		t.Fatal(err)
	}
	view, err = s.Message(t.Context(), "email-owner", mail.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.OriginalAvailable {
		t.Fatal("projection still advertises an original that is gone from disk")
	}
	// Gone by accident is not the same as cleaned up on purpose.
	if view.OriginalPurged {
		t.Fatal("an externally deleted original must not report as manually cleaned up")
	}
}

func TestRenderPreviewSurvivesSourceCleanup(t *testing.T) {
	s, _, _, mail := cleanupFixture(t)
	before, err := s.RenderPreview(t.Context(), "email-owner", mail.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.State != app.EmailRenderReady || len(before.Content) == 0 {
		t.Fatalf("baseline preview=%+v", before)
	}
	if _, err := s.CleanupSource(t.Context(), "email-owner", CleanupRequest{Scope: store.EmailPurgeScopeMail, MailID: mail.ID, CommandKey: "cleanup-1"}); err != nil {
		t.Fatal(err)
	}
	after, err := s.RenderPreview(t.Context(), "email-owner", mail.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != app.EmailRenderReady || !reflect.DeepEqual(after.Content, before.Content) {
		t.Fatalf("render preview changed after source cleanup")
	}
}

func TestRenderPreviewMigrationReusesValidatedCurrentPreview(t *testing.T) {
	s, _, _, _ := cleanupFixture(t)
	report, err := s.MigrateRenderPreviews(t.Context(), "email-owner")
	if err != nil {
		t.Fatal(err)
	}
	if report.Total != 1 || report.Reused != 1 || report.Migrated != 0 || report.Failed != 0 || len(report.Failures) != 0 {
		t.Fatalf("report=%+v", report)
	}
}
