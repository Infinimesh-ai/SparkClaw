package store

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/jackc/pgx/v5"
)

type fakeDocumentPostgresRow struct {
	record app.DocumentRecord
	err    error
}

type fakeDocumentCreatedAtRow struct {
	createdAt time.Time
	err       error
}

func (r fakeDocumentCreatedAtRow) Scan(destinations ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(destinations) != 1 {
		return errors.New("fake document created-at row shape mismatch")
	}
	*(destinations[0].(*time.Time)) = r.createdAt
	return nil
}

func (r fakeDocumentPostgresRow) Scan(destinations ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(destinations) != 20 {
		return errors.New("fake document row shape mismatch")
	}
	*(destinations[0].(*string)) = r.record.ID
	*(destinations[1].(*string)) = r.record.OwnerID
	*(destinations[2].(*string)) = r.record.SessionID
	*(destinations[3].(*string)) = r.record.GovernedPath
	*(destinations[4].(*string)) = r.record.Name
	*(destinations[5].(*string)) = r.record.ContentType
	*(destinations[6].(*string)) = r.record.Format
	*(destinations[7].(*int64)) = r.record.SizeBytes
	*(destinations[8].(*string)) = r.record.SHA256
	*(destinations[9].(*string)) = r.record.Status
	*(destinations[10].(*string)) = r.record.Source
	*(destinations[11].(*string)) = r.record.SourceMessageID
	*(destinations[12].(*string)) = r.record.SourceRunID
	*(destinations[13].(*string)) = r.record.SourceToolCallID
	*(destinations[14].(*string)) = r.record.ParentDocumentID
	*(destinations[15].(*string)) = r.record.LastActivity
	*(destinations[16].(*string)) = r.record.LastActivityID
	*(destinations[17].(*time.Time)) = r.record.LastActivityAt
	*(destinations[18].(*time.Time)) = r.record.CreatedAt
	*(destinations[19].(*time.Time)) = r.record.UpdatedAt
	return nil
}

type fakeDocumentPostgresRows struct {
	rows  []fakeDocumentPostgresRow
	index int
	err   error
}

func (r *fakeDocumentPostgresRows) Next() bool { return r.index < len(r.rows) }
func (r *fakeDocumentPostgresRows) Scan(destinations ...any) error {
	row := r.rows[r.index]
	r.index++
	return row.Scan(destinations...)
}
func (r *fakeDocumentPostgresRows) Err() error { return r.err }
func (r *fakeDocumentPostgresRows) Close()     {}

func TestPostgresDocumentSaveUsesOneAtomicTransaction(t *testing.T) {
	sentinel := errors.New("document lifecycle insert failed")
	for _, testCase := range []struct {
		name      string
		execError error
	}{
		{name: "success"},
		{name: "event failure rolls back", execError: sentinel},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			createdAt := normalizeDocumentTime(time.Now())
			transaction := &fakeConversationPostgresTx{
				rowQueue:   []onboardingPostgresRow{fakeDocumentCreatedAtRow{createdAt: createdAt}},
				execErrors: map[int]error{},
			}
			if testCase.execError != nil {
				transaction.execErrors[1] = testCase.execError
			}
			session := &fakeConversationPostgresSession{transaction: transaction}
			backend := &fakeConversationPostgresOps{session: session}
			repository := &PostgresStore{operationTimeouts: defaultOperationTimeouts, documentPostgres: backend}

			stored, err := repository.SaveDocumentRecord(t.Context(), app.DocumentRecord{
				ID: "document-postgres", SessionID: "session-postgres", LastActivityAt: time.Now(),
			})
			if testCase.execError == nil {
				if err != nil || stored.ID == "" || transaction.commits != 1 || transaction.rollbacks != 0 {
					t.Fatalf("stored=%#v err=%v commits=%d rollbacks=%d", stored, err, transaction.commits, transaction.rollbacks)
				}
			} else if stored.ID != "" || StoreErrorCodeOf(err) != StoreErrorUnavailable || !errors.Is(err, sentinel) || transaction.commits != 0 || transaction.rollbacks != 1 {
				t.Fatalf("stored=%#v err=%v commits=%d rollbacks=%d", stored, err, transaction.commits, transaction.rollbacks)
			}
			if len(transaction.rowSQL) != 1 || !strings.Contains(transaction.rowSQL[0], "INSERT INTO document_records") || !strings.Contains(transaction.rowSQL[0], "RETURNING created_at") ||
				len(transaction.execSQL) != 2 || !strings.Contains(transaction.execSQL[0], "INSERT INTO audit_events") || !strings.Contains(transaction.execSQL[1], "INSERT INTO events") ||
				session.releases != 1 || session.terminates != 0 {
				t.Fatalf("rows=%#v exec=%#v releases=%d terminates=%d", transaction.rowSQL, transaction.execSQL, session.releases, session.terminates)
			}
		})
	}
}

