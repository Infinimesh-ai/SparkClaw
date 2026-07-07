package weixin

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func TestSyncerStoresContextTokenFromInboundMessage(t *testing.T) {
	var gotAuth string
	var gotCursor string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var payload struct {
			GetUpdatesBuf string `json:"get_updates_buf"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		gotCursor = payload.GetUpdatesBuf
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"ret": 0,
			"get_updates_buf": "cursor-2",
			"msgs": [
				{"from_user_id": "wx-user-1", "context_token": "ctx-1"}
			]
		}`))
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
		BaseURL:           ts.URL,
		ProviderCursor:    "cursor-1",
		DefaultForChannel: true,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	})

	NewSyncer(st).Tick(t.Context())

	if gotAuth != "Bearer bot-secret" {
		t.Fatalf("expected auth header, got %q", gotAuth)
	}
	if gotCursor != "cursor-1" {
		t.Fatalf("expected cursor to be sent, got %q", gotCursor)
	}
	binding, ok := st.GetNotificationBinding("bind_1")
	if !ok {
		t.Fatal("binding missing")
	}
	if binding.ContextToken != "ctx-1" || binding.ProviderCursor != "cursor-2" {
		t.Fatalf("expected context and cursor update, got %#v", binding)
	}
}

func TestSyncerDispatchesInboundTextAndReplies(t *testing.T) {
	var sentText string
	var sentContext string
	var typingStatuses []int
	var paths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/ilink/bot/getupdates":
			_, _ = w.Write([]byte(`{
				"ret": 0,
				"get_updates_buf": "cursor-2",
				"msgs": [
					{
						"msg_id": "provider-msg-1",
						"from_user_id": "wx-user-1",
						"context_token": "ctx-1",
						"create_time": 1782800000,
						"item_list": [
							{"type": 1, "text_item": {"text": "你好\nMOCK_REACT_RESPONSE:{\"type\":\"final\",\"answer\":\"你好，我是 SparkClaw。\"}"}}
						]
					}
				]
			}`))
		case "/ilink/bot/getconfig":
			var payload struct {
				IlinkUserID  string `json:"ilink_user_id"`
				ContextToken string `json:"context_token"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload.IlinkUserID != "wx-user-1" || payload.ContextToken != "ctx-1" {
				t.Fatalf("unexpected getconfig payload: %#v", payload)
			}
			_, _ = w.Write([]byte(`{"ret":0,"typing_ticket":"typing-ticket-1"}`))
		case "/ilink/bot/sendtyping":
			var payload struct {
				IlinkUserID  string `json:"ilink_user_id"`
				TypingTicket string `json:"typing_ticket"`
				Status       int    `json:"status"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload.IlinkUserID != "wx-user-1" || payload.TypingTicket != "typing-ticket-1" {
				t.Fatalf("unexpected sendtyping payload: %#v", payload)
			}
			typingStatuses = append(typingStatuses, payload.Status)
			_, _ = w.Write([]byte(`{"ret":0}`))
		case "/ilink/bot/sendmessage":
			var payload struct {
				Msg struct {
					ContextToken string `json:"context_token"`
					ItemList     []struct {
						TextItem struct {
							Text string `json:"text"`
						} `json:"text_item"`
					} `json:"item_list"`
				} `json:"msg"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			sentContext = payload.Msg.ContextToken
			if len(payload.Msg.ItemList) > 0 {
				sentText = payload.Msg.ItemList[0].TextItem.Text
			}
			_, _ = w.Write([]byte(`{"ret":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	cfg := config.Default()
	cfg.Model.Mock = true
	cfg.Workspaces.DefaultRoot = t.TempDir()
	cfg.Workspaces.Allowlist = []string{cfg.Workspaces.DefaultRoot}
	cfg.Tools.Notifications.Channels["weixin"] = config.NotificationChannelConfig{
		Enabled:  true,
		Provider: "openclaw-weixin-qr",
		BaseURL:  ts.URL,
	}
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
		BaseURL:           ts.URL,
		DefaultForChannel: true,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	})
	runtime := agent.NewRuntime(st, toolhub.New(cfg, st), policy.New(cfg), modelrouter.New(cfg), nil)
	dispatcher := NewDispatcherWithConfig(st, runtime, cfg)

	NewSyncer(st).WithDispatcher(dispatcher).Tick(t.Context())

	if len(typingStatuses) != 2 || typingStatuses[0] != 1 || typingStatuses[1] != 2 {
		t.Fatalf("typing should start then cancel, got %#v", typingStatuses)
	}
	if sentContext != "ctx-1" {
		t.Fatalf("reply should use inbound context token, got %q", sentContext)
	}
	if strings.Join(paths, ",") != "/ilink/bot/getupdates,/ilink/bot/getconfig,/ilink/bot/sendtyping,/ilink/bot/sendmessage,/ilink/bot/getconfig,/ilink/bot/sendtyping" {
		t.Fatalf("unexpected provider call order: %#v", paths)
	}
	if !strings.Contains(sentText, "你好，我是 SparkClaw") {
		t.Fatalf("unexpected reply text: %q", sentText)
	}
	binding, _ := st.GetNotificationBinding("bind_1")
	if binding.ContextToken != "ctx-1" || binding.ProviderCursor != "cursor-2" {
		t.Fatalf("binding context was not synced: %#v", binding)
	}
	chatSession, ok := st.FindWeixinChatSession("bind_1", "wx-user-1")
	if !ok || chatSession.LinkedSessionID == "" {
		t.Fatalf("weixin chat session not saved: %#v", chatSession)
	}
	linkedSession, ok := st.GetSession(chatSession.LinkedSessionID)
	if !ok || linkedSession.Source != "weixin" || !linkedSession.Hidden {
		t.Fatalf("linked session should be hidden weixin session: %#v", linkedSession)
	}
	for _, session := range st.ListSessions() {
		if session.ID == chatSession.LinkedSessionID {
			t.Fatalf("weixin linked session should not appear in normal session list: %#v", session)
		}
	}
	messages := st.ListWeixinChatMessages(chatSession.ID, 10)
	if len(messages) != 2 {
		t.Fatalf("expected inbound and outbound chat messages, got %#v", messages)
	}
	if messages[0].Direction != "inbound" || messages[1].Direction != "outbound" {
		t.Fatalf("unexpected message directions: %#v", messages)
	}
}

func TestSyncerDispatchesMultipleWeixinUsersIndependently(t *testing.T) {
	sentRecipients := []string{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/ilink/bot/getupdates":
			_, _ = w.Write([]byte(`{
				"ret": 0,
				"get_updates_buf": "cursor-2",
				"msgs": [
					{
						"msg_id": "provider-msg-user-a",
						"from_user_id": "wx-user-a",
						"context_token": "ctx-a",
						"create_time": 1782800000,
						"item_list": [
							{"type": 1, "text_item": {"text": "你好A\nMOCK_REACT_RESPONSE:{\"type\":\"final\",\"answer\":\"回复A\"}"}}
						]
					},
					{
						"msg_id": "provider-msg-user-b",
						"from_user_id": "wx-user-b",
						"context_token": "ctx-b",
						"create_time": 1782800001,
						"item_list": [
							{"type": 1, "text_item": {"text": "你好B\nMOCK_REACT_RESPONSE:{\"type\":\"final\",\"answer\":\"回复B\"}"}}
						]
					}
				]
			}`))
		case "/ilink/bot/getconfig":
			_, _ = w.Write([]byte(`{"ret":0,"typing_ticket":"typing-ticket-1"}`))
		case "/ilink/bot/sendtyping":
			_, _ = w.Write([]byte(`{"ret":0}`))
		case "/ilink/bot/sendmessage":
			var payload struct {
				Msg struct {
					ToUserID string `json:"to_user_id"`
				} `json:"msg"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			sentRecipients = append(sentRecipients, payload.Msg.ToUserID)
			_, _ = w.Write([]byte(`{"ret":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	cfg := config.Default()
	cfg.Model.Mock = true
	cfg.Tools.Notifications.Channels["weixin"] = config.NotificationChannelConfig{
		Enabled:  true,
		Provider: "openclaw-weixin-qr",
		BaseURL:  ts.URL,
	}
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
		CredentialRef:     "provider:openclaw-weixin-qr:bind_1",
		BaseURL:           ts.URL,
		DefaultForChannel: true,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	})
	runtime := agent.NewRuntime(st, toolhub.New(cfg, st), policy.New(cfg), modelrouter.New(cfg), nil)
	dispatcher := NewDispatcherWithConfig(st, runtime, cfg)

	NewSyncer(st).WithDispatcher(dispatcher).Tick(t.Context())

	if strings.Join(sentRecipients, ",") != "wx-user-a,wx-user-b" {
		t.Fatalf("expected replies to separate weixin users, got %#v", sentRecipients)
	}
	sessionA, okA := st.FindWeixinChatSession("bind_1", "wx-user-a")
	sessionB, okB := st.FindWeixinChatSession("bind_1", "wx-user-b")
	if !okA || !okB {
		t.Fatalf("expected separate weixin chat sessions, got A=%#v ok=%v B=%#v ok=%v", sessionA, okA, sessionB, okB)
	}
	if sessionA.LinkedSessionID == "" || sessionB.LinkedSessionID == "" || sessionA.LinkedSessionID == sessionB.LinkedSessionID {
		t.Fatalf("weixin users should have isolated linked sessions: %#v %#v", sessionA, sessionB)
	}
	if sessionA.OwnerID == "" || sessionB.OwnerID == "" || sessionA.OwnerID == sessionB.OwnerID {
		t.Fatalf("weixin users should have isolated owner profiles: %#v %#v", sessionA, sessionB)
	}
	profileA, okA := st.GetOwnerProfileByID(sessionA.OwnerID)
	profileB, okB := st.GetOwnerProfileByID(sessionB.OwnerID)
	if !okA || !okB || profileA.Source != "weixin" || profileB.Source != "weixin" || profileA.ExternalRef == profileB.ExternalRef {
		t.Fatalf("weixin users should have separate persisted profiles: %#v ok=%v %#v ok=%v", profileA, okA, profileB, okB)
	}
	if sessionA.WorkspaceRoot == "" || sessionB.WorkspaceRoot == "" || sessionA.WorkspaceRoot == sessionB.WorkspaceRoot {
		t.Fatalf("weixin users should have isolated workspaces: %#v %#v", sessionA, sessionB)
	}
	linkedA, ok := st.GetSession(sessionA.LinkedSessionID)
	if !ok || linkedA.OwnerID != sessionA.OwnerID || linkedA.WorkspaceRoot != sessionA.WorkspaceRoot {
		t.Fatalf("linked session should carry weixin owner/workspace scope: %#v ok=%v chat=%#v", linkedA, ok, sessionA)
	}
	if sessionA.LastContextToken != "ctx-a" || sessionB.LastContextToken != "ctx-b" {
		t.Fatalf("context tokens should be tracked per user: %#v %#v", sessionA, sessionB)
	}
	binding, _ := st.GetNotificationBinding("bind_1")
	if binding.ExternalUserID != "" {
		t.Fatalf("binding should not be pinned to first chat user, got %#v", binding.ExternalUserID)
	}
}

func TestSyncerDispatchesSingleMediaMarkdownAsImage(t *testing.T) {
	var sawUpload bool
	var sentTextItems int
	var sentImageItems int
	var sentContext string
	var serverURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/ilink/bot/getupdates":
			_, _ = w.Write([]byte(`{
				"ret": 0,
				"get_updates_buf": "cursor-2",
				"msgs": [
					{
						"msg_id": "provider-msg-weather",
						"from_user_id": "wx-user-1",
						"context_token": "ctx-1",
						"create_time": 1782800000,
						"item_list": [
							{"type": 1, "text_item": {"text": "查询上海天气\nMOCK_REACT_RESPONSE:{\"type\":\"final\",\"answer\":\"![天气卡片](media/20260702/weather.png)\"}"}}
						]
					}
				]
			}`))
		case "/ilink/bot/getconfig":
			_, _ = w.Write([]byte(`{"ret":0,"typing_ticket":"typing-ticket-1"}`))
		case "/ilink/bot/sendtyping":
			_, _ = w.Write([]byte(`{"ret":0}`))
		case "/ilink/bot/getuploadurl":
			_, _ = w.Write([]byte(`{"upload_full_url":"` + serverURL + `/upload"}`))
		case "/upload":
			sawUpload = true
			w.Header().Set("x-encrypted-param", "download-param-1")
			_, _ = w.Write([]byte(`ok`))
		case "/ilink/bot/sendmessage":
			var payload struct {
				Msg struct {
					ContextToken string `json:"context_token"`
					ItemList     []struct {
						Type int `json:"type"`
					} `json:"item_list"`
				} `json:"msg"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			sentContext = payload.Msg.ContextToken
			for _, item := range payload.Msg.ItemList {
				switch item.Type {
				case 1:
					sentTextItems++
				case 2:
					sentImageItems++
				}
			}
			_, _ = w.Write([]byte(`{"ret":0}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	serverURL = ts.URL

	root := t.TempDir()
	relPath := filepath.Join("media", "20260702", "weather.png")
	absPath := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatal(err)
	}
	rawImage := tinyWeixinSyncerPNG(t)
	if err := os.WriteFile(absPath, rawImage, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Model.Mock = true
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	cfg.Tools.Notifications.Channels["weixin"] = config.NotificationChannelConfig{
		Enabled:    true,
		Provider:   "openclaw-weixin-qr",
		BaseURL:    ts.URL,
		CDNBaseURL: ts.URL,
	}
	st := store.NewMemoryStore()
	st.SaveArtifactObject(app.ArtifactObject{
		ID:          "obj_weather",
		Kind:        "media_weather_card",
		Backend:     "workspace",
		Key:         filepath.ToSlash(relPath),
		URI:         "workspace://" + filepath.ToSlash(relPath),
		Path:        absPath,
		ContentType: "image/png",
		Bytes:       len(rawImage),
		CreatedAt:   time.Now().UTC(),
	})
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
		BaseURL:           ts.URL,
		DefaultForChannel: true,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	})
	runtime := agent.NewRuntime(st, toolhub.New(cfg, st), policy.New(cfg), modelrouter.New(cfg), nil)
	dispatcher := NewDispatcher(st, runtime, cfg.Tools.Notifications.Channels["weixin"])

	NewSyncer(st).WithDispatcher(dispatcher).Tick(t.Context())

	if !sawUpload {
		t.Fatal("expected generated weather card to be uploaded as image")
	}
	if sentImageItems != 1 || sentTextItems != 0 {
		t.Fatalf("expected one image item and no text items, got image=%d text=%d", sentImageItems, sentTextItems)
	}
	if sentContext != "ctx-1" {
		t.Fatalf("image reply should use inbound context token, got %q", sentContext)
	}
}

func tinyWeixinSyncerPNG(t *testing.T) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
