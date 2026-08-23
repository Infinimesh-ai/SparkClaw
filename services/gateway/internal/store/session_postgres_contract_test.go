package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/sync/semaphore"
)

type fakeSessionPostgresOps struct {
	session    onboardingPostgresSession
	acquireErr error
	rows       onboardingPostgresRows
	queryErr   error
	querySQL   []string
}

func (o *fakeSessionPostgresOps) Acquire(context.Context) (onboardingPostgresSession, error) {
	if o.acquireErr != nil {
		return nil, o.acquireErr
	}
	return o.session, nil
}

func (o *fakeSessionPostgresOps) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (o *fakeSessionPostgresOps) Query(_ context.Context, sql string, _ ...any) (onboardingPostgresRows, error) {
	o.querySQL = append(o.querySQL, sql)
	return o.rows, o.queryErr
}

func (o *fakeSessionPostgresOps) QueryRow(context.Context, string, ...any) onboardingPostgresRow {
	return fakeSessionPostgresRow{err: errors.New("unexpected pool QueryRow")}
}

type fakeSessionPostgresSession struct {
	transaction  onboardingPostgresTx
	beginErr     error
	options      []pgx.TxOptions
	releases     int
	terminates   int
	terminateErr error
}

func (s *fakeSessionPostgresSession) Begin(_ context.Context, options pgx.TxOptions) (onboardingPostgresTx, error) {
	s.options = append(s.options, options)
	if s.beginErr != nil {
		return nil, s.beginErr
	}
	return s.transaction, nil
}

func (s *fakeSessionPostgresSession) Release() { s.releases++ }

func (s *fakeSessionPostgresSession) Terminate(context.Context) error {
	s.terminates++
	return s.terminateErr
}

type fakeSessionPostgresTx struct {
	execSQL     []string
	execErrors  map[int]error
	zeroRowsAt  int
	rowQueue    []onboardingPostgresRow
	current     app.Session
	echoWrites  bool
	rowSQL      []string
	rows        onboardingPostgresRows
	queryErr    error
	querySQL    []string
	commitErr   error
	rollbackErr error
	commits     int
	rollbacks   int
}

