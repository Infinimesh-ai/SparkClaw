package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestPassiveNotificationRepositoryMemoryAndFileContract(t *testing.T) {
	for _, backend := range []string{"memory", "file"} {
		t.Run(backend, func(t *testing.T) {
			var repository Store
			var restart func() Store
			switch backend {
			case "memory":
				repository = NewMemoryStore()
			case "file":
				path := filepath.Join(t.TempDir(), "state.json")
				file, err := NewFileStore(path)
				if err != nil {
					t.Fatal(err)
				}
				repository = file
				restart = func() Store {
					reloaded, err := NewFileStore(path)
					if err != nil {
						t.Fatal(err)
					}
					return reloaded
				}
			}
			exercisePassiveNotificationRepositoryContract(t, repository, restart)
		})
	}
}

func TestPostgresPassiveNotificationRepositoryConfiguredContract(t *testing.T) {
	dsn := os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set SPARKCLAW_TEST_POSTGRES_DSN to run postgres store integration tests")
	}
	repository, err := NewPostgresStore(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	truncatePostgresStore(t, repository)
	exercisePassiveNotificationRepositoryContract(t, repository, nil)
}

func exercisePassiveNotificationRepositoryContract(t *testing.T, repository Store, restart func() Store) {
	t.Helper()
	base := time.Date(2026, 8, 21, 14, 30, 0, 123456789, time.FixedZone("contract", 8*60*60))
	owner := "owner-passive-contract"
	firstInput := passiveNotificationContractFixture(owner, "passive-a", "endpoint-contract", "key-a", "fingerprint-a", base)
	first, inserted, err := repository.CreatePassiveNotification(t.Context(), firstInput)
	if err != nil || !inserted {
		t.Fatalf("create first=%#v inserted=%t err=%v", first, inserted, err)
	}
	if first.OccurredAt.Location() != time.UTC || first.OccurredAt.Nanosecond() != 123456000 ||
		first.CreatedAt.Location() != time.UTC || first.CreatedAt.Nanosecond() != 123456000 ||
		first.UpdatedAt.Location() != time.UTC || first.UpdatedAt.Nanosecond()%1000 != 0 {
		t.Fatalf("normalized notification=%#v", first)
	}
	if missing, found := mustGetPassiveNotification(t, repository, owner, "missing"); found || missing.ID != "" {
		t.Fatalf("missing notification=%#v found=%t", missing, found)
	}
	if foreign, found := mustGetPassiveNotification(t, repository, "other-owner", first.ID); found || foreign.ID != "" {
		t.Fatalf("foreign notification=%#v found=%t", foreign, found)
	}

	second := mustCreatePassiveNotification(t, repository, passiveNotificationContractFixture(owner, "passive-b", "endpoint-contract", "key-b", "fingerprint-b", base))
	third := mustCreatePassiveNotification(t, repository, passiveNotificationContractFixture(owner, "passive-c", "endpoint-contract", "key-c", "fingerprint-c", base.Add(time.Second)))
	newest := mustListPassiveNotifications(t, repository, owner, "", 2)
	if len(newest) != 2 || newest[0].ID != third.ID || newest[1].ID != second.ID {
		t.Fatalf("newest order/limit=%#v", newest)
	}
	afterFirst := mustListPassiveNotifications(t, repository, owner, first.ID, 10)
	if len(afterFirst) != 2 || afterFirst[0].ID != second.ID || afterFirst[1].ID != third.ID {
		t.Fatalf("cursor order=%#v", afterFirst)
	}
	if missingCursor := mustListPassiveNotifications(t, repository, owner, "missing", 10); missingCursor == nil || len(missingCursor) != 0 {
		t.Fatalf("missing cursor result=%#v", missingCursor)
	}
	other := mustCreatePassiveNotification(t, repository, passiveNotificationContractFixture("other-owner", "passive-other", "endpoint-other", "key-other", "fingerprint-other", base))
	if scoped := mustListPassiveNotifications(t, repository, owner, "", 100); len(scoped) != 3 {
		t.Fatalf("owner-scoped list=%#v", scoped)
	}

	replay := firstInput
	replay.ID = "passive-replay-id-is-ignored"
	existing, inserted, err := repository.CreatePassiveNotification(t.Context(), replay)
	if err != nil || inserted || existing.ID != first.ID {
		t.Fatalf("idempotent replay=%#v inserted=%t err=%v", existing, inserted, err)
	}
	conflict := replay
	conflict.Fingerprint = "changed"
	if _, _, err := repository.CreatePassiveNotification(t.Context(), conflict); !errors.Is(err, ErrPassiveNotificationConflict) || StoreErrorCodeOf(err) != StoreErrorConflict {
		t.Fatalf("fingerprint conflict=%v code=%q", err, StoreErrorCodeOf(err))
	}
	duplicateID := passiveNotificationContractFixture(owner, first.ID, "endpoint-contract", "different-key", "different-fingerprint", base)
	if _, _, err := repository.CreatePassiveNotification(t.Context(), duplicateID); !errors.Is(err, ErrPassiveNotificationConflict) || StoreErrorCodeOf(err) != StoreErrorConflict {
		t.Fatalf("duplicate ID conflict=%v code=%q", err, StoreErrorCodeOf(err))
	}
	invalid := firstInput
	invalid.EndpointID = ""
	if _, _, err := repository.CreatePassiveNotification(t.Context(), invalid); StoreErrorCodeOf(err) != StoreErrorInvalid {
		t.Fatalf("invalid create=%v code=%q", err, StoreErrorCodeOf(err))
	}

	beforeRead := mustPassiveNotificationRevision(t, repository, owner)
	readAt := base.Add(2*time.Hour + 987*time.Nanosecond)
	read, err := repository.MarkPassiveNotificationRead(t.Context(), owner, first.ID, readAt)
	if err != nil || read.ReadAt == nil || read.ReadAt.Location() != time.UTC || read.ReadAt.Nanosecond()%1000 != 0 {
		t.Fatalf("mark read=%#v err=%v", read, err)
	}
	storedReadAt := *read.ReadAt
	*read.ReadAt = read.ReadAt.Add(time.Hour)
	isolation, found := mustGetPassiveNotification(t, repository, owner, first.ID)
	if !found || isolation.ReadAt == nil || !isolation.ReadAt.Equal(storedReadAt) {
		t.Fatalf("read pointer alias escaped=%#v found=%t", isolation, found)
	}
	afterRead := mustPassiveNotificationRevision(t, repository, owner)
	if afterRead == beforeRead {
		t.Fatal("mark-read did not change revision")
	}
	again, err := repository.MarkPassiveNotificationRead(t.Context(), owner, first.ID, readAt.Add(time.Hour))
	if err != nil || again.ReadAt == nil || !again.ReadAt.Equal(storedReadAt) || mustPassiveNotificationRevision(t, repository, owner) != afterRead {
		t.Fatalf("idempotent mark-read=%#v err=%v", again, err)
	}
	if _, err := repository.MarkPassiveNotificationRead(t.Context(), owner, "missing", readAt); !errors.Is(err, ErrPassiveNotificationNotFound) || StoreErrorCodeOf(err) != StoreErrorNotFound {
		t.Fatalf("missing mark-read=%v code=%q", err, StoreErrorCodeOf(err))
	}
	if unread := mustCountUnreadPassiveNotifications(t, repository, owner); unread != 2 {
		t.Fatalf("unread before mark-all=%d", unread)
	}
	beforeMarkAll := mustPassiveNotificationRevision(t, repository, owner)
	count, err := repository.MarkAllPassiveNotificationsRead(t.Context(), owner, readAt)
	if err != nil || count != 2 || mustCountUnreadPassiveNotifications(t, repository, owner) != 0 || mustCountUnreadPassiveNotifications(t, repository, other.OwnerID) != 1 {
		t.Fatalf("mark-all count=%d err=%v", count, err)
	}
	afterMarkAll := mustPassiveNotificationRevision(t, repository, owner)
	if afterMarkAll == beforeMarkAll {
		t.Fatal("mark-all did not change revision")
	}
	count, err = repository.MarkAllPassiveNotificationsRead(t.Context(), owner, readAt.Add(time.Hour))
	if err != nil || count != 0 || mustPassiveNotificationRevision(t, repository, owner) != afterMarkAll {
		t.Fatalf("no-op mark-all count=%d err=%v", count, err)
	}

	pruneOwner := "owner-passive-prune"
	old := passiveNotificationContractFixture(pruneOwner, "passive-old", "endpoint-prune", "key-old", "fingerprint-old", base.Add(-24*time.Hour))
	mustCreatePassiveNotification(t, repository, old)
	if removed := mustPrunePassiveNotifications(t, repository, base.Add(-time.Hour), 0); removed != 1 {
		t.Fatalf("retention prune removed=%d", removed)
	}
	if _, inserted, err := repository.CreatePassiveNotification(t.Context(), old); err != nil || !inserted {
		t.Fatalf("pruned key replay inserted=%t err=%v", inserted, err)
	}
	for index, id := range []string{"read-old", "read-new", "unread-old", "unread-new"} {
		notification := passiveNotificationContractFixture(pruneOwner, id, "endpoint-prune", "key-"+id, "fingerprint-"+id, base.Add(time.Duration(index)*time.Minute))
		mustCreatePassiveNotification(t, repository, notification)
	}
	for _, id := range []string{"passive-old", "read-old", "read-new"} {
		if _, err := repository.MarkPassiveNotificationRead(t.Context(), pruneOwner, id, readAt); err != nil {
			t.Fatal(err)
		}
	}
	if removed := mustPrunePassiveNotifications(t, repository, time.Time{}, 3); removed != 2 {
		t.Fatalf("cap prune removed=%d", removed)
	}
	survivors := mustListPassiveNotifications(t, repository, pruneOwner, "", 10)
	if len(survivors) != 3 || survivors[0].ID != "unread-new" || survivors[1].ID != "unread-old" || survivors[2].ID != "read-new" {
		t.Fatalf("cap survivors=%#v", survivors)
	}

	auditTypes := map[string]bool{}
	for _, event := range mustListAudit(t, repository, "") {
		auditTypes[event.Type] = true
	}
	for _, required := range []string{"notification.received", "notification.pruned"} {
		if !auditTypes[required] {
			t.Fatalf("missing lifecycle audit %q in %#v", required, auditTypes)
		}
	}
	assertCanceledPassiveNotificationOperations(t, repository, first)
	if restart != nil {
		reloaded := restart()
		persisted, found := mustGetPassiveNotification(t, reloaded, owner, first.ID)
		if !found || persisted.ReadAt == nil || !persisted.ReadAt.Equal(storedReadAt) {
			t.Fatalf("restarted notification=%#v found=%t", persisted, found)
		}
		if replayed, inserted, err := reloaded.CreatePassiveNotification(t.Context(), firstInput); err != nil || inserted || replayed.ID != first.ID {
			t.Fatalf("restart replay=%#v inserted=%t err=%v", replayed, inserted, err)
		}
	}
}

