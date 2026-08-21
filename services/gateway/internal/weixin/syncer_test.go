package weixin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/connectorruntime"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/messagecontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/notification"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

type unavailableWeixinCredentialStore struct {
	store.Store
	err error
}

func (s *unavailableWeixinCredentialStore) SaveCredentialSecret(context.Context, store.CredentialSaveCommand) (app.CredentialSecret, error) {
	return app.CredentialSecret{}, s.err
}

func (s *unavailableWeixinCredentialStore) GetCredentialSecret(context.Context, string) (app.CredentialSecret, bool, error) {
	return app.CredentialSecret{}, false, s.err
}

func (s *unavailableWeixinCredentialStore) DeleteCredentialSecret(context.Context, store.CredentialDeleteCondition) (app.CredentialSecret, error) {
	return app.CredentialSecret{}, s.err
}

func weixinTestRuntimeScope() connectorruntime.RuntimeScope {
	return connectorruntime.RuntimeScope{
		Channel:      "weixin",
		OwnerEnabled: func(string) bool { return true },
	}
}

func newWeixinTestResultDeliverer(t *testing.T, st store.Store, cfg config.Config) connectorruntime.ResultDeliverer {
	t.Helper()
	endpoints := messagecontrol.NewEndpointRegistry(st)
	providers := delivery.NewProviderRegistry()
	if err := providers.Register(notification.NewWeixinAdapter("weixin", cfg.Tools.Notifications.Channels["weixin"], st, newWeixinTestVault(t, st))); err != nil {
		t.Fatal(err)
	}
	gateway := delivery.NewGateway(endpoints, providers, nil)
	return delivery.NewWorkflowResultDeliverer(messagecontrol.NewReturnRouteResolver(endpoints), gateway)
}

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
	vault := newWeixinTestVault(t, st)
	credentialRef := sealWeixinTestCredential(t, vault, "bind_1", "bot-secret")
	storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
		ID:                "bind_1",
		OwnerID:           app.DefaultOwnerID,
		Channel:           "weixin",
		Provider:          "openclaw-weixin-qr",
		Status:            "active",
		ExternalUserID:    "wx-user-1",
		CredentialRef:     credentialRef,
		BaseURL:           ts.URL,
		ProviderCursor:    "cursor-1",
		DefaultForChannel: true,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	})

	NewSyncer(st).WithCredentialVault(vault).Tick(t.Context(), weixinTestRuntimeScope())

	if gotAuth != "Bearer bot-secret" {
		t.Fatalf("expected auth header, got %q", gotAuth)
	}
	if gotCursor != "cursor-1" {
		t.Fatalf("expected cursor to be sent, got %q", gotCursor)
	}
	binding, ok := storetest.MustGetNotificationBinding(t, st, "bind_1")
	if !ok {
		t.Fatal("binding missing")
	}
	if binding.ContextToken != "ctx-1" || binding.ProviderCursor != "cursor-2" {
		t.Fatalf("expected context and cursor update, got %#v", binding)
	}
}

func TestSyncerOwnerGateSkipsDisabledBindingAcquisition(t *testing.T) {
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ret":0,"get_updates_buf":"cursor"}`))
	}))
	defer ts.Close()
	st := store.NewMemoryStore()
	vault := newWeixinTestVault(t, st)
	for _, ownerID := range []string{"owner-a", "owner-b"} {
		bindingID := "binding-" + ownerID
		credentialRef := sealWeixinTestCredential(t, vault, bindingID, "secret")
		storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
			ID: bindingID, OwnerID: ownerID, Channel: "weixin", Provider: "openclaw-weixin-qr",
			Status: "active", CredentialRef: credentialRef, BaseURL: ts.URL,
		})
	}
	scope := connectorruntime.RuntimeScope{
		Channel:      "weixin",
		OwnerEnabled: func(ownerID string) bool { return ownerID == "owner-b" },
	}
	NewSyncer(st).WithCredentialVault(vault).Tick(t.Context(), scope)
	if calls.Load() != 1 {
		t.Fatalf("provider polls = %d, want only enabled owner-b", calls.Load())
	}
}

