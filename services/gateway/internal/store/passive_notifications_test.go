package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestMemoryStorePassiveNotificationIdempotencyAndOwnerScope(t *testing.T) {
	st := NewMemoryStore()
	notification := testPassiveNotification("notification-1", "endpoint-1", "delivery-1", "fingerprint-1")

	created, inserted, err := st.CreatePassiveNotification(t.Context(), notification)
	if err != nil || !inserted || created.ID != notification.ID {
		t.Fatalf("create notification = %#v, %v, %v", created, inserted, err)
	}
	duplicate, inserted, err := st.CreatePassiveNotification(t.Context(), notification)
	if err != nil || inserted || duplicate.ID != notification.ID {
		t.Fatalf("duplicate notification = %#v, %v, %v", duplicate, inserted, err)
	}

	conflict := notification
	conflict.Fingerprint = "different"
	if _, _, err := st.CreatePassiveNotification(t.Context(), conflict); !errors.Is(err, ErrPassiveNotificationConflict) {
		t.Fatalf("different payload error = %v", err)
	}
	otherOwner := notification
	otherOwner.OwnerID = "other-owner"
	if _, _, err := st.CreatePassiveNotification(t.Context(), otherOwner); !errors.Is(err, ErrPassiveNotificationConflict) {
		t.Fatalf("different owner error = %v", err)
	}
	if _, ok := mustGetPassiveNotification(t, st, "other-owner", notification.ID); ok {
		t.Fatal("notification leaked across owners")
	}
	if got := mustCountUnreadPassiveNotifications(t, st, notification.OwnerID); got != 1 {
		t.Fatalf("unread count = %d", got)
	}
	read, err := st.MarkPassiveNotificationRead(t.Context(), notification.OwnerID, notification.ID, time.Time{})
	if err != nil || read.ReadAt == nil {
		t.Fatalf("mark read = %#v, %v", read, err)
	}
	if got := mustCountUnreadPassiveNotifications(t, st, notification.OwnerID); got != 0 {
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
	if _, inserted, err := st.CreatePassiveNotification(t.Context(), first); err != nil || !inserted {
		t.Fatalf("create first = %v, %v", inserted, err)
	}
	if _, inserted, err := st.CreatePassiveNotification(t.Context(), second); err != nil || !inserted {
		t.Fatalf("create second = %v, %v", inserted, err)
	}
	if count, err := st.MarkAllPassiveNotificationsRead(t.Context(), first.OwnerID, time.Time{}); err != nil || count != 2 {
		t.Fatalf("mark all read = %d, %v", count, err)
	}

	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	items := mustListPassiveNotifications(t, reloaded, first.OwnerID, "", 10)
	if len(items) != 2 || items[0].ID != second.ID || items[0].ReadAt == nil || items[1].ReadAt == nil {
		t.Fatalf("reloaded notifications = %#v", items)
	}
	if _, inserted, err := reloaded.CreatePassiveNotification(t.Context(), first); err != nil || inserted {
		t.Fatalf("restart duplicate = %v, %v", inserted, err)
	}
	if after := mustListPassiveNotifications(t, reloaded, first.OwnerID, first.ID, 10); len(after) != 1 || after[0].ID != second.ID {
		t.Fatalf("notifications after cursor = %#v", after)
	}
}

func TestMemoryStorePassiveNotificationIdempotentReingestionAtScale(t *testing.T) {
	st := NewMemoryStore()
	const total = 1500
	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < total; i++ {
		notification := testPassiveNotification(
			fmt.Sprintf("notification-%04d", i), "endpoint-1",
			fmt.Sprintf("delivery-%04d", i), fmt.Sprintf("fingerprint-%04d", i),
		)
		notification.CreatedAt = base.Add(time.Duration(i) * time.Millisecond)
		if _, inserted, err := st.CreatePassiveNotification(t.Context(), notification); err != nil || !inserted {
			t.Fatalf("create %d = %v, %v", i, inserted, err)
		}
	}
	for i := 0; i < total; i++ {
		notification := testPassiveNotification(
			fmt.Sprintf("notification-%04d", i), "endpoint-1",
			fmt.Sprintf("delivery-%04d", i), fmt.Sprintf("fingerprint-%04d", i),
		)
		existing, inserted, err := st.CreatePassiveNotification(t.Context(), notification)
		if err != nil || inserted || existing.ID != notification.ID {
			t.Fatalf("replay %d = %#v, %v, %v", i, existing, inserted, err)
		}
	}
	tampered := testPassiveNotification("notification-0000", "endpoint-1", "delivery-0000", "fingerprint-tampered")
	if _, _, err := st.CreatePassiveNotification(t.Context(), tampered); !errors.Is(err, ErrPassiveNotificationConflict) {
		t.Fatalf("tampered replay error = %v", err)
	}
	if got := mustCountUnreadPassiveNotifications(t, st, app.DefaultOwnerID); got != total {
		t.Fatalf("unread count after replays = %d, want %d", got, total)
	}
}

func TestPrunePassiveNotificationsRetentionSweep(t *testing.T) {
	st := NewMemoryStore()
	now := time.Now().UTC()
	old := testPassiveNotification("notification-old", "endpoint-1", "delivery-old", "fingerprint-old")
	old.CreatedAt = now.AddDate(0, 0, -10)
	fresh := testPassiveNotification("notification-fresh", "endpoint-1", "delivery-fresh", "fingerprint-fresh")
	fresh.CreatedAt = now
	for _, notification := range []app.PassiveNotification{old, fresh} {
		if _, inserted, err := st.CreatePassiveNotification(t.Context(), notification); err != nil || !inserted {
			t.Fatalf("create %s = %v, %v", notification.ID, inserted, err)
		}
	}

	if removed := mustPrunePassiveNotifications(t, st, time.Time{}, 0); removed != 0 {
		t.Fatalf("disabled prune removed %d", removed)
	}
	if removed := mustPrunePassiveNotifications(t, st, now.AddDate(0, 0, -7), 0); removed != 1 {
		t.Fatalf("retention sweep removed %d", removed)
	}
	items := mustListPassiveNotifications(t, st, app.DefaultOwnerID, "", 10)
	if len(items) != 1 || items[0].ID != fresh.ID {
		t.Fatalf("surviving notifications = %#v", items)
	}
	// A pruned idempotency key must become replayable again.
	if _, inserted, err := st.CreatePassiveNotification(t.Context(), old); err != nil || !inserted {
		t.Fatalf("replay after retention prune = %v, %v", inserted, err)
	}
}

func TestPrunePassiveNotificationsCapEvictsReadOldestFirst(t *testing.T) {
	st := NewMemoryStore()
	now := time.Now().UTC()
	ids := []string{"read-old", "read-new", "unread-old", "unread-mid", "unread-new"}
	offsets := map[string]time.Duration{
		"read-old": -50 * time.Minute, "read-new": -10 * time.Minute,
		"unread-old": -40 * time.Minute, "unread-mid": -30 * time.Minute, "unread-new": -time.Minute,
	}
	for _, id := range ids {
		notification := testPassiveNotification(id, "endpoint-1", "delivery-"+id, "fingerprint-"+id)
		notification.CreatedAt = now.Add(offsets[id])
		if _, inserted, err := st.CreatePassiveNotification(t.Context(), notification); err != nil || !inserted {
			t.Fatalf("create %s = %v, %v", id, inserted, err)
		}
	}
	for _, id := range []string{"read-old", "read-new"} {
		if _, err := st.MarkPassiveNotificationRead(t.Context(), app.DefaultOwnerID, id, time.Time{}); err != nil {
			t.Fatal(err)
		}
	}

	if removed := mustPrunePassiveNotifications(t, st, time.Time{}, 2); removed != 3 {
		t.Fatalf("cap eviction removed %d", removed)
	}
	items := mustListPassiveNotifications(t, st, app.DefaultOwnerID, "", 10)
	if len(items) != 2 || items[0].ID != "unread-new" || items[1].ID != "unread-mid" {
		// Both read records go first (oldest first), then the oldest unread.
		t.Fatalf("survivors after cap eviction = %#v", items)
	}
	// Owners at or under the cap are untouched.
	if removed := mustPrunePassiveNotifications(t, st, time.Time{}, 2); removed != 0 {
		t.Fatalf("stable prune removed %d", removed)
	}
}

func TestPassiveNotificationRevisionSignalsInboxChanges(t *testing.T) {
	st := NewMemoryStore()
	owner := app.DefaultOwnerID
	if got := mustPassiveNotificationRevision(t, st, owner); got != 0 {
		t.Fatalf("initial revision = %d", got)
	}
	notification := testPassiveNotification("notification-1", "endpoint-1", "delivery-1", "fingerprint-1")
	if _, _, err := st.CreatePassiveNotification(t.Context(), notification); err != nil {
		t.Fatal(err)
	}
	afterCreate := mustPassiveNotificationRevision(t, st, owner)
	if afterCreate == 0 {
		t.Fatal("create did not bump the revision")
	}
	if _, _, err := st.CreatePassiveNotification(t.Context(), notification); err != nil {
		t.Fatal(err)
	}
	if got := mustPassiveNotificationRevision(t, st, owner); got != afterCreate {
		t.Fatalf("idempotent replay changed revision %d -> %d", afterCreate, got)
	}
	if _, err := st.MarkPassiveNotificationRead(t.Context(), owner, notification.ID, time.Time{}); err != nil {
		t.Fatal(err)
	}
	afterRead := mustPassiveNotificationRevision(t, st, owner)
	if afterRead == afterCreate {
		t.Fatal("mark-read did not bump the revision")
	}
	if _, err := st.MarkPassiveNotificationRead(t.Context(), owner, notification.ID, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if got := mustPassiveNotificationRevision(t, st, owner); got != afterRead {
		t.Fatalf("repeated mark-read changed revision %d -> %d", afterRead, got)
	}
	if _, err := st.MarkAllPassiveNotificationsRead(t.Context(), owner, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if got := mustPassiveNotificationRevision(t, st, owner); got != afterRead {
		t.Fatalf("no-op mark-all changed revision %d -> %d", afterRead, got)
	}
	if removed := mustPrunePassiveNotifications(t, st, time.Now().UTC().Add(time.Minute), 0); removed != 1 {
		t.Fatalf("prune removed %d", removed)
	}
	if got := mustPassiveNotificationRevision(t, st, owner); got == afterRead {
		t.Fatal("prune did not bump the revision")
	}
	if got := mustPassiveNotificationRevision(t, st, "other-owner"); got != 0 {
		t.Fatalf("foreign owner revision = %d", got)
	}
}

func TestFileStoreSnapshotRebuildsPassiveNotificationIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	notification := testPassiveNotification("notification-1", "endpoint-1", "delivery-1", "fingerprint-1")
	if _, inserted, err := st.CreatePassiveNotification(t.Context(), notification); err != nil || !inserted {
		t.Fatalf("create = %v, %v", inserted, err)
	}

	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	// The snapshot never persists the derived index; dedup after reload proves
	// it was rebuilt from the notification records.
	if _, inserted, err := reloaded.CreatePassiveNotification(t.Context(), notification); err != nil || inserted {
		t.Fatalf("reload dedup = %v, %v", inserted, err)
	}
	tampered := notification
	tampered.Fingerprint = "fingerprint-tampered"
	if _, _, err := reloaded.CreatePassiveNotification(t.Context(), tampered); !errors.Is(err, ErrPassiveNotificationConflict) {
		t.Fatalf("reload conflict error = %v", err)
	}

	// Prune persists through the decorator and survives another reload.
	second := testPassiveNotification("notification-2", "endpoint-1", "delivery-2", "fingerprint-2")
	second.CreatedAt = time.Now().UTC().Add(time.Second)
	if _, inserted, err := reloaded.CreatePassiveNotification(t.Context(), second); err != nil || !inserted {
		t.Fatalf("create second = %v, %v", inserted, err)
	}
	if removed := mustPrunePassiveNotifications(t, reloaded, time.Time{}, 1); removed != 1 {
		t.Fatalf("prune removed %d", removed)
	}
	final, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	items := mustListPassiveNotifications(t, final, app.DefaultOwnerID, "", 10)
	if len(items) != 1 || items[0].ID != second.ID {
		t.Fatalf("persisted survivors = %#v", items)
	}
}

func TestFileStoreLoadsLegacySnapshotWithoutPassiveNotificationFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	// A pre-inbox snapshot has no passive_notifications key at all.
	if err := os.WriteFile(path, []byte(`{"sessions":{},"clients":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	notification := testPassiveNotification("notification-1", "endpoint-1", "delivery-1", "fingerprint-1")
	if _, inserted, err := st.CreatePassiveNotification(t.Context(), notification); err != nil || !inserted {
		t.Fatalf("create on legacy snapshot = %v, %v", inserted, err)
	}
	if _, inserted, err := st.CreatePassiveNotification(t.Context(), notification); err != nil || inserted {
		t.Fatalf("dedup on legacy snapshot = %v, %v", inserted, err)
	}
	if got := mustPassiveNotificationRevision(t, st, app.DefaultOwnerID); got == 0 {
		t.Fatal("revision did not track legacy-snapshot store")
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
