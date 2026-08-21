package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakeDeliveryPostgresOps struct {
	session  *fakeDeliveryPostgresSession
	row      onboardingPostgresRow
	rows     onboardingPostgresRows
	queryErr error
	querySQL []string
	rowSQL   []string
}

func (o *fakeDeliveryPostgresOps) Acquire(context.Context) (onboardingPostgresSession, error) {
	return o.session, nil
}

func (o *fakeDeliveryPostgresOps) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("unexpected pool Exec")
}

func (o *fakeDeliveryPostgresOps) Query(_ context.Context, sql string, _ ...any) (onboardingPostgresRows, error) {
	o.querySQL = append(o.querySQL, sql)
	return o.rows, o.queryErr
}

func (o *fakeDeliveryPostgresOps) QueryRow(_ context.Context, sql string, _ ...any) onboardingPostgresRow {
	o.rowSQL = append(o.rowSQL, sql)
	if o.row == nil {
		return fakeDeliveryPostgresRow{err: errors.New("unexpected pool QueryRow")}
	}
	return o.row
}

type fakeDeliveryPostgresSession struct {
	transaction *fakeDeliveryPostgresTx
	releases    int
	terminates  int
}

func (s *fakeDeliveryPostgresSession) Begin(context.Context, pgx.TxOptions) (onboardingPostgresTx, error) {
	return s.transaction, nil
}

func (s *fakeDeliveryPostgresSession) Release() { s.releases++ }

func (s *fakeDeliveryPostgresSession) Terminate(context.Context) error {
	s.terminates++
	return nil
}

type fakeDeliveryPostgresTx struct {
	execSQL     []string
	execArgs    [][]any
	execErrors  map[int]error
	rowQueue    []onboardingPostgresRow
	commitErr   error
	rollbackErr error
	commits     int
	rollbacks   int
}

