package store

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type safePostgresRetryError struct{ error }

func (safePostgresRetryError) SafeToRetry() bool { return true }

type fakeOnboardingPostgresOps struct {
	session    *fakeOnboardingPostgresSession
	acquireErr error
}

func (o *fakeOnboardingPostgresOps) Acquire(context.Context) (onboardingPostgresSession, error) {
	if o.acquireErr != nil {
		return nil, o.acquireErr
	}
	return o.session, nil
}

type fakeOnboardingPostgresSession struct {
	transaction  *fakeOnboardingPostgresTx
	beginErr     error
	options      []pgx.TxOptions
	releases     int
	terminates   int
	terminateErr error
}

func (s *fakeOnboardingPostgresSession) Begin(_ context.Context, options pgx.TxOptions) (onboardingPostgresTx, error) {
	s.options = append(s.options, options)
	if s.beginErr != nil {
		return nil, s.beginErr
	}
	return s.transaction, nil
}

func (s *fakeOnboardingPostgresSession) Release() { s.releases++ }

func (s *fakeOnboardingPostgresSession) Terminate(context.Context) error {
	s.terminates++
	return s.terminateErr
}

type fakeOnboardingPostgresTx struct {
	execErrors         []error
	execSQL            []string
	row                onboardingPostgresRow
	rowSQL             []string
	rows               onboardingPostgresRows
	queryErr           error
	querySQL           []string
	commitErr          error
	rollbackErr        error
	commits            int
	rollbacks          int
	rollbackRowsClosed bool
	execHook           func()
}

func (t *fakeOnboardingPostgresTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	t.execSQL = append(t.execSQL, sql)
	if t.execHook != nil {
		hook := t.execHook
		t.execHook = nil
		hook()
	}
	index := len(t.execSQL) - 1
	if index < len(t.execErrors) {
		return pgconn.CommandTag{}, t.execErrors[index]
	}
	return pgconn.CommandTag{}, nil
}

func (t *fakeOnboardingPostgresTx) QueryRow(_ context.Context, sql string, _ ...any) onboardingPostgresRow {
	t.rowSQL = append(t.rowSQL, sql)
	return t.row
}

func (t *fakeOnboardingPostgresTx) Query(_ context.Context, sql string, _ ...any) (onboardingPostgresRows, error) {
	t.querySQL = append(t.querySQL, sql)
	return t.rows, t.queryErr
}

func (t *fakeOnboardingPostgresTx) Commit(context.Context) error {
	t.commits++
	return t.commitErr
}

func (t *fakeOnboardingPostgresTx) Rollback(context.Context) error {
	t.rollbacks++
	if rows, ok := t.rows.(*fakeOnboardingPostgresRows); ok {
		t.rollbackRowsClosed = rows.closed
	}
	return t.rollbackErr
}

type fakeOnboardingPostgresRow struct {
	raw []byte
	err error
}

func (r fakeOnboardingPostgresRow) Scan(destinations ...any) error {
	if r.err != nil {
		return r.err
	}
	*(destinations[0].(*[]byte)) = append([]byte(nil), r.raw...)
	return nil
}

type fakeOnboardingPostgresRows struct {
	values  []fakeOnboardingPostgresListRow
	index   int
	scanErr error
	err     error
	closed  bool
}

type fakeOnboardingPostgresListRow struct {
	id      string
	ownerID string
	raw     []byte
}

func (r *fakeOnboardingPostgresRows) Next() bool { return r.index < len(r.values) }

func (r *fakeOnboardingPostgresRows) Scan(destinations ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	row := r.values[r.index]
	*(destinations[0].(*string)) = row.id
	*(destinations[1].(*string)) = row.ownerID
	*(destinations[2].(*[]byte)) = append([]byte(nil), row.raw...)
	r.index++
	return nil
}

func (r *fakeOnboardingPostgresRows) Err() error { return r.err }
func (r *fakeOnboardingPostgresRows) Close()     { r.closed = true }

