package store

import (
	"context"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *FileStore) CreatePassiveNotification(ctx context.Context, notification app.PassiveNotification) (app.PassiveNotification, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationPassiveNotificationCreate, fileAdmissionCapacity)
	if err != nil {
		return app.PassiveNotification{}, false, err
	}
	defer release()
	return runFileOptionalCommand(s, ctx, OperationPassiveNotificationCreate, func(ctx context.Context) (app.PassiveNotification, bool, error) {
		return s.inner.CreatePassiveNotification(ctx, notification)
	})
}

func (s *FileStore) GetPassiveNotification(ctx context.Context, ownerID, id string) (app.PassiveNotification, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationPassiveNotificationGet, 1)
	if err != nil {
		return app.PassiveNotification{}, false, err
	}
	defer release()
	return s.inner.GetPassiveNotification(ctx, ownerID, id)
}

func (s *FileStore) ListPassiveNotifications(ctx context.Context, ownerID, after string, limit int) ([]app.PassiveNotification, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationPassiveNotificationList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListPassiveNotifications(ctx, ownerID, after, limit)
}

func (s *FileStore) CountUnreadPassiveNotifications(ctx context.Context, ownerID string) (int, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationPassiveNotificationCount, 1)
	if err != nil {
		return 0, err
	}
	defer release()
	return s.inner.CountUnreadPassiveNotifications(ctx, ownerID)
}

func (s *FileStore) MarkPassiveNotificationRead(ctx context.Context, ownerID, id string, readAt time.Time) (app.PassiveNotification, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationPassiveNotificationMarkRead, fileAdmissionCapacity)
	if err != nil {
		return app.PassiveNotification{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationPassiveNotificationMarkRead, func(ctx context.Context) (app.PassiveNotification, error) {
		return s.inner.MarkPassiveNotificationRead(ctx, ownerID, id, readAt)
	})
}

func (s *FileStore) MarkAllPassiveNotificationsRead(ctx context.Context, ownerID string, readAt time.Time) (int, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationPassiveNotificationMarkAll, fileAdmissionCapacity)
	if err != nil {
		return 0, err
	}
	defer release()
	count, _, err := runFileOptionalCommand(s, ctx, OperationPassiveNotificationMarkAll, func(ctx context.Context) (int, bool, error) {
		count, err := s.inner.MarkAllPassiveNotificationsRead(ctx, ownerID, readAt)
		return count, count > 0, err
	})
	return count, err
}

func (s *FileStore) PrunePassiveNotifications(ctx context.Context, cutoff time.Time, maxPerOwner int) (int, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationPassiveNotificationPrune, fileAdmissionCapacity)
	if err != nil {
		return 0, err
	}
	defer release()
	removed, _, err := runFileOptionalCommand(s, ctx, OperationPassiveNotificationPrune, func(ctx context.Context) (int, bool, error) {
		removed, err := s.inner.PrunePassiveNotifications(ctx, cutoff, maxPerOwner)
		return removed, removed > 0, err
	})
	return removed, err
}

func (s *FileStore) PassiveNotificationRevision(ctx context.Context, ownerID string) (uint64, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationPassiveNotificationRevision, 1)
	if err != nil {
		return 0, err
	}
	defer release()
	return s.inner.PassiveNotificationRevision(ctx, ownerID)
}