func (t *fakeDeliveryPostgresTx) Exec(_ context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	index := len(t.execSQL)
	t.execSQL = append(t.execSQL, sql)
	t.execArgs = append(t.execArgs, append([]any(nil), arguments...))
	if err := t.execErrors[index]; err != nil {
		return pgconn.CommandTag{}, err
	}
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (t *fakeDeliveryPostgresTx) QueryRow(context.Context, string, ...any) onboardingPostgresRow {
	if len(t.rowQueue) == 0 {
		return fakeDeliveryPostgresRow{err: errors.New("unexpected transaction QueryRow")}
	}
	row := t.rowQueue[0]
	t.rowQueue = t.rowQueue[1:]
	return row
}

func (t *fakeDeliveryPostgresTx) Query(context.Context, string, ...any) (onboardingPostgresRows, error) {
	return nil, errors.New("unexpected transaction Query")
}

func (t *fakeDeliveryPostgresTx) Commit(context.Context) error {
	t.commits++
	return t.commitErr
}

func (t *fakeDeliveryPostgresTx) Rollback(context.Context) error {
	t.rollbacks++
	return t.rollbackErr
}

type fakeDeliveryPostgresRow struct {
	values []any
	err    error
}

func (r fakeDeliveryPostgresRow) Scan(destinations ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(destinations) != len(r.values) {
		return errors.New("fake delivery row shape mismatch")
	}
	for index, value := range r.values {
		target := reflect.ValueOf(destinations[index])
		if target.Kind() != reflect.Pointer || target.IsNil() {
			return errors.New("fake delivery row destination is not a pointer")
		}
		if value == nil {
			target.Elem().SetZero()
			continue
		}
		source := reflect.ValueOf(value)
		if source.Type().AssignableTo(target.Elem().Type()) {
			target.Elem().Set(source)
		} else if source.Type().ConvertibleTo(target.Elem().Type()) {
			target.Elem().Set(source.Convert(target.Elem().Type()))
		} else {
			return errors.New("fake delivery row value type mismatch")
		}
	}
	return nil
}

type fakeDeliveryPostgresRows struct {
	values []fakeDeliveryPostgresRow
	index  int
	err    error
	closed bool
}

func (r *fakeDeliveryPostgresRows) Next() bool { return r.index < len(r.values) }

func (r *fakeDeliveryPostgresRows) Scan(destinations ...any) error {
	row := r.values[r.index]
	r.index++
	return row.Scan(destinations...)
}

func (r *fakeDeliveryPostgresRows) Err() error { return r.err }
func (r *fakeDeliveryPostgresRows) Close()     { r.closed = true }

func newFakeDeliveryPostgresStore(transaction *fakeDeliveryPostgresTx) (*PostgresStore, *fakeDeliveryPostgresOps, *fakeDeliveryPostgresSession) {
	session := &fakeDeliveryPostgresSession{transaction: transaction}
	operations := &fakeDeliveryPostgresOps{session: session}
	return &PostgresStore{operationTimeouts: defaultOperationTimeouts, deliveryRecordPostgres: operations}, operations, session
}

func fakeDeliveryNoRows() onboardingPostgresRow {
	return fakeDeliveryPostgresRow{err: pgx.ErrNoRows}
}

func fakeDeliveryJSONRow(t testing.TB, value any) onboardingPostgresRow {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return fakeDeliveryPostgresRow{values: []any{raw}}
}

func fakeChannelInboxPostgresRow(update app.ChannelInboxUpdate) onboardingPostgresRow {
	payload := []byte(update.Payload)
	if len(payload) == 0 {
		payload = []byte("null")
	}
	return fakeDeliveryPostgresRow{values: []any{
		update.ID, update.BindingID, update.Channel, update.ExternalID, update.ChatKey, payload,
		update.Status, update.Attempts, update.AvailableAt, update.LastError, update.CreatedAt, update.UpdatedAt,
	}}
}

func TestPostgresDeliveryRecordWritesUseOneTransactionAndBothIdentityLocks(t *testing.T) {
	tests := []struct {
		name      string
		wantTable string
		wantAudit string
		invoke    func(*PostgresStore) error
	}{
		{
			name: "receive", wantTable: "message_receive_records", wantAudit: "audit_events",
			invoke: func(store *PostgresStore) error {
				_, err := store.SaveMessageReceive(t.Context(), app.MessageReceiveRecord{
					ID: "receive-atomic", SourceEndpointID: "endpoint-atomic", NativeMessageID: "native-atomic", Status: "received",
				})
				return err
			},
		},
		{
			name: "delivery", wantTable: "message_delivery_records", wantAudit: "audit_events",
			invoke: func(store *PostgresStore) error {
				_, err := store.SaveMessageDelivery(t.Context(), app.MessageDeliveryRecord{
					ID: "delivery-atomic", OwnerID: "owner", ActorID: "actor", Status: app.DeliveryPending, ContentDigest: "sha256:atomic",
					Request: app.DeliveryRequest{IdempotencyKey: "key-atomic", Target: "endpoint", ContentDigest: "sha256:atomic"},
				})
				return err
			},
		},
		{
			name: "inbox", wantTable: "channel_inbox_updates",
			invoke: func(store *PostgresStore) error {
				_, err := store.SaveChannelInboxUpdate(t.Context(), app.ChannelInboxUpdate{
					ID: "inbox-atomic", BindingID: "binding-atomic", Channel: "telegram", ExternalID: "external-atomic", Payload: json.RawMessage(`{"id":"atomic"}`),
				})
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx := &fakeDeliveryPostgresTx{rowQueue: []onboardingPostgresRow{fakeDeliveryNoRows(), fakeDeliveryNoRows()}, execErrors: map[int]error{}}
			store, _, session := newFakeDeliveryPostgresStore(tx)
			if err := test.invoke(store); err != nil || tx.commits != 1 || tx.rollbacks != 0 || session.releases != 1 {
				t.Fatalf("err=%v commit=%d rollback=%d release=%d", err, tx.commits, tx.rollbacks, session.releases)
			}
			if len(tx.execSQL) < 3 || !strings.Contains(tx.execSQL[0], "pg_advisory_xact_lock") || !strings.Contains(tx.execSQL[1], "pg_advisory_xact_lock") || !strings.Contains(strings.Join(tx.execSQL, "\n"), test.wantTable) {
				t.Fatalf("transaction SQL=%v", tx.execSQL)
			}
			if len(tx.execArgs[0]) != 1 || len(tx.execArgs[1]) != 1 || tx.execArgs[0][0] == tx.execArgs[1][0] {
				t.Fatalf("identity lock args=%v", tx.execArgs[:2])
			}
			if test.wantAudit != "" && !strings.Contains(strings.Join(tx.execSQL, "\n"), test.wantAudit) {
				t.Fatalf("record and audit were not written through one transaction: %v", tx.execSQL)
			}
		})
	}
}

func TestPostgresDeliveryRecordStatementAndCommitFailureSemantics(t *testing.T) {
	t.Run("safe audit failure rolls back record", func(t *testing.T) {
		injected := safePostgresRetryError{errors.New("audit not sent")}
		tx := &fakeDeliveryPostgresTx{
			rowQueue:   []onboardingPostgresRow{fakeDeliveryNoRows(), fakeDeliveryNoRows()},
			execErrors: map[int]error{3: injected},
		}
		store, _, session := newFakeDeliveryPostgresStore(tx)
		candidate, err := store.SaveMessageReceive(t.Context(), app.MessageReceiveRecord{
			ID: "receive-audit-failure", SourceEndpointID: "endpoint", NativeMessageID: "native", Status: "received",
		})
		if candidate.ID != "" || StoreErrorCodeOf(err) != StoreErrorUnavailable || tx.rollbacks != 1 || tx.commits != 0 || session.releases != 1 || session.terminates != 0 {
			t.Fatalf("candidate=%#v err=%v rollback=%d commit=%d release=%d terminate=%d", candidate, err, tx.rollbacks, tx.commits, session.releases, session.terminates)
		}
	})

	t.Run("unsafe audit failure returns candidate", func(t *testing.T) {
		injected := errors.New("audit submission uncertain")
		tx := &fakeDeliveryPostgresTx{
			rowQueue:   []onboardingPostgresRow{fakeDeliveryNoRows(), fakeDeliveryNoRows()},
			execErrors: map[int]error{3: injected},
		}
		store, _, session := newFakeDeliveryPostgresStore(tx)
		candidate, err := store.SaveMessageReceive(t.Context(), app.MessageReceiveRecord{
			ID: "receive-audit-unknown", SourceEndpointID: "endpoint", NativeMessageID: "native", Status: "received",
		})
		if candidate.ID != "receive-audit-unknown" || StoreErrorCodeOf(err) != StoreErrorUnknownOutcome || tx.rollbacks != 0 || session.releases != 0 || session.terminates != 1 || !errors.Is(err, injected) {
			t.Fatalf("candidate=%#v err=%v rollback=%d release=%d terminate=%d", candidate, err, tx.rollbacks, session.releases, session.terminates)
		}
	})

	t.Run("unique insert is conflict", func(t *testing.T) {
		tx := &fakeDeliveryPostgresTx{
			rowQueue:   []onboardingPostgresRow{fakeDeliveryNoRows(), fakeDeliveryNoRows()},
			execErrors: map[int]error{2: &pgconn.PgError{Code: "23505"}},
		}
		store, _, _ := newFakeDeliveryPostgresStore(tx)
		candidate, err := store.SaveMessageDelivery(t.Context(), app.MessageDeliveryRecord{
			ID: "delivery-unique", OwnerID: "owner", ActorID: "actor", Status: app.DeliveryPending, ContentDigest: "sha256:unique",
			Request: app.DeliveryRequest{IdempotencyKey: "key-unique", Target: "endpoint", ContentDigest: "sha256:unique"},
		})
		if candidate.ID != "" || StoreErrorCodeOf(err) != StoreErrorConflict || !errors.Is(err, ErrMessageDeliveryConflict) || tx.rollbacks != 1 {
			t.Fatalf("candidate=%#v err=%v rollback=%d", candidate, err, tx.rollbacks)
		}
	})

	t.Run("commit failure returns candidate", func(t *testing.T) {
		injected := errors.New("commit uncertain")
		tx := &fakeDeliveryPostgresTx{
			rowQueue: []onboardingPostgresRow{fakeDeliveryNoRows(), fakeDeliveryNoRows()}, execErrors: map[int]error{}, commitErr: injected,
		}
		store, _, session := newFakeDeliveryPostgresStore(tx)
		candidate, err := store.SaveChannelInboxUpdate(t.Context(), app.ChannelInboxUpdate{
			ID: "inbox-commit", BindingID: "binding", Channel: "telegram", ExternalID: "external", Payload: json.RawMessage(`{"id":"commit"}`),
		})
		if candidate.ID != "inbox-commit" || StoreErrorCodeOf(err) != StoreErrorUnknownOutcome || session.releases != 0 || session.terminates != 1 || !errors.Is(err, injected) {
			t.Fatalf("candidate=%#v err=%v release=%d terminate=%d", candidate, err, session.releases, session.terminates)
		}
	})
}

func TestPostgresDeliveryRecordRejectsCrossBoundIdentity(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name   string
		rows   []onboardingPostgresRow
		invoke func(*PostgresStore) error
		isErr  func(error) bool
	}{
		{
			name: "receive",
			rows: []onboardingPostgresRow{
				fakeDeliveryJSONRow(t, app.MessageReceiveRecord{ID: "receive-b", SourceEndpointID: "endpoint-b", NativeMessageID: "native-b", Status: "received", CreatedAt: now, UpdatedAt: now}),
				fakeDeliveryJSONRow(t, app.MessageReceiveRecord{ID: "receive-a", SourceEndpointID: "endpoint-a", NativeMessageID: "native-a", Status: "received", CreatedAt: now, UpdatedAt: now}),
			},
			invoke: func(store *PostgresStore) error {
				_, err := store.SaveMessageReceive(t.Context(), app.MessageReceiveRecord{ID: "receive-b", SourceEndpointID: "endpoint-a", NativeMessageID: "native-a", Status: "received"})
				return err
			},
			isErr: func(err error) bool { return errors.Is(err, ErrMessageReceiveConflict) },
		},
		{
			name: "delivery",
			rows: []onboardingPostgresRow{
				fakeDeliveryJSONRow(t, app.MessageDeliveryRecord{ID: "delivery-b", OwnerID: "owner", ActorID: "actor", Status: app.DeliveryPending, ContentDigest: "sha256:b", Request: app.DeliveryRequest{IdempotencyKey: "key-b", Target: "endpoint-b", ContentDigest: "sha256:b"}, CreatedAt: now, UpdatedAt: now}),
				fakeDeliveryJSONRow(t, app.MessageDeliveryRecord{ID: "delivery-a", OwnerID: "owner", ActorID: "actor", Status: app.DeliveryPending, ContentDigest: "sha256:a", Request: app.DeliveryRequest{IdempotencyKey: "key-a", Target: "endpoint-a", ContentDigest: "sha256:a"}, CreatedAt: now, UpdatedAt: now}),
			},
			invoke: func(store *PostgresStore) error {
				_, err := store.SaveMessageDelivery(t.Context(), app.MessageDeliveryRecord{ID: "delivery-b", OwnerID: "owner", ActorID: "actor", Status: app.DeliveryPending, ContentDigest: "sha256:a", Request: app.DeliveryRequest{IdempotencyKey: "key-a", Target: "endpoint-a", ContentDigest: "sha256:a"}})
				return err
			},
			isErr: func(err error) bool { return errors.Is(err, ErrMessageDeliveryConflict) },
		},
		{
			name: "inbox",
			rows: []onboardingPostgresRow{
				fakeChannelInboxPostgresRow(app.ChannelInboxUpdate{ID: "inbox-b", BindingID: "binding-b", Channel: "telegram", ExternalID: "external-b", Payload: json.RawMessage(`{"id":"b"}`), Status: "pending", AvailableAt: now, CreatedAt: now, UpdatedAt: now}),
				fakeChannelInboxPostgresRow(app.ChannelInboxUpdate{ID: "inbox-a", BindingID: "binding-a", Channel: "telegram", ExternalID: "external-a", Payload: json.RawMessage(`{"id":"a"}`), Status: "pending", AvailableAt: now, CreatedAt: now, UpdatedAt: now}),
			},
			invoke: func(store *PostgresStore) error {
				_, err := store.SaveChannelInboxUpdate(t.Context(), app.ChannelInboxUpdate{ID: "inbox-b", BindingID: "binding-a", Channel: "telegram", ExternalID: "external-a", Payload: json.RawMessage(`{"id":"a"}`)})
				return err
			},
			isErr: func(err error) bool { return errors.Is(err, ErrChannelInboxUpdateConflict) },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx := &fakeDeliveryPostgresTx{rowQueue: test.rows, execErrors: map[int]error{}}
			store, _, session := newFakeDeliveryPostgresStore(tx)
			err := test.invoke(store)
			if StoreErrorCodeOf(err) != StoreErrorConflict || !test.isErr(err) || tx.rollbacks != 1 || tx.commits != 0 || session.releases != 1 {
				t.Fatalf("err=%v rollback=%d commit=%d release=%d", err, tx.rollbacks, tx.commits, session.releases)
			}
		})
	}
}

