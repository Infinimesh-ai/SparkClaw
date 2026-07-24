package reminder

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/messagecontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

const (
	tickBatchLimit = 50
	sendingLease   = 6 * time.Minute
	retryBaseDelay = time.Minute
	retryMaxDelay  = 30 * time.Minute
	pollInterval   = 10 * time.Second
	workerCount    = 4
	jobTimeout     = 5 * time.Minute
)

// MessagePublisher submits due owner requests to the ordinary Message Runtime.
type MessagePublisher interface {
	Publish(context.Context, app.MessageEnvelope) error
}

type Scheduler struct {
	store     store.Store
	schedules *messagecontrol.ScheduleRegistry
	publisher MessagePublisher
	now       func() time.Time
	interval  time.Duration
}

func NewMessageScheduler(st store.Store, schedules *messagecontrol.ScheduleRegistry, publisher MessagePublisher) *Scheduler {
	return &Scheduler{
		store: st, schedules: schedules, publisher: publisher,
		now:      func() time.Time { return time.Now().UTC() },
		interval: pollInterval,
	}
}

// Run is the production Timer Runtime. Polling only claims and enqueues due
// schedules; fixed workers own provider calls and potentially slow Workflows.
func (s *Scheduler) Run(ctx context.Context) {
	jobs := make(chan app.MessageSchedule, tickBatchLimit)
	var workers sync.WaitGroup
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for schedule := range jobs {
				jobCtx, cancel := context.WithTimeout(ctx, jobTimeout)
				s.process(jobCtx, schedule)
				cancel()
			}
		}()
	}
	poll := func() bool {
		now := s.now().UTC()
		for _, schedule := range s.schedules.ClaimDue(ctx, now, now.Add(-sendingLease), tickBatchLimit) {
			select {
			case jobs <- schedule:
			case <-ctx.Done():
				return false
			}
		}
		return true
	}
	poll()
	ticker := time.NewTicker(s.interval)
	defer func() {
		ticker.Stop()
		close(jobs)
		workers.Wait()
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !poll() {
				return
			}
		}
	}
}

// Tick remains a synchronous compatibility API for deterministic tests and
// explicit administrative calls. The production ticker never calls it.
func (s *Scheduler) Tick(ctx context.Context) []app.ReminderDelivery {
	now := s.now().UTC()
	due := s.schedules.ClaimDue(ctx, now, now.Add(-sendingLease), tickBatchLimit)
	deliveries := make([]app.ReminderDelivery, 0, len(due))
	for _, schedule := range due {
		deliveries = append(deliveries, s.process(ctx, schedule))
	}
	return deliveries
}

func (s *Scheduler) process(ctx context.Context, schedule app.MessageSchedule) app.ReminderDelivery {
	attempt := schedule.DeliveryAttempt + 1
	dedupeKey := schedule.DedupeKey
	if strings.TrimSpace(schedule.Recurrence) != "" && dedupeKey != "" {
		dedupeKey = fmt.Sprintf("%s@%d", dedupeKey, schedule.DueTime.UTC().Unix())
	}
	deliveryRecord := app.ReminderDelivery{
		ID: app.NewID("rdel"), ReminderID: string(schedule.ID), Status: "sent", RetryState: "none", Attempt: attempt, CreatedAt: s.now().UTC(),
	}
	deliveryRecord.Provider = "message-runtime"
	deliveryRecord.ProviderStatus = "published"
	if s.publisher == nil {
		deliveryRecord.Status, deliveryRecord.ProviderStatus, deliveryRecord.Error, deliveryRecord.RetryState = "failed", "failed", "scheduled message publisher is unavailable", "blocked"
	} else if err := s.publisher.Publish(ctx, scheduledEnvelope(schedule, dedupeKey, s.now().UTC())); err != nil {
		deliveryRecord.Status, deliveryRecord.ProviderStatus, deliveryRecord.Error, deliveryRecord.RetryState = "failed", "failed", err.Error(), retryState(err)
	}
	if deliveryRecord.Status == "sent" && deliveryRecord.SentAt.IsZero() {
		deliveryRecord.SentAt = s.now().UTC()
	}
	if deliveryRecord.Status == "failed" && ctx.Err() != nil {
		// Process shutdown or job timeout leaves the claim recoverable after the
		// lease instead of turning infrastructure cancellation into a terminal
		// business failure.
		return deliveryRecord
	}
	deliveryRecord = s.store.SaveReminderDelivery(deliveryRecord)
	s.rearm(string(schedule.ID), deliveryRecord)
	return deliveryRecord
}

func scheduledEnvelope(schedule app.MessageSchedule, dedupeKey string, createdAt time.Time) app.MessageEnvelope {
	return app.MessageEnvelope{
		SchemaVersion: app.MessageEnvelopeSchemaVersion,
		ID:            "env_" + string(schedule.ID) + "_" + fmt.Sprint(schedule.DueTime.UTC().Unix()), IdempotencyKey: dedupeKey,
		CorrelationID: schedule.SessionID, CausationID: schedule.RunID,
		Source:  app.MessageSourceContext{Kind: app.MessageSourceTimer, Adapter: "timer", ScheduleID: schedule.ID},
		OwnerID: schedule.Spec.OwnerID, ActorID: schedule.Spec.ActorID, Content: schedule.Spec.Payload.Content,
		ReturnRoute: schedule.Spec.ReturnRoute, Authorization: schedule.Spec.Authorization, CreatedAt: createdAt,
	}
}

func (s *Scheduler) rearm(reminderID string, deliveryRecord app.ReminderDelivery) {
	reminder, ok := s.store.GetReminder(reminderID)
	if !ok {
		return
	}
	now := s.now().UTC()
	switch {
	case deliveryRecord.Status == "sent" && strings.TrimSpace(reminder.Recurrence) != "":
		next, ok := nextOccurrence(reminder, now)
		if !ok {
			return
		}
		reminder.Status, reminder.DueTime, reminder.DeliveryAttempt, reminder.LastError = "pending", next, 0, ""
	case deliveryRecord.Status == "failed" && deliveryRecord.RetryState == "retryable":
		reminder.Status = "pending"
		reminder.DueTime = now.Add(retryBackoff(deliveryRecord.Attempt))
	default:
		return
	}
	reminder.UpdatedAt = now
	s.store.SaveReminder(reminder)
}

func retryBackoff(attempt int) time.Duration {
	delay := retryBaseDelay
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay >= retryMaxDelay {
			return retryMaxDelay
		}
	}
	return delay
}

type retryable interface{ RetryState() string }

func retryState(err error) string {
	if err == nil {
		return "blocked"
	}
	var state retryable
	if errors.As(err, &state) && state.RetryState() != "" {
		return state.RetryState()
	}
	return "blocked"
}
