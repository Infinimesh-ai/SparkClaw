package store

import (
	"encoding/json"
	"fmt"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEmailScopeCountsValidityAndCursorBinding(t *testing.T) {
	for _, backend := range []string{"memory", "file", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var repo EmailRepository
			switch backend {
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
			f := emailFixture(t, repo)
			now := time.Now()
			ids := []string{}
			for i := 0; i < 3; i++ {
				m := f.parse(f.capture(f.admit(fmt.Sprint(i), now.Add(time.Duration(i)*time.Minute))), fmt.Sprintf("<%d@source>", i))
				ids = append(ids, m.ID)
				_, err := repo.RequestEmailJob(t.Context(), EmailJobRequest{EmailCommand: f.command(), Kind: app.EmailJobClassification, TargetID: m.ID, Rearm: true, ForceAnalysis: true})
				f.must(err)
				j := f.claim(app.EmailJobClassification)
				var verification *app.EmailVerification
				if i < 2 {
					at := now.Add(time.Hour)
					if i == 0 {
						at = now.Add(-time.Hour)
					}
					verification = &app.EmailVerification{Code: "purchase", Purpose: "fixture", EvidenceRef: "representation:" + m.RepresentationID + ":body", ExpiresAt: &at}
				}
				_, err = repo.PublishEmailClassification(t.Context(), EmailClassificationCommand{EmailCommand: f.command(), Lease: f.lease(j), MailID: m.ID, Generation: j.Generation, Verification: verification, Classification: app.EmailClassification{Category: "notification", NotificationSubtype: "verification", EvidenceRefs: []string{"representation:" + m.RepresentationID + ":body"}, InputFingerprint: j.InputFingerprint}})
				f.must(err)
				f.finish(j)
			}
			_, err := repo.MarkEmailMailsViewed(t.Context(), EmailViewedCommand{EmailCommand: f.command(), MailIDs: []string{ids[0]}})
			f.must(err)
			q := EmailQuery{OwnerID: f.owner, Entry: "notification", Limit: 1}
			page, err := repo.ListEmailMails(t.Context(), q)
			f.must(err)
			if len(page.Items) != 1 || page.Counts == nil || page.Counts.Total != 3 || page.Counts.Unseen != 2 || page.NextCursor == "" {
				t.Fatalf("scope counts truncated to page: %+v", page)
			}
			q.After = page.NextCursor
			second, err := repo.ListEmailMails(t.Context(), q)
			f.must(err)
			if second.Items[0].ID == page.Items[0].ID || second.Counts.Total != 3 {
				t.Fatal("page cursor/count incorrect")
			}
			changed := q
			changed.Entry = "interaction"
			if _, err := repo.ListEmailMails(t.Context(), changed); StoreErrorCodeOf(err) != StoreErrorInvalid {
				t.Fatal("cursor crossed category")
			}
			changed = q
			changed.Search = "other"
			if _, err := repo.ListEmailMails(t.Context(), changed); StoreErrorCodeOf(err) != StoreErrorInvalid {
				t.Fatal("cursor crossed search")
			}
			for _, state := range []string{"expired", "not_expired", "validity_unknown"} {
				filtered := EmailQuery{OwnerID: f.owner, Entry: "notification", Validity: state, AsOf: now, Limit: 10}
				out, err := repo.ListEmailMails(t.Context(), filtered)
				f.must(err)
				if len(out.Items) != 1 || out.Counts.Total != 1 {
					t.Fatalf("validity %s incorrectly includes unknown/other states: %+v", state, out)
				}
			}
			// A verification code is useful primary content, so a localized
			// presentation must preserve it rather than replacing it generically.
			pr := repo.(EmailPresentationRepository)
			ps, err := pr.EnsureEmailPresentations(t.Context(), EmailPresentationCommand{EmailCommand: f.command(), Query: EmailPresentationQuery{OwnerID: f.owner, TargetKind: "mail", TargetIDs: []string{ids[1]}, Language: "zh"}})
			f.must(err)
			pj := f.claim(app.EmailJobPresentation)
			p := ps[0]
			p.Title = "purchase"
			p.Summary = "purchase"
			p.Explanation = "purchase"
			p.RequestedResponse = "purchase"
			p.Purpose = "purchase"
			p.ServiceLabel = "purchase"
			p.Evidence = []app.EmailClassificationEvidence{{Ref: "body", Text: "purchase"}}
			p.ConcernExplanations = map[string]string{"c": "purchase"}
			published, err := pr.PublishEmailPresentation(t.Context(), EmailPresentationPublish{EmailCommand: f.command(), Lease: f.lease(pj), Presentation: p})
			f.must(err)
			encoded, _ := json.Marshal(published)
			if !strings.Contains(string(encoded), "purchase") {
				t.Fatal("known code was hidden from presentation")
			}
			other := q
			other.After = ""
			other.OwnerID = "other-owner"
			empty, err := repo.ListEmailMails(t.Context(), other)
			f.must(err)
			if empty.Counts.Total != 0 {
				t.Fatal("counts leaked owner")
			}
		})
	}
}
