package emailmanagement

import (
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// A cleanup that commits its tombstone and then dies leaves bytes behind. The
// recovery pass is what guarantees they do not stay there.
func TestRecoveryUnlinksCapturesTombstonedByACrashedCleanup(t *testing.T) {
	s, repo, browser, mail := cleanupFixture(t)
	capture, _, err := repo.GetEmailCapture(t.Context(), "email-owner", mail.CaptureID)
	if err != nil {
		t.Fatal(err)
	}
	// Tombstone through the repository directly, bypassing the service, so the
	// files survive exactly as they would after a crash between the two steps.
	if _, err := repo.PurgeEmailCaptures(t.Context(), store.EmailCapturePurgeCommand{
		EmailCommand: store.EmailCommand{OwnerID: "email-owner", CommandKey: "crashed-cleanup"},
		Scope:        store.EmailPurgeScopeMail, MailID: mail.ID, Reason: app.EmailPurgeManual, At: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(browser.root, filepath.FromSlash(path.Dir(capture.ManifestPath)))
	if _, err := os.Stat(directory); err != nil {
		t.Fatalf("crash simulation should have left the bytes: %v", err)
	}

	if err := s.reconcilePurgedSources(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovery left the tombstoned bytes: %v", err)
	}
	// A second run must not keep rediscovering the same reaped tombstone.
	remaining, err := repo.ScanEmailCaptures(t.Context(), store.EmailCaptureScan{OwnerID: "email-owner", State: "purged", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("reaped tombstone still indexed: %+v", remaining)
	}
}

// Only timeline journals may adopt uncommitted bytes; startup tombstone cleanup
// must not scan legacy indexes or treat an arbitrary old pointer as authority.
func TestStartupDoesNotAdoptUnjournaledCapture(t *testing.T) {
	repo := store.NewMemoryStore()
	s, browser, _ := newFixtureService(t, repo)
	box, err := s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := repo.AdmitEmailDiscovery(t.Context(), store.EmailDiscoveryCommand{
		EmailCommand: store.EmailCommand{OwnerID: "email-owner", CommandKey: "admit-orphan"},
		MailboxID:    box.ID, BindingGeneration: box.BindingGeneration, ObservedAt: time.Now().UTC(),
		Members: []store.EmailDiscoveryMember{{ProviderMessageID: "message-orphan", ProviderSelectionID: "message-orphan", Folder: "inbox", Direction: "inbound", Reason: "recent_inbound", RemoteReadState: "unknown"}},
	})
	if err != nil || len(admitted.Mails) != 1 {
		t.Fatalf("admit orphan mail: %+v %v", admitted, err)
	}
	mail := admitted.Mails[0]
	receipt, err := fixtureCaptureFor(t.Context(), browser.root, "email-owner", "invocation-orphan", box.ID, mail.ID, "message-orphan", "2026/09/09")
	if err != nil {
		t.Fatal(err)
	}

	// Preserve a pointer written by the retired index-before-rename path.
	indexDir := filepath.Join(browser.root, "email", ownerScope("email-owner"), "index", receipt.MailboxID, receipt.MailID)
	if err := os.MkdirAll(indexDir, 0700); err != nil {
		t.Fatal(err)
	}
	pointer, _ := json.Marshal(map[string]any{"schema_version": 1, "capture_id": receipt.CaptureID, "mailbox_id": receipt.MailboxID, "mail_id": receipt.MailID, "date_path": "2026/09/09", "manifest_path": receipt.ManifestPath})
	if err := os.WriteFile(filepath.Join(indexDir, receipt.CaptureID+".json"), pointer, 0600); err != nil {
		t.Fatal(err)
	}

	before := browser.captureCalls()
	if err := s.reconcilePurgedSources(t.Context()); err != nil {
		t.Fatal(err)
	}
	untouched, found, err := repo.GetEmailMail(t.Context(), "email-owner", mail.ID)
	if err != nil || !found || untouched.CaptureID != "" {
		t.Fatalf("startup bypassed timeline transaction: %+v %v", untouched, err)
	}
	if after := browser.captureCalls(); after != before {
		t.Fatal("startup reopened the browser")
	}
}

func TestStartupPreservesStagingWithoutExplicitTombstones(t *testing.T) {
	s, _, browser, _ := cleanupFixture(t)
	staging := filepath.Join(browser.root, "email", ownerScope("email-owner"), "staging", "mb_box", "mail_one")
	fresh := filepath.Join(staging, "attempt_"+strings.Repeat("a", 64))
	stale := filepath.Join(staging, "attempt_"+strings.Repeat("b", 64))
	for _, dir := range []string{fresh, stale} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if err := s.reconcilePurgedSources(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("startup swept possible timeline-owned staging: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("startup removed in-flight staging: %v", err)
	}
}

// emailJobsOfKind narrows a job listing to one kind, so assertions about a
// specific lane do not depend on how many other lanes the planner arms.
func emailJobsOfKind(jobs []app.EmailJob, kind string) []app.EmailJob {
	out := []app.EmailJob{}
	for _, job := range jobs {
		if job.Kind == kind {
			out = append(out, job)
		}
	}
	return out
}
