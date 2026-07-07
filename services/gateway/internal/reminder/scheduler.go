package reminder

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/notification"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

const (
	tickBatchLimit = 50
	// sendingLease bounds how long a reminder may sit in "sending" before a
	// later tick assumes the delivering process died and reclaims it.
	sendingLease   = 2 * time.Minute
	retryBaseDelay = time.Minute
	retryMaxDelay  = 30 * time.Minute
)

type Scheduler struct {
	store  store.Store
	router notification.Router
	now    func() time.Time
}

func NewScheduler(st store.Store, router notification.Router) *Scheduler {
	return &Scheduler{
		store:  st,
		router: router,
		now:    func() time.Time { return time.Now().UTC() },
	}
}

func (s *Scheduler) Tick(ctx context.Context) []app.ReminderDelivery {
	now := s.now().UTC()
	due := s.store.ClaimDueReminders(now, now.Add(-sendingLease), tickBatchLimit)
	deliveries := make([]app.ReminderDelivery, 0, len(due))
	for _, reminder := range due {
		delivery := s.deliver(ctx, reminder)
		deliveries = append(deliveries, delivery)
	}
	return deliveries
}

func (s *Scheduler) deliver(ctx context.Context, reminder app.Reminder) app.ReminderDelivery {
	attempt := reminder.DeliveryAttempt + 1
	dedupeKey := reminder.DedupeKey
	if strings.TrimSpace(reminder.Recurrence) != "" && dedupeKey != "" {
		// Scope the key to this occurrence so the provider does not dedupe
		// later occurrences against earlier ones; retries of the same
		// occurrence keep the same key.
		dedupeKey = fmt.Sprintf("%s@%d", dedupeKey, reminder.DueTime.UTC().Unix())
	}
	result, err := s.router.Send(ctx, notification.Notification{
		ReminderID:       reminder.ID,
		Channel:          reminder.Channel,
		Recipient:        reminder.Recipient,
		RecipientBinding: reminder.RecipientBinding,
		CredentialRef:    reminder.CredentialRef,
		BaseURL:          reminder.BaseURL,
		MessageText:      reminder.Text,
		DedupeKey:        dedupeKey,
	})
	status := result.Status
	if status == "" {
		status = "sent"
	}
	errText := result.Error
	if err != nil && errText == "" {
		errText = err.Error()
	}
	if err != nil {
		status = "failed"
	}
	sentAt := result.SentAt
	if sentAt.IsZero() && status == "sent" {
		sentAt = s.now().UTC()
	}
	delivery := app.ReminderDelivery{
		ID:             result.DeliveryID,
		ReminderID:     reminder.ID,
		Channel:        valueOr(result.Channel, reminder.Channel),
		Provider:       result.Provider,
		Recipient:      result.Recipient,
		Status:         status,
		ProviderStatus: result.ProviderStatus,
		Error:          errText,
		RetryState:     result.RetryState,
		Attempt:        attempt,
		SentAt:         sentAt,
		CreatedAt:      s.now().UTC(),
	}
	delivery = s.store.SaveReminderDelivery(delivery)
	s.rearm(reminder.ID, delivery)
	return delivery
}

// rearm returns a reminder to "pending" when it should fire again: recurring
// reminders advance to their next occurrence after a successful send, and
// retryable failures back off instead of failing terminally. Blocked failures
// stay failed.
func (s *Scheduler) rearm(reminderID string, delivery app.ReminderDelivery) {
	reminder, ok := s.store.GetReminder(reminderID)
	if !ok {
		return
	}
	now := s.now().UTC()
	switch {
	case delivery.Status == "sent" && strings.TrimSpace(reminder.Recurrence) != "":
		next, ok := nextOccurrence(reminder, now)
		if !ok {
			return
		}
		reminder.Status = "pending"
		reminder.DueTime = next
		reminder.DeliveryAttempt = 0
		reminder.LastError = ""
	case delivery.Status == "failed" && delivery.RetryState == "retryable":
		reminder.Status = "pending"
		reminder.DueTime = now.Add(retryBackoff(delivery.Attempt))
	default:
		return
	}
	reminder.UpdatedAt = now
	s.store.SaveReminder(reminder)
}

func retryBackoff(attempt int) time.Duration {
	delay := retryBaseDelay
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay >= retryMaxDelay {
			return retryMaxDelay
		}
	}
	return delay
}

func valueOr(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