func passiveNotificationContractFixture(ownerID, id, endpointID, key, fingerprint string, createdAt time.Time) app.PassiveNotification {
	return app.PassiveNotification{
		ID: id, OwnerID: ownerID, EndpointID: endpointID, IdempotencyKey: key, Fingerprint: fingerprint,
		NotificationID: "upstream-" + id, Source: "localmind", Kind: app.PassiveNotificationKindDocumentMention,
		DeepLink: "https://localmind.example/workspace/" + id, OccurredAt: createdAt, CreatedAt: createdAt,
	}
}

func mustCreatePassiveNotification(t testing.TB, repository PassiveNotificationRepository, notification app.PassiveNotification) app.PassiveNotification {
	t.Helper()
	created, inserted, err := repository.CreatePassiveNotification(t.Context(), notification)
	if err != nil || !inserted {
		t.Fatalf("create %s inserted=%t err=%v", notification.ID, inserted, err)
	}
	return created
}

func assertCanceledPassiveNotificationOperations(t *testing.T, repository PassiveNotificationRepository, notification app.PassiveNotification) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	checks := []struct {
		name string
		call func() error
	}{
		{name: "create", call: func() error { _, _, err := repository.CreatePassiveNotification(ctx, notification); return err }},
		{name: "get", call: func() error {
			_, _, err := repository.GetPassiveNotification(ctx, notification.OwnerID, notification.ID)
			return err
		}},
		{name: "list", call: func() error {
			_, err := repository.ListPassiveNotifications(ctx, notification.OwnerID, "", 10)
			return err
		}},
		{name: "count", call: func() error {
			_, err := repository.CountUnreadPassiveNotifications(ctx, notification.OwnerID)
			return err
		}},
		{name: "mark read", call: func() error {
			_, err := repository.MarkPassiveNotificationRead(ctx, notification.OwnerID, notification.ID, time.Now())
			return err
		}},
		{name: "mark all", call: func() error {
			_, err := repository.MarkAllPassiveNotificationsRead(ctx, notification.OwnerID, time.Now())
			return err
		}},
		{name: "prune", call: func() error { _, err := repository.PrunePassiveNotifications(ctx, time.Now(), 1); return err }},
		{name: "revision", call: func() error { _, err := repository.PassiveNotificationRevision(ctx, notification.OwnerID); return err }},
	}
	for _, check := range checks {
		t.Run("canceled "+check.name, func(t *testing.T) {
			if err := check.call(); StoreErrorCodeOf(err) != StoreErrorCanceled {
				t.Fatalf("error=%v code=%q", err, StoreErrorCodeOf(err))
			}
		})
	}
}

