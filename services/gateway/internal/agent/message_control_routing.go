package agent

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

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
	SessionID     string                   `json:"session_id"`
	Directive     DeliveryDirective        `json:"directive"`
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

func (r Runtime) resolveMessageControl(ctx context.Context, sessionID string, directive DeliveryDirective, envelope app.MessageEnvelope) (DeliveryTargetSelection, app.ReturnRoute, error) {
	normalized, err := normalizeDeliveryDirective(directive)
	if err != nil {
		return DeliveryTargetSelection{}, app.ReturnRoute{}, err
	}
	selection := defaultDeliveryTargetSelection(envelope)
	if r.messageControl != nil {
		resolved, err := r.messageControl.ResolveMessageControl(ctx, MessageControlRouteRequest{
			SessionID: sessionID, Directive: normalized, Source: envelope.Source, OwnerID: envelope.OwnerID, ActorID: envelope.ActorID,
			Authorization: envelope.Authorization, ReturnRoute: envelope.ReturnRoute,
		})
		if err != nil {
			return DeliveryTargetSelection{}, app.ReturnRoute{}, err
		}
		selection = resolved
	} else if normalized.ExplicitExternal {
		return DeliveryTargetSelection{}, app.ReturnRoute{}, errors.New("external delivery target resolver is unavailable")
	}
	if err := validateDeliveryTargetSelection(selection, normalized); err != nil {
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
	selection := DeliveryTargetSelection{Status: TargetDefaultWeb, ResolutionRule: "current_return_route"}
	if envelope.ReturnRoute.Mode == app.ReturnToSource {
		selection.ResolvedEndpointID = envelope.ReturnRoute.SourceEndpointID
	}
	return selection
}

func normalizeDeliveryDirective(directive DeliveryDirective) (DeliveryDirective, error) {
	directive.RequestedProviderKey = strings.ToLower(strings.TrimSpace(directive.RequestedProviderKey))
	directive.RequestedRecipientText = strings.TrimSpace(directive.RequestedRecipientText)
	if !directive.ExplicitExternal && (directive.RequestedProviderKey != "" || directive.RequestedRecipientText != "") {
		return DeliveryDirective{}, errors.New("non-external delivery directive cannot carry software or recipient slots")
	}
	if !utf8.ValidString(directive.RequestedProviderKey) || !utf8.ValidString(directive.RequestedRecipientText) ||
		len(directive.RequestedProviderKey) > 128 || len(directive.RequestedRecipientText) > 256 {
		return DeliveryDirective{}, errors.New("delivery directive slots are invalid or too long")
	}
	if strings.IndexFunc(directive.RequestedProviderKey+directive.RequestedRecipientText, unicode.IsControl) >= 0 {
		return DeliveryDirective{}, errors.New("delivery directive slots cannot contain control characters")
	}
	return directive, nil
}

func validateDeliveryTargetSelection(selection DeliveryTargetSelection, directive DeliveryDirective) error {
	if strings.TrimSpace(selection.ResolutionRule) == "" {
		return errors.New("message control selection requires a resolution rule")
	}
	if directive.ExplicitExternal {
		if selection.Status == TargetDefaultWeb || selection.Status == TargetSourceReply {
			return errors.New("explicit external delivery cannot fall back to the current Web or source route")
		}
		if selection.RequestedProviderKey != directive.RequestedProviderKey || selection.RequestedRecipientText != directive.RequestedRecipientText {
			return errors.New("message control selection changed the typed software or recipient request")
		}
	} else if selection.Status != TargetDefaultWeb && selection.Status != TargetSourceReply {
		return errors.New("ordinary reply cannot resolve or clarify an external delivery target")
	}
	switch selection.Status {
	case TargetDefaultWeb:
		// Task 1's canonical resolver includes the exact current Web endpoint.
		// It remains a source/default route, not an external TargetResolved grant.
	case TargetNeedsChannel, TargetNeedsRecipient, TargetAmbiguous, TargetUnavailable:
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
