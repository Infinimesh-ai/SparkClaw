package notification

import (
	"context"
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
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type unavailableNotificationCredentialRepository struct {
	err error
}

func (r unavailableNotificationCredentialRepository) SaveCredentialSecret(context.Context, store.CredentialSaveCommand) (app.CredentialSecret, error) {
	return app.CredentialSecret{}, r.err
}

func (r unavailableNotificationCredentialRepository) GetCredentialSecret(context.Context, string) (app.CredentialSecret, bool, error) {
	return app.CredentialSecret{}, false, r.err
}

func (r unavailableNotificationCredentialRepository) DeleteCredentialSecret(context.Context, store.CredentialDeleteCondition) (app.CredentialSecret, error) {
	return app.CredentialSecret{}, r.err
}

func newNotificationTestCredential(t *testing.T, st *store.MemoryStore, bindingID string) (credential.CredentialVault, string) {
	t.Helper()
	vault := credential.New(st, credential.Options{Key: strings.Repeat("n", 32)})
	ref, err := vault.Seal(t.Context(), bindingID, "openclaw-weixin-bot-token", []byte("bot-secret"))
	if err != nil {
		t.Fatal(err)
	}
	return vault, ref
}

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
	vault, credentialRef := newNotificationTestCredential(t, st, "bind_1")
	st.SaveNotificationBinding(app.NotificationBinding{
		ID:                "bind_1",
		OwnerID:           app.DefaultOwnerID,
		Channel:           "weixin",
		Provider:          "openclaw-weixin-qr",
		Status:            "active",
		ExternalUserID:    "wx-user-1",
		CredentialRef:     credentialRef,
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
	result, err := NewWeixinAdapter("weixin", cfg.Tools.Notifications.Channels["weixin"], st, vault).Send(t.Context(), Notification{
		Channel:          "weixin",
		Recipient:        "wx-user-1",
		RecipientBinding: "ctx-token",
		CredentialRef:    credentialRef,
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
	provider := NewWeixinAdapter("weixin", config.NotificationChannelConfig{Enabled: true, BaseURL: server.URL, Token: "token"}, st, nil)
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

func TestWeixinBindingScopeCompatibilityDefaultsToAllMessaging(t *testing.T) {
	for _, scopes := range [][]string{nil, {app.BindingScopeReminderSendSelf}, {app.BindingScopeMessageSendSelf}} {
		if !notificationBindingAllowsScope(scopes, app.BindingScopeMessageSendSelf, app.DeliveryOriginWebDirect) ||
			!notificationBindingAllowsScope(scopes, app.BindingScopeReminderSendSelf, app.DeliveryOriginSchedule) ||
			!notificationBindingAllowsSourceReply(scopes) {
			t.Fatalf("messaging binding did not grant full exchange for scopes %#v", scopes)
		}
	}
	if notificationBindingAllowsScope([]string{"unknown"}, app.BindingScopeMessageSendSelf, app.DeliveryOriginWebDirect) {
		t.Fatal("unknown scope unexpectedly granted messaging authority")
	}
}

func TestWeixinSendDoesNotUseDefaultBindingWhenRecipientMissing(t *testing.T) {
	st := store.NewMemoryStore()
	vault, credentialRef := newNotificationTestCredential(t, st, "bind_1")
	st.SaveNotificationBinding(app.NotificationBinding{
		ID:                "bind_1",
		OwnerID:           app.DefaultOwnerID,
		Channel:           "weixin",
		Provider:          "openclaw-weixin-qr",
		Status:            "active",
		ExternalUserID:    "wx-user-1",
		CredentialRef:     credentialRef,
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
	result, err := NewWeixinAdapter("weixin", cfg.Tools.Notifications.Channels["weixin"], st, vault).Send(t.Context(), Notification{
		Channel:          "weixin",
		RecipientBinding: "ctx-token",
		CredentialRef:    credentialRef,
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

func TestWeixinSendProjectsCredentialFailureWithoutBackendDetails(t *testing.T) {
	const canary = "private credential backend diagnostic"
	repository := unavailableNotificationCredentialRepository{err: &store.StoreError{
		Code: store.StoreErrorUnavailable, Operation: store.OperationCredentialSecretGet, Err: errors.New(canary),
	}}
	vault := credential.New(repository, credential.Options{Key: strings.Repeat("u", 32)})
	result, err := NewWeixinAdapter("weixin", config.NotificationChannelConfig{
		Enabled: true, Provider: "openclaw-weixin-qr", BaseURL: "http://127.0.0.1:1",
	}, store.NewMemoryStore(), vault).Send(t.Context(), Notification{
		Channel: "weixin", Recipient: "wx-user", RecipientBinding: "context",
		CredentialRef: "credential-sensitive-ref", MessageText: "message",
	})
	if err == nil || result.Status != "failed" || result.RetryState != "retryable" || result.Error != "credential is temporarily unavailable" {
		t.Fatalf("unexpected credential failure: result=%#v err=%v", result, err)
	}
	combined := result.Error + err.Error()
	if strings.Contains(combined, canary) || strings.Contains(combined, "credential-sensitive-ref") {
		t.Fatalf("credential failure disclosed backend data: %q", combined)
	}
}
