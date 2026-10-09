package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
)

func TestEventWindowMemoryFileBoundaries(t *testing.T) {
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
			reader := repo.(EventWindowReader)
			for i := 0; i < 3; i++ {
				if _, err := repo.SaveOwnerProfile(t.Context(), app.OwnerProfile{ID: fmt.Sprintf("owner-%d", i)}); err != nil {
					t.Fatal(err)
				}
			}
			all, _ := repo.EventsAfter(t.Context(), "", "")
			tail, err := reader.ReadEventWindow(t.Context(), "", 1, true)
			if err != nil || tail.Cursor != all[len(all)-1].ID || len(tail.Events) != 0 {
				t.Fatal(tail, err)
			}
			first, err := reader.ReadEventWindow(t.Context(), "", 1, false)
			if err != nil || len(first.Events) != 1 || !first.More || !first.CursorFound {
				t.Fatal(first, err)
			}
			next, err := reader.ReadEventWindow(t.Context(), first.Cursor, 1, false)
			if err != nil || len(next.Events) != 1 || next.Events[0].ID == first.Cursor {
				t.Fatal(next, err)
			}
			gap, err := reader.ReadEventWindow(t.Context(), "gone", 1, false)
			if err != nil || gap.CursorFound || len(gap.Events) != 0 {
				t.Fatal(gap, err)
			}
			if _, err = reader.ReadEventWindow(t.Context(), "", EventWindowLimit+1, false); err == nil {
				t.Fatal("unbounded window")
			}
		})
	}
}

type eventWindowPostgresOps struct {
	fakeAuditPostgresOps
	sql  string
	args []any
}

func (o *eventWindowPostgresOps) Query(ctx context.Context, sql string, args ...any) (onboardingPostgresRows, error) {
	o.sql = sql
	o.args = args
	return o.fakeAuditPostgresOps.Query(ctx, sql, args...)
}
func TestEventWindowPostgresBoundAndMissingCursor(t *testing.T) {
	ops := &eventWindowPostgresOps{fakeAuditPostgresOps: fakeAuditPostgresOps{row: fakeAuditPostgresRow{seq: 4}, rows: &fakeAuditPostgresRows{rows: []fakeAuditPostgresRow{{event: app.Event{ID: "a"}, payload: []byte(`{}`)}, {event: app.Event{ID: "b"}, payload: []byte(`{}`)}}}}}
	repo := &PostgresStore{operationTimeouts: defaultOperationTimeouts, auditPostgres: ops}
	window, err := repo.ReadEventWindow(t.Context(), "cursor", 1, false)
	if err != nil || len(window.Events) != 1 || !window.More || !window.CursorFound || !strings.Contains(ops.sql, "LIMIT $2") || ops.args[1] != 2 {
		t.Fatal(window, err, ops.sql, ops.args)
	}
	ops.row = fakeAuditPostgresRow{err: pgx.ErrNoRows}
	gap, err := repo.ReadEventWindow(t.Context(), "deleted", 1, false)
	if err != nil || gap.CursorFound {
		t.Fatal(gap, err)
	}
	ops.row = fakeAuditPostgresRow{event: app.Event{ID: "latest"}, payload: []byte(`{}`)}
	tail, err := repo.ReadEventWindow(t.Context(), "", 1, true)
	if err != nil || tail.Cursor != "latest" || len(tail.Events) != 0 {
		t.Fatal(tail, err)
	}
}
