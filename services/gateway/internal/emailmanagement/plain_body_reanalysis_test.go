package emailmanagement

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

const reanalysisPlainFixture = " \t中文\r\n\r\nSynthetic body\t \r\n"

// Publish a synthetic old-parser result through the ordinary leased repository
// operation. Neither this fixture nor the production repair edits stored rows.
func legacyPlainMail(t *testing.T, repo Repository, version string) (*Service, *intakeFixture, app.EmailMail, app.EmailRepresentation) {
	t.Helper()
	s, browser, _ := newFixtureService(t, repo)
	browser.body = reanalysisPlainFixture
	if _, err := s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, true, 0); err != nil {
		t.Fatal(err)
	}
	if err := seedFixtureCollection(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	job, found, err := repo.ClaimEmailJob(t.Context(), store.EmailJobClaim{OwnerID: "email-owner", Kinds: []string{app.EmailJobParse}, LeaseDuration: time.Minute})
	if err != nil || !found {
		t.Fatalf("initial parse lease: found=%t err=%v", found, err)
	}
	mail, _, err := repo.GetEmailMail(t.Context(), "email-owner", job.TargetID)
	if err != nil {
		t.Fatal(err)
	}
	box, _, err := repo.GetEmailMailbox(t.Context(), "email-owner", mail.MailboxID)
	if err != nil {
		t.Fatal(err)
	}
	capture, _, err := repo.GetEmailCapture(t.Context(), "email-owner", mail.CaptureID)
	if err != nil {
		t.Fatal(err)
	}
	representation, _, err := parseCapturedMail(t.Context(), browser.root, "email-owner", mail, box, capture)
	if err != nil {
		t.Fatal(err)
	}
	representation.ParserVersion = version
	representation.BodyText = strings.TrimSpace(reanalysisPlainFixture)
	relative := path.Join("email", ownerScope("email-owner"), "normalized", representation.ID, "representation.json")
	if err := publishJSON(t.Context(), browser.root, relative, representation); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(representation)
	if err != nil {
		t.Fatal(err)
	}
	representation.ManifestPath, representation.ManifestSHA256 = relative, sourceHash(raw)
	mail, err = repo.PublishEmailRepresentation(t.Context(), store.EmailRepresentationCommand{
		EmailCommand: command("email-owner", "legacy-body-fixture"), Lease: lease(job, s.now()), Representation: representation,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.FinishEmailJob(t.Context(), store.EmailJobFinish{EmailJobLease: lease(job, s.now())}); err != nil {
		t.Fatal(err)
	}
	representation, _, err = repo.GetEmailRepresentation(t.Context(), "email-owner", mail.RepresentationID)
	if err != nil {
		t.Fatal(err)
	}
	return s, browser, mail, representation
}

func TestExplicitReanalysisPreservesSourceAndOldRepresentation(t *testing.T) {
	for _, backend := range []string{"memory", "file"} {
		t.Run(backend, func(t *testing.T) {
			var repo Repository = store.NewMemoryStore()
			if backend == "file" {
				fileRepo, err := store.NewFileStore(filepath.Join(t.TempDir(), "state.json"))
				if err != nil {
					t.Fatal(err)
				}
				repo = fileRepo
			}
			s, browser, mail, old := legacyPlainMail(t, repo, "captured-mime-v5-safe-render")
			capture, _, err := repo.GetEmailCapture(t.Context(), "email-owner", mail.CaptureID)
			if err != nil {
				t.Fatal(err)
			}
			protected := map[string][]byte{}
			for _, name := range []string{capture.OriginalPath, capture.ManifestPath, old.ManifestPath} {
				protected[name], err = os.ReadFile(filepath.Join(browser.root, name))
				if err != nil {
					t.Fatal(err)
				}
			}
			if worked, err := s.workOne(t.Context(), []string{app.EmailJobParse}); err != nil || worked {
				t.Fatalf("ready old representation was automatically reparsed: worked=%t error=%v", worked, err)
			}
			calls := browser.captureCalls()
			var wg sync.WaitGroup
			errors := make(chan error, 8)
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, err := s.Reanalyze(t.Context(), "email-owner", mail.ID)
					errors <- err
				}()
			}
			wg.Wait()
			close(errors)
			for err := range errors {
				if err != nil {
					t.Fatal(err)
				}
			}
			jobs, err := repo.ListEmailJobs(t.Context(), store.EmailQuery{OwnerID: "email-owner", JobKind: app.EmailJobParse})
			pending := 0
			for _, job := range jobs {
				if job.State == app.EmailJobQueued || job.State == app.EmailJobRunning {
					pending++
					if job.TargetID != mail.ID {
						t.Fatal("reparse targeted a different mail")
					}
				}
			}
			if err != nil || pending != 1 {
				t.Fatalf("explicit requests did not coalesce to one target: count=%d error=%v", pending, err)
			}
			job, found, err := repo.ClaimEmailJob(t.Context(), store.EmailJobClaim{OwnerID: "email-owner", Kinds: []string{app.EmailJobParse}, LeaseDuration: time.Minute})
			if err != nil || !found {
				t.Fatalf("explicit parse lease: found=%t error=%v", found, err)
			}
			if _, err := s.Reanalyze(t.Context(), "email-owner", mail.ID); err != nil {
				t.Fatal(err)
			}
			if _, found, err := repo.ClaimEmailJob(t.Context(), store.EmailJobClaim{OwnerID: "email-owner", Kinds: []string{app.EmailJobParse}}); err != nil || found {
				t.Fatal("explicit reanalysis duplicated a leased parse")
			}
			s.execute(t.Context(), job)
			after, _, err := repo.GetEmailMail(t.Context(), "email-owner", mail.ID)
			if err != nil || after.CaptureID != mail.CaptureID || after.RepresentationID == old.ID || browser.captureCalls() != calls {
				t.Fatal("reparse replaced capture, reused old representation or reopened browser")
			}
			current, _, err := repo.GetEmailRepresentation(t.Context(), "email-owner", after.RepresentationID)
			if err != nil || current.ParserVersion != parserVersion || current.BodyText != reanalysisPlainFixture {
				t.Fatal("new parser representation lost original body bytes")
			}
			oldAfter, found, err := repo.GetEmailRepresentation(t.Context(), "email-owner", old.ID)
			if err != nil || !found || !reflect.DeepEqual(oldAfter, old) {
				t.Fatal("old representation was overwritten or removed")
			}
			captureAfter, _, err := repo.GetEmailCapture(t.Context(), "email-owner", mail.CaptureID)
			if err != nil || !reflect.DeepEqual(captureAfter, capture) {
				t.Fatal("capture record changed")
			}
			for name, original := range protected {
				now, err := os.ReadFile(filepath.Join(browser.root, name))
				if err != nil || !bytes.Equal(now, original) {
					t.Fatal("old source or representation file changed")
				}
			}
			view, err := s.Message(t.Context(), "email-owner", mail.ID)
			if err != nil || view.BodyText != reanalysisPlainFixture {
				t.Fatalf("MessageView did not preserve body: %v", err)
			}
			if _, err := s.Reanalyze(t.Context(), "email-owner", mail.ID); err != nil {
				t.Fatal(err)
			}
			if worked, err := s.workOne(t.Context(), []string{app.EmailJobParse}); err != nil || worked {
				t.Fatal("current parser was unnecessarily reparsed")
			}
		})
	}
}

