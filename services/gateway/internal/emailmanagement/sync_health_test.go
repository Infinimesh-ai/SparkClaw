package emailmanagement

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestSyncHealthUsesProgressAndActualSchedule(t *testing.T) {
	s, browser, _ := newFixtureService(t, store.NewMemoryStore())
	s.browser = &emptyPageFixture{intakeFixture: browser}
	s.opts.ScanInterval, s.opts.JobTimeout = time.Minute, 5*time.Minute
	s.opts.QualifiedWakeProviders = map[string]bool{app.EmailProviderGmail: true}
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	base := app.EmailMailbox{Active: true, IntakeEnabled: true, Provider: app.EmailProviderGmail, IntakeEnabledAt: now.Add(-time.Hour), ActivatedAt: now.Add(-24 * time.Hour), PollThrough: now.Add(-time.Minute)}
	for _, tc := range []struct {
		name   string
		change func(*app.EmailMailbox, *app.EmailJob)
		want   string
	}{
		{"empty successful poll", func(b *app.EmailMailbox, j *app.EmailJob) {}, SyncHealthHealthy},
		{"failed retries cannot reset stale progress", func(b *app.EmailMailbox, j *app.EmailJob) {
			b.PollThrough = now.Add(-time.Hour)
			b.UpdatedAt, b.LastCheckedAt = now, now
			j.State = app.EmailJobRetryWait
			j.UpdatedAt, j.NextAttemptAt = now, now.Add(time.Minute)
		}, SyncHealthStalled},
		{"new running lease cannot reset stale progress", func(b *app.EmailMailbox, j *app.EmailJob) {
			b.PollThrough = now.Add(-time.Hour)
			j.State = app.EmailJobRunning
			j.RoundStartedAt, j.LeaseExpiresAt = now, now.Add(3*time.Minute)
		}, SyncHealthStalled},
		{"quiet polling within budget", func(b *app.EmailMailbox, j *app.EmailJob) {
			b.PollThrough = now.Add(-15 * time.Minute)
			b.WakeAdapterQualified, b.WatchState = true, "watching"
		}, SyncHealthHealthy},
		{"quiet polling overdue", func(b *app.EmailMailbox, j *app.EmailJob) {
			b.PollThrough = now.Add(-17 * time.Minute)
			b.WakeAdapterQualified, b.WatchState = true, "watching"
		}, SyncHealthStalled},
		{"scheduled longer cadence", func(b *app.EmailMailbox, j *app.EmailJob) {
			b.PollThrough = now.Add(-20 * time.Minute)
			j.PollInterval = 10 * time.Minute
		}, SyncHealthHealthy},
		{"intake disabled", func(b *app.EmailMailbox, j *app.EmailJob) {
			b.IntakeEnabled = false
			b.PollThrough = now.Add(-time.Hour)
		}, SyncHealthPaused},
		{"inactive binding", func(b *app.EmailMailbox, j *app.EmailJob) { b.Active = false }, SyncHealthPaused},
		{"explicit job pause", func(b *app.EmailMailbox, j *app.EmailJob) {
			j.State = app.EmailJobPaused
			b.PollThrough = now.Add(-time.Hour)
		}, SyncHealthPaused},
		{"overflow has own diagnostic", func(b *app.EmailMailbox, j *app.EmailJob) {
			b.SyncState = app.EmailSyncOverflowConfirmation
			b.PollThrough = now.Add(-time.Hour)
		}, SyncHealthAttention},
		{"unqualified provider", func(b *app.EmailMailbox, j *app.EmailJob) { b.Provider = app.EmailProviderQQMail }, SyncHealthAttention},
		{"new enable keeps historical boundary", func(b *app.EmailMailbox, j *app.EmailJob) { b.IntakeEnabledAt = now; b.PollThrough = b.ActivatedAt }, SyncHealthStarting},
		{"first check overdue", func(b *app.EmailMailbox, j *app.EmailJob) { b.PollThrough = b.ActivatedAt }, SyncHealthStalled},
		{"legacy row falls back to initial job", func(b *app.EmailMailbox, j *app.EmailJob) {
			b.IntakeEnabledAt = time.Time{}
			b.PollThrough = b.ActivatedAt
			j.CreatedAt = now.Add(-time.Minute)
		}, SyncHealthStarting},
		{"legacy old job still stalls despite updates", func(b *app.EmailMailbox, j *app.EmailJob) {
			b.IntakeEnabledAt = time.Time{}
			b.PollThrough = b.ActivatedAt
			j.CreatedAt = now.Add(-time.Hour)
			j.UpdatedAt = now
		}, SyncHealthStalled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			box, job := base, app.EmailJob{State: app.EmailJobQueued, CreatedAt: now.Add(-time.Hour)}
			tc.change(&box, &job)
			got := s.mailboxSyncHealth(box, &job)
			if got.State != tc.want {
				t.Fatalf("health=%+v want=%s", got, tc.want)
			}
			if tc.want == SyncHealthStalled && got.Code != "email_sync_stalled" {
				t.Fatal(got)
			}
		})
	}
	if got := s.mailboxSyncHealth(base, nil); got.StallAfterSeconds != 15*60 {
		t.Fatal(got)
	}
	base.WakeAdapterQualified, base.WatchState = true, "watching"
	if got := s.mailboxSyncHealth(base, nil); got.StallAfterSeconds != 16*60 || got.ExpectedIntervalSeconds != 5*60 {
		t.Fatal(got)
	}
	base.IntakeEnabledAt, base.PollThrough = time.Time{}, time.Time{}
	if got := s.mailboxSyncHealth(base, nil); got.State != SyncHealthStalled {
		t.Fatal("legacy missing job uses durable activation", got)
	}
}

