package store

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *FileStore) GetOwnerProfile(ctx context.Context) (app.OwnerProfile, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationOwnerProfileGet, 1)
	if err != nil {
		return app.OwnerProfile{}, err
	}
	defer release()
	return s.inner.GetOwnerProfile(ctx)
}

func (s *FileStore) UpdateOwnerProfile(ctx context.Context, profile app.OwnerProfile) (app.OwnerProfile, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationOwnerProfileUpdate, fileAdmissionCapacity)
	if err != nil {
		return app.OwnerProfile{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationOwnerProfileUpdate, func(ctx context.Context) (app.OwnerProfile, error) {
		return s.inner.UpdateOwnerProfile(ctx, profile)
	})
}

func (s *FileStore) GetOwnerProfileByID(ctx context.Context, id string) (app.OwnerProfile, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationOwnerProfileGetByID, 1)
	if err != nil {
		return app.OwnerProfile{}, false, err
	}
	defer release()
	return s.inner.GetOwnerProfileByID(ctx, id)
}

func (s *FileStore) SaveOwnerProfile(ctx context.Context, profile app.OwnerProfile) (app.OwnerProfile, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationOwnerProfileSave, fileAdmissionCapacity)
	if err != nil {
		return app.OwnerProfile{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationOwnerProfileSave, func(ctx context.Context) (app.OwnerProfile, error) {
		return s.inner.SaveOwnerProfile(ctx, profile)
	})
}

func (s *FileStore) ListOwnerProfiles(ctx context.Context) ([]app.OwnerProfile, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationOwnerProfileList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListOwnerProfiles(ctx)
}

func (s *FileStore) FindOwnerProfileByExternalRef(ctx context.Context, source, externalRef string) (app.OwnerProfile, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationOwnerProfileFindExternalRef, 1)
	if err != nil {
		return app.OwnerProfile{}, false, err
	}
	defer release()
	return s.inner.FindOwnerProfileByExternalRef(ctx, source, externalRef)
}