func (t *fakeSessionPostgresTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	index := len(t.execSQL)
	t.execSQL = append(t.execSQL, sql)
	if err := t.execErrors[index]; err != nil {
		return pgconn.CommandTag{}, err
	}
	if strings.Contains(sql, "DELETE FROM sessions") {
		if t.zeroRowsAt == index {
			return pgconn.NewCommandTag("DELETE 0"), nil
		}
		return pgconn.NewCommandTag("DELETE 1"), nil
	}
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (t *fakeSessionPostgresTx) QueryRow(_ context.Context, sql string, arguments ...any) onboardingPostgresRow {
	t.rowSQL = append(t.rowSQL, sql)
	if len(t.rowQueue) > 0 {
		row := t.rowQueue[0]
		t.rowQueue = t.rowQueue[1:]
		return row
	}
	if t.echoWrites && strings.Contains(sql, "INSERT INTO sessions") && len(arguments) == 8 {
		return sessionPostgresRow(app.Session{
			ID: arguments[0].(string), OwnerID: arguments[1].(string), WorkspaceRoot: arguments[2].(string),
			Title: arguments[3].(string), Source: arguments[4].(string), Hidden: arguments[5].(bool),
			CreatedAt: arguments[6].(time.Time), UpdatedAt: arguments[7].(time.Time),
		})
	}
	if t.echoWrites && strings.Contains(sql, "UPDATE sessions") && len(arguments) == 3 {
		candidate := t.current
		candidate.ID = arguments[0].(string)
		candidate.Title = arguments[1].(string)
		candidate.UpdatedAt = arguments[2].(time.Time)
		return sessionPostgresRow(candidate)
	}
	return fakeSessionPostgresRow{err: errors.New("unexpected transaction QueryRow")}
}

func (t *fakeSessionPostgresTx) Query(_ context.Context, sql string, _ ...any) (onboardingPostgresRows, error) {
	t.querySQL = append(t.querySQL, sql)
	return t.rows, t.queryErr
}

func (t *fakeSessionPostgresTx) Commit(context.Context) error {
	t.commits++
	return t.commitErr
}

func (t *fakeSessionPostgresTx) Rollback(context.Context) error {
	t.rollbacks++
	return t.rollbackErr
}

type fakeSessionPostgresRow struct {
	session *app.Session
	boolean *bool
	err     error
}

func sessionPostgresRow(session app.Session) fakeSessionPostgresRow {
	copy := session
	return fakeSessionPostgresRow{session: &copy}
}

func sessionBooleanRow(value bool) fakeSessionPostgresRow {
	return fakeSessionPostgresRow{boolean: &value}
}

func (r fakeSessionPostgresRow) Scan(destinations ...any) error {
	if r.err != nil {
		return r.err
	}
	if r.session != nil && len(destinations) == 8 {
		*(destinations[0].(*string)) = r.session.ID
		*(destinations[1].(*string)) = r.session.OwnerID
		*(destinations[2].(*string)) = r.session.WorkspaceRoot
		*(destinations[3].(*string)) = r.session.Title
		*(destinations[4].(*string)) = r.session.Source
		*(destinations[5].(*bool)) = r.session.Hidden
		*(destinations[6].(*time.Time)) = r.session.CreatedAt
		*(destinations[7].(*time.Time)) = r.session.UpdatedAt
		return nil
	}
	if r.boolean != nil && len(destinations) == 1 {
		*(destinations[0].(*bool)) = *r.boolean
		return nil
	}
	return errors.New("fake session row shape mismatch")
}

type fakeSessionPostgresRows struct {
	values []fakeSessionPostgresRow
	index  int
	err    error
	closed bool
}

func (r *fakeSessionPostgresRows) Next() bool { return r.index < len(r.values) }

func (r *fakeSessionPostgresRows) Scan(destinations ...any) error {
	row := r.values[r.index]
	r.index++
	return row.Scan(destinations...)
}

func (r *fakeSessionPostgresRows) Err() error { return r.err }
func (r *fakeSessionPostgresRows) Close()     { r.closed = true }

func newFakePostgresSessionStore(now time.Time, transaction *fakeSessionPostgresTx) (*PostgresStore, *fakeSessionPostgresOps, *fakeSessionPostgresSession) {
	session := &fakeSessionPostgresSession{transaction: transaction}
	operations := &fakeSessionPostgresOps{session: session}
	return &PostgresStore{
		operationTimeouts: defaultOperationTimeouts,
		sessionPostgres:   operations, sessionCommandGate: semaphore.NewWeighted(1),
		sessionWriteHighWater: map[string]time.Time{}, sessionNow: func() time.Time { return now },
	}, operations, session
}

func validPostgresSession(now time.Time) app.Session {
	return app.Session{
		ID: "session-postgres", OwnerID: app.DefaultOwnerID, WorkspaceRoot: "/workspace",
		Title: "before", Source: "webchat", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Minute),
	}
}

func TestPostgresSessionCreateUpdateAndDeleteAreAtomic(t *testing.T) {
	now := time.Date(2026, 8, 21, 16, 0, 0, 0, time.UTC)

	t.Run("create", func(t *testing.T) {
		tx := &fakeSessionPostgresTx{echoWrites: true, execErrors: map[int]error{}, zeroRowsAt: -1}
		store, _, session := newFakePostgresSessionStore(now, tx)
		created, err := store.CreateSessionWithScope(t.Context(), "created", " owner ", " /workspace ", " telegram ", false)
		if err != nil || created.ID == "" || created.OwnerID != "owner" || created.Source != "telegram" || tx.commits != 1 || session.releases != 1 {
			t.Fatalf("created=%#v err=%v commit=%d release=%d", created, err, tx.commits, session.releases)
		}
		assertSessionLifecycleSQL(t, tx.execSQL, "session.created")
	})

	t.Run("update", func(t *testing.T) {
		current := validPostgresSession(now)
		tx := &fakeSessionPostgresTx{current: current, echoWrites: true, rowQueue: []onboardingPostgresRow{sessionPostgresRow(current)}, execErrors: map[int]error{}, zeroRowsAt: -1}
		store, _, session := newFakePostgresSessionStore(now, tx)
		updated, err := store.UpdateSessionTitle(t.Context(), current.ID, " renamed ")
		if err != nil || updated.Title != "renamed" || !updated.UpdatedAt.Equal(now) || tx.commits != 1 || session.releases != 1 {
			t.Fatalf("updated=%#v err=%v commit=%d release=%d", updated, err, tx.commits, session.releases)
		}
		assertSessionLifecycleSQL(t, tx.execSQL, "session.updated")
	})

	t.Run("delete", func(t *testing.T) {
		current := validPostgresSession(now)
		tx := &fakeSessionPostgresTx{rowQueue: []onboardingPostgresRow{sessionPostgresRow(current)}, execErrors: map[int]error{}, zeroRowsAt: -1}
		store, _, session := newFakePostgresSessionStore(now, tx)
		deleted, err := store.DeleteSession(t.Context(), current.ID)
		if err != nil || !sessionsEqual(deleted, current) || tx.commits != 1 || session.releases != 1 {
			t.Fatalf("deleted=%#v err=%v commit=%d release=%d", deleted, err, tx.commits, session.releases)
		}
		if len(tx.execSQL) != 1+len(sessionDeleteStatements)+2 {
			t.Fatalf("delete statements=%d want=%d", len(tx.execSQL), 1+len(sessionDeleteStatements)+2)
		}
		assertSessionLifecycleSQL(t, tx.execSQL, "session.deleted")
	})
}