func TestExplicitReanalysisOnlyUpgradesAvailableKnownOldSources(t *testing.T) {
	for _, kind := range []string{"current", "unknown-version", "missing-original", "missing-manifest", "purged", "other-owner"} {
		t.Run(kind, func(t *testing.T) {
			version := "captured-mime-v5-safe-render"
			if kind == "current" {
				version = parserVersion
			} else if kind == "unknown-version" {
				version = "future-parser"
			}
			repo := store.NewMemoryStore()
			s, browser, mail, old := legacyPlainMail(t, repo, version)
			capture, _, err := repo.GetEmailCapture(t.Context(), "email-owner", mail.CaptureID)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "missing-original" || kind == "missing-manifest" {
				relative := capture.OriginalPath
				if kind == "missing-manifest" {
					relative = capture.ManifestPath
				}
				if err := os.Remove(filepath.Join(browser.root, relative)); err != nil {
					t.Fatal(err)
				}
			} else if kind == "purged" {
				if _, err := repo.PurgeEmailCaptures(t.Context(), store.EmailCapturePurgeCommand{EmailCommand: command("email-owner", "purge-fixture"), Scope: store.EmailPurgeScopeMail, MailID: mail.ID, Reason: app.EmailPurgeManual, At: time.Now()}); err != nil {
					t.Fatal(err)
				}
			}
			owner := "email-owner"
			if kind == "other-owner" {
				owner = "another-owner"
			}
			_, err = s.Reanalyze(t.Context(), owner, mail.ID)
			if (kind == "other-owner" && !errors.Is(err, ErrNotFound)) || (kind != "other-owner" && err != nil) {
				t.Fatalf("unexpected reanalysis result: %v", err)
			}
			if worked, err := s.workOne(t.Context(), []string{app.EmailJobParse}); err != nil || worked {
				t.Fatal("unavailable or unrelated source was reparsed")
			}
			after, _, err := repo.GetEmailMail(t.Context(), "email-owner", mail.ID)
			if err != nil || after.RepresentationID != old.ID || browser.captureCalls() != 1 {
				t.Fatal("guard changed representation or reacquired source")
			}
		})
	}
}

