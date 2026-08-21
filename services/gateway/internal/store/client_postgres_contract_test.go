package store

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/sync/semaphore"
)

type fakeClientPostgresOps struct {
	session    onboardingPostgresSession
	acquireErr error
	rows       onboardingPostgresRows
	queryErr   error
	rowQueue   []onboardingPostgresRow
	querySQL   []string
	rowSQL     []string
}

func (o *fakeClientPostgresOps) Acquire(context.Context) (onboardingPostgresSession, error) {
	if o.acquireErr != nil {
		return nil, o.acquireErr
	}
	return o.session, nil
}

func (o *fakeClientPostgresOps) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (o *fakeClientPostgresOps) Query(_ context.Context, sql string, _ ...any) (onboardingPostgresRows, error) {
	o.querySQL = append(o.querySQL, sql)
	return o.rows, o.queryErr
}

func (o *fakeClientPostgresOps) QueryRow(_ context.Context, sql string, _ ...any) onboardingPostgresRow {
	o.rowSQL = append(o.rowSQL, sql)
	if len(o.rowQueue) == 0 {
		return fakeClientPostgresRow{err: errors.New("unexpected QueryRow")}
	}
	row := o.rowQueue[0]
	o.rowQueue = o.rowQueue[1:]
	return row
}

type fakeClientPostgresSession struct {
	transaction  onboardingPostgresTx
	beginErr     error
	options      []pgx.TxOptions
	releases     int
	terminates   int
	terminateErr error
}

func (s *fakeClientPostgresSession) Begin(_ context.Context, options pgx.TxOptions) (onboardingPostgresTx, error) {
	s.options = append(s.options, options)
	if s.beginErr != nil {
		return nil, s.beginErr
	}
	return s.transaction, nil
}

func (s *fakeClientPostgresSession) Release() { s.releases++ }

func (s *fakeClientPostgresSession) Terminate(context.Context) error {
	s.terminates++
	return s.terminateErr
}

type fakeClientPostgresTx struct {
	execErrors  []error
	execSQL     []string
	rowQueue    []onboardingPostgresRow
	rowSQL      []string
	rows        onboardingPostgresRows
	queryErr    error
	querySQL    []string
	commitErr   error
	rollbackErr error
	commits     int
	rollbacks   int
}

func (t *fakeClientPostgresTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	t.execSQL = append(t.execSQL, sql)
	index := len(t.execSQL) - 1
	if index < len(t.execErrors) {
		return pgconn.CommandTag{}, t.execErrors[index]
	}
	return pgconn.CommandTag{}, nil
}

func (t *fakeClientPostgresTx) QueryRow(_ context.Context, sql string, _ ...any) onboardingPostgresRow {
	t.rowSQL = append(t.rowSQL, sql)
	if len(t.rowQueue) == 0 {
		return fakeClientPostgresRow{err: errors.New("unexpected transaction QueryRow")}
	}
	row := t.rowQueue[0]
	t.rowQueue = t.rowQueue[1:]
	return row
}

func (t *fakeClientPostgresTx) Query(_ context.Context, sql string, _ ...any) (onboardingPostgresRows, error) {
	t.querySQL = append(t.querySQL, sql)
	return t.rows, t.queryErr
}

func (t *fakeClientPostgresTx) Commit(context.Context) error {
	t.commits++
	return t.commitErr
}

func (t *fakeClientPostgresTx) Rollback(context.Context) error {
	t.rollbacks++
	return t.rollbackErr
}

type fakeClientPostgresRow struct {
	client  *app.Client
	pairing *app.PairingCode
	err     error
}

func clientPostgresRow(client app.Client) fakeClientPostgresRow {
	client = cloneClient(client)
	return fakeClientPostgresRow{client: &client}
}

func pairingPostgresRow(code app.PairingCode) fakeClientPostgresRow {
	code = clonePairingCode(code)
	return fakeClientPostgresRow{pairing: &code}
}

