package store

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func emailCandidateFixtureMail(f *emailContractFixture, id string, at time.Time, from, subject, body, thread string, refs []string) app.EmailMail {
	f.t.Helper()
	discovery, err := f.repo.AdmitEmailDiscovery(f.t.Context(), EmailDiscoveryCommand{EmailCommand: f.command(), MailboxID: f.box.ID, BindingGeneration: f.box.BindingGeneration, ObservedAt: time.Now(), Coverage: "partial", Members: []EmailDiscoveryMember{{ProviderMessageID: id, ProviderSelectionID: id, ProviderThreadID: thread, Direction: "inbound", SourceTime: at}}})
	f.must(err)
	m := f.capture(discovery.Mails[0])
	j := f.claim(app.EmailJobParse)
	v, err := f.repo.PublishEmailRepresentation(f.t.Context(), EmailRepresentationCommand{EmailCommand: f.command(), Lease: f.lease(j), Representation: app.EmailRepresentation{ID: "representation-" + m.ID, MailID: m.ID, CaptureID: m.CaptureID, Subject: subject, From: []string{from}, To: []string{f.box.Address}, MessageID: "<" + id + "@source>", ReplyReferences: refs, BodyText: body, State: app.EmailParseReady, Coverage: "complete", ParserVersion: "v1", ManifestPath: "representations/message.json", ManifestSHA256: strings.Repeat("c", 64)}})
	f.must(err)
	f.finish(j)
	return f.classify(v)
}

func TestEmailManagementCandidateEvidencePriority(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		at := time.Now().Add(-time.Hour)
		old := emailCandidateFixtureMail(f, "older-topic", at.Add(-24*time.Hour), "vendor@business.test", "Fwd: Atlas procurement", "Quartz invoice", "", nil)
		old = f.assign(old, "Atlas procurement")
		crowdedTopic := emailCandidateFixtureMail(f, "crowded-old-topic", at.Add(-12*time.Hour), "crowded@example.net", "Fwd: Cobalt renewal", "Zircon statement", "", nil)
		crowdedTopic = f.assign(crowdedTopic, "Cobalt renewal")
		for i := 0; i < 12; i++ {
			emailCandidateFixtureMail(f, fmt.Sprintf("noise-%d", i), at.Add(time.Duration(i)*time.Minute), "crowded@example.net", "Weekly newsletter", "A separate digest quotes "+old.MessageID, "", nil)
		}
		cases := []struct {
			id, from, subject, body string
			refs                    []string
			want                    app.EmailMail
		}{
			{"counterpart", "vendor@business.test", "Updated proposal", "Details", nil, old},
			{"subject", "crowded@example.net", "回复： Fwd: Cobalt renewal", "Details", nil, crowdedTopic},
			{"content", "crowded@example.net", "Discussion update", "Zircon statement", nil, crowdedTopic},
			{"reply-priority", "crowded@example.net", "Weekly newsletter", "A separate digest", []string{old.MessageID}, old},
		}
		for _, tc := range cases {
			t.Run(tc.id, func(t *testing.T) {
				m := emailCandidateFixtureMail(f, tc.id, at.Add(time.Hour), tc.from, tc.subject, tc.body, "", tc.refs)
				candidates, err := repo.FindEmailCandidates(t.Context(), EmailCandidateQuery{OwnerID: f.owner, MailID: m.ID, Limit: 8})
				f.must(err)
				if !slices.ContainsFunc(candidates.RelatedMails, func(other app.EmailMail) bool { return other.ID == tc.want.ID }) {
					t.Fatalf("older evidence displaced by recent mail addressed to this owner: %+v", candidates.RelatedMails)
				}
				if tc.id != "content" && candidates.RelatedMails[0].ID != tc.want.ID {
					t.Fatal("stronger matching evidence did not retain priority")
				}
				if len(candidates.Conversations) == 0 || candidates.Conversations[0].ID != tc.want.ConversationID {
					t.Fatalf("lost older matching conversation: %+v", candidates.Conversations)
				}
			})
		}
	})
}

func TestEmailManagementCandidateProviderThreadScope(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		at := time.Now().Add(-time.Hour)
		original := f.box
		old := emailCandidateFixtureMail(f, "same-thread", at.Add(-time.Hour), "thread@business.test", "Old thread subject", "Original", "provider-thread", nil)
		var err error
		f.box, err = repo.BindEmailMailbox(t.Context(), EmailBindCommand{EmailCommand: f.command(), Provider: app.EmailProviderOutlook, Address: "alias@example.net", Enabled: true})
		f.must(err)
		wrong := emailCandidateFixtureMail(f, "other-mailbox", at, "other@business.test", "Different source", "Unrelated", "provider-thread", nil)
		alias := f.box.Address
		f.box = original
		m := emailCandidateFixtureMail(f, "thread-source", at.Add(time.Hour), alias, "No shared subject", "Distinct", "provider-thread", nil)
		set, err := repo.FindEmailCandidates(t.Context(), EmailCandidateQuery{OwnerID: f.owner, MailID: m.ID, Limit: 8})
		f.must(err)
		if len(set.RelatedMails) == 0 || set.RelatedMails[0].ID != old.ID {
			t.Fatalf("native thread did not precede semantic fallback: %+v", set.RelatedMails)
		}
		if slices.ContainsFunc(set.RelatedMails, func(other app.EmailMail) bool { return other.ID == wrong.ID }) {
			t.Fatal("same opaque thread ID or owned alias linked different receiving accounts")
		}
	})
}

