package store

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type emailContractFixture struct {
	t     *testing.T
	repo  EmailRepository
	owner string
	n     int
	box   app.EmailMailbox
}

func (f *emailContractFixture) command() EmailCommand {
	f.n++
	return EmailCommand{OwnerID: f.owner, CommandKey: fmt.Sprintf("command-%d", f.n)}
}
func (f *emailContractFixture) must(err error) {
	f.t.Helper()
	if err != nil {
		f.t.Fatal(err)
	}
}
func (f *emailContractFixture) claim(kind string) app.EmailJob {
	f.t.Helper()
	j, ok, err := f.repo.ClaimEmailJob(f.t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{kind}, LeaseDuration: time.Minute})
	f.must(err)
	if !ok {
		f.t.Fatalf("no %s job", kind)
	}
	return j
}
func (f *emailContractFixture) lease(j app.EmailJob) EmailJobLease {
	return EmailJobLease{OwnerID: f.owner, JobID: j.ID, LeaseToken: j.LeaseToken}
}
func (f *emailContractFixture) finish(j app.EmailJob) {
	_, err := f.repo.FinishEmailJob(f.t.Context(), EmailJobFinish{EmailJobLease: f.lease(j)})
	f.must(err)
}
func (f *emailContractFixture) admit(id string, at time.Time) app.EmailMail {
	f.t.Helper()
	r, err := f.repo.AdmitEmailDiscovery(f.t.Context(), EmailDiscoveryCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, ObservedAt: time.Now(), Coverage: "partial", Members: []EmailDiscoveryMember{{ProviderMessageID: id, ProviderSelectionID: id, Direction: "inbound", SourceTime: at}}})
	f.must(err)
	return r.Mails[0]
}
func (f *emailContractFixture) capture(m app.EmailMail) app.EmailMail {
	f.t.Helper()
	j := f.claim(app.EmailJobCapture)
	if j.TargetID != m.ID {
		f.t.Fatalf("capture target = %s want %s", j.TargetID, m.ID)
	}
	v, err := f.repo.PublishEmailCapture(f.t.Context(), EmailCaptureCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, Lease: f.lease(j), Capture: app.EmailCaptureVersion{ID: "capture-" + m.ID, MailID: m.ID, ManifestPath: "source/manifest.json", ManifestSHA256: strings.Repeat("a", 64), OriginalPath: "source/original.eml", OriginalSHA256: strings.Repeat("b", 64), State: app.EmailCaptureComplete}})
	f.must(err)
	f.finish(j)
	return v
}
func (f *emailContractFixture) parse(m app.EmailMail, messageID string) app.EmailMail {
	f.t.Helper()
	j := f.claim(app.EmailJobParse)
	v, err := f.repo.PublishEmailRepresentation(f.t.Context(), EmailRepresentationCommand{EmailCommand: f.command(), Lease: f.lease(j), Representation: app.EmailRepresentation{ID: "representation-" + m.ID, MailID: m.ID, CaptureID: m.CaptureID, Subject: "purchase", From: []string{"supplier@example.com"}, To: []string{f.box.Address}, MessageID: messageID, BodyText: "Please approve this purchase.", State: app.EmailParseReady, Coverage: "complete", ParserVersion: "v1", ManifestPath: "representations/message.json", ManifestSHA256: strings.Repeat("c", 64)}})
	f.must(err)
	f.finish(j)
	return f.classify(v)
}
func (f *emailContractFixture) classify(m app.EmailMail) app.EmailMail {
	f.t.Helper()
	for n := 0; n < 100; n++ {
		j := f.claim(app.EmailJobClassification)
		current, ok, err := f.repo.GetEmailMail(f.t.Context(), f.owner, j.TargetID)
		f.must(err)
		if !ok {
			f.t.Fatal("missing classification target")
		}
		_, err = f.repo.PublishEmailClassification(f.t.Context(), EmailClassificationCommand{EmailCommand: f.command(), Lease: f.lease(j), MailID: j.TargetID, Generation: j.Generation, Classification: app.EmailClassification{Category: "interaction", EvidenceRefs: []string{"representation:" + current.RepresentationID + ":body"}, InputFingerprint: j.InputFingerprint}})
		f.must(err)
		f.finish(j)
		if j.TargetID == m.ID {
			out, _, err := f.repo.GetEmailMail(f.t.Context(), f.owner, m.ID)
			f.must(err)
			return out
		}
	}
	f.t.Fatal("classification fixture exceeded bound")
	return m
}
func (f *emailContractFixture) drain() {
	f.t.Helper()
	for i := 0; i < 1000; i++ {
		r, err := f.repo.ExpandEmailRefresh(f.t.Context(), EmailRefreshCommand{EmailCommand: f.command(), Limit: 2})
		f.must(err)
		if !r.Remaining {
			return
		}
	}
	f.t.Fatal("refresh did not drain")
}
func (f *emailContractFixture) assign(m app.EmailMail, title string) app.EmailMail {
	f.t.Helper()
	f.drain()
	j := f.claim(app.EmailJobAssignment)
	target, ok, err := f.repo.GetEmailAnalysisTarget(f.t.Context(), f.owner, j.Kind, j.TargetID)
	f.must(err)
	if !ok {
		f.t.Fatal("missing assignment target")
	}
	candidates, err := f.repo.FindEmailCandidates(f.t.Context(), EmailCandidateQuery{OwnerID: f.owner, MailID: m.ID})
	f.must(err)
	v, err := f.repo.CommitEmailAssignment(f.t.Context(), EmailAssignmentCommand{EmailCommand: f.command(), Lease: f.lease(j), Generation: target.Generation, Decision: app.EmailAssignmentDecision{ID: "decision-" + m.ID, MailID: m.ID, Action: "new", Title: title, OwnerEpoch: candidates.OwnerEpoch, InputFingerprint: target.InputFingerprint, ModelVersion: "fixture", PromptVersion: "v1", EvidenceRefs: []string{m.ID}}})
	f.must(err)
	f.finish(j)
	return v
}
func emailFixture(t *testing.T, repo EmailRepository) *emailContractFixture {
	f := &emailContractFixture{t: t, repo: repo, owner: "email-owner"}
	var err error
	f.box, err = repo.BindEmailMailbox(t.Context(), EmailBindCommand{EmailCommand: f.command(), Provider: app.EmailProviderGmail, Address: "User+tag@EXAMPLE.com", Enabled: true})
	f.must(err)
	return f
}
func TestEmailManagementContract(t *testing.T) {
	for _, backend := range []string{"memory", "file", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var repo EmailRepository
			switch backend {
			case "memory":
				repo = NewMemoryStore()
			case "file":
				p := filepath.Join(t.TempDir(), "state.json")
				s, err := NewFileStore(p)
				if err != nil {
					t.Fatal(err)
				}
				repo = s
			case "postgres":
				dsn := newPostgresMigrationTestSchema(t)
				s, err := NewPostgresStore(t.Context(), dsn)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(s.Close)
				repo = s
			}
			exerciseEmailManagement(t, repo)
		})
	}
}
func exerciseEmailManagement(t *testing.T, repo EmailRepository) {
	f := emailFixture(t, repo)
	at := time.Now().Add(-time.Hour)
	// Exact identity admission and cursor are one idempotent commit.
	c := EmailDiscoveryCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, ObservedAt: time.Now(), Coverage: "partial", Members: []EmailDiscoveryMember{{ProviderMessageID: "A", ProviderSelectionID: "A", Direction: "inbound", SourceTime: at}}}
	first, err := repo.AdmitEmailDiscovery(t.Context(), c)
	f.must(err)
	again, err := repo.AdmitEmailDiscovery(t.Context(), c)
	f.must(err)
	if first.Mails[0].ID != again.Mails[0].ID {
		t.Fatal("duplicate discovery changed identity")
	}
	receipt, ok, err := repo.ReconcileEmailCommand(t.Context(), f.owner, c.CommandKey)
	f.must(err)
	if !ok || receipt.ContentHash == "" {
		t.Fatal("lost response cannot reconcile")
	}
	c.Cursor = "changed"
	if _, err = repo.AdmitEmailDiscovery(t.Context(), c); StoreErrorCodeOf(err) != StoreErrorConflict {
		t.Fatalf("changed command accepted: %v", err)
	}
	a := f.parse(f.capture(first.Mails[0]), "<A@example.com>")
	a = f.assign(a, "purchase A")
	b := f.parse(f.capture(f.admit("B", at.Add(-24*time.Hour))), "<B@example.com>")
	b = f.assign(b, "purchase B")
	cMail := f.parse(f.capture(f.admit("C", at.Add(time.Hour))), "<C@example.com>")
	// Actual presented IDs leave the history-page gap unseen.
	_, err = repo.MarkEmailMailsViewed(t.Context(), EmailViewedCommand{EmailCommand: f.command(), MailIDs: []string{a.ID, cMail.ID}})
	f.must(err)
	bRead, ok, err := repo.GetEmailMail(t.Context(), f.owner, b.ID)
	f.must(err)
	if !ok || bRead.ViewedAt != nil {
		t.Fatal("unseen historical B was acknowledged by a later mail")
	}
	_, err = repo.MarkEmailMailsViewed(t.Context(), EmailViewedCommand{EmailCommand: EmailCommand{OwnerID: "other-owner", CommandKey: "bad-view"}, MailIDs: []string{a.ID}})
	if StoreErrorCodeOf(err) != StoreErrorNotFound {
		t.Fatalf("cross-owner view accepted: %v", err)
	}
	// Publish a current summary, then invalidate its selected related context.
	f.drain()
	j := f.claim(app.EmailJobMessageSummary)
	target, _, err := repo.GetEmailAnalysisTarget(t.Context(), f.owner, j.Kind, j.TargetID)
	f.must(err)
	summary, err := repo.PublishEmailSummary(t.Context(), EmailSummaryCommand{EmailCommand: f.command(), Lease: f.lease(j), Summary: app.EmailSummary{ID: "summary-1", TargetKind: j.Kind, TargetID: j.TargetID, Text: "A purchase awaits approval.", ModelVersion: "fixture", PromptVersion: "v1", Generation: target.Generation, InputFingerprint: target.InputFingerprint}})
	f.must(err)
	if !summary.Current {
		t.Fatal("fresh summary rejected")
	}
	f.finish(j)
	changed, err := repo.PublishEmailContext(t.Context(), EmailContextCommand{EmailCommand: f.command(), Context: app.EmailContextVersion{ID: "context-1", MailID: j.TargetID, RelatedMailIDs: []string{a.ID, b.ID}, Coverage: "partial"}})
	f.must(err)
	projected, _, err := repo.GetEmailMail(t.Context(), f.owner, changed.ID)
	f.must(err)
	if projected.Summary == nil || projected.Summary.Current {
		t.Fatal("old summary remained current before fan-out")
	}
	f.drain()
	if assigned, _, err := repo.GetEmailMail(t.Context(), f.owner, a.ID); err != nil || assigned.ConversationID != a.ConversationID {
		t.Fatal("context refresh moved fixed membership")
	}
	// Account switch fences an admitted old browser attempt immediately.
	stale := f.claim(app.EmailJobMarkRead)
	newBox, err := repo.BindEmailMailbox(t.Context(), EmailBindCommand{EmailCommand: f.command(), Provider: app.EmailProviderGmail, Address: "replacement@example.com", Enabled: true, ExpectedVersion: f.box.Version})
	f.must(err)
	if newBox.ID == f.box.ID {
		t.Fatal("new account reused old mailbox")
	}
	_, err = repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(stale)})
	if StoreErrorCodeOf(err) != StoreErrorConflict {
		t.Fatalf("old binding job committed: %v", err)
	}
	status, err := repo.GetEmailOwnerStatus(t.Context(), f.owner)
	f.must(err)
	if status.CapturedCount != 3 || status.ConversationCount != 2 || status.PendingCount != 0 || status.Revision < 1 {
		t.Fatalf("wrong projection counters: %+v", status)
	}
}
func TestEmailManagementExpiredLeaseAndConcurrentClaim(t *testing.T) {
	f := emailFixture(t, NewMemoryStore())
	f.admit("lease", time.Now())
	var wg sync.WaitGroup
	claimed := make(chan app.EmailJob, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			j, ok, err := f.repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobCapture}, LeaseDuration: time.Minute})
			if err != nil {
				t.Error(err)
			}
			if ok {
				claimed <- j
			}
		}()
	}
	wg.Wait()
	close(claimed)
	if len(claimed) != 1 {
		t.Fatalf("parallel claims=%d", len(claimed))
	}
	old := <-claimed
	j, ok, err := f.repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobCapture}, Now: time.Now().Add(2 * time.Minute), LeaseDuration: time.Minute})
	f.must(err)
	if !ok || j.LeaseToken == old.LeaseToken {
		t.Fatal("expired job not recovered")
	}
	_, err = f.repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(old)})
	if StoreErrorCodeOf(err) != StoreErrorConflict {
		t.Fatalf("stale worker finished new lease: %v", err)
	}
}
func TestEmailManagementFileRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	f := emailFixture(t, s)
	m := f.admit("restart", time.Now())
	s, err = NewFileStore(path)
	f.must(err)
	loaded, ok, err := s.GetEmailMail(t.Context(), f.owner, m.ID)
	f.must(err)
	if !ok || loaded.ID != m.ID {
		t.Fatal("admitted identity lost on restart")
	}
	j, ok, err := s.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobCapture}})
	f.must(err)
	if !ok || j.TargetID != m.ID {
		t.Fatal("durable capture intent lost on restart")
	}
}
