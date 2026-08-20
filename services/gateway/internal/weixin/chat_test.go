package weixin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// fakeAgentRuntime counts how often the dispatcher enters the agent runtime.
// It deliberately implements only the base AgentRuntime interface so that any
// dispatcher fallback to it stands for "the workflow ran again".
type fakeAgentRuntime struct {
	mu       sync.Mutex
	handled  int
	executed int
	result   agent.Result
	err      error
}

func (f *fakeAgentRuntime) HandleMessageWithAttachments(_ context.Context, _ string, _ string, _ []agent.MessageAttachment) (agent.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handled++
	return f.result, f.err
}

func (f *fakeAgentRuntime) ExecuteApprovedToolCall(context.Context, app.Approval) (app.ToolCall, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.executed++
	return app.ToolCall{}, nil
}

func (f *fakeAgentRuntime) executedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.executed
}

func (f *fakeAgentRuntime) ResumeRunAfterApproval(context.Context, string, string) (agent.Result, bool, error) {
	return agent.Result{}, false, nil
}

func (f *fakeAgentRuntime) CompleteRunIfApprovalsResolved(string) {}

func (f *fakeAgentRuntime) handledCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.handled
}

// fakeResultDeliverer records delivered workflow results and fails with the
// queued errors first.
type fakeResultDeliverer struct {
	mu        sync.Mutex
	delivered []app.WorkflowResult
	errs      []error
}

func (f *fakeResultDeliverer) DeliverWorkflowResult(_ context.Context, result app.WorkflowResult) (app.DeliveryReceipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delivered = append(f.delivered, result)
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		if err != nil {
			return app.DeliveryReceipt{}, err
		}
	}
	return app.DeliveryReceipt{Status: app.DeliverySucceeded}, nil
}

func (f *fakeResultDeliverer) deliveredResults() []app.WorkflowResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]app.WorkflowResult(nil), f.delivered...)
}

