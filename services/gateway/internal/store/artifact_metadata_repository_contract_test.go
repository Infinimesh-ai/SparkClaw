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

func TestArtifactMetadataRepositoryMemoryAndFileContract(t *testing.T) {
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
			exerciseArtifactMetadataRepositoryContract(t, repository, restart)
		})
	}
}

func exerciseArtifactMetadataRepositoryContract(t *testing.T, repository testBackend, restart func() testBackend) {
	t.Helper()
	at := time.Date(2026, 8, 21, 10, 0, 0, 123456789, time.FixedZone("contract", 8*60*60))
	first := mustSaveArtifactObject(t, repository, app.ArtifactObject{
		ID: "artifact-b", Kind: "document", SessionID: "session-artifact", RunID: "run-b",
		URI: "artifact://shared", Key: "b", CreatedAt: at,
	})
	if first.CreatedAt.Location() != time.UTC || first.CreatedAt.Nanosecond() != 123456000 {
		t.Fatalf("normalized artifact = %#v", first)
	}
	mustSaveArtifactObject(t, repository, app.ArtifactObject{
		ID: "artifact-a", Kind: "document", SessionID: "session-artifact", RunID: "run-a",
		URI: "artifact://shared", Key: "a", CreatedAt: at,
	})
	mustSaveArtifactObject(t, repository, app.ArtifactObject{
		ID: "artifact-c", Kind: "trace", SessionID: "session-artifact", RunID: "run-c",
		URI: "artifact://shared", Key: "c", CreatedAt: at.Add(time.Minute),
	})

	objects := mustListArtifactObjects(t, repository, 0)
	if len(objects) != 3 || objects[0].ID != "artifact-c" || objects[1].ID != "artifact-a" || objects[2].ID != "artifact-b" {
		t.Fatalf("stable artifact order = %#v", objects)
	}
	if limited := mustListArtifactObjects(t, repository, 2); len(limited) != 2 || limited[1].ID != "artifact-a" {
		t.Fatalf("limited artifact order = %#v", limited)
	}
	if object, found := mustFindArtifactObjectByURI(t, repository, "artifact://shared", "session-artifact", ""); !found || object.ID != "artifact-c" {
		t.Fatalf("newest artifact = %#v found=%t", object, found)
	}
	if object, found := mustFindArtifactObjectByURI(t, repository, "artifact://shared", "session-artifact", "run-a"); !found || object.ID != "artifact-a" {
		t.Fatalf("run-scoped artifact = %#v found=%t", object, found)
	}
	if _, found := mustFindArtifactObjectByURI(t, repository, "artifact://shared", "other-session", ""); found {
		t.Fatal("artifact lookup crossed the session scope")
	}
	if _, found := mustFindArtifactObjectByURI(t, repository, "artifact://missing", "", ""); found {
		t.Fatal("missing artifact was found")
	}

	moved := first
	moved.URI = "artifact://moved"
	moved.CreatedAt = at.Add(2 * time.Minute)
	mustSaveArtifactObject(t, repository, moved)
	if object, found := mustFindArtifactObjectByURI(t, repository, "artifact://moved", "session-artifact", "run-b"); !found || object.ID != moved.ID {
		t.Fatalf("moved artifact = %#v found=%t", object, found)
	}
	if object, found := mustFindArtifactObjectByURI(t, repository, "artifact://shared", "session-artifact", ""); !found || object.ID != "artifact-c" {
		t.Fatalf("stale URI index changed shared lookup = %#v found=%t", object, found)
	}

	audits := mustListAudit(t, repository, "session-artifact")
	events := mustEventsAfter(t, repository, "session-artifact", "")
	if !hasAuditType(audits, "artifact.saved") || !hasEventType(events, "artifact.saved") {
		t.Fatal("artifact lifecycle was not persisted")
	}
	if events[len(events)-1].RunID != "run-b" {
		t.Fatalf("artifact event scope = %#v", events[len(events)-1])
	}

	if restart != nil {
		repository = restart()
		if object, found := mustFindArtifactObjectByURI(t, repository, "artifact://moved", "session-artifact", "run-b"); !found || object.ID != moved.ID {
			t.Fatalf("artifact restart = %#v found=%t", object, found)
		}
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repository.SaveArtifactObject(canceled, app.ArtifactObject{ID: "artifact-cancel"}); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("canceled save code=%q err=%v", StoreErrorCodeOf(err), err)
	}
	if _, err := repository.ListArtifactObjects(canceled, 10); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("canceled list code=%q err=%v", StoreErrorCodeOf(err), err)
	}
	if _, _, err := repository.FindArtifactObjectByURI(canceled, "artifact://moved", "", ""); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("canceled find code=%q err=%v", StoreErrorCodeOf(err), err)
	}
}