func TestFilePassiveNotificationRepositoryDefiniteFailuresRestoreAggregate(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, *FileStore) func() error
	}{
		{name: "create", setup: func(t *testing.T, repository *FileStore) func() error {
			return func() error {
				_, _, err := repository.CreatePassiveNotification(t.Context(), passiveNotificationContractFixture("owner-file", "file-create", "endpoint-file", "key-create", "fingerprint-create", time.Now()))
				return err
			}
		}},
		{name: "mark read", setup: func(t *testing.T, repository *FileStore) func() error {
			stored := mustCreatePassiveNotification(t, repository, passiveNotificationContractFixture("owner-file", "file-read", "endpoint-file", "key-read", "fingerprint-read", time.Now()))
			return func() error {
				_, err := repository.MarkPassiveNotificationRead(t.Context(), stored.OwnerID, stored.ID, time.Now())
				return err
			}
		}},
		{name: "mark all", setup: func(t *testing.T, repository *FileStore) func() error {
			stored := mustCreatePassiveNotification(t, repository, passiveNotificationContractFixture("owner-file", "file-all", "endpoint-file", "key-all", "fingerprint-all", time.Now()))
			return func() error {
				_, err := repository.MarkAllPassiveNotificationsRead(t.Context(), stored.OwnerID, time.Now())
				return err
			}
		}},
		{name: "prune", setup: func(t *testing.T, repository *FileStore) func() error {
			mustCreatePassiveNotification(t, repository, passiveNotificationContractFixture("owner-file", "file-prune", "endpoint-file", "key-prune", "fingerprint-prune", time.Now()))
			return func() error {
				_, err := repository.PrunePassiveNotifications(t.Context(), time.Now().Add(time.Hour), 0)
				return err
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			command := test.setup(t, repository)
			before := repository.captureFileRollback()
			repository.commitOps = &controlledFileCommitOps{failStage: "encode", failRemaining: 1}
			if err := command(); StoreErrorCodeOf(err) != StoreErrorDurability || !errorsIsFileCommitInjected(err) {
				t.Fatalf("error=%v code=%q", err, StoreErrorCodeOf(err))
			}
			if after := repository.captureFileRollback(); !reflect.DeepEqual(after, before) {
				t.Fatal("failed passive notification command retained record, index, audit, or revision state")
			}
		})
	}
}

