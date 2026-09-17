package store

import (
	"context"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *PostgresStore) ActivateEmailEventPolicy(ctx context.Context, c EmailCommand) (EmailEventPolicy, error) {
	if c.CommandKey == "" {
		return *new(EmailEventPolicy), errEmailCommandInvalid(ctx, OperationActivateEmailEventPolicy)
	}
	return emailPostgresRun(s, ctx, OperationActivateEmailEventPolicy, c.OwnerID, "", c, true, func(e *emailEngine) (EmailEventPolicy, error) { return emailActivateEvents(e, c) })
}

func (s *PostgresStore) ActivateEmailTimelinePolicy(ctx context.Context, c EmailCommand) (EmailTimelinePolicy, error) {
	if c.CommandKey == "" {
		return *new(EmailTimelinePolicy), errEmailCommandInvalid(ctx, OperationActivateEmailTimelinePolicy)
	}
	return emailPostgresRun(s, ctx, OperationActivateEmailTimelinePolicy, c.OwnerID, "", c, true, func(e *emailEngine) (EmailTimelinePolicy, error) { return emailActivateTimeline(e) })
}
func (s *PostgresStore) ChangeEmailAssignment(ctx context.Context, c EmailManualAssignment) (app.EmailMail, error) {
	if c.CommandKey == "" {
		return *new(app.EmailMail), errEmailCommandInvalid(ctx, OperationChangeEmailAssignment)
	}
	return emailPostgresRun(s, ctx, OperationChangeEmailAssignment, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailMail, error) { return emailManualAssignment(e, c) })
}
func (s *PostgresStore) RenameEmailConversation(ctx context.Context, c EmailConversationRename) (app.EmailConversation, error) {
	if c.CommandKey == "" {
		return *new(app.EmailConversation), errEmailCommandInvalid(ctx, OperationRenameEmailConversation)
	}
	return emailPostgresRun(s, ctx, OperationRenameEmailConversation, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailConversation, error) { return emailRenameConversation(e, c) })
}