func newPendingReplyTestBinding() app.NotificationBinding {
	return app.NotificationBinding{
		ID:            "bind_1",
		OwnerID:       app.DefaultOwnerID,
		Channel:       "weixin",
		Provider:      "openclaw-weixin-qr",
		Status:        "active",
		CredentialRef: "provider:openclaw-weixin-qr:bind_1",
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
}

func TestHandleInboundDeliveryRetryDoesNotRerunRuntime(t *testing.T) {
	st := store.NewMemoryStore()
	binding := newPendingReplyTestBinding()
	st.SaveNotificationBinding(binding)
	completed := time.Now().UTC()
	runtime := &fakeAgentRuntime{result: agent.Result{
		Run: app.AgentRun{ID: "run_pending", State: "completed", CompletedAt: &completed},
		WorkflowResult: &app.WorkflowResult{
			SchemaVersion: app.WorkflowResultSchemaVersion,
			ID:            "workflow_result_run_pending",
			RunID:         "run_pending",
			Status:        app.WorkflowResultSucceeded,
			Content: app.MessageContent{Parts: []app.MessagePart{{
				ID: "part_text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: "答案",
			}}},
		},
	}}
	deliverer := &fakeResultDeliverer{errs: []error{errors.New("provider timeout")}}
	dispatcher := NewDispatcher(st, runtime, config.NotificationChannelConfig{}).WithResultDeliverer(deliverer)
	inbound := InboundMessage{
		Binding:      binding,
		FromUserID:   "wx-user-1",
		ContextToken: "ctx-1",
		Text:         "查一下天气",
		ExternalID:   "provider-msg-pending",
	}

	if err := dispatcher.HandleInbound(context.Background(), inbound); err == nil {
		t.Fatal("expected the failed delivery to surface an error")
	}
	chatSession, ok := st.FindExternalChatSession(binding.ID, "wx-user-1", "")
	if !ok {
		t.Fatal("chat session missing")
	}
	failed, ok := st.FindExternalChatMessageByExternalID(chatSession.ID, "provider-msg-pending")
	if !ok || failed.Status != "delivery_failed" {
		t.Fatalf("inbound record should be delivery_failed: %#v ok=%v", failed, ok)
	}
	if failed.PendingReplyKind != pendingReplyWorkflowResult || failed.PendingReply == "" {
		t.Fatalf("produced reply was not persisted for the retry: %#v", failed)
	}
	if failed.LinkedRunID != "run_pending" {
		t.Fatalf("delivery_failed record should link the completed run: %#v", failed)
	}
	if got := runtime.handledCount(); got != 1 {
		t.Fatalf("first dispatch should run the agent once, got %d", got)
	}

	if err := dispatcher.HandleInbound(context.Background(), inbound); err != nil {
		t.Fatalf("delivery retry should succeed: %v", err)
	}
	if got := runtime.handledCount(); got != 1 {
		t.Fatalf("delivery retry must not re-enter the agent runtime, got %d handles", got)
	}
	delivered := deliverer.deliveredResults()
	if len(delivered) != 2 {
		t.Fatalf("expected the delivery step to run twice, got %d", len(delivered))
	}
	if delivered[1].RunID != "run_pending" || len(delivered[1].Content.Parts) != 1 || delivered[1].Content.Parts[0].Text != "答案" {
		t.Fatalf("retried delivery should reuse the persisted reply: %#v", delivered[1])
	}
	retried, _ := st.FindExternalChatMessageByExternalID(chatSession.ID, "provider-msg-pending")
	if retried.Status != "processed" || retried.Error != "" || retried.PendingReplyKind != "" || retried.PendingReply != "" {
		t.Fatalf("successful retry should finalize the record: %#v", retried)
	}

	if err := dispatcher.HandleInbound(context.Background(), inbound); err != nil {
		t.Fatalf("re-delivery of a processed message should be a no-op: %v", err)
	}
	if got := runtime.handledCount(); got != 1 {
		t.Fatalf("processed message must not run the agent again, got %d handles", got)
	}
	if got := len(deliverer.deliveredResults()); got != 2 {
		t.Fatalf("processed message must not be re-delivered, got %d deliveries", got)
	}
}

func TestHandleInboundMarksBlockedDeliveryTerminal(t *testing.T) {
	st := store.NewMemoryStore()
	binding := newPendingReplyTestBinding()
	st.SaveNotificationBinding(binding)
	runtime := &fakeAgentRuntime{result: agent.Result{
		Run: app.AgentRun{ID: "run_blocked", State: "completed"},
		WorkflowResult: &app.WorkflowResult{
			SchemaVersion: app.WorkflowResultSchemaVersion,
			ID:            "workflow_result_run_blocked",
			RunID:         "run_blocked",
			Status:        app.WorkflowResultSucceeded,
			Content: app.MessageContent{Parts: []app.MessagePart{{
				ID: "part_text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: "答案",
			}}},
		},
	}}
	blockedErr := delivery.NewError(delivery.CodeConnectorDisabled, "delivery connector is disabled", "blocked")
	deliverer := &fakeResultDeliverer{errs: []error{blockedErr}}
	dispatcher := NewDispatcher(st, runtime, config.NotificationChannelConfig{}).WithResultDeliverer(deliverer)
	inbound := InboundMessage{
		Binding:      binding,
		FromUserID:   "wx-user-1",
		ContextToken: "ctx-1",
		Text:         "发个结果",
		ExternalID:   "provider-msg-blocked",
	}

	err := dispatcher.HandleInbound(context.Background(), inbound)
	if !delivery.IsBlocked(err) {
		t.Fatalf("expected the blocked delivery error to surface, got %v", err)
	}
	chatSession, ok := st.FindExternalChatSession(binding.ID, "wx-user-1", "")
	if !ok {
		t.Fatal("chat session missing")
	}
	blocked, ok := st.FindExternalChatMessageByExternalID(chatSession.ID, "provider-msg-blocked")
	if !ok || blocked.Status != "delivery_blocked" {
		t.Fatalf("blocked delivery should be terminal on the record: %#v ok=%v", blocked, ok)
	}
	if blocked.Error == "" || blocked.PendingReplyKind != "" || blocked.PendingReply != "" {
		t.Fatalf("blocked record should keep the reason and drop the retry payload: %#v", blocked)
	}

	if err := dispatcher.HandleInbound(context.Background(), inbound); err != nil {
		t.Fatalf("re-delivery of a blocked message should be a no-op: %v", err)
	}
	if got := runtime.handledCount(); got != 1 {
		t.Fatalf("blocked message must not run the agent again, got %d handles", got)
	}
	if got := len(deliverer.deliveredResults()); got != 1 {
		t.Fatalf("blocked message must not be re-delivered, got %d deliveries", got)
	}
}

// controlProviderServer fails the first failSends /sendmessage calls with an
// HTTP 500 and records every text it was asked to send.
func controlProviderServer(t *testing.T, failSends int) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var sentTexts []string
	remainingFailures := failSends
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
			failing := remainingFailures > 0
			if failing {
				remainingFailures--
			}
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
	t.Cleanup(ts.Close)
	return ts, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), sentTexts...)
	}
}

