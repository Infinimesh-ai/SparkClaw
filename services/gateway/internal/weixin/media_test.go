package weixin

import (
	"context"
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
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/weixinproto"
)

func TestMediaAdapterDownloadsInboundImageAsAttachment(t *testing.T) {
	png := tinyPNG(t)
	key := []byte("0123456789abcdef")
	encrypted, err := weixinproto.EncryptAESECBPKCS7(png, key)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(encrypted)
	}))
	defer ts.Close()

	root := t.TempDir()
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Tools.Notifications.Channels["weixin"] = config.NotificationChannelConfig{
		Enabled:    true,
		Provider:   "openclaw-weixin-qr",
		BaseURL:    "https://ilinkai.weixin.qq.com",
		CDNBaseURL: defaultWeixinCDNBaseURL,
	}
	st := store.NewMemoryStore()
	adapter := NewMediaAdapter(cfg, st)

	attachment, err := adapter.DownloadInboundImage(context.Background(), app.NotificationBinding{
		ID:        "bind_1",
		Channel:   "weixin",
		Provider:  "openclaw-weixin-qr",
		Status:    "active",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}, imageItem{
		Media: cdnMedia{
			FullURL: ts.URL,
			AESKey:  base64.StdEncoding.EncodeToString(key),
		},
	}, "session_1", "provider-msg-1")
	if err != nil {
		t.Fatal(err)
	}
	if attachment.RelPath == "" || attachment.ArtifactID == "" {
		t.Fatalf("attachment missing fields: %#v", attachment)
	}
	if attachment.ContentType != "image/png" {
		t.Fatalf("unexpected content type: %q", attachment.ContentType)
	}
	if !strings.HasPrefix(attachment.RelPath, "media/") {
		t.Fatalf("attachment should be saved under media: %#v", attachment)
	}
	raw, err := os.ReadFile(filepath.Join(root, attachment.RelPath))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(png) {
		t.Fatal("saved image content does not match decrypted plaintext")
	}
	objects := st.ListArtifactObjects(10)
	if len(objects) != 1 || objects[0].Kind != "weixin_image_upload" {
		t.Fatalf("expected weixin image artifact, got %#v", objects)
	}
}

func TestAttachmentClarificationPromptDistinguishesDocuments(t *testing.T) {
	got := attachmentClarificationPrompt([]app.MessageAttachment{{
		Name:        "report.docx",
		RelPath:     "uploads/20260707/report.docx",
		ContentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	}})
	if !strings.Contains(got, "我已收到文档") || !strings.Contains(got, "你想让我") || !strings.Contains(got, "修改 Word/Excel/PPT/PDF") {
		t.Fatalf("unexpected document attachment prompt: %q", got)
	}
	image := attachmentClarificationPrompt([]app.MessageAttachment{{
		Name:        "photo.png",
		RelPath:     "media/20260707/photo.png",
		ContentType: "image/png",
	}})
	if !strings.Contains(image, "我已收到图片") || !strings.Contains(image, "你想让我") || !strings.Contains(image, "直接读出图片内原文") {
		t.Fatalf("unexpected image attachment prompt: %q", image)
	}
}

