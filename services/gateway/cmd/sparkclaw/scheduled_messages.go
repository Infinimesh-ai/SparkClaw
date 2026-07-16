package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/connectorruntime"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/delivery"
)

type scheduledRequestPublisher struct {
	bridge  connectorruntime.AgentBridge
	routes  delivery.ReturnRouteResolver
	gateway *delivery.Gateway
}

func newScheduledRequestPublisher(runtime connectorruntime.AgentRuntime, routes delivery.ReturnRouteResolver, gateway *delivery.Gateway) *scheduledRequestPublisher {
	return &scheduledRequestPublisher{bridge: connectorruntime.NewAgentBridge(runtime), routes: routes, gateway: gateway}
}

func (p *scheduledRequestPublisher) Publish(ctx context.Context, envelope app.MessageEnvelope) error {
	if p == nil || p.gateway == nil {
		return errors.New("scheduled message publisher is unavailable")
	}
	text, attachments, err := legacyAgentInput(envelope.Content)
	if err != nil {
		return err
	}
	result, err := p.bridge.Handle(ctx, connectorruntime.AgentRequest{
		SessionID: envelope.CorrelationID, MessageID: envelope.ID, RunID: "run_" + envelope.ID,
		Text: text, Attachments: attachments,
	})
	if err != nil {
		return err
	}
	switch result.Run.State {
	case "approval_pending", "browser_login_blocked":
		return fmt.Errorf("scheduled request is waiting in state %q", result.Run.State)
	case "failed":
		return errors.New("scheduled request workflow failed")
	}
	content, err := legacyResultContent(result.Message)
	if err != nil {
		return err
	}
	workflowResult := app.WorkflowResult{
		SchemaVersion: app.WorkflowResultSchemaVersion,
		ID:            "result_" + envelope.ID, RunID: result.Run.ID, OwnerID: envelope.OwnerID, Authorization: envelope.Authorization,
		Status:   app.WorkflowResultSucceeded,
		Workflow: app.WorkflowContractRef{ID: app.WorkflowID("legacy.timer_request"), Revision: 1},
		Content:  content, ReturnRoute: envelope.ReturnRoute,
	}
	request, deliverResult, err := delivery.RequestFromWorkflowResult(ctx, workflowResult, p.routes)
	if err != nil || !deliverResult {
		return err
	}
	_, err = p.gateway.Deliver(ctx, request)
	return err
}

func legacyAgentInput(content app.MessageContent) (string, []app.MessageAttachment, error) {
	texts := []string{}
	attachments := []app.MessageAttachment{}
	for _, part := range content.Parts {
		if part.Kind == app.MessagePartText {
			texts = append(texts, part.Text)
			continue
		}
		attachment := app.MessageAttachment{
			Name: part.Name, ContentType: part.ContentType, Bytes: part.Bytes, Width: part.Width, Height: part.Height,
			SHA256: part.SHA256, Caption: part.Caption, ArtifactID: part.ArtifactID, Source: "timer",
		}
		if part.Resource != nil {
			switch part.Resource.Kind {
			case "workspace_file":
				attachment.RelPath = part.Resource.Ref
			case "artifact":
				attachment.URI = part.Resource.Ref
			default:
				return "", nil, fmt.Errorf("scheduled part %q uses unsupported resource kind %q", part.ID, part.Resource.Kind)
			}
		}
		attachments = append(attachments, attachment)
	}
	return strings.TrimSpace(strings.Join(texts, "\n")), attachments, nil
}

func legacyResultContent(message app.Message) (app.MessageContent, error) {
	parts := []app.MessagePart{}
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
		parts = append(parts, app.MessagePart{ID: "scheduled-empty-result", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: "The scheduled request completed without a user-visible result."})
	}
	return app.MessageContent{Parts: parts}, nil
}