func assertSessionLifecycleSQL(t testing.TB, statements []string, eventType string) {
	t.Helper()
	joined := strings.Join(statements, "\n")
	for _, required := range []string{"pg_advisory_xact_lock", "audit_events", "events"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("%s lifecycle SQL is missing %q: %s", eventType, required, joined)
		}
	}
}

func TestPostgresSessionAcquisitionBeginAndAdmissionFailures(t *testing.T) {
	now := time.Date(2026, 8, 21, 16, 0, 0, 0, time.UTC)
	t.Run("acquire", func(t *testing.T) {
		store, operations, _ := newFakePostgresSessionStore(now, &fakeSessionPostgresTx{})
		operations.acquireErr = errors.New("pool unavailable")
		if _, err := store.CreateSession(t.Context(), "create"); StoreErrorCodeOf(err) != StoreErrorUnavailable {
			t.Fatalf("acquire error = %v", err)
		}
	})
	for _, testCase := range []struct {
		name          string
		failure       error
		wantCode      StoreErrorCode
		wantRelease   int
		wantTerminate int
	}{
		{name: "safe begin", failure: safePostgresRetryError{errors.New("not sent")}, wantCode: StoreErrorUnavailable, wantRelease: 1},
		{name: "server begin", failure: &pgconn.PgError{Code: "40001"}, wantCode: StoreErrorInternal, wantRelease: 1},
		{name: "unsafe begin", failure: errors.New("begin uncertain"), wantCode: StoreErrorUnknownOutcome, wantTerminate: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			store, _, session := newFakePostgresSessionStore(now, &fakeSessionPostgresTx{})
			session.beginErr = testCase.failure
			candidate, err := store.CreateSession(t.Context(), "create")
			if candidate.ID != "" || StoreErrorCodeOf(err) != testCase.wantCode || session.releases != testCase.wantRelease || session.terminates != testCase.wantTerminate {
				t.Fatalf("candidate=%#v err=%v release=%d terminate=%d", candidate, err, session.releases, session.terminates)
			}
		})
	}
	t.Run("admission deadline", func(t *testing.T) {
		store, _, _ := newFakePostgresSessionStore(now, &fakeSessionPostgresTx{})
		if err := store.sessionCommandGate.Acquire(t.Context(), 1); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		defer cancel()
		result := make(chan error, 1)
		go func() {
			_, err := store.DeleteSession(ctx, "session-postgres")
			result <- err
		}()
		err := <-result
		store.sessionCommandGate.Release(1)
		if StoreErrorCodeOf(err) != StoreErrorTimeout {
			t.Fatalf("admission error = %v", err)
		}
	})
}

