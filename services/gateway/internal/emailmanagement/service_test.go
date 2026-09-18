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
	repo          Repository
	root          string
	mu            sync.Mutex
	recentPartial bool
	observed      []app.EmailDiscoveryOptions
	captures      int
	subject       string
	body          string
}

// captureCalls counts browser round trips so tests can assert that recovery
// registers bytes already on disk instead of downloading them again.
func (f *intakeFixture) captureCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.captures
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
	f.mu.Lock()
	defer f.mu.Unlock()
	// The real timeline adapter reuses a previously validated original instead
	// of rewriting its manifest when an interval boundary repeats the same ID.
	mailID := "fixture-" + ownerScope(r.InvocationID)
	relative := path.Join("email", "2026/09/07", ownerScope(owner), "fixture-box", mailID, "source", "cap_"+strings.Repeat("a", 32), "capture.json")
	if raw, err := os.ReadFile(filepath.Join(f.root, relative)); err == nil {
		capture := app.EmailCaptureReceipt{CaptureID: "cap_" + strings.Repeat("a", 32), ManifestPath: relative, ManifestSHA256: sourceHash(raw), MailID: mailID, MailboxID: "fixture-box", ReadState: "unknown"}
		return app.EmailReadResult{Status: "collected", Capture: &capture}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return app.EmailReadResult{}, err
	}
	f.captures++
	// Every invocation gets an immutable fixture directory, matching production.
	capture, err := fixtureCaptureForContent(ctx, f.root, owner, r.InvocationID, "fixture-box", "fixture-"+ownerScope(r.InvocationID), r.Target.ProviderMessageID, "2026/09/07", f.subject, f.body)
	return app.EmailReadResult{Status: "collected", Capture: &capture}, err
}
func fixtureCollectPage(ctx context.Context, owner string, r app.EmailReadRequest, discover func(context.Context, string, app.EmailReadRequest) (app.EmailDiscoveryResult, error), capture func(context.Context, string, app.EmailReadRequest) (app.EmailReadResult, error)) (app.EmailPageResult, error) {
	discovery, err := discover(ctx, owner, r)
	if err != nil {
		return app.EmailPageResult{}, err
	}
	result := app.EmailPageResult{SchemaVersion: 1, Provider: r.Provider, AccountAddress: discovery.AccountAddress, PageID: "page_" + strings.Repeat("f", 64), Discovery: discovery, DiscoveryOptions: *r.Discovery, Captures: []app.EmailPageCapture{}, Failures: []app.EmailPageFailure{}, ObservedAt: discovery.ObservedAt, Status: "empty"}
	for _, target := range discovery.Candidates {
		binding := r
		binding.Target = &target
		binding.Discovery = nil
		binding.InvocationID = emailautomation.PageCaptureInvocationID(r.InvocationID, r.Provider, target)
		captured, captureErr := capture(ctx, owner, binding)
		if captureErr != nil {
			return result, captureErr
		}
		captured.Capture.ReadState = "read"
		result.Captures = append(result.Captures, app.EmailPageCapture{Target: target, Result: captured})
	}
	if len(result.Captures) > 0 {
		result.Status = "collected"
	}
	return result, nil
}
func (f *intakeFixture) CollectPageForOwner(ctx context.Context, owner string, r app.EmailReadRequest) (app.EmailPageResult, error) {
	return fixtureCollectPage(ctx, owner, r, f.DiscoverForOwner, f.CaptureForOwner)
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
	if input.PolicyVersion == analysisPromptVersion {
		output.Summary = ""
		output.Title = ""
		if input.Kind == app.EmailJobAssignment {
			output.Title = "Approve fixture purchase"
		}
	}
	if input.Kind == app.EmailJobClassification {
		output.Category = "interaction"
	}
	if input.Kind == app.EmailJobAssignment {
		output.EventScope = "single_event"
		output.Action = "new"
	}
	return output, nil
}

func fixtureCapture(ctx context.Context, root, owner, invocation, messageID string) (app.EmailCaptureReceipt, error) {
	return fixtureCaptureOn(ctx, root, owner, invocation, messageID, "2026/09/07")
}

func fixtureCaptureOn(ctx context.Context, root, owner, invocation, messageID, datePath string) (app.EmailCaptureReceipt, error) {
	return fixtureCaptureFor(ctx, root, owner, invocation, "fixture-box", "fixture-mail", messageID, datePath)
}

