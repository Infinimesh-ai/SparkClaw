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
	chat, ok := reloaded.FindExternalChatSession("bind_tg", "1001", "7")
	if !ok || chat.Channel != "telegram" || chat.ExternalUserID != "42" {
		t.Fatalf("external chat did not reload: %#v ok=%v", chat, ok)
	}
	inbox, ok := reloaded.FindChannelInboxUpdate("bind_tg", "9001")
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
	snapshot := Snapshot{
		Sessions: map[string]app.Session{
			"session_legacy": {ID: "session_legacy", Title: "微信会话"},
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
	chat, ok := st.FindExternalChatSession("bind_weixin", "wx-user", "")
	if !ok || chat.ID != "wxchat_legacy" || chat.ExternalChatID != "wx-user" {
		t.Fatalf("legacy chat was not migrated: %#v ok=%v", chat, ok)
	}
	message, ok := st.FindExternalChatMessageByExternalID(chat.ID, "provider-message")
	if !ok || message.ID != "wxmsg_legacy" || message.Channel != "weixin" {
		t.Fatalf("legacy message was not migrated: %#v ok=%v", message, ok)
	}
}

func testExternalChatAndInboxParity(t *testing.T, st Store) {
	t.Helper()
	linked := st.CreateSessionWithScope("Telegram session", app.DefaultOwnerID, t.TempDir(), "telegram", true)
	chat := st.SaveExternalChatSession(app.ExternalChatSession{
		BindingID:        "bind_tg",
		Channel:          "telegram",
		Provider:         "telegram-bot-api",
		ExternalUserID:   "42",
		ExternalChatID:   "1001",
		ExternalThreadID: "7",
		LinkedSessionID:  linked.ID,
		Status:           "active",
	})
	if chat.ID == "" {
		t.Fatal("external chat id was not assigned")
	}
	if found, ok := st.FindExternalChatSession("bind_tg", "1001", "7"); !ok || found.ID != chat.ID {
		t.Fatalf("external chat lookup failed: %#v ok=%v", found, ok)
	}
	if found, ok := st.FindExternalChatSessionByLinkedSessionID(linked.ID); !ok || found.ID != chat.ID {
		t.Fatalf("linked session lookup failed: %#v ok=%v", found, ok)
	}

	message := st.SaveExternalChatMessage(app.ExternalChatMessage{
		ChatSessionID:     chat.ID,
		BindingID:         chat.BindingID,
		Direction:         "inbound",
		Role:              "user",
		ExternalMessageID: "501",
		Content:           "hello",
		Status:            "processed",
	})
	if message.Channel != "telegram" {
		t.Fatalf("message channel was not inherited: %#v", message)
	}
	if found, ok := st.FindExternalChatMessageByExternalID(chat.ID, "501"); !ok || found.ID != message.ID {
		t.Fatalf("external message lookup failed: %#v ok=%v", found, ok)
	}

	first := st.SaveChannelInboxUpdate(app.ChannelInboxUpdate{
		BindingID:  "bind_tg",
		Channel:    "telegram",
		ExternalID: "9001",
		ChatKey:    "bind_tg:1001:7",
		Payload:    json.RawMessage(`{"update_id":9001}`),
		Status:     "pending",
	})
	duplicate := st.SaveChannelInboxUpdate(app.ChannelInboxUpdate{
		BindingID:  "bind_tg",
		Channel:    "telegram",
		ExternalID: "9001",
		ChatKey:    "bind_tg:1001:7",
		Payload:    json.RawMessage(`{"update_id":9001,"duplicate":true}`),
		Status:     "pending",
	})
	if duplicate.ID != first.ID || string(duplicate.Payload) != string(first.Payload) {
		t.Fatalf("duplicate transport update replaced durable record: first=%#v duplicate=%#v", first, duplicate)
	}
	first.Status = "processing"
	first.Attempts = 1
	first.AvailableAt = time.Now().UTC().Add(time.Minute)
	updated := st.SaveChannelInboxUpdate(first)
	if updated.Status != "processing" || updated.Attempts != 1 {
		t.Fatalf("existing inbox status did not advance: %#v", updated)
	}
	if ready := st.ListChannelInboxUpdates("telegram", "processing", time.Now().UTC(), 10); len(ready) != 0 {
		t.Fatalf("future inbox item listed as ready: %#v", ready)
	}
	if ready := st.ListChannelInboxUpdates("telegram", "processing", time.Now().UTC().Add(2*time.Minute), 10); len(ready) != 1 || ready[0].ID != first.ID {
		t.Fatalf("ready inbox lookup failed: %#v", ready)
	}
}
