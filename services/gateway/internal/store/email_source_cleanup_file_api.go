package store

import "context"

func (s *FileStore) PurgeEmailCaptures(ctx context.Context, c EmailCapturePurgeCommand) (EmailCapturePurgeResult, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationPurgeEmailCaptures, fileAdmissionCapacity)
	if err != nil {
		return *new(EmailCapturePurgeResult), err
	}
	defer release()
	if c.CommandKey == "" {
		return *new(EmailCapturePurgeResult), errEmailCommandInvalid(ctx, OperationPurgeEmailCaptures)
	}
	return emailFileRun(s, ctx, OperationPurgeEmailCaptures, c.OwnerID, c.CommandKey, c, true, func(e *emailEngine) (EmailCapturePurgeResult, error) { return emailPurgeCaptures(e, c) })
}