type fakePassiveNotificationPostgresOps struct {
	*fakeConnectorPostgresOps
	rowQueue  []onboardingPostgresRow
	rowSQL    []string
	execSQL   []string
	execTags  map[int]pgconn.CommandTag
	execError map[int]error
}

func (o *fakePassiveNotificationPostgresOps) QueryRow(_ context.Context, sql string, _ ...any) onboardingPostgresRow {
	o.rowSQL = append(o.rowSQL, sql)
	if len(o.rowQueue) == 0 {
		return fakeConnectorPostgresRow{}
	}
	row := o.rowQueue[0]
	o.rowQueue = o.rowQueue[1:]
	return row
}

func (o *fakePassiveNotificationPostgresOps) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	index := len(o.execSQL)
	o.execSQL = append(o.execSQL, sql)
	if err := o.execError[index]; err != nil {
		return pgconn.CommandTag{}, err
	}
	if tag, ok := o.execTags[index]; ok {
		return tag, nil
	}
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func newFakePassiveNotificationPostgresStore(transaction *fakeConnectorPostgresTx) (*PostgresStore, *fakePassiveNotificationPostgresOps, *fakeConnectorPostgresSession) {
	session := &fakeConnectorPostgresSession{transaction: transaction}
	base := &fakeConnectorPostgresOps{session: session}
	operations := &fakePassiveNotificationPostgresOps{fakeConnectorPostgresOps: base}
	return &PostgresStore{
		operationTimeouts: defaultOperationTimeouts, passiveNotificationPostgres: operations,
		passiveNotificationRevs: map[string]uint64{},
	}, operations, session
}

