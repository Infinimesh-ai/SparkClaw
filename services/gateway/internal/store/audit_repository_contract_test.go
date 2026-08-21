package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestAuditRepositoryMemoryAndFileContract(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "state.json")
	fileRepository, err := NewFileStore(filePath)
	if err != nil {
		t.Fatal(err)
	}
	backends := []struct {
		name       string
		repository AuditRepository
		restart    func(*testing.T) AuditRepository
	}{
		{name: "memory", repository: NewMemoryStore()},
		{name: "file", repository: fileRepository, restart: func(t *testing.T) AuditRepository {
			reloaded, err := NewFileStore(filePath)
			if err != nil {
				t.Fatal(err)
			}
			return reloaded
		}},
	}

	for _, backend := range backends {
		t.Run(backend.name, func(t *testing.T) {
			repository := backend.repository
			at := time.Date(2026, 8, 21, 12, 0, 0, 123456789, time.FixedZone("test", 8*60*60))
			fields := map[string]any{"nested": map[string]any{"value": "original"}, "source": app.MessageSourceWeb}
			if err := repository.AddAudit(t.Context(), app.AuditEvent{ID: "audit-b", Time: at, Type: "second", SessionID: "session-a", Fields: fields}); err != nil {
				t.Fatal(err)
			}
			if err := repository.AddAudit(t.Context(), app.AuditEvent{ID: "audit-a", Time: at, Type: "first", SessionID: "session-a"}); err != nil {
				t.Fatal(err)
			}
			if err := repository.AddAudit(t.Context(), app.AuditEvent{ID: "audit-c", Time: at.Add(-time.Second), Type: "other", SessionID: "session-b"}); err != nil {
				t.Fatal(err)
			}

			fields["nested"].(map[string]any)["value"] = "mutated-input"
			audits, err := repository.ListAudit(t.Context(), "session-a")
			if err != nil {
				t.Fatal(err)
			}
			if len(audits) != 2 || audits[0].ID != "audit-a" || audits[1].ID != "audit-b" {
				t.Fatalf("stable audit order = %#v", audits)
			}
			if !audits[1].Time.Equal(postgresTime(at)) || audits[1].Time.Location() != time.UTC || audits[1].Time.Nanosecond()%1000 != 0 {
				t.Fatalf("audit time was not normalized to UTC microseconds: %s", audits[1].Time)
			}
			if audits[1].Fields["source"] != app.MessageSourceWeb || audits[1].Fields["nested"].(map[string]any)["value"] != "original" {
				t.Fatalf("audit fields changed type or exposed input aliases: %#v", audits[1].Fields)
			}
			audits[1].Fields["nested"].(map[string]any)["value"] = "mutated-output"
			again, err := repository.ListAudit(t.Context(), "session-a")
			if err != nil || again[1].Fields["nested"].(map[string]any)["value"] != "original" {
				t.Fatalf("audit output exposed backend aliases: %#v err=%v", again, err)
			}
			empty, err := repository.ListAudit(t.Context(), "missing")
			if err != nil || empty == nil || len(empty) != 0 {
				t.Fatalf("successful absence = %#v err=%v", empty, err)
			}

			if backend.restart != nil {
				repository = backend.restart(t)
				restarted, err := repository.ListAudit(t.Context(), "session-a")
				if err != nil || len(restarted) != 2 || restarted[1].Fields["nested"].(map[string]any)["value"] != "original" {
					t.Fatalf("audit did not survive restart: %#v err=%v", restarted, err)
				}
			}

			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := repository.AddAudit(ctx, app.AuditEvent{Type: "cancelled"}); StoreErrorCodeOf(err) != StoreErrorCanceled {
				t.Fatalf("cancelled add code=%q err=%v", StoreErrorCodeOf(err), err)
			}
			if _, err := repository.ListAudit(ctx, ""); StoreErrorCodeOf(err) != StoreErrorCanceled {
				t.Fatalf("cancelled list code=%q err=%v", StoreErrorCodeOf(err), err)
			}
			if _, err := repository.EventsAfter(ctx, "", ""); StoreErrorCodeOf(err) != StoreErrorCanceled {
				t.Fatalf("cancelled events code=%q err=%v", StoreErrorCodeOf(err), err)
			}
		})
	}
}

func TestAuditRepositoryEventSequenceAndTypedIsolation(t *testing.T) {
	for _, backend := range newS0RepositoryBackends(t) {
		t.Run(backend.name, func(t *testing.T) {
			session := mustCreateSession(t, backend.store, "audit event sequence")
			mustAddMessage(t, backend.store, app.Message{
				ID: "message-audit-event", SessionID: session.ID, Role: "user", Content: "hello",
				Attachments: []app.MessageAttachment{{Name: "original"}},
			})
			events, err := backend.store.EventsAfter(t.Context(), session.ID, "")
			if err != nil || len(events) != 2 || events[0].Type != "session.created" || events[1].Type != "message.created" {
				t.Fatalf("event sequence = %#v err=%v", events, err)
			}
			message, ok := events[1].Payload.(app.Message)
			if !ok {
				t.Fatalf("memory/file event payload lost its concrete type: %T", events[1].Payload)
			}
			message.Attachments[0].Name = "mutated"
			again, err := backend.store.EventsAfter(t.Context(), session.ID, events[0].ID)
			if err != nil || len(again) != 1 || again[0].Payload.(app.Message).Attachments[0].Name != "original" {
				t.Fatalf("event output exposed backend aliases: %#v err=%v", again, err)
			}
			missing, err := backend.store.EventsAfter(t.Context(), session.ID, "missing-cursor")
			if err != nil || missing == nil || len(missing) != 0 {
				t.Fatalf("missing cursor = %#v err=%v", missing, err)
			}
		})
	}
}

