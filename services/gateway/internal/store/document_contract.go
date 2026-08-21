package store

import (
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const defaultDocumentRecordLimit = 100

func prepareDocumentRecord(record app.DocumentRecord, existing *app.DocumentRecord, now time.Time) app.DocumentRecord {
	now = normalizeDocumentTime(now)
	if record.ID == "" {
		record.ID = app.NewID("doc")
	}
	if record.OwnerID == "" {
		record.OwnerID = app.DefaultOwnerID
	}
	if record.Status == "" {
		record.Status = app.DocumentStatusAvailable
	}
	if existing != nil && !existing.CreatedAt.IsZero() {
		record.CreatedAt = existing.CreatedAt
	} else if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	} else {
		record.CreatedAt = normalizeDocumentTime(record.CreatedAt)
	}
	if record.LastActivityAt.IsZero() {
		record.LastActivityAt = now
	} else {
		record.LastActivityAt = normalizeDocumentTime(record.LastActivityAt)
	}
	if record.LastActivityID == "" {
		record.LastActivityID = record.ID
	}
	record.UpdatedAt = now
	return record
}

func normalizeDocumentTime(value time.Time) time.Time {
	if value.IsZero() {
		return value
	}
	return value.UTC().Truncate(time.Microsecond)
}

func normalizePersistedDocumentRecord(record app.DocumentRecord) app.DocumentRecord {
	record.LastActivityAt = normalizeDocumentTime(record.LastActivityAt)
	record.CreatedAt = normalizeDocumentTime(record.CreatedAt)
	record.UpdatedAt = normalizeDocumentTime(record.UpdatedAt)
	return record
}

func normalizeDocumentRecordLimit(limit int) int {
	if limit <= 0 {
		return defaultDocumentRecordLimit
	}
	return limit
}
