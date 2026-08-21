package toolhub

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func mustToolhubGetReminder(t testing.TB, repository store.ScheduleRepository, id string) (app.Reminder, bool) {
	t.Helper()
	reminder, found, err := repository.GetReminder(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return reminder, found
}

func mustToolhubListReminders(t testing.TB, repository store.ScheduleRepository, filter app.ReminderFilter) []app.Reminder {
	t.Helper()
	reminders, err := repository.ListReminders(t.Context(), filter)
	if err != nil {
		t.Fatal(err)
	}
	return reminders
}

func mustToolhubSaveReminder(t testing.TB, repository store.ScheduleRepository, reminder app.Reminder) app.Reminder {
	t.Helper()
	stored, err := repository.SaveReminder(t.Context(), reminder)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}
