package store

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *FileStore) SaveExternalChatSession(ctx context.Context, session app.ExternalChatSession) (app.ExternalChatSession, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationExternalChatSessionSave, fileAdmissionCapacity)
	if err != nil {
		return app.ExternalChatSession{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationExternalChatSessionSave, func(ctx context.Context) (app.ExternalChatSession, error) {
		return s.inner.SaveExternalChatSession(ctx, session)
	})
}

func (s *FileStore) GetExternalChatSession(ctx context.Context, id string) (app.ExternalChatSession, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationExternalChatSessionGet, 1)
	if err != nil {
		return app.ExternalChatSession{}, false, err
	}
	defer release()
	return s.inner.GetExternalChatSession(ctx, id)
}

func (s *FileStore) ListExternalChatSessions(ctx context.Context, channel, status string) ([]app.ExternalChatSession, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationExternalChatSessionList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListExternalChatSessions(ctx, channel, status)
}

func (s *FileStore) FindExternalChatSession(ctx context.Context, bindingID, externalChatID, externalThreadID string) (app.ExternalChatSession, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationExternalChatSessionFind, 1)
	if err != nil {
		return app.ExternalChatSession{}, false, err
	}
	defer release()
	return s.inner.FindExternalChatSession(ctx, bindingID, externalChatID, externalThreadID)
}

func (s *FileStore) FindExternalChatSessionByLinkedSessionID(ctx context.Context, sessionID string) (app.ExternalChatSession, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationExternalChatSessionFindLink, 1)
	if err != nil {
		return app.ExternalChatSession{}, false, err
	}
	defer release()
	return s.inner.FindExternalChatSessionByLinkedSessionID(ctx, sessionID)
}

func (s *FileStore) SaveExternalChatMessage(ctx context.Context, message app.ExternalChatMessage) (app.ExternalChatMessage, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationExternalChatMessageSave, fileAdmissionCapacity)
	if err != nil {
		return app.ExternalChatMessage{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationExternalChatMessageSave, func(ctx context.Context) (app.ExternalChatMessage, error) {
		return s.inner.SaveExternalChatMessage(ctx, message)
	})
}

func (s *FileStore) GetExternalChatMessage(ctx context.Context, id string) (app.ExternalChatMessage, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationExternalChatMessageGet, 1)
	if err != nil {
		return app.ExternalChatMessage{}, false, err
	}
	defer release()
	return s.inner.GetExternalChatMessage(ctx, id)
}

func (s *FileStore) FindExternalChatMessageByExternalID(ctx context.Context, chatSessionID, externalMessageID string) (app.ExternalChatMessage, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationExternalChatMessageFind, 1)
	if err != nil {
		return app.ExternalChatMessage{}, false, err
	}
	defer release()
	return s.inner.FindExternalChatMessageByExternalID(ctx, chatSessionID, externalMessageID)
}

func (s *FileStore) ListExternalChatMessages(ctx context.Context, chatSessionID string, limit int) ([]app.ExternalChatMessage, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationExternalChatMessageList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListExternalChatMessages(ctx, chatSessionID, limit)
}
