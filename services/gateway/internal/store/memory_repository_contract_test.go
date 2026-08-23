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
	"github.com/jackc/pgx/v5/pgconn"
)

func TestMemoryRepositoryMemoryAndFileContract(t *testing.T) {
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
			exerciseMemoryRepositoryContract(t, repository, restart)
		})
	}
}

func exerciseMemoryRepositoryContract(t *testing.T, repository testBackend, restart func() testBackend) {
	t.Helper()
	session := mustCreateSession(t, repository, "memory contract")
	run := app.AgentRun{ID: "run-memory-contract", SessionID: session.ID, State: "completed", StartedAt: time.Now().UTC()}
	if _, err := repository.SaveRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	replacementSession := mustCreateSession(t, repository, "memory replacement contract")
	replacementRun := app.AgentRun{ID: "run-memory-replacement", SessionID: replacementSession.ID, State: "completed", StartedAt: time.Now().UTC()}
	if _, err := repository.SaveRun(t.Context(), replacementRun); err != nil {
		t.Fatal(err)
	}

	createdAt := time.Date(2026, 8, 21, 10, 0, 0, 123456789, time.FixedZone("contract", 8*60*60))
	candidateB, err := repository.AddMemoryCandidate(t.Context(), app.MemoryCandidate{
		ID: "candidate-b", SessionID: session.ID, RunID: run.ID, Kind: "profile", Content: "original memory",
		Sensitivity: "normal", Status: "pending", Reason: "contract", CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if candidateB.CreatedAt.Location() != time.UTC || candidateB.CreatedAt.Nanosecond() != 123456000 {
		t.Fatalf("normalized candidate time = %v", candidateB.CreatedAt)
	}
	resolvedAt := createdAt.Add(time.Hour)
	if _, err := repository.AddMemoryCandidate(t.Context(), app.MemoryCandidate{
		ID: "candidate-overwrite", SessionID: session.ID, RunID: run.ID, Kind: "original-kind", Content: "original content",
		Sensitivity: "sensitive", Status: "accepted", Reason: "original reason", CreatedAt: createdAt, ResolvedAt: &resolvedAt,
	}); err != nil {
		t.Fatal(err)
	}
	replacementCreatedAt := createdAt.Add(2 * time.Hour)
	if _, err := repository.AddMemoryCandidate(t.Context(), app.MemoryCandidate{
		ID: "candidate-overwrite", SessionID: replacementSession.ID, RunID: replacementRun.ID, Kind: "replacement-kind", Content: "replacement content",
		Sensitivity: "normal", Status: "rejected", Reason: "replacement reason", CreatedAt: replacementCreatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	rejected, err := repository.ListMemoryCandidates(t.Context(), "rejected")
	if err != nil || len(rejected) != 1 {
		t.Fatalf("replacement candidates = %#v err=%v", rejected, err)
	}
	replacement := rejected[0]
	if replacement.SessionID != replacementSession.ID || replacement.RunID != replacementRun.ID || replacement.Kind != "replacement-kind" ||
		replacement.Content != "replacement content" || replacement.Sensitivity != "normal" || replacement.Reason != "replacement reason" ||
		!replacement.CreatedAt.Equal(postgresTime(replacementCreatedAt)) || replacement.ResolvedAt != nil {
		t.Fatalf("candidate duplicate ID did not fully replace record: %#v", replacement)
	}
	if _, err := repository.AddMemoryCandidate(t.Context(), app.MemoryCandidate{
		ID: "candidate-a", SessionID: session.ID, RunID: run.ID, Kind: "profile", Content: "pending memory",
		Sensitivity: "normal", Status: "pending", Reason: "contract", CreatedAt: createdAt,
	}); err != nil {
		t.Fatal(err)
	}
	pending, err := repository.ListMemoryCandidates(t.Context(), "pending")
	if err != nil || len(pending) != 2 || pending[0].ID != "candidate-a" || pending[1].ID != "candidate-b" {
		t.Fatalf("stable candidate order = %#v err=%v", pending, err)
	}

	resolved, memory, err := repository.ResolveMemoryCandidate(t.Context(), candidateB.ID, "accepted")
	if err != nil || memory == nil || resolved.ResolvedAt == nil || memory.Content != candidateB.Content {
		t.Fatalf("resolved=%#v memory=%#v err=%v", resolved, memory, err)
	}
	resolvedAt = *resolved.ResolvedAt
	*resolved.ResolvedAt = resolved.ResolvedAt.Add(time.Hour)
	accepted, err := repository.ListMemoryCandidates(t.Context(), "accepted")
	if err != nil || len(accepted) != 1 || accepted[0].ResolvedAt == nil || !accepted[0].ResolvedAt.Equal(resolvedAt) {
		t.Fatalf("candidate alias isolation = %#v err=%v", accepted, err)
	}
	if _, _, err := repository.ResolveMemoryCandidate(t.Context(), candidateB.ID, "rejected"); StoreErrorCodeOf(err) != StoreErrorConflict {
		t.Fatalf("second resolve code=%q err=%v", StoreErrorCodeOf(err), err)
	}
	if _, _, err := repository.ResolveMemoryCandidate(t.Context(), "missing", "accepted"); StoreErrorCodeOf(err) != StoreErrorNotFound {
		t.Fatalf("missing candidate code=%q err=%v", StoreErrorCodeOf(err), err)
	}

	matches, err := repository.SearchMemories(t.Context(), "ORIGINAL")
	if err != nil || len(matches) != 1 || matches[0].ID != memory.ID {
		t.Fatalf("memory search = %#v err=%v", matches, err)
	}
	updated, err := repository.UpdateMemory(t.Context(), memory.ID, "procedural", "updated memory")
	if err != nil || updated.Kind != "procedural" || updated.Content != "updated memory" || !updated.CreatedAt.Equal(memory.CreatedAt) {
		t.Fatalf("updated memory = %#v err=%v", updated, err)
	}
	if _, err := repository.UpdateMemory(t.Context(), "missing", "profile", "missing"); StoreErrorCodeOf(err) != StoreErrorNotFound {
		t.Fatalf("missing update code=%q err=%v", StoreErrorCodeOf(err), err)
	}
	deleted, err := repository.DeleteMemory(t.Context(), memory.ID)
	if err != nil || deleted.ID != memory.ID {
		t.Fatalf("deleted memory = %#v err=%v", deleted, err)
	}
	if _, err := repository.DeleteMemory(t.Context(), memory.ID); StoreErrorCodeOf(err) != StoreErrorNotFound {
		t.Fatalf("missing delete code=%q err=%v", StoreErrorCodeOf(err), err)
	}

	pruneCandidate, err := repository.AddMemoryCandidate(t.Context(), app.MemoryCandidate{
		ID: "candidate-prune", SessionID: session.ID, RunID: run.ID, Kind: "retention", Content: "prune memory", Status: "pending",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, pruneMemory, err := repository.ResolveMemoryCandidate(t.Context(), pruneCandidate.ID, "accepted")
	if err != nil || pruneMemory == nil {
		t.Fatalf("prune candidate memory=%#v err=%v", pruneMemory, err)
	}
	pruned, err := repository.PruneMemories(t.Context(), time.Now().UTC().Add(time.Hour))
	if err != nil || len(pruned) != 1 || pruned[0].ID != pruneMemory.ID {
		t.Fatalf("pruned memories = %#v err=%v", pruned, err)
	}
	if empty, err := repository.PruneMemories(t.Context(), time.Time{}); err != nil || len(empty) != 0 {
		t.Fatalf("zero-cutoff prune = %#v err=%v", empty, err)
	}

	audits := mustListAudit(t, repository, session.ID)
	events := mustEventsAfter(t, repository, session.ID, "")
	for _, eventType := range []string{"memory_candidate.created", "memory_candidate.accepted", "memory.updated", "memory.deleted", "memory.pruned"} {
		if !hasAuditType(audits, eventType) || !hasEventType(events, eventType) {
			t.Fatalf("missing lifecycle %q audits=%#v events=%#v", eventType, audits, events)
		}
	}

	if restart != nil {
		repository = restart()
		pending, err = repository.ListMemoryCandidates(t.Context(), "pending")
		if err != nil || len(pending) != 1 || pending[0].ID != "candidate-a" {
			t.Fatalf("candidate restart = %#v err=%v", pending, err)
		}
		memories, err := repository.SearchMemories(t.Context(), "")
		if err != nil || len(memories) != 0 {
			t.Fatalf("memory restart = %#v err=%v", memories, err)
		}
	}

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repository.AddMemoryCandidate(canceled, app.MemoryCandidate{}); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("canceled add code=%q err=%v", StoreErrorCodeOf(err), err)
	}
	if _, _, err := repository.ResolveMemoryCandidate(canceled, "candidate-a", "accepted"); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("canceled resolve code=%q err=%v", StoreErrorCodeOf(err), err)
	}
	if _, err := repository.ListMemoryCandidates(canceled, ""); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("canceled candidate list code=%q err=%v", StoreErrorCodeOf(err), err)
	}
	if _, err := repository.SearchMemories(canceled, ""); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("canceled search code=%q err=%v", StoreErrorCodeOf(err), err)
	}
	if _, err := repository.UpdateMemory(canceled, "missing", "profile", "memory"); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("canceled update code=%q err=%v", StoreErrorCodeOf(err), err)
	}
	if _, err := repository.DeleteMemory(canceled, "missing"); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("canceled delete code=%q err=%v", StoreErrorCodeOf(err), err)
	}
	if _, err := repository.PruneMemories(canceled, time.Now()); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("canceled prune code=%q err=%v", StoreErrorCodeOf(err), err)
	}
}

