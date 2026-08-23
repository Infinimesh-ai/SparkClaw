package store

import (
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func mustSaveReminder(t testing.TB, repository ScheduleRepository, reminder app.Reminder) app.Reminder {
	t.Helper()
	stored, err := repository.SaveReminder(t.Context(), reminder)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func testUpdatePendingReminder(t testing.TB, repository ScheduleRepository, reminder app.Reminder, expected time.Time) (app.Reminder, error) {
	t.Helper()
	return repository.UpdatePendingReminder(t.Context(), reminder, expected)
}

func mustGetReminder(t testing.TB, repository ScheduleRepository, id string) (app.Reminder, bool) {
	t.Helper()
	reminder, found, err := repository.GetReminder(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return reminder, found
}

func mustListReminders(t testing.TB, repository ScheduleRepository, filter app.ReminderFilter) []app.Reminder {
	t.Helper()
	reminders, err := repository.ListReminders(t.Context(), filter)
	if err != nil {
		t.Fatal(err)
	}
	return reminders
}

func mustClaimDueReminders(t testing.TB, repository ScheduleRepository, now, staleBefore time.Time, limit int) []app.Reminder {
	t.Helper()
	reminders, err := repository.ClaimDueReminders(t.Context(), now, staleBefore, limit)
	if err != nil {
		t.Fatal(err)
	}
	return reminders
}

func mustSaveReminderDelivery(t testing.TB, repository ScheduleRepository, delivery app.ReminderDelivery) app.ReminderDelivery {
	t.Helper()
	stored, err := repository.SaveReminderDelivery(t.Context(), delivery)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func mustListReminderDeliveries(t testing.TB, repository ScheduleRepository, reminderID string) []app.ReminderDelivery {
	t.Helper()
	deliveries, err := repository.ListReminderDeliveries(t.Context(), reminderID)
	if err != nil {
		t.Fatal(err)
	}
	return deliveries
}