func (r fakeClientPostgresRow) Scan(destinations ...any) error {
	if r.err != nil {
		return r.err
	}
	if r.client != nil && len(destinations) == 8 {
		client := cloneClient(*r.client)
		*(destinations[0].(*string)) = client.ID
		*(destinations[1].(*string)) = client.OwnerID
		*(destinations[2].(*string)) = client.ActorID
		*(destinations[3].(*string)) = client.Name
		*(destinations[4].(*string)) = client.TokenHash
		*(destinations[5].(*time.Time)) = client.CreatedAt
		*(destinations[6].(**time.Time)) = cloneTimePointer(client.LastSeenAt)
		*(destinations[7].(**time.Time)) = cloneTimePointer(client.RevokedAt)
		return nil
	}
	if r.pairing != nil && len(destinations) == 7 {
		code := clonePairingCode(*r.pairing)
		*(destinations[0].(*string)) = code.ID
		*(destinations[1].(*string)) = code.CodeHash
		*(destinations[2].(*string)) = code.Status
		*(destinations[3].(*time.Time)) = code.ExpiresAt
		*(destinations[4].(*time.Time)) = code.CreatedAt
		*(destinations[5].(**time.Time)) = cloneTimePointer(code.ClaimedAt)
		*(destinations[6].(*string)) = code.ClientID
		return nil
	}
	return errors.New("fake row shape mismatch")
}

type fakeClientPostgresRows struct {
	values []fakeClientPostgresRow
	index  int
	err    error
	closed bool
}

func (r *fakeClientPostgresRows) Next() bool { return r.index < len(r.values) }

func (r *fakeClientPostgresRows) Scan(destinations ...any) error {
	row := r.values[r.index]
	r.index++
	return row.Scan(destinations...)
}

func (r *fakeClientPostgresRows) Err() error { return r.err }
func (r *fakeClientPostgresRows) Close()     { r.closed = true }

func newFakePostgresClientStore(now time.Time, transaction *fakeClientPostgresTx) (*PostgresStore, *fakeClientPostgresOps, *fakeClientPostgresSession) {
	session := &fakeClientPostgresSession{transaction: transaction}
	operations := &fakeClientPostgresOps{session: session}
	return &PostgresStore{
		operationTimeouts:    defaultOperationTimeouts,
		clientPostgres:       operations,
		clientCommandGate:    semaphore.NewWeighted(1),
		clientWriteHighWater: map[string]time.Time{}, pairingWriteHighWater: map[string]time.Time{},
		clientNow: func() time.Time { return now },
	}, operations, session
}

func validPostgresClient(now time.Time) app.Client {
	return app.Client{
		ID: "client-postgres", OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID,
		Name: "Postgres Client", TokenHash: "client-postgres-hash", CreatedAt: now.Add(-time.Hour),
	}
}

func validPostgresPairing(now time.Time) app.PairingCode {
	return app.PairingCode{
		ID: "pair-postgres", CodeHash: "pair-postgres-hash", Status: "pending",
		CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
	}
}

func TestPostgresClientBeginFailureOwnsAcquiredSession(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	unsafeFailure := errors.New("begin outcome unknown")
	terminateFailure := errors.New("terminate failed")
	for _, testCase := range []struct {
		name          string
		failure       error
		terminateErr  error
		wantCode      StoreErrorCode
		wantRelease   int
		wantTerminate int
		wantCauses    []error
	}{
		{name: "safe transport", failure: safePostgresRetryError{errors.New("begin not sent")}, wantCode: StoreErrorUnavailable, wantRelease: 1},
		{name: "server rejection", failure: &pgconn.PgError{Code: "40001", Message: "rejected"}, wantCode: StoreErrorInternal, wantRelease: 1},
		{name: "unsafe transport", failure: unsafeFailure, terminateErr: terminateFailure, wantCode: StoreErrorUnknownOutcome, wantTerminate: 1, wantCauses: []error{unsafeFailure, terminateFailure}},
		{name: "context failure after acquire", failure: context.Canceled, wantCode: StoreErrorUnknownOutcome, wantTerminate: 1, wantCauses: []error{context.Canceled}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			store, _, session := newFakePostgresClientStore(now, &fakeClientPostgresTx{})
			session.beginErr = testCase.failure
			session.terminateErr = testCase.terminateErr
			_, err := store.RevokeClient(t.Context(), "client-postgres")
			if StoreErrorCodeOf(err) != testCase.wantCode {
				t.Fatalf("code = %q, want %q: %v", StoreErrorCodeOf(err), testCase.wantCode, err)
			}
			if session.releases != testCase.wantRelease || session.terminates != testCase.wantTerminate {
				t.Fatalf("release=%d terminate=%d, want %d/%d", session.releases, session.terminates, testCase.wantRelease, testCase.wantTerminate)
			}
			if session.releases != 0 && session.terminates != 0 {
				t.Fatal("terminated PostgreSQL session was released")
			}
			for _, cause := range testCase.wantCauses {
				if !errors.Is(err, cause) {
					t.Fatalf("error %v does not retain %v", err, cause)
				}
			}
		})
	}
}

