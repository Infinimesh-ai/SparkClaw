package telegram

import (
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/notification"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type NotificationAdapter struct {
	store     store.Store
	vault     credential.CredentialVault
	cfg       config.NotificationChannelConfig
	resources delivery.ResourceResolver
}

func NewNotificationAdapter(st store.Store, vault credential.CredentialVault, cfg config.NotificationChannelConfig) *NotificationAdapter {
	return &NotificationAdapter{store: st, vault: vault, cfg: cfg, resources: delivery.NewStoreResourceResolver(st)}
}

func (a *NotificationAdapter) Key() string { return "telegram" }

func (a *NotificationAdapter) Capabilities() app.DeliveryCapabilities {
	return app.DeliveryCapabilities{
		Kinds:             []app.MessagePartKind{app.MessagePartText, app.MessagePartImage, app.MessagePartAudio, app.MessagePartFile},
		Dispositions:      []app.MessagePartDisposition{app.MessageDispositionInline, app.MessageDispositionAttachment, app.MessageDispositionVoiceNote},
		FileFallbackKinds: []app.MessagePartKind{app.MessagePartImage, app.MessagePartAudio},
		NativeVoiceTypes:  []string{"audio/ogg", "audio/opus"},
		MaxParts:          8, MaxTotalBytes: 25 << 20,
		MaxBytesByKind:  map[app.MessagePartKind]int64{app.MessagePartImage: 25 << 20, app.MessagePartAudio: 25 << 20, app.MessagePartFile: 25 << 20},
		SupportsCaption: true, SupportsNativeVoice: true, SupportsFileFallback: true,
	}
}

func (a *NotificationAdapter) Deliver(ctx context.Context, endpoint app.MessageEndpoint, request app.DeliveryRequest) (app.DeliveryReceipt, error) {
	binding, err := a.deliveryBinding(endpoint, request)
	if err != nil {
		return a.deliveryFailure(endpoint, request, delivery.ErrorCode(err), err.Error(), "blocked", nil)
	}
	resources := delivery.ResourceResolver(a.resources)
	if endpoint.SessionID != "" {
		resources = delivery.NewEndpointResourceResolver(a.store, endpoint)
	}
	prepared, err := delivery.PrepareParts(ctx, request.Content, resources)
	if err != nil {
		return a.deliveryFailure(endpoint, request, delivery.CodeArtifactInvalid, "telegram delivery resource is invalid", "blocked", nil)
	}
	chatID, threadID, err := telegramDeliveryAddress(endpoint, binding)
	if err != nil {
		return a.deliveryFailure(endpoint, request, delivery.CodeBindingUnavailable, err.Error(), "blocked", nil)
	}
	if a.vault == nil {
		return a.deliveryFailure(endpoint, request, delivery.CodeBindingUnavailable, "telegram credential is unavailable", "blocked", nil)
	}
	token, err := a.vault.Open(ctx, binding.CredentialRef)
	if err != nil {
		return a.deliveryFailure(endpoint, request, delivery.CodeBindingUnavailable, "telegram credential is unavailable", "blocked", nil)
	}
	defer clear(token)
	baseURL := strings.TrimRight(strings.TrimSpace(binding.BaseURL), "/")
	if baseURL == "" {
		baseURL = strings.TrimRight(strings.TrimSpace(a.cfg.BaseURL), "/")
	}
	if baseURL == "" {
		return a.deliveryFailure(endpoint, request, delivery.CodeBindingUnavailable, "telegram provider is unavailable", "blocked", nil)
	}
	client := NewClient(baseURL, string(token), &http.Client{Timeout: 15 * time.Second})
	attemptedAt := time.Now().UTC()
	partReceipts := make([]app.PartDeliveryReceipt, 0, len(prepared))
	for index, item := range prepared {
		caption := strings.TrimSpace(item.Part.Caption)
		representation := "native"
		var sent Message
		switch item.Part.Kind {
		case app.MessagePartText:
			sent, err = client.SendMessage(ctx, chatID, threadID, strings.TrimSpace(item.Part.Text), nil)
		case app.MessagePartImage:
			sent, err = client.SendPhoto(ctx, chatID, threadID, item.Path, caption)
		case app.MessagePartAudio:
			if item.Part.Disposition == app.MessageDispositionVoiceNote && telegramNativeVoice(item.Part, item.Path) {
				sent, err = client.SendVoice(ctx, chatID, threadID, item.Path, caption)
			} else {
				representation = "file_fallback"
				sent, err = client.SendDocument(ctx, chatID, threadID, item.Path, firstNonEmpty(item.Part.Name, filepath.Base(item.Path)), caption)
			}
		case app.MessagePartFile:
			sent, err = client.SendDocument(ctx, chatID, threadID, item.Path, firstNonEmpty(item.Part.Name, filepath.Base(item.Path)), caption)
		}
		if err != nil {
			code, state := telegramDeliveryFailure(err)
			partReceipts = append(partReceipts, app.PartDeliveryReceipt{PartID: item.Part.ID, Status: "failed", Representation: representation, ErrorCode: code})
			for _, remaining := range prepared[index+1:] {
				partReceipts = append(partReceipts, app.PartDeliveryReceipt{PartID: remaining.Part.ID, Status: "not_attempted", Representation: telegramRepresentation(remaining), ErrorCode: code})
			}
			return a.deliveryFailure(endpoint, request, code, "telegram delivery failed", state, partReceipts)
		}
		partReceipts = append(partReceipts, app.PartDeliveryReceipt{PartID: item.Part.ID, Status: "sent", Representation: representation, ProviderRef: strconv.FormatInt(sent.MessageID, 10)})
	}
	deliveredAt := time.Now().UTC()
	receipt := app.DeliveryReceipt{DeliveryID: request.ID, EndpointID: endpoint.ID, Status: app.DeliverySucceeded, ProviderRef: "telegram-bot-api", Attempt: 1, PartReceipts: partReceipts, AttemptedAt: attemptedAt, DeliveredAt: &deliveredAt}
	delivery.RecordExternalDelivery(a.store, endpoint, request, receipt)
	return receipt, nil
}