func TestPostgresArtifactMetadataSaveUsesAtomicLifecycleTransaction(t *testing.T) {
	sentinel := errors.New("artifact lifecycle failed")
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
			repository := &PostgresStore{operationTimeouts: defaultOperationTimeouts, artifactMetadataPostgres: &fakeConversationPostgresOps{session: session}}
			stored, err := repository.SaveArtifactObject(t.Context(), app.ArtifactObject{ID: "artifact-postgres", URI: "artifact://postgres"})
			if testCase.execError == nil {
				if err != nil || stored.ID != "artifact-postgres" || transaction.commits != 1 || transaction.rollbacks != 0 {
					t.Fatalf("stored=%#v err=%v commits=%d rollbacks=%d", stored, err, transaction.commits, transaction.rollbacks)
				}
			} else if stored.ID != "" || StoreErrorCodeOf(err) != StoreErrorUnavailable || !errors.Is(err, sentinel) || transaction.commits != 0 || transaction.rollbacks != 1 {
				t.Fatalf("stored=%#v err=%v commits=%d rollbacks=%d", stored, err, transaction.commits, transaction.rollbacks)
			}
			if len(transaction.execSQL) != 3 || !strings.Contains(transaction.execSQL[0], "INSERT INTO artifact_objects") ||
				!strings.Contains(transaction.execSQL[1], "INSERT INTO audit_events") || !strings.Contains(transaction.execSQL[2], "INSERT INTO events") ||
				session.releases != 1 || session.terminates != 0 {
				t.Fatalf("exec=%#v releases=%d terminates=%d", transaction.execSQL, session.releases, session.terminates)
			}
		})
	}
}

type fakeArtifactMetadataPostgresRow struct {
	object app.ArtifactObject
	err    error
}

func (r fakeArtifactMetadataPostgresRow) Scan(destinations ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(destinations) != 13 {
		return errors.New("unexpected artifact metadata row shape")
	}
	*(destinations[0].(*string)) = r.object.ID
	*(destinations[1].(*string)) = r.object.Kind
	*(destinations[2].(*string)) = r.object.RunID
	*(destinations[3].(*string)) = r.object.EvalID
	*(destinations[4].(*string)) = r.object.SessionID
	*(destinations[5].(*string)) = r.object.Backend
	*(destinations[6].(*string)) = r.object.Bucket
	*(destinations[7].(*string)) = r.object.Key
	*(destinations[8].(*string)) = r.object.URI
	*(destinations[9].(*string)) = r.object.Path
	*(destinations[10].(*string)) = r.object.ContentType
	*(destinations[11].(*int)) = r.object.Bytes
	*(destinations[12].(*time.Time)) = r.object.CreatedAt
	return nil
}

type fakeArtifactMetadataPostgresRows struct {
	rows  []fakeArtifactMetadataPostgresRow
	index int
	err   error
}

func (r *fakeArtifactMetadataPostgresRows) Next() bool { return r.index < len(r.rows) }
func (r *fakeArtifactMetadataPostgresRows) Scan(destinations ...any) error {
	row := r.rows[r.index]
	r.index++
	return row.Scan(destinations...)
}
func (r *fakeArtifactMetadataPostgresRows) Err() error { return r.err }
func (r *fakeArtifactMetadataPostgresRows) Close()     {}

func TestPostgresArtifactMetadataReadsPropagateBackendErrors(t *testing.T) {
	sentinel := errors.New("artifact metadata read failed")
	missing := &PostgresStore{operationTimeouts: defaultOperationTimeouts, artifactMetadataPostgres: &fakeConversationPostgresOps{
		rowQueue: []onboardingPostgresRow{fakeArtifactMetadataPostgresRow{err: pgx.ErrNoRows}},
	}}
	if object, found, err := missing.FindArtifactObjectByURI(t.Context(), "artifact://missing", "", ""); err != nil || found || object.ID != "" {
		t.Fatalf("missing object=%#v found=%t err=%v", object, found, err)
	}
	failedFind := &PostgresStore{operationTimeouts: defaultOperationTimeouts, artifactMetadataPostgres: &fakeConversationPostgresOps{
		rowQueue: []onboardingPostgresRow{fakeArtifactMetadataPostgresRow{err: sentinel}},
	}}
	if _, _, err := failedFind.FindArtifactObjectByURI(t.Context(), "artifact://failed", "", ""); StoreErrorCodeOf(err) != StoreErrorUnavailable || !errors.Is(err, sentinel) {
		t.Fatalf("find error=%v", err)
	}

	for _, testCase := range []struct {
		name    string
		backend *fakeConversationPostgresOps
	}{
		{name: "query", backend: &fakeConversationPostgresOps{queryErr: sentinel}},
		{name: "scan", backend: &fakeConversationPostgresOps{rows: &fakeArtifactMetadataPostgresRows{rows: []fakeArtifactMetadataPostgresRow{{err: sentinel}}}}},
		{name: "rows", backend: &fakeConversationPostgresOps{rows: &fakeArtifactMetadataPostgresRows{err: sentinel}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repository := &PostgresStore{operationTimeouts: defaultOperationTimeouts, artifactMetadataPostgres: testCase.backend}
			objects, err := repository.ListArtifactObjects(t.Context(), 10)
			if objects != nil || StoreErrorCodeOf(err) != StoreErrorUnavailable || !errors.Is(err, sentinel) {
				t.Fatalf("objects=%#v code=%q err=%v", objects, StoreErrorCodeOf(err), err)
			}
		})
	}
}

func TestPostgresArtifactMetadataRepositoryConfiguredContract(t *testing.T) {
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
	exerciseArtifactMetadataRepositoryContract(t, repository, nil)
}