func TestPostgresClientCommandAdmissionHonorsDeadline(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	for _, testCase := range []struct {
		name   string
		invoke func(context.Context, *PostgresStore) error
	}{
		{name: "revoke", invoke: func(ctx context.Context, store *PostgresStore) error {
			_, err := store.RevokeClient(ctx, "client-postgres")
			return err
		}},
		{name: "touch", invoke: func(ctx context.Context, store *PostgresStore) error {
			_, _, err := store.TouchClient(ctx, "client-postgres")
			return err
		}},
		{name: "save pairing", invoke: func(ctx context.Context, store *PostgresStore) error {
			_, err := store.SavePairingCode(ctx, validPostgresPairing(now))
			return err
		}},
		{name: "claim pairing", invoke: func(ctx context.Context, store *PostgresStore) error {
			client := validPostgresClient(now)
			client.CreatedAt = time.Time{}
			_, _, err := store.ClaimPairingCode(ctx, "pair-postgres", client)
			return err
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			store, _, _ := newFakePostgresClientStore(now, &fakeClientPostgresTx{})
			if err := store.clientCommandGate.Acquire(t.Context(), 1); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- testCase.invoke(ctx, store) }()
			select {
			case err := <-result:
				store.clientCommandGate.Release(1)
				if StoreErrorCodeOf(err) != StoreErrorTimeout {
					t.Fatalf("code = %q, want %q: %v", StoreErrorCodeOf(err), StoreErrorTimeout, err)
				}
			case <-time.After(time.Second):
				store.clientCommandGate.Release(1)
				t.Fatal("PostgreSQL client command admission ignored its deadline")
			}
		})
	}
}

func TestPostgresClientGetBarriersUseReadCommitted(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	for _, testCase := range []struct {
		name      string
		operation StoreOperation
		row       onboardingPostgresRow
		invoke    func(*PostgresStore) (bool, error)
	}{
		{
			name: "client found", operation: OperationClientGet, row: clientPostgresRow(validPostgresClient(now)),
			invoke: func(store *PostgresStore) (bool, error) {
				_, found, err := store.GetClient(t.Context(), "client-postgres")
				return found, err
			},
		},
		{
			name: "client absent", operation: OperationClientGet, row: fakeClientPostgresRow{err: pgx.ErrNoRows},
			invoke: func(store *PostgresStore) (bool, error) {
				_, found, err := store.GetClient(t.Context(), "missing")
				return found, err
			},
		},
		{
			name: "pairing found", operation: OperationPairingCodeGet, row: pairingPostgresRow(validPostgresPairing(now)),
			invoke: func(store *PostgresStore) (bool, error) {
				_, found, err := store.GetPairingCode(t.Context(), "pair-postgres")
				return found, err
			},
		},
		{
			name: "pairing absent", operation: OperationPairingCodeGet, row: fakeClientPostgresRow{err: pgx.ErrNoRows},
			invoke: func(store *PostgresStore) (bool, error) {
				_, found, err := store.GetPairingCode(t.Context(), "missing")
				return found, err
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			transaction := &fakeClientPostgresTx{rowQueue: []onboardingPostgresRow{testCase.row}}
			store, _, session := newFakePostgresClientStore(now, transaction)
			found, err := testCase.invoke(store)
			wantFound := !strings.Contains(testCase.name, "absent")
			if err != nil || found != wantFound {
				t.Fatalf("found=%v err=%v", found, err)
			}
			if len(session.options) != 1 || session.options[0].IsoLevel != pgx.ReadCommitted ||
				len(transaction.execSQL) != 1 || !strings.Contains(transaction.execSQL[0], "pg_advisory_xact_lock") ||
				len(transaction.rowSQL) != 1 || transaction.commits != 1 || transaction.rollbacks != 0 || session.releases != 1 {
				t.Fatalf("operation=%s options=%#v exec=%v rows=%v commit=%d rollback=%d release=%d",
					testCase.operation, session.options, transaction.execSQL, transaction.rowSQL, transaction.commits, transaction.rollbacks, session.releases)
			}
		})
	}
}

