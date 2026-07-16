package delivery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type EndpointRegistry interface {
	Get(context.Context, app.EndpointID) (app.MessageEndpoint, error)
}

type WebDelivery interface {
	Deliver(context.Context, app.MessageEndpoint, app.DeliveryRequest) (app.DeliveryReceipt, error)
}

type Gateway struct {
	endpoints EndpointRegistry
	providers *ProviderRegistry
	web       WebDelivery
}

func NewGateway(endpoints EndpointRegistry, providers *ProviderRegistry, web WebDelivery) *Gateway {
	return &Gateway{endpoints: endpoints, providers: providers, web: web}
}

func (g *Gateway) Deliver(ctx context.Context, request app.DeliveryRequest) (app.DeliveryReceipt, error) {
	if g == nil || g.endpoints == nil {
		return app.DeliveryReceipt{}, errors.New("delivery gateway is unavailable")
	}
	if request.SchemaVersion != app.DeliveryRequestSchemaVersion || request.ID == "" || request.Target == "" || strings.TrimSpace(request.IdempotencyKey) == "" {
		return app.DeliveryReceipt{}, errors.New("delivery request identity, target, and supported schema are required")
	}
	if strings.TrimSpace(request.OwnerID) == "" || strings.TrimSpace(request.Authorization.PrincipalID) == "" || request.OwnerID != request.Authorization.PrincipalID {
		return app.DeliveryReceipt{}, errors.New("delivery owner and matching authorization principal are required")
	}
	endpoint, err := g.endpoints.Get(ctx, request.Target)
	if err != nil {
		return failedReceipt(app.MessageEndpoint{ID: request.Target}, request, err.Error()), err
	}
	if endpoint.OwnerID != request.OwnerID {
		err := errors.New("delivery endpoint does not belong to the authorized owner")
		return failedReceipt(endpoint, request, err.Error()), err
	}
	switch endpoint.Kind {
	case app.EndpointKindWeb:
		if g.web == nil {
			return failedReceipt(endpoint, request, "web delivery is unavailable"), errors.New("web delivery is unavailable")
		}
		return g.web.Deliver(ctx, endpoint, request)
	case app.EndpointKindThirdPartyDevice:
		if g.providers == nil {
			return failedReceipt(endpoint, request, "provider registry is unavailable"), errors.New("provider registry is unavailable")
		}
		return g.providers.Deliver(ctx, endpoint, request)
	default:
		err := fmt.Errorf("unsupported endpoint kind %q", endpoint.Kind)
		return failedReceipt(endpoint, request, err.Error()), err
	}
}

type LocalWebDelivery struct{}

func (LocalWebDelivery) Deliver(_ context.Context, endpoint app.MessageEndpoint, request app.DeliveryRequest) (app.DeliveryReceipt, error) {
	now := time.Now().UTC()
	return app.DeliveryReceipt{
		DeliveryID:  request.ID,
		EndpointID:  endpoint.ID,
		Status:      app.DeliverySucceeded,
		ProviderRef: "web-local",
		AttemptedAt: now,
		DeliveredAt: &now,
	}, nil
}

func failedReceipt(endpoint app.MessageEndpoint, request app.DeliveryRequest, message string) app.DeliveryReceipt {
	return app.DeliveryReceipt{
		DeliveryID:  request.ID,
		EndpointID:  endpoint.ID,
		Status:      app.DeliveryFailed,
		Error:       message,
		RetryState:  "blocked",
		AttemptedAt: time.Now().UTC(),
	}
}

type DeliveryError struct {
	Message string
	State   string
}

func (e DeliveryError) Error() string      { return e.Message }
func (e DeliveryError) RetryState() string { return e.State }
