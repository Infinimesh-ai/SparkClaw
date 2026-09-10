package emailmanagement

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// This fixture qualifies lifecycle/persistence boundaries, not model quality or
// provider DOM coverage. Every source is synthetic and local to the test.
type intakeFixture struct {
	repo             Repository
	root             string
	mu               sync.Mutex
	markBeforeCommit bool
	recentPartial    bool
	observed         []app.EmailDiscoveryOptions
}

func (f *intakeFixture) AdmitIntake(context.Context, string, string) (app.EmailAdmissionBinding, error) {
	return app.EmailAdmissionBinding{Provider: app.EmailProviderGmail, Account: app.EmailAccountDefault}, nil
}
func (f *intakeFixture) DiscoverForOwner(_ context.Context, _ string, r app.EmailReadRequest) (app.EmailDiscoveryResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Discovery == nil {
		return app.EmailDiscoveryResult{Provider: app.EmailProviderGmail, AccountAddress: "owner@example.com", ObservedAt: time.Now().UTC()}, nil
	}
	f.observed = append(f.observed, *r.Discovery)
	coverage := app.EmailDiscoveryCoverage{Lane: r.Discovery.Lane, ScanComplete: true, BoundaryQualified: r.Discovery.Lane == "recent_inbound"}
	if f.recentPartial && r.Discovery.Lane == "recent_inbound" {
		coverage.ScanComplete = false
		coverage.BoundaryQualified = false
		coverage.Continuation = "next-page"
		coverage.Reason = "fixture_partial"
	}
	return app.EmailDiscoveryResult{Provider: app.EmailProviderGmail, AccountAddress: "owner@example.com", ObservedAt: time.Now().UTC(), Coverage: coverage, Candidates: []app.EmailCaptureTarget{{AccountAddress: "owner@example.com", ProviderMessageID: "message-1", ProviderSelectionID: "selection-1", Folder: "inbox"}}}, nil
}
func (f *intakeFixture) CaptureForOwner(ctx context.Context, owner string, r app.EmailReadRequest) (app.EmailReadResult, error) {
	capture, err := fixtureCapture(ctx, f.root, owner, r.InvocationID, r.Target.ProviderMessageID)
	return app.EmailReadResult{Status: "collected", Capture: &capture}, err
}
func (f *intakeFixture) EnumerateThreadForOwner(context.Context, string, app.EmailThreadRequest) (app.EmailThreadResult, error) {
	return app.EmailThreadResult{}, errors.New("unexpected thread")
}
func (f *intakeFixture) MarkReadForOwner(ctx context.Context, owner string, r app.EmailMarkReadRequest) (app.EmailMarkReadResult, error) {
	_, found, err := f.repo.GetEmailCapture(ctx, owner, r.CommittedCapture.CaptureID)
	f.mu.Lock()
	f.markBeforeCommit = f.markBeforeCommit || !found
	f.mu.Unlock()
	return app.EmailMarkReadResult{ReadState: "read", ObservedAt: time.Now()}, err
}

type analyzerFixture struct {
	mu    sync.Mutex
	calls int
	fail  bool
}

func (f *analyzerFixture) Analyze(_ context.Context, input AnalysisInput) (AnalysisOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.fail {
		return AnalysisOutput{}, errors.New("fixture outage")
	}
	output := AnalysisOutput{Action: "none", Concern: "none", Title: "Fixture purchase", Summary: "The sender requests purchase approval.", Reason: "Source contains the request.", MissingContext: input.MissingContext, ModelVersion: "explicit-test-fixture"}
	for _, e := range input.Evidence {
		output.EvidenceRefs = append(output.EvidenceRefs, e.Ref)
	}
	if input.Kind == app.EmailJobClassification {
		output.Category = "interaction"
	}
	if input.Kind == app.EmailJobAssignment {
		output.Action = "new"
	}
	return output, nil
}

func fixtureCapture(ctx context.Context, root, owner, invocation, messageID string) (app.EmailCaptureReceipt, error) {
	id := "cap_" + strings.Repeat("a", 32)
	dir := path.Join("email", ownerScope(owner), "fixture-box", "fixture-mail", "source", id)
	manifest := sourceManifest{SchemaVersion: 1, Stage: "script_capture", Provider: app.EmailProviderGmail, AccountAddress: "owner@example.com", ProviderMessageID: messageID, MailID: "fixture-mail", MailboxID: "fixture-box", CaptureID: id, InvocationID: invocation, Status: "collected", Acquisition: "rfc822"}
	manifest.Coverage.InventoryComplete = true
	manifest.Coverage.AttachmentsComplete = true
	headers, _ := json.Marshal(capturedHeaders{Subject: "Purchase approval", From: []addressHeader{{Address: "sender@example.com"}}, To: []addressHeader{{Address: "owner@example.com"}}, MessageID: "<fixture@example.com>"})
	if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
		return app.EmailCaptureReceipt{}, err
	}
	for name, raw := range map[string][]byte{"message.eml": []byte("From: sender@example.com\r\nTo: owner@example.com\r\nSubject: Purchase approval\r\n\r\nPlease approve the purchase."), "headers.json": headers, "body.txt": []byte("Please approve the purchase.")} {
		relative := path.Join(dir, name)
		if err := os.WriteFile(filepath.Join(root, relative), raw, 0600); err != nil {
			return app.EmailCaptureReceipt{}, err
		}
		manifest.Files = append(manifest.Files, sourceFile{Path: relative, SHA256: sourceHash(raw), Bytes: int64(len(raw))})
	}
	raw, _ := json.Marshal(manifest)
	relative := path.Join(dir, "capture.json")
	if err := os.WriteFile(filepath.Join(root, relative), raw, 0600); err != nil {
		return app.EmailCaptureReceipt{}, err
	}
	return app.EmailCaptureReceipt{CaptureID: id, ManifestPath: relative, ManifestSHA256: sourceHash(raw), MailID: manifest.MailID, MailboxID: manifest.MailboxID, ReadState: "unknown"}, ctx.Err()
}