func TestPostgresClientListAndFindClassifyReadFailures(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	valid := validPostgresClient(now)
	corrupt := valid
	corrupt.CreatedAt = time.Time{}
	for _, testCase := range []struct {
		name     string
		rows     *fakeClientPostgresRows
		queryErr error
		wantCode StoreErrorCode
	}{
		{name: "query transport", rows: &fakeClientPostgresRows{}, queryErr: errors.New("query transport"), wantCode: StoreErrorUnavailable},
		{name: "scan transport", rows: &fakeClientPostgresRows{values: []fakeClientPostgresRow{{err: errors.New("scan transport")}}}, wantCode: StoreErrorUnavailable},
		{name: "scan context", rows: &fakeClientPostgresRows{values: []fakeClientPostgresRow{{err: context.Canceled}}}, wantCode: StoreErrorCanceled},
		{name: "corrupt row", rows: &fakeClientPostgresRows{values: []fakeClientPostgresRow{clientPostgresRow(corrupt)}}, wantCode: StoreErrorCorrupt},
		{name: "rows transport", rows: &fakeClientPostgresRows{err: errors.New("rows transport")}, wantCode: StoreErrorUnavailable},
	} {
		t.Run("list "+testCase.name, func(t *testing.T) {
			store, operations, _ := newFakePostgresClientStore(now, &fakeClientPostgresTx{})
			operations.rows, operations.queryErr = testCase.rows, testCase.queryErr
			listed, err := store.ListClients(t.Context())
			if listed != nil || StoreErrorCodeOf(err) != testCase.wantCode {
				t.Fatalf("list=%#v err=%v code=%q", listed, err, StoreErrorCodeOf(err))
			}
			if testCase.queryErr == nil && !testCase.rows.closed {
				t.Fatal("list rows were not closed")
			}
		})
	}
	for _, testCase := range []struct {
		name     string
		row      onboardingPostgresRow
		wantCode StoreErrorCode
	}{
		{name: "scan transport", row: fakeClientPostgresRow{err: errors.New("scan transport")}, wantCode: StoreErrorUnavailable},
		{name: "scan timeout", row: fakeClientPostgresRow{err: context.DeadlineExceeded}, wantCode: StoreErrorTimeout},
		{name: "corrupt row", row: clientPostgresRow(corrupt), wantCode: StoreErrorCorrupt},
	} {
		t.Run("find "+testCase.name, func(t *testing.T) {
			store, operations, _ := newFakePostgresClientStore(now, &fakeClientPostgresTx{})
			operations.rowQueue = []onboardingPostgresRow{testCase.row}
			client, found, err := store.FindClientByTokenHash(t.Context(), valid.TokenHash)
			if found || client.ID != "" || StoreErrorCodeOf(err) != testCase.wantCode {
				t.Fatalf("client=%#v found=%v err=%v code=%q", client, found, err, StoreErrorCodeOf(err))
			}
		})
	}
}

