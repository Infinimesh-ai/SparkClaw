package binding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

type StartRequest struct {
	OwnerID string
	Channel string
	Scopes  []string
}

type PollResult struct {
	Status           string
	DisplayName      string
	ExternalUserID   string
	AccountID        string
	CredentialRef    string
	CredentialKind   string
	CredentialSecret string
	BaseURL          string
	LastError        string
}

type Adapter interface {
	Start(ctx context.Context, binding app.NotificationBinding) (app.NotificationBinding, error)
	Poll(ctx context.Context, binding app.NotificationBinding) (PollResult, error)
	Cancel(ctx context.Context, binding app.NotificationBinding) error
}

type Router struct {
	adapters map[string]Adapter
}

const bindingSessionTTL = 365 * 24 * time.Hour

func NewRouter(cfg config.Config) Router {
	adapters := map[string]Adapter{}
	for channel, channelCfg := range cfg.Tools.Notifications.Channels {
		channel = strings.ToLower(strings.TrimSpace(channel))
		if channel == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(channelCfg.Provider)) {
		case "openclaw-weixin-qr", "openclaw-weixin-login-qr":
			adapters[channel] = NewWeixinQRAdapter(channel, channelCfg)
		default:
			adapters[channel] = NewManualWeixinAdapter(channel, channelCfg)
		}
	}
	return Router{adapters: adapters}
}

func (r Router) Start(ctx context.Context, binding app.NotificationBinding) (app.NotificationBinding, error) {
	adapter, ok := r.adapters[strings.ToLower(strings.TrimSpace(binding.Channel))]
	if !ok {
		return app.NotificationBinding{}, errors.New("binding channel is not configured")
	}
	return adapter.Start(ctx, binding)
}

func (r Router) Poll(ctx context.Context, binding app.NotificationBinding) (PollResult, error) {
	adapter, ok := r.adapters[strings.ToLower(strings.TrimSpace(binding.Channel))]
	if !ok {
		return PollResult{Status: "failed", LastError: "binding channel is not configured"}, errors.New("binding channel is not configured")
	}
	return adapter.Poll(ctx, binding)
}

func (r Router) Cancel(ctx context.Context, binding app.NotificationBinding) error {
	adapter, ok := r.adapters[strings.ToLower(strings.TrimSpace(binding.Channel))]
	if !ok {
		return nil
	}
	return adapter.Cancel(ctx, binding)
}

type ManualWeixinAdapter struct {
	channel string
	cfg     config.NotificationChannelConfig
}

func NewManualWeixinAdapter(channel string, cfg config.NotificationChannelConfig) *ManualWeixinAdapter {
	return &ManualWeixinAdapter{channel: channel, cfg: cfg}
}

func (a *ManualWeixinAdapter) Start(ctx context.Context, binding app.NotificationBinding) (app.NotificationBinding, error) {
	_ = ctx
	now := time.Now().UTC()
	if binding.ID == "" {
		binding.ID = app.NewID("bind")
	}
	if binding.Provider == "" {
		binding.Provider = providerName(a.cfg.Provider)
	}
	if binding.Channel == "" {
		binding.Channel = a.channel
	}
	if binding.CreatedAt.IsZero() {
		binding.CreatedAt = now
	}
	binding.UpdatedAt = now
	expires := now.Add(bindingSessionTTL)
	binding.ExpiresAt = &expires
	binding.Status = "waiting_scan"
	binding.QRCodeURL = "sparkclaw://notification-bindings/" + binding.ID + "/manual-weixin"
	binding.Scopes = normalizeScopes(binding.Scopes)
	return binding, nil
}

func (a *ManualWeixinAdapter) Poll(ctx context.Context, binding app.NotificationBinding) (PollResult, error) {
	_ = ctx
	if binding.ExpiresAt != nil && time.Now().UTC().After(*binding.ExpiresAt) && binding.Status != "active" {
		return PollResult{Status: "expired", LastError: "binding session expired"}, nil
	}
	if strings.TrimSpace(a.cfg.Recipient) == "" || strings.TrimSpace(a.cfg.Token) == "" {
		return PollResult{Status: binding.Status}, nil
	}
	return PollResult{
		Status:         "active",
		DisplayName:    "Weixin notification",
		ExternalUserID: strings.TrimSpace(a.cfg.Recipient),
		AccountID:      strings.TrimSpace(a.cfg.Recipient),
		CredentialRef:  "config:tools.notifications.channels." + a.channel + ".token",
		BaseURL:        strings.TrimSpace(a.cfg.BaseURL),
	}, nil
}

