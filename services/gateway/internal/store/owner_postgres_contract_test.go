package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakeOwnerPostgresOps struct {
	session    *fakeOnboardingPostgresSession
	acquireErr error
	execErrors []error
	execSQL    []string
	row        onboardingPostgresRow
	rowSQL     []string
	rows       onboardingPostgresRows
	queryErr   error
	querySQL   []string
}

func (o *fakeOwnerPostgresOps) Acquire(context.Context) (onboardingPostgresSession, error) {
	if o.acquireErr != nil {
		return nil, o.acquireErr
	}
	return o.session, nil
}

func (o *fakeOwnerPostgresOps) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	o.execSQL = append(o.execSQL, sql)
	index := len(o.execSQL) - 1
	if index < len(o.execErrors) {
		return pgconn.CommandTag{}, o.execErrors[index]
	}
	return pgconn.CommandTag{}, nil
}

func (o *fakeOwnerPostgresOps) Query(_ context.Context, sql string, _ ...any) (onboardingPostgresRows, error) {
	o.querySQL = append(o.querySQL, sql)
	return o.rows, o.queryErr
}

func (o *fakeOwnerPostgresOps) QueryRow(_ context.Context, sql string, _ ...any) onboardingPostgresRow {
	o.rowSQL = append(o.rowSQL, sql)
	return o.row
}

type fakeOwnerPostgresRow struct {
	profile     app.OwnerProfile
	preferences []byte
	err         error
}

func ownerPostgresRow(profile app.OwnerProfile) fakeOwnerPostgresRow {
	preferences, err := json.Marshal(profile.Preferences)
	if err != nil {
		panic(err)
	}
	return fakeOwnerPostgresRow{profile: profile, preferences: preferences}
}

func (r fakeOwnerPostgresRow) Scan(destinations ...any) error {
	if r.err != nil {
		return r.err
	}
	*(destinations[0].(*string)) = r.profile.ID
	*(destinations[1].(*string)) = r.profile.Source
	*(destinations[2].(*string)) = r.profile.ExternalRef
	*(destinations[3].(*string)) = r.profile.WorkspaceRoot
	*(destinations[4].(*string)) = r.profile.DefaultChannel
	*(destinations[5].(*string)) = r.profile.DefaultBindingID
	*(destinations[6].(*string)) = r.profile.DisplayName
	*(destinations[7].(*string)) = r.profile.Email
	*(destinations[8].(*[]byte)) = append([]byte(nil), r.preferences...)
	*(destinations[9].(*time.Time)) = r.profile.CreatedAt
	*(destinations[10].(*time.Time)) = r.profile.UpdatedAt
	return nil
}

type fakeOwnerPostgresRows struct {
	rows    []fakeOwnerPostgresRow
	index   int
	scanErr error
	err     error
	closed  bool
}

func (r *fakeOwnerPostgresRows) Next() bool { return r.index < len(r.rows) }

func (r *fakeOwnerPostgresRows) Scan(destinations ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	row := r.rows[r.index]
	r.index++
	return row.Scan(destinations...)
}

func (r *fakeOwnerPostgresRows) Err() error { return r.err }
func (r *fakeOwnerPostgresRows) Close()     { r.closed = true }

func newFakePostgresOwnerStore(transaction *fakeOnboardingPostgresTx) (*PostgresStore, *fakeOwnerPostgresOps, *fakeOnboardingPostgresSession) {
	session := &fakeOnboardingPostgresSession{transaction: transaction}
	operations := &fakeOwnerPostgresOps{session: session}
	return &PostgresStore{
		operationTimeouts: defaultOperationTimeouts, ownerPostgres: operations,
		ownerWriteHighWater: map[string]time.Time{}, ownerNow: time.Now,
	}, operations, session
}

