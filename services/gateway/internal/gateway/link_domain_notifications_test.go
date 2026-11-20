package gateway

import (
	"fmt"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
)

func TestLinkNotificationsPageAndMarkOnlyWhatTheDeviceListed(t *testing.T) {
	b, desk, ctx := linkDomainTestDevice(t)
	add := func(i int) {
		t.Helper()
		now := time.Now().UTC().Add(time.Duration(i) * time.Second)
		if _, _, err := b.server.store.CreatePassiveNotification(ctx, app.PassiveNotification{ID: fmt.Sprintf("pn_%02d", i), OwnerID: app.DefaultOwnerID, EndpointID: "ep",
			IdempotencyKey: fmt.Sprintf("k%d", i), Fingerprint: fmt.Sprintf("f%d", i), NotificationID: fmt.Sprintf("n%d", i), Source: "mail", Kind: "new_mail", OccurredAt: now, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		add(i)
	}
	// Another owner's notification is never listed or marked.
	if _, _, err := b.server.store.CreatePassiveNotification(ctx, app.PassiveNotification{ID: "pn_foreign", OwnerID: "other-owner", EndpointID: "ep", IdempotencyKey: "kf", Fingerprint: "ff",
		NotificationID: "nf", Source: "mail", Kind: "new_mail", OccurredAt: time.Now().UTC(), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	type list struct {
		Revision string `json:"revision"`
		Value    struct {
			Notifications []passiveNotificationView `json:"notifications"`
			Unread        int                       `json:"unread_count"`
			Watermark     string                    `json:"watermark"`
			Next          string                    `json:"next_cursor"`
		} `json:"value"`
	}
	var first list
	if err := linkDomainCall(ctx, desk, "notifications.list", map[string]any{"limit": 2}, "", &first); err != nil || len(first.Value.Notifications) != 2 || first.Value.Next == "" || first.Value.Unread != 3 || first.Revision == "" {
		t.Fatalf("first page %+v %v", first, err)
	}
	var rest list
	if err := linkDomainCall(ctx, desk, "notifications.list", map[string]any{"limit": 2, "after": first.Value.Next}, "", &rest); err != nil || len(rest.Value.Notifications) != 1 {
		t.Fatalf("second page %+v %v", rest, err)
	}
	// A notification that arrives after the list is not marked read.
	add(3)
	var marked struct {
		Value struct {
			Updated int `json:"updated"`
			Unread  int `json:"unread_count"`
		} `json:"value"`
	}
	readAll := newLinkUUID()
	if err := linkDomainCall(ctx, desk, "notifications.read_all", map[string]string{"watermark": first.Value.Watermark}, readAll, &marked); err != nil || marked.Value.Updated != 3 || marked.Value.Unread != 1 {
		t.Fatalf("read all %+v %v", marked, err)
	}
	if foreign, _, _ := b.server.store.GetPassiveNotification(ctx, "other-owner", "pn_foreign"); foreign.ReadAt != nil {
		t.Fatal("another owner's notification was marked")
	}
	// The same key replays the original answer.
	var again struct {
		Value struct {
			Updated int `json:"updated"`
		} `json:"value"`
	}
	if err := linkDomainCall(ctx, desk, "notifications.read_all", map[string]string{"watermark": first.Value.Watermark}, readAll, &again); err != nil || again.Value.Updated != 3 {
		t.Fatalf("replay %+v %v", again, err)
	}
	if err := linkDomainCall(ctx, desk, "notifications.read", map[string]string{"id": "pn_03"}, newLinkUUID(), &marked); err != nil || marked.Value.Updated != 1 || marked.Value.Unread != 0 {
		t.Fatalf("read one %+v %v", marked, err)
	}
	if err := linkDomainCall(ctx, desk, "notifications.read", map[string]string{"id": "missing"}, newLinkUUID(), nil); contract.AsError(err).Reason != "notification_not_found" {
		t.Fatal(err)
	}
	if err := linkDomainCall(ctx, desk, "notifications.read_all", map[string]string{"watermark": "forged"}, newLinkUUID(), nil); contract.AsError(err).Reason != "invalid_watermark" {
		t.Fatal(err)
	}
	if err := linkDomainCall(ctx, desk, "notifications.list", map[string]any{"after": "gone"}, "", nil); contract.AsError(err).Reason != "cursor_expired" {
		t.Fatal(err)
	}
	// A watermark sealed for another device is refused.
	token, _ := sealNotificationWatermark(notificationWatermark{Owner: app.DefaultOwnerID, Device: "another-device", ExpiresAt: time.Now().Add(time.Minute), IDs: []string{"pn_00"}})
	if err := linkDomainCall(ctx, desk, "notifications.read_all", map[string]string{"watermark": token}, newLinkUUID(), nil); contract.AsError(err).Reason != "watermark_not_owned" {
		t.Fatal(err)
	}
}
