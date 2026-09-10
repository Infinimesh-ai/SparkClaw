package store

import (
	"context"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *FileStore) ActivateEmailEventPolicy(ctx context.Context, c EmailCommand) (EmailEventPolicy, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationActivateEmailEventPolicy, fileAdmissionCapacity)
	if err != nil {
		return *new(EmailEventPolicy), err
	}
	defer release()
	if c.CommandKey == "" {
		return *new(EmailEventPolicy), errEmailCommandInvalid(ctx, OperationActivateEmailEventPolicy)
	}
	return emailFileRun(s, ctx, OperationActivateEmailEventPolicy, c.OwnerID, "", c, true, func(e *emailEngine) (EmailEventPolicy, error) { return emailActivateEvents(e, c) })
}
func (s *FileStore) ChangeEmailAssignment(ctx context.Context, c EmailManualAssignment) (app.EmailMail, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationChangeEmailAssignment, fileAdmissionCapacity)
	if err != nil {
		return *new(app.EmailMail), err
	}
	defer release()
	if c.CommandKey == "" {
		return *new(app.EmailMail), errEmailCommandInvalid(ctx, OperationChangeEmailAssignment)
	}
	return emailFileRun(s, ctx, OperationChangeEmailAssignment, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailMail, error) { return emailManualAssignment(e, c) })
}
func (s *FileStore) RenameEmailConversation(ctx context.Context, c EmailConversationRename) (app.EmailConversation, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationRenameEmailConversation, fileAdmissionCapacity)
	if err != nil {
		return *new(app.EmailConversation), err
	}
	defer release()
	if c.CommandKey == "" {
		return *new(app.EmailConversation), errEmailCommandInvalid(ctx, OperationRenameEmailConversation)
	}
	return emailFileRun(s, ctx, OperationRenameEmailConversation, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (app.EmailConversation, error) { return emailRenameConversation(e, c) })
}
