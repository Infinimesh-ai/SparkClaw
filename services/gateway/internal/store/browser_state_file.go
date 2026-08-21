package store

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *FileStore) SaveBrowserAuthRecord(ctx context.Context, record app.BrowserAuthRecord) (app.BrowserAuthRecord, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationBrowserAuthSave, fileAdmissionCapacity)
	if err != nil {
		return app.BrowserAuthRecord{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationBrowserAuthSave, func(ctx context.Context) (app.BrowserAuthRecord, error) {
		return s.inner.SaveBrowserAuthRecord(ctx, record)
	})
}

func (s *FileStore) GetBrowserAuthRecord(ctx context.Context, id string) (app.BrowserAuthRecord, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationBrowserAuthGet, 1)
	if err != nil {
		return app.BrowserAuthRecord{}, false, err
	}
	defer release()
	return s.inner.GetBrowserAuthRecord(ctx, id)
}

func (s *FileStore) FindBrowserAuthRecord(ctx context.Context, ownerID, browserProfileID, siteOrigin, siteRealm, accountHint string) (app.BrowserAuthRecord, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationBrowserAuthFind, 1)
	if err != nil {
		return app.BrowserAuthRecord{}, false, err
	}
	defer release()
	return s.inner.FindBrowserAuthRecord(ctx, ownerID, browserProfileID, siteOrigin, siteRealm, accountHint)
}

func (s *FileStore) ListBrowserAuthRecords(ctx context.Context, ownerID, browserProfileID string) ([]app.BrowserAuthRecord, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationBrowserAuthList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListBrowserAuthRecords(ctx, ownerID, browserProfileID)
}

func (s *FileStore) RevokeBrowserAuthRecord(ctx context.Context, id, reason string) (app.BrowserAuthRecord, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationBrowserAuthRevoke, fileAdmissionCapacity)
	if err != nil {
		return app.BrowserAuthRecord{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationBrowserAuthRevoke, func(ctx context.Context) (app.BrowserAuthRecord, error) {
		return s.inner.RevokeBrowserAuthRecord(ctx, id, reason)
	})
}

func (s *FileStore) SaveBrowserLoginBlock(ctx context.Context, block app.BrowserLoginBlock) (app.BrowserLoginBlock, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationBrowserLoginBlockSave, fileAdmissionCapacity)
	if err != nil {
		return app.BrowserLoginBlock{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationBrowserLoginBlockSave, func(ctx context.Context) (app.BrowserLoginBlock, error) {
		return s.inner.SaveBrowserLoginBlock(ctx, block)
	})
}

func (s *FileStore) UpdateBrowserLoginBlock(ctx context.Context, block app.BrowserLoginBlock, expectedVersion int64) (app.BrowserLoginBlock, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationBrowserLoginBlockUpdate, fileAdmissionCapacity)
	if err != nil {
		return app.BrowserLoginBlock{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationBrowserLoginBlockUpdate, func(ctx context.Context) (app.BrowserLoginBlock, error) {
		return s.inner.UpdateBrowserLoginBlock(ctx, block, expectedVersion)
	})
}

func (s *FileStore) GetBrowserLoginBlock(ctx context.Context, id string) (app.BrowserLoginBlock, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationBrowserLoginBlockGet, 1)
	if err != nil {
		return app.BrowserLoginBlock{}, false, err
	}
	defer release()
	return s.inner.GetBrowserLoginBlock(ctx, id)
}

func (s *FileStore) FindActiveBrowserLoginBlock(ctx context.Context, sessionID string) (app.BrowserLoginBlock, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationBrowserLoginBlockFindActive, 1)
	if err != nil {
		return app.BrowserLoginBlock{}, false, err
	}
	defer release()
	return s.inner.FindActiveBrowserLoginBlock(ctx, sessionID)
}

func (s *FileStore) ListBrowserLoginBlocks(ctx context.Context, sessionID, status string) ([]app.BrowserLoginBlock, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationBrowserLoginBlockList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListBrowserLoginBlocks(ctx, sessionID, status)
}
