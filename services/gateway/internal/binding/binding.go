package binding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/telegram"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/weixinproto"
)

type StartRequest struct {
	OwnerID string
	Channel string
	Scopes  []string
}

type StartOptions struct {
	CredentialSecret string
}

const (
	CodeConnectorUnavailable = "connector_unavailable"
	CodeOperatorDisabled     = "operator_disabled"
	CodeUserDisabled         = "connector_disabled"
	CodeBindingInProgress    = "binding_in_progress"
	CodeBindingActive        = "binding_active"
	CodeInvalidBotToken      = "invalid_bot_token"
	CodeTelegramRateLimited  = "telegram_rate_limited"
	CodeTelegramUnavailable  = "telegram_unavailable"
	CodeTelegramUnreachable  = "telegram_unreachable"
	CodeTelegramVerifyFailed = "telegram_verification_failed"
)

type BindingError struct {
	Code string
}

func (e *BindingError) Error() string {
	switch e.Code {
	case CodeConnectorUnavailable:
		return "connector is unavailable"
	case CodeOperatorDisabled:
		return "connector is disabled by the operator"
	case CodeUserDisabled:
		return "connector is disabled"
	case CodeBindingInProgress:
		return "a binding is already waiting for confirmation"
	case CodeBindingActive:
		return "an active binding already exists"
	case CodeInvalidBotToken:
		return "Telegram rejected the bot token; copy a fresh token from BotFather and try again"
	case CodeTelegramRateLimited:
		return "Telegram rate-limited bot token verification; try again later"
	case CodeTelegramUnavailable:
		return "Telegram Bot API is temporarily unavailable; try again later"
	case CodeTelegramUnreachable:
		return "Telegram Bot API could not be reached; check the network or proxy and try again"
	case CodeTelegramVerifyFailed:
		return "Telegram returned an unexpected response while checking the bot token"
	default:
		return "notification binding failed"
	}
}

func (e *BindingError) ErrorCode() string {
	if e == nil {
		return ""
	}
	return e.Code
}

func (e *BindingError) Retryable() bool {
	if e == nil {
		return false
	}
	return e.Code == CodeTelegramRateLimited || e.Code == CodeTelegramUnavailable || e.Code == CodeTelegramUnreachable
}

type ConnectorCapability struct {
	Channel         string `json:"channel"`
	Provider        string `json:"provider"`
	Available       bool   `json:"available"`
	OperatorEnabled bool   `json:"operator_enabled"`
	BindingStatus   string `json:"binding_status"`
	Startable       bool   `json:"startable"`
	DisabledReason  string `json:"disabled_reason"`
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

type AdapterPolicy struct {
	ExclusiveBinding bool
}

type Adapter interface {
	Availability() error
	Policy() AdapterPolicy
	Start(ctx context.Context, binding app.NotificationBinding, options StartOptions) (app.NotificationBinding, error)
	Poll(ctx context.Context, binding app.NotificationBinding) (PollResult, error)
	Cancel(ctx context.Context, binding app.NotificationBinding) error
}

type Router struct {
	adapters       map[string]Adapter
	configs        map[string]config.NotificationChannelConfig
	channelEnabled func(ownerID, channel string) bool
}

const bindingSessionTTL = 365 * 24 * time.Hour

func NewRouter(cfg config.Config, vaults ...credential.CredentialVault) Router {
	router := NewBaseRouter(cfg)
	var vault credential.CredentialVault
	if len(vaults) > 0 {
		vault = vaults[0]
	}
	for channel, channelCfg := range cfg.Tools.Notifications.Channels {
		channel = strings.ToLower(strings.TrimSpace(channel))
		if channel == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(channelCfg.Provider)) {
		case "telegram-bot-api":
			router = router.WithAdapter(channel, NewTelegramAdapter(channel, channelCfg, vault))
		default:
			router = router.WithAdapter(channel, NewWeixinAdapter(channel, channelCfg))
		}
	}
	return router
}