func (a *ManualWeixinAdapter) Cancel(ctx context.Context, binding app.NotificationBinding) error {
	_ = ctx
	_ = binding
	return nil
}

func providerName(provider string) string {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return "openclaw-weixin-compatible"
	}
	return provider
}

func normalizeScopes(scopes []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" || seen[scope] {
			continue
		}
		seen[scope] = true
		out = append(out, scope)
	}
	if len(out) == 0 {
		out = append(out, "reminder_send_self")
	}
	return out
}

type WeixinQRAdapter struct {
	channel string
	cfg     config.NotificationChannelConfig
	client  *http.Client
}

func NewWeixinQRAdapter(channel string, cfg config.NotificationChannelConfig) *WeixinQRAdapter {
	return &WeixinQRAdapter{
		channel: channel,
		cfg:     cfg,
		client:  &http.Client{Timeout: 35 * time.Second},
	}
}

func (a *WeixinQRAdapter) Start(ctx context.Context, binding app.NotificationBinding) (app.NotificationBinding, error) {
	endpoint := strings.TrimSpace(a.cfg.BaseURL)
	if endpoint == "" {
		endpoint = "https://ilinkai.weixin.qq.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/ilink/bot/get_bot_qrcode?bot_type=3", nil)
	if err != nil {
		return app.NotificationBinding{}, err
	}
	var decoded struct {
		Retcode   int    `json:"retcode"`
		Ret       int    `json:"ret"`
		QRCodeURL string `json:"qrcode_img_content"`
		QRCode    string `json:"qrcode"`
		Data      struct {
			QRCodeURL  string `json:"qrcode_url"`
			QRCode     string `json:"qrcode"`
			SessionKey string `json:"session_key"`
		} `json:"data"`
		Message string `json:"message"`
	}
	if err := a.doJSON(req, &decoded); err != nil {
		return app.NotificationBinding{}, err
	}
	if decoded.Retcode != 0 && decoded.Ret != 0 {
		return app.NotificationBinding{}, fmt.Errorf("weixin qr start failed: %s", decoded.Message)
	}
	qr := strings.TrimSpace(decoded.Data.QRCode)
	if qr == "" {
		qr = strings.TrimSpace(decoded.QRCode)
	}
	if qr == "" {
		qr = strings.TrimSpace(decoded.Data.SessionKey)
	}
	if qr == "" {
		return app.NotificationBinding{}, errors.New("weixin qr start returned no qrcode/session key")
	}
	now := time.Now().UTC()
	if binding.ID == "" {
		binding.ID = app.NewID("bind")
	}
	binding.Channel = valueOr(binding.Channel, a.channel)
	binding.Provider = providerName(a.cfg.Provider)
	binding.Status = "waiting_scan"
	binding.QRCodeURL = strings.TrimSpace(decoded.Data.QRCodeURL)
	if binding.QRCodeURL == "" {
		binding.QRCodeURL = strings.TrimSpace(decoded.QRCodeURL)
	}
	binding.ProviderSessionID = qr
	binding.ProviderState = qr
	binding.Scopes = normalizeScopes(binding.Scopes)
	binding.UpdatedAt = now
	if binding.CreatedAt.IsZero() {
		binding.CreatedAt = now
	}
	expires := now.Add(bindingSessionTTL)
	binding.ExpiresAt = &expires
	return binding, nil
}