func passiveNotificationPostgresRow(notification app.PassiveNotification) fakeConnectorPostgresRow {
	return fakeConnectorPostgresRow{scan: func(destinations ...any) error {
		values := []string{notification.ID, notification.OwnerID, notification.EndpointID, notification.IdempotencyKey,
			notification.Fingerprint, notification.NotificationID, notification.Source, notification.Kind, notification.DeepLink}
		for index, value := range values {
			*(destinations[index].(*string)) = value
		}
		*(destinations[9].(*time.Time)) = notification.OccurredAt
		*(destinations[10].(**time.Time)) = cloneTimePointer(notification.ReadAt)
		*(destinations[11].(*time.Time)) = notification.CreatedAt
		*(destinations[12].(*time.Time)) = notification.UpdatedAt
		return nil
	}}
}

func passiveNotificationReadPostgresRow(notification app.PassiveNotification, changed bool) fakeConnectorPostgresRow {
	base := passiveNotificationPostgresRow(notification)
	return fakeConnectorPostgresRow{scan: func(destinations ...any) error {
		if len(destinations) != 14 {
			return errors.New("mark-read query returned an unexpected column count")
		}
		if err := base.Scan(destinations[:13]...); err != nil {
			return err
		}
		*(destinations[13].(*bool)) = changed
		return nil
	}}
}

func passiveNotificationOwnerPostgresRow(ownerID string) fakeConnectorPostgresRow {
	return fakeConnectorPostgresRow{scan: func(destinations ...any) error {
		*(destinations[0].(*string)) = ownerID
		return nil
	}}
}

