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
	return RequestForMessage(ctx, result.ID, result.Content, result.ReturnRoute, routes)
}

// RequestForMessage is shared by explicit message.send and ordinary Workflow
// results so both enter the same Delivery Gateway contract.
func RequestForMessage(ctx context.Context, sourceID string, content app.MessageContent, route app.ReturnRoute, routes ReturnRouteResolver) (app.DeliveryRequest, bool, error) {
	if routes == nil {
		return app.DeliveryRequest{}, false, errors.New("return route resolver is unavailable")
	}
	endpoint, deliver, err := routes.Resolve(ctx, route)
	if err != nil || !deliver {
		return app.DeliveryRequest{}, deliver, err
	}
	now := time.Now().UTC()
	return app.DeliveryRequest{
		SchemaVersion:  app.DeliveryRequestSchemaVersion,
		ID:             app.DeliveryID(app.NewID("del")),
		IdempotencyKey: sourceID + ":" + string(endpoint.ID),
		ResultID:       sourceID,
		Target:         endpoint.ID,
		Content:        content,
		CreatedAt:      now,
	}, true, nil
}