func TestSyncHealthEmptyCollectionPersistsOnDefaultFileStore(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	repo, err := store.NewFileStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	s, browser, _ := newFixtureService(t, repo)
	s.browser = &emptyPageFixture{intakeFixture: browser}
	owner := "email-owner"
	before, err := s.ProviderSyncHealth(t.Context(), owner, app.EmailProviderGmail)
	if err != nil || before.State != SyncHealthNotEnabled {
		t.Fatalf("health=%+v err=%v", before, err)
	}
	box, err := repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: command(owner, "bind-health"), Provider: app.EmailProviderGmail, Address: "owner@example.com", Enabled: true, Boundary: time.Now().Add(-24 * time.Hour)})
	if err != nil || box.IntakeEnabledAt.IsZero() {
		t.Fatalf("box=%+v err=%v", box, err)
	}
	start := box.IntakeEnabledAt
	health, err := s.ProviderSyncHealth(t.Context(), owner, app.EmailProviderGmail)
	if err != nil || health.State != SyncHealthStarting {
		t.Fatalf("health=%+v err=%v", health, err)
	}
	if err := s.plan(t.Context()); err != nil {
		t.Fatal(err)
	}
	// A synthetic current clock is used only by the service; no database clock
	// mutation, real mail, account probe or browser effect is involved.
	now := time.Now().UTC().Add(time.Second)
	s.now = func() time.Time { return now }
	worked, err := s.workOne(t.Context(), []string{app.EmailJobDiscover})
	if err != nil || !worked {
		t.Fatalf("worked=%t err=%v", worked, err)
	}
	health, err = s.ProviderSyncHealth(t.Context(), owner, app.EmailProviderGmail)
	if err != nil || health.State != SyncHealthHealthy || health.PollThrough == nil {
		t.Fatalf("health=%+v err=%v", health, err)
	}
	foreign, err := s.ProviderSyncHealth(t.Context(), "other-owner", app.EmailProviderGmail)
	if err != nil || foreign.State != SyncHealthNotEnabled || foreign.PollThrough != nil {
		t.Fatalf("foreign owner health=%+v err=%v", foreign, err)
	}
	jobs, err := repo.ListEmailJobs(t.Context(), store.EmailQuery{OwnerID: owner, MailboxID: box.ID, JobKind: app.EmailJobDiscover})
	if err != nil || len(jobs) != 1 || jobs[0].Kind != app.EmailJobDiscover {
		t.Fatalf("jobs=%+v err=%v", jobs, err)
	}
	otherJobs, err := repo.ListEmailJobs(t.Context(), store.EmailQuery{OwnerID: owner, MailboxID: box.ID, JobKind: app.EmailJobParse})
	if err != nil || len(otherJobs) != 0 {
		t.Fatalf("job kind filter leaked discover jobs: %+v %v", otherJobs, err)
	}
	reopened, err := store.NewFileStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	s.repository = reopened
	saved, _, err := reopened.GetEmailMailbox(t.Context(), owner, box.ID)
	if err != nil || !saved.IntakeEnabledAt.Equal(start) {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}
	original, _ := json.Marshal(saved)
	now = now.Add(time.Hour)
	health, err = s.ProviderSyncHealth(t.Context(), owner, app.EmailProviderGmail)
	if err != nil || health.State != SyncHealthStalled {
		t.Fatalf("health=%+v err=%v", health, err)
	}
	unchanged, _, _ := reopened.GetEmailMailbox(t.Context(), owner, box.ID)
	after, _ := json.Marshal(unchanged)
	if string(original) != string(after) {
		t.Fatal("health check changed mailbox")
	}
	paused, err := reopened.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: command(owner, "pause-health"), Provider: box.Provider, Address: box.Address, ExpectedVersion: saved.Version, Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	health, err = s.ProviderSyncHealth(t.Context(), owner, app.EmailProviderGmail)
	if err != nil || health.State != SyncHealthPaused {
		t.Fatalf("health=%+v err=%v", health, err)
	}
	resumed, err := reopened.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: command(owner, "resume-health"), Provider: box.Provider, Address: box.Address, ExpectedVersion: paused.Version, Enabled: true})
	if err != nil || !resumed.IntakeEnabledAt.After(start) || !resumed.PollThrough.Equal(saved.PollThrough) {
		t.Fatalf("resume=%+v err=%v", resumed, err)
	}
	now = resumed.IntakeEnabledAt
	health, err = s.ProviderSyncHealth(t.Context(), owner, app.EmailProviderGmail)
	if err != nil || health.State == SyncHealthStalled {
		t.Fatalf("health=%+v err=%v", health, err)
	}
	// A pre-upgrade File snapshot has no receive-enabled timestamp. Its
	// original watermark still detects stale sync; a check must not backfill
	// it with today's time and conceal a historical outage.
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot store.Snapshot
	if err = json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	for key, record := range snapshot.EmailRecords {
		if record.Kind != "mailbox" {
			continue
		}
		var fields map[string]json.RawMessage
		if err = json.Unmarshal(record.Data, &fields); err != nil {
			t.Fatal(err)
		}
		delete(fields, "intake_enabled_at")
		record.Data, err = json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		snapshot.EmailRecords[key] = record
	}
	raw, err = json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(statePath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	legacy, err := store.NewFileStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	s.repository = legacy
	now = now.Add(time.Hour)
	health, err = s.ProviderSyncHealth(t.Context(), owner, app.EmailProviderGmail)
	if err != nil || health.State != SyncHealthStalled {
		t.Fatalf("legacy health=%+v err=%v", health, err)
	}
}
