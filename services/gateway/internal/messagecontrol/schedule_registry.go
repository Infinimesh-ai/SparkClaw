package messagecontrol

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type scheduleStore interface {
	SaveReminder(app.Reminder) app.Reminder
	UpdatePendingReminder(app.Reminder, time.Time) (app.Reminder, error)
	GetReminder(string) (app.Reminder, bool)
	ListReminders(app.ReminderFilter) []app.Reminder
	ClaimDueReminders(time.Time, time.Time, int) []app.Reminder
	GetSession(string) (app.Session, bool)
	GetNotificationBinding(string) (app.NotificationBinding, bool)
	GetExternalChatSession(string) (app.ExternalChatSession, bool)
	ListExternalChatSessions(string, string) []app.ExternalChatSession
}

type SchedulePatch struct {
	Content     *app.MessageContent
	DueTime     *time.Time
	Timezone    *string
	Recurrence  *string
	ReturnRoute *app.ReturnRoute
}

type ScheduleRegistry struct {
	store scheduleStore
}

func NewScheduleRegistry(st scheduleStore) *ScheduleRegistry {
	return &ScheduleRegistry{store: st}
}

func (r *ScheduleRegistry) Save(ctx context.Context, schedule app.MessageSchedule) (app.MessageSchedule, error) {
	if r == nil || r.store == nil {
		return app.MessageSchedule{}, errors.New("schedule registry is unavailable")
	}
	reminder, err := r.reminderForSchedule(ctx, schedule)
	if err != nil {
		return app.MessageSchedule{}, err
	}
	saved, ok := r.fromReminder(r.store.SaveReminder(reminder))
	if !ok {
		return app.MessageSchedule{}, errors.New("saved schedule spec is unavailable")
	}
	return saved, nil
}

func (r *ScheduleRegistry) UpdatePending(ctx context.Context, id app.ScheduleID, ownerID, actorID string, expectedUpdatedAt time.Time, patch SchedulePatch) (app.MessageSchedule, error) {
	schedule, err := r.pendingOwned(id, ownerID, actorID, expectedUpdatedAt)
	if err != nil {
		return app.MessageSchedule{}, err
	}
	if patch.Content != nil {
		schedule.Spec.Payload.Content = *patch.Content
	}
	if patch.DueTime != nil {
		schedule.DueTime = patch.DueTime.UTC()
	}
	if patch.Timezone != nil {
		schedule.Timezone = strings.TrimSpace(*patch.Timezone)
	}
	if patch.Recurrence != nil {
		schedule.Recurrence = strings.TrimSpace(*patch.Recurrence)
	}
	if patch.ReturnRoute != nil {
		schedule.Spec.ReturnRoute = *patch.ReturnRoute
	}
	schedule.UpdatedAt = nextUpdateTime(expectedUpdatedAt)
	reminder, err := r.reminderForSchedule(ctx, schedule)
	if err != nil {
		return app.MessageSchedule{}, err
	}
	updated, err := r.store.UpdatePendingReminder(reminder, expectedUpdatedAt.UTC())
	if err != nil {
		if errors.Is(err, store.ErrReminderConflict) {
			return app.MessageSchedule{}, store.ErrReminderConflict
		}
		return app.MessageSchedule{}, err
	}
	result, ok := r.fromReminder(updated)
	if !ok {
		return app.MessageSchedule{}, errors.New("updated schedule spec is unavailable")
	}
	return result, nil
}

func (r *ScheduleRegistry) CancelPending(ctx context.Context, id app.ScheduleID, ownerID, actorID string, expectedUpdatedAt time.Time) (app.MessageSchedule, error) {
	schedule, err := r.pendingOwned(id, ownerID, actorID, expectedUpdatedAt)
	if err != nil {
		return app.MessageSchedule{}, err
	}
	now := nextUpdateTime(expectedUpdatedAt)
	schedule.Status = "canceled"
	schedule.UpdatedAt = now
	schedule.CanceledAt = &now
	reminder, err := r.reminderForSchedule(ctx, schedule)
	if err != nil {
		return app.MessageSchedule{}, err
	}
	updated, err := r.store.UpdatePendingReminder(reminder, expectedUpdatedAt.UTC())
	if err != nil {
		if errors.Is(err, store.ErrReminderConflict) {
			return app.MessageSchedule{}, store.ErrReminderConflict
		}
		return app.MessageSchedule{}, err
	}
	result, ok := r.fromReminder(updated)
	if !ok {
		return app.MessageSchedule{}, errors.New("canceled schedule spec is unavailable")
	}
	return result, nil
}

