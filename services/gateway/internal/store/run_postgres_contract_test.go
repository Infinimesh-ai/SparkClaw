package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakeRunPostgresOps struct {
	session    onboardingPostgresSession
	acquireErr error
	row        onboardingPostgresRow
}

func (o *fakeRunPostgresOps) Acquire(context.Context) (onboardingPostgresSession, error) {
	return o.session, o.acquireErr
}

func (*fakeRunPostgresOps) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("unexpected pool Exec")
}

func (*fakeRunPostgresOps) Query(context.Context, string, ...any) (onboardingPostgresRows, error) {
	return nil, errors.New("unexpected pool Query")
}

func (o *fakeRunPostgresOps) QueryRow(context.Context, string, ...any) onboardingPostgresRow {
	if o.row == nil {
		return fakeRunPostgresRow{err: errors.New("unexpected pool QueryRow")}
	}
	return o.row
}

type fakeRunPostgresSession struct {
	transaction  onboardingPostgresTx
	beginErr     error
	releases     int
	terminates   int
	terminateErr error
}

func (s *fakeRunPostgresSession) Begin(context.Context, pgx.TxOptions) (onboardingPostgresTx, error) {
	return s.transaction, s.beginErr
}

func (s *fakeRunPostgresSession) Release() { s.releases++ }

func (s *fakeRunPostgresSession) Terminate(context.Context) error {
	s.terminates++
	return s.terminateErr
}

type fakeRunPostgresTx struct {
	execSQL     []string
	execErrors  map[int]error
	rowSQL      []string
	row         onboardingPostgresRow
	commitErr   error
	rollbackErr error
	commits     int
	rollbacks   int
}

func (t *fakeRunPostgresTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	index := len(t.execSQL)
	t.execSQL = append(t.execSQL, sql)
	if err := t.execErrors[index]; err != nil {
		return pgconn.CommandTag{}, err
	}
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (t *fakeRunPostgresTx) QueryRow(_ context.Context, sql string, _ ...any) onboardingPostgresRow {
	t.rowSQL = append(t.rowSQL, sql)
	if t.row == nil {
		return fakeRunPostgresRow{err: pgx.ErrNoRows}
	}
	return t.row
}

func (*fakeRunPostgresTx) Query(context.Context, string, ...any) (onboardingPostgresRows, error) {
	return nil, errors.New("unexpected transaction Query")
}

func (t *fakeRunPostgresTx) Commit(context.Context) error {
	t.commits++
	return t.commitErr
}

func (t *fakeRunPostgresTx) Rollback(context.Context) error {
	t.rollbacks++
	return t.rollbackErr
}

type fakeRunPostgresRow struct {
	scan func(...any) error
	err  error
}

func (r fakeRunPostgresRow) Scan(destinations ...any) error {
	if r.err != nil {
		return r.err
	}
	return r.scan(destinations...)
}

func newFakeRunPostgresStore(transaction *fakeRunPostgresTx) (*PostgresStore, *fakeRunPostgresSession) {
	session := &fakeRunPostgresSession{transaction: transaction}
	operations := &fakeRunPostgresOps{session: session}
	return &PostgresStore{runPostgres: operations, operationTimeouts: defaultOperationTimeouts}, session
}

func TestPostgresRunAndToolWritesAreAtomicLifecycleTransactions(t *testing.T) {
	tests := []struct {
		name    string
		invoke  func(*PostgresStore) error
		wantSQL []string
	}{
		{
			name: "run",
			invoke: func(repository *PostgresStore) error {
				_, err := repository.SaveRun(t.Context(), app.AgentRun{ID: "run-atomic", SessionID: "session", State: "completed"})
				return err
			},
			wantSQL: []string{"pg_advisory_xact_lock", "INSERT INTO agent_runs", "INSERT INTO events"},
		},
		{
			name: "tool call",
			invoke: func(repository *PostgresStore) error {
				_, err := repository.SaveToolCall(t.Context(), app.ToolCall{ID: "tool-atomic", SessionID: "session", RunID: "run", Tool: "files.read", Status: app.ToolCallStatusCompleted, Arguments: map[string]any{"path": "a.txt"}})
				return err
			},
			wantSQL: []string{"pg_advisory_xact_lock", "INSERT INTO tool_calls", "INSERT INTO audit_events", "INSERT INTO events"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transaction := &fakeRunPostgresTx{}
			repository, session := newFakeRunPostgresStore(transaction)
			if err := test.invoke(repository); err != nil {
				t.Fatal(err)
			}
			if len(transaction.execSQL) != len(test.wantSQL) || transaction.commits != 1 || transaction.rollbacks != 0 || session.releases != 1 || session.terminates != 0 {
				t.Fatalf("transaction ownership: sql=%d commits=%d rollbacks=%d releases=%d terminates=%d", len(transaction.execSQL), transaction.commits, transaction.rollbacks, session.releases, session.terminates)
			}
			for index, fragment := range test.wantSQL {
				if !strings.Contains(transaction.execSQL[index], fragment) {
					t.Fatalf("statement %d = %q, want %q", index, transaction.execSQL[index], fragment)
				}
			}
		})
	}
}

func TestPostgresRunWriteFailureClassificationAndOwnership(t *testing.T) {
	unsafeFailure := errors.New("transport outcome unknown")
	commitFailure := errors.New("commit outcome unknown")
	postgresFailure := &pgconn.PgError{Code: "23503", Message: "foreign key violation"}
	tests := []struct {
		name          string
		execErrors    map[int]error
		commitErr     error
		wantCode      StoreErrorCode
		wantCandidate bool
		wantRelease   int
		wantTerminate int
		wantRollback  int
		wantCause     error
	}{
		{name: "unsafe lock", execErrors: map[int]error{0: unsafeFailure}, wantCode: StoreErrorUnknownOutcome, wantRelease: 0, wantTerminate: 1, wantCause: unsafeFailure},
		{name: "unsafe record", execErrors: map[int]error{1: unsafeFailure}, wantCode: StoreErrorUnknownOutcome, wantCandidate: true, wantRelease: 0, wantTerminate: 1, wantCause: unsafeFailure},
		{name: "safe postgres record", execErrors: map[int]error{1: postgresFailure}, wantCode: StoreErrorInternal, wantRelease: 1, wantRollback: 1, wantCause: postgresFailure},
		{name: "commit unknown", commitErr: commitFailure, wantCode: StoreErrorUnknownOutcome, wantCandidate: true, wantRelease: 0, wantTerminate: 1, wantCause: commitFailure},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transaction := &fakeRunPostgresTx{execErrors: test.execErrors, commitErr: test.commitErr}
			repository, session := newFakeRunPostgresStore(transaction)
			candidate, err := repository.SaveRun(t.Context(), app.AgentRun{ID: "run-fault", SessionID: "session", State: "running"})
			if StoreErrorCodeOf(err) != test.wantCode || errors.Is(err, test.wantCause) != (test.wantCause != nil) {
				t.Fatalf("SaveRun error = %v code=%q", err, StoreErrorCodeOf(err))
			}
			if (candidate.ID != "") != test.wantCandidate || session.releases != test.wantRelease || session.terminates != test.wantTerminate || transaction.rollbacks != test.wantRollback {
				t.Fatalf("candidate=%#v releases=%d terminates=%d rollbacks=%d", candidate, session.releases, session.terminates, transaction.rollbacks)
			}
		})
	}
}