func testPostgresOwnerProfile(id string, createdAt, updatedAt time.Time) app.OwnerProfile {
	return app.OwnerProfile{
		ID: id, Source: "weixin", ExternalRef: "external-" + id, WorkspaceRoot: "/workspace/" + id,
		DefaultChannel: "weixin", DefaultBindingID: "binding-" + id, DisplayName: "Owner " + id,
		Email: id + "@example.test", Preferences: map[string]string{"tone": "brief"},
		CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
}

func TestPostgresOwnerStartupSeed(t *testing.T) {
	createdAt := time.Date(2026, 8, 20, 4, 0, 0, 123456000, time.UTC)
	existing := testPostgresOwnerProfile(app.DefaultOwnerID, createdAt, createdAt.Add(time.Minute))

	t.Run("missing owner inserted and confirmed without lifecycle", func(t *testing.T) {
		store, operations, _ := newFakePostgresOwnerStore(&fakeOnboardingPostgresTx{})
		operations.row = ownerPostgresRow(testPostgresOwnerProfile(app.DefaultOwnerID, createdAt, createdAt))
		if err := store.seedDefaultOwner(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(operations.execSQL) != 1 || !strings.Contains(operations.execSQL[0], "ON CONFLICT (id) DO NOTHING") ||
			len(operations.rowSQL) != 1 || store.ownerWriteHighWater[app.DefaultOwnerID].IsZero() {
			t.Fatalf("seed SQL exec=%v row=%v high-water=%v", operations.execSQL, operations.rowSQL, store.ownerWriteHighWater)
		}
		if strings.Contains(operations.execSQL[0], "audit_events") || strings.Contains(operations.execSQL[0], "events") {
			t.Fatalf("startup seed emitted lifecycle SQL: %s", operations.execSQL[0])
		}
	})

	t.Run("existing owner is preserved", func(t *testing.T) {
		store, operations, _ := newFakePostgresOwnerStore(&fakeOnboardingPostgresTx{})
		operations.row = ownerPostgresRow(existing)
		if err := store.seedDefaultOwner(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !store.ownerWriteHighWater[app.DefaultOwnerID].Equal(existing.UpdatedAt) {
			t.Fatalf("existing owner high-water = %s want %s", store.ownerWriteHighWater[app.DefaultOwnerID], existing.UpdatedAt)
		}
	})

	for _, testCase := range []struct {
		name      string
		execError error
		row       onboardingPostgresRow
	}{
		{name: "insert failure", execError: errors.New("insert failed"), row: ownerPostgresRow(existing)},
		{name: "confirmation failure", row: fakeOwnerPostgresRow{err: errors.New("confirm failed")}},
		{name: "invalid confirmation", row: ownerPostgresRow(testPostgresOwnerProfile("wrong-owner", createdAt, createdAt))},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			store, operations, _ := newFakePostgresOwnerStore(&fakeOnboardingPostgresTx{})
			operations.execErrors = []error{testCase.execError}
			operations.row = testCase.row
			if err := store.seedDefaultOwner(context.Background()); err == nil {
				t.Fatal("startup seed failure was accepted")
			}
			if len(store.ownerWriteHighWater) != 0 {
				t.Fatalf("failed startup marked owner ready: %#v", store.ownerWriteHighWater)
			}
		})
	}
}

func TestPostgresOwnerGetUsesReadCommittedBarrierAndDoesNotWrite(t *testing.T) {
	now := time.Date(2026, 8, 20, 5, 0, 0, 0, time.UTC)
	profile := testPostgresOwnerProfile(app.DefaultOwnerID, now, now)
	transaction := &fakeOnboardingPostgresTx{row: ownerPostgresRow(profile)}
	store, operations, session := newFakePostgresOwnerStore(transaction)
	got, err := store.GetOwnerProfile(context.Background())
	if err != nil || !OwnerProfilesEqual(got, profile) {
		t.Fatalf("get = %#v err=%v", got, err)
	}
	if len(session.options) != 1 || session.options[0].IsoLevel != pgx.ReadCommitted || transaction.commits != 1 {
		t.Fatalf("barrier options=%#v commits=%d", session.options, transaction.commits)
	}
	if len(transaction.execSQL) != 1 || !strings.Contains(transaction.execSQL[0], "pg_advisory_xact_lock") || len(transaction.rowSQL) != 1 {
		t.Fatalf("barrier statements exec=%v row=%v", transaction.execSQL, transaction.rowSQL)
	}
	joined := strings.Join(append(append([]string{}, operations.execSQL...), transaction.execSQL...), "\n")
	if strings.Contains(joined, "INSERT") || strings.Contains(joined, "UPDATE") || strings.Contains(joined, "audit_events") || strings.Contains(joined, "events") {
		t.Fatalf("owner GET issued a write: %s", joined)
	}
}

func TestPostgresOwnerGetClassifiesScanAndDecodeFailures(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		row      onboardingPostgresRow
		wantCode StoreErrorCode
	}{
		{name: "transport scan", row: fakeOwnerPostgresRow{err: errors.New("connection reset")}, wantCode: StoreErrorUnavailable},
		{name: "timeout scan", row: fakeOwnerPostgresRow{err: context.DeadlineExceeded}, wantCode: StoreErrorTimeout},
		{name: "corrupt preferences", row: fakeOwnerPostgresRow{profile: app.OwnerProfile{ID: "owner-corrupt"}, preferences: []byte("not-json")}, wantCode: StoreErrorCorrupt},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			transaction := &fakeOnboardingPostgresTx{row: testCase.row}
			store, _, session := newFakePostgresOwnerStore(transaction)
			_, _, err := store.GetOwnerProfileByID(context.Background(), "owner-corrupt")
			if StoreErrorCodeOf(err) != testCase.wantCode || transaction.rollbacks != 1 || session.releases != 1 {
				t.Fatalf("error=%v code=%q rollback=%d release=%d", err, StoreErrorCodeOf(err), transaction.rollbacks, session.releases)
			}
		})
	}

	transaction := &fakeOnboardingPostgresTx{row: fakeOwnerPostgresRow{err: pgx.ErrNoRows}}
	store, _, _ := newFakePostgresOwnerStore(transaction)
	if _, found, err := store.GetOwnerProfileByID(context.Background(), "missing"); err != nil || found || transaction.commits != 1 {
		t.Fatalf("absence found=%v err=%v commits=%d", found, err, transaction.commits)
	}

	transaction = &fakeOnboardingPostgresTx{row: fakeOwnerPostgresRow{err: pgx.ErrNoRows}}
	store, _, _ = newFakePostgresOwnerStore(transaction)
	if _, err := store.GetOwnerProfile(context.Background()); StoreErrorCodeOf(err) != StoreErrorCorrupt {
		t.Fatalf("missing default owner error=%v code=%q", err, StoreErrorCodeOf(err))
	}
}