func (r *ScheduleRegistry) pendingOwned(id app.ScheduleID, ownerID, actorID string, expectedUpdatedAt time.Time) (app.MessageSchedule, error) {
	if r == nil || r.store == nil {
		return app.MessageSchedule{}, errors.New("schedule registry is unavailable")
	}
	schedule, ok := r.Get(context.Background(), id)
	if !ok || schedule.Spec.OwnerID != strings.TrimSpace(ownerID) || schedule.Spec.ActorID != strings.TrimSpace(actorID) {
		return app.MessageSchedule{}, errors.New("schedule not found")
	}
	if schedule.Status != "pending" {
		return app.MessageSchedule{}, fmt.Errorf("only pending schedules can be changed, current status is %q", schedule.Status)
	}
	if expectedUpdatedAt.IsZero() || !schedule.UpdatedAt.Equal(expectedUpdatedAt.UTC()) {
		return app.MessageSchedule{}, store.ErrReminderConflict
	}
	return schedule, nil
}

func (r *ScheduleRegistry) reminderForSchedule(ctx context.Context, schedule app.MessageSchedule) (app.Reminder, error) {
	if err := validateSchedule(schedule); err != nil {
		return app.Reminder{}, err
	}
	if schedule.Spec.ReturnRoute.Mode != app.ReturnNowhere {
		endpoint, deliver, err := NewReturnRouteResolver(NewEndpointRegistry(r.store)).Resolve(ctx, schedule.Spec.ReturnRoute)
		if err != nil || !deliver {
			return app.Reminder{}, firstError(err, errors.New("schedule return endpoint is unavailable"))
		}
		if endpoint.OwnerID != schedule.Spec.OwnerID || schedule.Spec.Authorization.PrincipalID != schedule.Spec.OwnerID {
			return app.Reminder{}, errors.New("schedule return endpoint does not belong to the authorized owner")
		}
	}
	reminder := app.Reminder{}
	if existing, ok := r.store.GetReminder(string(schedule.ID)); ok {
		reminder = existing
	}
	reminder.ID = string(schedule.ID)
	reminder.SessionID = schedule.SessionID
	reminder.RunID = schedule.RunID
	reminder.Text = firstText(schedule.Spec.Payload.Content)
	reminder.TextSummary = summarizeText(reminder.Text)
	reminder.DueTime = schedule.DueTime.UTC()
	reminder.Timezone = schedule.Timezone
	reminder.Recurrence = schedule.Recurrence
	reminder.DedupeKey = schedule.DedupeKey
	reminder.Status = schedule.Status
	reminder.DeliveryAttempt = schedule.DeliveryAttempt
	reminder.CreatedAt = schedule.CreatedAt
	reminder.UpdatedAt = schedule.UpdatedAt
	reminder.SentAt = schedule.SentAt
	reminder.CanceledAt = schedule.CanceledAt
	spec := schedule.Spec
	reminder.ScheduleSpec = &spec
	r.applyTargetProjection(&reminder, spec.ReturnRoute)
	return reminder, nil
}

func (r *ScheduleRegistry) Get(_ context.Context, id app.ScheduleID) (app.MessageSchedule, bool) {
	if r == nil || r.store == nil {
		return app.MessageSchedule{}, false
	}
	reminder, ok := r.store.GetReminder(string(id))
	if !ok {
		return app.MessageSchedule{}, false
	}
	return r.fromReminder(reminder)
}

func (r *ScheduleRegistry) List(_ context.Context, filter app.ReminderFilter) []app.MessageSchedule {
	if r == nil || r.store == nil {
		return nil
	}
	reminders := r.store.ListReminders(filter)
	out := make([]app.MessageSchedule, 0, len(reminders))
	for _, reminder := range reminders {
		if schedule, ok := r.fromReminder(reminder); ok {
			out = append(out, schedule)
		}
	}
	return out
}

func (r *ScheduleRegistry) ClaimDue(_ context.Context, now, staleBefore time.Time, limit int) []app.MessageSchedule {
	if r == nil || r.store == nil {
		return nil
	}
	reminders := r.store.ClaimDueReminders(now.UTC(), staleBefore.UTC(), limit)
	out := make([]app.MessageSchedule, 0, len(reminders))
	for _, reminder := range reminders {
		if schedule, ok := r.fromReminder(reminder); ok {
			out = append(out, schedule)
		}
	}
	return out
}