func TestSyncerStoresOnlyStableCredentialFailure(t *testing.T) {
	const canary = "private sync credential backend diagnostic"
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()
	base := store.NewMemoryStore()
	binding := storetest.MustCreateNotificationBinding(t, base, app.NotificationBinding{
		ID: "binding-credential-failure", OwnerID: app.DefaultOwnerID, Channel: "weixin",
		Provider: "openclaw-weixin-qr", Status: "active", CredentialRef: "credential-private-ref", BaseURL: server.URL,
	})
	st := &unavailableWeixinCredentialStore{Store: base, err: &store.StoreError{
		Code: store.StoreErrorUnavailable, Operation: store.OperationCredentialSecretGet, Err: errors.New(canary),
	}}
	vault := credential.New(st, credential.Options{Key: strings.Repeat("u", 32)})
	NewSyncer(st).WithCredentialVault(vault).Tick(t.Context(), weixinTestRuntimeScope())
	stored, found := storetest.MustGetNotificationBinding(t, base, binding.ID)
	if !found || stored.LastError != "credential is temporarily unavailable" {
		t.Fatalf("stored credential error=%q found=%v", stored.LastError, found)
	}
	if strings.Contains(stored.LastError, canary) || strings.Contains(stored.LastError, binding.CredentialRef) || calls.Load() != 0 {
		t.Fatalf("credential failure leaked details or reached provider: error=%q calls=%d", stored.LastError, calls.Load())
	}
}

func TestSyncerStoresOnlyStableProviderFailure(t *testing.T) {
	const canary = "provider-controlled private diagnostic"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ret":1,"errmsg":"` + canary + `"}`))
	}))
	defer server.Close()
	st := store.NewMemoryStore()
	vault := newWeixinTestVault(t, st)
	credentialRef := sealWeixinTestCredential(t, vault, "binding-provider-failure", "bot-secret")
	binding := storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
		ID: "binding-provider-failure", OwnerID: app.DefaultOwnerID, Channel: "weixin",
		Provider: "openclaw-weixin-qr", Status: app.NotificationBindingActive,
		ExternalUserID: "recipient", CredentialRef: credentialRef, BaseURL: server.URL,
	})
	NewSyncer(st).WithCredentialVault(vault).Tick(t.Context(), weixinTestRuntimeScope())
	stored, found := storetest.MustGetNotificationBinding(t, st, binding.ID)
	if !found || stored.LastError != "weixin_sync_failed" || strings.Contains(stored.LastError, canary) {
		t.Fatalf("stored provider error=%q found=%v", stored.LastError, found)
	}
}

