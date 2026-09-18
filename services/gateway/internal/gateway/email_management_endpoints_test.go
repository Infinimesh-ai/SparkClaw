package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailmanagement"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

type noEmailBrowser struct{ calls int }

func (b *noEmailBrowser) AdmitIntake(context.Context, string, string) (app.EmailAdmissionBinding, error) {
	b.calls++
	return app.EmailAdmissionBinding{}, fmt.Errorf("browser must not run inline")
}
func (b *noEmailBrowser) DiscoverForOwner(context.Context, string, app.EmailReadRequest) (app.EmailDiscoveryResult, error) {
	b.calls++
	return app.EmailDiscoveryResult{}, fmt.Errorf("browser must not run inline")
}
func (b *noEmailBrowser) CaptureForOwner(context.Context, string, app.EmailReadRequest) (app.EmailReadResult, error) {
	b.calls++
	return app.EmailReadResult{}, fmt.Errorf("browser must not run inline")
}
func (b *noEmailBrowser) EnumerateThreadForOwner(context.Context, string, app.EmailThreadRequest) (app.EmailThreadResult, error) {
	b.calls++
	return app.EmailThreadResult{}, fmt.Errorf("browser must not run inline")
}
func (b *noEmailBrowser) MarkReadForOwner(context.Context, string, app.EmailMarkReadRequest) (app.EmailMarkReadResult, error) {
	b.calls++
	return app.EmailMarkReadResult{}, fmt.Errorf("browser must not run inline")
}

type emailHTTPFixture struct {
	t       *testing.T
	repo    *store.MemoryStore
	owner   string
	root    string
	box     app.EmailMailbox
	handler http.Handler
	browser *noEmailBrowser
}

func newEmailHTTPFixture(t *testing.T) *emailHTTPFixture {
	return newEmailHTTPFixtureWithAnalyzer(t, nil)
}

