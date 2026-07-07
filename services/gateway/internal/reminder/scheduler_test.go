package reminder

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/notification"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestSchedulerRecordsFailureWhenChannelDisabled(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.Notifications.Channels["weixin"] = config.NotificationChannelConfig{
		Enabled:  false,
		Provider: "openclaw-weixin-compatible",
	}
	st := store.NewMemoryStore()
	due := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	reminder := st.SaveReminder(app.Reminder{
		Text:        "带伞",
		TextSummary: "带伞",
		DueTime:     due,
		Timezone:    "Asia/Shanghai",
		Channel:     "weixin",
		Status:      "pending",
		CreatedAt:   due.Add(-time.Hour),
		UpdatedAt:   due.Add(-time.Hour),
	})
	scheduler := NewScheduler(st, notification.NewRouter(cfg, st))
	scheduler.now = func() time.Time { return due.Add(time.Minute) }

	deliveries := scheduler.Tick(t.Context())
	if len(deliveries) != 1 {
		t.Fatalf("expected one delivery attempt, got %d", len(deliveries))
	}
	if deliveries[0].Status != "failed" || deliveries[0].RetryState != "blocked" {
		t.Fatalf("unexpected delivery: %#v", deliveries[0])
	}
	updated, ok := st.GetReminder(reminder.ID)
	if !ok {
		t.Fatal("reminder missing")
	}
	if updated.Status != "failed" || updated.LastError == "" {
		t.Fatalf("expected reminder failure state, got %#v", updated)
	}
}

func TestSchedulerStoresWebReminderDelivery(t *testing.T) {
	cfg := config.Default()
	st := store.NewMemoryStore()
	due := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	reminder := st.SaveReminder(app.Reminder{
		Text:        "喝水",
		TextSummary: "喝水",
		DueTime:     due,
		Timezone:    "Asia/Shanghai",
		Channel:     "web",
		Status:      "pending",
		CreatedAt:   due.Add(-time.Hour),
		UpdatedAt:   due.Add(-time.Hour),
	})
	scheduler := NewScheduler(st, notification.NewRouter(cfg, st))
	scheduler.now = func() time.Time { return due.Add(time.Minute) }

	deliveries := scheduler.Tick(t.Context())
	if len(deliveries) != 1 || deliveries[0].Status != "sent" || deliveries[0].Provider != "web-local" {
		t.Fatalf("expected one stored web delivery, got %#v", deliveries)
	}
	updated, ok := st.GetReminder(reminder.ID)
	if !ok {
		t.Fatal("reminder missing")
	}
	if updated.Status != "sent" || updated.LastDeliveryID != deliveries[0].ID {
		t.Fatalf("expected reminder to be marked sent, got %#v", updated)
	}
}

func TestSchedulerUsesReminderSpecificWeixinRecipient(t *testing.T) {
	var sentRecipient string
	var sentContext string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/ilink/bot/sendmessage" {
			http.NotFound(w, r)
			return
		}
		var payload struct {
			Msg struct {
				ToUserID     string `json:"to_user_id"`
				ContextToken string `json:"context_token"`
			} `json:"msg"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		sentRecipient = payload.Msg.ToUserID
		sentContext = payload.Msg.ContextToken
		_, _ = w.Write([]byte(`{"ret":0}`))
	}))
	defer ts.Close()

	cfg := config.Default()
	cfg.Tools.Notifications.Channels["weixin"] = config.NotificationChannelConfig{
		Enabled:   true,
		Provider:  "openclaw-weixin-compatible",
		BaseURL:   ts.URL,
		Token:     "bot-token",
		Recipient: "wx-default-user",
	}
	st := store.NewMemoryStore()
	due := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	st.SaveNotificationBinding(app.NotificationBinding{
		ID:                "bind_default",
		Channel:           "weixin",
		Status:            "active",
		ExternalUserID:    "wx-default-user",
		ContextToken:      "ctx-default",
		CredentialRef:     "configured",
		BaseURL:           ts.URL,
		DefaultForChannel: true,
		CreatedAt:         due.Add(-time.Hour),
		UpdatedAt:         due.Add(-time.Hour),
	})
	st.SaveReminder(app.Reminder{
		Text:             "喝水",
		TextSummary:      "喝水",
		DueTime:          due,
		Timezone:         "Asia/Shanghai",
		Channel:          "weixin",
		Recipient:        "wx-user-a",
		RecipientBinding: "ctx-a",
		Status:           "pending",
		CreatedAt:        due.Add(-time.Hour),
		UpdatedAt:        due.Add(-time.Hour),
	})
	scheduler := NewScheduler(st, notification.NewRouter(cfg, st))
	scheduler.now = func() time.Time { return due.Add(time.Minute) }

	deliveries := scheduler.Tick(t.Context())
	if len(deliveries) != 1 || deliveries[0].Status != "sent" {
		t.Fatalf("expected one sent delivery, got %#v", deliveries)
	}
	if sentRecipient != "wx-user-a" || sentContext != "ctx-a" {
		t.Fatalf("reminder should use per-user recipient/context, got recipient=%q context=%q", sentRecipient, sentContext)
	}
}

func TestSchedulerDoesNotFallbackToDefaultWeixinBinding(t *testing.T) {
	var sendMessageCalls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ilink/bot/sendmessage" {
			sendMessageCalls++
		}
		_, _ = w.Write([]byte(`{"ret":0}`))
	}))
	defer ts.Close()

	cfg := config.Default()
	cfg.Tools.Notifications.Channels["weixin"] = config.NotificationChannelConfig{
		Enabled:  true,
		Provider: "openclaw-weixin-compatible",
		BaseURL:  ts.URL,
		Token:    "bot-token",
	}
	st := store.NewMemoryStore()
	due := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	st.SaveNotificationBinding(app.NotificationBinding{
		ID:                "bind_default",
		Channel:           "weixin",
		Status:            "active",
		ExternalUserID:    "wx-default-user",
		ContextToken:      "ctx-default",
		CredentialRef:     "configured",
		BaseURL:           ts.URL,
		DefaultForChannel: true,
		CreatedAt:         due.Add(-time.Hour),
		UpdatedAt:         due.Add(-time.Hour),
	})
	st.SaveReminder(app.Reminder{
		Text:        "喝水",
		TextSummary: "喝水",
		DueTime:     due,
		Timezone:    "Asia/Shanghai",
		Channel:     "weixin",
		Status:      "pending",
		CreatedAt:   due.Add(-time.Hour),
		UpdatedAt:   due.Add(-time.Hour),
	})
	scheduler := NewScheduler(st, notification.NewRouter(cfg, st))
	scheduler.now = func() time.Time { return due.Add(time.Minute) }

	deliveries := scheduler.Tick(t.Context())
	if len(deliveries) != 1 || deliveries[0].Status != "failed" {
		t.Fatalf("expected one failed delivery, got %#v", deliveries)
	}
	if deliveries[0].Error != "weixin recipient binding is not configured" {
		t.Fatalf("unexpected delivery error: %#v", deliveries[0])
	}
	if sendMessageCalls != 0 {
		t.Fatalf("scheduler must not use default binding, sendmessage calls=%d", sendMessageCalls)
	}
}