func TestSyncerOwnerGateRetainsUndispatchedBatchUntilReenabled(t *testing.T) {
	st := store.NewMemoryStore()
	binding := storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
		ID: "binding-owner-a", OwnerID: "owner-a", ActorID: "owner-a", Channel: "weixin",
		Provider: "openclaw-weixin-qr", Status: "active", ExternalUserID: "wx-owner-a",
		ProviderCursor: "cursor-1",
	})
	runtime := &fakeAgentRuntime{result: agent.Result{
		Run: app.AgentRun{ID: "run-owner-gate", State: "completed"},
		WorkflowResult: &app.WorkflowResult{
			SchemaVersion: app.WorkflowResultSchemaVersion, ID: "result-owner-gate", RunID: "run-owner-gate",
			Status: app.WorkflowResultSucceeded,
			Content: app.MessageContent{Parts: []app.MessagePart{{
				ID: "part-owner-gate", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: "reply",
			}}},
		},
	}}
	dispatcher := NewDispatcher(st, runtime, config.NotificationChannelConfig{}).WithResultDeliverer(&fakeResultDeliverer{})
	syncer := NewSyncer(st).WithDispatcher(dispatcher)
	batch := inboundBatch{
		Binding: binding,
		Cursor:  "cursor-2",
		Msgs: []inboundEnvelope{{
			FromUserID: "wx-owner-a", ContextToken: "context-owner-a", ExternalID: "message-owner-a",
			Items: []updateItem{{Type: 1, TextItem: updateTextItem{Text: "hello"}}},
		}},
	}
	var enabled atomic.Bool
	scope := connectorruntime.RuntimeScope{
		Channel:      "weixin",
		OwnerEnabled: func(ownerID string) bool { return ownerID == "owner-a" && enabled.Load() },
	}
	syncer.processBatch(t.Context(), scope, batch)
	unchanged, _ := storetest.MustGetNotificationBinding(t, st, binding.ID)
	if runtime.handledCount() != 0 || unchanged.ProviderCursor != "cursor-1" {
		t.Fatalf("disabled batch was consumed: handles=%d binding=%#v", runtime.handledCount(), unchanged)
	}
	enabled.Store(true)
	syncer.processBatch(t.Context(), scope, batch)
	resumed, _ := storetest.MustGetNotificationBinding(t, st, binding.ID)
	if runtime.handledCount() != 1 || resumed.ProviderCursor != "cursor-2" {
		t.Fatalf("reenabled batch did not resume: handles=%d binding=%#v", runtime.handledCount(), resumed)
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
							{"type": 1, "text_item": {"text": "你好\nMOCK_CONVERSATION_RESPONSE:你好，我是 SparkClaw。"}}
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
	vault := newWeixinTestVault(t, st)
	credentialRef := sealWeixinTestCredential(t, vault, "bind_1", "bot-secret")
	storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
		ID:                "bind_1",
		OwnerID:           app.DefaultOwnerID,
		Channel:           "weixin",
		Provider:          "openclaw-weixin-qr",
		Status:            "active",
		ExternalUserID:    "wx-user-1",
		CredentialRef:     credentialRef,
		BaseURL:           ts.URL,
		DefaultForChannel: true,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	})
	runtime := agent.NewRuntime(st, toolhub.New(cfg, st), policy.New(cfg), modelrouter.New(cfg), nil)
	dispatcher := NewDispatcherWithConfig(st, runtime, cfg).WithCredentialVault(vault).WithResultDeliverer(newWeixinTestResultDeliverer(t, st, cfg))

	syncer := NewSyncer(st).WithCredentialVault(vault).WithDispatcher(dispatcher)
	syncer.Tick(t.Context(), weixinTestRuntimeScope())
	syncer.Wait()

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
	binding, _ := storetest.MustGetNotificationBinding(t, st, "bind_1")
	if binding.ContextToken != "ctx-1" || binding.ProviderCursor != "cursor-2" {
		t.Fatalf("binding context was not synced: %#v", binding)
	}
	chatSession, ok := st.FindExternalChatSession("bind_1", "wx-user-1", "")
	if !ok || chatSession.LinkedSessionID == "" {
		t.Fatalf("weixin chat session not saved: %#v", chatSession)
	}
	linkedSession, ok := st.GetSession(chatSession.LinkedSessionID)
	if !ok || linkedSession.Source != "weixin" || !linkedSession.Hidden {
		t.Fatalf("linked session should be hidden weixin session: %#v", linkedSession)
	}
	runs := st.ListRuns(chatSession.LinkedSessionID)
	if len(runs) != 1 || runs[0].MessageContext == nil || runs[0].MessageContext.OwnerID != chatSession.OwnerID || runs[0].MessageContext.ReturnRoute.SourceEndpointID != app.EndpointID(chatSession.ID) || runs[0].MessageContext.Route.Status != app.RouteMatched || len(runs[0].MessageContext.Route.CapabilityPath) != 2 || runs[0].MessageContext.Route.CapabilityPath[1] != app.CapabilityConversationAnswer || runs[0].Workflow == nil || runs[0].Workflow.Plan.ProfileID != app.WorkflowConversationAnswer {
		t.Fatalf("weixin run did not persist the external endpoint route context: %#v", runs)
	}
	for _, session := range st.ListSessions() {
		if session.ID == chatSession.LinkedSessionID {
			t.Fatalf("weixin linked session should not appear in normal session list: %#v", session)
		}
	}
	messages := st.ListExternalChatMessages(chatSession.ID, 10)
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
							{"type": 1, "text_item": {"text": "你好A\nMOCK_STEP_RESPONSE:{\"type\":\"final\",\"answer\":\"回复A\"}"}}
						]
					},
					{
						"msg_id": "provider-msg-user-b",
						"from_user_id": "wx-user-b",
						"context_token": "ctx-b",
						"create_time": 1782800001,
						"item_list": [
							{"type": 1, "text_item": {"text": "你好B\nMOCK_STEP_RESPONSE:{\"type\":\"final\",\"answer\":\"回复B\"}"}}
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
	vault := newWeixinTestVault(t, st)
	credentialRef := sealWeixinTestCredential(t, vault, "bind_1", "bot-secret")
	storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
		ID:                "bind_1",
		OwnerID:           app.DefaultOwnerID,
		Channel:           "weixin",
		Provider:          "openclaw-weixin-qr",
		Status:            "active",
		CredentialRef:     credentialRef,
		BaseURL:           ts.URL,
		DefaultForChannel: true,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	})
	runtime := agent.NewRuntime(st, toolhub.New(cfg, st), policy.New(cfg), modelrouter.New(cfg), nil)
	dispatcher := NewDispatcherWithConfig(st, runtime, cfg).WithCredentialVault(vault).WithResultDeliverer(newWeixinTestResultDeliverer(t, st, cfg))

	syncer := NewSyncer(st).WithCredentialVault(vault).WithDispatcher(dispatcher)
	syncer.Tick(t.Context(), weixinTestRuntimeScope())
	syncer.Wait()

	if strings.Join(sentRecipients, ",") != "wx-user-a,wx-user-b" {
		t.Fatalf("expected replies to separate weixin users, got %#v", sentRecipients)
	}
	sessionA, okA := st.FindExternalChatSession("bind_1", "wx-user-a", "")
	sessionB, okB := st.FindExternalChatSession("bind_1", "wx-user-b", "")
	if !okA || !okB {
		t.Fatalf("expected separate weixin chat sessions, got A=%#v ok=%v B=%#v ok=%v", sessionA, okA, sessionB, okB)
	}
	if sessionA.LinkedSessionID == "" || sessionB.LinkedSessionID == "" || sessionA.LinkedSessionID == sessionB.LinkedSessionID {
		t.Fatalf("weixin users should have isolated linked sessions: %#v %#v", sessionA, sessionB)
	}
	if sessionA.OwnerID == "" || sessionB.OwnerID == "" || sessionA.OwnerID == sessionB.OwnerID {
		t.Fatalf("weixin users should have isolated owner profiles: %#v %#v", sessionA, sessionB)
	}
	profileA, okA, err := st.GetOwnerProfileByID(context.Background(), sessionA.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	profileB, okB, err := st.GetOwnerProfileByID(context.Background(), sessionB.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
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
	binding, _ := storetest.MustGetNotificationBinding(t, st, "bind_1")
	if binding.ExternalUserID != "" {
		t.Fatalf("binding should not be pinned to first chat user, got %#v", binding.ExternalUserID)
	}
}

func TestSyncerDoesNotInferMediaPartsFromMarkdown(t *testing.T) {
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
							{"type": 1, "text_item": {"text": "查询上海天气\nMOCK_STEP_RESPONSE:{\"type\":\"final\",\"answer\":\"![天气卡片](media/20260702/weather.png)\"}"}}
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
	vault := newWeixinTestVault(t, st)
	credentialRef := sealWeixinTestCredential(t, vault, "bind_1", "bot-secret")
	storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
		ID:                "bind_1",
		OwnerID:           app.DefaultOwnerID,
		Channel:           "weixin",
		Provider:          "openclaw-weixin-qr",
		Status:            "active",
		ExternalUserID:    "wx-user-1",
		CredentialRef:     credentialRef,
		BaseURL:           ts.URL,
		DefaultForChannel: true,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	})
	runtime := agent.NewRuntime(st, toolhub.New(cfg, st), policy.New(cfg), modelrouter.New(cfg), nil)
	dispatcher := NewDispatcher(st, runtime, cfg.Tools.Notifications.Channels["weixin"]).WithCredentialVault(vault).WithResultDeliverer(newWeixinTestResultDeliverer(t, st, cfg))

	syncer := NewSyncer(st).WithCredentialVault(vault).WithDispatcher(dispatcher)
	syncer.Tick(t.Context(), weixinTestRuntimeScope())
	syncer.Wait()

	if sawUpload {
		t.Fatal("Markdown text must not bypass the structured MessagePart contract")
	}
	if sentImageItems != 0 || sentTextItems != 1 {
		t.Fatalf("expected Markdown to remain one text part, got image=%d text=%d", sentImageItems, sentTextItems)
	}
	if sentContext != "ctx-1" {
		t.Fatalf("reply should use inbound context token, got %q", sentContext)
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

func TestSyncerKeepsCursorUntilDispatchSucceeds(t *testing.T) {
	var mu sync.Mutex
	var polledCursors []string
	var sendAttempts int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/ilink/bot/getupdates":
			var payload struct {
				GetUpdatesBuf string `json:"get_updates_buf"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			mu.Lock()
			polledCursors = append(polledCursors, payload.GetUpdatesBuf)
			mu.Unlock()
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
							{"type": 1, "text_item": {"text": "你好\nMOCK_STEP_RESPONSE:{\"type\":\"final\",\"answer\":\"回复\"}"}}
						]
					}
				]
			}`))
		case "/ilink/bot/getconfig":
			_, _ = w.Write([]byte(`{"ret":0,"typing_ticket":"typing-ticket-1"}`))
		case "/ilink/bot/sendtyping":
			_, _ = w.Write([]byte(`{"ret":0}`))
		case "/ilink/bot/sendmessage":
			mu.Lock()
			sendAttempts++
			failing := sendAttempts == 1
			mu.Unlock()
			if failing {
				w.WriteHeader(http.StatusInternalServerError)
				return
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
	vault := newWeixinTestVault(t, st)
	credentialRef := sealWeixinTestCredential(t, vault, "bind_1", "bot-secret")
	storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
		ID:                "bind_1",
		OwnerID:           app.DefaultOwnerID,
		Channel:           "weixin",
		Provider:          "openclaw-weixin-qr",
		Status:            "active",
		ExternalUserID:    "wx-user-1",
		CredentialRef:     credentialRef,
		BaseURL:           ts.URL,
		ProviderCursor:    "cursor-1",
		DefaultForChannel: true,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	})
	runtime := agent.NewRuntime(st, toolhub.New(cfg, st), policy.New(cfg), modelrouter.New(cfg), nil)
	dispatcher := NewDispatcherWithConfig(st, runtime, cfg).WithCredentialVault(vault).WithResultDeliverer(newWeixinTestResultDeliverer(t, st, cfg))
	syncer := NewSyncer(st).WithCredentialVault(vault).WithDispatcher(dispatcher)

	syncer.Tick(t.Context(), weixinTestRuntimeScope())
	syncer.Wait()

	binding, _ := storetest.MustGetNotificationBinding(t, st, "bind_1")
	if binding.ProviderCursor != "cursor-1" {
		t.Fatalf("cursor should not advance past failed dispatch, got %q", binding.ProviderCursor)
	}
	mu.Lock()
	attemptsAfterFirstTick := sendAttempts
	mu.Unlock()
	if attemptsAfterFirstTick != 1 {
		t.Fatalf("expected one send attempt on first tick, got %d", attemptsAfterFirstTick)
	}
	chatSession, ok := st.FindExternalChatSession("bind_1", "wx-user-1", "")
	if !ok {
		t.Fatal("weixin chat session missing after failed delivery")
	}
	failed, ok := st.FindExternalChatMessageByExternalID(chatSession.ID, "provider-msg-1")
	if !ok || failed.Status != "delivery_failed" {
		t.Fatalf("failed reply was not retained for delivery retry: %#v ok=%v", failed, ok)
	}
	runsAfterFirstTick := len(st.ListRuns(chatSession.LinkedSessionID))

	syncer.Tick(t.Context(), weixinTestRuntimeScope())
	syncer.Wait()

	binding, _ = storetest.MustGetNotificationBinding(t, st, "bind_1")
	if binding.ProviderCursor != "cursor-2" {
		t.Fatalf("cursor should advance once dispatch succeeds, got %q", binding.ProviderCursor)
	}
	mu.Lock()
	defer mu.Unlock()
	if sendAttempts != 2 {
		t.Fatalf("failed reply should be retried once, got %d send attempts", sendAttempts)
	}
	if strings.Join(polledCursors, ",") != "cursor-1,cursor-1" {
		t.Fatalf("second poll should reuse the unadvanced cursor: %#v", polledCursors)
	}
	if runsAfterRetry := len(st.ListRuns(chatSession.LinkedSessionID)); runsAfterRetry != runsAfterFirstTick {
		t.Fatalf("delivery retry reran the agent: before=%d after=%d", runsAfterFirstTick, runsAfterRetry)
	}
	delivered, _ := st.FindExternalChatMessageByExternalID(chatSession.ID, "provider-msg-1")
	if delivered.Status != "processed" || delivered.Error != "" {
		t.Fatalf("successful reply retry did not finalize the inbound record: %#v", delivered)
	}
}

