package delivery

import (
	"context"
	"errors"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type ReturnRouteResolver interface {
	Resolve(context.Context, app.ReturnRoute) (app.MessageEndpoint, bool, error)
}

func RequestFromWorkflowResult(ctx context.Context, result app.WorkflowResult, routes ReturnRouteResolver) (app.DeliveryRequest, bool, error) {
	if result.SchemaVersion != app.WorkflowResultSchemaVersion {
		return app.DeliveryRequest{}, false, errors.New("unsupported workflow result schema")
	}
	request, deliver, err := RequestForMessage(ctx, result.ID, result.OwnerID, result.Authorization, result.Content, result.ReturnRoute, routes)
	request.RunID = result.RunID
	request.ResultStatus = result.Status
	request.ResultError = result.Error
	request.MCP = result.MCP
	return request, deliver, err
}

type WorkflowResultDeliverer struct {
	routes  ReturnRouteResolver
	gateway *Gateway
}

func NewWorkflowResultDeliverer(routes ReturnRouteResolver, gateway *Gateway) *WorkflowResultDeliverer {
	return &WorkflowResultDeliverer{routes: routes, gateway: gateway}
}

func (d *WorkflowResultDeliverer) DeliverWorkflowResult(ctx context.Context, result app.WorkflowResult) (app.DeliveryReceipt, error) {
	if d == nil || d.gateway == nil {
		return app.DeliveryReceipt{}, errors.New("workflow result delivery is unavailable")
	}
	request, deliver, err := RequestFromWorkflowResult(ctx, result, d.routes)
	if err != nil || !deliver {
		return app.DeliveryReceipt{}, err
	}
	return d.gateway.Deliver(ctx, request)
}

// RequestForMessage is shared by explicit message.send and ordinary Workflow
// results so both enter the same Delivery Gateway contract.
func RequestForMessage(ctx context.Context, sourceID, ownerID string, authorization app.MessageAuthorization, content app.MessageContent, route app.ReturnRoute, routes ReturnRouteResolver) (app.DeliveryRequest, bool, error) {
	if routes == nil {
		return app.DeliveryRequest{}, false, errors.New("return route resolver is unavailable")
	}
	endpoint, deliver, err := routes.Resolve(ctx, route)
	if err != nil || !deliver {
		return app.DeliveryRequest{}, deliver, err
	}
	now := time.Now().UTC()
	origin := app.DeliveryOriginAgentWorkflow
	if route.Mode == app.ReturnToSource && endpoint.Kind == app.EndpointKindThirdPartyDevice {
		origin = app.DeliveryOriginSourceReply
	}
	return app.DeliveryRequest{
		SchemaVersion:  app.DeliveryRequestSchemaVersion,
		ID:             app.DeliveryID(app.NewID("del")),
		IdempotencyKey: sourceID + ":" + string(endpoint.ID),
		ResultID:       sourceID,
		OwnerID:        ownerID,
		ActorID:        authorization.PrincipalID,
		Authorization:  authorization,
		Target:         endpoint.ID,
		Content:        content,
		Origin:         origin,
		SourceAdmitted: route.Mode == app.ReturnToSource && route.SourceAdmitted,
		CreatedAt:      now,
	}, true, nil
}
