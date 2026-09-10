package store

import (
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"path/filepath"
	"testing"
	"time"
)

func activateEvents(t *testing.T, f *emailContractFixture) {
	t.Helper()
	_, err := f.repo.ActivateEmailEventPolicy(t.Context(), f.command())
	f.must(err)
}
func TestEmailEventsMissingTitleDoesNotBlockMembership(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		activateEvents(t, f)
		mail := f.parse(f.capture(f.admit("untitled-event", time.Now())), "<untitled@example.com>")
		mail = f.assign(mail, "")
		conversation, found, err := repo.GetEmailConversation(t.Context(), f.owner, mail.ConversationID)
		f.must(err)
		if !found || conversation.MemberCount != 1 || conversation.Title != mail.Subject || conversation.TitleState != "source_fallback" {
			t.Fatalf("missing title blocked membership or hid fallback: %+v", conversation)
		}
	})
}
func manualEvent(t *testing.T, f *emailContractFixture, m app.EmailMail, id, title string) app.EmailMail {
	t.Helper()
	out, err := f.repo.ChangeEmailAssignment(t.Context(), EmailManualAssignment{EmailCommand: f.command(), MailID: m.ID, ConversationID: id, Title: title, ExpectedVersion: m.InputVersion})
	f.must(err)
	return out
}
func TestEmailEventsManualCorrectionConvergesAndPreservesSources(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		activateEvents(t, f)
		a := f.parse(f.capture(f.admit("event-a", time.Now())), "<event-a@example.com>")
		a = manualEvent(t, f, a, "", "Confirm purchase quotation")
		b := f.parse(f.capture(f.admit("event-b", time.Now().Add(time.Second))), "<event-b@example.com>")
		// A leased decision from before the human correction cannot publish afterwards.
		f.drain()
		job := f.claim(app.EmailJobAssignment)
		target, _, err := repo.GetEmailAnalysisTarget(t.Context(), f.owner, job.Kind, b.ID)
		f.must(err)
		candidates, err := repo.FindEmailCandidates(t.Context(), EmailCandidateQuery{OwnerID: f.owner, MailID: b.ID})
		f.must(err)
		b = manualEvent(t, f, b, "", "Coordinate launch date")
		_, err = repo.CommitEmailAssignment(t.Context(), EmailAssignmentCommand{EmailCommand: f.command(), Lease: f.lease(job), Generation: job.Generation, Decision: app.EmailAssignmentDecision{ID: "old-decision", MailID: b.ID, Action: "new", Title: "Wrong old event", OwnerEpoch: candidates.OwnerEpoch, InputFingerprint: target.InputFingerprint, ModelVersion: "fixture", PromptVersion: EmailEventPolicyVersion}})
		if StoreErrorCodeOf(err) != StoreErrorConflict {
			t.Fatalf("late model accepted: %v", err)
		}
		oldEvent := b.ConversationID
		beforeSource := b.RepresentationID
		command := EmailManualAssignment{EmailCommand: f.command(), MailID: b.ID, ConversationID: a.ConversationID, ExpectedVersion: b.InputVersion}
		moved, err := repo.ChangeEmailAssignment(t.Context(), command)
		f.must(err)
		replay, err := repo.ChangeEmailAssignment(t.Context(), command)
		f.must(err)
		if replay.InputVersion != moved.InputVersion || moved.AssignmentSource != "manual" || moved.RepresentationID != beforeSource || moved.MessageID != b.MessageID {
			t.Fatal("correction lost source or replayed mutation")
		}
		if _, err = repo.ChangeEmailAssignment(t.Context(), EmailManualAssignment{EmailCommand: f.command(), MailID: b.ID, ConversationID: oldEvent, ExpectedVersion: b.InputVersion}); StoreErrorCodeOf(err) != StoreErrorConflict {
			t.Fatalf("stale correction accepted: %v", err)
		}
		current, _, err := repo.GetEmailConversation(t.Context(), f.owner, a.ConversationID)
		f.must(err)
		if current.MemberCount != 2 || current.UnseenCount != 2 {
			t.Fatalf("wrong aggregate %+v", current)
		}
		empty, _, err := repo.GetEmailConversation(t.Context(), f.owner, oldEvent)
		f.must(err)
		if empty.MemberCount != 0 {
			t.Fatal("old event member remains")
		}
		rows, err := repo.ListEmailConversations(t.Context(), EmailQuery{OwnerID: f.owner})
		f.must(err)
		if len(rows.Items) != 1 {
			t.Fatalf("empty event visible: %d", len(rows.Items))
		}
		// Titles are presentation metadata, not assignment identity or model evidence.
		renamed, err := repo.RenameEmailConversation(t.Context(), EmailConversationRename{EmailCommand: f.command(), ConversationID: current.ID, ExpectedVersion: current.InputVersion, Title: "Confirm revised purchase quotation"})
		f.must(err)
		if renamed.MembershipVersion != current.MembershipVersion {
			t.Fatal("rename changed membership")
		}
		_, err = repo.MarkEmailMailsViewed(t.Context(), EmailViewedCommand{EmailCommand: f.command(), MailIDs: []string{moved.ID}})
		f.must(err)
		f.drain()
		before, err := repo.ListEmailJobs(t.Context(), EmailQuery{OwnerID: f.owner, Limit: 100})
		f.must(err)
		for i := 0; i < 100; i++ {
			_, err = repo.ExpandEmailRefresh(t.Context(), EmailRefreshCommand{EmailCommand: f.command(), Limit: 100})
			f.must(err)
		}
		after, err := repo.ListEmailJobs(t.Context(), EmailQuery{OwnerID: f.owner, Limit: 100})
		f.must(err)
		if string(emailJSON(before)) != string(emailJSON(after)) {
			t.Fatal("fixed sources created or changed jobs")
		}
		current, _, err = repo.GetEmailConversation(t.Context(), f.owner, current.ID)
		f.must(err)
		if current.UnseenCount != 1 {
			t.Fatal("view count incorrect")
		}
		searched, err := repo.ListEmailConversations(t.Context(), EmailQuery{OwnerID: f.owner, Entry: "interaction", Search: "revised purchase"})
		f.must(err)
		if len(searched.Items) != 1 || searched.Counts.Total != 2 || searched.Counts.Unseen != 1 {
			t.Fatalf("event title search counts diverged: %+v", searched)
		}

	})
}
func TestEmailEventsNotificationAndMixedProjection(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		activateEvents(t, f)
		a := f.parse(f.capture(f.admit("notice-event", time.Now())), "<notice-event@example.com>")
		a, err := repo.OverrideEmailClassification(t.Context(), EmailClassificationOverride{EmailCommand: f.command(), MailID: a.ID, Entry: "notification", ExpectedVersion: a.Classification.Revision})
		f.must(err)
		a = manualEvent(t, f, a, "", "Track order delivery")
		notices, err := repo.ListEmailConversations(t.Context(), EmailQuery{OwnerID: f.owner, Entry: "notification"})
		f.must(err)
		if len(notices.Items) != 1 || notices.Counts == nil || notices.Counts.Total != 1 || notices.Items[0].EffectiveEntry != "notification" {
			t.Fatalf("notice event missing %+v", notices)
		}
		b := f.parse(f.capture(f.admit("interaction-event", time.Now().Add(time.Second))), "<interaction-event@example.com>")
		b = manualEvent(t, f, b, a.ConversationID, "")
		notices, err = repo.ListEmailConversations(t.Context(), EmailQuery{OwnerID: f.owner, Entry: "notification"})
		f.must(err)
		if len(notices.Items) != 0 || notices.Counts.Total != 0 {
			t.Fatal("mixed event duplicated in notices")
		}
		interactions, err := repo.ListEmailConversations(t.Context(), EmailQuery{OwnerID: f.owner, Entry: "interaction"})
		f.must(err)
		if len(interactions.Items) != 1 || interactions.Counts.Total != 2 || interactions.Counts.Unseen != 2 {
			t.Fatalf("mixed projection mismatch %+v", interactions)
		}
		a, _, err = repo.GetEmailMail(t.Context(), f.owner, a.ID)
		f.must(err)
		if a.Classification.EffectiveEntry != "notification" {
			t.Fatal("event projection overwrote mail category")
		}
		candidates, err := repo.FindEmailCandidates(t.Context(), EmailCandidateQuery{OwnerID: f.owner, MailID: b.ID})
		f.must(err)
		found := false
		for _, m := range candidates.RelatedMails {
			found = found || m.ID == a.ID
		}
		if !found {
			t.Fatal("notification excluded from evidence")
		}
	})
}
func TestEmailEventsPolicyFencesLegacyAndSurvivesFileRestart(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "state.json")
	repo, err := NewFileStore(filename)
	if err != nil {
		t.Fatal(err)
	}
	f := emailFixture(t, repo)
	m := f.parse(f.capture(f.admit("legacy", time.Now())), "<legacy@example.com>")
	legacy := f.claim(app.EmailJobMessageSummary)
	activateEvents(t, f)
	_, err = repo.PublishEmailSummary(t.Context(), EmailSummaryCommand{EmailCommand: f.command(), Lease: f.lease(legacy), Summary: app.EmailSummary{ID: "late", TargetKind: legacy.Kind, TargetID: m.ID, Text: "late summary", ModelVersion: "fixture", PromptVersion: "old", Generation: legacy.Generation, InputFingerprint: legacy.InputFingerprint}})
	if StoreErrorCodeOf(err) != StoreErrorConflict {
		t.Fatalf("legacy summary published: %v", err)
	}
	f.drain()
	resumed, err := NewFileStore(filename)
	f.must(err)
	f.repo = resumed
	for _, kind := range []string{app.EmailJobMessageSummary, app.EmailJobRelationshipCheck, app.EmailJobConversationSummary, app.EmailJobPresentation} {
		_, found, err := resumed.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{kind}})
		f.must(err)
		if found {
			t.Fatalf("disabled %s claimed", kind)
		}
	}
	p, err := resumed.EnsureEmailPresentations(t.Context(), EmailPresentationCommand{EmailCommand: f.command(), Query: EmailPresentationQuery{OwnerID: f.owner, TargetKind: "mail", TargetIDs: []string{m.ID}, Language: "zh"}, Retry: true})
	f.must(err)
	if len(p) != 1 || p[0].State != "suspended" {
		t.Fatalf("presentation rearmed %+v", p)
	}
	for i := 0; i < 3; i++ {
		job, err := resumed.RequestEmailJob(t.Context(), EmailJobRequest{EmailCommand: f.command(), Kind: app.EmailJobMessageSummary, TargetID: m.ID, Rearm: true, ForceAnalysis: true})
		f.must(err)
		if job.State != app.EmailJobPaused {
			t.Fatal("legacy summary revived")
		}
	}
}
