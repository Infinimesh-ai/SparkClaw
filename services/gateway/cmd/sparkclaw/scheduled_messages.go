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
	ingress := app.MessageIngressContext{Source: envelope.Source, OwnerID: envelope.OwnerID, Authorization: envelope.Authorization, ReturnRoute: envelope.ReturnRoute}
	result, err := p.bridge.Handle(ctx, connectorruntime.AgentRequest{
		SessionID: envelope.CorrelationID, MessageID: envelope.ID, RunID: "run_" + envelope.ID,
		Text: text, Attachments: attachments,
		Ingress: &ingress,
	})
	if err != nil {
		return err
	}
	switch result.Run.State {
	case "approval_pending", "browser_login_blocked":
		return fmt.Errorf("scheduled request is waiting in state %q", result.Run.State)
	}
	workflowResult, err := connectorruntime.WorkflowResultFromAgentResult(result, ingress)
	if err != nil {
		return err
	}
	if workflowResult.Status == app.WorkflowResultWaiting {
		return fmt.Errorf("scheduled request is waiting in workflow result state %q", workflowResult.Status)
	}
	request, deliverResult, err := delivery.RequestFromWorkflowResult(ctx, workflowResult, p.routes)
	if err != nil {
		return err
	}
	if !deliverResult {
		return errors.New("scheduled workflow result has no delivery route")
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
