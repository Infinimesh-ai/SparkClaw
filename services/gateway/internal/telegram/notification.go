package telegram

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/notification"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type NotificationAdapter struct {
	store store.Store
	vault credential.CredentialVault
	cfg   config.NotificationChannelConfig
}

func NewNotificationAdapter(st store.Store, vault credential.CredentialVault, cfg config.NotificationChannelConfig) *NotificationAdapter {
	return &NotificationAdapter{store: st, vault: vault, cfg: cfg}
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
