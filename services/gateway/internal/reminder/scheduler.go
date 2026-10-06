package reminder

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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

	// DefaultMaxDeliveryAttempts backstops a non-positive configured cap so a
	// permanently retryable publish failure can never reschedule forever.
	DefaultMaxDeliveryAttempts = 8
)

// MessagePublisher submits due owner requests to the ordinary Message Runtime.
type MessagePublisher interface {
	Publish(context.Context, app.MessageEnvelope) error
}

type Repository interface {
	store.ScheduleRepository
}

type Scheduler struct {
	store               Repository
	schedules           *messagecontrol.ScheduleRegistry
	publisher           MessagePublisher
	now                 func() time.Time
	interval            time.Duration
	maxDeliveryAttempts int
	pollMu              sync.Mutex
	lastPoll            time.Time
	eligibleAfter       time.Time
	unavailable         bool
}

func NewMessageScheduler(st Repository, schedules *messagecontrol.ScheduleRegistry, publisher MessagePublisher, maxDeliveryAttempts int) *Scheduler {
	if maxDeliveryAttempts <= 0 {
		maxDeliveryAttempts = DefaultMaxDeliveryAttempts
	}
	return &Scheduler{
		store: st, schedules: schedules, publisher: publisher,
		now:                 time.Now,
		interval:            pollInterval,
		maxDeliveryAttempts: maxDeliveryAttempts,
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
				if _, err := s.process(jobCtx, schedule); err != nil {
					slog.Warn("scheduled message persistence unavailable", "schedule_id", schedule.ID, "code", store.StoreErrorCodeOf(err))
				}
				cancel()
			}
		}()
	}
	var pending []app.MessageSchedule
	poll := func() bool {
		if len(pending) != 0 {
			s.observePoll()
			return true
		}
		schedules, err := s.claimDue(ctx)
		if err != nil {
			slog.Warn("scheduled message claim unavailable", "code", store.StoreErrorCodeOf(err))
			if ctx.Err() != nil {
				return false
			}
		}
		pending = schedules
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
		var ready chan<- app.MessageSchedule
		var next app.MessageSchedule
		if len(pending) != 0 {
			ready, next = jobs, pending[0]
		}
		select {
		case <-ctx.Done():
			return
		case ready <- next:
			pending = pending[1:]
		case <-ticker.C:
			if !poll() {
				return
			}
		}
	}
}

// Tick runs the same availability and occurrence rules synchronously for
// deterministic tests. The production ticker dispatches claimed work to workers.
func (s *Scheduler) Tick(ctx context.Context) ([]app.ReminderDelivery, error) {
	due, claimErr := s.claimDue(ctx)
	if claimErr != nil && len(due) == 0 {
		return nil, claimErr
	}
	deliveries := make([]app.ReminderDelivery, 0, len(due))
	for _, schedule := range due {
		delivery, err := s.process(ctx, schedule)
		if err != nil {
			return deliveries, err
		}
		deliveries = append(deliveries, delivery)
	}
	return deliveries, claimErr
}