func (a *NotificationAdapter) deliveryBinding(endpoint app.MessageEndpoint, request app.DeliveryRequest) (app.NotificationBinding, error) {
	if binding, ok := a.store.GetNotificationBinding(strings.TrimSpace(endpoint.BindingRef)); ok &&
		binding.Status == "active" && binding.Channel == a.Key() {
		if binding.RevokedAt != nil || (binding.ExpiresAt != nil && !binding.ExpiresAt.After(time.Now().UTC())) {
			return app.NotificationBinding{}, delivery.NewError(delivery.CodeBindingUnavailable, "telegram binding is unavailable", "blocked")
		}
		if request.Origin == app.DeliveryOriginSourceReply {
			if !bindingAllowsSourceReply(binding.Scopes) {
				return app.NotificationBinding{}, delivery.NewError(delivery.CodeScopeDenied, "telegram binding lacks ordinary message scope", "blocked")
			}
		} else {
			if binding.OwnerID != request.OwnerID || firstNonEmpty(binding.ActorID, binding.OwnerID) != request.ActorID {
				return app.NotificationBinding{}, delivery.NewError(delivery.CodeCrossUserDenied, "telegram binding is outside the actor scope", "blocked")
			}
			expectedScope := app.BindingScopeMessageSendSelf
			scopeDescription := "ordinary message"
			if request.Origin == app.DeliveryOriginSchedule {
				expectedScope = app.BindingScopeReminderSendSelf
				scopeDescription = "reminder"
			}
			if !bindingAllowsScope(binding.Scopes, expectedScope, request.Origin) {
				return app.NotificationBinding{}, delivery.NewError(delivery.CodeScopeDenied, "telegram binding lacks "+scopeDescription+" scope", "blocked")
			}
		}
		return binding, nil
	}
	if !strings.HasPrefix(string(endpoint.ID), "legacy-schedule:") {
		return app.NotificationBinding{}, delivery.NewError(delivery.CodeBindingUnavailable, "telegram binding is unavailable", "blocked")
	}
	reminder, ok := a.store.GetReminder(strings.TrimSpace(request.ResultID))
	if !ok || !strings.EqualFold(strings.TrimSpace(reminder.Channel), a.Key()) {
		return app.NotificationBinding{}, delivery.NewError(delivery.CodeBindingUnavailable, "telegram binding is unavailable", "blocked")
	}
	return app.NotificationBinding{
		ID: reminder.BindingID, OwnerID: request.OwnerID, Channel: a.Key(), Status: "active",
		ExternalChatID: reminder.Recipient, ExternalThreadID: reminder.RecipientBinding,
		CredentialRef: reminder.CredentialRef, BaseURL: reminder.BaseURL,
	}, nil
}

