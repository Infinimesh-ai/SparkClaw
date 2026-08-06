package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestMemoryStorePassiveNotificationIdempotencyAndOwnerScope(t *testing.T) {
	st := NewMemoryStore()
	notification := testPassiveNotification("notification-1", "endpoint-1", "delivery-1", "fingerprint-1")

	created, inserted, err := st.CreatePassiveNotification(notification)
	if err != nil || !inserted || created.ID != notification.ID {
		t.Fatalf("create notification = %#v, %v, %v", created, inserted, err)
	}
	duplicate, inserted, err := st.CreatePassiveNotification(notification)
	if err != nil || inserted || duplicate.ID != notification.ID {
		t.Fatalf("duplicate notification = %#v, %v, %v", duplicate, inserted, err)
	}

	conflict := notification
	conflict.Fingerprint = "different"
	if _, _, err := st.CreatePassiveNotification(conflict); !errors.Is(err, ErrPassiveNotificationConflict) {
		t.Fatalf("different payload error = %v", err)
	}
	otherOwner := notification
	otherOwner.OwnerID = "other-owner"
	if _, _, err := st.CreatePassiveNotification(otherOwner); !errors.Is(err, ErrPassiveNotificationConflict) {
		t.Fatalf("different owner error = %v", err)
	}
	if _, ok := st.GetPassiveNotification("other-owner", notification.ID); ok {
		t.Fatal("notification leaked across owners")
	}
	if got := st.CountUnreadPassiveNotifications(notification.OwnerID); got != 1 {
		t.Fatalf("unread count = %d", got)
	}
	read, err := st.MarkPassiveNotificationRead(notification.OwnerID, notification.ID, time.Time{})
	if err != nil || read.ReadAt == nil {
		t.Fatalf("mark read = %#v, %v", read, err)
	}
	if got := st.CountUnreadPassiveNotifications(notification.OwnerID); got != 0 {
		t.Fatalf("unread count after read = %d", got)
	}
}

func TestFileStorePassiveNotificationSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	first := testPassiveNotification("notification-1", "endpoint-1", "delivery-1", "fingerprint-1")
	second := testPassiveNotification("notification-2", "endpoint-1", "delivery-2", "fingerprint-2")
	second.CreatedAt = first.CreatedAt.Add(time.Second)
	if _, inserted, err := st.CreatePassiveNotification(first); err != nil || !inserted {
		t.Fatalf("create first = %v, %v", inserted, err)
	}
	if _, inserted, err := st.CreatePassiveNotification(second); err != nil || !inserted {
		t.Fatalf("create second = %v, %v", inserted, err)
	}
	if count, err := st.MarkAllPassiveNotificationsRead(first.OwnerID, time.Time{}); err != nil || count != 2 {
		t.Fatalf("mark all read = %d, %v", count, err)
	}

	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	items := reloaded.ListPassiveNotifications(first.OwnerID, "", 10)
	if len(items) != 2 || items[0].ID != second.ID || items[0].ReadAt == nil || items[1].ReadAt == nil {
		t.Fatalf("reloaded notifications = %#v", items)
	}
	if _, inserted, err := reloaded.CreatePassiveNotification(first); err != nil || inserted {
		t.Fatalf("restart duplicate = %v, %v", inserted, err)
	}
	if after := reloaded.ListPassiveNotifications(first.OwnerID, first.ID, 10); len(after) != 1 || after[0].ID != second.ID {
		t.Fatalf("notifications after cursor = %#v", after)
	}
}

func testPassiveNotification(id, endpointID, idempotencyKey, fingerprint string) app.PassiveNotification {
	now := time.Now().UTC().Truncate(time.Microsecond)
	return app.PassiveNotification{
		ID: id, OwnerID: app.DefaultOwnerID, EndpointID: endpointID,
		IdempotencyKey: idempotencyKey, Fingerprint: fingerprint,
		NotificationID: "localmind-" + id, Source: "localmind",
		Kind:     app.PassiveNotificationKindDocumentMention,
		DeepLink: "https://localmind.example/workspace/doc", OccurredAt: now, CreatedAt: now,
	}
}
