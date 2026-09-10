package store

import (
	"fmt"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"path/filepath"
	"testing"
	"time"
)

func TestEmailPresentationContract(t *testing.T) {
	for _, backend := range []string{"memory", "file", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var repo EmailRepository
			filePath := filepath.Join(t.TempDir(), "state.json")
			switch backend {
			case "memory":
				repo = NewMemoryStore()
			case "file":
				s, err := NewFileStore(filePath)
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
			m := f.admit("presentation-target", time.Now())
			m = f.capture(m)
			m = f.parse(m, "presentation@example.com")
			presentations := repo.(EmailPresentationRepository)
			q := EmailPresentationQuery{OwnerID: f.owner, TargetKind: "mail", TargetIDs: []string{m.ID}, Language: "zh"}
			statusBefore, err := repo.GetEmailOwnerStatus(t.Context(), f.owner)
			f.must(err)
			read, err := presentations.ReadEmailPresentations(t.Context(), q)
			f.must(err)
			if len(read) != 1 || read[0].State != "missing" {
				t.Fatalf("initial read=%+v", read)
			}
			statusAfter, err := repo.GetEmailOwnerStatus(t.Context(), f.owner)
			f.must(err)
			if statusBefore.Revision != statusAfter.Revision {
				t.Fatal("GET mutated owner projection")
			}
			ensure := func(q EmailPresentationQuery, retry bool) app.EmailLocalizedPresentation {
				t.Helper()
				out, err := presentations.EnsureEmailPresentations(t.Context(), EmailPresentationCommand{EmailCommand: f.command(), Query: q, Retry: retry})
				f.must(err)
				return out[0]
			}
			zh := ensure(q, false)
			for range 4 {
				same := ensure(q, false)
				if same.ID != zh.ID {
					t.Fatal("ensure failed coalescing")
				}
			}
			english := q
			english.Language = "en"
			en := ensure(english, false)
			if en.ID == zh.ID {
				t.Fatal("language key collision")
			}
			if backend == "file" {
				reopened, err := NewFileStore(filePath)
				f.must(err)
				repo = reopened
				f.repo = reopened
				presentations = reopened
				got, err := presentations.ReadEmailPresentations(t.Context(), q)
				f.must(err)
				if got[0].State != "queued" {
					t.Fatal("queued language job was not durable")
				}
			}
			first := f.claim(app.EmailJobPresentation)
			second := f.claim(app.EmailJobPresentation)
			if first.TargetID == second.TargetID {
				t.Fatal("same presentation admitted twice")
			}
			_, found, err := repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobPresentation}, LeaseDuration: time.Minute})
			f.must(err)
			if found {
				t.Fatal("duplicate jobs created by ensure")
			}
			enJob, zhJob := first, second
			if first.TargetID == zh.ID {
				zhJob, enJob = first, second
			}
			en.Title = "English title"
			en.Summary = "English summary"
			en.Explanation = "A specific confirmation request."
			_, err = presentations.PublishEmailPresentation(t.Context(), EmailPresentationPublish{EmailCommand: f.command(), Lease: f.lease(enJob), Presentation: en})
			f.must(err)
			alternate := en
			alternate.Summary = "MUST_NOT_REPLACE_COMMITTED_TEXT"
			preserved, err := presentations.PublishEmailPresentation(t.Context(), EmailPresentationPublish{EmailCommand: f.command(), Lease: f.lease(enJob), Presentation: alternate})
			f.must(err)
			if preserved.Summary != en.Summary || preserved.Revision != 1 {
				t.Fatal("repeat publication replaced committed cache")
			}
			f.finish(enJob)
			current, err := presentations.ReadEmailPresentations(t.Context(), q)
			f.must(err)
			if current[0].State == "ready" || current[0].Summary != "" {
				t.Fatal("late English result leaked into Chinese view")
			}
			_, err = repo.PublishEmailContext(t.Context(), EmailContextCommand{EmailCommand: f.command(), Context: app.EmailContextVersion{ID: "new-context", MailID: m.ID, Coverage: "updated"}})
			f.must(err)
			zh.Title = "中文标题"
			zh.Summary = "中文摘要"
			_, err = presentations.PublishEmailPresentation(t.Context(), EmailPresentationPublish{EmailCommand: f.command(), Lease: f.lease(zhJob), Presentation: zh})
			if err == nil {
				t.Fatal("stale source output published")
			}
			current, err = presentations.ReadEmailPresentations(t.Context(), english)
			f.must(err)
			if current[0].State != "missing" || current[0].Summary != "" {
				t.Fatal("stale English presentation served as current")
			}
			newZH := ensure(q, false)
			failed := f.claim(app.EmailJobPresentation)
			if failed.TargetID != newZH.ID {
				t.Fatal("unexpected fresh job")
			}
			_, err = repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(failed), ErrorCode: "email_model_unavailable"})
			f.must(err)
			if got := ensure(q, false); got.State != "failed" {
				t.Fatalf("polling rearmed failure: %s", got.State)
			}
			if got := ensure(q, true); got.State != "queued" {
				t.Fatal("explicit retry did not queue")
			}
			other := q
			other.OwnerID = "other-owner"
			if _, err := presentations.ReadEmailPresentations(t.Context(), other); err == nil {
				t.Fatal("cross owner target readable")
			}
			invalid := q
			invalid.Language = "fr"
			if _, err := presentations.ReadEmailPresentations(t.Context(), invalid); err == nil {
				t.Fatal("unsupported language accepted")
			}
			invalid = q
			invalid.TargetIDs = make([]string, 101)
			for i := range invalid.TargetIDs {
				invalid.TargetIDs[i] = fmt.Sprint(i)
			}
			if _, err := presentations.EnsureEmailPresentations(t.Context(), EmailPresentationCommand{EmailCommand: f.command(), Query: invalid}); err == nil {
				t.Fatal("unbounded ensure accepted")
			}
		})
	}
}