func TestPostgresSessionDeleteChecksEveryStatementFailure(t *testing.T) {
	now := time.Date(2026, 8, 21, 16, 0, 0, 0, time.UTC)
	current := validPostgresSession(now)
	statementCount := 1 + len(sessionDeleteStatements) + 2
	for failureIndex := 0; failureIndex < statementCount; failureIndex++ {
		t.Run(string(rune('A'+failureIndex)), func(t *testing.T) {
			injected := safePostgresRetryError{errors.New("statement not sent")}
			tx := &fakeSessionPostgresTx{
				rowQueue:   []onboardingPostgresRow{sessionPostgresRow(current)},
				execErrors: map[int]error{failureIndex: injected}, zeroRowsAt: -1,
			}
			store, _, session := newFakePostgresSessionStore(now, tx)
			candidate, err := store.DeleteSession(t.Context(), current.ID)
			if candidate.ID != "" || StoreErrorCodeOf(err) != StoreErrorUnavailable || tx.rollbacks != 1 || session.releases != 1 || session.terminates != 0 {
				t.Fatalf("index=%d candidate=%#v err=%v rollback=%d release=%d terminate=%d", failureIndex, candidate, err, tx.rollbacks, session.releases, session.terminates)
			}
			if len(tx.execSQL) != failureIndex+1 {
				t.Fatalf("index=%d executed=%d", failureIndex, len(tx.execSQL))
			}
		})
	}

	t.Run("rows affected", func(t *testing.T) {
		tx := &fakeSessionPostgresTx{rowQueue: []onboardingPostgresRow{sessionPostgresRow(current)}, execErrors: map[int]error{}, zeroRowsAt: len(sessionDeleteStatements)}
		store, _, session := newFakePostgresSessionStore(now, tx)
		if candidate, err := store.DeleteSession(t.Context(), current.ID); candidate.ID != "" || StoreErrorCodeOf(err) != StoreErrorInternal || tx.rollbacks != 1 || session.releases != 1 {
			t.Fatalf("candidate=%#v err=%v rollback=%d release=%d", candidate, err, tx.rollbacks, session.releases)
		}
	})

	t.Run("unsafe submitted statement", func(t *testing.T) {
		injected := errors.New("submission uncertain")
		tx := &fakeSessionPostgresTx{rowQueue: []onboardingPostgresRow{sessionPostgresRow(current)}, execErrors: map[int]error{1: injected}, zeroRowsAt: -1}
		store, _, session := newFakePostgresSessionStore(now, tx)
		candidate, err := store.DeleteSession(t.Context(), current.ID)
		if !sessionsEqual(candidate, current) || StoreErrorCodeOf(err) != StoreErrorUnknownOutcome || tx.rollbacks != 0 || session.releases != 0 || session.terminates != 1 || !errors.Is(err, injected) {
			t.Fatalf("candidate=%#v err=%v rollback=%d release=%d terminate=%d", candidate, err, tx.rollbacks, session.releases, session.terminates)
		}
	})

	t.Run("rollback failure terminates", func(t *testing.T) {
		primary := safePostgresRetryError{errors.New("not sent")}
		rollback := errors.New("rollback failed")
		terminate := errors.New("terminate failed")
		tx := &fakeSessionPostgresTx{rowQueue: []onboardingPostgresRow{sessionPostgresRow(current)}, execErrors: map[int]error{1: primary}, rollbackErr: rollback, zeroRowsAt: -1}
		store, _, session := newFakePostgresSessionStore(now, tx)
		session.terminateErr = terminate
		_, err := store.DeleteSession(t.Context(), current.ID)
		if session.releases != 0 || session.terminates != 1 || !errors.Is(err, primary) || !errors.Is(err, rollback) || !errors.Is(err, terminate) {
			t.Fatalf("err=%v release=%d terminate=%d", err, session.releases, session.terminates)
		}
	})
}

