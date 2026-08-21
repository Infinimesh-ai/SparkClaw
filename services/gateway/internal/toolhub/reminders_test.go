package toolhub

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
)

type cancelAfterBindingListStore struct {
	store.Store
	cancel context.CancelFunc
}

func (s *cancelAfterBindingListStore) ListNotificationBindings(ctx context.Context, channel, status string) ([]app.NotificationBinding, error) {
	bindings, err := s.Store.ListNotificationBindings(ctx, channel, status)
	s.cancel()
	return bindings, err
}

func TestRemindersCreateListCancel(t *testing.T) {
	cfg := config.Default()
	st := store.NewMemoryStore()
	hub := New(cfg, st)
	session := storetest.MustCreateSession(t, st, "Reminder test")

	created, err := hub.Execute(t.Context(), "reminders.create", map[string]any{
		"text":     "带伞",
		"due_time": "2026-07-01T09:00:00+08:00",
		"timezone": "Asia/Shanghai",
	}, session.ID, "r1")
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

	listed, err := hub.Execute(t.Context(), "reminders.list", map[string]any{"status": "pending"}, session.ID, "r1")
	if err != nil {
		t.Fatalf("list reminders: %v", err)
	}
	listOut := listed.Output.(map[string]any)
	if listOut["count"] != 1 {
		t.Fatalf("expected one reminder, got %#v", listOut)
	}

	canceled, err := hub.Execute(t.Context(), "reminders.cancel", map[string]any{
		"reminder_id": id, "expected_updated_at": out["updated_at"],
	}, session.ID, "r1")
	if err != nil {
		t.Fatalf("cancel reminder: %v", err)
	}
	cancelOut := canceled.Output.(map[string]any)
	if cancelOut["status"] != "canceled" {
		t.Fatalf("expected canceled reminder, got %#v", cancelOut)
	}
}

func TestRemindersCreatePersistsRuntimeSchedule(t *testing.T) {
	st := store.NewMemoryStore()
	session := storetest.MustCreateSession(t, st, "Scheduled browser request")
	hub := New(config.Default(), st)
	created, err := hub.Execute(t.Context(), "reminders.create", map[string]any{
		"text": "search tomorrow's weather", "due_time": "2026-07-18T09:00:00+08:00",
	}, session.ID, "run_schedule")
	if err != nil {
		t.Fatal(err)
	}
	reminder, ok := mustToolhubGetReminder(t, st, created.Output.(map[string]any)["reminder_id"].(string))
	if !ok || reminder.ScheduleSpec == nil {
		t.Fatalf("runtime schedule was not persisted: %#v", reminder)
	}
	if reminder.ScheduleSpec.SchemaVersion != app.ScheduleSpecSchemaVersion || reminder.ScheduleSpec.Payload.Content.Parts[0].Text != "search tomorrow's weather" {
		t.Fatalf("unexpected runtime schedule: %#v", reminder.ScheduleSpec)
	}
}

func TestRemindersCreateKeepsOwnedContextThroughScheduleSave(t *testing.T) {
	base := store.NewMemoryStore()
	session := storetest.MustCreateSession(t, base, "Canceled schedule")
	storetest.MustCreateNotificationBinding(t, base, app.NotificationBinding{
		ID: "binding-cancel-schedule", Channel: "alpha", Status: app.NotificationBindingActive,
		ExternalUserID: "recipient", Scopes: []string{app.BindingScopeReminderSendSelf},
	})
	ctx, cancel := context.WithCancel(t.Context())
	wrapped := &cancelAfterBindingListStore{Store: base, cancel: cancel}
	hub := New(config.Default(), wrapped)
	if _, err := hub.Execute(ctx, "reminders.create", map[string]any{
		"text": "must not persist", "due_time": "2026-07-18T09:00:00+08:00", "channel": "alpha",
	}, session.ID, "run_canceled_schedule"); err == nil {
		t.Fatal("canceled reminder schedule was persisted")
	}
	if reminders := mustToolhubListReminders(t, base, app.ReminderFilter{}); len(reminders) != 0 {
		t.Fatalf("canceled reminder persisted: %#v", reminders)
	}
}