type fakeMemoryPostgresOps struct {
	session  onboardingPostgresSession
	rows     onboardingPostgresRows
	queryErr error
}

func (o *fakeMemoryPostgresOps) Acquire(context.Context) (onboardingPostgresSession, error) {
	return o.session, nil
}
func (o *fakeMemoryPostgresOps) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("unexpected memory pool Exec")
}
func (o *fakeMemoryPostgresOps) Query(context.Context, string, ...any) (onboardingPostgresRows, error) {
	return o.rows, o.queryErr
}
func (o *fakeMemoryPostgresOps) QueryRow(context.Context, string, ...any) onboardingPostgresRow {
	return fakeMemoryPostgresRow{err: errors.New("unexpected memory pool QueryRow")}
}

type fakeMemoryPostgresSession struct {
	transaction onboardingPostgresTx
	releases    int
	terminates  int
}

func (s *fakeMemoryPostgresSession) Begin(context.Context, pgx.TxOptions) (onboardingPostgresTx, error) {
	return s.transaction, nil
}
func (s *fakeMemoryPostgresSession) Release() { s.releases++ }
func (s *fakeMemoryPostgresSession) Terminate(context.Context) error {
	s.terminates++
	return nil
}

type fakeMemoryPostgresTx struct {
	rowQueue   []onboardingPostgresRow
	rows       onboardingPostgresRows
	queryErr   error
	rowSQL     []string
	querySQL   []string
	execSQL    []string
	execErrors map[int]error
	commits    int
	rollbacks  int
}

