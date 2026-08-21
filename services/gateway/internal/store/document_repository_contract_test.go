package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestDocumentRepositoryMemoryAndFileContract(t *testing.T) {
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
			exerciseDocumentRepositoryContract(t, repository, restart)
		})
	}
}

func TestPostgresDocumentRepositoryContract(t *testing.T) {
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
	exerciseDocumentRepositoryContract(t, repository, nil)
}

func exerciseDocumentRepositoryContract(t *testing.T, repository testBackend, restart func() testBackend) {
	t.Helper()
	session := mustCreateSession(t, repository, "document contract")
	base := time.Date(2026, 8, 21, 14, 30, 0, 123456789, time.FixedZone("contract", 8*60*60))
	first := mustSaveDocumentRecord(t, repository, app.DocumentRecord{
		ID: "document-contract-a", OwnerID: session.OwnerID, SessionID: session.ID,
		GovernedPath: "reports/a.docx", Name: "a.docx", Source: app.DocumentSourceAttachment,
		LastActivity: app.DocumentActivityAttached, LastActivityID: "message-a", LastActivityAt: base,
		CreatedAt: base,
	})
	if first.OwnerID != session.OwnerID || first.Status != app.DocumentStatusAvailable || first.CreatedAt.Location() != time.UTC ||
		first.CreatedAt.Nanosecond() != 123456000 || first.LastActivityAt.Nanosecond() != 123456000 {
		t.Fatalf("normalized first record = %#v", first)
	}

	updatedCandidate := first
	updatedCandidate.Name = "a-updated.docx"
	updatedCandidate.CreatedAt = base.Add(time.Hour)
	updatedCandidate.LastActivity = app.DocumentActivityEdited
	updatedCandidate.LastActivityID = "tool-a"
	updatedCandidate.LastActivityAt = base.Add(time.Minute)
	updated := mustSaveDocumentRecord(t, repository, updatedCandidate)
	if updated.CreatedAt != first.CreatedAt || updated.Name != "a-updated.docx" {
		t.Fatalf("overwrite did not preserve creation time: first=%#v updated=%#v", first, updated)
	}
	second := mustSaveDocumentRecord(t, repository, app.DocumentRecord{
		ID: "document-contract-b", OwnerID: session.OwnerID, SessionID: session.ID,
		GovernedPath: "reports/b.pdf", Name: "b.pdf", Source: app.DocumentSourceWorkspace,
		LastActivity: app.DocumentActivityRead, LastActivityID: "tool-b", LastActivityAt: base.Add(2 * time.Minute),
	})
	mustSaveDocumentRecord(t, repository, app.DocumentRecord{
		ID: "document-contract-other", OwnerID: "other-owner", SessionID: session.ID,
		LastActivityAt: base.Add(3 * time.Minute),
	})

	records := mustListDocumentRecords(t, repository, session.OwnerID, session.ID, 10)
	if len(records) != 2 || records[0].ID != second.ID || records[1].ID != updated.ID {
		t.Fatalf("document order/scope = %#v", records)
	}
	if missing, found := mustGetDocumentRecord(t, repository, "missing-document"); found || missing.ID != "" {
		t.Fatalf("missing document = %#v found=%t", missing, found)
	}
	if empty := mustListDocumentRecords(t, repository, session.OwnerID, "missing-session", 10); empty == nil || len(empty) != 0 {
		t.Fatalf("empty document list = %#v", empty)
	}

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repository.SaveDocumentRecord(canceled, app.DocumentRecord{ID: "document-canceled"}); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("canceled save error = %v", err)
	}
	if _, _, err := repository.GetDocumentRecord(canceled, first.ID); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("canceled get error = %v", err)
	}
	if _, err := repository.ListDocumentRecords(canceled, session.OwnerID, session.ID, 10); StoreErrorCodeOf(err) != StoreErrorCanceled {
		t.Fatalf("canceled list error = %v", err)
	}
	if _, found := mustGetDocumentRecord(t, repository, "document-canceled"); found {
		t.Fatal("canceled document save mutated state")
	}

	if restart != nil {
		reloaded := restart()
		persisted, found := mustGetDocumentRecord(t, reloaded, updated.ID)
		if !found || persisted.Name != updated.Name || persisted.CreatedAt != updated.CreatedAt {
			t.Fatalf("restarted document = %#v found=%t", persisted, found)
		}
	}
}

func TestFileDocumentRepositoryDefiniteFailureRestoresAggregate(t *testing.T) {
	repository, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	session := mustCreateSession(t, repository, "document rollback")
	before := repository.captureFileRollback()
	repository.commitOps = &controlledFileCommitOps{failStage: "encode", failRemaining: 1}

	stored, err := repository.SaveDocumentRecord(t.Context(), app.DocumentRecord{
		ID: "document-rollback", OwnerID: session.OwnerID, SessionID: session.ID,
		LastActivity: app.DocumentActivityConfirmed, LastActivityAt: time.Now(),
	})
	if stored.ID != "" || StoreErrorCodeOf(err) != StoreErrorDurability || !errorsIsFileCommitInjected(err) {
		t.Fatalf("stored=%#v err=%v code=%q", stored, err, StoreErrorCodeOf(err))
	}
	if after := repository.captureFileRollback(); !reflect.DeepEqual(after, before) {
		t.Fatal("failed document save retained record, audit, or event state")
	}
}

func TestFileDocumentRepositoryNormalizesLegacySnapshotTimes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-state.json")
	legacyTime := time.Date(2026, 8, 20, 9, 15, 0, 987654321, time.FixedZone("legacy", 8*60*60))
	raw, err := json.Marshal(Snapshot{DocumentRecords: map[string]app.DocumentRecord{
		"document-legacy-time": {
			ID: "document-legacy-time", LastActivityAt: legacyTime, CreatedAt: legacyTime, UpdatedAt: legacyTime,
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	repository, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	record, found := mustGetDocumentRecord(t, repository, "document-legacy-time")
	if !found || record.CreatedAt.Location() != time.UTC || record.CreatedAt.Nanosecond() != 987654000 ||
		record.LastActivityAt != record.CreatedAt || record.UpdatedAt != record.CreatedAt {
		t.Fatalf("normalized legacy document = %#v found=%t", record, found)
	}
}
