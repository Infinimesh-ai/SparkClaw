package store

import (
	"context"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *FileStore) ReadEmailPresentations(ctx context.Context, q EmailPresentationQuery) ([]app.EmailLocalizedPresentation, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationReadEmailPresentations, 1)
	if err != nil {
		return *new([]app.EmailLocalizedPresentation), err
	}
	defer release()
	return s.inner.ReadEmailPresentations(ctx, q)
}
func (s *FileStore) EnsureEmailPresentations(ctx context.Context, c EmailPresentationCommand) ([]app.EmailLocalizedPresentation, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationEnsureEmailPresentations, fileAdmissionCapacity)
	if err != nil {
		return *new([]app.EmailLocalizedPresentation), err
	}
	defer release()
	if c.CommandKey == "" {
		return *new([]app.EmailLocalizedPresentation), errEmailCommandInvalid(ctx, OperationEnsureEmailPresentations)
	}
	return emailFileRun(s, ctx, OperationEnsureEmailPresentations, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) ([]app.EmailLocalizedPresentation, error) { return emailPresentationEnsure(e, c) })
}
func (s *FileStore) PublishEmailPresentation(ctx context.Context, c EmailPresentationPublish) (app.EmailLocalizedPresentation, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationPublishEmailPresentation, fileAdmissionCapacity)
	if err != nil {
		return *new(app.EmailLocalizedPresentation), err
	}
	defer release()
	if c.CommandKey == "" {
		return *new(app.EmailLocalizedPresentation), errEmailCommandInvalid(ctx, OperationPublishEmailPresentation)
	}
	return emailFileRun(s, ctx, OperationPublishEmailPresentation, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailLocalizedPresentation, error) { return emailPresentationPublish(e, c) })
}
func (s *FileStore) GetEmailPresentation(ctx context.Context, owner, id string) (app.EmailLocalizedPresentation, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationGetEmailPresentation, 1)
	if err != nil {
		return app.EmailLocalizedPresentation{}, false, err
	}
	defer release()
	return s.inner.GetEmailPresentation(ctx, owner, id)
}

var _ EmailPresentationRepository = (*FileStore)(nil)