func newControlReplyTestDispatcher(t *testing.T, st *store.MemoryStore, runtime *fakeAgentRuntime, baseURL string) (*Dispatcher, app.NotificationBinding) {
	t.Helper()
	vault := newWeixinTestVault(t, st)
	credentialRef := sealWeixinTestCredential(t, vault, "bind_1", "bot-secret")
	binding := newPendingReplyTestBinding()
	binding.CredentialRef = credentialRef
	binding.BaseURL = baseURL
	st.SaveNotificationBinding(binding)
	cfg := config.NotificationChannelConfig{Enabled: true, Provider: "openclaw-weixin-qr", BaseURL: baseURL}
	return NewDispatcher(st, runtime, cfg).WithCredentialVault(vault), binding
}

func TestClearConversationReplyIsRetriedWithoutRepeatingTheClear(t *testing.T) {
	ts, sentTexts := controlProviderServer(t, 1)
	st := store.NewMemoryStore()
	runtime := &fakeAgentRuntime{}
	dispatcher, binding := newControlReplyTestDispatcher(t, st, runtime, ts.URL)
	inbound := InboundMessage{
		Binding:      binding,
		FromUserID:   "wx-user-1",
		ContextToken: "ctx-1",
		Text:         "清空对话",
		ExternalID:   "provider-msg-clear",
	}

	if err := dispatcher.HandleInbound(context.Background(), inbound); err == nil {
		t.Fatal("expected the failed confirmation send to surface an error")
	}
	chatSession, ok := st.FindExternalChatSession(binding.ID, "wx-user-1", "")
	if !ok {
		t.Fatal("chat session missing")
	}
	clearedSessionID := chatSession.LinkedSessionID
	failed, ok := st.FindExternalChatMessageByExternalID(chatSession.ID, "provider-msg-clear")
	if !ok || failed.Status != "delivery_failed" || failed.PendingReplyKind != pendingReplyControlText {
		t.Fatalf("clear-conversation confirmation should be retryable: %#v ok=%v", failed, ok)
	}

	if err := dispatcher.HandleInbound(context.Background(), inbound); err != nil {
		t.Fatalf("confirmation resend should succeed: %v", err)
	}
	chatSession, _ = st.FindExternalChatSession(binding.ID, "wx-user-1", "")
	if chatSession.LinkedSessionID != clearedSessionID {
		t.Fatalf("retry must not clear the conversation again: %q -> %q", clearedSessionID, chatSession.LinkedSessionID)
	}
	if got := runtime.handledCount(); got != 0 {
		t.Fatalf("clear-conversation must never reach the agent, got %d handles", got)
	}
	retried, _ := st.FindExternalChatMessageByExternalID(chatSession.ID, "provider-msg-clear")
	if retried.Status != "processed" || retried.PendingReply != "" {
		t.Fatalf("successful resend should finalize the record: %#v", retried)
	}
	texts := sentTexts()
	if len(texts) != 2 || !strings.Contains(texts[1], "对话已清空") {
		t.Fatalf("expected the confirmation to be resent, got %#v", texts)
	}
}

