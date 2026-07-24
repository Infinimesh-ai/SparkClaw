package iscpbridge

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
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

func (*adapterRuntime) CompleteRunIfApprovalsResolved(string) {}

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
	if len(st.ListSessions()) != 1 {
		t.Fatalf("idempotent replay created %d sessions", len(st.ListSessions()))
	}
	firstRaw, _ := json.Marshal(first.Result)
	secondRaw, _ := json.Marshal(second.Result)
	if string(firstRaw) != string(secondRaw) {
		t.Fatalf("idempotent replay changed response: %s != %s", firstRaw, secondRaw)
	}
}

func TestGatewayAdapterMessageCancelAndIdempotencyConflict(t *testing.T) {
	st := store.NewMemoryStore()
	sessionRecord := st.CreateSession("Bridge")
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
	sessionRecord := st.CreateSession("Bridge")
	endpointID := "endpoint-app"
	idempotencyKey := "message-persisted"
	runID := stableID("run_iscp", endpointID, idempotencyKey)
	messageID := stableID("m_iscp", endpointID, idempotencyKey)
	st.AddMessage(app.Message{ID: messageID, SessionID: sessionRecord.ID, Role: "user", Content: "original"})
	st.SaveRun(app.AgentRun{
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
	sessionRecord := st.CreateSession("Approval")
	approval := app.Approval{
		ID: "approval-1", SessionID: sessionRecord.ID, RunID: "run-1", ToolCallID: "call-1",
		Tool: "files.write", Risk: app.RiskReversible, Status: "pending", Summary: "Write output",
		Arguments: map[string]any{"path": "out.txt"}, CreatedAt: time.Now().UTC(),
	}
	st.SaveApproval(approval)
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
