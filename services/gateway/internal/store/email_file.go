package store

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *FileStore) GetEmailProviderSetting(ctx context.Context, ownerID, provider string) (app.EmailProviderSetting, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationEmailProviderSettingGet, 1)
	if err != nil {
		return app.EmailProviderSetting{}, false, err
	}
	defer release()
	return s.inner.GetEmailProviderSetting(ctx, ownerID, provider)
}

func (s *FileStore) ListEmailProviderSettings(ctx context.Context, ownerID string) ([]app.EmailProviderSetting, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationEmailProviderSettingList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListEmailProviderSettings(ctx, ownerID)
}

func (s *FileStore) UpdateEmailProviderSetting(ctx context.Context, setting app.EmailProviderSetting, expectedVersion int64) (app.EmailProviderSetting, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationEmailProviderSettingUpdate, fileAdmissionCapacity)
	if err != nil {
		return app.EmailProviderSetting{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationEmailProviderSettingUpdate, func(ctx context.Context) (app.EmailProviderSetting, error) {
		return s.inner.UpdateEmailProviderSetting(ctx, setting, expectedVersion)
	})
}
