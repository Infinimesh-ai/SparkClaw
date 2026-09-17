package store

import (
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"strings"
	"testing"
	"time"
)

func TestSourceSummaryCannotFeedBackIntoClassification(t *testing.T) {
	repo := NewMemoryStore()
	f := emailFixture(t, repo)
	profile, err := repo.SaveOwnerProfile(t.Context(), app.OwnerProfile{ID: f.owner, DisplayName: "Owner", Preferences: map[string]string{app.OwnerPreferenceLanguage: "zh"}})
	f.must(err)
	_, err = repo.ActivateEmailEventPolicy(t.Context(), f.command())
	f.must(err)
	m := f.parse(f.capture(f.admit("source-summary", time.Now())), "<source@example.test>")
	j := f.claim(app.EmailJobMessageSummary)
	target, _, err := repo.GetEmailAnalysisTarget(t.Context(), f.owner, j.Kind, m.ID)
	f.must(err)
	for ref := range target.Inputs {
		if strings.HasPrefix(ref, "mail:") || strings.HasPrefix(ref, "summary:") || ref == "search" {
			t.Fatalf("derived dependency %q", ref)
		}
	}
	if !emailHasSourceSummaryPolicy(target) {
		t.Fatal("missing source summary policy fence")
	}
	classification, _, err := repo.GetEmailAnalysisTarget(t.Context(), f.owner, app.EmailJobClassification, m.ID)
	f.must(err)
	_, err = repo.PublishEmailSummary(t.Context(), EmailSummaryCommand{EmailCommand: f.command(), Lease: f.lease(j), Summary: app.EmailSummary{ID: "source-summary-result", TargetKind: j.Kind, TargetID: m.ID, Text: "供应商请求批准采购。", Language: "zh", ModelVersion: "fixture", PromptVersion: "source-v1", Generation: j.Generation, InputFingerprint: j.InputFingerprint}})
	f.must(err)
	f.finish(j)
	profile.Preferences[app.OwnerPreferenceLanguage] = "en"
	_, err = repo.SaveOwnerProfile(t.Context(), profile)
	f.must(err)
	for i := 0; i < 100; i++ {
		_, err = repo.ExpandEmailRefresh(t.Context(), EmailRefreshCommand{EmailCommand: f.command(), Limit: 100})
		f.must(err)
	}
	after, _, err := repo.GetEmailAnalysisTarget(t.Context(), f.owner, j.Kind, m.ID)
	f.must(err)
	if after.Generation != target.Generation || after.InputFingerprint != target.InputFingerprint || after.State != app.EmailSummaryCurrent {
		t.Fatalf("summary churned: before=%+v after=%+v", target, after)
	}
	c, _, err := repo.GetEmailAnalysisTarget(t.Context(), f.owner, app.EmailJobClassification, m.ID)
	f.must(err)
	if c.Generation != classification.Generation || c.InputFingerprint != classification.InputFingerprint {
		t.Fatal("summary invalidated classification")
	}
	_, claimed, err := repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobMessageSummary}, Now: time.Now().Add(time.Hour)})
	f.must(err)
	if claimed {
		t.Fatal("unchanged summary invoked again")
	}
}
