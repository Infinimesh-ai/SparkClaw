package binding

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
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
	started, err := adapter.Start(t.Context(), app.NotificationBinding{Channel: "weixin"})
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