func TestPostgresDeliveryRecordReadFailureClassification(t *testing.T) {
	tests := []struct {
		name     string
		rows     *fakeDeliveryPostgresRows
		queryErr error
		invoke   func(*PostgresStore) error
		wantCode StoreErrorCode
	}{
		{
			name: "query", rows: &fakeDeliveryPostgresRows{}, queryErr: errors.New("query failed"), wantCode: StoreErrorUnavailable,
			invoke: func(store *PostgresStore) error {
				_, err := store.ListMessageReceives(t.Context(), "", "", 10)
				return err
			},
		},
		{
			name: "scan", rows: &fakeDeliveryPostgresRows{values: []fakeDeliveryPostgresRow{{err: errors.New("scan failed")}}}, wantCode: StoreErrorUnavailable,
			invoke: func(store *PostgresStore) error {
				_, err := store.ListMessageDeliveries(t.Context(), "", "", 10)
				return err
			},
		},
		{
			name: "receive decode", rows: &fakeDeliveryPostgresRows{values: []fakeDeliveryPostgresRow{{values: []any{[]byte("{")}}}}, wantCode: StoreErrorCorrupt,
			invoke: func(store *PostgresStore) error {
				_, err := store.ListMessageReceives(t.Context(), "", "", 10)
				return err
			},
		},
		{
			name: "delivery decode", rows: &fakeDeliveryPostgresRows{values: []fakeDeliveryPostgresRow{{values: []any{[]byte("{")}}}}, wantCode: StoreErrorCorrupt,
			invoke: func(store *PostgresStore) error {
				_, err := store.ListMessageDeliveries(t.Context(), "", "", 10)
				return err
			},
		},
		{
			name: "inbox payload decode", rows: &fakeDeliveryPostgresRows{values: []fakeDeliveryPostgresRow{{values: []any{
				"inbox", "binding", "telegram", "external", "", []byte("{"), "pending", 0, time.Now().UTC(), "", time.Now().UTC(), time.Now().UTC(),
			}}}}, wantCode: StoreErrorCorrupt,
			invoke: func(store *PostgresStore) error {
				_, err := store.ListChannelInboxUpdates(t.Context(), "", "", time.Time{}, 10)
				return err
			},
		},
		{
			name: "rows iteration", rows: &fakeDeliveryPostgresRows{err: errors.New("rows failed")}, wantCode: StoreErrorUnavailable,
			invoke: func(store *PostgresStore) error {
				_, err := store.ListChannelInboxUpdates(t.Context(), "", "", time.Time{}, 10)
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, operations, _ := newFakeDeliveryPostgresStore(&fakeDeliveryPostgresTx{})
			operations.rows = test.rows
			operations.queryErr = test.queryErr
			err := test.invoke(store)
			if StoreErrorCodeOf(err) != test.wantCode {
				t.Fatalf("err=%v code=%q want=%q", err, StoreErrorCodeOf(err), test.wantCode)
			}
			if test.queryErr == nil && !test.rows.closed {
				t.Fatal("rows were not closed")
			}
		})
	}
}

