package reminder

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/messagecontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type blockingPublisher struct {
	received chan app.MessageEnvelope
}

func (p blockingPublisher) Publish(ctx context.Context, envelope app.MessageEnvelope) error {
	p.received <- envelope
	<-ctx.Done()
	return ctx.Err()
}

type publisherFunc func(context.Context, app.MessageEnvelope) error

func (fn publisherFunc) Publish(ctx context.Context, envelope app.MessageEnvelope) error {
	return fn(ctx, envelope)
}

type retryablePublishError struct{ error }

func (retryablePublishError) RetryState() string { return "retryable" }

func saveTestSchedule(t *testing.T, st store.Store, id string, due time.Time, recurrence string) app.MessageSchedule {
	t.Helper()
	session := st.CreateSession("Scheduled message")
	schedule := app.MessageSchedule{
		ID: app.ScheduleID(id), SessionID: session.ID, DueTime: due.UTC(), Timezone: "UTC", Recurrence: recurrence,
		DedupeKey: id, Status: "pending", CreatedAt: due.Add(-time.Hour), UpdatedAt: due.Add(-time.Hour),
		Spec: app.ScheduleSpec{
			SchemaVersion: app.ScheduleSpecSchemaVersion, OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID,
			Payload: app.SchedulePayload{Content: app.MessageContent{Parts: []app.MessagePart{{
				ID: id + ":text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: "search later",
			}}}},
			ReturnRoute:   app.ReturnRoute{Mode: app.ReturnToEndpoint, EndpointID: messagecontrol.WebEndpointID(session.ID)},
			Authorization: app.MessageAuthorization{PrincipalID: app.DefaultOwnerID},
		},
	}
	saved, err := messagecontrol.NewScheduleRegistry(st).Save(t.Context(), schedule)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func TestTimerRunKeepsPollingWhileScheduledMessagesAreSlow(t *testing.T) {
	st := store.NewMemoryStore()
	schedules := messagecontrol.NewScheduleRegistry(st)
	now := time.Now().UTC()
	for i := range workerCount {
		saveTestSchedule(t, st, fmt.Sprintf("sched_slow_%d", i), now.Add(-time.Minute), "")
	}
	publisher := blockingPublisher{received: make(chan app.MessageEnvelope, workerCount)}
	scheduler := NewMessageScheduler(st, schedules, publisher)
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
	saveTestSchedule(t, st, "sched_claimed_while_busy", now.Add(-time.Minute), "")
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

func TestSchedulerPublishesEveryDueScheduleThroughMessageRuntime(t *testing.T) {
	st := store.NewMemoryStore()
	due := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	schedule := saveTestSchedule(t, st, "sched_runtime", due, "")
	var got app.MessageEnvelope
	scheduler := NewMessageScheduler(st, messagecontrol.NewScheduleRegistry(st), publisherFunc(func(_ context.Context, envelope app.MessageEnvelope) error {
		got = envelope
		return nil
	}))
	scheduler.now = func() time.Time { return due.Add(time.Minute) }

	deliveries := scheduler.Tick(t.Context())
	if len(deliveries) != 1 || deliveries[0].Status != "sent" || deliveries[0].Provider != "message-runtime" {
		t.Fatalf("unexpected runtime publication: %#v", deliveries)
	}
	if got.Source.Kind != app.MessageSourceTimer || got.Source.ScheduleID != schedule.ID || got.Content.Parts[0].Text != "search later" {
		t.Fatalf("unexpected scheduled envelope: %#v", got)
	}
	updated, _ := st.GetReminder(string(schedule.ID))
	if updated.Status != "sent" {
		t.Fatalf("expected schedule marked sent, got %#v", updated)
	}
}

func TestCanceledPublicationRemainsLeasedForRecovery(t *testing.T) {
	st := store.NewMemoryStore()
	due := time.Now().UTC().Add(-time.Minute)
	saveTestSchedule(t, st, "sched_canceled", due, "")
	scheduler := NewMessageScheduler(st, messagecontrol.NewScheduleRegistry(st), publisherFunc(func(ctx context.Context, _ app.MessageEnvelope) error {
		return ctx.Err()
	}))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	scheduler.Tick(ctx)
	reminder, _ := st.GetReminder("sched_canceled")
	if reminder.Status != "sending" || len(st.ListReminderDeliveries(reminder.ID)) != 0 {
		t.Fatalf("canceled work should remain leased without a terminal delivery: %#v", reminder)
	}
}

func TestSchedulerKeepsRetryableRuntimeFailurePending(t *testing.T) {
	st := store.NewMemoryStore()
	due := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	saveTestSchedule(t, st, "sched_retry", due, "")
	scheduler := NewMessageScheduler(st, messagecontrol.NewScheduleRegistry(st), publisherFunc(func(context.Context, app.MessageEnvelope) error {
		return retryablePublishError{errors.New("runtime unavailable")}
	}))
	now := due.Add(time.Minute)
	scheduler.now = func() time.Time { return now }
	deliveries := scheduler.Tick(t.Context())
	if len(deliveries) != 1 || deliveries[0].RetryState != "retryable" {
		t.Fatalf("expected retryable publication failure, got %#v", deliveries)
	}
	updated, _ := st.GetReminder("sched_retry")
	if updated.Status != "pending" || !updated.DueTime.Equal(now.Add(time.Minute)) {
		t.Fatalf("expected pending schedule with backoff, got %#v", updated)
	}
}

func TestSchedulerReschedulesRecurringMessage(t *testing.T) {
	st := store.NewMemoryStore()
	due := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	saveTestSchedule(t, st, "sched_daily", due, "daily")
	scheduler := NewMessageScheduler(st, messagecontrol.NewScheduleRegistry(st), publisherFunc(func(context.Context, app.MessageEnvelope) error { return nil }))
	scheduler.now = func() time.Time { return due.Add(time.Minute) }

	deliveries := scheduler.Tick(t.Context())
	if len(deliveries) != 1 || deliveries[0].Status != "sent" {
		t.Fatalf("expected successful runtime publication, got %#v", deliveries)
	}
	updated, _ := st.GetReminder("sched_daily")
	if updated.Status != "pending" || !updated.DueTime.Equal(due.Add(24*time.Hour)) || updated.DeliveryAttempt != 0 || updated.SentAt == nil {
		t.Fatalf("recurring schedule was not re-armed: %#v", updated)
	}
}

func TestSchedulerWithoutMessagePublisherFailsClosed(t *testing.T) {
	st := store.NewMemoryStore()
	due := time.Now().UTC().Add(-time.Minute)
	saveTestSchedule(t, st, "sched_no_publisher", due, "")
	scheduler := NewMessageScheduler(st, messagecontrol.NewScheduleRegistry(st), nil)
	scheduler.now = func() time.Time { return due.Add(time.Minute) }
	deliveries := scheduler.Tick(t.Context())
	if len(deliveries) != 1 || deliveries[0].Status != "failed" || deliveries[0].RetryState != "blocked" {
		t.Fatalf("expected blocked failure, got %#v", deliveries)
	}
}

func TestNextOccurrence(t *testing.T) {
	base := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name, recurrence, timezone string
		now, want                  time.Time
		ok                         bool
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
			got, ok := nextOccurrence(app.Reminder{DueTime: base, Timezone: tc.timezone, Recurrence: tc.recurrence}, tc.now)
			if ok != tc.ok || (ok && !got.Equal(tc.want)) {
				t.Fatalf("got (%v, %v), want (%v, %v)", got, ok, tc.want, tc.ok)
			}
		})
	}
}
