package notification

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestWeixinSendRequiresExplicitRecipientContextAndCredential(t *testing.T) {
	var gotAuth string
	var gotRecipient string
	var gotContextToken string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var payload struct {
			Msg struct {
				ToUserID     string `json:"to_user_id"`
				ContextToken string `json:"context_token"`
			} `json:"msg"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		gotRecipient = payload.Msg.ToUserID
		gotContextToken = payload.Msg.ContextToken
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ret":0}`))
	}))
	defer ts.Close()

	st := store.NewMemoryStore()
	st.SaveCredentialSecret(app.CredentialSecret{
		Ref:   "provider:openclaw-weixin-qr:bind_1",
		Kind:  "openclaw-weixin-bot-token",
		Value: "bot-secret",
	})
	st.SaveNotificationBinding(app.NotificationBinding{
		ID:                "bind_1",
		OwnerID:           app.DefaultOwnerID,
		Channel:           "weixin",
		Provider:          "openclaw-weixin-qr",
		Status:            "active",
		ExternalUserID:    "wx-user-1",
		CredentialRef:     "provider:openclaw-weixin-qr:bind_1",
		ContextToken:      "ctx-token",
		DefaultForChannel: true,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	})
	cfg := config.Default()
	cfg.Tools.Notifications.Channels["weixin"] = config.NotificationChannelConfig{
		Enabled:  true,
		Provider: "openclaw-weixin-qr",
		BaseURL:  ts.URL,
	}
	result, err := NewWeixinAdapter("weixin", cfg.Tools.Notifications.Channels["weixin"], st).Send(t.Context(), Notification{
		Channel:          "weixin",
		Recipient:        "wx-user-1",
		RecipientBinding: "ctx-token",
		CredentialRef:    "provider:openclaw-weixin-qr:bind_1",
		BaseURL:          ts.URL,
		MessageText:      "提醒内容",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if result.Status != "sent" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if gotAuth != "Bearer bot-secret" {
		t.Fatalf("expected credential secret token, got %q", gotAuth)
	}
	if gotRecipient != "wx-user-1" {
		t.Fatalf("expected bound recipient, got %q", gotRecipient)
	}
	if gotContextToken != "ctx-token" {
		t.Fatalf("expected context token, got %q", gotContextToken)
	}
}

func TestWeixinProviderPreflightsAudioFallbackBeforeExternalSend(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	st := store.NewMemoryStore()
	binding := st.SaveNotificationBinding(app.NotificationBinding{ID: "bind_audio", Channel: "weixin", Status: "active", ExternalUserID: "wx-user", ContextToken: "ctx", BaseURL: server.URL, Scopes: []string{app.BindingScopeMessageSendSelf}})
	provider := NewWeixinAdapter("weixin", config.NotificationChannelConfig{Enabled: true, BaseURL: server.URL, Token: "token"}, st)
	providers := delivery.NewProviderRegistry()
	if err := providers.Register(provider); err != nil {
		t.Fatal(err)
	}
	request := app.DeliveryRequest{
		SchemaVersion: app.DeliveryRequestSchemaVersion, ID: "del_audio", IdempotencyKey: "audio", OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID,
		Authorization: app.MessageAuthorization{PrincipalID: app.DefaultOwnerID}, Origin: app.DeliveryOriginWebDirect, Target: app.EndpointID(binding.ID),
		Content: app.MessageContent{Parts: []app.MessagePart{
			{ID: "text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: "do not partially send"},
			{ID: "voice", Kind: app.MessagePartAudio, Disposition: app.MessageDispositionVoiceNote, ArtifactID: "voice"},
		}},
	}
	_, err := providers.Deliver(t.Context(), app.MessageEndpoint{ID: app.EndpointID(binding.ID), Kind: app.EndpointKindThirdPartyDevice, ProviderKey: "weixin", BindingRef: binding.ID}, request)
	if err == nil || delivery.ErrorCode(err) != delivery.CodeArtifactInvalid {
		t.Fatalf("expected missing audio artifact to fail preflight, got %v", err)
	}
	if calls != 0 {
		t.Fatalf("provider sent a partial payload before capability failure: calls=%d", calls)
	}
}

func TestWeixinSendDoesNotUseDefaultBindingWhenRecipientMissing(t *testing.T) {
	st := store.NewMemoryStore()
	st.SaveCredentialSecret(app.CredentialSecret{
		Ref:   "provider:openclaw-weixin-qr:bind_1",
		Kind:  "openclaw-weixin-bot-token",
		Value: "bot-secret",
	})
	st.SaveNotificationBinding(app.NotificationBinding{
		ID:                "bind_1",
		OwnerID:           app.DefaultOwnerID,
		Channel:           "weixin",
		Provider:          "openclaw-weixin-qr",
		Status:            "active",
		ExternalUserID:    "wx-user-1",
		CredentialRef:     "provider:openclaw-weixin-qr:bind_1",
		DefaultForChannel: true,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	})
	cfg := config.Default()
	cfg.Tools.Notifications.Channels["weixin"] = config.NotificationChannelConfig{
		Enabled:  true,
		Provider: "openclaw-weixin-qr",
		BaseURL:  "http://127.0.0.1:1",
	}
	result, err := NewWeixinAdapter("weixin", cfg.Tools.Notifications.Channels["weixin"], st).Send(t.Context(), Notification{
		Channel:          "weixin",
		RecipientBinding: "ctx-token",
		CredentialRef:    "provider:openclaw-weixin-qr:bind_1",
		BaseURL:          "http://127.0.0.1:1",
		MessageText:      "提醒内容",
	})
	if err == nil {
		t.Fatal("expected recipient error")
	}
	if result.Status != "failed" || result.RetryState != "blocked" || result.Error != "weixin recipient binding is not configured" {
		t.Fatalf("unexpected result: %#v err=%v", result, err)
	}
}