func TestPostgresOwnerSaveIsAtomicAndClassifiesOutcomes(t *testing.T) {
	now := time.Date(2026, 8, 20, 6, 0, 0, 123456000, time.UTC)
	currentCreatedAt := time.Date(2025, 1, 2, 3, 4, 5, 987654321, time.UTC)
	current := testPostgresOwnerProfile("owner-save", currentCreatedAt, now.Add(-time.Minute))

	t.Run("success preserves exact creation and commits lifecycle", func(t *testing.T) {
		transaction := &fakeOnboardingPostgresTx{row: ownerPostgresRow(current)}
		store, _, session := newFakePostgresOwnerStore(transaction)
		store.ownerNow = func() time.Time { return now }
		saved, err := store.SaveOwnerProfile(context.Background(), app.OwnerProfile{
			ID: " owner-save ", DisplayName: " Saved ", Preferences: map[string]string{},
		})
		if err != nil || saved.ID != "owner-save" || saved.DisplayName != "Saved" || saved.CreatedAt != currentCreatedAt {
			t.Fatalf("saved=%#v err=%v", saved, err)
		}
		if len(transaction.execSQL) != 4 || transaction.commits != 1 || transaction.rollbacks != 0 || session.releases != 1 || session.terminates != 0 {
			t.Fatalf("exec=%v commit=%d rollback=%d release=%d terminate=%d", transaction.execSQL, transaction.commits, transaction.rollbacks, session.releases, session.terminates)
		}
		for _, required := range []string{"pg_advisory_xact_lock", "INSERT INTO owners", "INSERT INTO audit_events", "INSERT INTO events"} {
			if !strings.Contains(strings.Join(transaction.execSQL, "\n"), required) {
				t.Fatalf("atomic transaction missing %q: %v", required, transaction.execSQL)
			}
		}
	})

	for _, testCase := range []struct {
		name          string
		execErrors    []error
		commitErr     error
		rollbackErr   error
		terminateErr  error
		wantCode      StoreErrorCode
		wantCandidate bool
		wantRollback  int
		wantTerminate int
	}{
		{name: "owner server rejection", execErrors: []error{nil, &pgconn.PgError{Code: "42501"}}, wantCode: StoreErrorInternal, wantRollback: 1},
		{name: "audit safe transport", execErrors: []error{nil, nil, safePostgresRetryError{errors.New("not sent")}}, wantCode: StoreErrorUnavailable, wantRollback: 1},
		{name: "event unsafe transport", execErrors: []error{nil, nil, nil, errors.New("submission uncertain")}, wantCode: StoreErrorUnknownOutcome, wantCandidate: true, wantTerminate: 1},
		{name: "commit uncertain", commitErr: errors.New("commit uncertain"), wantCode: StoreErrorUnknownOutcome, wantCandidate: true, wantTerminate: 1},
		{name: "safe rollback failure retains cleanup", execErrors: []error{nil, safePostgresRetryError{errors.New("not sent")}}, rollbackErr: errFileCleanupInjected, terminateErr: errFileCommitInjected, wantCode: StoreErrorUnavailable, wantRollback: 1, wantTerminate: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			transaction := &fakeOnboardingPostgresTx{
				row: ownerPostgresRow(current), execErrors: testCase.execErrors,
				commitErr: testCase.commitErr, rollbackErr: testCase.rollbackErr,
			}
			store, _, session := newFakePostgresOwnerStore(transaction)
			session.terminateErr = testCase.terminateErr
			store.ownerNow = func() time.Time { return now }
			candidate, err := store.SaveOwnerProfile(context.Background(), app.OwnerProfile{ID: current.ID, DisplayName: "Candidate"})
			if StoreErrorCodeOf(err) != testCase.wantCode || (candidate.ID != "") != testCase.wantCandidate {
				t.Fatalf("candidate=%#v err=%v code=%q", candidate, err, StoreErrorCodeOf(err))
			}
			if transaction.rollbacks != testCase.wantRollback || session.terminates != testCase.wantTerminate {
				t.Fatalf("rollback=%d terminate=%d want=%d/%d", transaction.rollbacks, session.terminates, testCase.wantRollback, testCase.wantTerminate)
			}
			if testCase.rollbackErr != nil && (!errors.Is(err, testCase.rollbackErr) || !errors.Is(err, testCase.terminateErr)) {
				t.Fatalf("cleanup errors were lost: %v", err)
			}
		})
	}
}