func TestPostgresClientReferencedClientScanTransportIsNotCorrupt(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	claimedAt := now.Add(-time.Minute)
	code := validPostgresPairing(now)
	code.Status, code.ClientID, code.ClaimedAt = "claimed", "referenced-client", &claimedAt
	transaction := &fakeClientPostgresTx{rowQueue: []onboardingPostgresRow{
		pairingPostgresRow(code), fakeClientPostgresRow{err: errors.New("referenced client transport")},
	}}
	store, _, session := newFakePostgresClientStore(now, transaction)
	got, found, err := store.GetPairingCode(t.Context(), code.ID)
	if found || got.ID != "" || StoreErrorCodeOf(err) != StoreErrorUnavailable || transaction.rollbacks != 1 || session.releases != 1 || session.terminates != 0 {
		t.Fatalf("pairing=%#v found=%v err=%v code=%q rollback=%d release=%d terminate=%d",
			got, found, err, StoreErrorCodeOf(err), transaction.rollbacks, session.releases, session.terminates)
	}
}

type fakeClientCommandResult struct {
	candidate bool
	err       error
}

func invokeFakePostgresClientCommand(t *testing.T, name string, store *PostgresStore, now time.Time) fakeClientCommandResult {
	t.Helper()
	switch name {
	case "save pairing":
		code, err := store.SavePairingCode(t.Context(), app.PairingCode{ID: "pair-postgres", CodeHash: "pair-postgres-hash", ExpiresAt: now.Add(time.Hour)})
		return fakeClientCommandResult{candidate: code.ID != "", err: err}
	case "revoke":
		client, err := store.RevokeClient(t.Context(), "client-postgres")
		return fakeClientCommandResult{candidate: client.ID != "", err: err}
	case "touch":
		client, found, err := store.TouchClient(t.Context(), "client-postgres")
		return fakeClientCommandResult{candidate: found || client.ID != "", err: err}
	case "claim":
		code, client, err := store.ClaimPairingCode(t.Context(), "pair-postgres", app.Client{ID: "claimed-client", Name: "Claimed Client", TokenHash: "claimed-token-hash"})
		return fakeClientCommandResult{candidate: code.ID != "" || client.ID != "", err: err}
	default:
		t.Fatalf("unknown command %q", name)
		return fakeClientCommandResult{}
	}
}

func fakePostgresClientCommandTx(name string, now time.Time) (*fakeClientPostgresTx, int) {
	client := validPostgresClient(now)
	pairing := validPostgresPairing(now)
	switch name {
	case "save pairing":
		return &fakeClientPostgresTx{rowQueue: []onboardingPostgresRow{fakeClientPostgresRow{err: pgx.ErrNoRows}}}, 1
	case "revoke", "touch":
		return &fakeClientPostgresTx{rowQueue: []onboardingPostgresRow{clientPostgresRow(client)}}, 1
	case "claim":
		return &fakeClientPostgresTx{rowQueue: []onboardingPostgresRow{pairingPostgresRow(pairing), fakeClientPostgresRow{err: pgx.ErrNoRows}}}, 2
	default:
		panic("unknown fake client command")
	}
}