func TestEmailManagementLongReplyHistoryUsesBoundedAnalysisWindow(t *testing.T) {
	emailManagementBackends(t, func(t *testing.T, repo EmailRepository) {
		f := emailFixture(t, repo)
		refs := make([]string, 240)
		for i := range refs {
			refs[i] = fmt.Sprintf("<parent-%03d@source>", i)
		}
		// A direct In-Reply-To occurrence at the end outranks older history,
		// including its own earlier appearance in the References header.
		refs = append(refs, refs[3])
		m := emailCandidateFixtureMail(f, "long-history", time.Now(), "longthread@example.net", "Long discussion", "Update", "", refs)
		if !slices.Equal(m.ReplyReferences, refs) {
			t.Fatal("Mail projection changed complete header order")
		}
		r, ok, err := repo.GetEmailRepresentation(t.Context(), f.owner, m.RepresentationID)
		f.must(err)
		if !ok || !slices.Equal(r.ReplyReferences, refs) {
			t.Fatal("normalized representation lost raw reply history")
		}
		selected := app.EmailAnalysisReferences(refs)
		if len(selected) != app.EmailAnalysisReferenceLimit || selected[0] != refs[3] || selected[1] != refs[239] {
			t.Fatalf("recent reference window dropped direct parent: %v", selected)
		}
		for _, kind := range []string{app.EmailJobMessageSummary, app.EmailJobAssignment} {
			target, found, err := repo.GetEmailAnalysisTarget(t.Context(), f.owner, kind, m.ID)
			f.must(err)
			if !found || len(target.Inputs) > app.EmailAnalysisReferenceLimit+2 {
				t.Fatalf("parse could not admit bounded %s work: %+v", kind, target)
			}
			for _, ref := range selected {
				if _, tracked := target.Inputs["reply:"+ref]; !tracked {
					t.Fatalf("selected reference is not tracked for invalidation: %s", ref)
				}
			}
			if _, tracked := target.Inputs["reply:"+refs[0]]; tracked {
				t.Fatal("historical reference escaped the selected analysis window")
			}
		}
		if _, err := repo.FindEmailCandidates(t.Context(), EmailCandidateQuery{OwnerID: f.owner, MailID: m.ID, Limit: 8}); err != nil {
			t.Fatalf("long history candidate lookup failed: %v", err)
		}
		// Explicitly selected references survive retries while automatic header
		// windows are replaced; repeated parsing cannot accumulate >200 edges.
		const explicit = "reply:<explicit-parent@source>"
		_, err = repo.RequestEmailJob(t.Context(), EmailJobRequest{EmailCommand: f.command(), Kind: app.EmailJobMessageSummary, TargetID: m.ID, Dependencies: []string{explicit}})
		f.must(err)
		for revision := 1; revision <= 8; revision++ {
			_, err = repo.RequestEmailJob(t.Context(), EmailJobRequest{EmailCommand: f.command(), Kind: app.EmailJobParse, TargetID: m.ID, Rearm: true})
			f.must(err)
			job := f.claim(app.EmailJobParse)
			r.ID = fmt.Sprintf("reparsed-%d", revision)
			r.ReplyReferences = make([]string, 240)
			for i := range r.ReplyReferences {
				r.ReplyReferences[i] = fmt.Sprintf("<revision-%d-parent-%03d@source>", revision, i)
			}
			m, err = repo.PublishEmailRepresentation(t.Context(), EmailRepresentationCommand{EmailCommand: f.command(), Lease: f.lease(job), Representation: r})
			f.must(err)
			f.finish(job)
			m = f.classify(m)
			target, found, err := repo.GetEmailAnalysisTarget(t.Context(), f.owner, app.EmailJobMessageSummary, m.ID)
			f.must(err)
			if !found || len(target.Inputs) != app.EmailAnalysisReferenceLimit+2 {
				t.Fatalf("revision %d accumulated old automatic windows: %+v", revision, target)
			}
			if _, tracked := target.Inputs[explicit]; !tracked {
				t.Fatal("explicit selected reference was lost during reparse")
			}
			if _, tracked := target.Inputs["reply:"+selected[0]]; tracked {
				t.Fatal("superseded automatic reference still affects this analysis")
			}
		}
	})
}
