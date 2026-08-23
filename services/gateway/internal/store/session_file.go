package store

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *FileStore) CreateSession(ctx context.Context, title string) (app.Session, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationSessionCreate, fileAdmissionCapacity)
	if err != nil {
		return app.Session{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationSessionCreate, func(ctx context.Context) (app.Session, error) {
		return s.inner.CreateSession(ctx, title)
	})
}

func (s *FileStore) CreateSessionWithScope(ctx context.Context, title, ownerID, workspaceRoot, source string, hidden bool) (app.Session, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationSessionCreateWithScope, fileAdmissionCapacity)
	if err != nil {
		return app.Session{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationSessionCreateWithScope, func(ctx context.Context) (app.Session, error) {
		return s.inner.CreateSessionWithScope(ctx, title, ownerID, workspaceRoot, source, hidden)
	})
}

func (s *FileStore) ListSessions(ctx context.Context) ([]app.Session, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationSessionList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListSessions(ctx)
}

func (s *FileStore) GetSession(ctx context.Context, id string) (app.Session, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationSessionGet, 1)
	if err != nil {
		return app.Session{}, false, err
	}
	defer release()
	return s.inner.GetSession(ctx, id)
}

func (s *FileStore) UpdateSessionTitle(ctx context.Context, id, title string) (app.Session, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationSessionUpdateTitle, fileAdmissionCapacity)
	if err != nil {
		return app.Session{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationSessionUpdateTitle, func(ctx context.Context) (app.Session, error) {
		return s.inner.UpdateSessionTitle(ctx, id, title)
	})
}

func (s *FileStore) DeleteSession(ctx context.Context, id string) (app.Session, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationSessionDelete, fileAdmissionCapacity)
	if err != nil {
		return app.Session{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationSessionDelete, func(ctx context.Context) (app.Session, error) {
		return s.inner.DeleteSession(ctx, id)
	})
}