func TestPostgresClientCommandFailureClassificationAndSessionOwnership(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	for _, command := range []string{"save pairing", "revoke", "touch", "claim"} {
		t.Run(command, func(t *testing.T) {
			for _, testCase := range []struct {
				name          string
				failure       error
				postCandidate bool
				commit        bool
				rollbackErr   error
				wantCode      StoreErrorCode
				wantCandidate bool
				wantRollback  int
				wantTerminate int
				wantRelease   int
			}{
				{name: "safe pre-candidate statement", failure: safePostgresRetryError{errors.New("not sent")}, wantCode: StoreErrorUnavailable, wantRollback: 1, wantRelease: 1},
				{name: "server pre-candidate rejection", failure: &pgconn.PgError{Code: "42501"}, wantCode: StoreErrorInternal, wantRollback: 1, wantRelease: 1},
				{name: "unsafe pre-candidate statement", failure: errors.New("submission uncertain"), wantCode: StoreErrorUnknownOutcome, wantTerminate: 1},
				{name: "safe post-candidate statement", failure: safePostgresRetryError{errors.New("not sent")}, postCandidate: true, wantCode: StoreErrorUnavailable, wantRollback: 1, wantRelease: 1},
				{name: "server unique post-candidate", failure: &pgconn.PgError{Code: "23505"}, postCandidate: true, wantCode: StoreErrorConflict, wantRollback: 1, wantRelease: 1},
				{name: "unsafe post-candidate statement", failure: errors.New("submission uncertain"), postCandidate: true, wantCode: StoreErrorUnknownOutcome, wantCandidate: true, wantTerminate: 1},
				{name: "commit unknown", commit: true, wantCode: StoreErrorUnknownOutcome, wantCandidate: true, wantTerminate: 1},
				{name: "rollback failure terminates", failure: safePostgresRetryError{errors.New("not sent")}, postCandidate: true, rollbackErr: errors.New("rollback failed"), wantCode: StoreErrorUnavailable, wantRollback: 1, wantTerminate: 1},
			} {
				t.Run(testCase.name, func(t *testing.T) {
					transaction, postIndex := fakePostgresClientCommandTx(command, now)
					if testCase.commit {
						transaction.commitErr = errors.New("commit uncertain")
					} else {
						failureIndex := 0
						if testCase.postCandidate {
							failureIndex = postIndex
						}
						transaction.execErrors = make([]error, failureIndex+1)
						transaction.execErrors[failureIndex] = testCase.failure
					}
					transaction.rollbackErr = testCase.rollbackErr
					store, _, session := newFakePostgresClientStore(now, transaction)
					result := invokeFakePostgresClientCommand(t, command, store, now)
					if StoreErrorCodeOf(result.err) != testCase.wantCode || result.candidate != testCase.wantCandidate {
						t.Fatalf("candidate=%v err=%v code=%q want candidate=%v code=%q", result.candidate, result.err, StoreErrorCodeOf(result.err), testCase.wantCandidate, testCase.wantCode)
					}
					if transaction.rollbacks != testCase.wantRollback || session.terminates != testCase.wantTerminate || session.releases != testCase.wantRelease {
						t.Fatalf("rollback=%d terminate=%d release=%d want %d/%d/%d", transaction.rollbacks, session.terminates, session.releases, testCase.wantRollback, testCase.wantTerminate, testCase.wantRelease)
					}
					if session.terminates > 0 && session.releases != 0 {
						t.Fatal("terminated PostgreSQL session was released")
					}
				})
			}
		})
	}
}

func TestPostgresClientHighWaterDoesNotRollback(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	for _, command := range []string{"save pairing", "revoke"} {
		t.Run(command, func(t *testing.T) {
			failedTx, postIndex := fakePostgresClientCommandTx(command, now)
			failedTx.execErrors = make([]error, postIndex+1)
			failedTx.execErrors[postIndex] = safePostgresRetryError{errors.New("not sent")}
			store, operations, _ := newFakePostgresClientStore(now, failedTx)
			result := invokeFakePostgresClientCommand(t, command, store, now)
			if StoreErrorCodeOf(result.err) != StoreErrorUnavailable || result.candidate {
				t.Fatalf("failed command candidate=%v err=%v", result.candidate, result.err)
			}
			var failedMark time.Time
			if command == "save pairing" {
				failedMark = store.pairingWriteHighWater["pair-postgres"]
			} else {
				failedMark = store.clientWriteHighWater["client-postgres"]
			}
			successTx, _ := fakePostgresClientCommandTx(command, now)
			session := &fakeClientPostgresSession{transaction: successTx}
			operations.session = session
			result = invokeFakePostgresClientCommand(t, command, store, now)
			if result.err != nil || !result.candidate {
				t.Fatalf("success candidate=%v err=%v", result.candidate, result.err)
			}
			var nextMark time.Time
			if command == "save pairing" {
				nextMark = store.pairingWriteHighWater["pair-postgres"]
			} else {
				nextMark = store.clientWriteHighWater["client-postgres"]
			}
			if failedMark.IsZero() || !nextMark.After(failedMark) {
				t.Fatalf("high-water failed=%s next=%s", failedMark, nextMark)
			}
		})
	}
}

