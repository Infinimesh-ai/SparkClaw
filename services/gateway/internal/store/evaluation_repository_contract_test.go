package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
)

func TestEvaluationRepositoryMemoryAndFileContract(t *testing.T) {
	for _, backend := range []string{"memory", "file"} {
		t.Run(backend, func(t *testing.T) {
			var repository testBackend
			var restart func() testBackend
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
				restart = func() testBackend {
					reloaded, err := NewFileStore(path)
					if err != nil {
						t.Fatal(err)
					}
					return reloaded
				}
			}
			exerciseEvaluationRepositoryContract(t, repository, restart)
		})
	}
}

func exerciseEvaluationRepositoryContract(t *testing.T, repository testBackend, restart func() testBackend) {
	t.Helper()
	at := time.Date(2026, 8, 21, 10, 0, 0, 123456789, time.FixedZone("contract", 8*60*60))
	completedAt := at.Add(time.Minute)
	input := app.EvalRun{
		ID: "eval-b", Profile: "smoke", Status: "failed", Summary: "original", StartedAt: at, CompletedAt: &completedAt,
		Cases:           []app.EvalCase{{Name: "case-original", Status: "failed"}},
		FailureArchives: []app.EvalArtifact{{CaseName: "case-original", URI: "artifact://failure"}},
	}
	stored, err := repository.SaveEvalRun(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if stored.StartedAt.Location() != time.UTC || stored.StartedAt.Nanosecond() != 123456000 || stored.CompletedAt == nil || stored.CompletedAt.Location() != time.UTC {
		t.Fatalf("normalized evaluation = %#v", stored)
	}
	input.Cases[0].Name = "mutated-input"
	input.FailureArchives[0].URI = "mutated-input"
	stored.Cases[0].Name = "mutated-output"
	stored.FailureArchives[0].URI = "mutated-output"
	got, found, err := repository.GetEvalRun(t.Context(), input.ID)
	if err != nil || !found || got.Cases[0].Name != "case-original" || got.FailureArchives[0].URI != "artifact://failure" {
		t.Fatalf("isolated evaluation = %#v found=%t err=%v", got, found, err)
	}
	if _, found, err := repository.GetEvalRun(t.Context(), "missing"); err != nil || found {
		t.Fatalf("missing evaluation found=%t err=%v", found, err)
	}
	if _, err := repository.SaveEvalRun(t.Context(), app.EvalRun{ID: "eval-a", Status: "passed", StartedAt: at}); err != nil {
		t.Fatal(err)
	}
	runs, err := repository.ListEvalRuns(t.Context())
	if err != nil || len(runs) != 2 || runs[0].ID != "eval-a" || runs[1].ID != "eval-b" {
		t.Fatalf("stable evaluation order = %#v err=%v", runs, err)
	}
	runs[1].Cases[0].Name = "mutated-list"
	again, _, err := repository.GetEvalRun(t.Context(), "eval-b")
	if err != nil || again.Cases[0].Name != "case-original" {
		t.Fatalf("list exposed aliases: %#v err=%v", again, err)
	}
	if _, err := repository.SaveEvalRun(t.Context(), app.EvalRun{ID: "eval-b", Status: "passed", Summary: "updated", StartedAt: at}); err != nil {
		t.Fatal(err)
	}
	if updated, found, err := repository.GetEvalRun(t.Context(), "eval-b"); err != nil || !found || updated.Summary != "updated" {
		t.Fatalf("evaluation overwrite = %#v found=%t err=%v", updated, found, err)
	}
	audits := mustListAudit(t, repository, "")
	events := mustEventsAfter(t, repository, "", "")
	if !hasAuditType(audits, "eval.passed") || !hasEventType(events, "eval.passed") {
		t.Fatal("evaluation lifecycle was not persisted")
	}
	for _, audit := range audits {
		if audit.Type == "eval.passed" && audit.RunID != "" {
			t.Fatalf("evaluation audit unexpectedly claimed run scope: %#v", audit)
		}
	}
	if events[len(events)-1].Type != "eval.passed" || events[len(events)-1].RunID != "eval-b" {
		t.Fatalf("evaluation event scope = %#v", events[len(events)-1])
	}

	if restart != nil {
		repository = restart()
		if updated, found, err := repository.GetEvalRun(t.Context(), "eval-b"); err != nil || !found || updated.Summary != "updated" {
			t.Fatalf("evaluation restart = %#v found=%t err=%v", updated, found, err)
		}
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repository.SaveEvalRun(canceled, app.EvalRun{ID: "eval-cancel"}); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("canceled save code=%q err=%v", StoreErrorCodeOf(err), err)
	}
	if _, _, err := repository.GetEvalRun(canceled, "eval-b"); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("canceled get code=%q err=%v", StoreErrorCodeOf(err), err)
	}
	if _, err := repository.ListEvalRuns(canceled); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("canceled list code=%q err=%v", StoreErrorCodeOf(err), err)
	}
}

