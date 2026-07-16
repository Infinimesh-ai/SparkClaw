package notification

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/weixinproto"
)

type Notification struct {
	ReminderID       string
	Channel          string
	BindingID        string
	BaseURL          string
	Recipient        string
	RecipientBinding string
	CredentialRef    string
	MessageText      string
	DedupeKey        string
	ImagePath        string
	FilePath         string
	FileName         string
}

type TypingStatus int

const (
	TypingStatusTyping TypingStatus = 1
	TypingStatusCancel TypingStatus = 2
)

func SendWeixinText(ctx context.Context, st store.Store, channelCfg config.NotificationChannelConfig, recipient, contextToken, credentialRef, baseURL, messageText, dedupeKey string) (Result, error) {
	return NewWeixinAdapter("weixin", channelCfg, st).Send(ctx, Notification{
		Channel:          "weixin",
		BaseURL:          baseURL,
		Recipient:        recipient,
		RecipientBinding: contextToken,
		CredentialRef:    credentialRef,
		MessageText:      messageText,
		DedupeKey:        dedupeKey,
	})
}

func SendWeixinImage(ctx context.Context, st store.Store, channelCfg config.NotificationChannelConfig, recipient, contextToken, credentialRef, baseURL, imagePath, caption, dedupeKey string) (Result, error) {
	return NewWeixinAdapter("weixin", channelCfg, st).SendImage(ctx, Notification{
		Channel:          "weixin",
		BaseURL:          baseURL,
		Recipient:        recipient,
		RecipientBinding: contextToken,
		CredentialRef:    credentialRef,
		MessageText:      caption,
		DedupeKey:        dedupeKey,
		ImagePath:        imagePath,
	})
}

func SendWeixinFile(ctx context.Context, st store.Store, channelCfg config.NotificationChannelConfig, recipient, contextToken, credentialRef, baseURL, filePath, fileName, caption, dedupeKey string) (Result, error) {
	return NewWeixinAdapter("weixin", channelCfg, st).SendFile(ctx, Notification{
		Channel:          "weixin",
		BaseURL:          baseURL,
		Recipient:        recipient,
		RecipientBinding: contextToken,
		CredentialRef:    credentialRef,
		MessageText:      caption,
		DedupeKey:        dedupeKey,
		FilePath:         filePath,
		FileName:         fileName,
	})
}

func SendWeixinTyping(ctx context.Context, st store.Store, channelCfg config.NotificationChannelConfig, recipient, contextToken, credentialRef, baseURL string, status TypingStatus) (Result, error) {
	return NewWeixinAdapter("weixin", channelCfg, st).SendTyping(ctx, Notification{
		Channel:          "weixin",
		BaseURL:          baseURL,
		Recipient:        recipient,
		RecipientBinding: contextToken,
		CredentialRef:    credentialRef,
	}, status)
}

type Result struct {
	DeliveryID     string
	Channel        string
	Provider       string
	Recipient      string
	Status         string
	ProviderStatus string
	Error          string
	RetryState     string
	SentAt         time.Time
}

type Adapter interface {
	Send(ctx context.Context, notification Notification) (Result, error)
}

type Router struct {
	channels map[string]Adapter
	store    store.Store
}

func (r Router) WithAdapter(channel string, adapter Adapter) Router {
	channel = strings.ToLower(strings.TrimSpace(channel))
	if channel != "" && adapter != nil {
		r.channels[channel] = adapter
	}
	return r
}

func NewRouter(cfg config.Config, stores ...store.Store) Router {
	router := NewBaseRouter(stores...)
	for name, channel := range cfg.Tools.Notifications.Channels {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" || !channel.Enabled {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(channel.Provider)) {
		case "openclaw-weixin-compatible", "openclaw-weixin-qr", "openclaw-weixin-login-qr", "weixin", "vx":
			router = router.WithAdapter(name, NewWeixinAdapter(name, channel, router.store))
		}
	}
	return router
}