func newFakePostgresOnboardingStore(transaction *fakeOnboardingPostgresTx) (*PostgresStore, *fakeOnboardingPostgresSession) {
	session := &fakeOnboardingPostgresSession{transaction: transaction}
	return &PostgresStore{
		operationTimeouts:  defaultOperationTimeouts,
		onboardingPostgres: &fakeOnboardingPostgresOps{session: session},
	}, session
}

func TestPostgresOnboardingSaveClassification(t *testing.T) {
	now := time.Now().UTC()
	receipt := testISCPOnboarding(now, "receipt-postgres-classification", app.DefaultOwnerID)
	tests := []struct {
		name          string
		execErrors    []error
		commitErr     error
		rollbackErr   error
		wantCode      StoreErrorCode
		wantConflict  bool
		wantRollback  int
		wantTerminate int
	}{
		{name: "server duplicate", execErrors: []error{nil, &pgconn.PgError{Code: "23505"}}, wantCode: StoreErrorConflict, wantConflict: true, wantRollback: 1},
		{name: "server rejection", execErrors: []error{&pgconn.PgError{Code: "42501"}}, wantCode: StoreErrorInternal, wantRollback: 1},
		{name: "safe lock transport", execErrors: []error{safePostgresRetryError{errors.New("not sent")}}, wantCode: StoreErrorUnavailable, wantRollback: 1},
		{name: "safe insert transport", execErrors: []error{nil, safePostgresRetryError{errors.New("not sent")}}, wantCode: StoreErrorUnavailable, wantRollback: 1},
		{name: "safe rollback failure closes session", execErrors: []error{safePostgresRetryError{errors.New("not sent")}}, rollbackErr: errors.New("rollback failed"), wantCode: StoreErrorUnavailable, wantRollback: 1, wantTerminate: 1},
		{name: "unsafe lock transport", execErrors: []error{errors.New("submission uncertain")}, wantCode: StoreErrorUnknownOutcome, wantTerminate: 1},
		{name: "unsafe insert transport", execErrors: []error{nil, errors.New("submission uncertain")}, wantCode: StoreErrorUnknownOutcome, wantTerminate: 1},
		{name: "unsafe context outcome", execErrors: []error{context.DeadlineExceeded}, wantCode: StoreErrorUnknownOutcome, wantTerminate: 1},
		{name: "commit error", commitErr: errors.New("commit uncertain"), wantCode: StoreErrorUnknownOutcome, wantTerminate: 1},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			transaction := &fakeOnboardingPostgresTx{
				execErrors: testCase.execErrors, commitErr: testCase.commitErr, rollbackErr: testCase.rollbackErr,
			}
			store, session := newFakePostgresOnboardingStore(transaction)
			_, err := store.SaveISCPOnboarding(context.Background(), receipt)
			if StoreErrorCodeOf(err) != testCase.wantCode {
				t.Fatalf("error = %v code=%q, want %q", err, StoreErrorCodeOf(err), testCase.wantCode)
			}
			if errors.Is(err, ErrISCPOnboardingConflict) != testCase.wantConflict {
				t.Fatalf("conflict preservation = %v, want %v", errors.Is(err, ErrISCPOnboardingConflict), testCase.wantConflict)
			}
			if transaction.rollbacks != testCase.wantRollback || session.terminates != testCase.wantTerminate {
				t.Fatalf("rollback=%d terminate=%d, want %d/%d", transaction.rollbacks, session.terminates, testCase.wantRollback, testCase.wantTerminate)
			}
		})
	}
}

func TestPostgresOnboardingSafeStatementUsesEffectiveContextOutcome(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	transaction := &fakeOnboardingPostgresTx{
		execErrors: []error{safePostgresRetryError{errors.New("statement not sent")}},
		execHook:   cancel,
	}
	store, session := newFakePostgresOnboardingStore(transaction)
	_, err := store.SaveISCPOnboarding(ctx, testISCPOnboarding(time.Now().UTC(), "receipt-safe-canceled", app.DefaultOwnerID))
	if StoreErrorCodeOf(err) != StoreErrorCanceled || transaction.rollbacks != 1 || session.terminates != 0 || session.releases != 1 {
		t.Fatalf("safe canceled statement err=%v code=%q rollback=%d terminate=%d release=%d", err, StoreErrorCodeOf(err), transaction.rollbacks, session.terminates, session.releases)
	}
}