func newEmailHTTPFixtureWithAnalyzer(t *testing.T, analyzer emailmanagement.Analyzer) *emailHTTPFixture {
	f := &emailHTTPFixture{t: t, repo: store.NewMemoryStore(), owner: app.DefaultOwnerID, root: t.TempDir(), browser: &noEmailBrowser{}}
	cfg := testConfig(f.root)
	cfg.Gateway.APIToken = "email-owner-token"
	cfg.Gateway.RateLimit.Enabled = false
	tools := toolhub.New(cfg, f.repo)
	t.Cleanup(func() { _ = tools.Close() })
	runtime := agent.NewRuntime(f.repo, tools, policy.New(cfg), modelrouter.New(cfg), nil)
	service, err := emailmanagement.New(f.repo, f.browser, emailautomation.DefaultRegistry(), analyzer, nil, emailmanagement.Options{WorkspaceRoot: f.root, QualifiedProviderModes: map[string]string{app.EmailProviderGmail: app.EmailProviderModeTimeRange}})
	f.must(err)
	f.handler = New(cfg, f.repo, tools, runtime, WithEmailManagement(service)).Handler()
	f.box, err = f.repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: f.command(), Provider: app.EmailProviderGmail, Address: "owner@example.com", Enabled: true})
	f.must(err)
	return f
}
func (f *emailHTTPFixture) must(err error) {
	f.t.Helper()
	if err != nil {
		f.t.Fatal(err)
	}
}
func (f *emailHTTPFixture) command() store.EmailCommand {
	return store.EmailCommand{OwnerID: f.owner, CommandKey: app.NewID("test-email")}
}
func (f *emailHTTPFixture) claim(kind string) app.EmailJob {
	f.t.Helper()
	job, found, err := f.repo.ClaimEmailJob(f.t.Context(), store.EmailJobClaim{OwnerID: f.owner, Kinds: []string{kind}, LeaseDuration: time.Minute})
	f.must(err)
	if !found {
		f.t.Fatalf("missing %s job", kind)
	}
	return job
}
func (f *emailHTTPFixture) lease(job app.EmailJob) store.EmailJobLease {
	return store.EmailJobLease{OwnerID: f.owner, JobID: job.ID, LeaseToken: job.LeaseToken}
}
func (f *emailHTTPFixture) finish(job app.EmailJob) {
	_, err := f.repo.FinishEmailJob(f.t.Context(), store.EmailJobFinish{EmailJobLease: f.lease(job)})
	f.must(err)
}
func emailTestHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func (f *emailHTTPFixture) receive(providerID string, at time.Time, partial ...bool) app.EmailMail {
	f.t.Helper()
	admission, err := f.repo.AdmitEmailDiscovery(f.t.Context(), store.EmailDiscoveryCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, ObservedAt: time.Now(), Coverage: "partial", Members: []store.EmailDiscoveryMember{{ProviderMessageID: providerID, ProviderSelectionID: providerID, Direction: "inbound", Folder: "inbox", SourceTime: at}}})
	f.must(err)
	mail := admission.Mails[0]
	scope := sha256.Sum256([]byte(f.owner))
	captureID := "capture-" + mail.ID
	directory := "email/2026/09/07/" + hex.EncodeToString(scope[:]) + "/" + f.box.ID + "/" + mail.ID + "/source/" + captureID
	original := []byte("Subject: Original " + providerID + "\r\n\r\nVerified body")
	attachment := []byte("Attachment evidence " + providerID)
	write := func(relative string, raw []byte) {
		f.must(os.MkdirAll(filepath.Dir(filepath.Join(f.root, relative)), 0700))
		f.must(os.WriteFile(filepath.Join(f.root, relative), raw, 0600))
	}
	originalPath, partPath := directory+"/message.eml", directory+"/attachments/report.txt"
	write(originalPath, original)
	write(partPath, attachment)
	captureState, parseState, manifestState := app.EmailCaptureComplete, app.EmailParseReady, "collected"
	if len(partial) > 0 && partial[0] {
		captureState, parseState, manifestState = app.EmailCapturePartial, app.EmailParsePartial, "partial"
	}
	manifest := map[string]any{"schema_version": 1, "stage": "script_capture", "acquisition": "rfc822", "provider": f.box.Provider, "account_address": f.box.Address, "provider_message_id": providerID, "mail_id": mail.ID, "mailbox_id": f.box.ID, "capture_id": captureID, "invocation_id": "test-capture", "status": "collected", "date_path": "2026/09/07", "files": []map[string]any{{"path": originalPath, "bytes": len(original), "sha256": emailTestHash(original)}, {"path": partPath, "bytes": len(attachment), "sha256": emailTestHash(attachment)}}, "attachments": []map[string]any{{"part_id": "part-1", "name": "报告.txt", "path": "attachments/report.txt", "bytes": len(attachment), "sha256": emailTestHash(attachment), "status": "available"}}}
	manifest["status"] = manifestState
	raw, err := json.Marshal(manifest)
	f.must(err)
	manifestPath := directory + "/capture.json"
	write(manifestPath, raw)
	job := f.claim(app.EmailJobCapture)
	mail, err = f.repo.PublishEmailCapture(f.t.Context(), store.EmailCaptureCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, Lease: f.lease(job), Capture: app.EmailCaptureVersion{ID: captureID, MailID: mail.ID, ManifestPath: manifestPath, ManifestSHA256: emailTestHash(raw), OriginalPath: originalPath, OriginalSHA256: emailTestHash(original), State: captureState}})
	f.must(err)
	f.finish(job)
	job = f.claim(app.EmailJobParse)
	representationID := "rep-" + mail.ID
	previewDocument := []byte(`{"version":"structured-mail-v3","content":[{"kind":"paragraph","children":[{"kind":"text","text":"Safe preview ` + providerID + `"}]}]}`)
	previewPath := "email/" + hex.EncodeToString(scope[:]) + "/normalized/" + representationID + "/render/structured-mail-v3/content.json"
	write(previewPath, previewDocument)
	mail, err = f.repo.PublishEmailRepresentation(f.t.Context(), store.EmailRepresentationCommand{
		EmailCommand: f.command(), Lease: f.lease(job),
		Representation: app.EmailRepresentation{ID: representationID, MailID: mail.ID, CaptureID: captureID, Subject: "Subject " + providerID, From: []string{"vendor@example.com"}, To: []string{f.box.Address}, SourceTime: at, BodyText: "unique-contract-term-" + providerID, State: parseState, Coverage: "complete", ParserVersion: "fixture-v1", ManifestPath: directory + "/representation.json", ManifestSHA256: emailTestHash([]byte("representation")), Attachments: []app.EmailAttachment{{ID: "part-1", Name: "报告.txt", Path: partPath, SizeBytes: int64(len(attachment)), SHA256: emailTestHash(attachment), State: app.EmailParseReady}}},
		RenderPreview:  &app.EmailRenderPreview{MailID: mail.ID, CaptureID: captureID, RepresentationID: representationID, SourceSHA256: emailTestHash(original), SanitizerVersion: "structured-mail-v3", State: app.EmailRenderReady, ArtifactPath: previewPath, ArtifactSHA256: emailTestHash(previewDocument), ArtifactBytes: int64(len(previewDocument))},
	})
	f.must(err)
	f.finish(job)
	for i := 0; i < 100; i++ {
		j := f.claim(app.EmailJobClassification)
		current, ok, err := f.repo.GetEmailMail(f.t.Context(), f.owner, j.TargetID)
		f.must(err)
		if !ok {
			f.t.Fatal("missing classification source")
		}
		_, err = f.repo.PublishEmailClassification(f.t.Context(), store.EmailClassificationCommand{EmailCommand: f.command(), Lease: f.lease(j), MailID: j.TargetID, Generation: j.Generation, Classification: app.EmailClassification{Category: "interaction", EvidenceRefs: []string{"representation:" + current.RepresentationID + ":body"}, InputFingerprint: j.InputFingerprint}})
		f.must(err)
		f.finish(j)
		if j.TargetID == mail.ID {
			mail, _, err = f.repo.GetEmailMail(f.t.Context(), f.owner, mail.ID)
			f.must(err)
			return mail
		}
	}
	f.t.Fatal("classification fixture exceeded bound")
	return mail
}
func (f *emailHTTPFixture) assign(mail app.EmailMail, id string) app.EmailMail {
	f.t.Helper()
	for {
		result, err := f.repo.ExpandEmailRefresh(f.t.Context(), store.EmailRefreshCommand{EmailCommand: f.command(), Limit: 100})
		f.must(err)
		if !result.Remaining {
			break
		}
	}
	job := f.claim(app.EmailJobAssignment)
	target, _, err := f.repo.GetEmailAnalysisTarget(f.t.Context(), f.owner, job.Kind, job.TargetID)
	f.must(err)
	candidates, err := f.repo.FindEmailCandidates(f.t.Context(), store.EmailCandidateQuery{OwnerID: f.owner, MailID: mail.ID})
	f.must(err)
	action := "new"
	ids := []string{}
	if id != "" {
		action = "append"
		ids = []string{id}
	}
	result, err := f.repo.CommitEmailAssignment(f.t.Context(), store.EmailAssignmentCommand{EmailCommand: f.command(), Lease: f.lease(job), Generation: target.Generation, CandidateConversationIDs: ids, Decision: app.EmailAssignmentDecision{ID: app.NewID("decision"), MailID: mail.ID, Action: action, ConversationID: id, Title: "Topic " + mail.Subject, OwnerEpoch: candidates.OwnerEpoch, InputFingerprint: target.InputFingerprint, ModelVersion: "fixture", PromptVersion: "fixture-v1"}})
	f.must(err)
	f.finish(job)
	return result
}
func (f *emailHTTPFixture) request(method, path, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer email-owner-token")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}
func emailDecode[T any](t *testing.T, w *httptest.ResponseRecorder, want int) T {
	t.Helper()
	if w.Code != want {
		t.Fatalf("status %d want %d: %s", w.Code, want, w.Body.String())
	}
	var out T
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestEmailRenderPreviewEndpointOnlyExposesSanitizedArtifact(t *testing.T) {
	f := newEmailHTTPFixture(t)
	mail := f.receive("render-preview", time.Now().UTC())
	response := f.request("GET", "/api/email/messages/"+mail.ID+"/render-preview", "")
	preview := emailDecode[emailmanagement.RenderPreviewView](t, response, http.StatusOK)
	if preview.State != app.EmailRenderReady || !strings.Contains(response.Body.String(), "Safe preview render-preview") || len(preview.Content) != 1 {
		t.Fatalf("preview=%+v", preview)
	}
	if response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers=%v", response.Header())
	}
	if strings.Contains(response.Body.String(), "body_text") {
		t.Fatal("render preview response exposed the legacy plain-text field")
	}
	if got := f.request("GET", "/api/email/messages/"+mail.ID+"/preview", ""); got.Code != http.StatusNotFound {
		t.Fatalf("legacy preview route status=%d body=%s", got.Code, got.Body.String())
	}
}

func TestEmailManagementHTTPViewGapPendingAndReanalysis(t *testing.T) {
	f := newEmailHTTPFixture(t)
	at := time.Now().UTC()
	older := f.assign(f.receive("101", at.Add(-7*24*time.Hour)), "")
	newer := f.assign(f.receive("102", at), older.ConversationID)
	pending := f.receive("pending", at.Add(time.Minute))
	before, err := f.repo.GetEmailOwnerStatus(t.Context(), f.owner)
	f.must(err)
	list := emailDecode[emailmanagement.ConversationsView](t, f.request("GET", "/api/email/conversations?limit=1", ""), 200)
	if len(list.Conversations) != 1 || list.Conversations[0].UnseenCount != 2 {
		t.Fatalf("conversations=%+v", list)
	}
	page := emailDecode[emailmanagement.MessagesView](t, f.request("GET", "/api/email/conversations/"+older.ConversationID+"/messages?limit=1", ""), 200)
	if len(page.Messages) != 1 || page.Messages[0].ID != newer.ID || page.NextCursor == "" {
		t.Fatalf("timeline=%+v", page)
	}
	if bytes.Contains(f.request("GET", "/api/email/interaction-mails", "").Body.Bytes(), []byte(f.root)) {
		t.Fatal("host path leaked")
	}
	after, err := f.repo.GetEmailOwnerStatus(t.Context(), f.owner)
	f.must(err)
	if after.Revision != before.Revision {
		t.Fatal("GET mutated email state")
	}
	body := fmt.Sprintf(`{"mail_ids":[%q,%q]}`, newer.ID, pending.ID)
	for i := 0; i < 2; i++ {
		out := emailDecode[emailmanagement.ViewedResult](t, f.request("POST", "/api/email/messages/viewed", body), 200)
		if len(out.MailIDs) != 2 {
			t.Fatalf("receipt=%+v", out)
		}
	}
	old, _, err := f.repo.GetEmailMail(t.Context(), f.owner, older.ID)
	f.must(err)
	if old.ViewedAt != nil {
		t.Fatal("viewing newest mail acknowledged unloaded history")
	}
	pendingView := emailDecode[emailmanagement.MessagesView](t, f.request("GET", "/api/email/interaction-mails", ""), 200)
	if len(pendingView.Messages) != 1 || !pendingView.Messages[0].Viewed {
		t.Fatalf("pending=%+v", pendingView)
	}
	emailDecode[emailmanagement.ScheduleResult](t, f.request("POST", "/api/email/messages/"+newer.ID+"/reanalyze", "{}"), 202)
	updated, _, err := f.repo.GetEmailMail(t.Context(), f.owner, newer.ID)
	f.must(err)
	if updated.ConversationID != older.ConversationID {
		t.Fatal("reanalysis changed committed membership")
	}
	jobs, err := f.repo.ListEmailJobs(t.Context(), store.EmailQuery{OwnerID: f.owner, Limit: 100})
	f.must(err)
	classification := false
	for _, job := range jobs {
		if job.TargetID == newer.ID && job.Kind == app.EmailJobClassification && job.State == app.EmailJobQueued {
			classification = true
		}
	}
	if !classification || f.browser.calls != 0 {
		t.Fatalf("classification=%v browser calls=%d", classification, f.browser.calls)
	}
	_, claimed, err := f.repo.ClaimEmailJob(t.Context(), store.EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobRelationshipCheck}, LeaseDuration: time.Minute})
	f.must(err)
	if claimed {
		t.Fatal("reanalysis revived disabled relationship generation")
	}
}