func TestPostgresPassiveNotificationWritesUseLifecycleTransactions(t *testing.T) {
	base := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)
	notification := passiveNotificationContractFixture("owner-postgres", "passive-postgres", "endpoint-postgres", "key-postgres", "fingerprint-postgres", base)
	notification.UpdatedAt = base

	t.Run("create", func(t *testing.T) {
		tx := &fakeConnectorPostgresTx{rowQueue: []onboardingPostgresRow{passiveNotificationPostgresRow(notification)}}
		repository, _, session := newFakePassiveNotificationPostgresStore(tx)
		stored, inserted, err := repository.CreatePassiveNotification(t.Context(), notification)
		if err != nil || !inserted || stored.ID != notification.ID || tx.commits != 1 || tx.rollbacks != 0 || session.releases != 1 {
			t.Fatalf("stored=%#v inserted=%t err=%v commit=%d rollback=%d release=%d", stored, inserted, err, tx.commits, tx.rollbacks, session.releases)
		}
		if len(tx.rowSQL) != 1 || !strings.Contains(tx.rowSQL[0], "passive_notifications") || len(tx.execSQL) != 1 || !strings.Contains(tx.execSQL[0], "audit_events") {
			t.Fatalf("create transaction row=%v exec=%v", tx.rowSQL, tx.execSQL)
		}
	})

	t.Run("create audit rollback", func(t *testing.T) {
		tx := &fakeConnectorPostgresTx{
			rowQueue:   []onboardingPostgresRow{passiveNotificationPostgresRow(notification)},
			execErrors: map[int]error{0: safePostgresRetryError{errors.New("not sent")}},
		}
		repository, _, session := newFakePassiveNotificationPostgresStore(tx)
		stored, inserted, err := repository.CreatePassiveNotification(t.Context(), notification)
		if stored.ID != "" || inserted || StoreErrorCodeOf(err) != StoreErrorUnavailable || tx.commits != 0 || tx.rollbacks != 1 || session.releases != 1 {
			t.Fatalf("stored=%#v inserted=%t err=%v code=%q commit=%d rollback=%d release=%d", stored, inserted, err, StoreErrorCodeOf(err), tx.commits, tx.rollbacks, session.releases)
		}
	})

	t.Run("prune", func(t *testing.T) {
		rows := &fakeConnectorPostgresRows{rows: []fakeConnectorPostgresRow{
			passiveNotificationOwnerPostgresRow("owner-a"), passiveNotificationOwnerPostgresRow("owner-b"),
		}}
		tx := &fakeConnectorPostgresTx{rowsQueue: []fakeConnectorRowsResult{{rows: rows}}}
		repository, _, session := newFakePassiveNotificationPostgresStore(tx)
		removed, err := repository.PrunePassiveNotifications(t.Context(), base, 0)
		if err != nil || removed != 2 || tx.commits != 1 || tx.rollbacks != 0 || session.releases != 1 || len(tx.execSQL) != 2 {
			t.Fatalf("removed=%d err=%v exec=%v commit=%d rollback=%d release=%d", removed, err, tx.execSQL, tx.commits, tx.rollbacks, session.releases)
		}
		for _, statement := range tx.execSQL {
			if !strings.Contains(statement, "audit_events") {
				t.Fatalf("prune transaction omitted audit: %v", tx.execSQL)
			}
		}
	})

	t.Run("prune audit rollback", func(t *testing.T) {
		rows := &fakeConnectorPostgresRows{rows: []fakeConnectorPostgresRow{passiveNotificationOwnerPostgresRow("owner-a")}}
		tx := &fakeConnectorPostgresTx{
			rowsQueue:  []fakeConnectorRowsResult{{rows: rows}},
			execErrors: map[int]error{0: safePostgresRetryError{errors.New("not sent")}},
		}
		repository, _, session := newFakePassiveNotificationPostgresStore(tx)
		removed, err := repository.PrunePassiveNotifications(t.Context(), base, 0)
		if removed != 0 || StoreErrorCodeOf(err) != StoreErrorUnavailable || tx.commits != 0 || tx.rollbacks != 1 || session.releases != 1 {
			t.Fatalf("removed=%d err=%v code=%q commit=%d rollback=%d release=%d", removed, err, StoreErrorCodeOf(err), tx.commits, tx.rollbacks, session.releases)
		}
	})

	t.Run("prune rows error", func(t *testing.T) {
		sentinel := errors.New("row stream failed")
		tx := &fakeConnectorPostgresTx{rowsQueue: []fakeConnectorRowsResult{{rows: &fakeConnectorPostgresRows{err: sentinel}}}}
		repository, _, session := newFakePassiveNotificationPostgresStore(tx)
		removed, err := repository.PrunePassiveNotifications(t.Context(), base, 0)
		if removed != 0 || StoreErrorCodeOf(err) != StoreErrorUnavailable || tx.rollbacks != 1 || session.releases != 1 {
			t.Fatalf("removed=%d err=%v code=%q rollback=%d release=%d", removed, err, StoreErrorCodeOf(err), tx.rollbacks, session.releases)
		}
	})
}