func NewBaseRouter(stores ...store.Store) Router {
	var st store.Store
	if len(stores) > 0 {
		st = stores[0]
	}
	return Router{
		channels: map[string]Adapter{"web": WebAdapter{}},
		store:    st,
	}
}

func (r Router) Send(ctx context.Context, notification Notification) (Result, error) {
	channel := strings.ToLower(strings.TrimSpace(notification.Channel))
	if channel == "" {
		channel = "web"
	}
	adapter, ok := r.channels[channel]
	if !ok {
		return Result{
			DeliveryID: app.NewID("rdel"),
			Channel:    channel,
			Status:     "failed",
			Error:      "notification channel is not configured or enabled",
			RetryState: "blocked",
		}, fmt.Errorf("notification channel %q is not configured or enabled", channel)
	}
	return adapter.Send(ctx, notification)
}

type WebAdapter struct{}

func (a WebAdapter) Send(ctx context.Context, notification Notification) (Result, error) {
	_ = ctx
	message := strings.TrimSpace(notification.MessageText)
	if message == "" {
		return Result{
			DeliveryID:     app.NewID("rdel"),
			Channel:        "web",
			Provider:       "web-local",
			Recipient:      strings.TrimSpace(notification.Recipient),
			Status:         "failed",
			ProviderStatus: "failed",
			Error:          "notification message cannot be empty",
			RetryState:     "blocked",
		}, errors.New("notification message cannot be empty")
	}
	return Result{
		DeliveryID:     app.NewID("rdel"),
		Channel:        "web",
		Provider:       "web-local",
		Recipient:      strings.TrimSpace(notification.Recipient),
		Status:         "sent",
		ProviderStatus: "stored",
		RetryState:     "none",
		SentAt:         time.Now().UTC(),
	}, nil
}

type WeixinAdapter struct {
	channel   string
	cfg       config.NotificationChannelConfig
	store     store.Store
	client    *http.Client
	resources delivery.ResourceResolver
}

