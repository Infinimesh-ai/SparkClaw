package store

import (
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"path/filepath"
	"testing"
	"time"
)

func TestEmailClassificationManualRulesAndDurability(t *testing.T) {
	for _, backend := range []string{"memory", "file", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var repo EmailRepository
			var filePath string
			switch backend {
			case "memory":
				repo = NewMemoryStore()
			case "file":
				filePath = filepath.Join(t.TempDir(), "state.json")
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
			at := time.Now().Add(-time.Hour)
			first := f.parse(f.capture(f.admit("first", at)), "<first@test>")
			first = f.assign(first, "matter")
			historic := f.parse(f.capture(f.admit("historic", at.Add(time.Minute))), "<historic@test>")
			c := EmailClassificationOverride{EmailCommand: f.command(), MailID: first.ID, Entry: "notification", ExpectedVersion: first.Classification.Revision, RememberSender: true}
			moved, err := repo.OverrideEmailClassification(t.Context(), c)
			f.must(err)
			if moved.ConversationID != first.ConversationID || moved.Classification.Source != "manual" {
				t.Fatal("manual edit changed membership or provenance")
			}
			replay, err := repo.OverrideEmailClassification(t.Context(), c)
			f.must(err)
			if replay.Classification.Revision != moved.Classification.Revision {
				t.Fatal("replay revision changed")
			}
			c.EmailCommand = f.command()
			if _, err = repo.OverrideEmailClassification(t.Context(), c); StoreErrorCodeOf(err) != StoreErrorConflict {
				t.Fatal("stale revision accepted")
			}
			future := f.parse(f.capture(f.admit("future", at.Add(2*time.Minute))), "<future@test>")
			if future.Classification.Source != "rule" || future.Classification.EffectiveEntry != "notification" || future.ConversationID != "" {
				t.Fatal("future exact sender rule not applied")
			}
			old, _, err := repo.GetEmailMail(t.Context(), f.owner, historic.ID)
			f.must(err)
			if old.Classification.EffectiveEntry != "interaction" {
				t.Fatal("rule rewrote admitted history")
			}
			notices, err := repo.ListEmailMails(t.Context(), EmailQuery{OwnerID: f.owner, Entry: "notification"})
			f.must(err)
			if len(notices.Items) != 2 {
				t.Fatalf("notice count %d", len(notices.Items))
			}
			pending, err := repo.ListEmailMails(t.Context(), EmailQuery{OwnerID: f.owner, PendingOnly: true})
			f.must(err)
			if len(pending.Items) != 0 {
				t.Fatal("routed mail remains pending")
			}
			rules, err := repo.ListEmailSenderRules(t.Context(), EmailQuery{OwnerID: f.owner})
			f.must(err)
			if len(rules) != 1 {
				t.Fatal("missing rule")
			}
			other, err := repo.ListEmailSenderRules(t.Context(), EmailQuery{OwnerID: "other"})
			f.must(err)
			if len(other) != 0 {
				t.Fatal("owner rule leak")
			}
			_, err = repo.UpdateEmailSenderRule(t.Context(), EmailSenderRuleCommand{EmailCommand: f.command(), RuleID: rules[0].ID, Entry: "notification", Enabled: false, ExpectedVersion: rules[0].Revision})
			f.must(err)
			after := f.parse(f.capture(f.admit("after", at.Add(3*time.Minute))), "<after@test>")
			if after.Classification.Source != "model" {
				t.Fatal("disabled rule applied")
			}
			if backend == "file" {
				reopened, err := NewFileStore(filePath)
				f.must(err)
				saved, ok, err := reopened.GetEmailMail(t.Context(), f.owner, first.ID)
				f.must(err)
				if !ok || saved.Classification.Source != "manual" {
					t.Fatal("manual choice not durable")
				}
			}
		})
	}
}
func TestEmailClassificationUnknownAndFailedFallback(t *testing.T) {
	f := emailFixture(t, NewMemoryStore())
	m := f.capture(f.admit("unknown", time.Now()))
	j := f.claim(app.EmailJobParse)
	_, err := f.repo.PublishEmailRepresentation(t.Context(), EmailRepresentationCommand{EmailCommand: f.command(), Lease: f.lease(j), Representation: app.EmailRepresentation{ID: "rep", MailID: m.ID, CaptureID: m.CaptureID, From: []string{"service@example.com"}, BodyText: "See attachment", State: app.EmailParseReady}})
	f.must(err)
	f.finish(j)
	j = f.claim(app.EmailJobClassification)
	result, err := f.repo.PublishEmailClassification(t.Context(), EmailClassificationCommand{EmailCommand: f.command(), Lease: f.lease(j), MailID: m.ID, Generation: j.Generation, Classification: app.EmailClassification{Category: "unknown", Uncertainty: true, InputFingerprint: j.InputFingerprint}})
	f.must(err)
	if result.Classification.EffectiveEntry != "interaction" || result.Classification.Source != "fallback" {
		t.Fatal("unknown did not route interaction")
	}
}

func TestManualChoiceRejectsInFlightClassification(t *testing.T) {
	f := emailFixture(t, NewMemoryStore())
	m := f.parse(f.capture(f.admit("race", time.Now())), "<race@test>")
	// Introduce a fresh source generation, lease it, and change routing while
	// model work is in flight. Old evidence must not win publication.
	_, err := f.repo.PublishEmailContext(t.Context(), EmailContextCommand{EmailCommand: f.command(), Context: app.EmailContextVersion{ID: "race-context", MailID: m.ID, Coverage: "partial"}})
	f.must(err)
	f.drain()
	j := f.claim(app.EmailJobClassification)
	changed, err := f.repo.OverrideEmailClassification(t.Context(), EmailClassificationOverride{EmailCommand: f.command(), MailID: m.ID, Entry: "notification", ExpectedVersion: m.Classification.Revision})
	f.must(err)
	_, err = f.repo.PublishEmailClassification(t.Context(), EmailClassificationCommand{EmailCommand: f.command(), Lease: f.lease(j), MailID: m.ID, Generation: j.Generation, Classification: app.EmailClassification{Category: "interaction", InputFingerprint: j.InputFingerprint}})
	if StoreErrorCodeOf(err) != StoreErrorConflict {
		t.Fatal("stale model publication not rejected")
	}
	saved, _, err := f.repo.GetEmailMail(t.Context(), f.owner, m.ID)
	f.must(err)
	if saved.Classification.Revision != changed.Classification.Revision || saved.Classification.Source != "manual" || saved.Classification.EffectiveEntry != "notification" {
		t.Fatal("manual route overwritten")
	}
}