func TestPostgresEvaluationSaveUsesAtomicLifecycleTransaction(t *testing.T) {
	sentinel := errors.New("evaluation lifecycle failed")
	for _, testCase := range []struct {
		name      string
		execError error
	}{
		{name: "success"},
		{name: "event failure rolls back", execError: sentinel},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			transaction := &fakeConversationPostgresTx{execErrors: map[int]error{}}
			if testCase.execError != nil {
				transaction.execErrors[2] = testCase.execError
			}
			session := &fakeConversationPostgresSession{transaction: transaction}
			repository := &PostgresStore{operationTimeouts: defaultOperationTimeouts, evaluationPostgres: &fakeConversationPostgresOps{session: session}}
			stored, err := repository.SaveEvalRun(t.Context(), app.EvalRun{ID: "eval-postgres", Status: "passed"})
			if testCase.execError == nil {
				if err != nil || stored.ID != "eval-postgres" || transaction.commits != 1 || transaction.rollbacks != 0 {
					t.Fatalf("stored=%#v err=%v commits=%d rollbacks=%d", stored, err, transaction.commits, transaction.rollbacks)
				}
			} else if stored.ID != "" || StoreErrorCodeOf(err) != StoreErrorUnavailable || !errors.Is(err, sentinel) || transaction.commits != 0 || transaction.rollbacks != 1 {
				t.Fatalf("stored=%#v err=%v commits=%d rollbacks=%d", stored, err, transaction.commits, transaction.rollbacks)
			}
			if len(transaction.execSQL) != 3 || !strings.Contains(transaction.execSQL[0], "INSERT INTO eval_runs") ||
				!strings.Contains(transaction.execSQL[1], "INSERT INTO audit_events") || !strings.Contains(transaction.execSQL[2], "INSERT INTO events") ||
				session.releases != 1 || session.terminates != 0 {
				t.Fatalf("exec=%#v releases=%d terminates=%d", transaction.execSQL, session.releases, session.terminates)
			}
		})
	}
}

type fakeEvaluationPostgresRow struct {
	run             app.EvalRun
	cases           []byte
	failureArchives []byte
	err             error
}

func (r fakeEvaluationPostgresRow) Scan(destinations ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(destinations) != 8 {
		return errors.New("unexpected evaluation row shape")
	}
	*(destinations[0].(*string)) = r.run.ID
	*(destinations[1].(*string)) = r.run.Profile
	*(destinations[2].(*string)) = r.run.Status
	*(destinations[3].(*string)) = r.run.Summary
	*(destinations[4].(*[]byte)) = append([]byte(nil), r.cases...)
	*(destinations[5].(*[]byte)) = append([]byte(nil), r.failureArchives...)
	*(destinations[6].(*time.Time)) = r.run.StartedAt
	*(destinations[7].(**time.Time)) = cloneTimePointer(r.run.CompletedAt)
	return nil
}

type fakeEvaluationPostgresRows struct {
	rows  []fakeEvaluationPostgresRow
	index int
	err   error
}

func (r *fakeEvaluationPostgresRows) Next() bool { return r.index < len(r.rows) }
func (r *fakeEvaluationPostgresRows) Scan(destinations ...any) error {
	row := r.rows[r.index]
	r.index++
	return row.Scan(destinations...)
}
func (r *fakeEvaluationPostgresRows) Err() error { return r.err }
func (r *fakeEvaluationPostgresRows) Close()     {}

func TestPostgresEvaluationReadsPropagateBackendErrors(t *testing.T) {
	sentinel := errors.New("evaluation read failed")
	missing := &PostgresStore{operationTimeouts: defaultOperationTimeouts, evaluationPostgres: &fakeConversationPostgresOps{
		rowQueue: []onboardingPostgresRow{fakeEvaluationPostgresRow{err: pgx.ErrNoRows}},
	}}
	if run, found, err := missing.GetEvalRun(t.Context(), "missing"); err != nil || found || run.ID != "" {
		t.Fatalf("missing run=%#v found=%t err=%v", run, found, err)
	}
	failedGet := &PostgresStore{operationTimeouts: defaultOperationTimeouts, evaluationPostgres: &fakeConversationPostgresOps{
		rowQueue: []onboardingPostgresRow{fakeEvaluationPostgresRow{err: sentinel}},
	}}
	if _, _, err := failedGet.GetEvalRun(t.Context(), "eval"); StoreErrorCodeOf(err) != StoreErrorUnavailable || !errors.Is(err, sentinel) {
		t.Fatalf("get error=%v", err)
	}

	for _, testCase := range []struct {
		name    string
		backend *fakeConversationPostgresOps
		code    StoreErrorCode
	}{
		{name: "query", backend: &fakeConversationPostgresOps{queryErr: sentinel}, code: StoreErrorUnavailable},
		{name: "scan", backend: &fakeConversationPostgresOps{rows: &fakeEvaluationPostgresRows{rows: []fakeEvaluationPostgresRow{{err: sentinel}}}}, code: StoreErrorUnavailable},
		{name: "rows", backend: &fakeConversationPostgresOps{rows: &fakeEvaluationPostgresRows{err: sentinel}}, code: StoreErrorUnavailable},
		{name: "corrupt cases", backend: &fakeConversationPostgresOps{rows: &fakeEvaluationPostgresRows{rows: []fakeEvaluationPostgresRow{{run: app.EvalRun{ID: "eval-corrupt", StartedAt: time.Now()}, cases: []byte("{"), failureArchives: []byte("[]")}}}}, code: StoreErrorCorrupt},
		{name: "corrupt archives", backend: &fakeConversationPostgresOps{rows: &fakeEvaluationPostgresRows{rows: []fakeEvaluationPostgresRow{{run: app.EvalRun{ID: "eval-corrupt", StartedAt: time.Now()}, cases: []byte("[]"), failureArchives: []byte("{")}}}}, code: StoreErrorCorrupt},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repository := &PostgresStore{operationTimeouts: defaultOperationTimeouts, evaluationPostgres: testCase.backend}
			runs, err := repository.ListEvalRuns(t.Context())
			if runs != nil || StoreErrorCodeOf(err) != testCase.code {
				t.Fatalf("runs=%#v code=%q err=%v", runs, StoreErrorCodeOf(err), err)
			}
		})
	}
}

func TestPostgresEvaluationRepositoryConfiguredContract(t *testing.T) {
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
	exerciseEvaluationRepositoryContract(t, repository, nil)
}
