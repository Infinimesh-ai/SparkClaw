package emailmanagement

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type blockedIntakeDiscovery struct {
	Browser
	started, release chan struct{}
}

func (b *blockedIntakeDiscovery) DiscoverForOwner(ctx context.Context, owner string, r app.EmailReadRequest) (app.EmailDiscoveryResult, error) {
	close(b.started)
	<-b.release
	return b.Browser.DiscoverForOwner(ctx, owner, r)
}
func TestIntakeRecoveryFencesLiveDiscoveryAndEnablesWithoutMailbox(t *testing.T) {
	for _, file := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "file"}[file], func(t *testing.T) {
			var repo Repository = store.NewMemoryStore()
			if file {
				s, err := store.NewFileStore(filepath.Join(t.TempDir(), "state.json"))
				if err != nil {
					t.Fatal(err)
				}
				repo = s
			}
			service, browser, _ := newFixtureService(t, repo)
			gate := &blockedIntakeDiscovery{Browser: browser, started: make(chan struct{}), release: make(chan struct{})}
			service.browser = gate
			original := store.EmailIntakeOperation{Key: strings.Repeat("a", 64), Digest: strings.Repeat("b", 64), Provider: app.EmailProviderGmail, Enabled: true}
			done := make(chan error, 1)
			go func() { _, err := service.ConfigureOperation(t.Context(), "email-owner", original); done <- err }()
			<-gate.started
			resolved, err := service.ResolveIntake(t.Context(), "email-owner", original)
			if err != nil || resolved.State != store.EmailIntakeNotApplied {
				t.Fatal(resolved, err)
			}
			close(gate.release)
			if err = <-done; store.StoreErrorCodeOf(err) != store.StoreErrorConflict {
				t.Fatal("late discovery committed", err)
			}
			status, err := service.Status(t.Context(), "email-owner")
			if err != nil || len(status.Mailboxes) != 0 || status.ProviderVersions[app.EmailProviderGmail] != 1 {
				t.Fatal(status, err)
			}
			service.browser = browser
			fresh := original
			fresh.Key = strings.Repeat("c", 64)
			fresh.ExpectedVersion = 1
			box, err := service.ConfigureOperation(t.Context(), "email-owner", fresh)
			if err != nil || !box.IntakeEnabled || box.Version != 2 {
				t.Fatal("new explicit intake cannot use empty binding version", box, err)
			}
		})
	}
}