func (a *WeixinQRAdapter) Poll(ctx context.Context, binding app.NotificationBinding) (PollResult, error) {
	if binding.ExpiresAt != nil && time.Now().UTC().After(*binding.ExpiresAt) && binding.Status != "active" {
		return PollResult{Status: "expired", LastError: "binding session expired"}, nil
	}
	session := strings.TrimSpace(binding.ProviderSessionID)
	if session == "" {
		session = strings.TrimSpace(binding.ProviderState)
	}
	if session == "" {
		return PollResult{Status: "failed", LastError: "missing provider session id"}, errors.New("missing provider session id")
	}
	endpoint := strings.TrimSpace(a.cfg.BaseURL)
	if endpoint == "" {
		endpoint = "https://ilinkai.weixin.qq.com"
	}
	pollURL := strings.TrimRight(endpoint, "/") + "/ilink/bot/get_qrcode_status?qrcode=" + url.QueryEscape(session)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pollURL, nil)
	if err != nil {
		return PollResult{}, err
	}
	var decoded struct {
		Retcode     int    `json:"retcode"`
		Ret         int    `json:"ret"`
		Status      string `json:"status"`
		BotToken    string `json:"bot_token"`
		AccountID   string `json:"account_id"`
		IlinkBotID  string `json:"ilink_bot_id"`
		UserID      string `json:"user_id"`
		IlinkUserID string `json:"ilink_user_id"`
		BaseURL     string `json:"base_url"`
		BaseURLAlt  string `json:"baseurl"`
		Nickname    string `json:"nickname"`
		Data        struct {
			Status      string `json:"status"`
			BotToken    string `json:"bot_token"`
			AccountID   string `json:"account_id"`
			IlinkBotID  string `json:"ilink_bot_id"`
			UserID      string `json:"user_id"`
			IlinkUserID string `json:"ilink_user_id"`
			BaseURL     string `json:"base_url"`
			BaseURLAlt  string `json:"baseurl"`
			Nickname    string `json:"nickname"`
		} `json:"data"`
		Message string `json:"message"`
	}
	if err := a.doJSON(req, &decoded); err != nil {
		return PollResult{}, err
	}
	if decoded.Retcode != 0 && decoded.Ret != 0 {
		return PollResult{Status: binding.Status, LastError: decoded.Message}, nil
	}
	rawStatus := strings.TrimSpace(decoded.Data.Status)
	if rawStatus == "" {
		rawStatus = strings.TrimSpace(decoded.Status)
	}
	status := normalizeWeixinLoginStatus(rawStatus)
	if status != "active" {
		return PollResult{Status: status}, nil
	}
	userID := strings.TrimSpace(decoded.Data.UserID)
	if userID == "" {
		userID = strings.TrimSpace(decoded.UserID)
	}
	if userID == "" {
		userID = strings.TrimSpace(decoded.Data.IlinkUserID)
	}
	if userID == "" {
		userID = strings.TrimSpace(decoded.IlinkUserID)
	}
	if userID == "" {
		userID = strings.TrimSpace(decoded.Data.AccountID)
	}
	accountID := strings.TrimSpace(decoded.Data.AccountID)
	if accountID == "" {
		accountID = strings.TrimSpace(decoded.AccountID)
	}
	if accountID == "" {
		accountID = strings.TrimSpace(decoded.Data.IlinkBotID)
	}
	if accountID == "" {
		accountID = strings.TrimSpace(decoded.IlinkBotID)
	}
	botToken := strings.TrimSpace(decoded.Data.BotToken)
	if botToken == "" {
		botToken = strings.TrimSpace(decoded.BotToken)
	}
	baseURL := strings.TrimSpace(decoded.Data.BaseURL)
	if baseURL == "" {
		baseURL = strings.TrimSpace(decoded.BaseURL)
	}
	if baseURL == "" {
		baseURL = strings.TrimSpace(decoded.Data.BaseURLAlt)
	}
	if baseURL == "" {
		baseURL = strings.TrimSpace(decoded.BaseURLAlt)
	}
	nickname := strings.TrimSpace(decoded.Data.Nickname)
	if nickname == "" {
		nickname = strings.TrimSpace(decoded.Nickname)
	}
	credentialRef := "provider:openclaw-weixin-qr:" + binding.ID
	return PollResult{
		Status:           "active",
		DisplayName:      nickname,
		ExternalUserID:   userID,
		AccountID:        accountID,
		CredentialRef:    credentialRef,
		CredentialKind:   "openclaw-weixin-bot-token",
		CredentialSecret: botToken,
		BaseURL:          baseURL,
	}, nil
}

func (a *WeixinQRAdapter) Cancel(ctx context.Context, binding app.NotificationBinding) error {
	_ = ctx
	_ = binding
	return nil
}

func (a *WeixinQRAdapter) doJSON(req *http.Request, out any) error {
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("weixin qr provider returned HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func normalizeWeixinLoginStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "confirmed", "success", "login_success", "logged_in", "active":
		return "active"
	case "scanned", "scaned", "scaned_but_redirect", "need_verifycode", "waiting_confirm", "confirming":
		return "waiting_confirm"
	case "expired", "timeout":
		return "expired"
	case "failed", "error", "verify_code_blocked":
		return "failed"
	case "binded_redirect":
		return "failed"
	case "wait", "waiting", "":
		return "waiting_scan"
	default:
		return "waiting_scan"
	}
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
