package store

import "context"

func (s *PostgresStore) PurgeEmailCaptures(ctx context.Context, c EmailCapturePurgeCommand) (EmailCapturePurgeResult, error) {
	if c.CommandKey == "" {
		return *new(EmailCapturePurgeResult), errEmailCommandInvalid(ctx, OperationPurgeEmailCaptures)
	}
	return emailPostgresRun(s, ctx, OperationPurgeEmailCaptures, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (EmailCapturePurgeResult, error) { return emailPurgeCaptures(e, c) })
}
