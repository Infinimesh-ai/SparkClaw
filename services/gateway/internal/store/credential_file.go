package store

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *FileStore) SaveCredentialSecret(ctx context.Context, command CredentialSaveCommand) (app.CredentialSecret, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationCredentialSecretSave, fileAdmissionCapacity)
	if err != nil {
		return app.CredentialSecret{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationCredentialSecretSave, func(ctx context.Context) (app.CredentialSecret, error) {
		return s.inner.SaveCredentialSecret(ctx, command)
	})
}

func (s *FileStore) GetCredentialSecret(ctx context.Context, ref string) (app.CredentialSecret, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationCredentialSecretGet, 1)
	if err != nil {
		return app.CredentialSecret{}, false, err
	}
	defer release()
	return s.inner.GetCredentialSecret(ctx, ref)
}

func (s *FileStore) DeleteCredentialSecret(ctx context.Context, condition CredentialDeleteCondition) (app.CredentialSecret, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationCredentialSecretDelete, fileAdmissionCapacity)
	if err != nil {
		return app.CredentialSecret{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationCredentialSecretDelete, func(ctx context.Context) (app.CredentialSecret, error) {
		return s.inner.DeleteCredentialSecret(ctx, condition)
	})
}