func TestPostgresOwnerPreCandidateFailureReturnsZeroAndRetainsCleanup(t *testing.T) {
	unsafe := errors.New("submission uncertain")
	rollback := errors.New("rollback failed")
	terminate := errors.New("terminate failed")
	corruptRow := fakeOwnerPostgresRow{profile: app.OwnerProfile{ID: "owner-before-candidate"}, preferences: []byte("not-json")}
	for _, testCase := range []struct {
		name           string
		lockError      error
		row            onboardingPostgresRow
		rollbackError  error
		terminateError error
		wantCode       StoreErrorCode
		wantRollback   int
		wantTerminate  int
		wantCauses     []error
	}{
		{name: "unsafe lock", lockError: unsafe, terminateError: terminate, wantCode: StoreErrorUnknownOutcome, wantTerminate: 1, wantCauses: []error{unsafe, terminate}},
		{name: "unsafe current row", row: fakeOwnerPostgresRow{err: unsafe}, terminateError: terminate, wantCode: StoreErrorUnknownOutcome, wantTerminate: 1, wantCauses: []error{unsafe, terminate}},
		{name: "canceled current row is uncertain", row: fakeOwnerPostgresRow{err: context.Canceled}, terminateError: terminate, wantCode: StoreErrorUnknownOutcome, wantTerminate: 1, wantCauses: []error{context.Canceled, terminate}},
		{name: "safe lock rollback cleanup", lockError: safePostgresRetryError{errors.New("lock not sent")}, rollbackError: rollback, terminateError: terminate, wantCode: StoreErrorUnavailable, wantRollback: 1, wantTerminate: 1, wantCauses: []error{rollback, terminate}},
		{name: "safe current row", row: fakeOwnerPostgresRow{err: safePostgresRetryError{errors.New("query not sent")}}, wantCode: StoreErrorUnavailable, wantRollback: 1},
		{name: "server lock rejection", lockError: &pgconn.PgError{Code: "42501"}, wantCode: StoreErrorInternal, wantRollback: 1},
		{name: "corrupt current row", row: corruptRow, wantCode: StoreErrorCorrupt, wantRollback: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			row := testCase.row
			if row == nil {
				row = fakeOwnerPostgresRow{err: pgx.ErrNoRows}
			}
			transaction := &fakeOnboardingPostgresTx{
				execErrors: []error{testCase.lockError}, row: row, rollbackErr: testCase.rollbackError,
			}
			store, _, session := newFakePostgresOwnerStore(transaction)
			session.terminateErr = testCase.terminateError
			candidate, err := store.SaveOwnerProfile(context.Background(), app.OwnerProfile{ID: "owner-before-candidate"})
			if candidate.ID != "" || StoreErrorCodeOf(err) != testCase.wantCode ||
				transaction.rollbacks != testCase.wantRollback || session.terminates != testCase.wantTerminate {
				t.Fatalf("candidate=%#v err=%v code=%q rollback=%d terminate=%d", candidate, err, StoreErrorCodeOf(err), transaction.rollbacks, session.terminates)
			}
			for _, cause := range testCase.wantCauses {
				if !errors.Is(err, cause) {
					t.Fatalf("error %v lost cause %v", err, cause)
				}
			}
		})
	}
}