func TestPostgresSessionReadFailureAndBarrierMatrix(t *testing.T) {
	now := time.Date(2026, 8, 21, 16, 0, 0, 0, time.UTC)
	valid := validPostgresSession(now)

	for _, testCase := range []struct {
		name     string
		rows     *fakeSessionPostgresRows
		queryErr error
		wantCode StoreErrorCode
	}{
		{name: "query", rows: &fakeSessionPostgresRows{}, queryErr: errors.New("query failed"), wantCode: StoreErrorUnavailable},
		{name: "scan", rows: &fakeSessionPostgresRows{values: []fakeSessionPostgresRow{{err: errors.New("scan failed")}}}, wantCode: StoreErrorUnavailable},
		{name: "corrupt", rows: &fakeSessionPostgresRows{values: []fakeSessionPostgresRow{sessionPostgresRow(app.Session{ID: "corrupt"})}}, wantCode: StoreErrorCorrupt},
		{name: "iteration", rows: &fakeSessionPostgresRows{err: errors.New("rows failed")}, wantCode: StoreErrorUnavailable},
	} {
		t.Run("list "+testCase.name, func(t *testing.T) {
			tx := &fakeSessionPostgresTx{rows: testCase.rows, queryErr: testCase.queryErr, execErrors: map[int]error{}, zeroRowsAt: -1}
			store, _, session := newFakePostgresSessionStore(now, tx)
			listed, err := store.ListSessions(t.Context())
			if listed != nil || StoreErrorCodeOf(err) != testCase.wantCode || tx.rollbacks != 1 || session.releases != 1 {
				t.Fatalf("listed=%#v err=%v rollback=%d release=%d", listed, err, tx.rollbacks, session.releases)
			}
			if testCase.queryErr == nil && !testCase.rows.closed {
				t.Fatal("rows were not closed")
			}
		})
	}

	t.Run("list success", func(t *testing.T) {
		older := valid
		older.ID = "session-older"
		older.UpdatedAt = valid.UpdatedAt.Add(-time.Minute)
		rows := &fakeSessionPostgresRows{values: []fakeSessionPostgresRow{sessionPostgresRow(valid), sessionPostgresRow(older)}}
		tx := &fakeSessionPostgresTx{rows: rows, execErrors: map[int]error{}, zeroRowsAt: -1}
		store, _, session := newFakePostgresSessionStore(now, tx)
		listed, err := store.ListSessions(t.Context())
		if err != nil || len(listed) != 2 || tx.commits != 1 || session.releases != 1 || len(tx.querySQL) != 1 || !strings.Contains(tx.querySQL[0], "ORDER BY updated_at DESC, id ASC") || session.options[0].IsoLevel != pgx.ReadCommitted {
			t.Fatalf("listed=%#v err=%v SQL=%v options=%#v", listed, err, tx.querySQL, session.options)
		}
	})

	t.Run("get found", func(t *testing.T) {
		tx := &fakeSessionPostgresTx{rowQueue: []onboardingPostgresRow{sessionPostgresRow(valid)}, execErrors: map[int]error{}, zeroRowsAt: -1}
		store, _, session := newFakePostgresSessionStore(now, tx)
		got, found, err := store.GetSession(t.Context(), valid.ID)
		if err != nil || !found || !sessionsEqual(got, valid) || tx.commits != 1 || session.releases != 1 || session.options[0].IsoLevel != pgx.ReadCommitted || !strings.Contains(tx.execSQL[0], "pg_advisory_xact_lock") {
			t.Fatalf("got=%#v found=%v err=%v", got, found, err)
		}
	})

	for _, complete := range []bool{true, false} {
		name := "complete"
		if !complete {
			name = "incomplete"
		}
		t.Run("get absent "+name, func(t *testing.T) {
			tx := &fakeSessionPostgresTx{rowQueue: []onboardingPostgresRow{fakeSessionPostgresRow{err: pgx.ErrNoRows}, sessionBooleanRow(complete)}, execErrors: map[int]error{}, zeroRowsAt: -1}
			store, _, _ := newFakePostgresSessionStore(now, tx)
			got, found, err := store.GetSession(t.Context(), "missing")
			wantCode := StoreErrorCode("")
			if !complete {
				wantCode = StoreErrorCorrupt
			}
			if found || got.ID != "" || StoreErrorCodeOf(err) != wantCode {
				t.Fatalf("got=%#v found=%v err=%v code=%q", got, found, err, StoreErrorCodeOf(err))
			}
		})
	}

	t.Run("read commit failure", func(t *testing.T) {
		commitFailure := errors.New("commit uncertain")
		tx := &fakeSessionPostgresTx{rowQueue: []onboardingPostgresRow{sessionPostgresRow(valid)}, execErrors: map[int]error{}, zeroRowsAt: -1, commitErr: commitFailure}
		store, _, session := newFakePostgresSessionStore(now, tx)
		_, _, err := store.GetSession(t.Context(), valid.ID)
		if StoreErrorCodeOf(err) != StoreErrorUnknownOutcome || session.terminates != 1 || session.releases != 0 || !errors.Is(err, commitFailure) {
			t.Fatalf("err=%v release=%d terminate=%d", err, session.releases, session.terminates)
		}
	})
}

