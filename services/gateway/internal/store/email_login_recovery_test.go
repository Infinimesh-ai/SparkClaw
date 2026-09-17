package store

import (
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"testing"
	"time"
)

func TestEmailLoginExpiryRetainsBoundaryBeforeRetriesExhaust(t *testing.T) {
	repo := NewMemoryStore()
	f := emailFixture(t, repo)
	m := f.admit("pending", time.Now())
	j := f.claim(app.EmailJobCapture)
	_, err := repo.FinishEmailJob(t.Context(), EmailJobFinish{EmailJobLease: f.lease(j), ErrorCode: string(app.ToolErrorEmailLoginRequired), RetryAt: time.Now().Add(time.Minute)})
	f.must(err)
	box, found, err := repo.GetEmailMailbox(t.Context(), f.owner, f.box.ID)
	f.must(err)
	if !found || box.ErrorCode != string(app.ToolErrorEmailLoginRequired) || !box.IntakeEnabled || !box.Boundary.Equal(f.box.Boundary) {
		t.Fatalf("lost recovery state: %+v", box)
	}
	_, err = repo.RequestEmailJob(t.Context(), EmailJobRequest{EmailCommand: f.command(), Kind: app.EmailJobCapture, TargetID: m.ID, MailboxID: box.ID, BindingGeneration: box.BindingGeneration, Rearm: true})
	f.must(err)
	_, claimed, err := repo.ClaimEmailJob(t.Context(), EmailJobClaim{OwnerID: f.owner, Kinds: []string{app.EmailJobCapture}, Now: time.Now().Add(2 * time.Minute), LeaseDuration: time.Minute})
	f.must(err)
	if claimed {
		t.Fatal("capture ran while login was expired")
	}
	recovered, err := repo.BindEmailMailbox(t.Context(), EmailBindCommand{EmailCommand: f.command(), Provider: box.Provider, Address: box.Address, Enabled: true, ExpectedVersion: box.Version, Boundary: time.Now().Add(time.Hour)})
	f.must(err)
	if recovered.ErrorCode != "" || !recovered.Boundary.Equal(box.Boundary) || !recovered.ActivatedAt.Equal(box.ActivatedAt) {
		t.Fatalf("recovery reset progress: %+v", recovered)
	}
}

func TestEmailDeploymentBoundaryIgnoresOlderMailboxHistory(t *testing.T) {
	repo := NewMemoryStore()
	f := emailFixture(t, repo)
	// Upgraded installations can have mailbox rows without the newer owner-level
	// deployment counter. The recorded deployment boundary, not that history,
	// establishes the clean cutover.
	repo.mu.Lock()
	delete(repo.emailRecords, emailRecordKey(f.owner, "counter", "email_deployment_boundary"))
	repo.mu.Unlock()
	want := time.Now().Add(time.Hour)
	later, err := repo.BindEmailMailbox(t.Context(), EmailBindCommand{EmailCommand: f.command(), Provider: app.EmailProviderOutlook, Address: "other@example.test", Enabled: true, Boundary: want})
	f.must(err)
	if !later.ActivatedAt.Equal(postgresTime(want)) {
		t.Fatalf("historical mailbox overrode deployment boundary: %v != %v", later.ActivatedAt, postgresTime(want))
	}
}