func TestSyncerDispatchesBindingsInParallel(t *testing.T) {
	var bSentOnce sync.Once
	bSent := make(chan struct{})
	var aObservedB atomic.Bool

	newProviderServer := func(user string, onSend func()) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/ilink/bot/getupdates":
				_, _ = w.Write([]byte(`{
					"ret": 0,
					"get_updates_buf": "cursor-2",
					"msgs": [
						{
							"msg_id": "provider-msg-` + user + `",
							"from_user_id": "` + user + `",
							"context_token": "ctx-` + user + `",
							"create_time": 1782800000,
							"item_list": [
								{"type": 1, "text_item": {"text": "你好\nMOCK_STEP_RESPONSE:{\"type\":\"final\",\"answer\":\"回复\"}"}}
							]
						}
					]
				}`))
			case "/ilink/bot/getconfig":
				_, _ = w.Write([]byte(`{"ret":0,"typing_ticket":"typing-ticket-1"}`))
			case "/ilink/bot/sendtyping":
				_, _ = w.Write([]byte(`{"ret":0}`))
			case "/ilink/bot/sendmessage":
				onSend()
				_, _ = w.Write([]byte(`{"ret":0}`))
			default:
				http.NotFound(w, r)
			}
		}))
	}
	serverA := newProviderServer("wx-user-a", func() {
		select {
		case <-bSent:
			aObservedB.Store(true)
		case <-time.After(10 * time.Second):
		}
	})
	defer serverA.Close()
	serverB := newProviderServer("wx-user-b", func() {
		bSentOnce.Do(func() { close(bSent) })
	})
	defer serverB.Close()

	cfg := config.Default()
	cfg.Model.Mock = true
	cfg.Workspaces.DefaultRoot = t.TempDir()
	cfg.Workspaces.Allowlist = []string{cfg.Workspaces.DefaultRoot}
	cfg.Tools.Notifications.Channels["weixin"] = config.NotificationChannelConfig{
		Enabled:  true,
		Provider: "openclaw-weixin-qr",
	}
	st := store.NewMemoryStore()
	vault := newWeixinTestVault(t, st)
	for id, baseURL := range map[string]string{"bind_a": serverA.URL, "bind_b": serverB.URL} {
		credentialRef := sealWeixinTestCredential(t, vault, id, "bot-secret")
		storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{
			ID:            id,
			OwnerID:       app.DefaultOwnerID,
			Channel:       "weixin",
			Provider:      "openclaw-weixin-qr",
			Status:        "active",
			CredentialRef: credentialRef,
			BaseURL:       baseURL,
			CreatedAt:     time.Now().UTC(),
			UpdatedAt:     time.Now().UTC(),
		})
	}
	runtime := agent.NewRuntime(st, toolhub.New(cfg, st), policy.New(cfg), modelrouter.New(cfg), nil)
	dispatcher := NewDispatcherWithConfig(st, runtime, cfg).WithCredentialVault(vault).WithResultDeliverer(newWeixinTestResultDeliverer(t, st, cfg))
	syncer := NewSyncer(st).WithCredentialVault(vault).WithDispatcher(dispatcher)

	syncer.Tick(t.Context(), weixinTestRuntimeScope())
	syncer.Wait()

	if !aObservedB.Load() {
		t.Fatal("binding B's dispatch should complete while binding A's dispatch is still running")
	}
	if _, ok := st.FindExternalChatSession("bind_a", "wx-user-a", ""); !ok {
		t.Fatal("binding A chat session missing")
	}
	if _, ok := st.FindExternalChatSession("bind_b", "wx-user-b", ""); !ok {
		t.Fatal("binding B chat session missing")
	}
}

