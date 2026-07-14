package toolhub

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (h *ToolHub) remindersCreate(args map[string]any, sessionID, runID string) (Result, error) {
	text := strings.TrimSpace(stringArg(args, "text", ""))
	if text == "" {
		return Result{}, errors.New("text cannot be empty")
	}
	dueTime, err := parseReminderTime(stringArg(args, "due_time", ""))
	if err != nil {
		return Result{}, err
	}
	channel := strings.TrimSpace(stringArg(args, "channel", ""))
	if channel == "" {
		if _, ok := h.store.FindExternalChatSessionByLinkedSessionID(sessionID); ok {
			channel = "weixin"
		} else {
			channel = "web"
		}
	}
	channel = strings.ToLower(strings.TrimSpace(channel))
	timezone := strings.TrimSpace(stringArg(args, "timezone", ""))
	if timezone == "" {
		timezone = "Asia/Shanghai"
	}
	recipient := strings.TrimSpace(stringArg(args, "recipient", ""))
	recipientBinding := ""
	bindingID := ""
	credentialRef := ""
	baseURL := ""
	if channel == "weixin" {
		resolved, err := h.resolveWeixinReminderRecipient(sessionID, recipient)
		if err != nil {
			return Result{}, err
		}
		recipient = resolved.recipient
		recipientBinding = resolved.contextToken
		bindingID = resolved.bindingID
		credentialRef = resolved.credentialRef
		baseURL = resolved.baseURL
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
		Recurrence:       strings.TrimSpace(stringArg(args, "recurrence", "")),
		DedupeKey:        strings.TrimSpace(stringArg(args, "dedupe_key", "")),
		Status:           "pending",
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if reminder.DedupeKey == "" {
		reminder.DedupeKey = reminder.ID
	}
	reminder = h.store.SaveReminder(reminder)
	return Result{Output: reminderToolOutput(reminder)}, nil
}

type weixinReminderRecipient struct {
	recipient     string
	contextToken  string
	bindingID     string
	credentialRef string
	baseURL       string
}

func (h *ToolHub) resolveWeixinReminderRecipient(sessionID, requestedRecipient string) (weixinReminderRecipient, error) {
	if chatSession, ok := h.store.FindExternalChatSessionByLinkedSessionID(sessionID); ok {
		binding, _ := h.store.GetNotificationBinding(strings.TrimSpace(chatSession.BindingID))
		return validateWeixinReminderRecipient(weixinReminderRecipient{
			recipient:     strings.TrimSpace(firstNonEmptyString(requestedRecipient, chatSession.ExternalUserID)),
			contextToken:  strings.TrimSpace(chatSession.LastContextToken),
			bindingID:     strings.TrimSpace(chatSession.BindingID),
			credentialRef: strings.TrimSpace(binding.CredentialRef),
			baseURL:       strings.TrimSpace(binding.BaseURL),
		})
	}
	bindings := h.store.ListNotificationBindings("weixin", "active")
	if len(bindings) == 0 {
		return weixinReminderRecipient{}, errors.New("微信提醒需要先绑定微信用户；当前没有可用的微信绑定")
	}
	if strings.TrimSpace(requestedRecipient) == "" {
		if len(bindings) > 1 {
			return weixinReminderRecipient{}, fmt.Errorf("检测到多个微信绑定用户，请先说明要提醒哪个用户：%s", describeWeixinBindings(bindings))
		}
		return validateWeixinReminderRecipient(recipientFromBinding(bindings[0]))
	}
	matches := []app.NotificationBinding{}
	needle := strings.ToLower(strings.TrimSpace(requestedRecipient))
	for _, binding := range bindings {
		if bindingMatchesRecipient(binding, needle) {
			matches = append(matches, binding)
		}
	}
	switch len(matches) {
	case 0:
		return weixinReminderRecipient{}, fmt.Errorf("没有找到匹配的微信绑定用户 %q；可选用户：%s", requestedRecipient, describeWeixinBindings(bindings))
	case 1:
		return validateWeixinReminderRecipient(recipientFromBinding(matches[0]))
	default:
		return weixinReminderRecipient{}, fmt.Errorf("微信绑定用户 %q 不唯一，请更具体说明：%s", requestedRecipient, describeWeixinBindings(matches))
	}
}

func validateWeixinReminderRecipient(recipient weixinReminderRecipient) (weixinReminderRecipient, error) {
	if strings.TrimSpace(recipient.recipient) == "" {
		return weixinReminderRecipient{}, errors.New("微信提醒缺少目标用户；请先让目标微信用户完成绑定或发送一条消息")
	}
	if strings.TrimSpace(recipient.contextToken) == "" {
		return weixinReminderRecipient{}, errors.New("微信提醒缺少 context_token；请先让目标微信用户向助手发送一条消息后再创建提醒")
	}
	return recipient, nil
}

func recipientFromBinding(binding app.NotificationBinding) weixinReminderRecipient {
	return weixinReminderRecipient{
		recipient:     strings.TrimSpace(binding.ExternalUserID),
		contextToken:  strings.TrimSpace(binding.ContextToken),
		bindingID:     strings.TrimSpace(binding.ID),
		credentialRef: strings.TrimSpace(binding.CredentialRef),
		baseURL:       strings.TrimSpace(binding.BaseURL),
	}
}

func bindingMatchesRecipient(binding app.NotificationBinding, needle string) bool {
	for _, value := range []string{binding.ID, binding.ExternalUserID, binding.DisplayName, binding.AccountID} {
		if strings.ToLower(strings.TrimSpace(value)) == needle {
			return true
		}
	}
	return false
}

func describeWeixinBindings(bindings []app.NotificationBinding) string {
	parts := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		label := strings.TrimSpace(binding.DisplayName)
		if label == "" {
			label = strings.TrimSpace(binding.AccountID)
		}
		if label == "" {
			label = strings.TrimSpace(binding.ExternalUserID)
		}
		if label == "" {
			label = strings.TrimSpace(binding.ID)
		}
		parts = append(parts, fmt.Sprintf("%s(%s)", label, binding.ID))
	}
	return strings.Join(parts, "、")
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (h *ToolHub) remindersList(args map[string]any, sessionID string) (Result, error) {
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
	reminders := h.store.ListReminders(filter)
	ownerID := h.ownerIDForSession(sessionID)
	items := make([]map[string]any, 0, len(reminders))
	for _, reminder := range reminders {
		if !h.sessionVisibleToOwner(reminder.SessionID, ownerID) {
			continue
		}
		items = append(items, reminderToolOutput(reminder))
	}
	return Result{Output: map[string]any{
		"reminders": items,
		"count":     len(items),
	}}, nil
}

func (h *ToolHub) remindersUpdate(args map[string]any, sessionID string) (Result, error) {
	id := strings.TrimSpace(stringArg(args, "reminder_id", ""))
	if id == "" {
		return Result{}, errors.New("reminder_id cannot be empty")
	}
	reminder, ok := h.store.GetReminder(id)
	if !ok {
		return Result{}, errors.New("reminder not found")
	}
	if !h.sessionVisibleToOwner(reminder.SessionID, h.ownerIDForSession(sessionID)) {
		return Result{}, errors.New("reminder not found")
	}
	if reminder.Status != "pending" {
		return Result{}, fmt.Errorf("only pending reminders can be updated, current status is %q", reminder.Status)
	}
	if text, ok := optionalStringArg(args, "text"); ok {
		text = strings.TrimSpace(text)
		if text == "" {
			return Result{}, errors.New("text cannot be empty")
		}
		reminder.Text = text
		reminder.TextSummary = summarizeText(text, 80)
	}
	if due, ok := optionalStringArg(args, "due_time"); ok {
		t, err := parseReminderTime(due)
		if err != nil {
			return Result{}, err
		}
		reminder.DueTime = t.UTC()
	}
	if timezone, ok := optionalStringArg(args, "timezone"); ok {
		reminder.Timezone = strings.TrimSpace(timezone)
	}
	if channel, ok := optionalStringArg(args, "channel"); ok {
		reminder.Channel = strings.TrimSpace(channel)
	}
	if recipient, ok := optionalStringArg(args, "recipient"); ok {
		reminder.Recipient = strings.TrimSpace(recipient)
	}
	if recurrence, ok := optionalStringArg(args, "recurrence"); ok {
		reminder.Recurrence = strings.TrimSpace(recurrence)
	}
	reminder.UpdatedAt = time.Now().UTC()
	reminder = h.store.SaveReminder(reminder)
	return Result{Output: reminderToolOutput(reminder)}, nil
}

func (h *ToolHub) remindersCancel(args map[string]any, sessionID string) (Result, error) {
	id := strings.TrimSpace(stringArg(args, "reminder_id", ""))
	if id == "" {
		return Result{}, errors.New("reminder_id cannot be empty")
	}
	reminder, ok := h.store.GetReminder(id)
	if !ok {
		return Result{}, errors.New("reminder not found")
	}
	if !h.sessionVisibleToOwner(reminder.SessionID, h.ownerIDForSession(sessionID)) {
		return Result{}, errors.New("reminder not found")
	}
	if reminder.Status != "pending" {
		return Result{}, fmt.Errorf("only pending reminders can be canceled, current status is %q", reminder.Status)
	}
	now := time.Now().UTC()
	reminder.Status = "canceled"
	reminder.CanceledAt = &now
	reminder.UpdatedAt = now
	reminder = h.store.SaveReminder(reminder)
	return Result{Output: reminderToolOutput(reminder)}, nil
}

func parseReminderTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errors.New("due_time cannot be empty")
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02 15:04", value); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", value); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("due_time must be RFC3339 or YYYY-MM-DD HH:MM, got %q", value)
}

func reminderToolOutput(reminder app.Reminder) map[string]any {
	out := map[string]any{
		"reminder_id":      reminder.ID,
		"text_summary":     reminder.TextSummary,
		"due_time":         reminder.DueTime.UTC().Format(time.RFC3339),
		"timezone":         reminder.Timezone,
		"channel":          reminder.Channel,
		"recipient":        reminder.Recipient,
		"binding_id":       reminder.BindingID,
		"status":           reminder.Status,
		"created_at":       reminder.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":       reminder.UpdatedAt.UTC().Format(time.RFC3339),
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