func TestPostgresDeliveryRecordRepositoryConcurrentIdempotencyAndAuditAtomicity(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set SPARKCLAW_TEST_POSTGRES_DSN to run postgres store integration tests")
	}
	first, err := NewPostgresStore(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	truncatePostgresStore(t, first)
	second, err := NewPostgresStore(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	t.Run("receive", func(t *testing.T) {
		start := make(chan struct{})
		results := make(chan app.MessageReceiveRecord, 2)
		errorsFound := make(chan error, 2)
		var wait sync.WaitGroup
		for index, repository := range []*PostgresStore{first, second} {
			wait.Add(1)
			go func() {
				defer wait.Done()
				<-start
				record, err := repository.SaveMessageReceive(t.Context(), app.MessageReceiveRecord{
					ID:      []string{"receive-concurrent-a", "receive-concurrent-b"}[index],
					OwnerID: "owner", ActorID: "actor", ProviderKey: "telegram", SourceEndpointID: "endpoint-concurrent", NativeMessageID: "native-concurrent", Status: "received",
				})
				if err != nil {
					errorsFound <- err
					return
				}
				results <- record
			}()
		}
		close(start)
		wait.Wait()
		close(results)
		close(errorsFound)
		for err := range errorsFound {
			t.Fatal(err)
		}
		var ids []string
		for record := range results {
			ids = append(ids, record.ID)
		}
		listed, err := first.ListMessageReceives(t.Context(), "owner", "actor", 10)
		if err != nil || len(ids) != 2 || ids[0] != ids[1] || len(listed) != 1 || listed[0].ID != ids[0] {
			t.Fatalf("ids=%v listed=%#v err=%v", ids, listed, err)
		}
	})

	var deliveryID app.DeliveryID
	t.Run("delivery", func(t *testing.T) {
		start := make(chan struct{})
		results := make(chan app.MessageDeliveryRecord, 2)
		errorsFound := make(chan error, 2)
		var wait sync.WaitGroup
		for index, repository := range []*PostgresStore{first, second} {
			wait.Add(1)
			go func() {
				defer wait.Done()
				<-start
				record, err := repository.SaveMessageDelivery(t.Context(), app.MessageDeliveryRecord{
					ID:      app.DeliveryID([]string{"delivery-concurrent-a", "delivery-concurrent-b"}[index]),
					OwnerID: "owner", ActorID: "actor", SoftwareDisplayName: "concurrent delivery", Status: app.DeliveryPending, ContentDigest: "sha256:concurrent",
					Request: app.DeliveryRequest{IdempotencyKey: "delivery-key-concurrent", Target: "endpoint-concurrent", ContentDigest: "sha256:concurrent"},
				})
				if err != nil {
					errorsFound <- err
					return
				}
				results <- record
			}()
		}
		close(start)
		wait.Wait()
		close(results)
		close(errorsFound)
		for err := range errorsFound {
			t.Fatal(err)
		}
		var ids []app.DeliveryID
		for record := range results {
			ids = append(ids, record.ID)
		}
		listed, err := first.ListMessageDeliveries(t.Context(), "owner", "actor", 10)
		if err != nil || len(ids) != 2 || ids[0] != ids[1] || len(listed) != 1 || listed[0].ID != ids[0] {
			t.Fatalf("ids=%v listed=%#v err=%v", ids, listed, err)
		}
		deliveryID = ids[0]
	})

	t.Run("inbox", func(t *testing.T) {
		start := make(chan struct{})
		results := make(chan app.ChannelInboxUpdate, 2)
		errorsFound := make(chan error, 2)
		var wait sync.WaitGroup
		for index, repository := range []*PostgresStore{first, second} {
			wait.Add(1)
			go func() {
				defer wait.Done()
				<-start
				update, err := repository.SaveChannelInboxUpdate(t.Context(), app.ChannelInboxUpdate{
					ID: []string{"inbox-concurrent-a", "inbox-concurrent-b"}[index], BindingID: "binding-concurrent", Channel: "telegram", ExternalID: "external-concurrent", Payload: json.RawMessage(`{"id":"concurrent"}`),
				})
				if err != nil {
					errorsFound <- err
					return
				}
				results <- update
			}()
		}
		close(start)
		wait.Wait()
		close(results)
		close(errorsFound)
		for err := range errorsFound {
			t.Fatal(err)
		}
		var ids []string
		for update := range results {
			ids = append(ids, update.ID)
		}
		listed, err := first.ListChannelInboxUpdates(t.Context(), "telegram", "pending", time.Time{}, 10)
		if err != nil || len(ids) != 2 || ids[0] != ids[1] || len(listed) != 1 || listed[0].ID != ids[0] || string(listed[0].Payload) != `{"id":"concurrent"}` {
			t.Fatalf("ids=%v listed=%#v err=%v", ids, listed, err)
		}
	})

	var deliveryAuditCount int
	if err := first.db.QueryRow(t.Context(), `
		SELECT count(*) FROM audit_events
		WHERE type = 'message.send.pending' AND fields->>'delivery_id' = $1
	`, deliveryID).Scan(&deliveryAuditCount); err != nil || deliveryAuditCount != 1 {
		t.Fatalf("delivery audit count=%d err=%v", deliveryAuditCount, err)
	}

	if _, err := first.db.Exec(t.Context(), `DROP TRIGGER IF EXISTS sparkclaw_test_delivery_audit_failure ON audit_events`); err != nil {
		t.Fatal(err)
	}
	if _, err := first.db.Exec(t.Context(), `DROP FUNCTION IF EXISTS sparkclaw_test_delivery_audit_failure()`); err != nil {
		t.Fatal(err)
	}
	if _, err := first.db.Exec(t.Context(), `
		CREATE FUNCTION sparkclaw_test_delivery_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.summary = 'force delivery audit rollback' THEN
				RAISE EXCEPTION 'forced delivery audit failure';
			END IF;
			RETURN NEW;
		END
		$$
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := first.db.Exec(t.Context(), `
		CREATE TRIGGER sparkclaw_test_delivery_audit_failure
		BEFORE INSERT ON audit_events FOR EACH ROW
		EXECUTE FUNCTION sparkclaw_test_delivery_audit_failure()
	`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = first.db.Exec(context.Background(), `DROP TRIGGER IF EXISTS sparkclaw_test_delivery_audit_failure ON audit_events`)
		_, _ = first.db.Exec(context.Background(), `DROP FUNCTION IF EXISTS sparkclaw_test_delivery_audit_failure()`)
	})

	candidate, err := first.SaveMessageDelivery(t.Context(), app.MessageDeliveryRecord{
		ID: "delivery-audit-rollback", OwnerID: "owner", ActorID: "actor", SoftwareDisplayName: "force delivery audit rollback", Status: app.DeliveryPending, ContentDigest: "sha256:audit-rollback",
		Request: app.DeliveryRequest{IdempotencyKey: "delivery-key-audit-rollback", Target: "endpoint", ContentDigest: "sha256:audit-rollback"},
	})
	if candidate.ID != "" || StoreErrorCodeOf(err) != StoreErrorInternal {
		t.Fatalf("candidate=%#v err=%v code=%q", candidate, err, StoreErrorCodeOf(err))
	}
	if _, found, findErr := first.FindMessageDeliveryByIdempotency(t.Context(), "owner", "actor", "delivery-key-audit-rollback"); findErr != nil || found {
		t.Fatalf("rolled-back delivery found=%v err=%v", found, findErr)
	}
}