func TestFileAuditRepositoryRestoresTypedEventPayload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	repository, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	session := mustCreateSession(t, repository, "typed event restart")
	mustAddMessage(t, repository, app.Message{
		ID: "message-file-restart", SessionID: session.ID, Role: "user", Content: "hello",
		Attachments: []app.MessageAttachment{{Name: "original"}},
	})

	restarted, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	events, err := restarted.EventsAfter(t.Context(), session.ID, "")
	if err != nil || len(events) != 2 {
		t.Fatalf("restarted events = %#v err=%v", events, err)
	}
	message, ok := events[1].Payload.(app.Message)
	if !ok || len(message.Attachments) != 1 || message.Attachments[0].Name != "original" {
		t.Fatalf("restarted event payload = %#v (%T)", events[1].Payload, events[1].Payload)
	}
}

type fakeAuditPostgresOps struct {
	execErr  error
	queryErr error
	rows     onboardingPostgresRows
	row      onboardingPostgresRow
}

func (o *fakeAuditPostgresOps) Acquire(context.Context) (onboardingPostgresSession, error) {
	return nil, errors.New("unexpected audit acquire")
}

func (o *fakeAuditPostgresOps) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, o.execErr
}

func (o *fakeAuditPostgresOps) Query(context.Context, string, ...any) (onboardingPostgresRows, error) {
	return o.rows, o.queryErr
}

func (o *fakeAuditPostgresOps) QueryRow(context.Context, string, ...any) onboardingPostgresRow {
	return o.row
}

type fakeAuditPostgresRow struct {
	audit   app.AuditEvent
	event   app.Event
	fields  []byte
	payload []byte
	seq     int64
	err     error
}

func (r fakeAuditPostgresRow) Scan(destinations ...any) error {
	if r.err != nil {
		return r.err
	}
	switch len(destinations) {
	case 1:
		*(destinations[0].(*int64)) = r.seq
	case 6:
		*(destinations[0].(*string)) = r.event.ID
		*(destinations[1].(*time.Time)) = r.event.Time
		*(destinations[2].(*string)) = r.event.Type
		*(destinations[3].(*string)) = r.event.SessionID
		*(destinations[4].(*string)) = r.event.RunID
		*(destinations[5].(*[]byte)) = append([]byte(nil), r.payload...)
	case 8:
		*(destinations[0].(*string)) = r.audit.ID
		*(destinations[1].(*time.Time)) = r.audit.Time
		*(destinations[2].(*string)) = r.audit.Type
		*(destinations[3].(*string)) = r.audit.SessionID
		*(destinations[4].(*string)) = r.audit.RunID
		*(destinations[5].(*string)) = r.audit.Actor
		*(destinations[6].(*string)) = r.audit.Summary
		*(destinations[7].(*[]byte)) = append([]byte(nil), r.fields...)
	default:
		return errors.New("unexpected audit row shape")
	}
	return nil
}

type fakeAuditPostgresRows struct {
	rows  []fakeAuditPostgresRow
	index int
	err   error
}

func (r *fakeAuditPostgresRows) Next() bool { return r.index < len(r.rows) }
func (r *fakeAuditPostgresRows) Scan(destinations ...any) error {
	row := r.rows[r.index]
	r.index++
	return row.Scan(destinations...)
}
func (r *fakeAuditPostgresRows) Err() error { return r.err }
func (r *fakeAuditPostgresRows) Close()     {}