func TestPostgresOwnerListAndExternalLookupPropagateFailures(t *testing.T) {
	now := time.Date(2026, 8, 20, 7, 0, 0, 0, time.UTC)
	profile := testPostgresOwnerProfile("owner-list", now, now)
	for _, testCase := range []struct {
		name     string
		rows     *fakeOwnerPostgresRows
		queryErr error
		wantCode StoreErrorCode
		wantLen  int
	}{
		{name: "success", rows: &fakeOwnerPostgresRows{rows: []fakeOwnerPostgresRow{ownerPostgresRow(profile)}}, wantLen: 1},
		{name: "query", rows: &fakeOwnerPostgresRows{}, queryErr: errors.New("query failed"), wantCode: StoreErrorUnavailable},
		{name: "scan", rows: &fakeOwnerPostgresRows{rows: []fakeOwnerPostgresRow{ownerPostgresRow(profile)}, scanErr: errors.New("scan failed")}, wantCode: StoreErrorUnavailable},
		{name: "decode", rows: &fakeOwnerPostgresRows{rows: []fakeOwnerPostgresRow{{profile: profile, preferences: []byte("not-json")}}}, wantCode: StoreErrorCorrupt},
		{name: "rows", rows: &fakeOwnerPostgresRows{err: errors.New("rows failed")}, wantCode: StoreErrorUnavailable},
	} {
		t.Run("list/"+testCase.name, func(t *testing.T) {
			store, operations, _ := newFakePostgresOwnerStore(&fakeOnboardingPostgresTx{})
			operations.rows = testCase.rows
			operations.queryErr = testCase.queryErr
			listed, err := store.ListOwnerProfiles(context.Background())
			if StoreErrorCodeOf(err) != testCase.wantCode || len(listed) != testCase.wantLen {
				t.Fatalf("listed=%#v err=%v code=%q", listed, err, StoreErrorCodeOf(err))
			}
			if testCase.queryErr == nil && !testCase.rows.closed {
				t.Fatal("owner rows were not closed")
			}
			if len(operations.querySQL) != 1 || !strings.Contains(operations.querySQL[0], "ORDER BY updated_at DESC, id ASC") {
				t.Fatalf("list SQL = %v", operations.querySQL)
			}
		})
	}

	for _, testCase := range []struct {
		name     string
		row      onboardingPostgresRow
		wantCode StoreErrorCode
		found    bool
	}{
		{name: "success", row: ownerPostgresRow(profile), found: true},
		{name: "absence", row: fakeOwnerPostgresRow{err: pgx.ErrNoRows}},
		{name: "scan", row: fakeOwnerPostgresRow{err: errors.New("scan failed")}, wantCode: StoreErrorUnavailable},
		{name: "decode", row: fakeOwnerPostgresRow{profile: profile, preferences: []byte("not-json")}, wantCode: StoreErrorCorrupt},
	} {
		t.Run("lookup/"+testCase.name, func(t *testing.T) {
			store, operations, _ := newFakePostgresOwnerStore(&fakeOnboardingPostgresTx{})
			operations.row = testCase.row
			got, found, err := store.FindOwnerProfileByExternalRef(context.Background(), profile.Source, profile.ExternalRef)
			if StoreErrorCodeOf(err) != testCase.wantCode || found != testCase.found || (found && got.ID != profile.ID) {
				t.Fatalf("got=%#v found=%v err=%v code=%q", got, found, err, StoreErrorCodeOf(err))
			}
			if len(operations.rowSQL) != 1 || !strings.Contains(operations.rowSQL[0], "ORDER BY updated_at DESC, id ASC LIMIT 1") {
				t.Fatalf("lookup SQL = %v", operations.rowSQL)
			}
		})
	}
}

