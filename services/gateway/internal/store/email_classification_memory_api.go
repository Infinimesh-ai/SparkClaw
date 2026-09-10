package store

import (
	"context"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *MemoryStore) PublishEmailClassification(ctx context.Context, c EmailClassificationCommand) (app.EmailMail, error) {
	if c.CommandKey == "" {
		return *new(app.EmailMail), errEmailCommandInvalid(ctx, OperationPublishEmailClassification)
	}
	return emailMemoryRun(s, ctx, OperationPublishEmailClassification, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailMail, error) { return emailClassification(e, c) })
}
func (s *MemoryStore) OverrideEmailClassification(ctx context.Context, c EmailClassificationOverride) (app.EmailMail, error) {
	if c.CommandKey == "" {
		return *new(app.EmailMail), errEmailCommandInvalid(ctx, OperationOverrideEmailClassification)
	}
	return emailMemoryRun(s, ctx, OperationOverrideEmailClassification, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailMail, error) { return emailOverrideClassification(e, c) })
}
func (s *MemoryStore) UpdateEmailSenderRule(ctx context.Context, c EmailSenderRuleCommand) (app.EmailSenderRule, error) {
	if c.CommandKey == "" {
		return *new(app.EmailSenderRule), errEmailCommandInvalid(ctx, OperationUpdateEmailSenderRule)
	}
	return emailMemoryRun(s, ctx, OperationUpdateEmailSenderRule, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailSenderRule, error) { return emailUpdateSenderRule(e, c) })
}
func (s *MemoryStore) ListEmailSenderRules(ctx context.Context, q EmailQuery) ([]app.EmailSenderRule, error) {
	return emailMemoryRun(s, ctx, OperationListEmailSenderRules, q.OwnerID, "", nil, false, func(e *emailEngine) ([]app.EmailSenderRule, error) {
		return emailList[app.EmailSenderRule](e, emailRowsQuery{Kind: "sender_rule", Parent: q.Search, After: q.After, Limit: emailLimit(q.Limit)}), e.err
	})
}
