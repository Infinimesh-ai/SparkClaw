package store

import "context"

func (s *MemoryStore) ChangeEmailDraft(ctx context.Context, c EmailDraftCommand) (EmailDraftResult, error) {
	return emailMemoryRun(s, ctx, OperationChangeEmailDraft, c.OwnerID, "", nil, true, func(e *emailEngine) (EmailDraftResult, error) { return changeEmailDraft(e, c) })
}
func (s *MemoryStore) ListEmailDrafts(ctx context.Context, owner, id string) ([]EmailDraft, error) {
	return emailMemoryRun(s, ctx, OperationListEmailDrafts, owner, "", nil, false, func(e *emailEngine) ([]EmailDraft, error) { return listEmailDrafts(e, id) })
}
func (s *PostgresStore) ChangeEmailDraft(ctx context.Context, c EmailDraftCommand) (EmailDraftResult, error) {
	return emailPostgresRun(s, ctx, OperationChangeEmailDraft, c.OwnerID, "", nil, true, func(e *emailEngine) (EmailDraftResult, error) { return changeEmailDraft(e, c) })
}
func (s *PostgresStore) ListEmailDrafts(ctx context.Context, owner, id string) ([]EmailDraft, error) {
	return emailPostgresRun(s, ctx, OperationListEmailDrafts, owner, "", nil, false, func(e *emailEngine) ([]EmailDraft, error) { return listEmailDrafts(e, id) })
}
func (s *FileStore) ChangeEmailDraft(ctx context.Context, c EmailDraftCommand) (EmailDraftResult, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationChangeEmailDraft, fileAdmissionCapacity)
	if err != nil {
		return *new(EmailDraftResult), err
	}
	defer release()
	return emailFileRun(s, ctx, OperationChangeEmailDraft, c.OwnerID, "", nil, true, func(e *emailEngine) (EmailDraftResult, error) { return changeEmailDraft(e, c) })
}
func (s *FileStore) ListEmailDrafts(ctx context.Context, owner, id string) ([]EmailDraft, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationListEmailDrafts, fileAdmissionCapacity)
	if err != nil {
		return *new([]EmailDraft), err
	}
	defer release()
	return emailFileRun(s, ctx, OperationListEmailDrafts, owner, "", nil, false, func(e *emailEngine) ([]EmailDraft, error) { return listEmailDrafts(e, id) })
}

func (s *MemoryStore) ListEmailDraftPage(ctx context.Context, q EmailQuery) (EmailDraftPage, error) {
	return emailMemoryRun(s, ctx, OperationListEmailDraftPage, q.OwnerID, "", nil, false, func(e *emailEngine) (EmailDraftPage, error) { return emailDraftPage(e, q) })
}

func (s *FileStore) ListEmailDraftPage(ctx context.Context, q EmailQuery) (EmailDraftPage, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationListEmailDraftPage, 1)
	if err != nil {
		return EmailDraftPage{}, err
	}
	defer release()
	return emailFileRun(s, ctx, OperationListEmailDraftPage, q.OwnerID, "", nil, false, func(e *emailEngine) (EmailDraftPage, error) { return emailDraftPage(e, q) })
}

func (s *PostgresStore) ListEmailDraftPage(ctx context.Context, q EmailQuery) (EmailDraftPage, error) {
	return emailPostgresRun(s, ctx, OperationListEmailDraftPage, q.OwnerID, "", nil, false, func(e *emailEngine) (EmailDraftPage, error) { return emailDraftPage(e, q) })
}
