package store

import (
	"context"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *MemoryStore) ActivateEmailEventPolicy(ctx context.Context, c EmailCommand) (EmailEventPolicy, error) {
	if c.CommandKey == "" {
		return *new(EmailEventPolicy), errEmailCommandInvalid(ctx, OperationActivateEmailEventPolicy)
	}
	return emailMemoryRun(s, ctx, OperationActivateEmailEventPolicy, c.OwnerID, "", c, true, func(e *emailEngine) (EmailEventPolicy, error) { return emailActivateEvents(e, c) })
}

func (s *MemoryStore) ActivateEmailTimelinePolicy(ctx context.Context, c EmailCommand) (EmailTimelinePolicy, error) {
	if c.CommandKey == "" {
		return *new(EmailTimelinePolicy), errEmailCommandInvalid(ctx, OperationActivateEmailTimelinePolicy)
	}
	return emailMemoryRun(s, ctx, OperationActivateEmailTimelinePolicy, c.OwnerID, "", c, true, func(e *emailEngine) (EmailTimelinePolicy, error) { return emailActivateTimeline(e) })
}
func (s *MemoryStore) ChangeEmailAssignment(ctx context.Context, c EmailManualAssignment) (app.EmailMail, error) {
	if c.CommandKey == "" {
		return *new(app.EmailMail), errEmailCommandInvalid(ctx, OperationChangeEmailAssignment)
	}
	return emailMemoryRun(s, ctx, OperationChangeEmailAssignment, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailMail, error) { return emailManualAssignment(e, c) })
}
func (s *MemoryStore) RenameEmailConversation(ctx context.Context, c EmailConversationRename) (app.EmailConversation, error) {
	if c.CommandKey == "" {
		return *new(app.EmailConversation), errEmailCommandInvalid(ctx, OperationRenameEmailConversation)
	}
	return emailMemoryRun(s, ctx, OperationRenameEmailConversation, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailConversation, error) { return emailRenameConversation(e, c) })
}
