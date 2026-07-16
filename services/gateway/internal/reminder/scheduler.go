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
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/notification"
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

type DeliveryGateway interface {
	Deliver(context.Context, app.DeliveryRequest) (app.DeliveryReceipt, error)
}

// MessagePublisher is the compatibility seam for scheduled request payloads.
// The composition layer can replace it with the Workflow message queue without
// changing scheduling or delivery semantics.
type MessagePublisher interface {
	Publish(context.Context, app.MessageEnvelope) error
}

type Scheduler struct {
	store     store.Store
	schedules *messagecontrol.ScheduleRegistry
	routes    *messagecontrol.ReturnRouteResolver
	gateway   DeliveryGateway
	publisher MessagePublisher
	now       func() time.Time
	interval  time.Duration
}

// NewScheduler preserves the original notification.Router constructor for
// downstream tests and callers. Production composition uses NewMessageScheduler.
func NewScheduler(st store.Store, router notification.Router) *Scheduler {
	endpoints := messagecontrol.NewEndpointRegistry(st)
	return NewMessageScheduler(
		st,
		messagecontrol.NewScheduleRegistry(st),
		messagecontrol.NewReturnRouteResolver(endpoints),
		notificationGateway{store: st, router: router},
		nil,
	)
}

func NewMessageScheduler(st store.Store, schedules *messagecontrol.ScheduleRegistry, routes *messagecontrol.ReturnRouteResolver, gateway DeliveryGateway, publisher MessagePublisher) *Scheduler {
	return &Scheduler{
		store: st, schedules: schedules, routes: routes, gateway: gateway, publisher: publisher,
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
	if schedule.Spec.Payload.Mode == app.SchedulePayloadRequest {
		deliveryRecord.Provider = "message-runtime"
		deliveryRecord.ProviderStatus = "published"
		if s.publisher == nil {
			deliveryRecord.Status, deliveryRecord.ProviderStatus, deliveryRecord.Error, deliveryRecord.RetryState = "failed", "failed", "scheduled request publisher is unavailable", "blocked"
		} else if err := s.publisher.Publish(ctx, scheduledEnvelope(schedule, dedupeKey, s.now().UTC())); err != nil {
			deliveryRecord.Status, deliveryRecord.ProviderStatus, deliveryRecord.Error, deliveryRecord.RetryState = "failed", "failed", err.Error(), retryState(err)
		}
	} else {
		deliveryRecord = s.deliverLiteral(ctx, schedule, dedupeKey, deliveryRecord)
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

func (s *Scheduler) deliverLiteral(ctx context.Context, schedule app.MessageSchedule, dedupeKey string, record app.ReminderDelivery) app.ReminderDelivery {
	endpoint, deliverResult, err := s.routes.Resolve(ctx, schedule.Spec.ReturnRoute)
	if err != nil || !deliverResult {
		if err == nil {
			err = errors.New("literal schedule requires a return endpoint")
		}
		record.Status, record.ProviderStatus, record.Error, record.RetryState = "failed", "failed", err.Error(), "blocked"
		return record
	}
	if endpoint.Kind == app.EndpointKindWeb {
		record.Channel = "web"
	} else {
		record.Channel = endpoint.ProviderKey
	}
	record.Recipient = endpoint.BindingRef
	request := app.DeliveryRequest{
		SchemaVersion: app.DeliveryRequestSchemaVersion,
		ID:            app.DeliveryID(record.ID), IdempotencyKey: dedupeKey, ResultID: string(schedule.ID), Target: endpoint.ID,
		OwnerID: schedule.Spec.OwnerID, Authorization: schedule.Spec.Authorization,
		Content: schedule.Spec.Payload.Content, CreatedAt: s.now().UTC(),
	}
	receipt, err := s.gateway.Deliver(ctx, request)
	if receipt.DeliveryID != "" {
		record.ID = string(receipt.DeliveryID)
	}
	record.Provider = receipt.ProviderRef
	record.ProviderStatus = string(receipt.Status)
	record.Error = receipt.Error
	record.RetryState = receipt.RetryState
	if err != nil || receipt.Status != app.DeliverySucceeded {
		record.Status = "failed"
		if record.Error == "" && err != nil {
			record.Error = err.Error()
		}
		if record.RetryState == "" {
			record.RetryState = retryState(err)
		}
		return record
	}
	record.Status = "sent"
	record.SentAt = valueTime(receipt.DeliveredAt, s.now().UTC())
	return record
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

func valueTime(value *time.Time, fallback time.Time) time.Time {
	if value != nil && !value.IsZero() {
		return value.UTC()
	}
	return fallback
}

type notificationGateway struct {
	store  store.Store
	router notification.Router
}

func (g notificationGateway) Deliver(ctx context.Context, request app.DeliveryRequest) (app.DeliveryReceipt, error) {
	endpointRegistry := messagecontrol.NewEndpointRegistry(g.store)
	endpoint, err := endpointRegistry.Get(ctx, request.Target)
	if err != nil {
		return app.DeliveryReceipt{DeliveryID: request.ID, EndpointID: request.Target, Status: app.DeliveryFailed, Error: err.Error(), RetryState: "blocked", AttemptedAt: time.Now().UTC()}, err
	}
	texts := make([]string, 0, len(request.Content.Parts))
	for _, part := range request.Content.Parts {
		if part.Kind != app.MessagePartText {
			err := fmt.Errorf("legacy notification gateway does not support %q", part.Kind)
			return app.DeliveryReceipt{DeliveryID: request.ID, EndpointID: endpoint.ID, Status: app.DeliveryFailed, Error: err.Error(), RetryState: "blocked", AttemptedAt: time.Now().UTC()}, err
		}
		texts = append(texts, part.Text)
	}
	notice := notification.Notification{Channel: "web", MessageText: strings.Join(texts, "\n"), DedupeKey: request.IdempotencyKey}
	if endpoint.Kind == app.EndpointKindThirdPartyDevice {
		if binding, ok := g.store.GetNotificationBinding(endpoint.BindingRef); ok {
			notice.Channel, notice.BindingID = endpoint.ProviderKey, binding.ID
			notice.Recipient = firstValue(binding.ExternalChatID, binding.ExternalUserID)
			notice.RecipientBinding = firstValue(binding.ExternalThreadID, binding.ContextToken)
			notice.CredentialRef, notice.BaseURL = binding.CredentialRef, binding.BaseURL
		} else if reminder, ok := g.store.GetReminder(request.ResultID); ok {
			notice.Channel, notice.BindingID = endpoint.ProviderKey, reminder.BindingID
			notice.Recipient, notice.RecipientBinding = reminder.Recipient, reminder.RecipientBinding
			notice.CredentialRef, notice.BaseURL = reminder.CredentialRef, reminder.BaseURL
		} else {
			return app.DeliveryReceipt{}, errors.New("notification binding is unavailable")
		}
	}
	result, err := g.router.Send(ctx, notice)
	receipt := app.DeliveryReceipt{
		DeliveryID: app.DeliveryID(result.DeliveryID), EndpointID: endpoint.ID, ProviderRef: result.Provider,
		Status: app.DeliverySucceeded, Error: result.Error, RetryState: result.RetryState, AttemptedAt: time.Now().UTC(),
	}
	if result.SentAt.IsZero() {
		result.SentAt = time.Now().UTC()
	}
	if err != nil || result.Status == "failed" {
		receipt.Status = app.DeliveryFailed
		return receipt, err
	}
	receipt.DeliveredAt = &result.SentAt
	return receipt, nil
}

func firstValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