func NewWeixinAdapter(channel string, cfg config.NotificationChannelConfig, stores ...store.Store) *WeixinAdapter {
	var st store.Store
	if len(stores) > 0 {
		st = stores[0]
	}
	return &WeixinAdapter{
		channel:   channel,
		cfg:       cfg,
		store:     st,
		client:    &http.Client{Timeout: 15 * time.Second},
		resources: delivery.NewStoreResourceResolver(st),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func (a *WeixinAdapter) Key() string { return a.channel }

func (a *WeixinAdapter) Capabilities() delivery.Capabilities {
	return delivery.Capabilities{Parts: map[app.MessagePartKind]bool{
		app.MessagePartText: true, app.MessagePartImage: true, app.MessagePartFile: true,
	}}
}

func (a *WeixinAdapter) Deliver(ctx context.Context, endpoint app.MessageEndpoint, request app.DeliveryRequest) (app.DeliveryReceipt, error) {
	if a.store == nil {
		return a.deliveryFailure(endpoint, request, "weixin binding store is unavailable", "blocked")
	}
	binding, ok := a.store.GetNotificationBinding(strings.TrimSpace(endpoint.BindingRef))
	if !ok || binding.Status != "active" || strings.ToLower(strings.TrimSpace(binding.Channel)) != strings.ToLower(strings.TrimSpace(a.channel)) {
		return a.deliveryFailure(endpoint, request, "weixin binding is unavailable", "blocked")
	}
	resources := delivery.ResourceResolver(a.resources)
	if endpoint.SessionID != "" {
		resources = delivery.NewEndpointResourceResolver(a.store, endpoint)
	}
	prepared, err := delivery.PrepareParts(ctx, request.Content, resources)
	if err != nil {
		return a.deliveryFailure(endpoint, request, err.Error(), "blocked")
	}
	attemptedAt := time.Now().UTC()
	providerRef := "openclaw-weixin-compatible"
	for _, item := range prepared {
		notice := Notification{
			Channel:          a.channel,
			BindingID:        binding.ID,
			BaseURL:          binding.BaseURL,
			Recipient:        firstNonEmpty(endpoint.Address, binding.ExternalChatID, binding.ExternalUserID),
			RecipientBinding: firstNonEmpty(endpoint.ThreadRef, endpoint.ContextRef, binding.ExternalThreadID, binding.ContextToken),
			CredentialRef:    binding.CredentialRef,
			MessageText:      firstNonEmpty(item.Part.Text, item.Part.Caption),
			DedupeKey:        request.IdempotencyKey + ":" + item.Part.ID,
		}
		var result Result
		switch item.Part.Kind {
		case app.MessagePartText:
			result, err = a.Send(ctx, notice)
		case app.MessagePartImage:
			notice.ImagePath = item.Path
			result, err = a.SendImage(ctx, notice)
		case app.MessagePartFile:
			notice.FilePath = item.Path
			notice.FileName = firstNonEmpty(item.Part.Name, filepath.Base(item.Path))
			result, err = a.SendFile(ctx, notice)
		default:
			err = fmt.Errorf("content kind %q is not supported", item.Part.Kind)
		}
		if err != nil {
			return a.deliveryFailure(endpoint, request, firstNonEmpty(result.Error, err.Error()), firstNonEmpty(result.RetryState, "blocked"))
		}
		if result.Provider != "" {
			providerRef = result.Provider
		}
	}
	deliveredAt := time.Now().UTC()
	receipt := app.DeliveryReceipt{DeliveryID: request.ID, EndpointID: endpoint.ID, Status: app.DeliverySucceeded, ProviderRef: providerRef, AttemptedAt: attemptedAt, DeliveredAt: &deliveredAt}
	delivery.RecordExternalDelivery(a.store, endpoint, request, receipt)
	return receipt, nil
}

func (a *WeixinAdapter) deliveryFailure(endpoint app.MessageEndpoint, request app.DeliveryRequest, message, retryState string) (app.DeliveryReceipt, error) {
	err := delivery.DeliveryError{Message: message, State: retryState}
	receipt := app.DeliveryReceipt{DeliveryID: request.ID, EndpointID: endpoint.ID, Status: app.DeliveryFailed, Error: message, RetryState: retryState, AttemptedAt: time.Now().UTC()}
	delivery.RecordExternalDelivery(a.store, endpoint, request, receipt)
	return receipt, err
}

func (a *WeixinAdapter) Send(ctx context.Context, notification Notification) (Result, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(notification.BaseURL), "/")
	if baseURL == "" {
		baseURL = strings.TrimRight(strings.TrimSpace(a.cfg.BaseURL), "/")
	}
	if baseURL == "" {
		return a.failed(notification, "openclaw-weixin baseUrl is not configured", "blocked")
	}
	token := strings.TrimSpace(a.cfg.Token)
	if token == "" && strings.TrimSpace(notification.CredentialRef) != "" && a.store != nil {
		if secret, ok := a.store.GetCredentialSecret(strings.TrimSpace(notification.CredentialRef)); ok {
			token = strings.TrimSpace(secret.Value)
		}
	}
	if token == "" {
		return a.failed(notification, "openclaw-weixin token is not configured", "blocked")
	}
	recipient := strings.TrimSpace(notification.Recipient)
	if recipient == "" {
		return a.failed(notification, "weixin recipient binding is not configured", "blocked")
	}
	if strings.TrimSpace(notification.RecipientBinding) == "" {
		return a.failed(notification, "weixin context token is not configured", "blocked")
	}
	message := strings.TrimSpace(notification.MessageText)
	if message == "" {
		return a.failed(notification, "notification message cannot be empty", "blocked")
	}
	payload := map[string]any{
		"msg": map[string]any{
			"from_user_id":  "",
			"to_user_id":    recipient,
			"client_id":     app.NewID("wxmsg"),
			"message_type":  2,
			"message_state": 3,
			"item_list": []map[string]any{{
				"type": 1,
				"text_item": map[string]any{
					"text": message,
				},
			}},
		},
		"base_info": map[string]any{
			"channel_version": weixinproto.ChannelVersion,
			"bot_agent":       "SparkClaw",
		},
	}
	msg := payload["msg"].(map[string]any)
	msg["context_token"] = strings.TrimSpace(notification.RecipientBinding)
	if strings.TrimSpace(notification.DedupeKey) != "" {
		msg["run_id"] = strings.TrimSpace(notification.DedupeKey)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return a.failed(notification, err.Error(), "blocked")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/ilink/bot/sendmessage", bytes.NewReader(raw))
	if err != nil {
		return a.failed(notification, err.Error(), "blocked")
	}
	weixinproto.SetHeaders(req, token)
	resp, err := a.client.Do(req)
	if err != nil {
		return a.failed(notification, err.Error(), "retryable")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return a.failed(notification, fmt.Sprintf("provider returned HTTP %d", resp.StatusCode), "retryable")
	}
	var decoded struct {
		Ret    int    `json:"ret"`
		ErrMsg string `json:"errmsg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err == nil && decoded.Ret != 0 {
		message := decoded.ErrMsg
		if message == "" {
			message = fmt.Sprintf("provider returned ret=%d", decoded.Ret)
		}
		return a.failed(notification, message, "retryable")
	}
	now := time.Now().UTC()
	return Result{
		DeliveryID:     app.NewID("rdel"),
		Channel:        a.channel,
		Provider:       weixinproto.ProviderName(a.cfg.Provider),
		Recipient:      redactRecipient(recipient),
		Status:         "sent",
		ProviderStatus: "sent",
		RetryState:     "none",
		SentAt:         now,
	}, nil
}

func (a *WeixinAdapter) SendImage(ctx context.Context, notification Notification) (Result, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(notification.BaseURL), "/")
	if baseURL == "" {
		baseURL = strings.TrimRight(strings.TrimSpace(a.cfg.BaseURL), "/")
	}
	if baseURL == "" {
		return a.failed(notification, "openclaw-weixin baseUrl is not configured", "blocked")
	}
	token := strings.TrimSpace(a.cfg.Token)
	if token == "" && strings.TrimSpace(notification.CredentialRef) != "" && a.store != nil {
		if secret, ok := a.store.GetCredentialSecret(strings.TrimSpace(notification.CredentialRef)); ok {
			token = strings.TrimSpace(secret.Value)
		}
	}
	if token == "" {
		return a.failed(notification, "openclaw-weixin token is not configured", "blocked")
	}
	recipient := strings.TrimSpace(notification.Recipient)
	if recipient == "" {
		return a.failed(notification, "weixin recipient binding is not configured", "blocked")
	}
	contextToken := strings.TrimSpace(notification.RecipientBinding)
	if contextToken == "" {
		return a.failed(notification, "weixin context token is not configured", "blocked")
	}
	imagePath := strings.TrimSpace(notification.ImagePath)
	if imagePath == "" {
		return a.failed(notification, "weixin image path is required", "blocked")
	}
	raw, err := os.ReadFile(imagePath)
	if err != nil {
		return a.failed(notification, err.Error(), "blocked")
	}
	if len(raw) == 0 {
		return a.failed(notification, "weixin image file is empty", "blocked")
	}
	if len(raw) > maxWeixinOutboundImageBytes {
		return a.failed(notification, fmt.Sprintf("weixin image exceeds limit: %d bytes", len(raw)), "blocked")
	}
	contentType := http.DetectContentType(raw)
	if !supportedWeixinOutboundImageType(contentType) {
		return a.failed(notification, fmt.Sprintf("unsupported weixin image content type %q", contentType), "blocked")
	}
	uploaded, err := a.uploadImageToCDN(ctx, baseURL, token, recipient, raw)
	if err != nil {
		return a.failed(notification, err.Error(), "retryable")
	}
	items := []map[string]any{}
	if caption := strings.TrimSpace(notification.MessageText); caption != "" {
		items = append(items, map[string]any{
			"type": 1,
			"text_item": map[string]any{
				"text": caption,
			},
		})
	}
	items = append(items, map[string]any{
		"type": 2,
		"image_item": map[string]any{
			"media": map[string]any{
				"encrypt_query_param": uploaded.DownloadEncryptedQueryParam,
				"aes_key":             base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(uploaded.AESKey))),
				"encrypt_type":        1,
			},
			"mid_size": uploaded.CiphertextSize,
		},
	})
	for _, item := range items {
		if err := a.sendWeixinMessageItem(ctx, baseURL, token, recipient, contextToken, notification.DedupeKey, item); err != nil {
			return a.failed(notification, err.Error(), "retryable")
		}
	}
	now := time.Now().UTC()
	return Result{
		DeliveryID:     app.NewID("rdel"),
		Channel:        a.channel,
		Provider:       weixinproto.ProviderName(a.cfg.Provider),
		Recipient:      redactRecipient(recipient),
		Status:         "sent",
		ProviderStatus: "sent",
		RetryState:     "none",
		SentAt:         now,
	}, nil
}

func (a *WeixinAdapter) SendFile(ctx context.Context, notification Notification) (Result, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(notification.BaseURL), "/")
	if baseURL == "" {
		baseURL = strings.TrimRight(strings.TrimSpace(a.cfg.BaseURL), "/")
	}
	if baseURL == "" {
		return a.failed(notification, "openclaw-weixin baseUrl is not configured", "blocked")
	}
	token := strings.TrimSpace(a.cfg.Token)
	if token == "" && strings.TrimSpace(notification.CredentialRef) != "" && a.store != nil {
		if secret, ok := a.store.GetCredentialSecret(strings.TrimSpace(notification.CredentialRef)); ok {
			token = strings.TrimSpace(secret.Value)
		}
	}
	if token == "" {
		return a.failed(notification, "openclaw-weixin token is not configured", "blocked")
	}
	recipient := strings.TrimSpace(notification.Recipient)
	if recipient == "" {
		return a.failed(notification, "weixin recipient binding is not configured", "blocked")
	}
	contextToken := strings.TrimSpace(notification.RecipientBinding)
	if contextToken == "" {
		return a.failed(notification, "weixin context token is not configured", "blocked")
	}
	filePath := strings.TrimSpace(notification.FilePath)
	if filePath == "" {
		return a.failed(notification, "weixin file path is required", "blocked")
	}
	raw, err := os.ReadFile(filePath)
	if err != nil {
		return a.failed(notification, err.Error(), "blocked")
	}
	if len(raw) == 0 {
		return a.failed(notification, "weixin file is empty", "blocked")
	}
	if len(raw) > maxWeixinOutboundFileBytes {
		return a.failed(notification, fmt.Sprintf("weixin file exceeds limit: %d bytes", len(raw)), "blocked")
	}
	fileName := strings.TrimSpace(notification.FileName)
	if fileName == "" {
		fileName = filepathBase(filePath)
	}
	if fileName == "" {
		fileName = "file.bin"
	}
	uploaded, err := a.uploadMediaToCDN(ctx, baseURL, token, recipient, raw, 3)
	if err != nil {
		return a.failed(notification, err.Error(), "retryable")
	}
	items := []map[string]any{}
	if caption := strings.TrimSpace(notification.MessageText); caption != "" {
		items = append(items, map[string]any{
			"type": 1,
			"text_item": map[string]any{
				"text": caption,
			},
		})
	}
	items = append(items, map[string]any{
		"type": 4,
		"file_item": map[string]any{
			"media": map[string]any{
				"encrypt_query_param": uploaded.DownloadEncryptedQueryParam,
				"aes_key":             base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(uploaded.AESKey))),
				"encrypt_type":        1,
			},
			"file_name": fileName,
			"len":       fmt.Sprintf("%d", uploaded.PlaintextSize),
		},
	})
	for _, item := range items {
		if err := a.sendWeixinMessageItem(ctx, baseURL, token, recipient, contextToken, notification.DedupeKey, item); err != nil {
			return a.failed(notification, err.Error(), "retryable")
		}
	}
	now := time.Now().UTC()
	return Result{
		DeliveryID:     app.NewID("rdel"),
		Channel:        a.channel,
		Provider:       weixinproto.ProviderName(a.cfg.Provider),
		Recipient:      redactRecipient(recipient),
		Status:         "sent",
		ProviderStatus: "sent",
		RetryState:     "none",
		SentAt:         now,
	}, nil
}

func (a *WeixinAdapter) sendWeixinMessageItem(ctx context.Context, baseURL, token, recipient, contextToken, runID string, item map[string]any) error {
	payload := map[string]any{
		"msg": map[string]any{
			"from_user_id":  "",
			"to_user_id":    recipient,
			"client_id":     app.NewID("wxmsg"),
			"message_type":  2,
			"message_state": 2,
			"context_token": contextToken,
			"item_list":     []map[string]any{item},
		},
		"base_info": map[string]any{
			"channel_version": weixinproto.ChannelVersion,
			"bot_agent":       "SparkClaw",
		},
	}
	if strings.TrimSpace(runID) != "" {
		payload["msg"].(map[string]any)["run_id"] = strings.TrimSpace(runID)
	}
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/ilink/bot/sendmessage", bytes.NewReader(rawPayload))
	if err != nil {
		return err
	}
	weixinproto.SetHeaders(req, token)
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("provider returned HTTP %d", resp.StatusCode)
	}
	var decoded struct {
		Ret    int    `json:"ret"`
		ErrMsg string `json:"errmsg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err == nil && decoded.Ret != 0 {
		if decoded.ErrMsg != "" {
			return fmt.Errorf("provider returned ret=%d: %s", decoded.Ret, decoded.ErrMsg)
		}
		return fmt.Errorf("provider returned ret=%d", decoded.Ret)
	}
	return nil
}

func (a *WeixinAdapter) SendTyping(ctx context.Context, notification Notification, status TypingStatus) (Result, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(notification.BaseURL), "/")
	if baseURL == "" {
		baseURL = strings.TrimRight(strings.TrimSpace(a.cfg.BaseURL), "/")
	}
	if baseURL == "" {
		return a.failed(notification, "openclaw-weixin baseUrl is not configured", "blocked")
	}
	token := strings.TrimSpace(a.cfg.Token)
	if token == "" && strings.TrimSpace(notification.CredentialRef) != "" && a.store != nil {
		if secret, ok := a.store.GetCredentialSecret(strings.TrimSpace(notification.CredentialRef)); ok {
			token = strings.TrimSpace(secret.Value)
		}
	}
	if token == "" {
		return a.failed(notification, "openclaw-weixin token is not configured", "blocked")
	}
	recipient := strings.TrimSpace(notification.Recipient)
	if recipient == "" {
		return a.failed(notification, "weixin recipient binding is not configured", "blocked")
	}
	if strings.TrimSpace(notification.RecipientBinding) == "" {
		return a.failed(notification, "weixin context token is not configured", "blocked")
	}
	if status == 0 {
		status = TypingStatusTyping
	}
	ticket, err := a.getTypingTicket(ctx, baseURL, token, recipient, strings.TrimSpace(notification.RecipientBinding))
	if err != nil {
		return a.failed(notification, err.Error(), "retryable")
	}
	if strings.TrimSpace(ticket) == "" {
		return a.failed(notification, "weixin typing ticket is not configured", "blocked")
	}
	payload := map[string]any{
		"ilink_user_id": recipient,
		"typing_ticket": ticket,
		"status":        int(status),
		"base_info": map[string]any{
			"channel_version": weixinproto.ChannelVersion,
			"bot_agent":       "SparkClaw",
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return a.failed(notification, err.Error(), "blocked")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/ilink/bot/sendtyping", bytes.NewReader(raw))
	if err != nil {
		return a.failed(notification, err.Error(), "blocked")
	}
	weixinproto.SetHeaders(req, token)
	resp, err := a.client.Do(req)
	if err != nil {
		return a.failed(notification, err.Error(), "retryable")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return a.failed(notification, fmt.Sprintf("provider returned HTTP %d", resp.StatusCode), "retryable")
	}
	var decoded struct {
		Ret    int    `json:"ret"`
		ErrMsg string `json:"errmsg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err == nil && decoded.Ret != 0 {
		message := decoded.ErrMsg
		if message == "" {
			message = fmt.Sprintf("provider returned ret=%d", decoded.Ret)
		}
		return a.failed(notification, message, "retryable")
	}
	now := time.Now().UTC()
	return Result{
		DeliveryID:     app.NewID("wxtyping"),
		Channel:        a.channel,
		Provider:       weixinproto.ProviderName(a.cfg.Provider),
		Recipient:      redactRecipient(recipient),
		Status:         "sent",
		ProviderStatus: resp.Status,
		RetryState:     "none",
		SentAt:         now,
	}, nil
}

func (a *WeixinAdapter) getTypingTicket(ctx context.Context, baseURL, token, recipient, contextToken string) (string, error) {
	payload := map[string]any{
		"ilink_user_id": recipient,
		"context_token": contextToken,
		"base_info": map[string]any{
			"channel_version": weixinproto.ChannelVersion,
			"bot_agent":       "SparkClaw",
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/ilink/bot/getconfig", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	weixinproto.SetHeaders(req, token)
	resp, err := a.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("getconfig returned HTTP %d", resp.StatusCode)
	}
	var decoded struct {
		Ret          int    `json:"ret"`
		ErrMsg       string `json:"errmsg"`
		TypingTicket string `json:"typing_ticket"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return "", err
	}
	if decoded.Ret != 0 {
		message := decoded.ErrMsg
		if message == "" {
			message = fmt.Sprintf("getconfig returned ret=%d", decoded.Ret)
		}
		return "", errors.New(message)
	}
	return strings.TrimSpace(decoded.TypingTicket), nil
}

const (
	maxWeixinOutboundImageBytes = 12 << 20
	maxWeixinOutboundFileBytes  = 50 << 20
)

type uploadedWeixinImage struct {
	DownloadEncryptedQueryParam string
	AESKey                      []byte
	CiphertextSize              int
	PlaintextSize               int
}

func (a *WeixinAdapter) uploadImageToCDN(ctx context.Context, baseURL, token, recipient string, plaintext []byte) (uploadedWeixinImage, error) {
	return a.uploadMediaToCDN(ctx, baseURL, token, recipient, plaintext, 1)
}

func (a *WeixinAdapter) uploadMediaToCDN(ctx context.Context, baseURL, token, recipient string, plaintext []byte, mediaType int) (uploadedWeixinImage, error) {
	key := make([]byte, aes.BlockSize)
	if _, err := rand.Read(key); err != nil {
		return uploadedWeixinImage{}, err
	}
	ciphertext, err := weixinproto.EncryptAESECBPKCS7(plaintext, key)
	if err != nil {
		return uploadedWeixinImage{}, err
	}
	fileKeyBytes := make([]byte, 16)
	if _, err := rand.Read(fileKeyBytes); err != nil {
		return uploadedWeixinImage{}, err
	}
	fileKey := hex.EncodeToString(fileKeyBytes)
	rawMD5 := md5.Sum(plaintext)
	uploadReq := map[string]any{
		"filekey":       fileKey,
		"media_type":    mediaType,
		"to_user_id":    recipient,
		"rawsize":       len(plaintext),
		"rawfilemd5":    hex.EncodeToString(rawMD5[:]),
		"filesize":      len(ciphertext),
		"no_need_thumb": true,
		"aeskey":        hex.EncodeToString(key),
		"base_info": map[string]any{
			"channel_version": weixinproto.ChannelVersion,
			"bot_agent":       "SparkClaw",
		},
	}
	body, err := json.Marshal(uploadReq)
	if err != nil {
		return uploadedWeixinImage{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/ilink/bot/getuploadurl", bytes.NewReader(body))
	if err != nil {
		return uploadedWeixinImage{}, err
	}
	weixinproto.SetHeaders(req, token)
	resp, err := a.client.Do(req)
	if err != nil {
		return uploadedWeixinImage{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return uploadedWeixinImage{}, fmt.Errorf("getuploadurl returned HTTP %d", resp.StatusCode)
	}
	var decoded struct {
		UploadParam   string `json:"upload_param"`
		UploadFullURL string `json:"upload_full_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return uploadedWeixinImage{}, err
	}
	uploadURL := strings.TrimSpace(decoded.UploadFullURL)
	if uploadURL == "" && strings.TrimSpace(decoded.UploadParam) != "" {
		cdnBase := strings.TrimRight(strings.TrimSpace(a.cfg.CDNBaseURL), "/")
		if cdnBase == "" {
			cdnBase = defaultWeixinNotificationCDNBaseURL
		}
		uploadURL = cdnBase + "/upload?encrypted_query_param=" + url.QueryEscape(strings.TrimSpace(decoded.UploadParam)) + "&filekey=" + url.QueryEscape(fileKey)
	}
	if uploadURL == "" {
		return uploadedWeixinImage{}, errors.New("getuploadurl response missing upload_full_url or upload_param")
	}
	cdnReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(ciphertext))
	if err != nil {
		return uploadedWeixinImage{}, err
	}
	cdnReq.Header.Set("Content-Type", "application/octet-stream")
	cdnResp, err := a.client.Do(cdnReq)
	if err != nil {
		return uploadedWeixinImage{}, err
	}
	defer cdnResp.Body.Close()
	if cdnResp.StatusCode != http.StatusOK {
		msg := cdnResp.Header.Get("x-error-message")
		if msg == "" {
			raw, _ := io.ReadAll(io.LimitReader(cdnResp.Body, 1024))
			msg = strings.TrimSpace(string(raw))
		}
		if msg == "" {
			msg = cdnResp.Status
		}
		return uploadedWeixinImage{}, fmt.Errorf("CDN upload failed: %s", msg)
	}
	downloadParam := strings.TrimSpace(cdnResp.Header.Get("x-encrypted-param"))
	if downloadParam == "" {
		return uploadedWeixinImage{}, errors.New("CDN upload response missing x-encrypted-param")
	}
	return uploadedWeixinImage{
		DownloadEncryptedQueryParam: downloadParam,
		AESKey:                      key,
		CiphertextSize:              len(ciphertext),
		PlaintextSize:               len(plaintext),
	}, nil
}

func filepathBase(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	path = strings.TrimRight(strings.ReplaceAll(path, "\\", "/"), "/")
	if path == "" {
		return ""
	}
	idx := strings.LastIndex(path, "/")
	if idx >= 0 {
		return path[idx+1:]
	}
	return path
}

func supportedWeixinOutboundImageType(contentType string) bool {
	switch strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0])) {
	case "image/png", "image/jpeg", "image/jpg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

const defaultWeixinNotificationCDNBaseURL = weixinproto.DefaultCDNBaseURL

func (a *WeixinAdapter) failed(notification Notification, message, retryState string) (Result, error) {
	err := errors.New(message)
	recipient := strings.TrimSpace(notification.Recipient)
	return Result{
		DeliveryID:     app.NewID("rdel"),
		Channel:        a.channel,
		Provider:       weixinproto.ProviderName(a.cfg.Provider),
		Recipient:      redactRecipient(recipient),
		Status:         "failed",
		ProviderStatus: "failed",
		Error:          message,
		RetryState:     retryState,
	}, err
}

func redactRecipient(recipient string) string {
	recipient = strings.TrimSpace(recipient)
	if len([]rune(recipient)) <= 6 {
		return recipient
	}
	runes := []rune(recipient)
	return string(runes[:3]) + "***" + string(runes[len(runes)-2:])
}
