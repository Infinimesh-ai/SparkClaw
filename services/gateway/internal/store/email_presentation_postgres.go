package store

import (
	"context"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *PostgresStore) ReadEmailPresentations(ctx context.Context, q EmailPresentationQuery) ([]app.EmailLocalizedPresentation, error) {
	return emailPostgresRun(s, ctx, OperationReadEmailPresentations, q.OwnerID, "", q, false, func(e *emailEngine) ([]app.EmailLocalizedPresentation, error) { return emailPresentationRead(e, q) })
}
func (s *PostgresStore) EnsureEmailPresentations(ctx context.Context, c EmailPresentationCommand) ([]app.EmailLocalizedPresentation, error) {
	if c.CommandKey == "" {
		return *new([]app.EmailLocalizedPresentation), errEmailCommandInvalid(ctx, OperationEnsureEmailPresentations)
	}
	return emailPostgresRun(s, ctx, OperationEnsureEmailPresentations, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) ([]app.EmailLocalizedPresentation, error) { return emailPresentationEnsure(e, c) })
}
func (s *PostgresStore) PublishEmailPresentation(ctx context.Context, c EmailPresentationPublish) (app.EmailLocalizedPresentation, error) {
	if c.CommandKey == "" {
		return *new(app.EmailLocalizedPresentation), errEmailCommandInvalid(ctx, OperationPublishEmailPresentation)
	}
	return emailPostgresRun(s, ctx, OperationPublishEmailPresentation, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailLocalizedPresentation, error) { return emailPresentationPublish(e, c) })
}
func (s *PostgresStore) GetEmailPresentation(ctx context.Context, owner, id string) (app.EmailLocalizedPresentation, bool, error) {
	pair, err := emailPostgresRun(s, ctx, OperationGetEmailPresentation, owner, "", nil, false, func(e *emailEngine) (emailOptional[app.EmailLocalizedPresentation], error) {
		return emailReadRecord[app.EmailLocalizedPresentation](e, "presentation", id)
	})
	return pair.Value, pair.Found, err
}

var _ EmailPresentationRepository = (*PostgresStore)(nil)