func TestAttachmentPromptIsRetriedWithoutDuplicatingContext(t *testing.T) {
	ts, sentTexts := controlProviderServer(t, 1)
	st := store.NewMemoryStore()
	runtime := &fakeAgentRuntime{}
	dispatcher, binding := newControlReplyTestDispatcher(t, st, runtime, ts.URL)
	inbound := InboundMessage{
		Binding:      binding,
		FromUserID:   "wx-user-1",
		ContextToken: "ctx-1",
		ExternalID:   "provider-msg-attach",
		Attachments: []app.MessageAttachment{{
			Name:        "报表.xlsx",
			RelPath:     "in/报表.xlsx",
			ContentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
			Bytes:       128,
		}},
	}

	if err := dispatcher.HandleInbound(context.Background(), inbound); err == nil {
		t.Fatal("expected the failed clarification send to surface an error")
	}
	chatSession, ok := st.FindExternalChatSession(binding.ID, "wx-user-1", "")
	if !ok {
		t.Fatal("chat session missing")
	}
	failed, ok := st.FindExternalChatMessageByExternalID(chatSession.ID, "provider-msg-attach")
	if !ok || failed.Status != "delivery_failed" || failed.PendingReplyKind != pendingReplyAttachmentPrompt {
		t.Fatalf("attachment clarification should be retryable: %#v ok=%v", failed, ok)
	}
	contextMessages := len(st.ListMessages(chatSession.LinkedSessionID))

	if err := dispatcher.HandleInbound(context.Background(), inbound); err != nil {
		t.Fatalf("clarification resend should succeed: %v", err)
	}
	retried, _ := st.FindExternalChatMessageByExternalID(chatSession.ID, "provider-msg-attach")
	if retried.Status != "needs_user_instruction" || retried.PendingReply != "" {
		t.Fatalf("successful resend should restore the pending-instruction state: %#v", retried)
	}
	if got := len(st.ListMessages(chatSession.LinkedSessionID)); got != contextMessages {
		t.Fatalf("retry must not duplicate the pending attachment context: before=%d after=%d", contextMessages, got)
	}
	if got := runtime.handledCount(); got != 0 {
		t.Fatalf("attachment-only inbound must not reach the agent, got %d handles", got)
	}
	texts := sentTexts()
	if len(texts) != 2 || !strings.Contains(texts[1], "我已收到") {
		t.Fatalf("expected the clarification prompt to be resent, got %#v", texts)
	}
}

func TestApprovalReplyConfirmationIsRetriedWithoutReexecuting(t *testing.T) {
	ts, sentTexts := controlProviderServer(t, 1)
	st := store.NewMemoryStore()
	runtime := &fakeAgentRuntime{}
	dispatcher, binding := newControlReplyTestDispatcher(t, st, runtime, ts.URL)
	inbound := InboundMessage{
		Binding:      binding,
		FromUserID:   "wx-user-1",
		ContextToken: "ctx-1",
		Text:         "是",
		ExternalID:   "provider-msg-approve",
	}
	chatSession, err := dispatcher.ensureChatSession(context.Background(), inbound)
	if err != nil {
		t.Fatal(err)
	}
	st.SaveApproval(app.Approval{
		ID:         "appr_1",
		SessionID:  chatSession.LinkedSessionID,
		RunID:      "run_appr",
		ToolCallID: "call_missing",
		Tool:       "shell.exec_sandboxed",
		Status:     "pending",
		Summary:    "执行一条沙箱命令",
	})

	if err := dispatcher.HandleInbound(context.Background(), inbound); err == nil {
		t.Fatal("expected the failed confirmation send to surface an error")
	}
	if approval, ok := st.GetApproval("appr_1"); !ok || approval.Status != "approved" {
		t.Fatalf("approval should be resolved on the first dispatch: %#v ok=%v", approval, ok)
	}
	if got := runtime.executedCount(); got != 1 {
		t.Fatalf("approved tool should run exactly once, got %d", got)
	}
	failed, ok := st.FindExternalChatMessageByExternalID(chatSession.ID, "provider-msg-approve")
	if !ok || failed.Status != "delivery_failed" || failed.PendingReplyKind != pendingReplyControlText || failed.LinkedRunID != "run_appr" {
		t.Fatalf("approval confirmation should be retryable: %#v ok=%v", failed, ok)
	}

	if err := dispatcher.HandleInbound(context.Background(), inbound); err != nil {
		t.Fatalf("confirmation resend should succeed: %v", err)
	}
	if got := runtime.executedCount(); got != 1 {
		t.Fatalf("retry must not execute the approved tool again, got %d", got)
	}
	if got := runtime.handledCount(); got != 0 {
		t.Fatalf("approval reply must not leak to the agent as a normal message, got %d handles", got)
	}
	retried, _ := st.FindExternalChatMessageByExternalID(chatSession.ID, "provider-msg-approve")
	if retried.Status != "processed" || retried.PendingReply != "" {
		t.Fatalf("successful resend should finalize the record: %#v", retried)
	}
	texts := sentTexts()
	if len(texts) != 2 || !strings.Contains(texts[1], "已确认并执行") {
		t.Fatalf("expected the confirmation to be resent, got %#v", texts)
	}
}