func TestPostgresSessionCommitUnknownReturnsEvidenceAndTerminates(t *testing.T) {
	now := time.Date(2026, 8, 21, 16, 0, 0, 0, time.UTC)
	current := validPostgresSession(now)
	commitFailure := errors.New("commit uncertain")
	tx := &fakeSessionPostgresTx{
		current: current, echoWrites: true, rowQueue: []onboardingPostgresRow{sessionPostgresRow(current)},
		execErrors: map[int]error{}, zeroRowsAt: -1, commitErr: commitFailure,
	}
	store, _, session := newFakePostgresSessionStore(now, tx)
	candidate, err := store.UpdateSessionTitle(t.Context(), current.ID, "renamed")
	if candidate.ID != current.ID || candidate.Title != "renamed" || StoreErrorCodeOf(err) != StoreErrorUnknownOutcome || session.terminates != 1 || session.releases != 0 || !errors.Is(err, commitFailure) {
		t.Fatalf("candidate=%#v err=%v release=%d terminate=%d", candidate, err, session.releases, session.terminates)
	}
}

func TestPostgresSessionRepositoryRoundTripAndConcurrentUpdateDelete(t *testing.T) {
	dsn := os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set SPARKCLAW_TEST_POSTGRES_DSN to run postgres store integration tests")
	}
	store, err := NewPostgresStore(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	truncatePostgresStore(t, store)

	visible, err := store.CreateSessionWithScope(t.Context(), "visible", " owner ", " /workspace ", " webchat ", false)
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := store.CreateSessionWithScope(t.Context(), "hidden", "owner", "/workspace", "weixin", true)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := store.ListSessions(t.Context())
	if err != nil || len(listed) != 1 || listed[0].ID != visible.ID {
		t.Fatalf("listed=%#v err=%v", listed, err)
	}
	if got, found, err := store.GetSession(t.Context(), hidden.ID); err != nil || !found || got.ID != hidden.ID {
		t.Fatalf("hidden get=%#v found=%v err=%v", got, found, err)
	}

	concurrent, err := store.CreateSession(t.Context(), "concurrent")
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	go func() {
		_, err := store.UpdateSessionTitle(t.Context(), concurrent.ID, "updated")
		results <- err
	}()
	go func() {
		_, err := store.DeleteSession(t.Context(), concurrent.ID)
		results <- err
	}()
	for range 2 {
		err := <-results
		if err != nil && StoreErrorCodeOf(err) != StoreErrorNotFound {
			t.Fatalf("concurrent command error = %v", err)
		}
	}
	if _, found, err := store.GetSession(t.Context(), concurrent.ID); err != nil || found {
		t.Fatalf("concurrent session found=%v err=%v", found, err)
	}

	legacySession, err := store.CreateSession(t.Context(), "legacy delete")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(t.Context(), `INSERT INTO weixin_chat_sessions (id,binding_id,linked_session_id,status) VALUES ('legacy-session','binding', $1, 'active')`, legacySession.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(t.Context(), `INSERT INTO weixin_chat_messages (id,chat_session_id,binding_id,direction,role,content,status) VALUES ('legacy-message','legacy-session','binding','inbound','user','message','completed')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteSession(t.Context(), legacySession.ID); err != nil {
		t.Fatal(err)
	}
	var legacyCount int
	if err := store.db.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM weixin_chat_sessions WHERE linked_session_id=$1) + (SELECT count(*) FROM weixin_chat_messages WHERE chat_session_id='legacy-session')`, legacySession.ID).Scan(&legacyCount); err != nil || legacyCount != 0 {
		t.Fatalf("legacy rows=%d err=%v", legacyCount, err)
	}
	store.Close()

	restarted, err := NewPostgresStore(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if _, found, err := restarted.GetSession(t.Context(), legacySession.ID); err != nil || found {
		t.Fatalf("deleted session after restart found=%v err=%v", found, err)
	}
}
