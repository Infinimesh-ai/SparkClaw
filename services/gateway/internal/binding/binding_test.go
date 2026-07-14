package binding

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	if started.Status != "waiting_confirm" || started.CredentialRef == "" || started.ExternalUserID != "" || started.ExternalChatID != "" {
		t.Fatalf("unexpected Telegram binding: %#v", started)
	}
	activation, err := url.Parse(started.QRCodeURL)
	if err != nil {
		t.Fatal(err)
	}
	challenge := activation.Query().Get("start")
	if activation.Host != "t.me" || challenge == "" || strings.Contains(started.ProviderState, challenge) || !strings.HasPrefix(started.ProviderState, "sha256:") {
		t.Fatalf("activation challenge was not isolated: binding=%#v", started)
	}
	stored, ok := st.GetCredentialSecret(started.CredentialRef)
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

func TestRouterTelegramCapabilitySeparatesAvailabilityAndState(t *testing.T) {
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
	if capability.Startable || capability.BindingStatus != "waiting_confirm" || capability.DisabledReason != CodeBindingInProgress {
		t.Fatalf("pending binding capability mismatch: %#v", capability)
	}

	channel = cfg.Tools.Notifications.Channels["telegram"]
	channel.Enabled = false
	cfg.Tools.Notifications.Channels["telegram"] = channel
	capability = NewRouter(cfg, vault).Capability("telegram", nil)
	if capability.OperatorEnabled || capability.DisabledReason != CodeOperatorDisabled {
		t.Fatalf("operator kill switch mismatch: %#v", capability)
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
