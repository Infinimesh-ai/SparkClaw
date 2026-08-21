package connectorruntime

import (
	"context"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type bridgeRuntime struct {
	normalCalls     int
	idempotentCalls int
	lastSessionID   string
	lastMessageID   string
	lastRunID       string
}

func (r *bridgeRuntime) HandleMessageWithAttachments(_ context.Context, sessionID, _ string, _ []agent.MessageAttachment) (agent.Result, error) {
	r.normalCalls++
	r.lastSessionID = sessionID
	return agent.Result{Run: app.AgentRun{ID: "normal-run"}}, nil
}

func (r *bridgeRuntime) HandleMessageWithAttachmentsIdempotent(_ context.Context, sessionID, messageID, runID, _ string, _ []agent.MessageAttachment) (agent.Result, error) {
	r.idempotentCalls++
	r.lastSessionID = sessionID
	r.lastMessageID = messageID
	r.lastRunID = runID
	return agent.Result{Run: app.AgentRun{ID: runID}}, nil
}

func (r *bridgeRuntime) ExecuteApprovedToolCall(context.Context, app.Approval) (app.ToolCall, error) {
	return app.ToolCall{}, nil
}

func (r *bridgeRuntime) ResumeRunAfterApproval(context.Context, string, string) (agent.Result, bool, error) {
	return agent.Result{}, false, nil
}

func (r *bridgeRuntime) CompleteRunIfApprovalsResolved(context.Context, string) error { return nil }

func TestAgentBridgeSelectsIdempotentExecutionWhenIdentifiersArePresent(t *testing.T) {
	runtime := &bridgeRuntime{}
	bridge := NewAgentBridge(runtime)
	result, err := bridge.Handle(context.Background(), AgentRequest{
		SessionID: "session-alpha",
		MessageID: "message-alpha",
		RunID:     "run-alpha",
		Text:      "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Run.ID != "run-alpha" || runtime.idempotentCalls != 1 || runtime.normalCalls != 0 {
		t.Fatalf("unexpected idempotent execution: result=%#v runtime=%#v", result, runtime)
	}
}

func TestAgentBridgeFallsBackToNormalExecutionWithoutIdentifiers(t *testing.T) {
	runtime := &bridgeRuntime{}
	bridge := NewAgentBridge(runtime)
	result, err := bridge.Handle(context.Background(), AgentRequest{SessionID: "session-beta", Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Run.ID != "normal-run" || runtime.normalCalls != 1 || runtime.idempotentCalls != 0 {
		t.Fatalf("unexpected normal execution: result=%#v runtime=%#v", result, runtime)
	}
}