func TestEmailManagementHTTPSourceAuthorizationAndIntegrity(t *testing.T) {
	f := newEmailHTTPFixture(t)
	mail := f.receive("source", time.Now(), true)
	original := f.request("GET", "/api/email/messages/"+mail.ID+"/file", "")
	if original.Code != 200 || !strings.Contains(original.Body.String(), "Verified body") || original.Header().Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(original.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatalf("original status=%d headers=%v body=%s", original.Code, original.Header(), original.Body.String())
	}
	attachment := f.request("GET", "/api/email/messages/"+mail.ID+"/file?part_id=part-1", "")
	if attachment.Code != 200 || attachment.Body.String() != "Attachment evidence source" {
		t.Fatalf("attachment=%d %s", attachment.Code, attachment.Body.String())
	}
	for _, suffix := range []string{"?path=/etc/passwd", "?part_id=../../etc/passwd"} {
		w := f.request("GET", "/api/email/messages/"+mail.ID+"/file"+suffix, "")
		if w.Code != 400 && w.Code != 404 {
			t.Fatalf("unsafe path status=%d", w.Code)
		}
	}
	request := httptest.NewRequest("GET", "/api/email/messages/"+mail.ID+"/file", nil)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, request)
	if w.Code != 401 {
		t.Fatalf("unauthenticated source status=%d", w.Code)
	}
	owner, box := f.owner, f.box
	f.owner = "other-owner"
	var err error
	f.box, err = f.repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: f.command(), Provider: app.EmailProviderGmail, Address: "other@example.com", Enabled: true})
	f.must(err)
	foreign := f.receive("foreign", time.Now())
	f.owner, f.box = owner, box
	if w := f.request("GET", "/api/email/messages/"+foreign.ID+"/file", ""); w.Code != 404 {
		t.Fatalf("foreign source status=%d", w.Code)
	}
	if w := f.request("POST", "/api/email/messages/viewed", fmt.Sprintf(`{"mail_ids":[%q]}`, foreign.ID)); w.Code != 404 {
		t.Fatalf("foreign viewing status=%d", w.Code)
	}
	representation, _, err := f.repo.GetEmailRepresentation(t.Context(), owner, mail.RepresentationID)
	f.must(err)
	f.must(os.WriteFile(filepath.Join(f.root, representation.Attachments[0].Path), []byte("tampered"), 0600))
	if w := f.request("GET", "/api/email/messages/"+mail.ID+"/file?part_id=part-1", ""); w.Code != 503 || strings.Contains(w.Body.String(), f.root) || strings.Contains(w.Body.String(), "tampered") {
		t.Fatalf("tampered source response=%d %s", w.Code, w.Body.String())
	}
}

