package main

import (
	"context"
	"errors"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/messagecontrol"
)

type endpointMessageControlRouter struct {
	endpoints *messagecontrol.EndpointRegistry
}

func (r endpointMessageControlRouter) ResolveMessageControl(ctx context.Context, request agent.MessageControlRouteRequest) (app.DeliveryTargetSelection, error) {
	if r.endpoints == nil {
		return app.DeliveryTargetSelection{}, errors.New("message control endpoint registry is unavailable")
	}
	if request.ActorID == "" || request.Authorization.PrincipalID != request.ActorID {
		return app.DeliveryTargetSelection{}, errors.New("message control actor does not match authorization")
	}
	if request.ReturnRoute.Mode == app.ReturnToEndpoint {
		var endpoint app.MessageEndpoint
		var err error
		if request.Source.Kind == app.MessageSourceTimer {
			endpoint, err = r.endpoints.Get(ctx, request.ReturnRoute.EndpointID)
			if err == nil && (endpoint.OwnerID != request.OwnerID || endpoint.ActorID != request.ActorID) {
				err = errors.New("scheduled return endpoint is outside the actor scope")
			}
		} else {
			endpoint, err = r.endpoints.GetForMessageSend(ctx, request.ReturnRoute.EndpointID, request.OwnerID, request.ActorID)
		}
		if err != nil {
			return app.DeliveryTargetSelection{}, err
		}
		return app.DeliveryTargetSelection{
			Status: app.TargetResolved, ResolvedEndpointID: endpoint.ID,
			RequestedProviderKey:   request.Directive.RequestedProviderKey,
			RequestedRecipientText: request.Directive.RequestedRecipientText,
			ResolutionRule:         "frozen_explicit_endpoint",
		}, nil
	}
	sourceEndpointID, err := frozenSourceEndpoint(request)
	if err != nil {
		return app.DeliveryTargetSelection{}, err
	}
	return r.endpoints.ResolveTarget(ctx, messagecontrol.TargetRequest{
		OwnerID: request.OwnerID, ActorID: request.ActorID,
		ExternalIntent: request.Directive.ExplicitExternal, WebSessionID: request.SessionID,
		SourceEndpointID: sourceEndpointID, ProviderKey: request.Directive.RequestedProviderKey,
		RecipientText: request.Directive.RequestedRecipientText,
	})
}

func frozenSourceEndpoint(request agent.MessageControlRouteRequest) (app.EndpointID, error) {
	// A third-party ingress only needs a frozen endpoint when the workflow is
	// actually returning to that source. JingSi Runtime v1 deliberately uses
	// return_nowhere and retrieves the result through its status contract, so it
	// must not be forced into an unrelated message-delivery boundary.
	if request.Directive.ExplicitExternal || request.Source.Kind != app.MessageSourceThirdPartyDevice ||
		request.ReturnRoute.Mode != app.ReturnToSource {
		return "", nil
	}
	if request.ReturnRoute.SourceEndpointID == "" ||
		request.Source.EndpointID != request.ReturnRoute.SourceEndpointID {
		return "", errors.New("third-party source reply requires one matching frozen endpoint")
	}
	return request.ReturnRoute.SourceEndpointID, nil
}