func fixtureCaptureFor(ctx context.Context, root, owner, invocation, mailboxID, mailID, messageID, datePath string) (app.EmailCaptureReceipt, error) {
	return fixtureCaptureForContent(ctx, root, owner, invocation, mailboxID, mailID, messageID, datePath, "", "")
}

func fixtureCaptureForContent(ctx context.Context, root, owner, invocation, mailboxID, mailID, messageID, datePath, subject, body string) (app.EmailCaptureReceipt, error) {
	return fixtureCaptureForProviderContent(ctx, root, owner, invocation, mailboxID, mailID, messageID, datePath, app.EmailProviderGmail, "owner@example.com", subject, body)
}

func fixtureCaptureForProviderContent(ctx context.Context, root, owner, invocation, mailboxID, mailID, messageID, datePath, provider, account, subject, body string) (app.EmailCaptureReceipt, error) {
	if subject == "" {
		subject = "Purchase approval"
	}
	if body == "" {
		body = "Please approve the purchase."
	}
	id := "cap_" + strings.Repeat("a", 32)
	dir := path.Join("email", datePath, ownerScope(owner), mailboxID, mailID, "source", id)
	manifest := sourceManifest{SchemaVersion: 1, Stage: "script_capture", Provider: provider, AccountAddress: account, ProviderMessageID: messageID, MailID: mailID, MailboxID: mailboxID, CaptureID: id, InvocationID: invocation, Status: "collected", Acquisition: "rfc822", DatePath: datePath, ReceivedAt: "2026-09-07T02:00:00Z", ReceivedSource: "eml_date"}
	manifest.Coverage.InventoryComplete = true
	manifest.Coverage.AttachmentsComplete = true
	headers, _ := json.Marshal(map[string]any{"subject": subject, "from": []map[string]string{{"address": "sender@example.com"}}, "to": []map[string]string{{"address": "owner@example.com"}}, "message_id": "<fixture@example.com>"})
	if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
		return app.EmailCaptureReceipt{}, err
	}
	for name, raw := range map[string][]byte{"message.eml": []byte("From: sender@example.com\r\nTo: owner@example.com\r\nSubject: " + subject + "\r\n\r\n" + body), "headers.json": headers, "body.txt": []byte(body)} {
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
	s, err := New(repo, browser, emailautomation.DefaultRegistry(), analyzer, nil, Options{WorkspaceRoot: browser.root, ScanInterval: time.Second, LeaseDuration: 3 * time.Second, JobTimeout: 10 * time.Second, QualifiedProviderModes: map[string]string{app.EmailProviderGmail: app.EmailProviderModeTimeRange}})
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
			if mail.ConversationID != "" && mail.Classification != nil && mail.RemoteReadState == "read" {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	if mail.ConversationID == "" || mail.Classification == nil || mail.RemoteReadState != "read" {
		jobs, _ := repo.ListEmailJobs(t.Context(), store.EmailQuery{OwnerID: "email-owner", Limit: 100})
		t.Fatalf("pipeline did not complete: mail=%+v jobs=%+v", mail, jobs)
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

func TestRecentPartialCoverageRetainsFixedInterval(t *testing.T) {
	repo := store.NewMemoryStore()
	s, browser, _ := newFixtureService(t, repo)
	browser.recentPartial = true
	box, err := s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.plan(t.Context()); err != nil {
		t.Fatal(err)
	}
	if worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover}); err != nil || !worked {
		t.Fatalf("first: %v", err)
	}
	first, _, err := repo.GetEmailMailbox(t.Context(), "email-owner", box.ID)
	if err != nil || first.SyncState != app.EmailSyncOverflowConfirmation {
		t.Fatalf("first: %+v %v", first, err)
	}
	initial := browser.observed[0]
	if _, err = s.Sync(t.Context(), "email-owner", box.ID); err != nil {
		t.Fatal(err)
	}
	if worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover}); err != nil || !worked {
		t.Fatalf("second: %v", err)
	}
	last := browser.observed[len(browser.observed)-1]
	if !last.IntervalStart.Equal(initial.IntervalStart) || !last.IntervalEnd.Equal(initial.IntervalEnd) || last.Continuation != "" {
		t.Fatalf("confirmation drifted: %+v", last)
	}
}