func TestPostgresAuditRepositoryPropagatesBackendFailures(t *testing.T) {
	for _, testCase := range []struct {
		name string
		ops  *fakeAuditPostgresOps
		call func(*PostgresStore) error
		code StoreErrorCode
	}{
		{name: "exec", ops: &fakeAuditPostgresOps{execErr: errors.New("exec failed")}, call: func(repository *PostgresStore) error {
			return repository.AddAudit(t.Context(), app.AuditEvent{Type: "test"})
		}, code: StoreErrorUnavailable},
		{name: "query", ops: &fakeAuditPostgresOps{queryErr: errors.New("query failed")}, call: func(repository *PostgresStore) error { _, err := repository.ListAudit(t.Context(), ""); return err }, code: StoreErrorUnavailable},
		{name: "scan", ops: &fakeAuditPostgresOps{rows: &fakeAuditPostgresRows{rows: []fakeAuditPostgresRow{{err: errors.New("scan failed")}}}}, call: func(repository *PostgresStore) error { _, err := repository.ListAudit(t.Context(), ""); return err }, code: StoreErrorUnavailable},
		{name: "rows", ops: &fakeAuditPostgresOps{rows: &fakeAuditPostgresRows{err: errors.New("rows failed")}}, call: func(repository *PostgresStore) error { _, err := repository.ListAudit(t.Context(), ""); return err }, code: StoreErrorUnavailable},
		{name: "corrupt fields", ops: &fakeAuditPostgresOps{rows: &fakeAuditPostgresRows{rows: []fakeAuditPostgresRow{{audit: app.AuditEvent{ID: "audit-corrupt"}, fields: []byte("{")}}}}, call: func(repository *PostgresStore) error { _, err := repository.ListAudit(t.Context(), ""); return err }, code: StoreErrorCorrupt},
		{name: "corrupt event payload", ops: &fakeAuditPostgresOps{rows: &fakeAuditPostgresRows{rows: []fakeAuditPostgresRow{{event: app.Event{ID: "event-corrupt"}, payload: []byte("{")}}}}, call: func(repository *PostgresStore) error {
			_, err := repository.EventsAfter(t.Context(), "", "")
			return err
		}, code: StoreErrorCorrupt},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repository := &PostgresStore{operationTimeouts: defaultOperationTimeouts, auditPostgres: testCase.ops}
			if err := testCase.call(repository); StoreErrorCodeOf(err) != testCase.code {
				t.Fatalf("code=%q want=%q err=%v", StoreErrorCodeOf(err), testCase.code, err)
			}
		})
	}
}

func TestPostgresAuditRepositoryMissingCursorIsEmpty(t *testing.T) {
	repository := &PostgresStore{operationTimeouts: defaultOperationTimeouts, auditPostgres: &fakeAuditPostgresOps{row: fakeAuditPostgresRow{err: pgx.ErrNoRows}}}
	events, err := repository.EventsAfter(t.Context(), "session", "missing")
	if err != nil || events == nil || len(events) != 0 {
		t.Fatalf("missing cursor = %#v err=%v", events, err)
	}
}

func TestPostgresAuditRepositoryRestoresTypedEventPayload(t *testing.T) {
	payload, err := json.Marshal(app.Message{
		ID: "message-postgres", SessionID: "session-postgres", Role: "user", Content: "hello",
		Attachments: []app.MessageAttachment{{Name: "original"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	repository := &PostgresStore{operationTimeouts: defaultOperationTimeouts, auditPostgres: &fakeAuditPostgresOps{
		rows: &fakeAuditPostgresRows{rows: []fakeAuditPostgresRow{{
			event: app.Event{ID: "event-postgres", Type: "message.created", SessionID: "session-postgres"}, payload: payload,
		}}},
	}}
	events, err := repository.EventsAfter(t.Context(), "session-postgres", "")
	if err != nil || len(events) != 1 {
		t.Fatalf("postgres events = %#v err=%v", events, err)
	}
	message, ok := events[0].Payload.(app.Message)
	if !ok || len(message.Attachments) != 1 || message.Attachments[0].Name != "original" {
		t.Fatalf("postgres event payload = %#v (%T)", events[0].Payload, events[0].Payload)
	}
}

func TestPostgresAuditRepositoryConfiguredContract(t *testing.T) {
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

	at := time.Date(2026, 8, 21, 12, 0, 0, 123456789, time.UTC)
	if err := repository.AddAudit(t.Context(), app.AuditEvent{ID: "audit-pg-b", Time: at, Type: "second", SessionID: "session-pg", Fields: map[string]any{"value": "ok"}}); err != nil {
		t.Fatal(err)
	}
	if err := repository.AddAudit(t.Context(), app.AuditEvent{ID: "audit-pg-a", Time: at, Type: "first", SessionID: "session-pg"}); err != nil {
		t.Fatal(err)
	}
	audits, err := repository.ListAudit(t.Context(), "session-pg")
	if err != nil || len(audits) != 2 || audits[0].ID != "audit-pg-a" || audits[1].Fields["value"] != "ok" {
		t.Fatalf("configured PostgreSQL audit contract = %#v err=%v", audits, err)
	}
	if events, err := repository.EventsAfter(t.Context(), "session-pg", "missing"); err != nil || events == nil || len(events) != 0 {
		t.Fatalf("configured PostgreSQL missing cursor = %#v err=%v", events, err)
	}
	session := mustCreateSession(t, repository, "configured typed event")
	mustAddMessage(t, repository, app.Message{ID: "message-pg", SessionID: session.ID, Role: "user", Content: "hello"})
	events, err := repository.EventsAfter(t.Context(), session.ID, "")
	if err != nil || len(events) != 2 {
		t.Fatalf("configured PostgreSQL events = %#v err=%v", events, err)
	}
	if _, ok := events[1].Payload.(app.Message); !ok {
		t.Fatalf("configured PostgreSQL payload type = %T, want app.Message", events[1].Payload)
	}
}
