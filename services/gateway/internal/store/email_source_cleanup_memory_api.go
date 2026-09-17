package store

import "context"

func (s *MemoryStore) PurgeEmailCaptures(ctx context.Context, c EmailCapturePurgeCommand) (EmailCapturePurgeResult, error) {
	if c.CommandKey == "" {
		return *new(EmailCapturePurgeResult), errEmailCommandInvalid(ctx, OperationPurgeEmailCaptures)
	}
	return emailMemoryRun(s, ctx, OperationPurgeEmailCaptures, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (EmailCapturePurgeResult, error) { return emailPurgeCaptures(e, c) })
}
