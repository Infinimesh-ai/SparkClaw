package main

import (
	"context"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/connectorruntime"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
)

type waitingScheduledRuntime struct{}

func (waitingScheduledRuntime) HandleMessageWithAttachments(context.Context, string, string, []agent.MessageAttachment) (agent.Result, error) {
	return agent.Result{Run: app.AgentRun{ID: "run_waiting", State: "approval_pending"}}, nil
}
func (waitingScheduledRuntime) ExecuteApprovedToolCall(context.Context, app.Approval) (app.ToolCall, error) {
	return app.ToolCall{}, nil
}
func (waitingScheduledRuntime) ResumeRunAfterApproval(context.Context, string, string) (agent.Result, bool, error) {
	return agent.Result{}, false, nil
}
func (waitingScheduledRuntime) CompleteRunIfApprovalsResolved(string) {}

func TestScheduledPublisherDoesNotPresentWaitingRunAsSuccess(t *testing.T) {
	publisher := newScheduledRequestPublisher(waitingScheduledRuntime{}, nil, &delivery.Gateway{})
	err := publisher.Publish(t.Context(), app.MessageEnvelope{
		ID: "env_wait", CorrelationID: "session", OwnerID: app.DefaultOwnerID,
		Content: app.MessageContent{Parts: []app.MessagePart{{ID: "text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: "needs approval"}}},
	})
	if err == nil || !strings.Contains(err.Error(), "waiting") {
		t.Fatalf("expected explicit waiting state, got %v", err)
	}
}

func TestLegacyResultContentPreservesGovernedReferenceKinds(t *testing.T) {
	ingress := app.MessageIngressContext{OwnerID: app.DefaultOwnerID, Authorization: app.MessageAuthorization{PrincipalID: app.DefaultOwnerID}, ReturnRoute: app.ReturnRoute{Mode: app.ReturnNowhere}}
	result, err := connectorruntime.WorkflowResultFromAgentResult(agent.Result{Run: app.AgentRun{ID: "run", State: "completed"}, Message: app.Message{ID: "message", Attachments: []app.MessageAttachment{
		{Name: "workspace.txt", RelPath: "outputs/workspace.txt", ContentType: "text/plain"},
		{Name: "artifact.pdf", URI: "artifact://result", ContentType: "application/pdf"},
	}}}, ingress)
	if err != nil {
		t.Fatal(err)
	}
	content := result.Content
	if content.Parts[0].Resource.Kind != "workspace_file" || content.Parts[1].Resource.Kind != "artifact" {
		t.Fatalf("unexpected resource projection: %#v", content.Parts)
	}
	if _, err := connectorruntime.WorkflowResultFromAgentResult(agent.Result{Run: app.AgentRun{ID: "run", State: "completed"}, Message: app.Message{Attachments: []app.MessageAttachment{{Name: "missing.bin"}}}}, ingress); err == nil {
		t.Fatal("expected attachment without a governed reference to fail")
	}
}