// NewWeixinAdapter picks the weixin binding adapter for the channel's
// configured provider: the QR login flow for the openclaw QR providers,
// the manual handshake otherwise.
func NewWeixinAdapter(channel string, cfg config.NotificationChannelConfig) Adapter {
	if weixinproto.IsQRLoginProvider(cfg.Provider) {
		return NewWeixinQRAdapter(channel, cfg)
	}
	return NewManualWeixinAdapter(channel, cfg)
}

func NewBaseRouter(cfg config.Config) Router {
	configs := make(map[string]config.NotificationChannelConfig, len(cfg.Tools.Notifications.Channels))
	for channel, channelCfg := range cfg.Tools.Notifications.Channels {
		channel = strings.ToLower(strings.TrimSpace(channel))
		if channel != "" {
			configs[channel] = channelCfg
		}
	}
	return Router{adapters: map[string]Adapter{}, configs: configs}
}

func (r Router) WithAdapter(channel string, adapter Adapter) Router {
	channel = strings.ToLower(strings.TrimSpace(channel))
	if channel != "" && adapter != nil {
		r.adapters[channel] = adapter
	}
	return r
}

func (r Router) WithChannelEnabled(enabled func(ownerID, channel string) bool) Router {
	r.channelEnabled = enabled
	return r
}

func (r Router) Start(ctx context.Context, binding app.NotificationBinding, options ...StartOptions) (app.NotificationBinding, error) {
	channel := strings.ToLower(strings.TrimSpace(binding.Channel))
	adapter, ok := r.adapters[channel]
	if !ok {
		return app.NotificationBinding{}, &BindingError{Code: CodeConnectorUnavailable}
	}
	if r.channelEnabled != nil && !r.channelEnabled(binding.OwnerID, channel) {
		return app.NotificationBinding{}, &BindingError{Code: CodeUserDisabled}
	}
	var startOptions StartOptions
	if len(options) > 0 {
		startOptions = options[0]
	}
	return adapter.Start(ctx, binding, startOptions)
}

func (r Router) Capability(channel string, bindings []app.NotificationBinding) ConnectorCapability {
	return r.CapabilityForOwner(app.DefaultOwnerID, channel, bindings)
}

func (r Router) CapabilityForOwner(ownerID, channel string, bindings []app.NotificationBinding) ConnectorCapability {
	channel = strings.ToLower(strings.TrimSpace(channel))
	cfg, configured := r.configs[channel]
	adapter, available := r.adapters[channel]
	enabled := cfg.Enabled
	if r.channelEnabled != nil {
		enabled = r.channelEnabled(ownerID, channel)
	}
	capability := ConnectorCapability{
		Channel:         channel,
		Provider:        strings.ToLower(strings.TrimSpace(cfg.Provider)),
		Available:       configured && available,
		OperatorEnabled: enabled,
		BindingStatus:   currentBindingStatus(bindings),
	}
	if !capability.Available {
		capability.DisabledReason = CodeConnectorUnavailable
	} else if !capability.OperatorEnabled {
		capability.DisabledReason = CodeUserDisabled
	} else if err := adapter.Availability(); err != nil {
		capability.DisabledReason = availabilityErrorCode(err)
	} else if adapter.Policy().ExclusiveBinding && (capability.BindingStatus == "waiting_confirm" || capability.BindingStatus == "waiting_scan") {
		capability.DisabledReason = CodeBindingInProgress
	} else if adapter.Policy().ExclusiveBinding && capability.BindingStatus == "active" {
		capability.DisabledReason = CodeBindingActive
	} else {
		capability.Startable = true
	}
	return capability
}

