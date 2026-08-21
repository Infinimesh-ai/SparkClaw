package agent

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func mustSaveAgentDocumentRecord(t testing.TB, repository store.DocumentRepository, record app.DocumentRecord) app.DocumentRecord {
	t.Helper()
	stored, err := repository.SaveDocumentRecord(t.Context(), record)
	if err != nil {
		t.Fatalf("save document record: %v", err)
	}
	return stored
}

func mustListAgentDocumentRecords(t testing.TB, repository store.DocumentRepository, ownerID, sessionID string, limit int) []app.DocumentRecord {
	t.Helper()
	records, err := repository.ListDocumentRecords(t.Context(), ownerID, sessionID, limit)
	if err != nil {
		t.Fatalf("list document records: %v", err)
	}
	return records
}