func TestEmailManagementHTTPBoundedInputAndCoalescedScheduling(t *testing.T) {
	f := newEmailHTTPFixture(t)
	for _, query := range []string{"?limit=101", "?limit=bad", "?owner_id=other-owner", "?limit=1&limit=2"} {
		if w := f.request("GET", "/api/email/conversations"+query, ""); w.Code != 400 {
			t.Fatalf("invalid query %s status=%d", query, w.Code)
		}
	}
	for _, body := range []string{`{"mail_ids":[]}`, `{"mail_ids":["x"],"owner_id":"other-owner"}`, `{"mail_ids":["x"]} {}`} {
		if w := f.request("POST", "/api/email/messages/viewed", body); w.Code != 400 {
			t.Fatalf("invalid view %s status=%d", body, w.Code)
		}
	}
	ids := make([]string, 101)
	raw, _ := json.Marshal(map[string]any{"mail_ids": ids})
	if w := f.request("POST", "/api/email/messages/viewed", string(raw)); w.Code != 400 {
		t.Fatalf("oversized batch status=%d", w.Code)
	}
	refreshID := ""
	for i := 0; i < 2; i++ {
		result := emailDecode[emailmanagement.ScheduleResult](t, f.request("POST", "/api/email/sync", `{}`), 202)
		if len(result.RefreshRequests) != 1 || result.RefreshRequests[0].MailboxID != f.box.ID || result.RefreshRequests[0].RefreshRequestID == "" {
			t.Fatal("refresh response missing request identity")
		}
		if i > 0 && refreshID != result.RefreshRequests[0].RefreshRequestID {
			t.Fatal("duplicate HTTP request created a different refresh")
		}
		refreshID = result.RefreshRequests[0].RefreshRequestID
	}
	status := emailDecode[emailmanagement.StatusView](t, f.request("GET", "/api/email/sync-status", ""), 200)
	if len(status.Mailboxes) != 1 || !status.Mailboxes[0].RefreshPending || status.Mailboxes[0].RefreshRequestID != refreshID {
		t.Fatal("status omitted pending refresh identity")
	}
	jobs, err := f.repo.ListEmailJobs(t.Context(), store.EmailQuery{OwnerID: f.owner, Limit: 100})
	f.must(err)
	if len(jobs) != 1 || jobs[0].Kind != app.EmailJobDiscover || f.browser.calls != 0 {
		t.Fatalf("sync jobs=%+v browser=%d", jobs, f.browser.calls)
	}
	body := fmt.Sprintf(`{"intake_enabled":false,"expected_mailbox_version":%d}`, f.box.Version)
	configured := emailDecode[struct {
		Mailbox emailmanagement.MailboxView `json:"mailbox"`
	}](t, f.request("PATCH", "/api/email/providers/gmail", body), 200)
	if configured.Mailbox.IntakeEnabled || !configured.Mailbox.ActiveBinding || f.browser.calls != 0 {
		t.Fatalf("mailbox=%+v browser=%d", configured.Mailbox, f.browser.calls)
	}
	if configured.Mailbox.RefreshPending {
		t.Fatal("paused mailbox retained refresh lock")
	}
	if w := f.request("PATCH", "/api/email/providers/gmail", body); w.Code != 409 {
		t.Fatalf("stale mailbox version status=%d: %s", w.Code, w.Body.String())
	}
	if w := f.request("POST", "/api/email/sync", `{}`); w.Code != 409 {
		t.Fatalf("paused sync status=%d", w.Code)
	}
}

