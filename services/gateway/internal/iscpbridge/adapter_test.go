package iscpbridge

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
)

type adapterRuntime struct {
	started chan struct{}
}

func (r *adapterRuntime) HandleMessageWithAttachmentsIdempotent(ctx context.Context, sessionID, _, runID, _ string, _ []agent.MessageAttachment) (agent.Result, error) {
	select {
	case r.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return agent.Result{Run: app.AgentRun{ID: runID, SessionID: sessionID, State: "cancelled"}}, ctx.Err()
}

func (r *adapterRuntime) HandleMessageWithIngress(ctx context.Context, sessionID, _, runID, _ string, _ []agent.MessageAttachment, _ app.MessageIngressContext) (agent.Result, error) {
	select {
	case r.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return agent.Result{Run: app.AgentRun{ID: runID, SessionID: sessionID, State: "cancelled"}}, ctx.Err()
}

func (*adapterRuntime) ExecuteApprovedToolCall(context.Context, app.Approval) (app.ToolCall, error) {
	return app.ToolCall{}, nil
}

func (*adapterRuntime) ResumeRunAfterApproval(context.Context, string, string) (agent.Result, bool, error) {
	return agent.Result{}, false, nil
}

func (*adapterRuntime) CompleteRunIfApprovalsResolved(context.Context, string) error { return nil }

func TestGatewayAdapterSessionCreateIsIdempotent(t *testing.T) {
	st := store.NewMemoryStore()
	runtime := &adapterRuntime{started: make(chan struct{}, 1)}
	adapter := NewGatewayAdapter(st, func() AgentRuntime { return runtime })
	request := validRequest(TypeSessionCreate, "request-create", "endpoint-app", "", "create-1", SessionCreatePayload{Title: "From JingSi"})

	first := adapter.Dispatch(t.Context(), Principal{OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID}, request)
	second := adapter.Dispatch(t.Context(), Principal{OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID}, request)
	if first.Status != "ok" || second.Status != "ok" {
		t.Fatalf("session create failed: first=%#v second=%#v", first, second)
	}
	if len(storetest.MustListSessions(t, st)) != 1 {
		t.Fatalf("idempotent replay created %d sessions", len(storetest.MustListSessions(t, st)))
	}
	firstRaw, _ := json.Marshal(first.Result)
	secondRaw, _ := json.Marshal(second.Result)
	if string(firstRaw) != string(secondRaw) {
		t.Fatalf("idempotent replay changed response: %s != %s", firstRaw, secondRaw)
	}
}

func TestGatewayAdapterPassiveNotificationPersistsWithoutAgentActivity(t *testing.T) {
	st := store.NewMemoryStore()
	runtimeRequested := false
	adapter := NewGatewayAdapter(st, func() AgentRuntime {
		runtimeRequested = true
		return &adapterRuntime{started: make(chan struct{}, 1)}
	})
	principal := Principal{OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID}
	request := validRequest(TypeNotificationDeliver, "request-notification", "localmind-notifications", "", "notification-delivery-1", NotificationDeliverPayload{
		NotificationID: "delivery-1",
		Source:         "localmind",
		Kind:           app.PassiveNotificationKindDocumentMention,
		DeepLink:       "https://localmind.example/workspace/doc",
		OccurredAt:     time.Now().UTC(),
	})

	first := adapter.Dispatch(t.Context(), principal, request)
	if first.Status != "ok" || first.Error != nil {
		t.Fatalf("notification delivery failed: %#v", first)
	}
	restarted := NewGatewayAdapter(st, func() AgentRuntime {
		runtimeRequested = true
		return &adapterRuntime{started: make(chan struct{}, 1)}
	})
	replay := restarted.Dispatch(t.Context(), principal, request)
	if replay.Status != "ok" || replay.Error != nil {
		t.Fatalf("notification replay after restart failed: %#v", replay)
	}
	if runtimeRequested {
		t.Fatal("passive notification requested the Agent runtime")
	}
	if got, err := st.ListPassiveNotifications(t.Context(), app.DefaultOwnerID, "", 10); err != nil || len(got) != 1 {
		t.Fatalf("persisted notifications = %#v", got)
	}
	if len(storetest.MustListSessions(t, st)) != 0 || len(testListRuns(st, "")) != 0 || len(testListModelCalls(st, "", "")) != 0 ||
		len(testListToolCalls(st, "")) != 0 || len(storetest.MustListApprovals(t, st, "")) != 0 {
		t.Fatal("passive notification created Agent activity")
	}

	conflict := request
	conflict.RequestID = "request-notification-conflict"
	conflict.Payload, _ = json.Marshal(NotificationDeliverPayload{
		NotificationID: "delivery-1",
		Source:         "localmind",
		Kind:           app.PassiveNotificationKindCommentMention,
		DeepLink:       "https://localmind.example/workspace/doc",
		OccurredAt:     time.Now().UTC(),
	})
	if response := restarted.Dispatch(t.Context(), principal, conflict); response.Error == nil || response.Error.Code != CodeConflict {
		t.Fatalf("notification idempotency conflict was not rejected: %#v", response)
	}
}

func TestGatewayAdapterPassiveNotificationIngestionEnforcesCap(t *testing.T) {
	st := store.NewMemoryStore()
	adapter := NewGatewayAdapter(st, func() AgentRuntime {
		t.Fatal("passive notification requested the Agent runtime")
		return nil
	})
	adapter.ConfigureNotificationRetention(3, 90)
	principal := Principal{OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID}
	for i := 0; i < 6; i++ {
		suffix := string(rune('a' + i))
		request := validRequest(TypeNotificationDeliver, "request-cap-"+suffix, "localmind-notifications", "", "delivery-cap-"+suffix, NotificationDeliverPayload{
			NotificationID: "delivery-cap-" + suffix,
			Source:         "localmind",
			Kind:           app.PassiveNotificationKindDocumentMention,
			DeepLink:       "https://localmind.example/workspace/doc",
			OccurredAt:     time.Now().UTC(),
		})
		if response := adapter.Dispatch(t.Context(), principal, request); response.Status != "ok" {
			t.Fatalf("delivery %d failed: %#v", i, response)
		}
	}
	listed, err := st.ListPassiveNotifications(t.Context(), app.DefaultOwnerID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(listed); got != 3 {
		t.Fatalf("inbox size after capped ingestion = %d, want 3", got)
	}
}

func TestGatewayAdapterPassiveNotificationRejectsUnsafePayloads(t *testing.T) {
	tests := []struct {
		name    string
		payload NotificationDeliverPayload
	}{
		{
			name: "unknown kind",
			payload: NotificationDeliverPayload{
				NotificationID: "delivery-1", Source: "localmind", Kind: "message",
				DeepLink: "https://localmind.example/doc", OccurredAt: time.Now().UTC(),
			},
		},
		{
			name: "remote insecure link",
			payload: NotificationDeliverPayload{
				NotificationID: "delivery-1", Source: "localmind", Kind: app.PassiveNotificationKindDocumentMention,
				DeepLink: "http://localmind.example/doc", OccurredAt: time.Now().UTC(),
			},
		},
		{
			name: "credentials in link",
			payload: NotificationDeliverPayload{
				NotificationID: "delivery-1", Source: "localmind", Kind: app.PassiveNotificationKindDocumentMention,
				DeepLink: "https://user:secret@localmind.example/doc", OccurredAt: time.Now().UTC(),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter := NewGatewayAdapter(store.NewMemoryStore(), func() AgentRuntime {
				t.Fatal("invalid passive notification requested Agent runtime")
				return nil
			})
			request := validRequest(TypeNotificationDeliver, "request-invalid", "localmind-notifications", "", "delivery-invalid", test.payload)
			response := adapter.Dispatch(t.Context(), Principal{OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID}, request)
			if response.Error == nil || response.Error.Code != CodeInvalidRequest {
				t.Fatalf("unsafe payload was accepted: %#v", response)
			}
		})
	}
}

func TestGatewayAdapterMessageCancelAndIdempotencyConflict(t *testing.T) {
	st := store.NewMemoryStore()
	sessionRecord := storetest.MustCreateSession(t, st, "Bridge")
	runtime := &adapterRuntime{started: make(chan struct{}, 1)}
	adapter := NewGatewayAdapter(st, func() AgentRuntime { return runtime })
	principal := Principal{OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID}
	send := validRequest(TypeMessageSend, "request-send", "endpoint-app", sessionRecord.ID, "message-1", MessageSendPayload{Content: "hello"})

	first := adapter.Dispatch(t.Context(), principal, send)
	if first.Status != "accepted" || first.Operation == nil {
		t.Fatalf("message was not accepted: %#v", first)
	}
	select {
	case <-runtime.started:
	case <-time.After(time.Second):
		t.Fatal("message operation did not start")
	}
	replay := adapter.Dispatch(t.Context(), principal, send)
	if replay.Operation == nil || replay.Operation.ID != first.Operation.ID {
		t.Fatalf("message replay changed operation: %#v", replay)
	}
	conflict := send
	conflict.RequestID = "request-conflict"
	conflict.Payload, _ = json.Marshal(MessageSendPayload{Content: "different"})
	if response := adapter.Dispatch(t.Context(), principal, conflict); response.Error == nil || response.Error.Code != CodeConflict {
		t.Fatalf("idempotency conflict was not rejected: %#v", response)
	}
	cancel := validRequest(TypeMessageCancel, "request-cancel", "endpoint-app", sessionRecord.ID, "cancel-1", MessageCancelPayload{OperationID: first.Operation.ID})
	response := adapter.Dispatch(t.Context(), principal, cancel)
	if response.Operation == nil || response.Operation.State != "cancelled" {
		t.Fatalf("operation was not cancelled: %#v", response)
	}
}

func TestGatewayAdapterMessageReplayAfterRestartChecksOriginalInput(t *testing.T) {
	st := store.NewMemoryStore()
	sessionRecord := storetest.MustCreateSession(t, st, "Bridge")
	endpointID := "endpoint-app"
	idempotencyKey := "message-persisted"
	runID := stableID("run_iscp", endpointID, idempotencyKey)
	messageID := stableID("m_iscp", endpointID, idempotencyKey)
	storetest.MustAddMessage(t, st, app.Message{ID: messageID, SessionID: sessionRecord.ID, Role: "user", Content: "original"})
	testSaveRun(st, app.AgentRun{
		ID: runID, SessionID: sessionRecord.ID, State: "completed", StartedAt: time.Now().UTC(),
		MessageContext: &app.MessageRunContext{Source: app.MessageSourceContext{
			Kind: app.MessageSourceThirdPartyDevice, Adapter: "iscp-bridge", EndpointID: app.EndpointID(endpointID),
		}},
	})

	adapter := NewGatewayAdapter(st, func() AgentRuntime { return &adapterRuntime{started: make(chan struct{}, 1)} })
	principal := Principal{OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID}

	replay := validRequest(TypeMessageSend, "request-replay", endpointID, sessionRecord.ID, idempotencyKey, MessageSendPayload{Content: "original"})
	if response := adapter.Dispatch(t.Context(), principal, replay); response.Operation == nil || response.Operation.ID != runID {
		t.Fatalf("persisted replay did not recover the original operation: %#v", response)
	}
	replay.RequestID = "request-conflict"
	replay.Payload, _ = json.Marshal(MessageSendPayload{Content: "changed"})
	if response := adapter.Dispatch(t.Context(), principal, replay); response.Error == nil || response.Error.Code != CodeConflict {
		t.Fatalf("persisted replay accepted changed input: %#v", response)
	}
}

func TestGatewayAdapterApprovalRequiresCurrentPreview(t *testing.T) {
	st := store.NewMemoryStore()
	sessionRecord := storetest.MustCreateSession(t, st, "Approval")
	approval := app.Approval{
		ID: "approval-1", SessionID: sessionRecord.ID, RunID: "run-1", ToolCallID: "call-1",
		Tool: "files.write", Risk: app.RiskReversible, Status: "pending", Summary: "Write output",
		Arguments: map[string]any{"path": "out.txt"}, CreatedAt: time.Now().UTC(),
	}
	storetest.MustSaveApproval(t, st, approval)
	runtime := &adapterRuntime{started: make(chan struct{}, 1)}
	adapter := NewGatewayAdapter(st, func() AgentRuntime { return runtime })
	principal := Principal{OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID}

	stale := validRequest(TypeApprovalResolve, "request-stale", "endpoint-app", sessionRecord.ID, "approval-stale", ApprovalResolvePayload{
		ApprovalID: approval.ID, Decision: "rejected", PreviewHash: "sha256:stale", ExpectedState: "pending",
	})
	if response := adapter.Dispatch(t.Context(), principal, stale); response.Error == nil || response.Error.Code != CodeStaleState {
		t.Fatalf("stale approval was not rejected: %#v", response)
	}
	resolve := validRequest(TypeApprovalResolve, "request-resolve", "endpoint-app", sessionRecord.ID, "approval-resolve", ApprovalResolvePayload{
		ApprovalID: approval.ID, Decision: "rejected", PreviewHash: ApprovalPreviewHash(approval), ExpectedState: "pending",
	})
	if response := adapter.Dispatch(t.Context(), principal, resolve); response.Status != "ok" {
		t.Fatalf("current approval was not resolved: %#v", response)
	}
	resolved := adapter.Dispatch(t.Context(), principal, resolve)
	if resolved.Status != "ok" {
		t.Fatalf("approval replay was not idempotent: %#v", resolved)
	}
}

func validRequest(requestType, requestID, endpointID, sessionID, idempotencyKey string, payload any) Request {
	raw, _ := json.Marshal(payload)
	now := time.Now().UTC()
	return Request{
		ProtocolVersion: ProtocolVersion, Type: requestType, RequestID: requestID,
		EndpointID: endpointID, SessionID: sessionID, IdempotencyKey: idempotencyKey,
		IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute), Payload: raw,
	}
}