func TestPostgresDocumentReadsPropagateErrors(t *testing.T) {
	sentinel := errors.New("document read failed")
	legacyTime := time.Date(2026, 8, 20, 9, 15, 0, 987654321, time.FixedZone("postgres", 8*60*60))
	successBackend := &fakeConversationPostgresOps{rowQueue: []onboardingPostgresRow{fakeDocumentPostgresRow{record: app.DocumentRecord{
		ID: "document", LastActivityAt: legacyTime, CreatedAt: legacyTime, UpdatedAt: legacyTime,
	}}}}
	successStore := &PostgresStore{operationTimeouts: defaultOperationTimeouts, documentPostgres: successBackend}
	if record, found, err := successStore.GetDocumentRecord(t.Context(), "document"); err != nil || !found ||
		record.CreatedAt.Location() != time.UTC || record.CreatedAt.Nanosecond() != 987654000 {
		t.Fatalf("normalized record=%#v found=%t err=%v", record, found, err)
	}

	missingBackend := &fakeConversationPostgresOps{rowQueue: []onboardingPostgresRow{fakeDocumentPostgresRow{err: pgx.ErrNoRows}}}
	missingStore := &PostgresStore{operationTimeouts: defaultOperationTimeouts, documentPostgres: missingBackend}
	if record, found, err := missingStore.GetDocumentRecord(t.Context(), "missing"); err != nil || found || record.ID != "" {
		t.Fatalf("missing record=%#v found=%t err=%v", record, found, err)
	}

	getBackend := &fakeConversationPostgresOps{rowQueue: []onboardingPostgresRow{fakeDocumentPostgresRow{err: sentinel}}}
	getStore := &PostgresStore{operationTimeouts: defaultOperationTimeouts, documentPostgres: getBackend}
	if _, _, err := getStore.GetDocumentRecord(t.Context(), "document"); StoreErrorCodeOf(err) != StoreErrorUnavailable || !errors.Is(err, sentinel) {
		t.Fatalf("get error = %v", err)
	}

	for _, testCase := range []struct {
		name    string
		backend *fakeConversationPostgresOps
	}{
		{name: "query", backend: &fakeConversationPostgresOps{queryErr: sentinel}},
		{name: "scan", backend: &fakeConversationPostgresOps{rows: &fakeDocumentPostgresRows{rows: []fakeDocumentPostgresRow{{err: sentinel}}}}},
		{name: "rows", backend: &fakeConversationPostgresOps{rows: &fakeDocumentPostgresRows{err: sentinel}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repository := &PostgresStore{operationTimeouts: defaultOperationTimeouts, documentPostgres: testCase.backend}
			records, err := repository.ListDocumentRecords(t.Context(), "owner", "session", 10)
			if records != nil || StoreErrorCodeOf(err) != StoreErrorUnavailable || !errors.Is(err, sentinel) {
				t.Fatalf("records=%#v err=%v", records, err)
			}
		})
	}
}
