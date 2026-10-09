package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestOwnerPreconditionMemoryAndFile(t *testing.T) {
	for _, backend := range []string{"memory", "file"} {
		t.Run(backend, func(t *testing.T) {
			var repo testBackend = NewMemoryStore()
			if backend == "file" {
				var err error
				repo, err = NewFileStore(filepath.Join(t.TempDir(), "state.json"))
				if err != nil {
					t.Fatal(err)
				}
			}
			ctx := t.Context()
			initial, err := repo.SaveOwnerProfile(WithOwnerProfilePrecondition(ctx, false, time.Time{}), app.OwnerProfile{ID: "conditional", DisplayName: "initial"})
			if err != nil {
				t.Fatal(err)
			}
			stale := WithOwnerProfilePrecondition(ctx, true, initial.UpdatedAt)
			updated, err := repo.SaveOwnerProfile(stale, app.OwnerProfile{ID: initial.ID, DisplayName: "winner"})
			if err != nil {
				t.Fatal(err)
			}
			events := len(mustEventsAfter(t, repo, "", ""))
			if _, err = repo.SaveOwnerProfile(stale, app.OwnerProfile{ID: initial.ID, DisplayName: "stale"}); !errors.Is(err, ErrOwnerProfileConflict) {
				t.Fatalf("stale err=%v", err)
			}
			got, _, err := repo.GetOwnerProfileByID(ctx, initial.ID)
			if err != nil || !OwnerProfilesEqual(got, updated) || len(mustEventsAfter(t, repo, "", "")) != events {
				t.Fatalf("conditional write changed committed state: %+v %v", got, err)
			}
			if _, err = repo.SaveOwnerProfile(WithOwnerProfilePrecondition(ctx, false, time.Time{}), updated); !errors.Is(err, ErrOwnerProfileConflict) {
				t.Fatalf("duplicate create err=%v", err)
			}
		})
	}
}
func TestOwnerPreconditionPostgresInsideAdvisoryTransaction(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	current := testPostgresOwnerProfile("conditional", now.Add(-time.Hour), now)
	for _, match := range []bool{false, true} {
		tx := &fakeOnboardingPostgresTx{row: ownerPostgresRow(current)}
		repo, _, _ := newFakePostgresOwnerStore(tx)
		expected := now
		if !match {
			expected = now.Add(-time.Second)
		}
		_, err := repo.SaveOwnerProfile(WithOwnerProfilePrecondition(context.Background(), true, expected), current)
		if match {
			if err != nil || tx.commits != 1 {
				t.Fatalf("match: %v", err)
			}
		} else if !errors.Is(err, ErrOwnerProfileConflict) || tx.rollbacks != 1 || len(tx.execSQL) != 1 {
			t.Fatalf("conflict changed DB: err=%v writes=%v", err, tx.execSQL)
		}
	}
}