func availabilityErrorCode(err error) string {
	var coded interface{ ErrorCode() string }
	if errors.As(err, &coded) && strings.TrimSpace(coded.ErrorCode()) != "" {
		return coded.ErrorCode()
	}
	return CodeConnectorUnavailable
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

type TelegramAdapter struct {
	channel string
	cfg     config.NotificationChannelConfig
	vault   credential.CredentialVault
}

func NewTelegramAdapter(channel string, cfg config.NotificationChannelConfig, vault credential.CredentialVault) *TelegramAdapter {
	return &TelegramAdapter{channel: channel, cfg: cfg, vault: vault}
}

func (a *TelegramAdapter) Availability() error {
	if a.vault == nil {
		return &credential.Error{Code: credential.CodeKeyUnavailable}
	}
	return a.vault.Ready()
}

func (a *TelegramAdapter) Policy() AdapterPolicy {
	return AdapterPolicy{}
}

func (a *TelegramAdapter) Start(ctx context.Context, binding app.NotificationBinding, options StartOptions) (app.NotificationBinding, error) {
	if a.vault == nil {
		return app.NotificationBinding{}, &credential.Error{Code: credential.CodeKeyUnavailable}
	}
	if err := a.vault.Ready(); err != nil {
		return app.NotificationBinding{}, err
	}
	token := []byte(strings.TrimSpace(options.CredentialSecret))
	defer clear(token)
	if !validTelegramToken(token) {
		return app.NotificationBinding{}, &BindingError{Code: CodeInvalidBotToken}
	}
	client := telegram.NewClient(a.cfg.BaseURL, string(token), nil)
	bot, err := client.GetMe(ctx)
	if err != nil {
		return app.NotificationBinding{}, classifyTelegramVerificationError(err)
	}
	if !bot.IsBot || strings.TrimSpace(bot.Username) == "" {
		return app.NotificationBinding{}, &BindingError{Code: CodeInvalidBotToken}
	}
	credentialRef, err := a.vault.Seal(ctx, "telegram-bot-token", token)
	if err != nil {
		return app.NotificationBinding{}, err
	}
	now := time.Now().UTC()
	if binding.ID == "" {
		binding.ID = app.NewID("bind")
	}
	if binding.CreatedAt.IsZero() {
		binding.CreatedAt = now
	}
	binding.Channel = a.channel
	binding.Provider = "telegram-bot-api"
	binding.Status = "active"
	binding.DisplayName = "@" + strings.TrimPrefix(bot.Username, "@")
	binding.AccountID = strconv.FormatInt(bot.ID, 10)
	binding.CredentialRef = credentialRef
	binding.BaseURL = a.cfg.BaseURL
	binding.ProviderState = ""
	binding.QRCodeURL = ""
	binding.QRCodeImage = ""
	binding.ExternalUserID = ""
	binding.ExternalChatID = ""
	binding.ExternalThreadID = ""
	binding.ContextToken = ""
	binding.DefaultForChannel = false
	binding.Scopes = normalizeScopes(binding.Scopes)
	binding.ExpiresAt = nil
	binding.LastError = ""
	binding.UpdatedAt = now
	return binding, nil
}

func classifyTelegramVerificationError(err error) *BindingError {
	var responseErr *telegram.ResponseError
	if errors.As(err, &responseErr) {
		switch {
		case responseErr.StatusCode == http.StatusTooManyRequests:
			return &BindingError{Code: CodeTelegramRateLimited}
		case responseErr.StatusCode == http.StatusRequestTimeout || responseErr.StatusCode >= 500:
			return &BindingError{Code: CodeTelegramUnavailable}
		default:
			return &BindingError{Code: CodeTelegramVerifyFailed}
		}
	}
	var apiErr *telegram.APIError
	if !errors.As(err, &apiErr) {
		return &BindingError{Code: CodeTelegramUnreachable}
	}
	switch {
	case apiErr.Code == http.StatusTooManyRequests:
		return &BindingError{Code: CodeTelegramRateLimited}
	case apiErr.Code == http.StatusRequestTimeout || apiErr.Code >= 500:
		return &BindingError{Code: CodeTelegramUnavailable}
	case apiErr.Code >= 400 && apiErr.Code < 500:
		return &BindingError{Code: CodeInvalidBotToken}
	default:
		return &BindingError{Code: CodeTelegramVerifyFailed}
	}
}

func (a *TelegramAdapter) Poll(ctx context.Context, binding app.NotificationBinding) (PollResult, error) {
	_ = ctx
	return PollResult{Status: binding.Status}, nil
}

func (a *TelegramAdapter) Cancel(ctx context.Context, binding app.NotificationBinding) error {
	_ = ctx
	_ = binding
	return nil
}

type ManualWeixinAdapter struct {
	channel string
	cfg     config.NotificationChannelConfig
}

func NewManualWeixinAdapter(channel string, cfg config.NotificationChannelConfig) *ManualWeixinAdapter {
	return &ManualWeixinAdapter{channel: channel, cfg: cfg}
}

func (a *ManualWeixinAdapter) Availability() error {
	return nil
}

func (a *ManualWeixinAdapter) Policy() AdapterPolicy {
	return AdapterPolicy{}
}

func (a *ManualWeixinAdapter) Start(ctx context.Context, binding app.NotificationBinding, options StartOptions) (app.NotificationBinding, error) {
	_ = ctx
	_ = options
	now := time.Now().UTC()
	if binding.ID == "" {
		binding.ID = app.NewID("bind")
	}
	if binding.Provider == "" {
		binding.Provider = weixinproto.ProviderName(a.cfg.Provider)
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

func normalizeScopes(scopes []string) []string {
	out := app.DefaultMessagingBindingScopes()
	seen := map[string]bool{
		app.BindingScopeReminderSendSelf: true,
		app.BindingScopeMessageSendSelf:  true,
	}
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" || seen[scope] {
			continue
		}
		seen[scope] = true
		out = append(out, scope)
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

func (a *WeixinQRAdapter) Availability() error {
	return nil
}

func (a *WeixinQRAdapter) Policy() AdapterPolicy {
	return AdapterPolicy{}
}

func (a *WeixinQRAdapter) Start(ctx context.Context, binding app.NotificationBinding, options StartOptions) (app.NotificationBinding, error) {
	_ = options
	endpoint := strings.TrimSpace(a.cfg.BaseURL)
	if endpoint == "" {
		endpoint = weixinproto.DefaultBaseURL
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
	binding.Provider = weixinproto.ProviderName(a.cfg.Provider)
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
		endpoint = weixinproto.DefaultBaseURL
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
	credentialRef := "provider:" + weixinproto.QRProvider + ":" + binding.ID
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

func currentBindingStatus(bindings []app.NotificationBinding) string {
	for _, binding := range bindings {
		if binding.Status == "active" {
			return "active"
		}
	}
	pendingStatus := ""
	pendingLatest := time.Time{}
	for _, binding := range bindings {
		if (binding.Status == "waiting_confirm" || binding.Status == "waiting_scan") && !binding.UpdatedAt.Before(pendingLatest) {
			pendingStatus = binding.Status
			pendingLatest = binding.UpdatedAt
		}
	}
	if pendingStatus != "" {
		return pendingStatus
	}
	status := "unbound"
	latest := time.Time{}
	for _, binding := range bindings {
		if !binding.UpdatedAt.Before(latest) {
			status = binding.Status
			latest = binding.UpdatedAt
		}
	}
	return valueOr(status, "unbound")
}

func validTelegramToken(token []byte) bool {
	if len(token) < 16 || len(token) > 256 {
		return false
	}
	parts := strings.SplitN(string(token), ":", 2)
	if len(parts) != 2 || len(parts[1]) < 10 {
		return false
	}
	if _, err := strconv.ParseInt(parts[0], 10, 64); err != nil {
		return false
	}
	return !strings.ContainsAny(string(token), " \t\r\n")
}