func TestRemindersListIgnoresRemovedLegacyScheduleSchema(t *testing.T) {
	st := store.NewMemoryStore()
	session := storetest.MustCreateSession(t, st, "Schedule schema cutoff")
	now := time.Now().UTC()
	mustToolhubSaveReminder(t, st, app.Reminder{
		ID: "legacy-reminder", SessionID: session.ID, Text: "old literal payload", DueTime: now.Add(time.Hour),
		Status: "pending", CreatedAt: now, UpdatedAt: now,
		ScheduleSpec: &app.ScheduleSpec{SchemaVersion: app.ScheduleSpecSchemaVersion - 1},
	})
	hub := New(config.Default(), st)
	listed, err := hub.Execute(t.Context(), "reminders.list", map[string]any{"status": "pending"}, session.ID, "run_list")
	if err != nil {
		t.Fatal(err)
	}
	out := listed.Output.(map[string]any)
	if out["count"] != 0 {
		t.Fatalf("removed legacy schema must not be manageable: %#v", out)
	}
}

func TestRemindersUpdateRejectsStaleListVersion(t *testing.T) {
	st := store.NewMemoryStore()
	session := storetest.MustCreateSession(t, st, "Schedule edit")
	hub := New(config.Default(), st)
	created, err := hub.Execute(t.Context(), "reminders.create", map[string]any{
		"text": "first", "due_time": "2026-07-18T09:00:00+08:00",
	}, session.ID, "run_create")
	if err != nil {
		t.Fatal(err)
	}
	out := created.Output.(map[string]any)
	args := map[string]any{
		"reminder_id": out["reminder_id"], "expected_updated_at": out["updated_at"], "text": "second",
	}
	updated, err := hub.Execute(t.Context(), "reminders.update", args, session.ID, "run_update")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Output.(map[string]any)["text"] != "second" {
		t.Fatalf("unexpected update output: %#v", updated.Output)
	}
	args["text"] = "stale overwrite"
	if _, err := hub.Execute(t.Context(), "reminders.update", args, session.ID, "run_stale"); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("expected stale update conflict, got %v", err)
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
		storetest.MustCreateNotificationBinding(t, st, binding)
	}
	hub := New(cfg, st)
	_, err := hub.Execute(t.Context(), "reminders.create", map[string]any{
		"text":     "喝水",
		"due_time": "2026-07-01T09:00:00+08:00",
		"channel":  "weixin",
	}, "web_session", "run_web")
	if err == nil || !strings.Contains(err.Error(), "multiple weixin bindings") {
		t.Fatalf("expected multiple binding clarification error, got %v", err)
	}
}

func TestRemindersCreateResolvesExplicitWeixinRecipientFromWebSession(t *testing.T) {
	cfg := config.Default()
	st := store.NewMemoryStore()
	now := time.Now().UTC()
	storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
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
	storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
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
	reminder, ok := mustToolhubGetReminder(t, st, out["reminder_id"].(string))
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
	linked := storetest.MustCreateSession(t, st, "微信会话")
	storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
		ID:            "bind_weixin",
		Channel:       "weixin",
		Provider:      "openclaw-weixin-qr",
		Status:        "active",
		CredentialRef: "cred_weixin",
		BaseURL:       "https://ilinkai.weixin.qq.com",
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	})
	storetest.MustSaveExternalChatSession(t, st, app.WeixinChatSession{
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
	reminder, ok := mustToolhubGetReminder(t, st, out["reminder_id"].(string))
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
	linked := storetest.MustCreateSession(t, st, "Telegram conversation")
	storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
		ID: "bind_telegram", Channel: "telegram", Provider: "telegram-bot-api", Status: "active",
		ExternalUserID: "42", ExternalChatID: "1001", ExternalThreadID: "7",
		CredentialRef: "cred_telegram", BaseURL: "https://api.telegram.org",
	})
	storetest.MustSaveExternalChatSession(t, st, app.ExternalChatSession{
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
	reminder, ok := mustToolhubGetReminder(t, st, created.Output.(map[string]any)["reminder_id"].(string))
	if !ok {
		t.Fatal("reminder missing")
	}
	if reminder.Channel != "telegram" || reminder.Recipient != "1001" || reminder.RecipientBinding != "7" || reminder.BindingID != "bind_telegram" || reminder.CredentialRef != "cred_telegram" {
		t.Fatalf("reminder should capture current Telegram target: %#v", reminder)
	}
}