func TestPostgresOnboardingPreTransactionAndCancellationClassification(t *testing.T) {
	store := &PostgresStore{
		operationTimeouts:  defaultOperationTimeouts,
		onboardingPostgres: &fakeOnboardingPostgresOps{acquireErr: errors.New("pool unavailable")},
	}
	if _, err := store.SaveISCPOnboarding(context.Background(), testISCPOnboarding(time.Now().UTC(), "receipt-pool", app.DefaultOwnerID)); StoreErrorCodeOf(err) != StoreErrorUnavailable {
		t.Fatalf("pool acquire error = %v code=%q", err, StoreErrorCodeOf(err))
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.SaveISCPOnboarding(canceled, testISCPOnboarding(time.Now().UTC(), "receipt-canceled", app.DefaultOwnerID)); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("canceled save error = %v code=%q", err, StoreErrorCodeOf(err))
	}

	transaction := &fakeOnboardingPostgresTx{}
	store, session := newFakePostgresOnboardingStore(transaction)
	session.beginErr = errors.New("begin unavailable")
	if _, err := store.SaveISCPOnboarding(context.Background(), testISCPOnboarding(time.Now().UTC(), "receipt-begin", app.DefaultOwnerID)); StoreErrorCodeOf(err) != StoreErrorUnavailable {
		t.Fatalf("begin error = %v code=%q", err, StoreErrorCodeOf(err))
	}
	if session.releases != 1 || session.terminates != 0 {
		t.Fatalf("begin failure session cleanup releases=%d terminates=%d", session.releases, session.terminates)
	}
}

func TestPostgresOnboardingGetUsesReadCommittedLockBarrier(t *testing.T) {
	receipt := testISCPOnboarding(time.Now().UTC(), "receipt-barrier", app.DefaultOwnerID)
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name  string
		row   onboardingPostgresRow
		found bool
	}{
		{name: "found", row: fakeOnboardingPostgresRow{raw: raw}, found: true},
		{name: "absence", row: fakeOnboardingPostgresRow{err: pgx.ErrNoRows}, found: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			transaction := &fakeOnboardingPostgresTx{row: testCase.row}
			store, session := newFakePostgresOnboardingStore(transaction)
			got, found, err := store.GetISCPOnboarding(context.Background(), receipt.ID)
			if err != nil || found != testCase.found || (found && got.ID != receipt.ID) {
				t.Fatalf("get = %#v found=%v err=%v", got, found, err)
			}
			if len(session.options) != 1 || session.options[0].IsoLevel != pgx.ReadCommitted {
				t.Fatalf("transaction options = %#v", session.options)
			}
			if len(transaction.execSQL) != 1 || len(transaction.rowSQL) != 1 || transaction.commits != 1 {
				t.Fatalf("barrier statements exec=%v query=%v commits=%d", transaction.execSQL, transaction.rowSQL, transaction.commits)
			}
		})
	}
}

