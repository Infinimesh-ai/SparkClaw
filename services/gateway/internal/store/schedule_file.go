package store

import (
	"context"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *FileStore) SaveReminder(ctx context.Context, reminder app.Reminder) (app.Reminder, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationReminderSave, fileAdmissionCapacity)
	if err != nil {
		return app.Reminder{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationReminderSave, func(ctx context.Context) (app.Reminder, error) {
		return s.inner.SaveReminder(ctx, reminder)
	})
}

func (s *FileStore) UpdatePendingReminder(ctx context.Context, reminder app.Reminder, expectedUpdatedAt time.Time) (app.Reminder, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationReminderUpdatePending, fileAdmissionCapacity)
	if err != nil {
		return app.Reminder{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationReminderUpdatePending, func(ctx context.Context) (app.Reminder, error) {
		return s.inner.UpdatePendingReminder(ctx, reminder, expectedUpdatedAt)
	})
}

func (s *FileStore) GetReminder(ctx context.Context, id string) (app.Reminder, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationReminderGet, 1)
	if err != nil {
		return app.Reminder{}, false, err
	}
	defer release()
	return s.inner.GetReminder(ctx, id)
}

func (s *FileStore) ListReminders(ctx context.Context, filter app.ReminderFilter) ([]app.Reminder, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationReminderList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListReminders(ctx, filter)
}

func (s *FileStore) ClaimDueReminders(ctx context.Context, now, staleBefore time.Time, limit int) ([]app.Reminder, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationReminderClaimDue, fileAdmissionCapacity)
	if err != nil {
		return nil, err
	}
	defer release()
	out, _, err := runFileOptionalCommand(s, ctx, OperationReminderClaimDue, func(ctx context.Context) ([]app.Reminder, bool, error) {
		claimed, err := s.inner.ClaimDueReminders(ctx, now, staleBefore, limit)
		return claimed, len(claimed) > 0, err
	})
	return out, err
}

func (s *FileStore) SaveReminderDelivery(ctx context.Context, delivery app.ReminderDelivery) (app.ReminderDelivery, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationReminderDeliverySave, fileAdmissionCapacity)
	if err != nil {
		return app.ReminderDelivery{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationReminderDeliverySave, func(ctx context.Context) (app.ReminderDelivery, error) {
		return s.inner.SaveReminderDelivery(ctx, delivery)
	})
}

func (s *FileStore) ListReminderDeliveries(ctx context.Context, reminderID string) ([]app.ReminderDelivery, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationReminderDeliveryList, 1)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.inner.ListReminderDeliveries(ctx, reminderID)
}