func TestPostgresToolCallUnknownOutcomesReturnReconciliationCandidate(t *testing.T) {
	unsafeFailure := errors.New("audit submission outcome unknown")
	commitFailure := errors.New("commit outcome unknown")
	tests := []struct {
		name       string
		execErrors map[int]error
		commitErr  error
		wantCause  error
	}{
		{name: "unsafe lifecycle statement", execErrors: map[int]error{2: unsafeFailure}, wantCause: unsafeFailure},
		{name: "commit unknown", commitErr: commitFailure, wantCause: commitFailure},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transaction := &fakeRunPostgresTx{execErrors: test.execErrors, commitErr: test.commitErr}
			repository, session := newFakeRunPostgresStore(transaction)
			candidate, err := repository.SaveToolCall(t.Context(), app.ToolCall{
				ID: "tool-fault", SessionID: "session", RunID: "run", Tool: "files.read", Status: app.ToolCallStatusCompleted,
				Arguments: map[string]any{"path": "a.txt"},
			})
			if candidate.ID != "tool-fault" || candidate.StartedAt.IsZero() || StoreErrorCodeOf(err) != StoreErrorUnknownOutcome || !errors.Is(err, test.wantCause) {
				t.Fatalf("candidate=%#v err=%v code=%q", candidate, err, StoreErrorCodeOf(err))
			}
			if transaction.rollbacks != 0 || session.releases != 0 || session.terminates != 1 {
				t.Fatalf("rollback=%d release=%d terminate=%d", transaction.rollbacks, session.releases, session.terminates)
			}
		})
	}
}

