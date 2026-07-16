package messagecontrol

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type scheduleStore interface {
	SaveReminder(app.Reminder) app.Reminder
	GetReminder(string) (app.Reminder, bool)
	ListReminders(app.ReminderFilter) []app.Reminder
	ClaimDueReminders(time.Time, time.Time, int) []app.Reminder
	GetSession(string) (app.Session, bool)
	GetNotificationBinding(string) (app.NotificationBinding, bool)
	GetExternalChatSession(string) (app.ExternalChatSession, bool)
}

type ScheduleRegistry struct {
	store scheduleStore
}

func NewScheduleRegistry(st scheduleStore) *ScheduleRegistry {
	return &ScheduleRegistry{store: st}
}

func (r *ScheduleRegistry) Save(_ context.Context, schedule app.MessageSchedule) (app.MessageSchedule, error) {
	if r == nil || r.store == nil {
		return app.MessageSchedule{}, errors.New("schedule registry is unavailable")
	}
	if err := validateSchedule(schedule); err != nil {
		return app.MessageSchedule{}, err
	}
	if schedule.Spec.ReturnRoute.Mode != app.ReturnNowhere {
		endpoint, deliver, err := NewReturnRouteResolver(NewEndpointRegistry(r.store)).Resolve(context.Background(), schedule.Spec.ReturnRoute)
		if err != nil || !deliver {
			return app.MessageSchedule{}, firstError(err, errors.New("schedule return endpoint is unavailable"))
		}
		if endpoint.OwnerID != schedule.Spec.OwnerID || schedule.Spec.Authorization.PrincipalID != schedule.Spec.OwnerID {
			return app.MessageSchedule{}, errors.New("schedule return endpoint does not belong to the authorized owner")
		}
	} else if schedule.Spec.Payload.Mode == app.SchedulePayloadLiteral {
		return app.MessageSchedule{}, errors.New("literal schedule requires a return endpoint")
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
	r.applyLegacyTarget(&reminder, spec.ReturnRoute)
	return r.fromReminder(r.store.SaveReminder(reminder)), nil
}

func (r *ScheduleRegistry) Get(_ context.Context, id app.ScheduleID) (app.MessageSchedule, bool) {
	if r == nil || r.store == nil {
		return app.MessageSchedule{}, false
	}
	reminder, ok := r.store.GetReminder(string(id))
	if !ok {
		return app.MessageSchedule{}, false
	}
	return r.fromReminder(reminder), true
}

func (r *ScheduleRegistry) List(_ context.Context, filter app.ReminderFilter) []app.MessageSchedule {
	if r == nil || r.store == nil {
		return nil
	}
	reminders := r.store.ListReminders(filter)
	out := make([]app.MessageSchedule, 0, len(reminders))
	for _, reminder := range reminders {
		out = append(out, r.fromReminder(reminder))
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
		out = append(out, r.fromReminder(reminder))
	}
	return out
}

func (r *ScheduleRegistry) fromReminder(reminder app.Reminder) app.MessageSchedule {
	spec := app.ScheduleSpec{}
	if reminder.ScheduleSpec != nil {
		spec = *reminder.ScheduleSpec
	} else {
		endpointID := BindingEndpointID(reminder.BindingID)
		ownerID := ""
		if binding, ok := r.store.GetNotificationBinding(reminder.BindingID); ok {
			ownerID = strings.TrimSpace(binding.OwnerID)
		}
		if endpointID == "" && reminder.SessionID != "" {
			endpointID = WebEndpointID(reminder.SessionID)
			if session, ok := r.store.GetSession(reminder.SessionID); ok {
				ownerID = strings.TrimSpace(session.OwnerID)
			}
		}
		if endpointID == "" || (strings.ToLower(strings.TrimSpace(reminder.Channel)) != "web" && reminder.BindingID == "") {
			endpointID = LegacyScheduleEndpointID(reminder.ID)
		}
		if ownerID == "" {
			ownerID = app.DefaultOwnerID
		}
		spec = app.ScheduleSpec{
			SchemaVersion: ScheduleSpecVersion(), OwnerID: ownerID, ActorID: ownerID,
			Payload:                app.SchedulePayload{Mode: app.SchedulePayloadLiteral, Content: textContent(string(reminder.ID), reminder.Text)},
			ReturnRoute:            app.ReturnRoute{Mode: app.ReturnToEndpoint, EndpointID: endpointID},
			Authorization:          app.MessageAuthorization{PrincipalID: ownerID},
			ExpectedCapabilityPath: []app.CapabilityID{"message", "message.send"},
		}
	}
	return app.MessageSchedule{
		ID: app.ScheduleID(reminder.ID), SessionID: reminder.SessionID, RunID: reminder.RunID, Spec: spec,
		DueTime: reminder.DueTime, Timezone: reminder.Timezone, Recurrence: reminder.Recurrence,
		DedupeKey: reminder.DedupeKey, Status: reminder.Status, DeliveryAttempt: reminder.DeliveryAttempt,
		CreatedAt: reminder.CreatedAt, UpdatedAt: reminder.UpdatedAt, SentAt: reminder.SentAt, CanceledAt: reminder.CanceledAt,
	}
}

func (r *ScheduleRegistry) applyLegacyTarget(reminder *app.Reminder, route app.ReturnRoute) {
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
	if schedule.Spec.Payload.Mode != app.SchedulePayloadLiteral && schedule.Spec.Payload.Mode != app.SchedulePayloadRequest {
		return fmt.Errorf("unsupported schedule payload mode %q", schedule.Spec.Payload.Mode)
	}
	if schedule.Spec.Payload.Mode == app.SchedulePayloadRequest && strings.TrimSpace(schedule.SessionID) == "" {
		return errors.New("request schedule requires a session id")
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
