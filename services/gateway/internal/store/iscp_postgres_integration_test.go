package store

import (
	"errors"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestPostgresISCPCASAndDurableEventWindow(t *testing.T) {
	dsn := newPostgresMigrationTestSchema(t)
	repo, err := NewPostgresStore(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(repo.Close)
	created, err := repo.SaveOwnerProfile(WithOwnerProfilePrecondition(t.Context(), false, time.Time{}), app.OwnerProfile{ID: "iscp-owner", DisplayName: "initial"})
	if err != nil {
		t.Fatal(err)
	}
	changed := created
	changed.DisplayName = "committed"
	if _, err = repo.SaveOwnerProfile(WithOwnerProfilePrecondition(t.Context(), true, created.UpdatedAt), changed); err != nil {
		t.Fatal(err)
	}
	changed.DisplayName = "stale"
	if _, err = repo.SaveOwnerProfile(WithOwnerProfilePrecondition(t.Context(), true, created.UpdatedAt), changed); !errors.Is(err, ErrOwnerProfileConflict) {
		t.Fatalf("stale CAS: %v", err)
	}
	first, err := repo.ReadEventWindow(t.Context(), "", 1, false)
	if err != nil || !first.CursorFound || !first.More || len(first.Events) != 1 {
		t.Fatalf("bounded first window: %+v %v", first, err)
	}
	next, err := repo.ReadEventWindow(t.Context(), first.Cursor, 1, false)
	if err != nil || !next.CursorFound || len(next.Events) != 1 || next.Events[0].ID == first.Cursor {
		t.Fatalf("ordered next window: %+v %v", next, err)
	}
	tail, err := repo.ReadEventWindow(t.Context(), "", 1, true)
	if err != nil || tail.Cursor == "" || len(tail.Events) != 0 {
		t.Fatalf("bounded snapshot boundary: %+v %v", tail, err)
	}
	gap, err := repo.ReadEventWindow(t.Context(), "retained-event-was-deleted", 1, false)
	if err != nil || gap.CursorFound || len(gap.Events) != 0 {
		t.Fatalf("missing cursor must require snapshot: %+v %v", gap, err)
	}
	reopened, err := NewPostgresStore(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	durable, err := reopened.ReadEventWindow(t.Context(), "", 1, true)
	if err != nil || durable.Cursor != tail.Cursor {
		t.Fatalf("event boundary changed on reopen: %+v %v", durable, err)
	}
}
