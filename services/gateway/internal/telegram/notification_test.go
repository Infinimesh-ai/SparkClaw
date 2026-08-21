package telegram

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/notification"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
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
	ref, err := vault.Seal(t.Context(), "bind_notification", "telegram-bot-token", []byte(token))
	if err != nil {
		t.Fatal(err)
	}
	binding := storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
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
	stored, ok, err := st.GetCredentialSecret(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || strings.Contains(stored.Value, token) {
		t.Fatalf("credential was not encrypted at rest: %#v", stored)
	}
}

func TestNotificationProviderDeliversEveryMultimediaPart(t *testing.T) {
	calls := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, filepath.Base(r.URL.Path))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1,"chat":{"id":99,"type":"private"},"date":1}}`))
	}))
	defer server.Close()

	st := store.NewMemoryStore()
	vault := credential.New(st, credential.Options{Key: telegramCredentialTestKey(9)})
	ref, err := vault.Seal(t.Context(), "bind_multimedia", "telegram-bot-token", []byte("123456:AA-multimedia"))
	if err != nil {
		t.Fatal(err)
	}
	binding := storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{ID: "bind_multimedia", Channel: "telegram", Status: "active", ExternalChatID: "99", CredentialRef: ref, BaseURL: server.URL, Scopes: []string{app.BindingScopeMessageSendSelf}})
	parts := []app.MessagePart{{ID: "text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: "hello"}}
	for _, item := range []struct {
		id, name, content string
		kind              app.MessagePartKind
		disposition       app.MessagePartDisposition
	}{
		{id: "image", name: "image.png", content: "image", kind: app.MessagePartImage, disposition: app.MessageDispositionAttachment},
		{id: "audio", name: "audio.mp3", content: "audio", kind: app.MessagePartAudio, disposition: app.MessageDispositionAttachment},
		{id: "voice", name: "voice.ogg", content: "voice", kind: app.MessagePartAudio, disposition: app.MessageDispositionVoiceNote},
		{id: "file", name: "report.txt", content: "file", kind: app.MessagePartFile, disposition: app.MessageDispositionAttachment},
	} {
		path := filepath.Join(t.TempDir(), item.name)
		if err := os.WriteFile(path, []byte(item.content), 0o600); err != nil {
			t.Fatal(err)
		}
		st.SaveArtifactObject(app.ArtifactObject{ID: item.id, Path: path, Key: item.name})
		parts = append(parts, app.MessagePart{ID: item.id, Kind: item.kind, Disposition: item.disposition, ArtifactID: item.id, Name: item.name})
	}
	adapter := NewNotificationAdapter(st, vault, config.Default().Tools.Notifications.Channels["telegram"])
	providers := delivery.NewProviderRegistry()
	if err := providers.Register(adapter); err != nil {
		t.Fatal(err)
	}
	request := app.DeliveryRequest{SchemaVersion: app.DeliveryRequestSchemaVersion, ID: "del_multimedia", IdempotencyKey: "multi", OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID, Authorization: app.MessageAuthorization{PrincipalID: app.DefaultOwnerID}, Origin: app.DeliveryOriginWebDirect, Target: app.EndpointID(binding.ID), Content: app.MessageContent{Parts: parts}}
	receipt, err := providers.Deliver(t.Context(), app.MessageEndpoint{ID: app.EndpointID(binding.ID), Kind: app.EndpointKindThirdPartyDevice, ProviderKey: "telegram", BindingRef: binding.ID}, request)
	if err != nil || receipt.Status != app.DeliverySucceeded {
		t.Fatalf("deliver: receipt=%#v err=%v", receipt, err)
	}
	want := []string{"sendMessage", "sendPhoto", "sendDocument", "sendVoice", "sendDocument"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected ordered provider calls: got=%v want=%v", calls, want)
	}
	if len(receipt.PartReceipts) != len(parts) || receipt.PartReceipts[2].Representation != "file_fallback" || receipt.PartReceipts[3].Representation != "native" {
		t.Fatalf("unexpected per-part receipt mapping: %#v", receipt.PartReceipts)
	}
}

func TestBindingScopeCompatibilityDefaultsToAllMessaging(t *testing.T) {
	for _, scopes := range [][]string{nil, {app.BindingScopeReminderSendSelf}, {app.BindingScopeMessageSendSelf}} {
		if !bindingAllowsScope(scopes, app.BindingScopeMessageSendSelf, app.DeliveryOriginWebDirect) ||
			!bindingAllowsScope(scopes, app.BindingScopeReminderSendSelf, app.DeliveryOriginSchedule) ||
			!bindingAllowsSourceReply(scopes) {
			t.Fatalf("messaging binding did not grant full exchange for scopes %#v", scopes)
		}
	}
	if bindingAllowsScope([]string{"unknown"}, app.BindingScopeMessageSendSelf, app.DeliveryOriginWebDirect) {
		t.Fatal("unknown scope unexpectedly granted messaging authority")
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
	ref, err := vault.Seal(t.Context(), "bind_failure", "telegram-bot-token", []byte(token))
	if err != nil {
		t.Fatal(err)
	}
	binding := storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{ID: "bind_failure", Channel: "telegram", Status: "active", ExternalChatID: "99", CredentialRef: ref, BaseURL: server.URL})
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