func TestPostgresOnboardingGetClassifiesFailuresAndOwnsSession(t *testing.T) {
	receipt := testISCPOnboarding(time.Now().UTC(), "receipt-get-failure", app.DefaultOwnerID)
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name          string
		execErrors    []error
		row           onboardingPostgresRow
		commitErr     error
		rollbackErr   error
		wantCode      StoreErrorCode
		wantRollback  int
		wantTerminate int
		wantRelease   int
	}{
		{name: "lock", execErrors: []error{errors.New("lock unavailable")}, row: fakeOnboardingPostgresRow{}, wantCode: StoreErrorUnavailable, wantRollback: 1, wantRelease: 1},
		{name: "scan timeout", row: fakeOnboardingPostgresRow{err: context.DeadlineExceeded}, wantCode: StoreErrorTimeout, wantRollback: 1, wantRelease: 1},
		{name: "corrupt payload", row: fakeOnboardingPostgresRow{raw: []byte("not-json")}, wantCode: StoreErrorCorrupt, wantRollback: 1, wantRelease: 1},
		{name: "rollback failure", row: fakeOnboardingPostgresRow{err: errors.New("scan failed")}, rollbackErr: errors.New("rollback failed"), wantCode: StoreErrorUnavailable, wantRollback: 1, wantTerminate: 1},
		{name: "absence commit", row: fakeOnboardingPostgresRow{err: pgx.ErrNoRows}, commitErr: errors.New("commit failed"), wantCode: StoreErrorUnavailable, wantTerminate: 1},
		{name: "found commit", row: fakeOnboardingPostgresRow{raw: raw}, commitErr: errors.New("commit failed"), wantCode: StoreErrorUnavailable, wantTerminate: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			transaction := &fakeOnboardingPostgresTx{
				execErrors: testCase.execErrors, row: testCase.row, commitErr: testCase.commitErr, rollbackErr: testCase.rollbackErr,
			}
			store, session := newFakePostgresOnboardingStore(transaction)
			_, _, err := store.GetISCPOnboarding(context.Background(), receipt.ID)
			if StoreErrorCodeOf(err) != testCase.wantCode {
				t.Fatalf("error = %v code=%q, want %q", err, StoreErrorCodeOf(err), testCase.wantCode)
			}
			if transaction.rollbacks != testCase.wantRollback || session.terminates != testCase.wantTerminate || session.releases != testCase.wantRelease {
				t.Fatalf("rollback=%d terminate=%d release=%d, want %d/%d/%d", transaction.rollbacks, session.terminates, session.releases, testCase.wantRollback, testCase.wantTerminate, testCase.wantRelease)
			}
		})
	}
}

func TestPostgresOnboardingListReturnsRowsError(t *testing.T) {
	receipt := testISCPOnboarding(time.Now().UTC(), "receipt-list", app.DefaultOwnerID)
	valid, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	validRow := fakeOnboardingPostgresListRow{id: receipt.ID, ownerID: receipt.OwnerID, raw: valid}
	for _, testCase := range []struct {
		name          string
		rows          *fakeOnboardingPostgresRows
		queryErr      error
		commitErr     error
		wantCode      StoreErrorCode
		wantRollback  int
		wantTerminate int
		wantRelease   int
	}{
		{name: "query", rows: &fakeOnboardingPostgresRows{}, queryErr: errors.New("query failed"), wantCode: StoreErrorUnavailable, wantRollback: 1, wantRelease: 1},
		{name: "scan", rows: &fakeOnboardingPostgresRows{values: []fakeOnboardingPostgresListRow{validRow}, scanErr: errors.New("scan failed")}, wantCode: StoreErrorUnavailable, wantRollback: 1, wantRelease: 1},
		{name: "scan context", rows: &fakeOnboardingPostgresRows{values: []fakeOnboardingPostgresListRow{validRow}, scanErr: context.Canceled}, wantCode: StoreErrorCanceled, wantRollback: 1, wantRelease: 1},
		{name: "decode", rows: &fakeOnboardingPostgresRows{values: []fakeOnboardingPostgresListRow{{id: receipt.ID, ownerID: receipt.OwnerID, raw: []byte("not-json")}}}, wantCode: StoreErrorCorrupt, wantRollback: 1, wantRelease: 1},
		{name: "row id mismatch", rows: &fakeOnboardingPostgresRows{values: []fakeOnboardingPostgresListRow{{id: "different-row", ownerID: receipt.OwnerID, raw: valid}}}, wantCode: StoreErrorCorrupt, wantRollback: 1, wantRelease: 1},
		{name: "row owner mismatch", rows: &fakeOnboardingPostgresRows{values: []fakeOnboardingPostgresListRow{{id: receipt.ID, ownerID: "different-owner", raw: valid}}}, wantCode: StoreErrorCorrupt, wantRollback: 1, wantRelease: 1},
		{name: "rows", rows: &fakeOnboardingPostgresRows{err: errors.New("rows failed")}, wantCode: StoreErrorUnavailable, wantRollback: 1, wantRelease: 1},
		{name: "commit", rows: &fakeOnboardingPostgresRows{}, commitErr: errors.New("commit failed"), wantCode: StoreErrorUnavailable, wantTerminate: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			transaction := &fakeOnboardingPostgresTx{rows: testCase.rows, queryErr: testCase.queryErr, commitErr: testCase.commitErr}
			store, session := newFakePostgresOnboardingStore(transaction)
			listed, err := store.ListISCPOnboardings(context.Background(), app.DefaultOwnerID)
			if listed != nil || StoreErrorCodeOf(err) != testCase.wantCode {
				t.Fatalf("list = %#v err=%v code=%q, want %q", listed, err, StoreErrorCodeOf(err), testCase.wantCode)
			}
			if transaction.rollbacks != testCase.wantRollback || session.terminates != testCase.wantTerminate || session.releases != testCase.wantRelease {
				t.Fatalf("rollback=%d terminate=%d release=%d, want %d/%d/%d", transaction.rollbacks, session.terminates, session.releases, testCase.wantRollback, testCase.wantTerminate, testCase.wantRelease)
			}
			if testCase.queryErr == nil && !testCase.rows.closed {
				t.Fatal("list rows were not closed")
			}
			if testCase.wantRollback > 0 && testCase.queryErr == nil && !transaction.rollbackRowsClosed {
				t.Fatal("list rolled back before closing rows")
			}
		})
	}
}

