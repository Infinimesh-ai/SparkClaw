package agent

import (
	"context"
	"errors"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/clarification"
)

func (r Runtime) publicationRoute(source app.MessageSourceKind, reason string) (app.RouteDecision, error) {
	node, ok := r.capabilities.Node(app.CapabilityConversationAnswer)
	if source == "" {
		source = app.MessageSourceWeb
	}
	if !ok || node.Route == nil || !node.Route.SupportsSource(app.RouteOperationPublish, source) {
		return app.RouteDecision{}, errors.New("ordinary media publication is unavailable")
	}
	path, err := r.capabilities.PathTo(app.CapabilityConversationAnswer)
	if err != nil {
		return app.RouteDecision{}, err
	}
	route := app.RouteDecision{SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched, CatalogRevision: r.capabilities.Revision(), CapabilityPath: path, Slots: app.RouteSlots{Operation: app.RouteOperationPublish}, Confidence: 1, Reason: reason}
	return route, r.capabilities.ValidateDecision(route)
}
func (r Runtime) deliveryEndpoint(ctx context.Context, run app.AgentRun, delivery DeliveryDirective) (string, error) {
	if run.MessageContext == nil {
		return "", clarification.ErrInvalid
	}
	m := run.MessageContext
	envelope := app.MessageEnvelope{OwnerID: m.OwnerID, ActorID: m.Authorization.PrincipalID, Authorization: m.Authorization, Source: m.Source, ReturnRoute: m.ReturnRoute}
	selection, route, err := r.resolveMessageControl(ctx, run.SessionID, delivery, envelope)
	if err != nil {
		return "", err
	}
	if selection.Status != TargetResolved || route.Mode != app.ReturnToEndpoint || route.EndpointID == "" {
		return "", clarification.ErrInvalid
	}
	return string(route.EndpointID), nil
}
func splitDeliveryDirective(split requestSplit) (DeliveryDirective, error) {
	if split.DeliveryMode != "explicit_external" {
		return DeliveryDirective{}, nil
	}
	return normalizeDeliveryDirective(DeliveryDirective{
		ExplicitExternal: true, RequestedProviderKey: split.ProviderSpan, RequestedRecipientText: split.RecipientSpan,
	})
}
func (r Runtime) routePureDelivery(ctx context.Context, run app.AgentRun, text string, split requestSplit, delivery DeliveryDirective, resources []app.MessagePart, locators []app.MessageMediaLocator, source app.MessageSourceKind, history invocationHistory, documents documentContextResolution, selected *clarificationSelection, expectedEndpoint string) (IntentRoutingOutput, error) {
	if split.Deferred {
		return r.terminalRouting(app.RouteClarify, "deferred_request_requires_schedule"), nil
	}
	route, err := r.publicationRoute(source, "pure_delivery")
	if err != nil {
		return r.terminalRouting(app.RouteBlocked, "publication_unavailable"), nil
	}
	content := app.MessageContent{Parts: make([]app.MessagePart, 0)}
	output := IntentRoutingOutput{Route: route, Delivery: delivery, ExpectedEndpoint: expectedEndpoint}
	switch split.DeliveryObjectKind {
	case "conversation_file":
		if documents.Unavailable {
			return r.terminalRouting(app.RouteClarify, "conversation_file_unavailable"), nil
		}
		if selected != nil && selected.option.Kind == "file" {
			var valid bool
			documents, valid = selectClarificationDocument(documents, selected.option)
			if !valid {
				return r.terminalRouting(app.RouteClarify, "clarification_binding_changed"), nil
			}
		}
		if len(documents.References) != 1 {
			failed := r.terminalRouting(app.RouteClarify, "required_grounded_target_ambiguous")
			if len(documents.References) == 0 {
				failed.Route.Reason = "conversation_file_unavailable"
			}
			if source == app.MessageSourceWeb {
				failed.Proposal = r.fileClarification(text, documents)
			}
			return failed, nil
		}
		d := documents.References[0]
		content.Parts = append(content.Parts, app.MessagePart{ID: "delivery_file", Kind: app.MessagePartFile, Disposition: app.MessageDispositionAttachment, ArtifactID: d.HandleID, Resource: &app.ResourceRef{Kind: "workspace_file", Ref: d.Ref, Provenance: "delivery_input"}, Name: d.Name, ContentType: d.ContentType, Bytes: d.Size, SHA256: d.SHA256})
	case "assistant_text":
		for i := len(history.Selected.Messages) - 1; i >= 0; i-- {
			m := history.Selected.Messages[i]
			if m.Role == "assistant" {
				if m.Clarification != nil || strings.TrimSpace(m.Content) == "" {
					break
				}
				content.Parts = append(content.Parts, deliveryTextPart(m.Content))
				break
			}
		}
		if len(content.Parts) == 0 {
			return r.terminalRouting(app.RouteClarify, "delivery_object_missing"), nil
		}
	case "current_message":
		if missing, _ := ctx.Value(sourceMessageFilesUnavailableKey{}).(bool); missing {
			return r.terminalRouting(app.RouteClarify, "conversation_file_unavailable"), nil
		}
		if len(resources) > 0 {
			content.Parts = publicationInputParts(resources)
		} else if strings.TrimSpace(split.ObjectSpan) != "" {
			content.Parts = append(content.Parts, deliveryTextPart(split.ObjectSpan))
		} else {
			return r.terminalRouting(app.RouteClarify, "delivery_object_missing"), nil
		}
	case "named_locator":
		// Preserve the governed locator path and its existing approval gate.
		if len(locators) > 0 {
			output.PublicationLocators = append([]app.MessageMediaLocator(nil), locators...)
		} else if len(documentRoutePaths(split.ObjectSpan)) == 1 {
			output.PublicationLocators = []app.MessageMediaLocator{{Path: documentRoutePaths(split.ObjectSpan)[0]}}
		} else if strings.TrimSpace(split.ObjectSpan) != "" {
			output.PublicationLocators = []app.MessageMediaLocator{{Name: strings.TrimSpace(split.ObjectSpan)}}
		} else {
			return r.terminalRouting(app.RouteClarify, "delivery_object_missing"), nil
		}
	default:
		return r.terminalRouting(app.RouteClarify, "delivery_object_missing"), nil
	}
	if selected != nil && selected.option.Kind == "file" {
		if err = r.claimClarification(selected); err != nil {
			return r.terminalRouting(app.RouteBlocked, "clarification_already_used"), nil
		}
	}
	output.Publication = &content
	return output, nil
}
func deliveryTextPart(text string) app.MessagePart {
	return app.MessagePart{ID: "delivery_text", Kind: app.MessagePartText, Disposition: app.MessageDispositionInline, Text: text}
}

func publicationInputParts(resources []app.MessagePart) []app.MessagePart {
	parts := make([]app.MessagePart, 0, len(resources))
	for _, part := range resources {
		copy := part
		if part.Resource != nil {
			ref := *part.Resource
			ref.Provenance = "delivery_input"
			copy.Resource = &ref
		}
		parts = append(parts, copy)
	}
	return parts
}
