package store

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func mustSaveDocumentRecord(t testing.TB, repository DocumentRepository, record app.DocumentRecord) app.DocumentRecord {
	t.Helper()
	stored, err := repository.SaveDocumentRecord(t.Context(), record)
	if err != nil {
		t.Fatalf("save document record: %v", err)
	}
	return stored
}

func mustGetDocumentRecord(t testing.TB, repository DocumentRepository, id string) (app.DocumentRecord, bool) {
	t.Helper()
	record, found, err := repository.GetDocumentRecord(t.Context(), id)
	if err != nil {
		t.Fatalf("get document record: %v", err)
	}
	return record, found
}

func mustListDocumentRecords(t testing.TB, repository DocumentRepository, ownerID, sessionID string, limit int) []app.DocumentRecord {
	t.Helper()
	records, err := repository.ListDocumentRecords(t.Context(), ownerID, sessionID, limit)
	if err != nil {
		t.Fatalf("list document records: %v", err)
	}
	return records
}
