package gateway

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
)

func TestDocumentFileCannotBypassEmailOwnershipThroughPathsOrAliases(t *testing.T) {
	f := newEmailHTTPFixture(t)
	mail := f.receive("private", time.Now())
	capture, found, err := f.repo.GetEmailCapture(t.Context(), f.owner, mail.CaptureID)
	f.must(err)
	if !found {
		t.Fatal("capture missing")
	}
	uploads := filepath.Join(f.root, "uploads")
	f.must(os.MkdirAll(uploads, 0700))
	f.must(os.Symlink(filepath.Join(f.root, capture.OriginalPath), filepath.Join(uploads, "mail.eml")))
	f.must(os.Symlink(filepath.Join(f.root, "email"), filepath.Join(uploads, "mail-folder")))
	aliasPath := filepath.ToSlash(filepath.Join("uploads", "mail-folder", strings.TrimPrefix(capture.OriginalPath, "email/")))
	for _, path := range []string{capture.OriginalPath, "uploads/mail.eml", aliasPath} {
		w := f.request(http.MethodGet, "/api/documents/file?path="+url.QueryEscape(path), "")
		if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "Verified body") {
			t.Fatalf("email bypass path=%q status=%d body=%s", path, w.Code, w.Body.String())
		}
	}
	nestedRoot := filepath.Join(f.root, filepath.Dir(capture.OriginalPath))
	session := storetest.MustCreateSessionWithScope(t, f.repo, "nested source", f.owner, nestedRoot, "webchat", false)
	w := f.request(http.MethodGet, "/api/documents/file?path=message.eml&session_id="+session.ID, "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("email session-root bypass status=%d body=%s", w.Code, w.Body.String())
	}
	// Ordinary document aliases retain their existing behavior.
	f.must(os.WriteFile(filepath.Join(uploads, "report.txt"), []byte("ordinary document"), 0600))
	f.must(os.Symlink(filepath.Join(uploads, "report.txt"), filepath.Join(uploads, "report-link.txt")))
	for _, path := range []string{"uploads/report.txt", "uploads/report-link.txt"} {
		w := f.request(http.MethodGet, "/api/documents/file?path="+url.QueryEscape(path), "")
		if w.Code != http.StatusOK || w.Body.String() != "ordinary document" {
			t.Fatalf("ordinary document regressed: %d %s", w.Code, w.Body.String())
		}
	}
	// An email directory may itself have a filesystem alias. Its actual target
	// remains reserved even when the caller avoids the lexical "email" name.
	f.must(os.Rename(filepath.Join(f.root, "email"), filepath.Join(f.root, "source-storage")))
	f.must(os.Symlink(filepath.Join(f.root, "source-storage"), filepath.Join(f.root, "email")))
	physicalSource := strings.Replace(capture.OriginalPath, "email/", "source-storage/", 1)
	w = f.request(http.MethodGet, "/api/documents/file?path="+url.QueryEscape(physicalSource), "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("physical email alias status=%d body=%s", w.Code, w.Body.String())
	}
}