func TestPostgresOnboardingBarrierIgnoresSessionDefaultIsolation(t *testing.T) {
	baseDSN := os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN")
	if baseDSN == "" {
		t.Skip("set SPARKCLAW_TEST_POSTGRES_DSN to run postgres store integration tests")
	}
	for _, isolation := range []string{"repeatable read", "serializable"} {
		t.Run(isolation, func(t *testing.T) {
			dsn := postgresTestDSNWithParameter(t, baseDSN, "default_transaction_isolation", isolation)
			store, err := NewPostgresStore(context.Background(), dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			truncatePostgresStore(t, store)
			receipt := testISCPOnboarding(time.Now().UTC(), "receipt-isolation-"+isolation, app.DefaultOwnerID)
			if _, err := store.SaveISCPOnboarding(context.Background(), receipt); err != nil {
				t.Fatal(err)
			}
			if got, found, err := store.GetISCPOnboarding(context.Background(), receipt.ID); err != nil || !found || got.ID != receipt.ID {
				t.Fatalf("found barrier = %#v found=%v err=%v", got, found, err)
			}
			if _, found, err := store.GetISCPOnboarding(context.Background(), "missing-"+receipt.ID); err != nil || found {
				t.Fatalf("absence barrier found=%v err=%v", found, err)
			}
		})
	}
}

func TestPostgresOnboardingPoolAcquireHonorsReadDeadline(t *testing.T) {
	baseDSN := os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN")
	if baseDSN == "" {
		t.Skip("set SPARKCLAW_TEST_POSTGRES_DSN to run postgres store integration tests")
	}
	dsn := postgresTestDSNWithParameter(t, baseDSN, "pool_max_conns", "1")
	store, err := NewPostgresStore(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	connection, err := store.db.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, _, err := store.GetISCPOnboarding(ctx, "pool-blocked"); StoreErrorCodeOf(err) != StoreErrorTimeout {
		t.Fatalf("pool acquisition deadline error = %v code=%q", err, StoreErrorCodeOf(err))
	}
}

func postgresTestDSNWithParameter(t *testing.T, baseDSN, key, value string) string {
	t.Helper()
	if strings.Contains(baseDSN, "://") {
		dsn, err := url.Parse(baseDSN)
		if err != nil {
			t.Fatal(err)
		}
		query := dsn.Query()
		query.Set(key, value)
		dsn.RawQuery = query.Encode()
		return dsn.String()
	}
	if strings.ContainsAny(value, `'\\`) {
		t.Fatalf("unsupported PostgreSQL test parameter value %q", value)
	}
	return strings.TrimSpace(baseDSN) + " " + key + "='" + value + "'"
}
