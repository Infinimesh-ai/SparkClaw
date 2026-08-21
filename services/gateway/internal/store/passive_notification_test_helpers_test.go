package store

import (
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func mustGetPassiveNotification(t testing.TB, repository PassiveNotificationRepository, ownerID, id string) (app.PassiveNotification, bool) {
	t.Helper()
	notification, found, err := repository.GetPassiveNotification(t.Context(), ownerID, id)
	if err != nil {
		t.Fatal(err)
	}
	return notification, found
}

func mustListPassiveNotifications(t testing.TB, repository PassiveNotificationRepository, ownerID, after string, limit int) []app.PassiveNotification {
	t.Helper()
	notifications, err := repository.ListPassiveNotifications(t.Context(), ownerID, after, limit)
	if err != nil {
		t.Fatal(err)
	}
	return notifications
}

func mustCountUnreadPassiveNotifications(t testing.TB, repository PassiveNotificationRepository, ownerID string) int {
	t.Helper()
	count, err := repository.CountUnreadPassiveNotifications(t.Context(), ownerID)
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func mustPrunePassiveNotifications(t testing.TB, repository PassiveNotificationRepository, cutoff time.Time, maxPerOwner int) int {
	t.Helper()
	removed, err := repository.PrunePassiveNotifications(t.Context(), cutoff, maxPerOwner)
	if err != nil {
		t.Fatal(err)
	}
	return removed
}

func mustPassiveNotificationRevision(t testing.TB, repository PassiveNotificationRepository, ownerID string) uint64 {
	t.Helper()
	revision, err := repository.PassiveNotificationRevision(t.Context(), ownerID)
	if err != nil {
		t.Fatal(err)
	}
	return revision
}
