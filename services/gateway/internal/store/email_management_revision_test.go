package store

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func emailManagementBackends(t *testing.T, exercise func(*testing.T, EmailRepository)) {
	t.Helper()
	for _, name := range []string{"memory", "file", "postgres"} {
		t.Run(name, func(t *testing.T) {
			var repo EmailRepository
			switch name {
			case "memory":
				repo = NewMemoryStore()
			case "file":
				s, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
				if err != nil {
					t.Fatal(err)
				}
				repo = s
			case "postgres":
				s, err := NewPostgresStore(t.Context(), newPostgresMigrationTestSchema(t))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(s.Close)
				repo = s
			}
			exercise(t, repo)
		})
	}
}
func TestEmailManagementConcurrentEpochAndFixedConcerns(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		a := f.parse(f.capture(f.admit("a", time.Now())), "<a@sample>")
		b := f.parse(f.capture(f.admit("b", time.Now())), "<b@sample>")
		f.drain()
		jobs := []app.EmailJob{f.claim(app.EmailJobAssignment), f.claim(app.EmailJobAssignment)}
		commands := []EmailAssignmentCommand{}
		for i, j := range jobs {
			target, _, err := repo.GetEmailAnalysisTarget(t.Context(), f.owner, j.Kind, j.TargetID)
			f.must(err)
			candidates, err := repo.FindEmailCandidates(t.Context(), EmailCandidateQuery{OwnerID: f.owner, MailID: j.TargetID})
			f.must(err)
			commands = append(commands, EmailAssignmentCommand{EmailCommand: f.command(), Lease: f.lease(j), Generation: target.Generation, Decision: app.EmailAssignmentDecision{ID: fmt.Sprintf("parallel-%d", i), MailID: j.TargetID, Action: "new", Title: "purchase", OwnerEpoch: candidates.OwnerEpoch, InputFingerprint: target.InputFingerprint, ModelVersion: "fixture", PromptVersion: "v1"}})
		}
		first, err := repo.CommitEmailAssignment(t.Context(), commands[0])
		f.must(err)
		f.finish(jobs[0])
		if _, err = repo.CommitEmailAssignment(t.Context(), commands[1]); StoreErrorCodeOf(err) != StoreErrorConflict {
			t.Fatalf("old epoch committed: %v", err)
		}
		f.finish(jobs[1])
		f.drain()
		jobs[1] = f.claim(app.EmailJobAssignment)
		refreshed, _, err := repo.GetEmailAnalysisTarget(t.Context(), f.owner, jobs[1].Kind, jobs[1].TargetID)
		f.must(err)
		commands[1].Lease = f.lease(jobs[1])
		commands[1].Generation = refreshed.Generation
		commands[1].Decision.InputFingerprint = refreshed.InputFingerprint
		fresh, err := repo.FindEmailCandidates(t.Context(), EmailCandidateQuery{OwnerID: f.owner, MailID: jobs[1].TargetID})
		f.must(err)
		commands[1].Decision.OwnerEpoch = fresh.OwnerEpoch
		second, err := repo.CommitEmailAssignment(t.Context(), commands[1])
		f.must(err)
		f.finish(jobs[1])
		if first.ConversationID == second.ConversationID {
			t.Fatal("test needs two established topics")
		}
		// Newly resolved evidence schedules a check and retains both memberships.
		_, err = repo.PublishEmailContext(t.Context(), EmailContextCommand{EmailCommand: f.command(), Context: app.EmailContextVersion{ID: "late-parent", MailID: a.ID, RelatedMailIDs: []string{b.ID}, Coverage: "complete_for_observation"}})
		f.must(err)
		f.drain()
		check := f.claim(app.EmailJobRelationshipCheck)
		target, _, err := repo.GetEmailAnalysisTarget(t.Context(), f.owner, check.Kind, check.TargetID)
		f.must(err)
		concern, err := repo.PublishEmailConcern(t.Context(), EmailConcernCommand{EmailCommand: f.command(), Lease: f.lease(check), TargetID: check.TargetID, Generation: target.Generation, Concern: app.EmailAssignmentConcern{ID: "duplicate-evidence", Kind: app.EmailConcernSuspectedDuplicate, MailIDs: []string{a.ID, b.ID}, ConversationIDs: []string{first.ConversationID, second.ConversationID}, Reason: "late source establishes same topic", EvidenceRefs: []string{a.ID, b.ID}, InputFingerprint: target.InputFingerprint}})
		f.must(err)
		f.finish(check)
		for _, mail := range []app.EmailMail{first, second} {
			stored, _, err := repo.GetEmailMail(t.Context(), f.owner, mail.ID)
			f.must(err)
			if stored.ConversationID != mail.ConversationID {
				t.Fatal("concern moved membership")
			}
			concerns, err := repo.ListEmailConcerns(t.Context(), EmailQuery{OwnerID: f.owner, ConversationID: mail.ConversationID})
			f.must(err)
			if len(concerns) != 1 || concerns[0].ID != concern.ID {
				t.Fatal("concern missing on one topic")
			}
		}
		// Full inputs, not member count, make an in-flight summary stale.
		job := f.claim(app.EmailJobConversationSummary)
		old, _, err := repo.GetEmailAnalysisTarget(t.Context(), f.owner, job.Kind, job.TargetID)
		f.must(err)
		member := first
		if member.ConversationID != job.TargetID {
			member = second
		}
		_, err = repo.PublishEmailContext(t.Context(), EmailContextCommand{EmailCommand: f.command(), Context: app.EmailContextVersion{ID: "better-coverage", MailID: member.ID, RelatedMailIDs: []string{a.ID, b.ID}, Coverage: "complete"}})
		f.must(err)
		f.drain()
		summary, err := repo.PublishEmailSummary(t.Context(), EmailSummaryCommand{EmailCommand: f.command(), Lease: f.lease(job), Summary: app.EmailSummary{ID: "obsolete-result", TargetKind: job.Kind, TargetID: job.TargetID, Text: "old summary", Generation: old.Generation, InputFingerprint: old.InputFingerprint, ModelVersion: "fixture", PromptVersion: "v1"}})
		f.must(err)
		if summary.Current {
			t.Fatal("old generation replaced current pointer")
		}
		f.finish(job)
		latest, _, err := repo.GetEmailAnalysisTarget(t.Context(), f.owner, job.Kind, job.TargetID)
		f.must(err)
		if latest.Generation <= old.Generation || latest.State != app.EmailSummaryPending {
			t.Fatalf("new refresh cleared by old completion: %+v", latest)
		}
		// Search through member body and mailbox filtering precede pagination.
		page, err := repo.ListEmailConversations(t.Context(), EmailQuery{OwnerID: f.owner, MailboxID: f.box.ID, Search: "approve", Limit: 1})
		f.must(err)
		if len(page.Items) != 1 || page.NextCursor == "" {
			t.Fatal("conversation content search/pagination failed")
		}
		none, err := repo.ListEmailConversations(t.Context(), EmailQuery{OwnerID: f.owner, MailboxID: "other-mailbox", Limit: 1})
		f.must(err)
		if len(none.Items) != 0 {
			t.Fatal("mailbox filter applied after paging")
		}
		// Entry counts include captured, unassigned interaction mail and do not
		// shrink to the conversation page. Cursors cannot cross endpoint scopes.
		f.parse(f.capture(f.admit("unassigned-count", time.Now())), "<unassigned@sample>")
		scope := EmailQuery{OwnerID: f.owner, MailboxID: f.box.ID, Entry: "interaction", Limit: 1}
		routed, err := repo.ListEmailConversations(t.Context(), scope)
		f.must(err)
		if len(routed.Items) != 1 || routed.NextCursor == "" || routed.Counts == nil || routed.Counts.Total != 3 || routed.Counts.Unseen != 3 {
			t.Fatalf("conversation page lost full interaction mail counts: %+v", routed)
		}
		scope.After = routed.NextCursor
		next, err := repo.ListEmailConversations(t.Context(), scope)
		f.must(err)
		if len(next.Items) != 1 || next.Items[0].ID == routed.Items[0].ID || next.Counts.Total != 3 {
			t.Fatal("conversation cursor/count incorrect")
		}
		if _, err := repo.ListEmailMails(t.Context(), scope); StoreErrorCodeOf(err) != StoreErrorInvalid {
			t.Fatal("conversation cursor accepted by mail endpoint")
		}
		scope.Search = "different"
		if _, err := repo.ListEmailConversations(t.Context(), scope); StoreErrorCodeOf(err) != StoreErrorInvalid {
			t.Fatal("conversation cursor crossed search scope")
		}
	})
}
func TestEmailManagementBindingResumeAndIndependentProgress(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		original := f.box
		m := f.admit("pinned", time.Now())
		replacement, err := repo.BindEmailMailbox(t.Context(), EmailBindCommand{EmailCommand: f.command(), Provider: f.box.Provider, Address: "replacement@example.com", ExpectedVersion: f.box.Version, Enabled: true})
		f.must(err)
		if _, ok, err := repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobCapture}}); err != nil || ok {
			t.Fatal("paused old account was admitted")
		}
		f.box, err = repo.BindEmailMailbox(t.Context(), EmailBindCommand{EmailCommand: f.command(), Provider: original.Provider, Address: original.Address, ExpectedVersion: replacement.Version, Enabled: true})
		f.must(err)
		if f.box.ID != original.ID || f.box.BindingGeneration <= original.BindingGeneration || !f.box.Boundary.Equal(original.Boundary) {
			t.Fatal("same account lost identity/boundary")
		}
		_, _, err = repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobCapture}})
		f.must(err)
		j := f.claim(app.EmailJobCapture)
		if j.TargetID != m.ID || j.BindingGeneration != f.box.BindingGeneration {
			t.Fatal("pinned work not rebound")
		}
		now := time.Now()
		_, err = repo.AdmitEmailDiscovery(t.Context(), EmailDiscoveryCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, Trigger: "recent", ObservedAt: now, Coverage: "complete", CompletedBoundary: now, Cursor: "recent-progress"})
		f.must(err)
		_, err = repo.AdmitEmailDiscovery(t.Context(), EmailDiscoveryCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, Trigger: "unread", ObservedAt: now, Coverage: "partial", Cursor: "unread-progress"})
		f.must(err)
		_, err = repo.AdmitEmailDiscovery(t.Context(), EmailDiscoveryCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, Trigger: "thread", ThreadID: "empty-thread", ProviderSelectionID: "thread-locator", ObservedAt: now, Coverage: "partial", Cursor: "thread-progress"})
		f.must(err)
		box, _, err := repo.GetEmailMailbox(t.Context(), f.owner, f.box.ID)
		f.must(err)
		if box.Cursor != "recent-progress" || box.Coverage != "complete" || !box.Boundary.Equal(postgresTime(now)) {
			t.Fatal("unread/thread overwritten recent coverage")
		}
		threads, err := repo.ListEmailThreads(t.Context(), EmailQuery{OwnerID: f.owner, MailboxID: box.ID})
		f.must(err)
		if len(threads) != 1 || threads[0].ProviderSelectionID != "thread-locator" {
			t.Fatal("memberless thread inventory not durable")
		}
	})
}
func TestEmailManagementClaimBeyondOtherKindBacklog(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		var m app.EmailMail
		for page := 0; page < 2; page++ {
			members := []EmailDiscoveryMember{}
			for n := 0; n < 60; n++ {
				id := fmt.Sprintf("backlog-%03d", page*60+n)
				members = append(members, EmailDiscoveryMember{ProviderMessageID: id, ProviderSelectionID: id, Direction: "inbound"})
			}
			r, err := repo.AdmitEmailDiscovery(t.Context(), EmailDiscoveryCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, Members: members, ObservedAt: time.Now(), Coverage: "partial"})
			f.must(err)
			m = r.Mails[0]
		}
		_, err := repo.RequestEmailJob(t.Context(), EmailJobRequest{EmailCommand: f.command(), Kind: app.EmailJobParse, TargetID: m.ID})
		f.must(err)
		job := f.claim(app.EmailJobParse)
		if job.TargetID != m.ID {
			t.Fatal("model/local work starved behind browser jobs")
		}
	})
}
func TestEmailManagementPartialSourcePermitsLocalParse(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		m := f.admit("partial", time.Now())
		j := f.claim(app.EmailJobCapture)
		m, err := repo.PublishEmailCapture(t.Context(), EmailCaptureCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, Lease: f.lease(j), Capture: app.EmailCaptureVersion{ID: "partial-capture", MailID: m.ID, State: app.EmailCapturePartial, ManifestPath: "source/manifest.json", ManifestSHA256: "sha256:" + strings.Repeat("a", 64), OriginalPath: "source/original.eml", OriginalSHA256: "sha256:" + strings.Repeat("b", 64)}})
		f.must(err)
		f.finish(j)
		parse := f.claim(app.EmailJobParse)
		_, err = repo.PublishEmailRepresentation(t.Context(), EmailRepresentationCommand{EmailCommand: f.command(), Lease: f.lease(parse), Representation: app.EmailRepresentation{ID: "partial-representation", MailID: m.ID, CaptureID: m.CaptureID, State: app.EmailParsePartial, BodyText: "readable body", Attachments: []app.EmailAttachment{{ID: "missing-1", Name: "unavailable.pdf", State: "missing"}}}})
		f.must(err)
		if _, ok, err := repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobMarkRead}}); err != nil || ok {
			t.Fatal("partial capture admitted mark-read")
		}
	})
}
