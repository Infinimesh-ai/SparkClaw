package telegram

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/notification"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestNotificationAdapterUsesBoundEncryptedCredential(t *testing.T) {
	var gotChatID int64
	var gotThreadID int64
	var gotText string
	token := "123456:AA-notification-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/sendMessage") {
			http.NotFound(w, r)
			return
		}
		var payload struct {
			ChatID   int64  `json:"chat_id"`
			ThreadID int64  `json:"message_thread_id"`
			Text     string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		gotChatID, gotThreadID, gotText = payload.ChatID, payload.ThreadID, payload.Text
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1,"chat":{"id":99,"type":"private"},"date":1}}`))
	}))
	defer server.Close()

	st := store.NewMemoryStore()
	vault := credential.New(st, credential.Options{Key: telegramCredentialTestKey(7)})
	ref, err := vault.Seal(t.Context(), "telegram-bot-token", []byte(token))
	if err != nil {
		t.Fatal(err)
	}
	binding := st.SaveNotificationBinding(app.NotificationBinding{
		ID: "bind_notification", Channel: "telegram", Provider: "telegram-bot-api", Status: "active",
		ExternalChatID: "99", ExternalThreadID: "7", CredentialRef: ref, BaseURL: server.URL,
	})
	adapter := NewNotificationAdapter(st, vault, config.Default().Tools.Notifications.Channels["telegram"])
	result, err := adapter.Send(t.Context(), notification.Notification{
		Channel: "telegram", BindingID: binding.ID, Recipient: "99", RecipientBinding: "7",
		CredentialRef: ref, MessageText: "scheduled reminder",
	})
	if err != nil || result.Status != "sent" {
		t.Fatalf("send failed: result=%#v err=%v", result, err)
	}
	if gotChatID != 99 || gotThreadID != 7 || gotText != "scheduled reminder" {
		t.Fatalf("unexpected Telegram payload: chat=%d thread=%d text=%q", gotChatID, gotThreadID, gotText)
	}
	stored, ok := st.GetCredentialSecret(ref)
	if !ok || strings.Contains(stored.Value, token) {
		t.Fatalf("credential was not encrypted at rest: %#v", stored)
	}
}

func TestNotificationAdapterSanitizesProviderFailure(t *testing.T) {
	token := "123456:AA-provider-echo-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":502,"description":"` + token + `"}`))
	}))
	defer server.Close()
	st := store.NewMemoryStore()
	vault := credential.New(st, credential.Options{Key: telegramCredentialTestKey(8)})
	ref, err := vault.Seal(t.Context(), "telegram-bot-token", []byte(token))
	if err != nil {
		t.Fatal(err)
	}
	binding := st.SaveNotificationBinding(app.NotificationBinding{ID: "bind_failure", Channel: "telegram", Status: "active", ExternalChatID: "99", CredentialRef: ref, BaseURL: server.URL})
	result, sendErr := NewNotificationAdapter(st, vault, config.Default().Tools.Notifications.Channels["telegram"]).Send(t.Context(), notification.Notification{
		Channel: "telegram", BindingID: binding.ID, Recipient: "99", CredentialRef: ref, MessageText: "reminder",
	})
	combined := result.Error
	if sendErr != nil {
		combined += sendErr.Error()
	}
	if result.RetryState != "retryable" || strings.Contains(combined, token) {
		t.Fatalf("provider failure was not sanitized: result=%#v err=%v", result, sendErr)
	}
}

func telegramCredentialTestKey(fill byte) string {
	return base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32))
}