func (s *Scheduler) process(ctx context.Context, schedule app.MessageSchedule) (app.ReminderDelivery, error) {
	attempt := schedule.DeliveryAttempt + 1
	dedupeKey := schedule.DedupeKey
	if strings.TrimSpace(schedule.Recurrence) != "" && dedupeKey != "" {
		dedupeKey = fmt.Sprintf("%s@%d", dedupeKey, schedule.DueTime.UTC().Unix())
	}
	deliveryRecord := app.ReminderDelivery{
		ID: app.NewID("rdel"), ReminderID: string(schedule.ID), Status: "sent", RetryState: "none", Attempt: attempt, CreatedAt: s.now().UTC(),
	}
	if schedule.Spec.WorkbenchOwned {
		deliveryRecord.ID = workbenchOccurrenceID(schedule)
		dedupeKey = deliveryRecord.ID
	}
	deliveryRecord.Provider = "message-runtime"
	deliveryRecord.ProviderStatus = "published"
	if s.publisher == nil {
		deliveryRecord.Status, deliveryRecord.ProviderStatus, deliveryRecord.Error, deliveryRecord.RetryState = "failed", "failed", "scheduled message publisher is unavailable", "blocked"
	} else if err := s.publisher.Publish(ctx, scheduledEnvelope(schedule, dedupeKey, s.now().UTC())); err != nil {
		deliveryRecord.Status, deliveryRecord.ProviderStatus, deliveryRecord.Error, deliveryRecord.RetryState = "failed", "failed", err.Error(), retryState(err)
	}
	if schedule.Spec.WorkbenchOwned && deliveryRecord.Status == "failed" {
		// Publication may have admitted an execution before its response was
		// lost. Preserve the original request ID for reconciliation only.
		deliveryRecord.Status, deliveryRecord.ProviderStatus, deliveryRecord.RetryState = "unknown", "unknown", "reconcile"
	}
	if deliveryRecord.Status == "sent" && deliveryRecord.SentAt.IsZero() {
		deliveryRecord.SentAt = s.now().UTC()
	}
	if deliveryRecord.Status == "failed" && ctx.Err() != nil {
		// Process shutdown or job timeout leaves the claim recoverable after the
		// lease instead of turning infrastructure cancellation into a terminal
		// business failure.
		return deliveryRecord, nil
	}
	deliveryRecord, err := s.store.SaveReminderDelivery(ctx, deliveryRecord)
	if err != nil {
		return deliveryRecord, err
	}
	if schedule.Spec.WorkbenchOwned {
		return deliveryRecord, nil
	}
	if err := s.rearm(ctx, string(schedule.ID), deliveryRecord); err != nil {
		return deliveryRecord, err
	}
	return deliveryRecord, nil
}

func scheduledEnvelope(schedule app.MessageSchedule, dedupeKey string, createdAt time.Time) app.MessageEnvelope {
	id := "env_" + string(schedule.ID) + "_" + fmt.Sprint(schedule.DueTime.UTC().Unix())
	if schedule.Spec.WorkbenchOwned {
		id = workbenchOccurrenceID(schedule)
	}
	return app.MessageEnvelope{
		SchemaVersion: app.MessageEnvelopeSchemaVersion,
		ID:            id, IdempotencyKey: dedupeKey,
		CorrelationID: schedule.SessionID, CausationID: schedule.RunID,
		Source:  app.MessageSourceContext{Kind: app.MessageSourceTimer, Adapter: "timer", ScheduleID: schedule.ID},
		OwnerID: schedule.Spec.OwnerID, ActorID: schedule.Spec.ActorID, Content: schedule.Spec.Payload.Content,
		ReturnRoute: schedule.Spec.ReturnRoute, Authorization: schedule.Spec.Authorization, CreatedAt: createdAt,
	}
}

func (s *Scheduler) rearm(ctx context.Context, reminderID string, deliveryRecord app.ReminderDelivery) error {
	reminder, ok, err := s.store.GetReminder(ctx, reminderID)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	now := s.now().UTC()
	switch {
	case deliveryRecord.Status == "sent" && strings.TrimSpace(reminder.Recurrence) != "":
		next, ok := nextOccurrence(reminder, now)
		if !ok {
			return nil
		}
		reminder.Status, reminder.DueTime, reminder.DeliveryAttempt, reminder.LastError = "pending", next, 0, ""
	case deliveryRecord.Status == "failed" && deliveryRecord.RetryState == "retryable":
		if deliveryRecord.Attempt >= s.maxDeliveryAttempts {
			// Retries are exhausted: keep the terminal "failed" status the
			// delivery write already recorded instead of re-arming forever.
			return nil
		}
		reminder.Status = "pending"
		reminder.DueTime = now.Add(retryBackoff(deliveryRecord.Attempt))
	default:
		return nil
	}
	reminder.UpdatedAt = now
	_, err = s.store.SaveReminder(ctx, reminder)
	return err
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