func newFixtureService(t *testing.T, repo Repository) (*Service, *intakeFixture, *analyzerFixture) {
	t.Helper()
	_, err := repo.SaveOwnerProfile(t.Context(), app.OwnerProfile{ID: "email-owner"})
	if err != nil {
		t.Fatal(err)
	}
	browser := &intakeFixture{repo: repo, root: t.TempDir()}
	analyzer := &analyzerFixture{}
	s, err := New(repo, browser, emailautomation.DefaultRegistry(), analyzer, nil, Options{WorkspaceRoot: browser.root, ScanInterval: time.Second, LeaseDuration: 3 * time.Second, JobTimeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return s, browser, analyzer
}

func TestTimerToDurableConversationAndOwnerDownload(t *testing.T) {
	repo, err := store.NewFileStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	s, browser, _ := newFixtureService(t, repo)
	box, err := s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s.Start(ctx)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	deadline := time.Now().Add(15 * time.Second)
	var mail app.EmailMail
	for time.Now().Before(deadline) {
		page, err := repo.ListEmailMails(t.Context(), store.EmailQuery{OwnerID: "email-owner", MailboxID: box.ID})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) == 1 {
			mail = page.Items[0]
			if mail.ConversationID != "" && mail.Summary != nil && mail.Summary.Current && mail.RemoteReadState == "read" {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	if mail.ConversationID == "" || mail.Summary == nil || !mail.Summary.Current || mail.RemoteReadState != "read" {
		jobs, _ := repo.ListEmailJobs(t.Context(), store.EmailQuery{OwnerID: "email-owner", Limit: 100})
		t.Fatalf("pipeline did not complete: mail=%+v jobs=%+v", mail, jobs)
	}
	browser.mu.Lock()
	premature := browser.markBeforeCommit
	browser.mu.Unlock()
	if premature {
		t.Fatal("marked remote read before durable capture")
	}
	file, name, err := s.OpenFile(t.Context(), "email-owner", mail.ID, "original")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(file)
	file.Close()
	if name != "message.eml" || !strings.Contains(string(raw), "Please approve") {
		t.Fatal("download was not the committed original")
	}
	if file, _, err := s.OpenFile(t.Context(), "other-owner", mail.ID, "original"); err == nil {
		file.Close()
		t.Fatal("cross-owner file opened")
	}
	if _, _, err := s.OpenFile(t.Context(), "email-owner", mail.ID, "../../other"); err == nil {
		t.Fatal("arbitrary path accepted")
	}
	capture, _, _ := repo.GetEmailCapture(t.Context(), "email-owner", mail.CaptureID)
	if err := os.WriteFile(filepath.Join(browser.root, capture.OriginalPath), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if file, _, err := s.OpenFile(t.Context(), "email-owner", mail.ID, "original"); err == nil {
		file.Close()
		t.Fatal("tampered original served")
	}
}

func TestRecentPartialCoverageSurvivesUnreadAndDowntime(t *testing.T) {
	repo := store.NewMemoryStore()
	s, browser, _ := newFixtureService(t, repo)
	browser.recentPartial = true
	activation := time.Now().Add(-7 * 24 * time.Hour)
	box, err := repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: command("email-owner", "bind"), Provider: app.EmailProviderGmail, Address: "owner@example.com", Enabled: true, Boundary: activation})
	if err != nil {
		t.Fatal(err)
	}
	job := app.EmailJob{OwnerID: "email-owner", MailboxID: box.ID, BindingGeneration: box.BindingGeneration}
	if err := s.discover(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	first, _, err := repo.GetEmailMailbox(t.Context(), job.OwnerID, box.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Cursor == "" || first.Coverage != "partial" || !first.Boundary.Equal(box.Boundary) {
		t.Fatalf("partial boundary lost: %+v", first)
	}
	if err := s.discover(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	browser.mu.Lock()
	last := browser.observed[len(browser.observed)-1]
	browser.mu.Unlock()
	var persisted discoveryCursor
	json.Unmarshal([]byte(first.Cursor), &persisted)
	if !last.IntervalStart.Equal(box.ActivatedAt) {
		t.Fatalf("downtime was skipped: got %s want %s", last.IntervalStart, box.ActivatedAt)
	}
	if last.Continuation != "next-page" || !last.IntervalStart.Equal(persisted.Start) || !last.IntervalEnd.Equal(persisted.End) {
		t.Fatalf("continuation interval drifted: %+v", last)
	}
}
