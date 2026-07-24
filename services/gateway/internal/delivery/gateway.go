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
	if strings.TrimSpace(request.OwnerID) == "" || strings.TrimSpace(request.ActorID) == "" || request.Authorization.PrincipalID != request.ActorID {
		return app.DeliveryReceipt{}, NewError(CodeCrossUserDenied, "delivery owner and actor authorization are required", "blocked")
	}
	endpoint, err := g.endpoints.Get(ctx, request.Target)
	if err != nil {
		return failedReceipt(app.MessageEndpoint{ID: request.Target}, request, err.Error()), err
	}
	if !requestAuthorizedForEndpoint(request, endpoint) {
		err := NewError(CodeCrossUserDenied, "delivery endpoint does not belong to the authorized actor", "blocked")
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
	Code    string
	Message string
	State   string
}

func (e DeliveryError) Error() string      { return e.Message }
func (e DeliveryError) RetryState() string { return e.State }
func (e DeliveryError) ErrorCode() string  { return e.Code }
func (e DeliveryError) Retryable() bool    { return e.State == "retryable" }

const (
	CodeBindingUnavailable  = "delivery_binding_unavailable"
	CodeScopeDenied         = "delivery_scope_denied"
	CodeCrossUserDenied     = "delivery_cross_user_denied"
	CodePartUnsupported     = "delivery_part_unsupported"
	CodePayloadTooLarge     = "delivery_payload_too_large"
	CodeArtifactInvalid     = "delivery_artifact_invalid"
	CodeIdempotencyConflict = "delivery_idempotency_conflict"
	CodeProviderRetryable   = "delivery_provider_retryable"
	CodeOutcomeUnknown      = "delivery_outcome_unknown"
)

func NewError(code, message, retryState string) error {
	return DeliveryError{Code: code, Message: message, State: retryState}
}

func ErrorCode(err error) string {
	var coded interface{ ErrorCode() string }
	if errors.As(err, &coded) {
		return coded.ErrorCode()
	}
	return ""
}

func requestAuthorizedForEndpoint(request app.DeliveryRequest, endpoint app.MessageEndpoint) bool {
	if request.Authorization.PrincipalID != request.ActorID || request.ActorID == "" {
		return false
	}
	if request.Origin == app.DeliveryOriginSourceReply {
		return endpoint.SourceActorID == request.OwnerID && endpoint.SourceActorID == request.ActorID
	}
	return endpoint.OwnerID == request.OwnerID && endpoint.ActorID == request.ActorID
}