func (t *fakeMemoryPostgresTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	index := len(t.execSQL)
	t.execSQL = append(t.execSQL, sql)
	if err := t.execErrors[index]; err != nil {
		return pgconn.CommandTag{}, err
	}
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}
func (t *fakeMemoryPostgresTx) QueryRow(_ context.Context, sql string, _ ...any) onboardingPostgresRow {
	t.rowSQL = append(t.rowSQL, sql)
	if len(t.rowQueue) == 0 {
		return fakeMemoryPostgresRow{err: errors.New("unexpected memory transaction QueryRow")}
	}
	row := t.rowQueue[0]
	t.rowQueue = t.rowQueue[1:]
	return row
}
func (t *fakeMemoryPostgresTx) Query(_ context.Context, sql string, _ ...any) (onboardingPostgresRows, error) {
	t.querySQL = append(t.querySQL, sql)
	return t.rows, t.queryErr
}
func (t *fakeMemoryPostgresTx) Commit(context.Context) error {
	t.commits++
	return nil
}
func (t *fakeMemoryPostgresTx) Rollback(context.Context) error {
	t.rollbacks++
	return nil
}

type fakeMemoryPostgresRow struct {
	candidate app.MemoryCandidate
	memory    app.Memory
	sessionID string
	kind      string
	err       error
}

