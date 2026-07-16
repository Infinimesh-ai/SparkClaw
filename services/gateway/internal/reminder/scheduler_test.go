package reminder

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/messagecontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/notification"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/telegram"
)

type blockingPublisher struct {
	received chan app.MessageEnvelope
}

func (p blockingPublisher) Publish(ctx context.Context, envelope app.MessageEnvelope) error {
	p.received <- envelope
	<-ctx.Done()
	return ctx.Err()
}

func TestTimerRunKeepsPollingWhileScheduledRequestsAreSlow(t *testing.T) {
	st := store.NewMemoryStore()
	session := st.CreateSession("Timer queue")
	schedules := messagecontrol.NewScheduleRegistry(st)
	endpoints := messagecontrol.NewEndpointRegistry(st)
	routes := messagecontrol.NewReturnRouteResolver(endpoints)
	now := time.Now().UTC()
	makeSchedule := func(id string) {
		schedule := app.MessageSchedule{
			ID: app.ScheduleID(id), SessionID: session.ID, DueTime: now.Add(-time.Minute), Timezone: "UTC", DedupeKey: id, Status: "pending", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
			Spec: app.ScheduleSpec{
				SchemaVersion: app.ScheduleSpecSchemaVersion, OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID,
				Payload:       app.SchedulePayload{Mode: app.SchedulePayloadRequest, Content: app.MessageContent{Parts: []app.MessagePart{{ID: id + ":text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: "search later"}}}},
				ReturnRoute:   app.ReturnRoute{Mode: app.ReturnToEndpoint, EndpointID: messagecontrol.WebEndpointID(session.ID)},
				Authorization: app.MessageAuthorization{PrincipalID: app.DefaultOwnerID}, ExpectedCapabilityPath: []app.CapabilityID{"browser"},
			},
		}
		if _, err := schedules.Save(t.Context(), schedule); err != nil {
			t.Fatal(err)
		}
	}
	for i := range workerCount {
		makeSchedule(fmt.Sprintf("sched_slow_%d", i))
	}
	publisher := blockingPublisher{received: make(chan app.MessageEnvelope, workerCount)}
	scheduler := NewMessageScheduler(st, schedules, routes, nil, publisher)
	scheduler.interval = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { scheduler.Run(ctx); close(done) }()
	for range workerCount {
		envelope := <-publisher.received
		if envelope.Source.Kind != app.MessageSourceTimer || envelope.Source.ScheduleID == "" {
			t.Fatalf("timer did not publish a normalized envelope: %#v", envelope)
		}
	}
	makeSchedule("sched_claimed_while_busy")
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if reminder, ok := st.GetReminder("sched_claimed_while_busy"); ok && reminder.Status == "sending" {
			cancel()
			<-done
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	t.Fatal("poll loop stopped claiming schedules while all workflow workers were busy")
}

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

func TestSchedulerDeliversTelegramReminderThroughBoundCredential(t *testing.T) {
	var gotChatID int64
	var gotText string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			ChatID int64  `json:"chat_id"`
			Text   string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		gotChatID, gotText = payload.ChatID, payload.Text
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1,"chat":{"id":88,"type":"private"},"date":1}}`))
	}))
	defer provider.Close()

	cfg := config.Default()
	st := store.NewMemoryStore()
	vault := credential.New(st, credential.Options{Key: base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))})
	ref, err := vault.Seal(t.Context(), "telegram-bot-token", []byte("123456:AA-reminder-secret"))
	if err != nil {
		t.Fatal(err)
	}
	binding := st.SaveNotificationBinding(app.NotificationBinding{
		ID: "bind_telegram_reminder", Channel: "telegram", Provider: "telegram-bot-api", Status: "active",
		ExternalChatID: "88", CredentialRef: ref, BaseURL: provider.URL,
	})
	due := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	reminder := st.SaveReminder(app.Reminder{
		Text: "Telegram reminder", TextSummary: "Telegram reminder", DueTime: due, Timezone: "Asia/Shanghai",
		Channel: "telegram", Recipient: "88", BindingID: binding.ID, CredentialRef: ref,
		BaseURL: provider.URL, Status: "pending", CreatedAt: due.Add(-time.Hour), UpdatedAt: due.Add(-time.Hour),
	})
	router := notification.NewRouter(cfg, st).WithAdapter("telegram", telegram.NewNotificationAdapter(st, vault, cfg.Tools.Notifications.Channels["telegram"]))
	scheduler := NewScheduler(st, router)
	scheduler.now = func() time.Time { return due.Add(time.Minute) }
	deliveries := scheduler.Tick(t.Context())
	if len(deliveries) != 1 || deliveries[0].Status != "sent" || deliveries[0].Provider != "telegram-bot-api" {
		t.Fatalf("unexpected Telegram delivery: %#v", deliveries)
	}
	if gotChatID != 88 || gotText != "Telegram reminder" {
		t.Fatalf("unexpected Telegram request: chat=%d text=%q", gotChatID, gotText)
	}
	updated, _ := st.GetReminder(reminder.ID)
	if updated.Status != "sent" || updated.LastDeliveryID != deliveries[0].ID {
		t.Fatalf("Telegram reminder state was not completed: %#v", updated)
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

func TestSchedulerReschedulesRecurringReminder(t *testing.T) {
	cfg := config.Default()
	st := store.NewMemoryStore()
	due := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	reminder := st.SaveReminder(app.Reminder{
		Text:        "喝水",
		TextSummary: "喝水",
		DueTime:     due,
		Timezone:    "UTC",
		Channel:     "web",
		Recurrence:  "daily",
		Status:      "pending",
		CreatedAt:   due.Add(-time.Hour),
		UpdatedAt:   due.Add(-time.Hour),
	})
	scheduler := NewScheduler(st, notification.NewRouter(cfg, st))
	now := due.Add(time.Minute)
	scheduler.now = func() time.Time { return now }

	deliveries := scheduler.Tick(t.Context())
	if len(deliveries) != 1 || deliveries[0].Status != "sent" {
		t.Fatalf("expected one sent delivery, got %#v", deliveries)
	}
	updated, ok := st.GetReminder(reminder.ID)
	if !ok {
		t.Fatal("reminder missing")
	}
	if updated.Status != "pending" {
		t.Fatalf("recurring reminder should be re-armed as pending, got %q", updated.Status)
	}
	wantNext := due.Add(24 * time.Hour)
	if !updated.DueTime.Equal(wantNext) {
		t.Fatalf("expected next due time %v, got %v", wantNext, updated.DueTime)
	}
	if updated.DeliveryAttempt != 0 {
		t.Fatalf("expected delivery attempt reset, got %d", updated.DeliveryAttempt)
	}
	if updated.SentAt == nil {
		t.Fatal("expected sent_at to record the last successful send")
	}

	if again := scheduler.Tick(t.Context()); len(again) != 0 {
		t.Fatalf("reminder not yet due should not fire, got %#v", again)
	}

	now = wantNext.Add(time.Minute)
	deliveries = scheduler.Tick(t.Context())
	if len(deliveries) != 1 || deliveries[0].Status != "sent" {
		t.Fatalf("expected the next occurrence to fire, got %#v", deliveries)
	}
	updated, _ = st.GetReminder(reminder.ID)
	if !updated.DueTime.Equal(due.Add(48 * time.Hour)) {
		t.Fatalf("expected due time to advance to %v, got %v", due.Add(48*time.Hour), updated.DueTime)
	}
}

func TestSchedulerKeepsRetryableFailurePending(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
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
	reminder := st.SaveReminder(app.Reminder{
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
	now := due.Add(time.Minute)
	scheduler.now = func() time.Time { return now }

	deliveries := scheduler.Tick(t.Context())
	if len(deliveries) != 1 || deliveries[0].Status != "failed" || deliveries[0].RetryState != "retryable" {
		t.Fatalf("expected one retryable failure, got %#v", deliveries)
	}
	updated, ok := st.GetReminder(reminder.ID)
	if !ok {
		t.Fatal("reminder missing")
	}
	if updated.Status != "pending" {
		t.Fatalf("retryable failure should stay pending, got %q", updated.Status)
	}
	if updated.LastError == "" {
		t.Fatal("expected last error to be recorded")
	}
	wantRetry := now.Add(time.Minute)
	if !updated.DueTime.Equal(wantRetry) {
		t.Fatalf("expected backoff due time %v, got %v", wantRetry, updated.DueTime)
	}

	if again := scheduler.Tick(t.Context()); len(again) != 0 {
		t.Fatalf("reminder in backoff should not fire, got %#v", again)
	}

	now = wantRetry.Add(time.Second)
	deliveries = scheduler.Tick(t.Context())
	if len(deliveries) != 1 || deliveries[0].Attempt != 2 {
		t.Fatalf("expected second attempt after backoff, got %#v", deliveries)
	}
	updated, _ = st.GetReminder(reminder.ID)
	if updated.Status != "pending" {
		t.Fatalf("second retryable failure should stay pending, got %q", updated.Status)
	}
	if !updated.DueTime.Equal(now.Add(2 * time.Minute)) {
		t.Fatalf("expected doubled backoff %v, got %v", now.Add(2*time.Minute), updated.DueTime)
	}
}

func TestSchedulerBlockedFailureIsTerminal(t *testing.T) {
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
	now := due.Add(time.Minute)
	scheduler.now = func() time.Time { return now }

	deliveries := scheduler.Tick(t.Context())
	if len(deliveries) != 1 || deliveries[0].RetryState != "blocked" {
		t.Fatalf("expected one blocked failure, got %#v", deliveries)
	}
	updated, _ := st.GetReminder(reminder.ID)
	if updated.Status != "failed" {
		t.Fatalf("blocked failure should be terminal, got %q", updated.Status)
	}

	now = now.Add(time.Hour)
	if again := scheduler.Tick(t.Context()); len(again) != 0 {
		t.Fatalf("terminally failed reminder must not be retried, got %#v", again)
	}
}

func TestSchedulerMarksReminderSendingDuringDelivery(t *testing.T) {
	st := store.NewMemoryStore()
	var reminderID string
	var statusDuringSend string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if current, ok := st.GetReminder(reminderID); ok {
			statusDuringSend = current.Status
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
	due := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	reminder := st.SaveReminder(app.Reminder{
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
	reminderID = reminder.ID
	scheduler := NewScheduler(st, notification.NewRouter(cfg, st))
	scheduler.now = func() time.Time { return due.Add(time.Minute) }

	deliveries := scheduler.Tick(t.Context())
	if len(deliveries) != 1 || deliveries[0].Status != "sent" {
		t.Fatalf("expected one sent delivery, got %#v", deliveries)
	}
	if statusDuringSend != "sending" {
		t.Fatalf("reminder should be claimed as sending during the network call, got %q", statusDuringSend)
	}
	claimed := st.ClaimDueReminders(due.Add(time.Minute), due.Add(-time.Hour), 10)
	if len(claimed) != 0 {
		t.Fatalf("sent reminder must not be claimable again, got %#v", claimed)
	}
}

func TestSchedulerReclaimsStaleSendingReminder(t *testing.T) {
	cfg := config.Default()
	st := store.NewMemoryStore()
	due := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	reminder := st.SaveReminder(app.Reminder{
		Text:        "喝水",
		TextSummary: "喝水",
		DueTime:     due,
		Timezone:    "Asia/Shanghai",
		Channel:     "web",
		Status:      "sending",
		CreatedAt:   due.Add(-time.Hour),
		UpdatedAt:   due.Add(-10 * time.Minute),
	})
	scheduler := NewScheduler(st, notification.NewRouter(cfg, st))
	scheduler.now = func() time.Time { return due.Add(time.Minute) }

	deliveries := scheduler.Tick(t.Context())
	if len(deliveries) != 1 || deliveries[0].Status != "sent" {
		t.Fatalf("expected stale sending reminder to be reclaimed and delivered, got %#v", deliveries)
	}
	updated, _ := st.GetReminder(reminder.ID)
	if updated.Status != "sent" {
		t.Fatalf("expected reminder marked sent, got %q", updated.Status)
	}
}

func TestNextOccurrence(t *testing.T) {
	base := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string
		recurrence string
		timezone   string
		now        time.Time
		want       time.Time
		ok         bool
	}{
		{"daily", "daily", "UTC", base.Add(time.Minute), base.Add(24 * time.Hour), true},
		{"daily chinese", "每天", "UTC", base.Add(time.Minute), base.Add(24 * time.Hour), true},
		{"weekly", "weekly", "UTC", base.Add(time.Minute), base.Add(7 * 24 * time.Hour), true},
		{"every 2 hours", "every 2 hours", "UTC", base.Add(time.Minute), base.Add(2 * time.Hour), true},
		{"duration", "45m", "UTC", base.Add(time.Minute), base.Add(45 * time.Minute), true},
		{"skips missed occurrences", "daily", "UTC", base.Add(3*24*time.Hour + time.Minute), base.Add(4 * 24 * time.Hour), true},
		{"monthly", "monthly", "UTC", base.Add(time.Minute), time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC), true},
		{"unknown", "sometimes", "UTC", base.Add(time.Minute), time.Time{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := nextOccurrence(app.Reminder{
				DueTime:    base,
				Timezone:   tc.timezone,
				Recurrence: tc.recurrence,
			}, tc.now)
			if ok != tc.ok {
				t.Fatalf("expected ok=%v, got %v", tc.ok, ok)
			}
			if ok && !got.Equal(tc.want) {
				t.Fatalf("expected next occurrence %v, got %v", tc.want, got)
			}
		})
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