func TestEmailManagementHTTPSourceSearchAndConcerns(t *testing.T) {
	f := newEmailHTTPFixture(t)
	at := time.Now()
	first := f.assign(f.receive("alpha", at), "")
	initialBox := f.box
	var err error
	f.box, err = f.repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: f.command(), Provider: app.EmailProviderOutlook, Address: "second@example.com", Enabled: true})
	f.must(err)
	second := f.assign(f.receive("beta", at.Add(time.Minute)), "")
	filtered := emailDecode[emailmanagement.ConversationsView](t, f.request("GET", "/api/email/conversations?mailbox_id="+initialBox.ID, ""), 200)
	if len(filtered.Conversations) != 1 || filtered.Conversations[0].ID != first.ConversationID {
		t.Fatalf("source filter=%+v", filtered)
	}
	searched := emailDecode[emailmanagement.ConversationsView](t, f.request("GET", "/api/email/conversations?q="+url.QueryEscape("unique-contract-term-beta"), ""), 200)
	if len(searched.Conversations) != 1 || searched.Conversations[0].ID != second.ConversationID {
		t.Fatalf("member content search=%+v", searched)
	}
	_, err = f.repo.RequestEmailJob(t.Context(), store.EmailJobRequest{EmailCommand: f.command(), Kind: app.EmailJobRelationshipCheck, TargetID: first.ID, Rearm: true})
	f.must(err)
	var job app.EmailJob
	for i := 0; i < 20; i++ {
		job = f.claim(app.EmailJobRelationshipCheck)
		if job.TargetID == first.ID {
			break
		}
		f.finish(job)
	}
	target, _, err := f.repo.GetEmailAnalysisTarget(t.Context(), f.owner, job.Kind, job.TargetID)
	f.must(err)
	_, err = f.repo.PublishEmailConcern(t.Context(), store.EmailConcernCommand{EmailCommand: f.command(), Lease: f.lease(job), TargetID: first.ID, Generation: target.Generation, Concern: app.EmailAssignmentConcern{ID: app.NewID("concern"), Kind: app.EmailConcernSuspectedDuplicate, MailIDs: []string{first.ID}, ConversationIDs: []string{first.ConversationID, second.ConversationID}, Reason: "Shared procurement evidence", InputFingerprint: target.InputFingerprint}})
	f.must(err)
	f.finish(job)
	detail := emailDecode[emailmanagement.ConversationDetail](t, f.request("GET", "/api/email/conversations/"+first.ConversationID, ""), 200)
	if len(detail.Conversation.Concerns) != 1 || len(detail.Conversation.Concerns[0].RelatedConversationIDs) != 1 || detail.Conversation.Concerns[0].RelatedConversationIDs[0] != second.ConversationID {
		t.Fatalf("concern projection=%+v", detail)
	}
	list := emailDecode[emailmanagement.ConversationsView](t, f.request("GET", "/api/email/conversations", ""), 200)
	if len(list.Conversations) != 2 {
		t.Fatal("concern merged or removed an existing conversation")
	}
}

