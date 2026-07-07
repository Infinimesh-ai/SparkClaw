package reminder

import (
	"context"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/notification"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
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
	due := s.store.ListReminders(app.ReminderFilter{
		Status: "pending",
		To:     &now,
		Limit:  50,
	})
	deliveries := make([]app.ReminderDelivery, 0, len(due))
	for _, reminder := range due {
		delivery := s.deliver(ctx, reminder)
		deliveries = append(deliveries, delivery)
	}
	return deliveries
}

func (s *Scheduler) deliver(ctx context.Context, reminder app.Reminder) app.ReminderDelivery {
	attempt := reminder.DeliveryAttempt + 1
	result, err := s.router.Send(ctx, notification.Notification{
		ReminderID:       reminder.ID,
		Channel:          reminder.Channel,
		Recipient:        reminder.Recipient,
		RecipientBinding: reminder.RecipientBinding,
		CredentialRef:    reminder.CredentialRef,
		BaseURL:          reminder.BaseURL,
		MessageText:      reminder.Text,
		DedupeKey:        reminder.DedupeKey,
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
	return s.store.SaveReminderDelivery(delivery)
}

func valueOr(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
