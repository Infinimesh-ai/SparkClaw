package binding

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestWeixinQRAdapterStartAndPoll(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ilink/bot/get_bot_qrcode":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"retcode": 0,
				"data": map[string]any{
					"qrcode_url": "https://example.test/qr",
					"qrcode":     "qr-session",
				},
			})
		case "/ilink/bot/get_qrcode_status":
			if r.URL.Query().Get("qrcode") != "qr-session" {
				t.Fatalf("unexpected qrcode query: %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"retcode": 0,
				"data": map[string]any{
					"status":     "confirmed",
					"bot_token":  "bot-secret",
					"account_id": "account-1",
					"user_id":    "user-1",
					"base_url":   "https://provider.test",
					"nickname":   "测试微信",
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	adapter := NewWeixinQRAdapter("weixin", config.NotificationChannelConfig{
		Provider: "openclaw-weixin-qr",
		BaseURL:  ts.URL,
	})
	started, err := adapter.Start(t.Context(), app.NotificationBinding{Channel: "weixin"}, StartOptions{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if started.Status != "waiting_scan" || started.QRCodeURL == "" || started.ProviderSessionID != "qr-session" {
		t.Fatalf("unexpected started binding: %#v", started)
	}
	if len(started.Scopes) != 2 || started.Scopes[0] != app.BindingScopeReminderSendSelf || started.Scopes[1] != app.BindingScopeMessageSendSelf {
		t.Fatalf("binding did not default to all messaging scopes: %#v", started.Scopes)
	}
	if started.ExpiresAt == nil || started.ExpiresAt.Sub(started.CreatedAt) < 360*24*time.Hour {
		t.Fatalf("binding QR session should stay valid for about one year, got created=%s expires=%v", started.CreatedAt, started.ExpiresAt)
	}
	polled, err := adapter.Poll(t.Context(), started)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if polled.Status != "active" || polled.ExternalUserID != "user-1" || polled.AccountID != "account-1" {
		t.Fatalf("unexpected poll result: %#v", polled)
	}
	if polled.CredentialRef == "" || polled.CredentialRef == "bot-secret" {
		t.Fatalf("credential ref should not be raw empty/plain token: %#v", polled)
	}
}

func TestTelegramAdapterVerifiesBeforeSealing(t *testing.T) {
	const token = "123456789:AA-verified-token-value"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bot"+token+"/getMe" {
			t.Fatalf("unexpected Telegram path %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"id":       123456789,
				"is_bot":   true,
				"username": "sparkclaw_test_bot",
			},
		})
	}))
	defer server.Close()
	st := store.NewMemoryStore()
	vault := credential.New(st, credential.Options{Key: strings.Repeat("k", 32)})
	adapter := NewTelegramAdapter("telegram", config.NotificationChannelConfig{
		Enabled:  true,
		Provider: "telegram-bot-api",
		BaseURL:  server.URL,
	}, vault)
	started, err := adapter.Start(t.Context(), app.NotificationBinding{OwnerID: app.DefaultOwnerID}, StartOptions{CredentialSecret: token})
	if err != nil {
		t.Fatal(err)
	}
	if started.Status != "active" || started.CredentialRef == "" || started.ExternalUserID != "" || started.ExternalChatID != "" || started.DefaultForChannel {
		t.Fatalf("unexpected Telegram binding: %#v", started)
	}
	if started.QRCodeURL != "" || started.QRCodeImage != "" || started.ProviderState != "" || started.ExpiresAt != nil || started.ContextToken != "" {
		t.Fatalf("Telegram bot verification should not create QR or challenge state: %#v", started)
	}
	stored, ok, err := st.GetCredentialSecret(t.Context(), started.CredentialRef)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || strings.Contains(stored.Value, token) || stored.Value == token {
		t.Fatalf("Telegram token reached store in plaintext: %#v ok=%v", stored, ok)
	}
	opened, err := vault.Open(t.Context(), started.CredentialRef)
	if err != nil || string(opened) != token {
		t.Fatalf("sealed token did not round trip: %q err=%v", opened, err)
	}
}

func TestTelegramAdapterDoesNotPersistRejectedToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": 401, "description": "Unauthorized"})
	}))
	defer server.Close()
	st := store.NewMemoryStore()
	vault := credential.New(st, credential.Options{Key: strings.Repeat("q", 32)})
	adapter := NewTelegramAdapter("telegram", config.NotificationChannelConfig{Enabled: true, BaseURL: server.URL}, vault)
	_, err := adapter.Start(t.Context(), app.NotificationBinding{}, StartOptions{CredentialSecret: "123456789:AA-rejected-token-value"})
	var bindingErr *BindingError
	if !errors.As(err, &bindingErr) || bindingErr.Code != CodeInvalidBotToken {
		t.Fatalf("unexpected rejected-token error: %v", err)
	}
	if secrets := st.ListNotificationBindings("telegram", ""); len(secrets) != 0 {
		t.Fatalf("rejected token created binding state: %#v", secrets)
	}
}

