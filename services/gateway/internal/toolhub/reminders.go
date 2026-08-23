package toolhub

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/messagecontrol"
)

// scheduleRegistry builds the schedule registry with the connector-gated
// endpoint registry so schedule admission honors the owner's opt-out; with no
// gate wired, third-party return routes fail closed.
func (h *ToolHub) scheduleRegistry() *messagecontrol.ScheduleRegistry {
	endpoints := messagecontrol.NewEndpointRegistry(h.store)
	if h.connectorGate != nil {
		endpoints.WithChannelEnabled(h.connectorGate)
	}
	return messagecontrol.NewScheduleRegistry(h.store).WithEndpoints(endpoints)
}

func (h *ToolHub) remindersCreate(ctx context.Context, args map[string]any, sessionID, runID string) (Result, error) {
	text := strings.TrimSpace(stringArg(args, "text", ""))
	if text == "" {
		return Result{}, errors.New("text cannot be empty")
	}
	timezone := strings.TrimSpace(stringArg(args, "timezone", ""))
	if timezone == "" {
		timezone = "Asia/Shanghai"
	}
	dueTime, err := parseReminderTimeInTimezone(stringArg(args, "due_time", ""), timezone)
	if err != nil {
		return Result{}, err
	}
	channel := strings.TrimSpace(stringArg(args, "channel", ""))
	if channel == "" {
		chatSession, ok, err := h.store.FindExternalChatSessionByLinkedSessionID(ctx, sessionID)
		if err != nil {
			return Result{}, err
		}
		if ok {
			channel = chatSession.Channel
			if strings.TrimSpace(channel) == "" {
				if binding, bindingOK, err := h.store.GetNotificationBinding(ctx, strings.TrimSpace(chatSession.BindingID)); err != nil {
					return Result{}, err
				} else if bindingOK {
					channel = binding.Channel
				}
			}
		}
		if strings.TrimSpace(channel) == "" {
			channel = "web"
		}
	}
	channel = normalizeReminderChannel(channel)
	recipient := strings.TrimSpace(stringArg(args, "recipient", ""))
	recipientBinding := ""
	bindingID := ""
	credentialRef := ""
	baseURL := ""
	endpointID := app.EndpointID("session:" + sessionID)
	if channel != "web" {
		resolved, err := h.reminders.Resolve(ctx, channel, sessionID, recipient)
		if err != nil {
			return Result{}, err
		}
		recipient = resolved.Recipient
		recipientBinding = resolved.RecipientBinding
		bindingID = resolved.BindingID
		credentialRef = resolved.CredentialRef
		baseURL = resolved.BaseURL
		endpointID = resolved.EndpointID
	}
	ownerID, err := h.ownerIDForSession(ctx, sessionID)
	if err != nil {
		return Result{}, err
	}
	now := time.Now().UTC()
	reminder := app.Reminder{
		ID:               app.NewID("rem"),
		SessionID:        sessionID,
		RunID:            runID,
		Text:             text,
		TextSummary:      summarizeText(text, 80),
		DueTime:          dueTime.UTC(),
		Timezone:         timezone,
		Channel:          channel,
		Recipient:        recipient,
		RecipientBinding: recipientBinding,
		BindingID:        bindingID,
		CredentialRef:    credentialRef,
		BaseURL:          baseURL,
		Recurrence:       normalizeReminderRecurrence(stringArg(args, "recurrence", "")),
		DedupeKey:        strings.TrimSpace(stringArg(args, "dedupe_key", "")),
		Status:           "pending",
		CreatedAt:        now,
		UpdatedAt:        now,
		ScheduleSpec: &app.ScheduleSpec{
			SchemaVersion: app.ScheduleSpecSchemaVersion,
			OwnerID:       ownerID,
			ActorID:       ownerID,
			Payload: app.SchedulePayload{
				Content: app.MessageContent{Parts: []app.MessagePart{{ID: "schedule:text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: text}}},
			},
			ReturnRoute:   app.ReturnRoute{Mode: app.ReturnToEndpoint, EndpointID: endpointID},
			Authorization: app.MessageAuthorization{PrincipalID: ownerID},
		},
	}
	if reminder.DedupeKey == "" {
		reminder.DedupeKey = reminder.ID
	}
	schedule := app.MessageSchedule{
		ID: app.ScheduleID(reminder.ID), SessionID: reminder.SessionID, RunID: reminder.RunID, Spec: *reminder.ScheduleSpec,
		DueTime: reminder.DueTime, Timezone: reminder.Timezone, Recurrence: reminder.Recurrence, DedupeKey: reminder.DedupeKey,
		Status: reminder.Status, CreatedAt: reminder.CreatedAt, UpdatedAt: reminder.UpdatedAt,
	}
	if _, err := h.scheduleRegistry().Save(ctx, schedule); err != nil {
		return Result{}, err
	}
	reminder, ok, err := h.store.GetReminder(ctx, reminder.ID)
	if err != nil {
		return Result{}, err
	}
	if !ok {
		return Result{}, errors.New("saved reminder is unavailable")
	}
	return Result{Output: reminderToolOutput(reminder)}, nil
}

func normalizeReminderChannel(channel string) string {
	channel = strings.ToLower(strings.TrimSpace(channel))
	switch channel {
	case "vx", "wechat":
		return "weixin"
	case "tg":
		return "telegram"
	default:
		return channel
	}
}

func normalizeReminderRecurrence(value string) string {
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case "none", "one-time", "one_time", "once", "单次", "不重复":
		return ""
	default:
		return value
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (h *ToolHub) remindersList(ctx context.Context, args map[string]any, sessionID string) (Result, error) {
	filter := app.ReminderFilter{
		Status: strings.TrimSpace(stringArg(args, "status", "")),
		Limit:  intArg(args, "limit", 20),
	}
	if from := strings.TrimSpace(stringArg(args, "from_time", "")); from != "" {
		t, err := parseReminderTime(from)
		if err != nil {
			return Result{}, fmt.Errorf("from_time %w", err)
		}
		filter.From = &t
	}
	if to := strings.TrimSpace(stringArg(args, "to_time", "")); to != "" {
		t, err := parseReminderTime(to)
		if err != nil {
			return Result{}, fmt.Errorf("to_time %w", err)
		}
		filter.To = &t
	}
	reminders, err := h.store.ListReminders(ctx, filter)
	if err != nil {
		return Result{}, err
	}
	ownerID, err := h.ownerIDForSession(ctx, sessionID)
	if err != nil {
		return Result{}, err
	}
	items := make([]map[string]any, 0, len(reminders))
	for _, reminder := range reminders {
		if reminder.ScheduleSpec == nil || reminder.ScheduleSpec.SchemaVersion != app.ScheduleSpecSchemaVersion {
			continue
		}
		visible, err := h.sessionVisibleToOwner(ctx, reminder.SessionID, ownerID)
		if err != nil {
			return Result{}, err
		}
		if !visible {
			continue
		}
		items = append(items, reminderToolOutput(reminder))
	}
	return Result{Output: map[string]any{
		"reminders": items,
		"count":     len(items),
	}}, nil
}

func (h *ToolHub) remindersUpdate(ctx context.Context, args map[string]any, sessionID string) (Result, error) {
	id := strings.TrimSpace(stringArg(args, "reminder_id", ""))
	if id == "" {
		return Result{}, errors.New("reminder_id cannot be empty")
	}
	expectedUpdatedAt, err := parseExpectedUpdatedAt(args)
	if err != nil {
		return Result{}, err
	}
	registry := h.scheduleRegistry()
	schedule, ok, err := registry.Get(ctx, app.ScheduleID(id))
	if err != nil {
		return Result{}, err
	}
	if !ok {
		return Result{}, errors.New("reminder not found")
	}
	patch := messagecontrol.SchedulePatch{}
	if text, ok := optionalStringArg(args, "text"); ok {
		text = strings.TrimSpace(text)
		if text == "" {
			return Result{}, errors.New("text cannot be empty")
		}
		content := app.MessageContent{Parts: []app.MessagePart{{ID: "schedule:text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: text}}}
		patch.Content = &content
	}
	effectiveTimezone := schedule.Timezone
	if timezone, ok := optionalStringArg(args, "timezone"); ok {
		timezone = strings.TrimSpace(timezone)
		effectiveTimezone = timezone
		patch.Timezone = &timezone
	}
	if dueValue, ok := optionalStringArg(args, "due_time"); ok {
		parsed, err := parseReminderTimeInTimezone(dueValue, effectiveTimezone)
		if err != nil {
			return Result{}, err
		}
		due := parsed.UTC()
		patch.DueTime = &due
	}
	channelValue, channelChanged := optionalStringArg(args, "channel")
	recipientValue, recipientChanged := optionalStringArg(args, "recipient")
	if channelChanged || recipientChanged {
		reminder, reminderOK, err := h.store.GetReminder(ctx, id)
		if err != nil {
			return Result{}, err
		}
		if !reminderOK {
			return Result{}, errors.New("reminder not found")
		}
		channel := reminder.Channel
		if channelChanged {
			channel = normalizeReminderChannel(channelValue)
		}
		requestedRecipient := reminder.Recipient
		if recipientChanged {
			requestedRecipient = strings.TrimSpace(recipientValue)
		}
		endpointID := app.EndpointID("session:" + schedule.SessionID)
		if channel == "web" {
		} else {
			resolved, err := h.reminders.Resolve(ctx, channel, schedule.SessionID, requestedRecipient)
			if err != nil {
				return Result{}, err
			}
			endpointID = resolved.EndpointID
		}
		route := app.ReturnRoute{Mode: app.ReturnToEndpoint, EndpointID: endpointID}
		patch.ReturnRoute = &route
	}
	if recurrence, ok := optionalStringArg(args, "recurrence"); ok {
		recurrence = normalizeReminderRecurrence(recurrence)
		patch.Recurrence = &recurrence
	}
	ownerID, err := h.ownerIDForSession(ctx, sessionID)
	if err != nil {
		return Result{}, err
	}
	updated, err := registry.UpdatePending(ctx, app.ScheduleID(id), ownerID, ownerID, expectedUpdatedAt, patch)
	if err != nil {
		return Result{}, err
	}
	reminder, ok, err := h.store.GetReminder(ctx, string(updated.ID))
	if err != nil {
		return Result{}, err
	}
	if !ok {
		return Result{}, errors.New("updated reminder is unavailable")
	}
	return Result{Output: reminderToolOutput(reminder)}, nil
}

func (h *ToolHub) remindersCancel(ctx context.Context, args map[string]any, sessionID string) (Result, error) {
	id := strings.TrimSpace(stringArg(args, "reminder_id", ""))
	if id == "" {
		return Result{}, errors.New("reminder_id cannot be empty")
	}
	expectedUpdatedAt, err := parseExpectedUpdatedAt(args)
	if err != nil {
		return Result{}, err
	}
	ownerID, err := h.ownerIDForSession(ctx, sessionID)
	if err != nil {
		return Result{}, err
	}
	canceled, err := h.scheduleRegistry().CancelPending(ctx, app.ScheduleID(id), ownerID, ownerID, expectedUpdatedAt)
	if err != nil {
		return Result{}, err
	}
	reminder, ok, err := h.store.GetReminder(ctx, string(canceled.ID))
	if err != nil {
		return Result{}, err
	}
	if !ok {
		return Result{}, errors.New("canceled reminder is unavailable")
	}
	return Result{Output: reminderToolOutput(reminder)}, nil
}

func parseReminderTime(value string) (time.Time, error) {
	return parseReminderTimeInTimezone(value, "UTC")
}

func parseReminderTimeInTimezone(value, timezone string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errors.New("due_time cannot be empty")
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, nil
	}
	location, err := time.LoadLocation(strings.TrimSpace(timezone))
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid timezone %q", timezone)
	}
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, value, location); err == nil {
			return t, nil
		}
	}
	if t, err := time.ParseInLocation("2006-01-02", value, location); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("due_time must be RFC3339 or YYYY-MM-DD HH:MM in %s, got %q", timezone, value)
}

func reminderToolOutput(reminder app.Reminder) map[string]any {
	out := map[string]any{
		"reminder_id":      reminder.ID,
		"text":             reminder.Text,
		"text_summary":     reminder.TextSummary,
		"due_time":         reminder.DueTime.UTC().Format(time.RFC3339),
		"timezone":         reminder.Timezone,
		"channel":          reminder.Channel,
		"recurrence":       reminder.Recurrence,
		"recipient":        reminder.Recipient,
		"binding_id":       reminder.BindingID,
		"status":           reminder.Status,
		"created_at":       reminder.CreatedAt.UTC().Format(time.RFC3339Nano),
		"updated_at":       reminder.UpdatedAt.UTC().Format(time.RFC3339Nano),
		"last_delivery_id": reminder.LastDeliveryID,
		"last_error":       reminder.LastError,
	}
	if reminder.CanceledAt != nil {
		out["canceled_at"] = reminder.CanceledAt.UTC().Format(time.RFC3339)
	} else {
		out["canceled_at"] = ""
	}
	return out
}

func parseExpectedUpdatedAt(args map[string]any) (time.Time, error) {
	value := strings.TrimSpace(stringArg(args, "expected_updated_at", ""))
	if value == "" {
		return time.Time{}, errors.New("expected_updated_at cannot be empty")
	}
	expected, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("expected_updated_at must be RFC3339: %w", err)
	}
	return expected.UTC(), nil
}

func optionalStringArg(args map[string]any, key string) (string, bool) {
	value, ok := args[key]
	if !ok || value == nil {
		return "", false
	}
	switch v := value.(type) {
	case string:
		return v, true
	default:
		return fmt.Sprint(v), true
	}
}

func summarizeText(content string, limit int) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return "Reminder"
	}
	runes := []rune(content)
	if limit > 0 && len(runes) > limit {
		return string(runes[:limit]) + "..."
	}
	return content
}