func TestExplicitReanalysisStillVerifiesCapturedSource(t *testing.T) {
	for _, damage := range []string{"manifest", "original"} {
		t.Run(damage, func(t *testing.T) {
			repo := store.NewMemoryStore()
			s, browser, mail, old := legacyPlainMail(t, repo, "captured-mime-v5-safe-render")
			capture, _, err := repo.GetEmailCapture(t.Context(), "email-owner", mail.CaptureID)
			if err != nil {
				t.Fatal(err)
			}
			relative := capture.OriginalPath
			if damage == "manifest" {
				relative = capture.ManifestPath
			}
			if err := os.WriteFile(filepath.Join(browser.root, relative), []byte("corrupt fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Reanalyze(t.Context(), "email-owner", mail.ID); err != nil {
				t.Fatal(err)
			}
			if worked, err := s.workOne(t.Context(), []string{app.EmailJobParse}); err != nil || !worked {
				t.Fatal("parse integrity validation did not run")
			}
			after, _, err := repo.GetEmailMail(t.Context(), "email-owner", mail.ID)
			if err != nil || after.CaptureID != mail.CaptureID || after.RepresentationID != old.ID || browser.captureCalls() != 1 {
				t.Fatal("invalid source replaced the prior representation or accessed the provider")
			}
			oldAfter, found, err := repo.GetEmailRepresentation(t.Context(), "email-owner", old.ID)
			if err != nil || !found || !reflect.DeepEqual(oldAfter, old) {
				t.Fatal("integrity failure modified the old representation")
			}
			jobs, err := repo.ListEmailJobs(t.Context(), store.EmailQuery{OwnerID: "email-owner", JobKind: app.EmailJobParse})
			if err != nil {
				t.Fatal(err)
			}
			failed := false
			for _, job := range jobs {
				failed = failed || (job.State == app.EmailJobRetryWait && job.ErrorCode != "")
			}
			if !failed {
				t.Fatal("source integrity failure was not retained by the parse job")
			}
		})
	}
}