func TestEmailManagementHTTPReanalysisRetainsHistoryWithoutSummaryResurrection(t *testing.T) {
	f := newEmailHTTPFixture(t)
	mail := f.receive("historical-summary", time.Now())
	job := f.claim(app.EmailJobMessageSummary)
	summary, err := f.repo.PublishEmailSummary(t.Context(), store.EmailSummaryCommand{EmailCommand: f.command(), Lease: f.lease(job), Summary: app.EmailSummary{ID: "historical-output", TargetKind: job.Kind, TargetID: mail.ID, Text: "Historical generated output", ModelVersion: "fixture", PromptVersion: "legacy", Generation: job.Generation, InputFingerprint: job.InputFingerprint}})
	f.must(err)
	f.finish(job)
	if !summary.Current {
		t.Fatal("historical fixture was not current")
	}
	emailDecode[emailmanagement.ScheduleResult](t, f.request("POST", "/api/email/messages/"+mail.ID+"/reanalyze", "{}"), 202)
	current, found, err := f.repo.GetEmailMail(t.Context(), f.owner, mail.ID)
	f.must(err)
	if !found || current.Summary == nil || current.Summary.Text != summary.Text {
		t.Fatal("reanalysis deleted historical output")
	}
	view := emailDecode[emailmanagement.MessageView](t, f.request("GET", "/api/email/messages/"+mail.ID, ""), 200)
	if view.BodyText != "unique-contract-term-historical-summary" || !view.OriginalAvailable {
		t.Fatalf("reading depends on generated summary: %+v", view)
	}
	_, claimed, err := f.repo.ClaimEmailJob(t.Context(), store.EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobMessageSummary, app.EmailJobConversationSummary, app.EmailJobRelationshipCheck, app.EmailJobPresentation}, LeaseDuration: time.Minute})
	f.must(err)
	if claimed || f.browser.calls != 0 {
		t.Fatalf("disabled generation claimed=%v browser=%d", claimed, f.browser.calls)
	}
	classification, found, err := f.repo.ClaimEmailJob(t.Context(), store.EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobClassification}, LeaseDuration: time.Minute})
	f.must(err)
	if !found || classification.TargetID != mail.ID {
		t.Fatal("explicit reanalysis did not schedule classification")
	}
}

