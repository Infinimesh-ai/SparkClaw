package connectorruntime

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type Runtime interface {
	Run(context.Context) error
}

type AgentRuntime interface {
	HandleMessageWithAttachments(context.Context, string, string, []agent.MessageAttachment) (agent.Result, error)
	ExecuteApprovedToolCall(context.Context, app.Approval) (app.ToolCall, error)
	ResumeRunAfterApproval(context.Context, string, string) (agent.Result, bool, error)
	CompleteRunIfApprovalsResolved(string)
}

type idempotentAgentRuntime interface {
	HandleMessageWithAttachmentsIdempotent(context.Context, string, string, string, string, []agent.MessageAttachment) (agent.Result, error)
}

type AgentRequest struct {
	SessionID   string
	MessageID   string
	RunID       string
	Text        string
	Attachments []app.MessageAttachment
}

type AgentBridge struct {
	runtime AgentRuntime
}

func NewAgentBridge(runtime AgentRuntime) AgentBridge {
	return AgentBridge{runtime: runtime}
}

func (b AgentBridge) Handle(ctx context.Context, request AgentRequest) (agent.Result, error) {
	if idempotent, ok := b.runtime.(idempotentAgentRuntime); ok && request.MessageID != "" && request.RunID != "" {
		return idempotent.HandleMessageWithAttachmentsIdempotent(
			ctx,
			request.SessionID,
			request.MessageID,
			request.RunID,
			request.Text,
			request.Attachments,
		)
	}
	return b.runtime.HandleMessageWithAttachments(ctx, request.SessionID, request.Text, request.Attachments)
}

func (b AgentBridge) ExecuteApprovedToolCall(ctx context.Context, approval app.Approval) (app.ToolCall, error) {
	return b.runtime.ExecuteApprovedToolCall(ctx, approval)
}

func (b AgentBridge) ResumeRunAfterApproval(ctx context.Context, sessionID, runID string) (agent.Result, bool, error) {
	return b.runtime.ResumeRunAfterApproval(ctx, sessionID, runID)
}

func (b AgentBridge) CompleteRunIfApprovalsResolved(runID string) {
	b.runtime.CompleteRunIfApprovalsResolved(runID)
}