func (r fakeMemoryPostgresRow) Scan(destinations ...any) error {
	if r.err != nil {
		return r.err
	}
	switch r.kind {
	case "candidate":
		if len(destinations) != 10 {
			return errors.New("unexpected memory candidate row shape")
		}
		*(destinations[0].(*string)) = r.candidate.ID
		*(destinations[1].(*string)) = r.candidate.SessionID
		*(destinations[2].(*string)) = r.candidate.RunID
		*(destinations[3].(*string)) = r.candidate.Kind
		*(destinations[4].(*string)) = r.candidate.Content
		*(destinations[5].(*string)) = r.candidate.Sensitivity
		*(destinations[6].(*string)) = r.candidate.Status
		*(destinations[7].(*string)) = r.candidate.Reason
		*(destinations[8].(*time.Time)) = r.candidate.CreatedAt
		*(destinations[9].(**time.Time)) = cloneTimePointer(r.candidate.ResolvedAt)
		return nil
	case "memory":
		if len(destinations) != 5 && len(destinations) != 6 {
			return errors.New("unexpected memory row shape")
		}
		*(destinations[0].(*string)) = r.memory.ID
		*(destinations[1].(*string)) = r.memory.Kind
		*(destinations[2].(*string)) = r.memory.Content
		*(destinations[3].(*string)) = r.memory.SourceID
		*(destinations[4].(*time.Time)) = r.memory.CreatedAt
		if len(destinations) == 6 {
			*(destinations[5].(*string)) = r.sessionID
		}
		return nil
	default:
		return errors.New("unexpected memory row kind")
	}
}

type fakeMemoryPostgresRows struct {
	rows   []fakeMemoryPostgresRow
	index  int
	err    error
	closed bool
}

func (r *fakeMemoryPostgresRows) Next() bool { return r.index < len(r.rows) }
func (r *fakeMemoryPostgresRows) Scan(destinations ...any) error {
	row := r.rows[r.index]
	r.index++
	return row.Scan(destinations...)
}
func (r *fakeMemoryPostgresRows) Err() error { return r.err }
func (r *fakeMemoryPostgresRows) Close()     { r.closed = true }

