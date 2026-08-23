package store

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *FileStore) AddAudit(ctx context.Context, event app.AuditEvent) error {
	ctx, release, err := s.admitMigrated(ctx, OperationAuditAdd, fileAdmissionCapacity)
	if err != nil {
		return err
	}
	defer release()
	_, err = runFileCommand(s, ctx, OperationAuditAdd, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, s.inner.AddAudit(ctx, event)
	})
	return err
}

func (s *FileStore) ListAudit(ctx context.Context, sessionID string) ([]app.AuditEvent, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationAuditList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListAudit(ctx, sessionID)
}

func (s *FileStore) EventsAfter(ctx context.Context, sessionID, after string) ([]app.Event, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationAuditEventsAfter, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.EventsAfter(ctx, sessionID, after)
}