func telegramDeliveryAddress(endpoint app.MessageEndpoint, binding app.NotificationBinding) (int64, int64, error) {
	chatID, err := strconv.ParseInt(firstNonEmpty(endpoint.Address, binding.ExternalChatID), 10, 64)
	if err != nil || chatID == 0 {
		return 0, 0, errors.New("telegram chat binding is invalid")
	}
	threadID := int64(0)
	if value := firstNonEmpty(endpoint.ThreadRef, binding.ExternalThreadID); value != "" {
		threadID, err = strconv.ParseInt(value, 10, 64)
		if err != nil || threadID == 0 {
			return 0, 0, errors.New("telegram thread binding is invalid")
		}
	}
	return chatID, threadID, nil
}

func (a *NotificationAdapter) deliveryFailure(endpoint app.MessageEndpoint, request app.DeliveryRequest, code, message, retryState string, parts []app.PartDeliveryReceipt) (app.DeliveryReceipt, error) {
	err := delivery.DeliveryError{Code: code, Message: message, State: retryState}
	status := app.DeliveryFailed
	for _, part := range parts {
		if part.Status == "sent" {
			status = app.DeliveryPartiallySent
			break
		}
	}
	if code == delivery.CodeOutcomeUnknown && status != app.DeliveryPartiallySent {
		status = app.DeliveryOutcomeUnknown
	}
	receipt := app.DeliveryReceipt{DeliveryID: request.ID, EndpointID: endpoint.ID, Status: status, Error: message, ErrorCode: code, RetryState: retryState, Attempt: 1, PartReceipts: parts, AttemptedAt: time.Now().UTC()}
	delivery.RecordExternalDelivery(a.store, endpoint, request, receipt)
	return receipt, err
}

func telegramNativeVoice(part app.MessagePart, path string) bool {
	contentType := strings.ToLower(strings.TrimSpace(part.ContentType))
	if contentType == "audio/ogg" || contentType == "audio/opus" {
		return true
	}
	switch strings.ToLower(filepath.Ext(firstNonEmpty(part.Name, path))) {
	case ".ogg", ".oga", ".opus":
		return true
	default:
		return false
	}
}

func telegramRepresentation(item delivery.PreparedPart) string {
	if item.Part.Kind == app.MessagePartAudio && item.Part.Disposition == app.MessageDispositionVoiceNote && !telegramNativeVoice(item.Part, item.Path) {
		return "file_fallback"
	}
	return "native"
}

func telegramDeliveryFailure(err error) (string, string) {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return delivery.CodeOutcomeUnknown, "unsafe"
	}
	if telegramNotificationRetryable(err) {
		return delivery.CodeProviderRetryable, "retryable"
	}
	return delivery.CodeBindingUnavailable, "blocked"
}

func bindingHasScope(scopes []string, expected string) bool {
	for _, scope := range scopes {
		if strings.TrimSpace(scope) == expected {
			return true
		}
	}
	return false
}

