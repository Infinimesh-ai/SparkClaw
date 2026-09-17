package store

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *PostgresStore) ScanEmailCaptures(ctx context.Context, c EmailCaptureScan) ([]app.EmailCaptureVersion, error) {
	return emailPostgresRun(s, ctx, OperationScanEmailCaptures, c.OwnerID, "", nil, false, func(e *emailEngine) ([]app.EmailCaptureVersion, error) { return emailScanCaptures(e, c) })
}
func (s *PostgresStore) AdoptEmailCapture(ctx context.Context, c EmailCaptureAdoption) (app.EmailMail, error) {
	if c.CommandKey == "" {
		return *new(app.EmailMail), errEmailCommandInvalid(ctx, OperationAdoptEmailCapture)
	}
	return emailPostgresRun(s, ctx, OperationAdoptEmailCapture, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailMail, error) { return emailAdoptCapture(e, c) })
}
