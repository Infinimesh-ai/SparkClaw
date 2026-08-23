package messagecontrol

import (
	"context"
	"errors"
	"fmt"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type EndpointResolver interface {
	Get(context.Context, app.EndpointID) (app.MessageEndpoint, error)
	// GetAdmittedSource resolves a source endpoint for work admitted while
	// its connector was enabled, without re-applying a later owner opt-out.
	// Part of the interface so an implementation cannot silently fall back
	// to the wrong semantics.
	GetAdmittedSource(context.Context, app.EndpointID) (app.MessageEndpoint, error)
}

type ReturnRouteResolver struct {
	endpoints EndpointResolver
}

func NewReturnRouteResolver(endpoints EndpointResolver) *ReturnRouteResolver {
	return &ReturnRouteResolver{endpoints: endpoints}
}

func (r *ReturnRouteResolver) Resolve(ctx context.Context, route app.ReturnRoute) (app.MessageEndpoint, bool, error) {
	if r == nil || r.endpoints == nil {
		return app.MessageEndpoint{}, false, errors.New("return route resolver is unavailable")
	}
	var id app.EndpointID
	switch route.Mode {
	case app.ReturnToSource:
		id = route.SourceEndpointID
	case app.ReturnToEndpoint:
		id = route.EndpointID
	case app.ReturnNowhere:
		return app.MessageEndpoint{}, false, nil
	default:
		return app.MessageEndpoint{}, false, fmt.Errorf("unsupported return mode %q", route.Mode)
	}
	if id == "" {
		return app.MessageEndpoint{}, false, errors.New("return route endpoint is required")
	}
	var endpoint app.MessageEndpoint
	var err error
	if route.Mode == app.ReturnToSource && route.SourceAdmitted {
		endpoint, err = r.endpoints.GetAdmittedSource(ctx, id)
	} else {
		endpoint, err = r.endpoints.Get(ctx, id)
	}
	return endpoint, err == nil, err
}
