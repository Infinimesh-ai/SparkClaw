package store

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *FileStore) SaveArtifactObject(ctx context.Context, object app.ArtifactObject) (app.ArtifactObject, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationArtifactMetadataSave, fileAdmissionCapacity)
	if err != nil {
		return app.ArtifactObject{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationArtifactMetadataSave, func(ctx context.Context) (app.ArtifactObject, error) {
		return s.inner.SaveArtifactObject(ctx, object)
	})
}

func (s *FileStore) ListArtifactObjects(ctx context.Context, limit int) ([]app.ArtifactObject, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationArtifactMetadataList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListArtifactObjects(ctx, limit)
}

func (s *FileStore) FindArtifactObjectByURI(ctx context.Context, uri, sessionID, runID string) (app.ArtifactObject, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationArtifactMetadataFindByURI, 1)
	if err != nil {
		return app.ArtifactObject{}, false, err
	}
	defer release()
	return s.inner.FindArtifactObjectByURI(ctx, uri, sessionID, runID)
}
