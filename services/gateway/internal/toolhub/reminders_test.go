package toolhub

import (
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestRemindersCreateListCancel(t *testing.T) {
	cfg := config.Default()
	st := store.NewMemoryStore()
	hub := New(cfg, st)

	created, err := hub.Execute(t.Context(), "reminders.create", map[string]any{
		"text":     "带伞",
		"due_time": "2026-07-01T09:00:00+08:00",
		"timezone": "Asia/Shanghai",
	}, "s1", "r1")
	if err != nil {
		t.Fatalf("create reminder: %v", err)
	}
	out := created.Output.(map[string]any)
	id := out["reminder_id"].(string)
	if id == "" || out["status"] != "pending" {
		t.Fatalf("unexpected create output: %#v", out)
	}
	if out["channel"] != "web" {
		t.Fatalf("web-origin reminder should default to web, got %#v", out)
	}

	listed, err := hub.Execute(t.Context(), "reminders.list", map[string]any{"status": "pending"}, "s1", "r1")
	if err != nil {
		t.Fatalf("list reminders: %v", err)
	}
	listOut := listed.Output.(map[string]any)
	if listOut["count"] != 1 {
		t.Fatalf("expected one reminder, got %#v", listOut)
	}

	canceled, err := hub.Execute(t.Context(), "reminders.cancel", map[string]any{"reminder_id": id}, "s1", "r1")
	if err != nil {
		t.Fatalf("cancel reminder: %v", err)
	}
	cancelOut := canceled.Output.(map[string]any)
	if cancelOut["status"] != "canceled" {
		t.Fatalf("expected canceled reminder, got %#v", cancelOut)
	}
}

func TestRemindersCreateRequiresRecipientWhenWebSessionHasMultipleWeixinBindings(t *testing.T) {
	cfg := config.Default()
	st := store.NewMemoryStore()
	now := time.Now().UTC()
	for _, binding := range []app.NotificationBinding{
		{ID: "bind_a", Channel: "weixin", Provider: "openclaw-weixin-qr", Status: "active", DisplayName: "用户A", ExternalUserID: "wx-a", ContextToken: "ctx-a", CredentialRef: "cred-a", CreatedAt: now, UpdatedAt: now},
		{ID: "bind_b", Channel: "weixin", Provider: "openclaw-weixin-qr", Status: "active", DisplayName: "用户B", ExternalUserID: "wx-b", ContextToken: "ctx-b", CredentialRef: "cred-b", CreatedAt: now, UpdatedAt: now},
	} {
		st.SaveNotificationBinding(binding)
	}
	hub := New(cfg, st)
	_, err := hub.Execute(t.Context(), "reminders.create", map[string]any{
		"text":     "喝水",
		"due_time": "2026-07-01T09:00:00+08:00",
		"channel":  "weixin",
	}, "web_session", "run_web")
	if err == nil || !strings.Contains(err.Error(), "多个微信绑定用户") {
		t.Fatalf("expected multiple binding clarification error, got %v", err)
	}
}

func TestRemindersCreateResolvesExplicitWeixinRecipientFromWebSession(t *testing.T) {
	cfg := config.Default()
	st := store.NewMemoryStore()
	now := time.Now().UTC()
	st.SaveNotificationBinding(app.NotificationBinding{
		ID:             "bind_a",
		Channel:        "weixin",
		Provider:       "openclaw-weixin-qr",
		Status:         "active",
		DisplayName:    "用户A",
		ExternalUserID: "wx-a",
		ContextToken:   "ctx-a",
		CredentialRef:  "cred-a",
		BaseURL:        "https://ilinkai.weixin.qq.com",
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	st.SaveNotificationBinding(app.NotificationBinding{
		ID:             "bind_b",
		Channel:        "weixin",
		Provider:       "openclaw-weixin-qr",
		Status:         "active",
		DisplayName:    "用户B",
		ExternalUserID: "wx-b",
		ContextToken:   "ctx-b",
		CredentialRef:  "cred-b",
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	hub := New(cfg, st)
	created, err := hub.Execute(t.Context(), "reminders.create", map[string]any{
		"text":      "喝水",
		"due_time":  "2026-07-01T09:00:00+08:00",
		"channel":   "weixin",
		"recipient": "用户B",
	}, "web_session", "run_web")
	if err != nil {
		t.Fatal(err)
	}
	out := created.Output.(map[string]any)
	reminder, ok := st.GetReminder(out["reminder_id"].(string))
	if !ok {
		t.Fatal("reminder missing")
	}
	if reminder.Recipient != "wx-b" || reminder.RecipientBinding != "ctx-b" || reminder.BindingID != "bind_b" || reminder.CredentialRef != "cred-b" {
		t.Fatalf("reminder should capture selected weixin recipient, got %#v", reminder)
	}
}

func TestRemindersCreateUsesCurrentWeixinChatRecipient(t *testing.T) {
	cfg := config.Default()
	st := store.NewMemoryStore()
	linked := st.CreateSession("微信会话")
	st.SaveNotificationBinding(app.NotificationBinding{
		ID:            "bind_weixin",
		Channel:       "weixin",
		Provider:      "openclaw-weixin-qr",
		Status:        "active",
		CredentialRef: "cred_weixin",
		BaseURL:       "https://ilinkai.weixin.qq.com",
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	})
	st.SaveWeixinChatSession(app.WeixinChatSession{
		BindingID:        "bind_weixin",
		Channel:          "weixin",
		Provider:         "openclaw-weixin-qr",
		ExternalUserID:   "wx-user-a",
		LinkedSessionID:  linked.ID,
		Status:           "active",
		LastContextToken: "ctx-a",
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	})
	hub := New(cfg, st)

	created, err := hub.Execute(t.Context(), "reminders.create", map[string]any{
		"text":     "喝水",
		"due_time": "2026-07-01T09:00:00+08:00",
		"channel":  "weixin",
	}, linked.ID, "run_weixin")
	if err != nil {
		t.Fatal(err)
	}
	out := created.Output.(map[string]any)
	reminder, ok := st.GetReminder(out["reminder_id"].(string))
	if !ok {
		t.Fatal("reminder missing")
	}
	if reminder.Recipient != "wx-user-a" || reminder.RecipientBinding != "ctx-a" || reminder.BindingID != "bind_weixin" || reminder.CredentialRef != "cred_weixin" {
		t.Fatalf("reminder should capture current weixin recipient context, got %#v", reminder)
	}
}

func TestRemindersCreateUsesCurrentTelegramChatRecipient(t *testing.T) {
	cfg := config.Default()
	st := store.NewMemoryStore()
	linked := st.CreateSession("Telegram conversation")
	st.SaveNotificationBinding(app.NotificationBinding{
		ID: "bind_telegram", Channel: "telegram", Provider: "telegram-bot-api", Status: "active",
		ExternalUserID: "42", ExternalChatID: "1001", ExternalThreadID: "7",
		CredentialRef: "cred_telegram", BaseURL: "https://api.telegram.org",
	})
	st.SaveExternalChatSession(app.ExternalChatSession{
		BindingID: "bind_telegram", Channel: "telegram", Provider: "telegram-bot-api",
		ExternalUserID: "42", ExternalChatID: "1001", ExternalThreadID: "7",
		LinkedSessionID: linked.ID, Status: "active",
	})
	hub := New(cfg, st)
	created, err := hub.Execute(t.Context(), "reminders.create", map[string]any{
		"text": "喝水", "due_time": "2026-07-01T09:00:00+08:00",
	}, linked.ID, "run_telegram")
	if err != nil {
		t.Fatal(err)
	}
	reminder, ok := st.GetReminder(created.Output.(map[string]any)["reminder_id"].(string))
	if !ok {
		t.Fatal("reminder missing")
	}
	if reminder.Channel != "telegram" || reminder.Recipient != "1001" || reminder.RecipientBinding != "7" || reminder.BindingID != "bind_telegram" || reminder.CredentialRef != "cred_telegram" {
		t.Fatalf("reminder should capture current Telegram target: %#v", reminder)
	}
}
