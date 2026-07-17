package agent

import (
	"context"
	"errors"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type TargetResolutionStatus string

const (
	TargetDefaultWeb     TargetResolutionStatus = "default_web"
	TargetSourceReply    TargetResolutionStatus = "source_reply"
	TargetNeedsChannel   TargetResolutionStatus = "needs_channel"
	TargetNeedsRecipient TargetResolutionStatus = "needs_recipient"
	TargetAmbiguous      TargetResolutionStatus = "ambiguous"
	TargetResolved       TargetResolutionStatus = "resolved"
	TargetUnavailable    TargetResolutionStatus = "unavailable"
)

type DeliveryDirective struct {
	ExplicitExternal       bool   `json:"explicit_external"`
	RequestedProviderKey   string `json:"requested_provider_key,omitempty"`
	RequestedRecipientText string `json:"requested_recipient_text,omitempty"`
}

type DeliveryTargetSelection struct {
	Status                 TargetResolutionStatus `json:"status"`
	RequestedProviderKey   string                 `json:"requested_provider_key,omitempty"`
	RequestedRecipientText string                 `json:"requested_recipient_text,omitempty"`
	CandidateEndpointIDs   []app.EndpointID       `json:"candidate_endpoint_ids,omitempty"`
	ResolvedEndpointID     app.EndpointID         `json:"resolved_endpoint_id,omitempty"`
	ResolutionRule         string                 `json:"resolution_rule"`
}

type MessageControlRouteRequest struct {
	Content       string                   `json:"content"`
	Source        app.MessageSourceContext `json:"source"`
	OwnerID       string                   `json:"owner_id"`
	ActorID       string                   `json:"actor_id"`
	Authorization app.MessageAuthorization `json:"authorization"`
	ReturnRoute   app.ReturnRoute          `json:"return_route"`
}

// MessageControlRouter is implemented by the actor-scoped deterministic
// endpoint resolver. It must never return provider credentials or native IDs.
type MessageControlRouter interface {
	ResolveMessageControl(context.Context, MessageControlRouteRequest) (DeliveryTargetSelection, error)
}

func (r Runtime) WithMessageControlRouter(router MessageControlRouter) Runtime {
	r.messageControl = router
	return r
}

func (r Runtime) resolveMessageControl(ctx context.Context, content string, envelope app.MessageEnvelope) (DeliveryTargetSelection, app.ReturnRoute, error) {
	selection := defaultDeliveryTargetSelection(envelope)
	if r.messageControl != nil {
		resolved, err := r.messageControl.ResolveMessageControl(ctx, MessageControlRouteRequest{
			Content: content, Source: envelope.Source, OwnerID: envelope.OwnerID, ActorID: envelope.ActorID,
			Authorization: envelope.Authorization, ReturnRoute: envelope.ReturnRoute,
		})
		if err != nil {
			return DeliveryTargetSelection{}, app.ReturnRoute{}, err
		}
		selection = resolved
	}
	if err := validateDeliveryTargetSelection(selection); err != nil {
		return DeliveryTargetSelection{}, app.ReturnRoute{}, err
	}
	returnRoute := envelope.ReturnRoute
	if selection.Status == TargetResolved {
		returnRoute = app.ReturnRoute{Mode: app.ReturnToEndpoint, EndpointID: selection.ResolvedEndpointID}
	}
	return selection, returnRoute, nil
}

func defaultDeliveryTargetSelection(envelope app.MessageEnvelope) DeliveryTargetSelection {
	if envelope.Source.Kind == app.MessageSourceThirdPartyDevice && envelope.ReturnRoute.Mode == app.ReturnToSource && envelope.ReturnRoute.SourceEndpointID != "" {
		return DeliveryTargetSelection{
			Status: TargetSourceReply, ResolvedEndpointID: envelope.ReturnRoute.SourceEndpointID,
			ResolutionRule: "frozen_source_endpoint",
		}
	}
	return DeliveryTargetSelection{Status: TargetDefaultWeb, ResolutionRule: "current_return_route"}
}

func validateDeliveryTargetSelection(selection DeliveryTargetSelection) error {
	if strings.TrimSpace(selection.ResolutionRule) == "" {
		return errors.New("message control selection requires a resolution rule")
	}
	switch selection.Status {
	case TargetDefaultWeb, TargetNeedsChannel, TargetNeedsRecipient, TargetAmbiguous, TargetUnavailable:
		if selection.ResolvedEndpointID != "" {
			return errors.New("unresolved message control selection cannot freeze an endpoint")
		}
	case TargetSourceReply, TargetResolved:
		if selection.ResolvedEndpointID == "" {
			return errors.New("resolved message control selection requires an exact endpoint")
		}
	default:
		return errors.New("message control selection has an unknown status")
	}
	return nil
}

func messageControlTerminalRoute(selection DeliveryTargetSelection, catalogRevision string) (app.RouteDecision, bool) {
	status := app.RouteClarify
	reason := ""
	switch selection.Status {
	case TargetNeedsChannel:
		reason = "External delivery requires an explicit software or channel."
	case TargetNeedsRecipient:
		reason = "External delivery requires one exact recipient endpoint."
	case TargetAmbiguous:
		reason = "External delivery recipient is ambiguous; choose one exact account or chat."
	case TargetUnavailable:
		status = app.RouteBlocked
		reason = "The requested external delivery endpoint is unavailable."
	default:
		return app.RouteDecision{}, false
	}
	return app.RouteDecision{
		SchemaVersion: app.RouteDecisionSchemaVersion, Status: status, CatalogRevision: catalogRevision, Reason: reason,
	}, true
}
