package store

import (
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const defaultReminderQueryLimit = 50

var errReminderScheduleSpecJSONDecode = errors.New("decode persisted reminder schedule spec")

func prepareReminder(reminder app.Reminder, now time.Time) app.Reminder {
	if strings.TrimSpace(reminder.ID) == "" {
		reminder.ID = app.NewID("rem")
	}
	if reminder.CreatedAt.IsZero() {
		reminder.CreatedAt = now
	}
	if reminder.UpdatedAt.IsZero() {
		reminder.UpdatedAt = now
	}
	if reminder.Status == "" {
		reminder.Status = "pending"
	}
	if reminder.TextSummary == "" {
		reminder.TextSummary = summarizeReminderText(reminder.Text)
	}
	return normalizeReminder(reminder)
}

func prepareReminderUpdate(reminder, current app.Reminder, now time.Time) app.Reminder {
	if reminder.CreatedAt.IsZero() {
		reminder.CreatedAt = current.CreatedAt
	}
	if reminder.UpdatedAt.IsZero() {
		reminder.UpdatedAt = now
	}
	reminder.UpdatedAt = nextRepositoryTime(reminder.UpdatedAt, current.UpdatedAt)
	if reminder.TextSummary == "" {
		reminder.TextSummary = summarizeReminderText(reminder.Text)
	}
	return normalizeReminder(reminder)
}

func normalizeReminder(reminder app.Reminder) app.Reminder {
	reminder.DueTime = postgresTime(reminder.DueTime)
	reminder.CreatedAt = postgresTime(reminder.CreatedAt)
	reminder.UpdatedAt = postgresTime(reminder.UpdatedAt)
	reminder.SentAt = normalizeScheduleTimePointer(reminder.SentAt)
	reminder.CanceledAt = normalizeScheduleTimePointer(reminder.CanceledAt)
	reminder.ScheduleSpec = cloneScheduleSpec(reminder.ScheduleSpec)
	return reminder
}

func normalizeScheduleTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := postgresTime(*value)
	return &normalized
}

func cloneScheduleSpec(spec *app.ScheduleSpec) *app.ScheduleSpec {
	if spec == nil {
		return nil
	}
	cloned := *spec
	cloned.Authorization.Scope = append([]string(nil), spec.Authorization.Scope...)
	cloned.Payload.Content.Parts = make([]app.MessagePart, len(spec.Payload.Content.Parts))
	for index, part := range spec.Payload.Content.Parts {
		cloned.Payload.Content.Parts[index] = part
		if part.Resource == nil {
			continue
		}
		resource := *part.Resource
		if part.Resource.Attributes != nil {
			resource.Attributes = make(map[string]string, len(part.Resource.Attributes))
			for key, value := range part.Resource.Attributes {
				resource.Attributes[key] = value
			}
		}
		cloned.Payload.Content.Parts[index].Resource = &resource
	}
	return &cloned
}

func cloneReminder(reminder app.Reminder) app.Reminder {
	reminder.SentAt = cloneTimePointer(reminder.SentAt)
	reminder.CanceledAt = cloneTimePointer(reminder.CanceledAt)
	reminder.ScheduleSpec = cloneScheduleSpec(reminder.ScheduleSpec)
	return reminder
}

func cloneReminderMap(values map[string]app.Reminder) map[string]app.Reminder {
	out := make(map[string]app.Reminder, len(values))
	for id, reminder := range values {
		out[id] = cloneReminder(reminder)
	}
	return out
}

func sortReminders(reminders []app.Reminder) {
	slices.SortFunc(reminders, func(a, b app.Reminder) int {
		if order := a.DueTime.Compare(b.DueTime); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
}

func prepareReminderDelivery(delivery app.ReminderDelivery, now time.Time) app.ReminderDelivery {
	if strings.TrimSpace(delivery.ID) == "" {
		delivery.ID = app.NewID("rdel")
	}
	if delivery.CreatedAt.IsZero() {
		delivery.CreatedAt = now
	}
	if delivery.Status == "sent" && delivery.SentAt.IsZero() {
		delivery.SentAt = now
	}
	return normalizeReminderDelivery(delivery)
}

func normalizeReminderDelivery(delivery app.ReminderDelivery) app.ReminderDelivery {
	delivery.SentAt = postgresTime(delivery.SentAt)
	delivery.CreatedAt = postgresTime(delivery.CreatedAt)
	return delivery
}

func sortReminderDeliveries(deliveries []app.ReminderDelivery) {
	slices.SortFunc(deliveries, func(a, b app.ReminderDelivery) int {
		if order := a.CreatedAt.Compare(b.CreatedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
}

func normalizeReminderQueryLimit(limit int) int {
	if limit <= 0 {
		return defaultReminderQueryLimit
	}
	return limit
}
