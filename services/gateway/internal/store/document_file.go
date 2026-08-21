package store

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *FileStore) SaveDocumentRecord(ctx context.Context, record app.DocumentRecord) (app.DocumentRecord, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationDocumentRecordSave, fileAdmissionCapacity)
	if err != nil {
		return app.DocumentRecord{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationDocumentRecordSave, func(ctx context.Context) (app.DocumentRecord, error) {
		return s.inner.SaveDocumentRecord(ctx, record)
	})
}

func (s *FileStore) GetDocumentRecord(ctx context.Context, id string) (app.DocumentRecord, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationDocumentRecordGet, 1)
	if err != nil {
		return app.DocumentRecord{}, false, err
	}
	defer release()
	return s.inner.GetDocumentRecord(ctx, id)
}

func (s *FileStore) ListDocumentRecords(ctx context.Context, ownerID, sessionID string, limit int) ([]app.DocumentRecord, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationDocumentRecordList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListDocumentRecords(ctx, ownerID, sessionID, limit)
}