func TestPostgresRunFeedbackLocksBeforeDeduplication(t *testing.T) {
	created := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)
	existing := app.RunFeedback{
		ID: "feedback-existing", SessionID: "session", RunID: "run", MessageID: "message", Rating: "up",
		CreatedAt: created, UpdatedAt: created,
	}
	transaction := &fakeRunPostgresTx{row: fakeRunPostgresRow{scan: func(destinations ...any) error {
		*destinations[0].(*string) = existing.ID
		*destinations[1].(*string) = existing.SessionID
		*destinations[2].(*string) = existing.RunID
		*destinations[3].(*string) = existing.MessageID
		*destinations[4].(*string) = existing.Rating
		*destinations[5].(*string) = existing.Note
		*destinations[6].(*string) = existing.Correction
		*destinations[7].(*time.Time) = existing.CreatedAt
		*destinations[8].(*time.Time) = existing.UpdatedAt
		return nil
	}}}
	repository, session := newFakeRunPostgresStore(transaction)
	saved, err := repository.SaveRunFeedback(t.Context(), app.RunFeedback{SessionID: "session", RunID: "run", MessageID: "message", Rating: "down"})
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID != existing.ID || !saved.CreatedAt.Equal(existing.CreatedAt) || len(transaction.rowSQL) != 1 || len(transaction.execSQL) != 4 ||
		!strings.Contains(transaction.execSQL[0], "pg_advisory_xact_lock") || !strings.Contains(transaction.rowSQL[0], "FROM run_feedback") ||
		!strings.Contains(transaction.execSQL[1], "INSERT INTO run_feedback") || session.releases != 1 {
		t.Fatalf("feedback transaction = %#v rowSQL=%#v execSQL=%#v releases=%d", saved, transaction.rowSQL, transaction.execSQL, session.releases)
	}
}

func TestPostgresRunCorruptJSONIsExplicit(t *testing.T) {
	t.Run("run workflow", func(t *testing.T) {
		row := fakeRunPostgresRow{scan: func(destinations ...any) error {
			*destinations[0].(*string) = "run-corrupt"
			*destinations[1].(*string) = "session"
			*destinations[2].(*string) = "running"
			*destinations[3].(*string) = "fast"
			*destinations[4].(*string) = string(app.RiskRead)
			*destinations[5].(*time.Time) = time.Now().UTC()
			*destinations[7].(*string) = ""
			*destinations[8].(*[]byte) = []byte(`{"broken"`)
			return nil
		}}
		repository := &PostgresStore{runPostgres: &fakeRunPostgresOps{row: row}, operationTimeouts: defaultOperationTimeouts}
		if _, _, err := repository.GetRun(t.Context(), "run-corrupt"); StoreErrorCodeOf(err) != StoreErrorCorrupt {
			t.Fatalf("GetRun corrupt error = %v code=%q", err, StoreErrorCodeOf(err))
		}
	})

	t.Run("tool arguments", func(t *testing.T) {
		row := fakeRunPostgresRow{scan: func(destinations ...any) error {
			*destinations[0].(*string) = "tool-corrupt"
			*destinations[1].(*string) = "session"
			*destinations[2].(*string) = "run"
			*destinations[7].(*string) = "files.read"
			*destinations[8].(*string) = string(app.RiskRead)
			*destinations[9].(*string) = "completed"
			*destinations[10].(*[]byte) = []byte(`{"broken"`)
			*destinations[15].(*time.Time) = time.Now().UTC()
			return nil
		}}
		repository := &PostgresStore{runPostgres: &fakeRunPostgresOps{row: row}, operationTimeouts: defaultOperationTimeouts}
		if _, _, err := repository.GetToolCall(t.Context(), "tool-corrupt"); StoreErrorCodeOf(err) != StoreErrorCorrupt {
			t.Fatalf("GetToolCall corrupt error = %v code=%q", err, StoreErrorCodeOf(err))
		}
	})
}

func TestPostgresRunFeedbackConcurrentDedupe(t *testing.T) {
	dsn := os.Getenv("SPARKCLAW_TEST_POSTGRES_DSN")
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
	session, err := first.CreateSession(t.Context(), "Concurrent feedback")
	if err != nil {
		t.Fatal(err)
	}
	run, err := first.SaveRun(t.Context(), app.AgentRun{ID: app.NewID("run_feedback"), SessionID: session.ID, State: "completed"})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan app.RunFeedback, 2)
	errorsFound := make(chan error, 2)
	var wait sync.WaitGroup
	for index, repository := range []*PostgresStore{first, second} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			feedback, err := repository.SaveRunFeedback(t.Context(), app.RunFeedback{
				SessionID: session.ID, RunID: run.ID, MessageID: "message-shared", Rating: []string{"up", "down"}[index],
			})
			if err != nil {
				errorsFound <- err
				return
			}
			results <- feedback
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
	for feedback := range results {
		ids = append(ids, feedback.ID)
	}
	items, err := first.ListRunFeedback(t.Context(), run.ID)
	if err != nil || len(ids) != 2 || ids[0] != ids[1] || len(items) != 1 || items[0].ID != ids[0] {
		t.Fatalf("concurrent feedback ids=%#v items=%#v err=%v", ids, items, err)
	}
}