func TestPostgresClientLifecycleStatementsAreAtomicAndOrdered(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	for _, testCase := range []struct {
		command string
		wantSQL []string
	}{
		{command: "save pairing", wantSQL: []string{"INSERT INTO pairing_codes", "INSERT INTO audit_events", "INSERT INTO events"}},
		{command: "revoke", wantSQL: []string{"UPDATE clients", "INSERT INTO audit_events", "INSERT INTO events"}},
		{command: "touch", wantSQL: []string{"UPDATE clients"}},
		{command: "claim", wantSQL: []string{"INSERT INTO clients", "UPDATE pairing_codes", "'client.saved'", "'pairing_code.claimed'", "'client.saved'", "'pairing_code.claimed'"}},
	} {
		t.Run(testCase.command, func(t *testing.T) {
			transaction, firstEffect := fakePostgresClientCommandTx(testCase.command, now)
			store, _, session := newFakePostgresClientStore(now, transaction)
			result := invokeFakePostgresClientCommand(t, testCase.command, store, now)
			if result.err != nil || !result.candidate || transaction.commits != 1 || transaction.rollbacks != 0 || session.releases != 1 {
				t.Fatalf("candidate=%v err=%v commit=%d rollback=%d release=%d", result.candidate, result.err, transaction.commits, transaction.rollbacks, session.releases)
			}
			effects := transaction.execSQL[firstEffect:]
			if len(effects) != len(testCase.wantSQL) {
				t.Fatalf("effect statements=%v want %v", effects, testCase.wantSQL)
			}
			for index, fragment := range testCase.wantSQL {
				if !strings.Contains(effects[index], fragment) {
					t.Fatalf("effect %d = %q, want fragment %q", index, effects[index], fragment)
				}
			}
		})
	}
}