func TestPostgresPassiveNotificationReadsPropagateBackendErrors(t *testing.T) {
	sentinel := errors.New("backend failed")
	base := time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)
	notification := passiveNotificationContractFixture("owner-read", "passive-read", "endpoint-read", "key-read", "fingerprint-read", base)
	notification.UpdatedAt = base

	t.Run("get missing", func(t *testing.T) {
		repository, _, _ := newFakePassiveNotificationPostgresStore(&fakeConnectorPostgresTx{})
		if value, found, err := repository.GetPassiveNotification(t.Context(), notification.OwnerID, "missing"); err != nil || found || value.ID != "" {
			t.Fatalf("value=%#v found=%t err=%v", value, found, err)
		}
	})

	tests := []struct {
		name  string
		setup func(*fakePassiveNotificationPostgresOps)
		call  func(*PostgresStore) error
	}{
		{name: "get scan", setup: func(operations *fakePassiveNotificationPostgresOps) {
			operations.rowQueue = []onboardingPostgresRow{fakeConnectorPostgresRow{scan: func(...any) error { return sentinel }}}
		}, call: func(repository *PostgresStore) error {
			_, _, err := repository.GetPassiveNotification(t.Context(), notification.OwnerID, notification.ID)
			return err
		}},
		{name: "list query", setup: func(operations *fakePassiveNotificationPostgresOps) {
			operations.queryResults = []fakeConnectorRowsResult{{err: sentinel}}
		}, call: func(repository *PostgresStore) error {
			_, err := repository.ListPassiveNotifications(t.Context(), notification.OwnerID, "", 10)
			return err
		}},
		{name: "list scan", setup: func(operations *fakePassiveNotificationPostgresOps) {
			operations.queryResults = []fakeConnectorRowsResult{{rows: &fakeConnectorPostgresRows{scanErr: sentinel, rows: []fakeConnectorPostgresRow{{}}}}}
		}, call: func(repository *PostgresStore) error {
			_, err := repository.ListPassiveNotifications(t.Context(), notification.OwnerID, "", 10)
			return err
		}},
		{name: "list rows", setup: func(operations *fakePassiveNotificationPostgresOps) {
			operations.queryResults = []fakeConnectorRowsResult{{rows: &fakeConnectorPostgresRows{err: sentinel}}}
		}, call: func(repository *PostgresStore) error {
			_, err := repository.ListPassiveNotifications(t.Context(), notification.OwnerID, "", 10)
			return err
		}},
		{name: "count scan", setup: func(operations *fakePassiveNotificationPostgresOps) {
			operations.rowQueue = []onboardingPostgresRow{fakeConnectorPostgresRow{scan: func(...any) error { return sentinel }}}
		}, call: func(repository *PostgresStore) error {
			_, err := repository.CountUnreadPassiveNotifications(t.Context(), notification.OwnerID)
			return err
		}},
		{name: "mark read scan", setup: func(operations *fakePassiveNotificationPostgresOps) {
			operations.rowQueue = []onboardingPostgresRow{fakeConnectorPostgresRow{scan: func(...any) error { return sentinel }}}
		}, call: func(repository *PostgresStore) error {
			_, err := repository.MarkPassiveNotificationRead(t.Context(), notification.OwnerID, notification.ID, base)
			return err
		}},
		{name: "mark all exec", setup: func(operations *fakePassiveNotificationPostgresOps) {
			operations.execError = map[int]error{0: sentinel}
		}, call: func(repository *PostgresStore) error {
			_, err := repository.MarkAllPassiveNotificationsRead(t.Context(), notification.OwnerID, base)
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository, operations, _ := newFakePassiveNotificationPostgresStore(&fakeConnectorPostgresTx{})
			test.setup(operations)
			if err := test.call(repository); StoreErrorCodeOf(err) != StoreErrorUnavailable {
				t.Fatalf("error=%v code=%q", err, StoreErrorCodeOf(err))
			}
		})
	}

	t.Run("mark read result columns", func(t *testing.T) {
		readAt := base.Add(time.Minute)
		notification.ReadAt = &readAt
		repository, operations, _ := newFakePassiveNotificationPostgresStore(&fakeConnectorPostgresTx{})
		operations.rowQueue = []onboardingPostgresRow{passiveNotificationReadPostgresRow(notification, true)}
		stored, err := repository.MarkPassiveNotificationRead(t.Context(), notification.OwnerID, notification.ID, readAt)
		if err != nil || stored.ID != notification.ID || stored.ReadAt == nil || mustPassiveNotificationRevision(t, repository, notification.OwnerID) != 1 {
			t.Fatalf("stored=%#v err=%v", stored, err)
		}
		if len(operations.rowSQL) != 1 || !strings.Contains(operations.rowSQL[0], "FOR UPDATE") {
			t.Fatalf("mark-read query does not lock before deciding revision change: %v", operations.rowSQL)
		}
	})
}
