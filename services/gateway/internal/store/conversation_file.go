package store

import (
	"context"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *FileStore) AddMessage(ctx context.Context, message app.Message) (app.Message, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationConversationAddMessage, fileAdmissionCapacity)
	if err != nil {
		return app.Message{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationConversationAddMessage, func(ctx context.Context) (app.Message, error) {
		return s.inner.AddMessage(ctx, message)
	})
}

func (s *FileStore) ListRecentMessages(ctx context.Context, sessionID string, cutoff time.Time, excludeMessageID string, scanLimit int) ([]app.Message, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationConversationListRecent, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListRecentMessages(ctx, sessionID, cutoff, excludeMessageID, scanLimit)
}

func (s *FileStore) ListMessages(ctx context.Context, sessionID string) ([]app.Message, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationConversationListMessages, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListMessages(ctx, sessionID)
}

func (s *FileStore) MessageEventHead(ctx context.Context, sessionID string) (string, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationConversationMessageHead, 1)
	if err != nil {
		return "", err
	}
	defer release()
	return s.inner.MessageEventHead(ctx, sessionID)
}

func (s *FileStore) MessageEventsAfter(ctx context.Context, sessionID, after string, limit int) (MessageEventPage, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationConversationMessagesAfter, 1)
	if err != nil {
		return MessageEventPage{}, err
	}
	defer release()
	return s.inner.MessageEventsAfter(ctx, sessionID, after, limit)
}
