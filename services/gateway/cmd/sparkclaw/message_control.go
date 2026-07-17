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
	if request.Directive.ExplicitExternal || request.Source.Kind != app.MessageSourceThirdPartyDevice {
		return "", nil
	}
	if request.ReturnRoute.Mode != app.ReturnToSource || request.ReturnRoute.SourceEndpointID == "" ||
		request.Source.EndpointID != request.ReturnRoute.SourceEndpointID {
		return "", errors.New("third-party source reply requires one matching frozen endpoint")
	}
	return request.ReturnRoute.SourceEndpointID, nil
}