func TestHandleInboundRetriesPreviouslyFailedMessage(t *testing.T) {
	var mu sync.Mutex
	var sentTexts []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/ilink/bot/getconfig":
			_, _ = w.Write([]byte(`{"ret":0,"typing_ticket":"typing-ticket-1"}`))
		case "/ilink/bot/sendtyping":
			_, _ = w.Write([]byte(`{"ret":0}`))
		case "/ilink/bot/sendmessage":
			var payload struct {
				Msg struct {
					ItemList []struct {
						TextItem struct {
							Text string `json:"text"`
						} `json:"text_item"`
					} `json:"item_list"`
				} `json:"msg"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			mu.Lock()
			if len(payload.Msg.ItemList) > 0 {
				sentTexts = append(sentTexts, payload.Msg.ItemList[0].TextItem.Text)
			}
			mu.Unlock()
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
	vault := newWeixinTestVault(t, st)
	credentialRef := sealWeixinTestCredential(t, vault, "bind_1", "bot-secret")
	binding := app.NotificationBinding{
		ID:            "bind_1",
		Channel:       "weixin",
		Provider:      "openclaw-weixin-qr",
		Status:        "active",
		CredentialRef: credentialRef,
		BaseURL:       ts.URL,
	}
	storetest.MustCreateNotificationBinding(t, st, binding)
	runtime := agent.NewRuntime(st, toolhub.New(cfg, st), policy.New(cfg), modelrouter.New(cfg), nil)
	dispatcher := NewDispatcherWithConfig(st, runtime, cfg).WithCredentialVault(vault).WithResultDeliverer(newWeixinTestResultDeliverer(t, st, cfg))

	inbound := InboundMessage{
		Binding:      binding,
		FromUserID:   "wx-user-1",
		ContextToken: "ctx-1",
		Text:         "你好\nMOCK_CONVERSATION_RESPONSE:重试成功",
		ExternalID:   "provider-msg-retry",
	}
	chatSession, err := dispatcher.ensureChatSession(context.Background(), inbound)
	if err != nil {
		t.Fatal(err)
	}
	failed := st.SaveExternalChatMessage(app.WeixinChatMessage{
		ChatSessionID:     chatSession.ID,
		BindingID:         binding.ID,
		Direction:         "inbound",
		Role:              "user",
		ExternalMessageID: "provider-msg-retry",
		Content:           "你好",
		Status:            "failed",
		Error:             "model unavailable",
	})

	if err := dispatcher.HandleInbound(context.Background(), inbound); err != nil {
		t.Fatal(err)
	}

	retried, ok := st.FindExternalChatMessageByExternalID(chatSession.ID, "provider-msg-retry")
	if !ok {
		t.Fatal("inbound message missing after retry")
	}
	if retried.ID != failed.ID {
		t.Fatalf("retry should reuse the failed message record, got %q vs %q", retried.ID, failed.ID)
	}
	if retried.Status != "processed" {
		t.Fatalf("retried message should be processed, got %q", retried.Status)
	}
	inboundCount := 0
	for _, message := range st.ListExternalChatMessages(chatSession.ID, 20) {
		if message.Direction == "inbound" {
			inboundCount++
		}
	}
	if inboundCount != 1 {
		t.Fatalf("retry should not duplicate inbound chat history, got %d inbound messages", inboundCount)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sentTexts) != 1 || !strings.Contains(sentTexts[0], "重试成功") {
		t.Fatalf("expected one retry reply, got %#v", sentTexts)
	}

	runsBefore := len(st.ListRuns(chatSession.LinkedSessionID))
	if err := dispatcher.HandleInbound(context.Background(), inbound); err != nil {
		t.Fatal(err)
	}
	if runsAfter := len(st.ListRuns(chatSession.LinkedSessionID)); runsAfter != runsBefore {
		t.Fatalf("processed message must not run the agent again: before=%d after=%d", runsBefore, runsAfter)
	}
}

func TestSyncerDispatchAttemptsSurviveRestart(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/ilink/bot/getupdates":
			_, _ = w.Write([]byte(`{
				"ret": 0,
				"get_updates_buf": "cursor-2",
				"msgs": [
					{
						"msg_id": "provider-msg-stubborn",
						"from_user_id": "wx-user-1",
						"context_token": "ctx-1",
						"create_time": 1782800000,
						"item_list": [
							{"type": 1, "text_item": {"text": "你好"}}
						]
					}
				]
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	st := store.NewMemoryStore()
	vault := newWeixinTestVault(t, st)
	credentialRef := sealWeixinTestCredential(t, vault, "bind_1", "bot-secret")
	binding := app.NotificationBinding{
		ID:             "bind_1",
		OwnerID:        app.DefaultOwnerID,
		Channel:        "weixin",
		Provider:       "openclaw-weixin-qr",
		Status:         "active",
		ExternalUserID: "wx-user-1",
		CredentialRef:  credentialRef,
		BaseURL:        ts.URL,
		ProviderCursor: "cursor-1",
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	storetest.MustCreateNotificationBinding(t, st, binding)
	runtime := &fakeAgentRuntime{result: agent.Result{
		Run: app.AgentRun{ID: "run_stubborn", State: "completed"},
		WorkflowResult: &app.WorkflowResult{
			SchemaVersion: app.WorkflowResultSchemaVersion,
			ID:            "workflow_result_run_stubborn",
			RunID:         "run_stubborn",
			Status:        app.WorkflowResultSucceeded,
			Content: app.MessageContent{Parts: []app.MessagePart{{
				ID: "part_text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: "答案",
			}}},
		},
	}}
	transient := errors.New("provider connection reset")
	deliverer := &fakeResultDeliverer{errs: []error{transient, transient, transient}}
	dispatcher := NewDispatcher(st, runtime, config.NotificationChannelConfig{}).WithCredentialVault(vault).WithResultDeliverer(deliverer)

	chatSessionID := ""
	for tick, wantAttempts := range []int{1, 2, 3} {
		// A fresh Syncer per tick simulates a gateway restart between polls:
		// the retry budget must come from the store, not from syncer memory.
		syncer := NewSyncer(st).WithCredentialVault(vault).WithDispatcher(dispatcher)
		syncer.Tick(t.Context(), weixinTestRuntimeScope())
		syncer.Wait()
		chatSession, ok := st.FindExternalChatSession("bind_1", "wx-user-1", "")
		if !ok {
			t.Fatalf("tick %d: chat session missing", tick)
		}
		chatSessionID = chatSession.ID
		record, ok := st.FindExternalChatMessageByExternalID(chatSession.ID, "provider-msg-stubborn")
		if !ok || record.DispatchAttempts != wantAttempts {
			t.Fatalf("tick %d: attempts should persist across restarts, want %d got %#v ok=%v", tick, wantAttempts, record, ok)
		}
		updated, _ := storetest.MustGetNotificationBinding(t, st, "bind_1")
		wantCursor := "cursor-1"
		if wantAttempts >= maxDispatchAttempts {
			wantCursor = "cursor-2"
		}
		if updated.ProviderCursor != wantCursor {
			t.Fatalf("tick %d: cursor %q, want %q", tick, updated.ProviderCursor, wantCursor)
		}
	}
	if got := runtime.handledCount(); got != 1 {
		t.Fatalf("only the first dispatch may run the agent, got %d handles", got)
	}
	if got := len(deliverer.deliveredResults()); got != 3 {
		t.Fatalf("each dispatch should retry only the delivery step, got %d deliveries", got)
	}
	record, _ := st.FindExternalChatMessageByExternalID(chatSessionID, "provider-msg-stubborn")
	if record.Status != "delivery_failed" || record.Error == "" {
		t.Fatalf("dropped message should keep its failure state for later redeliveries: %#v", record)
	}
}

func TestSyncerAdvancesCursorPastBlockedDelivery(t *testing.T) {
	var mu sync.Mutex
	var polledCursors []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/ilink/bot/getupdates":
			var payload struct {
				GetUpdatesBuf string `json:"get_updates_buf"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			mu.Lock()
			polledCursors = append(polledCursors, payload.GetUpdatesBuf)
			mu.Unlock()
			_, _ = w.Write([]byte(`{
				"ret": 0,
				"get_updates_buf": "cursor-2",
				"msgs": [
					{
						"msg_id": "provider-msg-blocked",
						"from_user_id": "wx-user-1",
						"context_token": "ctx-1",
						"create_time": 1782800000,
						"item_list": [
							{"type": 1, "text_item": {"text": "你好"}}
						]
					}
				]
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	st := store.NewMemoryStore()
	vault := newWeixinTestVault(t, st)
	credentialRef := sealWeixinTestCredential(t, vault, "bind_1", "bot-secret")
	binding := app.NotificationBinding{
		ID:             "bind_1",
		OwnerID:        app.DefaultOwnerID,
		Channel:        "weixin",
		Provider:       "openclaw-weixin-qr",
		Status:         "active",
		ExternalUserID: "wx-user-1",
		CredentialRef:  credentialRef,
		BaseURL:        ts.URL,
		ProviderCursor: "cursor-1",
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	storetest.MustCreateNotificationBinding(t, st, binding)
	runtime := &fakeAgentRuntime{result: agent.Result{
		Run: app.AgentRun{ID: "run_blocked_sync", State: "completed"},
		WorkflowResult: &app.WorkflowResult{
			SchemaVersion: app.WorkflowResultSchemaVersion,
			ID:            "workflow_result_run_blocked_sync",
			RunID:         "run_blocked_sync",
			Status:        app.WorkflowResultSucceeded,
			Content: app.MessageContent{Parts: []app.MessagePart{{
				ID: "part_text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: "答案",
			}}},
		},
	}}
	deliverer := &fakeResultDeliverer{errs: []error{
		delivery.NewError(delivery.CodeBindingUnavailable, "weixin binding is unavailable", "blocked"),
	}}
	dispatcher := NewDispatcher(st, runtime, config.NotificationChannelConfig{}).WithCredentialVault(vault).WithResultDeliverer(deliverer)
	syncer := NewSyncer(st).WithCredentialVault(vault).WithDispatcher(dispatcher)

	syncer.Tick(t.Context(), weixinTestRuntimeScope())
	syncer.Wait()

	updated, _ := storetest.MustGetNotificationBinding(t, st, "bind_1")
	if updated.ProviderCursor != "cursor-2" {
		t.Fatalf("cursor must advance past a blocked delivery, got %q", updated.ProviderCursor)
	}
	chatSession, ok := st.FindExternalChatSession("bind_1", "wx-user-1", "")
	if !ok {
		t.Fatal("chat session missing")
	}
	blocked, ok := st.FindExternalChatMessageByExternalID(chatSession.ID, "provider-msg-blocked")
	if !ok || blocked.Status != "delivery_blocked" || blocked.Error == "" {
		t.Fatalf("blocked message should be terminal with a recorded reason: %#v ok=%v", blocked, ok)
	}

	syncer.Tick(t.Context(), weixinTestRuntimeScope())
	syncer.Wait()

	if got := runtime.handledCount(); got != 1 {
		t.Fatalf("blocked message must not be dispatched again, got %d handles", got)
	}
	if got := len(deliverer.deliveredResults()); got != 1 {
		t.Fatalf("blocked message must not be re-delivered, got %d deliveries", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(polledCursors, ",") != "cursor-1,cursor-2" {
		t.Fatalf("second poll should use the advanced cursor: %#v", polledCursors)
	}
}