func TestPostgresOwnerFailedCandidateHighWaterDoesNotRollback(t *testing.T) {
	fixed := time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)
	firstTransaction := &fakeOnboardingPostgresTx{
		row: fakeOwnerPostgresRow{err: pgx.ErrNoRows}, execErrors: []error{nil, nil, nil, errors.New("event uncertain")},
	}
	store, operations, _ := newFakePostgresOwnerStore(firstTransaction)
	store.ownerNow = func() time.Time { return fixed }
	first, err := store.SaveOwnerProfile(context.Background(), app.OwnerProfile{ID: "owner-high-water"})
	if StoreErrorCodeOf(err) != StoreErrorUnknownOutcome || first.ID == "" {
		t.Fatalf("first candidate=%#v err=%v", first, err)
	}

	secondTransaction := &fakeOnboardingPostgresTx{row: fakeOwnerPostgresRow{err: pgx.ErrNoRows}}
	operations.session = &fakeOnboardingPostgresSession{transaction: secondTransaction}
	store.ownerNow = func() time.Time { return fixed.Add(-time.Hour) }
	second, err := store.SaveOwnerProfile(context.Background(), app.OwnerProfile{ID: first.ID})
	if err != nil || !second.UpdatedAt.After(first.UpdatedAt) {
		t.Fatalf("second candidate=%#v err=%v first=%s", second, err, first.UpdatedAt)
	}
}

func TestPostgresOwnerRepositoryRealDatabase(t *testing.T) {
	dsn := os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set SPARKCLAW_TEST_POSTGRES_DSN to run postgres store integration tests")
	}
	store, err := NewPostgresStore(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	truncatePostgresStore(t, store)
	if err := store.seedDefaultOwner(context.Background()); err != nil {
		t.Fatal(err)
	}
	seeded, err := store.GetOwnerProfile(context.Background())
	if err != nil || seeded.ID != app.DefaultOwnerID {
		t.Fatalf("seeded=%#v err=%v", seeded, err)
	}

	existing := mustUpdateOwnerProfile(t, store, app.OwnerProfile{DisplayName: "Existing default"})
	if err := store.seedDefaultOwner(context.Background()); err != nil {
		t.Fatal(err)
	}
	preserved := mustGetOwnerProfile(t, store)
	if preserved.DisplayName != existing.DisplayName || !preserved.CreatedAt.Equal(existing.CreatedAt) {
		t.Fatalf("existing default overwritten: %#v want %#v", preserved, existing)
	}

	auditsBefore := len(store.ListAudit(""))
	eventsBefore := len(store.EventsAfter("", ""))
	createdAt := time.Date(2025, 2, 3, 4, 5, 6, 123456789, time.UTC)
	saved := mustSaveOwnerProfile(t, store, app.OwnerProfile{
		ID: "owner-real", Source: "weixin", ExternalRef: "real-ref", DisplayName: "Real",
		Preferences: map[string]string{"mode": "real"}, CreatedAt: createdAt,
	})
	if saved.CreatedAt.Nanosecond()%1000 != 0 || len(store.ListAudit("")) != auditsBefore+1 || len(store.EventsAfter("", "")) != eventsBefore+1 {
		t.Fatalf("real atomic save=%#v audit=%d event=%d", saved, len(store.ListAudit(""))-auditsBefore, len(store.EventsAfter("", ""))-eventsBefore)
	}
	listed, err := store.ListOwnerProfiles(context.Background())
	if err != nil || len(listed) != 2 {
		t.Fatalf("real list=%#v err=%v", listed, err)
	}
	lookedUp, found, err := store.FindOwnerProfileByExternalRef(context.Background(), "weixin", "real-ref")
	if err != nil || !found || !OwnerProfilesEqual(lookedUp, saved) {
		t.Fatalf("real lookup=%#v found=%v err=%v", lookedUp, found, err)
	}

}
