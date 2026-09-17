package emailmanagement

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// writeCapture lays a manifest down at an arbitrary directory so the layout
// checks can be exercised independently of what the Controller would produce.
func writeCapture(t *testing.T, root, dir, datePath, mailboxID, mailID string) app.EmailCaptureVersion {
	t.Helper()
	id := "cap_" + strings.Repeat("a", 32)
	manifest := sourceManifest{SchemaVersion: 1, Stage: "script_capture", Provider: app.EmailProviderGmail,
		AccountAddress: "owner@example.com", ProviderMessageID: "message-1", MailID: mailID, MailboxID: mailboxID,
		CaptureID: id, InvocationID: "invocation-1", Status: "collected", Acquisition: "rfc822", DatePath: datePath}
	manifest.Coverage.InventoryComplete = true
	manifest.Coverage.AttachmentsComplete = true
	if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
		t.Fatal(err)
	}
	body := []byte("Please approve the purchase.")
	relative := path.Join(dir, "body.txt")
	if err := os.WriteFile(filepath.Join(root, relative), body, 0600); err != nil {
		t.Fatal(err)
	}
	manifest.Files = append(manifest.Files, sourceFile{Path: relative, SHA256: sourceHash(body), Bytes: int64(len(body))})
	raw, _ := json.Marshal(manifest)
	manifestPath := path.Join(dir, "capture.json")
	if err := os.WriteFile(filepath.Join(root, manifestPath), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return app.EmailCaptureVersion{ID: id, ManifestPath: manifestPath, ManifestSHA256: sourceHash(raw), State: app.EmailCaptureComplete}
}

func TestLoadManifestAcceptsOnlyTheDateLayout(t *testing.T) {
	owner, root := "email-owner", t.TempDir()
	id := "cap_" + strings.Repeat("a", 32)
	legacy := path.Join("email", ownerScope(owner), "fixture-box", "fixture-mail", "source", id)
	capture := writeCapture(t, root, legacy, "", "fixture-box", "fixture-mail")
	if _, _, err := loadManifest(t.Context(), root, owner, capture); err == nil || err.Error() != "email_source_scope" {
		t.Fatalf("legacy flat layout must be rejected: err=%v", err)
	}
	dated := path.Join("email", "2026", "09", "07", ownerScope(owner), "fixture-box", "fixture-mail", "source", id)
	capture = writeCapture(t, root, dated, "2026/09/07", "fixture-box", "fixture-mail")
	if _, _, err := loadManifest(t.Context(), root, owner, capture); err != nil {
		t.Fatalf("date layout must be accepted: %v", err)
	}
}

func TestLoadManifestRejectsManifestThatDisagreesWithItsDirectory(t *testing.T) {
	owner := "email-owner"
	id := "cap_" + strings.Repeat("a", 32)
	for name, claim := range map[string]struct{ datePath, mailboxID, mailID string }{
		// The tree is written at 2026/09/07 under fixture-box/fixture-mail; each
		// case makes the manifest claim to belong somewhere else.
		"another day":     {"2026/09/08", "fixture-box", "fixture-mail"},
		"another mailbox": {"2026/09/07", "other-box", "fixture-mail"},
		"another mail":    {"2026/09/07", "fixture-box", "other-mail"},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dir := path.Join("email", "2026", "09", "07", ownerScope(owner), "fixture-box", "fixture-mail", "source", id)
			capture := writeCapture(t, root, dir, claim.datePath, claim.mailboxID, claim.mailID)
			if _, _, err := loadManifest(t.Context(), root, owner, capture); err == nil || err.Error() != "email_source_invalid" {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestCaptureDirScopeRejectsForgedSegments(t *testing.T) {
	owner := "email-owner"
	scope, mailbox, mail, id := ownerScope(owner), "mb_"+strings.Repeat("b", 32), "mail_"+strings.Repeat("c", 32), "cap_"+strings.Repeat("d", 32)
	valid := path.Join("email", "2026", "09", "07", scope, mailbox, mail, "source", id, "capture.json")
	if gotBox, gotMail, gotDate, ok := captureDirScope(valid, owner); !ok || gotBox != mailbox || gotMail != mail || gotDate != "2026/09/07" {
		t.Fatalf("box=%q mail=%q date=%q ok=%v", gotBox, gotMail, gotDate, ok)
	}
	for _, forged := range []string{
		"email/2026/09/07/" + strings.Repeat("f", 64) + "/" + mailbox + "/" + mail + "/source/" + id + "/capture.json", // another owner
		"email/" + scope + "/" + mailbox + "/" + mail + "/source/" + id + "/capture.json",                              // legacy flat
		"email/2026/13/07/" + scope + "/" + mailbox + "/" + mail + "/source/" + id + "/capture.json",                   // impossible month
		"email/2026/09/32/" + scope + "/" + mailbox + "/" + mail + "/source/" + id + "/capture.json",                   // impossible day
		"email/2026/9/07/" + scope + "/" + mailbox + "/" + mail + "/source/" + id + "/capture.json",                    // unpadded month
		"email/2026/09/07/" + scope + "/" + mailbox + "/" + mail + "/parts/" + id + "/capture.json",                    // not a source directory
		"email/2026/09/07/" + scope + "/" + mailbox + "/" + mail + "/source/" + id + "/read-state.json",                // not the manifest
		"email/2026/09/07/" + scope + "/x/" + mailbox + "/" + mail + "/source/" + id + "/capture.json",                 // one segment long
		"email/2026/09/07/" + scope + "/" + mailbox + "/source/" + id + "/capture.json",                                // one segment short
		"../email/2026/09/07/" + scope + "/" + mailbox + "/" + mail + "/source/" + id + "/capture.json",                // traversal
		"/email/2026/09/07/" + scope + "/" + mailbox + "/" + mail + "/source/" + id + "/capture.json",                  // absolute
	} {
		if _, _, _, ok := captureDirScope(forged, owner); ok {
			t.Fatalf("accepted forged path %q", forged)
		}
	}
}