func bindingAllowsScope(scopes []string, expected string, origin app.DeliveryOrigin) bool {
	return bindingHasScope(scopes, expected) || (origin == app.DeliveryOriginSchedule && len(scopes) == 0)
}

func bindingAllowsSourceReply(scopes []string) bool {
	return len(scopes) == 0 || bindingHasScope(scopes, app.BindingScopeMessageSendSelf)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func (a *NotificationAdapter) Send(ctx context.Context, request notification.Notification) (notification.Result, error) {
	binding, ok := a.store.GetNotificationBinding(strings.TrimSpace(request.BindingID))
	if !ok || binding.Channel != "telegram" || binding.Status != "active" {
		return a.failed(request, "telegram binding is unavailable", "blocked")
	}
	if strings.TrimSpace(request.CredentialRef) == "" || request.CredentialRef != binding.CredentialRef {
		return a.failed(request, "telegram credential reference is invalid", "blocked")
	}
	if strings.TrimSpace(request.Recipient) == "" || request.Recipient != binding.ExternalChatID ||
		strings.TrimSpace(request.RecipientBinding) != strings.TrimSpace(binding.ExternalThreadID) {
		return a.failed(request, "telegram reminder target no longer matches its binding", "blocked")
	}
	chatID, err := strconv.ParseInt(request.Recipient, 10, 64)
	if err != nil || chatID == 0 {
		return a.failed(request, "telegram chat binding is invalid", "blocked")
	}
	threadID := int64(0)
	if strings.TrimSpace(request.RecipientBinding) != "" {
		threadID, err = strconv.ParseInt(request.RecipientBinding, 10, 64)
		if err != nil || threadID == 0 {
			return a.failed(request, "telegram thread binding is invalid", "blocked")
		}
	}
	message := strings.TrimSpace(request.MessageText)
	if message == "" {
		return a.failed(request, "notification message cannot be empty", "blocked")
	}
	if a.vault == nil {
		return a.failed(request, "telegram credential vault is unavailable", "blocked")
	}
	token, err := a.vault.Open(ctx, binding.CredentialRef)
	if err != nil {
		return a.failed(request, "telegram credential is unavailable", "blocked")
	}
	defer clear(token)
	baseURL := strings.TrimRight(strings.TrimSpace(binding.BaseURL), "/")
	if baseURL == "" {
		baseURL = strings.TrimRight(strings.TrimSpace(a.cfg.BaseURL), "/")
	}
	if baseURL == "" {
		return a.failed(request, "telegram base URL is unavailable", "blocked")
	}
	client := NewClient(baseURL, string(token), &http.Client{Timeout: 15 * time.Second})
	if _, err := client.SendMessage(ctx, chatID, threadID, message, nil); err != nil {
		retryState := "blocked"
		if telegramNotificationRetryable(err) {
			retryState = "retryable"
		}
		return a.failed(request, "telegram delivery failed", retryState)
	}
	return notification.Result{
		DeliveryID:     app.NewID("rdel"),
		Channel:        "telegram",
		Provider:       "telegram-bot-api",
		Recipient:      request.Recipient,
		Status:         "sent",
		ProviderStatus: "sent",
		RetryState:     "none",
		SentAt:         time.Now().UTC(),
	}, nil
}

func (a *NotificationAdapter) failed(request notification.Notification, message, retryState string) (notification.Result, error) {
	return notification.Result{
		DeliveryID:     app.NewID("rdel"),
		Channel:        "telegram",
		Provider:       "telegram-bot-api",
		Recipient:      strings.TrimSpace(request.Recipient),
		Status:         "failed",
		ProviderStatus: "failed",
		Error:          message,
		RetryState:     retryState,
	}, errors.New(message)
}

func telegramNotificationRetryable(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.RetryAfter > 0 || apiErr.Code == http.StatusTooManyRequests || apiErr.Code >= 500
	}
	var netErr net.Error
	return errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary())
}
