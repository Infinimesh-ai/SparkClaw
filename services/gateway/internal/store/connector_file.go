package store

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *FileStore) GetConnectorSetting(ctx context.Context, ownerID, channel string) (app.ConnectorSetting, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationConnectorSettingGet, 1)
	if err != nil {
		return app.ConnectorSetting{}, false, err
	}
	defer release()
	return s.inner.GetConnectorSetting(ctx, ownerID, channel)
}

func (s *FileStore) ListConnectorSettings(ctx context.Context, ownerID string) ([]app.ConnectorSetting, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationConnectorSettingList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListConnectorSettings(ctx, ownerID)
}

func (s *FileStore) ListAllConnectorSettings(ctx context.Context) ([]app.ConnectorSetting, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationConnectorSettingListAll, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListAllConnectorSettings(ctx)
}

func (s *FileStore) UpdateConnectorSetting(ctx context.Context, setting app.ConnectorSetting, expectedVersion int64) (app.ConnectorSetting, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationConnectorSettingUpdate, fileAdmissionCapacity)
	if err != nil {
		return app.ConnectorSetting{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationConnectorSettingUpdate, func(ctx context.Context) (app.ConnectorSetting, error) {
		return s.inner.UpdateConnectorSetting(ctx, setting, expectedVersion)
	})
}

func (s *FileStore) CreateNotificationBinding(ctx context.Context, binding app.NotificationBinding) (app.NotificationBinding, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationNotificationBindingCreate, fileAdmissionCapacity)
	if err != nil {
		return app.NotificationBinding{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationNotificationBindingCreate, func(ctx context.Context) (app.NotificationBinding, error) {
		return s.inner.CreateNotificationBinding(ctx, binding)
	})
}

func (s *FileStore) GetNotificationBinding(ctx context.Context, id string) (app.NotificationBinding, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationNotificationBindingGet, 1)
	if err != nil {
		return app.NotificationBinding{}, false, err
	}
	defer release()
	return s.inner.GetNotificationBinding(ctx, id)
}

func (s *FileStore) ListNotificationBindings(ctx context.Context, channel, status string) ([]app.NotificationBinding, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationNotificationBindingList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListNotificationBindings(ctx, channel, status)
}

func (s *FileStore) UpdateNotificationBinding(ctx context.Context, command NotificationBindingUpdateCommand) (app.NotificationBinding, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationNotificationBindingUpdate, fileAdmissionCapacity)
	if err != nil {
		return app.NotificationBinding{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationNotificationBindingUpdate, func(ctx context.Context) (app.NotificationBinding, error) {
		return s.inner.UpdateNotificationBinding(ctx, command)
	})
}