func (r *ScheduleRegistry) fromReminder(reminder app.Reminder) (app.MessageSchedule, bool) {
	if reminder.ScheduleSpec == nil || reminder.ScheduleSpec.SchemaVersion != app.ScheduleSpecSchemaVersion {
		return app.MessageSchedule{}, false
	}
	dedupeKey := strings.TrimSpace(reminder.DedupeKey)
	if dedupeKey == "" {
		dedupeKey = "reminder:" + reminder.ID
	}
	spec := *reminder.ScheduleSpec
	return app.MessageSchedule{
		ID: app.ScheduleID(reminder.ID), SessionID: reminder.SessionID, RunID: reminder.RunID, Spec: spec,
		DueTime: reminder.DueTime, Timezone: reminder.Timezone, Recurrence: reminder.Recurrence,
		DedupeKey: dedupeKey, Status: reminder.Status, DeliveryAttempt: reminder.DeliveryAttempt,
		CreatedAt: reminder.CreatedAt, UpdatedAt: reminder.UpdatedAt, SentAt: reminder.SentAt, CanceledAt: reminder.CanceledAt,
	}, true
}

func (r *ScheduleRegistry) applyTargetProjection(reminder *app.Reminder, route app.ReturnRoute) {
	endpointID := route.EndpointID
	if route.Mode == app.ReturnToSource {
		endpointID = route.SourceEndpointID
	}
	if strings.HasPrefix(string(endpointID), "session:") {
		reminder.Channel = "web"
		reminder.BindingID, reminder.Recipient, reminder.RecipientBinding, reminder.CredentialRef, reminder.BaseURL = "", "", "", "", ""
		return
	}
	if endpoint, err := NewEndpointRegistry(r.store).Get(context.Background(), endpointID); err == nil && endpoint.Kind == app.EndpointKindThirdPartyDevice {
		reminder.Channel, reminder.BindingID = endpoint.ProviderKey, endpoint.BindingRef
		reminder.Recipient = firstValue(endpoint.Address, reminder.Recipient)
		reminder.RecipientBinding = firstValue(endpoint.ThreadRef, endpoint.ContextRef, reminder.RecipientBinding)
		if binding, ok := r.store.GetNotificationBinding(endpoint.BindingRef); ok {
			reminder.CredentialRef, reminder.BaseURL = binding.CredentialRef, binding.BaseURL
		}
		return
	}
	binding, ok := r.store.GetNotificationBinding(string(endpointID))
	if !ok {
		return
	}
	reminder.Channel = strings.ToLower(strings.TrimSpace(binding.Channel))
	reminder.BindingID = binding.ID
	reminder.Recipient = firstValue(binding.ExternalChatID, binding.ExternalUserID, reminder.Recipient)
	reminder.RecipientBinding = firstValue(binding.ExternalThreadID, binding.ContextToken, reminder.RecipientBinding)
	reminder.CredentialRef = binding.CredentialRef
	reminder.BaseURL = binding.BaseURL
}

func validateSchedule(schedule app.MessageSchedule) error {
	if schedule.ID == "" || schedule.DueTime.IsZero() || strings.TrimSpace(schedule.DedupeKey) == "" {
		return errors.New("schedule identity, due time, and dedupe key are required")
	}
	if schedule.Spec.SchemaVersion != app.ScheduleSpecSchemaVersion {
		return fmt.Errorf("unsupported schedule spec version %d", schedule.Spec.SchemaVersion)
	}
	if strings.TrimSpace(schedule.SessionID) == "" {
		return errors.New("schedule requires a session id")
	}
	if len(schedule.Spec.Payload.Content.Parts) == 0 {
		return errors.New("schedule payload content is required")
	}
	if strings.TrimSpace(schedule.Spec.OwnerID) == "" || strings.TrimSpace(schedule.Spec.ActorID) == "" || strings.TrimSpace(schedule.Spec.Authorization.PrincipalID) == "" || schedule.Spec.ActorID != schedule.Spec.Authorization.PrincipalID {
		return errors.New("schedule owner, actor, and authorization principal are required")
	}
	return nil
}

func firstError(primary, fallback error) error {
	if primary != nil {
		return primary
	}
	return fallback
}

func ScheduleSpecVersion() int { return app.ScheduleSpecSchemaVersion }

func textContent(prefix, text string) app.MessageContent {
	return app.MessageContent{Parts: []app.MessagePart{{ID: prefix + ":text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: text}}}
}

func firstText(content app.MessageContent) string {
	for _, part := range content.Parts {
		if part.Kind == app.MessagePartText {
			return part.Text
		}
	}
	return "Scheduled message"
}

func summarizeText(value string) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > 80 {
		return string(runes[:80]) + "..."
	}
	if len(runes) == 0 {
		return "Scheduled message"
	}
	return string(runes)
}

func firstValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func nextUpdateTime(expected time.Time) time.Time {
	now := time.Now().UTC()
	if !now.After(expected.UTC()) {
		return expected.UTC().Add(time.Nanosecond)
	}
	return now
}
