package weixin

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// fakeAgentRuntime counts how often the dispatcher enters the agent runtime.
// It deliberately implements only the base AgentRuntime interface so that any
// dispatcher fallback to it stands for "the workflow ran again".
type fakeAgentRuntime struct {
	mu      sync.Mutex
	handled int
	result  agent.Result
	err     error
}

func (f *fakeAgentRuntime) HandleMessageWithAttachments(_ context.Context, _ string, _ string, _ []agent.MessageAttachment) (agent.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handled++
	return f.result, f.err
}

func (f *fakeAgentRuntime) ExecuteApprovedToolCall(context.Context, app.Approval) (app.ToolCall, error) {
	return app.ToolCall{}, errors.New("not supported in fake runtime")
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
