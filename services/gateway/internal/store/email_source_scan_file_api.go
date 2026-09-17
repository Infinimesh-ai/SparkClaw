package store

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *FileStore) ScanEmailCaptures(ctx context.Context, c EmailCaptureScan) ([]app.EmailCaptureVersion, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationScanEmailCaptures, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ScanEmailCaptures(ctx, c)
}
func (s *FileStore) AdoptEmailCapture(ctx context.Context, c EmailCaptureAdoption) (app.EmailMail, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationAdoptEmailCapture, fileAdmissionCapacity)
	if err != nil {
		return *new(app.EmailMail), err
	}
	defer release()
	if c.CommandKey == "" {
		return *new(app.EmailMail), errEmailCommandInvalid(ctx, OperationAdoptEmailCapture)
	}
	return emailFileRun(s, ctx, OperationAdoptEmailCapture, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailMail, error) { return emailAdoptCapture(e, c) })
}
