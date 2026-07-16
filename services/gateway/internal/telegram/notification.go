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

func (a *NotificationAdapter) Capabilities() delivery.Capabilities {
	return delivery.Capabilities{
		Parts: map[app.MessagePartKind]bool{
			app.MessagePartText: true, app.MessagePartImage: true, app.MessagePartAudio: true, app.MessagePartFile: true,
		},
		AudioDispositions: map[app.MessagePartDisposition]bool{
			app.MessageDispositionVoiceNote: true, app.MessageDispositionAttachment: true,
		},
	}
}

func (a *NotificationAdapter) Deliver(ctx context.Context, endpoint app.MessageEndpoint, request app.DeliveryRequest) (app.DeliveryReceipt, error) {
	binding, ok := a.store.GetNotificationBinding(strings.TrimSpace(endpoint.BindingRef))
	if !ok || binding.Channel != a.Key() || binding.Status != "active" {
		return a.deliveryFailure(endpoint, request, "telegram binding is unavailable", "blocked")
	}
	resources := delivery.ResourceResolver(a.resources)
	if endpoint.SessionID != "" {
		resources = delivery.NewEndpointResourceResolver(a.store, endpoint)
	}
	prepared, err := delivery.PrepareParts(ctx, request.Content, resources)
	if err != nil {
		return a.deliveryFailure(endpoint, request, err.Error(), "blocked")
	}
	chatID, threadID, err := telegramDeliveryAddress(endpoint, binding)
	if err != nil {
		return a.deliveryFailure(endpoint, request, err.Error(), "blocked")
	}
	if a.vault == nil {
		return a.deliveryFailure(endpoint, request, "telegram credential vault is unavailable", "blocked")
	}
	token, err := a.vault.Open(ctx, binding.CredentialRef)
	if err != nil {
		return a.deliveryFailure(endpoint, request, "telegram credential is unavailable", "blocked")
	}
	defer clear(token)
	baseURL := strings.TrimRight(strings.TrimSpace(binding.BaseURL), "/")
	if baseURL == "" {
		baseURL = strings.TrimRight(strings.TrimSpace(a.cfg.BaseURL), "/")
	}
	if baseURL == "" {
		return a.deliveryFailure(endpoint, request, "telegram base URL is unavailable", "blocked")
	}
	client := NewClient(baseURL, string(token), &http.Client{Timeout: 15 * time.Second})
	attemptedAt := time.Now().UTC()
	for _, item := range prepared {
		caption := strings.TrimSpace(item.Part.Caption)
		switch item.Part.Kind {
		case app.MessagePartText:
			_, err = client.SendMessage(ctx, chatID, threadID, strings.TrimSpace(item.Part.Text), nil)
		case app.MessagePartImage:
			_, err = client.SendPhoto(ctx, chatID, threadID, item.Path, caption)
		case app.MessagePartAudio:
			if item.Part.Disposition == app.MessageDispositionVoiceNote {
				_, err = client.SendVoice(ctx, chatID, threadID, item.Path, caption)
			} else {
				_, err = client.SendDocument(ctx, chatID, threadID, item.Path, firstNonEmpty(item.Part.Name, filepath.Base(item.Path)), caption)
			}
		case app.MessagePartFile:
			_, err = client.SendDocument(ctx, chatID, threadID, item.Path, firstNonEmpty(item.Part.Name, filepath.Base(item.Path)), caption)
		}
		if err != nil {
			state := "blocked"
			if telegramNotificationRetryable(err) {
				state = "retryable"
			}
			return a.deliveryFailure(endpoint, request, "telegram delivery failed", state)
		}
	}
	deliveredAt := time.Now().UTC()
	receipt := app.DeliveryReceipt{DeliveryID: request.ID, EndpointID: endpoint.ID, Status: app.DeliverySucceeded, ProviderRef: "telegram-bot-api", AttemptedAt: attemptedAt, DeliveredAt: &deliveredAt}
	delivery.RecordExternalDelivery(a.store, endpoint, request, receipt)
	return receipt, nil
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

func (a *NotificationAdapter) deliveryFailure(endpoint app.MessageEndpoint, request app.DeliveryRequest, message, retryState string) (app.DeliveryReceipt, error) {
	err := delivery.DeliveryError{Message: message, State: retryState}
	receipt := app.DeliveryReceipt{DeliveryID: request.ID, EndpointID: endpoint.ID, Status: app.DeliveryFailed, Error: message, RetryState: retryState, AttemptedAt: time.Now().UTC()}
	delivery.RecordExternalDelivery(a.store, endpoint, request, receipt)
	return receipt, err
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