func TestEmailManagementHTTPReanalysisRetriesIncompleteParsingWithoutRecapture(t *testing.T) {
	for _, state := range []string{app.EmailParsePartial, app.EmailParseUnsupported, app.EmailParseFailed, app.EmailParseReady} {
		t.Run(state, func(t *testing.T) {
			f := newEmailHTTPFixture(t)
			mail := f.receive("retry-extraction", time.Now())
			representation, found, err := f.repo.GetEmailRepresentation(t.Context(), f.owner, mail.RepresentationID)
			f.must(err)
			if !found {
				t.Fatal("missing source representation")
			}
			_, err = f.repo.RequestEmailJob(t.Context(), store.EmailJobRequest{EmailCommand: f.command(), Kind: app.EmailJobParse, TargetID: mail.ID, Rearm: true})
			f.must(err)
			job := f.claim(app.EmailJobParse)
			representation.ID, representation.State = app.NewID("rep"), state
			_, err = f.repo.PublishEmailRepresentation(t.Context(), store.EmailRepresentationCommand{EmailCommand: f.command(), Lease: f.lease(job), Representation: representation})
			f.must(err)
			f.finish(job)

			emailDecode[emailmanagement.ScheduleResult](t, f.request("POST", "/api/email/messages/"+mail.ID+"/reanalyze", "{}"), 202)
			parse, queued, err := f.repo.ClaimEmailJob(t.Context(), store.EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobParse}, LeaseDuration: time.Minute})
			f.must(err)
			if queued != (state != app.EmailParseReady) || (queued && parse.TargetID != mail.ID) {
				t.Fatalf("parse retry for %s: queued=%v job=%+v", state, queued, parse)
			}
			_, recapture, err := f.repo.ClaimEmailJob(t.Context(), store.EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobCapture}, LeaseDuration: time.Minute})
			f.must(err)
			if recapture || f.browser.calls != 0 {
				t.Fatal("reanalysis recaptured an already committed source")
			}
		})
	}
}