func TestSendAssistantAnswerUploadsAbsoluteWeatherCardArtifact(t *testing.T) {
	var sawUpload bool
	var sentTextItems int
	var sentImageItems int
	var serverURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/ilink/bot/getuploadurl":
			_, _ = w.Write([]byte(`{"upload_full_url":"` + serverURL + `/upload"}`))
		case "/upload":
			sawUpload = true
			w.Header().Set("x-encrypted-param", "download-param-weather")
			_, _ = w.Write([]byte(`ok`))
		case "/ilink/bot/sendmessage":
			var payload struct {
				Msg struct {
					ItemList []struct {
						Type int `json:"type"`
					} `json:"item_list"`
				} `json:"msg"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
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
	relPath := filepath.Join("media", "20260708", "weather_card.png")
	absPath := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatal(err)
	}
	rawImage := tinyPNG(t)
	if err := os.WriteFile(absPath, rawImage, 0o644); err != nil {
		t.Fatal(err)
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
	dispatcher := NewDispatcher(st, agent.Runtime{}, config.NotificationChannelConfig{
		Enabled:    true,
		Provider:   "openclaw-weixin-qr",
		BaseURL:    ts.URL,
		CDNBaseURL: ts.URL,
		Token:      "bot-token",
	})

	_, err := dispatcher.sendAssistantAnswer(context.Background(), InboundMessage{
		Binding: app.NotificationBinding{
			ID:             "bind_1",
			Channel:        "weixin",
			Provider:       "openclaw-weixin-qr",
			ExternalUserID: "wx-user",
			BaseURL:        ts.URL,
		},
		FromUserID:   "wx-user",
		ContextToken: "ctx-1",
	}, "![]("+absPath+")", "run-weather")
	if err != nil {
		t.Fatal(err)
	}
	if !sawUpload {
		t.Fatal("expected absolute weather card artifact to be uploaded as image")
	}
	if sentImageItems != 1 || sentTextItems != 0 {
		t.Fatalf("expected one image item and no text items, got image=%d text=%d", sentImageItems, sentTextItems)
	}
}

func TestHandleInboundAttachmentOnlyAsksForInstruction(t *testing.T) {
	var sentText string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ilink/bot/sendmessage" {
			http.NotFound(w, r)
			return
		}
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
		if len(payload.Msg.ItemList) > 0 {
			sentText = payload.Msg.ItemList[0].TextItem.Text
		}
		_, _ = w.Write([]byte(`{"ret":0}`))
	}))
	defer ts.Close()

	st := store.NewMemoryStore()
	binding := storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{ID: "bind_1", OwnerID: app.DefaultOwnerID, Channel: "weixin", Status: "active", ExternalUserID: "wx-user", BaseURL: ts.URL})
	dispatcher := NewDispatcher(st, agent.Runtime{}, config.NotificationChannelConfig{
		Enabled:  true,
		Provider: "openclaw-weixin-qr",
		BaseURL:  ts.URL,
		Token:    "bot-token",
	})
	err := dispatcher.HandleInbound(context.Background(), InboundMessage{
		Binding:      binding,
		FromUserID:   "wx-user",
		ContextToken: "ctx-1",
		ExternalID:   "msg-attachment-only",
		Attachments: []app.MessageAttachment{{
			Name:        "report.docx",
			RelPath:     "uploads/20260707/report.docx",
			ContentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
			Bytes:       1234,
			SHA256:      "abc123",
			Source:      "weixin_inbound",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sentText, "你想让我") || !strings.Contains(sentText, "总结内容") {
		t.Fatalf("expected clarification reply, got %q", sentText)
	}
	chatSession, ok := st.FindExternalChatSession("bind_1", "wx-user", "")
	if !ok {
		t.Fatal("expected weixin chat session")
	}
	agentMessages := storetest.MustListMessages(t, st, chatSession.LinkedSessionID)
	if len(agentMessages) != 1 || len(agentMessages[0].Attachments) != 1 || !strings.Contains(agentMessages[0].Content, "uploads/20260707/report.docx") {
		t.Fatalf("expected pending attachment in local agent context: %#v", agentMessages)
	}
	if runs := testListRuns(st, chatSession.LinkedSessionID); len(runs) != 0 {
		t.Fatalf("attachment-only clarification should not run agent: %#v", runs)
	}
}

func TestHandleInboundClearConversationStartsFreshAgentSession(t *testing.T) {
	var sentText string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ilink/bot/sendmessage" {
			http.NotFound(w, r)
			return
		}
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
		if len(payload.Msg.ItemList) > 0 {
			sentText = payload.Msg.ItemList[0].TextItem.Text
		}
		_, _ = w.Write([]byte(`{"ret":0}`))
	}))
	defer ts.Close()

	st := store.NewMemoryStore()
	root := t.TempDir()
	oldSession := storetest.MustCreateSessionWithScope(t, st, "wx", "owner", root, "weixin", true)
	storetest.MustAddMessage(t, st, app.Message{SessionID: oldSession.ID, Role: "user", Content: "旧问题", CreatedAt: time.Now().UTC()})
	testSaveEpisodeSummary(st, app.EpisodeSummary{SessionID: oldSession.ID, RunID: "run_old", Goal: "旧任务", Outcome: "completed", Summary: "旧摘要", CreatedAt: time.Now().UTC()})
	testSaveToolCall(st, app.ToolCall{ID: app.NewID("tc"), SessionID: oldSession.ID, RunID: "run_old", Tool: "files.read", Status: "completed", ObservationSummary: "old file context", StartedAt: time.Now().UTC()})
	chatSession := st.SaveExternalChatSession(app.WeixinChatSession{
		OwnerID:         "owner",
		WorkspaceRoot:   root,
		BindingID:       "bind_1",
		ExternalUserID:  "wx-user",
		LinkedSessionID: oldSession.ID,
		Status:          "active",
	})
	binding := storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{ID: chatSession.BindingID, OwnerID: "owner", Channel: "weixin", Status: "active", ExternalUserID: chatSession.ExternalUserID, BaseURL: ts.URL})
	dispatcher := NewDispatcher(st, agent.Runtime{}, config.NotificationChannelConfig{
		Enabled:  true,
		Provider: "openclaw-weixin-qr",
		BaseURL:  ts.URL,
		Token:    "bot-token",
	})

	err := dispatcher.HandleInbound(context.Background(), InboundMessage{
		Binding:      binding,
		FromUserID:   chatSession.ExternalUserID,
		ContextToken: "ctx-clear",
		ExternalID:   "clear-msg",
		Text:         "清空对话",
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, ok := st.FindExternalChatSession("bind_1", "wx-user", "")
	if !ok {
		t.Fatal("expected weixin chat session")
	}
	if updated.LinkedSessionID == "" || updated.LinkedSessionID == oldSession.ID {
		t.Fatalf("clear should link to a fresh Agent session: old=%s updated=%#v", oldSession.ID, updated)
	}
	if messages := storetest.MustListMessages(t, st, updated.LinkedSessionID); len(messages) != 0 {
		t.Fatalf("fresh Agent session should not carry old messages: %#v", messages)
	}
	if episodes := testListEpisodeSummaries(st, updated.LinkedSessionID); len(episodes) != 0 {
		t.Fatalf("fresh Agent session should not carry old episodes: %#v", episodes)
	}
	if calls := testListToolCalls(st, updated.LinkedSessionID); len(calls) != 0 {
		t.Fatalf("fresh Agent session should not carry old tool results: %#v", calls)
	}
	if !strings.Contains(sentText, "后续消息会从新的上下文开始") {
		t.Fatalf("expected clear confirmation reply, got %q", sentText)
	}
}

func TestHandleInboundApprovalReplyApprovesPendingAction(t *testing.T) {
	var sentText string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ilink/bot/sendmessage" {
			http.NotFound(w, r)
			return
		}
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
		if len(payload.Msg.ItemList) > 0 {
			sentText = payload.Msg.ItemList[0].TextItem.Text
		}
		_, _ = w.Write([]byte(`{"ret":0}`))
	}))
	defer ts.Close()

	st := store.NewMemoryStore()
	session := storetest.MustCreateSessionWithScope(t, st, "wx", "owner", t.TempDir(), "weixin", true)
	chatSession := st.SaveExternalChatSession(app.WeixinChatSession{BindingID: "bind_1", ExternalUserID: "wx-user", LinkedSessionID: session.ID, Status: "active"})
	binding := storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{ID: chatSession.BindingID, OwnerID: "owner", Channel: "weixin", Status: "active", ExternalUserID: chatSession.ExternalUserID, BaseURL: ts.URL})
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, State: "approval_pending", ModelLane: "fast", Risk: app.RiskReversible, StartedAt: time.Now().UTC()}
	testSaveRun(st, run)
	approvalID := app.NewID("ap")
	call := app.ToolCall{ID: app.NewID("tc"), SessionID: session.ID, RunID: run.ID, Tool: "notify.ask_approval", Risk: app.RiskReversible, Status: "approval_pending", ApprovalID: approvalID, StartedAt: time.Now().UTC()}
	testSaveToolCall(st, call)
	approval := app.Approval{ID: approvalID, SessionID: session.ID, RunID: run.ID, ToolCallID: call.ID, Tool: call.Tool, Risk: call.Risk, Status: "pending", Summary: "Approve notify", Arguments: map[string]any{}, CreatedAt: time.Now().UTC()}
	st.SaveApproval(approval)

	cfg := config.Default()
	cfg.Model.Mock = true
	hub := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, hub, policy.New(cfg), modelrouter.New(cfg), nil)
	dispatcher := NewDispatcherWithConfig(st, runtime, cfg)
	dispatcher.cfg = config.NotificationChannelConfig{Enabled: true, Provider: "openclaw-weixin-qr", BaseURL: ts.URL, Token: "bot-token"}
	err := dispatcher.HandleInbound(context.Background(), InboundMessage{
		Binding:      binding,
		FromUserID:   chatSession.ExternalUserID,
		ContextToken: "ctx-1",
		ExternalID:   "approve-msg",
		Text:         "是",
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ := testGetToolCall(st, call.ID)
	if resolved.Status != "completed_after_approval" {
		t.Fatalf("expected approved call to execute, got %#v sent=%q approvals=%#v", resolved, sentText, st.ListApprovals(""))
	}
	approvals := st.ListApprovals("")
	if len(approvals) != 1 || approvals[0].Status != "approved" {
		t.Fatalf("expected approved approval, got %#v", approvals)
	}
	if !strings.Contains(sentText, "已确认") {
		t.Fatalf("expected confirmation reply, got %q", sentText)
	}
}

func TestHandleInboundApprovalReplyRejectsPendingAction(t *testing.T) {
	var sentText string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ilink/bot/sendmessage" {
			http.NotFound(w, r)
			return
		}
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
		if len(payload.Msg.ItemList) > 0 {
			sentText = payload.Msg.ItemList[0].TextItem.Text
		}
		_, _ = w.Write([]byte(`{"ret":0}`))
	}))
	defer ts.Close()

	st := store.NewMemoryStore()
	session := storetest.MustCreateSessionWithScope(t, st, "wx", "owner", t.TempDir(), "weixin", true)
	chatSession := st.SaveExternalChatSession(app.WeixinChatSession{BindingID: "bind_1", ExternalUserID: "wx-user", LinkedSessionID: session.ID, Status: "active"})
	binding := storetest.MustCreateNotificationBinding(t, st, app.NotificationBinding{ID: chatSession.BindingID, OwnerID: "owner", Channel: "weixin", Status: "active", ExternalUserID: chatSession.ExternalUserID, BaseURL: ts.URL})
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, State: "approval_pending", ModelLane: "fast", Risk: app.RiskReversible, StartedAt: time.Now().UTC()}
	testSaveRun(st, run)
	call := app.ToolCall{ID: app.NewID("tc"), SessionID: session.ID, RunID: run.ID, Tool: "docx.replace_paragraph", Risk: app.RiskReversible, Status: "approval_pending", StartedAt: time.Now().UTC()}
	testSaveToolCall(st, call)
	approval := app.Approval{ID: app.NewID("ap"), SessionID: session.ID, RunID: run.ID, ToolCallID: call.ID, Tool: call.Tool, Risk: call.Risk, Status: "pending", Summary: "Approve docx", Arguments: map[string]any{}, CreatedAt: time.Now().UTC()}
	st.SaveApproval(approval)

	cfg := config.Default()
	cfg.Model.Mock = true
	hub := toolhub.New(cfg, st)
	runtime := agent.NewRuntime(st, hub, policy.New(cfg), modelrouter.New(cfg), nil)
	dispatcher := NewDispatcherWithConfig(st, runtime, cfg)
	dispatcher.cfg = config.NotificationChannelConfig{Enabled: true, Provider: "openclaw-weixin-qr", BaseURL: ts.URL, Token: "bot-token"}
	err := dispatcher.HandleInbound(context.Background(), InboundMessage{
		Binding:      binding,
		FromUserID:   chatSession.ExternalUserID,
		ContextToken: "ctx-1",
		ExternalID:   "reject-msg",
		Text:         "否",
	})
	if err != nil {
		t.Fatal(err)
	}
	rejected, _ := testGetToolCall(st, call.ID)
	if rejected.Status != "rejected" {
		t.Fatalf("expected call rejection, got %#v", rejected)
	}
	approvals := st.ListApprovals("")
	if len(approvals) != 1 || approvals[0].Status != "rejected" {
		t.Fatalf("expected rejected approval, got %#v", approvals)
	}
	if !strings.Contains(sentText, "已取消") {
		t.Fatalf("expected cancel reply, got %q", sentText)
	}
}

func TestWorkspaceFilePathOnlyReturnsLikelyOutputFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "uploads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "outputs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "uploads", "source.docx"), []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "outputs", "edited.docx"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := store.NewMemoryStore()
	session := storetest.MustCreateSessionWithScope(t, st, "wx", "owner", root, "weixin", true)
	chatSession := st.SaveExternalChatSession(app.WeixinChatSession{
		BindingID:       "bind_1",
		ExternalUserID:  "wx-user",
		LinkedSessionID: session.ID,
		WorkspaceRoot:   root,
		Status:          "active",
	})
	dispatcher := NewDispatcher(st, agent.Runtime{}, config.NotificationChannelConfig{})
	inbound := InboundMessage{Binding: app.NotificationBinding{ID: chatSession.BindingID}, FromUserID: chatSession.ExternalUserID}
	if _, _, ok, err := dispatcher.workspaceFilePath(t.Context(), "已读取 uploads/source.docx，内容如下。", inbound); err != nil || ok {
		t.Fatal("read-only uploads path should not be treated as a file reply")
	}
	path, name, ok, err := dispatcher.workspaceFilePath(t.Context(), "输出文件：outputs/edited.docx", inbound)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || name != "edited.docx" || !strings.HasSuffix(path, filepath.Join("outputs", "edited.docx")) {
		t.Fatalf("expected output file path, got path=%q name=%q ok=%v", path, name, ok)
	}
	path, name, ok, err = dispatcher.workspaceFilePath(t.Context(), "修改好的文件：outputs/edited.docx", inbound)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || name != "edited.docx" || !strings.HasSuffix(path, filepath.Join("outputs", "edited.docx")) {
		t.Fatalf("expected modified file path, got path=%q name=%q ok=%v", path, name, ok)
	}
}

func TestWeixinApprovalPromptUsesChineseUserFacingDetails(t *testing.T) {
	prompt := weixinApprovalPrompt(app.Approval{
		Tool:    "docx.replace_paragraph",
		Summary: "修改 Word 文档段落：uploads/report.docx",
		Arguments: map[string]any{
			"path":            "uploads/report.docx",
			"output_path":     "outputs/report-edited.docx",
			"paragraph_index": 25,
			"old_text":        "心得与体会原文",
			"text":            "新的心得与体会正文",
		},
	})
	for _, want := range []string{
		"需要你确认后才能执行",
		"操作：修改 Word 文档段落：uploads/report.docx",
		"文件：uploads/report.docx",
		"将生成：outputs/report-edited.docx",
		"目标段落：第 25 段",
		"原文：心得与体会原文",
		"修改为：新的心得与体会正文",
		"请回复“是”确认执行，或回复“否”取消执行。",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("approval prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, leaked := range []string{"approval_pending", "Approval:", "Tool:"} {
		if strings.Contains(prompt, leaked) {
			t.Fatalf("approval prompt leaked internal marker %q:\n%s", leaked, prompt)
		}
	}
}

func TestMediaAdapterDownloadsInboundFileAsUploadAttachment(t *testing.T) {
	content := []byte("hello from weixin document")
	key := []byte("0123456789abcdef")
	encrypted, err := weixinproto.EncryptAESECBPKCS7(content, key)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(encrypted)
	}))
	defer ts.Close()

	root := t.TempDir()
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Tools.Notifications.Channels["weixin"] = config.NotificationChannelConfig{
		Enabled:  true,
		Provider: "openclaw-weixin-qr",
		BaseURL:  "https://ilinkai.weixin.qq.com",
	}
	st := store.NewMemoryStore()
	adapter := NewMediaAdapter(cfg, st)

	attachment, err := adapter.DownloadInboundFile(context.Background(), app.NotificationBinding{
		ID:        "bind_1",
		Channel:   "weixin",
		Provider:  "openclaw-weixin-qr",
		Status:    "active",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}, fileItem{
		Media: cdnMedia{
			FullURL: ts.URL,
			AESKey:  base64.StdEncoding.EncodeToString(key),
		},
		FileName: "测试文档.txt",
		Len:      "26",
	}, "session_1", "provider-msg-1")
	if err != nil {
		t.Fatal(err)
	}
	if attachment.RelPath == "" || attachment.ArtifactID == "" {
		t.Fatalf("attachment missing fields: %#v", attachment)
	}
	if !strings.HasPrefix(attachment.RelPath, "uploads/") {
		t.Fatalf("file attachment should be saved under uploads: %#v", attachment)
	}
	if !strings.HasSuffix(attachment.RelPath, ".txt") {
		t.Fatalf("file attachment should preserve extension: %#v", attachment)
	}
	raw, err := os.ReadFile(filepath.Join(root, attachment.RelPath))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(content) {
		t.Fatal("saved file content does not match decrypted plaintext")
	}
	objects := st.ListArtifactObjects(10)
	if len(objects) != 1 || objects[0].Kind != "weixin_file_upload" {
		t.Fatalf("expected weixin file artifact, got %#v", objects)
	}
}

func TestMediaAdapterDownloadsPlainInboundFileWithoutAES(t *testing.T) {
	content := []byte("plain weixin attachment")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(content)
	}))
	defer ts.Close()

	root := t.TempDir()
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Tools.Notifications.Channels["weixin"] = config.NotificationChannelConfig{
		Enabled:  true,
		Provider: "openclaw-weixin-qr",
		BaseURL:  "https://ilinkai.weixin.qq.com",
	}
	st := store.NewMemoryStore()
	adapter := NewMediaAdapter(cfg, st)

	attachment, err := adapter.DownloadInboundFile(context.Background(), app.NotificationBinding{
		ID:        "bind_1",
		Channel:   "weixin",
		Provider:  "openclaw-weixin-qr",
		Status:    "active",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}, fileItem{
		Media: cdnMedia{
			FullURL: ts.URL,
		},
		FileName: "plain.txt",
	}, "session_1", "provider-msg-plain")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(attachment.RelPath, "uploads/") {
		t.Fatalf("file attachment should be saved under uploads: %#v", attachment)
	}
	raw, err := os.ReadFile(filepath.Join(root, attachment.RelPath))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(content) {
		t.Fatal("saved file content does not match plaintext download")
	}
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
