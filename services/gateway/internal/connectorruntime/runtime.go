package connectorruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type Runtime interface {
	Run(context.Context) error
}

// WorkflowResultFromAgentResult preserves the new WorkflowResult unchanged.
// The adapter exists only for legacy runtimes that returned no route, or for a
// persisted legacy ReAct result; matched results must never be re-created.
func WorkflowResultFromAgentResult(result agent.Result, ingress app.MessageIngressContext) (app.WorkflowResult, error) {
	if result.WorkflowResult != nil {
		return *result.WorkflowResult, nil
	}
	if result.RouteDecision != nil && result.RouteDecision.Status != app.RouteUnmatched {
		return app.WorkflowResult{}, errors.New("matched agent result is missing its WorkflowResult")
	}
	content, err := legacyResultContent(result.Message)
	if err != nil {
		return app.WorkflowResult{}, err
	}
	status := app.WorkflowResultSucceeded
	if result.Run.State == "approval_pending" || result.Run.State == "browser_login_blocked" {
		status = app.WorkflowResultWaiting
	} else if result.Run.State == "blocked" {
		status = app.WorkflowResultBlocked
	}
	return app.WorkflowResult{
		SchemaVersion: app.WorkflowResultSchemaVersion,
		ID:            "workflow_result_" + result.Run.ID,
		RunID:         result.Run.ID,
		OwnerID:       ingress.OwnerID,
		Authorization: ingress.Authorization,
		Status:        status,
		Workflow:      app.WorkflowContractRef{ID: app.WorkflowID("react.unmatched"), Revision: 1},
		Content:       content,
		ReturnRoute:   ingress.ReturnRoute,
	}, nil
}

func legacyResultContent(message app.Message) (app.MessageContent, error) {
	parts := make([]app.MessagePart, 0, len(message.Attachments)+1)
	if strings.TrimSpace(message.Content) != "" {
		parts = append(parts, app.MessagePart{ID: message.ID + ":text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: message.Content})
	}
	for _, attachment := range message.Attachments {
		kind := app.MessagePartFile
		if strings.HasPrefix(strings.ToLower(attachment.ContentType), "image/") {
			kind = app.MessagePartImage
		} else if strings.HasPrefix(strings.ToLower(attachment.ContentType), "audio/") {
			kind = app.MessagePartAudio
		}
		var resource *app.ResourceRef
		switch {
		case strings.TrimSpace(attachment.RelPath) != "":
			resource = &app.ResourceRef{Kind: "workspace_file", Ref: attachment.RelPath, Provenance: "workflow_result"}
		case strings.TrimSpace(attachment.URI) != "":
			resource = &app.ResourceRef{Kind: "artifact", Ref: attachment.URI, Provenance: "workflow_result"}
		case strings.TrimSpace(attachment.ArtifactID) != "":
			resource = &app.ResourceRef{Kind: "artifact", Ref: attachment.ArtifactID, Provenance: "workflow_result"}
		default:
			return app.MessageContent{}, fmt.Errorf("workflow result attachment %q has no governed resource reference", attachment.Name)
		}
		disposition := app.MessageDispositionAttachment
		if kind == app.MessagePartAudio && strings.Contains(strings.ToLower(attachment.Source), "voice") {
			disposition = app.MessageDispositionVoiceNote
		}
		parts = append(parts, app.MessagePart{
			ID: "result:part:" + fmt.Sprint(len(parts)), Kind: kind, Disposition: disposition,
			ArtifactID: attachment.ArtifactID, Resource: resource, Name: attachment.Name, ContentType: attachment.ContentType,
			Bytes: attachment.Bytes, Width: attachment.Width, Height: attachment.Height, SHA256: attachment.SHA256, Caption: attachment.Caption,
		})
	}
	if len(parts) == 0 {
		parts = append(parts, app.MessagePart{ID: "legacy-empty-result", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: "The request completed without a user-visible result."})
	}
	return app.MessageContent{Parts: parts}, nil
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

type ingressAgentRuntime interface {
	HandleMessageWithIngress(context.Context, string, string, string, string, []agent.MessageAttachment, app.MessageIngressContext) (agent.Result, error)
}

type ResultDeliverer interface {
	DeliverWorkflowResult(context.Context, app.WorkflowResult) (app.DeliveryReceipt, error)
}

type AgentRequest struct {
	SessionID   string
	MessageID   string
	RunID       string
	Text        string
	Attachments []app.MessageAttachment
	Ingress     *app.MessageIngressContext
}

type AgentBridge struct {
	runtime AgentRuntime
}

func NewAgentBridge(runtime AgentRuntime) AgentBridge {
	return AgentBridge{runtime: runtime}
}

func (b AgentBridge) Handle(ctx context.Context, request AgentRequest) (agent.Result, error) {
	if request.Ingress != nil {
		if runtime, ok := b.runtime.(ingressAgentRuntime); ok {
			return runtime.HandleMessageWithIngress(ctx, request.SessionID, request.MessageID, request.RunID, request.Text, request.Attachments, *request.Ingress)
		}
	}
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
