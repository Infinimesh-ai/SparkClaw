package store

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *MemoryStore) SaveDocumentRecord(ctx context.Context, record app.DocumentRecord) (app.DocumentRecord, error) {
	ctx, cancel := operationContext(ctx, OperationDocumentRecordSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationDocumentRecordSave, ctx); err != nil {
		return app.DocumentRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationDocumentRecordSave, ctx); err != nil {
		return app.DocumentRecord{}, err
	}
	var existing *app.DocumentRecord
	if current, ok := s.documentRecords[record.ID]; ok {
		existing = &current
	}
	record = prepareDocumentRecord(record, existing, time.Now())
	s.documentRecords[record.ID] = record
	s.appendAuditLocked("document.saved", record.SessionID, record.SourceRunID, "document_registry", record.LastActivity, map[string]any{
		"document_id": record.ID,
		"path":        record.GovernedPath,
		"activity_id": record.LastActivityID,
	})
	s.appendEventLocked("document.saved", record.SessionID, record.SourceRunID, record)
	return record, nil
}

func (s *MemoryStore) GetDocumentRecord(ctx context.Context, id string) (app.DocumentRecord, bool, error) {
	ctx, cancel := operationContext(ctx, OperationDocumentRecordGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationDocumentRecordGet, ctx); err != nil {
		return app.DocumentRecord{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationDocumentRecordGet, ctx); err != nil {
		return app.DocumentRecord{}, false, err
	}
	record, ok := s.documentRecords[id]
	return record, ok, nil
}

func (s *MemoryStore) ListDocumentRecords(ctx context.Context, ownerID, sessionID string, limit int) ([]app.DocumentRecord, error) {
	ctx, cancel := operationContext(ctx, OperationDocumentRecordList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationDocumentRecordList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationDocumentRecordList, ctx); err != nil {
		return nil, err
	}
	out := make([]app.DocumentRecord, 0)
	for _, record := range s.documentRecords {
		if (ownerID == "" || record.OwnerID == ownerID) && (sessionID == "" || record.SessionID == sessionID) {
			out = append(out, record)
		}
	}
	slices.SortFunc(out, func(a, b app.DocumentRecord) int {
		if order := b.LastActivityAt.Compare(a.LastActivityAt); order != 0 {
			return order
		}
		if order := b.UpdatedAt.Compare(a.UpdatedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	limit = normalizeDocumentRecordLimit(limit)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