func TestPostgresMemoryWritesUseAtomicLifecycleTransactions(t *testing.T) {
	sentinel := errors.New("memory lifecycle failed")
	now := time.Now().UTC()
	candidate := app.MemoryCandidate{ID: "candidate-postgres", SessionID: "session-postgres", RunID: "run-postgres", Status: "pending", CreatedAt: now}
	memory := app.Memory{ID: "memory-postgres", Kind: "profile", Content: "memory", SourceID: candidate.RunID, CreatedAt: now}

	for _, testCase := range []struct {
		name      string
		operation func(*PostgresStore) error
		buildTx   func() *fakeMemoryPostgresTx
		primary   string
	}{
		{
			name: "add candidate", primary: "INSERT INTO memory_candidates",
			buildTx: func() *fakeMemoryPostgresTx { return &fakeMemoryPostgresTx{execErrors: map[int]error{2: sentinel}} },
			operation: func(repository *PostgresStore) error {
				_, err := repository.AddMemoryCandidate(t.Context(), candidate)
				return err
			},
		},
		{
			name: "resolve candidate", primary: "UPDATE memory_candidates",
			buildTx: func() *fakeMemoryPostgresTx {
				return &fakeMemoryPostgresTx{rowQueue: []onboardingPostgresRow{fakeMemoryPostgresRow{kind: "candidate", candidate: candidate}}, execErrors: map[int]error{3: sentinel}}
			},
			operation: func(repository *PostgresStore) error {
				_, _, err := repository.ResolveMemoryCandidate(t.Context(), candidate.ID, "accepted")
				return err
			},
		},
		{
			name: "update memory", primary: "UPDATE memories",
			buildTx: func() *fakeMemoryPostgresTx {
				return &fakeMemoryPostgresTx{rowQueue: []onboardingPostgresRow{fakeMemoryPostgresRow{kind: "memory", memory: memory, sessionID: candidate.SessionID}}, execErrors: map[int]error{1: sentinel}}
			},
			operation: func(repository *PostgresStore) error {
				_, err := repository.UpdateMemory(t.Context(), memory.ID, "profile", "updated")
				return err
			},
		},
		{
			name: "delete memory", primary: "DELETE FROM memories",
			buildTx: func() *fakeMemoryPostgresTx {
				return &fakeMemoryPostgresTx{rowQueue: []onboardingPostgresRow{fakeMemoryPostgresRow{kind: "memory", memory: memory, sessionID: candidate.SessionID}}, execErrors: map[int]error{1: sentinel}}
			},
			operation: func(repository *PostgresStore) error {
				_, err := repository.DeleteMemory(t.Context(), memory.ID)
				return err
			},
		},
		{
			name: "prune memories", primary: "DELETE FROM memories",
			buildTx: func() *fakeMemoryPostgresTx {
				return &fakeMemoryPostgresTx{rows: &fakeMemoryPostgresRows{rows: []fakeMemoryPostgresRow{{kind: "memory", memory: memory, sessionID: candidate.SessionID}}}, execErrors: map[int]error{1: sentinel}}
			},
			operation: func(repository *PostgresStore) error {
				_, err := repository.PruneMemories(t.Context(), now.Add(time.Hour))
				return err
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			transaction := testCase.buildTx()
			session := &fakeMemoryPostgresSession{transaction: transaction}
			repository := &PostgresStore{operationTimeouts: defaultOperationTimeouts, memoryPostgres: &fakeMemoryPostgresOps{session: session}}
			err := testCase.operation(repository)
			if StoreErrorCodeOf(err) != StoreErrorUnavailable || !errors.Is(err, sentinel) || transaction.commits != 0 || transaction.rollbacks != 1 || session.releases != 1 || session.terminates != 0 {
				t.Fatalf("err=%v commits=%d rollbacks=%d releases=%d terminates=%d", err, transaction.commits, transaction.rollbacks, session.releases, session.terminates)
			}
			allSQL := strings.Join(append(append(append([]string{}, transaction.rowSQL...), transaction.querySQL...), transaction.execSQL...), "\n")
			if !strings.Contains(allSQL, testCase.primary) || !strings.Contains(allSQL, "INSERT INTO audit_events") || !strings.Contains(allSQL, "INSERT INTO events") {
				t.Fatalf("transaction SQL = %q", allSQL)
			}
		})
	}
}

func TestPostgresMemoryReadsPropagateBackendErrors(t *testing.T) {
	sentinel := errors.New("memory read failed")
	for _, testCase := range []struct {
		name      string
		operation func(*PostgresStore) error
		backend   *fakeMemoryPostgresOps
	}{
		{
			name: "candidate query", backend: &fakeMemoryPostgresOps{queryErr: sentinel},
			operation: func(repository *PostgresStore) error {
				_, err := repository.ListMemoryCandidates(t.Context(), "")
				return err
			},
		},
		{
			name: "candidate scan", backend: &fakeMemoryPostgresOps{rows: &fakeMemoryPostgresRows{rows: []fakeMemoryPostgresRow{{err: sentinel}}}},
			operation: func(repository *PostgresStore) error {
				_, err := repository.ListMemoryCandidates(t.Context(), "")
				return err
			},
		},
		{
			name: "memory rows", backend: &fakeMemoryPostgresOps{rows: &fakeMemoryPostgresRows{err: sentinel}},
			operation: func(repository *PostgresStore) error {
				_, err := repository.SearchMemories(t.Context(), "")
				return err
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repository := &PostgresStore{operationTimeouts: defaultOperationTimeouts, memoryPostgres: testCase.backend}
			err := testCase.operation(repository)
			if StoreErrorCodeOf(err) != StoreErrorUnavailable || !errors.Is(err, sentinel) {
				t.Fatalf("code=%q err=%v", StoreErrorCodeOf(err), err)
			}
		})
	}
}

func TestPostgresMemoryRepositoryConfiguredContract(t *testing.T) {
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
	exerciseMemoryRepositoryContract(t, repository, nil)
}