func TestPostgresClientRealDatabaseContract(t *testing.T) {
	dsn := os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN")
	if strings.TrimSpace(dsn) == "" {
		t.Skip("set SPARKCLAW_TEST_POSTGRES_DSN to run postgres store integration tests")
	}
	store, err := NewPostgresStore(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.db.Exec(t.Context(), `TRUNCATE pairing_codes, clients, events, audit_events RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}

	if clients, err := store.ListClients(t.Context()); err != nil || clients == nil || len(clients) != 0 {
		t.Fatalf("empty list=%#v err=%v", clients, err)
	}
	if _, found, err := store.GetClient(t.Context(), "missing"); err != nil || found {
		t.Fatalf("missing client found=%v err=%v", found, err)
	}
	if _, found, err := store.FindClientByTokenHash(t.Context(), "missing"); err != nil || found {
		t.Fatalf("missing hash found=%v err=%v", found, err)
	}
	if _, found, err := store.TouchClient(t.Context(), "missing"); err != nil || found {
		t.Fatalf("missing touch found=%v err=%v", found, err)
	}
	if _, err := store.RevokeClient(t.Context(), "missing"); StoreErrorCodeOf(err) != StoreErrorNotFound {
		t.Fatalf("missing revoke err=%v code=%q", err, StoreErrorCodeOf(err))
	}

	pairing, err := store.SavePairingCode(t.Context(), app.PairingCode{ID: "pair-real", CodeHash: "pair-real-hash", ExpiresAt: time.Now().UTC().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if got, found, err := store.GetPairingCode(t.Context(), pairing.ID); err != nil || !found || !PairingCodesEqual(got, pairing) {
		t.Fatalf("pairing=%#v found=%v err=%v", got, found, err)
	}
	if _, err := store.SavePairingCode(t.Context(), app.PairingCode{ID: pairing.ID, CodeHash: "different-hash", ExpiresAt: pairing.ExpiresAt}); StoreErrorCodeOf(err) != StoreErrorConflict {
		t.Fatalf("duplicate pairing ID err=%v code=%q", err, StoreErrorCodeOf(err))
	}
	if _, err := store.SavePairingCode(t.Context(), app.PairingCode{ID: "pair-real-other", CodeHash: pairing.CodeHash, ExpiresAt: pairing.ExpiresAt}); StoreErrorCodeOf(err) != StoreErrorConflict {
		t.Fatalf("duplicate pairing hash err=%v code=%q", err, StoreErrorCodeOf(err))
	}

	claimedPairing, client, err := store.ClaimPairingCode(t.Context(), pairing.ID, app.Client{ID: "client-real", Name: "Real Client", TokenHash: "client-real-hash"})
	if err != nil || claimedPairing.Status != "claimed" || claimedPairing.ClientID != client.ID || claimedPairing.ClaimedAt == nil || client.CreatedAt.IsZero() {
		t.Fatalf("claim pairing=%#v client=%#v err=%v", claimedPairing, client, err)
	}
	if got, found, err := store.GetClient(t.Context(), client.ID); err != nil || !found || !ClientsEqual(got, client) {
		t.Fatalf("client=%#v found=%v err=%v", got, found, err)
	}
	if listed, err := store.ListClients(t.Context()); err != nil || len(listed) != 1 || listed[0].ID != client.ID {
		t.Fatalf("clients=%#v err=%v", listed, err)
	}
	if got, found, err := store.FindClientByTokenHash(t.Context(), client.TokenHash); err != nil || !found || got.ID != client.ID {
		t.Fatalf("token client=%#v found=%v err=%v", got, found, err)
	}
	touched, found, err := store.TouchClient(t.Context(), client.ID)
	if err != nil || !found || touched.LastSeenAt == nil {
		t.Fatalf("touch=%#v found=%v err=%v", touched, found, err)
	}

	rollbackPair, err := store.SavePairingCode(t.Context(), app.PairingCode{ID: "pair-real-rollback", CodeHash: "pair-real-rollback-hash", ExpiresAt: time.Now().UTC().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ClaimPairingCode(t.Context(), rollbackPair.ID, app.Client{ID: "other-client", Name: "Duplicate Token", TokenHash: client.TokenHash}); StoreErrorCodeOf(err) != StoreErrorConflict {
		t.Fatalf("duplicate client claim err=%v code=%q", err, StoreErrorCodeOf(err))
	}
	if got, found, err := store.GetPairingCode(t.Context(), rollbackPair.ID); err != nil || !found || got.Status != "pending" || got.ClientID != "" || got.ClaimedAt != nil {
		t.Fatalf("rolled-back pairing=%#v found=%v err=%v", got, found, err)
	}
	if _, found, err := store.GetClient(t.Context(), "other-client"); err != nil || found {
		t.Fatalf("partial client found=%v err=%v", found, err)
	}

	events := mustEventsAfter(t, store, "", "")
	eventTypes := make([]string, 0, len(events))
	for _, event := range events {
		eventTypes = append(eventTypes, event.Type)
	}
	claimStart := slices.Index(eventTypes, "client.saved")
	if claimStart < 0 || claimStart+1 >= len(eventTypes) || eventTypes[claimStart+1] != "pairing_code.claimed" {
		t.Fatalf("event sequence=%v", eventTypes)
	}
	auditTypes := map[string]int{}
	for _, audit := range mustListAudit(t, store, "") {
		auditTypes[audit.Type]++
	}
	if auditTypes["client.saved"] != 1 || auditTypes["pairing_code.claimed"] != 1 {
		t.Fatalf("claim audit set=%v", auditTypes)
	}

	revoked, err := store.RevokeClient(t.Context(), client.ID)
	if err != nil || revoked.RevokedAt == nil {
		t.Fatalf("revoke=%#v err=%v", revoked, err)
	}
	if _, found, err := store.FindClientByTokenHash(t.Context(), client.TokenHash); err != nil || found {
		t.Fatalf("revoked token found=%v err=%v", found, err)
	}
}