func TestTelegramAdapterClassifiesVerificationFailures(t *testing.T) {
	const token = "123456789:AA-verification-failure-token"
	tests := []struct {
		name     string
		status   int
		body     string
		wantCode string
	}{
		{name: "malformed token", status: http.StatusBadRequest, body: `{"ok":false,"error_code":400,"description":"Bad Request"}`, wantCode: CodeInvalidBotToken},
		{name: "rate limited", status: http.StatusTooManyRequests, body: `{"ok":false,"error_code":429,"description":"Too Many Requests"}`, wantCode: CodeTelegramRateLimited},
		{name: "service unavailable HTML", status: http.StatusServiceUnavailable, body: "upstream unavailable", wantCode: CodeTelegramUnavailable},
		{name: "unexpected success response", status: http.StatusOK, body: "not-json", wantCode: CodeTelegramVerifyFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			adapter := newTestTelegramAdapter(server.URL)
			_, err := adapter.Start(t.Context(), app.NotificationBinding{}, StartOptions{CredentialSecret: token})
			var bindingErr *BindingError
			if !errors.As(err, &bindingErr) || bindingErr.Code != tt.wantCode {
				t.Fatalf("verification error = %#v, want code %q", err, tt.wantCode)
			}
		})
	}
}

func TestTelegramAdapterClassifiesTransportFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	baseURL := server.URL
	server.Close()

	adapter := newTestTelegramAdapter(baseURL)
	_, err := adapter.Start(t.Context(), app.NotificationBinding{}, StartOptions{CredentialSecret: "123456789:AA-transport-failure-token"})
	var bindingErr *BindingError
	if !errors.As(err, &bindingErr) || bindingErr.Code != CodeTelegramUnreachable {
		t.Fatalf("transport error = %#v, want code %q", err, CodeTelegramUnreachable)
	}
}

func TestTelegramBindingErrorRetryability(t *testing.T) {
	for _, code := range []string{CodeTelegramRateLimited, CodeTelegramUnavailable, CodeTelegramUnreachable} {
		if !(&BindingError{Code: code}).Retryable() {
			t.Errorf("binding error %q should be retryable", code)
		}
	}
	for _, code := range []string{CodeInvalidBotToken, CodeTelegramVerifyFailed} {
		if (&BindingError{Code: code}).Retryable() {
			t.Errorf("binding error %q should not be retryable", code)
		}
	}
}

func newTestTelegramAdapter(baseURL string) *TelegramAdapter {
	st := store.NewMemoryStore()
	vault := credential.New(st, credential.Options{Key: strings.Repeat("t", 32)})
	return NewTelegramAdapter("telegram", config.NotificationChannelConfig{Enabled: true, BaseURL: baseURL}, vault)
}

func TestRouterTelegramCapabilityAllowsMultipleBindings(t *testing.T) {
	cfg := config.Default()
	channel := cfg.Tools.Notifications.Channels["telegram"]
	channel.Enabled = true
	cfg.Tools.Notifications.Channels["telegram"] = channel
	st := store.NewMemoryStore()
	vault := credential.New(st, credential.Options{Key: strings.Repeat("v", 32)})
	router := NewRouter(cfg, vault)
	capability := router.Capability("telegram", nil)
	if !capability.Available || !capability.OperatorEnabled || !capability.Startable || capability.BindingStatus != "unbound" || capability.DisabledReason != "" {
		t.Fatalf("unexpected default capability: %#v", capability)
	}

	pending := app.NotificationBinding{Channel: "telegram", Status: "waiting_confirm", UpdatedAt: time.Now().UTC()}
	capability = router.Capability("telegram", []app.NotificationBinding{pending})
	if !capability.Startable || capability.BindingStatus != "waiting_confirm" || capability.DisabledReason != "" {
		t.Fatalf("pending binding capability mismatch: %#v", capability)
	}

	active := app.NotificationBinding{Channel: "telegram", Status: "active", UpdatedAt: time.Now().UTC().Add(time.Second)}
	capability = router.Capability("telegram", []app.NotificationBinding{pending, active})
	if !capability.Startable || capability.BindingStatus != "active" || capability.DisabledReason != "" {
		t.Fatalf("active binding capability mismatch: %#v", capability)
	}

	channel = cfg.Tools.Notifications.Channels["telegram"]
	channel.Enabled = false
	cfg.Tools.Notifications.Channels["telegram"] = channel
	capability = NewRouter(cfg, vault).Capability("telegram", nil)
	if capability.OperatorEnabled || capability.DisabledReason != CodeUserDisabled {
		t.Fatalf("user opt-in mismatch: %#v", capability)
	}

	missingKeyCfg := config.Default()
	channel = missingKeyCfg.Tools.Notifications.Channels["telegram"]
	channel.Enabled = true
	missingKeyCfg.Tools.Notifications.Channels["telegram"] = channel
	capability = NewRouter(missingKeyCfg, credential.New(st, credential.Options{})).Capability("telegram", nil)
	if capability.Startable || capability.DisabledReason != credential.CodeKeyUnavailable {
		t.Fatalf("missing credential key mismatch: %#v", capability)
	}
}

func TestNormalizeWeixinLoginStatusScannedIntermediateStates(t *testing.T) {
	cases := map[string]string{
		"scaned":              "waiting_confirm",
		"scanned":             "waiting_confirm",
		"scaned_but_redirect": "waiting_confirm",
		"need_verifycode":     "waiting_confirm",
		"verify_code_blocked": "failed",
		"binded_redirect":     "failed",
	}
	for input, want := range cases {
		if got := normalizeWeixinLoginStatus(input); got != want {
			t.Fatalf("normalizeWeixinLoginStatus(%q) = %q, want %q", input, got, want)
		}
	}
}
