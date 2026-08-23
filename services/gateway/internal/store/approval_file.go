package store

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *FileStore) SaveApproval(ctx context.Context, approval app.Approval) (app.Approval, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationApprovalSave, fileAdmissionCapacity)
	if err != nil {
		return app.Approval{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationApprovalSave, func(ctx context.Context) (app.Approval, error) {
		return s.inner.SaveApproval(ctx, approval)
	})
}

func (s *FileStore) GetApproval(ctx context.Context, id string) (app.Approval, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationApprovalGet, 1)
	if err != nil {
		return app.Approval{}, false, err
	}
	defer release()
	return s.inner.GetApproval(ctx, id)
}

func (s *FileStore) FindApprovalByExternalRef(ctx context.Context, source app.ApprovalSource, externalID string) (app.Approval, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationApprovalFindExternalRef, 1)
	if err != nil {
		return app.Approval{}, false, err
	}
	defer release()
	return s.inner.FindApprovalByExternalRef(ctx, source, externalID)
}

func (s *FileStore) UpdatePendingApproval(ctx context.Context, command ApprovalUpdateCommand) (app.Approval, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationApprovalUpdatePending, fileAdmissionCapacity)
	if err != nil {
		return app.Approval{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationApprovalUpdatePending, func(ctx context.Context) (app.Approval, error) {
		return s.inner.UpdatePendingApproval(ctx, command)
	})
}

func (s *FileStore) ResolveApproval(ctx context.Context, id, status, note string) (app.Approval, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationApprovalResolve, fileAdmissionCapacity)
	if err != nil {
		return app.Approval{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationApprovalResolve, func(ctx context.Context) (app.Approval, error) {
		return s.inner.ResolveApproval(ctx, id, status, note)
	})
}

func (s *FileStore) ListApprovals(ctx context.Context, status string) ([]app.Approval, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationApprovalList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListApprovals(ctx, status)
}
