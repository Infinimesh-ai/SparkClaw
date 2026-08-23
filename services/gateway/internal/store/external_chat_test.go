package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func TestMemoryStoreExternalChatAndInboxParity(t *testing.T) {
	testExternalChatAndInboxParity(t, NewMemoryStore())
}

func TestFileStoreExternalChatAndInboxParity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway-state.json")
	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	testExternalChatAndInboxParity(t, st)

	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	chat, ok, err := reloaded.FindExternalChatSession(t.Context(), "bind_tg", "1001", "7")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || chat.Channel != "telegram" || chat.ExternalUserID != "42" {
		t.Fatalf("external chat did not reload: %#v ok=%v", chat, ok)
	}
	retained, ok, err := reloaded.FindExternalChatMessageByExternalID(t.Context(), chat.ID, "502")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || retained.PendingReplyKind != "control_text" || retained.PendingReply != "请确认" || retained.DispatchAttempts != 1 {
		t.Fatalf("pending reply state did not survive reload: %#v ok=%v", retained, ok)
	}
	inbox, ok, err := reloaded.FindChannelInboxUpdate(t.Context(), "bind_tg", "9001")
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		UpdateID int64 `json:"update_id"`
	}
	if err := json.Unmarshal(inbox.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if !ok || inbox.Status != "processing" || inbox.Attempts != 1 || payload.UpdateID != 9001 {
		t.Fatalf("inbox did not reload: %#v ok=%v", inbox, ok)
	}
}

func TestFileStoreReadsLegacyWeixinChatSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway-state.json")
	now := time.Now().UTC().Truncate(time.Microsecond)
	snapshot := Snapshot{
		Sessions: map[string]app.Session{
			"session_legacy": {ID: "session_legacy", Title: "微信会话", CreatedAt: now, UpdatedAt: now},
		},
		WeixinChatSessions: map[string]app.WeixinChatSession{
			"wxchat_legacy": {
				ID:              "wxchat_legacy",
				BindingID:       "bind_weixin",
				Channel:         "weixin",
				ExternalUserID:  "wx-user",
				LinkedSessionID: "session_legacy",
				Status:          "active",
			},
		},
		WeixinChatMessages: map[string]app.WeixinChatMessage{
			"wxmsg_legacy": {
				ID:                "wxmsg_legacy",
				ChatSessionID:     "wxchat_legacy",
				BindingID:         "bind_weixin",
				ExternalMessageID: "provider-message",
				Status:            "processed",
			},
		},
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	st, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	chat, ok, err := st.FindExternalChatSession(t.Context(), "bind_weixin", "wx-user", "")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || chat.ID != "wxchat_legacy" || chat.ExternalChatID != "wx-user" {
		t.Fatalf("legacy chat was not migrated: %#v ok=%v", chat, ok)
	}
	message, ok, err := st.FindExternalChatMessageByExternalID(t.Context(), chat.ID, "provider-message")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || message.ID != "wxmsg_legacy" || message.Channel != "weixin" {
		t.Fatalf("legacy message was not migrated: %#v ok=%v", message, ok)
	}
}

func testExternalChatAndInboxParity(t *testing.T, st testBackend) {
	t.Helper()
	linked := mustCreateSessionWithScope(t, st, "Telegram session", app.DefaultOwnerID, t.TempDir(), "telegram", true)
	chat, err := st.SaveExternalChatSession(t.Context(), app.ExternalChatSession{
		BindingID:        "bind_tg",
		Channel:          "telegram",
		Provider:         "telegram-bot-api",
		ExternalUserID:   "42",
		ExternalChatID:   "1001",
		ExternalThreadID: "7",
		LinkedSessionID:  linked.ID,
		Status:           "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	if chat.ID == "" {
		t.Fatal("external chat id was not assigned")
	}
	if found, ok, err := st.FindExternalChatSession(t.Context(), "bind_tg", "1001", "7"); err != nil || !ok || found.ID != chat.ID {
		t.Fatalf("external chat lookup failed: %#v ok=%v", found, ok)
	}
	if found, ok, err := st.FindExternalChatSessionByLinkedSessionID(t.Context(), linked.ID); err != nil || !ok || found.ID != chat.ID {
		t.Fatalf("linked session lookup failed: %#v ok=%v", found, ok)
	}

	message, err := st.SaveExternalChatMessage(t.Context(), app.ExternalChatMessage{
		ChatSessionID:     chat.ID,
		BindingID:         chat.BindingID,
		Direction:         "inbound",
		Role:              "user",
		ExternalMessageID: "501",
		Content:           "hello",
		Status:            "delivery_failed",
		PendingReplyKind:  "workflow_result",
		PendingReply:      `{"run_id":"run_1"}`,
		DispatchAttempts:  2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if message.Channel != "telegram" {
		t.Fatalf("message channel was not inherited: %#v", message)
	}
	found, ok, err := st.FindExternalChatMessageByExternalID(t.Context(), chat.ID, "501")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || found.ID != message.ID {
		t.Fatalf("external message lookup failed: %#v ok=%v", found, ok)
	}
	if found.PendingReplyKind != "workflow_result" || found.PendingReply != `{"run_id":"run_1"}` || found.DispatchAttempts != 2 {
		t.Fatalf("pending reply state was not persisted: %#v", found)
	}
	message.Status = "processed"
	message.PendingReplyKind, message.PendingReply = "", ""
	message.DispatchAttempts = 0
	if _, err := st.SaveExternalChatMessage(t.Context(), message); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveExternalChatMessage(t.Context(), app.ExternalChatMessage{
		ChatSessionID:     chat.ID,
		BindingID:         chat.BindingID,
		Direction:         "inbound",
		Role:              "user",
		ExternalMessageID: "502",
		Content:           "再来一条",
		Status:            "delivery_failed",
		PendingReplyKind:  "control_text",
		PendingReply:      "请确认",
		DispatchAttempts:  1,
	}); err != nil {
		t.Fatal(err)
	}

	first, err := st.SaveChannelInboxUpdate(t.Context(), app.ChannelInboxUpdate{
		BindingID:  "bind_tg",
		Channel:    "telegram",
		ExternalID: "9001",
		ChatKey:    "bind_tg:1001:7",
		Payload:    json.RawMessage(`{"update_id":9001}`),
		Status:     "pending",
	})
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := st.SaveChannelInboxUpdate(t.Context(), app.ChannelInboxUpdate{
		BindingID:  "bind_tg",
		Channel:    "telegram",
		ExternalID: "9001",
		ChatKey:    "bind_tg:1001:7",
		Payload:    json.RawMessage(`{"update_id":9001,"duplicate":true}`),
		Status:     "pending",
	})
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.ID != first.ID || string(duplicate.Payload) != string(first.Payload) {
		t.Fatalf("duplicate transport update replaced durable record: first=%#v duplicate=%#v", first, duplicate)
	}
	first.Status = "processing"
	first.Attempts = 1
	first.AvailableAt = time.Now().UTC().Add(time.Minute)
	updated, err := st.SaveChannelInboxUpdate(t.Context(), first)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "processing" || updated.Attempts != 1 {
		t.Fatalf("existing inbox status did not advance: %#v", updated)
	}
	ready, err := st.ListChannelInboxUpdates(t.Context(), "telegram", "processing", time.Now().UTC(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ready) != 0 {
		t.Fatalf("future inbox item listed as ready: %#v", ready)
	}
	ready, err = st.ListChannelInboxUpdates(t.Context(), "telegram", "processing", time.Now().UTC().Add(2*time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ready) != 1 || ready[0].ID != first.ID {
		t.Fatalf("ready inbox lookup failed: %#v", ready)
	}
}
