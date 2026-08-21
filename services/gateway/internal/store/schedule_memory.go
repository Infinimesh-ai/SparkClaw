package store

import (
	"context"
	"errors"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *MemoryStore) SaveReminder(ctx context.Context, reminder app.Reminder) (app.Reminder, error) {
	ctx, cancel := operationContext(ctx, OperationReminderSave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationReminderSave, ctx); err != nil {
		return app.Reminder{}, err
	}
	reminder = prepareReminder(reminder, time.Now().UTC())
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationReminderSave, ctx); err != nil {
		return app.Reminder{}, err
	}
	s.reminders[reminder.ID] = cloneReminder(reminder)
	s.appendAuditLocked("reminder."+reminder.Status, reminder.SessionID, reminder.RunID, "toolhub", reminder.TextSummary, map[string]any{
		"reminder_id": reminder.ID,
		"due_time":    reminder.DueTime.UTC().Format(time.RFC3339),
		"channel":     reminder.Channel,
	})
	s.appendEventLocked("reminder."+reminder.Status, reminder.SessionID, reminder.RunID, reminder)
	return cloneReminder(reminder), nil
}

func (s *MemoryStore) UpdatePendingReminder(ctx context.Context, reminder app.Reminder, expectedUpdatedAt time.Time) (app.Reminder, error) {
	ctx, cancel := operationContext(ctx, OperationReminderUpdatePending, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationReminderUpdatePending, ctx); err != nil {
		return app.Reminder{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationReminderUpdatePending, ctx); err != nil {
		return app.Reminder{}, err
	}
	current, ok := s.reminders[reminder.ID]
	if !ok || current.Status != "pending" || !current.UpdatedAt.Equal(postgresTime(expectedUpdatedAt)) {
		return app.Reminder{}, storeError(ctx, OperationReminderUpdatePending, StoreErrorConflict, ErrReminderConflict)
	}
	reminder = prepareReminderUpdate(reminder, current, time.Now().UTC())
	s.reminders[reminder.ID] = cloneReminder(reminder)
	s.appendAuditLocked("reminder."+reminder.Status, reminder.SessionID, reminder.RunID, "toolhub", reminder.TextSummary, map[string]any{
		"reminder_id": reminder.ID,
		"due_time":    reminder.DueTime.UTC().Format(time.RFC3339),
		"channel":     reminder.Channel,
	})
	s.appendEventLocked("reminder."+reminder.Status, reminder.SessionID, reminder.RunID, reminder)
	return cloneReminder(reminder), nil
}

func (s *MemoryStore) GetReminder(ctx context.Context, id string) (app.Reminder, bool, error) {
	ctx, cancel := operationContext(ctx, OperationReminderGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationReminderGet, ctx); err != nil {
		return app.Reminder{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationReminderGet, ctx); err != nil {
		return app.Reminder{}, false, err
	}
	reminder, ok := s.reminders[id]
	return cloneReminder(reminder), ok, nil
}

func (s *MemoryStore) ListReminders(ctx context.Context, filter app.ReminderFilter) ([]app.Reminder, error) {
	ctx, cancel := operationContext(ctx, OperationReminderList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationReminderList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationReminderList, ctx); err != nil {
		return nil, err
	}
	out := []app.Reminder{}
	for _, reminder := range s.reminders {
		if filter.Status != "" && reminder.Status != filter.Status {
			continue
		}
		if filter.From != nil && reminder.DueTime.Before(filter.From.UTC()) {
			continue
		}
		if filter.To != nil && reminder.DueTime.After(filter.To.UTC()) {
			continue
		}
		out = append(out, cloneReminder(reminder))
	}
	sortReminders(out)
	limit := normalizeReminderQueryLimit(filter.Limit)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ClaimDueReminders atomically flips due pending reminders to "sending" and
// returns them, so overlapping ticks cannot deliver the same reminder twice.
// Reminders left in "sending" since before staleBefore (a crashed or hung
// delivery) are reclaimed.
func (s *MemoryStore) ClaimDueReminders(ctx context.Context, now, staleBefore time.Time, limit int) ([]app.Reminder, error) {
	ctx, cancel := operationContext(ctx, OperationReminderClaimDue, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationReminderClaimDue, ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationReminderClaimDue, ctx); err != nil {
		return nil, err
	}
	now = postgresTime(now)
	staleBefore = postgresTime(staleBefore)
	claimed := []app.Reminder{}
	for _, reminder := range s.reminders {
		switch reminder.Status {
		case "pending":
			if reminder.DueTime.After(now) {
				continue
			}
		case "sending":
			if reminder.UpdatedAt.After(staleBefore) {
				continue
			}
		default:
			continue
		}
		claimed = append(claimed, cloneReminder(reminder))
	}
	sortReminders(claimed)
	limit = normalizeReminderQueryLimit(limit)
	if len(claimed) > limit {
		claimed = claimed[:limit]
	}
	for i, reminder := range claimed {
		reminder.Status = "sending"
		reminder.UpdatedAt = now
		s.reminders[reminder.ID] = cloneReminder(reminder)
		claimed[i] = cloneReminder(reminder)
	}
	return claimed, nil
}

func (s *MemoryStore) SaveReminderDelivery(ctx context.Context, delivery app.ReminderDelivery) (app.ReminderDelivery, error) {
	ctx, cancel := operationContext(ctx, OperationReminderDeliverySave, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationReminderDeliverySave, ctx); err != nil {
		return app.ReminderDelivery{}, err
	}
	now := postgresTime(time.Now().UTC())
	delivery = prepareReminderDelivery(delivery, now)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationReminderDeliverySave, ctx); err != nil {
		return app.ReminderDelivery{}, err
	}
	reminder, ok := s.reminders[delivery.ReminderID]
	if !ok {
		return app.ReminderDelivery{}, storeError(ctx, OperationReminderDeliverySave, StoreErrorNotFound, errors.New("reminder not found"))
	}
	s.reminderDelivery[delivery.ID] = delivery
	reminder.LastDeliveryID = delivery.ID
	reminder.LastError = delivery.Error
	reminder.DeliveryAttempt = delivery.Attempt
	if delivery.Status == "sent" {
		reminder.SentAt = cloneTimePointer(&delivery.SentAt)
		reminder.Status = "sent"
	} else if delivery.Status == "failed" {
		reminder.Status = "failed"
	}
	reminder.UpdatedAt = nextRepositoryTime(now, reminder.UpdatedAt)
	s.reminders[reminder.ID] = cloneReminder(reminder)
	s.appendAuditLocked("reminder_delivery."+delivery.Status, "", "", "scheduler", delivery.ProviderStatus, map[string]any{
		"delivery_id": delivery.ID,
		"reminder_id": delivery.ReminderID,
		"channel":     delivery.Channel,
		"provider":    delivery.Provider,
		"attempt":     delivery.Attempt,
	})
	s.appendEventLocked("reminder_delivery."+delivery.Status, "", delivery.ReminderID, delivery)
	return delivery, nil
}

func (s *MemoryStore) ListReminderDeliveries(ctx context.Context, reminderID string) ([]app.ReminderDelivery, error) {
	ctx, cancel := operationContext(ctx, OperationReminderDeliveryList, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationReminderDeliveryList, ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationReminderDeliveryList, ctx); err != nil {
		return nil, err
	}
	out := []app.ReminderDelivery{}
	for _, delivery := range s.reminderDelivery {
		if reminderID == "" || delivery.ReminderID == reminderID {
			out = append(out, delivery)
		}
	}
	sortReminderDeliveries(out)
	return out, nil
}
