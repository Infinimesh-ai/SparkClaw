package store

import (
	"context"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *FileStore) PublishEmailClassification(ctx context.Context, c EmailClassificationCommand) (app.EmailMail, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationPublishEmailClassification, fileAdmissionCapacity)
	if err != nil {
		return *new(app.EmailMail), err
	}
	defer release()
	if c.CommandKey == "" {
		return *new(app.EmailMail), errEmailCommandInvalid(ctx, OperationPublishEmailClassification)
	}
	return emailFileRun(s, ctx, OperationPublishEmailClassification, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailMail, error) { return emailClassification(e, c) })
}
func (s *FileStore) OverrideEmailClassification(ctx context.Context, c EmailClassificationOverride) (app.EmailMail, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationOverrideEmailClassification, fileAdmissionCapacity)
	if err != nil {
		return *new(app.EmailMail), err
	}
	defer release()
	if c.CommandKey == "" {
		return *new(app.EmailMail), errEmailCommandInvalid(ctx, OperationOverrideEmailClassification)
	}
	return emailFileRun(s, ctx, OperationOverrideEmailClassification, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailMail, error) { return emailOverrideClassification(e, c) })
}
func (s *FileStore) UpdateEmailSenderRule(ctx context.Context, c EmailSenderRuleCommand) (app.EmailSenderRule, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationUpdateEmailSenderRule, fileAdmissionCapacity)
	if err != nil {
		return *new(app.EmailSenderRule), err
	}
	defer release()
	if c.CommandKey == "" {
		return *new(app.EmailSenderRule), errEmailCommandInvalid(ctx, OperationUpdateEmailSenderRule)
	}
	return emailFileRun(s, ctx, OperationUpdateEmailSenderRule, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailSenderRule, error) { return emailUpdateSenderRule(e, c) })
}
func (s *FileStore) ListEmailSenderRules(ctx context.Context, q EmailQuery) ([]app.EmailSenderRule, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationListEmailSenderRules, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListEmailSenderRules(ctx, q)
}
